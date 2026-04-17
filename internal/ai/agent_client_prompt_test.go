package ai

import (
	"fmt"
	"strings"
	"testing"
)

func TestBuildAgentMessages_FaultTreeUsesCompactPromptV2(t *testing.T) {
	req := AgentChatRequest{
		ChatRequest: ChatRequest{
			GraphType: "faultTree",
			Message:   "请检查并修复故障树逻辑",
		},
		ToolGuide: "Contract order for graph edits: read_context -> validate -> mutate -> validate.\n[read_context]\n- get_graph_snapshot | required=[none] | flags=[read_only]",
	}

	messages := buildAgentMessages(req)
	if len(messages) < 1 {
		t.Fatalf("expected at least one message")
	}
	sys := messages[0].Content
	if len([]rune(sys)) > 2600 {
		t.Fatalf("expected compact system prompt, got %d chars: %s", len([]rune(sys)), sys)
	}

	checks := []string{
		"Top Event",
		"Basic Event",
		"Tool operation constraints",
		"rewires existing parent->child edges only",
		"There is no dedicated edge deletion tool",
		"Runtime Tool Guide",
		"get_graph_snapshot | required=[none]",
		"read_context -> validate -> mutate -> validate",
	}
	for _, check := range checks {
		if !strings.Contains(sys, check) {
			t.Fatalf("expected system prompt to contain %q, got: %s", check, sys)
		}
	}
	for _, bad := range []string{"add_edge", "delete_edge", "change_gate_type", "restructure_subtree", "+node", "+edge", "-edge", "local subtree refactor"} {
		if strings.Contains(sys, bad) {
			t.Fatalf("system prompt should not contain misleading action %q: %s", bad, sys)
		}
	}
}

func TestBuildAgentMessages_KnowledgeGraphNotPollutedByFTA(t *testing.T) {
	req := AgentChatRequest{
		ChatRequest: ChatRequest{
			GraphType: "knowledgeGraph",
			Message:   "请分析知识图谱",
		},
	}

	messages := buildAgentMessages(req)
	if len(messages) < 1 {
		t.Fatalf("expected at least one message")
	}
	sys := messages[0].Content

	if !strings.Contains(sys, "knowledge graph") {
		t.Fatalf("expected knowledge graph wording in system prompt: %s", sys)
	}
}

func TestBuildAgentMessages_UsesSummaryContextModeForLargeGraphs(t *testing.T) {
	nodes := make([]map[string]interface{}, 0, 450)
	for i := 0; i < 450; i++ {
		nodes = append(nodes, map[string]interface{}{
			"id":   fmt.Sprintf("n_%d", i),
			"type": "basicEvent",
			"name": fmt.Sprintf("Node-%d", i),
		})
	}

	req := AgentChatRequest{
		ChatRequest: ChatRequest{
			GraphType: "faultTree",
			Message:   "检查上下文模式",
			ContextData: map[string]interface{}{
				"nodes": nodes,
				"edges": []map[string]interface{}{},
			},
		},
	}

	messages := buildAgentMessages(req)
	if len(messages) < 2 {
		t.Fatalf("expected system and user messages")
	}
	userMsg := messages[len(messages)-1].Content

	if !strings.Contains(userMsg, "Context mode: summary") {
		t.Fatalf("expected agent context mode to be summary, got user prompt: %s", userMsg)
	}
	if strings.Contains(userMsg, `"chunks"`) {
		t.Fatalf("large graph should not include full chunks by default: %s", userMsg)
	}
}

func TestBuildAgentMessages_UsesChunkedContextOnlyWhenExplicitlyRequested(t *testing.T) {
	nodes := make([]map[string]interface{}, 0, 450)
	for i := 0; i < 450; i++ {
		nodes = append(nodes, map[string]interface{}{
			"id":   fmt.Sprintf("n_%d", i),
			"type": "basicEvent",
			"name": fmt.Sprintf("Node-%d", i),
		})
	}

	req := AgentChatRequest{
		ChatRequest: ChatRequest{
			GraphType: "faultTree",
			Message:   "请全图遍历分析所有节点",
			ContextData: map[string]interface{}{
				"nodes": nodes,
				"edges": []map[string]interface{}{},
			},
		},
	}

	messages := buildAgentMessages(req)
	userMsg := messages[len(messages)-1].Content
	if !strings.Contains(userMsg, "Context mode: chunked") {
		t.Fatalf("expected explicit full graph request to use chunked mode, got: %s", userMsg)
	}
	if !strings.Contains(userMsg, `"chunks"`) {
		t.Fatalf("expected chunk payload for explicit full graph request, got: %s", userMsg)
	}
}

func TestBuildAgentMessages_RespectsFullContextThreshold(t *testing.T) {
	nodes := make([]map[string]interface{}, 0, 150)
	for i := 0; i < 150; i++ {
		nodes = append(nodes, map[string]interface{}{
			"id":   fmt.Sprintf("n_%d", i),
			"type": "basicEvent",
		})
	}

	req := AgentChatRequest{
		ChatRequest: ChatRequest{
			GraphType: "faultTree",
			Message:   "检查当前图",
			ContextData: map[string]interface{}{
				"nodes": nodes,
				"edges": []map[string]interface{}{},
			},
		},
		FullContextThreshold: 200,
	}

	messages := buildAgentMessages(req)
	userMsg := messages[len(messages)-1].Content
	if !strings.Contains(userMsg, "Context mode: full") {
		t.Fatalf("expected raised threshold to keep full context, got: %s", userMsg)
	}
}

func TestBuildAgentMessages_PromptVersionV3InjectsExecutionDiscipline(t *testing.T) {
	req := AgentChatRequest{
		ChatRequest: ChatRequest{
			GraphType: "faultTree",
			Message:   "请修复故障树结构",
		},
		PromptVersion: "v3",
		ToolGuide:     "[read_context]\n- get_graph_snapshot | required=[none]",
	}

	messages := buildAgentMessages(req)
	if len(messages) < 1 {
		t.Fatalf("expected at least one message")
	}
	sys := messages[0].Content
	if !strings.Contains(sys, "Execution discipline") {
		t.Fatalf("expected v3 prompt to include execution discipline, got: %s", sys)
	}
	if !strings.Contains(sys, "do not call it again") {
		t.Fatalf("expected v3 prompt to include anti-repeat instruction, got: %s", sys)
	}
}
