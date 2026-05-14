package cmd

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/devenjarvis/refrain/internal/planner"
)

func shortPlannerSocketPath(t *testing.T) string {
	t.Helper()
	h := sha256.Sum256([]byte(t.Name() + fmt.Sprintf("-%d", time.Now().UnixNano())))
	p := filepath.Join(os.TempDir(), fmt.Sprintf("refrain-pq-%x.sock", h[:6]))
	t.Cleanup(func() { _ = os.Remove(p) })
	return p
}

// runStdioServer pipes a sequence of JSON-RPC request bodies into the server
// and returns the parsed responses (one per non-notification request).
func runStdioServer(t *testing.T, socketPath string, requests []string) []map[string]any {
	t.Helper()

	in, inWriter := io.Pipe()
	var out bytes.Buffer

	done := make(chan error, 1)
	go func() {
		done <- servePlannerQuestionStdio(in, &out, socketPath)
	}()

	for _, req := range requests {
		if !strings.HasSuffix(req, "\n") {
			req += "\n"
		}
		if _, err := inWriter.Write([]byte(req)); err != nil {
			t.Fatalf("writing request: %v", err)
		}
	}
	_ = inWriter.Close()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("server returned error: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("server did not exit after stdin EOF")
	}

	scanner := bufio.NewScanner(&out)
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	var resps []map[string]any
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var m map[string]any
		if err := json.Unmarshal(line, &m); err != nil {
			t.Fatalf("response not JSON: %q (%v)", string(line), err)
		}
		resps = append(resps, m)
	}
	return resps
}

func TestPlannerQuestion_Initialize(t *testing.T) {
	socketPath := shortPlannerSocketPath(t)
	resps := runStdioServer(t, socketPath, []string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05"}}`,
	})
	if len(resps) != 1 {
		t.Fatalf("got %d responses, want 1: %v", len(resps), resps)
	}
	r := resps[0]
	if r["jsonrpc"] != "2.0" {
		t.Errorf("jsonrpc = %v, want 2.0", r["jsonrpc"])
	}
	result, ok := r["result"].(map[string]any)
	if !ok {
		t.Fatalf("result missing or wrong type: %v", r)
	}
	if result["protocolVersion"] != "2024-11-05" {
		t.Errorf("protocolVersion = %v, want 2024-11-05", result["protocolVersion"])
	}
	caps, _ := result["capabilities"].(map[string]any)
	if _, ok := caps["tools"]; !ok {
		t.Errorf("capabilities missing 'tools': %v", caps)
	}
}

func TestPlannerQuestion_ToolsList(t *testing.T) {
	socketPath := shortPlannerSocketPath(t)
	resps := runStdioServer(t, socketPath, []string{
		`{"jsonrpc":"2.0","id":2,"method":"tools/list"}`,
	})
	if len(resps) != 1 {
		t.Fatalf("want 1 response, got %d", len(resps))
	}
	result, _ := resps[0]["result"].(map[string]any)
	tools, _ := result["tools"].([]any)
	if len(tools) != 1 {
		t.Fatalf("want 1 tool, got %d: %v", len(tools), tools)
	}
	tool, _ := tools[0].(map[string]any)
	if tool["name"] != "ask_user" {
		t.Errorf("tool name = %v, want ask_user", tool["name"])
	}
	schema, _ := tool["inputSchema"].(map[string]any)
	props, _ := schema["properties"].(map[string]any)
	if _, ok := props["question"]; !ok {
		t.Errorf("inputSchema missing 'question' property: %v", schema)
	}
}

func TestPlannerQuestion_ToolsCall_RoundTrip(t *testing.T) {
	socketPath := shortPlannerSocketPath(t)
	srv, err := planner.NewServer(socketPath)
	if err != nil {
		t.Fatalf("planner server: %v", err)
	}
	t.Cleanup(func() { _ = srv.Close() })

	go func() {
		ev := <-srv.Events()
		if !strings.Contains(ev.Question, "dark mode") {
			t.Errorf("question = %q, want substring 'dark mode'", ev.Question)
		}
		ev.AnswerCh <- "yes, system-wide preference"
	}()

	resps := runStdioServer(t, socketPath, []string{
		`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"ask_user","arguments":{"question":"Is dark mode app-wide or per-view?"}}}`,
	})
	if len(resps) != 1 {
		t.Fatalf("want 1 response, got %d", len(resps))
	}
	result, _ := resps[0]["result"].(map[string]any)
	content, _ := result["content"].([]any)
	if len(content) != 1 {
		t.Fatalf("want 1 content block, got %d", len(content))
	}
	first, _ := content[0].(map[string]any)
	if first["type"] != "text" {
		t.Errorf("content type = %v, want text", first["type"])
	}
	text, _ := first["text"].(string)
	if !strings.Contains(text, "system-wide preference") {
		t.Errorf("answer text = %q, want substring 'system-wide preference'", text)
	}
}

func TestPlannerQuestion_ToolsCall_EmptyAnswerSurfacesSkipNote(t *testing.T) {
	socketPath := shortPlannerSocketPath(t)
	srv, err := planner.NewServer(socketPath)
	if err != nil {
		t.Fatalf("planner server: %v", err)
	}
	t.Cleanup(func() { _ = srv.Close() })

	go func() {
		ev := <-srv.Events()
		ev.AnswerCh <- ""
	}()

	resps := runStdioServer(t, socketPath, []string{
		`{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"ask_user","arguments":{"question":"anything?"}}}`,
	})
	result, _ := resps[0]["result"].(map[string]any)
	content, _ := result["content"].([]any)
	first, _ := content[0].(map[string]any)
	text, _ := first["text"].(string)
	if !strings.Contains(strings.ToLower(text), "skipped") {
		t.Errorf("empty-answer text = %q, expected to mention skip", text)
	}
}

func TestPlannerQuestion_NotificationProducesNoResponse(t *testing.T) {
	socketPath := shortPlannerSocketPath(t)
	resps := runStdioServer(t, socketPath, []string{
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`,
	})
	if len(resps) != 0 {
		t.Errorf("notifications must not produce a response; got %v", resps)
	}
}

func TestPlannerQuestion_UnknownMethodReturnsError(t *testing.T) {
	socketPath := shortPlannerSocketPath(t)
	resps := runStdioServer(t, socketPath, []string{
		`{"jsonrpc":"2.0","id":9,"method":"resources/list"}`,
	})
	if len(resps) != 1 {
		t.Fatalf("want 1 response, got %d", len(resps))
	}
	errBlock, _ := resps[0]["error"].(map[string]any)
	if errBlock == nil {
		t.Fatalf("expected error block; got %v", resps[0])
	}
	code, _ := errBlock["code"].(float64)
	if int(code) != -32601 {
		t.Errorf("error code = %v, want -32601", errBlock["code"])
	}
}

func TestPlannerQuestion_ToolsCall_DialFailureReturnsToolError(t *testing.T) {
	// Point at a socket that isn't bound — AskQuestion will fail to dial.
	socketPath := filepath.Join(os.TempDir(), "refrain-nonexistent.sock")
	_ = os.Remove(socketPath)

	resps := runStdioServer(t, socketPath, []string{
		`{"jsonrpc":"2.0","id":5,"method":"tools/call","params":{"name":"ask_user","arguments":{"question":"hello?"}}}`,
	})
	if len(resps) != 1 {
		t.Fatalf("want 1 response, got %d", len(resps))
	}
	result, ok := resps[0]["result"].(map[string]any)
	if !ok {
		t.Fatalf("expected result block (tool errors live on result.isError); got %v", resps[0])
	}
	if isErr, _ := result["isError"].(bool); !isErr {
		t.Errorf("expected isError=true on dial failure; got %v", result)
	}
}
