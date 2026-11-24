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

// 知识库字段上限（PRD FR-02）。
const (
	maxKBNameLen = 50
	maxKBDescLen = 500
)

// KBRepo 知识库存储接口（实现在 internal/repo）。
type KBRepo interface {
	Create(ctx context.Context, kb *model.KnowledgeBase) error
	Save(ctx context.Context, kb *model.KnowledgeBase) error
	GetByID(ctx context.Context, id int64) (*model.KnowledgeBase, error)
	GetByName(ctx context.Context, name string) (*model.KnowledgeBase, error)
	Delete(ctx context.Context, id int64) error
	CountDocuments(ctx context.Context, kbID int64) (int64, error)
	ListForUser(ctx context.Context, userID int64) ([]repo.KBRow, error)
	ListAll(ctx context.Context) ([]repo.KBRow, error)
	GetMemberRole(ctx context.Context, kbID, userID int64) (int16, error)
	ListMembers(ctx context.Context, kbID int64) ([]repo.KBMemberRow, error)
	UpsertMember(ctx context.Context, m *model.KBMember) error
	DeleteMember(ctx context.Context, kbID, userID int64) error
	UserExists(ctx context.Context, userID int64) (bool, error)
}

// KBItem 知识库列表/详情响应。
type KBItem struct {
	ID            int64  `json:"id"`
	Name          string `json:"name"`
	Description   string `json:"description"`
	DocumentCount int64  `json:"document_count"`
	MyRole        string `json:"my_role"`
	CreatedBy     int64  `json:"created_by"`
	CreatedAt     string `json:"created_at"`
	UpdatedAt     string `json:"updated_at"`
}

// KBMemberItem 成员列表响应。
type KBMemberItem struct {
	UserID    int64  `json:"user_id"`
	Username  string `json:"username"`
	Role      string `json:"role"`
	CreatedAt string `json:"created_at"`
}

// KBService 知识库与成员权限（PRD FR-02 / R6）。
// 库管理操作（建/改/删、成员授权）仅管理员；库内读写权限由中间件按角色把关。
type KBService struct {
	kbs   KBRepo
	audit *AuditService
	now   func() time.Time
}

func NewKBService(kbs KBRepo, audit *AuditService) *KBService {
	return &KBService{kbs: kbs, audit: audit, now: time.Now}
}

// SetClock 注入时钟（测试用）。
func (s *KBService) SetClock(now func() time.Time) { s.now = now }

func (s *KBService) toItem(row repo.KBRow, role string) KBItem {
	return KBItem{
		ID:            row.ID,
		Name:          row.Name,
		Description:   row.Description,
		DocumentCount: row.DocCount,
		MyRole:        role,
		CreatedBy:     row.CreatedBy,
		CreatedAt:     row.CreatedAt.UTC().Format(time.RFC3339),
		UpdatedAt:     row.UpdatedAt.UTC().Format(time.RFC3339),
	}
}

// List 我可见的库：管理员=全部（my_role=admin），普通用户=被授权库（my_role=库内角色）。
func (s *KBService) List(ctx context.Context, userID int64, isAdmin bool) ([]KBItem, error) {
	var (
		rows []repo.KBRow
		err  error
	)
	if isAdmin {
		rows, err = s.kbs.ListAll(ctx)
	} else {
		rows, err = s.kbs.ListForUser(ctx, userID)
	}
	if err != nil {
		return nil, err
	}
	items := make([]KBItem, 0, len(rows))
	for _, row := range rows {
		role := "admin"
		if !isAdmin {
			role = model.KBRoleName(row.Role)
		}
		items = append(items, s.toItem(row, role))
	}
	return items, nil
}

// Get 库详情（成员资格由中间件先行校验；此处再次校验以防越权调用）。
func (s *KBService) Get(ctx context.Context, kbID, userID int64, isAdmin bool) (*KBItem, error) {
	kb, err := s.kbs.GetByID(ctx, kbID)
	if err != nil {
		if errors.Is(err, repo.ErrNotFound) {
			return nil, NewServiceError(CodeNotFound, "知识库不存在")
		}
		return nil, err
	}
	role := "admin"
	if !isAdmin {
		r, err := s.kbs.GetMemberRole(ctx, kbID, userID)
		if err != nil {
			if errors.Is(err, repo.ErrNotFound) {
				return nil, NewServiceError(CodeNotFound, "知识库不存在")
			}
			return nil, err
		}
		role = model.KBRoleName(r)
	}
	docCount, err := s.kbs.CountDocuments(ctx, kbID)
	if err != nil {
		return nil, err
	}
	item := s.toItem(repo.KBRow{
		ID:          kb.ID,
		Name:        kb.Name,
		Description: kb.Description,
		CreatedBy:   kb.CreatedBy,
		CreatedAt:   kb.CreatedAt,
		UpdatedAt:   kb.UpdatedAt,
		DocCount:    docCount,
	}, role)
	return &item, nil
}

// Create 建库（仅管理员，FR-02）：名称必填且 ≤50 字符，重名 → KB_001。
func (s *KBService) Create(ctx context.Context, actorID int64, name, description, ip string) (*KBItem, error) {
	name = strings.TrimSpace(name)
	if err := validateKBFields(name, &description); err != nil {
		return nil, err
	}
	if _, err := s.kbs.GetByName(ctx, name); err == nil {
		return nil, NewServiceError(CodeKBNameExists, "知识库名称已存在")
	} else if !errors.Is(err, repo.ErrNotFound) {
		return nil, err
	}
	kb := &model.KnowledgeBase{
		Name:        name,
		Description: description,
		CreatedBy:   actorID,
	}
	if err := s.kbs.Create(ctx, kb); err != nil {
		return nil, err
	}
	s.audit.Record(ctx, &actorID, "kb.create", "kb", &kb.ID,
		map[string]any{"name": kb.Name}, ip)
	item := s.toItem(repo.KBRow{
		ID:          kb.ID,
		Name:        kb.Name,
		Description: kb.Description,
		CreatedBy:   kb.CreatedBy,
		CreatedAt:   kb.CreatedAt,
		UpdatedAt:   kb.UpdatedAt,
	}, "admin")
	return &item, nil
}

// Update 改名/改描述（仅管理员）：字段为 nil 表示不修改。
func (s *KBService) Update(ctx context.Context, actorID, kbID int64, name, description *string, ip string) (*KBItem, error) {
	kb, err := s.kbs.GetByID(ctx, kbID)
	if err != nil {
		if errors.Is(err, repo.ErrNotFound) {
			return nil, NewServiceError(CodeNotFound, "知识库不存在")
		}
		return nil, err
	}
	detail := map[string]any{}
	if name != nil {
		n := strings.TrimSpace(*name)
		if err := validateKBFields(n, nil); err != nil {
			return nil, err
		}
		if n != kb.Name {
			if existing, err := s.kbs.GetByName(ctx, n); err == nil && existing.ID != kb.ID {
				return nil, NewServiceError(CodeKBNameExists, "知识库名称已存在")
			} else if err != nil && !errors.Is(err, repo.ErrNotFound) {
				return nil, err
			}
			detail["name"] = n
			kb.Name = n
		}
	}
	if description != nil {
		if utf8.RuneCountInString(*description) > maxKBDescLen {
			return nil, NewServiceError(CodeValidation, "描述不能超过 500 字符")
		}
		detail["description"] = *description
		kb.Description = *description
	}
	kb.UpdatedAt = s.now()
	if err := s.kbs.Save(ctx, kb); err != nil {
		return nil, err
	}
	s.audit.Record(ctx, &actorID, "kb.update", "kb", &kb.ID, detail, ip)
	docCount, err := s.kbs.CountDocuments(ctx, kbID)
	if err != nil {
		return nil, err
	}
	item := s.toItem(repo.KBRow{
		ID:          kb.ID,
		Name:        kb.Name,
		Description: kb.Description,
		CreatedBy:   kb.CreatedBy,
		CreatedAt:   kb.CreatedAt,
		UpdatedAt:   kb.UpdatedAt,
		DocCount:    docCount,
	}, "admin")
	return &item, nil
}

// Delete 删库（仅管理员）：数据库级联删除文档/分块/会话（R4）。
func (s *KBService) Delete(ctx context.Context, actorID, kbID int64, ip string) error {
	kb, err := s.kbs.GetByID(ctx, kbID)
	if err != nil {
		if errors.Is(err, repo.ErrNotFound) {
			return NewServiceError(CodeNotFound, "知识库不存在")
		}
		return err
	}
	if err := s.kbs.Delete(ctx, kbID); err != nil {
		return err
	}
	s.audit.Record(ctx, &actorID, "kb.delete", "kb", &kbID,
		map[string]any{"name": kb.Name}, ip)
	return nil
}

// ListMembers 成员列表（仅管理员）。
func (s *KBService) ListMembers(ctx context.Context, kbID int64) ([]KBMemberItem, error) {
	if _, err := s.kbs.GetByID(ctx, kbID); err != nil {
		if errors.Is(err, repo.ErrNotFound) {
			return nil, NewServiceError(CodeNotFound, "知识库不存在")
		}
		return nil, err
	}
	rows, err := s.kbs.ListMembers(ctx, kbID)
	if err != nil {
		return nil, err
	}
	items := make([]KBMemberItem, 0, len(rows))
	for _, row := range rows {
		items = append(items, KBMemberItem{
			UserID:    row.UserID,
			Username:  row.Username,
			Role:      model.KBRoleName(row.Role),
			CreatedAt: row.CreatedAt.UTC().Format(time.RFC3339),
		})
	}
	return items, nil
}

// AddMember 添加成员或直接指派角色（仅管理员）。
func (s *KBService) AddMember(ctx context.Context, actorID, kbID, userID int64, role, ip string) (*KBMemberItem, error) {
	if _, err := s.kbs.GetByID(ctx, kbID); err != nil {
		if errors.Is(err, repo.ErrNotFound) {
			return nil, NewServiceError(CodeNotFound, "知识库不存在")
		}
		return nil, err
	}
	r, ok := model.ParseKBRole(role)
	if !ok {
		return nil, NewServiceError(CodeValidation, "role 取值仅 owner|editor|member")
	}
	if userID <= 0 {
		return nil, NewServiceError(CodeValidation, "user_id 非法")
	}
	exists, err := s.kbs.UserExists(ctx, userID)
	if err != nil {
		return nil, err
	}
	if !exists {
		return nil, NewServiceError(CodeNotFound, "用户不存在")
	}
	if err := s.kbs.UpsertMember(ctx, &model.KBMember{KBID: kbID, UserID: userID, Role: r}); err != nil {
		return nil, err
	}
	s.audit.Record(ctx, &actorID, "member.grant", "kb", &kbID,
		map[string]any{"user_id": userID, "role": role}, ip)
	return &KBMemberItem{UserID: userID, Role: role}, nil
}

// UpdateMemberRole 变更成员角色（仅管理员）。
func (s *KBService) UpdateMemberRole(ctx context.Context, actorID, kbID, userID int64, role, ip string) error {
	if _, err := s.kbs.GetByID(ctx, kbID); err != nil {
		if errors.Is(err, repo.ErrNotFound) {
			return NewServiceError(CodeNotFound, "知识库不存在")
		}
		return err
	}
	r, ok := model.ParseKBRole(role)
	if !ok {
		return NewServiceError(CodeValidation, "role 取值仅 owner|editor|member")
	}
	from, err := s.kbs.GetMemberRole(ctx, kbID, userID)
	if err != nil {
		if errors.Is(err, repo.ErrNotFound) {
			return NewServiceError(CodeNotFound, "成员不存在")
		}
		return err
	}
	if err := s.kbs.UpsertMember(ctx, &model.KBMember{KBID: kbID, UserID: userID, Role: r}); err != nil {
		return err
	}
	s.audit.Record(ctx, &actorID, "member.role_change", "kb", &kbID,
		map[string]any{"user_id": userID, "from": model.KBRoleName(from), "to": role}, ip)
	return nil
}

// RemoveMember 移除成员（仅管理员）：历史会话随库保留，成员立即失访（R6）。
func (s *KBService) RemoveMember(ctx context.Context, actorID, kbID, userID int64, ip string) error {
	if _, err := s.kbs.GetByID(ctx, kbID); err != nil {
		if errors.Is(err, repo.ErrNotFound) {
			return NewServiceError(CodeNotFound, "知识库不存在")
		}
		return err
	}
	if _, err := s.kbs.GetMemberRole(ctx, kbID, userID); err != nil {
		if errors.Is(err, repo.ErrNotFound) {
			return NewServiceError(CodeNotFound, "成员不存在")
		}
		return err
	}
	if err := s.kbs.DeleteMember(ctx, kbID, userID); err != nil {
		return err
	}
	s.audit.Record(ctx, &actorID, "member.remove", "kb", &kbID,
		map[string]any{"user_id": userID}, ip)
	return nil
}

func validateKBFields(name string, description *string) error {
	if name == "" {
		return NewServiceError(CodeValidation, "知识库名称不能为空")
	}
	if utf8.RuneCountInString(name) > maxKBNameLen {
		return NewServiceError(CodeValidation, "知识库名称不能超过 50 字符")
	}
	if description != nil && utf8.RuneCountInString(*description) > maxKBDescLen {
		return NewServiceError(CodeValidation, "描述不能超过 500 字符")
	}
	return nil
}
