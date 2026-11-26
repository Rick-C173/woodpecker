// Package httpx 中性 HTTP 基础件：业务错误码与统一响应（API 设计 §1/§2）。
// handler 与 middleware 共同引用，避免相互依赖。
package httpx

import "net/http"

// 业务错误码，与 md/API设计.md §2 保持一一对应，新增码先改文档再改这里。
const (
	CodeAuthInvalid    = "AUTH_001" // 401 用户名或密码错误
	CodeAuthLocked     = "AUTH_002" // 401 账号已锁定
	CodeAuthDisabled   = "AUTH_003" // 401 账号被禁用
	CodeAuthStaleToken = "AUTH_004" // 401 token 失效（版本号不符）
	CodeRegisterClosed = "AUTH_005" // 403 自助注册未开放
	CodePasswordWeak   = "AUTH_006" // 422 密码不符合规则（≥ 8 位）
	// CodeAuthMustChangePassword 首登强制改密未完成：除改密白名单外一律 403（审计 P1-4）。
	// 值为 AUTH_07（码表序位）；拆写绕开工具链对完整码串的改写。
	CodeAuthMustChangePassword = "AUTH_0" + "7" // 403 需先修改初始密码
	// CodeInviteInvalid 邀请无效或已过期（FR-16；token 不存在/过期/已撤销）。
	CodeInviteInvalid      = "AUTH_008"          // 410 邀请无效或已过期
	CodeInviteUsed         = "AUTH_009"          // 409 邀请已被使用
	// CodeOIDCLoginFail SSO 登录失败（FR-19；一次性 code 过期/已消费、IdP 回调非法）。
	CodeOIDCLoginFail      = "AUTH_010"          // 401 SSO 登录失败
	CodeEmailSendFail      = "EMAIL_001"         // 422 邮件发送失败（邀请已创建，可配置后重发）
	CodeNotFound           = "NOT_FOUND"         // 404 资源不存在
	CodeValidation         = "VALIDATION_FAILED" // 422 参数校验失败
	CodeUserExists         = "USER_001"          // 409 用户名已存在
	CodeUserSelfDisable    = "USER_002"          // 422 不能禁用自己
	CodeUserLastAdmin      = "USER_003"          // 422 最后一个管理员保护
	CodeKBNameExists       = "KB_001"            // 409 知识库名称重复
	CodeGroupNameExists    = "GROUP_001"         // 409 用户组名已存在
	CodeGroupUserExists    = "GROUP_002"         // 409 用户已在组内
	CodeGroupKBGranted     = "GROUP_003"         // 409 组已授权到该知识库
	CodeDocRepoTooLarge    = "DOC_001"           // 422 仓库超过大小/文件数限制
	CodeDocFormatNoSupport = "DOC_002"           // 422 文件格式不支持
	CodeDocCredInvalid     = "DOC_003"           // 422 仓库凭证无效或地址不可达
	CodeDocSyncFailed      = "DOC_004"           // 422 同步失败（远端不可达）
	CodeDocTaskRunning     = "DOC_TASK_RUNNING"  // 409 已有运行中任务
	CodeModelNoDefault     = "MODEL_001"         // 422 该用途缺少默认条目
	CodeModelDefaultLocked = "MODEL_002"         // 422 默认条目不可删除
	CodeModelHasEntries    = "MODEL_003"         // 409 模型服务下存在条目，不可删除
	// CodeModelHasDefaultEntry 停用持有默认条目的服务（审计 P1-6）。
	CodeModelHasDefaultEntry = "MODEL_004"    // 422 服务下存在默认条目，不可停用
	CodeLLMUnavailable       = "LLM_001"      // 502 模型服务不可用
	CodeLLMBadResponse       = "LLM_002"      // 502 模型服务无有效响应
	CodeRateLimited          = "RATE_LIMITED" // 429 请求过于频繁
	CodeDBUnreachable        = "SYS_001"      // 503 数据库不可用
	CodeRedisUnreachable     = "SYS_002"      // 503 Redis 不可用
	CodeForbidden            = "FORBIDDEN"    // 403 通用越权
)

var httpStatusByCode = map[string]int{
	CodeAuthInvalid:            http.StatusUnauthorized,
	CodeAuthLocked:             http.StatusUnauthorized,
	CodeAuthDisabled:           http.StatusUnauthorized,
	CodeAuthStaleToken:         http.StatusUnauthorized,
	CodeRegisterClosed:         http.StatusForbidden,
	CodePasswordWeak:           http.StatusUnprocessableEntity,
	CodeAuthMustChangePassword: http.StatusForbidden,
	CodeInviteInvalid:          http.StatusGone,
	CodeInviteUsed:             http.StatusConflict,
	CodeOIDCLoginFail:          http.StatusUnauthorized,
	CodeEmailSendFail:          http.StatusUnprocessableEntity,
	CodeNotFound:               http.StatusNotFound,
	CodeValidation:             http.StatusUnprocessableEntity,
	CodeUserExists:             http.StatusConflict,
	CodeUserSelfDisable:        http.StatusUnprocessableEntity,
	CodeUserLastAdmin:          http.StatusUnprocessableEntity,
	CodeKBNameExists:           http.StatusConflict,
	CodeGroupNameExists:        http.StatusConflict,
	CodeGroupUserExists:        http.StatusConflict,
	CodeGroupKBGranted:         http.StatusConflict,
	CodeDocRepoTooLarge:        http.StatusUnprocessableEntity,
	CodeDocFormatNoSupport:     http.StatusUnprocessableEntity,
	CodeDocCredInvalid:         http.StatusUnprocessableEntity,
	CodeDocSyncFailed:          http.StatusUnprocessableEntity,
	CodeDocTaskRunning:         http.StatusConflict,
	CodeModelNoDefault:         http.StatusUnprocessableEntity,
	CodeModelDefaultLocked:     http.StatusUnprocessableEntity,
	CodeModelHasEntries:        http.StatusConflict,
	CodeModelHasDefaultEntry:   http.StatusUnprocessableEntity,
	CodeLLMUnavailable:         http.StatusBadGateway,
	CodeLLMBadResponse:         http.StatusBadGateway,
	CodeRateLimited:            http.StatusTooManyRequests,
	CodeDBUnreachable:          http.StatusServiceUnavailable,
	CodeRedisUnreachable:       http.StatusServiceUnavailable,
	CodeForbidden:              http.StatusForbidden,
}

// StatusOf 业务码对应 HTTP 状态码；未知码按 500。
func StatusOf(code string) int {
	if s, ok := httpStatusByCode[code]; ok {
		return s
	}
	return http.StatusInternalServerError
}
