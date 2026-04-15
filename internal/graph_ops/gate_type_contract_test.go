package graph_ops

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"optitree-backend/internal/model"
)

func TestGateTypeContract_RegistryValidatorAndSemantics(t *testing.T) {
	for _, toolName := range []string{"update_gate", "add_gate"} {
		def, ok := GetTool(toolName)
		if !ok {
			t.Fatalf("missing tool %s", toolName)
		}
		var schema map[string]interface{}
		if err := json.Unmarshal(def.Parameters, &schema); err != nil {
			t.Fatalf("unmarshal %s schema failed: %v", toolName, err)
		}
		props, _ := schema["properties"].(map[string]interface{})
		gateType, _ := props["gateType"].(map[string]interface{})
		enums, _ := gateType["enum"].([]interface{})
		enumSet := make(map[string]struct{}, len(enums))
		for _, item := range enums {
			value, _ := item.(string)
			enumSet[strings.ToUpper(strings.TrimSpace(value))] = struct{}{}
		}
		if _, ok := enumSet["AND"]; !ok {
			t.Fatalf("%s enum should contain AND, got %v", toolName, enums)
		}
		if _, ok := enumSet["OR"]; !ok {
			t.Fatalf("%s enum should contain OR, got %v", toolName, enums)
		}
		if _, ok := enumSet["NOT"]; !ok {
			t.Fatalf("%s enum should contain AND/OR/NOT, got %v", toolName, enums)
		}
		if _, hasVote := enumSet["VOTE"]; hasVote {
			t.Fatalf("%s enum should not contain VOTE, got %v", toolName, enums)
		}
	}

	if !isValidGateType("AND") || !isValidGateType("OR") || !isValidGateType("NOT") {
		t.Fatal("validator should accept AND/OR/NOT")
	}
	if isValidGateType("VOTE") {
		t.Fatal("validator should reject VOTE")
	}

	e := &Executor{}
	state := newFaultTreeState(
		[]model.FaultTreeNode{
			{ID: "g1", Type: "gate", Name: "NOT", GateType: strPtr("NOT")},
			{ID: "c1", Type: "basicEvent", Name: "C1"},
			{ID: "c2", Type: "basicEvent", Name: "C2"},
		},
		[]model.FaultTreeEdge{
			{ID: "e1", FromNodeID: "g1", ToNodeID: "c1"},
			{ID: "e2", FromNodeID: "g1", ToNodeID: "c2"},
		},
	)
	res, err := e.applyCheckGateSemantics(state, json.RawMessage(`{"nodeId":"g1"}`))
	if err != nil {
		t.Fatalf("applyCheckGateSemantics error: %v", err)
	}
	if !strings.Contains(res.summary, "NOT_GATE_CHILD_COUNT_TOO_HIGH") {
		t.Fatalf("expected NOT cardinality issue in summary, got %s", res.summary)
	}
}

func TestGateTypeContract_MigrationMatchesANDORNOT(t *testing.T) {
	migrationPath := filepath.Join("..", "..", "migration", "000022_add_agent_runtime_and_fix_gate_type_contract.up.sql")
	raw, err := os.ReadFile(migrationPath)
	if err != nil {
		t.Fatalf("read migration failed: %v", err)
	}
	sql := string(raw)

	if !strings.Contains(sql, "gate_type IN ('AND', 'OR', 'NOT')") {
		t.Fatalf("migration should enforce AND/OR/NOT check, sql=%s", sql)
	}
	if !strings.Contains(sql, "SET gate_type = 'OR'") || !strings.Contains(sql, "= 'VOTE'") {
		t.Fatalf("migration should contain VOTE->OR compatibility update, sql=%s", sql)
	}
}
