package repository

import (
	"errors"
	"strings"
	"time"

	"optitree-backend/internal/model"

	"gorm.io/gorm"
)

type MessageCursor struct {
	CreatedAt time.Time
	ID        string
}

type AIConversationRepository struct {
	db *gorm.DB
}

func NewAIConversationRepository(db *gorm.DB) *AIConversationRepository {
	return &AIConversationRepository{db: db}
}

func (r *AIConversationRepository) Create(conversation *model.AIConversation) error {
	return r.db.Create(conversation).Error
}

func (r *AIConversationRepository) FindByID(id string) (*model.AIConversation, error) {
	var conversation model.AIConversation
	err := r.db.Where("id = ?", strings.TrimSpace(id)).First(&conversation).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	return &conversation, err
}

func (r *AIConversationRepository) ListByUser(userID, projectID, conversationType string, page, pageSize int) ([]model.AIConversation, int64, error) {
	q := r.db.Model(&model.AIConversation{}).Where("user_id = ?", strings.TrimSpace(userID))
	if strings.TrimSpace(projectID) != "" {
		q = q.Where("project_id = ?", strings.TrimSpace(projectID))
	}
	if strings.TrimSpace(conversationType) != "" {
		q = q.Where("type = ?", strings.TrimSpace(conversationType))
	}

	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}

	var conversations []model.AIConversation
	err := q.Order("COALESCE(last_message_at, created_at) DESC").
		Order("updated_at DESC").
		Offset((page - 1) * pageSize).
		Limit(pageSize).
		Find(&conversations).Error
	if err != nil {
		return nil, 0, err
	}
	return conversations, total, nil
}

func (r *AIConversationRepository) RecordMessageActivity(conversationID string, happenedAt time.Time, titleHint string) error {
	updates := map[string]interface{}{
		"updated_at":      happenedAt.UTC(),
		"last_message_at": happenedAt.UTC(),
		"message_count":   gorm.Expr("message_count + 1"),
	}
	if strings.TrimSpace(titleHint) != "" {
		updates["title"] = gorm.Expr("CASE WHEN COALESCE(title, '') = '' THEN ? ELSE title END", strings.TrimSpace(titleHint))
	}
	return r.db.Model(&model.AIConversation{}).
		Where("id = ?", strings.TrimSpace(conversationID)).
		Updates(updates).Error
}

func (r *AIConversationRepository) DeleteByID(id string) error {
	return r.db.Where("id = ?", strings.TrimSpace(id)).Delete(&model.AIConversation{}).Error
}

type AIChatMessageRepository struct {
	db *gorm.DB
}

func NewAIChatMessageRepository(db *gorm.DB) *AIChatMessageRepository {
	return &AIChatMessageRepository{db: db}
}

func (r *AIChatMessageRepository) Create(message *model.AIChatMessage) error {
	return r.db.Create(message).Error
}

func (r *AIChatMessageRepository) ListByConversationBefore(conversationID string, before *MessageCursor, limit int) ([]model.AIChatMessage, bool, error) {
	q := r.db.Model(&model.AIChatMessage{}).Where("conversation_id = ?", strings.TrimSpace(conversationID))
	if before != nil {
		q = q.Where("(created_at < ?) OR (created_at = ? AND id < ?)", before.CreatedAt.UTC(), before.CreatedAt.UTC(), strings.TrimSpace(before.ID))
	}

	var messages []model.AIChatMessage
	err := q.Order("created_at DESC").Order("id DESC").Limit(limit + 1).Find(&messages).Error
	if err != nil {
		return nil, false, err
	}

	hasMore := len(messages) > limit
	if hasMore {
		messages = messages[:limit]
	}
	return messages, hasMore, nil
}

// ListRecentByConversation returns recent messages in chronological order.
func (r *AIChatMessageRepository) ListRecentByConversation(conversationID string, limit int) ([]model.AIChatMessage, error) {
	if limit <= 0 {
		return []model.AIChatMessage{}, nil
	}

	var messages []model.AIChatMessage
	err := r.db.Model(&model.AIChatMessage{}).
		Where("conversation_id = ?", strings.TrimSpace(conversationID)).
		Order("created_at DESC").
		Order("id DESC").
		Limit(limit).
		Find(&messages).Error
	if err != nil {
		return nil, err
	}

	for i, j := 0, len(messages)-1; i < j; i, j = i+1, j-1 {
		messages[i], messages[j] = messages[j], messages[i]
	}
	return messages, nil
}
