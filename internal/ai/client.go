package ai

import (
	"bufio"
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
	// chatModel is used exclusively for Chat/ChatStream calls.
	// Falls back to defaultModel when empty.
	chatModel  string
	httpClient *http.Client
	// streamClient has no global timeout; context cancellation controls streaming calls.
	streamClient *http.Client
}

// NewClient creates a new OpenAI-compatible AI client.
// chatModel is used exclusively for Chat/ChatStream; pass "" to fall back to defaultModel.
// apiKey may be empty for services that do not require auth (e.g. local Ollama).
func NewClient(endpoint, apiKey, defaultModel, chatModel string, timeout time.Duration) *Client {
	if timeout <= 0 {
		timeout = 120 * time.Second
	}
	return &Client{
		endpoint:     strings.TrimRight(endpoint, "/"),
		apiKey:       apiKey,
		defaultModel: defaultModel,
		chatModel:    chatModel,
		httpClient:   &http.Client{Timeout: timeout},
		streamClient: &http.Client{Timeout: 0}, // no deadline; rely on context cancellation
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

// chatModelFor resolves the model for Chat/ChatStream calls.
// Priority: per-request override → dedicated chat model → default model.
func (c *Client) chatModelFor(override string) string {
	if override != "" {
		return override
	}
	if c.chatModel != "" {
		return c.chatModel
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
	graphTypeName := "fault tree"
	if req.GraphType == "knowledgeGraph" {
		graphTypeName = "knowledge graph"
	}

	contextSummary := buildContextSummary(req.ContextData, req.GraphType)

	sys := fmt.Sprintf(`You are an expert AI assistant helping users analyze and improve %s diagrams.
Be concise and helpful. Respond in the same language the user uses.
Output ONLY valid JSON (no markdown, no extra text):
{"reply":"<your answer>","suggestions":["<optional short suggestion>"]}
Use an empty array for suggestions if none are needed.
/no_think`, graphTypeName)

	usr := fmt.Sprintf("%s\nUser question: %s", contextSummary, req.Message)
	raw, err := c.complete(ctx, c.chatModelFor(req.Model), []oaiMsg{
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

// ─── Streaming types ──────────────────────────────────────────────────────────

type oaiStreamRequest struct {
	Model       string   `json:"model"`
	Messages    []oaiMsg `json:"messages"`
	Temperature float64  `json:"temperature"`
	Stream      bool     `json:"stream"`
}

type oaiStreamChunk struct {
	Choices []struct {
		Delta struct {
			Content string `json:"content"`
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
		Model:       model,
		Messages:    messages,
		Temperature: 0.7,
		Stream:      true,
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
	if c.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.apiKey)
	}

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
	scanner.Buffer(make([]byte, 128*1024), 128*1024)

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

	contextSummary := buildContextSummary(req.ContextData, req.GraphType)

	sys := fmt.Sprintf(`You are an expert AI assistant helping users analyze and improve %s diagrams.
Be concise, accurate, and helpful. Respond in the same language the user uses.
Respond in plain natural language — do NOT wrap your answer in JSON.
/no_think`, graphTypeName) //你是一位专家级 AI 助手，帮助用户分析和改进 %s 图表。请保持回答简洁、准确且有帮助。请使用与用户相同的语言进行回复。请用自然的纯文本回复——不要将答案包裹在 JSON 格式中。

	usr := fmt.Sprintf("%s\nUser question: %s", contextSummary, req.Message)

	modelUsed = c.chatModelFor(req.Model)
	tokensUsed, err = c.completeStream(ctx, modelUsed, []oaiMsg{
		{Role: "system", Content: sys},
		{Role: "user", Content: usr},
	}, onChunk)
	return
}

// ─── Context summary builders ─────────────────────────────────────────────────

// buildContextSummary converts raw graph context (nodes/edges) into a structured
// natural-language description so the LLM receives compact, readable input instead
// of a large raw JSON blob.
func buildContextSummary(contextData interface{}, graphType string) string {
	raw, err := json.Marshal(contextData)
	if err != nil || contextData == nil {
		return "(no graph context provided)\n"
	}

	// Decode into a loose struct to support both snake_case and camelCase field names.
	var ctx struct {
		Nodes   interface{} `json:"nodes"`
		Edges   interface{} `json:"edges"`
		RfNodes interface{} `json:"rfNodes"`
		RfEdges interface{} `json:"rfEdges"`
	}
	if err := json.Unmarshal(raw, &ctx); err != nil {
		return "(unable to parse graph context)\n"
	}

	nodes := toMapSlice(ctx.Nodes)
	edges := toMapSlice(ctx.Edges)
	// Knowledge graphs may use rfNodes/rfEdges instead of nodes/edges.
	if len(nodes) == 0 {
		nodes = toMapSlice(ctx.RfNodes)
	}
	if len(edges) == 0 {
		edges = toMapSlice(ctx.RfEdges)
	}

	if graphType == "knowledgeGraph" {
		return buildKGSummary(nodes, edges)
	}
	return buildFaultTreeSummary(nodes, edges)
}

func buildFaultTreeSummary(nodes []map[string]interface{}, edges []map[string]interface{}) string {
	var sb strings.Builder
	sb.WriteString("[故障树上下文]\n")
	sb.WriteString(fmt.Sprintf("- 节点总数：%d，边总数：%d\n", len(nodes), len(edges)))

	// Build the set of connected node IDs for isolated-node detection.
	connected := make(map[string]bool, len(edges)*2)
	for _, e := range edges {
		if src := edgeSrc(e); src != "" {
			connected[src] = true
		}
		if tgt := edgeTgt(e); tgt != "" {
			connected[tgt] = true
		}
	}

	var topEventLabel string
	var orGates, andGates, notGates []string

	type basicEvt struct {
		label   string
		prob    float64
		hasProb bool
	}
	var basicEvents []basicEvt
	var midEvents []string
	var isolated []string

	for _, n := range nodes {
		id := getStr(n, "id")
		nType := getStr(n, "type")
		data := getMap(n, "data")
		label := nType
		nodeType := nType
		if data != nil {
			if l := getStr(data, "label"); l != "" {
				label = l
			}
			if nt := getStr(data, "nodeType"); nt != "" {
				nodeType = nt
			}
		}
		lnType := strings.ToLower(nodeType)

		switch {
		case lnType == "topevent":
			topEventLabel = label
		case lnType == "orgate":
			orGates = append(orGates, id)
		case lnType == "andgate":
			andGates = append(andGates, id)
		case lnType == "notgate":
			notGates = append(notGates, id)
		case lnType == "basicevent":
			be := basicEvt{label: label}
			if data != nil {
				if p, ok := getFloat(data, "probability"); ok {
					be.prob = p
					be.hasProb = true
				}
			}
			basicEvents = append(basicEvents, be)
		case lnType == "intermediateevent" || lnType == "midevent":
			midEvents = append(midEvents, label)
		}

		if len(edges) > 0 && !connected[id] {
			isolated = append(isolated, id)
		}
	}

	if topEventLabel != "" {
		sb.WriteString(fmt.Sprintf("- 顶事件：\"%s\"\n", topEventLabel))
	}

	var gateDesc []string
	if len(orGates) > 0 {
		gateDesc = append(gateDesc, fmt.Sprintf("OR门×%d(%s)", len(orGates), strings.Join(orGates, ", ")))
	}
	if len(andGates) > 0 {
		gateDesc = append(gateDesc, fmt.Sprintf("AND门×%d(%s)", len(andGates), strings.Join(andGates, ", ")))
	}
	if len(notGates) > 0 {
		gateDesc = append(gateDesc, fmt.Sprintf("NOT门×%d(%s)", len(notGates), strings.Join(notGates, ", ")))
	}
	if len(gateDesc) > 0 {
		sb.WriteString(fmt.Sprintf("- 逻辑门列表：%s\n", strings.Join(gateDesc, "，")))
	}

	if len(basicEvents) > 0 {
		sb.WriteString("- 基本事件（附概率）：\n")
		for _, be := range basicEvents {
			if be.hasProb {
				sb.WriteString(fmt.Sprintf("    %s（%.3f）\n", be.label, be.prob))
			} else {
				sb.WriteString(fmt.Sprintf("    %s\n", be.label))
			}
		}
	}

	if len(midEvents) > 0 {
		sb.WriteString(fmt.Sprintf("- 中间事件：%s\n", strings.Join(midEvents, "、")))
	}

	if len(isolated) > 0 {
		sb.WriteString(fmt.Sprintf("- 孤立节点（无连接）：%s\n", strings.Join(isolated, "、")))
	}

	return sb.String()
}

func buildKGSummary(nodes []map[string]interface{}, edges []map[string]interface{}) string {
	var sb strings.Builder
	sb.WriteString("[知识图谱上下文]\n")
	sb.WriteString(fmt.Sprintf("- 实体总数：%d，关系总数：%d\n", len(nodes), len(edges)))

	// Build id→label map for edge descriptions.
	idToLabel := make(map[string]string, len(nodes))

	maxNodes := 20
	if len(nodes) < maxNodes {
		maxNodes = len(nodes)
	}
	if maxNodes > 0 {
		sb.WriteString("- 实体列表（最多20个）：\n")
		for _, n := range nodes[:maxNodes] {
			id := getStr(n, "id")
			data := getMap(n, "data")
			label := getStr(n, "label")
			entityType := ""
			if data != nil {
				if l := getStr(data, "label"); l != "" {
					label = l
				}
				entityType = getStr(data, "entityType")
			}
			idToLabel[id] = label
			if entityType != "" {
				sb.WriteString(fmt.Sprintf("    %s [%s]\n", label, entityType))
			} else {
				sb.WriteString(fmt.Sprintf("    %s\n", label))
			}
		}
	}

	maxEdges := 15
	if len(edges) < maxEdges {
		maxEdges = len(edges)
	}
	if maxEdges > 0 {
		sb.WriteString("- 关系列表（最多15条）：\n")
		for _, e := range edges[:maxEdges] {
			src := edgeSrc(e)
			tgt := edgeTgt(e)
			relLabel := getStr(e, "label")
			if relLabel == "" {
				if data := getMap(e, "data"); data != nil {
					relLabel = getStr(data, "label")
				}
			}
			srcLabel := idToLabel[src]
			if srcLabel == "" {
				srcLabel = src
			}
			tgtLabel := idToLabel[tgt]
			if tgtLabel == "" {
				tgtLabel = tgt
			}
			if relLabel != "" {
				sb.WriteString(fmt.Sprintf("    %s -[%s]-> %s\n", srcLabel, relLabel, tgtLabel))
			} else {
				sb.WriteString(fmt.Sprintf("    %s --> %s\n", srcLabel, tgtLabel))
			}
		}
	}

	return sb.String()
}

// ─── Small map helpers ────────────────────────────────────────────────────────

func getStr(m map[string]interface{}, key string) string {
	if v, ok := m[key]; ok {
		s, _ := v.(string)
		return s
	}
	return ""
}

func getMap(m map[string]interface{}, key string) map[string]interface{} {
	if v, ok := m[key]; ok {
		sub, _ := v.(map[string]interface{})
		return sub
	}
	return nil
}

func getFloat(m map[string]interface{}, key string) (float64, bool) {
	v, ok := m[key]
	if !ok {
		return 0, false
	}
	switch f := v.(type) {
	case float64:
		return f, true
	case float32:
		return float64(f), true
	}
	return 0, false
}

func toMapSlice(v interface{}) []map[string]interface{} {
	arr, ok := v.([]interface{})
	if !ok {
		return nil
	}
	result := make([]map[string]interface{}, 0, len(arr))
	for _, item := range arr {
		if m, ok := item.(map[string]interface{}); ok {
			result = append(result, m)
		}
	}
	return result
}

func edgeSrc(e map[string]interface{}) string {
	if s := getStr(e, "source"); s != "" {
		return s
	}
	return getStr(e, "from")
}

func edgeTgt(e map[string]interface{}) string {
	if t := getStr(e, "target"); t != "" {
		return t
	}
	return getStr(e, "to")
}
