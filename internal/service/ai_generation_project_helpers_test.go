package service

import (
	"testing"

	"optitree-backend/internal/ai"
)

func TestMapFaultTreeNodeType_SupportsNOTGate(t *testing.T) {
	typeCases := []struct {
		name     string
		rawType  string
		data     map[string]interface{}
		wantType string
		wantGate string
	}{
		{name: "not_gate_raw_type", rawType: "notGate", data: map[string]interface{}{}, wantType: "gate", wantGate: "NOT"},
		{name: "gate_with_not_label", rawType: "gate", data: map[string]interface{}{"label": "NOT"}, wantType: "gate", wantGate: "NOT"},
		{name: "gate_with_vote_label_fallback", rawType: "gate", data: map[string]interface{}{"label": "VOTE"}, wantType: "gate", wantGate: ""},
	}

	for _, tc := range typeCases {
		t.Run(tc.name, func(t *testing.T) {
			nodeType, gateType := mapFaultTreeNodeType(tc.rawType, tc.data)
			if nodeType != tc.wantType {
				t.Fatalf("nodeType=%s, want %s", nodeType, tc.wantType)
			}
			if tc.wantGate == "" {
				if gateType != nil {
					t.Fatalf("gateType should be nil, got %v", *gateType)
				}
				return
			}
			if gateType == nil || *gateType != tc.wantGate {
				t.Fatalf("gateType=%v, want %s", gateType, tc.wantGate)
			}
		})
	}
}

func TestToFaultTreeGraph_MapsNOTGateFromAIResult(t *testing.T) {
	result := &ai.FaultTreeResult{
		Nodes: []map[string]interface{}{
			{"id": "top", "type": "topEvent", "name": "Top"},
			{"id": "g1", "type": "gate", "data": map[string]interface{}{"label": "NOT"}},
			{"id": "b1", "type": "basicEvent", "name": "Leaf"},
		},
		Edges: []map[string]interface{}{
			{"id": "e1", "from": "top", "to": "g1"},
			{"id": "e2", "from": "g1", "to": "b1"},
		},
	}

	nodes, _, err := toFaultTreeGraph(result)
	if err != nil {
		t.Fatalf("toFaultTreeGraph error: %v", err)
	}

	var gateFound bool
	for _, node := range nodes {
		if node.ID != "g1" {
			continue
		}
		gateFound = true
		if node.GateType == nil || *node.GateType != "NOT" {
			t.Fatalf("expected gate g1 to map to NOT, got %+v", node.GateType)
		}
	}
	if !gateFound {
		t.Fatal("expected mapped gate node g1")
	}
}
