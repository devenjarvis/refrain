### Added

- `p` in the review panel and pipeline now creates PRs directly from baton when none exists. Pressing `p` on a session with no open PR pushes the branch to origin, drafts a title and body via Claude Haiku (using commits, diff stats, and any repo PR template), and opens an editable compose modal. Confirming creates the PR on GitHub and transitions the session to Shipping. If a PR already exists, `p` opens it in the browser as before.
- `git.Push` helper pushes a branch to origin with `--set-upstream` from the agent worktree.
- `github.Client.CreatePR` wraps the GitHub API to create a pull request and return a `*PRState`.
- `PRDrafter` in `internal/agent/haikuname.go` mirrors `BranchNamer`: pipes commits, diff stats, the user prompt, and the repo PR template into Haiku and returns a `{title, body}` pair.
- PR template discovery searches `.github/PULL_REQUEST_TEMPLATE.md`, `docs/PULL_REQUEST_TEMPLATE.md`, and `PULL_REQUEST_TEMPLATE.md` (case-insensitive) and feeds the result verbatim into the drafter prompt.
- New settings keys: `PRDraftByDefault` (default `true`), `AutoOpenPRInBrowser` (default `true`).

### Fixed

- `p` no longer silently opens stale closed PRs. `GetPRBySHA` now returns `nil` when all matching PRs are closed, so the compose-and-create flow triggers instead of opening a stale URL.
