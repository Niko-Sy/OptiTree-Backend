package ai

import (
	"encoding/json"
	"testing"
)

func TestParseFallbackToolCalls_MixedContent(t *testing.T) {
	raw := "请帮我修改节点\nFUNCTION_CALL: update_node({\"nodeId\":\"n1\",\"name\":\"Pump\"})\n完成后告诉我"

	calls, reply := ParseFallbackToolCalls(raw)
	if len(calls) != 1 {
		t.Fatalf("expected 1 call, got %d", len(calls))
	}
	if calls[0].Name != "update_node" {
		t.Fatalf("expected tool name update_node, got %s", calls[0].Name)
	}
	if !json.Valid(calls[0].Arguments) {
		t.Fatalf("expected valid json arguments, got %s", string(calls[0].Arguments))
	}
	if reply != "请帮我修改节点\n完成后告诉我" {
		t.Fatalf("unexpected clean reply: %q", reply)
	}
}

func TestParseFallbackToolCalls_InvalidLineKept(t *testing.T) {
	raw := "FUNCTION_CALL: 123_invalid({})"

	calls, reply := ParseFallbackToolCalls(raw)
	if len(calls) != 0 {
		t.Fatalf("expected no parsed calls, got %d", len(calls))
	}
	if reply != raw {
		t.Fatalf("expected original line kept, got %q", reply)
	}
}

func TestNormalizeToolArguments_CodeFenceAndFallback(t *testing.T) {
	parsed := normalizeToolArguments("```json\n{\"nodeId\":\"n1\"}\n```")
	if !json.Valid(parsed) {
		t.Fatalf("expected parsed json from fenced content, got %s", string(parsed))
	}

	fallback := normalizeToolArguments("not-a-json")
	if !json.Valid(fallback) {
		t.Fatalf("expected fallback value to be json string, got %s", string(fallback))
	}
	if string(fallback) != "\"not-a-json\"" {
		t.Fatalf("unexpected fallback value: %s", string(fallback))
	}
}
