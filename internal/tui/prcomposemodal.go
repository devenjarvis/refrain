package tui

import (
	"strings"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

type prComposeMode int

const (
	prComposeModeScroll prComposeMode = iota
	prComposeModeEdit
)

// prComposeModal is a full-page view for reviewing and editing an AI-drafted
// PR title and body before creation. Opens in scroll mode; press i to edit.
type prComposeModal struct {
	active     bool
	titleInput textinput.Model
	doc        docEditor
	mode       prComposeMode
	draft      bool
	sessName   string
}

// prComposeSubmitMsg fires when the user confirms the PR draft.
type prComposeSubmitMsg struct {
	title string
	body  string
	draft bool
}

// prComposeCancelMsg fires when the user presses esc.
type prComposeCancelMsg struct{}

func newPRComposeModal() prComposeModal {
	ti := textinput.New()
	ti.Placeholder = "Pull request title"
	ti.CharLimit = 255

	return prComposeModal{
		titleInput: ti,
		doc:        newDocEditor(0, 0),
		draft:      true,
		mode:       prComposeModeScroll,
	}
}

// Open shows the full-page PR compose view pre-filled with the AI-drafted
// title and body. Opens in scroll mode so the user can review before editing.
func (m *prComposeModal) Open(title, body string, draft bool, sessName string) tea.Cmd {
	m.active = true
	m.draft = draft
	m.mode = prComposeModeScroll
	m.doc.scrollOff = 0
	m.sessName = sessName
	m.titleInput.SetValue(title)
	m.doc.SetValue(body)
	m.titleInput.Blur()
	m.doc.Blur()
	return nil
}

// Close hides the view and blurs both fields.
func (m *prComposeModal) Close() {
	m.active = false
	m.titleInput.Blur()
	m.doc.Blur()
	m.mode = prComposeModeScroll
}

// Active reports whether the view is currently visible.
func (m *prComposeModal) Active() bool { return m.active }

// SetSize updates the viewport dimensions.
func (m *prComposeModal) SetSize(w, h int) {
	m.doc.SetSize(w, h)
	m.titleInput.SetWidth(w - 4)
	m.doc.textarea.SetHeight(m.doc.BodyHeight(8))
	rendered := m.doc.RenderLines()
	m.doc.ClampScroll(len(rendered))
}

// Update routes a tea.Msg to updateScroll or updateEdit based on mode.
// PasteMsg in edit mode is forwarded to the focused field before mode dispatch.
func (m *prComposeModal) Update(msg tea.Msg) tea.Cmd {
	if !m.active {
		return nil
	}
	if paste, ok := msg.(tea.PasteMsg); ok {
		if m.mode == prComposeModeEdit {
			if m.titleInput.Focused() {
				var cmd tea.Cmd
				m.titleInput, cmd = m.titleInput.Update(paste)
				return cmd
			}
			return m.doc.UpdateTextarea(paste)
		}
		return nil
	}
	switch m.mode {
	case prComposeModeEdit:
		return m.updateEdit(msg)
	default:
		return m.updateScroll(msg)
	}
}

func (m *prComposeModal) updateScroll(msg tea.Msg) tea.Cmd {
	key, ok := msg.(tea.KeyPressMsg)
	if !ok {
		return nil
	}
	switch key.String() {
	case "esc":
		m.Close()
		return func() tea.Msg { return prComposeCancelMsg{} }
	case "ctrl+enter":
		title := strings.TrimSpace(m.titleInput.Value())
		if title == "" {
			return nil
		}
		body := strings.TrimSpace(m.doc.Value())
		draft := m.draft
		m.Close()
		return func() tea.Msg {
			return prComposeSubmitMsg{title: title, body: body, draft: draft}
		}
	case "ctrl+d":
		m.draft = !m.draft
	case "i":
		m.mode = prComposeModeEdit
		return m.titleInput.Focus()
	case "j":
		m.doc.scrollOff++
		rendered := m.doc.RenderLines()
		m.doc.ClampScroll(len(rendered))
	case "k":
		if m.doc.scrollOff > 0 {
			m.doc.scrollOff--
		}
	case "pgdown":
		m.doc.scrollOff += m.doc.BodyHeight(7) / 2
		rendered := m.doc.RenderLines()
		m.doc.ClampScroll(len(rendered))
	case "pgup":
		m.doc.scrollOff -= m.doc.BodyHeight(7) / 2
		if m.doc.scrollOff < 0 {
			m.doc.scrollOff = 0
		}
	case "g":
		m.doc.scrollOff = 0
	case "G":
		m.doc.scrollOff = 9999
		rendered := m.doc.RenderLines()
		m.doc.ClampScroll(len(rendered))
	}
	return nil
}

func (m *prComposeModal) updateEdit(msg tea.Msg) tea.Cmd {
	key, ok := msg.(tea.KeyPressMsg)
	if !ok {
		if m.titleInput.Focused() {
			var cmd tea.Cmd
			m.titleInput, cmd = m.titleInput.Update(msg)
			return cmd
		}
		return m.doc.UpdateTextarea(msg)
	}
	switch key.String() {
	case "esc":
		m.titleInput.Blur()
		m.doc.Blur()
		m.mode = prComposeModeScroll
		return nil
	case "ctrl+enter":
		title := strings.TrimSpace(m.titleInput.Value())
		if title == "" {
			return nil
		}
		body := strings.TrimSpace(m.doc.Value())
		draft := m.draft
		m.Close()
		return func() tea.Msg {
			return prComposeSubmitMsg{title: title, body: body, draft: draft}
		}
	case "ctrl+d":
		m.draft = !m.draft
		return nil
	case "tab", "shift+tab":
		if m.titleInput.Focused() {
			m.titleInput.Blur()
			return m.doc.Focus()
		}
		m.doc.Blur()
		return m.titleInput.Focus()
	}
	if m.titleInput.Focused() {
		var cmd tea.Cmd
		m.titleInput, cmd = m.titleInput.Update(msg)
		return cmd
	}
	return m.doc.UpdateTextarea(msg)
}

// View renders the full-page PR compose. Only call when Active() is true.
func (m *prComposeModal) View() string {
	var lines []string
	lines = append(lines, m.renderHeader())
	lines = append(lines, StyleSubtle.Render(strings.Repeat("─", max(1, m.doc.width-2))))
	switch m.mode {
	case prComposeModeEdit:
		lines = append(lines, m.renderEditBody())
	default:
		lines = append(lines, m.renderScrollBody())
	}
	lines = append(lines, m.renderFooter())
	return strings.Join(lines, "\n")
}

func (m *prComposeModal) renderEditBody() string {
	pad := strings.Repeat(" ", m.doc.DisplayLeftPad())

	titleLabel := StyleSubtle.Render("Title")
	bodyLabel := StyleSubtle.Render("Body")
	if m.titleInput.Focused() {
		titleLabel = StyleTitle.Render("Title")
	} else {
		bodyLabel = StyleTitle.Render("Body")
	}

	return strings.Join([]string{
		pad + titleLabel,
		m.titleInput.View(),
		"",
		pad + bodyLabel,
		m.doc.textarea.View(),
	}, "\n")
}

func (m *prComposeModal) renderScrollBody() string {
	pad := strings.Repeat(" ", m.doc.DisplayLeftPad())

	titleVal := m.titleInput.Value()
	if titleVal == "" {
		titleVal = StyleSubtle.Render("(no title)")
	}
	titleLine := pad + StyleTitle.Render("Title: ") + titleVal

	var bodyContent string
	if m.doc.Value() == "" {
		bodyContent = pad + StyleSubtle.Render("(no body)")
	} else {
		rendered := m.doc.RenderLines()
		bh := m.doc.BodyHeight(7)
		windowed := strings.Split(m.doc.ScrollWindow(rendered, bh), "\n")
		for i, l := range windowed {
			windowed[i] = pad + l
		}
		bodyContent = strings.Join(windowed, "\n")
	}

	return strings.Join([]string{
		titleLine,
		"",
		pad + StyleSubtle.Render("Body"),
		bodyContent,
	}, "\n")
}

func (m *prComposeModal) renderFooter() string {
	divider := StyleSubtle.Render(strings.Repeat("─", max(1, m.doc.width-2)))
	var hints string
	switch m.mode {
	case prComposeModeEdit:
		hints = StyleActive.Render("tab") + StyleSubtle.Render(" switch  ") +
			StyleActive.Render("ctrl+↵") + StyleSubtle.Render(" create  ") +
			StyleActive.Render("ctrl+d") + StyleSubtle.Render(" toggle draft  ") +
			StyleActive.Render("esc") + StyleSubtle.Render(" back")
	default:
		hints = StyleActive.Render("j/k") + StyleSubtle.Render(" scroll  ") +
			StyleActive.Render("i") + StyleSubtle.Render(" edit  ") +
			StyleActive.Render("ctrl+↵") + StyleSubtle.Render(" create  ") +
			StyleActive.Render("ctrl+d") + StyleSubtle.Render(" toggle draft  ") +
			StyleActive.Render("esc") + StyleSubtle.Render(" cancel")
	}
	return divider + "\n" + hints
}

func (m *prComposeModal) renderHeader() string {
	title := StyleHeading.Render("PR DRAFT")
	left := title
	if m.sessName != "" {
		left = title + "  " + StyleSubtle.Render("›") + "  " + m.sessName
	}
	var draftLabel string
	if m.draft {
		draftLabel = StyleWarning.Bold(true).Render("● draft")
	} else {
		draftLabel = StyleSuccess.Bold(true).Render("● ready")
	}
	gap := m.doc.width - ansi.StringWidth(left) - ansi.StringWidth(draftLabel) - 4
	if gap < 1 {
		gap = 1
	}
	return left + strings.Repeat(" ", gap) + draftLabel
}
