package repo

import (
	"context"
	"encoding/json"
	"time"

	"woodpecker/internal/model"
)

// AuditQuery 审计日志筛选（FR-15）。
type AuditQuery struct {
	UserID     int64  // 0=不过滤
	Action     string // 空=不过滤
	ObjectType string // 空=不过滤
	From       *time.Time
	To         *time.Time
	Page       int
	PerPage    int
}

// AuditRow 审计日志行（含操作人用户名）。
type AuditRow struct {
	ID         int64          `json:"id"`
	UserID     *int64         `json:"user_id"`
	Username   *string        `json:"username"`
	Action     string         `json:"action"`
	ObjectType string         `json:"object_type"`
	ObjectID   *int64         `json:"object_id"`
	Detail     map[string]any `json:"detail"`
	IP         string         `json:"ip"`
	CreatedAt  time.Time      `json:"created_at"`
}

// List 审计日志查询（新→旧，筛选分页）。
func (r *AuditRepo) List(ctx context.Context, q AuditQuery) ([]AuditRow, int64, error) {
	if q.Page < 1 {
		q.Page = 1
	}
	if q.PerPage < 1 || q.PerPage > 100 {
		q.PerPage = 20
	}
	tx := r.db.WithContext(ctx).Model(&model.AuditLog{})
	if q.UserID > 0 {
		tx = tx.Where("user_id = ?", q.UserID)
	}
	if q.Action != "" {
		tx = tx.Where("action = ?", q.Action)
	}
	if q.ObjectType != "" {
		tx = tx.Where("object_type = ?", q.ObjectType)
	}
	if q.From != nil {
		tx = tx.Where("created_at >= ?", *q.From)
	}
	if q.To != nil {
		tx = tx.Where("created_at < ?", *q.To)
	}
	var total int64
	if err := tx.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var logs []model.AuditLog
	if err := tx.
		Order("created_at DESC, id DESC").
		Limit(q.PerPage).Offset((q.Page - 1) * q.PerPage).
		Find(&logs).Error; err != nil {
		return nil, 0, err
	}
	// 批量取操作人用户名
	ids := make([]int64, 0, len(logs))
	for _, a := range logs {
		if a.UserID != nil {
			ids = append(ids, *a.UserID)
		}
	}
	names := map[int64]string{}
	if len(ids) > 0 {
		var users []model.User
		if err := r.db.WithContext(ctx).Select("id, username").Where("id IN ?", ids).Find(&users).Error; err != nil {
			return nil, 0, err
		}
		for _, u := range users {
			names[u.ID] = u.Username
		}
	}
	rows := make([]AuditRow, 0, len(logs))
	for _, a := range logs {
		row := AuditRow{
			ID: a.ID, UserID: a.UserID, Action: a.Action, ObjectType: a.ObjectType,
			ObjectID: a.ObjectID, IP: a.IP, CreatedAt: a.CreatedAt,
		}
		if a.UserID != nil {
			if name, ok := names[*a.UserID]; ok {
				row.Username = &name
			}
		}
		if len(a.Detail) > 0 {
			var d map[string]any
			if err := json.Unmarshal(a.Detail, &d); err == nil {
				row.Detail = d
			}
		}
		rows = append(rows, row)
	}
	return rows, total, nil
}

// Set 写入设置（Upsert：键存在则更新值与更新人）。
func (r *SettingRepo) Set(ctx context.Context, key string, value []byte, updatedBy int64) error {
	return r.db.WithContext(ctx).Exec(`INSERT INTO settings (key, value, updated_at, updated_by)
		VALUES (?, ?::jsonb, now(), ?)
		ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value, updated_at = now(), updated_by = EXCLUDED.updated_by`,
		key, string(value), updatedBy).Error
}

// FeedbackQuery 管理端反馈筛选（FR-14）。
type FeedbackQuery struct {
	KBID    int64 // 0=不过滤
	Reason  int16 // 0=不过滤
	From    *time.Time
	To      *time.Time
	Page    int
	PerPage int
}

// FeedbackAdminRow 管理端反馈行（含反馈人与所属库）。
type FeedbackAdminRow struct {
	ID          int64     `json:"id"`
	MessageID   int64     `json:"message_id"`
	ConvID      int64     `json:"conversation_id"`
	KBID        int64     `json:"kb_id"`
	Reason      int16     `json:"reason"`
	Comment     *string   `json:"comment"`
	Username    string    `json:"username"`
	MessageText string    `json:"message_text"`
	CreatedAt   time.Time `json:"created_at"`
}

// ListFeedbacks 反馈列表（联 messages/conversations/users；新→旧）。
func (r *ConversationRepo) ListFeedbacks(ctx context.Context, q FeedbackQuery) ([]FeedbackAdminRow, int64, error) {
	if q.Page < 1 {
		q.Page = 1
	}
	if q.PerPage < 1 || q.PerPage > 100 {
		q.PerPage = 20
	}
	base := `FROM feedbacks f
		JOIN messages m ON m.id = f.message_id
		JOIN conversations c ON c.id = m.conversation_id
		JOIN users u ON u.id = f.user_id
		WHERE 1=1`
	var args []any
	if q.KBID > 0 {
		base += " AND c.kb_id = ?"
		args = append(args, q.KBID)
	}
	if q.Reason > 0 {
		base += " AND f.reason = ?"
		args = append(args, q.Reason)
	}
	if q.From != nil {
		base += " AND f.created_at >= ?"
		args = append(args, *q.From)
	}
	if q.To != nil {
		base += " AND f.created_at < ?"
		args = append(args, *q.To)
	}
	var total int64
	if err := r.dbx(ctx).Raw("SELECT COUNT(*) "+base, args...).Scan(&total).Error; err != nil {
		return nil, 0, err
	}
	sql := `SELECT f.id, f.message_id, m.conversation_id, c.kb_id, f.reason, f.comment, u.username, m.content AS message_text, f.created_at
		` + base + ` ORDER BY f.created_at DESC, f.id DESC LIMIT ? OFFSET ?`
	args = append(args, q.PerPage, (q.Page-1)*q.PerPage)
	rows := make([]FeedbackAdminRow, 0, q.PerPage)
	err := r.dbx(ctx).Raw(sql, args...).Scan(&rows).Error
	return rows, total, err
}
