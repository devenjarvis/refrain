### Added

- Sessions in Building, ReadyForReview, or InReview phases now auto-advance to Shipping when the PR poller discovers an open PR opened outside refrain (e.g. via `gh` or the GitHub web UI). The review panel closes if it was open for the promoted session.
