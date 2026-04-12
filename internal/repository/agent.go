package repository

import (
	"encoding/json"
	"errors"
	"strings"
	"time"

	"optitree-backend/internal/model"

	"gorm.io/gorm"
)

type AgentSessionRepository struct {
	db *gorm.DB
}

func NewAgentSessionRepository(db *gorm.DB) *AgentSessionRepository {
	return &AgentSessionRepository{db: db}
}

func (r *AgentSessionRepository) Create(tx *gorm.DB, session *model.AgentSession) error {
	if tx == nil {
		tx = r.db
	}
	return tx.Create(session).Error
}

func (r *AgentSessionRepository) Update(tx *gorm.DB, session *model.AgentSession) error {
	if tx == nil {
		tx = r.db
	}
	return tx.Save(session).Error
}

func (r *AgentSessionRepository) FindByID(id string) (*model.AgentSession, error) {
	var session model.AgentSession
	err := r.db.Where("id = ?", strings.TrimSpace(id)).Take(&session).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &session, nil
}

func (r *AgentSessionRepository) UpdateState(id, state string, errorMessage *string, endedAt *time.Time) error {
	updates := map[string]interface{}{
		"state":      strings.TrimSpace(state),
		"updated_at": time.Now().UTC(),
	}
	if errorMessage != nil {
		updates["error_message"] = strings.TrimSpace(*errorMessage)
	}
	if endedAt != nil {
		updates["ended_at"] = endedAt.UTC()
	}
	return r.db.Model(&model.AgentSession{}).Where("id = ?", strings.TrimSpace(id)).Updates(updates).Error
}

type AgentToolCallRepository struct {
	db *gorm.DB
}

func NewAgentToolCallRepository(db *gorm.DB) *AgentToolCallRepository {
	return &AgentToolCallRepository{db: db}
}

func (r *AgentToolCallRepository) Create(tx *gorm.DB, call *model.AgentToolCall) error {
	if tx == nil {
		tx = r.db
	}
	if call != nil {
		if len(call.Arguments) == 0 || !json.Valid(call.Arguments) {
			call.Arguments = []byte("{}")
		}
		if len(call.PatchJSON) == 0 || !json.Valid(call.PatchJSON) {
			call.PatchJSON = []byte("{}")
		}
	}
	return tx.Create(call).Error
}

func (r *AgentToolCallRepository) Update(tx *gorm.DB, call *model.AgentToolCall) error {
	if tx == nil {
		tx = r.db
	}
	return tx.Save(call).Error
}

func (r *AgentToolCallRepository) UpdateStatus(id, status string, result *string, patchJSON []byte, errorMsg *string) error {
	updates := map[string]interface{}{
		"status":      strings.TrimSpace(status),
		"finished_at": time.Now().UTC(),
	}
	if result != nil {
		updates["result"] = *result
	}
	if len(patchJSON) > 0 && json.Valid(patchJSON) {
		updates["patch_json"] = patchJSON
	} else {
		updates["patch_json"] = []byte("{}")
	}
	if errorMsg != nil {
		updates["error_msg"] = strings.TrimSpace(*errorMsg)
	}
	return r.db.Model(&model.AgentToolCall{}).Where("id = ?", strings.TrimSpace(id)).Updates(updates).Error
}

func (r *AgentToolCallRepository) ListBySession(sessionID string, limit int) ([]model.AgentToolCall, error) {
	if limit <= 0 {
		limit = 100
	}
	var calls []model.AgentToolCall
	err := r.db.Where("session_id = ?", strings.TrimSpace(sessionID)).
		Order("created_at ASC").
		Limit(limit).
		Find(&calls).Error
	if err != nil {
		return nil, err
	}
	return calls, nil
}
