package ai

import "context"

// AIProvider defines all AI capabilities.
// Implementations use an OpenAI-compatible HTTP API so any endpoint
// (Qwen, DeepSeek, OpenAI, Ollama, etc.) can be swapped via config without code changes.
type AIProvider interface {
	GenerateFaultTree(ctx context.Context, req GenerateFaultTreeRequest) (*FaultTreeResult, error)
	GenerateKnowledgeGraph(ctx context.Context, req GenerateKnowledgeGraphRequest) (*KnowledgeGraphResult, error)
	Chat(ctx context.Context, req ChatRequest) (*ChatResponse, error)
	// ChatStream streams the AI reply token by token via onChunk callbacks.
	// Returns the total tokens consumed and the actual model name used.
	ChatStream(ctx context.Context, req ChatRequest, onChunk func(chunk string)) (tokensUsed int, modelUsed string, err error)
}

// GenerateConfig holds per-request LLM parameters.
type GenerateConfig struct {
	Quality  string // fast | balanced | precise
	Model    string // model override; empty = use client default
	Depth    int
	MaxNodes int
}

// GenerateFaultTreeRequest is the input to GenerateFaultTree.
type GenerateFaultTreeRequest struct {
	DocumentContents []string // raw text extracted from uploaded documents
	TopEvent         string
	Config           GenerateConfig
}

// FaultTreeResult is the structured AI output, compatible with the React Flow fault tree editor.
type FaultTreeResult struct {
	Nodes    []map[string]interface{} `json:"nodes"`
	Edges    []map[string]interface{} `json:"edges"`
	Accuracy float64                  `json:"accuracy"`
	Summary  string                   `json:"summary"`
}

// GenerateKnowledgeGraphRequest is the input to GenerateKnowledgeGraph.
type GenerateKnowledgeGraphRequest struct {
	DocumentContents []string
	Config           GenerateConfig
}

// KnowledgeGraphResult is the structured AI output, compatible with the React Flow knowledge graph editor.
type KnowledgeGraphResult struct {
	Nodes         []map[string]interface{} `json:"nodes"`
	Edges         []map[string]interface{} `json:"edges"`
	EntityCount   int                      `json:"entityCount"`
	RelationCount int                      `json:"relationCount"`
	Summary       string                   `json:"summary"`
}

// ChatRequest is the input to Chat.
type ChatRequest struct {
	ContextData interface{} // full graph context (nodes/edges)
	GraphType   string      // "faultTree" | "knowledgeGraph"
	Message     string
	Model       string // optional model override
}

// ChatResponse is the synchronous AI reply.
type ChatResponse struct {
	Reply       string   `json:"reply"`
	Suggestions []string `json:"suggestions"`
}
