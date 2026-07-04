package tui

import (
	"strings"

	"charm.land/bubbles/v2/textarea"
	tea "charm.land/bubbletea/v2"
	xlipgloss "charm.land/lipgloss/v2"
	"github.com/charmbracelet/lipgloss"

	"github.com/devenjarvis/refrain/internal/tui/theme"
)

// planGoalSubmitMsg is emitted when the user confirms a plan goal in the
// planGoalModal. App handles it by dispatching Manager.StartDraft for the
// session and opening its plan editor (rollback design §4.5: `P` on a
// plan-less session prompts for a goal, then drafts).
type planGoalSubmitMsg struct {
	sessionID string
	repoPath  string
	goal      string
}

// planGoalModal is a centered textarea overlay that asks for the goal a plan
// should be drafted against. Opened by the `P` action on a session with no
// plan yet. Enter submits, esc cancels; an empty goal is treated as a cancel
// (StartDraft rejects non-actionable prompts anyway).
type planGoalModal struct {
	active    bool
	sessionID string
	repoPath  string
	sessName  string
	ta        textarea.Model
	width     int
	height    int
}

func newPlanGoalModal() planGoalModal {
	ta := textarea.New()
	ta.Prompt = ""
	ta.ShowLineNumbers = false
	ta.CharLimit = 2000
	ta.SetHeight(4)
	ta.Placeholder = "What should this session accomplish?"
	styles := ta.Styles()
	styles.Focused.CursorLine = xlipgloss.NewStyle()
	styles.Cursor.Color = ColorPrimary
	ta.SetStyles(styles)
	ta.KeyMap.InsertNewline.SetKeys("ctrl+j", "shift+enter")
	return planGoalModal{ta: ta}
}

// SetSize updates the modal's understanding of the surrounding viewport.
func (m *planGoalModal) SetSize(w, h int) {
	m.width = w
	m.height = h
	m.ta.SetWidth(modalContentWidth(modalWidth(w)))
}

// Open shows the modal for the given session.
func (m *planGoalModal) Open(sessionID, repoPath, sessName string) tea.Cmd {
	m.active = true
	m.sessionID = sessionID
	m.repoPath = repoPath
	m.sessName = sessName
	m.ta.SetValue("")
	m.ta.SetWidth(modalContentWidth(modalWidth(m.width)))
	return m.ta.Focus()
}

// Close hides the modal and blurs the textarea.
func (m *planGoalModal) Close() {
	m.active = false
	m.ta.Blur()
}

// Active reports whether the modal is currently visible.
func (m *planGoalModal) Active() bool { return m.active }

// Update routes a tea.Msg to the modal. On enter it closes synchronously and
// returns a command yielding a planGoalSubmitMsg so App dispatches the draft
// one Update cycle later (CONVENTIONS.md §3/§4); esc — or enter on an empty
// goal — closes with no draft.
func (m planGoalModal) Update(msg tea.Msg) (planGoalModal, tea.Cmd) {
	if !m.active {
		return m, nil
	}
	if key, ok := msg.(tea.KeyPressMsg); ok {
		switch key.String() {
		case "esc":
			m.Close()
			return m, nil
		case "enter":
			goal := strings.TrimSpace(m.ta.Value())
			sessionID, repoPath := m.sessionID, m.repoPath
			m.Close()
			if goal == "" {
				return m, nil
			}
			return m, func() tea.Msg {
				return planGoalSubmitMsg{sessionID: sessionID, repoPath: repoPath, goal: goal}
			}
		}
	}
	var cmd tea.Cmd
	m.ta, cmd = m.ta.Update(msg)
	return m, cmd
}

// View renders the modal box. Caller should overlay this when Active().
func (m planGoalModal) View() string {
	w := modalWidth(m.width)
	innerW := modalContentWidth(w)

	// Header: "PLAN GOAL" left, session name right.
	left := StyleSubtle.Render("PLAN GOAL")
	right := StyleSubtle.Render(truncateVisible(m.sessName, innerW-lipgloss.Width(left)-2))
	gap := innerW - lipgloss.Width(left) - lipgloss.Width(right)
	if gap < 1 {
		gap = 1
	}
	header := left + strings.Repeat(" ", gap) + right

	enter := StyleTitle.Render("enter")
	ctrlJ := StyleTitle.Render("ctrl+j")
	esc := StyleTitle.Render("esc")
	footer := enter + StyleSubtle.Render(" — draft plan") +
		"   " + ctrlJ + StyleSubtle.Render(" — newline") +
		"   " + esc + StyleSubtle.Render(" — cancel")

	body := lipgloss.JoinVertical(
		lipgloss.Left,
		header,
		"",
		m.ta.View(),
		"",
		footer,
	)
	return theme.BorderModal().
		Padding(0, 1).
		Width(w).
		Render(body)
}
