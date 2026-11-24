package handler_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"woodpecker/internal/llm"
	"woodpecker/internal/model"
	"woodpecker/internal/repo"
	"woodpecker/internal/service"
)

// ---- M6 会话问答 handler 测试替身 ----

type fakeChatStore struct {
	mu        sync.Mutex
	convs     map[int64]*model.Conversation
	msgs      map[int64]*model.Message
	cites     map[int64][]model.MessageCitation
	feedbacks map[string]*model.Feedback
	nextConv  int64
	nextMsg   int64
}

func newFakeChatStore() *fakeChatStore {
	return &fakeChatStore{
		convs:     map[int64]*model.Conversation{},
		msgs:      map[int64]*model.Message{},
		cites:     map[int64][]model.MessageCitation{},
		feedbacks: map[string]*model.Feedback{},
	}
}

func (f *fakeChatStore) CreateConversation(_ context.Context, c *model.Conversation) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	c.ID = f.nextConv + 1
	f.nextConv++
	cp := *c
	f.convs[c.ID] = &cp
	return nil
}

func (f *fakeChatStore) SaveConversation(_ context.Context, c *model.Conversation) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.convs[c.ID]; !ok {
		return repo.ErrNotFound
	}
	cp := *c
	f.convs[c.ID] = &cp
	return nil
}

func (f *fakeChatStore) GetConversationByID(_ context.Context, id int64) (*model.Conversation, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	c, ok := f.convs[id]
	if !ok {
		return nil, repo.ErrNotFound
	}
	cp := *c
	return &cp, nil
}

func (f *fakeChatStore) ListMine(_ context.Context, kbID, userID int64) ([]repo.ConversationRow, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	rows := make([]repo.ConversationRow, 0)
	for _, c := range f.convs {
		if c.KBID != kbID || c.UserID != userID {
			continue
		}
		rows = append(rows, repo.ConversationRow{ID: c.ID, KBID: c.KBID, UserID: c.UserID, Title: c.Title, UpdatedAt: c.UpdatedAt})
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].UpdatedAt.After(rows[j].UpdatedAt) })
	return rows, nil
}

func (f *fakeChatStore) DeleteConversation(_ context.Context, id int64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.convs[id]; !ok {
		return repo.ErrNotFound
	}
	delete(f.convs, id)
	for mid, m := range f.msgs {
		if m.ConversationID == id {
			delete(f.msgs, mid)
			delete(f.cites, mid)
		}
	}
	return nil
}

func (f *fakeChatStore) CreateMessage(_ context.Context, m *model.Message) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	m.ID = f.nextMsg + 1
	f.nextMsg++
	cp := *m
	f.msgs[m.ID] = &cp
	return nil
}

func (f *fakeChatStore) SaveMessage(_ context.Context, m *model.Message) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.msgs[m.ID]; !ok {
		return repo.ErrNotFound
	}
	cp := *m
	f.msgs[m.ID] = &cp
	return nil
}

func (f *fakeChatStore) GetMessageByID(_ context.Context, id int64) (*model.Message, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	m, ok := f.msgs[id]
	if !ok {
		return nil, repo.ErrNotFound
	}
	cp := *m
	return &cp, nil
}

func (f *fakeChatStore) ListMessages(_ context.Context, conversationID int64) ([]repo.MessageRow, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	rows := make([]repo.MessageRow, 0)
	for _, m := range f.msgs {
		if m.ConversationID != conversationID {
			continue
		}
		row := repo.MessageRow{
			ID: m.ID, ConversationID: m.ConversationID, Role: m.Role, Content: m.Content,
			Interrupted: m.Interrupted, PromptTokens: m.PromptTokens, CompletionTokens: m.CompletionTokens,
			ModelEntryID: m.ModelEntryID, CreatedAt: m.CreatedAt, Citations: []repo.CitationRow{},
		}
		for _, ct := range f.cites[m.ID] {
			row.Citations = append(row.Citations, repo.CitationRow{
				ID: ct.ID, MessageID: ct.MessageID, DocumentID: ct.DocumentID, Seq: ct.Seq, ChunkID: ct.ChunkID,
				FilePath: ct.FilePath, LineStart: ct.LineStart, LineEnd: ct.LineEnd,
				HeadingPath: ct.HeadingPath, SourceURL: ct.SourceURL, Snippet: ct.Snippet,
			})
		}
		rows = append(rows, row)
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].ID < rows[j].ID })
	return rows, nil
}

func (f *fakeChatStore) LastMessages(ctx context.Context, conversationID int64, n int) ([]model.Message, error) {
	all, err := f.ListMessages(ctx, conversationID)
	if err != nil {
		return nil, err
	}
	if len(all) > n {
		all = all[len(all)-n:]
	}
	out := make([]model.Message, 0, len(all))
	for _, r := range all {
		out = append(out, model.Message{ID: r.ID, ConversationID: conversationID, Role: r.Role, Content: r.Content})
	}
	return out, nil
}

func (f *fakeChatStore) ReplaceCitations(_ context.Context, messageID int64, cs []model.MessageCitation) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.cites[messageID] = cs
	return nil
}

func (f *fakeChatStore) UpsertFeedback(_ context.Context, fb *model.Feedback) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.feedbacks[fmt.Sprintf("%d:%d", fb.MessageID, fb.UserID)] = fb
	return nil
}

func (f *fakeChatStore) DeleteFeedback(_ context.Context, messageID, userID int64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	key := fmt.Sprintf("%d:%d", messageID, userID)
	if _, ok := f.feedbacks[key]; !ok {
		return repo.ErrNotFound
	}
	delete(f.feedbacks, key)
	return nil
}

func (f *fakeChatStore) GetFeedbackByUser(_ context.Context, messageID, userID int64) (*repo.FeedbackRow, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	fb, ok := f.feedbacks[fmt.Sprintf("%d:%d", messageID, userID)]
	if !ok {
		return nil, nil
	}
	return &repo.FeedbackRow{Reason: fb.Reason, Comment: fb.Comment}, nil
}

type fakeChatStreamH struct {
	deltas []string
	err    error
}

func (f *fakeChatStreamH) ChatStream(context.Context, llm.ResolvedConfig, llm.ChatRequest) (<-chan llm.ChatDelta, error) {
	if f.err != nil {
		return nil, f.err
	}
	ch := make(chan llm.ChatDelta, len(f.deltas))
	for _, d := range f.deltas {
		ch <- llm.ChatDelta{Content: d}
	}
	close(ch)
	return ch, nil
}

type fakeChatResolverH struct{}

func (fakeChatResolverH) GetDefault(_ context.Context, purpose int16) (llm.ResolvedConfig, error) {
	if purpose != model.ModelPurposeChat {
		return llm.ResolvedConfig{}, fmt.Errorf("unexpected purpose %d", purpose)
	}
	return llm.ResolvedConfig{EntryID: 3, ModelName: "qwen2.5"}, nil
}

type fakeChatSearchH struct {
	hits []service.SearchItem
}

func (f *fakeChatSearchH) SearchTopN(context.Context, int64, string, int) ([]service.SearchItem, error) {
	return f.hits, nil
}

func intptrH(v int) *int          { return &v }
func strPtrChat(v string) *string { return &v }

// ---- 准备：建库 + 建会话 ----

func newChatEnv(t *testing.T) (*hEnv, string, int64) {
	t.Helper()
	h := setup(t)
	_, body := post(t, h.ts, "/api/v1/auth/register", "", map[string]any{"username": "root", "password": "password8"})
	token, _ := body["token"].(string)
	require.NotEmpty(t, token)
	sc, kb := post(t, h.ts, "/api/v1/kbs", token, map[string]any{"name": "问答库"})
	require.Equal(t, 201, sc, kb)
	return h, token, int64(kb["id"].(float64))
}

// postSSE 发送 SSE 请求，返回状态码、Content-Type 与原始响应体。
func postSSE(t *testing.T, ts *httptest.Server, path, token, payload string) (int, string, string) {
	t.Helper()
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodPost, ts.URL+path, strings.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := ts.Client().Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	var buf bytes.Buffer
	_, _ = io.Copy(&buf, resp.Body)
	return resp.StatusCode, resp.Header.Get("Content-Type"), buf.String()
}

type sseEvent struct {
	event string
	data  string
}

func parseSSE(t *testing.T, raw string) []sseEvent {
	t.Helper()
	events := []sseEvent{}
	var cur sseEvent
	for _, line := range strings.Split(raw, "\n") {
		line = strings.TrimRight(line, "\r")
		switch {
		case strings.HasPrefix(line, "event: "):
			cur.event = strings.TrimPrefix(line, "event: ")
		case strings.HasPrefix(line, "data: "):
			cur.data = strings.TrimPrefix(line, "data: ")
		case line == "" && cur.event != "":
			events = append(events, cur)
			cur = sseEvent{}
		}
	}
	if cur.event != "" {
		events = append(events, cur)
	}
	return events
}

// ---- SSE 全流程 ----

func TestChatSSEFlow(t *testing.T) {
	h, token, kbID := newChatEnv(t)
	h.chatSearch.hits = []service.SearchItem{
		{ChunkID: 31, DocumentID: 12, FilePath: strPtrChat("internal/handler/auth.go"), LineStart: intptrH(45), LineEnd: intptrH(62), Content: "func Login"},
	}

	// 建会话
	sc, body := post(t, h.ts, "/api/v1/kbs/"+i64(kbID)+"/conversations", token, nil)
	require.Equal(t, 201, sc, body)
	convID := int64(body["id"].(float64))

	// 我的会话列表
	sc, body = get(t, h.ts, "/api/v1/kbs/"+i64(kbID)+"/conversations", token)
	require.Equal(t, 200, sc)
	items, _ := body["items"].([]any)
	require.Len(t, items, 1)

	// 提问（SSE）
	sc, ct, raw := postSSE(t, h.ts, "/api/v1/conversations/"+i64(convID)+"/messages", token, `{"content":"登录怎么实现"}`)
	require.Equal(t, 200, sc, raw)
	assert.Equal(t, "text/event-stream", ct)

	events := parseSSE(t, raw)
	require.Len(t, events, 3, raw)
	assert.Equal(t, []string{"delta", "citations", "done"}, []string{events[0].event, events[1].event, events[2].event})

	var doneData map[string]any
	require.NoError(t, json.Unmarshal([]byte(events[2].data), &doneData))
	require.Greater(t, doneData["message_id"].(float64), float64(0))
	assert.Positive(t, doneData["prompt_tokens"].(float64))
	assert.Positive(t, doneData["completion_tokens"].(float64))

	// 消息历史含引用
	sc, body = get(t, h.ts, "/api/v1/conversations/"+i64(convID), token)
	require.Equal(t, 200, sc, body)
	msgs, _ := body["messages"].([]any)
	require.Len(t, msgs, 2)
	ans, _ := msgs[1].(map[string]any)
	assert.Equal(t, "assistant", ans["role"])
	assert.Equal(t, "回答内容", ans["content"])
	cites, _ := ans["citations"].([]any)
	require.Len(t, cites, 1)
	c0, _ := cites[0].(map[string]any)
	assert.Equal(t, "internal/handler/auth.go", c0["file_path"])

	// 重命名
	sc, body = patch(t, h.ts, "/api/v1/conversations/"+i64(convID), token, map[string]any{"title": "新标题"})
	assert.Equal(t, 200, sc, body)
	sc, body = get(t, h.ts, "/api/v1/conversations/"+i64(convID), token)
	assert.Equal(t, 200, sc)
	assert.Equal(t, "新标题", body["title"])

	// 删除后 404
	sc, body = del(t, h.ts, "/api/v1/conversations/"+i64(convID), token)
	assert.Equal(t, 200, sc, body)
	sc, _ = get(t, h.ts, "/api/v1/conversations/"+i64(convID), token)
	assert.Equal(t, 404, sc)
}

// ---- 访问规则 ----

func TestChatAccessRulesAPI(t *testing.T) {
	h, rootToken, kbID := newChatEnv(t)
	sc, body := post(t, h.ts, "/api/v1/kbs/"+i64(kbID)+"/conversations", rootToken, nil)
	require.Equal(t, 201, sc, body)
	convID := int64(body["id"].(float64))

	// 他人（非成员）访问 → 404（不泄露）
	_, _ = post(t, h.ts, "/api/v1/admin/users", rootToken, map[string]any{"username": "guest", "password": "guest-pass-1"})
	guestToken := loginReadyToken(t, h.ts, "guest", "guest-pass-1", "guest-pass-2")
	sc, body = get(t, h.ts, "/api/v1/conversations/"+i64(convID), guestToken)
	assert.Equal(t, 404, sc)
	assert.Equal(t, "NOT_FOUND", jcode(t, body))
	sc, _, _ = postSSE(t, h.ts, "/api/v1/conversations/"+i64(convID)+"/messages", guestToken, `{"content":"hi"}`)
	assert.Equal(t, 404, sc)

	// 管理员读他人会话 → 200；改删 → 404
	_, _ = post(t, h.ts, "/api/v1/admin/users", rootToken, map[string]any{"username": "boss", "password": "boss-pass-12", "is_admin": true})
	bossToken := loginReadyToken(t, h.ts, "boss", "boss-pass-12", "boss-pass-13")
	sc, _ = get(t, h.ts, "/api/v1/conversations/"+i64(convID), bossToken)
	assert.Equal(t, 200, sc)
	sc, _ = patch(t, h.ts, "/api/v1/conversations/"+i64(convID), bossToken, map[string]any{"title": "x"})
	assert.Equal(t, 404, sc)
	sc, _ = del(t, h.ts, "/api/v1/conversations/"+i64(convID), bossToken)
	assert.Equal(t, 404, sc)

	// 未登录 → 401
	sc, _ = get(t, h.ts, "/api/v1/conversations/"+i64(convID), "")
	assert.Equal(t, 401, sc)
}

// ---- 问答限流 10 req/min ----

func TestChatRateLimit(t *testing.T) {
	h, token, kbID := newChatEnv(t)
	_, body := post(t, h.ts, "/api/v1/kbs/"+i64(kbID)+"/conversations", token, nil)
	convID := int64(body["id"].(float64))

	path := "/api/v1/conversations/" + i64(convID) + "/messages"
	status := make([]int, 0)
	for i := 0; i < 11; i++ {
		code, _, _ := postSSE(t, h.ts, path, token, fmt.Sprintf(`{"content":"问题%d"}`, i))
		status = append(status, code)
	}
	for i, code := range status[:10] {
		assert.Equal(t, 200, code, "第 %d 次应放行", i+1)
	}
	assert.Equal(t, 429, status[10], "第 11 次应限流")
}

// ---- 重新生成 ----

func TestRegenerateAPI(t *testing.T) {
	h, token, kbID := newChatEnv(t)
	sc, body := post(t, h.ts, "/api/v1/kbs/"+i64(kbID)+"/conversations", token, nil)
	require.Equal(t, 201, sc, body)
	convID := int64(body["id"].(float64))
	sc, _, raw := postSSE(t, h.ts, "/api/v1/conversations/"+i64(convID)+"/messages", token, `{"content":"第一问"}`)
	require.Equal(t, 200, sc, raw)
	events := parseSSE(t, raw)
	var done map[string]any
	require.NoError(t, json.Unmarshal([]byte(events[len(events)-1].data), &done))
	msgID := i64(int64(done["message_id"].(float64)))

	// 重新生成：SSE done 且 message_id 不变
	sc, _, raw = postSSE(t, h.ts, "/api/v1/messages/"+msgID+"/regenerate", token, "")
	require.Equal(t, 200, sc, raw)
	ev2 := parseSSE(t, raw)
	require.NotEmpty(t, ev2)
	assert.Equal(t, "done", ev2[len(ev2)-1].event)
	var done2 map[string]any
	require.NoError(t, json.Unmarshal([]byte(ev2[len(ev2)-1].data), &done2))
	assert.Equal(t, done["message_id"], done2["message_id"], "重新生成复用同一消息 ID")

	// user 消息不可重新生成 → 422（JSON 错误响应而非 SSE）
	sc, body = get(t, h.ts, "/api/v1/conversations/"+i64(convID), token)
	require.Equal(t, 200, sc, body)
	msgs, _ := body["messages"].([]any)
	userMsg, _ := msgs[0].(map[string]any)
	userID := i64(int64(userMsg["id"].(float64)))
	sc, body = post(t, h.ts, "/api/v1/messages/"+userID+"/regenerate", token, nil)
	assert.Equal(t, 422, sc)
	assert.Equal(t, "VALIDATION_FAILED", jcode(t, body))
}

// ---- 点踩反馈 ----

func TestFeedbackAPI(t *testing.T) {
	h, token, kbID := newChatEnv(t)
	sc, body := post(t, h.ts, "/api/v1/kbs/"+i64(kbID)+"/conversations", token, nil)
	require.Equal(t, 201, sc, body)
	convID := int64(body["id"].(float64))
	sc, _, raw := postSSE(t, h.ts, "/api/v1/conversations/"+i64(convID)+"/messages", token, `{"content":"第一问"}`)
	require.Equal(t, 200, sc, raw)
	events := parseSSE(t, raw)
	var done map[string]any
	require.NoError(t, json.Unmarshal([]byte(events[len(events)-1].data), &done))
	msgID := i64(int64(done["message_id"].(float64)))

	// reason 非法 → 422
	sc, body = post(t, h.ts, "/api/v1/messages/"+msgID+"/feedback", token, map[string]any{"reason": 9})
	assert.Equal(t, 422, sc)
	assert.Equal(t, "VALIDATION_FAILED", jcode(t, body))

	// reason=4 缺 comment → 422
	sc, _ = post(t, h.ts, "/api/v1/messages/"+msgID+"/feedback", token, map[string]any{"reason": 4})
	assert.Equal(t, 422, sc)

	// 合法点踩 → 历史含反馈
	sc, body = post(t, h.ts, "/api/v1/messages/"+msgID+"/feedback", token, map[string]any{"reason": 1})
	assert.Equal(t, 200, sc, body)
	fb := msgFeedback(t, h.ts, token, convID)
	require.NotNil(t, fb)
	assert.InDelta(t, float64(1), fb["reason"], 0.001)

	// 重复覆盖
	_, _ = post(t, h.ts, "/api/v1/messages/"+msgID+"/feedback", token, map[string]any{"reason": 2, "comment": "不准"})
	fb = msgFeedback(t, h.ts, token, convID)
	assert.InDelta(t, float64(2), fb["reason"], 0.001)
	assert.Equal(t, "不准", fb["comment"])

	// 撤销 → 反馈消失；再撤销 → 404
	sc, _ = del(t, h.ts, "/api/v1/messages/"+msgID+"/feedback", token)
	assert.Equal(t, 200, sc)
	assert.Nil(t, msgFeedback(t, h.ts, token, convID))
	sc, _ = del(t, h.ts, "/api/v1/messages/"+msgID+"/feedback", token)
	assert.Equal(t, 404, sc)
}

// msgFeedback 读取会话历史中 assistant 消息的反馈。
func msgFeedback(t *testing.T, ts *httptest.Server, token string, convID int64) map[string]any {
	t.Helper()
	sc, body := get(t, ts, "/api/v1/conversations/"+i64(convID), token)
	require.Equal(t, 200, sc, body)
	msgs, _ := body["messages"].([]any)
	ans, _ := msgs[1].(map[string]any)
	fb, _ := ans["feedback"].(map[string]any)
	return fb
}

// ---- 绑定失败 ----

func TestChatBindFailures(t *testing.T) {
	h, token, kbID := newChatEnv(t)
	_, body := post(t, h.ts, "/api/v1/kbs/"+i64(kbID)+"/conversations", token, nil)
	convID := int64(body["id"].(float64))

	// 缺 content → 422
	sc, body := post(t, h.ts, "/api/v1/conversations/"+i64(convID)+"/messages", token, nil)
	assert.Equal(t, 422, sc)
	assert.Equal(t, "VALIDATION_FAILED", jcode(t, body))

	// 重命名缺 title → 422
	sc, _ = patch(t, h.ts, "/api/v1/conversations/"+i64(convID), token, map[string]any{})
	assert.Equal(t, 422, sc)

	// 反馈缺 reason → 422
	sc, _ = post(t, h.ts, "/api/v1/messages/1/feedback", token, map[string]any{})
	assert.Equal(t, 422, sc)
}
