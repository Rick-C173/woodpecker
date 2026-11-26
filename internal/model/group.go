package model

import "time"

// UserGroup 用户组（FR-18）：按部门/项目组整组授权。
type UserGroup struct {
	ID          int64     `gorm:"primaryKey;column:id"`
	Name        string    `gorm:"column:name;size:50"`
	Description string    `gorm:"column:description;size:500"`
	CreatedBy   int64     `gorm:"column:created_by"`
	CreatedAt   time.Time `gorm:"column:created_at"`
	UpdatedAt   time.Time `gorm:"column:updated_at"`
}

func (UserGroup) TableName() string { return "user_groups" }

// GroupMember 组成员（组删除/用户删除级联清除）。
type GroupMember struct {
	ID        int64     `gorm:"primaryKey;column:id"`
	GroupID   int64     `gorm:"column:group_id"`
	UserID    int64     `gorm:"column:user_id"`
	CreatedAt time.Time `gorm:"column:created_at"`
}

func (GroupMember) TableName() string { return "group_members" }

// KBGroupRole 组 → 库的角色授权（role 与 KBMember.Role 同枚举）。
type KBGroupRole struct {
	ID        int64     `gorm:"primaryKey;column:id"`
	KBID      int64     `gorm:"column:kb_id"`
	GroupID   int64     `gorm:"column:group_id"`
	Role      int16     `gorm:"column:role"`
	CreatedAt time.Time `gorm:"column:created_at"`
}

func (KBGroupRole) TableName() string { return "kb_group_roles" }
