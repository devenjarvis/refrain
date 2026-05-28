package tui

import (
	"testing"
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
	if msg.output == "" {
		t.Error("output should contain 'hello'")
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
