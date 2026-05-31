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

// promptModalSubmitMsg fires when the user accepts the prompt. SkipPlanning
// is true for the ctrl+enter "do today's flow" path, false for the default
// enter path that runs through the plan-first drafting/editor flow.
type promptModalSubmitMsg struct {
	prompt       string
	skipPlanning bool
	overrides    sessionOverrides
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

// promptModalTitles rotate through the screen header. Short and imperative,
// each one nudges toward Refrain's one-primary-goal-per-block frame.
var promptModalTitles = []string{
	"What do you want to build?",
	"What's today's primary goal?",
	"What's the one thing?",
	"What does done look like?",
	"What's the next move?",
	"What should we ship?",
	"What would make this block count?",
	"Define the goal.",
}

// promptModalPlaceholders show concrete task shapes inside the textarea so
// a first-time user has a model of what a planner-friendly prompt looks like.
var promptModalPlaceholders = []string{
	"e.g. Add a dark-mode toggle to the settings page",
	"e.g. Fix the flaky test in foo_test.go",
	"e.g. Migrate auth from sessions to JWT",
	"e.g. Wire up file upload to S3",
	"e.g. Add unit tests for the shutdown sequence",
	"e.g. Refactor the dashboard render path",
}

// pickPrompt is the indirection tests use to make rotation deterministic.
var pickPrompt = func(n int) int { return rand.IntN(n) }

const (
	// newSessionSidebarWidth matches the dashboard sidebar (defaultSidebarWidth)
	// so the new-session form lines up with the pipeline view behind it.
	newSessionSidebarWidth  = defaultSidebarWidth
	newSessionSidebarMinVP  = 110 // sidebar shown only when viewport width >= this
	newSessionMaxTextareaW  = 120
	newSessionVerticalSlack = 8 // rows consumed by header + title + blank rows + footer
)

// newSessionModel is the full-viewport new-session composition screen.
// It replaces the old centered overlay modal when PlanFirstEnabled is on.
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
	overrideFields  []overrideField
	overrideFocus   int // -1 = textarea focused
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

// buildOverrideFields reconstructs the overrideFields slice from overrideDefaults.
// Called by SetDefaults and Open so the form always reflects the latest defaults.
func (m *newSessionModel) buildOverrideFields() {
	planModelSel := optionIndex(config.KnownModels, m.overrideDefaults.PlanModel)
	agentModelSel := optionIndex(config.KnownAgentModels, m.overrideDefaults.AgentModel)
	bypassVal := false
	if m.overrideDefaults.BypassPermissions != nil {
		bypassVal = *m.overrideDefaults.BypassPermissions
	}
	m.overrideFields = []overrideField{
		{label: "Plan Model", kind: overrideFieldSelect, options: config.KnownModels, selected: planModelSel},
		{label: "Agent Model", kind: overrideFieldSelect, options: config.KnownAgentModels, selected: agentModelSel},
		{label: "Bypass Permissions", kind: overrideFieldToggle, toggleValue: bypassVal},
	}
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

// Update routes a tea.Msg. Intercepts esc / enter / ctrl+enter for control;
// all other messages (including ctrl+j via InsertNewline) go to the textarea.
func (m newSessionModel) Update(msg tea.Msg) (newSessionModel, tea.Cmd) {
	if key, ok := msg.(tea.KeyPressMsg); ok {
		switch key.String() {
		case "esc":
			m.Close()
			return m, func() tea.Msg { return promptModalCancelMsg{} }
		case "ctrl+enter":
			val := strings.TrimSpace(m.textarea.Value())
			if val == "" {
				return m, nil
			}
			m.Close()
			return m, func() tea.Msg {
				return promptModalSubmitMsg{prompt: val, skipPlanning: true}
			}
		case "enter":
			val := strings.TrimSpace(m.textarea.Value())
			if val == "" {
				return m, nil
			}
			m.Close()
			return m, func() tea.Msg {
				return promptModalSubmitMsg{prompt: val, skipPlanning: false}
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

	// Rotating title prompt.
	title := StyleTitle.Render(promptModalTitles[m.titleIdx])

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
	flowLabel := StyleSubtle.Render("FLOW")
	flow := StyleAccent.Render("Plan → Build → Review → Ship")
	flowBlock := lipgloss.JoinVertical(lipgloss.Left, flowLabel, flow)

	overLabel := StyleSubtle.Render("OVERRIDES")
	labelW := 20 // fixed label width keeps value column aligned
	labelStyle := lipgloss.NewStyle().Width(labelW).Foreground(ColorText)
	focusedLabelStyle := StyleActive.Bold(true).Width(labelW)
	toggleOn := StyleSuccess.Render("[x]")
	toggleOff := StyleSubtle.Render("[ ]")

	rows := make([]string, 0, len(m.overrideFields))
	for i, f := range m.overrideFields {
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
		rows = append(rows, cursor+label+" "+value)
	}
	overBlock := lipgloss.JoinVertical(lipgloss.Left, append([]string{overLabel}, rows...)...)

	return lipgloss.JoinVertical(lipgloss.Left, flowBlock, "", overBlock)
}
