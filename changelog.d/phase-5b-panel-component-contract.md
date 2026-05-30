### Fixed

- Validation-check run state is now keyed by `(repoPath, sessionID)` instead of
  bare session ID, so two repos hosting a session with the same ID no longer
  collide; stale entries are also cleaned up when their session disappears.

### Changed

- Review and shipping panels now conform to the §3 Component contract: their
  `Update`/`View` no longer take a per-tick `PanelServices` value. Reference-typed
  handles are injected at construction and panel→App scalar mutations flow as
  messages applied centrally by `App.Update`. The `PanelServices` seam and its
  per-tick builder have been removed.
