package tui

import (
	"math/rand/v2"
	"strings"

	"charm.land/bubbles/v2/textarea"
	tea "charm.land/bubbletea/v2"
	xlipgloss "charm.land/lipgloss/v2"
	"github.com/charmbracelet/lipgloss"
)

// promptModalSubmitMsg fires when the user accepts the prompt. SkipPlanning
// is true for the ctrl+enter "do today's flow" path, false for the default
// enter path that runs through the plan-first drafting/editor flow.
type promptModalSubmitMsg struct {
	prompt       string
	skipPlanning bool
}

// promptModalCancelMsg fires on `esc`.
type promptModalCancelMsg struct{}

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
	return newSessionModel{textarea: ta}
}

// SetSize updates the model's understanding of the terminal dimensions.
func (m *newSessionModel) SetSize(w, h int) {
	m.width = w
	m.height = h
	m.textarea.SetWidth(m.textareaWidth())
	m.textarea.SetHeight(m.textareaHeight())
}

// Open activates the screen, resets textarea content, and picks a fresh
// title/placeholder pair. returnTo is the ViewMode to restore on cancel.
func (m *newSessionModel) Open(returnTo ViewMode) tea.Cmd {
	m.active = true
	m.returnTo = returnTo
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
		return lipgloss.JoinVertical(lipgloss.Left, header, "", title, "", body)
	}

	return lipgloss.JoinVertical(lipgloss.Left, header, "", title, "", m.textarea.View())
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
	w := newSessionSidebarWidth

	flowLabel := StyleSubtle.Render("FLOW")
	flow := StyleAccent.Render("Plan → Build → Review → Ship")
	flowBlock := lipgloss.JoinVertical(lipgloss.Left, flowLabel, flow)

	exLabel := StyleSubtle.Render("EXAMPLES")
	// Pick three example prompts starting at placeholderIdx.
	examples := make([]string, 0, 3)
	for i := 0; i < 3; i++ {
		idx := (m.placeholderIdx + i) % len(promptModalPlaceholders)
		ex := promptModalPlaceholders[idx]
		// Strip leading "e.g. " for brevity in sidebar.
		ex = strings.TrimPrefix(ex, "e.g. ")
		ex = lipgloss.NewStyle().Width(w).Render(StyleSubtle.Render("• " + ex))
		examples = append(examples, ex)
	}
	exLines := append([]string{exLabel}, examples...)
	exBlock := lipgloss.JoinVertical(lipgloss.Left, exLines...)

	return lipgloss.JoinVertical(lipgloss.Left, flowBlock, "", exBlock)
}
