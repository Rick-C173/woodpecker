package model

import "time"

// UserGroup 用户组（FR-18）：按部门/项目组整组授权。
// Auto（FR-19）：true=OIDC 组 claim 同步的自动组（登录时按 claim 双向对齐）；false=手动组不参与同步。
// CreatedBy：创建者用户 ID；0=系统自动创建（OIDC 组同步，无 users 外键）。
type UserGroup struct {
	ID          int64     `gorm:"primaryKey;column:id"`
	Name        string    `gorm:"column:name;size:50"`
	Description string    `gorm:"column:description;size:500"`
	Auto        bool      `gorm:"column:auto"`
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
