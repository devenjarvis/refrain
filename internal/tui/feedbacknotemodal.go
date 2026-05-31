package tui

import (
	"strings"

	"charm.land/bubbles/v2/textarea"
	tea "charm.land/bubbletea/v2"
	xlipgloss "charm.land/lipgloss/v2"
	"github.com/charmbracelet/lipgloss"

	"github.com/devenjarvis/refrain/internal/tui/theme"
)

// feedbackNoteModal is a centered textarea overlay for attaching a guidance
// note to a feedback item (approve/disagree verdict). Border + chip footer,
// enter to save, esc to cancel.
type feedbackNoteModal struct {
	active  bool
	itemKey string
	ta      textarea.Model
	width   int
	height  int
}

func newFeedbackNoteModal() feedbackNoteModal {
	ta := textarea.New()
	ta.Prompt = ""
	ta.ShowLineNumbers = false
	ta.CharLimit = 2000
	ta.SetHeight(4)
	styles := ta.Styles()
	styles.Focused.CursorLine = xlipgloss.NewStyle()
	styles.Cursor.Color = ColorPrimary
	ta.SetStyles(styles)
	ta.KeyMap.InsertNewline.SetKeys("enter", "ctrl+m", "shift+enter")
	return feedbackNoteModal{ta: ta}
}

// SetSize updates the modal's understanding of the surrounding viewport.
func (m *feedbackNoteModal) SetSize(w, h int) {
	m.width = w
	m.height = h
	m.ta.SetWidth(modalContentWidth(modalWidth(w)))
}

// Open shows the modal, pre-populating the textarea with an existing note.
func (m *feedbackNoteModal) Open(itemKey, existing string) tea.Cmd {
	m.active = true
	m.itemKey = itemKey
	m.ta.SetValue(existing)
	m.ta.SetWidth(modalContentWidth(modalWidth(m.width)))
	return m.ta.Focus()
}

// Close hides the modal and blurs the textarea.
func (m *feedbackNoteModal) Close() {
	m.active = false
	m.ta.Blur()
}

// Active reports whether the modal is currently visible.
func (m *feedbackNoteModal) Active() bool { return m.active }

// Update routes a tea.Msg to the modal and returns the next modal state plus a
// command. On enter it closes synchronously and returns a command yielding a
// feedbackNoteSubmitMsg so the owning panel persists the note one Update cycle
// later (CONVENTIONS.md §3/§4); esc closes with no save.
func (m feedbackNoteModal) Update(msg tea.Msg) (feedbackNoteModal, tea.Cmd) {
	if !m.active {
		return m, nil
	}
	if key, ok := msg.(tea.KeyPressMsg); ok {
		switch key.String() {
		case "esc":
			m.Close()
			return m, nil
		case "enter":
			val := strings.TrimSpace(m.ta.Value())
			itemKey := m.itemKey
			m.Close()
			return m, func() tea.Msg {
				return feedbackNoteSubmitMsg{itemKey: itemKey, note: val}
			}
		}
	}
	var cmd tea.Cmd
	m.ta, cmd = m.ta.Update(msg)
	return m, cmd
}

// View renders the modal box. Caller should overlay this when Active().
func (m feedbackNoteModal) View() string {
	w := modalWidth(m.width)
	innerW := modalContentWidth(w)

	// Header: "FEEDBACK NOTE" left, truncated itemKey right.
	left := StyleSubtle.Render("FEEDBACK NOTE")
	right := StyleSubtle.Render(truncateVisible(m.itemKey, innerW-lipgloss.Width(left)-2))
	gap := innerW - lipgloss.Width(left) - lipgloss.Width(right)
	if gap < 1 {
		gap = 1
	}
	header := left + strings.Repeat(" ", gap) + right

	// Footer chips.
	enter := StyleTitle.Render("enter")
	shiftEnter := StyleTitle.Render("shift+enter")
	esc := StyleTitle.Render("esc")
	footer := enter + StyleSubtle.Render(" — save") +
		"   " + shiftEnter + StyleSubtle.Render(" — newline") +
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

// modalWidth computes a clamped overlay width from the surrounding viewport.
// The modal occupies 2/3 of the viewport, clamped between 40 and 80 columns,
// with an extra clamp so the border always fits (viewport - 2).
func modalWidth(viewportW int) int {
	const (
		modalMaxWidth = 80
		modalMinWidth = 40
	)
	w := viewportW * 2 / 3
	if w > modalMaxWidth {
		w = modalMaxWidth
	}
	if w < modalMinWidth {
		w = modalMinWidth
	}
	if viewportW > 0 && w > viewportW-2 {
		w = viewportW - 2
	}
	return w
}
