package ai

import (
	"fmt"
	"strings"
	"testing"
)

func TestBuildAgentMessages_FaultTreeIncludesIECAndToolchainRules(t *testing.T) {
	req := AgentChatRequest{
		ChatRequest: ChatRequest{
			GraphType: "faultTree",
			Message:   "请检查并修复故障树逻辑",
		},
	}

	messages := buildAgentMessages(req)
	if len(messages) < 1 {
		t.Fatalf("expected at least one message")
	}
	sys := messages[0].Content

	checks := []string{
		"IEC 61025",
		"Top Event",
		"Basic Event",
		"Toolchain-first",
		"get_graph_snapshot",
		"do NOT ask the user to resend raw nodes/edges",
	}
	for _, check := range checks {
		if !strings.Contains(sys, check) {
			t.Fatalf("expected system prompt to contain %q, got: %s", check, sys)
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

	if strings.Contains(sys, "IEC 61025") {
		t.Fatalf("knowledge graph prompt should not include IEC 61025 rules: %s", sys)
	}
	if !strings.Contains(sys, "knowledge graph") {
		t.Fatalf("expected knowledge graph wording in system prompt: %s", sys)
	}
}

func TestBuildAgentMessages_UsesFullContextModeForLargeGraphs(t *testing.T) {
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

	if !strings.Contains(userMsg, "Context mode: full") {
		t.Fatalf("expected agent context mode to be full, got user prompt: %s", userMsg)
	}
	if strings.Contains(userMsg, "Context mode: chunked") {
		t.Fatalf("agent should not use chunked context mode: %s", userMsg)
	}
}
