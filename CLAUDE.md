# CLAUDE.md

> **Architecture & code conventions:** see [`CONVENTIONS.md`](CONVENTIONS.md). This file describes *what the system does*; `CONVENTIONS.md` describes *how the code must be structured* (component architecture, message/render rules, layering). Read it before writing or changing code.
>
> **Design system / visual tokens:** see [`DESIGN.md`](DESIGN.md). Colors, glyphs, borders, spacing, and animation palettes live in one registry (`internal/tui/theme`). Never hardcode a hex or glyph rune — reference a token.

## Project

Refrain is a terminal-native TUI for orchestrating multiple Claude Code agents in parallel, designed around the BCG/2026 finding that oversight cost exceeds output value beyond ~3 concurrent agents. The product is a single opinionated workflow — one primary goal, ≤3 agents, a defined review point, a 90-minute block — and every feature is evaluated against that frame. Written in Go 1.25.

The dashboard is a single four-section pipeline view (PLANNING → BUILDING → REVIEWING → SHIPPING) with one cursor. There is no alternate layout, mode toggle, or "advanced" view — the pipeline is the product. When proposing changes, assume the user is running ≤3 agents and reviewing in batch, not streaming output across many panes.

## Build & Test

```bash
go build -o refrain .         # build
go test ./...               # unit tests
go test -race ./...         # with race detector (required before commit)
go vet ./...                # static analysis
golangci-lint run           # lint (uses .golangci.yml)
gofmt -w .                  # format all Go files
./refrain doctor              # validate environment + hook pipeline round-trip
```

End-to-end TUI tests live under `internal/e2e/` behind the `e2e` build tag and need `tu` v0.6.0+:

```bash
go test -tags e2e -timeout 300s -v ./internal/e2e/
```

Always run `go test -race ./...` before committing — concurrency bugs have been caught and fixed by the race detector.

## Architecture

- `cmd/root.go` — Cobra root, launches the TUI.
- `cmd/doctor.go` — Environment validation (git ≥ 2.20, `claude` on PATH with `--settings` support, hook-socket round-trip, git repo, GitHub auth).
- `cmd/hook.go` — Not for humans. Claude Code invokes `refrain hook <event>` per the settings file refrain writes; it forwards the JSON payload to the running refrain over `REFRAIN_HOOK_SOCKET`. Always exits 0 so hook failures never block Claude.
- `internal/pty/` — Raw PTY I/O around `creack/pty`. No goroutines — callers manage read loops.
- `internal/vt/` — Virtual terminal bridge around `x/vt.SafeEmulator`. Uses an `io.Pipe` for `Read()` so `Close()` is thread-safe without racing emulator internals.
- `internal/git/` — Worktree CRUD, diff, merge via `exec.Command("git", ...)`. Worktrees live at `.refrain/worktrees/<name>` on branches `refrain/<name>`.
- `internal/agent/` — Composes PTY + VT + git into managed agents. Each agent has 3 goroutines: readLoop (PTY→VT), writeLoop (VT→PTY), statusLoop (idle detection). Sessions group agents sharing a worktree. Manager handles lifecycle and events.
- `internal/tui/` — Bubble Tea v2 views: dashboard (list + preview), diff summary/detail, global/repo config forms, file browser (add repo), branch picker (session on existing branch/PR), statusbar.
- `internal/hook/` — Unix-socket server + client for Claude hook events (`session-start`, `stop`, `session-end`, `notification`, `user-prompt-submit`) plus the settings file generator that wires Claude's hooks to `refrain hook`.
- `internal/github/` — GitHub API wrapper for PRs, checks, and review status.
- `internal/config/` — Global settings (`~/.config/refrain/settings.json`) and per-repo settings (`.refrain/settings.json`) plus resolution logic.
- `internal/state/` — Session persistence so `q` detaches cleanly and a later `refrain` invocation reattaches.
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
- **Hooks**: Refrain writes a per-session settings JSON and launches `claude --settings <path>` so each hook invocation carries `REFRAIN_HOOK_SOCKET` and `REFRAIN_AGENT_ID` env, routing events back over a unix socket. Running `claude` outside refrain exits the hook CLI silently.
- **Status detection**: visual-stability detection + hook-driven signals classify agents as idle / active / waiting / done / error. `StatusWaiting` is distinct (permission prompts, input blocks) and surfaces in a dashboard accent color.
- **Branch naming**: sessions are created on a random adjective-noun branch under the configured `BranchPrefix` (default `refrain/`). On the first actionable `user-prompt-submit` hook (non-empty, not a bare slash command), the manager runs the configured `BranchNamer` in a goroutine tracked by `m.watchers`. `DefaultBranchNamer()` shells out to `claude -p --model claude-haiku-4-5` with a 15s timeout, piping a fully rendered instruction string to stdin: the resolved `BranchNamePrompt` (default in `config.DefaultBranchNamePrompt`) with the `{prompt}` token replaced by the user's prompt; if a custom template lacks `{prompt}`, the prompt is appended on a new paragraph rather than silently dropped. On success, `Session.RenameBranch` updates the HEAD symref atomically (Claude's cwd stays valid) and the session display name updates to the Haiku-derived suffix (e.g. `add-dark-mode`) so the sidebar separator shows what the session is working on; agents keep their stable `Track N` identities and are never renamed. There is no slugify-from-prompt fallback: on error/empty/timeout the random branch persists and `Session.hasClaudeName` stays false so the next prompt retries. `Session.TryStartRename`/`finishRename` gate double-dispatch so a second prompt arriving mid-Haiku is a no-op. Tests inject a fake namer via `Manager.SetBranchNamer`. Sessions started on an existing branch via `CreateSessionOnBranch*` set `hasClaudeName=true` at creation so they keep their original branch. `BranchPrefix` supports `{user}` (slugified `git config user.name` → `$USER` fallback) and `{date}` (`YYYY-MM-DD`) tokens, resolved in `config.ExpandBranchPrefix`.
- **Plan drafting**: The planning Sonnet subprocess (`internal/agent/planner.go`) produces an eight-section markdown plan written to `<worktree>/.claude/plan.md` and consumed by *two readers at once* — the human reviewing in the plan editor, and the building agent later executing each task end-to-end via `BuildFromPlanPrompt`. Sections in order: `# Goal` (one sentence), `## Spec` (numbered acceptance criteria, one line each, ~12 max), `## Context` (file:line refs for the area touched), `## Reuse` (existing helpers to build on rather than recreate), `## Risks` (unknowns and load-bearing assumptions), `## Tasks` (test-first checklist), `## Verification` (concrete commands), `## Not in scope`. Per-task sub-bullets are labeled (`Files: ...`, `Signatures: ...`, `Test first: ...`, `Implement: ...`, `Verify: ...`) and use `  - ` indented bullets — NEVER `- [ ]`, since `ParsePlanTasks` (`internal/agent/session.go`) and `planTaskCounts` (`internal/tui/dashboard.go`) count ALL checkbox lines top-to-bottom across the document to derive the `[task N]` commit prefix; a stray `- [ ]` outside `## Tasks` corrupts the review panel's commit-to-task mapping. The prompt mandates verbosity in Spec/Reuse/Risks/sub-bullets because research (Plan-and-Solve, TDAD, Superpowers, OpenSpec) shows plan completeness — not brevity — predicts build-agent success. The matched UI obligation is keeping the human's scan path short: Goal one sentence, Spec items one line, task names imperative phrases, sub-bullets treated as reference material the developer may skip on first review. There is no word cap. The drafter runs with the read-only tool allowlist `Read,Grep,Glob,LS,LSP,WebFetch,WebSearch` and may call `ask_user` once via the MCP bridge if a load-bearing ambiguity blocks drafting.
- **Dashboard**: The single top-level view is a four-section pipeline rendered in this top-to-bottom order — PLANNING (LifecyclePlanning), BUILDING (LifecycleInProgress, with inline status badge: error/waiting/finished/normal), REVIEWING (LifecycleReadyForReview + LifecycleInReview, the latter tagged with `(reviewing)`), and SHIPPING (LifecycleShipping, with PR/CI state phrase: `Ready` / `CI N/M failing` / `Changes requested` / `Conflicts` / `Waiting on CI`). New sessions land in PLANNING by default; `b` advances the cursor-selected planning session to BUILDING, `m` advances a finished Building session to ReadyForReview, `r` opens the review panel, `p` in the review panel opens the PR and transitions to Shipping, and the PR poller transitions Shipping → Complete on merge (Complete sessions disappear from the dashboard). Navigation across sections is tracked by the `focusCursorSection` enum (`focusSectionPlanning`, `focusSectionBuilding`, `focusSectionReview`, `focusSectionShipping`) plus `focusSectionsInOrder()` for render-order traversal; `j`/`k` walk the cursor through all four sections, skipping empty ones. Workflow keys (`c` add agent, `t` shell, `e` IDE, `p` PR, `d` diff, `x` kill agent, `X` kill session, `o` open branch, `R` manage repos, `a` add repo, `s` settings) act on the cursor-selected session in any section. `focusLaunch` is a separate `panelFocus` value used for the fullscreen per-agent terminal; `space`/`enter` on a Planning/Building row opens it, on a Reviewing row opens the review panel, on a Shipping row opens the **shipping panel** (`focusShipping`). The shipping panel (`internal/tui/shippingpanel.go`) shows PR title/base/mergeable state, per-check CI rows (icon + name + duration), and review threads grouped by reviewer. Keys in the shipping panel: `m` merge (squash by default, gated on `isMergeReady`); `M` force-merge; `r` synthesize a prompt from failing checks + `CHANGES_REQUESTED` threads and spawn a new agent in the existing worktree (session returns to LifecycleInProgress, PR stays open); `p` open PR in browser; `t` agent terminal; `esc` back. `mergePRCmd` calls `github.Client.MergePR` and emits `mergePRMsg`; success immediately transitions to LifecycleComplete. `buildFeedbackPrompt` assembles the `r` prompt from `prCacheEntry.checks.Runs` (failed runs with URLs) and `prCacheEntry.threads` (CHANGES_REQUESTED + COMMENTED). `MergeMethod` is configurable per-repo (`"squash"` | `"merge"` | `"rebase"`, default `"squash"`). `esc` returns to the pipeline. Key state fields on the App: `focusPlanningIdx int`, `focusBuildingIdx int`, `focusReviewIdx int`, `focusShippingIdx int`, `focusCursorSection focusSection`, `focusLaunchAgent *agent.Agent`, `shippingSession *agent.Session`. Helpers `focusSectionIdx(s)` and `focusSectionItems(s)` keep nav/clamp/hit-test logic out of fan-out switches. Mouse: click on a row moves the cursor; double-click activates (matches `space`/`enter`); PR-indicator clicks on Reviewing or Shipping rows open the PR. The pre-pipeline split-panel layout, `f` toggle, and `focus_mode_enabled` config key have been removed; `focus_mode_enabled` is silently ignored if present in older config files. Wellness features: audio chimes are suppressed for non-waiting status changes; a session timer tracks elapsed time against `FocusSessionMinutes` and triggers an automatic break overlay when the limit is reached; every session block is logged to `.refrain/logs/wellness.log`. A soft session limit (`MaxConcurrentSessions`, default 3) warns when exceeded — the count tracks active sessions (excluding Shipping and Complete phases, which are parked on CI/reviews) rather than individual agents, since a session is the unit of developer attention. Double-pressing `n` overrides the warning and spawns anyway. Configuration fields: `FocusSessionMinutes int`, `FocusBreakMinutes int`, `MaxConcurrentSessions int`, `MaxReviewBacklog int`, `MergeMethod string`.

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
- `.refrain/` is gitignored (auto-added to the repo's `.gitignore` on first run).
- Errors display briefly in the TUI and clear on next tick; no modal error dialogs.
- Claude hook CLI (`refrain hook`) is silent on stdout and always exits 0 — Claude interprets stdout as hook feedback.
- Changelog: every PR should add a fragment file under `changelog.d/` (e.g. `changelog.d/fix-login-redirect.md`) using `### Added`, `### Fixed`, etc. section headers. A release script assembles fragments into `CHANGELOG.md` when a version is cut.

## Dashboard Development Philosophy

**The 3-agent ceiling** — BCG research shows oversight cost exceeds output value beyond ~3 concurrent agents. Design around this: use soft limits, surface warnings early, never silently spawn past the cap. If a feature increases the viable agent count without reducing monitoring burden, it's the wrong feature.

**Batch over stream** — Cognitive resource depletion research supports reviewing agent output at completion, not continuously. Prioritize features that support "check in when done" workflows (push notification, status-on-return summary) over features that require constant attention. Interrupt-driven review is the goal; polling is the anti-pattern.

**Signal noise budget** — Habituation is real. Every ambient status update trains the developer to ignore the sidebar. Persistent state changes (idle→done, waiting for input, error) are worth surfacing; running-normally is not. Resist adding new status indicators that fire continuously — the signal budget is finite and already largely spent.

**The oversight tax** — METR's 2025 RCT found a 19% productivity slowdown for experienced developers using AI tools, driven by verification overhead. Refrain's diff view, review queue, and confidence-surfacing features directly reduce this tax. Prioritize these over raw throughput features; throughput without trust is just more work.

**The 90-minute cycle** — The BCG model (1 primary goal + max 3 agents, 90-minute block, defined review point) is the UX target. Design features so this workflow is the path of least resistance, not an advanced option. A feature that only makes sense if you run 8 agents for 4 hours is misaligned with the product.

**Plan richness over plan brevity** — High-quality plans for coding agents (Plan-and-Solve / TDAD / Superpowers / OpenSpec consensus) include explicit acceptance criteria, file:line targeting, type signatures, reuse audits, and per-task test-first flows. These traits compound downstream: a complete plan reduces the verification tax during diff review, because the human's job shifts from "verify the agent's judgment call by call" (slow, distributed across the diff) to "verify the plan was executed" (fast, concentrated at planning time). The wellness trade-off is plan length, which is real but bounded by writing discipline — Spec/Goal/task-name density determines the human's scan cost; per-task sub-bullets are executable spec for the building agent and don't have to be read line-by-line. If the plan editor's cognitive load grows past comfort in practice, the right fix is a UI affordance (section folding, summary-vs-detail panes) — not a return to terse plans that just push verification cost onto diff review.

**Wellbeing is the differentiator** — The drain after managing 5–8 agents is real and measurable. Features that reduce this drain (the pipeline view, wellness timer, attention routing) are Refrain's moat. Don't sacrifice them for features that increase raw agent throughput. If a proposed feature makes Refrain faster but more exhausting, it belongs in a different tool.
