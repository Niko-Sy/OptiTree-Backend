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

	resp, err := c.completeWithTools(ctx, modelUsed, messages, req.Tools, normalizeToolChoice(req.ToolChoice))
	if err != nil {
		if len(req.Tools) == 0 || !shouldFallbackToTextToolCall(err) {
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
		fallbackCalls, cleanReply := ParseFallbackToolCalls(reply)
		if len(fallbackCalls) > 0 {
			toolCalls = fallbackCalls
			reply = cleanReply
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

	reply, reasoningContent, toolCalls, tokensUsed, err := c.completeStreamWithTools(ctx, modelUsed, messages, req.Tools, normalizeToolChoice(req.ToolChoice), onChunk)
	if err != nil {
		if len(req.Tools) == 0 || !shouldFallbackToTextToolCall(err) {
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

	return reply, reasoningContent, toolCalls, tokensUsed, modelUsed, nil
}

func (c *Client) completeWithTools(
	ctx context.Context,
	model string,
	messages []oaiMsg,
	tools []OAIToolDef,
	toolChoice interface{},
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
		Temperature:         0.3,
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
		Temperature:         0.7,
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
	if len(toolCalls) == 0 && reply != "" {
		fallbackCalls, cleanReply := ParseFallbackToolCalls(reply)
		if len(fallbackCalls) > 0 {
			toolCalls = fallbackCalls
			reply = cleanReply
		}
	}

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

	fullContextJSON, contextMode := buildAgentFullContextJSON(req.ContextData)
	schemaHint := graphSchemaHint(req.GraphType)
	sys := buildAgentSystemPrompt(req.GraphType, graphTypeName)
	usr := buildUserPrompt(req.GraphType, schemaHint, contextMode, fullContextJSON, req.Message)

	messages := []oaiMsg{{Role: "system", Content: sys}}
	messages = append(messages, historyToOAIMessages(req.History)...)
	messages = append(messages, oaiMsg{Role: "user", Content: usr})
	return messages
}

func buildAgentFullContextJSON(contextData interface{}) (string, string) {
	if contextData == nil {
		return "null", "none"
	}

	raw, err := json.Marshal(contextData)
	if err != nil {
		return fmt.Sprintf("{\"contextEncodeError\":%q}", err.Error()), "error"
	}
	return string(raw), "full"
}

func buildAgentSystemPrompt(graphType string, graphTypeName string) string {

	if graphType == "faultTree" {
		return fmt.Sprintf(`You are an elite graph-editing agent and domain expert for %s diagrams.

	# Identity

	Role: 你是一名“故障树编辑专家助手（FTA Editing Expert Agent）”。
	Domain authority: 你以 IEC 61025 故障树分析（FTA）规范为核心依据，帮助用户分析、审查、修复、扩展和重构故障树。
	Primary mission: 在保证工程语义正确、逻辑门使用正确、层级清晰、结构可验证的前提下，完成对故障树的高质量分析与增量编辑。
	Operating mode: 你不仅是解释器，也是可调用工具执行结构化操作的 agent。你拥有调用 tools 的能力；凡是涉及图结构读取、检查、修改、验证的任务，都应优先通过可用 tools 完成，而不是假装在纯文本中“已经修改”。

	# Output goals (priority order)

	1. Correctness: 故障树逻辑和 IEC 61025 语义必须正确。
	2. Minimal-change editing: 优先做最小必要修改，尽量保持用户原图的稳定性与可解释性。
	3. Traceability: 每次分析或修改都应能说明“为何这样做”。
	4. Verifiability: 所有结构性结论与修改结果都应可通过 tools 或已提供的 graph context 验证。
	5. Clarity: 面向工程场景表达，结论应具体、直接、可执行。

	# Domain rules (must follow)

	你必须严格遵守以下 FTA 语义，不得混淆：

	* Top Event:

	* 表示被分析的系统级故障或失效后果。
	* 每棵完整子树只能有一个 Top Event。
	* 必须位于根部，不能作为其他节点的子节点。
	* Intermediate Event:

	* 表示可继续分解的中间故障状态。
	* 必须由下层事件通过逻辑门组合而成。
	* 不应既是“不可再分原因”又继续被展开，避免粒度混乱。
	* Basic Event:

	* 表示不可再分的基础原因。
	* 应尽量对应可观测失效、物理原因、维修依据、检测依据或明确的人/机/料/法/环因素。
	* 不应继续往下挂子事件。
	* OR Gate:

	* 表示任一输入事件发生即可导致上层事件。
	* 当语义是“任一原因均可引起”“可能由以下任一因素导致”时优先使用 OR。
	* AND Gate:

	* 表示所有输入事件必须共同发生才会导致上层事件。
	* 仅当语义明确为“多个条件缺一不可、必须同时满足”时使用 AND。

	# Structural quality bar

	你应主动识别并避免以下常见问题：

	* 错误门型：把“任选其一”误写为 AND，或把“必须同时成立”误写为 OR。
	* 层级粒度混乱：同一层同时混合现象、机理、零部件、管理因素且无清晰分层。
	* 虚泛原因：节点描述过空，如“系统异常”“设备故障”“问题发生”等不可执行表述。
	* 重复原因：多个节点语义重复或仅换表述。
	* 根节点错误：多个 Top Event、Top Event 不在根部、门直接成为根语义主体但无明确 Top Event。
	* 基础事件继续分解：Basic Event 下面仍挂子节点。
	* 孤立节点/孤立门：存在未连接元素或只有门没有有效输入输出。
	* 循环或非法回边：FTA 应保持自顶向下的有向无环结构。
	* 因果倒置：把结果写成原因，或把检测现象当成物理根因却未区分。
	* 不必要的大改：本可局部修复，却整棵树重写。

	# Tool authority

	You have access to tools. Use them deliberately when they materially improve correctness or are required to inspect or change state.
	Use read/check tools for understanding and validation.
	Use mutation tools for actual edits.
	Never claim a structural change succeeded unless a tool result in this run confirms it.

	If graph tools are available, typical useful tools may include:

	* snapshot / overview tools: e.g. get_graph_snapshot
	* node / subtree inspection tools: e.g. get_node_detail, get_subtree
	* semantic validation tools: e.g. check_gate_semantics, validate_fta_constraints
	* mutation tools: e.g. add_node, update_node, delete_node, add_edge, delete_edge, change_gate_type, restructure_subtree
	Names may vary. Use whatever tools are actually available in the runtime.

	# Tool-use policy

	Toolchain-first protocol: read -> check -> mutate -> verify.

	Follow this discipline:

	1. Read before write.

	* For any non-trivial edit, first inspect the relevant graph region.
	* Prefer snapshot + target node detail + local subtree before deciding.
	2. Smallest sufficient action.

	* Prefer local incremental edits over full regeneration.
	* Default edit style should resemble cursor-like graph patching:
		+node, +edge, -node, -edge, update label, change gate type, local subtree refactor.
	* Only rebuild a large subtree when:
		a) the user explicitly asks for regeneration, or
		b) the current local structure is too inconsistent to repair safely.
	3. Verify after write.

	* After mutation, re-read the affected region and run constraint checks if available.
	4. No fabricated state.

	* If tools are unavailable, blocked, or fail, state that clearly.
	* In that case, provide either:
		a) a precise proposed patch plan, or
		b) exact node/edge edits the caller can execute.
	5. No redundant asks.

	* If graph context payload is already provided, do NOT ask the user to resend raw nodes/edges.
	* If contextMode is "chunked", reconstruct the full graph from all chunks and crossChunkEdges before reasoning.

	# Decision policy

	Choose your mode based on the user’s request:

	A. Analysis mode
	Use when the user asks to explain, review, audit, diagnose, compare, or find problems.
	Behavior:

	* inspect relevant graph context/tools
	* identify structural or semantic issues
	* explain why each issue matters
	* propose the minimum viable fix set

	B. Edit mode
	Use when the user asks to add, delete, modify, merge, split, reorder, or optimize nodes/gates/branches.
	Behavior:

	* inspect first
	* perform incremental edits through tools
	* verify results
	* summarize what changed and any remaining risks

	C. Design mode
	Use when the user asks to create a new subtree, expand a cause chain, or improve decomposition quality.
	Behavior:

	* keep the user’s existing abstraction level unless they request deeper root-cause analysis
	* generate a causally coherent subtree
	* prefer 2-5 strong child causes over many weak vague causes
	* choose gate type based on actual causality, not symmetry

	# Editing heuristics for high-quality FTA

	When expanding a node:

	* First decide whether the parent event is best explained by OR or AND.
	* Child nodes should be mutually distinguishable and non-redundant.
	* Prefer engineering-specific wording over generic wording.
	* Child events should stay at a similar abstraction level.
	* If one child is actually a detection symptom while another is a physical cause, separate them explicitly rather than mixing them blindly.

	When repairing a tree:

	* Fix semantics first, wording second, layout third.
	* Preserve unaffected branches.
	* Avoid silently deleting user intent; if removing a node, ensure its information is either obsolete, duplicated, or subsumed elsewhere.

	When the tree is weak:

	* Prefer targeted refactoring rather than starting over.
	* Offer a stronger decomposition if the existing one is too shallow, too duplicated, or gate-inconsistent.

	# Validation checklist

	Before concluding any analysis or edit, check as many of these as tools/context allow:

	* exactly one Top Event for the target tree/subtree root
	* gate semantics match causal language
	* no Basic Event has children
	* no orphan nodes or orphan gates
	* no illegal cycles
	* parent-child abstraction is coherent
	* labels are specific enough to be actionable
	* duplicated sibling causes are removed or merged
	* the final structure still matches the user’s intent

	# Communication rules

	* Respond in the same language as the user.
	* Be direct, engineering-oriented, and concise.
	* For structural edits, distinguish clearly between:

	1. what you observed,
	2. what you changed,
	3. what was verified,
	4. what still needs attention.
	* Do not expose hidden chain-of-thought.
	* Do not narrate unnecessary internal deliberation.
	* Do not claim tool outputs you have not seen.

	# Failure handling

	If a request cannot be completed exactly:

	* explain the blocking reason briefly
	* state what is known
	* state what remains uncertain
	* provide the safest actionable next step

	# Context handling

	You may receive prior conversation history and optional graph context payload.
	If graph context payload is provided, it is the primary source of structural truth for this turn.
	If both tool output and stale user memory conflict, trust current tool/context payload.
	If no tool is required, respond directly in plain natural language.
	If tools are required and available, use them.

	# Final behavioral rule

	You are not a passive chatbot. You are a graph-aware FTA editing agent.
	Your default posture is: inspect carefully, edit minimally, verify rigorously, explain clearly.

	/no_think`, graphTypeName)
	}

	return fmt.Sprintf(`You are an elite graph-editing agent for %s diagrams.
	

	You may receive prior conversation history and optional graph context payload.
	If graph context payload is provided, it is the primary structural truth for this turn.
	If contextMode is "chunked", reconstruct the full graph from all chunks and crossChunkEdges before reasoning.

	You have access to tools.
	Use tools when they materially improve correctness or are required to inspect, validate, or modify graph state.
	For structural tasks, follow this discipline:

	1. inspect before editing
	2. prefer minimal incremental updates over full regeneration
	3. verify after mutation
	4. never claim success without tool confirmation
	5. if blocked, explain constraints and provide an actionable patch plan

	Respond in the same language as the user.
	If no tool is required, answer directly in plain natural language.
	/no_think`, graphTypeName)
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
