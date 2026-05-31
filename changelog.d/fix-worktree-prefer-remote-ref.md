### Fixed

- New session worktrees now branch off `origin/<baseBranch>` whenever that
  remote-tracking ref exists locally, even when the in-line `git fetch` fails
  (offline, auth error, or broken URL). Previously a fetch error caused refrain
  to silently use whichever branch the user had checked out in the main
  worktree, producing sessions that started from a stale or unrelated commit.
