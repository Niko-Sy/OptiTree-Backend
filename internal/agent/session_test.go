package agent

import (
	"errors"
	"testing"
	"time"
)

func TestAgentSessionManager_ConfirmFlow(t *testing.T) {
	mgr := NewAgentSessionManager(time.Minute)
	session := mgr.NewSession("s1", "c1", "p1", "u1", "faultTree")
	if err := mgr.Create(session); err != nil {
		t.Fatalf("create session failed: %v", err)
	}

	received := make(chan ConfirmSignal, 1)
	go func() {
		received <- <-session.ConfirmChan()
	}()

	signal := ConfirmSignal{CallID: "call_1", Approved: true}
	var err error
	for i := 0; i < 10; i++ {
		err = mgr.Confirm("s1", signal)
		if err == nil {
			break
		}
		if !errors.Is(err, ErrSessionNotWaiting) {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if err != nil {
		t.Fatalf("confirm failed: %v", err)
	}

	select {
	case got := <-received:
		if got.CallID != "call_1" || !got.Approved {
			t.Fatalf("unexpected signal: %+v", got)
		}
	case <-time.After(200 * time.Millisecond):
		t.Fatal("did not receive confirm signal")
	}
}

func TestAgentSessionManager_CancelClosesSession(t *testing.T) {
	mgr := NewAgentSessionManager(time.Minute)
	session := mgr.NewSession("s2", "c1", "p1", "u1", "faultTree")
	if err := mgr.Create(session); err != nil {
		t.Fatalf("create session failed: %v", err)
	}

	if err := mgr.Cancel("s2"); err != nil {
		t.Fatalf("cancel failed: %v", err)
	}
	if session.State() != StateCancelled {
		t.Fatalf("expected state %s, got %s", StateCancelled, session.State())
	}

	err := mgr.Confirm("s2", ConfirmSignal{CallID: "call_2", Approved: true})
	if !errors.Is(err, ErrSessionClosed) {
		t.Fatalf("expected ErrSessionClosed, got %v", err)
	}
}

func TestAgentSession_CountersAndSnapshot(t *testing.T) {
	session := NewAgentSession("s3", "c1", "p1", "u1", "faultTree", time.Minute)
	session.IncToolCallCount()
	session.IncTierOps("server")
	session.IncTierOps("client")
	session.IncTierOps("hybrid")
	session.AddTokens(42)
	session.SetPending("call_3", "update_node", []byte(`{"nodeId":"n1"}`))

	snap := session.Snapshot()
	if snap.ToolCallCount != 1 {
		t.Fatalf("expected toolCallCount=1, got %d", snap.ToolCallCount)
	}
	if snap.ServerOps != 1 || snap.ClientOps != 1 || snap.HybridOps != 1 {
		t.Fatalf("unexpected tier ops snapshot: %+v", snap)
	}
	if snap.TokensUsed != 42 {
		t.Fatalf("expected tokens=42, got %d", snap.TokensUsed)
	}
	if snap.PendingCallID != "call_3" || snap.PendingTool != "update_node" {
		t.Fatalf("unexpected pending state: %+v", snap)
	}
}

func TestAgentSessionManager_ConfirmFlow_WithContinueRounds(t *testing.T) {
	mgr := NewAgentSessionManager(time.Minute)
	session := mgr.NewSession("s4", "c1", "p1", "u1", "faultTree")
	if err := mgr.Create(session); err != nil {
		t.Fatalf("create session failed: %v", err)
	}

	received := make(chan ConfirmSignal, 1)
	go func() {
		received <- <-session.ConfirmChan()
	}()

	signal := ConfirmSignal{CallID: "iter_limit_10_10", Approved: true, ContinueRounds: 3}
	var err error
	for i := 0; i < 10; i++ {
		err = mgr.Confirm("s4", signal)
		if err == nil {
			break
		}
		if !errors.Is(err, ErrSessionNotWaiting) {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if err != nil {
		t.Fatalf("confirm failed: %v", err)
	}

	select {
	case got := <-received:
		if got.ContinueRounds != 3 {
			t.Fatalf("expected continueRounds=3, got %+v", got)
		}
	case <-time.After(200 * time.Millisecond):
		t.Fatal("did not receive confirm signal")
	}
}
