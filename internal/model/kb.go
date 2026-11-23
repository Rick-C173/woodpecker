package model

import "time"

// 库内角色（PRD R6）：层级 管理者 > 编辑者 > 成员，数值越小权限越高。
const (
	KBRoleOwner  int16 = 1
	KBRoleEditor int16 = 2
	KBRoleMember int16 = 3
)

// KBRoleName 角色对外名称（API `my_role` 字段）。
func KBRoleName(role int16) string {
	switch role {
	case KBRoleOwner:
		return "owner"
	case KBRoleEditor:
		return "editor"
	case KBRoleMember:
		return "member"
	default:
		return ""
	}
}

// ParseKBRole 解析 API 传入的角色字符串。
func ParseKBRole(s string) (int16, bool) {
	switch s {
	case "owner":
		return KBRoleOwner, true
	case "editor":
		return KBRoleEditor, true
	case "member":
		return KBRoleMember, true
	default:
		return 0, false
	}
}

type KnowledgeBase struct {
	ID          int64     `gorm:"primaryKey;column:id"`
	Name        string    `gorm:"column:name;size:50"`
	Description string    `gorm:"column:description;size:500"`
	CreatedBy   int64     `gorm:"column:created_by"`
	CreatedAt   time.Time `gorm:"column:created_at"`
	UpdatedAt   time.Time `gorm:"column:updated_at"`
}

func (KnowledgeBase) TableName() string { return "knowledge_bases" }

type KBMember struct {
	ID        int64     `gorm:"primaryKey;column:id"`
	KBID      int64     `gorm:"column:kb_id"`
	UserID    int64     `gorm:"column:user_id"`
	Role      int16     `gorm:"column:role"`
	CreatedAt time.Time `gorm:"column:created_at"`
}

func (KBMember) TableName() string { return "kb_members" }
