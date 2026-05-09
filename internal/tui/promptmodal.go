package tui

import (
	"strings"

	"charm.land/bubbles/v2/textarea"
	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/lipgloss"
)

// promptModalModel is the centered "What are you working on?" textarea modal
// that opens when PlanFirstEnabled is on and the user presses `n`. It owns
// its own multi-line textarea and emits one of the three messages below
// when the user submits or cancels.
type promptModalModel struct {
	active   bool
	textarea textarea.Model
	width    int // viewport width — used to clamp modal width
	height   int // viewport height
}

// promptModalSubmitMsg fires when the user accepts the prompt. SkipPlanning
// is true for the ctrl+enter "do today's flow" path, false for the default
// enter path that runs through the plan-first drafting/editor flow.
type promptModalSubmitMsg struct {
	prompt       string
	skipPlanning bool
}

// promptModalCancelMsg fires on `esc`.
type promptModalCancelMsg struct{}

const (
	promptModalMaxWidth  = 80
	promptModalMinWidth  = 40
	promptModalRows      = 6
	promptModalCharLimit = 4000
)

func newPromptModal() promptModalModel {
	ta := textarea.New()
	ta.Placeholder = "Describe what you're working on…"
	ta.Prompt = ""
	ta.ShowLineNumbers = false
	ta.CharLimit = promptModalCharLimit
	ta.SetHeight(promptModalRows)
	return promptModalModel{textarea: ta}
}

// SetSize updates the modal's understanding of the surrounding viewport so
// the textarea width matches the modal's clamped width.
func (m *promptModalModel) SetSize(w, h int) {
	m.width = w
	m.height = h
	m.textarea.SetWidth(modalWidth(w) - 4) // leave room for border + padding
}

// Open shows the modal, focuses the textarea, and clears any prior content.
func (m *promptModalModel) Open() tea.Cmd {
	m.active = true
	m.textarea.SetValue("")
	m.textarea.SetWidth(modalWidth(m.width) - 4)
	return m.textarea.Focus()
}

// Close hides the modal and blurs the textarea.
func (m *promptModalModel) Close() {
	m.active = false
	m.textarea.Blur()
}

// Active reports whether the modal is currently visible.
func (m *promptModalModel) Active() bool { return m.active }

// Update routes a tea.Msg to the modal. Returns the cmd to run; non-key
// messages are forwarded to the textarea (e.g. cursor blink ticks).
func (m *promptModalModel) Update(msg tea.Msg) tea.Cmd {
	if !m.active {
		return nil
	}
	if key, ok := msg.(tea.KeyPressMsg); ok {
		switch key.String() {
		case "esc":
			m.Close()
			return func() tea.Msg { return promptModalCancelMsg{} }
		case "ctrl+enter":
			val := strings.TrimSpace(m.textarea.Value())
			if val == "" {
				return nil
			}
			m.Close()
			return func() tea.Msg {
				return promptModalSubmitMsg{prompt: val, skipPlanning: true}
			}
		case "enter":
			// Plain enter submits via the planning path. Note: this means
			// the textarea cannot insert newlines via enter — users wanting
			// multi-line prompts use shift+enter (textarea default).
			val := strings.TrimSpace(m.textarea.Value())
			if val == "" {
				return nil
			}
			m.Close()
			return func() tea.Msg {
				return promptModalSubmitMsg{prompt: val, skipPlanning: false}
			}
		}
	}
	var cmd tea.Cmd
	m.textarea, cmd = m.textarea.Update(msg)
	return cmd
}

// View renders the modal centered over the supplied background body.
// background should be the rendered dashboard content; Place draws the
// modal over a blank canvas of the configured viewport size, so the caller
// must keep using View only when the modal is active.
func (m *promptModalModel) View() string {
	w := modalWidth(m.width)
	title := StyleTitle.Render("What are you working on?")
	hint := StyleSubtle.Render(
		"enter — draft a plan first   ctrl+enter — skip planning, run now   esc — cancel",
	)
	body := lipgloss.JoinVertical(
		lipgloss.Left,
		title,
		"",
		m.textarea.View(),
		"",
		hint,
	)
	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(ColorPrimary).
		Padding(0, 1).
		Width(w).
		Render(body)
}

func modalWidth(viewportW int) int {
	w := viewportW * 2 / 3
	if w > promptModalMaxWidth {
		w = promptModalMaxWidth
	}
	if w < promptModalMinWidth {
		w = promptModalMinWidth
	}
	if viewportW > 0 && w > viewportW-2 {
		w = viewportW - 2
	}
	return w
}
