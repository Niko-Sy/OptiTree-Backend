package agent

import (
	"testing"
	"time"

	"optitree-backend/internal/model"

	"gorm.io/datatypes"
)

func TestCanResumeFromRuntime_OnlyWaitingActionableTypes(t *testing.T) {
	for _, waitType := range []string{"confirm", "preview", "iteration"} {
		r := &model.AgentSessionRuntime{WaitType: waitType, WaitStatus: "waiting"}
		if !canResumeFromRuntime(r) {
			t.Fatalf("expected waitType=%s to be resumable when waiting", waitType)
		}
	}

	notResumable := []*model.AgentSessionRuntime{
		{WaitType: "none", WaitStatus: "waiting"},
		{WaitType: "confirm", WaitStatus: "approved"},
		{WaitType: "preview", WaitStatus: "rejected"},
		{WaitType: "iteration", WaitStatus: "timeout"},
		{WaitType: "confirm", WaitStatus: "cleared"},
	}
	for i, item := range notResumable {
		if canResumeFromRuntime(item) {
			t.Fatalf("expected runtime[%d] not resumable: %+v", i, item)
		}
	}
}

func TestCanResumeFromRuntime_ExpiredRuntimeNotResumable(t *testing.T) {
	expiredAt := time.Now().UTC().Add(-time.Second)
	runtime := &model.AgentSessionRuntime{
		WaitType:   "confirm",
		WaitStatus: "waiting",
		ExpiresAt:  &expiredAt,
	}
	if canResumeFromRuntime(runtime) {
		t.Fatalf("expected expired runtime not resumable: %+v", runtime)
	}
}

func TestBuildPersistedRuntimeSummary_RedactsRawPayload(t *testing.T) {
	expiresAt := time.Now().UTC().Add(2 * time.Minute)
	callID := "call_1"
	tool := "update_node"
	tier := "server"
	r := &model.AgentSessionRuntime{
		SessionID:      "s1",
		PendingCallID:  &callID,
		PendingTool:    &tool,
		PendingTier:    &tier,
		PendingArgs:    datatypes.JSON([]byte(`{"nodeId":"n1","name":"A"}`)),
		PendingPreview: datatypes.JSON([]byte(`{"ops":[{"tool":"update_node"}]}`)),
		WaitType:       "confirm",
		WaitStatus:     "waiting",
		LastEventSeq:   7,
		ExpiresAt:      &expiresAt,
		UpdatedAt:      time.Now().UTC(),
	}

	summary := buildPersistedRuntimeSummary(r)
	if summary == nil {
		t.Fatal("expected runtime summary")
	}
	if summary.PendingArgsSummary == string(r.PendingArgs) {
		t.Fatalf("expected pending args to be summarized, got raw payload: %s", summary.PendingArgsSummary)
	}
	if summary.PendingPreviewSummary == string(r.PendingPreview) {
		t.Fatalf("expected pending preview to be summarized, got raw payload: %s", summary.PendingPreviewSummary)
	}
	if summary.LastEventSeq != 7 {
		t.Fatalf("expected lastEventSeq=7, got %d", summary.LastEventSeq)
	}
	if summary.PendingCallID != callID || summary.PendingTool != tool || summary.PendingTier != tier {
		t.Fatalf("unexpected pending fields: %+v", summary)
	}
}
