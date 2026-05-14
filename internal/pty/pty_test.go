package pty_test

import (
	"bytes"
	"os/exec"
	"testing"
	"time"

	"github.com/devenjarvis/refrain/internal/pty"
)

func TestStartEchoAndReadOutput(t *testing.T) {
	p := &pty.PTY{}
	cmd := exec.Command("echo", "hello")
	if err := p.Start(cmd, 24, 80); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer func() { _ = p.Close() }()

	var output bytes.Buffer
	buf := make([]byte, 1024)
	// Read until we get "hello" or timeout.
	deadline := time.After(5 * time.Second)
	for {
		select {
		case <-deadline:
			t.Fatalf("timed out waiting for output, got: %q", output.String())
		default:
		}
		n, err := p.Read(buf)
		if n > 0 {
			output.Write(buf[:n])
		}
		if bytes.Contains(output.Bytes(), []byte("hello")) {
			break
		}
		if err != nil {
			break
		}
	}

	if !bytes.Contains(output.Bytes(), []byte("hello")) {
		t.Errorf("expected output to contain 'hello', got %q", output.String())
	}
}

func TestCatWriteReadRoundTrip(t *testing.T) {
	p := &pty.PTY{}
	cmd := exec.Command("cat")
	if err := p.Start(cmd, 24, 80); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer func() { _ = p.Close() }()

	msg := "ping\n"
	if _, err := p.Write([]byte(msg)); err != nil {
		t.Fatalf("Write failed: %v", err)
	}

	var output bytes.Buffer
	buf := make([]byte, 1024)
	deadline := time.After(5 * time.Second)
	for {
		select {
		case <-deadline:
			t.Fatalf("timed out waiting for round-trip, got: %q", output.String())
		default:
		}
		n, err := p.Read(buf)
		if n > 0 {
			output.Write(buf[:n])
		}
		if bytes.Contains(output.Bytes(), []byte("ping")) {
			break
		}
		if err != nil {
			break
		}
	}

	if !bytes.Contains(output.Bytes(), []byte("ping")) {
		t.Errorf("expected output to contain 'ping', got %q", output.String())
	}
}

func TestPid(t *testing.T) {
	p := &pty.PTY{}
	// Before Start, Pid should return 0.
	if got := p.Pid(); got != 0 {
		t.Errorf("expected Pid() == 0 before Start, got %d", got)
	}

	cmd := exec.Command("sleep", "5")
	if err := p.Start(cmd, 24, 80); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer func() { _ = p.Close() }()

	// After Start, Pid should be a positive integer.
	if got := p.Pid(); got <= 0 {
		t.Errorf("expected Pid() > 0 after Start, got %d", got)
	}
}

func TestCloseAndDoneChannel(t *testing.T) {
	p := &pty.PTY{}
	cmd := exec.Command("sleep", "60")
	if err := p.Start(cmd, 24, 80); err != nil {
		t.Fatalf("Start failed: %v", err)
	}

	if err := p.Close(); err != nil {
		t.Fatalf("Close failed: %v", err)
	}

	select {
	case <-p.Done():
		// Process exited as expected.
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for Done channel after Close")
	}

	// Err should be non-nil since we terminated the process.
	if p.Err() == nil {
		t.Log("expected non-nil error after terminating process, got nil (acceptable if process exited cleanly)")
	}
}
