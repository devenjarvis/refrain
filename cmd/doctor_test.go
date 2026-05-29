package cmd

import (
	"testing"
)

// TestCheckHookPipeline exercises the doctor self-check: it invokes
// checkHookPipeline against the binary built in TestMain and asserts the socket
// round trip succeeds.
func TestCheckHookPipeline(t *testing.T) {
	bin := testBin
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
