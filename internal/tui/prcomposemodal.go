package tui

import (
	"fmt"
	"strings"

	"charm.land/bubbles/v2/textarea"
	tea "charm.land/bubbletea/v2"
	xlipgloss "charm.land/lipgloss/v2"
	"github.com/charmbracelet/lipgloss"
)

// prComposeModal is a centered overlay for reviewing and editing an AI-drafted
// PR title and body before creation. The user can tab between fields, toggle
// draft mode, confirm, or cancel.
type prComposeModal struct {
	active    bool
	titleArea textarea.Model
	bodyArea  textarea.Model
	focused   int // 0 = title field, 1 = body field
	draft     bool
	width     int
	height    int
}

// prComposeSubmitMsg fires when the user confirms the PR draft.
type prComposeSubmitMsg struct {
	title string
	body  string
	draft bool
}

// prComposeCancelMsg fires when the user presses esc.
type prComposeCancelMsg struct{}

const (
	prModalMaxWidth  = 90
	prModalMinWidth  = 50
	prModalTitleRows = 1
	prModalBodyRows  = 10
	prModalCharLimit = 65536
)

func newPRComposeModal() prComposeModal {
	ta := newPRTextArea(prModalTitleRows)
	ba := newPRTextArea(prModalBodyRows)
	return prComposeModal{titleArea: ta, bodyArea: ba, draft: true}
}

func newPRTextArea(rows int) textarea.Model {
	ta := textarea.New()
	ta.Prompt = ""
	ta.ShowLineNumbers = false
	ta.CharLimit = prModalCharLimit
	ta.SetHeight(rows)
	styles := ta.Styles()
	styles.Focused.CursorLine = xlipgloss.NewStyle()
	styles.Cursor.Color = ColorPrimary
	ta.SetStyles(styles)
	return ta
}

// Open shows the modal pre-filled with the AI-drafted title and body.
// The title field receives focus. Returns the Cmd to focus the textarea.
func (m *prComposeModal) Open(title, body string, draft bool) tea.Cmd {
	m.active = true
	m.draft = draft
	m.focused = 0
	m.titleArea.SetValue(title)
	m.bodyArea.SetValue(body)
	m.titleArea.SetWidth(prModalWidth(m.width) - 4)
	m.bodyArea.SetWidth(prModalWidth(m.width) - 4)
	m.bodyArea.Blur()
	return m.titleArea.Focus()
}

// Close hides the modal and blurs both fields.
func (m *prComposeModal) Close() {
	m.active = false
	m.titleArea.Blur()
	m.bodyArea.Blur()
}

// Active reports whether the modal is currently visible.
func (m *prComposeModal) Active() bool { return m.active }

// SetSize updates the modal's viewport dimensions.
func (m *prComposeModal) SetSize(w, h int) {
	m.width = w
	m.height = h
	inner := prModalWidth(w) - 4
	m.titleArea.SetWidth(inner)
	m.bodyArea.SetWidth(inner)
}

// Update routes a tea.Msg to the active modal. Returns the Cmd to run.
func (m *prComposeModal) Update(msg tea.Msg) tea.Cmd {
	if !m.active {
		return nil
	}
	if key, ok := msg.(tea.KeyPressMsg); ok {
		switch key.String() {
		case "esc":
			m.Close()
			return func() tea.Msg { return prComposeCancelMsg{} }
		case "enter":
			title := strings.TrimSpace(m.titleArea.Value())
			if title == "" {
				return nil
			}
			body := strings.TrimSpace(m.bodyArea.Value())
			draft := m.draft // snapshot before Close() resets state
			m.Close()
			return func() tea.Msg {
				return prComposeSubmitMsg{title: title, body: body, draft: draft}
			}
		case "tab", "shift+tab":
			if m.focused == 0 {
				m.focused = 1
				m.titleArea.Blur()
				return m.bodyArea.Focus()
			}
			m.focused = 0
			m.bodyArea.Blur()
			return m.titleArea.Focus()
		case "d":
			m.draft = !m.draft
			return nil
		}
	}
	var cmd tea.Cmd
	if m.focused == 0 {
		m.titleArea, cmd = m.titleArea.Update(msg)
	} else {
		m.bodyArea, cmd = m.bodyArea.Update(msg)
	}
	return cmd
}

// View renders the modal. Callers should invoke only when Active() is true.
func (m *prComposeModal) View() string {
	w := prModalWidth(m.width)
	innerW := w - 4

	titleLabel := StyleSubtle.Render("Title")
	bodyLabel := StyleSubtle.Render("Body")
	if m.focused == 0 {
		titleLabel = StyleTitle.Render("Title")
	} else {
		bodyLabel = StyleTitle.Render("Body")
	}

	var draftLabel string
	if m.draft {
		draftLabel = lipgloss.NewStyle().Foreground(lipgloss.Color("#F59E0B")).Bold(true).Render("● draft")
	} else {
		draftLabel = lipgloss.NewStyle().Foreground(lipgloss.Color("#10B981")).Bold(true).Render("● ready")
	}

	header := prComposeHeader(innerW, draftLabel)
	footer := prComposeFooter()

	body := lipgloss.JoinVertical(
		lipgloss.Left,
		header,
		"",
		titleLabel,
		m.titleArea.View(),
		"",
		bodyLabel,
		m.bodyArea.View(),
		"",
		footer,
	)
	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(ColorPrimary).
		Padding(0, 1).
		Width(w).
		Render(body)
}

func prComposeHeader(innerW int, draftLabel string) string {
	left := StyleSubtle.Render("CREATE PR")
	right := draftLabel
	gap := innerW - lipgloss.Width(left) - lipgloss.Width(right)
	if gap < 1 {
		gap = 1
	}
	return lipgloss.JoinHorizontal(lipgloss.Left, left, strings.Repeat(" ", gap), right)
}

func prComposeFooter() string {
	chip := func(key string) string { return StyleTitle.Render(key) }
	desc := func(s string) string { return StyleSubtle.Render(s) }
	line1 := fmt.Sprintf(
		"%s %s   %s %s   %s %s   %s %s",
		chip("↵"), desc("create"),
		chip("⇥"), desc("switch field"),
		chip("d"), desc("toggle draft"),
		chip("esc"), desc("cancel"),
	)
	return line1
}

func prModalWidth(viewportW int) int {
	w := viewportW * 2 / 3
	if w > prModalMaxWidth {
		w = prModalMaxWidth
	}
	if w < prModalMinWidth {
		w = prModalMinWidth
	}
	if viewportW > 0 && w > viewportW-2 {
		w = viewportW - 2
	}
	return w
}
