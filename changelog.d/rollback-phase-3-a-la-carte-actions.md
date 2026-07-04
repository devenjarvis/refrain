### Added

- `P` plan action on the session list (rollback Phase 3): opens the plan
  editor when the session already has a plan (or a draft/revise in flight);
  otherwise prompts for a goal in a small overlay and drafts a plan against
  the session's directory — including mid-conversation, alongside running
  agents. Planning is now an action you invoke, not a phase you are born into.

### Changed

- The shipping panel is now the **PR panel** (`shippingpanel_*` →
  `prpanel_*`, header `PULL REQUEST`), opened on demand via `p` when the
  poller knows a PR — never via lifecycle phase. All of its behavior keeps:
  per-check CI rows, review-thread triage, feedback respawn (`r`), gated and
  force merge.
- The PR poller no longer mutates sessions. Discovering an open PR only
  updates the badge cache (no auto-promotion, no review-panel hijack), and
  review threads are fetched for any session with an open PR. Merge —
  internal or external — marks the session done and renders a `Merged` badge
  plus an `X to clean up` hint on the row; the session stays in the list
  until the user removes it. A closed PR shows a `Closed` badge and leaves
  the session fully live. Merged PRs stop polling (terminal state).
- Plan drafting (`StartDraft`/`RevisePlan`), plan approval, raw-session
  creation, review-panel shipping (`p`), and the PR panel's feedback respawn
  no longer set lifecycle phases — the drafting/revising state is observable
  via the session's own flags, and commit-task progress refreshes on Stop
  hooks for any session with a plan.

### Removed

- The merged/closed auto-cleanup path: sessions are never killed by the
  poller or by a merge. Teardown is always the user's explicit `x`/`X`.
- The `transitionShipping` plumbing through the PR draft/compose/create
  pipeline.
