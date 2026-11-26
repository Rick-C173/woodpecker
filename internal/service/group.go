package service

import (
	"context"
	"errors"
	"strings"
	"time"
	"unicode/utf8"

	"woodpecker/internal/model"
	"woodpecker/internal/repo"
)

// 组参数（FR-18）。
const (
	groupNameMax = 50
	groupDescMax = 500
)

// GroupStore 组存储接口（repo.GroupRepo 满足；测试用 fake 替换）。
type GroupStore interface {
	Create(ctx context.Context, g *model.UserGroup) error
	Save(ctx context.Context, g *model.UserGroup) error
	GetByID(ctx context.Context, id int64) (*model.UserGroup, error)
	GetByName(ctx context.Context, name string) (*model.UserGroup, error)
	Delete(ctx context.Context, id int64) error
	List(ctx context.Context) ([]repo.GroupRow, error)
	AddMember(ctx context.Context, groupID, userID int64) error
	RemoveMember(ctx context.Context, groupID, userID int64) error
	ListMembers(ctx context.Context, groupID int64) ([]repo.GroupMemberRow, error)
	GetMemberByID(ctx context.Context, groupID, userID int64) (*model.GroupMember, error)
	Grant(ctx context.Context, g *model.KBGroupRole) error
	UpdateGrant(ctx context.Context, kbID, groupID int64, role int16) error
	RevokeGrant(ctx context.Context, kbID, groupID int64) error
	ListGrantsByKB(ctx context.Context, kbID int64) ([]repo.KBGroupRoleRow, error)
	GrantByGroupAndKB(ctx context.Context, kbID, groupID int64) (*model.KBGroupRole, error)
	GetEffectiveRole(ctx context.Context, kbID, userID int64) (int16, error)
}

// GroupUserLookup 用户存在性（repo.UserRepo 满足）。
type GroupUserLookup interface {
	UserExists(ctx context.Context, userID int64) (bool, error)
}

// GroupItem 组列表/详情项。
type GroupItem struct {
	ID          int64  `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	MemberCount int64  `json:"member_count"`
	KBCount     int64  `json:"kb_count"`
	CreatedAt   string `json:"created_at"`
	UpdatedAt   string `json:"updated_at"`
}

// GroupMemberItem 组成员项。
type GroupMemberItem struct {
	UserID    int64  `json:"user_id"`
	Username  string `json:"username"`
	CreatedAt string `json:"created_at"`
}

// KBGroupRoleItem 库的组授权项。
type KBGroupRoleItem struct {
	GroupID   int64  `json:"group_id"`
	Name      string `json:"name"`
	Role      string `json:"role"` // owner|editor|member
	CreatedAt string `json:"created_at"`
}

// GroupService 用户组与组授权（FR-18）：组管理（管理员）+ 库组授权（管理员/owner）。
// 生效判定在 repo.GetEffectiveRole（实时查询，无缓存）。
type GroupService struct {
	groups GroupStore
	users  GroupUserLookup
	kbs    KBRepo
	audit  *AuditService
	now    func() time.Time
}

func NewGroupService(groups GroupStore, users GroupUserLookup, kbs KBRepo, audit *AuditService) *GroupService {
	return &GroupService{groups: groups, users: users, kbs: kbs, audit: audit, now: time.Now}
}

// SetClock 注入时钟（测试用）。
func (s *GroupService) SetClock(now func() time.Time) { s.now = now }

func groupItem(r repo.GroupRow) GroupItem {
	return GroupItem{
		ID:          r.ID,
		Name:        r.Name,
		Description: r.Description,
		MemberCount: r.MemberCount,
		KBCount:     r.KBCount,
		CreatedAt:   r.CreatedAt.UTC().Format(time.RFC3339),
		UpdatedAt:   r.UpdatedAt.UTC().Format(time.RFC3339),
	}
}

// List 组列表（管理员）。
func (s *GroupService) List(ctx context.Context) ([]GroupItem, error) {
	rows, err := s.groups.List(ctx)
	if err != nil {
		return nil, err
	}
	items := make([]GroupItem, 0, len(rows))
	for _, r := range rows {
		items = append(items, groupItem(r))
	}
	return items, nil
}

// Create 建组（管理员）：名称唯一 ≤50 字符，重名 → GROUP_001。
func (s *GroupService) Create(ctx context.Context, actorID int64, name, description, ip string) (*GroupItem, error) {
	name = strings.TrimSpace(name)
	description = strings.TrimSpace(description)
	if name == "" || utf8.RuneCountInString(name) > groupNameMax {
		return nil, NewServiceError(CodeValidation, "组名必填且不超过 50 字符")
	}
	if utf8.RuneCountInString(description) > groupDescMax {
		return nil, NewServiceError(CodeValidation, "描述不能超过 500 字符")
	}
	if _, err := s.groups.GetByName(ctx, name); err == nil {
		return nil, NewServiceError(CodeGroupNameExists, "用户组名已存在")
	} else if !errors.Is(err, repo.ErrNotFound) {
		return nil, err
	}
	g := &model.UserGroup{Name: name, Description: description, CreatedBy: actorID}
	if err := s.groups.Create(ctx, g); err != nil {
		return nil, err
	}
	s.audit.Record(ctx, &actorID, "group.create", "group", &g.ID, map[string]any{"name": name}, ip)
	return &GroupItem{
		ID:          g.ID,
		Name:        g.Name,
		Description: g.Description,
		CreatedAt:   g.CreatedAt.UTC().Format(time.RFC3339),
		UpdatedAt:   g.UpdatedAt.UTC().Format(time.RFC3339),
	}, nil
}

// Update 改组名/描述（管理员）：指针为 nil 表示不修改。
func (s *GroupService) Update(ctx context.Context, actorID, groupID int64, name, description *string, ip string) (*GroupItem, error) {
	g, err := s.groups.GetByID(ctx, groupID)
	if err != nil {
		if errors.Is(err, repo.ErrNotFound) {
			return nil, NewServiceError(CodeNotFound, "用户组不存在")
		}
		return nil, err
	}
	detail := map[string]any{}
	if name != nil {
		n := strings.TrimSpace(*name)
		if n == "" || utf8.RuneCountInString(n) > groupNameMax {
			return nil, NewServiceError(CodeValidation, "组名必填且不超过 50 字符")
		}
		if n != g.Name {
			if existing, gerr := s.groups.GetByName(ctx, n); gerr == nil && existing.ID != g.ID {
				return nil, NewServiceError(CodeGroupNameExists, "用户组名已存在")
			} else if gerr != nil && !errors.Is(gerr, repo.ErrNotFound) {
				return nil, gerr
			}
			detail["name"] = n
			g.Name = n
		}
	}
	if description != nil {
		if utf8.RuneCountInString(*description) > groupDescMax {
			return nil, NewServiceError(CodeValidation, "描述不能超过 500 字符")
		}
		detail["description"] = *description
		g.Description = strings.TrimSpace(*description)
	}
	g.UpdatedAt = s.now()
	if err := s.groups.Save(ctx, g); err != nil {
		return nil, err
	}
	s.audit.Record(ctx, &actorID, "group.update", "group", &g.ID, detail, ip)
	return &GroupItem{
		ID:          g.ID,
		Name:        g.Name,
		Description: g.Description,
		CreatedAt:   g.CreatedAt.UTC().Format(time.RFC3339),
		UpdatedAt:   g.UpdatedAt.UTC().Format(time.RFC3339),
	}, nil
}

// Delete 删组（管理员）：级联解除组员与授权（DB CASCADE）。
func (s *GroupService) Delete(ctx context.Context, actorID, groupID int64, ip string) error {
	g, err := s.groups.GetByID(ctx, groupID)
	if err != nil {
		if errors.Is(err, repo.ErrNotFound) {
			return NewServiceError(CodeNotFound, "用户组不存在")
		}
		return err
	}
	if err := s.groups.Delete(ctx, groupID); err != nil {
		return err
	}
	s.audit.Record(ctx, &actorID, "group.delete", "group", &groupID, map[string]any{"name": g.Name}, ip)
	return nil
}

// AddMember 加组员（管理员）：用户不存在 404，已在组内 409 GROUP_002。
func (s *GroupService) AddMember(ctx context.Context, actorID, groupID, userID int64, ip string) (*GroupMemberItem, error) {
	if _, err := s.groups.GetByID(ctx, groupID); err != nil {
		if errors.Is(err, repo.ErrNotFound) {
			return nil, NewServiceError(CodeNotFound, "用户组不存在")
		}
		return nil, err
	}
	ok, err := s.users.UserExists(ctx, userID)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, NewServiceError(CodeNotFound, "用户不存在")
	}
	if _, err := s.groups.GetMemberByID(ctx, groupID, userID); err == nil {
		return nil, NewServiceError(CodeGroupUserExists, "用户已在组内")
	} else if !errors.Is(err, repo.ErrNotFound) {
		return nil, err
	}
	if err := s.groups.AddMember(ctx, groupID, userID); err != nil {
		return nil, err
	}
	s.audit.Record(ctx, &actorID, "group.member_add", "group", &groupID,
		map[string]any{"user_id": userID}, ip)
	return &GroupMemberItem{UserID: userID}, nil
}

// RemoveMember 移出组（管理员）：即时失去其组授库的访问。
func (s *GroupService) RemoveMember(ctx context.Context, actorID, groupID, userID int64, ip string) error {
	if _, err := s.groups.GetByID(ctx, groupID); err != nil {
		if errors.Is(err, repo.ErrNotFound) {
			return NewServiceError(CodeNotFound, "用户组不存在")
		}
		return err
	}
	if err := s.groups.RemoveMember(ctx, groupID, userID); err != nil {
		if errors.Is(err, repo.ErrNotFound) {
			return NewServiceError(CodeNotFound, "成员不存在")
		}
		return err
	}
	s.audit.Record(ctx, &actorID, "group.member_remove", "group", &groupID,
		map[string]any{"user_id": userID}, ip)
	return nil
}

// ListMembers 组成员列表（管理员）。
func (s *GroupService) ListMembers(ctx context.Context, groupID int64) ([]GroupMemberItem, error) {
	rows, err := s.groups.ListMembers(ctx, groupID)
	if err != nil {
		return nil, err
	}
	items := make([]GroupMemberItem, 0, len(rows))
	for _, r := range rows {
		items = append(items, GroupMemberItem{
			UserID:    r.UserID,
			Username:  r.Username,
			CreatedAt: r.CreatedAt.UTC().Format(time.RFC3339),
		})
	}
	return items, nil
}

// Grant 组授权到库（管理员/owner）：409 GROUP_003 已授权；同组重复 → 用 PATCH 变更。
func (s *GroupService) Grant(ctx context.Context, actorID, kbID, groupID int64, role, ip string) (*KBGroupRoleItem, error) {
	if _, err := s.kbs.GetByID(ctx, kbID); err != nil {
		if errors.Is(err, repo.ErrNotFound) {
			return nil, NewServiceError(CodeNotFound, "知识库不存在")
		}
		return nil, err
	}
	g, err := s.groups.GetByID(ctx, groupID)
	if err != nil {
		if errors.Is(err, repo.ErrNotFound) {
			return nil, NewServiceError(CodeNotFound, "用户组不存在")
		}
		return nil, err
	}
	r, ok := model.ParseKBRole(role)
	if !ok {
		return nil, NewServiceError(CodeValidation, "role 取值仅 owner|editor|member")
	}
	if _, err := s.groups.GrantByGroupAndKB(ctx, kbID, groupID); err == nil {
		return nil, NewServiceError(CodeGroupKBGranted, "该组已授权到本库")
	} else if !errors.Is(err, repo.ErrNotFound) {
		return nil, err
	}
	if err := s.groups.Grant(ctx, &model.KBGroupRole{KBID: kbID, GroupID: groupID, Role: r}); err != nil {
		return nil, err
	}
	s.audit.Record(ctx, &actorID, "kb.group_grant", "kb", &kbID,
		map[string]any{"group_id": groupID, "group_name": g.Name, "role": role}, ip)
	return &KBGroupRoleItem{GroupID: groupID, Name: g.Name, Role: role}, nil
}

// UpdateGrant 变更组角色（管理员/owner）。
func (s *GroupService) UpdateGrant(ctx context.Context, actorID, kbID, groupID int64, role, ip string) error {
	r, ok := model.ParseKBRole(role)
	if !ok {
		return NewServiceError(CodeValidation, "role 取值仅 owner|editor|member")
	}
	from, err := s.groups.GrantByGroupAndKB(ctx, kbID, groupID)
	if err != nil {
		if errors.Is(err, repo.ErrNotFound) {
			return NewServiceError(CodeNotFound, "组授权不存在")
		}
		return err
	}
	if err := s.groups.UpdateGrant(ctx, kbID, groupID, r); err != nil {
		return err
	}
	s.audit.Record(ctx, &actorID, "kb.group_role_change", "kb", &kbID,
		map[string]any{"group_id": groupID, "from": model.KBRoleName(from.Role), "to": role}, ip)
	return nil
}

// RevokeGrant 撤销组授权（管理员/owner）：组员即时失访。
func (s *GroupService) RevokeGrant(ctx context.Context, actorID, kbID, groupID int64, ip string) error {
	g, err := s.groups.GrantByGroupAndKB(ctx, kbID, groupID)
	if err != nil {
		if errors.Is(err, repo.ErrNotFound) {
			return NewServiceError(CodeNotFound, "组授权不存在")
		}
		return err
	}
	if err := s.groups.RevokeGrant(ctx, kbID, groupID); err != nil {
		return err
	}
	s.audit.Record(ctx, &actorID, "kb.group_revoke", "kb", &kbID,
		map[string]any{"group_id": groupID, "role": model.KBRoleName(g.Role)}, ip)
	return nil
}

// ListGrantsByKB 库的组授权列表（管理员/owner）。
func (s *GroupService) ListGrantsByKB(ctx context.Context, kbID int64) ([]KBGroupRoleItem, error) {
	rows, err := s.groups.ListGrantsByKB(ctx, kbID)
	if err != nil {
		return nil, err
	}
	items := make([]KBGroupRoleItem, 0, len(rows))
	for _, r := range rows {
		items = append(items, KBGroupRoleItem{
			GroupID:   r.GroupID,
			Name:      r.Name,
			Role:      model.KBRoleName(r.Role),
			CreatedAt: r.CreatedAt.UTC().Format(time.RFC3339),
		})
	}
	return items, nil
}
