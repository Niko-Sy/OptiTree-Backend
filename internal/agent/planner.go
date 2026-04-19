package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"optitree-backend/internal/ai"
	"optitree-backend/internal/graph_ops"
)

const (
	planStepPending = "pending"
	planStepDone    = "done"
	planStepFailed  = "failed"
)

type failedAttempt struct {
	Round int    `json:"round"`
	Tool  string `json:"tool"`
	Error string `json:"error"`
}

type executionPlanStep struct {
	ID               int             `json:"id"`
	Description      string          `json:"description"`
	ExpectedTool     string          `json:"expectedTool,omitempty"`
	Purpose          string          `json:"purpose,omitempty"`
	SuccessCriterion string          `json:"successCriterion,omitempty"`
	Status           string          `json:"status,omitempty"`
	Result           string          `json:"result,omitempty"`
	Round            int             `json:"round,omitempty"`
	FailedAttempts   []failedAttempt `json:"failedAttempts,omitempty"`
}

type executionPlan struct {
	Goal            string              `json:"goal"`
	Steps           []executionPlanStep `json:"steps"`
	BlockedReason   string              `json:"blockedReason,omitempty"`
	SuccessfulTools map[string]bool     `json:"successfulTools,omitempty"`
}

type plannerPhaseInput struct {
	Message       string
	Model         string
	GraphType     string
	ContextData   interface{}
	History       []ai.ChatHistoryMessage
	ToolGuide     string
	ReadOnly      bool
	PromptVersion string
	PriorFailures []failedAttempt
}

func (s *AgentService) runPlannerPhase(ctx context.Context, input plannerPhaseInput) (*executionPlan, error) {
	if s == nil || s.provider == nil {
		return nil, errors.New("planner provider unavailable")
	}

	temperature := 0.05
	resp, err := s.provider.ChatWithTools(ctx, ai.AgentChatRequest{
		ChatRequest: ai.ChatRequest{
			ContextData: input.ContextData,
			GraphType:   input.GraphType,
			Message:     buildPlannerPrompt(input.Message, input.ToolGuide, input.GraphType, input.ReadOnly, input.PriorFailures),
			Model:       strings.TrimSpace(input.Model),
			History:     append([]ai.ChatHistoryMessage(nil), input.History...),
		},
		Tools:                nil,
		ToolChoice:           "none",
		ToolGuide:            strings.TrimSpace(input.ToolGuide),
		PromptVersion:        strings.TrimSpace(input.PromptVersion),
		ReadOnly:             input.ReadOnly,
		FullContextThreshold: s.cfg.FullContextNodeThreshold,
		Temperature:          &temperature,
		EnableFallbackParser: s.cfg.EnableFallbackParser,
	})
	if err != nil {
		return nil, err
	}
	if resp == nil {
		return nil, errors.New("planner returned nil response")
	}

	plannerReply := strings.TrimSpace(resp.Reply)
	if plannerReply == "" {
		plannerReply = strings.TrimSpace(resp.ReasoningContent)
	}
	if plannerReply == "" {
		return nil, errors.New("planner returned empty content")
	}

	return parseExecutionPlan(plannerReply, input.Message, input.ReadOnly, input.ToolGuide)
}

func buildPlannerPrompt(userGoal, toolGuide, graphType string, readOnly bool, priorFailures []failedAttempt) string {
	goal := strings.TrimSpace(userGoal)
	if goal == "" {
		goal = "完成用户目标"
	}

	allowedTools := parseAllowedToolNamesFromGuide(toolGuide)
	allowedList := "none"
	if len(allowedTools) > 0 {
		allowedList = strings.Join(allowedTools, ", ")
	}

	modeLabel := "write-capable"
	if readOnly {
		modeLabel = "read-only"
	}

	graphLabel := strings.TrimSpace(graphType)
	if graphLabel == "" {
		graphLabel = "faultTree"
	}

	failureHint := formatPlannerPriorFailures(priorFailures)

	return fmt.Sprintf(`You are now in PLANNER PHASE for a graph agent.
Do not execute tools and do not provide final answer in this phase.

Output must be a STRICT JSON object only (no markdown, no code block, no explanation).
The output must start with { and end with }.
{
  "goal": "string",
  "steps": [
    {
      "id": 1,
      "description": "step description in Chinese",
      "expectedTool": "single tool name from allowed tools, or empty string",
      "purpose": "why this step is needed in Chinese",
      "successCriterion": "observable success signal in Chinese"
    }
  ]
}

Hard rules:
- Plan 2-5 concrete and non-overlapping steps.
- Each step must be actionable and minimal.
- expectedTool must be a SINGLE tool name (no slash, no list).
- If no tool is needed for a step, set expectedTool to empty string.
- If mode is read-only, never use mutation or hybrid preview tools.
- Use concise Chinese for goal/description/purpose.
- Every step must include successCriterion.
- Do not include trailing commas or comments in JSON.
- Mutation steps must be concrete and executable, not abstract labels.
- BAD step example: "执行最小必要结构修复".
- GOOD step example: "move_node(q1_N009,newParentId=q1_N005) 将 basicEvent 的子节点迁移到中间事件".
- Each mutation step should include target node IDs and expected structural change.

CRITICAL fault-tree tool constraints:
- add_gate(parentId, childIds, gateType) rewires EXISTING parent->child edges only. Do not use it to create a new connection.
- There is no dedicated edge deletion tool in available tools. Remove wrong connection by move_node or deleting/recreating wrong node.
- move_node(nodeId, newParentId): newParentId must be a valid parent in current structure; avoid creating cycles.
- add_node requires a valid gate-like parent in fault tree mutation flow; if parent relation is missing, repair structure first.
- For heavily broken structures, prefer batch_operations with repairMode=true to do phased restructuring.
- For reversed relation or cycle fixes, prefer atomic batch_operations with validate before and after.

Recent failed attempts (do NOT repeat same failing strategy):
%s

Allowed tools (choose expectedTool only from this set):
%s

graph_type=%s
mode=%s
user_goal=%s`, failureHint, allowedList, graphLabel, modeLabel, goal)
}

func parseExecutionPlan(raw, fallbackGoal string, readOnly bool, toolGuide string) (*executionPlan, error) {
	jsonText := extractJSONObject(raw)
	if jsonText == "" {
		return nil, errors.New("planner response has no valid json object")
	}

	var payload struct {
		Goal  string              `json:"goal"`
		Steps []executionPlanStep `json:"steps"`
	}
	if err := json.Unmarshal([]byte(jsonText), &payload); err != nil {
		return nil, fmt.Errorf("planner json parse failed: %w", err)
	}

	goal := strings.TrimSpace(payload.Goal)
	if goal == "" {
		goal = strings.TrimSpace(fallbackGoal)
	}
	if goal == "" {
		goal = "完成用户目标"
	}
	allowedToolSet := parseAllowedToolSet(toolGuide)

	steps := make([]executionPlanStep, 0, len(payload.Steps))
	for _, item := range payload.Steps {
		description := strings.TrimSpace(item.Description)
		if description == "" {
			continue
		}
		expectedTool := normalizeExpectedTool(item.ExpectedTool, allowedToolSet)
		if readOnly && expectedTool != "" && isMutatingTool(expectedTool) {
			expectedTool = pickReadOnlyFallbackTool(allowedToolSet)
		}
		step := executionPlanStep{
			Description:      description,
			ExpectedTool:     expectedTool,
			Purpose:          summarizePlainText(strings.TrimSpace(item.Purpose), 120),
			SuccessCriterion: summarizePlainText(strings.TrimSpace(item.SuccessCriterion), 120),
			Status:           planStepPending,
		}
		if step.SuccessCriterion == "" {
			step.SuccessCriterion = "可通过工具返回 status=success 且关键约束通过来判定成功"
		}
		steps = append(steps, step)
		if len(steps) >= 8 {
			break
		}
	}
	if len(steps) == 0 {
		return nil, errors.New("planner output contains no valid steps")
	}
	for i := range steps {
		steps[i].ID = i + 1
	}

	return &executionPlan{Goal: goal, Steps: steps, SuccessfulTools: make(map[string]bool)}, nil
}

func buildFallbackExecutionPlan(goal string, readOnly bool) *executionPlan {
	goal = strings.TrimSpace(goal)
	if goal == "" {
		goal = "完成用户目标"
	}

	steps := []executionPlanStep{}
	if readOnly {
		steps = []executionPlanStep{
			{ID: 1, Description: "读取相关节点与子树上下文", ExpectedTool: "get_node_detail", Purpose: "补齐必要证据", SuccessCriterion: "返回目标节点与上下游关系", Status: planStepPending},
			{ID: 2, Description: "执行约束与语义校验", ExpectedTool: "validate_fta_constraints", Purpose: "确认问题范围", SuccessCriterion: "issueCount 明确且可定位问题节点", Status: planStepPending},
			{ID: 3, Description: "输出诊断结论与下一步建议", ExpectedTool: "", Purpose: "形成可执行建议", SuccessCriterion: "给出可执行修复建议且与校验结果一致", Status: planStepPending},
		}
	} else {
		steps = []executionPlanStep{
			{ID: 1, Description: "读取上下文并确认目标差异", ExpectedTool: "get_node_detail", Purpose: "避免盲改", SuccessCriterion: "明确待修复节点和边关系", Status: planStepPending},
			{ID: 2, Description: "按问题节点执行结构修复（优先 move_node，复杂场景用 batch_operations）", ExpectedTool: "batch_operations", Purpose: "推进核心目标", SuccessCriterion: "修复操作执行成功且 patch 有效", Status: planStepPending},
			{ID: 3, Description: "复核约束并收敛答复", ExpectedTool: "validate_fta_constraints", Purpose: "确认结果可用", SuccessCriterion: "校验无阻塞问题并可给出结论", Status: planStepPending},
		}
	}

	return &executionPlan{Goal: goal, Steps: steps, SuccessfulTools: make(map[string]bool)}
}

func (p *executionPlan) NextPendingStep() *executionPlanStep {
	if p == nil {
		return nil
	}
	for i := range p.Steps {
		status := strings.TrimSpace(p.Steps[i].Status)
		if status == "" || status == planStepPending {
			return &p.Steps[i]
		}
	}
	return nil
}

func (p *executionPlan) collectAllFailures() []failedAttempt {
	if p == nil {
		return nil
	}
	all := make([]failedAttempt, 0, 12)
	for _, step := range p.Steps {
		for _, item := range step.FailedAttempts {
			all = append(all, item)
			if len(all) >= 12 {
				return all
			}
		}
	}
	return all
}

func (p *executionPlan) RecordSuccessfulTool(toolName string) {
	if p == nil {
		return
	}
	key := normalizeToolNameKey(toolName)
	if key == "" {
		return
	}
	if p.SuccessfulTools == nil {
		p.SuccessfulTools = make(map[string]bool)
	}
	p.SuccessfulTools[key] = true
}

func (p *executionPlan) HasSuccessfulTool(toolName string) bool {
	if p == nil {
		return false
	}
	if strings.TrimSpace(toolName) == "" {
		return true
	}
	if p.SuccessfulTools == nil {
		return false
	}
	return p.SuccessfulTools[normalizeToolNameKey(toolName)]
}

func normalizeToolNameKey(toolName string) string {
	return strings.ToLower(strings.TrimSpace(toolName))
}

func formatPlannerPriorFailures(attempts []failedAttempt) string {
	if len(attempts) == 0 {
		return "- none"
	}
	b := strings.Builder{}
	start := 0
	if len(attempts) > 6 {
		start = len(attempts) - 6
	}
	for _, item := range attempts[start:] {
		tool := strings.TrimSpace(item.Tool)
		if tool == "" {
			tool = "unknown_tool"
		}
		errMsg := summarizePlainText(strings.TrimSpace(item.Error), 120)
		if errMsg == "" {
			errMsg = "unknown_error"
		}
		b.WriteString(fmt.Sprintf("- round=%d tool=%s error=%s\n", item.Round, tool, errMsg))
	}
	return strings.TrimSpace(b.String())
}

func summarizeExecutionPlan(plan *executionPlan) string {
	if plan == nil || len(plan.Steps) == 0 {
		return "无有效步骤"
	}
	labels := make([]string, 0, len(plan.Steps))
	for i, step := range plan.Steps {
		if i >= 3 {
			break
		}
		labels = append(labels, strings.TrimSpace(step.Description))
	}
	if len(plan.Steps) > 3 {
		return strings.Join(labels, "；") + fmt.Sprintf(" 等 %d 步", len(plan.Steps))
	}
	return strings.Join(labels, "；")
}

func extractJSONObject(text string) string {
	start := strings.Index(text, "{")
	if start < 0 {
		return ""
	}

	depth := 0
	inString := false
	escaped := false
	for i := start; i < len(text); i++ {
		ch := text[i]
		if inString {
			if escaped {
				escaped = false
				continue
			}
			if ch == '\\' {
				escaped = true
				continue
			}
			if ch == '"' {
				inString = false
			}
			continue
		}
		switch ch {
		case '"':
			inString = true
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return strings.TrimSpace(text[start : i+1])
			}
		}
	}
	return ""
}

func isMutatingTool(toolName string) bool {
	name := strings.TrimSpace(toolName)
	if name == "" {
		return false
	}
	def, ok := graph_ops.GetTool(name)
	if !ok {
		return false
	}
	return def.MutatesGraph
}

func parseAllowedToolNamesFromGuide(toolGuide string) []string {
	allowedSet := parseAllowedToolSet(toolGuide)
	if len(allowedSet) == 0 {
		return nil
	}
	out := make([]string, 0, len(allowedSet))
	for name := range allowedSet {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

func parseAllowedToolSet(toolGuide string) map[string]struct{} {
	guide := strings.TrimSpace(toolGuide)
	if guide == "" {
		return nil
	}

	allowed := make(map[string]struct{})
	for _, line := range strings.Split(guide, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || !strings.HasPrefix(line, "-") {
			continue
		}
		line = strings.TrimSpace(strings.TrimPrefix(line, "-"))
		if idx := strings.Index(line, "|"); idx >= 0 {
			line = strings.TrimSpace(line[:idx])
		}
		if line == "" {
			continue
		}
		if _, ok := graph_ops.GetTool(line); !ok {
			continue
		}
		allowed[line] = struct{}{}
	}

	if len(allowed) == 0 {
		return nil
	}
	return allowed
}

func normalizeExpectedTool(raw string, allowed map[string]struct{}) string {
	for _, candidate := range splitExpectedToolCandidates(raw) {
		if _, ok := graph_ops.GetTool(candidate); !ok {
			continue
		}
		if len(allowed) > 0 {
			if _, ok := allowed[candidate]; !ok {
				continue
			}
		}
		return candidate
	}
	return ""
}

func splitExpectedToolCandidates(raw string) []string {
	v := strings.TrimSpace(raw)
	if v == "" {
		return nil
	}
	replacer := strings.NewReplacer(
		"/", " ",
		",", " ",
		"|", " ",
		"，", " ",
		"、", " ",
		";", " ",
		"；", " ",
		"\n", " ",
		"\t", " ",
	)
	normalized := replacer.Replace(v)
	fields := strings.Fields(normalized)
	if len(fields) == 0 {
		return nil
	}

	seen := make(map[string]struct{}, len(fields))
	out := make([]string, 0, len(fields))
	for _, field := range fields {
		name := strings.TrimSpace(field)
		if name == "" {
			continue
		}
		if _, ok := seen[name]; ok {
			continue
		}
		seen[name] = struct{}{}
		out = append(out, name)
	}
	return out
}

func pickReadOnlyFallbackTool(allowed map[string]struct{}) string {
	preferred := []string{
		"get_node_detail",
		"get_subtree",
		"get_graph_snapshot",
		"validate_fta_constraints",
		"check_gate_semantics",
	}

	for _, name := range preferred {
		if len(allowed) > 0 {
			if _, ok := allowed[name]; !ok {
				continue
			}
		}
		if _, ok := graph_ops.GetTool(name); ok {
			return name
		}
	}

	if len(allowed) > 0 {
		keys := make([]string, 0, len(allowed))
		for name := range allowed {
			keys = append(keys, name)
		}
		sort.Strings(keys)
		for _, name := range keys {
			if isMutatingTool(name) {
				continue
			}
			return name
		}
	}

	return "get_node_detail"
}
