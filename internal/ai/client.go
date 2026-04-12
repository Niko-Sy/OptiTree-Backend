package ai

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"
)

const (
	contextChunkNodeThreshold      = 400
	contextChunkSize               = 400
	maxHistoryMessages             = 20
	defaultMiMoMaxCompletionTokens = 8192
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
	// chatModel is used exclusively for Chat/ChatStream calls.
	// Falls back to defaultModel when empty.
	chatModel  string
	httpClient *http.Client
	// streamClient has no global timeout; context cancellation controls streaming calls.
	streamClient *http.Client
	// maxCompletionTokens controls request-level max_completion_tokens.
	// nil = use provider defaults; 0 = disable field; >0 = explicitly set.
	maxCompletionTokens *int
	// modelMaxCompletionTokens overrides max tokens by exact model name (case-insensitive).
	modelMaxCompletionTokens map[string]int
}

type ClientOptions struct {
	MaxCompletionTokens *int
	ModelMaxCompletion  map[string]int
}

// NewClient creates a new OpenAI-compatible AI client.
// chatModel is used exclusively for Chat/ChatStream; pass "" to fall back to defaultModel.
// apiKey may be empty for services that do not require auth (e.g. local Ollama).
func NewClient(endpoint, apiKey, defaultModel, chatModel string, timeout time.Duration) *Client {
	return NewClientWithOptions(endpoint, apiKey, defaultModel, chatModel, timeout, ClientOptions{})
}

func NewClientWithOptions(endpoint, apiKey, defaultModel, chatModel string, timeout time.Duration, opts ClientOptions) *Client {
	if timeout <= 0 {
		timeout = 120 * time.Second
	}
	var maxCompletionTokens *int
	if opts.MaxCompletionTokens != nil {
		v := *opts.MaxCompletionTokens
		maxCompletionTokens = &v
	}

	return &Client{
		endpoint:                 strings.TrimRight(endpoint, "/"),
		apiKey:                   apiKey,
		defaultModel:             defaultModel,
		chatModel:                chatModel,
		httpClient:               &http.Client{Timeout: timeout},
		streamClient:             &http.Client{Timeout: 0}, // no deadline; rely on context cancellation
		maxCompletionTokens:      maxCompletionTokens,
		modelMaxCompletionTokens: normalizeModelMaxCompletion(opts.ModelMaxCompletion),
	}
}

// --- OpenAI wire types ---

type oaiRequest struct {
	Model               string       `json:"model"`
	Messages            []oaiMsg     `json:"messages"`
	Temperature         float64      `json:"temperature"`
	MaxCompletionTokens *int         `json:"max_completion_tokens,omitempty"`
	Tools               []OAIToolDef `json:"tools,omitempty"`
	ToolChoice          interface{}  `json:"tool_choice,omitempty"`
}

type oaiMsg struct {
	Role             string        `json:"role"`
	Content          string        `json:"content,omitempty"`
	ReasoningContent string        `json:"reasoning_content,omitempty"`
	ToolCalls        []oaiToolCall `json:"tool_calls,omitempty"`
	ToolCallID       string        `json:"tool_call_id,omitempty"`
}

type oaiResponse struct {
	Choices []struct {
		Message      oaiMsg  `json:"message"`
		FinishReason *string `json:"finish_reason"`
	} `json:"choices"`
	Usage *struct {
		TotalTokens int `json:"total_tokens"`
	} `json:"usage,omitempty"`
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
		Model:               model,
		Messages:            messages,
		Temperature:         0.3,
		MaxCompletionTokens: c.maxCompletionTokensFor(model),
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
	c.applyAuthHeader(req, model)

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

// normalizeModelName maps user-facing aliases to actually configured provider models.
// This prevents request-level overrides such as "qwen3" from breaking when the account
// only has access to a concrete deployable model like "qwen3.5-flash".
func normalizeModelName(name string, fallback string) string {
	trimmed := strings.TrimSpace(name)
	if trimmed == "" {
		return strings.TrimSpace(fallback)
	}

	switch strings.ToLower(trimmed) {
	case "qwen3", "qwen-3":
		fallback = strings.TrimSpace(fallback)
		if fallback != "" && !strings.EqualFold(fallback, trimmed) {
			return fallback
		}
		return "qwen3.5-flash"
	default:
		return trimmed
	}
}

// chatModelFor resolves the model for Chat/ChatStream calls.
// Priority: per-request override → dedicated chat model → default model.
func (c *Client) chatModelFor(override string) string {
	if override != "" {
		fallback := c.chatModel
		if fallback == "" {
			fallback = c.defaultModel
		}
		return normalizeModelName(override, fallback)
	}
	if c.chatModel != "" {
		return normalizeModelName(c.chatModel, c.defaultModel)
	}
	return normalizeModelName(c.defaultModel, c.defaultModel)
}

func (c *Client) applyAuthHeader(req *http.Request, model string) {
	if req == nil || strings.TrimSpace(c.apiKey) == "" {
		return
	}
	if isMiMoProvider(c.endpoint, model) {
		req.Header.Set("api-key", c.apiKey)
		return
	}
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
}

func (c *Client) maxCompletionTokensFor(model string) *int {
	modelKey := strings.ToLower(strings.TrimSpace(model))
	if modelKey != "" {
		if v, ok := c.modelMaxCompletionTokens[modelKey]; ok {
			if v <= 0 {
				return nil
			}
			vv := v
			return &vv
		}
	}

	if c.maxCompletionTokens != nil {
		if *c.maxCompletionTokens <= 0 {
			return nil
		}
		v := *c.maxCompletionTokens
		return &v
	}

	if !isMiMoProvider(c.endpoint, model) {
		return nil
	}
	v := defaultMiMoMaxCompletionTokens
	return &v
}

func normalizeModelMaxCompletion(in map[string]int) map[string]int {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]int, len(in))
	for model, v := range in {
		key := strings.ToLower(strings.TrimSpace(model))
		if key == "" {
			continue
		}
		out[key] = v
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func isMiMoProvider(endpoint, model string) bool {
	haystack := strings.ToLower(strings.TrimSpace(endpoint) + " " + strings.TrimSpace(model))
	if haystack == "" {
		return false
	}
	return strings.Contains(haystack, "mimo") || strings.Contains(haystack, "minimax")
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

// Chat performs a synchronous AI conversation about the current graph context.
func (c *Client) Chat(ctx context.Context, req ChatRequest) (*ChatResponse, error) {
	graphTypeName := "fault tree"
	if req.GraphType == "knowledgeGraph" {
		graphTypeName = "knowledge graph"
	}

	fullContextJSON, contextMode := buildFullContextJSON(req.ContextData, req.GraphType)
	schemaHint := graphSchemaHint(req.GraphType)

	sys := fmt.Sprintf(`You are an expert AI assistant helping users analyze and improve %s diagrams.
You may receive prior conversation history and optional graph context payload.
If graph context payload is provided, it is complete for this turn; use it as the primary source of structural truth.
If contextMode is "chunked", reconstruct the full graph by combining all chunks and crossChunkEdges before reasoning.
If contextMode is "chunked", first scan summaryHeader for quick orientation, then verify details in chunks and crossChunkEdges.
First build an internal structural understanding of the graph, then answer the user question.
When relevant, explain key node/edge relationships and logic paths that support your conclusion.
If data is missing for a precise answer, clearly state what is missing and provide the best actionable next step.
Be concise and helpful. Respond in the same language the user uses.
Output ONLY valid JSON (no markdown, no extra text):
{"reply":"<your answer>","suggestions":["<optional short suggestion>"]}
Use an empty array for suggestions if none are needed.
/no_think`, graphTypeName)

	usr := buildUserPrompt(req.GraphType, schemaHint, contextMode, fullContextJSON, req.Message)

	messages := []oaiMsg{{Role: "system", Content: sys}}
	messages = append(messages, historyToOAIMessages(req.History)...)
	messages = append(messages, oaiMsg{Role: "user", Content: usr})

	raw, err := c.complete(ctx, c.chatModelFor(req.Model), messages)
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

// ─── Streaming types ──────────────────────────────────────────────────────────

type oaiStreamRequest struct {
	Model               string       `json:"model"`
	Messages            []oaiMsg     `json:"messages"`
	Temperature         float64      `json:"temperature"`
	Stream              bool         `json:"stream"`
	MaxCompletionTokens *int         `json:"max_completion_tokens,omitempty"`
	Tools               []OAIToolDef `json:"tools,omitempty"`
	ToolChoice          interface{}  `json:"tool_choice,omitempty"`
}

type oaiStreamChunk struct {
	Choices []struct {
		Delta struct {
			Content          string                   `json:"content"`
			ReasoningContent string                   `json:"reasoning_content,omitempty"`
			ToolCalls        []oaiStreamToolCallDelta `json:"tool_calls,omitempty"`
		} `json:"delta"`
		FinishReason *string `json:"finish_reason"`
	} `json:"choices"`
	// Some providers (e.g. DashScope) include usage in the final chunk.
	Usage *struct {
		TotalTokens int `json:"total_tokens"`
	} `json:"usage,omitempty"`
}

// completeStream sends a streaming chat completions request and calls onChunk for each
// content delta received. It returns the total token usage reported by the provider
// (0 if the provider does not include usage in the stream).
func (c *Client) completeStream(ctx context.Context, model string, messages []oaiMsg, onChunk func(string)) (int, error) {
	if c.endpoint == "" {
		return 0, fmt.Errorf("ai: endpoint not configured")
	}

	body, err := json.Marshal(oaiStreamRequest{
		Model:               model,
		Messages:            messages,
		Temperature:         0.7,
		Stream:              true,
		MaxCompletionTokens: c.maxCompletionTokensFor(model),
	})
	if err != nil {
		return 0, fmt.Errorf("ai: marshal stream request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		c.endpoint+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return 0, fmt.Errorf("ai: build stream request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")
	c.applyAuthHeader(req, model)

	resp, err := c.streamClient.Do(req)
	if err != nil {
		return 0, fmt.Errorf("ai: stream http request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(resp.Body)
		return 0, fmt.Errorf("ai: stream request failed (HTTP %d): %s", resp.StatusCode, string(raw))
	}

	var tokensUsed int
	// Qwen3 may emit <think>…</think> segments even with /no_think; filter them out.
	inThink := false

	scanner := bufio.NewScanner(resp.Body)
	// Raise max token buffer to reduce scanner interruptions on large provider deltas.
	scanner.Buffer(make([]byte, 256*1024), 1024*1024)

	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		data := strings.TrimPrefix(line, "data: ")
		if data == "[DONE]" {
			break
		}

		var chunk oaiStreamChunk
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			continue
		}
		if chunk.Usage != nil {
			tokensUsed = chunk.Usage.TotalTokens
		}
		if len(chunk.Choices) == 0 {
			continue
		}
		content := chunk.Choices[0].Delta.Content
		if content == "" {
			continue
		}

		// Filter out Qwen3 thinking blocks (<think>…</think>).
		for len(content) > 0 {
			if inThink {
				end := strings.Index(content, "</think>")
				if end < 0 {
					content = ""
				} else {
					inThink = false
					content = content[end+len("</think>"):]
				}
			} else {
				start := strings.Index(content, "<think>")
				if start < 0 {
					onChunk(content)
					content = ""
				} else {
					if start > 0 {
						onChunk(content[:start])
					}
					inThink = true
					content = content[start+len("<think>"):]
				}
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return tokensUsed, fmt.Errorf("ai: stream read error: %w", err)
	}
	return tokensUsed, nil
}

// ChatStream streams AI replies token by token via onChunk.
// It returns the total tokens consumed and the model name actually used.
func (c *Client) ChatStream(ctx context.Context, req ChatRequest, onChunk func(chunk string)) (tokensUsed int, modelUsed string, err error) {
	graphTypeName := "fault tree"
	if req.GraphType == "knowledgeGraph" {
		graphTypeName = "knowledge graph"
	}

	fullContextJSON, contextMode := buildFullContextJSON(req.ContextData, req.GraphType)
	schemaHint := graphSchemaHint(req.GraphType)

	sys := fmt.Sprintf(`You are an expert AI assistant helping users analyze and improve %s diagrams.
You may receive prior conversation history and optional graph context payload.
If graph context payload is provided, it is complete for this turn; use it as the primary source of structural truth.
If contextMode is "chunked", reconstruct the full graph by combining all chunks and crossChunkEdges before reasoning.
If contextMode is "chunked", first scan summaryHeader for quick orientation, then verify details in chunks and crossChunkEdges.
First build an internal structural understanding of the graph, then answer the user question.
When relevant, explain key node/edge relationships and logic paths that support your conclusion.
If data is missing for a precise answer, clearly state what is missing and provide the best actionable next step.
Be concise, accurate, and helpful. Respond in the same language the user uses.
Respond in plain natural language — do NOT wrap your answer in JSON.
/no_think`, graphTypeName) //你是一位专家级 AI 助手，帮助用户分析和改进 %s 图表。请保持回答简洁、准确且有帮助。请使用与用户相同的语言进行回复。请用自然的纯文本回复——不要将答案包裹在 JSON 格式中。

	usr := buildUserPrompt(req.GraphType, schemaHint, contextMode, fullContextJSON, req.Message)

	messages := []oaiMsg{{Role: "system", Content: sys}}
	messages = append(messages, historyToOAIMessages(req.History)...)
	messages = append(messages, oaiMsg{Role: "user", Content: usr})

	modelUsed = c.chatModelFor(req.Model)
	tokensUsed, err = c.completeStream(ctx, modelUsed, messages, onChunk)
	return
}

func buildFullContextJSON(contextData interface{}, graphType string) (string, string) {
	if contextData == nil {
		return "null", "none"
	}

	chunkedPayload, ok := buildChunkedGraphPayload(contextData, graphType)
	if ok {
		encoded, err := json.Marshal(chunkedPayload)
		if err == nil {
			return string(encoded), "chunked"
		}
	}

	raw, err := json.Marshal(contextData)
	if err != nil {
		return fmt.Sprintf("{\"contextEncodeError\":%q}", err.Error()), "error"
	}
	return string(raw), "full"
}

func graphSchemaHint(graphType string) string {
	if graphType == "knowledgeGraph" {
		return "- nodes/rfNodes: knowledge graph entities.\\n- edges/rfEdges: directed relations between entities, relation label may appear on edge.label or edge.data.label.\\n- Use entity and relation structure as primary evidence for answers."
	}
	return "- nodes: fault tree nodes (top/mid/basic/gate). Key fields may appear as type/name/label/data.nodeType/data.label/probability.\\n- edges: fault-tree logical connections, source/target may appear as source-target or from-to forms.\\n- Use topology and logic-gate paths as primary evidence for answers."
}

func buildUserPrompt(graphType, schemaHint, contextMode, contextPayload, question string) string {
	if contextMode == "none" {
		return fmt.Sprintf("Graph type: %s\nSchema notes:\n%s\nGraph context for this turn: omitted to save tokens; rely on conversation history from previous turns.\nUser question: %s", graphType, schemaHint, question)
	}
	return fmt.Sprintf("Graph type: %s\nSchema notes:\n%s\nContext mode: %s\nGraph context payload:\n%s\nUser question: %s", graphType, schemaHint, contextMode, contextPayload, question)
}

func historyToOAIMessages(history []ChatHistoryMessage) []oaiMsg {
	if len(history) == 0 {
		return []oaiMsg{}
	}

	if len(history) > maxHistoryMessages {
		history = history[len(history)-maxHistoryMessages:]
	}

	messages := make([]oaiMsg, 0, len(history))
	for _, h := range history {
		role := strings.ToLower(strings.TrimSpace(h.Role))

		switch role {
		case "user":
			content := strings.TrimSpace(h.Content)
			if content == "" {
				continue
			}
			messages = append(messages, oaiMsg{Role: role, Content: content})
		case "assistant":
			content := strings.TrimSpace(h.Content)
			reasoning := strings.TrimSpace(h.ReasoningContent)
			toolCalls := historyToolCallsToOAIToolCalls(h.ToolCalls)
			if content == "" && reasoning == "" && len(toolCalls) == 0 {
				continue
			}
			messages = append(messages, oaiMsg{
				Role:             role,
				Content:          content,
				ReasoningContent: reasoning,
				ToolCalls:        toolCalls,
			})
		case "tool":
			content := strings.TrimSpace(h.Content)
			toolCallID := strings.TrimSpace(h.ToolCallID)
			if content == "" || toolCallID == "" {
				continue
			}
			messages = append(messages, oaiMsg{Role: role, Content: content, ToolCallID: toolCallID})
		}
	}
	return messages
}

func historyToolCallsToOAIToolCalls(calls []ToolCall) []oaiToolCall {
	if len(calls) == 0 {
		return nil
	}

	out := make([]oaiToolCall, 0, len(calls))
	for i, call := range calls {
		name := strings.TrimSpace(call.Name)
		if name == "" {
			continue
		}

		id := strings.TrimSpace(call.ID)
		if id == "" {
			id = fmt.Sprintf("history_call_%d", i+1)
		}

		item := oaiToolCall{ID: id, Type: "function"}
		item.Function.Name = name
		item.Function.Arguments = normalizeToolArgumentsJSON(call.Arguments)
		out = append(out, item)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func normalizeToolArgumentsJSON(args json.RawMessage) string {
	raw := bytes.TrimSpace(args)
	if len(raw) == 0 || !json.Valid(raw) {
		return "{}"
	}

	var compact bytes.Buffer
	if err := json.Compact(&compact, raw); err != nil {
		return string(raw)
	}
	return compact.String()
}

func buildChunkedGraphPayload(contextData interface{}, graphType string) (map[string]interface{}, bool) {
	raw, err := json.Marshal(contextData)
	if err != nil {
		return nil, false
	}

	var obj map[string]interface{}
	if err := json.Unmarshal(raw, &obj); err != nil {
		return nil, false
	}

	nodesField, edgesField, nodes, edges, ok := extractGraphArrays(obj, graphType)
	if !ok || len(nodes) <= contextChunkNodeThreshold {
		return nil, false
	}

	chunkCount := (len(nodes) + contextChunkSize - 1) / contextChunkSize
	chunkNodes := make([][]map[string]interface{}, chunkCount)
	chunkEdges := make([][]map[string]interface{}, chunkCount)
	nodeChunkIndex := make(map[string]int, len(nodes))

	for i, n := range nodes {
		chunk := (i / contextChunkSize) + 1
		chunkNodes[chunk-1] = append(chunkNodes[chunk-1], n)
		if id := nodeID(n); id != "" {
			nodeChunkIndex[id] = chunk
		}
	}

	crossChunkEdges := make([]map[string]interface{}, 0)
	for _, e := range edges {
		srcChunk := nodeChunkIndex[edgeSourceID(e)]
		tgtChunk := nodeChunkIndex[edgeTargetID(e)]
		if srcChunk > 0 && tgtChunk > 0 && srcChunk == tgtChunk {
			chunkEdges[srcChunk-1] = append(chunkEdges[srcChunk-1], e)
			continue
		}
		ec := cloneMap(e)
		ec["sourceChunk"] = srcChunk
		ec["targetChunk"] = tgtChunk
		crossChunkEdges = append(crossChunkEdges, ec)
	}

	chunks := make([]map[string]interface{}, 0, chunkCount)
	for i := 0; i < chunkCount; i++ {
		start := i * contextChunkSize
		end := start + len(chunkNodes[i])
		chunks = append(chunks, map[string]interface{}{
			"index":          i + 1,
			"nodeStart":      start,
			"nodeEnd":        end,
			"nodeCount":      len(chunkNodes[i]),
			"edgeCount":      len(chunkEdges[i]),
			"nodes":          chunkNodes[i],
			"edges":          chunkEdges[i],
			"nodeIdRangeRef": fmt.Sprintf("nodes[%d:%d]", start, end),
		})
	}

	summaryHeader := buildChunkSummaryHeader(graphType, nodes, edges, chunkNodes, chunkEdges, crossChunkEdges)

	return map[string]interface{}{
		"contextMode":         "chunked",
		"graphType":           graphType,
		"chunkSize":           contextChunkSize,
		"nodeCount":           len(nodes),
		"edgeCount":           len(edges),
		"chunkCount":          chunkCount,
		"nodesField":          nodesField,
		"edgesField":          edgesField,
		"summaryHeader":       summaryHeader,
		"chunks":              chunks,
		"crossChunkEdges":     crossChunkEdges,
		"reconstructionHint":  "Reconstruct full graph by merging all chunks.nodes/chunks.edges and then adding crossChunkEdges.",
		"indexingDescription": "chunk.index is 1-based. sourceChunk/targetChunk indicate cross-chunk edge endpoints.",
	}, true
}

func buildChunkSummaryHeader(
	graphType string,
	nodes []map[string]interface{},
	edges []map[string]interface{},
	chunkNodes [][]map[string]interface{},
	chunkEdges [][]map[string]interface{},
	crossChunkEdges []map[string]interface{},
) map[string]interface{} {
	nodeTypeCounts := make(map[string]int)
	nodeIDs := make(map[string]struct{}, len(nodes))
	for _, n := range nodes {
		if id := nodeID(n); id != "" {
			nodeIDs[id] = struct{}{}
		}
		t := normalizeNodeCategory(graphType, n)
		nodeTypeCounts[t]++
	}

	inDegree := make(map[string]int)
	outDegree := make(map[string]int)
	for _, e := range edges {
		src := edgeSourceID(e)
		tgt := edgeTargetID(e)
		if src != "" {
			outDegree[src]++
		}
		if tgt != "" {
			inDegree[tgt]++
		}
	}

	isolated := 0
	roots := 0
	leaves := 0
	for id := range nodeIDs {
		in := inDegree[id]
		out := outDegree[id]
		if in == 0 && out == 0 {
			isolated++
		}
		if in == 0 {
			roots++
		}
		if out == 0 {
			leaves++
		}
	}

	chunkNodeCounts := make([]int, 0, len(chunkNodes))
	chunkEdgeCounts := make([]int, 0, len(chunkEdges))
	for i := 0; i < len(chunkNodes); i++ {
		chunkNodeCounts = append(chunkNodeCounts, len(chunkNodes[i]))
		chunkEdgeCounts = append(chunkEdgeCounts, len(chunkEdges[i]))
	}

	return map[string]interface{}{
		"version":             1,
		"purpose":             "quick orientation only; full reasoning must still use chunks and crossChunkEdges",
		"nodeCount":           len(nodes),
		"edgeCount":           len(edges),
		"crossChunkEdgeCount": len(crossChunkEdges),
		"chunkCount":          len(chunkNodes),
		"chunkNodeCounts":     chunkNodeCounts,
		"chunkEdgeCounts":     chunkEdgeCounts,
		"nodeTypeTop":         topCountEntries(nodeTypeCounts, 10),
		"topology": map[string]interface{}{
			"rootLikeNodes":    roots,
			"leafLikeNodes":    leaves,
			"isolatedNodes":    isolated,
			"estimatedDensity": estimateDensity(len(nodes), len(edges)),
		},
	}
}

func normalizeNodeCategory(graphType string, node map[string]interface{}) string {
	if graphType == "knowledgeGraph" {
		if data, ok := node["data"].(map[string]interface{}); ok {
			if s := mapString(data, "entityType"); s != "" {
				return strings.ToLower(s)
			}
		}
		if s := mapString(node, "entityType"); s != "" {
			return strings.ToLower(s)
		}
		if s := mapString(node, "type"); s != "" {
			return strings.ToLower(s)
		}
		return "unknown"
	}

	if data, ok := node["data"].(map[string]interface{}); ok {
		if s := mapString(data, "nodeType"); s != "" {
			return strings.ToLower(s)
		}
	}
	if s := mapString(node, "type"); s != "" {
		return strings.ToLower(s)
	}
	if s := mapString(node, "gateType"); s != "" {
		return "gate:" + strings.ToLower(s)
	}
	return "unknown"
}

func mapString(m map[string]interface{}, key string) string {
	v, ok := m[key]
	if !ok {
		return ""
	}
	s, _ := v.(string)
	return strings.TrimSpace(s)
}

func topCountEntries(counts map[string]int, limit int) []map[string]interface{} {
	type kv struct {
		Key   string
		Count int
	}
	items := make([]kv, 0, len(counts))
	for k, c := range counts {
		items = append(items, kv{Key: k, Count: c})
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].Count == items[j].Count {
			return items[i].Key < items[j].Key
		}
		return items[i].Count > items[j].Count
	})
	if limit > 0 && len(items) > limit {
		items = items[:limit]
	}
	out := make([]map[string]interface{}, 0, len(items))
	for _, it := range items {
		out = append(out, map[string]interface{}{"type": it.Key, "count": it.Count})
	}
	return out
}

func estimateDensity(nodeCount, edgeCount int) float64 {
	if nodeCount <= 1 {
		return 0
	}
	denominator := float64(nodeCount * (nodeCount - 1))
	if denominator == 0 {
		return 0
	}
	value := float64(edgeCount) / denominator
	if value < 0 {
		return 0
	}
	if value > 1 {
		return 1
	}
	return value
}

func extractGraphArrays(obj map[string]interface{}, graphType string) (string, string, []map[string]interface{}, []map[string]interface{}, bool) {
	nodesCandidates := []string{"nodes", "rfNodes"}
	edgesCandidates := []string{"edges", "rfEdges"}
	if graphType == "knowledgeGraph" {
		nodesCandidates = []string{"rfNodes", "nodes"}
		edgesCandidates = []string{"rfEdges", "edges"}
	}

	var nodesField string
	var nodes []map[string]interface{}
	for _, key := range nodesCandidates {
		nodes = toMapSlice(obj[key])
		if len(nodes) > 0 {
			nodesField = key
			break
		}
	}
	if nodesField == "" {
		if arr, ok := obj[nodesCandidates[0]].([]interface{}); ok && len(arr) == 0 {
			nodesField = nodesCandidates[0]
			nodes = []map[string]interface{}{}
		}
	}

	var edgesField string
	var edges []map[string]interface{}
	for _, key := range edgesCandidates {
		edges = toMapSlice(obj[key])
		if len(edges) > 0 {
			edgesField = key
			break
		}
	}
	if edgesField == "" {
		if arr, ok := obj[edgesCandidates[0]].([]interface{}); ok && len(arr) == 0 {
			edgesField = edgesCandidates[0]
			edges = []map[string]interface{}{}
		}
	}

	if nodesField == "" || edgesField == "" {
		return "", "", nil, nil, false
	}
	return nodesField, edgesField, nodes, edges, true
}

func nodeID(node map[string]interface{}) string {
	if v, ok := node["id"]; ok {
		s, _ := v.(string)
		return strings.TrimSpace(s)
	}
	return ""
}

func edgeSourceID(edge map[string]interface{}) string {
	for _, key := range []string{"source", "from", "fromNodeId", "from_node_id"} {
		if v, ok := edge[key]; ok {
			s, _ := v.(string)
			s = strings.TrimSpace(s)
			if s != "" {
				return s
			}
		}
	}
	return ""
}

func edgeTargetID(edge map[string]interface{}) string {
	for _, key := range []string{"target", "to", "toNodeId", "to_node_id"} {
		if v, ok := edge[key]; ok {
			s, _ := v.(string)
			s = strings.TrimSpace(s)
			if s != "" {
				return s
			}
		}
	}
	return ""
}

func toMapSlice(v interface{}) []map[string]interface{} {
	arr, ok := v.([]interface{})
	if !ok {
		return nil
	}
	out := make([]map[string]interface{}, 0, len(arr))
	for _, item := range arr {
		if m, ok := item.(map[string]interface{}); ok {
			out = append(out, m)
		}
	}
	return out
}

func cloneMap(in map[string]interface{}) map[string]interface{} {
	out := make(map[string]interface{}, len(in)+2)
	for k, v := range in {
		out[k] = v
	}
	return out
}
