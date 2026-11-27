package httpx

// ServiceError 业务错误：Code 与 md/API设计.md §2 一一对应，常量唯一定义于本包 errcode.go。
// service 层构造、handler/middleware 层渲染（FailServiceError / AbortFail），全链路共用。
type ServiceError struct {
	Code    string
	Message string
}

func (e *ServiceError) Error() string { return e.Code + ": " + e.Message }

func NewServiceError(code, message string) *ServiceError {
	return &ServiceError{Code: code, Message: message}
}
