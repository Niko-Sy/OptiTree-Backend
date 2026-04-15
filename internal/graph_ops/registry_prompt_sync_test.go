package graph_ops

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func TestBuildToolPromptGuide_CoversRegistryAndNoLegacyTools(t *testing.T) {
	defs := GetToolsForGraphType("faultTree")
	if len(defs) == 0 {
		t.Fatal("expected faultTree tools from registry")
	}

	guide := BuildToolPromptGuide(defs)
	if strings.TrimSpace(guide) == "" {
		t.Fatal("expected non-empty tool prompt guide")
	}

	for _, def := range defs {
		needle := "- " + def.Name + " |"
		if !strings.Contains(guide, needle) {
			t.Fatalf("tool guide missing registry tool %q", def.Name)
		}
	}

	legacy := []string{"add_edge", "delete_edge", "change_gate_type", "restructure_subtree"}
	for _, bad := range legacy {
		if strings.Contains(guide, bad) {
			t.Fatalf("tool guide should not include legacy tool %q", bad)
		}
	}
}

func TestGateTypeSchemaAndValidatorContract(t *testing.T) {
	checkGateEnum := func(toolName string) {
		t.Helper()
		def, ok := GetTool(toolName)
		if !ok {
			t.Fatalf("missing tool %s", toolName)
		}

		var schema map[string]interface{}
		if err := json.Unmarshal(def.Parameters, &schema); err != nil {
			t.Fatalf("unmarshal %s schema failed: %v", toolName, err)
		}

		properties, _ := schema["properties"].(map[string]interface{})
		gateProp, _ := properties["gateType"].(map[string]interface{})
		enumRaw, _ := gateProp["enum"].([]interface{})
		if len(enumRaw) == 0 {
			t.Fatalf("%s gateType enum is empty", toolName)
		}

		enumSet := make(map[string]struct{}, len(enumRaw))
		for _, item := range enumRaw {
			enumSet[strings.ToUpper(strings.TrimSpace(fmt.Sprintf("%v", item)))] = struct{}{}
		}

		for _, expected := range []string{"AND", "OR", "NOT"} {
			if _, ok := enumSet[expected]; !ok {
				t.Fatalf("%s gateType enum missing %s", toolName, expected)
			}
		}
		if _, hasVote := enumSet["VOTE"]; hasVote {
			t.Fatalf("%s gateType enum should not contain VOTE", toolName)
		}
	}

	checkGateEnum("update_gate")
	checkGateEnum("add_gate")

	if !isValidGateType("NOT") {
		t.Fatal("validator should accept NOT")
	}
	if isValidGateType("VOTE") {
		t.Fatal("validator should reject VOTE")
	}
}
