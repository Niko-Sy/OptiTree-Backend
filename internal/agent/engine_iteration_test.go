package agent

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"optitree-backend/internal/ai"
	"optitree-backend/internal/config"
	"optitree-backend/internal/model"

	"gorm.io/datatypes"
)

func TestRequestIterationContinuation_Approved(t *testing.T) {
	svc := &AgentService{cfg: config.AgentConfig{ConfirmTimeout: time.Second}}
	session := NewAgentSession("s1", "c1", "p1", "u1", "faultTree", time.Minute)

	events := make([]string, 0, 4)
	writeEvent := func(payload map[string]interface{}) bool {
		t, _ := payload["type"].(string)
		if t != "" {
			events = append(events, t)
		}
		return true
	}

	go func() {
		time.Sleep(10 * time.Millisecond)
		session.confirmCh <- ConfirmSignal{CallID: "iter_limit_10_10", Approved: true, ContinueRounds: 3}
	}()

	continueRounds, approved, err := svc.requestIterationContinuation(context.Background(), session, 10, 10, writeEvent)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !approved {
		t.Fatal("expected approved=true")
	}
	if continueRounds != 3 {
		t.Fatalf("expected continueRounds=3, got %d", continueRounds)
	}

	if len(events) < 2 || events[0] != "iteration_limit_reached" || events[1] != "iteration_resumed" {
		t.Fatalf("unexpected event sequence: %v", events)
	}
	if session.State() != StateRunning {
		t.Fatalf("expected session running, got %s", session.State())
	}
	if snap := session.Snapshot(); snap.PendingCallID != "" || snap.PendingTool != "" {
		t.Fatalf("expected pending info cleared, got %+v", snap)
	}
}

func TestRequestIterationContinuation_Rejected(t *testing.T) {
	svc := &AgentService{cfg: config.AgentConfig{ConfirmTimeout: time.Second}}
	session := NewAgentSession("s2", "c1", "p1", "u1", "faultTree", time.Minute)

	events := make([]string, 0, 4)
	writeEvent := func(payload map[string]interface{}) bool {
		t, _ := payload["type"].(string)
		if t != "" {
			events = append(events, t)
		}
		return true
	}

	go func() {
		time.Sleep(10 * time.Millisecond)
		session.confirmCh <- ConfirmSignal{CallID: "iter_limit_5_5", Approved: false}
	}()

	continueRounds, approved, err := svc.requestIterationContinuation(context.Background(), session, 5, 5, writeEvent)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if approved {
		t.Fatal("expected approved=false")
	}
	if continueRounds != 0 {
		t.Fatalf("expected continueRounds=0, got %d", continueRounds)
	}

	if len(events) < 2 || events[0] != "iteration_limit_reached" || events[1] != "iteration_stopped" {
		t.Fatalf("unexpected event sequence: %v", events)
	}
}

func TestWaitForConfirmation_StrictCallIDMatch(t *testing.T) {
	svc := &AgentService{cfg: config.AgentConfig{ConfirmTimeout: time.Second}}
	session := NewAgentSession("s3", "c1", "p1", "u1", "faultTree", time.Minute)
	session.SetPending("call_expected", "delete_node", nil)

	go func() {
		time.Sleep(10 * time.Millisecond)
		session.confirmCh <- ConfirmSignal{CallID: "", Approved: true}
		time.Sleep(10 * time.Millisecond)
		session.confirmCh <- ConfirmSignal{CallID: "call_expected", Approved: true}
	}()

	signal, err := svc.waitForConfirmation(context.Background(), session, time.Second)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if signal.CallID != "call_expected" || !signal.Approved {
		t.Fatalf("expected strict match signal, got %+v", signal)
	}
}

func TestNormalizeToolFinalStatus(t *testing.T) {
	cases := map[string]string{
		"success":       toolStatusSuccess,
		"failed":        toolStatusFailed,
		"cancelled":     toolStatusCancelled,
		"discarded":     toolStatusDiscarded,
		"client_only":   toolStatusClientOnly,
		"pending":       toolStatusPending,
		"unknown_value": toolStatusSuccess,
	}

	for in, want := range cases {
		got := normalizeToolFinalStatus(in)
		if got != want {
			t.Fatalf("normalizeToolFinalStatus(%q)=%q, want %q", in, got, want)
		}
	}
}

func TestBuildAssistantToolHistoryMessage(t *testing.T) {
	toolCalls := []ai.ToolCall{{
		ID:        "call_1",
		Name:      "add_node",
		Arguments: json.RawMessage(`{"nodeType":"basic"}`),
	}}

	msg, ok := buildAssistantToolHistoryMessage("", "reasoning", toolCalls)
	if !ok {
		t.Fatal("expected assistant history message")
	}
	if msg.Role != "assistant" {
		t.Fatalf("expected role assistant, got %q", msg.Role)
	}
	if msg.ReasoningContent != "reasoning" {
		t.Fatalf("unexpected reasoning content: %q", msg.ReasoningContent)
	}
	if len(msg.ToolCalls) != 1 {
		t.Fatalf("expected 1 tool call, got %d", len(msg.ToolCalls))
	}
	if msg.ToolCalls[0].ID != "call_1" || msg.ToolCalls[0].Name != "add_node" {
		t.Fatalf("unexpected tool call: %+v", msg.ToolCalls[0])
	}
}

func TestBuildToolResultHistoryMessage(t *testing.T) {
	msg := buildToolResultHistoryMessage("call_1", "move_node", toolStatusFailed, "move_node: invalid target", nil, "invalid target")
	if msg.Role != "tool" {
		t.Fatalf("expected tool role, got %q", msg.Role)
	}
	if msg.ToolCallID != "call_1" {
		t.Fatalf("expected tool call id call_1, got %q", msg.ToolCallID)
	}

	var payload map[string]interface{}
	if err := json.Unmarshal([]byte(msg.Content), &payload); err != nil {
		t.Fatalf("tool result content should be valid json: %v", err)
	}
	if payload["tool"] != "move_node" {
		t.Fatalf("unexpected tool field: %#v", payload["tool"])
	}
	if payload["status"] != toolStatusFailed {
		t.Fatalf("unexpected status field: %#v", payload["status"])
	}
	if payload["error"] != "invalid target" {
		t.Fatalf("unexpected error field: %#v", payload["error"])
	}
}

func TestBuildToolResultHistoryMessage_RedactsRawReadPayload(t *testing.T) {
	rawSummary := `{"nodes":[{"id":"n1","name":"泄露检查"},{"id":"n2","name":"子节点"}],"edges":[{"id":"e1","source":"n1","target":"n2"}],"issues":[{"code":"GATE_CHILD_COUNT_TOO_LOW","nodeId":"n1"}]}`
	msg := buildToolResultHistoryMessage("call_read", "get_graph_snapshot", toolStatusSuccess, rawSummary, nil, "")

	if strings.Contains(msg.Content, `"nodes"`) || strings.Contains(msg.Content, `"edges"`) || strings.Contains(msg.Content, "泄露检查") {
		t.Fatalf("tool history should contain compact observation, not raw graph payload: %s", msg.Content)
	}

	var payload ToolObservationSummary
	if err := json.Unmarshal([]byte(msg.Content), &payload); err != nil {
		t.Fatalf("tool result content should be compact json: %v", err)
	}
	if payload.NodeCount != 2 || payload.EdgeCount != 1 || payload.IssueCount != 1 {
		t.Fatalf("unexpected compact counts: %+v", payload)
	}
	if len(payload.IssueCodes) != 1 || payload.IssueCodes[0] != "GATE_CHILD_COUNT_TOO_LOW" {
		t.Fatalf("unexpected issue codes: %+v", payload.IssueCodes)
	}
}

func TestBuildAutoReadCallForMutation_TargetAware(t *testing.T) {
	call := ai.ToolCall{
		Name:      "update_node",
		Arguments: json.RawMessage(`{"nodeId":"gate_1","name":"Gate"}`),
	}
	readCall := buildAutoReadCallForMutation(call)
	if readCall.Name != "get_node_detail" {
		t.Fatalf("expected targeted get_node_detail, got %s", readCall.Name)
	}
	if !strings.Contains(string(readCall.Arguments), `"gate_1"`) {
		t.Fatalf("expected node id in auto read args, got %s", string(readCall.Arguments))
	}

	batchCall := ai.ToolCall{
		Name:      "batch_operations",
		Arguments: json.RawMessage(`{"operations":[{"tool":"move_node","args":{"newParentId":"gate_2"}}]}`),
	}
	readCall = buildAutoReadCallForMutation(batchCall)
	if readCall.Name != "get_node_detail" || !strings.Contains(string(readCall.Arguments), `"gate_2"`) {
		t.Fatalf("expected batch auto read to target gate_2, got %s %s", readCall.Name, string(readCall.Arguments))
	}

	untargeted := buildAutoReadCallForMutation(ai.ToolCall{Name: "add_node", Arguments: json.RawMessage(`{"name":"A"}`)})
	if untargeted.Name != "get_graph_snapshot" {
		t.Fatalf("expected fallback get_graph_snapshot, got %s", untargeted.Name)
	}
	if !strings.Contains(string(untargeted.Arguments), `"maxNodes":120`) || !strings.Contains(string(untargeted.Arguments), `"maxEdges":200`) {
		t.Fatalf("expected bounded snapshot args, got %s", string(untargeted.Arguments))
	}
}

func TestResolveAgentModel_PrefersInputThenConfig(t *testing.T) {
	svc := &AgentService{cfg: config.AgentConfig{AgentModel: "agent-default"}}
	if got := svc.resolveAgentModel(" user-model "); got != "user-model" {
		t.Fatalf("expected explicit model to win, got %q", got)
	}
	if got := svc.resolveAgentModel(""); got != "agent-default" {
		t.Fatalf("expected configured agent model, got %q", got)
	}
	if got := (*AgentService)(nil).resolveAgentModel(""); got != "" {
		t.Fatalf("expected nil service to return empty provider default, got %q", got)
	}
}

func TestBuildToolCallResultEvent_HasSchemaVersion(t *testing.T) {
	event := buildToolCallResultEvent("call_1", "update_node", toolStatusSuccess, true, "ok", "", nil)
	if event["type"] != "tool_call_result" {
		t.Fatalf("unexpected type: %#v", event["type"])
	}
	if event["schemaVersion"] != toolCallResultSchemaV1 {
		t.Fatalf("unexpected schema version: %#v", event["schemaVersion"])
	}
	if event["status"] != toolStatusSuccess {
		t.Fatalf("unexpected status: %#v", event["status"])
	}
}

func TestToPersistedAIChatMessage_AssistantWithToolCalls(t *testing.T) {
	history := ai.ChatHistoryMessage{
		Role:             "assistant",
		Content:          "",
		ReasoningContent: "reasoning",
		ToolCalls: []ai.ToolCall{{
			ID:        "call_1",
			Name:      "add_node",
			Arguments: json.RawMessage(`{"nodeType":"basic"}`),
		}},
	}

	msg, ok, err := toPersistedAIChatMessage("conv_1", history, time.Now().UTC())
	if err != nil {
		t.Fatalf("toPersistedAIChatMessage error: %v", err)
	}
	if !ok || msg == nil {
		t.Fatal("expected persisted message")
	}
	if msg.Role != "assistant" {
		t.Fatalf("unexpected role: %q", msg.Role)
	}
	if msg.ReasoningContent == nil || *msg.ReasoningContent != "reasoning" {
		t.Fatalf("unexpected reasoning content: %#v", msg.ReasoningContent)
	}

	calls := parseToolCallsFromStorage(msg.ToolCalls)
	if len(calls) != 1 {
		t.Fatalf("expected one stored tool call, got %d", len(calls))
	}
	if calls[0].ID != "call_1" || calls[0].Name != "add_node" {
		t.Fatalf("unexpected stored call: %+v", calls[0])
	}
}

func TestToChatHistory_RebuildsAssistantToolChain(t *testing.T) {
	reasoning := "reasoning"
	toolCallID := "call_1"
	messages := []model.AIChatMessage{
		{
			Role:             "assistant",
			Content:          "",
			ReasoningContent: &reasoning,
			ToolCalls:        datatypes.JSON([]byte(`[{"id":"call_1","name":"add_node","arguments":{"nodeType":"basic"}}]`)),
		},
		{
			Role:       "tool",
			Content:    `{"status":"success"}`,
			ToolCallID: &toolCallID,
		},
	}

	history := toChatHistory(messages)
	if len(history) != 2 {
		t.Fatalf("expected 2 history messages, got %d", len(history))
	}
	if history[0].Role != "assistant" || len(history[0].ToolCalls) != 1 {
		t.Fatalf("unexpected assistant history: %+v", history[0])
	}
	if history[1].Role != "tool" || history[1].ToolCallID != "call_1" {
		t.Fatalf("unexpected tool history: %+v", history[1])
	}
}
