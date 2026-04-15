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

func TestToolKindContractSeparatesReadContextAndValidation(t *testing.T) {
	readContext := []string{"get_graph_snapshot", "get_node_detail", "get_subtree"}
	for _, name := range readContext {
		def, ok := GetTool(name)
		if !ok {
			t.Fatalf("missing tool %s", name)
		}
		if def.Kind != ToolKindReadContext {
			t.Fatalf("%s kind=%s, want %s", name, def.Kind, ToolKindReadContext)
		}
		if !ToolIsReadContext(name) {
			t.Fatalf("%s should satisfy read context precondition", name)
		}
	}

	validateTools := []string{"validate_fta_constraints", "check_gate_semantics"}
	for _, name := range validateTools {
		def, ok := GetTool(name)
		if !ok {
			t.Fatalf("missing tool %s", name)
		}
		if def.Kind != ToolKindValidate {
			t.Fatalf("%s kind=%s, want %s", name, def.Kind, ToolKindValidate)
		}
		if ToolIsReadContext(name) {
			t.Fatalf("%s must not satisfy read context precondition", name)
		}
	}
}

func TestBuildToolPromptGuide_CompactKindSections(t *testing.T) {
	defs := FilterToolsForMode("faultTree", false, false)
	guide := BuildToolPromptGuide(defs)

	for _, section := range []string{"[read_context]", "[validate]", "[mutate]", "[client_ui]"} {
		if !strings.Contains(guide, section) {
			t.Fatalf("expected compact guide section %s, got: %s", section, guide)
		}
	}
	if strings.Contains(guide, "[hybrid_preview]") {
		t.Fatalf("hybrid tools should be hidden by default: %s", guide)
	}
	if strings.Contains(guide, `"properties"`) || strings.Contains(guide, `"type":"object"`) {
		t.Fatalf("prompt guide should not duplicate full JSON schema: %s", guide)
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
