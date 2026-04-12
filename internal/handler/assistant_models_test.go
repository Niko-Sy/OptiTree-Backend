package handler

import (
	"testing"

	"optitree-backend/internal/config"
)

func TestBuildAssistantModelItems_FromConfig(t *testing.T) {
	cfg := config.AIConfig{
		Models: []config.AIModelConfig{
			{Model: "qwen3.5-flash", Label: "Qwen 3.5", Provider: "qwen", Recommended: true},
			{Value: "mimo-thinking", Label: "MiMo Thinking", Provider: "mimo"},
			{Value: "mimo-thinking", Label: "MiMo duplicate", Provider: "mimo"},
		},
	}

	items := buildAssistantModelItems(cfg)
	if len(items) != 2 {
		t.Fatalf("expected 2 items, got %d: %+v", len(items), items)
	}

	if items[0].Value != "qwen:qwen3.5-flash" || !items[0].Recommended {
		t.Fatalf("unexpected first item: %+v", items[0])
	}
	if items[1].Value != "mimo:mimo-thinking" {
		t.Fatalf("unexpected second item: %+v", items[1])
	}
}

func TestBuildAssistantModelItems_FallbackFromProviders(t *testing.T) {
	cfg := config.AIConfig{
		DefaultProvider: "qwen",
		Providers: map[string]config.AIProviderConfig{
			"qwen": {
				ChatModel:    "qwen3.5-flash",
				DefaultModel: "qwen-plus",
			},
			"mimo": {
				ChatModel:    "mimo-thinking",
				DefaultModel: "mimo-basic",
			},
		},
	}

	items := buildAssistantModelItems(cfg)
	if len(items) == 0 {
		t.Fatal("expected non-empty fallback model list")
	}

	foundQwen := false
	foundMimo := false
	recommendedQwen := false
	for _, item := range items {
		if item.Value == "qwen3.5-flash" {
			foundQwen = true
			if item.Recommended {
				recommendedQwen = true
			}
		}
		if item.Value == "mimo:mimo-thinking" {
			foundMimo = true
		}
	}
	if !foundQwen {
		t.Fatalf("expected qwen model in fallback list, got %+v", items)
	}
	if !foundMimo {
		t.Fatalf("expected mimo model in fallback list, got %+v", items)
	}
	if !recommendedQwen {
		t.Fatalf("expected qwen chat model to be recommended, got %+v", items)
	}
}
