package graph_ops

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"optitree-backend/internal/model"
)

func TestBatchOperations_IntermediateInvalidButFinalValid(t *testing.T) {
	e := &Executor{}
	state := newFaultTreeState(
		[]model.FaultTreeNode{
			{ID: "t1", Type: "topEvent", Name: "Top"},
			{ID: "g1", Type: "gate", Name: "AND", GateType: strPtr("AND")},
			{ID: "c1", Type: "basicEvent", Name: "C1"},
			{ID: "c2", Type: "basicEvent", Name: "C2"},
		},
		[]model.FaultTreeEdge{
			{ID: "e1", FromNodeID: "t1", ToNodeID: "g1"},
			{ID: "e2", FromNodeID: "g1", ToNodeID: "c1"},
			{ID: "e3", FromNodeID: "g1", ToNodeID: "c2"},
		},
	)

	args := json.RawMessage(`{"operations":[{"tool":"delete_node","args":{"nodeId":"c2","deleteChildren":false}},{"tool":"add_node","args":{"name":"C3","nodeType":"basicEvent","parentId":"g1"}}]}`)
	res, err := e.applyBatchOperations(state, "p1", args)
	if err != nil {
		t.Fatalf("applyBatchOperations error: %v", err)
	}
	if !operationHasGraphChanges(res) {
		t.Fatal("expected batch to produce graph changes")
	}
	if err := enforceFaultTreeMutationSafety(state); err != nil {
		t.Fatalf("final state should pass mutation safety check, got %v", err)
	}
}

func TestApplyPlannedPatchSet_FinalInvalidBatchRejected(t *testing.T) {
	e := &Executor{}
	state := newFaultTreeState(
		[]model.FaultTreeNode{
			{ID: "t1", Type: "topEvent", Name: "Top"},
			{ID: "g1", Type: "gate", Name: "AND", GateType: strPtr("AND")},
			{ID: "c1", Type: "basicEvent", Name: "C1"},
			{ID: "c2", Type: "basicEvent", Name: "C2"},
		},
		[]model.FaultTreeEdge{
			{ID: "e1", FromNodeID: "t1", ToNodeID: "g1"},
			{ID: "e2", FromNodeID: "g1", ToNodeID: "c1"},
			{ID: "e3", FromNodeID: "g1", ToNodeID: "c2"},
		},
	)

	args := json.RawMessage(`{"operations":[{"tool":"delete_node","args":{"nodeId":"c2","deleteChildren":false}}]}`)
	plan, err := e.PlanFaultTreeOperation(state, "p1", "batch_operations", args)
	if err != nil {
		t.Fatalf("PlanFaultTreeOperation error: %v", err)
	}
	if plan == nil || len(plan.Operations) == 0 {
		t.Fatalf("expected non-empty plan, got %+v", plan)
	}

	_, _, _, _, err = e.applyPlannedPatchSet(context.Background(), "p1", "faultTree", "batch_operations", state, plan, -1)
	if err == nil {
		t.Fatal("expected invalid final batch to be rejected")
	}
	if !errors.Is(err, ErrOperationNotAllowed) {
		t.Fatalf("expected ErrOperationNotAllowed, got %v", err)
	}
	if !strings.Contains(err.Error(), "GATE_CHILD_COUNT_TOO_LOW") {
		t.Fatalf("expected gate child-count issue in error, got %v", err)
	}
}

func strPtr(v string) *string {
	s := v
	return &s
}
