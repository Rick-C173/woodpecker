// Package model GORM 数据模型，与 md/数据库设计.md §3 一一对应。
package model

import "time"

const (
	UserStatusActive   int16 = 1
	UserStatusDisabled int16 = 2
)

type User struct {
	ID                 int64      `gorm:"primaryKey;column:id"`
	Username           string     `gorm:"column:username;size:64;uniqueIndex"`
	PasswordHash       string     `gorm:"column:password_hash"`
	IsAdmin            bool       `gorm:"column:is_admin"`
	Status             int16      `gorm:"column:status"`
	MustChangePassword bool       `gorm:"column:must_change_password"`
	TokenVersion       int        `gorm:"column:token_version"`
	FailedAttempts     int        `gorm:"column:failed_attempts"`
	LockedUntil        *time.Time `gorm:"column:locked_until"`
	Email              *string    `gorm:"column:email;size:255"` // FR-16：登录可用户名或邮箱；存量账号为空
	CreatedAt          time.Time  `gorm:"column:created_at"`
	UpdatedAt          time.Time  `gorm:"column:updated_at"`
}

func (User) TableName() string { return "users" }

// Invite 邀请（FR-16）：每邮箱一条记录，重复邀请覆盖 token（重发）。
// token 只存 sha256 hex（URL 明文仅出现在邮件与创建响应）。
type Invite struct {
	ID          int64      `gorm:"primaryKey;column:id"`
	Email       string     `gorm:"column:email;size:255;uniqueIndex"`
	TokenHash   string     `gorm:"column:token_hash;size:64;uniqueIndex"`
	Status      int16      `gorm:"column:status"`       // 1=待接受 2=已接受 3=已撤销
	ExpiresAt   time.Time  `gorm:"column:expires_at"`
	AcceptedAt  *time.Time `gorm:"column:accepted_at"`
	AcceptedBy  *int64     `gorm:"column:accepted_by"`
	SendStatus  int16      `gorm:"column:send_status"` // 1=已发送 2=发送失败 3=未发送
	SendError   *string    `gorm:"column:send_error"`
	SendCount   int        `gorm:"column:send_count"`
	CreatedBy   int64      `gorm:"column:created_by"`
	CreatedAt   time.Time  `gorm:"column:created_at"`
	UpdatedAt   time.Time  `gorm:"column:updated_at"`
}

func (Invite) TableName() string { return "invites" }

// 邀请状态与发送状态（FR-16）。
const (
	InviteStatusPending  int16 = 1
	InviteStatusAccepted int16 = 2
	InviteStatusRevoked  int16 = 3

	InviteSentFailed     int16 = 2
	InviteSentNotSent    int16 = 3
)

type AuditLog struct {
	ID         int64     `gorm:"primaryKey;column:id"`
	UserID     *int64    `gorm:"column:user_id"`
	Action     string    `gorm:"column:action;size:50"`
	ObjectType string    `gorm:"column:object_type;size:30"`
	ObjectID   *int64    `gorm:"column:object_id"`
	Detail     []byte    `gorm:"column:detail;type:jsonb"`
	IP         string    `gorm:"column:ip;size:45"`
	CreatedAt  time.Time `gorm:"column:created_at"`
}

func (AuditLog) TableName() string { return "audit_logs" }

type Setting struct {
	Key       string    `gorm:"primaryKey;column:key;size:50"`
	Value     []byte    `gorm:"column:value;type:jsonb"`
	UpdatedAt time.Time `gorm:"column:updated_at"`
	UpdatedBy *int64    `gorm:"column:updated_by"`
}

func (Setting) TableName() string { return "settings" }
