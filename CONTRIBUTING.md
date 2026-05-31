# Contributing to Refrain

Refrain is an early, single-maintainer project. Focused bug reports, fixes, and small features are the easiest kind of contribution to review.

## Filing issues

Search existing issues (open and closed) before opening a new one.

- **Bugs** — use the "Bug report" form. Include `refrain doctor` output, steps to reproduce, and expected vs. actual behavior.
- **Feature requests** — use the "Feature request" form. Describe the underlying problem, not just the proposal.
- **Security vulnerabilities** — do not file a public issue. See [SECURITY.md](./SECURITY.md).

## Development setup

Requirements:

- Go 1.25+
- Git 2.20+
- [Claude Code CLI](https://docs.anthropic.com/en/docs/claude-code) (`claude` on PATH) — runtime-only

```bash
git clone https://github.com/devenjarvis/refrain.git
cd refrain
go build -o refrain .
./refrain doctor
```

## Build, test, lint

```bash
go build -o refrain .         # build
go test ./...               # run all tests
go test -race ./...         # run with race detector
go vet ./...                # static analysis
golangci-lint run           # lint (config in .golangci.yml)
go-arch-lint check          # architecture (config in .go-arch-lint.yml)
gofmt -w .                  # format all Go files
```

Always run `go test -race ./...` before opening a PR. Refrain runs several goroutines per agent (PTY/VT/git) and the race detector has caught real bugs here.

End-to-end TUI tests live in `internal/e2e/` and require the `tu` CLI v0.6.0+:

```bash
go test -tags e2e -timeout 300s -v ./internal/e2e/
```

A `Mutation` workflow runs [gremlins](https://github.com/go-gremlins/gremlins) on every PR in **diff mode** — only lines changed against the PR base are mutated, and a sticky PR comment summarizes test efficacy and per-file mutation counts. It is **non-blocking** while we establish a baseline; failures do not gate merges yet. To reproduce locally:

```bash
go install github.com/go-gremlins/gremlins/cmd/gremlins@latest
gremlins unleash --diff origin/main .
```

## PR workflow

1. Fork the repo and branch from `main`.
2. Keep PRs focused — one concern per PR.
3. Add or update tests. Bug fixes should include a regression test where practical.
4. Update `CHANGELOG.md` under `[Unreleased]`.
5. Run locally before pushing: `go test -race ./... && go vet ./... && golangci-lint run && gofmt -l .` (the last should print nothing).
6. Open a PR. The template prompts for summary and test plan.

## Commit messages

Match the existing style (see `git log`):

- `feat: <what>` — new user-facing functionality
- `fix: <what>` — bug fix
- `test: <what>` — test-only changes
- `refactor: <what>` — no behavior change
- `docs: <what>` — documentation
- `chore: <what>` — tooling, dependencies, release plumbing

One-liners are fine. A body is welcome when the *why* isn't obvious from the diff.

## Architecture

```
main.go              Entry point
cmd/
  root.go            Cobra root, launches TUI
  doctor.go          Environment validation (git, claude, hook round-trip)
  hook.go            Forwards Claude hook payloads to the running refrain over a unix socket
internal/
  pty/               Raw PTY wrapper (creack/pty)
  vt/                Virtual terminal bridge (x/vt SafeEmulator + io.Pipe)
  git/               Worktree CRUD, diff, merge via exec.Command("git", ...)
  agent/             Agent + Session + Manager (composes PTY + VT + git, runs read/write/status loops)
  tui/               Bubble Tea v2 views (dashboard, diff, repo/global config, file/branch pickers)
  hook/              Unix-socket server + client for Claude Code hook events
  github/            GitHub API wrapper for PRs, checks, review status
  config/            Global and per-repo settings (JSON on disk, resolved at runtime)
  state/             Session persistence across refrain restarts
  editor/            IDE launcher helpers (macOS app probing, quote-aware tokenizer)
  audio/             Optional chimes for status transitions
  e2e/               End-to-end TUI tests (behind the `e2e` build tag)
```

See [`CLAUDE.md`](./CLAUDE.md) for key patterns (Bubble Tea v2, thread safety, shutdown sequence, hook wiring).
