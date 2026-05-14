package cmd

import (
	"testing"
)

// TestCheckHookPipeline exercises the doctor self-check: it builds the refrain
// binary, invokes checkHookPipeline against it, and asserts the socket round
// trip succeeds. Uses buildRefrain from hook_test.go.
func TestCheckHookPipeline(t *testing.T) {
	bin := buildRefrain(t)
	if err := checkHookPipeline(bin); err != nil {
		t.Fatalf("checkHookPipeline: %v", err)
	}
}

// TestCheckHookPipelineBadBinary verifies the check surfaces an actionable
// error when the refrain binary path is wrong.
func TestCheckHookPipelineBadBinary(t *testing.T) {
	err := checkHookPipeline("/nonexistent/refrain-binary")
	if err == nil {
		t.Fatal("expected error for missing binary, got nil")
	}
}
