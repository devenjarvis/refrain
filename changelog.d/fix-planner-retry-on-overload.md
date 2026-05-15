### Fixed

- Transient `claude` planner failures (notably 529 `overloaded_error` responses) now retry automatically up to 3 times with 2s and 5s backoffs before surfacing an error. The Planning card shows `✎ retrying… (N/3)` during retry attempts so the pipeline view reflects progress without requiring user action. Pressing `R` in the plan editor replays the saved prompt when auto-retry has been exhausted, giving a one-key path to recover without re-typing the original goal.
