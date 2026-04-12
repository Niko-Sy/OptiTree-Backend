package agent

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"time"
)

const (
	StateRunning          = "running"
	StatePausedForConfirm = "paused_confirm"
	StatePausedForPreview = "paused_preview"
	StateDone             = "done"
	StateCancelled        = "cancelled"
)

var (
	ErrSessionNotFound       = errors.New("agent session not found")
	ErrSessionClosed         = errors.New("agent session already closed")
	ErrSessionNotWaiting     = errors.New("agent session not waiting for confirmation")
	ErrSessionAlreadyExists  = errors.New("agent session already exists")
	ErrSessionConfirmTimeout = errors.New("agent session confirmation timeout")
)

// ConfirmSignal carries user confirmation for dangerous or preview operations.
type ConfirmSignal struct {
	CallID         string   `json:"callId"`
	Approved       bool     `json:"approved"`
	ApprovedOps    []string `json:"approvedOps,omitempty"`
	ContinueRounds int      `json:"continueRounds,omitempty"`
}

// AgentSession keeps in-memory runtime state for one streaming run.
type AgentSession struct {
	ID             string
	ConversationID string
	ProjectID      string
	UserID         string
	GraphType      string

	PendingCallID string
	PendingTool   string
	PendingArgs   json.RawMessage

	confirmCh  chan ConfirmSignal
	cancelCh   chan struct{}
	cancelOnce sync.Once

	createdAt time.Time
	expiresAt time.Time

	mu            sync.RWMutex
	state         string
	toolCallCount int
	serverOps     int
	clientOps     int
	hybridOps     int
	tokensUsed    int
}

// SessionSnapshot is a thread-safe view used by status APIs.
type SessionSnapshot struct {
	SessionID      string    `json:"sessionId"`
	ConversationID string    `json:"conversationId"`
	ProjectID      string    `json:"projectId"`
	UserID         string    `json:"userId"`
	GraphType      string    `json:"graphType"`
	State          string    `json:"state"`
	PendingCallID  string    `json:"pendingCallId,omitempty"`
	PendingTool    string    `json:"pendingTool,omitempty"`
	ToolCallCount  int       `json:"toolCallCount"`
	ServerOps      int       `json:"serverOps"`
	ClientOps      int       `json:"clientOps"`
	HybridOps      int       `json:"hybridOps"`
	TokensUsed     int       `json:"tokensUsed"`
	CreatedAt      time.Time `json:"createdAt"`
	ExpiresAt      time.Time `json:"expiresAt"`
}

func NewAgentSession(id, conversationID, projectID, userID, graphType string, ttl time.Duration) *AgentSession {
	if ttl <= 0 {
		ttl = 30 * time.Minute
	}
	now := time.Now().UTC()
	return &AgentSession{
		ID:             strings.TrimSpace(id),
		ConversationID: strings.TrimSpace(conversationID),
		ProjectID:      strings.TrimSpace(projectID),
		UserID:         strings.TrimSpace(userID),
		GraphType:      strings.TrimSpace(graphType),
		state:          StateRunning,
		confirmCh:      make(chan ConfirmSignal),
		cancelCh:       make(chan struct{}),
		createdAt:      now,
		expiresAt:      now.Add(ttl),
	}
}

func (s *AgentSession) ConfirmChan() <-chan ConfirmSignal { return s.confirmCh }
func (s *AgentSession) CancelChan() <-chan struct{}       { return s.cancelCh }

func (s *AgentSession) Snapshot() SessionSnapshot {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return SessionSnapshot{
		SessionID:      s.ID,
		ConversationID: s.ConversationID,
		ProjectID:      s.ProjectID,
		UserID:         s.UserID,
		GraphType:      s.GraphType,
		State:          s.state,
		PendingCallID:  s.PendingCallID,
		PendingTool:    s.PendingTool,
		ToolCallCount:  s.toolCallCount,
		ServerOps:      s.serverOps,
		ClientOps:      s.clientOps,
		HybridOps:      s.hybridOps,
		TokensUsed:     s.tokensUsed,
		CreatedAt:      s.createdAt,
		ExpiresAt:      s.expiresAt,
	}
}

func (s *AgentSession) State() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.state
}

func (s *AgentSession) SetState(state string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.state = strings.TrimSpace(state)
}

func (s *AgentSession) SetPending(callID, toolName string, args json.RawMessage) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.PendingCallID = strings.TrimSpace(callID)
	s.PendingTool = strings.TrimSpace(toolName)
	s.PendingArgs = args
}

func (s *AgentSession) ClearPending() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.PendingCallID = ""
	s.PendingTool = ""
	s.PendingArgs = nil
}

func (s *AgentSession) IncToolCallCount() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.toolCallCount++
}

func (s *AgentSession) ToolCallCount() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.toolCallCount
}

func (s *AgentSession) IncTierOps(tier string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	switch strings.ToLower(strings.TrimSpace(tier)) {
	case "server":
		s.serverOps++
	case "client":
		s.clientOps++
	case "hybrid":
		s.hybridOps++
	}
}

func (s *AgentSession) AddTokens(tokens int) {
	if tokens <= 0 {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.tokensUsed += tokens
}

func (s *AgentSession) MarkCancelled() {
	s.mu.Lock()
	s.state = StateCancelled
	s.mu.Unlock()
	s.cancelOnce.Do(func() {
		close(s.cancelCh)
	})
}

func (s *AgentSession) IsExpired(now time.Time) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return now.After(s.expiresAt)
}

// AgentSessionManager manages active sessions in memory.
type AgentSessionManager struct {
	sessionTTL time.Duration
	mu         sync.RWMutex
	sessions   map[string]*AgentSession
}

func NewAgentSessionManager(sessionTTL time.Duration) *AgentSessionManager {
	if sessionTTL <= 0 {
		sessionTTL = 30 * time.Minute
	}
	return &AgentSessionManager{
		sessionTTL: sessionTTL,
		sessions:   make(map[string]*AgentSession),
	}
}

func (m *AgentSessionManager) Create(session *AgentSession) error {
	if session == nil || strings.TrimSpace(session.ID) == "" {
		return ErrSessionNotFound
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	if _, exists := m.sessions[session.ID]; exists {
		return ErrSessionAlreadyExists
	}
	m.sessions[session.ID] = session
	return nil
}

func (m *AgentSessionManager) NewSession(id, conversationID, projectID, userID, graphType string) *AgentSession {
	return NewAgentSession(id, conversationID, projectID, userID, graphType, m.sessionTTL)
}

func (m *AgentSessionManager) Get(sessionID string) (*AgentSession, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	s, ok := m.sessions[strings.TrimSpace(sessionID)]
	return s, ok
}

func (m *AgentSessionManager) Confirm(sessionID string, signal ConfirmSignal) error {
	session, ok := m.Get(sessionID)
	if !ok {
		return ErrSessionNotFound
	}
	if session.State() == StateCancelled || session.State() == StateDone {
		return ErrSessionClosed
	}

	select {
	case <-session.CancelChan():
		return ErrSessionClosed
	case session.confirmCh <- signal:
		return nil
	default:
		return ErrSessionNotWaiting
	}
}

func (m *AgentSessionManager) Cancel(sessionID string) error {
	session, ok := m.Get(sessionID)
	if !ok {
		return ErrSessionNotFound
	}
	session.MarkCancelled()
	return nil
}

func (m *AgentSessionManager) Remove(sessionID string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.sessions, strings.TrimSpace(sessionID))
}

func (m *AgentSessionManager) StartCleanup(ctx context.Context) {
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			now := time.Now().UTC()
			m.mu.Lock()
			for id, session := range m.sessions {
				if session == nil || session.IsExpired(now) {
					delete(m.sessions, id)
				}
			}
			m.mu.Unlock()
		}
	}
}
