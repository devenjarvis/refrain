### Changed

- Split `internal/tui/app.go` (5,232 lines) into per-domain files: `update_lifecycle.go`, `update_plan.go`, `update_pr.go`, `update_review.go`, `update_keys.go`, `update_pickers.go`, `update_views.go`. `Update()` is now a thin dispatcher that delegates to `handleXMsg` methods. No user-visible behavior change; pure mechanical refactor that drops `app.go` to ~1,400 lines and makes each message domain reviewable in isolation.
