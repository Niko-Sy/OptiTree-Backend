package ai

import (
	"context"
	"testing"
)

type stubProvider struct {
	name string
}

func (s *stubProvider) Chat(ctx context.Context, req ChatRequest) (*ChatResponse, error) {
	return &ChatResponse{Reply: s.name + "|" + req.Model, Suggestions: []string{}}, nil
}

func (s *stubProvider) ChatStream(ctx context.Context, req ChatRequest, onChunk func(chunk string)) (tokensUsed int, modelUsed string, err error) {
	if onChunk != nil {
		onChunk("ok")
	}
	return 1, s.name + "|" + req.Model, nil
}

func (s *stubProvider) ChatWithTools(ctx context.Context, req AgentChatRequest) (*AgentChatResponse, error) {
	return &AgentChatResponse{ModelUsed: s.name + "|" + req.Model}, nil
}

func (s *stubProvider) ChatStreamWithTools(ctx context.Context, req AgentChatRequest, onChunk func(chunk string)) (reply string, reasoningContent string, toolCalls []ToolCall, tokensUsed int, modelUsed string, err error) {
	return "", "", nil, 0, s.name + "|" + req.Model, nil
}

func TestRoutedProvider_RouteByModelPrefixAndDefault(t *testing.T) {
	router, err := NewRoutedProvider(RoutedProviderConfig{
		DefaultProvider: "qwen",
		Providers: map[string]AIProvider{
			"qwen": &stubProvider{name: "qwen"},
			"mimo": &stubProvider{name: "mimo"},
		},
		ModelPrefixes: map[string][]string{
			"qwen": []string{"qwen"},
			"mimo": []string{"mimo"},
		},
	})
	if err != nil {
		t.Fatalf("NewRoutedProvider error: %v", err)
	}

	resp, err := router.ChatWithTools(context.Background(), AgentChatRequest{ChatRequest: ChatRequest{Model: "qwen3.5-flash"}})
	if err != nil {
		t.Fatalf("ChatWithTools error: %v", err)
	}
	if resp.ModelUsed != "qwen|qwen3.5-flash" {
		t.Fatalf("unexpected qwen route: %s", resp.ModelUsed)
	}

	resp, err = router.ChatWithTools(context.Background(), AgentChatRequest{ChatRequest: ChatRequest{Model: "mimo-thinking"}})
	if err != nil {
		t.Fatalf("ChatWithTools error: %v", err)
	}
	if resp.ModelUsed != "mimo|mimo-thinking" {
		t.Fatalf("unexpected mimo route: %s", resp.ModelUsed)
	}

	resp, err = router.ChatWithTools(context.Background(), AgentChatRequest{ChatRequest: ChatRequest{Model: "unknown-model"}})
	if err != nil {
		t.Fatalf("ChatWithTools error: %v", err)
	}
	if resp.ModelUsed != "qwen|unknown-model" {
		t.Fatalf("unexpected default route: %s", resp.ModelUsed)
	}
}

func TestRoutedProvider_ExplicitProviderPrefix(t *testing.T) {
	router, err := NewRoutedProvider(RoutedProviderConfig{
		DefaultProvider: "qwen",
		Providers: map[string]AIProvider{
			"qwen": &stubProvider{name: "qwen"},
			"mimo": &stubProvider{name: "mimo"},
		},
	})
	if err != nil {
		t.Fatalf("NewRoutedProvider error: %v", err)
	}

	resp, err := router.ChatWithTools(context.Background(), AgentChatRequest{ChatRequest: ChatRequest{Model: "mimo:mimo-thinking-v2"}})
	if err != nil {
		t.Fatalf("ChatWithTools error: %v", err)
	}
	if resp.ModelUsed != "mimo|mimo-thinking-v2" {
		t.Fatalf("unexpected prefixed route: %s", resp.ModelUsed)
	}
}
