package handler

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	"woodpecker/internal/httpx"
	"woodpecker/internal/middleware"
	"woodpecker/internal/service"
)

// KBHandler 知识库与成员管理端点（FR-02 / R6）。
type KBHandler struct {
	svc *service.KBService
}

func NewKBHandler(svc *service.KBService) *KBHandler { return &KBHandler{svc: svc} }

// List GET /kbs：管理员=全部，普通用户=被授权库；均含 my_role。
func (h *KBHandler) List(c *gin.Context) {
	items, err := h.svc.List(c.Request.Context(), middleware.UserID(c), middleware.IsAdmin(c))
	if err != nil {
		httpx.FailServiceError(c, err)
		return
	}
	httpx.OK(c, gin.H{"items": items})
}

type createKBReq struct {
	Name        string `json:"name" binding:"required"`
	Description string `json:"description"`
}

// Create POST /kbs（管理员）：名称必填 ≤50 字符，重名 → KB_001。
func (h *KBHandler) Create(c *gin.Context) {
	var req createKBReq
	if err := c.ShouldBindJSON(&req); err != nil {
		httpx.Fail(c, httpx.CodeValidation, "知识库名称不能为空")
		return
	}
	item, err := h.svc.Create(c.Request.Context(), middleware.UserID(c), req.Name, req.Description, c.ClientIP())
	if err != nil {
		httpx.FailServiceError(c, err)
		return
	}
	httpx.Created(c, item)
}

// Get GET /kbs/{kb}（成员可见；非成员 404，由 KBAccess 拦截）。
func (h *KBHandler) Get(c *gin.Context) {
	item, err := h.svc.Get(c.Request.Context(), middleware.KBID(c), middleware.UserID(c), middleware.IsAdmin(c))
	if err != nil {
		httpx.FailServiceError(c, err)
		return
	}
	httpx.OK(c, item)
}

type patchKBReq struct {
	Name        *string `json:"name"`
	Description *string `json:"description"`
}

// Update PATCH /kbs/{kb}（管理员）：字段缺省表示不修改。
func (h *KBHandler) Update(c *gin.Context) {
	var req patchKBReq
	if err := c.ShouldBindJSON(&req); err != nil {
		httpx.Fail(c, httpx.CodeValidation, "请求体非法")
		return
	}
	item, err := h.svc.Update(c.Request.Context(), middleware.UserID(c), middleware.KBID(c), req.Name, req.Description, c.ClientIP())
	if err != nil {
		httpx.FailServiceError(c, err)
		return
	}
	httpx.OK(c, item)
}

// Delete DELETE /kbs/{kb}（管理员）：级联删除文档/分块/会话，不可恢复。
func (h *KBHandler) Delete(c *gin.Context) {
	if err := h.svc.Delete(c.Request.Context(), middleware.UserID(c), middleware.KBID(c), c.ClientIP()); err != nil {
		httpx.FailServiceError(c, err)
		return
	}
	httpx.OK(c, gin.H{"message": "已删除"})
}

// ListMembers GET /kbs/{kb}/members（管理员）。
func (h *KBHandler) ListMembers(c *gin.Context) {
	items, err := h.svc.ListMembers(c.Request.Context(), middleware.KBID(c))
	if err != nil {
		httpx.FailServiceError(c, err)
		return
	}
	httpx.OK(c, gin.H{"items": items})
}

type addMemberReq struct {
	UserID int64  `json:"user_id" binding:"required"`
	Role   string `json:"role" binding:"required"`
}

// AddMember POST /kbs/{kb}/members（管理员）。
func (h *KBHandler) AddMember(c *gin.Context) {
	var req addMemberReq
	if err := c.ShouldBindJSON(&req); err != nil {
		httpx.Fail(c, httpx.CodeValidation, "user_id 与 role 不能为空")
		return
	}
	item, err := h.svc.AddMember(c.Request.Context(), middleware.UserID(c), middleware.KBID(c), req.UserID, req.Role, c.ClientIP())
	if err != nil {
		httpx.FailServiceError(c, err)
		return
	}
	httpx.Created(c, item)
}

type patchMemberReq struct {
	Role string `json:"role" binding:"required"`
}

// UpdateMemberRole PATCH /kbs/{kb}/members/{user}（管理员）。
func (h *KBHandler) UpdateMemberRole(c *gin.Context) {
	userID, err := strconv.ParseInt(c.Param("user"), 10, 64)
	if err != nil || userID <= 0 {
		httpx.FailWith(c, http.StatusNotFound, httpx.CodeNotFound, "成员不存在")
		return
	}
	var req patchMemberReq
	if err := c.ShouldBindJSON(&req); err != nil {
		httpx.Fail(c, httpx.CodeValidation, "role 不能为空")
		return
	}
	if err := h.svc.UpdateMemberRole(c.Request.Context(), middleware.UserID(c), middleware.KBID(c), userID, req.Role, c.ClientIP()); err != nil {
		httpx.FailServiceError(c, err)
		return
	}
	httpx.OK(c, gin.H{"message": "已更新"})
}

// RemoveMember DELETE /kbs/{kb}/members/{user}（管理员）。
func (h *KBHandler) RemoveMember(c *gin.Context) {
	userID, err := strconv.ParseInt(c.Param("user"), 10, 64)
	if err != nil || userID <= 0 {
		httpx.FailWith(c, http.StatusNotFound, httpx.CodeNotFound, "成员不存在")
		return
	}
	if err := h.svc.RemoveMember(c.Request.Context(), middleware.UserID(c), middleware.KBID(c), userID, c.ClientIP()); err != nil {
		httpx.FailServiceError(c, err)
		return
	}
	httpx.OK(c, gin.H{"message": "已移除"})
}
