# In-Place Agent Interaction Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace the fullscreen focus view with in-place interactive mode — right arrow enters the preview terminal, enter/esc returns to sidebar navigation.

**Architecture:** Add a `panelFocus` field to `dashboardModel` that toggles between `focusList` (sidebar navigation) and `focusTerminal` (keys forwarded to agent). The existing `ViewFocus` / `focus.go` is deleted entirely. App-level action keys (n, d, x, m, q) are gated behind a `focusList` check. Dashboard handles all key forwarding internally.

**Tech Stack:** Go 1.25, Bubble Tea v2 (`charm.land/bubbletea/v2`), Lipgloss (`github.com/charmbracelet/lipgloss`), x/vt (`github.com/charmbracelet/x/vt`)

---

### Task 1: Delete ViewFocus and clean up references

Remove the fullscreen focus view entirely. After this task, `enter` on the dashboard is a no-op, and `focus.go` is gone.

**Files:**
- Delete: `internal/tui/focus.go`
- Modify: `internal/tui/keymap.go`
- Modify: `internal/tui/app.go`
- Modify: `internal/tui/statusbar.go`

- [ ] **Step 1: Delete focus.go**

```bash
git rm internal/tui/focus.go
```

- [ ] **Step 2: Remove ViewFocus from keymap.go**

In `internal/tui/keymap.go`, replace:
```go
const (
	ViewDashboard ViewMode = iota
	ViewFocus
	ViewDiff
	ViewPrompt  // overlay
	ViewMerge   // overlay
)
```

With:
```go
const (
	ViewDashboard ViewMode = iota
	ViewDiff
	ViewPrompt  // overlay
	ViewMerge   // overlay
)
```

- [ ] **Step 3: Remove focus field from App struct in app.go**

In `internal/tui/app.go`, remove the `focus focusModel` line from the `App` struct:

```go
// Before:
type App struct {
	manager   *agent.Manager
	repoPath  string

	view      ViewMode
	dashboard dashboardModel
	focus     focusModel
	diff      diffModel
	prompt    promptModel
	merge     mergeModel

	width       int
	height      int
	err         string
	errTicks    int
	confirmQuit bool
}

// After:
type App struct {
	manager   *agent.Manager
	repoPath  string

	view      ViewMode
	dashboard dashboardModel
	diff      diffModel
	prompt    promptModel
	merge     mergeModel

	width       int
	height      int
	err         string
	errTicks    int
	confirmQuit bool
}
```

- [ ] **Step 4: Remove focus resize logic from WindowSizeMsg handler in app.go**

Replace this block in the `WindowSizeMsg` case:
```go
// Before:
if a.view == ViewFocus && a.focus.agent != nil {
    a.focus.agent.Resize(msg.Height, msg.Width)
} else if a.view == ViewDashboard {
    a.resizeAllForDashboard()
}

// After:
if a.view == ViewDashboard {
    a.resizeAllForDashboard()
}
```

- [ ] **Step 5: Remove enter→focus binding and focusExitMsg from updateDashboard in app.go**

Delete the `"enter"` case from the key switch in `updateDashboard`:
```go
// Delete this block entirely:
case "enter":
    if ag := a.dashboard.selectedAgent(); ag != nil {
        a.view = ViewFocus
        a.focus = newFocusModel(ag)
        a.focus.width = a.width
        a.focus.height = a.height
        ag.Resize(a.height, a.width)
        return a, nil
    }
```

- [ ] **Step 6: Remove ViewFocus case from View() in app.go**

Delete this block from the `switch a.view` in `View()`:
```go
// Delete this block entirely:
case ViewFocus:
    body := a.focus.View()
    statusbar := renderStatusBar(focusHints, a.width)
    content = lipgloss.JoinVertical(lipgloss.Left, body, statusbar)
```

- [ ] **Step 7: Remove focusHints from statusbar.go**

In `internal/tui/statusbar.go`, delete the `focusHints` variable from the `var` block:
```go
// Delete these lines:
focusHints = []keyHint{
    {"ctrl+b", "back"},
}
```

- [ ] **Step 8: Remove unused focusExitMsg type**

The `focusExitMsg` type was defined in `focus.go` which is now deleted. Verify it's gone — it should be, since it only lived in `focus.go`. If there are any lingering references in `app.go` (there shouldn't be after Steps 5-6), remove them.

- [ ] **Step 9: Build to verify no compilation errors**

```bash
go build ./...
```

Expected: no output (clean build).

- [ ] **Step 10: Run tests**

```bash
go test -race ./...
```

Expected: all tests pass.

- [ ] **Step 11: Commit**

```bash
git add internal/tui/keymap.go internal/tui/app.go internal/tui/statusbar.go
git commit -m "refactor: remove ViewFocus and fullscreen focus mode"
```

---

### Task 2: Add panelFocus type and field to dashboardModel

Introduce the `panelFocus` enum and add it to `dashboardModel`. No behavior changes yet.

**Files:**
- Modify: `internal/tui/keymap.go`
- Modify: `internal/tui/dashboard.go`

- [ ] **Step 1: Add panelFocus type to keymap.go**

Append after the existing `ViewMode` constants block in `internal/tui/keymap.go`:

```go
// panelFocus tracks which dashboard panel has keyboard focus.
type panelFocus int

const (
	focusList     panelFocus = iota // sidebar: j/k navigate agents
	focusTerminal                    // preview: keys forwarded to agent
)
```

- [ ] **Step 2: Add panelFocus field to dashboardModel in dashboard.go**

```go
// Before:
type dashboardModel struct {
	agents   []*agent.Agent
	selected int
	width    int
	height   int
}

// After:
type dashboardModel struct {
	agents     []*agent.Agent
	selected   int
	width      int
	height     int
	panelFocus panelFocus
}
```

- [ ] **Step 3: Build to verify**

```bash
go build ./...
```

Expected: no output.

- [ ] **Step 4: Run tests**

```bash
go test -race ./...
```

Expected: all pass.

- [ ] **Step 5: Commit**

```bash
git add internal/tui/keymap.go internal/tui/dashboard.go
git commit -m "feat: add panelFocus type and field to dashboardModel"
```

---

### Task 3: Implement panel switching and key forwarding

Wire up the actual behavior: right arrow enters focusTerminal, esc exits, enter forwards to agent + exits.

**Files:**
- Modify: `internal/tui/dashboard.go`
- Modify: `internal/tui/app.go`
- Modify: `internal/tui/app_test.go`

- [ ] **Step 1: Write failing tests first**

Add to `internal/tui/app_test.go`:

```go
func TestPanelFocusSwitching(t *testing.T) {
	dir, err := os.MkdirTemp("", "refrain-focus-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)

	run := func(args ...string) {
		cmd := exec.Command(args[0], args[1:]...)
		cmd.Dir = dir
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("cmd %v: %v\n%s", args, err, out)
		}
	}
	run("git", "init")
	run("git", "commit", "--allow-empty", "-m", "init")

	mgr := agent.NewManager(dir)
	defer mgr.Shutdown()

	app := NewApp()
	app.width = 120
	app.height = 40
	app.dashboard.width = 120
	app.dashboard.height = 39
	app.manager = mgr
	app.repoPath = dir

	app = createAgentViaPrompt(t, app, "focus-test", "do stuff")
	if len(app.dashboard.agents) == 0 {
		t.Fatal("Expected at least one agent")
	}

	// Initially in focusList
	if app.dashboard.panelFocus != focusList {
		t.Fatalf("Expected focusList initially, got %v", app.dashboard.panelFocus)
	}

	// Right arrow enters focusTerminal
	model, _ := app.Update(tea.KeyPressMsg{Code: tea.KeyRight})
	app = model.(App)
	if app.dashboard.panelFocus != focusTerminal {
		t.Fatalf("Expected focusTerminal after →, got %v", app.dashboard.panelFocus)
	}

	// Esc returns to focusList
	model, _ = app.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	app = model.(App)
	if app.dashboard.panelFocus != focusList {
		t.Fatalf("Expected focusList after esc, got %v", app.dashboard.panelFocus)
	}

	// Right arrow again, then enter returns to focusList
	model, _ = app.Update(tea.KeyPressMsg{Code: tea.KeyRight})
	app = model.(App)
	model, _ = app.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	app = model.(App)
	if app.dashboard.panelFocus != focusList {
		t.Fatalf("Expected focusList after enter, got %v", app.dashboard.panelFocus)
	}
}

func TestActionKeysBlockedInFocusTerminal(t *testing.T) {
	dir, err := os.MkdirTemp("", "refrain-block-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)

	run := func(args ...string) {
		cmd := exec.Command(args[0], args[1:]...)
		cmd.Dir = dir
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("cmd %v: %v\n%s", args, err, out)
		}
	}
	run("git", "init")
	run("git", "commit", "--allow-empty", "-m", "init")

	mgr := agent.NewManager(dir)
	defer mgr.Shutdown()

	app := NewApp()
	app.width = 120
	app.height = 40
	app.dashboard.width = 120
	app.dashboard.height = 39
	app.manager = mgr
	app.repoPath = dir

	app = createAgentViaPrompt(t, app, "block-test", "do stuff")

	// Enter focusTerminal
	model, _ := app.Update(tea.KeyPressMsg{Code: tea.KeyRight})
	app = model.(App)
	if app.dashboard.panelFocus != focusTerminal {
		t.Fatalf("Expected focusTerminal after →")
	}

	// Press "n" — should be forwarded to agent, NOT open prompt overlay
	// panelFocus must stay focusTerminal (n is not enter/esc)
	model, _ = app.Update(tea.KeyPressMsg{Code: 'n', Text: "n"})
	app = model.(App)
	if app.view != ViewDashboard {
		t.Fatalf("Expected ViewDashboard (n forwarded to agent, not prompt), got %v", app.view)
	}
	if app.dashboard.panelFocus != focusTerminal {
		t.Fatalf("Expected focusTerminal to persist after 'n', got %v", app.dashboard.panelFocus)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

```bash
go test -race ./internal/tui/... -run "TestPanelFocusSwitching|TestActionKeysBlockedInFocusTerminal" -v
```

Expected: FAIL — `focusTerminal` constant exists but `→` doesn't switch to it yet.

- [ ] **Step 3: Add xvt import to dashboard.go**

In `internal/tui/dashboard.go`, update the import block:
```go
import (
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/lipgloss"
	"github.com/devenjarvis/refrain/internal/agent"
	xvt "github.com/charmbracelet/x/vt"
)
```

- [ ] **Step 4: Replace dashboard.Update with panel-aware version**

Replace the entire `Update` method in `internal/tui/dashboard.go`:

```go
func (d dashboardModel) Update(msg tea.Msg) (dashboardModel, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyPressMsg:
		if d.panelFocus == focusTerminal {
			ag := d.selectedAgent()
			switch msg.String() {
			case "esc":
				d.panelFocus = focusList
			case "enter":
				if ag != nil {
					ag.SendKey(xvt.KeyPressEvent(msg))
				}
				d.panelFocus = focusList
			default:
				if ag != nil {
					ag.SendKey(xvt.KeyPressEvent(msg))
				}
			}
			return d, nil
		}

		// focusList mode
		switch msg.String() {
		case "j", "down":
			if d.selected < len(d.agents)-1 {
				d.selected++
			}
		case "k", "up":
			if d.selected > 0 {
				d.selected--
			}
		case "right":
			if d.selectedAgent() != nil {
				d.panelFocus = focusTerminal
			}
		}
	}
	return d, nil
}
```

- [ ] **Step 5: Gate app-level action keys behind focusList in app.go**

In `internal/tui/app.go`, update `updateDashboard` to skip app-level key handling when in focusTerminal. Add the guard at the top of the `tea.KeyPressMsg` case, before the quit/action key switch:

```go
func (a App) updateDashboard(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyPressMsg:
		// When the terminal panel has focus, skip all app-level bindings.
		// dashboard.Update handles key forwarding to the agent.
		if a.dashboard.panelFocus == focusTerminal {
			break
		}

		switch msg.String() {
		case "q", "ctrl+c":
			if a.manager != nil && a.manager.AgentCount() > 0 && !a.confirmQuit {
				a.confirmQuit = true
				return a, nil
			}
			if a.manager != nil {
				a.manager.Shutdown()
			}
			return a, tea.Quit
		default:
			a.confirmQuit = false
		}

		switch msg.String() {
		case "n":
			a.view = ViewPrompt
			a.prompt = newPromptModel()
			a.prompt.width = a.width
			a.prompt.height = a.height
			return a, nil
		case "d":
			if ag := a.dashboard.selectedAgent(); ag != nil {
				rawDiff, err := git.Diff(a.repoPath, ag.Worktree)
				if err != nil {
					a.setError(err.Error())
					return a, nil
				}
				if rawDiff == "" {
					a.setError("No changes yet")
					return a, nil
				}
				a.view = ViewDiff
				a.diff = newDiffModel(rawDiff, a.width, a.height-1)
				return a, nil
			}
		case "x":
			if ag := a.dashboard.selectedAgent(); ag != nil {
				if err := a.manager.Kill(ag.ID); err != nil {
					a.setError(err.Error())
				}
				a.refreshAgentList()
				if a.dashboard.selected >= len(a.dashboard.agents) && a.dashboard.selected > 0 {
					a.dashboard.selected--
				}
				return a, nil
			}
		case "m":
			if ag := a.dashboard.selectedAgent(); ag != nil {
				if ag.Status() != agent.StatusDone && ag.Status() != agent.StatusIdle {
					a.setError("Agent must be done or idle to merge")
					return a, nil
				}
				a.view = ViewMerge
				a.merge = newMergeModel(ag.Name, ag.Worktree.Branch, ag.Worktree.BaseBranch)
				a.merge.width = a.width
				a.merge.height = a.height
				return a, nil
			}
		}
	}

	prevSelected := a.dashboard.selected
	var cmd tea.Cmd
	a.dashboard, cmd = a.dashboard.Update(msg)
	if a.dashboard.selected != prevSelected {
		a.resizeSelectedForDashboard()
	}
	return a, cmd
}
```

- [ ] **Step 6: Run tests to verify they pass**

```bash
go test -race ./internal/tui/... -v
```

Expected: all tests PASS including the two new ones.

- [ ] **Step 7: Commit**

```bash
git add internal/tui/dashboard.go internal/tui/app.go internal/tui/app_test.go
git commit -m "feat: implement in-place panel focus switching with key forwarding"
```

---

### Task 4: Visual feedback — preview border and statusbar hints

Add the colored border on the preview panel when in focusTerminal, and swap statusbar hints based on panel focus.

**Files:**
- Modify: `internal/tui/statusbar.go`
- Modify: `internal/tui/app.go`
- Modify: `internal/tui/dashboard.go`

- [ ] **Step 1: Add focusTerminalHints to statusbar.go**

In `internal/tui/statusbar.go`, add `focusTerminalHints` after `diffHints` in the `var` block:

```go
var (
	dashboardHints = []keyHint{
		{"j/k", "navigate"},
		{"→", "interact"},
		{"n", "new"},
		{"d", "diff"},
		{"x", "kill"},
		{"m", "merge"},
		{"q", "quit"},
	}

	focusTerminalHints = []keyHint{
		{"enter", "send"},
		{"esc", "back"},
	}

	diffHints = []keyHint{
		{"j/k", "scroll"},
		{"q/esc", "back"},
	}
)
```

Note: also update `dashboardHints` to replace `{"enter", "focus"}` with `{"→", "interact"}` since `enter` no longer does anything and `→` is the new way to interact.

- [ ] **Step 2: Update View() in app.go to switch hints based on panelFocus**

In the `ViewDashboard` case of `View()` in `internal/tui/app.go`:

```go
// Before:
case ViewDashboard:
    body := a.dashboard.View()
    statusbar := renderStatusBar(dashboardHints, a.width)
    content = lipgloss.JoinVertical(lipgloss.Left, body, statusbar)

// After:
case ViewDashboard:
    body := a.dashboard.View()
    hints := dashboardHints
    if a.dashboard.panelFocus == focusTerminal {
        hints = focusTerminalHints
    }
    statusbar := renderStatusBar(hints, a.width)
    content = lipgloss.JoinVertical(lipgloss.Left, body, statusbar)
```

- [ ] **Step 3: Add colored border on preview panel when focusTerminal in dashboard.go**

In `internal/tui/dashboard.go`, update the `View()` method. Replace the `previewStyle` definition:

```go
// Before:
previewStyle := lipgloss.NewStyle().
    Width(previewWidth).
    Height(d.contentHeight())

// After:
previewStyle := lipgloss.NewStyle().
    Width(previewWidth).
    Height(d.contentHeight())
if d.panelFocus == focusTerminal {
    // Subtract 2 from width and height to account for the 1-char border on each side,
    // keeping the terminal content the same rendered size (no reflow).
    previewStyle = lipgloss.NewStyle().
        Width(previewWidth - 2).
        Height(d.contentHeight() - 2).
        Border(lipgloss.NormalBorder()).
        BorderForeground(ColorSecondary)
}
```

- [ ] **Step 4: Build to verify**

```bash
go build ./...
```

Expected: no output.

- [ ] **Step 5: Run all tests**

```bash
go test -race ./...
```

Expected: all pass.

- [ ] **Step 6: Commit**

```bash
git add internal/tui/statusbar.go internal/tui/app.go internal/tui/dashboard.go
git commit -m "feat: add visual feedback for focusTerminal panel (border + statusbar hints)"
```

---

## Self-Review Notes

- `xvt.KeyPressEvent(msg)` cast works because `tea.KeyPressMsg` and `xvt.KeyPressEvent` share the same underlying `ultraviolet.Key` struct — same pattern used in the now-deleted `focus.go`.
- `tea.KeyRight` and `tea.KeyEscape` / `tea.KeyEnter` are the correct Bubble Tea v2 key code constants (same as `tea.KeyTab` used in existing tests).
- Border compensation in Task 4 Step 3 (subtracting 2 from width/height) is important — without it the agent VT renders at a different width than it was sized for, causing wrapping artifacts.
- `dashboardHints` is updated in Task 4 Step 1 to remove the `enter → focus` hint (deleted in Task 1) and add `→ interact`.
