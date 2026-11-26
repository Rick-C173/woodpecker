package service

import "woodpecker/internal/httpx"

// 业务错误类型与错误码的包内绑定（唯一定义在 internal/httpx）。
//
// 历史上 service 不 import httpx（httpx.FailServiceError 反向依赖本包的 ServiceError），
// 错误码以字符串副本散落本包各文件、靠注释「保持同步」——2026-11-26 ServiceError 挪入
// httpx 后依赖方向为 service → httpx，本文件以 type/const/var 别名绑定唯一定义：
// 字面量全局仅存于 httpx/errcode.go，新增/改名/调整 HTTP 状态码只改那一处。
// 历史短名（CodeDocUnsupported/CodeModelDefaultDel 等）已废弃，统一用 httpx 命名。
type ServiceError = httpx.ServiceError

// NewServiceError 构造业务错误（转发 httpx.NewServiceError）。
var NewServiceError = httpx.NewServiceError

const (
	// 认证与会话（API设计.md §2 AUTH_0xx）
	CodeAuthInvalid    = httpx.CodeAuthInvalid    // 401 用户名或密码错误
	CodeAuthLocked     = httpx.CodeAuthLocked     // 401 账号已锁定
	CodeAuthDisabled   = httpx.CodeAuthDisabled   // 401 账号被禁用
	CodeAuthStaleToken = httpx.CodeAuthStaleToken // 401 token 失效（版本号不符）
	CodeRegisterClosed = httpx.CodeRegisterClosed // 403 自助注册未开放
	CodePasswordWeak   = httpx.CodePasswordWeak   // 422 密码不符合规则
	CodeInviteInvalid  = httpx.CodeInviteInvalid  // 410 邀请无效或已过期
	CodeInviteUsed     = httpx.CodeInviteUsed     // 409 邀请已被使用
	CodeOIDCLoginFail  = httpx.CodeOIDCLoginFail  // 401 SSO 登录失败
	CodeEmailSendFail  = httpx.CodeEmailSendFail  // 422 邮件发送失败

	// 用户与权限
	CodeUserExists      = httpx.CodeUserExists      // 409 用户名已存在
	CodeUserSelfDisable = httpx.CodeUserSelfDisable // 422 不能禁用自己
	CodeUserLastAdmin   = httpx.CodeUserLastAdmin   // 422 最后一个管理员保护
	CodeKBNameExists    = httpx.CodeKBNameExists    // 409 知识库名称重复
	CodeForbidden       = httpx.CodeForbidden       // 403 通用越权

	// 用户组（FR-18）
	CodeGroupNameExists = httpx.CodeGroupNameExists // 409 用户组名已存在
	CodeGroupUserExists = httpx.CodeGroupUserExists // 409 用户已在组内
	CodeGroupKBGranted  = httpx.CodeGroupKBGranted  // 409 组已授权到该库

	// 文档与摄取（DOC_0xx）
	CodeDocRepoTooLarge = httpx.CodeDocRepoTooLarge // 422 仓库超过大小/文件数限制
	CodeDocSyncFailed   = httpx.CodeDocSyncFailed   // 422 同步失败
	CodeDocWebhookAuth  = httpx.CodeDocWebhookAuth  // 401 webhook 验签 token 无效
	CodeDocTaskRunning  = httpx.CodeDocTaskRunning  // 409 已有运行中任务

	// 模型配置（MODEL_0xx）
	CodeModelNoDefault  = httpx.CodeModelNoDefault  // 422 该用途缺少默认条目
	CodeModelHasEntries = httpx.CodeModelHasEntries // 409 模型服务下存在条目

	// 通用
	CodeNotFound   = httpx.CodeNotFound   // 404 资源不存在
	CodeValidation = httpx.CodeValidation // 422 参数校验失败

	// 个人 API Token（M15 / FR-20）
	CodeTokenLimit   = httpx.CodeTokenLimit   // 422 数量超出上限
	CodeTokenInvalid = httpx.CodeTokenInvalid // 401 无效或已吊销/过期
)
