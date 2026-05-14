# Changelog

All notable changes to Refrain will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [0.1.0] — 2026-04-18

Initial public release.

### Added

- Dashboard view listing all managed repos, sessions, and agents with live status (idle/active/waiting/done/error) and visual-stability detection.
- Focus mode: interactive preview of a selected agent with keys forwarded to the PTY, keyboard scrollback (`pgup` / `pgdn` / `home`), and native click-and-drag text selection in the host terminal.
- Diff view: summary list sorted by change magnitude, plus side-by-side (≥120 cols) or unified (below) detail view.
- Merge: `git merge --no-ff` from a worktree branch into the base branch with cleanup of the worktree.
- Prompt and merge overlays for creating and landing agent work without leaving the TUI.
- `refrain doctor` for validating git, `claude` on PATH (with `--settings` support), the refrain binary, hook-pipeline round-trip, git repo, and GitHub auth.
- Hook pipeline: per-session settings file wires Claude Code's hooks (`session-start`, `stop`, `session-end`, `notification`, `user-prompt-submit`) to `refrain hook <event>`, routed back to the running TUI over a unix socket for hook-driven status detection.
- GitHub integration: PR creation, checks/review polling, and a "fix failing checks" flow (`f`) that fetches failed check logs and dispatches them to an idle agent.
- IDE editor dropdown in global and per-repo settings. On macOS, probes `/Applications` and `~/Applications` for a curated list of editors (Zed, VS Code, Cursor, JetBrains IDEs) and generates `open -a "<App>"` invocations that take focus and support opening additional worktrees alongside an already-running editor window. Custom Command option preserves free-text entry.
- Shell agent: open a shell in the selected session's worktree without leaving the TUI (`t`).
- Branch picker: start a session on an existing branch or PR (`o`).
- Global and per-repo settings persisted on disk (`AgentProgram`, `BypassPermissions`, `IDECommand`, etc.).
- Session persistence: `q` detaches cleanly; a later `refrain` invocation reattaches to preserved worktrees.
- Mouse support on the dashboard (click-to-select, click-to-focus).
- Isolated git worktrees under `.refrain/worktrees/<name>` on branches `refrain/<name>`, with `.refrain/` auto-added to the repo's `.gitignore` on first run.
- Virtual terminal bridge built on `charmbracelet/x/vt` for thread-safe rendering of agent output.
- Optional audio chimes for status transitions.

[0.1.0]: https://github.com/devenjarvis/refrain/releases/tag/v0.1.0
