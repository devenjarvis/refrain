package agent

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/charmbracelet/x/ansi"
	xvt "github.com/charmbracelet/x/vt"
	"github.com/devenjarvis/baton/internal/hook"
	bpty "github.com/devenjarvis/baton/internal/pty"
	"github.com/devenjarvis/baton/internal/vt"
)

// Agent ties together a PTY and VT terminal into a managed unit.
// Agents do not own worktrees — sessions do.
type Agent struct {
	ID           string
	Name         string
	Task         string
	IsShell      bool   // true for shell agents (not Claude)
	WorktreePath string // reference only; session owns the worktree
	CreatedAt    time.Time

	pty      *bpty.PTY
	terminal *vt.Terminal

	mu               sync.RWMutex
	displayName      string
	claudeSessionID  string
	hasClaudeName    bool
	status           Status
	waitingReason    string
	askingQuestion   bool
	lastOutput       time.Time
	lastInput        time.Time
	composing        bool
	exitErr          error
	chimedForTurn    bool
	sessionStartedAt time.Time
	cleanExit        bool

	done          chan struct{}
	writeLoopDone chan struct{}
}

// Config holds parameters for creating a new agent.
type Config struct {
	Name              string
	Task              string
	Rows              int
	Cols              int
	RepoPath          string
	BypassPermissions bool
	AgentProgram      string // CLI program to spawn; defaults to "claude" if empty
	// AgentModel, when non-empty, is forwarded as `--model <AgentModel>` to
	// spawned `claude` agents. Empty means "no --model flag" (Claude CLI
	// picks its own default). Ignored when AgentProgram is not `claude` —
	// passing --model to a custom binary is undefined.
	AgentModel string
	// BuildSystemPrompt, when non-empty, is forwarded as
	// `--append-system-prompt <BuildSystemPrompt>` to spawned `claude`
	// agents. Empty means no flag. Ignored when AgentProgram is not
	// `claude` — passing --append-system-prompt to a custom binary is
	// undefined.
	BuildSystemPrompt string
}

// agentProgram returns the CLI program from cfg, defaulting to "claude".
func agentProgram(cfg Config) string {
	if cfg.AgentProgram != "" {
		return cfg.AgentProgram
	}
	return "claude"
}

// supportsHooks reports whether the configured agent program understands
// Claude Code's --settings flag and lifecycle hooks. Other programs (e.g. a
// bash shim used by e2e tests, or a custom tool a user wires in) get no
// hook wiring — their agents will sit at Active indefinitely, matching the
// "non-claude" assumption in the hook-integration plan.
func supportsHooks(cfg Config) bool {
	return filepath.Base(agentProgram(cfg)) == "claude"
}

// newAgent creates and starts an agent with the configured agent program.
// The worktreePath is provided by the session — agents do not create worktrees.
// socketPath is the unix socket the baton-hook CLI should dial; when non-empty,
// a Claude settings file is written that routes SessionStart/Stop/SessionEnd
// events back to this baton process.
func newAgent(id string, cfg Config, worktreePath, socketPath string) (*Agent, error) {
	term := vt.New(cfg.Cols, cfg.Rows)

	prog := agentProgram(cfg)
	args, err := buildSpawnArgs(cfg, worktreePath, socketPath)
	if err != nil {
		return nil, err
	}
	cmd := exec.Command(prog, args...)
	cmd.Dir = worktreePath
	cmd.Env = append(cmd.Environ(), "TERM=xterm-256color")
	if err := applyHookEnv(cmd, cfg, id, socketPath); err != nil {
		return nil, err
	}

	p := &bpty.PTY{}
	if err := p.Start(cmd, uint16(cfg.Rows), uint16(cfg.Cols)); err != nil {
		return nil, err
	}

	a := &Agent{
		ID:            id,
		Name:          cfg.Name,
		Task:          cfg.Task,
		WorktreePath:  worktreePath,
		CreatedAt:     time.Now(),
		pty:           p,
		terminal:      term,
		status:        StatusStarting,
		done:          make(chan struct{}),
		writeLoopDone: make(chan struct{}),
	}

	if cfg.Task != "" {
		a.SetDisplayName(slugify(cfg.Task))
	}

	go a.readLoop()
	go a.writeLoop()

	return a, nil
}

// newResumedAgent creates and starts an agent that resumes a previous Claude session.
// It uses `claude --resume <sessionId>` if claudeSessionID is provided, falls back to
// `claude --continue` if empty, then plain `claude` as last resort.
func newResumedAgent(
	id string,
	cfg Config,
	worktreePath string,
	claudeSessionID string,
	socketPath string,
) (*Agent, error) {
	term := vt.New(cfg.Cols, cfg.Rows)

	resumeArgs := buildResumeArgs(cfg, claudeSessionID)
	hookArgs, err := buildHookArgs(cfg, worktreePath, socketPath)
	if err != nil {
		return nil, err
	}
	// --settings must come before --resume / --continue (flags before positional args).
	args := append(hookArgs, resumeArgs...)
	cmd := exec.Command(agentProgram(cfg), args...)
	cmd.Dir = worktreePath
	cmd.Env = append(cmd.Environ(), "TERM=xterm-256color")
	if err := applyHookEnv(cmd, cfg, id, socketPath); err != nil {
		return nil, err
	}

	p := &bpty.PTY{}
	if err := p.Start(cmd, uint16(cfg.Rows), uint16(cfg.Cols)); err != nil {
		return nil, err
	}

	a := &Agent{
		ID:              id,
		Name:            cfg.Name,
		Task:            cfg.Task,
		WorktreePath:    worktreePath,
		CreatedAt:       time.Now(),
		pty:             p,
		terminal:        term,
		claudeSessionID: claudeSessionID,
		status:          StatusStarting,
		done:            make(chan struct{}),
		writeLoopDone:   make(chan struct{}),
	}

	go a.readLoop()
	go a.writeLoop()

	return a, nil
}

// buildResumeArgs constructs claude CLI arguments for resuming a session.
func buildResumeArgs(cfg Config, claudeSessionID string) []string {
	var args []string

	// --model goes before --dangerously-skip-permissions so flags stay
	// grouped before the positional --resume/--continue/Task args.
	if cfg.AgentModel != "" && supportsHooks(cfg) {
		args = append(args, "--model", cfg.AgentModel)
	}

	// Re-attach the build-phase prompt on resume too. Anthropic doesn't
	// promise that --append-system-prompt persists across --resume, and
	// duplicating identical text is harmless if it does.
	if cfg.BuildSystemPrompt != "" && supportsHooks(cfg) {
		args = append(args, "--append-system-prompt", cfg.BuildSystemPrompt)
	}

	if cfg.BypassPermissions {
		args = append(args, "--dangerously-skip-permissions")
	}

	if claudeSessionID != "" {
		args = append(args, "--resume", claudeSessionID)
	} else {
		args = append(args, "--continue")
	}

	if cfg.Task != "" {
		args = append(args, cfg.Task)
	}

	return args
}

// buildSpawnArgs builds the argv for a fresh `claude` invocation, prefixed with
// the hook settings (`--settings <file>`) when hooks are supported.
func buildSpawnArgs(cfg Config, worktreePath, socketPath string) ([]string, error) {
	args, err := buildHookArgs(cfg, worktreePath, socketPath)
	if err != nil {
		return nil, err
	}
	// --model goes before --dangerously-skip-permissions so flags stay
	// grouped before the positional Task arg. Only applied when the program
	// is claude — passing --model to a custom binary is undefined.
	if cfg.AgentModel != "" && supportsHooks(cfg) {
		args = append(args, "--model", cfg.AgentModel)
	}
	if cfg.BuildSystemPrompt != "" && supportsHooks(cfg) {
		args = append(args, "--append-system-prompt", cfg.BuildSystemPrompt)
	}
	if cfg.BypassPermissions {
		args = append(args, "--dangerously-skip-permissions")
	}
	if cfg.Task != "" {
		args = append(args, cfg.Task)
	}
	return args, nil
}

// buildHookArgs writes the per-agent hooks settings file and returns the
// `--settings <path>` pair. Returns an empty slice when hooks are disabled
// (socketPath empty, or agent program is not claude).
//
// The file lives at `<worktreePath>/.baton/hooks.json` so it sits inside the
// already-gitignored `.baton/` tree and doesn't show up as an untracked file
// in the user's worktree `git status`.
func buildHookArgs(cfg Config, worktreePath, socketPath string) ([]string, error) {
	if socketPath == "" || !supportsHooks(cfg) {
		return nil, nil
	}
	hookPath := filepath.Join(worktreePath, ".baton", "hooks.json")
	if err := hook.WriteHooksFile(hookPath); err != nil {
		return nil, fmt.Errorf("writing hooks settings: %w", err)
	}
	return []string{"--settings", hookPath}, nil
}

// applyHookEnv sets BATON_HOOK_SOCKET and BATON_AGENT_ID on cmd so the
// baton-hook CLI (invoked by claude) knows where to forward events.
// No-op when hooks are disabled for this program (socketPath empty or
// program is not claude).
//
// Returns an error if agentID is empty while hooks are otherwise enabled —
// without an agent ID, the hook server can't route events back to a
// specific agent and we'd produce a zombie.
func applyHookEnv(cmd *exec.Cmd, cfg Config, agentID, socketPath string) error {
	if socketPath == "" || !supportsHooks(cfg) {
		return nil
	}
	if agentID == "" {
		return fmt.Errorf("applyHookEnv: agentID is empty for claude agent on %s", socketPath)
	}
	cmd.Env = append(
		cmd.Env,
		"BATON_HOOK_SOCKET="+socketPath,
		"BATON_AGENT_ID="+agentID,
	)
	return nil
}

// newAgentWithCommand creates an agent using a custom command instead of claude.
// Used for testing. The worktreePath is provided by the session.
func newAgentWithCommand(id string, cfg Config, worktreePath string, cmd *exec.Cmd) (*Agent, error) {
	term := vt.New(cfg.Cols, cfg.Rows)

	cmd.Dir = worktreePath
	cmd.Env = append(cmd.Environ(), "TERM=xterm-256color")

	p := &bpty.PTY{}
	if err := p.Start(cmd, uint16(cfg.Rows), uint16(cfg.Cols)); err != nil {
		return nil, err
	}

	a := &Agent{
		ID:            id,
		Name:          cfg.Name,
		Task:          cfg.Task,
		WorktreePath:  worktreePath,
		CreatedAt:     time.Now(),
		pty:           p,
		terminal:      term,
		status:        StatusStarting,
		done:          make(chan struct{}),
		writeLoopDone: make(chan struct{}),
	}

	go a.readLoop()
	go a.writeLoop()

	return a, nil
}

// newShellAgent creates an agent that spawns the user's shell instead of claude.
// It reuses the same PTY+VT setup and readLoop/writeLoop but skips statusLoop —
// shell agents stay StatusActive once output is received.
func newShellAgent(id string, cfg Config, worktreePath string) (*Agent, error) {
	term := vt.New(cfg.Cols, cfg.Rows)

	shell := os.Getenv("SHELL")
	if shell == "" {
		shell = "/bin/sh"
	}
	cmd := exec.Command(shell)
	cmd.Dir = worktreePath
	cmd.Env = append(cmd.Environ(), "TERM=xterm-256color")

	p := &bpty.PTY{}
	if err := p.Start(cmd, uint16(cfg.Rows), uint16(cfg.Cols)); err != nil {
		return nil, err
	}

	a := &Agent{
		ID:            id,
		Name:          "shell",
		IsShell:       true,
		WorktreePath:  worktreePath,
		CreatedAt:     time.Now(),
		pty:           p,
		terminal:      term,
		displayName:   "shell",
		status:        StatusStarting,
		done:          make(chan struct{}),
		writeLoopDone: make(chan struct{}),
	}

	go a.readLoop()
	go a.writeLoop()
	// No statusLoop — shell agents don't transition to Idle.

	return a, nil
}

// readLoop reads PTY output and feeds it to the VT terminal.
func (a *Agent) readLoop() {
	buf := make([]byte, 4096)
	for {
		n, err := a.pty.Read(buf)
		if n > 0 {
			_, _ = a.terminal.Write(buf[:n])
			a.mu.Lock()
			a.lastOutput = time.Now()
			if a.status == StatusStarting {
				a.status = StatusActive
			}
			a.mu.Unlock()
		}
		if err != nil {
			break
		}
	}

	// Process has exited — wait for the PTY done signal.
	<-a.pty.Done()

	a.mu.Lock()
	a.exitErr = a.pty.Err()
	if a.exitErr != nil {
		a.status = StatusError
	} else {
		a.status = StatusDone
	}
	a.mu.Unlock()

	// Close terminal to unblock writeLoop's Read call and bridgeRead's pipe,
	// mirroring what Kill() does for forced exits.
	a.terminal.Close()
	<-a.writeLoopDone

	close(a.done)
}

// writeLoop reads escape sequences from the VT terminal and writes them to the PTY.
func (a *Agent) writeLoop() {
	defer close(a.writeLoopDone)
	buf := make([]byte, 256)
	for {
		n, err := a.terminal.Read(buf)
		if n > 0 {
			_, _ = a.pty.Write(buf[:n])
		}
		if err != nil {
			return
		}
	}
}

// OnHookEvent applies a Claude Code hook event to the agent's state.
// Returns true if the agent's status changed as a result (so the manager
// can emit EventStatusChanged).
//
// Status transitions driven here replace the old statusLoop heuristic —
// Claude's SessionStart/Stop/SessionEnd events are the authoritative signal.
func (a *Agent) OnHookEvent(e hook.Event) (statusChanged bool) {
	a.mu.Lock()
	defer a.mu.Unlock()

	switch e.Kind {
	case hook.KindSessionStart:
		// A late SessionStart after process exit must not resurrect a Done or
		// Error agent — mirror the guard pattern on the other hook kinds.
		if a.status == StatusDone || a.status == StatusError {
			return false
		}
		if e.SessionID != "" {
			a.claudeSessionID = e.SessionID
		}
		a.sessionStartedAt = time.Now()
		if a.status != StatusActive {
			a.status = StatusActive
			return true
		}
		return false

	case hook.KindStop:
		// A late Stop after process exit must not resurrect a Done or Error
		// agent. This race is common: the PTY closes (readLoop sets Done/Error)
		// and Claude's in-flight Stop hook event lands a moment later on the
		// unix socket. Without this guard the agent's status would flip back to
		// Idle, showing a wrong indicator and potentially triggering a chime.
		if a.status == StatusDone || a.status == StatusError {
			return false
		}
		a.waitingReason = ""
		// Claude finished its turn; the user is now in control. Clear the
		// composing flag so input-display logic returns to its idle baseline.
		a.composing = false
		// Scan the visible viewport to find the last non-empty line and detect
		// a trailing "?". RenderRegion holds only the terminal's own emulator
		// lock; there is no a.mu path that would invert the order, so holding
		// a.mu here is safe.
		h := a.terminal.Height()
		a.askingQuestion = false
		if h > 0 {
			raw := ansi.Strip(a.terminal.RenderRegion(0, h-1))
			lines := strings.Split(raw, "\n")
			for i := len(lines) - 1; i >= 0; i-- {
				line := strings.TrimSpace(lines[i])
				if line != "" {
					a.askingQuestion = strings.HasSuffix(line, "?")
					break
				}
			}
		}
		if a.status != StatusIdle {
			a.status = StatusIdle
			return true
		}
		return false

	case hook.KindSessionEnd:
		// Flag a clean exit for observability. Actual teardown still goes
		// through the PTY close path (readLoop -> close(done)).
		a.cleanExit = true
		a.waitingReason = ""
		return false

	case hook.KindNotification:
		// Claude is waiting for the user (typically a permission prompt).
		// Only override Active/Waiting so a late Notification can't revive a
		// Done or Error agent, and don't clobber Idle either — if Claude sent
		// Stop already, a trailing Notification shouldn't re-attention the row.
		if a.status == StatusActive || a.status == StatusWaiting {
			a.waitingReason = e.Message
			if a.status != StatusWaiting {
				a.status = StatusWaiting
				return true
			}
		}
		return false

	case hook.KindUserPromptSubmit:
		// User just submitted a new turn. Re-arm the chime and drive the
		// agent back to Active — this is the authoritative re-arm signal
		// alongside the existing SendKey(Enter) path. Mirror the Notification
		// guard: a late event after the agent exited must not resurrect a
		// Done or Error row, and must not reset chimedForTurn either.
		if a.status == StatusDone || a.status == StatusError {
			return false
		}
		a.waitingReason = ""
		a.askingQuestion = false
		a.chimedForTurn = false
		if a.status != StatusActive {
			a.status = StatusActive
			return true
		}
		return false

	case hook.KindPreToolUse:
		// Claude resumed tool execution — authoritative signal that the
		// agent is no longer waiting on the user (e.g. a permission prompt
		// was just approved). Clear Waiting back to Active so the yellow
		// indicator doesn't linger until Stop fires at end of turn.
		// Mirror the late-event guard on Notification/UserPromptSubmit so a
		// trailing event can't revive a Done or Error row. Do NOT reset
		// chimedForTurn: that's gated to new user turns, not every tool call.
		if a.status == StatusDone || a.status == StatusError {
			return false
		}
		a.waitingReason = ""
		if a.status != StatusActive {
			a.status = StatusActive
			return true
		}
		return false
	}
	return false
}

// Render returns the full terminal screen as an ANSI string.
func (a *Agent) Render() string {
	return a.terminal.Render()
}

// StableRender returns a cached render if the terminal was recently written to,
// avoiding mid-repaint snapshots.
func (a *Agent) StableRender() string {
	return a.terminal.StableRender()
}

// AltScreenEntered returns true if the terminal transitioned into alternate
// screen mode since the last call. The flag resets on each read.
func (a *Agent) AltScreenEntered() bool {
	return a.terminal.AltScreenEntered()
}

// RenderRegion returns a subset of terminal rows.
func (a *Agent) RenderRegion(startRow, endRow int) string {
	return a.terminal.RenderRegion(startRow, endRow)
}

// SendKey forwards a key event to the VT terminal.
func (a *Agent) SendKey(key xvt.KeyPressEvent) {
	a.mu.Lock()
	a.lastInput = time.Now()
	if key.Code == xvt.KeyEnter {
		a.composing = false
		// Enter re-arms the chime: the user just submitted a new turn.
		// Other keys (edits, backspace, arrow keys) must not reset the flag.
		a.chimedForTurn = false
	} else {
		a.composing = true
	}
	a.mu.Unlock()
	a.terminal.SendKey(key)
}

// SendMouse forwards a mouse event to the VT terminal. The terminal's emulator
// only emits bytes to the PTY when the running program has enabled mouse
// reporting (DECSET 1000/1002/1003 + SGR 1006), so this is a no-op when the
// agent hasn't opted in. Used to drive Claude Code's `/tui fullscreen` scrollback.
func (a *Agent) SendMouse(m xvt.Mouse) {
	a.terminal.SendMouse(m)
}

// IsAltScreen reports whether the agent's terminal is currently rendering into
// the alternate screen buffer (DECSET 1049). True while Claude is in
// `/tui fullscreen`; false for normal line-mode Claude.
func (a *Agent) IsAltScreen() bool {
	return a.terminal.IsAltScreen()
}

// CursorPosition returns the current cursor position (x, y) where x is the
// column and y is the row, both zero-indexed.
func (a *Agent) CursorPosition() (x, y int) {
	return a.terminal.CursorPosition()
}

// CursorVisible reports whether the inner program wants the cursor shown
// (DECTCEM, mode 25). Used by the TUI to suppress the host-terminal cursor
// while a full-screen app draws its own.
func (a *Agent) CursorVisible() bool {
	return a.terminal.CursorVisible()
}

// SendText forwards text input to the VT terminal.
func (a *Agent) SendText(text string) {
	a.mu.Lock()
	a.lastInput = time.Now()
	a.composing = true
	a.mu.Unlock()
	a.terminal.SendText(text)
}

// Paste forwards a paste event to the VT terminal.
func (a *Agent) Paste(text string) {
	a.mu.Lock()
	a.lastInput = time.Now()
	a.composing = true
	a.mu.Unlock()
	a.terminal.Paste(text)
}

// ScrollbackLines returns the scrollback buffer as ANSI-encoded strings, oldest first.
func (a *Agent) ScrollbackLines() []string {
	return a.terminal.ScrollbackLines()
}

// RenderPadded returns a deterministic width×height rectangle of the current
// screen with every line padded to full width. See [vt.Terminal.RenderPadded].
func (a *Agent) RenderPadded(width, height int) string {
	return a.terminal.RenderPadded(width, height)
}

// RenderPaddedWithSelection returns a width×height render with the cells inside
// sel rendered in reverse video. See [vt.Terminal.RenderPaddedWithSelection].
func (a *Agent) RenderPaddedWithSelection(width, height int, sel vt.SelectionRect) string {
	return a.terminal.RenderPaddedWithSelection(width, height, sel)
}

// ExtractText returns the plain-text content of the cells inside rect.
// See [vt.Terminal.ExtractText].
func (a *Agent) ExtractText(rect vt.SelectionRect) string {
	return a.terminal.ExtractText(rect)
}

// ExtractTextFromSnapshot extracts plain text from the correct visible window
// in the combined scrollback+viewport content, accounting for scroll offset.
// See [vt.Terminal.ExtractTextFromSnapshot].
func (a *Agent) ExtractTextFromSnapshot(width, height, scrollOffset int, rect vt.SelectionRect) string {
	return a.terminal.ExtractTextFromSnapshot(width, height, scrollOffset, rect)
}

// Snapshot returns a consistent (scrollback, viewport) pair for splice-safe
// reads during pgup scrolling. See [vt.Terminal.Snapshot].
func (a *Agent) Snapshot(width, height int) (scrollback []string, viewport string) {
	return a.terminal.Snapshot(width, height)
}

// Resize updates both the VT terminal and PTY dimensions.
func (a *Agent) Resize(rows, cols int) {
	a.terminal.Resize(cols, rows)
	_ = a.pty.Resize(uint16(rows), uint16(cols))
}

// Status returns the current agent status.
func (a *Agent) Status() Status {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.status
}

// WaitingReason returns the message from the most recent KindNotification event
// that drove the agent to StatusWaiting, or "" if the agent is not waiting.
// The reason is cleared when the agent transitions away from StatusWaiting via
// KindPreToolUse, KindStop, or KindUserPromptSubmit.
func (a *Agent) WaitingReason() string {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.waitingReason
}

// AskingQuestion reports whether the agent's last terminal output (at the time
// of the most recent KindStop event) ended with a "?", suggesting Claude asked
// an informal question rather than finishing a task. Cleared on KindUserPromptSubmit.
func (a *Agent) AskingQuestion() bool {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.askingQuestion
}

// SetDisplayName sets the human-readable display name for this agent.
func (a *Agent) SetDisplayName(name string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.displayName = name
}

// GetDisplayName returns the display name if set, otherwise Name.
func (a *Agent) GetDisplayName() string {
	a.mu.RLock()
	defer a.mu.RUnlock()
	if a.displayName != "" {
		return a.displayName
	}
	return a.Name
}

// HasReceivedInput reports whether the user has sent any input to this agent.
func (a *Agent) HasReceivedInput() bool {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return !a.lastInput.IsZero()
}

// TimeSinceOutput returns how long since the PTY last produced output.
// Returns 0 before any output has been observed.
func (a *Agent) TimeSinceOutput() time.Duration {
	a.mu.RLock()
	defer a.mu.RUnlock()
	if a.lastOutput.IsZero() {
		return 0
	}
	return time.Since(a.lastOutput)
}

// ChimedForTurn reports whether the chime has already fired for the current
// user turn. Resets to false when the user presses Enter.
func (a *Agent) ChimedForTurn() bool {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.chimedForTurn
}

// MarkChimedForTurn marks the current turn as having chimed, so the trigger
// won't re-fire until the user presses Enter to start a new turn.
func (a *Agent) MarkChimedForTurn() {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.chimedForTurn = true
}

// HasDisplayName reports whether a display name has been explicitly set.
func (a *Agent) HasDisplayName() bool {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.displayName != ""
}

// Elapsed returns how long the agent has been running.
func (a *Agent) Elapsed() time.Duration {
	return time.Since(a.CreatedAt)
}

// Done returns a channel that fires when the agent's process exits.
func (a *Agent) Done() <-chan struct{} {
	return a.done
}

// ExitErr returns the process exit error, if any.
func (a *Agent) ExitErr() error {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.exitErr
}

// Kill terminates the agent's process and waits for goroutines to exit.
// Safe to call multiple times — subsequent calls are no-ops.
func (a *Agent) Kill() {
	_ = a.pty.Close()
	// Close terminal to unblock writeLoop's Read call.
	a.terminal.Close()
	// Wait for writeLoop to finish before returning.
	<-a.writeLoopDone
}

// ClaudeSessionID returns the Claude session ID captured from the session file.
func (a *Agent) ClaudeSessionID() string {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.claudeSessionID
}

// SetClaudeSessionID sets the Claude session ID.
func (a *Agent) SetClaudeSessionID(id string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.claudeSessionID = id
}

// HasClaudeName reports whether the agent has been given a Claude session name.
func (a *Agent) HasClaudeName() bool {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.hasClaudeName
}

// SetClaudeName sets whether the agent has been given a Claude session name.
func (a *Agent) SetClaudeName(v bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.hasClaudeName = v
}
