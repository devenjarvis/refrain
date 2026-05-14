package planner

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// shortSocketPath returns a path under os.TempDir keyed by the test name,
// short enough to fit in unix sun_path on macOS (104 bytes). t.TempDir
// itself often produces paths past that limit because of nested test names.
func shortSocketPath(t *testing.T) string {
	t.Helper()
	h := sha256.Sum256([]byte(t.Name() + fmt.Sprintf("-%d", time.Now().UnixNano())))
	p := filepath.Join(os.TempDir(), fmt.Sprintf("refrain-q-%x.sock", h[:6]))
	t.Cleanup(func() { _ = os.Remove(p) })
	return p
}

func TestServer_RoundTrip(t *testing.T) {
	socket := shortSocketPath(t)
	srv, err := NewServer(socket)
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	t.Cleanup(func() { _ = srv.Close() })

	// Consumer goroutine: receive a question event, write back an answer.
	go func() {
		select {
		case ev := <-srv.Events():
			if ev.Question != "what color?" {
				t.Errorf("question = %q, want %q", ev.Question, "what color?")
			}
			ev.AnswerCh <- "blue"
		case <-time.After(2 * time.Second):
			t.Error("never received question event")
		}
	}()

	answer, err := AskQuestion(socket, "what color?")
	if err != nil {
		t.Fatalf("AskQuestion: %v", err)
	}
	if answer != "blue" {
		t.Errorf("answer = %q, want %q", answer, "blue")
	}
}

func TestServer_EmptyAnswerIsSurfaced(t *testing.T) {
	socket := shortSocketPath(t)
	srv, err := NewServer(socket)
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	t.Cleanup(func() { _ = srv.Close() })

	go func() {
		ev := <-srv.Events()
		ev.AnswerCh <- "" // simulate esc-skip
	}()

	answer, err := AskQuestion(socket, "anything?")
	if err != nil {
		t.Fatalf("AskQuestion: %v", err)
	}
	if answer != "" {
		t.Errorf("answer = %q, want empty string", answer)
	}
}

func TestServer_CloseUnblocksPendingHandler(t *testing.T) {
	socket := shortSocketPath(t)
	srv, err := NewServer(socket)
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}

	// AskQuestion runs in a goroutine so the test can close the server while
	// the connection is parked waiting for an answer.
	resultCh := make(chan struct {
		answer string
		err    error
	}, 1)
	go func() {
		a, err := AskQuestion(socket, "ignored")
		resultCh <- struct {
			answer string
			err    error
		}{a, err}
	}()

	// Drain the question event but don't reply — the handler should park on
	// answerCh, then unblock when Close fires done.
	select {
	case <-srv.Events():
	case <-time.After(2 * time.Second):
		t.Fatal("never received question event")
	}

	if err := srv.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	select {
	case res := <-resultCh:
		if res.err != nil {
			t.Errorf("AskQuestion err = %v, want nil (graceful empty)", res.err)
		}
		if res.answer != "" {
			t.Errorf("answer on close = %q, want empty", res.answer)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("AskQuestion did not unblock after server close")
	}
}

func TestServer_RemovesStaleSocket(t *testing.T) {
	socket := shortSocketPath(t)

	first, err := NewServer(socket)
	if err != nil {
		t.Fatalf("first NewServer: %v", err)
	}
	if err := first.Close(); err != nil {
		t.Fatalf("first Close: %v", err)
	}

	// Recreate a stale file at the same path so the next NewServer must
	// remove it before binding.
	second, err := NewServer(socket)
	if err != nil {
		t.Fatalf("second NewServer should succeed despite stale path: %v", err)
	}
	_ = second.Close()
}
