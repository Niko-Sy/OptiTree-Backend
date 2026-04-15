package agent

import (
	"encoding/json"
	"errors"
	"strings"
	"sync"

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
	NodeID   string
	GateType string
}

// SafetyController enforces runtime limits for one agent session.
type SafetyController struct {
	cfg config.AgentConfig

	mu            sync.Mutex
	currentRound  map[string]int
	roundCalls    map[string]map[string]int
	historyCalls  map[string][]callFingerprint
	nodeMutations map[string]int
}

func NewSafetyController(cfg config.AgentConfig) *SafetyController {
	return &SafetyController{
		cfg:           cfg,
		currentRound:  make(map[string]int),
		roundCalls:    make(map[string]map[string]int),
		historyCalls:  make(map[string][]callFingerprint),
		nodeMutations: make(map[string]int),
	}
}

func (c *SafetyController) BeginRound(sessionID string, round int) {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	if c.currentRound[sessionID] == round {
		if _, ok := c.roundCalls[sessionID]; !ok {
			c.roundCalls[sessionID] = make(map[string]int)
		}
		return
	}
	c.currentRound[sessionID] = round
	c.roundCalls[sessionID] = make(map[string]int)
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

	sessionID := strings.TrimSpace(session.ID)
	if sessionID == "" {
		return ErrAgentSessionInactive
	}
	kind := classifyToolKind(call.Name)

	c.mu.Lock()
	defer c.mu.Unlock()

	if c.cfg.ToolCallRateLimit > 0 {
		if _, ok := c.roundCalls[sessionID]; !ok {
			c.roundCalls[sessionID] = make(map[string]int)
		}
		used := c.roundCalls[sessionID][kind]
		if used >= c.cfg.ToolCallRateLimit {
			return ErrAgentRateLimited
		}
		c.roundCalls[sessionID][kind] = used + 1
	}

	fingerprint := callFingerprint{
		ToolName: strings.TrimSpace(call.Name),
		ArgsHash: normalizeArgsHash(call.Arguments),
	}
	if strings.EqualFold(strings.TrimSpace(call.Name), "update_gate") {
		var gateArgs struct {
			NodeID   string `json:"nodeId"`
			GateType string `json:"gateType"`
		}
		if err := json.Unmarshal(call.Arguments, &gateArgs); err == nil {
			fingerprint.NodeID = strings.TrimSpace(gateArgs.NodeID)
			fingerprint.GateType = strings.ToUpper(strings.TrimSpace(gateArgs.GateType))
		}
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
	if len(history) >= 4 {
		a := history[len(history)-1]
		b := history[len(history)-2]
		c2 := history[len(history)-3]
		d := history[len(history)-4]
		if a == c2 && b == d {
			return ErrAgentLoopDetected
		}
		if isReadValidateAlternatingLoop(d, c2, b, a) {
			return ErrAgentLoopDetected
		}
		if isGateTypeToggleLoop(d, c2, b, a) {
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

func (c *SafetyController) RecordMutation(sessionID string, changedNodes int) error {
	if changedNodes <= 0 || c.cfg.MaxNodesPerSession <= 0 {
		return nil
	}
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return ErrAgentSessionInactive
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	next := c.nodeMutations[sessionID] + changedNodes
	if next > c.cfg.MaxNodesPerSession {
		return ErrAgentNodeLimitExceeded
	}
	c.nodeMutations[sessionID] = next
	return nil
}

func (c *SafetyController) ClearSession(sessionID string) {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.currentRound, sessionID)
	delete(c.roundCalls, sessionID)
	delete(c.historyCalls, sessionID)
	delete(c.nodeMutations, sessionID)
}

func classifyToolKind(toolName string) string {
	name := strings.ToLower(strings.TrimSpace(toolName))
	switch name {
	case "get_graph_snapshot", "get_node_detail", "get_subtree":
		return "read"
	case "check_gate_semantics", "validate_fta_constraints":
		return "validate"
	case "update_node", "update_gate", "add_node", "add_gate", "delete_node", "move_node", "batch_operations":
		return "mutation"
	default:
		return "other"
	}
}

func isReadValidateAlternatingLoop(a, b, c, d callFingerprint) bool {
	kinds := []string{
		classifyToolKind(a.ToolName),
		classifyToolKind(b.ToolName),
		classifyToolKind(c.ToolName),
		classifyToolKind(d.ToolName),
	}
	for _, kind := range kinds {
		if kind == "mutation" {
			return false
		}
	}
	return (kinds[0] == "read" && kinds[1] == "validate" && kinds[2] == "read" && kinds[3] == "validate") ||
		(kinds[0] == "validate" && kinds[1] == "read" && kinds[2] == "validate" && kinds[3] == "read")
}

func isGateTypeToggleLoop(a, b, c, d callFingerprint) bool {
	if !strings.EqualFold(a.ToolName, "update_gate") || !strings.EqualFold(b.ToolName, "update_gate") || !strings.EqualFold(c.ToolName, "update_gate") || !strings.EqualFold(d.ToolName, "update_gate") {
		return false
	}
	if a.NodeID == "" || a.NodeID != b.NodeID || a.NodeID != c.NodeID || a.NodeID != d.NodeID {
		return false
	}
	if a.GateType == "" || b.GateType == "" || c.GateType == "" || d.GateType == "" {
		return false
	}
	return a.GateType == c.GateType && b.GateType == d.GateType && a.GateType != b.GateType
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
