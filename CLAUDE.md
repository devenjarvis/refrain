# CLAUDE.md

> **Architecture & code conventions:** see [`CONVENTIONS.md`](CONVENTIONS.md). This file describes *what the system does*; `CONVENTIONS.md` describes *how the code must be structured* (component architecture, message/render rules, layering). Read it before writing or changing code.
>
> **Design system / visual tokens:** see [`DESIGN.md`](DESIGN.md). Colors, glyphs, borders, spacing, and animation palettes live in one registry (`internal/tui/theme`). Never hardcode a hex or glyph rune — reference a token.

## Project

Refrain is a terminal-native ADE (agent development environment) for Claude Code: a raw Claude terminal experience at the center, with worktree parallelism, plan drafting, code review, and PR shipping as **actions you invoke** on a session — not phases a session is marched through. Written in Go 1.25.

The home screen is a repo-grouped session list with one cursor. A session is a named group of agents sharing a directory; what the UI knows about it is expressed by orthogonal signals — agent status (the attention signal), derived badges (plan, PR/CI state, merged, done), and session kind (worktree vs. checkout) — never by list placement. There are no lifecycle phases and no state machine; sessions stay in creation order and nothing leaves the list on its own. The design doc for this shape is `docs/superpowers/specs/2026-07-04-ade-rollback-design.md`.

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
- `internal/agent/` — Composes PTY + VT + git into managed agents. Each agent has 3 goroutines: readLoop (PTY→VT), writeLoop (VT→PTY), statusLoop (idle detection). Sessions group agents sharing a directory. Manager handles lifecycle and events.
- `internal/tui/` — Bubble Tea v2 views: session list (root screen), fullscreen agent terminal (focusLaunch), review panel, PR panel, plan editor, diff summary/detail, global/repo config forms, file browser (add repo), branch picker (session on existing branch/PR), statusbar.
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
- **Status detection**: visual-stability detection + hook-driven signals classify agents as idle / active / waiting / done / error. `StatusWaiting` is distinct (permission prompts, input blocks) and surfaces in the row's accent color. A session's displayed status is the max-severity aggregate of its non-shell agents.
- **Session kinds**: `KindWorktree` (default — isolated worktree on an owned `refrain/*` branch) vs. `KindCheckout` (runs in the repo's main working tree via `Manager.CreateSessionInDir`; at most one per repo, enforced with `ErrCheckoutSessionExists`). Checkout sessions never own their branch, suppress the Haiku branch rename, and `Session.Cleanup` is a guaranteed no-op on the tree — killing one kills agents only. The kind is persisted in `state.json` (`"kind"`; missing/empty means worktree).
- **Branch naming**: worktree sessions are created on a random adjective-noun branch under the configured `BranchPrefix` (default `refrain/`). On the first actionable `user-prompt-submit` hook (non-empty, not a bare slash command), the manager runs the configured `BranchNamer` in a goroutine tracked by `m.watchers`. `DefaultBranchNamer()` shells out to `claude -p --model claude-haiku-4-5` with a 15s timeout, piping a fully rendered instruction string to stdin: the resolved `BranchNamePrompt` (default in `config.DefaultBranchNamePrompt`) with the `{prompt}` token replaced by the user's prompt; if a custom template lacks `{prompt}`, the prompt is appended on a new paragraph rather than silently dropped. On success, `Session.RenameBranch` updates the HEAD symref atomically (Claude's cwd stays valid) and the session display name updates to the Haiku-derived suffix (e.g. `add-dark-mode`); agents keep their stable `Track N` identities and are never renamed. There is no slugify-from-prompt fallback: on error/empty/timeout the random branch persists and `Session.hasClaudeName` stays false so the next prompt retries. `Session.TryStartRename`/`finishRename` gate double-dispatch so a second prompt arriving mid-Haiku is a no-op. Tests inject a fake namer via `Manager.SetBranchNamer`. Sessions started on an existing branch via `CreateSessionOnBranch*` and checkout sessions set `hasClaudeName=true` at creation so they keep their original branch. `BranchPrefix` supports `{user}` (slugified `git config user.name` → `$USER` fallback) and `{date}` (`YYYY-MM-DD`) tokens, resolved in `config.ExpandBranchPrefix`.
- **New-session flow** (`n`, `internal/tui/newsession.go`): a full-viewport screen. `enter` spawns a raw Claude session with the prompt as its task; an empty prompt spawns a blank Claude REPL — the everyday case for debugging and exploring. `ctrl+p` runs the plan-first path (`CreateSessionNoAgent` + `StartDraft` → plan editor). The form's Context field selects `Worktree (new branch)` (default) or `Current checkout`; existing-branch/PR entry stays on `o` (branch picker → `AttachWorktree`).
- **Plan drafting** (the `P` action; drafts if no plan exists, opens the plan editor if one does): the planning Sonnet subprocess (`internal/agent/planner.go`) produces an eight-section markdown plan written to `<worktree>/.claude/plan.md` and consumed by *two readers at once* — the human reviewing in the plan editor, and the building agent later executing each task end-to-end via `BuildFromPlanPrompt`. Sections in order: `# Goal` (one sentence), `## Spec` (numbered acceptance criteria, one line each, ~12 max), `## Context` (file:line refs for the area touched), `## Reuse` (existing helpers to build on rather than recreate), `## Risks` (unknowns and load-bearing assumptions), `## Tasks` (test-first checklist), `## Verification` (concrete commands), `## Not in scope`. Per-task sub-bullets are labeled (`Files: ...`, `Signatures: ...`, `Test first: ...`, `Implement: ...`, `Verify: ...`) and use `  - ` indented bullets — NEVER `- [ ]`, since `ParsePlanTasks` (`internal/agent/session.go`) and `planTaskCounts` (`internal/tui/sessionlist_view.go`) count `- [ ]` / `- [x]` lines inside `## Tasks` only to track task progress. The building agent maps commits to tasks via `Plan-Task: N` trailers in the commit body (where N is the 1-based position of the checkbox inside `## Tasks`); stray checkboxes outside that section no longer shift commit indices, but should still be avoided for clarity. The prompt mandates verbosity in Spec/Reuse/Risks/sub-bullets because research (Plan-and-Solve, TDAD, Superpowers, OpenSpec) shows plan completeness — not brevity — predicts build-agent success. The matched UI obligation is keeping the human's scan path short: Goal one sentence, Spec items one line, task names imperative phrases, sub-bullets treated as reference material the developer may skip on first review. There is no word cap. The drafter runs with the read-only tool allowlist `Read,Grep,Glob,LS,LSP,WebFetch,WebSearch` and may call `ask_user` once via the MCP bridge if a load-bearing ambiguity blocks drafting. Plan approval in the editor spawns a build agent with `BuildFromPlanPrompt` into the same session — an action, not a transition.
- **Session list** (`internal/tui/sessionlist_{model,update,view}.go`): the root screen is a repo-grouped flat list of 2-line session cards under muted repo headers, with one plain `int` cursor over a flattened row slice. Line 1: display name, status glyph + word, badges (`plan`, `draft…`, PR indicator with the CI/review phrase `Ready` / `CI N/M failing` / `Changes requested` / `Conflicts` / `Waiting on CI`, `merged`). Line 2: branch, context tag (`worktree` | `checkout @ <branch>` — checkout gets a distinct accent), age, agent count. Attention (waiting/error, draft finished, PR actionable) renders as the row's accent stripe and **never reorders the list** — rows stay in creation order. Sessions never leave the list on their own: a merged PR shows a `merged` badge plus a "press X to clean up" hint. `enter`/`space` on any row opens focusLaunch, the fullscreen agent terminal — the terminal is the product; panels are actions. Mouse: click moves the cursor, double-click opens the terminal, PR-indicator click opens the PR.
- **À-la-carte actions** (all act on the cursor's session): `P` plan (draft-or-edit), `r` review panel, `p` PR (compose-or-panel), `d` diff, `c` add agent, `t` shell, `e` IDE, `o` open branch/PR, `R` manage repos, `a` add repo, `s` settings, `x`/`X` kill agent/session, `N` cycle repo. Bindings live in `KeyMap` (`internal/tui/keymap.go`).
- **Review panel** (`r`; `internal/tui/reviewpanel_*.go`): a single no-tab screen — header, optional inline checks strip, and a two-pane body at width ≥ 120 (task-card ledger left, embedded per-task diff viewport right; narrow terminals stack vertically). The ledger has a three-mode fallback chain (`fetchReviewDiffCmd`, `internal/tui/update_review.go`): (1) plan tasks via `ParsePlanTasks` + `GroupCommitsByTask` over `Plan-Task: N` trailers; (2) no plan but commits exist — one card per commit (capped on long branches with an aggregate card); (3) no commits — per-file cards from uncommitted changes. AI verdicts (`ReviewerAgent.Review`, all-strings) run per card in modes 1–2 and are disabled in mode 3 (uncommitted fragments produce noise; that mode is manual review). Keys: `j`/`k` task cursor, `enter` maximize diff, `s` side-by-side, `[`/`]` cycle files, `pgdn`/`pgup`/`ctrl+d`/`ctrl+u`/`g`/`G` scroll, `f` flag, `b` rework (synthesizes a feedback prompt from flagged cards and spawns a new agent in the session via `AddAgent`), `m` approve (MarkDone + kill session), `d` defer (just closes), `r` rerun validation checks, `?` spec overlay (plan-backed sessions only), `p` ship (open or create PR).
- **PR panel** (`internal/tui/prpanel_*.go`, opened via `p` when a PR exists): PR title/base/mergeable state, per-check CI rows (icon + name + duration), review threads grouped by reviewer. Keys: `m` merge (squash by default, gated on `isMergeReady`); `M` force-merge; `r` synthesize a prompt from failing checks + `CHANGES_REQUESTED` threads (`buildFeedbackPrompt` from `prCacheEntry.checks.Runs` and `prCacheEntry.threads`) and spawn a new agent in the existing worktree; `p` open PR in browser; `t` agent terminal; `esc` back. `mergePRCmd` calls `github.Client.MergePR`; merge (internal or external, detected by the poller) calls `sess.MarkDone()` and flips the row badge to `merged`. `MergeMethod` is configurable per-repo (`"squash"` | `"merge"` | `"rebase"`, default `"squash"`).
- **PR poller** (`internal/tui/update_pr.go`): polls adaptively per session and caches into `prCacheEntry`, keyed by `cacheKey(repoPath, sessionID)`. It **never mutates sessions** beyond `MarkDone` on merge — PR presence/state is a badge, not a phase. The review-thread fetch gate keys off "has an open PR".
- **Config**: legacy keys from removed features (`focus_mode_enabled`, `focus_session_minutes`, `focus_break_minutes`, `max_concurrent_sessions`, `max_review_backlog`, `plan_first_enabled`) are silently ignored by `encoding/json` when present in older settings files, as is `lifecyclePhase` in older `state.json` files.

## Testing

- `internal/pty/` — Echo, cat round-trip, close+done.
- `internal/vt/` — Write/render, ANSI preservation, resize, SendText/Read.
- `internal/git/` — Real git on temp repos.
- `internal/agent/` — Uses `bash -c` instead of `claude`.
- `internal/hook/` — Socket server unit tests, settings generator tests.
- `internal/config/` — Global + per-repo settings, resolution, migration (including silent-ignore of removed keys).
- `internal/tui/` — Mostly manual; `app_test.go`, `sessionlist_test.go`, and `idecommand_test.go` cover the testable pieces.
- `internal/e2e/` — End-to-end via `tu` CLI, `e2e` build tag.

## Conventions

- Mouse support: click-to-select on session cards; double-click opens the terminal; drag-to-select inside the focusLaunch agent terminal copies text. PR-indicator clicks open the PR in the browser.
- Agent program defaults to `claude` but is configurable via global/per-repo settings (`AgentProgram`).
- `.refrain/` is gitignored (auto-added to the repo's `.gitignore` on first run).
- Errors display briefly in the TUI and clear on next tick; no modal error dialogs.
- Claude hook CLI (`refrain hook`) is silent on stdout and always exits 0 — Claude interprets stdout as hook feedback.
- Changelog: every PR should add a fragment file under `changelog.d/` (e.g. `changelog.d/fix-login-redirect.md`) using `### Added`, `### Fixed`, etc. section headers. A release script assembles fragments into `CHANGELOG.md` when a version is cut.

## Design Philosophy

**Batch over stream** — The workflow Refrain optimizes for is "check in when something needs you", not continuous monitoring. Hook-driven status means the list can tell you an agent is waiting, errored, or done; you don't watch output streams. Prioritize features that support batch review (status-on-return, persistent badges) over features that require constant attention. Interrupt-driven review is the goal; polling is the anti-pattern.

**Signal noise budget** — Habituation is real. Every ambient status update trains the developer to ignore the list. Persistent state changes (idle→done, waiting for input, error, PR became actionable) are worth surfacing; running-normally is not. Attention never reorders the list — rows stay in creation order so the screen doesn't churn. Resist adding new status indicators that fire continuously — the signal budget is finite and already largely spent.

**The oversight tax** — METR's 2025 RCT found a 19% productivity slowdown for experienced developers using AI tools, driven by verification overhead. Refrain's diff view, review ledger, AI verdicts, and confidence-surfacing features directly reduce this tax. Prioritize these over raw throughput features; throughput without trust is just more work.

**Plan richness over plan brevity** — High-quality plans for coding agents (Plan-and-Solve / TDAD / Superpowers / OpenSpec consensus) include explicit acceptance criteria, file:line targeting, type signatures, reuse audits, and per-task test-first flows. These traits compound downstream: a complete plan reduces the verification tax during diff review, because the human's job shifts from "verify the agent's judgment call by call" (slow, distributed across the diff) to "verify the plan was executed" (fast, concentrated at planning time). The human's scan cost is bounded by writing discipline — Spec/Goal/task-name density determines it; per-task sub-bullets are executable spec for the building agent and don't have to be read line-by-line. If the plan editor's cognitive load grows past comfort in practice, the right fix is a UI affordance (section folding, summary-vs-detail panes) — not a return to terse plans that just push verification cost onto diff review.

**The terminal is the product** — focusLaunch is a near-complete raw Claude passthrough (full key forwarding, mouse, bracketed paste, scrollback, selection, clipboard, resize, alt-screen). Planning, review, and shipping are panels you open on demand and close. A feature that only makes sense as a mandatory stage in a pipeline is misaligned with the product.
