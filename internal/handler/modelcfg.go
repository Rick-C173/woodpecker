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
//
//	@Summary	模型服务列表（管理员）
//	@Description	API Key 永不回显，仅返回 has_key 标记。
//	@Tags		model-config
//	@Produce	json
//	@Success	200	{object}	object{items=[]service.ModelServiceItem}
//	@Failure	403	{object}	object{code=string,message=string,request_id=string}
//	@Router		/admin/model-services [get]
//	@Security	BearerAuth
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
//
//	@Summary	新建模型服务（管理员）
//	@Description	base_url 为 OpenAI 兼容端点前缀；api_key 以 AES-GCM 加密落库。
//	@Tags		model-config
//	@Accept		json
//	@Produce	json
//	@Param		body	body		object{name=string,base_url=string,api_key=string}	true	"服务名称、端点与密钥"
//	@Success	201		{object}	service.ModelServiceItem
//	@Failure	422		{object}	object{code=string,message=string,request_id=string}
//	@Router		/admin/model-services [post]
//	@Security	BearerAuth
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
//
//	@Summary	修改模型服务（管理员）
//	@Description	api_key 缺省表示不修改；停用仍持有默认条目的服务 → 422 MODEL_004（审计 P1-6）。
//	@Tags		model-config
//	@Accept		json
//	@Produce	json
//	@Param		id		path		int															true	"服务 ID"
//	@Param		body	body		object{name=string,base_url=string,api_key=string,enabled=bool}	true	"待修改字段（均可缺省）"
//	@Success	200		{object}	service.ModelServiceItem
//	@Failure	404		{object}	object{code=string,message=string,request_id=string}
//	@Failure	422		{object}	object{code=string,message=string,request_id=string}
//	@Router		/admin/model-services/{id} [patch]
//	@Security	BearerAuth
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
//
//	@Summary	删除模型服务（管理员）
//	@Description	服务下仍有条目 → 409 MODEL_003（先删条目）。
//	@Tags		model-config
//	@Produce	json
//	@Param		id	path		int	true	"服务 ID"
//	@Success	200	{object}	object{message=string}
//	@Failure	404	{object}	object{code=string,message=string,request_id=string}
//	@Failure	409	{object}	object{code=string,message=string,request_id=string}
//	@Router		/admin/model-services/{id} [delete]
//	@Security	BearerAuth
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
//
//	@Summary	模型服务连通性测试（管理员）
//	@Description	逐个用途发一次最小请求；响应仅包含该服务已配置用途，值为 "ok" 或 "err: <原因>"。
//	@Tags		model-config
//	@Produce	json
//	@Param		id	path		int	true	"服务 ID"
//	@Success	200	{object}	object{chat=string,embedding=string,rerank=string}
//	@Failure	404	{object}	object{code=string,message=string,request_id=string}
//	@Router		/admin/model-services/{id}/test [post]
//	@Security	BearerAuth
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
//
//	@Summary	模型条目列表（管理员）
//	@Description	purpose 取值 chat|embedding|rerank，缺省表示全部。
//	@Tags		model-config
//	@Produce	json
//	@Param		service_id	query		int		false	"按服务筛选"
//	@Param		purpose		query		string	false	"按用途筛选：chat|embedding|rerank"
//	@Success	200			{object}	object{items=[]service.ModelEntryItem}
//	@Failure	422			{object}	object{code=string,message=string,request_id=string}
//	@Router		/admin/model-entries [get]
//	@Security	BearerAuth
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
//
//	@Summary	新建模型条目（管理员）
//	@Description	params 为模型参数（temperature/top_p/dimensions/top_n 等），向量化维度需与 vector(1024) 一致或显式声明 dimensions。
//	@Tags		model-config
//	@Accept		json
//	@Produce	json
//	@Param		body	body		object{service_id=int,purpose=string,model_name=string,params=object,is_default=bool}	true	"条目定义"
//	@Success	201		{object}	service.ModelEntryItem
//	@Failure	422		{object}	object{code=string,message=string,request_id=string}
//	@Router		/admin/model-entries [post]
//	@Security	BearerAuth
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
//
//	@Summary	修改模型条目（管理员）
//	@Description	is_default=true 时切换该用途的默认条目（同用途仅一条默认）。
//	@Tags		model-config
//	@Accept		json
//	@Produce	json
//	@Param		id		path		int															true	"条目 ID"
//	@Param		body	body		object{purpose=string,model_name=string,params=object,is_default=bool}	true	"待修改字段（均可缺省）"
//	@Success	200		{object}	service.ModelEntryItem
//	@Failure	404		{object}	object{code=string,message=string,request_id=string}
//	@Failure	422		{object}	object{code=string,message=string,request_id=string}
//	@Router		/admin/model-entries/{id} [patch]
//	@Security	BearerAuth
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
//
//	@Summary	删除模型条目（管理员）
//	@Description	默认条目不可删 → 422 MODEL_002（先切换默认）。
//	@Tags		model-config
//	@Produce	json
//	@Param		id	path		int	true	"条目 ID"
//	@Success	200	{object}	object{message=string}
//	@Failure	404	{object}	object{code=string,message=string,request_id=string}
//	@Failure	422	{object}	object{code=string,message=string,request_id=string}
//	@Router		/admin/model-entries/{id} [delete]
//	@Security	BearerAuth
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
