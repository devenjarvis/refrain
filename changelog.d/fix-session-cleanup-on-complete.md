### Fixed

- Completed sessions (merged PR, external merge, or 'c' in review panel) now remove their worktree and disappear from the manager immediately, rather than persisting until the next `refrain` launch. `Detach()` filters out any lingering `LifecycleComplete` sessions and cleans up their worktrees during quit.
