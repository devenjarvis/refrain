package tui

import (
	"path/filepath"
	"testing"

	"github.com/devenjarvis/refrain/internal/agent"
)

// shouldAutoPromote is the pure decision extracted from handleAgentEvent: a
// BUILDING session auto-advances to ReadyForReview only when its plan/commit
// task counts are all satisfied (or it has no plan at all).
func TestShouldAutoPromote(t *testing.T) {
	const tasksHeader = "# Goal\nDo the thing\n\n## Tasks\n"

	tests := []struct {
		name       string
		plan       string // "" means write no plan file (no-plan case)
		commitDone int
		commitMax  int
		want       bool
	}{
		{
			name: "no plan is always promotable",
			plan: "",
			want: true,
		},
		{
			name: "plan with no task lines is promotable",
			plan: "# Goal\nNo tasks here\n",
			want: true,
		},
		{
			name: "plan with all tasks done is promotable",
			plan: tasksHeader + "  - [x] one\n  - [x] two\n",
			want: true,
		},
		{
			name: "plan with an outstanding task is not promotable",
			plan: tasksHeader + "  - [x] one\n  - [ ] two\n",
			want: false,
		},
		{
			name:       "commit count satisfies more tasks than the plan marks done",
			plan:       tasksHeader + "  - [ ] one\n  - [ ] two\n",
			commitDone: 2,
			commitMax:  2,
			want:       true,
		},
		{
			name:       "commit count short of the plan total keeps it building",
			plan:       tasksHeader + "  - [ ] one\n  - [ ] two\n  - [ ] three\n",
			commitDone: 1,
			commitMax:  1,
			want:       false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var sess *agent.Session
			if tc.plan == "" {
				// Empty worktree path => PlanPath()=="" => CachedPlan absent.
				sess = agent.NewSessionForTest("s1", "fix-auth")
			} else {
				worktree := t.TempDir()
				sess = agent.NewSessionForTestWithPath("s1", "fix-auth", worktree)
				if err := writePlanForTest(filepath.Join(worktree, ".claude"), sess, tc.plan); err != nil {
					t.Fatalf("write plan: %v", err)
				}
			}
			if tc.commitMax > 0 || tc.commitDone > 0 {
				sess.SetCommitTaskCountForTest(tc.commitDone, tc.commitMax)
			}

			if got := shouldAutoPromote(sess); got != tc.want {
				t.Errorf("shouldAutoPromote() = %v, want %v", got, tc.want)
			}
		})
	}
}
