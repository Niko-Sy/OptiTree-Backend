package repository

import "testing"

func TestNormalizeWaitStatus(t *testing.T) {
	if got := normalizeWaitStatus(" waiting ", "cleared"); got != "waiting" {
		t.Fatalf("expected waiting, got %s", got)
	}
	if got := normalizeWaitStatus("APPROVED", "cleared"); got != "approved" {
		t.Fatalf("expected approved, got %s", got)
	}
	if got := normalizeWaitStatus("invalid", "cleared"); got != "cleared" {
		t.Fatalf("expected fallback cleared, got %s", got)
	}
	if got := normalizeWaitStatus("", ""); got != "waiting" {
		t.Fatalf("expected default waiting, got %s", got)
	}
}

func TestShouldClearPendingForStatus(t *testing.T) {
	terminal := []string{"approved", "rejected", "timeout", "cleared"}
	for _, status := range terminal {
		if !shouldClearPendingForStatus(status) {
			t.Fatalf("expected terminal status %s to clear pending", status)
		}
	}

	nonTerminal := []string{"waiting", "unknown"}
	for _, status := range nonTerminal {
		if shouldClearPendingForStatus(status) {
			t.Fatalf("expected non-terminal status %s to keep pending", status)
		}
	}
}
