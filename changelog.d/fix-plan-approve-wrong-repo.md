### Fixed

- Approving a plan in one repo no longer opens the new agent in a session belonging to a different repo. Session IDs are per-manager counters so two repos can both have `session-1`; the plan editor now carries its repo path from the moment it opens, eliminating the first-match ambiguity in `repoPathForSession`. The same fix covers the revise and abandon actions.
