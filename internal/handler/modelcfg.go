package handler

import (
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	"woodpecker/internal/httpx"
	"woodpecker/internal/middleware"
	"woodpecker/internal/model"
	"woodpecker/internal/service"
)

// ModelCfgHandler 模型服务配置端点（FR-12，管理员）。
type ModelCfgHandler struct {
	svc *service.ModelConfigService
}

func NewModelCfgHandler(svc *service.ModelConfigService) *ModelCfgHandler {
	return &ModelCfgHandler{svc: svc}
}

// ListServices GET /admin/model-services：API Key 不回显，仅 has_key。
func (h *ModelCfgHandler) ListServices(c *gin.Context) {
	items, err := h.svc.ListServices(c.Request.Context())
	if err != nil {
		httpx.FailServiceError(c, err)
		return
	}
	httpx.OK(c, gin.H{"items": items})
}

type createModelServiceReq struct {
	Name    string `json:"name" binding:"required"`
	BaseURL string `json:"base_url" binding:"required"`
	APIKey  string `json:"api_key" binding:"required"`
}

// CreateService POST /admin/model-services。
func (h *ModelCfgHandler) CreateService(c *gin.Context) {
	var req createModelServiceReq
	if err := c.ShouldBindJSON(&req); err != nil {
		httpx.Fail(c, httpx.CodeValidation, "name、base_url 与 api_key 不能为空")
		return
	}
	item, err := h.svc.CreateService(c.Request.Context(), middleware.UserID(c), req.Name, req.BaseURL, req.APIKey, c.ClientIP())
	if err != nil {
		httpx.FailServiceError(c, err)
		return
	}
	httpx.Created(c, item)
}

type patchModelServiceReq struct {
	Name    *string `json:"name"`
	BaseURL *string `json:"base_url"`
	APIKey  *string `json:"api_key"` // 缺省=不修改
	Enabled *bool   `json:"enabled"`
}

// UpdateService PATCH /admin/model-services/{id}。
func (h *ModelCfgHandler) UpdateService(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		httpx.FailWith(c, http.StatusNotFound, httpx.CodeNotFound, "模型服务不存在")
		return
	}
	var req patchModelServiceReq
	if err := c.ShouldBindJSON(&req); err != nil {
		httpx.Fail(c, httpx.CodeValidation, "请求体非法")
		return
	}
	item, err := h.svc.UpdateService(c.Request.Context(), middleware.UserID(c), id, req.Name, req.BaseURL, req.APIKey, req.Enabled, c.ClientIP())
	if err != nil {
		httpx.FailServiceError(c, err)
		return
	}
	httpx.OK(c, item)
}

// DeleteService DELETE /admin/model-services/{id}：存在条目 → 409 MODEL_003。
func (h *ModelCfgHandler) DeleteService(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		httpx.FailWith(c, http.StatusNotFound, httpx.CodeNotFound, "模型服务不存在")
		return
	}
	if err := h.svc.DeleteService(c.Request.Context(), middleware.UserID(c), id, c.ClientIP()); err != nil {
		httpx.FailServiceError(c, err)
		return
	}
	httpx.OK(c, gin.H{"message": "已删除"})
}

// TestConnection POST /admin/model-services/{id}/test：
// 响应 {chat: ok/err…, embedding: …, rerank: …}，仅含该服务已配置的用途。
func (h *ModelCfgHandler) TestConnection(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		httpx.FailWith(c, http.StatusNotFound, httpx.CodeNotFound, "模型服务不存在")
		return
	}
	result, err := h.svc.TestConnection(c.Request.Context(), middleware.UserID(c), id, c.ClientIP())
	if err != nil {
		httpx.FailServiceError(c, err)
		return
	}
	httpx.OK(c, result)
}

// ListEntries GET /admin/model-entries?service_id=&purpose=
func (h *ModelCfgHandler) ListEntries(c *gin.Context) {
	serviceID, _ := strconv.ParseInt(c.Query("service_id"), 10, 64)
	var purpose int16
	if p, ok := parsePurpose(c.Query("purpose")); ok {
		purpose = p
	} else if c.Query("purpose") != "" {
		httpx.Fail(c, httpx.CodeValidation, "purpose 取值仅 chat|embedding|rerank")
		return
	}
	items, err := h.svc.ListEntries(c.Request.Context(), serviceID, purpose)
	if err != nil {
		httpx.FailServiceError(c, err)
		return
	}
	httpx.OK(c, gin.H{"items": items})
}

type createModelEntryReq struct {
	ServiceID int64          `json:"service_id" binding:"required"`
	Purpose   string         `json:"purpose" binding:"required"`
	ModelName string         `json:"model_name" binding:"required"`
	Params    map[string]any `json:"params"`
	IsDefault bool           `json:"is_default"`
}

// CreateEntry POST /admin/model-entries。
func (h *ModelCfgHandler) CreateEntry(c *gin.Context) {
	var req createModelEntryReq
	if err := c.ShouldBindJSON(&req); err != nil {
		httpx.Fail(c, httpx.CodeValidation, "service_id、purpose 与 model_name 不能为空")
		return
	}
	item, err := h.svc.CreateEntry(c.Request.Context(), middleware.UserID(c), req.ServiceID, req.Purpose, req.ModelName, marshalParams(req.Params), req.IsDefault, c.ClientIP())
	if err != nil {
		httpx.FailServiceError(c, err)
		return
	}
	httpx.Created(c, item)
}

type patchModelEntryReq struct {
	Purpose   *string        `json:"purpose"`
	ModelName *string        `json:"model_name"`
	Params    map[string]any `json:"params"`
	IsDefault *bool          `json:"is_default"`
}

// UpdateEntry PATCH /admin/model-entries/{id}：is_default=true 切换默认。
func (h *ModelCfgHandler) UpdateEntry(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		httpx.FailWith(c, http.StatusNotFound, httpx.CodeNotFound, "模型条目不存在")
		return
	}
	var req patchModelEntryReq
	if err := c.ShouldBindJSON(&req); err != nil {
		httpx.Fail(c, httpx.CodeValidation, "请求体非法")
		return
	}
	var paramsPtr *string
	if req.Params != nil {
		p := marshalParams(req.Params)
		paramsPtr = &p
	}
	item, err := h.svc.UpdateEntry(c.Request.Context(), middleware.UserID(c), id, req.Purpose, req.ModelName, paramsPtr, req.IsDefault, c.ClientIP())
	if err != nil {
		httpx.FailServiceError(c, err)
		return
	}
	httpx.OK(c, item)
}

// DeleteEntry DELETE /admin/model-entries/{id}：默认条目 → 422 MODEL_002。
func (h *ModelCfgHandler) DeleteEntry(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		httpx.FailWith(c, http.StatusNotFound, httpx.CodeNotFound, "模型条目不存在")
		return
	}
	if err := h.svc.DeleteEntry(c.Request.Context(), middleware.UserID(c), id, c.ClientIP()); err != nil {
		httpx.FailServiceError(c, err)
		return
	}
	httpx.OK(c, gin.H{"message": "已删除"})
}

func parsePurpose(s string) (int16, bool) {
	return model.ParseModelPurpose(s)
}

// marshalParams 把 params 对象序列化为 JSON 字符串（空对象 → "{}"）。
func marshalParams(m map[string]any) string {
	if len(m) == 0 {
		return "{}"
	}
	b, err := json.Marshal(m)
	if err != nil {
		return "{}"
	}
	return string(b)
}
