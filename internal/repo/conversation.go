package repo

import (
	"context"
	"time"

	"gorm.io/gorm"

	"woodpecker/internal/model"
)

// ConversationRow 会话列表行（含消息数与最近时间）。
type ConversationRow struct {
	ID           int64
	KBID         int64
	UserID       int64
	Title        *string
	MessageCount int64
	UpdatedAt    time.Time
}

// CitationRow 引用行（消息历史内嵌）。
type CitationRow struct {
	ID          int64
	MessageID   int64
	DocumentID  int64
	Seq         int
	ChunkID     int64
	FilePath    *string
	LineStart   *int
	LineEnd     *int
	HeadingPath *string
	SourceURL   *string
	Snippet     *string
}

// MessageRow 消息行（含引用）。
type MessageRow struct {
	ID               int64
	ConversationID   int64
	Role             int16
	Content          string
	Interrupted      bool
	PromptTokens     *int
	CompletionTokens *int
	ModelEntryID     *int64
	CreatedAt        time.Time
	Citations        []CitationRow
	Feedback         *FeedbackRow
}

// FeedbackRow 反馈行。
type FeedbackRow struct {
	MessageID int64
	UserID    int64
	Reason    int16
	Comment   *string
	CreatedAt time.Time
}

// ConversationRepo 会话/消息/引用/反馈存储。
type ConversationRepo struct{ db *gorm.DB }

func NewConversationRepo(db *gorm.DB) *ConversationRepo { return &ConversationRepo{db: db} }

func (r *ConversationRepo) dbx(ctx context.Context) *gorm.DB { return r.db.WithContext(ctx) }

// ---- conversations ----

func (r *ConversationRepo) CreateConversation(ctx context.Context, c *model.Conversation) error {
	return r.dbx(ctx).Create(c).Error
}

func (r *ConversationRepo) SaveConversation(ctx context.Context, c *model.Conversation) error {
	return r.dbx(ctx).Save(c).Error
}

// GetConversationByID 未找到返回 ErrNotFound。
func (r *ConversationRepo) GetConversationByID(ctx context.Context, id int64) (*model.Conversation, error) {
	var c model.Conversation
	if err := r.dbx(ctx).First(&c, id).Error; err != nil {
		return nil, mapNotFound(err)
	}
	return &c, nil
}

// ListMine 我的会话列表（按知识库，最近更新倒序；标题缺省取首条提问摘要）。
func (r *ConversationRepo) ListMine(ctx context.Context, kbID, userID int64) ([]ConversationRow, error) {
	var rows []ConversationRow
	err := r.dbx(ctx).Raw(`
		SELECT c.id, c.kb_id, c.user_id, c.title, c.updated_at,
		       (SELECT COUNT(*) FROM messages m WHERE m.conversation_id = c.id) AS message_count
		FROM conversations c
		WHERE c.kb_id = ? AND c.user_id = ?
		ORDER BY c.updated_at DESC`, kbID, userID).Scan(&rows).Error
	return rows, err
}

// DeleteConversation 删除会话（messages/citations/feedbacks 外键级联）。
func (r *ConversationRepo) DeleteConversation(ctx context.Context, id int64) error {
	return r.dbx(ctx).Delete(&model.Conversation{}, id).Error
}

// ---- messages ----

func (r *ConversationRepo) CreateMessage(ctx context.Context, m *model.Message) error {
	return r.dbx(ctx).Create(m).Error
}

func (r *ConversationRepo) SaveMessage(ctx context.Context, m *model.Message) error {
	return r.dbx(ctx).Save(m).Error
}

// GetMessageByID 未找到返回 ErrNotFound。
func (r *ConversationRepo) GetMessageByID(ctx context.Context, id int64) (*model.Message, error) {
	var m model.Message
	if err := r.dbx(ctx).First(&m, id).Error; err != nil {
		return nil, mapNotFound(err)
	}
	return &m, nil
}

// ListMessages 会话全部消息（按时间正序）。
func (r *ConversationRepo) ListMessages(ctx context.Context, conversationID int64) ([]MessageRow, error) {
	var msgs []model.Message
	if err := r.dbx(ctx).Where("conversation_id = ?", conversationID).Order("id").Find(&msgs).Error; err != nil {
		return nil, err
	}
	// 引用批量装配
	ids := make([]int64, 0, len(msgs))
	for _, m := range msgs {
		ids = append(ids, m.ID)
	}
	cites := map[int64][]CitationRow{}
	if len(ids) > 0 {
		var cs []model.MessageCitation
		if err := r.dbx(ctx).Where("message_id IN ?", ids).Order("seq").Find(&cs).Error; err != nil {
			return nil, err
		}
		for _, c := range cs {
			cites[c.MessageID] = append(cites[c.MessageID], CitationRow{
				ID: c.ID, MessageID: c.MessageID, DocumentID: c.DocumentID, Seq: c.Seq,
				ChunkID: c.ChunkID, FilePath: c.FilePath, LineStart: c.LineStart, LineEnd: c.LineEnd,
				HeadingPath: c.HeadingPath, SourceURL: c.SourceURL, Snippet: c.Snippet,
			})
		}
	}
	rows := make([]MessageRow, 0, len(msgs))
	for _, m := range msgs {
		rows = append(rows, MessageRow{
			ID: m.ID, ConversationID: m.ConversationID, Role: m.Role, Content: m.Content,
			Interrupted: m.Interrupted, PromptTokens: m.PromptTokens, CompletionTokens: m.CompletionTokens,
			ModelEntryID: m.ModelEntryID, CreatedAt: m.CreatedAt, Citations: cites[m.ID],
		})
	}
	return rows, nil
}

// LastMessages 最近 N 条消息（正序返回，供多轮上下文）。
func (r *ConversationRepo) LastMessages(ctx context.Context, conversationID int64, n int) ([]model.Message, error) {
	var msgs []model.Message
	if err := r.dbx(ctx).Where("conversation_id = ?", conversationID).
		Order("id DESC").Limit(n).Find(&msgs).Error; err != nil {
		return nil, err
	}
	// 反转成正序
	for i, j := 0, len(msgs)-1; i < j; i, j = i+1, j-1 {
		msgs[i], msgs[j] = msgs[j], msgs[i]
	}
	return msgs, nil
}

// ---- citations ----

// ReplaceCitations 整体替换某消息的引用（事务：先删后插）。
func (r *ConversationRepo) ReplaceCitations(ctx context.Context, messageID int64, cs []model.MessageCitation) error {
	return r.dbx(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("message_id = ?", messageID).Delete(&model.MessageCitation{}).Error; err != nil {
			return err
		}
		if len(cs) == 0 {
			return nil
		}
		return tx.CreateInBatches(cs, 100).Error
	})
}

// ---- feedbacks ----

// UpsertFeedback 重复点踩覆盖前次（UNIQUE(message_id, user_id) ON CONFLICT 语义）。
func (r *ConversationRepo) UpsertFeedback(ctx context.Context, f *model.Feedback) error {
	return r.dbx(ctx).Exec(`INSERT INTO feedbacks (message_id, user_id, reason, comment, created_at)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT (message_id, user_id)
		DO UPDATE SET reason = EXCLUDED.reason, comment = EXCLUDED.comment`,
		f.MessageID, f.UserID, f.Reason, f.Comment, f.CreatedAt).Error
}

// DeleteFeedback 撤销点踩；无记录返回 ErrNotFound。
func (r *ConversationRepo) DeleteFeedback(ctx context.Context, messageID, userID int64) error {
	res := r.dbx(ctx).Where("message_id = ? AND user_id = ?", messageID, userID).Delete(&model.Feedback{})
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return ErrNotFound
	}
	return nil
}

// GetFeedbackByUser 我的点踩（消息历史装配）。
func (r *ConversationRepo) GetFeedbackByUser(ctx context.Context, messageID, userID int64) (*FeedbackRow, error) {
	var f model.Feedback
	err := r.dbx(ctx).First(&f, "message_id = ? AND user_id = ?", messageID, userID).Error
	if err != nil {
		if isNoRows(err) {
			return nil, nil
		}
		return nil, err
	}
	return &FeedbackRow{MessageID: f.MessageID, UserID: f.UserID, Reason: f.Reason, Comment: f.Comment, CreatedAt: f.CreatedAt}, nil
}
