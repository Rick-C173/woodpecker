package service

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"woodpecker/internal/llm"
	"woodpecker/internal/model"
	"woodpecker/internal/repo"
)

// 会话与问答参数（技术选型/调参笔记）。
const (
	chatMaxContentLen = 4000 // 提问长度上限（rune）
	chatHistoryTurns  = 8    // 多轮上下文携带的最近消息数
	chatTopN          = 5    // 引用上限（PRD R5）
	titleMaxRunes     = 50   // 会话标题截断（FR-09 首条提问摘要）
	chatMaxAttempts   = 2    // 回答生成次数上限（引用编号越界时重试一次）
)

// persistTimeout 落库超时：客户端断开后 ctx 已取消，落库必须脱离该取消信号（P0-2）。
const persistTimeout = 5 * time.Second

// ChatEvent SSE 事件（handler 序列化为 event/data 帧）。
type ChatEvent struct {
	Type             string         `json:"type"` // delta | citations | done | error
	Content          string         `json:"content,omitempty"`
	Items            []CitationItem `json:"items,omitempty"`
	MessageID        int64          `json:"message_id,omitempty"`
	PromptTokens     int            `json:"prompt_tokens,omitempty"`
	CompletionTokens int            `json:"completion_tokens,omitempty"`
	Code             string         `json:"code,omitempty"`
	Message          string         `json:"message,omitempty"`
}

// CitationItem 引用条目（API设计.md §4 citations 帧）。
type CitationItem struct {
	// 引用序号（按回答中 [n] 首次出现顺序，从 1 开始）
	Seq int `json:"seq"`
	// 来源文档 ID
	DocumentID int64 `json:"document_id"`
	// 来源分块 ID
	ChunkID int64 `json:"chunk_id"`
	// 代码/文件路径（url 类型文档为 null）
	FilePath *string `json:"file_path"`
	// 起始行（1-based，url 类型为 null）
	LineStart *int `json:"line_start"`
	// 结束行（含）
	LineEnd *int `json:"line_end"`
	// Markdown 标题路径（如 设计 > 限流），代码文件为 null
	HeadingPath *string `json:"heading_path"`
	// 网页原文 URL（仅 url 类型文档）
	SourceURL *string `json:"source_url"`
	// 来源分块内容快照
	Snippet string `json:"snippet"`
}

// ConversationItem 会话列表条目。
type ConversationItem struct {
	// 会话 ID
	ID int64 `json:"id"`
	// 所属知识库 ID
	KBID int64 `json:"kb_id"`
	// 标题（默认取首条提问摘要，不超过 50 字）
	Title string `json:"title"`
	// 消息条数
	MessageCount int64 `json:"message_count"`
	// 最近活跃时间 RFC3339 UTC
	UpdatedAt string `json:"updated_at"`
}

// MessageItem 消息历史条目（含引用与我的反馈）。
type MessageItem struct {
	// 消息 ID
	ID int64 `json:"id"`
	// 角色
	Role string `json:"role"` // user | assistant
	// 消息内容（回答中的引用以 [n] 形式内嵌）
	Content string `json:"content"`
	// 回答是否被中断（客户端断开）
	Interrupted bool `json:"interrupted"`
	// 回答生成 token 数（user 消息为 0）
	CompletionTokens int `json:"completion_tokens"`
	// 创建时间 RFC3339 UTC
	CreatedAt string `json:"created_at"`
	// 引用列表（仅 assistant 且回答中有 [n] 标注时非空）
	Citations []CitationItem `json:"citations"`
	// 我（当前请求者）对该回答的点踩，未点踩为 null
	Feedback *FeedbackItem `json:"feedback,omitempty"`
}

// FeedbackItem 我的点踩。
type FeedbackItem struct {
	// 点踩原因 1-4：1 答案不相关 / 2 引用错误 / 3 内容过时 / 4 其他
	Reason int `json:"reason"`
	// 补充说明（可空）
	Comment string `json:"comment,omitempty"`
}

// ConversationDetail 会话详情（消息历史含引用）。
type ConversationDetail struct {
	ID       int64         `json:"id"`
	KBID     int64         `json:"kb_id"`
	Title    string        `json:"title"`
	Messages []MessageItem `json:"messages"`
}

// ConvStore 会话存储（repo.ConversationRepo 满足）。
type ConvStore interface {
	CreateConversation(ctx context.Context, c *model.Conversation) error
	SaveConversation(ctx context.Context, c *model.Conversation) error
	GetConversationByID(ctx context.Context, id int64) (*model.Conversation, error)
	ListMine(ctx context.Context, kbID, userID int64) ([]repo.ConversationRow, error)
	DeleteConversation(ctx context.Context, id int64) error
	CreateMessage(ctx context.Context, m *model.Message) error
	SaveMessage(ctx context.Context, m *model.Message) error
	GetMessageByID(ctx context.Context, id int64) (*model.Message, error)
	ListMessages(ctx context.Context, conversationID int64) ([]repo.MessageRow, error)
	LastMessages(ctx context.Context, conversationID int64, n int) ([]model.Message, error)
	ReplaceCitations(ctx context.Context, messageID int64, cs []model.MessageCitation) error
	UpsertFeedback(ctx context.Context, f *model.Feedback) error
	DeleteFeedback(ctx context.Context, messageID, userID int64) error
	GetFeedbackByUser(ctx context.Context, messageID, userID int64) (*repo.FeedbackRow, error)
}

// ChatMemberChecker 会话所属库的成员资格查询（repo.KBRepo 满足；R6 非成员 404）。
type ChatMemberChecker interface {
	GetMemberRole(ctx context.Context, kbID, userID int64) (int16, error)
}

// ChatStreamer 流式对话（llm.Provider 满足）。
type ChatStreamer interface {
	ChatStream(ctx context.Context, cfg llm.ResolvedConfig, req llm.ChatRequest) (<-chan llm.ChatDelta, error)
}

// ChatConfigResolver 默认对话模型配置（*ModelConfigService 满足）。
type ChatConfigResolver interface {
	GetDefault(ctx context.Context, purpose int16) (llm.ResolvedConfig, error)
}

// ChatSearcher 问答检索（*SearchService 满足）。
type ChatSearcher interface {
	SearchTopN(ctx context.Context, kbID int64, q string, n int) ([]SearchItem, error)
}

// ChatService 会话与问答（FR-07/08/09/14）。
type ChatService struct {
	store    ConvStore
	members  ChatMemberChecker
	resolver ChatConfigResolver
	streamer ChatStreamer
	search   ChatSearcher
	now      func() time.Time
}

func NewChatService(store ConvStore, members ChatMemberChecker, resolver ChatConfigResolver, streamer ChatStreamer, search ChatSearcher) *ChatService {
	return &ChatService{store: store, members: members, resolver: resolver, streamer: streamer, search: search, now: time.Now}
}

// canAccess 会话访问规则（API设计.md §3.7）：非成员 404（不泄露库名）→ 他人会话 404 → 管理员放行。
func (s *ChatService) canAccess(ctx context.Context, convID, userID int64, isAdmin bool) (*model.Conversation, error) {
	c, err := s.store.GetConversationByID(ctx, convID)
	if err != nil {
		if errors.Is(err, repo.ErrNotFound) {
			return nil, NewServiceError(CodeNotFound, "会话不存在")
		}
		return nil, err
	}
	if !isAdmin {
		role, err := s.members.GetMemberRole(ctx, c.KBID, userID)
		if err != nil {
			if errors.Is(err, repo.ErrNotFound) {
				return nil, NewServiceError(CodeNotFound, "会话不存在")
			}
			return nil, err
		}
		_ = role
	}
	if !isAdmin && c.UserID != userID {
		return nil, NewServiceError(CodeNotFound, "会话不存在")
	}
	return c, nil
}

// CreateConversation 新建会话（FR-09）。
func (s *ChatService) CreateConversation(ctx context.Context, kbID, userID int64) (*ConversationItem, error) {
	c := &model.Conversation{KBID: kbID, UserID: userID, CreatedAt: s.now(), UpdatedAt: s.now()}
	if err := s.store.CreateConversation(ctx, c); err != nil {
		return nil, err
	}
	return &ConversationItem{ID: c.ID, KBID: kbID, Title: "", UpdatedAt: c.UpdatedAt.UTC().Format(time.RFC3339)}, nil
}

// ListMine 我的会话列表（FR-09）。
func (s *ChatService) ListMine(ctx context.Context, kbID, userID int64) ([]ConversationItem, error) {
	rows, err := s.store.ListMine(ctx, kbID, userID)
	if err != nil {
		return nil, err
	}
	items := make([]ConversationItem, 0, len(rows))
	for _, r := range rows {
		items = append(items, ConversationItem{
			ID: r.ID, KBID: r.KBID, Title: derefTitle(r.Title),
			MessageCount: r.MessageCount, UpdatedAt: r.UpdatedAt.UTC().Format(time.RFC3339),
		})
	}
	return items, nil
}

// Get 会话详情：消息历史（含引用与我的反馈）。
func (s *ChatService) Get(ctx context.Context, convID, userID int64, isAdmin bool) (*ConversationDetail, error) {
	c, err := s.canAccess(ctx, convID, userID, isAdmin)
	if err != nil {
		return nil, err
	}
	msgRows, err := s.store.ListMessages(ctx, convID)
	if err != nil {
		return nil, err
	}
	detail := &ConversationDetail{ID: c.ID, KBID: c.KBID, Title: derefTitle(c.Title), Messages: []MessageItem{}}
	for _, m := range msgRows {
		item := MessageItem{
			ID: m.ID, Role: msgRoleName(m.Role), Content: m.Content,
			Interrupted: m.Interrupted, CreatedAt: m.CreatedAt.UTC().Format(time.RFC3339),
			Citations: []CitationItem{},
		}
		if m.CompletionTokens != nil {
			item.CompletionTokens = *m.CompletionTokens
		}
		for _, ct := range m.Citations {
			item.Citations = append(item.Citations, citationItem(ct))
		}
		if m.Role == model.MsgRoleAssistant {
			fb, err := s.store.GetFeedbackByUser(ctx, m.ID, c.UserID)
			if err != nil {
				return nil, err
			}
			if fb != nil {
				fi := &FeedbackItem{Reason: int(fb.Reason)}
				if fb.Comment != nil {
					fi.Comment = *fb.Comment
				}
				item.Feedback = fi
			}
		}
		detail.Messages = append(detail.Messages, item)
	}
	return detail, nil
}

// Rename 重命名会话（所属用户；管理员不可改他人会话）。
func (s *ChatService) Rename(ctx context.Context, convID, userID int64, isAdmin bool, title string) error {
	c, err := s.canAccess(ctx, convID, userID, isAdmin)
	if err != nil {
		return err
	}
	if isAdmin && c.UserID != userID {
		return NewServiceError(CodeNotFound, "会话不存在")
	}
	title = strings.TrimSpace(title)
	if title == "" || utf8.RuneCountInString(title) > 200 {
		return NewServiceError(CodeValidation, "标题不能为空且不超过 200 字符")
	}
	c.Title = &title
	c.UpdatedAt = s.now()
	return s.store.SaveConversation(ctx, c)
}

// Delete 删除会话（所属用户或管理员；反馈级联删）。
func (s *ChatService) Delete(ctx context.Context, convID, userID int64, isAdmin bool) error {
	c, err := s.canAccess(ctx, convID, userID, isAdmin)
	if err != nil {
		return err
	}
	if isAdmin && c.UserID != userID {
		return NewServiceError(CodeNotFound, "会话不存在")
	}
	return s.store.DeleteConversation(ctx, convID)
}

// Ask 提问（FR-07）：返回事件通道（delta* → citations? → done；异常 error；ctx 取消=停止生成）。
// 调用返回即完成校验与 user 消息落库；SSE 流式与 assistant 落库在后台进行。
func (s *ChatService) Ask(ctx context.Context, convID, userID int64, isAdmin bool, content string) (<-chan ChatEvent, error) {
	c, err := s.canAccess(ctx, convID, userID, isAdmin)
	if err != nil {
		return nil, err
	}
	if c.UserID != userID {
		return nil, NewServiceError(CodeNotFound, "会话不存在")
	}
	content = strings.TrimSpace(content)
	if content == "" {
		return nil, NewServiceError(CodeValidation, "提问内容不能为空")
	}
	if utf8.RuneCountInString(content) > chatMaxContentLen {
		return nil, NewServiceError(CodeValidation, "提问不超过 4000 字符")
	}

	// user 消息先落库（提问已发生；停止生成也保留提问）
	now := s.now()
	msg := &model.Message{
		ConversationID: convID, Role: model.MsgRoleUser, Content: content, CreatedAt: now,
	}
	if err := s.store.CreateMessage(ctx, msg); err != nil {
		return nil, err
	}
	// 会话标题缺省取首条提问摘要（FR-09）
	if c.Title == nil {
		title := truncateRunes(content, titleMaxRunes)
		c.Title = &title
	}
	c.UpdatedAt = now
	if err := s.store.SaveConversation(ctx, c); err != nil {
		return nil, err
	}
	return s.askCore(ctx, c, content, 0), nil
}

// Regenerate 重新生成（FR-07 例外）：替换指定回答内容（保留原提问）。
func (s *ChatService) Regenerate(ctx context.Context, messageID, userID int64, isAdmin bool) (<-chan ChatEvent, error) {
	m, err := s.store.GetMessageByID(ctx, messageID)
	if err != nil {
		if errors.Is(err, repo.ErrNotFound) {
			return nil, NewServiceError(CodeNotFound, "消息不存在")
		}
		return nil, err
	}
	if m.Role != model.MsgRoleAssistant {
		return nil, NewServiceError(CodeValidation, "仅回答消息可重新生成")
	}
	c, err := s.canAccess(ctx, m.ConversationID, userID, isAdmin)
	if err != nil {
		return nil, err
	}
	if c.UserID != userID {
		return nil, NewServiceError(CodeNotFound, "会话不存在")
	}
	// 该回答前最近一条 user 消息为原提问
	history, err := s.store.LastMessages(ctx, m.ConversationID, chatHistoryTurns+2)
	if err != nil {
		return nil, err
	}
	var question string
	for i := len(history) - 1; i >= 0; i-- {
		if history[i].ID == messageID {
			continue // 跳过被替换的回答本身
		}
		if history[i].Role == model.MsgRoleUser {
			question = history[i].Content
			break
		}
	}
	if question == "" {
		return nil, NewServiceError(CodeValidation, "未找到原提问")
	}
	return s.askCore(ctx, c, question, messageID), nil
}

// askCore 问答编排核心（SSE 事件流后台推进）。
// replaceMsgID > 0 表示重新生成：更新该消息而非新建。
func (s *ChatService) askCore(ctx context.Context, c *model.Conversation, question string, replaceMsgID int64) <-chan ChatEvent {
	ch := make(chan ChatEvent, 16)
	go func() {
		defer close(ch)
		emit := func(e ChatEvent) bool {
			select {
			case ch <- e:
				return true
			case <-ctx.Done():
				return false
			}
		}

		// 默认对话模型（缺省 → LLM 场景映射 MODEL_001）
		cfg, err := s.resolver.GetDefault(ctx, model.ModelPurposeChat)
		if err != nil {
			emit(ChatEvent{Type: "error", Code: codeOfModel(err), Message: err.Error()})
			return
		}

		// 检索 Top-5 引用（PRD R5）；检索失败降级为无引用直答
		var hits []SearchItem
		if s.search != nil {
			hits, _ = s.search.SearchTopN(ctx, c.KBID, question, chatTopN)
		}

		// 多轮上下文（最近 N 条；重新生成时排除被替换的旧回答）
		history, _ := s.store.LastMessages(ctx, c.ID, chatHistoryTurns+2)
		var hist strings.Builder
		for _, m := range history {
			if replaceMsgID > 0 && m.ID == replaceMsgID {
				continue
			}
			role := "用户"
			if m.Role == model.MsgRoleAssistant {
				role = "助手"
			}
			hist.WriteString(role + ": " + m.Content + "\n")
		}

		// 生成回答（引用编号越界时重试一次，FR-07 输出校验）
		var answer string
		for attempt := 0; attempt < chatMaxAttempts; attempt++ {
			stream, serr := s.streamer.ChatStream(ctx, cfg, llm.ChatRequest{
				System: buildChatSystem(hits), Prompt: buildChatPrompt(hist.String(), question),
			})
			if serr != nil {
				emit(ChatEvent{Type: "error", Code: "LLM_001", Message: serr.Error()})
				return
			}
			var sb strings.Builder
			generated := false
			for delta := range stream {
				sb.WriteString(delta.Content)
				generated = true
				if !emit(ChatEvent{Type: "delta", Content: delta.Content}) {
					break // 客户端断开：中断生成，走下方落库（保留已生成部分）
				}
			}
			if !generated && ctx.Err() == nil {
				emit(ChatEvent{Type: "error", Code: "LLM_002", Message: "模型服务无有效响应"})
				return
			}
			answer = strings.TrimSpace(sb.String())
			if answer == "" {
				answer = "（未生成内容）"
			}
			if ctx.Err() != nil || attempt == chatMaxAttempts-1 || !hasOutOfBoundsCitation(answer, len(hits)) {
				break
			}
			// 回答引用了超出资料范围的编号 → 重新生成一次
		}

		// 引用事件：只发回答里真正标注过的 [n]（审计 P1-16）。
		// 回答未引用任何命中（含「未找到足够依据」自述）→ cites 为空 → 不发明细帧；
		// 落库与 SSE 使用同一份 cites，避免「库里 5 条、前端 1 条」的不一致。
		cites := citationsOf(answer, hits)
		promptTokens := utf8.RuneCountInString(buildChatPrompt(hist.String(), question)) + utf8.RuneCountInString(buildChatSystem(hits))
		completionTokens := utf8.RuneCountInString(answer)

		// done 时才写库（API设计.md §4）；中断标记 interrupted。
		// 客户端断开后入参 ctx 已被取消，若沿用同一个 ctx 落库，database/sql 会立即返回
		// context canceled，「已生成部分保留 + interrupted=true」就永远不成立（P0-2）。
		// 故落库走脱离取消信号的 persistCtx（保留 ctx 的 value/trace，仅去掉取消与超时）。
		interrupted := ctx.Err() != nil
		persistCtx, cancelPersist := context.WithTimeout(context.WithoutCancel(ctx), persistTimeout)
		defer cancelPersist()
		now := s.now()
		var savedMsgID int64
		if replaceMsgID > 0 {
			m, gerr := s.store.GetMessageByID(persistCtx, replaceMsgID)
			if gerr != nil {
				emit(ChatEvent{Type: "error", Code: "INTERNAL", Message: gerr.Error()})
				return
			}
			m.Content = answer
			m.Interrupted = interrupted
			m.PromptTokens = &promptTokens
			m.CompletionTokens = &completionTokens
			m.ModelEntryID = modelEntryID(cfg)
			if gerr := s.store.SaveMessage(persistCtx, m); gerr != nil {
				emit(ChatEvent{Type: "error", Code: "INTERNAL", Message: gerr.Error()})
				return
			}
			if gerr := s.store.ReplaceCitations(persistCtx, replaceMsgID, citationModels(replaceMsgID, cites)); gerr != nil {
				emit(ChatEvent{Type: "error", Code: "INTERNAL", Message: gerr.Error()})
				return
			}
			savedMsgID = replaceMsgID
		} else {
			m := &model.Message{
				ConversationID: c.ID, Role: model.MsgRoleAssistant, Content: answer,
				Interrupted: interrupted, PromptTokens: &promptTokens, CompletionTokens: &completionTokens,
				ModelEntryID: modelEntryID(cfg), CreatedAt: now,
			}
			if gerr := s.store.CreateMessage(persistCtx, m); gerr != nil {
				emit(ChatEvent{Type: "error", Code: "INTERNAL", Message: gerr.Error()})
				return
			}
			if gerr := s.store.ReplaceCitations(persistCtx, m.ID, citationModels(m.ID, cites)); gerr != nil {
				emit(ChatEvent{Type: "error", Code: "INTERNAL", Message: gerr.Error()})
				return
			}
			savedMsgID = m.ID
		}
		if len(cites) > 0 {
			emit(ChatEvent{Type: "citations", Items: cites})
		}
		emit(ChatEvent{Type: "done", MessageID: savedMsgID, PromptTokens: promptTokens, CompletionTokens: completionTokens})
	}()
	return ch
}

// Feedback 点踩（FR-14）：仅回答消息；重复覆盖；reason=4（其他）comment 必填。
func (s *ChatService) Feedback(ctx context.Context, messageID, userID int64, isAdmin bool, reason int16, comment string) error {
	m, err := s.store.GetMessageByID(ctx, messageID)
	if err != nil {
		if errors.Is(err, repo.ErrNotFound) {
			return NewServiceError(CodeNotFound, "消息不存在")
		}
		return err
	}
	if m.Role != model.MsgRoleAssistant {
		return NewServiceError(CodeValidation, "仅回答消息可反馈")
	}
	c, err := s.canAccess(ctx, m.ConversationID, userID, isAdmin)
	if err != nil {
		return err
	}
	if c.UserID != userID {
		return NewServiceError(CodeNotFound, "会话不存在")
	}
	if reason < model.FBReasonIrrelevant || reason > model.FBReasonOther {
		return NewServiceError(CodeValidation, "reason 取值 1-4")
	}
	comment = strings.TrimSpace(comment)
	if reason == model.FBReasonOther && comment == "" {
		return NewServiceError(CodeValidation, "原因选择“其他”时必须填写说明")
	}
	var commentPtr *string
	if comment != "" {
		commentPtr = &comment
	}
	return s.store.UpsertFeedback(ctx, &model.Feedback{
		MessageID: messageID, UserID: userID, Reason: reason, Comment: commentPtr, CreatedAt: s.now(),
	})
}

// DeleteFeedback 撤销点踩（FR-14）：无记录 → 404。
func (s *ChatService) DeleteFeedback(ctx context.Context, messageID, userID int64) error {
	if err := s.store.DeleteFeedback(ctx, messageID, userID); err != nil {
		if errors.Is(err, repo.ErrNotFound) {
			return NewServiceError(CodeNotFound, "未找到该反馈")
		}
		return err
	}
	return nil
}

// ---- prompt 组装 ----

// buildChatSystem 系统提示：仅依据资料回答 + 依据不足自述（FR-07 依据不足规则）。
func buildChatSystem(hits []SearchItem) string {
	var b strings.Builder
	b.WriteString("你是企业知识库助手。仅依据下方【资料】回答用户问题；资料不足以回答时，明确说明\"知识库中未找到足够依据\"，不要编造。回答使用中文。引用资料时，必须在对应语句末尾标注该资料的编号：格式为方括号内纯数字，如 [1]、[2]（不要写成 [编号1] 或其他变体）。")
	if len(hits) == 0 {
		return b.String()
	}
	b.WriteString("\n\n【资料】\n")
	for i, h := range hits {
		loc := citationLoc(h)
		fmt.Fprintf(&b, "[%d] (%s)\n%s\n\n", i+1, loc, h.Content)
	}
	return b.String()
}

// buildChatPrompt 对话历史 + 本轮提问。
func buildChatPrompt(history, question string) string {
	var b strings.Builder
	if history != "" {
		b.WriteString("【对话历史】\n")
		b.WriteString(history)
		b.WriteString("\n")
	}
	b.WriteString("【提问】\n")
	b.WriteString(question)
	return b.String()
}

// citationLoc 引用位置描述（文件路径+行号 或 标题路径）。
func citationLoc(h SearchItem) string {
	if h.FilePath != nil && *h.FilePath != "" {
		if h.LineStart != nil {
			return fmt.Sprintf("%s:%d", *h.FilePath, *h.LineStart)
		}
		return *h.FilePath
	}
	if h.HeadingPath != nil && *h.HeadingPath != "" {
		return *h.HeadingPath
	}
	return "未知来源"
}

// citationsOf 回答中被引用的检索命中 → 引用条目（审计 P1-16）。
//
// 旧实现无条件把全部命中（≤5 条）当作引用发送、Seq 按检索排名编号，
// 与回答正文里的 [n] 完全脱钩：回答只用 [2] 时前端仍展示 5 条、且编号对不上。
// 新语义：只取回答中标注过且未越界的编号，Seq 按「回答中首次出现顺序」从 1 重新编号，
// 未被引用的命中一律不发（Seq 与正文 [n] 的对应关系由前端按 seq 展示，故必须重排）。
func citationsOf(answer string, hits []SearchItem) []CitationItem {
	refs := parseCitationRefs(answer, len(hits))
	items := make([]CitationItem, 0, len(refs))
	for i, ref := range refs {
		h := hits[ref-1] // ref 是资料编号（1 基），转成 hits 下标
		items = append(items, CitationItem{
			Seq: i + 1, DocumentID: h.DocumentID, ChunkID: h.ChunkID,
			FilePath: h.FilePath, LineStart: h.LineStart, LineEnd: h.LineEnd,
			HeadingPath: h.HeadingPath, Snippet: h.Content,
		})
	}
	return items
}

// citationModels 引用落库模型。
func citationModels(messageID int64, items []CitationItem) []model.MessageCitation {
	cs := make([]model.MessageCitation, 0, len(items))
	for _, it := range items {
		snippet := it.Snippet
		cs = append(cs, model.MessageCitation{
			MessageID: messageID, DocumentID: it.DocumentID, Seq: it.Seq, ChunkID: it.ChunkID,
			FilePath: it.FilePath, LineStart: it.LineStart, LineEnd: it.LineEnd,
			HeadingPath: it.HeadingPath, SourceURL: it.SourceURL, Snippet: &snippet,
		})
	}
	return cs
}

// citationItem 引用行 → API 条目。
func citationItem(r repo.CitationRow) CitationItem {
	it := CitationItem{
		Seq: r.Seq, DocumentID: r.DocumentID, ChunkID: r.ChunkID,
		FilePath: r.FilePath, LineStart: r.LineStart, LineEnd: r.LineEnd,
		HeadingPath: r.HeadingPath, SourceURL: r.SourceURL,
	}
	if r.Snippet != nil {
		it.Snippet = *r.Snippet
	}
	return it
}

// citationRefRe 回答中的引用标注 [编号]。
var citationRefRe = regexp.MustCompile(`\[(\d+)\]`)

// citationRefNumbers 回答中所有 [n] 标记的编号序列（去重前，按出现顺序；非法数字丢弃）。
func citationRefNumbers(answer string) []int {
	matches := citationRefRe.FindAllStringSubmatch(answer, -1)
	nums := make([]int, 0, len(matches))
	for _, m := range matches {
		num, err := strconv.Atoi(m[1])
		if err != nil {
			continue
		}
		nums = append(nums, num)
	}
	return nums
}

// parseCitationRefs 从回答正文提取 [n] 引用标记，按首次出现顺序去重，
// 过滤越界编号（n < 1 || n > maxRef），返回编号序列（n 本身，不是 hits 下标）。
// maxRef 为资料条数；maxRef <= 0 表示本次没有资料，任何编号都视为越界（返回空）。
func parseCitationRefs(answer string, maxRef int) []int {
	if maxRef <= 0 {
		return nil
	}
	nums := citationRefNumbers(answer)
	if len(nums) == 0 {
		return nil
	}
	seen := make(map[int]struct{}, len(nums))
	refs := make([]int, 0, len(nums))
	for _, num := range nums {
		if num < 1 || num > maxRef {
			continue // 越界编号不产生引用（由输出校验触发重新生成）
		}
		if _, dup := seen[num]; dup {
			continue
		}
		seen[num] = struct{}{}
		refs = append(refs, num)
	}
	return refs
}

// hasOutOfBoundsCitation 回答中的 [编号] 是否超出资料范围（输出校验，FR-07）。
// 复用 parseCitationRefs 的过滤规则：正文里出现过的不同编号数与过滤后的有效编号数
// 不一致，说明模型引用了不存在的资料 → 需要重新生成一次。
func hasOutOfBoundsCitation(answer string, n int) bool {
	nums := citationRefNumbers(answer)
	if len(nums) == 0 {
		return false
	}
	if n == 0 {
		return true // 无资料时不应出现任何编号引用
	}
	distinct := make(map[int]struct{}, len(nums))
	for _, num := range nums {
		distinct[num] = struct{}{}
	}
	return len(distinct) != len(parseCitationRefs(answer, n))
}

// codeOfModel 模型配置错误码映射（MODEL_001 等透传，其余 LLM_001）。
func codeOfModel(err error) string {
	var se *ServiceError
	if errors.As(err, &se) {
		return se.Code
	}
	return "LLM_001"
}

// modelEntryID 从配置提取默认条目 ID（溯源 messages.model_entry_id）。
func modelEntryID(cfg llm.ResolvedConfig) *int64 {
	if cfg.EntryID == 0 {
		return nil
	}
	id := cfg.EntryID
	return &id
}

func msgRoleName(r int16) string {
	if r == model.MsgRoleAssistant {
		return "assistant"
	}
	return "user"
}

func derefTitle(t *string) string {
	if t == nil {
		return ""
	}
	return *t
}

func truncateRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
}
