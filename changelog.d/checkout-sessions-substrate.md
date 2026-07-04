### Added

- Checkout sessions (domain layer, rollback Phase 1): `Manager.CreateSessionInDir` starts a session directly in the repo's main working tree — no worktree, no owned branch, at most one per repo (`ErrCheckoutSessionExists`). The first-prompt Haiku branch rename is suppressed, `Session.Cleanup` is a guaranteed no-op on the tree for this kind, and the session survives detach/resume (re-reading HEAD in case the branch changed while detached). Not yet reachable from the UI; the session list lands in Phase 2.
- `Session.Kind` (`worktree` | `checkout`) with persistence in `state.json` (`"kind"`); missing/empty loads as `worktree` so legacy state files keep working.

### Changed

- `Manager.CreateSessionForPlanning` is renamed `CreateSessionNoAgent` — it is the "session exists before its first agent" primitive, no longer framed around a lifecycle phase.
