# Refrain

A terminal-native agent development environment (ADE) for [Claude Code](https://docs.anthropic.com/en/docs/claude-code): a raw Claude experience at the center, with parallel worktree sessions, plan drafting, code review, and PR shipping available as actions you invoke — not phases you are marched through.

> **Alpha software — v0.1.0 shipped.** The core loop is working and stabilizing, but rough edges remain. APIs, config schema, and keybindings may change between versions. Git operations are conservative — worktree sessions only write to `refrain/*` branches inside `.refrain/worktrees/` — but keep your work committed and file issues when things break.

## Install

**Homebrew (recommended):**

```bash
brew install devenjarvis/tap/refrain
```

**Go install:**

```bash
go install github.com/devenjarvis/refrain@latest
```

**Build from source:**

```bash
git clone https://github.com/devenjarvis/refrain.git
cd refrain
go build -o refrain .
```

## Requirements

- **Platforms:** macOS (amd64, arm64) and Linux (amd64, arm64). Windows is not currently supported.
- Git 2.20+
- [Claude Code CLI](https://docs.anthropic.com/en/docs/claude-code) (`claude` on PATH, with `--settings` support for hook integration)
- Optional: `gh` CLI or `GITHUB_TOKEN` for PR creation and checks polling

**Platform notes:**
- Audio chimes work on macOS (`afplay`) and Linux (`paplay` or `aplay`). Silent on other platforms.
- IDE detection auto-discovers editors on macOS (`.app` bundles) and Linux (PATH lookup). The `IDECommand` setting works everywhere.

Verify your environment:

```bash
refrain doctor
```

`doctor` checks git, Claude Code, the refrain binary, hook-pipeline round-trip, and GitHub auth.

## Why Refrain?

A developer's day with Claude is more than shipping feature branches: debugging in a half-broken checkout, exploring an unfamiliar codebase, drafting a ticket, reviewing someone else's PR, or just talking through a problem. Refrain gives all of that one calm home — a session list — and keeps the two things that make parallel agent work sustainable:

- **Isolated worktrees by default.** Coding sessions get their own git worktree on a `refrain/*` branch, so agents never collide with each other or with your checkout. When you *want* Claude in your real working tree — debugging your half-broken state — a **checkout session** runs there instead, clearly tagged so you always know an agent is loose in your tree.
- **Attention by signal, not by streaming.** The unix-socket hook pipeline classifies every agent as idle / active / waiting / done / error, and the list surfaces persistent state changes (waiting for input, errored, done) rather than a firehose of output. Batch review — check in when something needs you — is the workflow, and the signal budget is deliberately spent sparingly: rows never reorder themselves, chimes fire only for blocked agents, and running-normally is silent.

On top of that substrate, the heavier machinery is à-la-carte: `P` drafts an eight-section implementation plan with a read-only planning subprocess, `r` opens a task-card review ledger with per-task diffs and AI verdicts, `p` composes and tracks a PR through CI, review threads, and merge. Use all of it, some of it, or none of it — a blank REPL session is one keypress.

## Quick Start

```bash
refrain                         # inside any git repo
```

The first run registers the repo and adds `.refrain/` to `.gitignore`. From there:

1. `n` — open the new-session screen. Type a task (or nothing, for a blank Claude REPL) and press `⏎`. The session spawns in a fresh worktree and opens its terminal.
   - Choose **Context: Current checkout** in the form to run in your real working tree instead (one checkout session per repo).
   - Press `ctrl+p` instead of `⏎` to draft a plan first — a read-only Sonnet subprocess writes `.claude/plan.md`, you review/edit it in the plan editor, and approving spawns the build agent.
2. `esc` — return to the session list. Claude keeps running; the row's status glyph tells you when it needs you.
3. `r` — review any session with commits or uncommitted changes: task cards (from the plan, per-commit, or per-file), per-card diffs, AI verdicts, and a rework loop that spawns a feedback agent.
4. `p` — push and compose a PR, or open the PR panel once one exists: CI checks, review threads, merge (`m`), or spawn an agent to address feedback (`r`).
5. When a PR merges, the row shows a `merged` badge — press `X` to clean the session up. Nothing disappears on its own.

## Keybindings

**Session list** (the home screen):

| Key              | Action                                                          |
|------------------|-----------------------------------------------------------------|
| `j` / `k`        | Move the cursor through the list                                |
| `⏎` / `space`    | Open the session's fullscreen agent terminal                    |
| `n`              | New session (worktree or checkout; raw, blank, or plan-first)   |
| `N`              | Cycle to the next registered repo                               |
| `P`              | Plan: draft one if none exists, else open the plan editor       |
| `r`              | Open the review panel                                           |
| `p`              | PR: compose one if none exists, else open the PR panel          |
| `c`              | Add another agent to the cursor-selected session                |
| `t`              | Open or focus a shell in the cursor-selected session            |
| `d`              | Diff the cursor-selected session's worktree                     |
| `e`              | Open the worktree in the configured IDE                         |
| `o`              | Create a session on an existing branch or PR                    |
| `R`              | Manage registered repos                                         |
| `a`              | Add a repo (file browser)                                       |
| `s`              | Global settings                                                 |
| `x`              | Kill the cursor-selected session's primary agent                |
| `X`              | Kill the entire cursor-selected session                         |
| `q`              | Detach and exit (prompts if agents are running)                 |

Mouse: single-click on a session card moves the cursor; double-click opens the terminal. Clicking a row's PR indicator opens the PR in the browser.

**New-session screen** (`n`):

| Key        | Action                                                        |
|------------|---------------------------------------------------------------|
| `⏎`        | Start the session (empty prompt = blank Claude REPL)          |
| `ctrl+p`   | Draft a plan first, then review it before building            |
| `tab`      | Move to the Context / overrides form (worktree vs. checkout)  |
| `ctrl+j`   | Insert a newline in the prompt                                |
| `esc`      | Cancel                                                        |

**Review panel** (`r`):

| Key             | Action                                                    |
|-----------------|-----------------------------------------------------------|
| `j` / `k`       | Move the task-card cursor                                 |
| `⏎`             | Maximize the selected card's diff                         |
| `s`             | Toggle side-by-side diff                                  |
| `[` / `]`       | Cycle files within a multi-file card                      |
| `f`             | Flag the card for rework                                  |
| `b`             | Rework: spawn a feedback agent from flagged cards         |
| `m`             | Approve: mark the session done and tear it down           |
| `d`             | Defer: close the panel, come back later                   |
| `p`             | Ship: open the PR, or push + compose one                  |
| `r`             | Rerun validation checks                                   |
| `?`             | Spec overlay (plan-backed sessions only)                  |
| `e` / `t`       | Open IDE / agent terminal                                 |
| `esc`           | Back to the list                                          |

**PR panel** (`p` on a session with a PR, `esc` returns):

| Key  | Action                                                                             |
|------|------------------------------------------------------------------------------------|
| `m`  | Merge the PR (squash by default; gated on CI green + approved review)              |
| `M`  | Force merge (bypasses the merge-ready gate)                                        |
| `r`  | Address feedback: synthesize prompt from failing checks + review comments, spawn agent |
| `p`  | Open the PR in the browser                                                         |
| `t`  | Open the session's agent terminal                                                  |
| `esc`| Return to the session list                                                         |

**Agent terminal** (opened by pressing `⏎` on a session, `esc` returns):

| Key              | Action                                          |
|------------------|-------------------------------------------------|
| `esc`            | Return to the session list                      |
| `shift+esc`      | Send ESC to the agent (e.g. Claude interrupt)   |
| `alt+[` / `alt+]`| Switch between agents in the same session       |
| `ctrl+t`         | Add a shell to this session                     |
| `ctrl+n`         | Add a new agent to this session                 |
| `ctrl+w`         | Close the current tab                           |
| `pgup` / `pgdn`  | Scroll backward / forward                       |
| `home`           | Jump back to live output                        |
| *drag*           | Native terminal text selection                  |
| *other keys*     | Forwarded to the agent                          |

**Diff summary:**

| Key       | Action            |
|-----------|-------------------|
| `j` / `k` | Navigate files    |
| `⏎`       | Open file detail  |
| `g` / `G` | Top / bottom      |
| `q`       | Back to the list  |

**Diff detail:**

| Key       | Action            |
|-----------|-------------------|
| `j` / `k` | Scroll            |
| `d` / `u` | Page down / up    |
| `esc`     | Back to summary   |
| `q`       | Back to the list  |

## Session kinds

- **Worktree sessions (default)** get an isolated git worktree at `.refrain/worktrees/<name>` on a `refrain/*` branch. This is what makes running several coding agents in parallel safe.
- **Checkout sessions** run in the repo's main working tree, on your current branch — for debugging, exploration, and everyday tasks that need to see your real state. One per repo; the row carries a distinct `checkout @ <branch>` tag and accent so it's always obvious an agent is working in your tree. Killing a checkout session kills its agents only — the tree is never cleaned up.

## Branch naming

New worktree sessions start on a random adjective-noun branch (e.g. `refrain/warm-ibis`) so Claude can launch immediately. On the first real `user-prompt-submit`, the branch is renamed in place — `git branch -m` atomically updates the worktree's HEAD symref — to a slug of the prompt, e.g. `refrain/add-dark-mode-to-dashboard`. Slash commands (`/clear`, `/help`) are skipped, so the next real prompt still triggers the rename. Sessions started on an existing branch (`o`) and checkout sessions keep their branch as-is.

The prefix is configurable via `BranchPrefix` in global or per-repo settings, and supports two template variables:

- `{user}` — slugified `git config user.name` (falls back to `$USER`)
- `{date}` — today's date in `YYYY-MM-DD`

Unknown `{tokens}` are left literal. Example: `BranchPrefix: "{user}/"` produces `dj/add-dark-mode` after the first-prompt rename.

## How It Works

When you create a worktree session, Refrain:

1. Creates an isolated git worktree at `.refrain/worktrees/<name>` on branch `refrain/<name>` (checkout sessions skip this and run in the repo itself).
2. Writes a settings file wiring Claude Code's hooks (`session-start`, `stop`, `notification`, `user-prompt-submit`, `session-end`) to `refrain hook <event>` and points Claude at it with `claude --settings`.
3. Spawns `claude "<task>"` in a PTY inside the worktree.
4. Feeds PTY output through a virtual terminal emulator ([charmbracelet/x/vt](https://github.com/charmbracelet/x/vt)) and renders it via [Bubble Tea v2](https://github.com/charmbracelet/bubbletea).
5. Listens on a per-process unix socket for hook events so the TUI can distinguish idle / active / waiting / done states without screen-scraping.

`q` detaches — sessions and worktrees persist, and the next `refrain` invocation reattaches to them via `claude --resume`.

## What's Coming

- Support for agents beyond Claude Code (any CLI that accepts a prompt and produces output)
- Richer merge and conflict resolution flows
- Better multi-repo session management

## Development

Refrain is Go 1.25, single-binary. Common loop:

```bash
go build -o refrain .         # build
go test -race ./...         # run unit tests with the race detector (required before committing)
go vet ./...                # static analysis
golangci-lint run           # lint (config in .golangci.yml)
gofmt -w .                  # format
./refrain doctor              # validate environment + hook pipeline round-trip
```

End-to-end TUI tests live under `internal/e2e/` behind the `e2e` build tag (needs [`tu`](https://github.com/charmbracelet/x/tree/main/tu) v0.6.0+):

```bash
go test -tags e2e -timeout 300s -v ./internal/e2e/
```

Every PR should drop a fragment under `changelog.d/` (e.g. `changelog.d/fix-login-redirect.md`) using `### Added` / `### Fixed` / `### Changed` / `### Removed` headers — the release script assembles `CHANGELOG.md` from those when a version is cut.

For architecture, internal package layout, and the design philosophy behind the session list, see [`CLAUDE.md`](./CLAUDE.md).

## Contributing

See [CONTRIBUTING.md](./CONTRIBUTING.md). Bug reports and focused PRs are welcome; because Refrain is a single-maintainer alpha, larger feature proposals should start as an issue. Proposals that flood the session list with continuously-firing status indicators are unlikely to land — see the design philosophy in [CLAUDE.md](./CLAUDE.md).

## Security

See [SECURITY.md](./SECURITY.md) for how to report vulnerabilities.

## License

MIT — see [LICENSE](./LICENSE).
