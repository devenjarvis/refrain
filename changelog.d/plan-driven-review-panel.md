### Added

- Review panel now shows a per-task task list when the session has a plan, mapping each `[task N]` commit group to its plan task with commit counts, insertion/deletion counts, and a verdict badge.
- A Sonnet reviewer subprocess runs automatically for each task group when the review panel opens, producing a `pass` / `concerns` / `fail` verdict with a one-sentence rationale. The reviewer model is configurable via `reviewer_model` in global or per-repo config.
- `enter` / `space` on a task row opens the existing diff browser filtered to that task's commits; `esc` returns to the task list.
- `j` / `k` navigate the task list rows.
- Sessions without a plan still show the aggregate file-centric view.

### Changed

- `renderReviewPanel` now accepts cursor, width, and height parameters; the review panel renders as a scrollable task list rather than a split file/shape view when plan data is available.
