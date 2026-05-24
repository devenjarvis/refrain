package agent

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/devenjarvis/refrain/internal/config"
)

// VerdictKind is the outcome of a per-task review.
type VerdictKind string

const (
	// VerdictPass means the diff clearly fulfills the task.
	VerdictPass VerdictKind = "pass"
	// VerdictConcerns means the diff addresses the task but has notable gaps or risks.
	VerdictConcerns VerdictKind = "concerns"
	// VerdictFail means the diff does not fulfill the task or introduces clear regressions.
	VerdictFail VerdictKind = "fail"
)

// ReviewVerdict is the outcome of a ReviewerAgent.Review call.
type ReviewVerdict struct {
	Kind      VerdictKind
	Rationale string // 1-2 sentence explanation
}

// ReviewRequest is the input to ReviewerAgent.Review.
type ReviewRequest struct {
	TaskIndex      int
	TaskText       string
	TaskDiff       string
	OriginalPrompt string
	TaskDetail     string   // plan sub-bullets for this task; empty when no plan or freeform plan
	ChangedFiles   []string // paths of files modified in this task's commits; empty when unknown
}

// ReviewerAgent reviews a single task's diff and returns a verdict.
// Implementations may shell out to a binary, mock the call for tests, or
// call an API directly. The method accepts a context for cancellation.
type ReviewerAgent interface {
	Review(ctx context.Context, req ReviewRequest) (ReviewVerdict, error)
}

// DefaultReviewerAgent returns a ReviewerAgent that shells out to
// `claude -p --model <model>`. An empty model falls back to
// config.DefaultReviewerModel. The subprocess inherits a sanitized env so
// refrain hook wiring doesn't bleed into the reviewer subprocess.
func DefaultReviewerAgent(model string) ReviewerAgent {
	if model == "" {
		model = config.DefaultReviewerModel
	}
	return &defaultReviewerAgent{model: model}
}

type defaultReviewerAgent struct {
	model string
}

func buildReviewPrompt(req ReviewRequest) string {
	prompt := strings.TrimSpace(req.OriginalPrompt)
	if prompt == "" {
		prompt = "(no original intent recorded)"
	}
	diff := strings.TrimSpace(req.TaskDiff)
	if diff == "" {
		diff = "(no diff — task may have no committed changes)"
	}

	var b strings.Builder
	b.WriteString("You are a code reviewer checking whether a code diff fulfills a specific task.\n\n")
	fmt.Fprintf(&b, "ORIGINAL INTENT:\n%s\n\n", prompt)
	fmt.Fprintf(&b, "TASK #%d:\n%s\n\n", req.TaskIndex, req.TaskText)

	if req.TaskDetail != "" {
		fmt.Fprintf(&b, "TASK DETAIL:\n%s\n\n", req.TaskDetail)
	}

	if len(req.ChangedFiles) > 0 {
		b.WriteString("CHANGED FILES:\n")
		for _, f := range req.ChangedFiles {
			fmt.Fprintf(&b, "- %s\n", f)
		}
		b.WriteString("\n")
	}

	fmt.Fprintf(&b, "DIFF:\n%s\n\n", diff)

	b.WriteString("Review the diff and decide:\n")
	b.WriteString("- \"pass\" — the diff clearly fulfills this task\n")
	b.WriteString("- \"concerns\" — the diff addresses this task but has notable gaps, risks, or incomplete work\n")
	b.WriteString("- \"fail\" — the diff does not fulfill this task or introduces clear regressions\n\n")
	if req.TaskDetail != "" || len(req.ChangedFiles) > 0 {
		b.WriteString("Check that the diff touches the files and implements the behavior described in TASK DETAIL.\n\n")
	}
	b.WriteString("Respond with EXACTLY this format (no other text):\n")
	b.WriteString("VERDICT: <pass|concerns|fail>\n")
	b.WriteString("RATIONALE: <one or two sentences explaining your verdict>\n")

	return b.String()
}

// buildReviewerArgs returns the argv (excluding the binary path) for the
// reviewer subprocess. Mirrors buildClaudeHaikuArgs / buildClaudePlannerArgs:
// read-only tools only, no MCP servers, no session persistence. --bare is
// added when ANTHROPIC_API_KEY is set (disables hooks, keychain reads, etc.).
func buildReviewerArgs(model string) []string {
	args := []string{"-p", "--model", model}
	if os.Getenv("ANTHROPIC_API_KEY") != "" {
		args = append(args, "--bare")
	}
	tools := "Read,Grep,Glob,LS"
	args = append(
		args,
		"--strict-mcp-config", "--mcp-config", `{"mcpServers":{}}`,
		"--disable-slash-commands",
		"--no-session-persistence",
		"--tools", tools,
		"--allowed-tools", tools,
		"--setting-sources", "user,project",
		"--exclude-dynamic-system-prompt-sections",
	)
	return args
}

func (r *defaultReviewerAgent) Review(ctx context.Context, req ReviewRequest) (ReviewVerdict, error) {
	claudePath, err := exec.LookPath("claude")
	if err != nil {
		return ReviewVerdict{}, fmt.Errorf("%w: %v", ErrClaudeNotFound, err)
	}

	cmd := exec.CommandContext(ctx, claudePath, buildReviewerArgs(r.model)...)
	cmd.Stdin = strings.NewReader(buildReviewPrompt(req))
	cmd.Env = sanitizedHaikuEnv(os.Environ())
	cmd.WaitDelay = 500 * time.Millisecond

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return ReviewVerdict{}, fmt.Errorf("claude reviewer: %w (stderr=%q)", err, strings.TrimSpace(stderr.String()))
	}

	return parseReviewerOutput(strings.TrimSpace(stdout.String()))
}

// parseReviewerOutput extracts the VERDICT and RATIONALE lines from the
// reviewer subprocess output. It is lenient: if the output does not match the
// expected format exactly, it returns VerdictConcerns with the raw output as
// the rationale so callers always get a usable verdict rather than an error.
func parseReviewerOutput(output string) (ReviewVerdict, error) {
	var kind VerdictKind
	var rationale string

	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(strings.ToUpper(line), "VERDICT:") {
			v := strings.TrimSpace(line[len("VERDICT:"):])
			switch strings.ToLower(v) {
			case "pass":
				kind = VerdictPass
			case "concerns":
				kind = VerdictConcerns
			case "fail":
				kind = VerdictFail
			default:
				kind = VerdictConcerns
			}
		} else if strings.HasPrefix(strings.ToUpper(line), "RATIONALE:") {
			rationale = strings.TrimSpace(line[len("RATIONALE:"):])
		}
	}

	if kind == "" {
		// Reviewer output was not in the expected format.
		if output == "" {
			return ReviewVerdict{}, errors.New("reviewer: empty output")
		}
		return ReviewVerdict{Kind: VerdictConcerns, Rationale: output}, nil
	}

	return ReviewVerdict{Kind: kind, Rationale: rationale}, nil
}
