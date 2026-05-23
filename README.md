# Refrain

A terminal-native dashboard for running [Claude Code](https://docs.anthropic.com/en/docs/claude-code) agents in parallel — designed around how many agents a human can actually oversee, not how many you can launch.

> **Alpha software — v0.1.0 shipped.** The core loop is working and stabilizing, but rough edges remain. APIs, config schema, and keybindings may change between versions. Git operations are conservative — Refrain only writes to `refrain/*` branches inside `.refrain/worktrees/` and uses `git merge --no-ff` with explicit confirmation — but keep your work committed and file issues when things break.

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

Most parallel-agent tools optimize for throughput — how many agents you can run at once. Refrain optimizes for the human running them. The whole product is built around a single, opinionated workflow: **one primary goal, up to three agents, a defined review point, and a 90-minute block.**

That opinion is grounded in evidence. BCG's 2026 study (n=1,488) found developers managing more than ~3 concurrent agents made 39% more errors and reported 33% more decision fatigue. METR's 2025 RCT measured a 19% productivity *slowdown* among experienced developers using AI tools, driven almost entirely by verification overhead. And every unnecessary context switch costs ~9.5 minutes of recovery time. The instinct to spawn another agent is almost always right; the instinct to monitor all of them simultaneously is what drains the day.

Refrain turns that into a workflow:

- **One pipeline, one cursor.** A four-section dashboard — PLANNING → BUILDING → REVIEWING → SHIPPING — replaces tmux pane-juggling and tab-switching. `j`/`k` walk every section; everything else acts on whatever the cursor is on.
- **Isolated git worktrees per session.** Each agent works on `refrain/<name>` under `.refrain/worktrees/`. Branches are conservative, merges are explicit (`git merge --no-ff` with confirmation), and your main checkout is never touched.
- **Batch review, not continuous monitoring.** Hook-driven status (idle / active / waiting / done / error) means Refrain tells you when an agent needs you. You don't watch streams — you check in when there's something to check.
- **Wellness baked in, not bolted on.** A soft 3-agent cap, an automatic break overlay at 90 minutes, suppressed chimes for routine state changes, and a `.refrain/logs/wellness.log` of every block. These aren't toggles — they're the product.

If you want to run 8 agents for 4 hours straight, Refrain is the wrong tool. If you want to actually finish three things in a focused block and ship them, keep reading.

## Quick Start

```bash
refrain                         # inside any git repo
```

The first run registers the repo and adds `.refrain/` to `.gitignore`. From there:

1. `n` — create a session. It lands in PLANNING; the cursor jumps to it and opens its terminal so you can scope the work with Claude.
2. `b` — promote the planning session to BUILDING when you've nailed down what to do.
3. `esc` — return to the pipeline. Claude keeps running.
4. `m` — mark a building session ready when Claude finishes its turn (it moves to REVIEWING).
5. `r` — open the review panel; press `p` there to ship a PR (the session moves to SHIPPING).
6. `⏎` on a SHIPPING row — open the shipping panel: see CI check results and review threads, then `m` to merge (squash by default), `r` to address feedback with a new agent, or `p` to open the PR in the browser. Worktree is cleaned up automatically on merge.

No tmux. No tab-switching. One cursor.

## Keybindings

**Pipeline view** (the dashboard — the only top-level view):

| Key              | Action                                                                   |
|------------------|--------------------------------------------------------------------------|
| `j` / `k`        | Move the cursor across all four pipeline sections                        |
| `⏎` / `space`    | Open the cursor-selected row (terminal, review panel, or shipping panel) |
| `n`              | Create a new session (lands in PLANNING)                                 |
| `N`              | Cycle to the next registered repo                                        |
| `b`              | On a PLANNING row: advance to BUILDING. Anywhere else: take a break      |
| `m`              | Mark the cursor-selected BUILDING row ready for review                   |
| `r`              | Open the review panel for the cursor-selected REVIEWING row              |
| `c`              | Add another agent to the cursor-selected session                         |
| `t`              | Open or focus a shell in the cursor-selected session                     |
| `d`              | Diff the cursor-selected session's worktree                              |
| `e`              | Open the worktree in the configured IDE                                  |
| `p`              | Open the session's PR in the browser                                     |
| `o`              | Create a session on an existing branch or PR                             |
| `a`              | Add a repo (file browser)                                                |
| `s`              | Global settings                                                          |
| `x`              | Kill the cursor-selected session's primary agent                         |
| `X`              | Kill the entire cursor-selected session                                  |
| `q`              | Detach and exit (prompts if agents are running)                          |

Mouse: single-click on a session card moves the cursor; double-click activates (agent terminal for PLANNING / BUILDING, review panel for REVIEWING, shipping panel for SHIPPING). Clicking the PR indicator on a REVIEWING or SHIPPING row opens the PR in the browser.

**Shipping panel** (opened by pressing `⏎` on a SHIPPING row, `esc` returns):

| Key  | Action                                                                             |
|------|------------------------------------------------------------------------------------|
| `m`  | Merge the PR (squash by default; gated on CI green + approved review)              |
| `M`  | Force merge (bypasses the merge-ready gate)                                        |
| `r`  | Address feedback: synthesize prompt from failing checks + review comments, spawn agent |
| `p`  | Open the PR in the browser                                                         |
| `t`  | Open the session's agent terminal                                                  |
| `esc`| Return to the pipeline                                                             |

**Agent terminal** (opened by pressing `⏎` on a session, `esc` returns):

| Key              | Action                                          |
|------------------|-------------------------------------------------|
| `esc`            | Return to the pipeline                          |
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
| `q`       | Back to dashboard |

**Diff detail:**

| Key       | Action            |
|-----------|-------------------|
| `j` / `k` | Scroll            |
| `d` / `u` | Page down / up    |
| `esc`     | Back to summary   |
| `q`       | Back to dashboard |

## Branch naming

New sessions start on a random adjective-noun branch (e.g. `refrain/warm-ibis`) so Claude can launch immediately. On the first real `user-prompt-submit`, the branch is renamed in place — `git branch -m` atomically updates the worktree's HEAD symref — to a slug of the prompt, e.g. `refrain/add-dark-mode-to-dashboard`. Slash commands (`/clear`, `/help`) are skipped, so the next real prompt still triggers the rename. Sessions started on an existing branch (`o`) keep that branch as-is.

The prefix is configurable via `BranchPrefix` in global or per-repo settings, and supports two template variables:

- `{user}` — slugified `git config user.name` (falls back to `$USER`)
- `{date}` — today's date in `YYYY-MM-DD`

Unknown `{tokens}` are left literal. Example: `BranchPrefix: "{user}/"` produces `dj/add-dark-mode` after the first-prompt rename.

## Wellness controls

The dashboard surfaces three wellness affordances tuned to keep parallel-agent work sustainable:

- **Session timer** (`focus_session_minutes`, default `90`) — when the configured block elapses, Refrain automatically opens a centered break overlay with a coherent-breathing animation.
- **Soft agent limit** (`max_concurrent_agents`, default `3`) — pressing `n` past the cap shows a one-key warning; pressing `n` a second time overrides and spawns anyway.
- **Soft review backlog** (`max_review_backlog`, default `5`) — same two-press override pattern when the REVIEWING section has too many sessions waiting.

Every block (work + break) is appended to `.refrain/logs/wellness.log` so you can audit your own pacing later.

## How It Works

When you create a session, Refrain:

1. Creates an isolated git worktree at `.refrain/worktrees/<name>` on branch `refrain/<name>`.
2. Writes a settings file wiring Claude Code's hooks (`session-start`, `stop`, `notification`, `user-prompt-submit`, `session-end`) to `refrain hook <event>` and points Claude at it with `claude --settings`.
3. Spawns `claude "<task>"` in a PTY inside the worktree.
4. Feeds PTY output through a virtual terminal emulator ([charmbracelet/x/vt](https://github.com/charmbracelet/x/vt)) and renders it in the dashboard via [Bubble Tea v2](https://github.com/charmbracelet/bubbletea).
5. Listens on a per-process unix socket for hook events so the TUI can distinguish idle / active / waiting / done states without screen-scraping.

When you merge, Refrain runs `git merge --no-ff` from the worktree branch into the session's base branch and cleans up the worktree.

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
gofumpt -w .                # format
./refrain doctor              # validate environment + hook pipeline round-trip
```

End-to-end TUI tests live under `internal/e2e/` behind the `e2e` build tag (needs [`tu`](https://github.com/charmbracelet/x/tree/main/tu) v0.6.0+):

```bash
go test -tags e2e -timeout 300s -v ./internal/e2e/
```

Every PR should drop a fragment under `changelog.d/` (e.g. `changelog.d/fix-login-redirect.md`) using `### Added` / `### Fixed` / `### Changed` / `### Removed` headers — the release script assembles `CHANGELOG.md` from those when a version is cut.

For architecture, internal package layout, and the design philosophy behind the dashboard, see [`CLAUDE.md`](./CLAUDE.md).

## Contributing

See [CONTRIBUTING.md](./CONTRIBUTING.md). Bug reports and focused PRs are welcome; because Refrain is a single-maintainer alpha, larger feature proposals should start as an issue. Proposals that increase raw parallel-agent throughput at the cost of monitoring burden are unlikely to land — see the design philosophy in [CLAUDE.md](./CLAUDE.md).

## Security

See [SECURITY.md](./SECURITY.md) for how to report vulnerabilities.

## License

MIT — see [LICENSE](./LICENSE).
