package service

import "testing"

func TestIsAITaskIdempotencyConflict(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{
			name: "constraint name hit",
			err:  testErr("ERROR: duplicate key value violates unique constraint \"uq_ai_tasks_idempotency_key\" (SQLSTATE 23505)"),
			want: true,
		},
		{
			name: "duplicate idempotency key",
			err:  testErr("duplicate key value violates unique constraint on idempotency_key"),
			want: true,
		},
		{
			name: "other db error",
			err:  testErr("connection reset by peer"),
			want: false,
		},
		{
			name: "nil",
			err:  nil,
			want: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isAITaskIdempotencyConflict(tc.err); got != tc.want {
				t.Fatalf("isAITaskIdempotencyConflict()=%v, want %v", got, tc.want)
			}
		})
	}
}

func TestProjectGenerationStatusFromTaskStatus(t *testing.T) {
	cases := []struct {
		status string
		want   string
	}{
		{status: "pending", want: "pending_generating"},
		{status: "processing", want: "generating"},
		{status: "retrying", want: "generating"},
		{status: "completed", want: "completed"},
		{status: "failed", want: "failed"},
		{status: "dead", want: "failed"},
		{status: "", want: "pending_generating"},
	}

	for _, tc := range cases {
		t.Run(tc.status, func(t *testing.T) {
			if got := projectGenerationStatusFromTaskStatus(tc.status); got != tc.want {
				t.Fatalf("projectGenerationStatusFromTaskStatus(%q)=%q, want %q", tc.status, got, tc.want)
			}
		})
	}
}

func TestNormalizeDocIDs(t *testing.T) {
	got := normalizeDocIDs([]string{" doc_a ", "doc_b", "", "doc_a", "doc_c", "doc_b"})
	want := []string{"doc_a", "doc_b", "doc_c"}

	if len(got) != len(want) {
		t.Fatalf("normalizeDocIDs len=%d, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("normalizeDocIDs[%d]=%q, want %q", i, got[i], want[i])
		}
	}
}

func TestProjectIDValue(t *testing.T) {
	if got := projectIDValue(nil); got != "" {
		t.Fatalf("projectIDValue(nil)=%q, want empty", got)
	}

	v := "  proj_123  "
	if got := projectIDValue(&v); got != "proj_123" {
		t.Fatalf("projectIDValue(ptr)=%q, want %q", got, "proj_123")
	}
}

type testErr string

func (e testErr) Error() string {
	return string(e)
}
