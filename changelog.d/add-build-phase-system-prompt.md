### Added

- Build-phase agents now receive a system prompt (via Claude's `--append-system-prompt` flag) instructing them to (a) use the `TodoWrite` tool to break work into atomic steps and (b) commit one git commit per step with a parseable subject prefix — `[task N]` when a `.claude/plan.md` exists (where `N` is the 1-based plan task index), or `task:` otherwise. This lays the groundwork for surfacing TodoWrite progress on dashboard rows and filtering review diffs by plan task. New global and per-repo `build_system_prompt` setting overrides the default.
