package model

import "time"

// 个人 API Token（M15 / FR-20）：机器访问凭证（脚本/CI/CLI），MCP Server 前置依赖。
// 库中仅存 SHA-256 哈希；明文 wpk_<64hex> 仅创建响应返回一次；
// 权限=所属用户本人权限（禁用即时失效）；不受 users.token_version 影响（改密不使机器凭证失效）。
type APIToken struct {
	ID         int64      `gorm:"primaryKey;column:id"`
	UserID     int64      `gorm:"column:user_id"`
	Name       string     `gorm:"column:name;size:100"`
	TokenHash  string     `gorm:"column:token_hash;size:64"`
	Prefix     string     `gorm:"column:prefix;size:8"` // 64hex 前 8 字符（展示时拼 wpk_ 前缀）
	LastUsedAt *time.Time `gorm:"column:last_used_at"`
	ExpiresAt  *time.Time `gorm:"column:expires_at"` // NULL=永久
	CreatedAt  time.Time  `gorm:"column:created_at"`
	RevokedAt  *time.Time `gorm:"column:revoked_at"` // NULL=有效；软吊销
}

func (APIToken) TableName() string { return "api_tokens" }
