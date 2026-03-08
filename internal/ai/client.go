package ai

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Client implements AIProvider using an OpenAI-compatible chat completions API.
// The endpoint can point to any compatible service:
//   - OpenAI:        https://api.openai.com/v1
//   - Qwen (Aliyun): https://dashscope.aliyuncs.com/compatible-mode/v1
//   - DeepSeek:      https://api.deepseek.com/v1
//   - Ollama local:  http://localhost:11434/v1
type Client struct {
	endpoint     string
	apiKey       string
	defaultModel string
	httpClient   *http.Client
}

// NewClient creates a new OpenAI-compatible AI client.
// apiKey may be empty for services that do not require auth (e.g. local Ollama).
func NewClient(endpoint, apiKey, defaultModel string, timeout time.Duration) *Client {
	if timeout <= 0 {
		timeout = 120 * time.Second
	}
	return &Client{
		endpoint:     strings.TrimRight(endpoint, "/"),
		apiKey:       apiKey,
		defaultModel: defaultModel,
		httpClient:   &http.Client{Timeout: timeout},
	}
}

// --- OpenAI wire types ---

type oaiRequest struct {
	Model       string   `json:"model"`
	Messages    []oaiMsg `json:"messages"`
	Temperature float64  `json:"temperature"`
}

type oaiMsg struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type oaiResponse struct {
	Choices []struct {
		Message oaiMsg `json:"message"`
	} `json:"choices"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

// complete sends a chat completions request and returns the assistant message content.
func (c *Client) complete(ctx context.Context, model string, messages []oaiMsg) (string, error) {
	if c.endpoint == "" {
		return "", fmt.Errorf("ai: endpoint not configured")
	}

	body, err := json.Marshal(oaiRequest{
		Model:       model,
		Messages:    messages,
		Temperature: 0.3,
	})
	if err != nil {
		return "", fmt.Errorf("ai: marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		c.endpoint+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("ai: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if c.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.apiKey)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("ai: http request: %w", err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("ai: read response: %w", err)
	}

	var oaiResp oaiResponse
	if err := json.Unmarshal(raw, &oaiResp); err != nil {
		return "", fmt.Errorf("ai: parse response (status %d): %w", resp.StatusCode, err)
	}
	if oaiResp.Error != nil {
		return "", fmt.Errorf("ai: provider error: %s", oaiResp.Error.Message)
	}
	if len(oaiResp.Choices) == 0 {
		return "", fmt.Errorf("ai: empty choices in response")
	}
	return oaiResp.Choices[0].Message.Content, nil
}

func (c *Client) modelFor(override string) string {
	if override != "" {
		return override
	}
	return c.defaultModel
}

// extractJSON finds the first complete JSON object in s, tolerating markdown code fences.
func extractJSON(s string) string {
	start := strings.Index(s, "{")
	end := strings.LastIndex(s, "}")
	if start < 0 || end < 0 || end <= start {
		return ""
	}
	return s[start : end+1]
}

// GenerateFaultTree asks the LLM to build a ReactFlow-compatible fault tree from documents.
func (c *Client) GenerateFaultTree(ctx context.Context, req GenerateFaultTreeRequest) (*FaultTreeResult, error) {
	docsText := strings.Join(req.DocumentContents, "\n\n---\n\n")
	if docsText == "" {
		docsText = "(no document content provided)"
	}

	depth := req.Config.Depth
	if depth <= 0 {
		depth = 4
	}
	maxNodes := req.Config.MaxNodes
	if maxNodes <= 0 {
		maxNodes = 30
	}

	sys := fmt.Sprintf(`You are a fault tree analysis (FTA) expert. Analyze the provided technical documents and generate a fault tree JSON for the specified top event.

Output ONLY a valid JSON object — no markdown, no explanation, no code fences:
{
  "nodes": [
    {"id":"n1","type":"topEvent","data":{"label":"<top event text>","nodeType":"topEvent"},"position":{"x":400,"y":50}},
    {"id":"n2","type":"gateNode","data":{"label":"OR","nodeType":"orGate"},"position":{"x":400,"y":180}},
    {"id":"n3","type":"basicEvent","data":{"label":"<basic cause>","nodeType":"basicEvent"},"position":{"x":250,"y":320}}
  ],
  "edges": [
    {"id":"e1","source":"n1","target":"n2"},
    {"id":"e2","source":"n2","target":"n3"}
  ],
  "accuracy": 0.82,
  "summary": "Brief summary of the fault tree"
}

Node type rules:
- topEvent: exactly one (the root failure being analyzed)
- orGate / andGate: logical gate nodes (type="gateNode")
- intermediateEvent: intermediate failure events
- basicEvent: leaf-level causes (no children)
Max tree depth: %d. Max total nodes: %d.
Layout: x spacing ~200px, y spacing ~150px.
accuracy: your estimated 0.0–1.0.`, depth, maxNodes)

	usr := fmt.Sprintf("Technical documents:\n%s\n\nTop event (root failure to analyze): %s", docsText, req.TopEvent)
	raw, err := c.complete(ctx, c.modelFor(req.Config.Model), []oaiMsg{
		{Role: "system", Content: sys},
		{Role: "user", Content: usr},
	})
	if err != nil {
		return nil, err
	}

	jsonStr := extractJSON(raw)
	if jsonStr == "" {
		return nil, fmt.Errorf("ai: no JSON object found in fault-tree response")
	}

	var result FaultTreeResult
	if err := json.Unmarshal([]byte(jsonStr), &result); err != nil {
		return nil, fmt.Errorf("ai: parse fault tree JSON: %w", err)
	}
	if len(result.Nodes) == 0 {
		return nil, fmt.Errorf("ai: generated fault tree has no nodes")
	}
	if result.Summary == "" {
		result.Summary = fmt.Sprintf("生成故障树：%d 个节点，%d 条连接", len(result.Nodes), len(result.Edges))
	}
	return &result, nil
}

// GenerateKnowledgeGraph asks the LLM to extract a ReactFlow-compatible knowledge graph from documents.
func (c *Client) GenerateKnowledgeGraph(ctx context.Context, req GenerateKnowledgeGraphRequest) (*KnowledgeGraphResult, error) {
	docsText := strings.Join(req.DocumentContents, "\n\n---\n\n")
	if docsText == "" {
		docsText = "(no document content provided)"
	}

	sys := `You are a knowledge graph extraction expert specializing in technical documentation.

Output ONLY a valid JSON object — no markdown, no explanation, no code fences:
{
  "nodes": [
    {"id":"e1","type":"entityNode","data":{"label":"<entity name>","entityType":"component","description":"<short desc>"},"position":{"x":100,"y":100}}
  ],
  "edges": [
    {"id":"r1","source":"e1","target":"e2","data":{"label":"<relationship>"}}
  ],
  "entityCount": 5,
  "relationCount": 4,
  "summary": "Brief summary"
}

Allowed entityType values: component, event, cause, effect, system, process.
Space nodes evenly across a 1200×800 canvas.
entityCount and relationCount must match the actual array lengths.`

	usr := fmt.Sprintf("Technical documents:\n%s", docsText)
	raw, err := c.complete(ctx, c.modelFor(req.Config.Model), []oaiMsg{
		{Role: "system", Content: sys},
		{Role: "user", Content: usr},
	})
	if err != nil {
		return nil, err
	}

	jsonStr := extractJSON(raw)
	if jsonStr == "" {
		return nil, fmt.Errorf("ai: no JSON object found in knowledge-graph response")
	}

	var result KnowledgeGraphResult
	if err := json.Unmarshal([]byte(jsonStr), &result); err != nil {
		return nil, fmt.Errorf("ai: parse knowledge graph JSON: %w", err)
	}
	if result.EntityCount == 0 {
		result.EntityCount = len(result.Nodes)
	}
	if result.RelationCount == 0 {
		result.RelationCount = len(result.Edges)
	}
	if result.Summary == "" {
		result.Summary = fmt.Sprintf("提取到 %d 个实体，%d 条关系", result.EntityCount, result.RelationCount)
	}
	return &result, nil
}

// Chat performs a synchronous AI conversation about the current graph context.
func (c *Client) Chat(ctx context.Context, req ChatRequest) (*ChatResponse, error) {
	ctxJSON, _ := json.Marshal(req.ContextData)
	graphTypeName := "fault tree"
	if req.GraphType == "knowledgeGraph" {
		graphTypeName = "knowledge graph"
	}

	sys := fmt.Sprintf(`You are an expert AI assistant helping users analyze and improve %s diagrams.
Be concise and helpful. Respond in the same language the user uses.
Output ONLY valid JSON (no markdown, no extra text):
{"reply":"<your answer>","suggestions":["<optional short suggestion>"]}
Use an empty array for suggestions if none are needed.`, graphTypeName)

	usr := fmt.Sprintf("Current graph structure:\n%s\n\nUser question: %s", string(ctxJSON), req.Message)
	raw, err := c.complete(ctx, c.modelFor(req.Model), []oaiMsg{
		{Role: "system", Content: sys},
		{Role: "user", Content: usr},
	})
	if err != nil {
		return nil, err
	}

	jsonStr := extractJSON(raw)
	if jsonStr == "" {
		return &ChatResponse{Reply: strings.TrimSpace(raw), Suggestions: []string{}}, nil
	}
	var resp ChatResponse
	if err := json.Unmarshal([]byte(jsonStr), &resp); err != nil {
		return &ChatResponse{Reply: strings.TrimSpace(raw), Suggestions: []string{}}, nil
	}
	if resp.Suggestions == nil {
		resp.Suggestions = []string{}
	}
	return &resp, nil
}
