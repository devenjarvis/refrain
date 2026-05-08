# Baton

A lightweight, TUI-first interface for running multiple [Claude Code](https://docs.anthropic.com/en/docs/claude-code) agents in parallel.

<!-- TODO: record demo GIF and replace placeholder -->
![Baton demo](docs/demo.gif)

> **Alpha software — v0.1.0 shipped.** The core loop is working and stabilizing, but rough edges remain. APIs, config schema, and keybindings may change between versions. Git operations are conservative — Baton only writes to `baton/*` branches inside `.baton/worktrees/` and uses `git merge --no-ff` with explicit confirmation — but keep your work committed and file issues when things break.

## Install

**Homebrew (recommended):**

```bash
brew install devenjarvis/tap/baton
```

**Go install:**

```bash
go install github.com/devenjarvis/baton@latest
```

**Build from source:**

```bash
git clone https://github.com/devenjarvis/baton.git
cd baton
go build -o baton .
```

## Requirements

- Git 2.20+
- [Claude Code CLI](https://docs.anthropic.com/en/docs/claude-code) (`claude` on PATH, with `--settings` support for hook integration)
- Optional: `gh` CLI or `GITHUB_TOKEN` for PR creation and checks polling

Verify your environment:

```bash
baton doctor
```

`doctor` checks git, Claude Code, the baton binary, hook-pipeline round-trip, and GitHub auth.

## Why Baton?

If you use Claude Code regularly, you've probably wanted to run multiple tasks at once without juggling terminal windows. Baton gives you a single dashboard to manage parallel agents — each in its own isolated git worktree — and surfaces output, diffs, and status all in one place.

**The core loop:**

1. Run `baton` inside a git repo
2. Press `n` to create a session and give Claude a task
3. Watch the agent work; press `⏎` to drop into the agent terminal, `esc` to return
4. Press `m` to mark the session ready when Claude finishes its turn
5. Press `r` to review and ship

No tmux required. No context switching.

Running three or more parallel agents creates real cognitive overhead that's easy to underestimate. BCG research (2026) found that developers managing more than roughly three concurrent agents experienced 33% more decision fatigue and made 39% more errors — a "brain fry" effect that compounds quickly as you add more threads. The instinct to spin up another agent is almost always right; the instinct to monitor all of them simultaneously is not.

The design targets behind Baton come from two well-established findings: context-switch recovery takes on average 9.5 minutes, and deep work cycles around a 90-minute block. Every unnecessary interrupt — a glance at a stuck agent, a quick tab-switch to check output — drains from those budgets in ways that don't show up until you're staring at your screen at 4 pm wondering where the day went.

Baton's dashboard is the answer: a pipeline view with one cursor that moves across SESSIONS (in-progress) and a REVIEW QUEUE (ready to merge). You work in flow; Baton does the monitoring.

## Usage

Run `baton` inside a git repository:

```bash
baton
```

The first run auto-registers the current directory and adds `.baton/` to `.gitignore`. Additional repos can be added from the TUI (`a`).

### Keybindings

**Pipeline view** (the dashboard):

| Key              | Action                                                     |
|------------------|------------------------------------------------------------|
| `j` / `k`        | Move the cursor across SESSIONS and REVIEW QUEUE rows      |
| `⏎` / `space`    | Open the cursor-selected session's agent in focusLaunch    |
| `n`              | Create a new session                                       |
| `N`              | Cycle to the next registered repo                          |
| `m`              | Mark the cursor-selected SESSIONS row ready for review     |
| `r`              | Open the review panel for the cursor-selected REVIEW row   |
| `c`              | Add another agent to the cursor-selected session           |
| `t`              | Open or focus a shell in the cursor-selected session       |
| `d`              | Diff the cursor-selected session's worktree                |
| `e`              | Open the worktree in the configured IDE                    |
| `p`              | Open the session's PR in the browser                       |
| `o`              | Create a session on an existing branch or PR               |
| `a`              | Add a repo (file browser)                                  |
| `s`              | Global settings                                            |
| `x`              | Kill the cursor-selected session's primary agent           |
| `X`              | Kill the entire cursor-selected session                    |
| `b`              | Take a break (engages the wellness break overlay)          |
| `q`              | Detach and exit (prompts if agents are running)            |

**Agent terminal** (focusLaunch — opened by pressing `⏎` on a session):

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

Click support on the dashboard: single-click on a session card moves the cursor; double-click activates (focusLaunch for an active session, review panel for a queue row). Clicking the PR indicator on a review row opens the PR in the browser.

### Branch naming

New sessions start on a random adjective-noun branch (e.g. `baton/warm-ibis`) so Claude can launch immediately. On the first real `user-prompt-submit`, the branch is renamed in place — `git branch -m` atomically updates the worktree's HEAD symref — to a slug of the prompt, e.g. `baton/add-dark-mode-to-dashboard`. Slash commands (`/clear`, `/help`) are skipped, so the next real prompt still triggers the rename. Sessions started on an existing branch (`o`) keep that branch as-is.

The prefix is configurable via `BranchPrefix` in global or per-repo settings, and supports two template variables:

- `{user}` — slugified `git config user.name` (falls back to `$USER`)
- `{date}` — today's date in `YYYY-MM-DD`

Unknown `{tokens}` are left literal. Example: `BranchPrefix: "{user}/"` produces `dj/add-dark-mode` after the first-prompt rename.

### Wellness controls

The dashboard surfaces three wellness affordances tuned to keep parallel-agent work sustainable:

- **Session timer** (`focus_session_minutes`, default `90`) — when the configured block elapses, Baton automatically opens a centered break overlay with a coherent-breathing animation.
- **Soft agent limit** (`max_concurrent_agents`, default `3`) — pressing `n` past the cap shows a one-key warning; pressing `n` a second time overrides and spawns anyway.
- **Soft review backlog** (`max_review_backlog`, default `5`) — same two-press override pattern when the REVIEW QUEUE has too many sessions waiting.

Every block (work + break) is appended to `.baton/logs/wellness.log` so you can audit your own pacing later.

## How It Works

When you create a session, Baton:

1. Creates an isolated git worktree at `.baton/worktrees/<name>` on branch `baton/<name>`.
2. Writes a settings file wiring Claude Code's hooks (`session-start`, `stop`, `notification`, `user-prompt-submit`, `session-end`) to `baton hook <event>` and points Claude at it with `claude --settings`.
3. Spawns `claude "<task>"` in a PTY inside the worktree.
4. Feeds PTY output through a virtual terminal emulator ([charmbracelet/x/vt](https://github.com/charmbracelet/x/vt)) and renders it in the dashboard via [Bubble Tea v2](https://github.com/charmbracelet/bubbletea).
5. Listens on a per-process unix socket for hook events so the TUI can distinguish idle / active / waiting / done states without screen-scraping.

When you merge, Baton runs `git merge --no-ff` from the worktree branch into the session's base branch and cleans up the worktree.

## What's Coming

- Support for agents beyond Claude Code (any CLI that accepts a prompt and produces output)
- Richer merge and conflict resolution flows
- Better multi-repo session management

## Contributing

See [CONTRIBUTING.md](./CONTRIBUTING.md). Bug reports and focused PRs are welcome; because Baton is a single-maintainer alpha, larger feature proposals should start as an issue.

## Security

See [SECURITY.md](./SECURITY.md) for how to report vulnerabilities.

## License

MIT — see [LICENSE](./LICENSE).
