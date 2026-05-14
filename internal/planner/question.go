// Package planner carries planner-question events between a spawned
// `refrain planner-question-server` MCP subprocess and the long-running refrain
// TUI process over a unix socket.
//
// Unlike the hook protocol — which is one-way fire-and-forget — the planner
// question protocol is request/response on a single connection: the MCP
// server writes one `{"question":"..."}` line, then blocks on the same
// connection until refrain writes back one `{"answer":"..."}` line.
package planner

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"sync"
	"time"
)

// PlannerQuestionEvent is delivered to consumers when the MCP server forwards
// an `ask_user` tool call. The receiver MUST eventually send a single value
// on AnswerCh — including the empty string — so the in-flight subprocess
// unblocks and the planner can either incorporate the answer or proceed
// without one. Failure to do so leaves the planner subprocess wedged until
// its context is cancelled.
type PlannerQuestionEvent struct {
	Question string
	AnswerCh chan<- string
}

// questionRequest is the wire payload the MCP server sends.
type questionRequest struct {
	Question string `json:"question"`
}

// questionResponse is the wire payload refrain writes back on the same
// connection. Empty Answer is a legitimate value meaning "no answer; carry
// on" — the planner CLI is responsible for translating that into prose for
// the model.
type questionResponse struct {
	Answer string `json:"answer"`
}

// Server listens on a unix socket and surfaces incoming planner questions on
// an Events channel. Each accepted connection corresponds to one ask_user
// tool call: the server reads the question, emits an event with a per-call
// answer channel, blocks until the consumer responds, then flushes the
// answer back on the same connection before closing it.
//
// Lifecycle mirrors hook.Server: NewServer binds and starts the accept loop;
// Close stops accepting, drains in-flight handlers, and removes the socket
// file. Close is idempotent.
type Server struct {
	socketPath string
	listener   net.Listener
	events     chan PlannerQuestionEvent
	done       chan struct{}

	wg       sync.WaitGroup
	closeMu  sync.Mutex
	closed   bool
	closeErr error
}

// NewServer creates the socket at socketPath and starts the accept loop.
// A stale socket file at the path is removed before binding. The directory
// containing the socket must already exist.
func NewServer(socketPath string) (*Server, error) {
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
		events:     make(chan PlannerQuestionEvent, 8),
		done:       make(chan struct{}),
	}

	s.wg.Add(1)
	go s.acceptLoop()

	return s, nil
}

// SocketPath returns the filesystem path the server is listening on.
func (s *Server) SocketPath() string { return s.socketPath }

// Events returns a channel that emits one PlannerQuestionEvent per ask_user
// tool call. The channel is closed when Close completes.
func (s *Server) Events() <-chan PlannerQuestionEvent { return s.events }

func (s *Server) acceptLoop() {
	defer s.wg.Done()
	for {
		conn, err := s.listener.Accept()
		if err != nil {
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
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	if !scanner.Scan() {
		return
	}
	line := scanner.Bytes()
	if len(line) == 0 {
		return
	}
	var req questionRequest
	if err := json.Unmarshal(line, &req); err != nil {
		return
	}

	answerCh := make(chan string, 1)
	select {
	case s.events <- PlannerQuestionEvent{Question: req.Question, AnswerCh: answerCh}:
	case <-s.done:
		return
	}

	var answer string
	select {
	case answer = <-answerCh:
	case <-s.done:
		// Server is shutting down. Reply with an empty answer so the MCP
		// client can return cleanly to the planner subprocess instead of
		// hanging until its own context is cancelled.
		answer = ""
	}

	resp, err := json.Marshal(questionResponse{Answer: answer})
	if err != nil {
		return
	}
	resp = append(resp, '\n')
	_ = conn.SetWriteDeadline(time.Now().Add(2 * time.Second))
	_, _ = conn.Write(resp)
}

// Close stops accepting new connections, signals in-flight handlers to drain
// (any blocked ones reply with an empty answer so the MCP client can exit),
// closes the events channel, and removes the socket file. Idempotent.
func (s *Server) Close() error {
	s.closeMu.Lock()
	if s.closed {
		s.closeMu.Unlock()
		return s.closeErr
	}
	s.closed = true
	close(s.done)
	s.closeMu.Unlock()

	err := s.listener.Close()
	s.wg.Wait()
	close(s.events)

	if rmErr := os.Remove(s.socketPath); rmErr != nil && !errors.Is(rmErr, os.ErrNotExist) && err == nil {
		err = rmErr
	}

	s.closeErr = err
	return err
}

// AskQuestion is the client-side helper used by the MCP subprocess. It dials
// socketPath, writes a single newline-terminated `{"question":"..."}`, then
// blocks reading one newline-terminated `{"answer":"..."}` line back on the
// same connection. The returned answer may be empty — that's the agreed
// signal for "user submitted without typing anything; carry on".
func AskQuestion(socketPath, question string) (string, error) {
	conn, err := net.DialTimeout("unix", socketPath, 5*time.Second)
	if err != nil {
		return "", err
	}
	defer func() { _ = conn.Close() }()

	reqBytes, err := json.Marshal(questionRequest{Question: question})
	if err != nil {
		return "", err
	}
	reqBytes = append(reqBytes, '\n')
	_ = conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
	if _, err := conn.Write(reqBytes); err != nil {
		return "", err
	}

	// No read deadline: the user may take an arbitrarily long time to answer,
	// and the planner subprocess is bounded by the parent context anyway.
	scanner := bufio.NewScanner(conn)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	if !scanner.Scan() {
		if err := scanner.Err(); err != nil {
			return "", err
		}
		return "", errors.New("planner question server closed connection without answer")
	}
	var resp questionResponse
	if err := json.Unmarshal(scanner.Bytes(), &resp); err != nil {
		return "", err
	}
	return resp.Answer, nil
}
