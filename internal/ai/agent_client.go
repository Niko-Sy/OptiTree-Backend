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
)

type streamToolCallAccumulator struct {
	ID        string
	Name      string
	Arguments strings.Builder
}

// ChatWithTools performs a non-streaming assistant call with optional tools.
func (c *Client) ChatWithTools(ctx context.Context, req AgentChatRequest) (*AgentChatResponse, error) {
	messages := buildAgentMessages(req)
	modelUsed := c.chatModelFor(req.Model)

	resp, err := c.completeWithTools(ctx, modelUsed, messages, req.Tools, normalizeToolChoice(req.ToolChoice), req.Temperature)
	if err != nil {
		if !req.EnableFallbackParser || len(req.Tools) == 0 || !shouldFallbackToTextToolCall(err) {
			return nil, err
		}

		fallbackMessages := buildFallbackMessages(messages, req.Tools)
		raw, fallbackErr := c.complete(ctx, modelUsed, fallbackMessages)
		if fallbackErr != nil {
			return nil, fallbackErr
		}
		fallbackCalls, cleanReply := ParseFallbackToolCalls(raw)
		return &AgentChatResponse{
			Reply:      strings.TrimSpace(cleanReply),
			ToolCalls:  fallbackCalls,
			TokensUsed: 0,
			ModelUsed:  modelUsed,
		}, nil
	}

	if resp == nil || len(resp.Choices) == 0 {
		return nil, fmt.Errorf("ai: empty choices in response")
	}

	first := resp.Choices[0].Message
	toolCalls := convertOAIToolCalls(first.ToolCalls)
	reply := strings.TrimSpace(first.Content)
	if len(toolCalls) == 0 && reply != "" {
		if req.EnableFallbackParser {
			fallbackCalls, cleanReply := ParseFallbackToolCalls(reply)
			if len(fallbackCalls) > 0 {
				toolCalls = fallbackCalls
				reply = cleanReply
			}
		} else {
			reply = stripFallbackFunctionCallLines(reply)
		}
	}

	tokensUsed := 0
	if resp.Usage != nil {
		tokensUsed = resp.Usage.TotalTokens
	}

	return &AgentChatResponse{
		Reply:            reply,
		ReasoningContent: strings.TrimSpace(first.ReasoningContent),
		ToolCalls:        toolCalls,
		TokensUsed:       tokensUsed,
		ModelUsed:        modelUsed,
	}, nil
}

// ChatStreamWithTools streams text chunks while accumulating tool call deltas.
func (c *Client) ChatStreamWithTools(ctx context.Context, req AgentChatRequest, onChunk func(string)) (string, string, []ToolCall, int, string, error) {
	if onChunk == nil {
		onChunk = func(string) {}
	}

	messages := buildAgentMessages(req)
	modelUsed := c.chatModelFor(req.Model)

	reply, reasoningContent, toolCalls, tokensUsed, err := c.completeStreamWithTools(ctx, modelUsed, messages, req.Tools, normalizeToolChoice(req.ToolChoice), req.Temperature, onChunk)
	if err != nil {
		if !req.EnableFallbackParser || len(req.Tools) == 0 || !shouldFallbackToTextToolCall(err) {
			return "", "", nil, tokensUsed, modelUsed, err
		}

		fallbackMessages := buildFallbackMessages(messages, req.Tools)
		var replyBuilder strings.Builder
		tokensUsed, err = c.completeStream(ctx, modelUsed, fallbackMessages, func(chunk string) {
			replyBuilder.WriteString(chunk)
			onChunk(chunk)
		})
		if err != nil {
			return "", "", nil, tokensUsed, modelUsed, err
		}

		fallbackCalls, cleanReply := ParseFallbackToolCalls(replyBuilder.String())
		return strings.TrimSpace(cleanReply), "", fallbackCalls, tokensUsed, modelUsed, nil
	}

	if len(toolCalls) == 0 && reply != "" {
		if req.EnableFallbackParser {
			fallbackCalls, cleanReply := ParseFallbackToolCalls(reply)
			if len(fallbackCalls) > 0 {
				toolCalls = fallbackCalls
				reply = strings.TrimSpace(cleanReply)
			}
		} else {
			reply = strings.TrimSpace(stripFallbackFunctionCallLines(reply))
		}
	}

	return reply, reasoningContent, toolCalls, tokensUsed, modelUsed, nil
}

func (c *Client) completeWithTools(
	ctx context.Context,
	model string,
	messages []oaiMsg,
	tools []OAIToolDef,
	toolChoice interface{},
	temperature *float64,
) (*oaiResponse, error) {
	if c.endpoint == "" {
		return nil, fmt.Errorf("ai: endpoint not configured")
	}

	if len(tools) == 0 {
		tools = nil
		toolChoice = nil
	}

	body, err := json.Marshal(oaiRequest{
		Model:               model,
		Messages:            messages,
		Temperature:         resolveToolTemperature(temperature, 0.3),
		MaxCompletionTokens: c.maxCompletionTokensFor(model),
		Tools:               tools,
		ToolChoice:          toolChoice,
	})
	if err != nil {
		return nil, fmt.Errorf("ai: marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost,
		c.endpoint+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("ai: build request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	c.applyAuthHeader(httpReq, model)

	httpResp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("ai: http request: %w", err)
	}
	defer httpResp.Body.Close()

	raw, err := io.ReadAll(httpResp.Body)
	if err != nil {
		return nil, fmt.Errorf("ai: read response: %w", err)
	}

	var parsed oaiResponse
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return nil, fmt.Errorf("ai: parse response (status %d): %w", httpResp.StatusCode, err)
	}
	if parsed.Error != nil {
		return nil, fmt.Errorf("ai: provider error: %s", parsed.Error.Message)
	}
	if len(parsed.Choices) == 0 {
		return nil, fmt.Errorf("ai: empty choices in response")
	}
	return &parsed, nil
}

func (c *Client) completeStreamWithTools(
	ctx context.Context,
	model string,
	messages []oaiMsg,
	tools []OAIToolDef,
	toolChoice interface{},
	temperature *float64,
	onChunk func(string),
) (string, string, []ToolCall, int, error) {
	if c.endpoint == "" {
		return "", "", nil, 0, fmt.Errorf("ai: endpoint not configured")
	}

	if len(tools) == 0 {
		tools = nil
		toolChoice = nil
	}

	body, err := json.Marshal(oaiStreamRequest{
		Model:               model,
		Messages:            messages,
		Temperature:         resolveToolTemperature(temperature, 0.7),
		Stream:              true,
		MaxCompletionTokens: c.maxCompletionTokensFor(model),
		Tools:               tools,
		ToolChoice:          toolChoice,
	})
	if err != nil {
		return "", "", nil, 0, fmt.Errorf("ai: marshal stream request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost,
		c.endpoint+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return "", "", nil, 0, fmt.Errorf("ai: build stream request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "text/event-stream")
	c.applyAuthHeader(httpReq, model)

	httpResp, err := c.streamClient.Do(httpReq)
	if err != nil {
		return "", "", nil, 0, fmt.Errorf("ai: stream http request: %w", err)
	}
	defer httpResp.Body.Close()

	if httpResp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(httpResp.Body)
		return "", "", nil, 0, fmt.Errorf("ai: stream request failed (HTTP %d): %s", httpResp.StatusCode, string(raw))
	}

	acc := make(map[int]*streamToolCallAccumulator)
	indexes := make(map[int]struct{})
	var replyBuilder strings.Builder
	var reasoningBuilder strings.Builder
	var tokensUsed int
	inThink := false

	scanner := bufio.NewScanner(httpResp.Body)
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

		delta := chunk.Choices[0].Delta
		if delta.ReasoningContent != "" {
			reasoningBuilder.WriteString(delta.ReasoningContent)
		}
		content := delta.Content
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
					if content != "" {
						replyBuilder.WriteString(content)
						onChunk(content)
					}
					content = ""
				} else {
					if start > 0 {
						visible := content[:start]
						replyBuilder.WriteString(visible)
						onChunk(visible)
					}
					inThink = true
					content = content[start+len("<think>"):]
				}
			}
		}

		for _, tc := range delta.ToolCalls {
			current, ok := acc[tc.Index]
			if !ok {
				current = &streamToolCallAccumulator{}
				acc[tc.Index] = current
				indexes[tc.Index] = struct{}{}
			}
			if tc.ID != nil && strings.TrimSpace(*tc.ID) != "" {
				current.ID = strings.TrimSpace(*tc.ID)
			}
			if tc.Function != nil {
				if tc.Function.Name != nil && strings.TrimSpace(*tc.Function.Name) != "" {
					current.Name = strings.TrimSpace(*tc.Function.Name)
				}
				if tc.Function.Arguments != nil {
					current.Arguments.WriteString(*tc.Function.Arguments)
				}
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return "", "", nil, tokensUsed, fmt.Errorf("ai: stream read error: %w", err)
	}

	sortedIdx := make([]int, 0, len(indexes))
	for idx := range indexes {
		sortedIdx = append(sortedIdx, idx)
	}
	sort.Ints(sortedIdx)

	toolCalls := make([]ToolCall, 0, len(sortedIdx))
	for _, idx := range sortedIdx {
		item := acc[idx]
		if item == nil || item.Name == "" {
			continue
		}
		callID := strings.TrimSpace(item.ID)
		if callID == "" {
			callID = fmt.Sprintf("call_%d", idx+1)
		}
		toolCalls = append(toolCalls, ToolCall{
			ID:        callID,
			Name:      item.Name,
			Arguments: normalizeToolArguments(item.Arguments.String()),
		})
	}

	reply := strings.TrimSpace(replyBuilder.String())
	return reply, strings.TrimSpace(reasoningBuilder.String()), toolCalls, tokensUsed, nil
}

func convertOAIToolCalls(calls []oaiToolCall) []ToolCall {
	if len(calls) == 0 {
		return []ToolCall{}
	}

	out := make([]ToolCall, 0, len(calls))
	for i, call := range calls {
		name := strings.TrimSpace(call.Function.Name)
		if name == "" {
			continue
		}
		callID := strings.TrimSpace(call.ID)
		if callID == "" {
			callID = fmt.Sprintf("call_%d", i+1)
		}
		out = append(out, ToolCall{
			ID:        callID,
			Name:      name,
			Arguments: normalizeToolArguments(call.Function.Arguments),
		})
	}
	return out
}

func normalizeToolChoice(choice string) interface{} {
	v := strings.ToLower(strings.TrimSpace(choice))
	switch v {
	case "":
		return nil
	case "required":
		// DashScope compatibility: fallback to auto when required is not supported.
		return "auto"
	case "auto", "none":
		return v
	default:
		return "auto"
	}
}

func shouldFallbackToTextToolCall(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	hasToolKeyword := strings.Contains(msg, "tool") || strings.Contains(msg, "function") || strings.Contains(msg, "tool_choice")
	hasUnsupportedHint := strings.Contains(msg, "unsupported") ||
		strings.Contains(msg, "not support") ||
		strings.Contains(msg, "unknown") ||
		strings.Contains(msg, "invalid") ||
		strings.Contains(msg, "unrecognized")
	return hasToolKeyword && hasUnsupportedHint
}

func buildAgentMessages(req AgentChatRequest) []oaiMsg {
	graphTypeName := "fault tree"
	if req.GraphType == "knowledgeGraph" {
		graphTypeName = "knowledge graph"
	}

	fullContextJSON, contextMode := buildAgentFullContextJSON(req.GraphType, req.ContextData, req.Message, req.FullContextThreshold)
	schemaHint := graphSchemaHint(req.GraphType)
	sys := buildAgentSystemPrompt(req, graphTypeName)
	usr := buildUserPrompt(req.GraphType, schemaHint, contextMode, fullContextJSON, req.Message)

	messages := []oaiMsg{{Role: "system", Content: sys}}
	messages = append(messages, historyToOAIMessages(req.History)...)
	messages = append(messages, oaiMsg{Role: "user", Content: usr})
	return messages
}

func buildAgentFullContextJSON(graphType string, contextData interface{}, question string, fullContextThreshold int) (string, string) {
	if contextData == nil {
		return "null", "none"
	}

	if wantsFullGraphContext(question) {
		if chunkedPayload, ok := buildChunkedGraphPayload(contextData, graphType); ok {
			raw, err := json.Marshal(chunkedPayload)
			if err == nil {
				return string(raw), "chunked"
			}
		}
	}

	if summaryPayload, ok := buildAgentSummaryContextPayload(graphType, contextData, fullContextThreshold); ok {
		raw, err := json.Marshal(summaryPayload)
		if err == nil {
			return string(raw), "summary"
		}
	}

	raw, err := json.Marshal(contextData)
	if err != nil {
		return fmt.Sprintf("{\"contextEncodeError\":%q}", err.Error()), "error"
	}
	return string(raw), "full"
}

func buildFallbackMessages(messages []oaiMsg, tools []OAIToolDef) []oaiMsg {
	if len(tools) == 0 {
		return messages
	}

	toolLines := make([]string, 0, len(tools))
	for _, tool := range tools {
		name := strings.TrimSpace(tool.Function.Name)
		if name == "" {
			continue
		}
		desc := strings.TrimSpace(tool.Function.Description)
		paramSchema := strings.TrimSpace(string(tool.Function.Parameters))
		if paramSchema == "" {
			paramSchema = "{}"
		}
		toolLines = append(toolLines, fmt.Sprintf("- %s: %s | parameters schema: %s", name, desc, paramSchema))
	}

	if len(toolLines) == 0 {
		return messages
	}

	fallbackInstruction := "When you need to call a tool, output one line per call using exactly this format: " +
		"FUNCTION_CALL: tool_name({\"arg\":\"value\"}). Do not use markdown code fences for tool calls.\n" +
		"Available tools:\n" + strings.Join(toolLines, "\n")

	out := make([]oaiMsg, 0, len(messages))
	if len(messages) > 0 && messages[0].Role == "system" {
		out = append(out, oaiMsg{Role: "system", Content: strings.TrimSpace(messages[0].Content) + "\n\n" + fallbackInstruction})
		out = append(out, messages[1:]...)
		return out
	}

	out = append(out, oaiMsg{Role: "system", Content: fallbackInstruction})
	out = append(out, messages...)
	return out
}

func resolveToolTemperature(value *float64, defaultValue float64) float64 {
	if value == nil {
		return defaultValue
	}
	t := *value
	if t < 0 {
		return 0
	}
	if t > 2 {
		return 2
	}
	return t
}

func stripFallbackFunctionCallLines(raw string) string {
	normalized := strings.ReplaceAll(raw, "\r\n", "\n")
	lines := strings.Split(normalized, "\n")
	kept := make([]string, 0, len(lines))
	inFence := false
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "```") {
			inFence = !inFence
		}
		if strings.Contains(strings.ToUpper(trimmed), "FUNCTION_CALL:") {
			continue
		}
		if inFence && strings.Contains(strings.ToUpper(line), "FUNCTION_CALL") {
			continue
		}
		kept = append(kept, line)
	}
	return strings.Join(kept, "\n")
}

func buildAgentSummaryContextPayload(graphType string, contextData interface{}, fullContextThreshold int) (map[string]interface{}, bool) {
	raw, err := json.Marshal(contextData)
	if err != nil {
		return nil, false
	}

	var obj map[string]interface{}
	if err := json.Unmarshal(raw, &obj); err != nil {
		return nil, false
	}

	nodesField, edgesField, nodes, edges, ok := extractGraphArrays(obj, graphType)
	if !ok {
		return nil, false
	}
	if fullContextThreshold <= 0 {
		fullContextThreshold = 120
	}
	edgeThreshold := fullContextThreshold * 2
	if edgeThreshold < 200 {
		edgeThreshold = 200
	}
	if len(nodes) <= fullContextThreshold && len(edges) <= edgeThreshold {
		return nil, false
	}

	nodeSample := 50
	if len(nodes) < nodeSample {
		nodeSample = len(nodes)
	}
	edgeSample := 80
	if len(edges) < edgeSample {
		edgeSample = len(edges)
	}

	chunkNodes := [][]map[string]interface{}{nodes[:nodeSample]}
	chunkEdges := [][]map[string]interface{}{edges[:edgeSample]}
	summaryHeader := buildChunkSummaryHeader(graphType, nodes, edges, chunkNodes, chunkEdges, nil)

	return map[string]interface{}{
		"contextMode":   "summary",
		"graphType":     graphType,
		"nodesField":    nodesField,
		"edgesField":    edgesField,
		"nodeCount":     len(nodes),
		"edgeCount":     len(edges),
		"summaryHeader": summaryHeader,
		"nodeSamples":   nodes[:nodeSample],
		"edgeSamples":   edges[:edgeSample],
		"note":          "Context is summarized due to graph size; use read tools before mutation.",
	}, true
}

func wantsFullGraphContext(question string) bool {
	q := strings.ToLower(strings.TrimSpace(question))
	if q == "" {
		return false
	}
	fullHints := []string{
		"全图遍历",
		"遍历全图",
		"完整上下文",
		"完整图",
		"full graph",
		"full context",
		"scan entire",
	}
	for _, hint := range fullHints {
		if strings.Contains(q, hint) {
			return true
		}
	}
	return false
}
