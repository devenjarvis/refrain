### Changed

- The review panel's ledger now follows a fallback chain (rollback Phase 4)
  instead of collapsing to a single "Overview" row without a plan:
  1. **Plan tasks** — unchanged: plan-task cards with `Plan-Task: N` trailer
     grouping and per-task AI verdicts.
  2. **Commits** — no plan, commits exist: one card per commit under a
     `COMMITS` header, titled by commit subject with a short-hash label. The
     AI reviewer runs per card with the commit subject standing in for the
     task text, so review works on attached foreign branches. Branches longer
     than 20 commits get an aggregate "Earlier changes" rollup card (no AI
     verdict) so a 100-commit branch doesn't produce a 100-row ledger.
  3. **Changed files** — no commits (e.g. a checkout session with uncommitted
     work): one card per file under a `CHANGED FILES` header with a per-file
     diff in the right pane. AI verdicts are disabled in this mode — cards
     show a `manual review` badge — since verdicts over fragments of
     uncommitted work produce noise.
- Rework (`b` in the review panel) synthesizes its feedback prompt from the
  ledger's cards in all three modes — plan prompts keep the `Plan-Task`
  trailer contract; commit/file prompts reference commits and files directly
  — and no longer transitions the session's lifecycle: it just spawns a fresh
  agent in the session's directory.

### Removed

- The synthetic "Overview" ledger row for plan-less sessions, replaced by the
  per-commit and per-file modes above. An entry with no changes at all now
  shows a "(no changes to review yet)" hint.
