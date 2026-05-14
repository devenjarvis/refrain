package cmd

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/devenjarvis/refrain/internal/planner"
	"github.com/spf13/cobra"
)

// plannerQuestionCmd is the MCP-stdio bridge that lets the planner Sonnet
// subprocess pause mid-draft and ask the user for clarification. Claude Code
// spawns this binary as an MCP server (configured via --mcp-config in the
// planner argv) and routes any `ask_user` tool call to it. We translate that
// JSON-RPC call into a unix-socket round-trip with the running refrain TUI.
//
// Like `refrain hook`, this command is not meant to be run by humans. It exits
// silently when REFRAIN_PLANNER_QUESTION_SOCKET is unset so a developer who
// happens to run `refrain planner-question-server` from a shell doesn't see
// anything alarming.
var plannerQuestionCmd = &cobra.Command{
	Use:           "planner-question-server",
	Short:         "MCP stdio server that bridges planner ask_user calls to refrain",
	Hidden:        true,
	Args:          cobra.NoArgs,
	RunE:          runPlannerQuestionServer,
	SilenceUsage:  true,
	SilenceErrors: true,
}

func init() {
	rootCmd.AddCommand(plannerQuestionCmd)
}

// jsonrpcRequest matches the subset of JSON-RPC 2.0 the MCP protocol uses on
// the request/notification side. Notifications omit "id"; we represent that
// as a json.RawMessage so we can distinguish absent from null.
type jsonrpcRequest struct {
	Jsonrpc string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type jsonrpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type jsonrpcResponse struct {
	Jsonrpc string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result,omitempty"`
	Error   *jsonrpcError   `json:"error,omitempty"`
}

const (
	mcpDefaultProtocolVersion = "2024-11-05"
	askUserToolName           = "ask_user"
)

func runPlannerQuestionServer(_ *cobra.Command, _ []string) error {
	socketPath := os.Getenv("REFRAIN_PLANNER_QUESTION_SOCKET")
	if socketPath == "" {
		// Running outside refrain: exit silently so a curious developer poking
		// the binary doesn't get spam on stderr.
		return nil
	}
	return servePlannerQuestionStdio(os.Stdin, os.Stdout, socketPath)
}

// servePlannerQuestionStdio runs the JSON-RPC loop against arbitrary
// reader/writer streams so the unit test can drive it with pipes.
func servePlannerQuestionStdio(r io.Reader, w io.Writer, socketPath string) error {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)

	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var req jsonrpcRequest
		if err := json.Unmarshal(line, &req); err != nil {
			// Malformed input is unrecoverable on a single-stream protocol —
			// log and skip rather than killing the server.
			fmt.Fprintln(os.Stderr, "refrain planner-question-server: malformed JSON-RPC line:", err)
			continue
		}

		// Notifications (no id) must not produce a response.
		if len(req.ID) == 0 {
			continue
		}

		resp := dispatchPlannerRPC(req, socketPath)
		out, err := json.Marshal(resp)
		if err != nil {
			fmt.Fprintln(os.Stderr, "refrain planner-question-server: encoding response:", err)
			continue
		}
		out = append(out, '\n')
		if _, err := w.Write(out); err != nil {
			return err
		}
	}
	if err := scanner.Err(); err != nil && !errors.Is(err, io.EOF) {
		return err
	}
	return nil
}

func dispatchPlannerRPC(req jsonrpcRequest, socketPath string) jsonrpcResponse {
	switch req.Method {
	case "initialize":
		return handleInitialize(req)
	case "tools/list":
		return handleToolsList(req)
	case "tools/call":
		return handleToolsCall(req, socketPath)
	default:
		return jsonrpcResponse{
			Jsonrpc: "2.0",
			ID:      req.ID,
			Error: &jsonrpcError{
				Code:    -32601,
				Message: "method not found: " + req.Method,
			},
		}
	}
}

func handleInitialize(req jsonrpcRequest) jsonrpcResponse {
	// Echo the client's protocolVersion when present so we land on the same
	// dialect; fall back to a known-good default otherwise.
	protocolVersion := mcpDefaultProtocolVersion
	if len(req.Params) > 0 {
		var p struct {
			ProtocolVersion string `json:"protocolVersion"`
		}
		if err := json.Unmarshal(req.Params, &p); err == nil && p.ProtocolVersion != "" {
			protocolVersion = p.ProtocolVersion
		}
	}
	return jsonrpcResponse{
		Jsonrpc: "2.0",
		ID:      req.ID,
		Result: map[string]any{
			"protocolVersion": protocolVersion,
			"capabilities": map[string]any{
				"tools": map[string]any{},
			},
			"serverInfo": map[string]any{
				"name":    "refrain-planner-question",
				"version": "0.1.0",
			},
		},
	}
}

func handleToolsList(req jsonrpcRequest) jsonrpcResponse {
	return jsonrpcResponse{
		Jsonrpc: "2.0",
		ID:      req.ID,
		Result: map[string]any{
			"tools": []any{
				map[string]any{
					"name":        askUserToolName,
					"description": "Ask the developer a single clarifying question and wait for their answer. Use this when the task description is ambiguous or missing key information you need before drafting a useful plan. Prefer asking one focused question over fabricating assumptions.",
					"inputSchema": map[string]any{
						"type": "object",
						"properties": map[string]any{
							"question": map[string]any{
								"type":        "string",
								"description": "The clarifying question to show the developer.",
							},
						},
						"required": []string{"question"},
					},
				},
			},
		},
	}
}

func handleToolsCall(req jsonrpcRequest, socketPath string) jsonrpcResponse {
	var params struct {
		Name      string `json:"name"`
		Arguments struct {
			Question string `json:"question"`
		} `json:"arguments"`
	}
	if err := json.Unmarshal(req.Params, &params); err != nil {
		return jsonrpcResponse{
			Jsonrpc: "2.0",
			ID:      req.ID,
			Error:   &jsonrpcError{Code: -32602, Message: "invalid params: " + err.Error()},
		}
	}
	if params.Name != askUserToolName {
		return jsonrpcResponse{
			Jsonrpc: "2.0",
			ID:      req.ID,
			Error:   &jsonrpcError{Code: -32601, Message: "unknown tool: " + params.Name},
		}
	}
	question := params.Arguments.Question
	if question == "" {
		return toolTextResult(req.ID, "(ask_user called without a question; carry on with best assumption)")
	}

	answer, err := planner.AskQuestion(socketPath, question)
	if err != nil {
		// Surface the IPC error as a tool error so the planner sees it and
		// can fall back to fabricating an assumption rather than retrying
		// forever.
		return jsonrpcResponse{
			Jsonrpc: "2.0",
			ID:      req.ID,
			Result: map[string]any{
				"isError": true,
				"content": []any{
					map[string]any{
						"type": "text",
						"text": "ask_user failed: " + err.Error(),
					},
				},
			},
		}
	}

	if answer == "" {
		return toolTextResult(req.ID, "(developer skipped the question without answering; proceed with your best assumption)")
	}
	return toolTextResult(req.ID, "Developer answered: "+answer)
}

func toolTextResult(id json.RawMessage, text string) jsonrpcResponse {
	return jsonrpcResponse{
		Jsonrpc: "2.0",
		ID:      id,
		Result: map[string]any{
			"content": []any{
				map[string]any{"type": "text", "text": text},
			},
		},
	}
}
