package agent

import (
	"errors"
	"testing"
	"time"
)

func TestSessionConfirm_RequiresPausedStateAndMatchingCallID(t *testing.T) {
	mgr := NewAgentSessionManager(time.Minute)
	session := mgr.NewSession("s_resume_1", "c1", "p1", "u1", "faultTree")
	if err := mgr.Create(session); err != nil {
		t.Fatalf("create session failed: %v", err)
	}

	session.SetPending("call_expected", "update_node", []byte(`{"nodeId":"n1"}`))
	if err := mgr.Confirm(session.ID, ConfirmSignal{CallID: "call_expected", Approved: true}); !errors.Is(err, ErrSessionNotWaiting) {
		t.Fatalf("expected ErrSessionNotWaiting when not paused, got %v", err)
	}

	session.SetState(StatePausedForConfirm)
	if err := mgr.Confirm(session.ID, ConfirmSignal{CallID: "call_other", Approved: true}); !errors.Is(err, ErrSessionNotWaiting) {
		t.Fatalf("expected ErrSessionNotWaiting for mismatched callId, got %v", err)
	}

	if err := mgr.Confirm(session.ID, ConfirmSignal{Approved: true}); err != nil {
		t.Fatalf("expected auto-filled pending call id to pass, got %v", err)
	}

	select {
	case got := <-session.ConfirmChan():
		if got.CallID != "call_expected" {
			t.Fatalf("expected call_expected, got %+v", got)
		}
	case <-time.After(200 * time.Millisecond):
		t.Fatal("did not receive confirmation signal")
	}
}

func TestSessionSnapshot_UsesPendingArgsSummary(t *testing.T) {
	session := NewAgentSession("s_resume_2", "c1", "p1", "u1", "faultTree", time.Minute)
	session.SetPending("call_1", "update_node", []byte(`{"nodeId":"n1","name":"Pump","description":"desc"}`))

	snap := session.Snapshot()
	if snap.PendingArgsSummary == "" {
		t.Fatal("expected pending args summary")
	}
	if snap.PendingArgsSummary == string(session.PendingArgs) {
		t.Fatalf("summary should not expose raw args, got %q", snap.PendingArgsSummary)
	}
}
