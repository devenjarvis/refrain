### Changed

- Encapsulated the dashboard's panel-focus state machine in a `Modals` controller (`internal/tui/modals.go`). Each transition site now atomically sets focus and the modal model pointer through one typed call, and `Close()` always nils every owned model. The previous invariant — "the model for `panelFocus X` is non-nil iff `panelFocus == X`" — is now enforced by the type instead of by ~30 paired field assignments and ~28 guard-pair checks scattered across `update_*.go`. No behavior change.
