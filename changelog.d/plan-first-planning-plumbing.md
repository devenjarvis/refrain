### Added

- `PlanDrafter` interface in `internal/agent` with a default Sonnet-backed implementation (`claude -p --model claude-sonnet-4-6`) for generating and revising plan markdown
- `Session.PlanPath`, `ReadPlan`, `WritePlan`, `HasPlan` helpers — plan markdown lives at `<worktree>/.claude/plan.md`, with atomic temp+rename writes and best-effort `.gitignore` upkeep so the plan never leaks into the PR diff
- `LifecycleDrafting` phase plus `Manager.StartDraft(sessionID, prompt)` to drive the async draft. Drafting is gated by `Session.TryStartDraft` to prevent double dispatch, transitions back to `LifecyclePlanning` on completion (with `Session.DraftError` set on failure), is cancellable via `Session.CancelDraft`, and is drained on `Manager.Shutdown` / `Manager.KillSession`. Drafting subprocesses do not count against `MaxConcurrentAgents`.

### Notes

- No user-visible change yet — this is the plumbing for the plan-first planning flow. Editor, prompt modal, approve→spawn, and revise loop ship in follow-up PRs.
