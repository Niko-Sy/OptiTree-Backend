package agent

import (
	"encoding/json"
	"strings"
	"testing"

	"optitree-backend/internal/ai"
)

func TestParseExecutionPlan_ExtractAndNormalize(t *testing.T) {
	raw := `规划如下：
{"goal":"修复故障树","steps":[{"id":9,"description":"读取关键节点","expectedTool":"get_node_detail","purpose":"定位问题"},{"description":"执行约束校验","expectedTool":"validate_fta_constraints"}]}
请执行。`

	plan, err := parseExecutionPlan(raw, "兜底目标", false, "")
	if err != nil {
		t.Fatalf("parseExecutionPlan should succeed, got %v", err)
	}
	if plan.Goal != "修复故障树" {
		t.Fatalf("unexpected goal: %s", plan.Goal)
	}
	if len(plan.Steps) != 2 {
		t.Fatalf("expected 2 steps, got %d", len(plan.Steps))
	}
	if plan.Steps[0].ID != 1 || plan.Steps[1].ID != 2 {
		t.Fatalf("step ids should be normalized sequentially, got %+v", plan.Steps)
	}
	if plan.Steps[0].Status != planStepPending {
		t.Fatalf("step status should default to pending, got %s", plan.Steps[0].Status)
	}
}

func TestParseExecutionPlan_ReadOnlyRewritesMutatingTool(t *testing.T) {
	raw := `{"goal":"只读检查","steps":[{"description":"尝试修改","expectedTool":"update_node"}]}`
	plan, err := parseExecutionPlan(raw, "只读检查", true, "")
	if err != nil {
		t.Fatalf("parseExecutionPlan should succeed, got %v", err)
	}
	if len(plan.Steps) != 1 {
		t.Fatalf("expected 1 step, got %d", len(plan.Steps))
	}
	if plan.Steps[0].ExpectedTool != "get_node_detail" {
		t.Fatalf("read-only step should be rewritten to read tool, got %s", plan.Steps[0].ExpectedTool)
	}
}

func TestParseExecutionPlan_NormalizesExpectedToolByGuide(t *testing.T) {
	raw := `{"goal":"修复","steps":[{"description":"执行修复","expectedTool":"update_node/add_node"}]}`
	guide := "[mutations]\n- add_node | required=[parentId,nodeType,name]"

	plan, err := parseExecutionPlan(raw, "修复", false, guide)
	if err != nil {
		t.Fatalf("parseExecutionPlan should succeed, got %v", err)
	}
	if len(plan.Steps) != 1 {
		t.Fatalf("expected 1 step, got %d", len(plan.Steps))
	}
	if plan.Steps[0].ExpectedTool != "add_node" {
		t.Fatalf("expected expectedTool normalized to add_node, got %s", plan.Steps[0].ExpectedTool)
	}
}

func TestBuildPlannerPrompt_ContainsStrictOutputAndAllowedTools(t *testing.T) {
	guide := "[read_context]\n- get_node_detail | required=[nodeId]\n- validate_fta_constraints | required=[none]"
	prompt := buildPlannerPrompt("修复故障树", guide, "faultTree", true, []failedAttempt{{Round: 2, Tool: "add_gate", Error: "edge does not exist"}})

	checks := []string{
		"STRICT JSON object only",
		"must start with { and end with }",
		"successCriterion",
		"Allowed tools",
		"get_node_detail",
		"mode=read-only",
		"Recent failed attempts",
		"add_gate",
	}
	for _, item := range checks {
		if !strings.Contains(prompt, item) {
			t.Fatalf("expected prompt to contain %q, got: %s", item, prompt)
		}
	}
}

func TestBuildFallbackExecutionPlan_HasDefaultSteps(t *testing.T) {
	plan := buildFallbackExecutionPlan("", false)
	if plan.Goal != "完成用户目标" {
		t.Fatalf("unexpected fallback goal: %s", plan.Goal)
	}
	if len(plan.Steps) != 3 {
		t.Fatalf("expected 3 fallback steps, got %d", len(plan.Steps))
	}
	if !strings.Contains(summarizeExecutionPlan(plan), "；") {
		t.Fatalf("expected summarized plan to contain separator, got %s", summarizeExecutionPlan(plan))
	}
}

func TestRoundExecutionState_RecordRound_TracksFactsAndActions(t *testing.T) {
	state := newRoundExecutionState("修复结构")
	state.SetPlan(&executionPlan{
		Goal: "修复结构",
		Steps: []executionPlanStep{
			{ID: 1, Description: "先读取", Status: planStepPending},
			{ID: 2, Description: "再修复", Status: planStepPending},
		},
	})

	state.RecordRound(
		[]string{"get_node_detail: ok"},
		[]ai.ToolCall{{ID: "c1", Name: "get_node_detail", Arguments: json.RawMessage(`{"nodeId":"n1"}`)}},
	)
	if len(state.knownFacts) == 0 {
		t.Fatal("expected known facts to be recorded")
	}
	if len(state.recentActions) == 0 {
		t.Fatal("expected recent actions to be recorded")
	}
	if state.NextPendingPlanStep() == nil || state.NextPendingPlanStep().ID != 1 {
		t.Fatalf("record-only round should not auto-advance step, got %+v", state.NextPendingPlanStep())
	}
}

func TestUpdatePlanAfterRound_ExpectedToolAndSuccessRequired(t *testing.T) {
	plan := &executionPlan{
		Goal: "修复",
		Steps: []executionPlanStep{{
			ID:           1,
			Description:  "读取关键节点",
			ExpectedTool: "get_node_detail",
			Status:       planStepPending,
		}},
	}

	toolCalls := []ai.ToolCall{{ID: "c1", Name: "get_node_detail", Arguments: json.RawMessage(`{"nodeId":"n1"}`)}}
	toolResults := []ai.ChatHistoryMessage{buildToolResultHistoryMessage("c1", "get_node_detail", toolStatusSuccess, "ok", nil, "")}

	completed, blocked := updatePlanAfterRound(plan, 1, []string{"get_node_detail: ok"}, toolCalls, toolResults)
	if blocked != nil {
		t.Fatalf("expected no blocked step, got %+v", blocked)
	}
	if completed == nil {
		t.Fatal("expected step completed")
	}
	if completed.Status != planStepDone || completed.Round != 1 {
		t.Fatalf("unexpected completed step state: %+v", completed)
	}
}

func TestUpdatePlanAfterRound_FailureAccumulationBlocksStep(t *testing.T) {
	plan := &executionPlan{
		Goal: "修复",
		Steps: []executionPlanStep{{
			ID:           1,
			Description:  "读取关键节点",
			ExpectedTool: "get_node_detail",
			Status:       planStepPending,
		}},
	}

	for round := 1; round <= 3; round++ {
		toolCalls := []ai.ToolCall{{ID: "cx", Name: "update_node", Arguments: json.RawMessage(`{"nodeId":"n1","name":"A"}`)}}
		toolResults := []ai.ChatHistoryMessage{buildToolResultHistoryMessage("cx", "update_node", toolStatusFailed, "failed", nil, "expected tool not called")}
		completed, blocked := updatePlanAfterRound(plan, round, []string{"update_node failed"}, toolCalls, toolResults)
		if completed != nil {
			t.Fatalf("round %d should not complete step", round)
		}
		if round < 3 && blocked != nil {
			t.Fatalf("round %d should not block yet", round)
		}
		if round == 3 && blocked == nil {
			t.Fatal("round 3 should block plan step")
		}
	}

	step := plan.Steps[0]
	if step.Status != planStepFailed {
		t.Fatalf("expected step failed, got %s", step.Status)
	}
	if len(step.FailedAttempts) < 3 {
		t.Fatalf("expected >=3 failed attempts, got %d", len(step.FailedAttempts))
	}
	if strings.TrimSpace(plan.BlockedReason) == "" {
		t.Fatal("expected blocked reason")
	}
}
