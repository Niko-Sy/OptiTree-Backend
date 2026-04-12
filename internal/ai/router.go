package ai

import (
	"context"
	"fmt"
	"sort"
	"strings"
)

// RoutedProviderConfig configures model-based routing across multiple AI providers.
type RoutedProviderConfig struct {
	DefaultProvider string
	Providers       map[string]AIProvider
	ModelPrefixes   map[string][]string
}

type modelRoute struct {
	prefix      string
	providerKey string
}

// RoutedProvider dispatches requests to different providers based on model name.
// It supports provider-prefixed models like "mimo:model-name" or "qwen/model-name".
type RoutedProvider struct {
	defaultProvider AIProvider
	providers       map[string]AIProvider
	routes          []modelRoute
}

func NewRoutedProvider(cfg RoutedProviderConfig) (AIProvider, error) {
	providers := normalizeProviders(cfg.Providers)
	if len(providers) == 0 {
		return nil, fmt.Errorf("ai: no providers configured")
	}

	defaultKey := strings.ToLower(strings.TrimSpace(cfg.DefaultProvider))
	if defaultKey == "" {
		if _, ok := providers["qwen"]; ok {
			defaultKey = "qwen"
		} else {
			keys := make([]string, 0, len(providers))
			for k := range providers {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			defaultKey = keys[0]
		}
	}
	defaultProvider, ok := providers[defaultKey]
	if !ok {
		return nil, fmt.Errorf("ai: default provider not found: %s", cfg.DefaultProvider)
	}

	prefixes := normalizeModelPrefixes(cfg.ModelPrefixes)
	routes := buildModelRoutes(providers, prefixes)

	return &RoutedProvider{
		defaultProvider: defaultProvider,
		providers:       providers,
		routes:          routes,
	}, nil
}

func normalizeProviders(in map[string]AIProvider) map[string]AIProvider {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]AIProvider, len(in))
	for key, provider := range in {
		normalized := strings.ToLower(strings.TrimSpace(key))
		if normalized == "" || provider == nil {
			continue
		}
		out[normalized] = provider
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func normalizeModelPrefixes(in map[string][]string) map[string][]string {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string][]string, len(in))
	for key, prefixes := range in {
		normalizedKey := strings.ToLower(strings.TrimSpace(key))
		if normalizedKey == "" {
			continue
		}
		clean := make([]string, 0, len(prefixes))
		for _, prefix := range prefixes {
			p := strings.ToLower(strings.TrimSpace(prefix))
			if p == "" {
				continue
			}
			clean = append(clean, p)
		}
		out[normalizedKey] = clean
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func buildModelRoutes(providers map[string]AIProvider, modelPrefixes map[string][]string) []modelRoute {
	routes := make([]modelRoute, 0)
	for key := range providers {
		prefixes := modelPrefixes[key]
		if len(prefixes) == 0 {
			prefixes = []string{key}
		}
		for _, prefix := range prefixes {
			routes = append(routes, modelRoute{prefix: prefix, providerKey: key})
		}
	}
	sort.Slice(routes, func(i, j int) bool {
		if len(routes[i].prefix) == len(routes[j].prefix) {
			return routes[i].prefix < routes[j].prefix
		}
		return len(routes[i].prefix) > len(routes[j].prefix)
	})
	return routes
}

func (r *RoutedProvider) resolve(model string) (AIProvider, string) {
	trimmed := strings.TrimSpace(model)
	if trimmed == "" {
		return r.defaultProvider, ""
	}

	lowerModel := strings.ToLower(trimmed)
	for providerKey, provider := range r.providers {
		if strings.HasPrefix(lowerModel, providerKey+":") {
			return provider, strings.TrimSpace(trimmed[len(providerKey)+1:])
		}
		if strings.HasPrefix(lowerModel, providerKey+"/") {
			return provider, strings.TrimSpace(trimmed[len(providerKey)+1:])
		}
	}

	for _, route := range r.routes {
		if strings.HasPrefix(lowerModel, route.prefix) {
			if provider, ok := r.providers[route.providerKey]; ok {
				return provider, trimmed
			}
		}
	}

	return r.defaultProvider, trimmed
}

func (r *RoutedProvider) Chat(ctx context.Context, req ChatRequest) (*ChatResponse, error) {
	provider, model := r.resolve(req.Model)
	req.Model = model
	return provider.Chat(ctx, req)
}

func (r *RoutedProvider) ChatStream(ctx context.Context, req ChatRequest, onChunk func(chunk string)) (tokensUsed int, modelUsed string, err error) {
	provider, model := r.resolve(req.Model)
	req.Model = model
	return provider.ChatStream(ctx, req, onChunk)
}

func (r *RoutedProvider) ChatWithTools(ctx context.Context, req AgentChatRequest) (*AgentChatResponse, error) {
	provider, model := r.resolve(req.Model)
	req.Model = model
	return provider.ChatWithTools(ctx, req)
}

func (r *RoutedProvider) ChatStreamWithTools(ctx context.Context, req AgentChatRequest, onChunk func(chunk string)) (reply string, reasoningContent string, toolCalls []ToolCall, tokensUsed int, modelUsed string, err error) {
	provider, model := r.resolve(req.Model)
	req.Model = model
	return provider.ChatStreamWithTools(ctx, req, onChunk)
}
