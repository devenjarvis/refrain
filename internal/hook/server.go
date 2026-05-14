package hook

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"sync"
)

// Server listens on a unix socket for hook events and exposes them on a channel.
//
// Lifecycle:
//   - NewServer creates the listener and starts the accept loop.
//   - Events() returns a channel that emits one Event per newline-delimited JSON
//     message from any connected client. The channel is closed when the server
//     shuts down.
//   - Close stops accepting new connections, waits for in-flight handlers, then
//     removes the socket file.
//
// Back-pressure: hook events are status-critical (Stop, UserPromptSubmit,
// Notification) — silently dropping them leaves agents stuck in the wrong
// status, which directly defeats the dashboard's attention-routing thesis.
// handleConn therefore performs a *blocking* send into events, guarded by
// the done channel so a slow consumer can't wedge handlers past Close.
type Server struct {
	socketPath string
	listener   net.Listener
	events     chan Event
	done       chan struct{}

	wg       sync.WaitGroup
	closeMu  sync.Mutex
	closed   bool
	closeErr error
}

// NewServer creates the socket at the given path and starts accepting connections.
//
// If a stale socket file exists at the path, it is removed before binding.
// The directory containing the socket must already exist.
func NewServer(socketPath string) (*Server, error) {
	// Remove stale socket file from a previous baton run — unix domain sockets
	// don't auto-clean on process crash.
	if err := os.Remove(socketPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("removing stale socket: %w", err)
	}

	l, err := net.Listen("unix", socketPath)
	if err != nil {
		return nil, fmt.Errorf("listening on %s: %w", socketPath, err)
	}

	s := &Server{
		socketPath: socketPath,
		listener:   l,
		// Buffer is a cushion against bursts; the real guarantee against
		// dropped events is the blocking send + done guard in handleConn.
		events: make(chan Event, 256),
		done:   make(chan struct{}),
	}

	s.wg.Add(1)
	go s.acceptLoop()

	return s, nil
}

// SocketPath returns the filesystem path the server is listening on.
func (s *Server) SocketPath() string {
	return s.socketPath
}

// Events returns a channel that emits hook events as they arrive. The channel
// is closed when the server shuts down.
func (s *Server) Events() <-chan Event {
	return s.events
}

func (s *Server) acceptLoop() {
	defer s.wg.Done()
	for {
		conn, err := s.listener.Accept()
		if err != nil {
			// Any error here — including "use of closed network connection" on
			// Close — means we're done accepting.
			return
		}
		s.wg.Add(1)
		go s.handleConn(conn)
	}
}

func (s *Server) handleConn(conn net.Conn) {
	defer s.wg.Done()
	defer func() { _ = conn.Close() }()

	scanner := bufio.NewScanner(conn)
	// Hook payloads can be up to a few KB; bump the scan buffer so large
	// SessionStart payloads don't trip the default 64KB line limit either way.
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var e Event
		if err := json.Unmarshal(line, &e); err != nil {
			// Malformed line — skip. Hook failures must not block Claude, and
			// we don't want one bad client to kill the server.
			continue
		}
		// Blocking send guarded by done: never drop status-critical events,
		// but abort if Close was called so handlers don't outlive the server.
		select {
		case s.events <- e:
		case <-s.done:
			return
		}
	}
}

// Close stops accepting new connections, waits for in-flight handlers to finish,
// closes the events channel, and removes the socket file. Idempotent.
func (s *Server) Close() error {
	s.closeMu.Lock()
	if s.closed {
		s.closeMu.Unlock()
		return s.closeErr
	}
	s.closed = true
	s.closeMu.Unlock()

	// Close done first so any handler blocked on a slow consumer aborts
	// instead of hanging wg.Wait() indefinitely.
	close(s.done)
	err := s.listener.Close()
	s.wg.Wait()
	close(s.events)

	// Best-effort cleanup; Listener.Close may or may not remove the file
	// depending on the platform.
	if rmErr := os.Remove(s.socketPath); rmErr != nil && !errors.Is(rmErr, os.ErrNotExist) && err == nil {
		err = rmErr
	}

	s.closeErr = err
	return err
}
