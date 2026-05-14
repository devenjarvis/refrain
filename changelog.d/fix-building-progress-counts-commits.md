### Fixed

Building card progress bar now advances on every `[task N]` commit, not only when Claude toggles the matching checkbox in `.claude/plan.md`. Commits are the build agent's authoritative per-task signal (mandated by the build system prompt) and now contribute to the bar via `max(planCheckboxesDone, distinctCommittedTaskIndices)`. The cache refreshes only on Claude `Stop` hook events for Building-phase sessions, so the 100ms render path stays shell-out-free.
