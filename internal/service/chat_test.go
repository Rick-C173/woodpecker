package service_test

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"woodpecker/internal/llm"
	"woodpecker/internal/model"
	"woodpecker/internal/repo"
	"woodpecker/internal/service"
)

// ---- M6 会话问答测试替身 ----

type fakeConvStore struct {
	mu           sync.Mutex
	convs        map[int64]*model.Conversation
	msgs         map[int64]*model.Message
	cites        map[int64][]model.MessageCitation // message_id → citations
	feedbacks    map[int64]*model.Feedback         // (message_id,user_id) 打包 key
	nextConv     int64
	nextMsg      int64
	ErrOnSaveMsg error
}

func newFakeConvStore() *fakeConvStore {
	return &fakeConvStore{
		convs:     map[int64]*model.Conversation{},
		msgs:      map[int64]*model.Message{},
		cites:     map[int64][]model.MessageCitation{},
		feedbacks: map[int64]*model.Feedback{},
	}
}

func fbKey(messageID, userID int64) int64 { return messageID*1e9 + userID }

func (f *fakeConvStore) CreateConversation(_ context.Context, c *model.Conversation) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	c.ID = f.nextConv + 1
	f.nextConv++
	cp := *c
	f.convs[c.ID] = &cp
	return nil
}

func (f *fakeConvStore) SaveConversation(_ context.Context, c *model.Conversation) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.convs[c.ID]; !ok {
		return repo.ErrNotFound
	}
	cp := *c
	f.convs[c.ID] = &cp
	return nil
}

func (f *fakeConvStore) GetConversationByID(_ context.Context, id int64) (*model.Conversation, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	c, ok := f.convs[id]
	if !ok {
		return nil, repo.ErrNotFound
	}
	cp := *c
	return &cp, nil
}

func (f *fakeConvStore) ListMine(_ context.Context, kbID, userID int64) ([]repo.ConversationRow, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	rows := make([]repo.ConversationRow, 0)
	for _, c := range f.convs {
		if c.KBID != kbID || c.UserID != userID {
			continue
		}
		rows = append(rows, repo.ConversationRow{
			ID: c.ID, KBID: c.KBID, UserID: c.UserID, Title: c.Title,
			MessageCount: 0, UpdatedAt: c.UpdatedAt,
		})
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].UpdatedAt.After(rows[j].UpdatedAt) })
	return rows, nil
}

func (f *fakeConvStore) DeleteConversation(_ context.Context, id int64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.convs[id]; !ok {
		return repo.ErrNotFound
	}
	delete(f.convs, id)
	for id, m := range f.msgs {
		if m.ConversationID == id {
			delete(f.msgs, id)
			delete(f.cites, id)
		}
	}
	return nil
}

// CreateMessage 尊重 ctx：已取消/已超时立即返回错误，与真库 database/sql 行为一致
// （否则「断连后落库」的缺陷在单测里永远不可见，见审计 §3.4）。
func (f *fakeConvStore) CreateMessage(ctx context.Context, m *model.Message) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	m.ID = f.nextMsg + 1
	f.nextMsg++
	cp := *m
	f.msgs[m.ID] = &cp
	return nil
}

func (f *fakeConvStore) SaveMessage(ctx context.Context, m *model.Message) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.ErrOnSaveMsg != nil {
		return f.ErrOnSaveMsg
	}
	if _, ok := f.msgs[m.ID]; !ok {
		return repo.ErrNotFound
	}
	cp := *m
	f.msgs[m.ID] = &cp
	return nil
}

func (f *fakeConvStore) GetMessageByID(_ context.Context, id int64) (*model.Message, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	m, ok := f.msgs[id]
	if !ok {
		return nil, repo.ErrNotFound
	}
	cp := *m
	return &cp, nil
}

func (f *fakeConvStore) ListMessages(_ context.Context, conversationID int64) ([]repo.MessageRow, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	rows := make([]repo.MessageRow, 0)
	for _, m := range f.msgs {
		if m.ConversationID != conversationID {
			continue
		}
		row := repo.MessageRow{
			ID: m.ID, ConversationID: m.ConversationID, Role: m.Role, Content: m.Content, Interrupted: m.Interrupted,
			PromptTokens: m.PromptTokens, CompletionTokens: m.CompletionTokens, ModelEntryID: m.ModelEntryID,
			CreatedAt: m.CreatedAt,
			Citations: []repo.CitationRow{},
		}
		for _, ct := range f.cites[m.ID] {
			row.Citations = append(row.Citations, repo.CitationRow{
				ID: ct.ID, MessageID: ct.MessageID, DocumentID: ct.DocumentID, Seq: ct.Seq,
				ChunkID: ct.ChunkID, FilePath: ct.FilePath, LineStart: ct.LineStart, LineEnd: ct.LineEnd,
				HeadingPath: ct.HeadingPath, SourceURL: ct.SourceURL, Snippet: ct.Snippet,
			})
		}
		rows = append(rows, row)
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].ID < rows[j].ID })
	return rows, nil
}

func (f *fakeConvStore) LastMessages(_ context.Context, conversationID int64, n int) ([]model.Message, error) {
	all, err := f.ListMessages(context.Background(), conversationID)
	if err != nil {
		return nil, err
	}
	if len(all) > n {
		all = all[len(all)-n:]
	}
	out := make([]model.Message, 0, len(all))
	for _, r := range all {
		out = append(out, model.Message{
			ID: r.ID, ConversationID: conversationID, Role: r.Role, Content: r.Content,
			Interrupted: r.Interrupted, CreatedAt: r.CreatedAt,
		})
	}
	return out, nil
}

func (f *fakeConvStore) ReplaceCitations(ctx context.Context, messageID int64, cs []model.MessageCitation) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.cites[messageID] = cs
	return nil
}

func (f *fakeConvStore) UpsertFeedback(_ context.Context, fb *model.Feedback) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.feedbacks[fbKey(fb.MessageID, fb.UserID)] = fb
	return nil
}

func (f *fakeConvStore) DeleteFeedback(_ context.Context, messageID, userID int64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	key := fbKey(messageID, userID)
	if _, ok := f.feedbacks[key]; !ok {
		return repo.ErrNotFound
	}
	delete(f.feedbacks, key)
	return nil
}

func (f *fakeConvStore) GetFeedbackByUser(_ context.Context, messageID, userID int64) (*repo.FeedbackRow, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	fb, ok := f.feedbacks[fbKey(messageID, userID)]
	if !ok {
		return nil, nil
	}
	return &repo.FeedbackRow{Reason: fb.Reason, Comment: fb.Comment}, nil
}

// conv 添加一条会话（测试种子）。
func (f *fakeConvStore) conv(userID int64) *model.Conversation {
	c := &model.Conversation{KBID: 1, UserID: userID, CreatedAt: time.Now(), UpdatedAt: time.Now()}
	_ = f.CreateConversation(context.Background(), c)
	return c
}

func (f *fakeConvStore) message(convID int64, role int16, content string) *model.Message {
	m := &model.Message{ConversationID: convID, Role: role, Content: content, CreatedAt: time.Now()}
	_ = f.CreateMessage(context.Background(), m)
	return m
}

type fakeChatMembers struct{ role int16 }

func (f *fakeChatMembers) GetMemberRole(context.Context, int64, int64) (int16, error) {
	if f.role == 0 {
		return 0, repo.ErrNotFound
	}
	return f.role, nil
}

type fakeChatStream struct {
	deltas   []string   // 单轮序列（所有调用相同）
	attempts [][]string // 多轮序列（每次 ChatStream 取下一个；耗尽后复用最后一轮）
	delay    time.Duration
	err      error
	mu       sync.Mutex
	gotReq   llm.ChatRequest
	calls    int
}

func (f *fakeChatStream) ChatStream(ctx context.Context, _ llm.ResolvedConfig, req llm.ChatRequest) (<-chan llm.ChatDelta, error) {
	f.mu.Lock()
	f.gotReq = req
	err := f.err
	idx := f.calls
	f.calls++
	f.mu.Unlock()
	if err != nil {
		return nil, err
	}
	var deltas []string
	if len(f.attempts) > 0 {
		if idx >= len(f.attempts) {
			idx = len(f.attempts) - 1
		}
		deltas = f.attempts[idx]
	} else {
		deltas = f.deltas
	}
	ch := make(chan llm.ChatDelta, len(deltas))
	go func() {
		defer close(ch)
		for _, d := range deltas {
			if f.delay > 0 {
				time.Sleep(f.delay)
			}
			select {
			case ch <- llm.ChatDelta{Content: d}:
			case <-ctx.Done():
				return
			}
		}
	}()
	return ch, nil
}

func (f *fakeChatStream) request() llm.ChatRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.gotReq
}

type fakeChatResolver struct {
	err error
}

func (f *fakeChatResolver) GetDefault(_ context.Context, purpose int16) (llm.ResolvedConfig, error) {
	if f.err != nil {
		return llm.ResolvedConfig{}, f.err
	}
	if purpose != model.ModelPurposeChat {
		return llm.ResolvedConfig{}, fmt.Errorf("unexpected purpose %d", purpose)
	}
	return llm.ResolvedConfig{EntryID: 7, ModelName: "qwen2.5"}, nil
}

type fakeChatSearch struct {
	hits []service.SearchItem
}

func (f *fakeChatSearch) SearchTopN(_ context.Context, _ int64, _ string, _ int) ([]service.SearchItem, error) {
	if f.hits == nil {
		return nil, nil
	}
	return f.hits, nil
}

func intptr6(v int) *int       { return &v }
func strPtr6(v string) *string { return &v }

func newChatSvc(store *fakeConvStore, members service.ChatMemberChecker, stream *fakeChatStream, resolver service.ChatConfigResolver, search service.ChatSearcher) *service.ChatService {
	return service.NewChatService(store, members, resolver, stream, search)
}

func drainEvents(t *testing.T, ch <-chan service.ChatEvent) []service.ChatEvent {
	t.Helper()
	var out []service.ChatEvent
	for e := range ch {
		out = append(out, e)
	}
	return out
}

// ---- 会话访问规则（R6 非成员 404 → 他人 404 → 管理员放行） ----

func TestChatAccessRules(t *testing.T) {
	store := newFakeConvStore()
	svc := newChatSvc(store, &fakeChatMembers{role: 1}, &fakeChatStream{}, &fakeChatResolver{}, &fakeChatSearch{})

	owner := store.conv(100)
	other := store.conv(200)

	// 非成员 → 404（不泄露）
	_, err := svc.Get(context.Background(), owner.ID, 300, false)
	requireSvcCode(t, err, service.CodeNotFound)

	// 成员但非本人会话 → 404
	_, err = svc.Get(context.Background(), other.ID, 100, false)
	requireSvcCode(t, err, service.CodeNotFound)

	// 本人 → OK
	d, err := svc.Get(context.Background(), owner.ID, 100, false)
	require.NoError(t, err)
	assert.Equal(t, owner.ID, d.ID)

	// 管理员访问他人 → OK；但改删他人会话 → 404
	_, err = svc.Get(context.Background(), other.ID, 1, true)
	require.NoError(t, err)
	err = svc.Rename(context.Background(), other.ID, 1, true, "新名")
	requireSvcCode(t, err, service.CodeNotFound)
	err = svc.Delete(context.Background(), other.ID, 1, true)
	requireSvcCode(t, err, service.CodeNotFound)

	// 会话不存在 → 404
	_, err = svc.Get(context.Background(), 9999, 100, false)
	requireSvcCode(t, err, service.CodeNotFound)
}

// ---- Ask：事件序列与落库 ----

func TestAskEventSequenceWithCitations(t *testing.T) {
	store := newFakeConvStore()
	c := store.conv(100)
	search := &fakeChatSearch{hits: []service.SearchItem{
		{ChunkID: 11, DocumentID: 5, FilePath: strPtr6("internal/handler/auth.go"), LineStart: intptr6(45), LineEnd: intptr6(62), Content: "func Login"},
		{ChunkID: 12, DocumentID: 5, HeadingPath: strPtr6("指南/限流"), Content: "限流实现"},
	}}
	stream := &fakeChatStream{deltas: []string{"该接口在 ", "internal/handler 中实现"}}
	svc := newChatSvc(store, &fakeChatMembers{role: 1}, stream, &fakeChatResolver{}, search)

	events, err := svc.Ask(context.Background(), c.ID, 100, false, "登录怎么实现的")
	require.NoError(t, err)
	got := drainEvents(t, events)

	// 序列：delta* → citations → done
	require.Len(t, got, 4)
	assert.Equal(t, "delta", got[0].Type)
	assert.Equal(t, "delta", got[1].Type)
	assert.Equal(t, "citations", got[2].Type)
	assert.Equal(t, "done", got[3].Type)
	assert.Equal(t, "该接口在 ", got[0].Content)
	require.Len(t, got[2].Items, 2)
	assert.Equal(t, 1, got[2].Items[0].Seq)
	assert.Equal(t, "internal/handler/auth.go", *got[2].Items[0].FilePath)
	assert.Positive(t, got[3].MessageID)
	assert.Positive(t, got[3].PromptTokens)
	assert.Positive(t, got[3].CompletionTokens)

	// 落库：user + assistant 消息、引用 2 条、溯源 entry_id
	msgs, _ := store.ListMessages(context.Background(), c.ID)
	require.Len(t, msgs, 2)
	assert.Equal(t, model.MsgRoleUser, msgs[0].Role)
	ans := msgs[1]
	assert.Equal(t, model.MsgRoleAssistant, ans.Role)
	assert.Equal(t, "该接口在 internal/handler 中实现", ans.Content)
	assert.False(t, ans.Interrupted)
	require.NotNil(t, ans.ModelEntryID)
	assert.Equal(t, int64(7), *ans.ModelEntryID)
	cites := store.cites[ans.ID]
	require.Len(t, cites, 2)
	assert.Equal(t, int64(11), cites[0].ChunkID)

	// 标题缺省取首条提问摘要（FR-09）
	cNow, _ := store.GetConversationByID(context.Background(), c.ID)
	require.NotNil(t, cNow.Title)
	assert.Equal(t, "登录怎么实现的", *cNow.Title)

	// 流式请求包含检索资料与提问
	req := stream.request()
	assert.Contains(t, req.System, "【资料】")
	assert.Contains(t, req.System, "func Login")
	assert.Contains(t, req.Prompt, "【提问】")
}

func TestAskNoHitsNoCitations(t *testing.T) {
	store := newFakeConvStore()
	c := store.conv(100)
	stream := &fakeChatStream{deltas: []string{"知识库中未找到足够依据"}}
	svc := newChatSvc(store, &fakeChatMembers{role: 1}, stream, &fakeChatResolver{}, &fakeChatSearch{})

	events, err := svc.Ask(context.Background(), c.ID, 100, false, "冷门问题")
	require.NoError(t, err)
	got := drainEvents(t, events)

	// 依据不足：正常输出说明文字，不发 citations（FR-07）
	require.Len(t, got, 2)
	assert.Equal(t, "delta", got[0].Type)
	assert.Equal(t, "done", got[1].Type)
	msgs, _ := store.ListMessages(context.Background(), c.ID)
	assert.Empty(t, store.cites[msgs[1].ID])
}

func TestAskValidation(t *testing.T) {
	store := newFakeConvStore()
	c := store.conv(100)
	svc := newChatSvc(store, &fakeChatMembers{role: 1}, &fakeChatStream{}, &fakeChatResolver{}, &fakeChatSearch{})

	// 空内容 → 422
	_, err := svc.Ask(context.Background(), c.ID, 100, false, "  ")
	requireSvcCode(t, err, service.CodeValidation)

	// 超长 → 422
	_, err = svc.Ask(context.Background(), c.ID, 100, false, strings.Repeat("长", 4001))
	requireSvcCode(t, err, service.CodeValidation)

	// 他人会话 → 404
	_, err = svc.Ask(context.Background(), c.ID, 200, false, "问题")
	requireSvcCode(t, err, service.CodeNotFound)
}

func TestAskModelConfigMissing(t *testing.T) {
	store := newFakeConvStore()
	c := store.conv(100)
	svc := newChatSvc(store, &fakeChatMembers{role: 1}, &fakeChatStream{},
		&fakeChatResolver{err: service.NewServiceError("MODEL_001", "缺默认")}, &fakeChatSearch{})

	events, err := svc.Ask(context.Background(), c.ID, 100, false, "问题")
	require.NoError(t, err)
	got := drainEvents(t, events)
	require.Len(t, got, 1)
	assert.Equal(t, "error", got[0].Type)
	assert.Equal(t, "MODEL_001", got[0].Code)

	// user 消息已落库，无 assistant 消息
	msgs, _ := store.ListMessages(context.Background(), c.ID)
	require.Len(t, msgs, 1)
	assert.Equal(t, model.MsgRoleUser, msgs[0].Role)
}

func TestAskStreamError(t *testing.T) {
	store := newFakeConvStore()
	c := store.conv(100)
	svc := newChatSvc(store, &fakeChatMembers{role: 1}, &fakeChatStream{err: assert.AnError},
		&fakeChatResolver{}, &fakeChatSearch{})

	events, err := svc.Ask(context.Background(), c.ID, 100, false, "问题")
	require.NoError(t, err)
	got := drainEvents(t, events)
	require.Len(t, got, 1)
	assert.Equal(t, "error", got[0].Type)
	assert.Equal(t, "LLM_001", got[0].Code)
}

// 中断生成：ctx 取消后已生成部分落库并标记 interrupted。
func TestAskInterruptedSavesPartial(t *testing.T) {
	store := newFakeConvStore()
	c := store.conv(100)
	stream := &fakeChatStream{deltas: []string{"第一段", "第二段", "第三段"}, delay: 30 * time.Millisecond}
	svc := newChatSvc(store, &fakeChatMembers{role: 1}, stream, &fakeChatResolver{}, &fakeChatSearch{})

	ctx, cancel := context.WithCancel(context.Background())
	events, err := svc.Ask(ctx, c.ID, 100, false, "问题")
	require.NoError(t, err)

	// 收到首个 delta 后取消
	<-events
	cancel()
	drainEvents(t, events)

	// 中断路径：已生成部分落库并标记 interrupted（done/citations 是否发出与断开竞速，不作断言）
	msgs, _ := store.ListMessages(context.Background(), c.ID)
	require.Len(t, msgs, 2)
	ans := msgs[1]
	assert.Equal(t, model.MsgRoleAssistant, ans.Role)
	assert.True(t, ans.Interrupted, "中断标记 interrupted")
	assert.NotEmpty(t, ans.Content)
}

// ---- Regenerate：替换回答保留提问 ----

func TestRegenerateReplacesAnswer(t *testing.T) {
	store := newFakeConvStore()
	c := store.conv(100)
	q := store.message(c.ID, model.MsgRoleUser, "什么是限流")
	old := store.message(c.ID, model.MsgRoleAssistant, "旧回答")
	store.ReplaceCitations(context.Background(), old.ID, []model.MessageCitation{
		{MessageID: old.ID, DocumentID: 99, Seq: 1, ChunkID: 1},
	})

	stream := &fakeChatStream{deltas: []string{"新回答"}}
	search := &fakeChatSearch{hits: []service.SearchItem{{ChunkID: 20, DocumentID: 3, Content: "限流资料"}}}
	svc := newChatSvc(store, &fakeChatMembers{role: 1}, stream, &fakeChatResolver{}, search)

	events, err := svc.Regenerate(context.Background(), old.ID, 100, false)
	require.NoError(t, err)
	got := drainEvents(t, events)

	require.Len(t, got, 3) // delta + citations + done
	assert.Equal(t, "done", got[2].Type)
	assert.Equal(t, old.ID, got[2].MessageID, "done 回传被替换消息 ID")

	// 提问保留，回答被替换
	msgs, _ := store.ListMessages(context.Background(), c.ID)
	require.Len(t, msgs, 2)
	assert.Equal(t, q.ID, msgs[0].ID)
	assert.Equal(t, "什么是限流", msgs[0].Content)
	assert.Equal(t, old.ID, msgs[1].ID)
	assert.Equal(t, "新回答", msgs[1].Content)
	assert.False(t, msgs[1].Interrupted)

	// 引用被替换
	cites := store.cites[old.ID]
	require.Len(t, cites, 1)
	assert.Equal(t, int64(20), cites[0].ChunkID)

	// 重新生成的 prompt 不含旧回答
	req := stream.request()
	assert.NotContains(t, req.Prompt, "旧回答")
	assert.Contains(t, req.Prompt, "什么是限流")
}

func TestRegenerateValidation(t *testing.T) {
	store := newFakeConvStore()
	c := store.conv(100)
	u := store.message(c.ID, model.MsgRoleUser, "提问")
	a := store.message(c.ID, model.MsgRoleAssistant, "回答")
	svc := newChatSvc(store, &fakeChatMembers{role: 1}, &fakeChatStream{}, &fakeChatResolver{}, &fakeChatSearch{})

	// user 消息不可重新生成
	_, err := svc.Regenerate(context.Background(), u.ID, 100, false)
	requireSvcCode(t, err, service.CodeValidation)

	// 消息不存在 → 404
	_, err = svc.Regenerate(context.Background(), 9999, 100, false)
	requireSvcCode(t, err, service.CodeNotFound)

	// 他人会话 → 404
	_, err = svc.Regenerate(context.Background(), a.ID, 200, false)
	requireSvcCode(t, err, service.CodeNotFound)
}

// ---- 会话 CRUD ----

func TestConversationCRUD(t *testing.T) {
	store := newFakeConvStore()
	c := store.conv(100)
	svc := newChatSvc(store, &fakeChatMembers{role: 1}, &fakeChatStream{}, &fakeChatResolver{}, &fakeChatSearch{})
	ctx := context.Background()

	// 新建
	item, err := svc.CreateConversation(ctx, 1, 100)
	require.NoError(t, err)
	assert.Positive(t, item.ID)

	// 列表（仅我的）
	store.conv(200) // 他人的
	items, err := svc.ListMine(ctx, 1, 100)
	require.NoError(t, err)
	assert.Len(t, items, 2)

	// 重命名
	require.NoError(t, svc.Rename(ctx, c.ID, 100, false, "我的会话"))
	cNow, _ := store.GetConversationByID(ctx, c.ID)
	assert.Equal(t, "我的会话", *cNow.Title)

	// 重命名校验：空 / 超长
	requireSvcCode(t, svc.Rename(ctx, c.ID, 100, false, "  "), service.CodeValidation)
	requireSvcCode(t, svc.Rename(ctx, c.ID, 100, false, strings.Repeat("题", 201)), service.CodeValidation)

	// 删除（级联：消息与反馈随会话清理）
	fb := &model.Feedback{MessageID: 1, UserID: 100, Reason: 1, CreatedAt: time.Now()}
	_ = store.UpsertFeedback(ctx, fb)
	require.NoError(t, svc.Delete(ctx, c.ID, 100, false))
	_, err = store.GetConversationByID(ctx, c.ID)
	require.ErrorIs(t, err, repo.ErrNotFound)
}

// ---- 点踩反馈（FR-14） ----

func TestFeedbackRules(t *testing.T) {
	store := newFakeConvStore()
	c := store.conv(100)
	u := store.message(c.ID, model.MsgRoleUser, "提问")
	a := store.message(c.ID, model.MsgRoleAssistant, "回答")
	svc := newChatSvc(store, &fakeChatMembers{role: 1}, &fakeChatStream{}, &fakeChatResolver{}, &fakeChatSearch{})
	ctx := context.Background()

	// 仅 assistant 消息可反馈
	requireSvcCode(t, svc.Feedback(ctx, u.ID, 100, false, 1, ""), service.CodeValidation)

	// reason 非法
	requireSvcCode(t, svc.Feedback(ctx, a.ID, 100, false, 0, ""), service.CodeValidation)
	requireSvcCode(t, svc.Feedback(ctx, a.ID, 100, false, 5, ""), service.CodeValidation)

	// reason=4（其他）comment 必填
	requireSvcCode(t, svc.Feedback(ctx, a.ID, 100, false, 4, " "), service.CodeValidation)

	// 他人会话 → 404
	requireSvcCode(t, svc.Feedback(ctx, a.ID, 200, false, 1, ""), service.CodeNotFound)

	// 合法：重复覆盖（UNIQUE(message_id,user_id) 语义）
	require.NoError(t, svc.Feedback(ctx, a.ID, 100, false, 1, ""))
	fb, _ := store.GetFeedbackByUser(ctx, a.ID, 100)
	require.NotNil(t, fb)
	assert.Equal(t, int16(1), fb.Reason)
	require.NoError(t, svc.Feedback(ctx, a.ID, 100, false, 4, "换个说法"))
	fb, _ = store.GetFeedbackByUser(ctx, a.ID, 100)
	require.NotNil(t, fb)
	assert.Equal(t, int16(4), fb.Reason)
	require.NotNil(t, fb.Comment)
	assert.Equal(t, "换个说法", *fb.Comment)

	// 历史展示含反馈
	detail, err := svc.Get(ctx, c.ID, 100, false)
	require.NoError(t, err)
	for _, m := range detail.Messages {
		if m.ID == a.ID {
			require.NotNil(t, m.Feedback)
			assert.Equal(t, 4, m.Feedback.Reason)
		}
	}

	// 撤销点踩：无记录 → 404
	requireSvcCode(t, svc.DeleteFeedback(ctx, u.ID, 100), service.CodeNotFound)
	require.NoError(t, svc.DeleteFeedback(ctx, a.ID, 100))
	fb, _ = store.GetFeedbackByUser(ctx, a.ID, 100)
	assert.Nil(t, fb)
}

// ---- 多轮上下文 ----

func TestAskCarriesHistory(t *testing.T) {
	store := newFakeConvStore()
	c := store.conv(100)
	store.message(c.ID, model.MsgRoleUser, "第一问")
	store.message(c.ID, model.MsgRoleAssistant, "第一答")
	stream := &fakeChatStream{deltas: []string{"回答"}}
	svc := newChatSvc(store, &fakeChatMembers{role: 1}, stream, &fakeChatResolver{}, &fakeChatSearch{})

	events, err := svc.Ask(context.Background(), c.ID, 100, false, "第二问")
	require.NoError(t, err)
	drainEvents(t, events)

	req := stream.request()
	assert.Contains(t, req.Prompt, "第一问")
	assert.Contains(t, req.Prompt, "第一答")
	assert.Contains(t, req.Prompt, "第二问")
}

// ---- 输出校验与重试（FR-07：引用编号越界 → 重新生成一次） ----

func TestAskRetriesOnOutOfBoundsCitation(t *testing.T) {
	store := newFakeConvStore()
	c := store.conv(100)
	search := &fakeChatSearch{hits: []service.SearchItem{
		{ChunkID: 11, DocumentID: 5, FilePath: strPtr6("a.go"), Content: "资料一"},
	}}
	// 第一轮引用 [9] 越界 → 重试；第二轮正常
	stream := &fakeChatStream{attempts: [][]string{
		{"这是错误的", "回答 [9]"},
		{"正确回答 ", "[1]"},
	}}
	svc := newChatSvc(store, &fakeChatMembers{role: 1}, stream, &fakeChatResolver{}, search)

	events, err := svc.Ask(context.Background(), c.ID, 100, false, "问题")
	require.NoError(t, err)
	got := drainEvents(t, events)

	// 两次生成的 delta 都流出；最终 citations + done
	types := make([]string, 0, len(got))
	for _, e := range got {
		types = append(types, e.Type)
	}
	assert.Equal(t, []string{"delta", "delta", "delta", "delta", "citations", "done"}, types)

	// 落库为第二轮（正常）回答
	msgs, _ := store.ListMessages(context.Background(), c.ID)
	require.Len(t, msgs, 2)
	assert.Equal(t, "正确回答 [1]", msgs[1].Content)
	assert.Equal(t, 2, stream.calls, "调用了两次流式生成")
}

func TestAskNoRetryWhenCitationValid(t *testing.T) {
	store := newFakeConvStore()
	c := store.conv(100)
	search := &fakeChatSearch{hits: []service.SearchItem{
		{ChunkID: 11, DocumentID: 5, FilePath: strPtr6("a.go"), Content: "资料一"},
	}}
	stream := &fakeChatStream{deltas: []string{"依据 [1] 的回答"}}
	svc := newChatSvc(store, &fakeChatMembers{role: 1}, stream, &fakeChatResolver{}, search)

	events, err := svc.Ask(context.Background(), c.ID, 100, false, "问题")
	require.NoError(t, err)
	got := drainEvents(t, events)
	require.Len(t, got, 3) // delta + citations + done
	assert.Equal(t, 1, stream.calls, "编号合法不重试")
}

// ---- 辅助 ----

func requireSvcCode(t *testing.T, err error, code string) {
	t.Helper()
	require.Error(t, err)
	var se *service.ServiceError
	require.ErrorAs(t, err, &se)
	assert.Equal(t, code, se.Code)
}
