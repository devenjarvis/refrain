# CONVENTIONS.md

Architecture and code conventions for Refrain (Go 1.25 + Bubble Tea v2).

**This is the "how to build it" doc.** `CLAUDE.md` describes *what the system does*; this describes *how the code must be structured*. When they disagree about structure, this file wins.

## How to use this doc

- Rules are **mandatory** unless a rule says otherwise. They are written as imperatives so an agent can follow them mechanically.
- Blocks marked **TARGET** describe the aspirational architecture. Today's code diverges from these in places. When you touch diverging code, move it *toward* the target — never away from it. Do not add new code that violates a TARGET.
- When a rule and existing code conflict, follow the rule and note the divergence. Do not copy a nearby violation as precedent ("the file already does X" is not a license to do more X).
- If a change cannot follow a rule, say so explicitly in the PR and explain why. Silent divergence is the thing this doc exists to stop.

---

## 1. The one architecture

Refrain's UI is a **tree of components**. There is exactly one shape:

```
App (root)
├── domain state  ── the single source of truth (sessions, config, caches)
├── global Cmd fan-out ── window-size, tick, agent events
└── active Component ── the one screen the user is looking at
    └── child Components (panels, modals, pickers)
```

**Rules**

- The root `App` holds three things and nothing else conceptually: (a) shared **domain state**, (b) the wiring to fan out **global messages**, (c) the **active component** (plus the small stack of components needed to render it, e.g. an open modal over a screen).
- Everything the user sees is a **Component** (§3). Screens, panels, modals, pickers, forms — all components. There is no "special" rendering that lives directly on `App` outside a component.
- Business/domain logic lives in domain packages (`agent`, `git`, `github`, `config`, `hook`, `diffmodel`, …), **never** in components. A component decides *what to show and which command to fire*; it does not decide *how a worktree is created* or *how a PR merges*.
- **TARGET:** the root `App` is thin. A 150-field root model is the anti-pattern this architecture exists to kill. New per-screen state goes on the screen's component, not on `App`.

---

## 2. Layering & dependency direction

```
cmd/  ──────────────►  internal/tui/  ──────────────►  internal/<domain>/
(wires concretes)      (presentation)                  (agent, git, github,
                                                         config, hook, vt, …)
```

**Rules**

- Domain packages **must not import `internal/tui`**. Ever. The dependency arrow points one way: presentation depends on domain.
- `tui` depends on domain packages through **interfaces it defines** (e.g. `SessionManager` in `manager_iface.go`), not concrete types, wherever the dependency needs to be faked in a test.
- `cmd/` is the **only** place concrete types are wired together (construct the real `Manager`, inject it into the `App`). Keep wiring out of constructors that tests need to call.
- No import cycles. If two packages need each other, the shared thing belongs in a third package or behind an interface defined by the consumer.
- A domain package must compile and be testable without a terminal.
- These dependency rules are mechanically enforced by `.go-arch-lint.yml`; changing them requires editing that file and is reviewed in PRs. Adding a new `internal/tui/foo/` sub-package also requires a new component entry there.

---

## 3. The component contract

**TARGET.** Every screen, panel, modal, and picker implements one interface:

```go
// Component is the contract every visible unit implements.
type Component interface {
    // Update handles a message and returns the next state of this component
    // plus any command to run. It must be pure w.r.t. I/O: side effects go in
    // the returned Cmd, never inline.
    Update(tea.Msg) (Component, tea.Cmd)

    // View renders the component within its current size. Pure: no mutation,
    // no I/O, deterministic from state. See §5.
    View() string

    // SetSize informs the component of the space it has to render in.
    // The component must render within (w, h) and be safe at minimum size.
    SetSize(w, h int)
}
```

**Rules**

- **Value receivers, value returns.** `Update` returns a `Component`, not a mutated pointer. State transitions are explicit: `m.foo = x; return m, cmd`. This makes "what changed" reviewable in one place and keeps components copy-safe.
- A component **owns 100% of its own state**. No other component (including the parent) reads or writes its fields directly. If a parent needs a value out of a child, the child exposes a method.
- A component never reads global terminal size. It renders within the `(w, h)` it was given via `SetSize`. (§5)
- **One component per concept.** Don't fold two screens into one model with a mode flag; make two components and let the parent switch between them.
- Constructors are `New<Name>(...)` and return the concrete component fully initialized. No half-built components that require a later setter to be valid.

---

## 4. Message & Update rules

> Your #1 bug source. These rules are strict on purpose.

**Ownership**

- Every `Msg` type is **owned by exactly one component** and defined in that component's file (next to the handler that consumes it).
- A given `Msg` type is **handled in exactly one place**. If you find yourself handling the same message in two `Update`s, the state is in the wrong place — move it.
- Naming: `<noun><Verb>Msg` — `planEditorApproveMsg`, `prPollMsg`, `reviewVerdictMsg`. Keep them unexported unless a sibling package must construct them.

**Routing**

- The root routes by **active component**: it forwards the message down, gets back `(Component, Cmd)`, stores the component, returns the `Cmd`. A parent **forwards or handles** — it never *reimplements* a child's logic.
- **Global messages** are broadcast by the root to whoever needs them. The global set is fixed and small: `tea.WindowSizeMsg`, the app tick, and `agentEventMsg` (manager events). Everything else is **local** to one component.
- Adding a new global message is a deliberate decision — justify it in the PR. The default for a new message is **local**.

**Side effects & async**

- **All** side effects and async work happen inside a `tea.Cmd` that returns a `Msg`. A goroutine **never** mutates model state — it produces a message and the `Update` applies it. This is the single most important rule in this file.
- A `Cmd` returns exactly one `Msg` describing its result (including the error case). Don't fire-and-forget work whose result the UI depends on.

```go
// GOOD: side effect in a Cmd, result threaded back as a Msg.
func mergePRCmd(client *github.Client, id string) tea.Cmd {
    return func() tea.Msg {
        err := client.MergePR(id)
        return mergePRMsg{sessionID: id, err: err}   // success AND failure
    }
}

// BAD: goroutine mutates state and/or swallows the result.
go func() { a.session.merged = client.MergePR(id) == nil }()
```

**Update size**

- **TARGET.** No single `Update` is a 600-line switch. If a component's `Update` grows past comprehension, that's a signal it owns too much state — split the component, not just the file. Mechanical file-splitting (`update_*.go`) is allowed but is a smell when used to hide an oversized model.

---

## 5. Rendering rules

> Your #2 bug source.

**Purity**

- `View()` is **pure**: deterministic from the component's state, **zero mutation**, **zero I/O**. If `View` needs a value, that value is already in the model (put there by an `Update`), not computed by reading a file/socket/clock at render time.
- A render bug should always be reproducible from model state alone. If you can't reproduce a wrong render by constructing the model, `View` is impure — fix that first.

**Single source of truth**

- `View` reads model fields. It does **not** keep a second, derived copy of state that can drift from the source. Derive at render time from the one source, or store the derived value and update it in `Update` — never both.
- **TARGET — no mirror fields.** Do not duplicate one piece of state onto two structs and sync them (the `syncModalsToDashboard` pattern is the canonical anti-example). One owner, one field. If two views need it, the parent owns it and passes it down, or the child exposes a getter.

**Size & layout**

- A component renders within the `(w, h)` from `SetSize`. It never assumes 80×24, never reads the OS terminal, and must not panic or overflow at the **minimum** supported size.
- Layout arithmetic (splitting width across columns, reserving header/footer rows) goes through **shared layout helpers**, not ad-hoc `w - 4` math scattered across files. A width change should be fixable in one place.
- All strings placed in a fixed-width region must be width-clamped (truncate or wrap) before composition. Never trust content to fit.

**Styles**

- Styles come from **one theme/style registry**: `internal/tui/theme`. Don't scatter `lipgloss.NewStyle().Foreground(...)` literals, raw hex colors, or glyph runes at call sites; reference the named tokens so the palette changes in one place. The registry is a leaf package (no internal deps) so the `diff` and `mdrender` subpackages consume the same tokens. See [`DESIGN.md`](DESIGN.md) for the token catalog and role guidance.
- Color/accent semantics (e.g. the `StatusWaiting` accent) are defined once and reused, not re-picked per component.

---

## 6. State management

- **One domain-state struct is the source of truth** for sessions, config, and caches. Components read from it; transient UI state (cursor, focus, scroll position, in-progress form input) lives on the component.
- **No duplicated/mirror fields** (see §5). The owner is whoever's lifecycle the data follows. If you're tempted to copy a field "so the renderer can see it," expose a method instead.
- **Derive, don't store**, when the derivation is cheap and the inputs are already in the model. Store a derived value only when (a) it's expensive and (b) you update it in the same `Update` that changes its inputs.
- Cursor/focus state has **one** owner (the component that navigates) and a single enum/index — don't track the same cursor in two places (the `focusCursorSection` + per-section index pattern is the model: one section enum, helpers for clamp/hit-test, no parallel bookkeeping).

---

## 7. Concurrency *(codifies what already works)*

- Async = `tea.Cmd` (§4). That is the default and covers almost everything the UI does.
- Long-lived domain goroutines (the hook dispatcher, agent read/write/status loops) are owned by the **domain layer** (`Manager`, `Agent`), tracked in a `WaitGroup`, and shut down in documented order. New long-lived goroutines must register with the owner's lifecycle, not float free.
- Channel sends that race a close use the **send-guard**: `RLock` around the send, `Lock` + flag + `close` in shutdown (`Manager.emit`/`Shutdown`). Don't send on a channel you don't own without this guard.
- Protect mutable shared state with `sync.RWMutex`; read paths take `RLock`. Don't reach for channels where a mutex is simpler, or vice versa — match the existing package's idiom.
- **Always run `go test -race ./...` before committing.** Concurrency bugs here have been real.

---

## 8. Errors

- Wrap with context: `fmt.Errorf("pkg: action: %w", err)`. Keep the `package: action:` prefix style so a stack of wraps reads as a path.
- Use **sentinel errors** (`var ErrFoo = errors.New("...")`) for expected conditions callers branch on; check with `errors.Is`. Don't string-match error text.
- Custom error *types* only when callers need structured fields (rare — e.g. the JSON-RPC error object).
- In the TUI, errors display **transiently** and clear on the next tick. No modal error dialogs. The hook CLI is silent and always exits 0.

---

## 9. Naming & file layout

- Constructors: `New<Type>(...)`. A fully-private package may use bare `New(...)`.
- Interfaces: singular noun + `-er`/`-or` (`SessionManager`, `PlanDrafter`, `BranchNamer`). Define the interface in the **consumer** package when it exists for injection/testing.
- Messages: `<noun><Verb>Msg`, defined with their handler (§4).
- **TARGET — file-per-component with split.** A large component splits into:
  - `<name>_model.go` — the struct, its constructor, `SetSize`, getters.
  - `<name>_update.go` — `Update` and the messages it owns.
  - `<name>_view.go` — `View` and render helpers.

  Small components may keep all three in one `<name>.go`. Do **not** introduce new `xxxmodel.go` + `xxxpanel.go` pairs; standardize on the `_model`/`_update`/`_view` split as files grow.
- Test files sit beside the code as `<name>_test.go`. Property-test helpers as `<name>_prop_helpers_test.go`.

---

## 10. Testing

- **Table-driven by default.** Name cases; one `t.Run` per case.
- **Inject dependencies through interfaces and fakes** so units test without subprocesses or terminals — the `SessionManager` interface and `SetBranchNamer`/`SetPlanDrafter` injection points are the pattern to follow. New external dependencies (subprocess, network, clock) must be injectable the same way.
- **Render tests guard View purity (§5).** A component's `View` should be testable by constructing a model and asserting on the output string. Add render/golden coverage for any component whose layout has bitten you.
- **`e2e` build tag** for integration tests that spawn the real binary (`internal/e2e/`, `//go:build e2e`). Keep them out of the default `go test ./...` path.
- A bug fix lands with a test that fails before the fix and passes after.

---

## 11. Pre-commit checklist

Run before every commit / in every PR:

- [ ] `go test -race ./...` passes (required — not optional).
- [ ] `go vet ./...` clean.
- [ ] `gofmt -w .` applied.
- [ ] `golangci-lint run` clean.
- [ ] `go-arch-lint check` clean (enforces §2 layering rules).
- [ ] Changelog fragment added under `changelog.d/` (`### Added` / `### Fixed` / …).
- [ ] No new field added to a model without a clear single owner (§6).
- [ ] No new message handled in more than one place (§4).
- [ ] No side effect or state mutation inside a goroutine (§4) or inside `View` (§5).
- [ ] Any divergence from a rule above is called out explicitly in the PR.
