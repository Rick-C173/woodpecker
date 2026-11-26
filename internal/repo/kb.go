package repo

import (
	"context"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"woodpecker/internal/model"
)

// KBRow 知识库列表/详情行：含当前用户角色与文档数。
type KBRow struct {
	ID          int64
	Name        string
	Description string
	Visibility  int16
	CreatedBy   int64
	CreatedAt   time.Time
	UpdatedAt   time.Time
	DocCount    int64
	Role        int16 // 0=无成员行（管理员视角或全员可见访客）
}

// KBMemberRow 成员列表行（含用户名）。
type KBMemberRow struct {
	UserID    int64
	Username  string
	Role      int16
	CreatedAt time.Time
}

type KBRepo struct{ db *gorm.DB }

func NewKBRepo(db *gorm.DB) *KBRepo { return &KBRepo{db: db} }

func (r *KBRepo) dbx(ctx context.Context) *gorm.DB { return r.db.WithContext(ctx) }

func (r *KBRepo) Create(ctx context.Context, kb *model.KnowledgeBase) error {
	return r.dbx(ctx).Create(kb).Error
}

func (r *KBRepo) Save(ctx context.Context, kb *model.KnowledgeBase) error {
	return r.dbx(ctx).Save(kb).Error
}

func (r *KBRepo) GetByID(ctx context.Context, id int64) (*model.KnowledgeBase, error) {
	var kb model.KnowledgeBase
	if err := r.dbx(ctx).First(&kb, id).Error; err != nil {
		if isNoRows(err) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &kb, nil
}

func (r *KBRepo) GetByName(ctx context.Context, name string) (*model.KnowledgeBase, error) {
	var kb model.KnowledgeBase
	if err := r.dbx(ctx).Where("name = ?", name).First(&kb).Error; err != nil {
		if isNoRows(err) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &kb, nil
}

func (r *KBRepo) Delete(ctx context.Context, id int64) error {
	return r.dbx(ctx).Where("id = ?", id).Delete(&model.KnowledgeBase{}).Error
}

func (r *KBRepo) CountDocuments(ctx context.Context, kbID int64) (int64, error) {
	var n int64
	err := r.dbx(ctx).Table("documents").Where("kb_id = ?", kbID).Count(&n).Error
	return n, err
}

// GetVisibility 库可见性（FR-17）：KBAccess 中间件判定非成员访问用。
func (r *KBRepo) GetVisibility(ctx context.Context, kbID int64) (int16, error) {
	var vis int16
	err := r.dbx(ctx).Table("knowledge_bases").Select("visibility").Where("id = ?", kbID).Scan(&vis).Error
	if err != nil {
		return 0, err
	}
	if vis == 0 {
		return 0, ErrNotFound // Scan 未命中不报错，行不存在以 0 值判别
	}
	return vis, nil
}

// ListForUser 我可见的库（FR-02 列表页 + FR-17）：成员库 ∪ 全员可见库，附角色与文档数。
// role=0 表示无成员行（仅全员可见访客），由 service 层映射为 my_role=org。
func (r *KBRepo) ListForUser(ctx context.Context, userID int64) ([]KBRow, error) {
	const q = `
SELECT kb.id, kb.name, COALESCE(kb.description, '') AS description, kb.visibility, kb.created_by,
       kb.created_at, kb.updated_at, COALESCE(m.role, 0) AS role,
       (SELECT COUNT(*) FROM documents d WHERE d.kb_id = kb.id) AS doc_count
FROM knowledge_bases kb
LEFT JOIN kb_members m ON m.kb_id = kb.id AND m.user_id = ?
WHERE m.id IS NOT NULL OR kb.visibility = 2
ORDER BY kb.updated_at DESC, kb.id DESC`
	var rows []KBRow
	err := r.dbx(ctx).Raw(q, userID).Scan(&rows).Error
	return rows, err
}

// ListAll 管理员视角：全部知识库（role=0 表示无库内角色）。
func (r *KBRepo) ListAll(ctx context.Context) ([]KBRow, error) {
	const q = `
SELECT kb.id, kb.name, COALESCE(kb.description, '') AS description, kb.visibility, kb.created_by,
       kb.created_at, kb.updated_at, 0 AS role,
       (SELECT COUNT(*) FROM documents d WHERE d.kb_id = kb.id) AS doc_count
FROM knowledge_bases kb
ORDER BY kb.updated_at DESC, kb.id DESC`
	var rows []KBRow
	err := r.dbx(ctx).Raw(q).Scan(&rows).Error
	return rows, err
}

// GetMemberRole 指定用户在库内角色；非成员返回 ErrNotFound。
func (r *KBRepo) GetMemberRole(ctx context.Context, kbID, userID int64) (int16, error) {
	var m model.KBMember
	if err := r.dbx(ctx).Where("kb_id = ? AND user_id = ?", kbID, userID).First(&m).Error; err != nil {
		if isNoRows(err) {
			return 0, ErrNotFound
		}
		return 0, err
	}
	return m.Role, nil
}

func (r *KBRepo) ListMembers(ctx context.Context, kbID int64) ([]KBMemberRow, error) {
	const q = `
SELECT m.user_id, u.username, m.role, m.created_at
FROM kb_members m
JOIN users u ON u.id = m.user_id
WHERE m.kb_id = ?
ORDER BY m.role ASC, u.username ASC`
	var rows []KBMemberRow
	err := r.dbx(ctx).Raw(q, kbID).Scan(&rows).Error
	return rows, err
}

// UpsertMember 添加成员或更新角色（kb_id+user_id 唯一）。
func (r *KBRepo) UpsertMember(ctx context.Context, m *model.KBMember) error {
	return r.dbx(ctx).Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "kb_id"}, {Name: "user_id"}},
		DoUpdates: clause.Assignments(map[string]any{"role": m.Role}),
	}).Create(m).Error
}

func (r *KBRepo) DeleteMember(ctx context.Context, kbID, userID int64) error {
	return r.dbx(ctx).Where("kb_id = ? AND user_id = ?", kbID, userID).Delete(&model.KBMember{}).Error
}

func (r *KBRepo) UserExists(ctx context.Context, userID int64) (bool, error) {
	var ok bool
	err := r.dbx(ctx).Raw("SELECT EXISTS(SELECT 1 FROM users WHERE id = ?)", userID).Scan(&ok).Error
	return ok, err
}
