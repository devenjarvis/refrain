### Added

- Press `b` in the review panel to send tasks marked concerns/fail by the AI reviewer (plus any rows flagged with `f`) back to a new building agent in the same worktree. The session returns to BUILDING with a prompt that names the failing tasks and instructs the agent to keep using `[task N]` commit prefixes so the next review groups round-2 commits under the same task index.
- Press `f` in the review panel to toggle a flag on the cursor task. Flagged tasks are included in the `b` rework prompt regardless of the AI verdict (including plan tasks the agent never touched, which previously couldn't be flagged), and display an orange `⚑` badge.

### Changed

- Review panel: the selected task row is now wrapped in a primary-color vertical-bar border instead of a barely-visible background tint, making the cursor obvious at a glance.
