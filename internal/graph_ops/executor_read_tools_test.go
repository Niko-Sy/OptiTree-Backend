package graph_ops

import (
	"encoding/json"
	"testing"

	"optitree-backend/internal/model"
)

func TestApplyGetGraphSnapshot_ReadOnlyPayload(t *testing.T) {
	e := &Executor{}
	state := newFaultTreeState(
		[]model.FaultTreeNode{
			{ID: "n1", Type: "topEvent", Name: "Top"},
			{ID: "n2", Type: "basicEvent", Name: "Basic"},
		},
		[]model.FaultTreeEdge{
			{ID: "e1", FromNodeID: "n1", ToNodeID: "n2"},
		},
	)

	res, err := e.applyGetGraphSnapshot(state, json.RawMessage(`{"maxNodes":10,"maxEdges":10}`))
	if err != nil {
		t.Fatalf("applyGetGraphSnapshot error: %v", err)
	}
	if operationHasGraphChanges(res) {
		t.Fatalf("snapshot tool should be read-only")
	}

	var payload map[string]interface{}
	if err := json.Unmarshal([]byte(res.summary), &payload); err != nil {
		t.Fatalf("summary is not valid json: %v", err)
	}
	if payload["tool"] != "get_graph_snapshot" {
		t.Fatalf("unexpected tool field: %v", payload["tool"])
	}
}

func TestApplyGetNodeDetail_NotFound(t *testing.T) {
	e := &Executor{}
	state := newFaultTreeState([]model.FaultTreeNode{{ID: "n1", Type: "topEvent", Name: "Top"}}, nil)

	_, err := e.applyGetNodeDetail(state, json.RawMessage(`{"nodeId":"missing"}`))
	if err == nil {
		t.Fatal("expected node not found error")
	}
}

func TestApplyValidateFTAConstraints_DetectsMissingTopEvent(t *testing.T) {
	e := &Executor{}
	state := newFaultTreeState(
		[]model.FaultTreeNode{
			{ID: "n1", Type: "midEvent", Name: "Mid"},
			{ID: "n2", Type: "basicEvent", Name: "Basic"},
		},
		[]model.FaultTreeEdge{{ID: "e1", FromNodeID: "n1", ToNodeID: "n2"}},
	)

	res, err := e.applyValidateFTAConstraints(state)
	if err != nil {
		t.Fatalf("applyValidateFTAConstraints error: %v", err)
	}

	var payload struct {
		IssueCount int                      `json:"issueCount"`
		Issues     []map[string]interface{} `json:"issues"`
	}
	if err := json.Unmarshal([]byte(res.summary), &payload); err != nil {
		t.Fatalf("summary is not valid json: %v", err)
	}
	if payload.IssueCount == 0 {
		t.Fatal("expected at least one issue")
	}

	found := false
	for _, issue := range payload.Issues {
		if code, _ := issue["code"].(string); code == "MISSING_TOP_EVENT" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected MISSING_TOP_EVENT in issues: %+v", payload.Issues)
	}
}
