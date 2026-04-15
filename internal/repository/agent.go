package repository

import (
	"encoding/json"
	"errors"
	"strings"
	"time"

	"optitree-backend/internal/model"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
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

type AgentSessionRuntimeRepository struct {
	db *gorm.DB
}

func NewAgentSessionRuntimeRepository(db *gorm.DB) *AgentSessionRuntimeRepository {
	return &AgentSessionRuntimeRepository{db: db}
}

func (r *AgentSessionRuntimeRepository) UpsertPending(runtime *model.AgentSessionRuntime) error {
	if runtime == nil {
		return errors.New("runtime is nil")
	}
	runtime.SessionID = strings.TrimSpace(runtime.SessionID)
	if runtime.SessionID == "" {
		return errors.New("session_id is required")
	}
	if strings.TrimSpace(runtime.WaitType) == "" {
		runtime.WaitType = "none"
	}
	if strings.TrimSpace(runtime.WaitStatus) == "" {
		runtime.WaitStatus = "waiting"
	}
	if runtime.LastEventSeq <= 0 {
		runtime.LastEventSeq = 1
	}
	if len(runtime.PendingArgs) == 0 || !json.Valid(runtime.PendingArgs) {
		runtime.PendingArgs = []byte("{}")
	}
	if len(runtime.PendingPreview) == 0 || !json.Valid(runtime.PendingPreview) {
		runtime.PendingPreview = []byte("{}")
	}

	now := time.Now().UTC()
	runtime.UpdatedAt = now
	if runtime.CreatedAt.IsZero() {
		runtime.CreatedAt = now
	}

	return r.db.Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "session_id"}},
		DoUpdates: clause.Assignments(map[string]interface{}{
			"pending_call_id":   runtime.PendingCallID,
			"pending_tool_name": runtime.PendingTool,
			"pending_tier":      runtime.PendingTier,
			"pending_args":      runtime.PendingArgs,
			"pending_preview":   runtime.PendingPreview,
			"wait_type":         runtime.WaitType,
			"wait_status":       runtime.WaitStatus,
			"last_event_seq":    gorm.Expr("agent_session_runtime.last_event_seq + ?", 1),
			"expires_at":        runtime.ExpiresAt,
			"updated_at":        now,
		}),
	}).Create(runtime).Error
}

func (r *AgentSessionRuntimeRepository) ClearPending(sessionID string, waitStatus string) error {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return errors.New("session_id is required")
	}
	waitStatus = normalizeWaitStatus(waitStatus, "cleared")
	return r.db.Model(&model.AgentSessionRuntime{}).
		Where("session_id = ?", sessionID).
		Updates(map[string]interface{}{
			"pending_call_id":   nil,
			"pending_tool_name": nil,
			"pending_tier":      nil,
			"pending_args":      []byte("{}"),
			"pending_preview":   []byte("{}"),
			"wait_type":         "none",
			"wait_status":       waitStatus,
			"expires_at":        nil,
			"last_event_seq":    gorm.Expr("agent_session_runtime.last_event_seq + ?", 1),
			"updated_at":        time.Now().UTC(),
		}).Error
}

func (r *AgentSessionRuntimeRepository) FindBySessionID(sessionID string) (*model.AgentSessionRuntime, error) {
	var runtime model.AgentSessionRuntime
	err := r.db.Where("session_id = ?", strings.TrimSpace(sessionID)).Take(&runtime).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &runtime, nil
}

func (r *AgentSessionRuntimeRepository) MarkWaitStatus(sessionID string, waitStatus string) error {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return errors.New("session_id is required")
	}
	status := strings.TrimSpace(waitStatus)
	if status == "" {
		return errors.New("wait_status is required")
	}
	status = normalizeWaitStatus(status, "waiting")
	updates := map[string]interface{}{
		"wait_status":    status,
		"last_event_seq": gorm.Expr("agent_session_runtime.last_event_seq + ?", 1),
		"updated_at":     time.Now().UTC(),
	}
	if shouldClearPendingForStatus(status) {
		updates["pending_call_id"] = nil
		updates["pending_tool_name"] = nil
		updates["pending_tier"] = nil
		updates["pending_args"] = []byte("{}")
		updates["pending_preview"] = []byte("{}")
		updates["wait_type"] = "none"
		updates["expires_at"] = nil
	}
	return r.db.Model(&model.AgentSessionRuntime{}).
		Where("session_id = ?", sessionID).
		Updates(updates).Error
}

func normalizeWaitStatus(status string, fallback string) string {
	v := strings.ToLower(strings.TrimSpace(status))
	if v == "" {
		v = strings.ToLower(strings.TrimSpace(fallback))
	}
	switch v {
	case "waiting", "approved", "rejected", "timeout", "cleared":
		return v
	default:
		if strings.TrimSpace(fallback) == "" {
			return "waiting"
		}
		return strings.ToLower(strings.TrimSpace(fallback))
	}
}

func shouldClearPendingForStatus(waitStatus string) bool {
	switch strings.ToLower(strings.TrimSpace(waitStatus)) {
	case "approved", "rejected", "timeout", "cleared":
		return true
	default:
		return false
	}
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
