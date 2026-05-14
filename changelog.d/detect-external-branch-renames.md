### Fixed

- Detect when a branch is renamed outside refrain (e.g. `git branch -m`) and reconcile the in-memory session state within ~2 seconds, preventing a stale branch name from breaking sidebar labels and PR polling.
