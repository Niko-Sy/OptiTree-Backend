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

type testErr string

func (e testErr) Error() string {
	return string(e)
}
