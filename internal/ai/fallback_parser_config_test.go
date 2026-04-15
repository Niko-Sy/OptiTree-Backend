package ai

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestChatWithTools_FallbackParserDisabled_StripsFunctionCallLeak(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"choices":[{"message":{"role":"assistant","content":"FUNCTION_CALL: update_node({\"nodeId\":\"n1\",\"name\":\"A\"})\n已完成分析"}}],"usage":{"total_tokens":11}}`)
	}))
	defer srv.Close()

	client := NewClient(srv.URL, "", "test-model", "", time.Second)
	resp, err := client.ChatWithTools(context.Background(), AgentChatRequest{
		ChatRequest:          ChatRequest{GraphType: "faultTree", Message: "请处理"},
		EnableFallbackParser: false,
	})
	if err != nil {
		t.Fatalf("ChatWithTools error: %v", err)
	}
	if len(resp.ToolCalls) != 0 {
		t.Fatalf("fallback parser disabled: expected no tool calls, got %+v", resp.ToolCalls)
	}
	if strings.Contains(strings.ToUpper(resp.Reply), "FUNCTION_CALL:") {
		t.Fatalf("fallback parser disabled: FUNCTION_CALL line should be stripped, got %q", resp.Reply)
	}
	if !strings.Contains(resp.Reply, "已完成分析") {
		t.Fatalf("expected cleaned reply to keep normal content, got %q", resp.Reply)
	}
}

func TestChatWithTools_FallbackParserEnabled_ParsesFunctionCall(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"choices":[{"message":{"role":"assistant","content":"FUNCTION_CALL: update_node({\"nodeId\":\"n1\",\"name\":\"A\"})\n已完成分析"}}],"usage":{"total_tokens":11}}`)
	}))
	defer srv.Close()

	client := NewClient(srv.URL, "", "test-model", "", time.Second)
	resp, err := client.ChatWithTools(context.Background(), AgentChatRequest{
		ChatRequest:          ChatRequest{GraphType: "faultTree", Message: "请处理"},
		EnableFallbackParser: true,
	})
	if err != nil {
		t.Fatalf("ChatWithTools error: %v", err)
	}
	if len(resp.ToolCalls) != 1 {
		t.Fatalf("fallback parser enabled: expected 1 tool call, got %+v", resp.ToolCalls)
	}
	if resp.ToolCalls[0].Name != "update_node" {
		t.Fatalf("unexpected tool name: %+v", resp.ToolCalls[0])
	}
	if strings.Contains(strings.ToUpper(resp.Reply), "FUNCTION_CALL:") {
		t.Fatalf("parsed reply should not keep FUNCTION_CALL line, got %q", resp.Reply)
	}
}
