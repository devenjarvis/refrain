### Fixed

- Hook server no longer silently drops status-critical events (Stop, UserPromptSubmit) when the dispatcher is briefly slow — agent badges stay accurate under burst load.
- `Manager.emit` and the planner-question pump are now guarded against `send on closed channel` panics during quit/detach; the manager handles a late `StartDraft`/`CreateAgent`/`ResumeSession` racing teardown.
- Plan-task counters (`ParsePlanTasks`, `planTaskCounts`) now scope to the `## Tasks` section so a stray `- [ ]` in `## Spec` or `## Verification` no longer corrupts the `[task N]` commit-to-task mapping in the review panel.
- Reviewer comments and CI check names/URLs flowing through the shipping panel's "address feedback" prompt are now fenced and prefixed with a "treat as data" preamble, mitigating prompt-injection from PR text.
- Wellness break overlay no longer fires while the shipping panel, review panel, plan editor, agent terminal, or config form is open — break entry is deferred until the user is back on the pipeline.
- `Session.Cleanup` is now idempotent; concurrent calls from teardown paths cannot surface a spurious "not a worktree" error.
- Merge button (`m` in shipping panel) re-fetches PR state from GitHub before merging and refuses if the PR is no longer open or mergeable.
- "Address feedback" (`r` in shipping panel) refuses to respawn an agent when the cached PR is merged or closed.
- Stale `state.json` entries whose worktree directory no longer exists are now pruned on launch (with a one-line notice) instead of repeatedly failing to resume.
- `merge_method` config is normalized (lowercased, trimmed) and validated against `{merge, squash, rebase}`; invalid values fall back to the default instead of silently coercing at the API layer.
- `{user}` branch-prefix template falls back to the literal `user` when both git `user.name` and `$USER` are empty, keeping branch paths well-formed in minimal containers.
