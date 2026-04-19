package agent

import (
	"encoding/json"
	"errors"
	"fmt"
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

type ToolCallWarning struct {
	Code    string
	Message string
}

const (
	safetyHistoryWindow         = 16
	maxLoopSoftWarningsPerKey   = 2
	readValidateStreakThreshold = 4
)

// SafetyController enforces runtime limits for one agent session.
type SafetyController struct {
	cfg config.AgentConfig

	mu            sync.Mutex
	currentRound  map[string]int
	roundCalls    map[string]map[string]int
	historyCalls  map[string][]callFingerprint
	nodeMutations map[string]int
	loopWarnings  map[string]map[string]int
}

func NewSafetyController(cfg config.AgentConfig) *SafetyController {
	return &SafetyController{
		cfg:           cfg,
		currentRound:  make(map[string]int),
		roundCalls:    make(map[string]map[string]int),
		historyCalls:  make(map[string][]callFingerprint),
		nodeMutations: make(map[string]int),
		loopWarnings:  make(map[string]map[string]int),
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
	_, err := c.CheckToolCallWithWarning(session, call, estimatedNodeMutations)
	return err
}

func (c *SafetyController) CheckToolCallWithWarning(session *AgentSession, call ai.ToolCall, estimatedNodeMutations int) (*ToolCallWarning, error) {
	if session == nil {
		return nil, ErrAgentSessionInactive
	}

	state := session.State()
	if state == StateCancelled || state == StateDone {
		return nil, ErrAgentSessionInactive
	}

	if c.cfg.MaxToolCalls > 0 && session.ToolCallCount() >= c.cfg.MaxToolCalls {
		return nil, ErrAgentMaxToolCalls
	}

	sessionID := strings.TrimSpace(session.ID)
	if sessionID == "" {
		return nil, ErrAgentSessionInactive
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
			return nil, ErrAgentRateLimited
		}
		c.roundCalls[sessionID][kind] = used + 1
	}

	fingerprint := callFingerprintFromToolCall(call)
	if fingerprint.ToolName == "" {
		fingerprint.ToolName = "unknown"
	}

	history := c.historyCalls[sessionID]
	history = append(history, fingerprint)
	if len(history) > safetyHistoryWindow {
		history = history[len(history)-safetyHistoryWindow:]
	}
	c.historyCalls[sessionID] = history

	if c.cfg.EnableLoopSoftWarning {
		if warning := c.buildEarlyDuplicateWarning(sessionID, call, history); warning != nil {
			return warning, nil
		}
		if warning := c.buildReadValidateStreakWarning(sessionID, history); warning != nil {
			return warning, nil
		}
	}

	if len(history) >= 3 {
		a := history[len(history)-1]
		b := history[len(history)-2]
		d := history[len(history)-3]
		if a == b && b == d {
			if c.cfg.EnableLoopSoftWarning && c.consumeLoopWarningToken(sessionID, loopWarningKeyForFingerprint(a)) {
				return &ToolCallWarning{Code: "loop_warning", Message: formatLoopWarningMessage(call.Name)}, nil
			}
			return nil, ErrAgentLoopDetected
		}
	}
	if len(history) >= 4 {
		a := history[len(history)-1]
		b := history[len(history)-2]
		c2 := history[len(history)-3]
		d := history[len(history)-4]
		if isGenericABABLoop(d, c2, b, a) {
			if c.cfg.EnableLoopSoftWarning && c.consumeLoopWarningToken(sessionID, loopWarningKeyForPattern("abab", d, c2, b, a)) {
				return &ToolCallWarning{Code: "loop_warning", Message: formatLoopWarningMessage(call.Name)}, nil
			}
			return nil, ErrAgentLoopDetected
		}
		if isReadValidateAlternatingLoop(d, c2, b, a) {
			if c.cfg.EnableLoopSoftWarning && c.consumeLoopWarningToken(sessionID, loopWarningKeyForPattern("read_validate", d, c2, b, a)) {
				return &ToolCallWarning{Code: "loop_warning", Message: formatLoopWarningMessage(call.Name)}, nil
			}
			return nil, ErrAgentLoopDetected
		}
		if isGateTypeToggleLoop(d, c2, b, a) {
			if c.cfg.EnableLoopSoftWarning && c.consumeLoopWarningToken(sessionID, loopWarningKeyForPattern("gate_toggle", d, c2, b, a)) {
				return &ToolCallWarning{Code: "loop_warning", Message: formatLoopWarningMessage(call.Name)}, nil
			}
			return nil, ErrAgentLoopDetected
		}
	}

	if estimatedNodeMutations > 0 && c.cfg.MaxNodesPerSession > 0 {
		next := c.nodeMutations[sessionID] + estimatedNodeMutations
		if next > c.cfg.MaxNodesPerSession {
			return nil, ErrAgentNodeLimitExceeded
		}
		c.nodeMutations[sessionID] = next
	}

	return nil, nil
}

func isGenericABABLoop(a, b, c, d callFingerprint) bool {
	if !(a == c && b == d) {
		return false
	}

	k1 := classifyToolKind(a.ToolName)
	k2 := classifyToolKind(b.ToolName)
	// Allow read/validate repetitions to be decided by the dedicated, stricter rule.
	if isReadOrValidateKind(k1) && isReadOrValidateKind(k2) {
		return false
	}
	return true
}

func isReadOrValidateKind(kind string) bool {
	return kind == "read" || kind == "validate"
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
	delete(c.loopWarnings, sessionID)
}

func callFingerprintFromToolCall(call ai.ToolCall) callFingerprint {
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
	return fingerprint
}

func (c *SafetyController) buildEarlyDuplicateWarning(sessionID string, call ai.ToolCall, history []callFingerprint) *ToolCallWarning {
	if len(history) < 2 {
		return nil
	}
	last := history[len(history)-1]
	prev := history[len(history)-2]
	if last != prev {
		return nil
	}
	if !c.consumeLoopWarningToken(sessionID, loopWarningKeyForFingerprint(last)) {
		return nil
	}
	return &ToolCallWarning{Code: "loop_warning", Message: formatLoopWarningMessage(call.Name)}
}

func (c *SafetyController) buildReadValidateStreakWarning(sessionID string, history []callFingerprint) *ToolCallWarning {
	if len(history) < readValidateStreakThreshold {
		return nil
	}

	recent := history[len(history)-readValidateStreakThreshold:]
	parts := make([]string, 0, len(recent))
	unique := make(map[string]struct{}, len(recent))
	for _, fp := range recent {
		kind := classifyToolKind(fp.ToolName)
		if kind != "read" && kind != "validate" {
			return nil
		}
		key := callFingerprintKey(fp)
		parts = append(parts, kind+":"+key)
		unique[key] = struct{}{}
	}

	// Exact same-fingerprint repetition is handled by dedicated loop detection.
	if len(unique) < 2 {
		return nil
	}

	key := "read_streak:" + strings.Join(parts, "|")
	if !c.consumeLoopWarningToken(sessionID, key) {
		return nil
	}

	return &ToolCallWarning{
		Code:    "read_streak_warning",
		Message: formatReadValidateStreakWarningMessage(readValidateStreakThreshold),
	}
}

func (c *SafetyController) consumeLoopWarningToken(sessionID, key string) bool {
	sessionID = strings.TrimSpace(sessionID)
	key = strings.TrimSpace(key)
	if sessionID == "" || key == "" {
		return false
	}
	if _, ok := c.loopWarnings[sessionID]; !ok {
		c.loopWarnings[sessionID] = make(map[string]int)
	}
	used := c.loopWarnings[sessionID][key]
	c.loopWarnings[sessionID][key] = used + 1
	return used < maxLoopSoftWarningsPerKey
}

func loopWarningKeyForFingerprint(fp callFingerprint) string {
	return fmt.Sprintf("fp:%s", callFingerprintKey(fp))
}

func loopWarningKeyForPattern(kind string, a, b, c, d callFingerprint) string {
	return fmt.Sprintf("%s:%s|%s|%s|%s", strings.TrimSpace(kind), callFingerprintKey(a), callFingerprintKey(b), callFingerprintKey(c), callFingerprintKey(d))
}

func callFingerprintKey(fp callFingerprint) string {
	return strings.Join([]string{
		strings.TrimSpace(fp.ToolName),
		strings.TrimSpace(fp.ArgsHash),
		strings.TrimSpace(fp.NodeID),
		strings.TrimSpace(fp.GateType),
	}, "#")
}

func formatLoopWarningMessage(toolName string) string {
	name := strings.TrimSpace(toolName)
	if name == "" {
		name = "unknown_tool"
	}
	switch classifyToolKind(name) {
	case "read", "validate":
		return fmt.Sprintf("detected repetitive call pattern around %s; stop repeating read/validate and move to mutation or final answer", name)
	case "mutation":
		return fmt.Sprintf("detected repetitive mutation pattern around %s; change strategy or consolidate via batch_operations", name)
	default:
		return fmt.Sprintf("detected repetitive call pattern around %s; please switch strategy or proceed to the next unresolved step", name)
	}
}

func formatReadValidateStreakWarningMessage(streak int) string {
	if streak <= 0 {
		streak = readValidateStreakThreshold
	}
	return fmt.Sprintf("detected %d consecutive read/validate calls without mutation; stop repeated reads and proceed with mutation tools or provide final answer", streak)
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
	if !(a == c && b == d) {
		return false
	}

	kinds := []string{
		classifyToolKind(a.ToolName),
		classifyToolKind(b.ToolName),
		classifyToolKind(c.ToolName),
		classifyToolKind(d.ToolName),
	}
	for _, kind := range kinds {
		if kind != "read" && kind != "validate" {
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
