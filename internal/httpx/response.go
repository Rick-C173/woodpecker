package httpx

import (
	"errors"
	"log/slog"
	"net/http"

	"github.com/gin-gonic/gin"

	"woodpecker/internal/service"
)

// 统一响应约定（md/API设计.md §1）：
//   成功：直接返回数据对象，或分页 {items, total, page, page_size}
//   失败：{"code", "message", "request_id"}，HTTP 状态码由错误码决定

// OK 返回数据对象。
func OK(c *gin.Context, data any) {
	c.JSON(http.StatusOK, data)
}

// Created 返回 201 数据对象。
func Created(c *gin.Context, data any) {
	c.JSON(http.StatusCreated, data)
}

// Accepted 返回 202（任务受理）。
func Accepted(c *gin.Context, data any) {
	c.JSON(http.StatusAccepted, data)
}

// PageOK 返回分页信封。
func PageOK(c *gin.Context, items any, total int64, page, pageSize int) {
	c.JSON(http.StatusOK, gin.H{
		"items":     items,
		"total":     total,
		"page":      page,
		"page_size": pageSize,
	})
}

// Fail 按业务错误码渲染错误响应。
func Fail(c *gin.Context, code, message string) {
	c.JSON(StatusOf(code), gin.H{
		"code":       code,
		"message":    message,
		"request_id": c.GetString("request_id"),
	})
}

// FailWith 渲染指定状态码的错误响应（兜底场景，如 404 不可见原则）。
func FailWith(c *gin.Context, status int, code, message string) {
	c.JSON(status, gin.H{
		"code":       code,
		"message":    message,
		"request_id": c.GetString("request_id"),
	})
}

// FailServiceError 渲染 service.ServiceError；其他错误按 500。
func FailServiceError(c *gin.Context, err error) {
	var se *service.ServiceError
	if errors.As(err, &se) {
		Fail(c, se.Code, se.Message)
		return
	}
	// 非 ServiceError 的未知错误必须留痕，否则 500 无从排查（端到端验证教训）。
	slog.ErrorContext(c.Request.Context(), "handler 非业务错误",
		"err", err, "method", c.Request.Method, "path", c.Request.URL.Path,
		"request_id", c.GetString("request_id"))
	Fail(c, "INTERNAL", "服务内部错误")
}

// AbortFail 渲染错误并终止后续 handler（中间件拦截场景）。
func AbortFail(c *gin.Context, code, message string) {
	Fail(c, code, message)
	c.Abort()
}

// AbortFailWith 渲染指定状态码错误并终止后续 handler（中间件兜底场景）。
func AbortFailWith(c *gin.Context, status int, code, message string) {
	FailWith(c, status, code, message)
	c.Abort()
}

// ErrorBody 统一错误信封（仅用于接口文档标注：@Failure {object} httpx.ErrorBody）。
type ErrorBody struct {
	Code      string `json:"code" example:"KB_001"`
	Message   string `json:"message" example:"知识库名称已存在"`
	RequestID string `json:"request_id" example:"req_01HXXXXXXX"`
}
