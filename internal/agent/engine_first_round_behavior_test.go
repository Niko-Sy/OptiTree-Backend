package agent

import (
	"errors"
	"testing"

	"optitree-backend/internal/ai"
)

func TestToolPlanningTemperature_FirstRoundAndFallback(t *testing.T) {
	if got := toolPlanningTemperature(0, true); got != 0.1 {
		t.Fatalf("expected first-round tool temperature=0.1, got %.2f", got)
	}
	if got := toolPlanningTemperature(0, false); got != 0.3 {
		t.Fatalf("expected first-round without tools temperature=0.3, got %.2f", got)
	}
	if got := toolPlanningTemperature(1, true); got != 0.3 {
		t.Fatalf("expected non-first round temperature=0.3, got %.2f", got)
	}
}

func TestShouldEmitFirstRoundContent(t *testing.T) {
	if !shouldEmitFirstRoundContent(0, nil, nil, "final reply") {
		t.Fatal("expected first round plain reply to emit content")
	}
	if shouldEmitFirstRoundContent(0, nil, []ai.ToolCall{{ID: "c1", Name: "update_node"}}, "final reply") {
		t.Fatal("should not emit content when first round contains tool calls")
	}
	if shouldEmitFirstRoundContent(0, errors.New("model error"), nil, "final reply") {
		t.Fatal("should not emit content when first round has model error")
	}
	if shouldEmitFirstRoundContent(1, nil, nil, "round2") {
		t.Fatal("should not use first-round emission rule for later rounds")
	}
	if shouldEmitFirstRoundContent(0, nil, nil, "   ") {
		t.Fatal("should not emit empty reply")
	}
}
