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
	CreatedAt          time.Time  `gorm:"column:created_at"`
	UpdatedAt          time.Time  `gorm:"column:updated_at"`
}

func (User) TableName() string { return "users" }

type AuditLog struct {
	ID         int64          `gorm:"primaryKey;column:id"`
	UserID     *int64         `gorm:"column:user_id"`
	Action     string         `gorm:"column:action;size:50"`
	ObjectType string         `gorm:"column:object_type;size:30"`
	ObjectID   *int64         `gorm:"column:object_id"`
	Detail     []byte         `gorm:"column:detail;type:jsonb"`
	IP         string         `gorm:"column:ip;size:45"`
	CreatedAt  time.Time      `gorm:"column:created_at"`
}

func (AuditLog) TableName() string { return "audit_logs" }

type Setting struct {
	Key       string    `gorm:"primaryKey;column:key;size:50"`
	Value     []byte    `gorm:"column:value;type:jsonb"`
	UpdatedAt time.Time `gorm:"column:updated_at"`
	UpdatedBy *int64    `gorm:"column:updated_by"`
}

func (Setting) TableName() string { return "settings" }
