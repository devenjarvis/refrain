### Fixed

Sessions in the SHIPPING column now auto-complete and clear from the dashboard when their PR is merged or closed externally (e.g. via the GitHub UI or `gh pr merge`). Previously the open-only PR poll filtered merged PRs out, leaving the session stuck in Shipping until the next refrain restart.
