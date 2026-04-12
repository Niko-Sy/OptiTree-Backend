package graph_ops

import (
	"encoding/json"
	"errors"
	"testing"
)

func TestValidateParameters_ValidCases(t *testing.T) {
	validCases := []struct {
		name string
		tool string
		args string
	}{
		{name: "update_node", tool: "update_node", args: `{"nodeId":"n1","name":"Node A"}`},
		{name: "add_gate", tool: "add_gate", args: `{"gateType":"AND","parentId":"p1","childIds":["c1","c2"]}`},
		{name: "add_gate_alias_childNodeIds", tool: "add_gate", args: `{"gateType":"AND","parentNodeId":"p1","childNodeIds":["c1","c2"]}`},
		{name: "get_graph_snapshot", tool: "get_graph_snapshot", args: `{"maxNodes":100,"maxEdges":100}`},
		{name: "get_node_detail", tool: "get_node_detail", args: `{"nodeId":"n1"}`},
		{name: "get_subtree", tool: "get_subtree", args: `{"rootNodeId":"n1","maxDepth":3}`},
		{name: "validate_fta_constraints", tool: "validate_fta_constraints", args: `{}`},
		{name: "hybrid_optional_args", tool: "suggest_batch_label_fix", args: `{}`},
	}

	for _, tc := range validCases {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateParameters(tc.tool, json.RawMessage(tc.args))
			if err != nil {
				t.Fatalf("expected nil error, got %v", err)
			}
		})
	}
}

func TestValidateParameters_InvalidCases(t *testing.T) {
	if err := ValidateParameters("unknown_tool", json.RawMessage(`{}`)); !errors.Is(err, ErrUnknownTool) {
		t.Fatalf("expected ErrUnknownTool, got %v", err)
	}

	err := ValidateParameters("update_gate", json.RawMessage(`{"nodeId":"n1","gateType":"XOR"}`))
	if !errors.Is(err, ErrInvalidParameters) {
		t.Fatalf("expected ErrInvalidParameters for bad gateType, got %v", err)
	}

	nestedBatch := `{
		"operations": [
			{"tool": "batch_operations", "args": {"operations": []}}
		]
	}`
	err = ValidateParameters("batch_operations", json.RawMessage(nestedBatch))
	if !errors.Is(err, ErrOperationNotAllowed) {
		t.Fatalf("expected ErrOperationNotAllowed for nested batch, got %v", err)
	}

	err = ValidateParameters("get_subtree", json.RawMessage(`{"rootNodeId":"n1","maxDepth":0}`))
	if !errors.Is(err, ErrInvalidParameters) {
		t.Fatalf("expected ErrInvalidParameters for bad maxDepth, got %v", err)
	}

	err = ValidateParameters("get_node_detail", json.RawMessage(`{}`))
	if !errors.Is(err, ErrInvalidParameters) {
		t.Fatalf("expected ErrInvalidParameters for missing nodeId, got %v", err)
	}

	err = ValidateParameters("add_node", json.RawMessage(`{"name":"g1","nodeType":"gate"}`))
	if !errors.Is(err, ErrInvalidParameters) {
		t.Fatalf("expected ErrInvalidParameters for add_node gate type, got %v", err)
	}
}

func TestSanitizeStringParam(t *testing.T) {
	got := SanitizeStringParam("  \u0000abc  ", 2)
	if got != "ab" {
		t.Fatalf("expected sanitized value 'ab', got %q", got)
	}
}
