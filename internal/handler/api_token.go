package handler

import (
	"context"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	"woodpecker/internal/httpx"
	"woodpecker/internal/middleware"
	"woodpecker/internal/service"
)

// TokenValidator API Token 校验所需（service.APITokenService 满足）。
type TokenValidator interface {
	Validate(ctx context.Context, bearer string) (int64, error)
}

// APITokenHandler 个人 API Token（M15 / FR-20）：创建/列表/吊销（仅本人）。
type APITokenHandler struct {
	svc *service.APITokenService
}

func NewAPITokenHandler(svc *service.APITokenService) *APITokenHandler {
	return &APITokenHandler{svc: svc}
}

type createTokenReq struct {
	Name          string `json:"name"          binding:"required"`
	ExpiresInDays *int   `json:"expires_in_days"` // 可空=永久；1~3650
}

// List GET /tokens：当前用户 token 列表（含已吊销；不含哈希，明文永不回显）。
//
//	@Summary	我的 API Token 列表
//	@Description	按创建时间倒序；`prefix` 展示时拼 `wpk_` 前缀。
//	@Tags		tokens
//	@Produce	json
//	@Security	BearerAuth
//	@Success	200	{object}	object{items=[]service.APITokenItem}
//	@Router		/tokens [get]
func (h *APITokenHandler) List(c *gin.Context) {
	items, err := h.svc.List(c.Request.Context(), middleware.UserID(c))
	if err != nil {
		httpx.FailServiceError(c, err)
		return
	}
	httpx.OK(c, gin.H{"items": items})
}

// Create POST /tokens：创建 token，明文仅本次响应返回。
//
//	@Summary	创建 API Token
//	@Description	明文 `wpk_<64hex>` 仅此一次返回；每人上限 20 个有效 Token（TOK_001）。
//	@Tags		tokens
//	@Accept		json
//	@Produce	json
//	@Security	BearerAuth
//	@Param		body	body	createTokenReq	true	"名称 + 可选有效天数（1~3650，缺省永久）"
//	@Success	201	{object}	service.CreatedToken
//	@Failure	422	{object}	httpx.ErrorBody	"TOK_001 超上限 / VALIDATION_FAILED 参数非法"
//	@Router		/tokens [post]
func (h *APITokenHandler) Create(c *gin.Context) {
	var req createTokenReq
	if err := c.ShouldBindJSON(&req); err != nil {
		httpx.FailWith(c, http.StatusUnprocessableEntity, httpx.CodeValidation, "参数非法："+err.Error())
		return
	}
	ct, err := h.svc.Create(c.Request.Context(), middleware.UserID(c), req.Name, req.ExpiresInDays, c.ClientIP())
	if err != nil {
		httpx.FailServiceError(c, err)
		return
	}
	httpx.Created(c, ct)
}

// Revoke DELETE /tokens/:id：吊销自己的 token（他人与不存在统一 404）。
//
//	@Summary	吊销 API Token
//	@Description	软吊销（revoked_at 置时间）；仅本人。
//	@Tags		tokens
//	@Produce	json
//	@Security	BearerAuth
//	@Param		id	path	integer	true	"token ID"
//	@Success	200	{object}	object{revoked=bool}
//	@Failure	404	{object}	httpx.ErrorBody
//	@Router		/tokens/{id} [delete]
func (h *APITokenHandler) Revoke(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		httpx.FailWith(c, http.StatusNotFound, httpx.CodeNotFound, "资源不存在")
		return
	}
	if err := h.svc.Revoke(c.Request.Context(), middleware.UserID(c), id, c.ClientIP()); err != nil {
		httpx.FailServiceError(c, err)
		return
	}
	httpx.OK(c, gin.H{"revoked": true})
}
