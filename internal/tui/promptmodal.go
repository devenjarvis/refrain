package tui

import (
	"fmt"
	"math/rand/v2"
	"strings"

	"charm.land/bubbles/v2/textarea"
	tea "charm.land/bubbletea/v2"
	// Two lipgloss versions intentionally: the v1 import drives Refrain's
	// layout helpers (StyleTitle, JoinVertical, etc.), while xlipgloss is
	// the v2 type the bubbles/v2 textarea exposes in its Styles struct —
	// we only need it to construct the empty CursorLine override below.
	xlipgloss "charm.land/lipgloss/v2"
	"github.com/charmbracelet/lipgloss"
)

// promptModalModel is the centered prompt modal that opens when
// PlanFirstEnabled is on and the user presses `n`. It owns its own
// multi-line textarea and emits one of the messages below when the user
// submits or cancels.
type promptModalModel struct {
	active         bool
	textarea       textarea.Model
	width          int // viewport width — used to clamp modal width
	height         int // viewport height
	titleIdx       int // index into promptModalTitles, picked at Open()
	placeholderIdx int // index into promptModalPlaceholders, picked at Open()
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

// promptModalTitles rotate through the modal header. Short and imperative,
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
// a first-time user has a model of what a planner-friendly prompt looks
// like.
var promptModalPlaceholders = []string{
	"e.g. Add a dark-mode toggle to the settings page",
	"e.g. Fix the flaky test in foo_test.go",
	"e.g. Migrate auth from sessions to JWT",
	"e.g. Wire up file upload to S3",
	"e.g. Add unit tests for the shutdown sequence",
	"e.g. Refactor the dashboard render path",
}

// pickPrompt is the indirection tests use to make rotation deterministic.
// Production wires through to math/rand/v2's IntN.
var pickPrompt = func(n int) int { return rand.IntN(n) }

func newPromptModal() promptModalModel {
	ta := textarea.New()
	ta.Prompt = ""
	ta.ShowLineNumbers = false
	ta.CharLimit = promptModalCharLimit
	ta.SetHeight(promptModalRows)
	// Strip bubbles' default focused CursorLine background — on dark
	// terminals it paints a near-black rectangle over Refrain's #111827 bg.
	// The cursor itself signals position; the active line should read
	// identically to every other line.
	styles := ta.Styles()
	styles.Focused.CursorLine = xlipgloss.NewStyle()
	// ColorPrimary is a v1 lipgloss.Color; it satisfies the v2
	// CursorStyle.Color field (image/color.Color) via its RGBA() method.
	// If a future bubbles upgrade tightens the interface, this assignment
	// will fail to compile — fix at that point with a v2-typed constant.
	styles.Cursor.Color = ColorPrimary
	ta.SetStyles(styles)
	// Bubbles' default InsertNewline binding only lists "enter"/"ctrl+m",
	// and the modal's Update intercepts plain "enter" as submit — so
	// without extending the binding, shift+enter falls through to the
	// textarea's text-input path and the literal modifier name gets
	// typed. The footer advertises shift+enter for newline; honor that.
	ta.KeyMap.InsertNewline.SetKeys("enter", "ctrl+m", "shift+enter")
	return promptModalModel{textarea: ta}
}

// SetSize updates the modal's understanding of the surrounding viewport so
// the textarea width matches the modal's clamped width.
func (m *promptModalModel) SetSize(w, h int) {
	m.width = w
	m.height = h
	m.textarea.SetWidth(modalWidth(w) - 4) // leave room for border + padding
}

// Open shows the modal, focuses the textarea, clears any prior content,
// and picks a fresh title/placeholder pairing for this session. The pair
// stays stable across re-renders within one open session.
func (m *promptModalModel) Open() tea.Cmd {
	m.active = true
	m.textarea.SetValue("")
	m.titleIdx = pickPrompt(len(promptModalTitles))
	m.placeholderIdx = pickPrompt(len(promptModalPlaceholders))
	m.textarea.Placeholder = promptModalPlaceholders[m.placeholderIdx]
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
// messages are forwarded to the textarea (e.g. cursor blink ticks, paste).
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
			// Plain enter submits via the planning path. Multi-line
			// prompts use shift+enter, which is wired into the textarea's
			// InsertNewline binding in newPromptModal.
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
// The caller is responsible for only invoking View while the modal is
// active.
func (m *promptModalModel) View() string {
	w := modalWidth(m.width)
	innerW := w - 4 // matches textarea width: border (2) + padding (2)
	body := lipgloss.JoinVertical(
		lipgloss.Left,
		promptModalHeader(innerW),
		"",
		StyleTitle.Render(promptModalTitles[m.titleIdx]),
		"",
		m.textarea.View(),
		"",
		promptModalFooter(),
	)
	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(ColorPrimary).
		Padding(0, 1).
		Width(w).
		Render(body)
}

// promptModalHeader returns "NEW SESSION" justified left and "Planning →"
// justified right across innerW columns.
func promptModalHeader(innerW int) string {
	left := StyleSubtle.Render("NEW SESSION")
	right := StyleSubtle.Render("Planning →")
	gap := innerW - lipgloss.Width(left) - lipgloss.Width(right)
	if gap < 1 {
		gap = 1
	}
	return lipgloss.JoinHorizontal(lipgloss.Left, left, strings.Repeat(" ", gap), right)
}

// promptModalFooter renders the two-line key chip footer with column
// widths derived from the longest glyphs ("ctrl+enter", "shift+enter",
// "run now (skip plan)") so columns line up regardless of which keys
// they describe.
func promptModalFooter() string {
	const (
		col1Key  = 10 // len("ctrl+enter")
		col1Desc = 19 // len("run now (skip plan)")
		col2Key  = 11 // len("shift+enter")
	)
	keyChip := func(s string, w int) string {
		return StyleTitle.Render(fmt.Sprintf("%-*s", w, s))
	}
	descChip := func(s string, w int) string {
		return StyleSubtle.Render(fmt.Sprintf("%-*s", w, s))
	}
	line1 := keyChip("enter", col1Key) + " " + descChip("draft a plan", col1Desc) +
		"  " + keyChip("shift+enter", col2Key) + " " + StyleSubtle.Render("newline")
	line2 := keyChip("ctrl+enter", col1Key) + " " + descChip("run now (skip plan)", col1Desc) +
		"  " + keyChip("esc", col2Key) + " " + StyleSubtle.Render("cancel")
	return lipgloss.JoinVertical(lipgloss.Left, line1, line2)
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
