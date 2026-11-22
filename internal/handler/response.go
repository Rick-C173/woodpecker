package handler

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// 统一响应约定（md/API设计.md §1）：
//   成功：直接返回数据对象，或分页 {items, total, page, page_size}
//   失败：{"code", "message", "request_id"}，HTTP 状态码由错误码决定

// AppError 业务错误；service 层返回它，handler 统一渲染。
type AppError struct {
	Code    string
	Message string
}

func (e *AppError) Error() string { return e.Code + ": " + e.Message }

func NewErr(code, message string) *AppError {
	return &AppError{Code: code, Message: message}
}

func StatusOf(code string) int {
	if s, ok := httpStatusByCode[code]; ok {
		return s
	}
	return http.StatusInternalServerError
}

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
func Fail(c *gin.Context, err *AppError) {
	c.JSON(StatusOf(err.Code), gin.H{
		"code":       err.Code,
		"message":    err.Message,
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
