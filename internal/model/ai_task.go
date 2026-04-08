package model

import (
	"time"

	"gorm.io/datatypes"
)

type AITask struct {
	ID             string         `gorm:"primaryKey;column:id;size:32" json:"id"`
	Type           string         `gorm:"column:type;size:40;not null" json:"type"`
	Status         string         `gorm:"column:status;size:20;not null;default:'pending'" json:"status"`
	Progress       int            `gorm:"column:progress;default:0" json:"progress"`
	Stage          string         `gorm:"column:stage;size:40" json:"stage"`
	StageLabel     string         `gorm:"column:stage_label;size:100" json:"stageLabel"`
	ResultJson     datatypes.JSON `gorm:"column:result_json;type:jsonb" json:"result,omitempty"`
	ErrorMessage   *string        `gorm:"column:error_message" json:"errorMessage,omitempty"`
	AttemptCount   int            `gorm:"column:attempt_count;not null;default:0" json:"attemptCount"`
	MaxAttempts    int            `gorm:"column:max_attempts;not null;default:3" json:"maxAttempts"`
	WorkerID       *string        `gorm:"column:worker_id;size:64" json:"workerId,omitempty"`
	QueuedAt       *time.Time     `gorm:"column:queued_at" json:"queuedAt,omitempty"`
	StartedAt      *time.Time     `gorm:"column:started_at" json:"startedAt,omitempty"`
	CompletedAt    *time.Time     `gorm:"column:completed_at" json:"completedAt,omitempty"`
	IdempotencyKey *string        `gorm:"column:idempotency_key;size:64" json:"idempotencyKey,omitempty"`
	Model          string         `gorm:"column:model;size:50" json:"model"`
	CreatedBy      string         `gorm:"column:created_by;size:32;not null" json:"createdBy"`
	ProjectID      *string        `gorm:"column:project_id;size:32" json:"projectId,omitempty"`
	CreatedAt      time.Time      `gorm:"column:created_at;not null;autoCreateTime" json:"createdAt"`
	UpdatedAt      time.Time      `gorm:"column:updated_at;not null;autoUpdateTime" json:"updatedAt"`
}

func (AITask) TableName() string {
	return "ai_tasks"
}

type AIConversation struct {
	ID        string    `gorm:"primaryKey;column:id;size:32" json:"id"`
	ProjectID *string   `gorm:"column:project_id;size:32" json:"projectId"`
	UserID    string    `gorm:"column:user_id;size:32;not null" json:"userId"`
	Type      string    `gorm:"column:type;size:20;not null" json:"type"`
	CreatedAt time.Time `gorm:"column:created_at;not null;autoCreateTime" json:"createdAt"`
	UpdatedAt time.Time `gorm:"column:updated_at;not null;autoUpdateTime" json:"updatedAt"`
}

func (AIConversation) TableName() string {
	return "ai_conversations"
}

type AIChatMessage struct {
	ID             string    `gorm:"primaryKey;column:id;size:32" json:"id"`
	ConversationID string    `gorm:"column:conversation_id;size:32;not null;index" json:"conversationId"`
	Role           string    `gorm:"column:role;size:20;not null" json:"role"`
	Content        string    `gorm:"column:content;not null" json:"content"`
	CreatedAt      time.Time `gorm:"column:created_at;not null;autoCreateTime" json:"createdAt"`
}

func (AIChatMessage) TableName() string {
	return "ai_chat_messages"
}
