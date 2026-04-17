package agent

import (
	"encoding/json"
	"errors"
	"testing"
	"time"

	"optitree-backend/internal/ai"
	"optitree-backend/internal/config"
)

func TestSafetyController_CheckRound(t *testing.T) {
	s := NewSafetyController(config.AgentConfig{MaxRounds: 2})
	if err := s.CheckRound(1); err != nil {
		t.Fatalf("unexpected error at round 1: %v", err)
	}
	if err := s.CheckRound(2); !errors.Is(err, ErrAgentMaxRoundsExceeded) {
		t.Fatalf("expected ErrAgentMaxRoundsExceeded, got %v", err)
	}
}

func TestSafetyController_MaxToolCalls(t *testing.T) {
	s := NewSafetyController(config.AgentConfig{MaxToolCalls: 1})
	session := NewAgentSession("sid-max", "c1", "p1", "u1", "faultTree", time.Minute)
	session.IncToolCallCount()

	err := s.CheckToolCall(session, ai.ToolCall{Name: "update_node", Arguments: json.RawMessage(`{"nodeId":"n1"}`)}, 1)
	if !errors.Is(err, ErrAgentMaxToolCalls) {
		t.Fatalf("expected ErrAgentMaxToolCalls, got %v", err)
	}
}

func TestSafetyController_RateLimit(t *testing.T) {
	s := NewSafetyController(config.AgentConfig{ToolCallRateLimit: 2, MaxToolCalls: 10})
	session := NewAgentSession("sid-rate", "c1", "p1", "u1", "faultTree", time.Minute)
	call := ai.ToolCall{Name: "update_node", Arguments: json.RawMessage(`{"nodeId":"n1"}`)}
	s.BeginRound(session.ID, 0)

	if err := s.CheckToolCall(session, call, 0); err != nil {
		t.Fatalf("first call should pass, got %v", err)
	}
	if err := s.CheckToolCall(session, call, 0); err != nil {
		t.Fatalf("second call should pass, got %v", err)
	}
	if err := s.CheckToolCall(session, call, 0); !errors.Is(err, ErrAgentRateLimited) {
		t.Fatalf("expected ErrAgentRateLimited, got %v", err)
	}

	s.BeginRound(session.ID, 1)
	nextRoundCall := ai.ToolCall{Name: "update_node", Arguments: json.RawMessage(`{"nodeId":"n1","name":"round2"}`)}
	if err := s.CheckToolCall(session, nextRoundCall, 0); err != nil {
		t.Fatalf("new round should reset category quota, got %v", err)
	}

	readCall := ai.ToolCall{Name: "get_graph_snapshot", Arguments: json.RawMessage(`{}`)}
	if err := s.CheckToolCall(session, readCall, 0); err != nil {
		t.Fatalf("different category should have independent quota, got %v", err)
	}
}

func TestSafetyController_LoopAndNodeLimit(t *testing.T) {
	sLoop := NewSafetyController(config.AgentConfig{MaxToolCalls: 10})
	sessionLoop := NewAgentSession("sid-loop", "c1", "p1", "u1", "faultTree", time.Minute)
	call := ai.ToolCall{Name: "update_node", Arguments: json.RawMessage(`{"nodeId":"n1","name":"A"}`)}

	if err := sLoop.CheckToolCall(sessionLoop, call, 0); err != nil {
		t.Fatalf("first call should pass, got %v", err)
	}
	if err := sLoop.CheckToolCall(sessionLoop, call, 0); err != nil {
		t.Fatalf("second call should pass, got %v", err)
	}
	if err := sLoop.CheckToolCall(sessionLoop, call, 0); !errors.Is(err, ErrAgentLoopDetected) {
		t.Fatalf("expected ErrAgentLoopDetected, got %v", err)
	}

	sNode := NewSafetyController(config.AgentConfig{MaxNodesPerSession: 2, MaxToolCalls: 10})
	sessionNode := NewAgentSession("sid-node", "c1", "p1", "u1", "faultTree", time.Minute)
	varyingCall := ai.ToolCall{Name: "add_node", Arguments: json.RawMessage(`{"name":"x","nodeType":"basicEvent"}`)}
	if err := sNode.CheckToolCall(sessionNode, varyingCall, 2); err != nil {
		t.Fatalf("first mutation should pass, got %v", err)
	}
	varyingCall.Arguments = json.RawMessage(`{"name":"y","nodeType":"basicEvent"}`)
	if err := sNode.CheckToolCall(sessionNode, varyingCall, 1); !errors.Is(err, ErrAgentNodeLimitExceeded) {
		t.Fatalf("expected ErrAgentNodeLimitExceeded, got %v", err)
	}
}

func TestSafetyController_ValidateABAB_DoesNotTriggerLoop(t *testing.T) {
	s := NewSafetyController(config.AgentConfig{MaxToolCalls: 20})
	session := NewAgentSession("sid-validate-abab", "c1", "p1", "u1", "faultTree", time.Minute)

	calls := []ai.ToolCall{
		{Name: "validate_fta_constraints", Arguments: json.RawMessage(`{}`)},
		{Name: "check_gate_semantics", Arguments: json.RawMessage(`{"nodeId":"g1"}`)},
		{Name: "validate_fta_constraints", Arguments: json.RawMessage(`{}`)},
		{Name: "check_gate_semantics", Arguments: json.RawMessage(`{"nodeId":"g1"}`)},
	}

	for i, call := range calls {
		if err := s.CheckToolCall(session, call, 0); err != nil {
			t.Fatalf("validate/check ABAB should not be treated as loop at step %d, got %v", i, err)
		}
	}
}

func TestSafetyController_ReadValidateAlternating_ExactRepeatTriggersLoop(t *testing.T) {
	s := NewSafetyController(config.AgentConfig{MaxToolCalls: 20})
	session := NewAgentSession("sid-read-validate-loop", "c1", "p1", "u1", "faultTree", time.Minute)

	calls := []ai.ToolCall{
		{Name: "get_node_detail", Arguments: json.RawMessage(`{"nodeId":"n1"}`)},
		{Name: "validate_fta_constraints", Arguments: json.RawMessage(`{}`)},
		{Name: "get_node_detail", Arguments: json.RawMessage(`{"nodeId":"n1"}`)},
		{Name: "validate_fta_constraints", Arguments: json.RawMessage(`{}`)},
	}

	for i := 0; i < len(calls)-1; i++ {
		if err := s.CheckToolCall(session, calls[i], 0); err != nil {
			t.Fatalf("pre-loop call %d should pass, got %v", i, err)
		}
	}
	if err := s.CheckToolCall(session, calls[len(calls)-1], 0); !errors.Is(err, ErrAgentLoopDetected) {
		t.Fatalf("expected ErrAgentLoopDetected for exact repeated read/validate loop, got %v", err)
	}
}

func TestSafetyController_ReadValidateAlternating_DifferentReadTargetDoesNotLoop(t *testing.T) {
	s := NewSafetyController(config.AgentConfig{MaxToolCalls: 20})
	session := NewAgentSession("sid-read-validate-diff", "c1", "p1", "u1", "faultTree", time.Minute)

	calls := []ai.ToolCall{
		{Name: "get_node_detail", Arguments: json.RawMessage(`{"nodeId":"n1"}`)},
		{Name: "validate_fta_constraints", Arguments: json.RawMessage(`{}`)},
		{Name: "get_node_detail", Arguments: json.RawMessage(`{"nodeId":"n2"}`)},
		{Name: "validate_fta_constraints", Arguments: json.RawMessage(`{}`)},
	}

	for i, call := range calls {
		if err := s.CheckToolCall(session, call, 0); err != nil {
			t.Fatalf("different read targets should not trigger loop at step %d, got %v", i, err)
		}
	}
}

func TestSafetyController_LoopSoftWarningThenHardStop(t *testing.T) {
	s := NewSafetyController(config.AgentConfig{MaxToolCalls: 20, EnableLoopSoftWarning: true})
	session := NewAgentSession("sid-loop-soft-warning", "c1", "p1", "u1", "faultTree", time.Minute)
	call := ai.ToolCall{Name: "get_node_detail", Arguments: json.RawMessage(`{"nodeId":"n1"}`)}

	warning, err := s.CheckToolCallWithWarning(session, call, 0)
	if err != nil {
		t.Fatalf("first call should pass without warning, got err=%v", err)
	}
	if warning != nil {
		t.Fatalf("first call should not produce warning, got %+v", warning)
	}

	warning, err = s.CheckToolCallWithWarning(session, call, 0)
	if err != nil {
		t.Fatalf("second identical call should warn, got err=%v", err)
	}
	if warning == nil {
		t.Fatal("second identical call should return loop warning")
	}
	if warning.Code != "loop_warning" {
		t.Fatalf("unexpected warning code: %+v", warning)
	}

	warning, err = s.CheckToolCallWithWarning(session, call, 0)
	if warning != nil {
		t.Fatalf("third identical call should hard stop without warning, got %+v", warning)
	}
	if !errors.Is(err, ErrAgentLoopDetected) {
		t.Fatalf("third identical call should hard stop with ErrAgentLoopDetected, got %v", err)
	}
}
