package agent

import (
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"time"

	"optitree-backend/internal/ai"
	"optitree-backend/internal/config"
)

var (
	ErrAgentMaxRoundsExceeded = errors.New("agent max rounds exceeded")
	ErrAgentMaxToolCalls      = errors.New("agent max tool calls exceeded")
	ErrAgentRateLimited       = errors.New("agent tool call rate limited")
	ErrAgentLoopDetected      = errors.New("agent repetitive loop detected")
	ErrAgentNodeLimitExceeded = errors.New("agent node mutation limit exceeded")
	ErrAgentSessionInactive   = errors.New("agent session is not active")
)

type callFingerprint struct {
	ToolName string
	ArgsHash string
}

// SafetyController enforces runtime limits for one agent session.
type SafetyController struct {
	cfg config.AgentConfig

	mu            sync.Mutex
	rateWindowSec time.Duration
	recentCalls   map[string][]time.Time
	historyCalls  map[string][]callFingerprint
	nodeMutations map[string]int
}

func NewSafetyController(cfg config.AgentConfig) *SafetyController {
	return &SafetyController{
		cfg:           cfg,
		rateWindowSec: 10 * time.Second,
		recentCalls:   make(map[string][]time.Time),
		historyCalls:  make(map[string][]callFingerprint),
		nodeMutations: make(map[string]int),
	}
}

func (c *SafetyController) CheckRound(round int) error {
	return c.CheckRoundWithLimit(round, c.cfg.MaxRounds)
}

func (c *SafetyController) CheckRoundWithLimit(round int, roundLimit int) error {
	if roundLimit > 0 && round >= roundLimit {
		return ErrAgentMaxRoundsExceeded
	}
	return nil
}

func (c *SafetyController) CheckToolCall(session *AgentSession, call ai.ToolCall, estimatedNodeMutations int) error {
	if session == nil {
		return ErrAgentSessionInactive
	}

	state := session.State()
	if state == StateCancelled || state == StateDone {
		return ErrAgentSessionInactive
	}

	if c.cfg.MaxToolCalls > 0 && session.ToolCallCount() >= c.cfg.MaxToolCalls {
		return ErrAgentMaxToolCalls
	}

	now := time.Now().UTC()
	sessionID := strings.TrimSpace(session.ID)
	if sessionID == "" {
		return ErrAgentSessionInactive
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	if c.cfg.ToolCallRateLimit > 0 {
		history := c.recentCalls[sessionID]
		threshold := now.Add(-c.rateWindowSec)
		trimmed := history[:0]
		for _, ts := range history {
			if ts.After(threshold) {
				trimmed = append(trimmed, ts)
			}
		}
		if len(trimmed) >= c.cfg.ToolCallRateLimit {
			c.recentCalls[sessionID] = trimmed
			return ErrAgentRateLimited
		}
		trimmed = append(trimmed, now)
		c.recentCalls[sessionID] = trimmed
	}

	fingerprint := callFingerprint{
		ToolName: strings.TrimSpace(call.Name),
		ArgsHash: normalizeArgsHash(call.Arguments),
	}
	if fingerprint.ToolName == "" {
		fingerprint.ToolName = "unknown"
	}

	history := c.historyCalls[sessionID]
	history = append(history, fingerprint)
	if len(history) > 8 {
		history = history[len(history)-8:]
	}
	c.historyCalls[sessionID] = history

	if len(history) >= 3 {
		a := history[len(history)-1]
		b := history[len(history)-2]
		d := history[len(history)-3]
		if a == b && b == d {
			return ErrAgentLoopDetected
		}
	}

	if estimatedNodeMutations > 0 && c.cfg.MaxNodesPerSession > 0 {
		next := c.nodeMutations[sessionID] + estimatedNodeMutations
		if next > c.cfg.MaxNodesPerSession {
			return ErrAgentNodeLimitExceeded
		}
		c.nodeMutations[sessionID] = next
	}

	return nil
}

func (c *SafetyController) ClearSession(sessionID string) {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.recentCalls, sessionID)
	delete(c.historyCalls, sessionID)
	delete(c.nodeMutations, sessionID)
}

func normalizeArgsHash(raw json.RawMessage) string {
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" {
		return "{}"
	}
	var obj interface{}
	if err := json.Unmarshal(raw, &obj); err != nil {
		return trimmed
	}
	canonical, err := json.Marshal(obj)
	if err != nil {
		return trimmed
	}
	return string(canonical)
}
