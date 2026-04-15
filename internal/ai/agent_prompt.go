package ai

import (
	"fmt"
	"strings"
)

func buildAgentSystemPromptV2(req AgentChatRequest, graphTypeName string) string {
	parts := []string{baseSystemPrompt(req.GraphType, graphTypeName)}
	if strings.EqualFold(strings.TrimSpace(req.GraphType), "faultTree") {
		parts = append(parts, faultTreeDomainRulesCompact())
	}
	parts = append(parts, runtimeToolPolicy(req.ToolGuide, req.ReadOnly))
	return strings.Join(parts, "\n\n")
}

func baseSystemPrompt(graphType, graphTypeName string) string {
	if strings.EqualFold(strings.TrimSpace(graphType), "faultTree") {
		return fmt.Sprintf(`You are a graph-aware engineering agent for %s diagrams.

目标：帮助用户审查、解释和安全编辑故障树。涉及图结构事实时，以当前图上下文和工具结果为准；没有工具确认时，不要声称结构已修改。

工作方式：
- 图结构任务优先使用工具，第一步通常是读取目标区域或运行校验。
- 编辑时保持最小必要修改，保留无关分支。
- 多步编辑优先一次性使用 batch_operations，避免跨轮零散 mutation。
- 如果工具失败或信息不足，简短说明阻塞原因和下一步，不编造状态。
- 用用户相同语言回复，结论面向工程操作，避免暴露隐藏推理。`, graphTypeName)
	}
	return fmt.Sprintf(`You are a graph-aware assistant for %s diagrams.

Use current graph context and available tools as the source of truth. Inspect before structural claims, use tools for graph operations when useful, and answer in the user's language.`, graphTypeName)
}

func faultTreeDomainRulesCompact() string {
	return `Fault tree rules:
- Top Event: one root-level system failure; it should not be a child.
- Intermediate Event: decomposable failure state.
- Basic Event: leaf/root cause; it should not have children.
- Gate: AND means all inputs are required, OR means any input is sufficient, NOT means negated single input.
- Prefer specific, observable engineering labels; avoid vague or duplicate causes.`
}

func runtimeToolPolicy(toolGuide string, readOnly bool) string {
	mode := "write-capable"
	if readOnly {
		mode = "read-only"
	}
	guide := strings.TrimSpace(toolGuide)
	if guide == "" {
		guide = "No tool is available for this turn."
	}
	return fmt.Sprintf(`Runtime tool policy:
- Mode: %s.
- Use only tool names listed in Runtime Tool Guide. Do not invent tool names, patch syntax, JSON commands, or hidden operations.
- For graph edits follow: read_context -> validate -> mutate -> validate.
- If the user asks for structural edits, first choose a read_context or validate tool; do not give a final natural-language conclusion before tool evidence.
- In read-only mode, analyze and validate only; never call mutate or hybrid preview tools.

Runtime Tool Guide:
%s`, mode, guide)
}
