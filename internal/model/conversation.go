package model

import "time"

// 消息角色（messages.role）。
const (
	MsgRoleUser      int16 = 1
	MsgRoleAssistant int16 = 2
)

// 反馈原因（feedbacks.reason，PRD FR-14）。
const (
	FBReasonIrrelevant int16 = 1 // 引用不相关
	FBReasonWrong      int16 = 2 // 内容错误
	FBReasonNotFound   int16 = 3 // 没有找到想要的
	FBReasonOther      int16 = 4 // 其他（comment 必填）
)

// Conversation 会话（按知识库组织，FR-09）。
type Conversation struct {
	ID        int64     `gorm:"primaryKey;column:id"`
	KBID      int64     `gorm:"column:kb_id"`
	UserID    int64     `gorm:"column:user_id"`
	Title     *string   `gorm:"column:title;size:200"`
	CreatedAt time.Time `gorm:"column:created_at"`
	UpdatedAt time.Time `gorm:"column:updated_at"`
}

func (Conversation) TableName() string { return "conversations" }

// Message 消息（role 1=user 2=assistant）。
type Message struct {
	ID               int64     `gorm:"primaryKey;column:id"`
	ConversationID   int64     `gorm:"column:conversation_id"`
	Role             int16     `gorm:"column:role"`
	Content          string    `gorm:"column:content"`
	Interrupted      bool      `gorm:"column:interrupted"`
	PromptTokens     *int      `gorm:"column:prompt_tokens"`
	CompletionTokens *int      `gorm:"column:completion_tokens"`
	ModelEntryID     *int64    `gorm:"column:model_entry_id"`
	CreatedAt        time.Time `gorm:"column:created_at"`
}

func (Message) TableName() string { return "messages" }

// MessageCitation 回答引用（快照字段，M5 检索结果固化）。
type MessageCitation struct {
	ID          int64   `gorm:"primaryKey;column:id"`
	MessageID   int64   `gorm:"column:message_id"`
	DocumentID  int64   `gorm:"column:document_id"`
	Seq         int     `gorm:"column:seq"`
	ChunkID     int64   `gorm:"column:chunk_id"`
	FilePath    *string `gorm:"column:file_path;size:1000"`
	LineStart   *int    `gorm:"column:line_start"`
	LineEnd     *int    `gorm:"column:line_end"`
	HeadingPath *string `gorm:"column:heading_path"`
	SourceURL   *string `gorm:"column:source_url;size:2000"`
	Snippet     *string `gorm:"column:snippet"`
}

func (MessageCitation) TableName() string { return "message_citations" }

// Feedback 回答点踩（UNIQUE(message_id, user_id)，重复覆盖）。
type Feedback struct {
	ID        int64     `gorm:"primaryKey;column:id"`
	MessageID int64     `gorm:"column:message_id"`
	UserID    int64     `gorm:"column:user_id"`
	Reason    int16     `gorm:"column:reason"`
	Comment   *string   `gorm:"column:comment;size:500"`
	CreatedAt time.Time `gorm:"column:created_at"`
}

func (Feedback) TableName() string { return "feedbacks" }
