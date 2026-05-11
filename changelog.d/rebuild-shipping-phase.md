### Added

- **Shipping panel**: pressing `enter`/`space` on a SHIPPING row now opens a fullscreen panel (mirroring the review panel) instead of jumping straight to the browser. The panel shows PR title, base branch, mergeable state, per-check CI rows with name + duration, and review threads grouped by reviewer with inline comments.
- **`m` to merge**: press `m` in the shipping panel to merge the PR (squash by default; gated on CI green + approved review). `M` force-merges regardless of merge-ready state. On success the session immediately transitions to Complete.
- **`r` to address feedback**: press `r` to synthesize a prompt from failing CI check names (with URLs) and reviewer `CHANGES_REQUESTED` / `COMMENTED` threads, spawn a new agent in the existing session worktree, and transition the session back to BUILDING. The PR stays open.
- **`p` opens PR in browser** from within the shipping panel; `t` opens the agent terminal; `esc` returns to the pipeline.
- **`MergeMethod` config**: global and per-repo setting (`"squash"` / `"merge"` / `"rebase"`, default `"squash"`) controls how baton merges PRs.
- **`GetReviewThreads` GitHub API**: new method on `github.Client` fetches review state + inline comments grouped by reviewer; cached on `prCacheEntry.threads`.
- **`MergePR` GitHub API**: new method on `github.Client` calls the GitHub merge endpoint.
- **`CheckRun.URL` field**: check run detail URL (from `html_url`) is now included in `github.CheckStatus.Runs` and surfaced in the `r` feedback prompt.

### Changed

- **Row state phrase**: the bare check symbol in SHIPPING rows is replaced with a descriptive phrase: `Ready` / `CI N/M failing` / `Changes requested` / `Conflicts` / `Waiting on CI`. Color-coded by severity.
