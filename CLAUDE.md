# CLAUDE.md

## Project

Baton is a terminal-native TUI for orchestrating multiple Claude Code agents in parallel. Written in Go 1.25.

## Build & Test

```bash
go build -o baton .         # build
go test ./...               # unit tests
go test -race ./...         # with race detector (required before commit)
go vet ./...                # static analysis
golangci-lint run           # lint (uses .golangci.yml)
gofumpt -w .                # format all Go files
./baton doctor              # validate environment + hook pipeline round-trip
```

End-to-end TUI tests live under `internal/e2e/` behind the `e2e` build tag and need `tu` v0.6.0+:

```bash
go test -tags e2e -timeout 300s -v ./internal/e2e/
```

Always run `go test -race ./...` before committing — concurrency bugs have been caught and fixed by the race detector.

## Architecture

- `cmd/root.go` — Cobra root, launches the TUI.
- `cmd/doctor.go` — Environment validation (git ≥ 2.20, `claude` on PATH with `--settings` support, hook-socket round-trip, git repo, GitHub auth).
- `cmd/hook.go` — Not for humans. Claude Code invokes `baton hook <event>` per the settings file baton writes; it forwards the JSON payload to the running baton over `BATON_HOOK_SOCKET`. Always exits 0 so hook failures never block Claude.
- `internal/pty/` — Raw PTY I/O around `creack/pty`. No goroutines — callers manage read loops.
- `internal/vt/` — Virtual terminal bridge around `x/vt.SafeEmulator`. Uses an `io.Pipe` for `Read()` so `Close()` is thread-safe without racing emulator internals.
- `internal/git/` — Worktree CRUD, diff, merge via `exec.Command("git", ...)`. Worktrees live at `.baton/worktrees/<name>` on branches `baton/<name>`.
- `internal/agent/` — Composes PTY + VT + git into managed agents. Each agent has 3 goroutines: readLoop (PTY→VT), writeLoop (VT→PTY), statusLoop (idle detection). Sessions group agents sharing a worktree. Manager handles lifecycle and events.
- `internal/tui/` — Bubble Tea v2 views: dashboard (list + preview), diff summary/detail, global/repo config forms, file browser (add repo), branch picker (session on existing branch/PR), statusbar.
- `internal/hook/` — Unix-socket server + client for Claude hook events (`session-start`, `stop`, `session-end`, `notification`, `user-prompt-submit`) plus the settings file generator that wires Claude's hooks to `baton hook`.
- `internal/github/` — GitHub API wrapper for PRs, checks, and review status.
- `internal/config/` — Global settings (`~/.config/baton/settings.json`) and per-repo settings (`.baton/settings.json`) plus resolution logic.
- `internal/state/` — Session persistence so `q` detaches cleanly and a later `baton` invocation reattaches.
- `internal/editor/` — IDE launcher helpers; macOS app probing and quote-aware tokenizer for `open -a "Visual Studio Code"` style commands.
- `internal/audio/` — Optional chimes for status transitions (best-effort; nil on failure).
- `internal/e2e/` — End-to-end TUI tests driven by the `tu` headless virtual terminal (build tag `e2e`).

## Key Patterns

- **Bubble Tea v2**: `View()` returns `tea.View`, not string. Use `tea.NewView(content)` with `v.AltScreen = true`. No `tea.WithAltScreen()` option.
- **Charm imports**: Bubble Tea v2 is `charm.land/bubbletea/v2`. Lipgloss is still `github.com/charmbracelet/lipgloss`. x/vt is `github.com/charmbracelet/x/vt`.
- **Key forwarding**: `tea.KeyPressMsg` converts directly to `xvt.KeyPressEvent` — same underlying `ultraviolet.Key` struct.
- **Thread safety**: VT bridge uses an `io.Pipe` to decouple `Read()` from `SafeEmulator.Read()`, so `Close()` can unblock readers without racing emulator state.
- **Agent names**: Must match `[a-zA-Z0-9][a-zA-Z0-9_-]*`. Enforced in `Manager.Create()`.
- **Shutdown sequence**: close `m.done` → stop hook server → wait for watcher goroutines (watchAgent + in-flight branch-rename goroutines) → kill agents + cleanup worktrees → close events channel. Hook-first ordering ensures no new rename goroutines spawn during teardown, and the drain-before-cleanup step prevents a rename from racing worktree removal. Agent kill: close PTY → close terminal (unblocks writeLoop) → wait for writeLoopDone.
- **Hooks**: Baton writes a per-session settings JSON and launches `claude --settings <path>` so each hook invocation carries `BATON_HOOK_SOCKET` and `BATON_AGENT_ID` env, routing events back over a unix socket. Running `claude` outside baton exits the hook CLI silently.
- **Status detection**: visual-stability detection + hook-driven signals classify agents as idle / active / waiting / done / error. `StatusWaiting` is distinct (permission prompts, input blocks) and surfaces in a dashboard accent color.
- **Branch naming**: sessions are created on a random adjective-noun branch under the configured `BranchPrefix` (default `baton/`). On the first actionable `user-prompt-submit` hook (non-empty, not a bare slash command), the manager runs the configured `BranchNamer` in a goroutine tracked by `m.watchers`. `DefaultBranchNamer()` shells out to `claude -p --model claude-haiku-4-5` with a 15s timeout, piping a fully rendered instruction string to stdin: the resolved `BranchNamePrompt` (default in `config.DefaultBranchNamePrompt`) with the `{prompt}` token replaced by the user's prompt; if a custom template lacks `{prompt}`, the prompt is appended on a new paragraph rather than silently dropped. On success, `Session.RenameBranch` updates the HEAD symref atomically (Claude's cwd stays valid) and the session display name updates to the Haiku-derived suffix (e.g. `add-dark-mode`) so the sidebar separator shows what the session is working on; agents keep their stable `Track N` identities and are never renamed. There is no slugify-from-prompt fallback: on error/empty/timeout the random branch persists and `Session.hasClaudeName` stays false so the next prompt retries. `Session.TryStartRename`/`finishRename` gate double-dispatch so a second prompt arriving mid-Haiku is a no-op. Tests inject a fake namer via `Manager.SetBranchNamer`. Sessions started on an existing branch via `CreateSessionOnBranch*` set `hasClaudeName=true` at creation so they keep their original branch. `BranchPrefix` supports `{user}` (slugified `git config user.name` → `$USER` fallback) and `{date}` (`YYYY-MM-DD`) tokens, resolved in `config.ExpandBranchPrefix`.
- **Dashboard**: Two-section pipeline view with a SESSIONS section (all in-progress sessions, each with an inline status badge: error/waiting/finished/normal) and a REVIEW QUEUE section (sessions awaiting review). This is the only dashboard mode — there is no toggle. Navigation between sections is tracked by the `focusCursorSection` enum (`focusSectionActive`, `focusSectionReview`); `j/k` move the cursor across both sections in render order. Workflow keys (`c` add agent, `t` shell, `e` IDE, `p` PR, `d` diff, `x` kill agent, `X` kill session, `o` open branch, `a` add repo, `s` settings) act on the cursor-selected session. `focusLaunch` is a separate `panelFocus` value used for the fullscreen per-agent terminal; pressing space/enter on a session opens this view, esc returns to the pipeline. Key state fields on the App: `focusActiveIdx int`, `focusQueueIndex int`, `focusCursorSection focusSection`, `focusLaunchAgent *agent.Agent`. Mouse: click on a session card moves the cursor; double-click activates (focusLaunch for active, review panel for queue). Wellness features: audio chimes are suppressed for non-waiting status changes; a session timer tracks elapsed time against `FocusSessionMinutes` and triggers an automatic break overlay when the limit is reached; every session block is logged to `.baton/logs/wellness.log`. A soft agent limit (`MaxConcurrentAgents`, default 3) warns when exceeded — double-pressing `n` overrides the warning and spawns anyway. Configuration fields: `FocusSessionMinutes int`, `FocusBreakMinutes int`, `MaxConcurrentAgents int`, `MaxReviewBacklog int`.

## Testing

- `internal/pty/` — Echo, cat round-trip, close+done.
- `internal/vt/` — Write/render, ANSI preservation, resize, SendText/Read.
- `internal/git/` — Real git on temp repos.
- `internal/agent/` — Uses `bash -c` instead of `claude`.
- `internal/hook/` — Socket server unit tests, settings generator tests.
- `internal/config/` — Global + per-repo settings, resolution, migration.
- `internal/tui/` — Mostly manual; `app_test.go` and `idecommand_test.go` cover the testable pieces.
- `internal/e2e/` — End-to-end via `tu` CLI, `e2e` build tag.

## Conventions

- Mouse support: click-to-select on session cards in the pipeline; double-click activates (focusLaunch / review panel); drag-to-select inside the focusLaunch agent terminal copies text. PR-indicator clicks on review queue rows open the PR in the browser.
- Agent program defaults to `claude` but is configurable via global/per-repo settings (`AgentProgram`).
- `.baton/` is gitignored (auto-added to the repo's `.gitignore` on first run).
- Errors display briefly in the TUI and clear on next tick; no modal error dialogs.
- Claude hook CLI (`baton hook`) is silent on stdout and always exits 0 — Claude interprets stdout as hook feedback.
- Changelog: every PR should add a fragment file under `changelog.d/` (e.g. `changelog.d/fix-login-redirect.md`) using `### Added`, `### Fixed`, etc. section headers. A release script assembles fragments into `CHANGELOG.md` when a version is cut.

## Dashboard Development Philosophy

**The 3-agent ceiling** — BCG research shows oversight cost exceeds output value beyond ~3 concurrent agents. Design around this: use soft limits, surface warnings early, never silently spawn past the cap. If a feature increases the viable agent count without reducing monitoring burden, it's the wrong feature.

**Batch over stream** — Cognitive resource depletion research supports reviewing agent output at completion, not continuously. Prioritize features that support "check in when done" workflows (push notification, status-on-return summary) over features that require constant attention. Interrupt-driven review is the goal; polling is the anti-pattern.

**Signal noise budget** — Habituation is real. Every ambient status update trains the developer to ignore the sidebar. Persistent state changes (idle→done, waiting for input, error) are worth surfacing; running-normally is not. Resist adding new status indicators that fire continuously — the signal budget is finite and already largely spent.

**The oversight tax** — METR's 2025 RCT found a 19% productivity slowdown for experienced developers using AI tools, driven by verification overhead. Baton's diff view, review queue, and confidence-surfacing features directly reduce this tax. Prioritize these over raw throughput features; throughput without trust is just more work.

**The 90-minute cycle** — The BCG model (1 primary goal + max 3 agents, 90-minute block, defined review point) is the UX target. Design features so this workflow is the path of least resistance, not an advanced option. A feature that only makes sense if you run 8 agents for 4 hours is misaligned with the product.

**Wellbeing is the differentiator** — The drain after managing 5–8 agents is real and measurable. Features that reduce this drain (the pipeline view, wellness timer, attention routing) are Baton's moat. Don't sacrifice them for features that increase raw agent throughput. If a proposed feature makes Baton faster but more exhausting, it belongs in a different tool.
