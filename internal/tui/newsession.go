package tui

import (
	"math/rand/v2"
	"strings"

	"charm.land/bubbles/v2/textarea"
	tea "charm.land/bubbletea/v2"
	xlipgloss "charm.land/lipgloss/v2"
	"github.com/charmbracelet/lipgloss"

	"github.com/devenjarvis/refrain/internal/config"
)

// sessionOverrides holds per-session model and permission overrides set in the
// new-session form. Fields are zero/nil when no override was specified — a nil
// BypassPermissions means "use the resolved default".
type sessionOverrides struct {
	PlanModel         string
	AgentModel        string
	BypassPermissions *bool
}

// sessionContext selects where a new session's agents run: an isolated
// worktree on a fresh branch (the default, parallel-safe) or the repo's main
// working tree (a checkout session — for debugging and everyday tasks that
// need the user's real state).
type sessionContext int

const (
	contextWorktree sessionContext = iota
	contextCheckout
)

// promptModalSubmitMsg fires when the user accepts the prompt. planFirst is
// true for the ctrl+p plan-drafting path; the default enter path spawns a raw
// claude session immediately (prompt may be empty — a blank REPL).
type promptModalSubmitMsg struct {
	prompt    string
	planFirst bool
	context   sessionContext
	overrides sessionOverrides
}

// promptModalCancelMsg fires on `esc`.
type promptModalCancelMsg struct{}

// overrideFieldKind distinguishes select (cycle through options) from toggle (bool).
type overrideFieldKind int

const (
	overrideFieldSelect overrideFieldKind = iota
	overrideFieldToggle
)

// overrideField represents a single row in the OVERRIDES sidebar panel.
type overrideField struct {
	label       string
	kind        overrideFieldKind
	options     []string // for overrideFieldSelect
	selected    int      // index into options (select) or ignored (toggle)
	toggleValue bool     // for overrideFieldToggle
}

const promptModalCharLimit = 4000

// promptModalTitles rotate through the screen header. Task-neutral: a session
// is a conversation with Claude, not necessarily a build.
var promptModalTitles = []string{
	"What's the task?",
	"What should Claude look at?",
	"What's next?",
}

// promptModalPlaceholders show concrete task shapes inside the textarea —
// deliberately mixed (explore, debug, write, build) so a first-time user
// knows a session doesn't have to end in a PR.
var promptModalPlaceholders = []string{
	"e.g. Explain how auth works in this repo",
	"e.g. Fix the flaky test in foo_test.go",
	"e.g. Draft a ticket for the flaky CI failure",
	"e.g. Add a dark-mode toggle to the settings page",
	"e.g. Review the error handling in internal/git",
	"e.g. Add unit tests for the shutdown sequence",
}

// pickPrompt is the indirection tests use to make rotation deterministic.
var pickPrompt = func(n int) int { return rand.IntN(n) }

const (
	// newSessionSidebarWidth fits the widest field row: cursor (2) + label
	// (20) + space + "< Worktree (new branch) >" (25) = 48, plus slack.
	newSessionSidebarWidth  = 50
	newSessionSidebarMinVP  = 110 // sidebar shown only when viewport width >= this
	newSessionMaxTextareaW  = 120
	newSessionVerticalSlack = 8 // rows consumed by header + title + blank rows + footer
)

// newSessionModel is the full-viewport new-session composition screen.
type newSessionModel struct {
	active         bool
	textarea       textarea.Model
	width          int
	height         int
	returnTo       ViewMode
	titleIdx       int
	placeholderIdx int
	repoName       string
	baseBranch     string

	// Override form state. overrideFocus == -1 means the textarea has focus;
	// >= 0 means that index of overrideFields has focus.
	overrideFields   []overrideField
	overrideFocus    int // -1 = textarea focused
	overrideDefaults sessionOverrides
}

func newNewSessionModel() newSessionModel {
	ta := textarea.New()
	ta.Prompt = ""
	ta.ShowLineNumbers = false
	ta.CharLimit = promptModalCharLimit
	// Strip bubbles' default focused CursorLine background.
	styles := ta.Styles()
	styles.Focused.CursorLine = xlipgloss.NewStyle()
	// ColorPrimary is a v1 lipgloss.Color; it satisfies the v2
	// CursorStyle.Color field (image/color.Color) via its RGBA() method.
	styles.Cursor.Color = ColorPrimary
	ta.SetStyles(styles)
	// Extend InsertNewline to include ctrl+j and alt+enter so newlines work
	// on terminals that don't disambiguate shift+enter. The Update method
	// intercepts plain "enter" for submit before the textarea sees it, so
	// listing "enter" here is safe — ctrl+j / shift+enter never reach the
	// submit branch.
	ta.KeyMap.InsertNewline.SetKeys("enter", "ctrl+m", "ctrl+j", "shift+enter", "alt+enter")
	return newSessionModel{textarea: ta, overrideFocus: -1}
}

// SetSize updates the model's understanding of the terminal dimensions.
func (m *newSessionModel) SetSize(w, h int) {
	m.width = w
	m.height = h
	m.textarea.SetWidth(m.textareaWidth())
	m.textarea.SetHeight(m.textareaHeight())
}

// SetDefaults seeds the override form's default values from resolved repo
// settings. Called by openNewSession before Open so the form reflects what
// would be used absent any override. Stores both the displayed state and the
// baseline for equality detection on submit (override == default ⇒ no-override).
func (m *newSessionModel) SetDefaults(resolved config.ResolvedSettings) {
	m.overrideDefaults = sessionOverrides{
		PlanModel:         resolved.PlanModel,
		AgentModel:        resolved.AgentModel,
		BypassPermissions: &resolved.BypassPermissions,
	}
	m.buildOverrideFields()
}

// contextOptions are the display labels for the Context field, indexed by
// sessionContext.
var contextOptions = []string{"Worktree (new branch)", "Current checkout"}

// buildOverrideFields reconstructs the sidebar field rows from
// overrideDefaults. The Context row leads (it changes where the session runs);
// the remaining rows are per-session overrides. Called by SetDefaults and Open
// so the form always reflects the latest defaults.
func (m *newSessionModel) buildOverrideFields() {
	planModelSel := optionIndex(config.KnownModels, m.overrideDefaults.PlanModel)
	agentModelSel := optionIndex(config.KnownAgentModels, m.overrideDefaults.AgentModel)
	bypassVal := false
	if m.overrideDefaults.BypassPermissions != nil {
		bypassVal = *m.overrideDefaults.BypassPermissions
	}
	m.overrideFields = []overrideField{
		{label: "Context", kind: overrideFieldSelect, options: contextOptions, selected: int(contextWorktree)},
		{label: "Plan Model", kind: overrideFieldSelect, options: config.KnownModels, selected: planModelSel},
		{label: "Agent Model", kind: overrideFieldSelect, options: config.KnownAgentModels, selected: agentModelSel},
		{label: "Bypass Permissions", kind: overrideFieldToggle, toggleValue: bypassVal},
	}
}

// selectedContext returns the Context row's current value.
func (m *newSessionModel) selectedContext() sessionContext {
	for _, f := range m.overrideFields {
		if f.label == "Context" {
			return sessionContext(f.selected)
		}
	}
	return contextWorktree
}

// Open activates the screen, resets textarea content, and picks a fresh
// title/placeholder pair. returnTo is the ViewMode to restore on cancel.
func (m *newSessionModel) Open(returnTo ViewMode) tea.Cmd {
	m.active = true
	m.returnTo = returnTo
	m.overrideFocus = -1
	m.buildOverrideFields()
	m.textarea.SetValue("")
	m.titleIdx = pickPrompt(len(promptModalTitles))
	m.placeholderIdx = pickPrompt(len(promptModalPlaceholders))
	m.textarea.Placeholder = promptModalPlaceholders[m.placeholderIdx]
	m.textarea.SetWidth(m.textareaWidth())
	m.textarea.SetHeight(m.textareaHeight())
	return m.textarea.Focus()
}

// Close deactivates the screen and blurs the textarea.
func (m *newSessionModel) Close() {
	m.active = false
	m.textarea.Blur()
}

// buildSubmitOverrides computes the sessionOverrides to attach to the submit
// message. A field value equal to its resolved default is treated as "not
// overridden" (empty string / nil pointer) so submitPromptModal can fall back
// to the resolved value cleanly.
func (m *newSessionModel) buildSubmitOverrides() sessionOverrides {
	var over sessionOverrides
	for _, f := range m.overrideFields {
		switch f.label {
		case "Plan Model":
			val := ""
			if len(f.options) > 0 {
				val = f.options[f.selected]
			}
			def := m.overrideDefaults.PlanModel
			if val != def {
				over.PlanModel = val
			}
		case "Agent Model":
			val := ""
			if len(f.options) > 0 {
				val = f.options[f.selected]
			}
			def := m.overrideDefaults.AgentModel
			if val != def {
				over.AgentModel = val
			}
		case "Bypass Permissions":
			defVal := false
			if m.overrideDefaults.BypassPermissions != nil {
				defVal = *m.overrideDefaults.BypassPermissions
			}
			if f.toggleValue != defVal {
				v := f.toggleValue
				over.BypassPermissions = &v
			}
		}
	}
	return over
}

// Update routes a tea.Msg. Intercepts focus-navigation (tab/shift+tab),
// override-field interaction (enter/space/left/right/h/l) when focus is on a
// form row, and esc/enter/ctrl+enter for cancel/submit from the textarea.
// ctrl+j/shift+enter/alt+enter still reach the textarea for newline insertion.
func (m newSessionModel) Update(msg tea.Msg) (newSessionModel, tea.Cmd) {
	if key, ok := msg.(tea.KeyPressMsg); ok {
		switch key.String() {
		case "esc":
			// Cancel from any focus state.
			m.Close()
			return m, func() tea.Msg { return promptModalCancelMsg{} }

		case "ctrl+p":
			// Plan-first: draft a plan before any agent spawns. Needs a task
			// description — an empty prompt has nothing to plan.
			val := strings.TrimSpace(m.textarea.Value())
			if val == "" {
				return m, nil
			}
			over := m.buildSubmitOverrides()
			ctx := m.selectedContext()
			m.Close()
			return m, func() tea.Msg {
				return promptModalSubmitMsg{prompt: val, planFirst: true, context: ctx, overrides: over}
			}

		case "tab":
			// Advance focus: textarea → field0 → … → fieldn → textarea.
			n := len(m.overrideFields)
			if n == 0 {
				break
			}
			if m.overrideFocus == -1 {
				m.overrideFocus = 0
				m.textarea.Blur()
			} else if m.overrideFocus < n-1 {
				m.overrideFocus++
			} else {
				m.overrideFocus = -1
				return m, m.textarea.Focus()
			}
			return m, nil

		case "shift+tab":
			// Retreat focus: textarea → fieldn → … → field0 → textarea.
			n := len(m.overrideFields)
			if n == 0 {
				break
			}
			if m.overrideFocus == -1 {
				m.overrideFocus = n - 1
				m.textarea.Blur()
			} else if m.overrideFocus > 0 {
				m.overrideFocus--
			} else {
				m.overrideFocus = -1
				return m, m.textarea.Focus()
			}
			return m, nil
		}

		// Field-level key handling when an override row has focus.
		if m.overrideFocus >= 0 && m.overrideFocus < len(m.overrideFields) {
			f := &m.overrideFields[m.overrideFocus]
			switch key.String() {
			case "enter", "right", "l":
				if f.kind == overrideFieldSelect && len(f.options) > 0 {
					f.selected = (f.selected + 1) % len(f.options)
				} else if f.kind == overrideFieldToggle {
					f.toggleValue = !f.toggleValue
				}
				return m, nil
			case "left", "h":
				if f.kind == overrideFieldSelect && len(f.options) > 0 {
					f.selected = (f.selected - 1 + len(f.options)) % len(f.options)
				}
				return m, nil
			case "space":
				if f.kind == overrideFieldToggle {
					f.toggleValue = !f.toggleValue
				} else if f.kind == overrideFieldSelect && len(f.options) > 0 {
					f.selected = (f.selected + 1) % len(f.options)
				}
				return m, nil
			}
			// Other keys are swallowed while a field has focus.
			return m, nil
		}

		// Textarea-focused submit path: enter starts a raw session. An empty
		// prompt is allowed — it opens a blank claude REPL, the everyday case
		// for debugging and exploring.
		if key.String() == "enter" {
			val := strings.TrimSpace(m.textarea.Value())
			over := m.buildSubmitOverrides()
			ctx := m.selectedContext()
			m.Close()
			return m, func() tea.Msg {
				return promptModalSubmitMsg{prompt: val, context: ctx, overrides: over}
			}
		}
	}
	var cmd tea.Cmd
	m.textarea, cmd = m.textarea.Update(msg)
	return m, cmd
}

// View renders the full-viewport composition screen.
func (m newSessionModel) View() string {
	// Header: "NEW SESSION" left, "repoName · branch" right.
	left := StyleSubtle.Render("NEW SESSION")
	var rightStr string
	if m.repoName != "" && m.baseBranch != "" {
		rightStr = StyleSubtle.Render(m.repoName + " · " + m.baseBranch)
	} else if m.repoName != "" {
		rightStr = StyleSubtle.Render(m.repoName)
	}
	header := rightAlign(left, rightStr, m.width)

	// Rotating title prompt, with the blank-REPL affordance spelled out —
	// an empty submit is a feature, not an error.
	title := StyleTitle.Render(promptModalTitles[m.titleIdx]) + "  " +
		StyleSubtle.Render("(empty ⏎ opens a blank claude session)")

	if m.showSidebar() {
		tw := m.textareaWidth()
		textareaCol := lipgloss.NewStyle().Width(tw).Render(m.textarea.View())
		sidebar := m.renderSidebar()
		body := lipgloss.JoinHorizontal(lipgloss.Top, textareaCol, "  ", sidebar)
		out := lipgloss.JoinVertical(lipgloss.Left, header, "", title, "", body)
		return fillHeight(out, m.width, m.height)
	}

	out := lipgloss.JoinVertical(lipgloss.Left, header, "", title, "", m.textarea.View())
	return fillHeight(out, m.width, m.height)
}

func (m *newSessionModel) showSidebar() bool {
	return m.width >= newSessionSidebarMinVP
}

func (m *newSessionModel) textareaWidth() int {
	w := m.width
	if m.showSidebar() {
		w = w - newSessionSidebarWidth - 4 // sidebar + padding
	} else {
		w = w - 4
	}
	if w > newSessionMaxTextareaW {
		w = newSessionMaxTextareaW
	}
	if w < 10 {
		w = 10
	}
	return w
}

func (m *newSessionModel) textareaHeight() int {
	h := m.height - newSessionVerticalSlack
	if h < 3 {
		h = 3
	}
	return h
}

func (m *newSessionModel) renderSidebar() string {
	labelW := 20 // fixed label width keeps value column aligned
	labelStyle := lipgloss.NewStyle().Width(labelW).Foreground(ColorText)
	focusedLabelStyle := StyleActive.Bold(true).Width(labelW)
	toggleOn := StyleSuccess.Render("[x]")
	toggleOff := StyleSubtle.Render("[ ]")

	renderRow := func(i int, f overrideField) string {
		cursor := "  "
		ls := labelStyle
		if i == m.overrideFocus {
			cursor = StyleActive.Render("> ")
			ls = focusedLabelStyle
		}
		label := ls.Render(f.label)
		var value string
		switch f.kind {
		case overrideFieldSelect:
			chevronStyle := StyleSubtle
			if i == m.overrideFocus {
				chevronStyle = StyleActive
			}
			opt := ""
			if len(f.options) > 0 {
				opt = f.options[f.selected]
			}
			if opt == "" {
				opt = "(default)"
			}
			value = chevronStyle.Render("< ") + opt + chevronStyle.Render(" >")
		case overrideFieldToggle:
			if f.toggleValue {
				value = toggleOn
			} else {
				value = toggleOff
			}
		}
		return cursor + label + " " + value
	}

	// Row 0 (Context) gets its own section: it changes where the session
	// runs, not just which model runs it.
	ctxRows := []string{StyleSubtle.Render("CONTEXT")}
	overRows := []string{StyleSubtle.Render("OVERRIDES")}
	for i, f := range m.overrideFields {
		if f.label == "Context" {
			ctxRows = append(ctxRows, renderRow(i, f))
		} else {
			overRows = append(overRows, renderRow(i, f))
		}
	}
	ctxBlock := lipgloss.JoinVertical(lipgloss.Left, ctxRows...)
	if m.selectedContext() == contextCheckout {
		ctxBlock = lipgloss.JoinVertical(lipgloss.Left, ctxBlock,
			StyleWarning.Render("  runs in your real working tree"))
	}
	overBlock := lipgloss.JoinVertical(lipgloss.Left, overRows...)

	return lipgloss.JoinVertical(lipgloss.Left, ctxBlock, "", overBlock)
}
