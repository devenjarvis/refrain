### Changed

- Internal cleanup with no behavior change: removed dead property-test generators, consolidated the planner/reviewer/Haiku `claude -p` subprocess invocation and argument building into a single shared helper (standardizing on `--allowedTools`), and extracted testable helpers (`shouldAutoPromote`, `handlePRNotFound`, per-kind hook-event handlers) plus shared TUI modal/navigation helpers.
