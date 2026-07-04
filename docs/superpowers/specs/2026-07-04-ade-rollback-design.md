# Refrain vNext: Rollback to a General-Purpose TUI ADE

**Status:** Approved direction. Implementation phased (§7); each phase is one implementation session.
**Date:** 2026-07-04

---

## 1. Context & motivation

Refrain set out to be the calm way to run Claude Code agents in parallel, and it grew into a
single opinionated workflow: every session is born into a PLANNING → BUILDING → REVIEWING →
SHIPPING pipeline, gets an isolated worktree on an auto-named branch, and is expected to end in
a merged PR. That shape is the product today — the pipeline is the only home screen, and a
session that doesn't fit it has nowhere to render.

The honest post-mortem is that we optimized for one workflow shape and made the tool unusable
for most of what a developer actually does with Claude in a day:

- **Debugging** — you want Claude in *your* checkout, on *your* branch, looking at *your*
  half-broken state. Refrain forces a fresh worktree cut from `origin/<base>`, which can't see
  the problem.
- **Exploring a codebase** — a read-only conversation. There is nothing to plan, build, review,
  or ship, yet the session still occupies a pipeline stage and gets a branch it will never use.
- **Writing tickets and design docs** — no diff, no PR, no tasks. The pipeline's organizing
  concepts are all wrong for it.
- **Editing another user's branch** — partially supported (`CreateSessionOnBranch` attaches a
  worktree), but the review panel collapses to a single "Overview" row on any branch without a
  refrain-authored `plan.md`, so "our code review process" only really works for sessions
  refrain started itself.
- **Just talking to Claude** — the new-session form rejects an empty prompt; there is no way to
  open a blank REPL.

Meanwhile, some of what we built is genuinely valuable and worth protecting:

- **Worktree parallelism** — isolated worktrees + branch management make concurrent coding
  sessions safe. This stays the default for coding work.
- **Hook-driven status** — the unix-socket hook pipeline gives reliable idle/waiting/done/error
  signals per agent. This is the attention backbone and survives untouched.
- **In-app plan drafting and review** — the eight-section plan, the plan editor, and the
  plan-quality research behind them measurably improve build-agent output.
- **The review flow** — task-card ledger, per-task diffs, AI verdicts, rework loop. Too rigid
  today (plan-gated), but the machinery is right.
- **The terminal itself** — the PTY/VT bridge and the fullscreen passthrough terminal are a
  near-complete raw Claude experience already.

The exploration that preceded this doc found something encouraging: the valuable machinery is
almost entirely **decoupled already**. The plan drafter takes a prompt and a cwd
(`internal/agent/planner.go:34`); the plan parsers are pure string functions
(`internal/agent/session.go:899`); the AI reviewer takes strings (`internal/agent/reviewer.go:119`);
the diff viewer takes a parsed diff; the GitHub client is standalone; the git diff/log helpers
work on any checkout. The *rigidity* is concentrated in three welds:

1. **The dashboard triad** — four hardcoded phase render blocks
   (`internal/tui/dashboard_view.go:1140-1260`), a `focusSection` enum with `[4]int` cursor
   arrays (`internal/tui/keymap.go:101`, `internal/tui/focuscursor.go`), and a `panic` guard on
   the four-section invariant (`internal/tui/dashboard_model.go:220-312`).
2. **~19 scattered `SetLifecyclePhase` call sites** across the TUI layer with no central
   transition table — the workflow is encoded in the callers, not the type.
3. **The Manager's creation paths** — every one forces a worktree
   (`internal/agent/manager.go:1207` / `:1125`); there is no "run in this directory" path, even
   though `NewSessionForTestWithPath` (`internal/agent/session.go:675`) proves a bare-path
   session works end to end.

This doc specifies the rollback: a TUI-based ADE (agent development environment) with a raw
Claude experience at the center, where planning, review, and shipping are **actions you invoke**,
not phases you are marched through.

## 2. Decisions (non-negotiable)

Ruled by the owner; future sessions should not relitigate these.

1. **À-la-carte rollback.** The four-section pipeline home screen is replaced by a general
   session list. The lifecycle phase machinery as forced progression goes away. Plan drafting/
   review and code review become per-session opt-in actions bound to keys.
2. **Worktrees stay the default** execution context for new sessions (parallel-safety
   preserved). A new no-worktree "checkout session" kind is added for everyday tasks that need
   the real working tree.
3. **Wellness features are cut for now**: session timer, break overlay, breathing animation,
   soft session cap, wellness log. The code is separable; remove it.
4. **The deliverable of the design session is this doc + roadmap**, committed; implementation
   happens in later sessions per phase.

## 3. What we keep, and why

Everything below survives unchanged (or with only call-site adjustments). The architecture rules
in `CONVENTIONS.md` also survive intact — this is a product rollback, not an architecture
rewrite.

| Component | Why it stays |
|---|---|
| `internal/pty`, `internal/vt` | Fully general PTY + emulator engine, zero pipeline coupling. |
| `agent.Agent` | Explicitly worktree-agnostic ("Agents do not own worktrees — sessions do", `agent.go:50`). Spawn variants for resumed claude, arbitrary commands, and shells already exist. |
| focusLaunch fullscreen terminal | Near-complete Claude passthrough: full key forwarding, mouse, bracketed paste, scrollback + selection + clipboard, resize, alt-screen cursor handling. This *is* the raw experience. |
| `internal/hook` | Per-claude-process, keyed by `REFRAIN_AGENT_ID`; event set (start/stop/notification/prompt/pre-tool-use) is exactly what attention tracking needs. |
| `PlanDrafter` + planner question server | `DraftRequest{UserPrompt, Model, QuestionSocket, Cwd}` is Session-free; any directory works. |
| `ParsePlanTasks` / `ParsePlanSections` / `GroupCommitsByTask` | Pure functions; `GroupCommitsByTask` already degrades gracefully on untagged commits. |
| `ReviewerAgent.Review` | All-strings request; Session-free. |
| Diff viewer (`diffview.go`, `internal/diffmodel`) | Takes a name + parsed diff; Session-free. |
| `internal/github` | Standalone GitHub client; no refrain types cross the boundary. |
| `internal/git` | Diff/log/worktree helpers take `WorktreeInfo{Path, BaseBranch}` and work on any checkout; `AttachWorktree` + `ownsBranch=false` already models "existing branch, don't own it". |
| Multi-repo (one `Manager` per repo, `repos.json`, `R`/`a` keys) | Mature and workflow-neutral. |
| `internal/state` save/detach/resume | `claude --resume` reattach is worktree-independent; only the persisted shape changes (§6). |
| `internal/config` layering, `AgentProgram`/`AgentModel` | Already designed for non-default programs. |
| `internal/editor`, `internal/audio` | Editor launch is workflow-neutral; chimes are status-driven, not wellness-driven. |
| Plan editor UI (`planeditor_*`) | Thin session coupling (`ReadPlan`/`IsDrafting`); survives as the `P` action's screen. |
| Shipping panel internals | PR title/checks/threads rendering, `buildFeedbackPrompt`, `mergePRCmd` all keep; only the phase-gating dies (§4.7). |
| `songs`/`setlist` session naming | Zero coupling; keep the whimsy. |

## 4. Target architecture

### 4.1 The organizing concept that replaces lifecycle

There are **no phases and no transitions**. A session is a named group of agents sharing a
directory. What the UI needs to know about a session is expressed by four orthogonal signals,
three of which already exist:

1. **`agent.Status`** (unchanged: Starting / Active / Waiting / Idle / Done / Error) — the
   per-agent process state and the primary attention signal. A session's displayed status is the
   max-severity aggregate of its non-shell agents: `Error > Waiting > Active > Starting > Idle > Done`.
2. **Derived session facts, rendered as badges** — never as list placement: `HasPlan()`,
   `IsDrafting()`, PR presence + CI/review phrase (from the existing `prCacheEntry` poller
   cache), `merged`, `DoneAt()`.
3. **Session kind** (new): `KindWorktree` (today's behavior, the default) vs `KindCheckout`
   (new — runs in the repo's main working tree, no worktree, no owned branch). Persisted; drives
   cleanup and rename-suppression behavior.
4. **Attention flag** (derived boolean): `status ∈ {Waiting, Error}`, draft just finished, or PR
   state became actionable. Rendered as the row's accent stripe/glyph, reusing the existing
   `StatusWaiting` accent semantics. Attention **never reorders the list** — rows stay in
   creation order so the list doesn't churn. (The finite signal budget is the piece of the old
   philosophy that survives: persistent state changes are worth surfacing; running-normally is not.)

`internal/agent/lifecycle.go` is deleted (Phase 5). There is no replacement state machine
because there is nothing to transition.

### 4.2 Home screen: the session list

A new component `internal/tui/sessionlist_{model,update,view}.go` replaces the dashboard triad
as the root screen. Per `CONVENTIONS.md` §3/§9 it is written fresh — the `dashboard_*` files are
~3,000 lines of phase-shaped rendering and are deleted once unreachable, not adapted.

Layout: **repo-grouped flat list**. Repo headers stay (they are how `R`/`a`/repo-config are
reached today, and they answer "which repo does `n` target"). One cursor, one plain `int` index
over a flattened row slice — `FocusedCursor` and its `[4]int` per-section indices die with the
dashboard. Each session renders as a 2-line card (visual language lifted from
`renderFocusSessionCard`):

```
 refrain ──────────────────────────────────────────────────────────
 ▐ add-dark-mode            ● waiting     plan · PR #42 CI 2/3
 ▐   refrain/add-dark-mode · worktree     12m · 2 agents
   fix-flaky-ci             ○ idle        merged — X to clean up
     main · checkout @ main               41m · 1 agent
 dotfiles ─────────────────────────────────────────────────────────
   explain-zsh-setup        ● active
     master · checkout @ master           3m · 1 agent
```

- Line 1: display name, status glyph + word (theme tokens), badges: `plan`, `draft…`, PR
  indicator with the existing CI/review phrase (`Ready` / `CI N/M failing` / `Changes requested`
  / `Conflicts` / `Waiting on CI`), `merged`.
- Line 2: branch, context tag (`worktree` | `checkout @ <branch>` — checkout gets a distinct
  accent color so the user always knows an agent is loose in their real tree), age, agent count.
- Empty state: a hint block listing `n` new session, `o` open on branch/PR, `a` add repo.

`enter`/`space` on any row **always** opens focusLaunch (the fullscreen terminal). The old
per-phase dispatch — review panel for Reviewing rows, shipping panel for Shipping rows — is
gone. The terminal is the product; panels are actions. Mouse behavior carries over: click moves
the cursor, double-click opens the terminal, PR-indicator click opens the PR.

### 4.3 Checkout sessions

New Manager entry point:

```go
func (m *Manager) CreateSessionInDir(cfg Config) (*Session, *Agent, error)
```

A sibling of `createSessionOnBranchWorktree` (`manager.go:1125`), not of
`createSessionWorktree`:

- Builds `WorktreeInfo{Path: m.repoPath, Branch: <current branch>, BaseBranch: <detected>}` —
  no `git worktree add`. (`NewSessionForTestWithPath` already proves a bare-path session works.)
- `ownsBranch = false` and `sess.SetClaudeName(true)` — the latter suppresses the Haiku branch
  rename exactly as the attach path does (`manager.go:1163`). **Safety-critical:** without it,
  the first prompt would rename the user's real branch.
- `sess.kind = KindCheckout`. `Session.Cleanup` (`session.go:453`) becomes a guaranteed no-op on
  the tree for this kind — it must never call `git.RemoveWorktree` on the main checkout. Killing
  a checkout session kills agents only.
- **At most one checkout session per repo**, enforced in the Manager with a sentinel
  (`ErrCheckoutSessionExists`). Two sessions in one directory are two agent groups fighting over
  one working tree; the TUI responds to the sentinel by offering to add an agent (`c`) to the
  existing session instead. Multiple *agents* in one checkout session are fine — that is exactly
  what a Session models.
- Hooks need no relocation: `buildHookArgs` (`agent.go:285`) writes
  `<worktreePath>/.refrain/hooks.json`, and for a checkout session `worktreePath == repoPath`,
  whose `.refrain/` is already gitignored. Verified by test, not by code change.
- Display name: slugified current branch, so the row reads as "the checkout".

`CreateSessionForPlanning` (`manager.go:1291`) is renamed `CreateSessionNoAgent` and loses the
lifecycle framing; it remains the "session exists before its first agent" primitive used by the
plan-first flow.

### 4.4 New-session flow

`newsession.go` keeps the full-viewport screen; the semantics change:

- **`enter` = raw session.** Create the session, spawn `claude` with the prompt as its task. No
  plan, no phase. This is the default — the raw experience is the product.
- **Empty prompt + `enter` = blank claude REPL.** The current block on empty prompts
  (`newsession.go:323`) is removed; a blank conversation is the everyday case for debugging and
  exploring.
- **`ctrl+p` = plan-first.** Replaces today's inverted pair (enter=plan, ctrl+enter=skip). Runs
  `CreateSessionNoAgent` + `StartDraft` → plan editor, as today.
- The form gains a **Context** field: `Worktree (new branch)` (default) / `Current checkout`.
  Existing-branch/PR entry stays on `o` (branch picker → `AttachWorktree`), unchanged.
- Copy is rewritten task-neutral: "What's the task?" with placeholders like "Explain how auth
  works in this repo" and "Draft a ticket for the flaky CI failure" — the "Plan → Build → Review
  → Ship" FLOW block is pipeline branding and goes.
- `PlanFirstEnabled` config is deleted (one less mode; both submit keys are always offered).

### 4.5 À-la-carte actions and keymap

| Key | Action | Change |
|---|---|---|
| `n` | New session screen (context toggle inside) | reworked (§4.4) |
| `enter`/`space` | Open terminal (focusLaunch) — any session | was per-phase dispatch |
| `P` | Plan: draft if none (prompts for goal), open plan editor if one exists | new; replaces the planning phase |
| `r` | Review panel — any session with commits or dirty changes | phase gate removed |
| `d` | Diff view | unchanged |
| `p` | PR: compose + create if none; open PR panel if one exists | absorbs shipping-panel activation |
| `b`, `m` | — | **deleted** (advance-to-Building / mark-ready have no meaning) |
| `c` `t` `e` `o` `R` `a` `s` `x` `X` `N` | add agent / shell / IDE / open branch / repos / add repo / settings / kill agent / kill session / cycle repo | unchanged |

`Manager.StartDraft`/`RevisePlan` (`manager.go:590`/`:791`) drop their `SetLifecyclePhase`
calls but keep `TryStartDraft` double-dispatch gating and the planner question server. Plan
approval in the editor still spawns a build agent with `BuildFromPlanPrompt` into the same
session (`update_plan.go:370-425`, minus the transitions at `:299`/`:438`) — an action, not a
transition.

### 4.6 Review panel generalization

The single `hasPlan` gate in `fetchReviewDiffCmd` (`update_review.go:621`) is replaced by a
**ledger fallback chain**:

1. **Plan tasks** (today's path): `ParsePlanTasks` + `GroupCommitsByTask` over `Plan-Task: N`
   trailers. Unchanged.
2. **No plan, commits exist**: one card per commit — title = commit subject,
   `DiffForCommits` per single hash. This makes the ledger work on any branch, including
   attached branches from another user. Long-lived branches cap the ledger (last N commits plus
   an aggregate card) so a 100-commit branch doesn't produce a 100-row ledger.
3. **No commits** (checkout session with uncommitted work): per-file cards from
   `GetPerFileDiffStats`, rendered with the existing per-file diff viewport.

The AI verdict flow (`reviewTaskCmd` → `ReviewerAgent.Review`, already all-strings) runs per
card in modes 1 and 2, with the commit subject standing in for the task text. Verdicts are
**disabled in mode 3** — the reviewer seeing fragments of uncommitted work produces noise, and
that mode is manual review. The rework key `b` no longer requires a live build agent: it
synthesizes a feedback prompt from flagged cards (the shipping panel's proven
`buildFeedbackPrompt` pattern) and **spawns a new agent in the session** via `AddAgent`. The
Spec overlay (`?`) stays gated on `HasPlan` — correct, since it renders the plan.

### 4.7 PR panel (ex-shipping panel)

`shippingpanel_*` is renamed `prpanel_*` and de-phased. All data and behavior keep: PR
title/base/mergeable, per-check CI rows, review threads grouped by reviewer,
`buildFeedbackPrompt`, `mergePRCmd`, merge-method config.

- Opened on demand via `p` when the poller knows a PR; never via phase.
- The PR poller (`update_pr.go`) keeps polling and caching but **stops mutating sessions**: the
  `SetLifecyclePhase(Shipping)` calls at `:63`/`:163` and `(Complete)` at `:178`/`:245` are
  deleted. PR presence/state is a badge. Merge (internal or external) calls `sess.MarkDone()`
  and shows a `merged` badge plus a "press X to clean up" hint on the row; the session stays in
  the list until the user removes it. No sessions vanish on their own.
- The review-thread fetch gate keys off "has an open PR" instead of the Shipping phase.

## 5. What gets removed

Deleted in Phase 5, after Phases 2–4 make it unreachable:

| Target | Details |
|---|---|
| `internal/agent/lifecycle.go` | + its unit and property tests; every `LifecyclePhase` reference (`grep Lifecycle` must return zero) |
| Wellness | `internal/tui/wellness.go` + tests; break overlay (`dashboard_view.go:984-1136`); `sessionLimitModal`; `writeWellnessLog` (`app.go:1424`); wellness branches in `handleTick` (`app_lifecycle.go:199-240`); `MaxReviewBacklog` gate |
| Cursor machinery | `internal/tui/focuscursor.go` + tests; `focusSection` enum + `focusSectionsInOrder` (`keymap.go:98-120`); `sectionItems`/`sectionCounts` (`dashboard_model.go:220-312`) |
| Dashboard triad | `dashboard_view.go`, `dashboard_model.go`, `dashboard_keys.go`, `dashboard_update.go`, `dashboard_props.go` + tests — after lifting `renderFocusSessionCard` visuals, the PR-phrase logic, mouse hit-testing, and focusLaunch key forwarding into the session list |
| `autopromote_test.go` | tests phase auto-promotion |
| `Manager.ActiveSessionCount` (`manager.go:1612`) | the only lifecycle-reading wellness code; call sites get a plain `SessionCount()` |
| Config fields | `FocusSessionMinutes`, `FocusBreakMinutes`, `MaxConcurrentSessions`, `MaxReviewBacklog`, `PlanFirstEnabled` — struct fields, defaults, resolution, and their global-config form rows |
| Keys `b`, `m` | and their keymap entries/help text |

## 6. Migration & compatibility

| Concern | Handling |
|---|---|
| `state.json` `"lifecyclePhase"` | Field removed from `SessionState`; old files' keys are silently ignored by `encoding/json` — same mechanics as the `focus_mode_enabled` precedent. Add a fixture test loading a state file containing `"lifecyclePhase": "shipping"`. |
| `state.json` `"kind"` (new) | Missing/empty → `worktree` (all legacy sessions are worktree sessions). No version bump; load stays tolerant. |
| `settings.json` wellness keys | Silently ignored; extend the migration/compat tests with the five removed keys. |
| `.refrain/logs/wellness.log` | Stop writing; never read or delete existing files. |
| Detach → upgrade → reattach | Old sessions reattach into the flat list regardless of former phase. Acceptable and intended. |
| Changelog | Each phase lands its own `changelog.d/` fragment (`### Added` / `### Changed` / `### Removed`). |

## 7. Phased roadmap

Every phase ships green: `go test -race ./...`, `go vet ./...`, `golangci-lint run`,
`go-arch-lint check`, `./refrain doctor`, and the `e2e` suite. Ordering rationale: substrate
first so every later phase can be tested against checkout sessions; the home screen lands before
action rewiring so lifecycle dies by attrition (unreachable) before it dies by deletion; docs
and deletions last so every intermediate build stays revertable.

### Phase 1 — Substrate: checkout sessions (domain layer only)
- **Goals:** `Session.Kind` field + persistence; `CreateSessionInDir` with cleanup guard,
  rename suppression, and the one-per-repo sentinel; `CreateSessionForPlanning` →
  `CreateSessionNoAgent`; resume of checkout sessions (`claude --resume` in the same dir);
  test that hooks.json lands inside the repo's gitignored `.refrain/`.
- **Files:** `internal/agent/manager.go`, `internal/agent/session.go`,
  `internal/state/state.go`, detach/resume tests.
- **Risks:** the cleanup guard is safety-critical (a bug deletes the user's checkout); resuming
  a checkout session whose branch changed underneath it.
- **Verification:** table-driven tests with `bash -c` agents on temp repos; an explicit test
  that `Cleanup` never invokes worktree removal for `KindCheckout`; race detector. No UI change.

### Phase 2 — New home screen
- **Goals:** `sessionlist_{model,update,view}.go` (repo-grouped list, single cursor,
  status/badge rendering, mouse); App routes the root view to it (old dashboard files become
  unreachable but are not yet deleted); focusLaunch opens from any row; new-session screen gets
  the Context toggle, raw-by-default submit semantics, empty-prompt REPL, and neutral copy.
- **Files:** new `sessionlist_*.go`, `internal/tui/app.go` routing, `keymap.go`,
  `newsession.go`, `internal/e2e/`.
- **Risks:** the big-bang UX moment; the e2e suite is pipeline-shaped and needs rewriting;
  focusLaunch key-forwarding regressions while lifting it out of `dashboard_keys.go`.
- **Verification:** render/golden tests for the list component (CONVENTIONS §10); rewritten e2e
  flows: raw session, checkout session, blank session, terminal passthrough.

### Phase 3 — À-la-carte actions
- **Goals:** `P` plan action (draft-or-edit); `p` PR compose-or-panel; `shippingpanel_*` →
  `prpanel_*` rename + de-phasing; PR poller stops mutating sessions (badges + `MarkDone` on
  merge); transition removal in `StartDraft`/`RevisePlan`/`update_plan.go`.
- **Files:** `update_plan.go`, `update_pr.go`, `shippingpanel_*` → `prpanel_*`,
  `planeditor_*` (thin), sessionlist key handling.
- **Risks:** PR badge state without phases (open→merged detection becomes purely
  cache-diff-driven); the external-merge cleanup path regressed once before
  (`fix-shipping-auto-complete-on-external-merge`) — its replacement behavior (badge + hint)
  needs equivalent coverage.
- **Verification:** poller tests updated to assert **no** session mutation; badge unit tests.

### Phase 4 — Review generalization
- **Goals:** the ledger fallback chain (plan → per-commit → per-file); rework `b` spawns a
  feedback agent instead of requiring a live build agent; review works on checkout sessions and
  attached foreign branches; verdicts per card in modes 1–2, disabled in mode 3.
- **Files:** `update_review.go` (`fetchReviewDiffCmd`), `reviewpanel_{model,update,view}.go`,
  reviewer request assembly.
- **Risks:** per-commit grouping on long branches (cap the ledger); reviewing uncommitted work
  on a dirty checkout races the agent's own in-flight edits.
- **Verification:** table-driven tests for the three ledger modes; e2e review of a plan-less
  branch.

### Phase 5 — Deletions + docs
- **Goals:** delete everything in §5; config field removal + silent-ignore tests; rewrite
  `CLAUDE.md`, `README.md`, `DESIGN.md` (the stage-color half of the two parallel color
  systems), and the `CONVENTIONS.md` examples that reference `focusCursorSection`; final
  changelog fragment.
- **Risks:** lowest-risk phase — everything deleted is already unreachable; the residual risk
  is missed references (compile errors catch most; `grep -r Lifecycle` must return zero).
- **Verification:** full suite + `refrain doctor` + a manual pass of every keybinding documented
  in the rewritten README.

**Docs philosophy ruling** (owner default, see §9): the rewritten docs keep "signal noise
budget" and "batch over stream" as TUI design principles — they describe good attention design
regardless of workflow. The 3-agent ceiling, the 90-minute cycle, and the
"wellbeing is the differentiator" framing are removed rather than preserved as history.

## 8. Out of scope / later

- VT gaps: OSC 52 clipboard passthrough, image protocols (Sixel/Kitty/iTerm2), scrollback
  fidelity (DECSTBM sub-regions, alt-screen history wipe, 5000-line cap).
- Keybinding configurability.
- Non-claude agent programs as a first-class product surface (the plumbing exists via
  `AgentProgram`; productizing it is separate).
- Session search/filter (revisit if lists grow past a screen in practice).

## 9. Open questions

Each has a recommendation baked into this doc; the owner can overrule before the relevant phase.

1. **Merged-session hygiene** — *default taken:* badge + manual `X` with a row hint. Alternative:
   config-gated auto-cleanup on merge. (Phase 3)
2. **Docs philosophy tone** — *default taken:* keep design taste (signal budget, batch over
   stream), drop the BCG dogma. Alternative: drop all research framing, or preserve it as a
   history section. (Phase 5)
3. **Checkout-session safety posture** — *default taken:* distinct tag + accent, one per repo,
   cleanup guard; permissions behave exactly like plain `claude`. Open variant: force
   `BypassPermissions` off for checkout sessions even when enabled globally. (Phase 1/2)
4. **Flat vs repo-grouped list** — recommendation: repo-grouped (preserves repo-header
   interactions and the "which repo does `n` target" answer). (Phase 2)
5. **Plan drafting from inside an existing session** — `DraftRequest.Cwd` already supports
   drafting against any directory mid-conversation; recommendation: yes, as a Phase 3 stretch.
6. **AI verdicts on uncommitted work** — recommendation: disabled (mode 3 is manual review);
   revisit if per-file verdicts prove useful. (Phase 4)
