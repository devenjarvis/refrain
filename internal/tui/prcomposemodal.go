package tui

import (
	"strings"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/devenjarvis/refrain/internal/tui/mdrender"
	"github.com/devenjarvis/refrain/internal/tui/mdtextarea"
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
	bodyArea   mdtextarea.Model
	renderer   *mdrender.Renderer
	mode       prComposeMode
	focused    int // 0=title, 1=body (meaningful in edit mode)
	draft      bool
	width      int
	height     int
	scrollOff  int
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

	ta := mdtextarea.New()
	ta.Prompt = ""
	ta.ShowLineNumbers = false

	renderer := mdrender.New(planEditorChromaStyle)
	ta.SetMarkdownRenderer(renderer)

	return prComposeModal{
		titleInput: ti,
		bodyArea:   ta,
		renderer:   renderer,
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
	m.scrollOff = 0
	m.sessName = sessName
	m.focused = 0
	m.titleInput.SetValue(title)
	m.bodyArea.SetValue(body)
	m.titleInput.Blur()
	m.bodyArea.Blur()
	return nil
}

// Close hides the view and blurs both fields.
func (m *prComposeModal) Close() {
	m.active = false
	m.titleInput.Blur()
	m.bodyArea.Blur()
	m.mode = prComposeModeScroll
}

// Active reports whether the view is currently visible.
func (m *prComposeModal) Active() bool { return m.active }

// SetSize updates the viewport dimensions.
func (m *prComposeModal) SetSize(w, h int) {
	m.width = w
	m.height = h
	m.titleInput.SetWidth(w - 4)
	m.bodyArea.SetWidth(textareaWidth(w))
	m.bodyArea.SetHeight(m.editBodyHeight())
	m.clampScroll()
}

// Update routes a tea.Msg to updateScroll or updateEdit based on mode.
// PasteMsg in edit mode is forwarded to the focused field before mode dispatch.
func (m *prComposeModal) Update(msg tea.Msg) tea.Cmd {
	if !m.active {
		return nil
	}
	if paste, ok := msg.(tea.PasteMsg); ok {
		if m.mode == prComposeModeEdit {
			if m.focused == 0 {
				var cmd tea.Cmd
				m.titleInput, cmd = m.titleInput.Update(paste)
				return cmd
			}
			var cmd tea.Cmd
			m.bodyArea, cmd = m.bodyArea.Update(paste)
			return cmd
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
		body := strings.TrimSpace(m.bodyArea.Value())
		draft := m.draft
		m.Close()
		return func() tea.Msg {
			return prComposeSubmitMsg{title: title, body: body, draft: draft}
		}
	case "ctrl+d":
		m.draft = !m.draft
	case "i":
		m.mode = prComposeModeEdit
		m.focused = 0
		return m.titleInput.Focus()
	case "j":
		m.scrollOff++
		m.clampScroll()
	case "k":
		if m.scrollOff > 0 {
			m.scrollOff--
		}
	case "pgdown":
		m.scrollOff += m.scrollBodyHeight() / 2
		m.clampScroll()
	case "pgup":
		m.scrollOff -= m.scrollBodyHeight() / 2
		if m.scrollOff < 0 {
			m.scrollOff = 0
		}
	case "g":
		m.scrollOff = 0
	case "G":
		m.scrollOff = 9999
		m.clampScroll()
	}
	return nil
}

func (m *prComposeModal) updateEdit(msg tea.Msg) tea.Cmd {
	key, ok := msg.(tea.KeyPressMsg)
	if !ok {
		if m.focused == 0 {
			var cmd tea.Cmd
			m.titleInput, cmd = m.titleInput.Update(msg)
			return cmd
		}
		var cmd tea.Cmd
		m.bodyArea, cmd = m.bodyArea.Update(msg)
		return cmd
	}
	switch key.String() {
	case "esc":
		m.titleInput.Blur()
		m.bodyArea.Blur()
		m.mode = prComposeModeScroll
		return nil
	case "ctrl+enter":
		title := strings.TrimSpace(m.titleInput.Value())
		if title == "" {
			return nil
		}
		body := strings.TrimSpace(m.bodyArea.Value())
		draft := m.draft
		m.Close()
		return func() tea.Msg {
			return prComposeSubmitMsg{title: title, body: body, draft: draft}
		}
	case "ctrl+d":
		m.draft = !m.draft
		return nil
	case "tab", "shift+tab":
		if m.focused == 0 {
			m.focused = 1
			m.titleInput.Blur()
			return m.bodyArea.Focus()
		}
		m.focused = 0
		m.bodyArea.Blur()
		return m.titleInput.Focus()
	}
	if m.focused == 0 {
		var cmd tea.Cmd
		m.titleInput, cmd = m.titleInput.Update(msg)
		return cmd
	}
	var cmd tea.Cmd
	m.bodyArea, cmd = m.bodyArea.Update(msg)
	return cmd
}

// View renders the full-page PR compose. Only call when Active() is true.
func (m *prComposeModal) View() string {
	var lines []string
	lines = append(lines, m.renderHeader())
	lines = append(lines, StyleSubtle.Render(strings.Repeat("─", max(1, m.width-2))))
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
	pad := strings.Repeat(" ", m.displayLeftPad())

	titleLabel := StyleSubtle.Render("Title")
	bodyLabel := StyleSubtle.Render("Body")
	if m.focused == 0 {
		titleLabel = StyleTitle.Render("Title")
	} else {
		bodyLabel = StyleTitle.Render("Body")
	}

	return strings.Join([]string{
		pad + titleLabel,
		m.titleInput.View(),
		"",
		pad + bodyLabel,
		m.bodyArea.View(),
	}, "\n")
}

func (m *prComposeModal) renderScrollBody() string {
	pad := strings.Repeat(" ", m.displayLeftPad())

	titleVal := m.titleInput.Value()
	if titleVal == "" {
		titleVal = StyleSubtle.Render("(no title)")
	}
	titleLine := pad + StyleTitle.Render("Title: ") + titleVal

	bodyVal := m.bodyArea.Value()
	var bodyContent string
	if bodyVal == "" {
		bodyContent = pad + StyleSubtle.Render("(no body)")
	} else {
		rendered := m.renderer.RenderLines(bodyVal, m.contentWidth())
		bh := m.scrollBodyHeight()
		start := m.scrollOff
		if start > len(rendered) {
			start = len(rendered)
		}
		end := start + bh
		if end > len(rendered) {
			end = len(rendered)
		}
		paddedLines := make([]string, len(rendered[start:end]))
		for i, l := range rendered[start:end] {
			paddedLines[i] = pad + l
		}
		bodyContent = strings.Join(paddedLines, "\n")
	}

	return strings.Join([]string{
		titleLine,
		"",
		pad + StyleSubtle.Render("Body"),
		bodyContent,
	}, "\n")
}

func (m *prComposeModal) renderFooter() string {
	divider := StyleSubtle.Render(strings.Repeat("─", max(1, m.width-2)))
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
	title := lipgloss.NewStyle().Foreground(ColorPrimary).Bold(true).Render("PR DRAFT")
	left := title
	if m.sessName != "" {
		left = title + "  " + StyleSubtle.Render("›") + "  " + m.sessName
	}
	var draftLabel string
	if m.draft {
		draftLabel = lipgloss.NewStyle().Foreground(lipgloss.Color("#F59E0B")).Bold(true).Render("● draft")
	} else {
		draftLabel = lipgloss.NewStyle().Foreground(lipgloss.Color("#10B981")).Bold(true).Render("● ready")
	}
	gap := m.width - ansi.StringWidth(left) - ansi.StringWidth(draftLabel) - 4
	if gap < 1 {
		gap = 1
	}
	return left + strings.Repeat(" ", gap) + draftLabel
}

func (m *prComposeModal) contentWidth() int {
	w, _ := mdrender.ContentMeasure(m.width, planEditorMaxMeasure)
	return w
}

func (m *prComposeModal) displayLeftPad() int {
	_, pad := mdrender.ContentMeasure(m.width, planEditorMaxMeasure)
	return pad
}

func (m *prComposeModal) scrollBodyHeight() int {
	h := m.height - 7
	if h < 1 {
		h = 1
	}
	return h
}

func (m *prComposeModal) editBodyHeight() int {
	h := m.height - 8
	if h < 1 {
		h = 1
	}
	return h
}

func (m *prComposeModal) clampScroll() {
	if m.width == 0 || m.renderer == nil {
		return
	}
	rendered := m.renderer.RenderLines(m.bodyArea.Value(), m.contentWidth())
	maxOff := len(rendered) - m.scrollBodyHeight()
	if maxOff < 0 {
		maxOff = 0
	}
	if m.scrollOff > maxOff {
		m.scrollOff = maxOff
	}
	if m.scrollOff < 0 {
		m.scrollOff = 0
	}
}
