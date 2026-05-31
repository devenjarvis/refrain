package tui

import (
	"strings"
	"testing"

	"github.com/devenjarvis/refrain/internal/agent"
)

func TestRunValidationCheckCmd_PassingCommand(t *testing.T) {
	worktreeDir := t.TempDir()
	cmd := runValidationCheckCmd("s1", "/repo", worktreeDir, 0, 1, "echo hello")
	if cmd == nil {
		t.Fatal("runValidationCheckCmd returned nil cmd")
	}
	msg, ok := cmd().(validationCheckResultMsg)
	if !ok {
		t.Fatalf("cmd() returned %T, want validationCheckResultMsg", cmd())
	}
	if msg.runID != 1 {
		t.Errorf("runID = %d, want 1", msg.runID)
	}
	if msg.sessionID != "s1" {
		t.Errorf("sessionID = %q, want s1", msg.sessionID)
	}
	if msg.checkIndex != 0 {
		t.Errorf("checkIndex = %d, want 0", msg.checkIndex)
	}
	if msg.state != checkPassed {
		t.Errorf("state = %v, want checkPassed", msg.state)
	}
	if msg.exitCode != 0 {
		t.Errorf("exitCode = %d, want 0", msg.exitCode)
	}
	if !strings.Contains(msg.output, "hello") {
		t.Errorf("output = %q, want it to contain 'hello'", msg.output)
	}
}

func TestRunValidationCheckCmd_FailingCommand(t *testing.T) {
	worktreeDir := t.TempDir()
	cmd := runValidationCheckCmd("s1", "/repo", worktreeDir, 1, 2, "exit 42")
	if cmd == nil {
		t.Fatal("runValidationCheckCmd returned nil cmd")
	}
	msg, ok := cmd().(validationCheckResultMsg)
	if !ok {
		t.Fatalf("cmd() returned %T, want validationCheckResultMsg", cmd())
	}
	if msg.runID != 2 {
		t.Errorf("runID = %d, want 2", msg.runID)
	}
	if msg.state != checkFailed {
		t.Errorf("state = %v, want checkFailed", msg.state)
	}
	if msg.exitCode != 42 {
		t.Errorf("exitCode = %d, want 42", msg.exitCode)
	}
}

// TestBuildReviewReworkPrompt_InstructsTrailer verifies the rework prompt uses
// Plan-Task trailers and not the legacy subject-prefix format.
func TestBuildReviewReworkPrompt_InstructsTrailer(t *testing.T) {
	entry := &reviewDiffEntry{
		tasks: []agent.PlanTask{{Index: 2, Text: "Add widget"}},
		verdicts: map[int]*taskVerdictRecord{
			2: {state: verdictDone, verdict: agent.ReviewVerdict{Kind: agent.VerdictFail, Rationale: "missing validation"}},
		},
	}
	prompt := buildReviewReworkPrompt(entry)

	if !strings.Contains(prompt, "Plan-Task: 2") {
		t.Errorf("rework prompt must instruct Plan-Task: 2 trailer, got: %q", prompt)
	}
	if strings.Contains(prompt, "[task 2]") {
		t.Errorf("rework prompt must NOT contain legacy [task 2] subject prefix, got: %q", prompt)
	}
	// The "Other changes" branch must still be explained.
	if !strings.Contains(prompt, "Other changes") {
		t.Errorf("rework prompt must still mention 'Other changes', got: %q", prompt)
	}
}
