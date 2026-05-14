package hook

import (
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestServerRoundTrip(t *testing.T) {
	socketPath := filepath.Join(t.TempDir(), "hook.sock")
	srv, err := NewServer(socketPath)
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	defer func() {
		if err := srv.Close(); err != nil {
			t.Errorf("Close: %v", err)
		}
	}()

	want := Event{
		Kind:      KindSessionStart,
		AgentID:   "session-1-agent-1",
		SessionID: "uuid-abc",
		CWD:       "/tmp/worktree",
	}

	if err := SendEvent(socketPath, want); err != nil {
		t.Fatalf("SendEvent: %v", err)
	}

	select {
	case got := <-srv.Events():
		if got.Kind != want.Kind {
			t.Errorf("Kind: got %q, want %q", got.Kind, want.Kind)
		}
		if got.AgentID != want.AgentID {
			t.Errorf("AgentID: got %q, want %q", got.AgentID, want.AgentID)
		}
		if got.SessionID != want.SessionID {
			t.Errorf("SessionID: got %q, want %q", got.SessionID, want.SessionID)
		}
		if got.CWD != want.CWD {
			t.Errorf("CWD: got %q, want %q", got.CWD, want.CWD)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for event")
	}
}

func TestServerMultipleEvents(t *testing.T) {
	socketPath := filepath.Join(t.TempDir(), "hook.sock")
	srv, err := NewServer(socketPath)
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	defer func() { _ = srv.Close() }()

	events := []Event{
		{Kind: KindSessionStart, AgentID: "a1", SessionID: "s1"},
		{Kind: KindStop, AgentID: "a1", SessionID: "s1"},
		{Kind: KindSessionEnd, AgentID: "a1", SessionID: "s1"},
	}
	for _, e := range events {
		if err := SendEvent(socketPath, e); err != nil {
			t.Fatalf("SendEvent %v: %v", e.Kind, err)
		}
	}

	// Each SendEvent opens its own connection and connections race through
	// Accept, so we can't rely on ordering — just verify all three kinds arrive.
	seen := make(map[Kind]int)
	for range events {
		select {
		case got := <-srv.Events():
			seen[got.Kind]++
		case <-time.After(2 * time.Second):
			t.Fatalf("timed out waiting for events; seen %v", seen)
		}
	}
	for _, want := range events {
		if seen[want.Kind] < 1 {
			t.Errorf("missing event kind %q; seen %v", want.Kind, seen)
		}
	}
}

func TestServerCloseRemovesSocket(t *testing.T) {
	socketPath := filepath.Join(t.TempDir(), "hook.sock")
	srv, err := NewServer(socketPath)
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}

	if err := srv.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// SocketPath should no longer exist.
	if err := SendEvent(socketPath, Event{Kind: KindStop, AgentID: "a1"}); err == nil {
		t.Error("SendEvent to closed server should fail")
	}
}

func TestServerCloseIdempotent(t *testing.T) {
	socketPath := filepath.Join(t.TempDir(), "hook.sock")
	srv, err := NewServer(socketPath)
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	if err := srv.Close(); err != nil {
		t.Fatalf("Close 1: %v", err)
	}
	// Second Close must not panic or deadlock.
	if err := srv.Close(); err != nil {
		t.Fatalf("Close 2: %v", err)
	}
}

func TestServerClosesEventsChannel(t *testing.T) {
	socketPath := filepath.Join(t.TempDir(), "hook.sock")
	srv, err := NewServer(socketPath)
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	if err := srv.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	// Channel should be closed.
	_, ok := <-srv.Events()
	if ok {
		t.Error("expected events channel to be closed")
	}
}

// TestPreToolUseRoundTrip verifies that ToolName and ToolInput survive the
// JSON marshal/unmarshal path through the hook server.
func TestPreToolUseRoundTrip(t *testing.T) {
	socketPath := filepath.Join(t.TempDir(), "hook.sock")
	srv, err := NewServer(socketPath)
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	defer func() { _ = srv.Close() }()

	toolInput := json.RawMessage(`{"todos":[{"content":"write tests","status":"in_progress","activeForm":"Writing tests"}]}`)
	want := Event{
		Kind:      KindPreToolUse,
		AgentID:   "session-1-agent-1",
		ToolName:  "TodoWrite",
		ToolInput: toolInput,
	}
	if err := SendEvent(socketPath, want); err != nil {
		t.Fatalf("SendEvent: %v", err)
	}

	select {
	case got := <-srv.Events():
		if got.Kind != want.Kind {
			t.Errorf("Kind: got %q, want %q", got.Kind, want.Kind)
		}
		if got.ToolName != want.ToolName {
			t.Errorf("ToolName: got %q, want %q", got.ToolName, want.ToolName)
		}
		if string(got.ToolInput) != string(want.ToolInput) {
			t.Errorf("ToolInput: got %q, want %q", got.ToolInput, want.ToolInput)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for event")
	}
}

func TestServerStaleSocketReuse(t *testing.T) {
	socketPath := filepath.Join(t.TempDir(), "hook.sock")

	// First server.
	srv1, err := NewServer(socketPath)
	if err != nil {
		t.Fatalf("NewServer 1: %v", err)
	}
	// Deliberately don't Close, then simulate stale by closing only listener.
	// We just Close properly since Close removes the file; then we touch a
	// stale file and open again.
	if err := srv1.Close(); err != nil {
		t.Fatalf("Close 1: %v", err)
	}

	// Re-create — should succeed even if file were stale.
	srv2, err := NewServer(socketPath)
	if err != nil {
		t.Fatalf("NewServer 2: %v", err)
	}
	if err := srv2.Close(); err != nil {
		t.Fatalf("Close 2: %v", err)
	}
}

func TestSendEventNoServer(t *testing.T) {
	// Dialing a nonexistent socket must fail (caller decides to ignore).
	if err := SendEvent(filepath.Join(t.TempDir(), "nope.sock"), Event{Kind: KindStop, AgentID: "a"}); err == nil {
		t.Error("expected error dialing nonexistent socket")
	}
}

// TestServerRemovesPriorRegularFile verifies that NewServer cleans up a
// stale leftover at the socket path even if the previous refrain crashed.
func TestServerRemovesPriorRegularFile(t *testing.T) {
	dir := t.TempDir()
	socketPath := filepath.Join(dir, "hook.sock")

	if err := os.WriteFile(socketPath, []byte("stale"), 0o644); err != nil {
		t.Fatal(err)
	}

	srv, err := NewServer(socketPath)
	if err != nil {
		t.Fatalf("NewServer should remove stale file and bind, got: %v", err)
	}
	defer func() { _ = srv.Close() }()

	// Round-trip a real event to confirm the listener is functional.
	if err := SendEvent(socketPath, Event{Kind: KindStop, AgentID: "a"}); err != nil {
		t.Fatalf("SendEvent after stale-file cleanup: %v", err)
	}
	select {
	case <-srv.Events():
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for event")
	}
}

// TestServerMalformedJSONSkipped verifies that a malformed line on a single
// connection is ignored without killing the server, and a subsequent
// well-formed message on the same connection is still delivered.
func TestServerMalformedJSONSkipped(t *testing.T) {
	socketPath := filepath.Join(t.TempDir(), "hook.sock")
	srv, err := NewServer(socketPath)
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	defer func() { _ = srv.Close() }()

	conn, err := net.Dial("unix", socketPath)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	// First a malformed line, then a valid event.
	if _, err := conn.Write([]byte("{not json\n{\"kind\":\"stop\",\"agentId\":\"a\"}\n")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	_ = conn.Close()

	select {
	case got := <-srv.Events():
		if got.Kind != KindStop {
			t.Errorf("expected Stop event after malformed line, got %q", got.Kind)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out — server may have died on malformed JSON")
	}
}

// TestServerConnectionDropMidLine verifies the server handles a connection
// closed before sending a newline-terminator without crashing or blocking
// other handlers.
func TestServerConnectionDropMidLine(t *testing.T) {
	socketPath := filepath.Join(t.TempDir(), "hook.sock")
	srv, err := NewServer(socketPath)
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	defer func() { _ = srv.Close() }()

	// Open a connection, send an incomplete JSON fragment, close.
	conn, err := net.Dial("unix", socketPath)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	if _, err := conn.Write([]byte("{\"kind\":\"Sto")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	_ = conn.Close()

	// Server should still accept and process subsequent valid events.
	if err := SendEvent(socketPath, Event{Kind: KindStop, AgentID: "alive"}); err != nil {
		t.Fatalf("SendEvent after dropped conn: %v", err)
	}
	select {
	case got := <-srv.Events():
		if got.AgentID != "alive" {
			t.Errorf("AgentID = %q, want alive", got.AgentID)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out — server may have wedged after partial-line drop")
	}
}

// TestServerSlowConsumerDeliversAllEvents verifies that a temporarily slow
// consumer does not lose hook events. Status-critical hooks (Stop,
// UserPromptSubmit) drive agent status transitions — dropping them silently
// leaves the dashboard stuck on the wrong badge, which defeats the whole
// attention-routing thesis. Senders may pause if the buffer fills (that's
// fine — they're short-lived hook CLIs), but events must not be discarded.
func TestServerSlowConsumerDeliversAllEvents(t *testing.T) {
	socketPath := filepath.Join(t.TempDir(), "hook.sock")
	srv, err := NewServer(socketPath)
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	defer func() { _ = srv.Close() }()

	const burst = 500 // exceeds the buffer to force back-pressure
	go func() {
		for i := 0; i < burst; i++ {
			_ = SendEvent(socketPath, Event{Kind: KindStop, AgentID: "a"})
		}
	}()

	// Drain on this goroutine with a small sleep on every read to simulate a
	// briefly slow consumer. The blocking-send guard must hold the senders
	// (or buffer) without dropping anything.
	received := 0
	deadline := time.After(15 * time.Second)
	for received < burst {
		select {
		case <-srv.Events():
			received++
			time.Sleep(time.Millisecond)
		case <-deadline:
			t.Fatalf("only received %d/%d events before deadline — events were dropped", received, burst)
		}
	}
}

// TestServerCloseUnblocksHandlerWaitingOnSlowConsumer verifies that a handler
// blocked on a full events channel exits cleanly when Close is called,
// rather than leaving wg.Wait stuck forever.
func TestServerCloseUnblocksHandlerWaitingOnSlowConsumer(t *testing.T) {
	socketPath := filepath.Join(t.TempDir(), "hook.sock")
	srv, err := NewServer(socketPath)
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}

	// Fill the events buffer + a bit more so a handler is blocked on send.
	go func() {
		for i := 0; i < 300; i++ {
			_ = SendEvent(socketPath, Event{Kind: KindStop, AgentID: "a"})
		}
	}()

	// Give senders time to fill the buffer.
	time.Sleep(50 * time.Millisecond)

	closed := make(chan error, 1)
	go func() { closed <- srv.Close() }()

	select {
	case err := <-closed:
		if err != nil {
			t.Errorf("Close returned error: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Close blocked on handler stuck on slow consumer")
	}
}
