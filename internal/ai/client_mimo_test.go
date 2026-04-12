package ai

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestHistoryToOAIMessages_PreservesAssistantReasoning(t *testing.T) {
	history := []ChatHistoryMessage{
		{Role: "assistant", ReasoningContent: "first trace"},
		{Role: "assistant", Content: "final answer", ReasoningContent: "second trace"},
		{Role: "user", Content: "next question"},
	}

	messages := historyToOAIMessages(history)
	if len(messages) != 3 {
		t.Fatalf("expected 3 messages, got %d", len(messages))
	}
	if messages[0].Role != "assistant" || messages[0].ReasoningContent != "first trace" {
		t.Fatalf("unexpected first message: %+v", messages[0])
	}
	if messages[0].Content != "" {
		t.Fatalf("expected empty content for reasoning-only assistant message, got %q", messages[0].Content)
	}
}

func TestHistoryToOAIMessages_ConvertsStructuredToolMessages(t *testing.T) {
	history := []ChatHistoryMessage{
		{
			Role: "assistant",
			ToolCalls: []ToolCall{{
				ID:        "call_1",
				Name:      "add_node",
				Arguments: json.RawMessage(`{ "nodeType" : "basic" }`),
			}},
		},
		{Role: "tool", ToolCallID: "call_1", Content: `{"status":"success","summary":"ok"}`},
	}

	messages := historyToOAIMessages(history)
	if len(messages) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(messages))
	}

	assistant := messages[0]
	if assistant.Role != "assistant" {
		t.Fatalf("expected assistant role, got %q", assistant.Role)
	}
	if len(assistant.ToolCalls) != 1 {
		t.Fatalf("expected 1 tool call, got %d", len(assistant.ToolCalls))
	}
	if assistant.ToolCalls[0].ID != "call_1" || assistant.ToolCalls[0].Function.Name != "add_node" {
		t.Fatalf("unexpected assistant tool call: %+v", assistant.ToolCalls[0])
	}
	if assistant.ToolCalls[0].Function.Arguments != `{"nodeType":"basic"}` {
		t.Fatalf("unexpected tool arguments: %q", assistant.ToolCalls[0].Function.Arguments)
	}

	toolMsg := messages[1]
	if toolMsg.Role != "tool" {
		t.Fatalf("expected tool role, got %q", toolMsg.Role)
	}
	if toolMsg.ToolCallID != "call_1" {
		t.Fatalf("unexpected tool_call_id: %q", toolMsg.ToolCallID)
	}
	if toolMsg.Content != `{"status":"success","summary":"ok"}` {
		t.Fatalf("unexpected tool content: %q", toolMsg.Content)
	}
}

func TestApplyAuthHeader_UsesAPIKeyForMiMo(t *testing.T) {
	client := NewClient("https://api.minimax.chat/v1", "mimo-key", "mimo-v1", "", time.Second)
	req, err := http.NewRequest(http.MethodPost, "https://example.com", nil)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}

	client.applyAuthHeader(req, "mimo-v1")
	if got := req.Header.Get("api-key"); got != "mimo-key" {
		t.Fatalf("expected api-key header, got %q", got)
	}
	if got := req.Header.Get("Authorization"); got != "" {
		t.Fatalf("expected empty Authorization header, got %q", got)
	}
}

func TestCompleteStreamWithTools_CollectsReasoningContent(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("api-key"); got != "mimo-key" {
			t.Errorf("expected api-key auth header, got %q", got)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"reasoning_content\":\"first \"}}]}\n\n")
		_, _ = io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"content\":\"hello \",\"tool_calls\":[{\"index\":0,\"id\":\"call_1\",\"function\":{\"name\":\"add_node\",\"arguments\":\"{\\\"name\\\":\\\"x\\\"}\"}}]}}]}\n\n")
		_, _ = io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"reasoning_content\":\"second\",\"content\":\"world\"}}],\"usage\":{\"total_tokens\":9}}\n\n")
		_, _ = io.WriteString(w, "data: [DONE]\n\n")
	}))
	defer srv.Close()

	client := NewClient(srv.URL+"/minimax", "mimo-key", "mimo-v1", "", time.Second)
	reply, reasoning, calls, tokens, err := client.completeStreamWithTools(
		context.Background(),
		"mimo-v1",
		[]oaiMsg{{Role: "user", Content: "hi"}},
		nil,
		nil,
		func(string) {},
	)
	if err != nil {
		t.Fatalf("completeStreamWithTools error: %v", err)
	}
	if reply != "hello world" {
		t.Fatalf("unexpected reply: %q", reply)
	}
	if reasoning != "first second" {
		t.Fatalf("unexpected reasoning: %q", reasoning)
	}
	if tokens != 9 {
		t.Fatalf("expected tokens=9, got %d", tokens)
	}
	if len(calls) != 1 || calls[0].Name != "add_node" {
		t.Fatalf("unexpected tool calls: %+v", calls)
	}
	if !json.Valid(calls[0].Arguments) {
		t.Fatalf("expected valid tool arguments json, got %s", string(calls[0].Arguments))
	}
}

func TestMaxCompletionTokensFor_ConfigurableAndPerModelOverride(t *testing.T) {
	globalLimit := 4096
	client := NewClientWithOptions(
		"https://dashscope.aliyuncs.com/compatible-mode/v1",
		"",
		"qwen3.5-flash",
		"",
		time.Second,
		ClientOptions{
			MaxCompletionTokens: &globalLimit,
			ModelMaxCompletion: map[string]int{
				"mimo-thinking":  8192,
				"disabled-model": 0,
			},
		},
	)

	if got := client.maxCompletionTokensFor("qwen3.5-flash"); got == nil || *got != 4096 {
		t.Fatalf("expected global max_completion_tokens=4096, got %#v", got)
	}
	if got := client.maxCompletionTokensFor("mimo-thinking"); got == nil || *got != 8192 {
		t.Fatalf("expected model override max_completion_tokens=8192, got %#v", got)
	}
	if got := client.maxCompletionTokensFor("disabled-model"); got != nil {
		t.Fatalf("expected disabled model to return nil, got %#v", got)
	}
}
