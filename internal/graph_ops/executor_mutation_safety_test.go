package graph_ops

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"optitree-backend/internal/model"
)

func TestApplyAddGate_RewiresParentChildren(t *testing.T) {
	e := &Executor{}
	state := newFaultTreeState(
		[]model.FaultTreeNode{
			{ID: "p1", Type: "topEvent", Name: "Top"},
			{ID: "c1", Type: "midEvent", Name: "Mid"},
			{ID: "c2", Type: "basicEvent", Name: "Basic"},
		},
		[]model.FaultTreeEdge{
			{ID: "e1", FromNodeID: "p1", ToNodeID: "c1"},
			{ID: "e2", FromNodeID: "p1", ToNodeID: "c2"},
		},
	)

	res, err := e.applyAddGate(state, json.RawMessage(`{"gateType":"AND","parentId":"p1","childIds":["c1","c2"]}`))
	if err != nil {
		t.Fatalf("applyAddGate error: %v", err)
	}

	if _, ok := state.findEdgeID("p1", "c1"); ok {
		t.Fatal("expected edge p1->c1 to be removed by rewire")
	}
	if _, ok := state.findEdgeID("p1", "c2"); ok {
		t.Fatal("expected edge p1->c2 to be removed by rewire")
	}

	if len(res.patch.UpsertNodes) != 1 {
		t.Fatalf("expected one upserted gate node, got %d", len(res.patch.UpsertNodes))
	}
	gateID, _ := res.patch.UpsertNodes[0]["id"].(string)
	if strings.TrimSpace(gateID) == "" {
		t.Fatalf("expected generated gate id in patch: %+v", res.patch.UpsertNodes[0])
	}
	if _, ok := state.findEdgeID("p1", gateID); !ok {
		t.Fatal("expected edge p1->gate after rewire")
	}
	if _, ok := state.findEdgeID(gateID, "c1"); !ok {
		t.Fatal("expected edge gate->c1 after rewire")
	}
	if _, ok := state.findEdgeID(gateID, "c2"); !ok {
		t.Fatal("expected edge gate->c2 after rewire")
	}

	if len(res.patch.DeleteEdges) != 2 {
		t.Fatalf("expected two deleted edges in patch, got %+v", res.patch.DeleteEdges)
	}
}

func TestApplyAddGate_AcceptsAliasChildNodeIDs(t *testing.T) {
	e := &Executor{}
	state := newFaultTreeState(
		[]model.FaultTreeNode{
			{ID: "p1", Type: "topEvent", Name: "Top"},
			{ID: "c1", Type: "midEvent", Name: "Mid"},
			{ID: "c2", Type: "basicEvent", Name: "Basic"},
		},
		[]model.FaultTreeEdge{
			{ID: "e1", FromNodeID: "p1", ToNodeID: "c1"},
			{ID: "e2", FromNodeID: "p1", ToNodeID: "c2"},
		},
	)

	_, err := e.applyAddGate(state, json.RawMessage(`{"gateType":"OR","parentNodeId":"p1","childNodeIds":["c1","c2"]}`))
	if err != nil {
		t.Fatalf("applyAddGate alias args error: %v", err)
	}
}

func TestApplyMoveNode_RejectsCycle(t *testing.T) {
	e := &Executor{}
	state := newFaultTreeState(
		[]model.FaultTreeNode{
			{ID: "n1", Type: "topEvent", Name: "Top"},
			{ID: "n2", Type: "midEvent", Name: "Mid"},
			{ID: "n3", Type: "basicEvent", Name: "Leaf"},
		},
		[]model.FaultTreeEdge{
			{ID: "e1", FromNodeID: "n1", ToNodeID: "n2"},
			{ID: "e2", FromNodeID: "n2", ToNodeID: "n3"},
		},
	)

	_, err := e.applyMoveNode(state, json.RawMessage(`{"nodeId":"n2","newParentId":"n3"}`))
	if err == nil {
		t.Fatal("expected cycle-protection error")
	}
	if !errors.Is(err, ErrOperationNotAllowed) {
		t.Fatalf("expected ErrOperationNotAllowed, got %v", err)
	}
}

func TestApplyUpdateGate_RejectsNonGateNode(t *testing.T) {
	e := &Executor{}
	state := newFaultTreeState(
		[]model.FaultTreeNode{{ID: "n1", Type: "basicEvent", Name: "Basic"}},
		nil,
	)

	_, err := e.applyUpdateGate(state, json.RawMessage(`{"nodeId":"n1","gateType":"OR"}`))
	if err == nil {
		t.Fatal("expected update_gate rejection for non-gate node")
	}
	if !errors.Is(err, ErrOperationNotAllowed) {
		t.Fatalf("expected ErrOperationNotAllowed, got %v", err)
	}
}

func TestApplyDeleteNode_PromotesChildrenWhenNotDeletingSubtree(t *testing.T) {
	e := &Executor{}
	state := newFaultTreeState(
		[]model.FaultTreeNode{
			{ID: "p1", Type: "topEvent", Name: "Top"},
			{ID: "g1", Type: "gate", Name: "AND"},
			{ID: "c1", Type: "basicEvent", Name: "C1"},
			{ID: "c2", Type: "basicEvent", Name: "C2"},
		},
		[]model.FaultTreeEdge{
			{ID: "e1", FromNodeID: "p1", ToNodeID: "g1"},
			{ID: "e2", FromNodeID: "g1", ToNodeID: "c1"},
			{ID: "e3", FromNodeID: "g1", ToNodeID: "c2"},
		},
	)

	res, err := e.applyDeleteNode(state, json.RawMessage(`{"nodeId":"g1","deleteChildren":false}`))
	if err != nil {
		t.Fatalf("applyDeleteNode error: %v", err)
	}

	if _, ok := state.getNode("g1"); ok {
		t.Fatal("expected g1 to be deleted")
	}
	if _, ok := state.findEdgeID("p1", "c1"); !ok {
		t.Fatal("expected promoted edge p1->c1")
	}
	if _, ok := state.findEdgeID("p1", "c2"); !ok {
		t.Fatal("expected promoted edge p1->c2")
	}
	if len(res.patch.DeleteNodes) != 1 || res.patch.DeleteNodes[0] != "g1" {
		t.Fatalf("unexpected delete nodes patch: %+v", res.patch.DeleteNodes)
	}
	if len(res.patch.UpsertEdges) < 2 {
		t.Fatalf("expected promoted upsert edges, got %+v", res.patch.UpsertEdges)
	}
}

func TestApplyDeleteNode_RejectsTopEvent(t *testing.T) {
	e := &Executor{}
	state := newFaultTreeState(
		[]model.FaultTreeNode{{ID: "t1", Type: "topEvent", Name: "Top"}},
		nil,
	)

	_, err := e.applyDeleteNode(state, json.RawMessage(`{"nodeId":"t1","deleteChildren":true}`))
	if err == nil {
		t.Fatal("expected topEvent deletion to be blocked")
	}
	if !errors.Is(err, ErrOperationNotAllowed) {
		t.Fatalf("expected ErrOperationNotAllowed, got %v", err)
	}
}

func TestApplyAddNode_RejectsGateTypeAndNonGateParent(t *testing.T) {
	e := &Executor{}
	state := newFaultTreeState(
		[]model.FaultTreeNode{
			{ID: "t1", Type: "topEvent", Name: "Top"},
			{ID: "g1", Type: "gate", Name: "AND"},
		},
		[]model.FaultTreeEdge{{ID: "e1", FromNodeID: "t1", ToNodeID: "g1"}},
	)

	_, err := e.applyAddNode(state, json.RawMessage(`{"name":"G2","nodeType":"gate","parentId":"t1"}`))
	if err == nil || !errors.Is(err, ErrOperationNotAllowed) {
		t.Fatalf("expected add_node gate type rejection, got %v", err)
	}

	_, err = e.applyAddNode(state, json.RawMessage(`{"name":"B1","nodeType":"basicEvent","parentId":"t1"}`))
	if err == nil || !errors.Is(err, ErrOperationNotAllowed) {
		t.Fatalf("expected non-gate parent rejection, got %v", err)
	}

	_, err = e.applyAddNode(state, json.RawMessage(`{"name":"B2","nodeType":"basicEvent","parentId":"g1"}`))
	if err != nil {
		t.Fatalf("expected add_node under gate parent success, got %v", err)
	}
}

func TestEnforceFaultTreeMutationSafety_BlocksCriticalIssues(t *testing.T) {
	state := newFaultTreeState(
		[]model.FaultTreeNode{
			{ID: "t1", Type: "topEvent", Name: "Top"},
			{ID: "b1", Type: "basicEvent", Name: "Basic"},
			{ID: "m1", Type: "midEvent", Name: "Mid"},
		},
		[]model.FaultTreeEdge{
			{ID: "e1", FromNodeID: "t1", ToNodeID: "b1"},
			{ID: "e2", FromNodeID: "b1", ToNodeID: "m1"},
		},
	)

	err := enforceFaultTreeMutationSafety(state)
	if err == nil {
		t.Fatal("expected safety gate to block invalid structure")
	}
	if !strings.Contains(err.Error(), "BASIC_EVENT_HAS_CHILDREN") {
		t.Fatalf("expected BASIC_EVENT_HAS_CHILDREN in error, got %v", err)
	}
}

func TestEnforceFaultTreeMutationSafetyPermissive_AllowsStructuralWarnings(t *testing.T) {
	state := newFaultTreeState(
		[]model.FaultTreeNode{
			{ID: "t1", Type: "topEvent", Name: "Top"},
			{ID: "g1", Type: "gate", Name: "OR", GateType: strPtr("OR")},
			{ID: "b1", Type: "basicEvent", Name: "Basic"},
		},
		[]model.FaultTreeEdge{
			{ID: "e1", FromNodeID: "t1", ToNodeID: "g1"},
			{ID: "e2", FromNodeID: "g1", ToNodeID: "b1"},
		},
	)

	if err := enforceFaultTreeMutationSafety(state); err == nil {
		t.Fatal("strict safety should block gate child-count warning")
	}
	if err := enforceFaultTreeMutationSafetyPermissive(state); err != nil {
		t.Fatalf("permissive safety should allow non-fatal warnings, got %v", err)
	}
}

func TestEnforceFaultTreeMutationSafetyPermissive_BlocksFatalIssues(t *testing.T) {
	state := newFaultTreeState(
		[]model.FaultTreeNode{
			{ID: "n1", Type: "midEvent", Name: "Mid"},
			{ID: "n2", Type: "basicEvent", Name: "Basic"},
		},
		[]model.FaultTreeEdge{{ID: "e1", FromNodeID: "n1", ToNodeID: "n2"}},
	)

	err := enforceFaultTreeMutationSafetyPermissive(state)
	if err == nil {
		t.Fatal("permissive safety should still block missing top event")
	}
	if !errors.Is(err, ErrOperationNotAllowed) {
		t.Fatalf("expected ErrOperationNotAllowed, got %v", err)
	}
	if !strings.Contains(err.Error(), "MISSING_TOP_EVENT") {
		t.Fatalf("expected MISSING_TOP_EVENT in error, got %v", err)
	}
}
