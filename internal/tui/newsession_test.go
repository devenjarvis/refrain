package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

func TestNewSession_OpenSetsActiveAndFocusesTextarea(t *testing.T) {
	prev := pickPrompt
	defer func() { pickPrompt = prev }()
	picks := []int{3, 2}
	calls := 0
	pickPrompt = func(n int) int {
		i := picks[calls%len(picks)] % n
		calls++
		return i
	}

	m := newNewSessionModel()
	m.SetSize(120, 40)
	cmd := m.Open(ViewDashboard)

	if !m.active {
		t.Fatal("Open should set active=true")
	}
	if m.returnTo != ViewDashboard {
		t.Errorf("returnTo = %v, want ViewDashboard", m.returnTo)
	}
	if m.textarea.Value() != "" {
		t.Errorf("textarea not cleared on Open, got %q", m.textarea.Value())
	}
	if m.titleIdx < 0 || m.titleIdx >= len(promptModalTitles) {
		t.Errorf("titleIdx = %d out of range [0,%d)", m.titleIdx, len(promptModalTitles))
	}
	if m.placeholderIdx < 0 || m.placeholderIdx >= len(promptModalPlaceholders) {
		t.Errorf("placeholderIdx = %d out of range [0,%d)", m.placeholderIdx, len(promptModalPlaceholders))
	}
	if cmd == nil {
		t.Fatal("Open should return a non-nil cmd (textarea focus)")
	}
}

func TestNewSession_View_WideTerminal_RendersSidebar(t *testing.T) {
	m := newNewSessionModel()
	m.SetSize(140, 40)
	m.repoName = "myrepo"
	m.baseBranch = "main"
	m.Open(ViewDashboard)

	view := m.View()

	if !strings.Contains(view, "NEW SESSION") {
		t.Error("wide view should contain 'NEW SESSION'")
	}
	if !strings.Contains(view, "myrepo") {
		t.Error("wide view should contain repo name")
	}
	if !strings.Contains(view, "main") {
		t.Error("wide view should contain base branch")
	}
	if !strings.Contains(view, "EXAMPLES") {
		t.Error("wide view (>=110) should contain sidebar with EXAMPLES")
	}
	if !strings.Contains(view, "Plan") || !strings.Contains(view, "Build") ||
		!strings.Contains(view, "Review") || !strings.Contains(view, "Ship") {
		t.Error("wide view should contain FLOW block with Plan/Build/Review/Ship")
	}
}

func TestNewSession_View_NarrowTerminal_OmitsSidebar(t *testing.T) {
	m := newNewSessionModel()
	m.SetSize(90, 24)
	m.repoName = "myrepo"
	m.baseBranch = "main"
	m.Open(ViewDashboard)

	view := m.View()

	if strings.Contains(view, "EXAMPLES") {
		t.Error("narrow view (<110) should NOT contain EXAMPLES sidebar")
	}
}

func TestNewSession_EnterSubmitsPlanning(t *testing.T) {
	m := newNewSessionModel()
	m.SetSize(120, 40)
	m.Open(ViewDashboard)
	m.textarea.SetValue("add dark mode toggle")

	m, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter, Text: "enter"})
	if cmd == nil {
		t.Fatal("expected cmd from enter")
	}
	got := cmd()
	submit, ok := got.(promptModalSubmitMsg)
	if !ok {
		t.Fatalf("got %T, want promptModalSubmitMsg", got)
	}
	if submit.skipPlanning {
		t.Error("plain enter should NOT set skipPlanning")
	}
	if !strings.Contains(submit.prompt, "dark mode") {
		t.Errorf("prompt = %q", submit.prompt)
	}
	if m.active {
		t.Error("model should deactivate on submit")
	}
}

func TestNewSession_CtrlEnterSubmitsSkip(t *testing.T) {
	m := newNewSessionModel()
	m.SetSize(120, 40)
	m.Open(ViewDashboard)
	m.textarea.SetValue("trivial fix")

	m, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter, Mod: tea.ModCtrl, Text: "ctrl+enter"})
	if cmd == nil {
		t.Fatal("expected cmd from ctrl+enter")
	}
	got := cmd()
	submit, ok := got.(promptModalSubmitMsg)
	if !ok {
		t.Fatalf("got %T, want promptModalSubmitMsg", got)
	}
	if !submit.skipPlanning {
		t.Error("ctrl+enter MUST set skipPlanning=true")
	}
	if submit.prompt != "trivial fix" {
		t.Errorf("prompt = %q", submit.prompt)
	}
}

func TestNewSession_EscEmitsCancel(t *testing.T) {
	m := newNewSessionModel()
	m.SetSize(120, 40)
	m.Open(ViewDashboard)
	m.textarea.SetValue("never mind")

	m, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	if cmd == nil {
		t.Fatal("expected cancel cmd")
	}
	got := cmd()
	if _, ok := got.(promptModalCancelMsg); !ok {
		t.Fatalf("got %T, want promptModalCancelMsg", got)
	}
	if m.active {
		t.Error("model should deactivate on esc")
	}
}

func TestNewSession_EmptyEnterIsNoop(t *testing.T) {
	m := newNewSessionModel()
	m.SetSize(120, 40)
	m.Open(ViewDashboard)
	m.textarea.SetValue("   \n\t")

	m, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter, Text: "enter"})
	if cmd != nil {
		t.Fatal("empty/whitespace enter should be a no-op")
	}
	if !m.active {
		t.Error("model should stay active on empty submit")
	}
}

func TestNewSession_CtrlJInsertsNewline(t *testing.T) {
	m := newNewSessionModel()
	m.SetSize(120, 40)
	m.Open(ViewDashboard)
	m.textarea.SetValue("first line")

	m, cmd := m.Update(tea.KeyPressMsg{Code: 'j', Mod: tea.ModCtrl, Text: "ctrl+j"})
	// ctrl+j must not produce a submit/cancel cmd
	if cmd != nil {
		msg := cmd()
		if _, bad := msg.(promptModalSubmitMsg); bad {
			t.Fatal("ctrl+j triggered submit; want newline insertion")
		}
		if _, bad := msg.(promptModalCancelMsg); bad {
			t.Fatal("ctrl+j triggered cancel")
		}
	}
	if !m.active {
		t.Fatal("ctrl+j must not close the model")
	}
	m.textarea.InsertString("second line")
	if got := m.textarea.Value(); !strings.Contains(got, "\n") {
		t.Errorf("textarea value = %q, want a newline", got)
	}
}

func TestNewSession_View_FillsTerminalHeight(t *testing.T) {
	cases := []struct {
		name string
		w, h int
	}{
		{"wide tall", 120, 40},
		{"narrow shorter", 90, 24},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := newNewSessionModel()
			m.SetSize(tc.w, tc.h)
			m.Open(ViewDashboard)
			view := m.View()
			got := strings.Count(view, "\n") + 1
			if got != tc.h {
				t.Errorf("SetSize(%d,%d): View() has %d lines, want %d", tc.w, tc.h, got, tc.h)
			}
		})
	}
}

func TestNewSession_ShiftEnterInsertsNewline(t *testing.T) {
	m := newNewSessionModel()
	m.SetSize(120, 40)
	m.Open(ViewDashboard)
	m.textarea.SetValue("first line")

	m, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter, Mod: tea.ModShift, Text: "shift+enter"})
	if cmd != nil {
		if msg := cmd(); msg != nil {
			if _, ok := msg.(promptModalSubmitMsg); ok {
				t.Fatal("shift+enter triggered submit; want newline insertion")
			}
		}
	}
	if !m.active {
		t.Fatal("shift+enter must not close the model")
	}
}
