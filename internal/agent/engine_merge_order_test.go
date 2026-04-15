package agent

import (
	"encoding/json"
	"testing"

	"optitree-backend/internal/ai"
)

func TestMergeRoundMutationCalls_PreservesOrderAcrossReadAndValidate(t *testing.T) {
	calls := []ai.ToolCall{
		{ID: "c1", Name: "update_node", Arguments: json.RawMessage(`{"nodeId":"n1","name":"A"}`)},
		{ID: "c2", Name: "get_node_detail", Arguments: json.RawMessage(`{"nodeId":"n1"}`)},
		{ID: "c3", Name: "add_node", Arguments: json.RawMessage(`{"name":"N2","nodeType":"basicEvent","parentId":"g1"}`)},
		{ID: "c4", Name: "move_node", Arguments: json.RawMessage(`{"nodeId":"n2","newParentId":"g2"}`)},
		{ID: "c5", Name: "validate_fta_constraints", Arguments: json.RawMessage(`{}`)},
		{ID: "c6", Name: "delete_node", Arguments: json.RawMessage(`{"nodeId":"n3","deleteChildren":false}`)},
	}

	merged := mergeRoundMutationCalls(calls)
	if len(merged) != 3 {
		t.Fatalf("expected 3 calls after round-level merge, got %d: %+v", len(merged), merged)
	}

	want := []string{"batch_operations", "get_node_detail", "validate_fta_constraints"}
	for i := range want {
		if merged[i].Name != want[i] {
			t.Fatalf("unexpected call order at %d: got %s, want %s", i, merged[i].Name, want[i])
		}
	}

	if merged[0].ID != "c1" {
		t.Fatalf("expected merged call to keep first mutation call id c1, got %s", merged[0].ID)
	}

	var payload struct {
		Operations []struct {
			Tool string `json:"tool"`
		} `json:"operations"`
	}
	if err := json.Unmarshal(merged[0].Arguments, &payload); err != nil {
		t.Fatalf("failed to parse merged batch args: %v", err)
	}
	if len(payload.Operations) != 4 {
		t.Fatalf("expected 4 merged operations, got %d", len(payload.Operations))
	}
	if payload.Operations[0].Tool != "update_node" || payload.Operations[1].Tool != "add_node" || payload.Operations[2].Tool != "move_node" || payload.Operations[3].Tool != "delete_node" {
		t.Fatalf("unexpected merged operation order: %+v", payload.Operations)
	}
}
