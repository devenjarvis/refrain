### Changed

- Extract 14 wellness/focus-block fields off `App` into a `wellnessState` sub-struct in `internal/tui/wellness.go`. `App.wellness` now owns the break-overlay state machine, the focus-block timer, and the session/agent counters. Pure structural move — field names are preserved (`a.focusBreakMode` becomes `a.wellness.focusBreakMode`), no behaviour change, all tests pass under `-race`. Sets the seam for follow-up encapsulation work (StartBreak/EndBreak/OnTick methods).
