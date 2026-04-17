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

func TestApplyBatchOperations_RepairMode_AllowsRootLikeDeleteAndSingleChildGate(t *testing.T) {
	e := &Executor{}
	state := newFaultTreeState(
		[]model.FaultTreeNode{
			{ID: "t1", Type: "topEvent", Name: "Top"},
			{ID: "x1", Type: "midEvent", Name: "X1"},
			{ID: "p1", Type: "midEvent", Name: "P1"},
			{ID: "c1", Type: "basicEvent", Name: "C1"},
		},
		[]model.FaultTreeEdge{
			{ID: "e1", FromNodeID: "t1", ToNodeID: "x1"},
			{ID: "e2", FromNodeID: "p1", ToNodeID: "c1"},
		},
	)

	args := json.RawMessage(`{"repairMode":true,"operations":[{"tool":"delete_node","args":{"nodeId":"p1","deleteChildren":false}},{"tool":"add_gate","args":{"gateType":"OR","parentId":"t1","childIds":["x1"]}}]}`)
	res, err := e.applyBatchOperations(state, "p1", args)
	if err != nil {
		t.Fatalf("repairMode batch should succeed, got %v", err)
	}
	if !strings.Contains(res.summary, "repairMode") {
		t.Fatalf("expected repairMode summary marker, got %s", res.summary)
	}
	if _, ok := state.getNode("c1"); !ok {
		t.Fatal("expected child node to remain after permissive root-like delete")
	}
	if _, ok := state.getNode("p1"); ok {
		t.Fatal("expected p1 to be deleted")
	}
	if _, ok := state.findEdgeID("t1", "x1"); ok {
		t.Fatal("expected t1->x1 to be rewired through new gate")
	}

	gateCount := 0
	for _, node := range state.nodes {
		if strings.EqualFold(strings.TrimSpace(node.Type), "gate") {
			gateCount++
		}
	}
	if gateCount != 1 {
		t.Fatalf("expected one new gate node, got %d", gateCount)
	}
}

func TestPlanFaultTreeOperation_BatchRepairModeMarked(t *testing.T) {
	e := &Executor{}
	state := newFaultTreeState(
		[]model.FaultTreeNode{
			{ID: "t1", Type: "topEvent", Name: "Top"},
			{ID: "p1", Type: "midEvent", Name: "P1"},
			{ID: "c1", Type: "basicEvent", Name: "C1"},
		},
		[]model.FaultTreeEdge{{ID: "e1", FromNodeID: "p1", ToNodeID: "c1"}},
	)

	args := json.RawMessage(`{"repairMode":true,"operations":[{"tool":"delete_node","args":{"nodeId":"p1","deleteChildren":false}}]}`)
	plan, err := e.PlanFaultTreeOperation(state, "p1", "batch_operations", args)
	if err != nil {
		t.Fatalf("expected repairMode plan to succeed, got %v", err)
	}
	if plan == nil || !plan.RepairMode {
		t.Fatalf("expected plan.RepairMode=true, got %+v", plan)
	}
	if plan.ChangedNodes == 0 {
		t.Fatalf("expected graph changes in repairMode plan, got %+v", plan)
	}
}

func strPtr(v string) *string {
	s := v
	return &s
}
