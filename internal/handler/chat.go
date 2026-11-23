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
//	@Tags		conversations
//	@Produce	json
//	@Success	200	{object}	object{items=[]service.ConversationItem}
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
//	@Tags		conversations
//	@Produce	json
//	@Success	201	{object}	service.ConversationItem
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
//	@Tags		conversations
//	@Produce	json
//	@Success	200	{object}	service.ConversationDetail
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
//	@Tags		conversations
//	@Produce	json
//	@Success	200	{object}	object{message=string}
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
//	@Tags		conversations
//	@Produce	json
//	@Success	200	{object}	object{message=string}
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
//	@Tags		conversations
//	@Accept		json
//	@Produce	text/event-stream
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
//	@Tags		messages
//	@Produce	text/event-stream
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
//	@Tags		messages
//	@Accept		json
//	@Produce	json
//	@Success	200	{object}	object{message=string}
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
//	@Tags		messages
//	@Produce	json
//	@Success	200	{object}	object{message=string}
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
