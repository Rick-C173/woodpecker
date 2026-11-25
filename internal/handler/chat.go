package handler

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"

	"woodpecker/internal/httpx"
	"woodpecker/internal/middleware"
	"woodpecker/internal/service"
)

// ChatHandler 会话与问答（FR-07/08/09/14，API设计.md §3.7/§4）。
type ChatHandler struct{ svc *service.ChatService }

func NewChatHandler(svc *service.ChatService) *ChatHandler {
	return &ChatHandler{svc: svc}
}

// convID 解析路径参数（非法返回 0）。
func convID(c *gin.Context) int64 {
	id, _ := strconv.ParseInt(c.Param("c"), 10, 64)
	return id
}

// ListConversations GET /kbs/{kb}/conversations（成员，仅我的）
//
//	@Summary	我的会话列表
//	@Description	我在该知识库下的会话列表（仅自己的，不含他人），按最近活跃排序。返回 {items:[...]}，不分页（会话数规模小）。
//	@Description
//	@Description	权限：库成员。
//	@Tags		conversations
//	@Produce	json
//	@Success	200	{object}	object{items=[]service.ConversationItem}
//	@Param	kb	path	integer	true	"知识库 ID"
//	@Failure	401	{object}	httpx.ErrorBody	"未登录 / token 失效（AUTH_004）/ 账号被禁用（AUTH_003）"
//	@Router		/kbs/{kb}/conversations [get]
func (h *ChatHandler) ListConversations(c *gin.Context) {
	items, err := h.svc.ListMine(c.Request.Context(), middleware.KBID(c), middleware.UserID(c))
	if err != nil {
		httpx.FailServiceError(c, err)
		return
	}
	httpx.OK(c, gin.H{"items": items})
}

// CreateConversation POST /kbs/{kb}/conversations（成员）
//
//	@Summary	新建会话
//	@Description	在该知识库下新建空会话，返回会话条目。标题默认为「新会话」，首条提问后自动改为提问摘要（≤50 字）。
//	@Description
//	@Description	权限：库成员。
//	@Tags		conversations
//	@Produce	json
//	@Success	201	{object}	service.ConversationItem
//	@Param	kb	path	integer	true	"知识库 ID"
//	@Failure	401	{object}	httpx.ErrorBody	"未登录 / token 失效（AUTH_004）/ 账号被禁用（AUTH_003）"
//	@Router		/kbs/{kb}/conversations [post]
func (h *ChatHandler) CreateConversation(c *gin.Context) {
	item, err := h.svc.CreateConversation(c.Request.Context(), middleware.KBID(c), middleware.UserID(c))
	if err != nil {
		httpx.FailServiceError(c, err)
		return
	}
	httpx.Created(c, item)
}

// GetConversation GET /conversations/{c}（所属用户/管理员）
//
//	@Summary	消息历史（含引用与我的反馈）
//	@Description	会话详情：基础信息 + 全部消息（含每条回答的 citations 引用与我（当前请求者）的 feedback 点踩）。
//	@Description
//	@Description	访问规则：先校验会话所属知识库的成员资格（R6 非成员 → 404，不泄露存在性），再校验会话所属用户（他人会话 → 404）；管理员可访问任意会话。被移除的成员立即失去历史会话访问。
//	@Description
//	@Description	权限：会话所属用户或管理员。
//	@Tags		conversations
//	@Produce	json
//	@Success	200	{object}	service.ConversationDetail
//	@Param	c	path	integer	true	"会话 ID"
//	@Failure	404	{object}	httpx.ErrorBody	"会话不存在 / 非所属用户且非管理员 / 非库成员（统一 404，不泄露存在性）"
//	@Failure	401	{object}	httpx.ErrorBody	"未登录 / token 失效（AUTH_004）/ 账号被禁用（AUTH_003）"
//	@Router		/conversations/{c} [get]
func (h *ChatHandler) GetConversation(c *gin.Context) {
	detail, err := h.svc.Get(c.Request.Context(), convID(c), middleware.UserID(c), middleware.IsAdmin(c))
	if err != nil {
		httpx.FailServiceError(c, err)
		return
	}
	httpx.OK(c, detail)
}

// RenameConversation PATCH /conversations/{c}（所属用户）
//
//	@Summary	重命名会话
//	@Description	重命名会话。字段缺省不修改；title 必填。
//	@Description
//	@Description	权限：会话所属用户（管理员也只能改自己的？——与实现一致：管理员可访问任意会话，但重命名/删除按所属用户语义处理）。
//	@Tags		conversations
//	@Produce	json
//	@Success	200	{object}	object{message=string}
//	@Param	c	path	integer	true	"会话 ID"
//	@Failure	422	{object}	httpx.ErrorBody	"title 为空（VALIDATION_FAILED）"
//	@Failure	404	{object}	httpx.ErrorBody	"会话不存在或无权访问"
//	@Failure	401	{object}	httpx.ErrorBody	"未登录 / token 失效（AUTH_004）/ 账号被禁用（AUTH_003）"
//	@Router		/conversations/{c} [patch]
func (h *ChatHandler) RenameConversation(c *gin.Context) {
	var req struct {
		Title string `json:"title" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		httpx.Fail(c, httpx.CodeValidation, "title 不能为空")
		return
	}
	if err := h.svc.Rename(c.Request.Context(), convID(c), middleware.UserID(c), middleware.IsAdmin(c), req.Title); err != nil {
		httpx.FailServiceError(c, err)
		return
	}
	httpx.OK(c, gin.H{"message": "已重命名"})
}

// DeleteConversation DELETE /conversations/{c}（所属用户；反馈级联删）
//
//	@Summary	删除会话
//	@Description	删除会话及其全部消息，反馈记录级联删除。
//	@Description
//	@Description	权限：会话所属用户（管理员可删任意会话）。
//	@Tags		conversations
//	@Produce	json
//	@Success	200	{object}	object{message=string}
//	@Param	c	path	integer	true	"会话 ID"
//	@Failure	404	{object}	httpx.ErrorBody	"会话不存在或无权访问"
//	@Failure	401	{object}	httpx.ErrorBody	"未登录 / token 失效（AUTH_004）/ 账号被禁用（AUTH_003）"
//	@Router		/conversations/{c} [delete]
func (h *ChatHandler) DeleteConversation(c *gin.Context) {
	if err := h.svc.Delete(c.Request.Context(), convID(c), middleware.UserID(c), middleware.IsAdmin(c)); err != nil {
		httpx.FailServiceError(c, err)
		return
	}
	httpx.OK(c, gin.H{"message": "已删除"})
}

// Ask POST /conversations/{c}/messages（所属用户，问答限流 10 req/min）：SSE 响应。
//
//	@Summary	提问（SSE 流式回答 + 引用）
//	@Description	提问（SSE 流式回答）。Content-Type: text/event-stream。
//	@Description
//	@Description	事件流顺序：delta*（回答增量，多次）→ citations?（引用列表，仅当回答中出现 [n] 标注且引用有效时发一次）→ done（结束）。异常时发 error 终止；每 15s 发 ping 心跳防代理断连。
//	@Description
//	@Description	citations 规则（审计 P1-16）：只包含回答正文里真正标注过的资料，seq 按「回答中首次出现顺序」从 1 重新编号；检索命中但回答未引用的一律不发、不落库。依据不足时正常输出说明文字，不发 citations 直接 done（PRD FR-07）。
//	@Description
//	@Description	停止生成 = 客户端断开连接；已生成的部分照常落库（role=assistant，标记 interrupted）。
//	@Description
//	@Description	流式期间消息不落库，done 时才写库（含 tokens 与 citations）。
//	@Description
//	@Description	权限：会话所属用户（管理员可代任意会话提问）；问答限流 10 req/min/用户 → 429 RATE_LIMITED。
//	@Tags		conversations
//	@Accept		json
//	@Produce	text/event-stream
//	@Param	c	path	integer	true	"会话 ID"
//	@Param	body	body	object{content=string}	true	"提问内容（≤4000 字符）"
//	@Success	200	{string}	string	"SSE 事件流：delta*（回答增量）→ citations?（引用，仅回答中实际标注 [n] 时）→ done（message_id/prompt_tokens/completion_tokens）；异常 error；每 15s 心跳 ping；停止生成=客户端断开"
//	@Failure	422	{object}	httpx.ErrorBody	"content 为空或超长（VALIDATION_FAILED）"
//	@Failure	404	{object}	httpx.ErrorBody	"会话不存在或无权访问"
//	@Failure	429	{object}	httpx.ErrorBody	"问答限流 10 req/min/用户（RATE_LIMITED）"
//	@Failure	401	{object}	httpx.ErrorBody	"未登录 / token 失效（AUTH_004）/ 账号被禁用（AUTH_003）"
//	@Router		/conversations/{c}/messages [post]
func (h *ChatHandler) Ask(c *gin.Context) {
	var req struct {
		Content string `json:"content" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		httpx.Fail(c, httpx.CodeValidation, "提问内容不能为空")
		return
	}
	events, err := h.svc.Ask(c.Request.Context(), convID(c), middleware.UserID(c), middleware.IsAdmin(c), req.Content)
	if err != nil {
		httpx.FailServiceError(c, err)
		return
	}
	writeSSE(c, events)
}

// Regenerate POST /messages/{m}/regenerate（所属用户）：SSE；替换该回答内容。
//
//	@Summary	重新生成（SSE）
//	@Description	重新生成指定回答（SSE 流，事件协议同提问接口）。成功后**替换**该条 assistant 消息内容与引用，原回答不保留版本。
//	@Description
//	@Description	权限：会话所属用户；同样受问答限流 10 req/min → 429。
//	@Tags		messages
//	@Produce	text/event-stream
//	@Param	m	path	integer	true	"回答消息 ID（须为 assistant 消息）"
//	@Success	200	{string}	string	"SSE 事件流：协议同提问接口；成功后替换该回答内容与引用"
//	@Failure	404	{object}	httpx.ErrorBody	"消息不存在或无权访问"
//	@Failure	429	{object}	httpx.ErrorBody	"问答限流（RATE_LIMITED）"
//	@Failure	401	{object}	httpx.ErrorBody	"未登录 / token 失效（AUTH_004）/ 账号被禁用（AUTH_003）"
//	@Router		/messages/{m}/regenerate [post]
func (h *ChatHandler) Regenerate(c *gin.Context) {
	id, _ := strconv.ParseInt(c.Param("m"), 10, 64)
	events, err := h.svc.Regenerate(c.Request.Context(), id, middleware.UserID(c), middleware.IsAdmin(c))
	if err != nil {
		httpx.FailServiceError(c, err)
		return
	}
	writeSSE(c, events)
}

// Feedback POST /messages/{m}/feedback（所属用户）：点踩（重复覆盖）。
//
//	@Summary	点踩反馈
//	@Description	对回答点踩（FR-14）。reason: 1=答案不相关 2=引用错误 3=内容过时 4=其他；comment 可选。重复提交覆盖上次反馈（一条回答一人一条反馈）。
//	@Description
//	@Description	权限：会话所属用户。
//	@Tags		messages
//	@Accept		json
//	@Produce	json
//	@Success	200	{object}	object{message=string}
//	@Param	m	path	integer	true	"回答消息 ID"
//	@Failure	422	{object}	httpx.ErrorBody	"reason 缺省或非法（VALIDATION_FAILED）"
//	@Failure	404	{object}	httpx.ErrorBody	"消息不存在或无权访问"
//	@Failure	401	{object}	httpx.ErrorBody	"未登录 / token 失效（AUTH_004）/ 账号被禁用（AUTH_003）"
//	@Router		/messages/{m}/feedback [post]
func (h *ChatHandler) Feedback(c *gin.Context) {
	var req struct {
		Reason  int16  `json:"reason" binding:"required"`
		Comment string `json:"comment"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		httpx.Fail(c, httpx.CodeValidation, "reason 不能为空")
		return
	}
	id, _ := strconv.ParseInt(c.Param("m"), 10, 64)
	if err := h.svc.Feedback(c.Request.Context(), id, middleware.UserID(c), middleware.IsAdmin(c), req.Reason, req.Comment); err != nil {
		httpx.FailServiceError(c, err)
		return
	}
	httpx.OK(c, gin.H{"message": "已记录反馈"})
}

// DeleteFeedback DELETE /messages/{m}/feedback（所属用户）：撤销点踩。
//
//	@Summary	撤销点踩
//	@Description	撤销自己对某条回答的点踩。未点踩时同样返回成功（幂等）。
//	@Description
//	@Description	权限：会话所属用户。
//	@Tags		messages
//	@Produce	json
//	@Success	200	{object}	object{message=string}
//	@Param	m	path	integer	true	"回答消息 ID"
//	@Failure	404	{object}	httpx.ErrorBody	"消息不存在或无权访问"
//	@Failure	401	{object}	httpx.ErrorBody	"未登录 / token 失效（AUTH_004）/ 账号被禁用（AUTH_003）"
//	@Router		/messages/{m}/feedback [delete]
func (h *ChatHandler) DeleteFeedback(c *gin.Context) {
	id, _ := strconv.ParseInt(c.Param("m"), 10, 64)
	if err := h.svc.DeleteFeedback(c.Request.Context(), id, middleware.UserID(c)); err != nil {
		httpx.FailServiceError(c, err)
		return
	}
	httpx.OK(c, gin.H{"message": "已撤销"})
}

// writeSSE 按 SSE 协议写出事件流（API设计.md §4）：
// delta* → citations? → done；异常 error；每 15s 心跳 ping；客户端断开即停止。
func writeSSE(c *gin.Context, events <-chan service.ChatEvent) {
	flusher, ok := c.Writer.(http.Flusher)
	if !ok {
		httpx.Fail(c, "INTERNAL", "当前连接不支持流式响应")
		return
	}
	c.Writer.Header().Set("Content-Type", "text/event-stream")
	c.Writer.Header().Set("Cache-Control", "no-cache")
	c.Writer.Header().Set("Connection", "keep-alive")
	c.Writer.WriteHeader(http.StatusOK)

	// 心跳：每 15s ping（防止代理断连）
	heartbeat := make(chan struct{})
	done := make(chan struct{})
	defer func() { close(done); flusher.Flush() }()
	go func() {
		ticker := time.NewTicker(15 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				select {
				case heartbeat <- struct{}{}:
				case <-done:
					return
				}
			case <-done:
				return
			}
		}
	}()

	for {
		select {
		case <-c.Request.Context().Done():
			return
		case <-heartbeat:
			_, _ = fmt.Fprint(c.Writer, "event: ping\ndata: {}\n\n")
			flusher.Flush()
		case e, ok := <-events:
			if !ok {
				return
			}
			writeEvent(c.Writer, e)
			flusher.Flush()
			if e.Type == "done" || e.Type == "error" {
				return
			}
		}
	}
}

// writeEvent 单帧序列化（event 行 + data 行 + 空行结束）。
func writeEvent(w http.ResponseWriter, e service.ChatEvent) {
	switch e.Type {
	case "delta":
		_, _ = fmt.Fprint(w, "event: delta\ndata: ")
		mustWriteJSON(w, map[string]string{"content": e.Content})
	case "citations":
		_, _ = fmt.Fprint(w, "event: citations\ndata: ")
		mustWriteJSON(w, map[string]any{"items": e.Items})
	case "done":
		_, _ = fmt.Fprint(w, "event: done\ndata: ")
		mustWriteJSON(w, map[string]any{
			"message_id": e.MessageID, "prompt_tokens": e.PromptTokens, "completion_tokens": e.CompletionTokens,
		})
	case "error":
		_, _ = fmt.Fprint(w, "event: error\ndata: ")
		mustWriteJSON(w, map[string]string{"code": e.Code, "message": e.Message})
	}
	_, _ = fmt.Fprint(w, "\n\n")
}

func mustWriteJSON(w http.ResponseWriter, v any) {
	b, _ := json.Marshal(v)
	_, _ = w.Write(b)
}
