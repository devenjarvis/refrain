package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/devenjarvis/refrain/internal/config"
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
	if !strings.Contains(view, "OVERRIDES") {
		t.Error("wide view (>=110) should contain sidebar with OVERRIDES")
	}
	if strings.Contains(view, "EXAMPLES") {
		t.Error("wide view should NOT contain EXAMPLES (replaced by OVERRIDES)")
	}
	if !strings.Contains(view, "Plan Model") {
		t.Error("wide view OVERRIDES block should contain 'Plan Model'")
	}
	if !strings.Contains(view, "Agent Model") {
		t.Error("wide view OVERRIDES block should contain 'Agent Model'")
	}
	if !strings.Contains(view, "Bypass Permissions") {
		t.Error("wide view OVERRIDES block should contain 'Bypass Permissions'")
	}
	if !strings.Contains(view, "CONTEXT") {
		t.Error("wide view should contain the CONTEXT block")
	}
	if !strings.Contains(view, "Worktree (new branch)") {
		t.Error("wide view CONTEXT block should default to 'Worktree (new branch)'")
	}
	if strings.Contains(view, "Plan → Build → Review → Ship") {
		t.Error("wide view should NOT contain the retired FLOW pipeline block")
	}
}

func TestNewSession_View_NarrowTerminal_OmitsSidebar(t *testing.T) {
	m := newNewSessionModel()
	m.SetSize(90, 24)
	m.repoName = "myrepo"
	m.baseBranch = "main"
	m.Open(ViewDashboard)

	view := m.View()

	if strings.Contains(view, "OVERRIDES") {
		t.Error("narrow view (<110) should NOT contain OVERRIDES sidebar")
	}
	if strings.Contains(view, "EXAMPLES") {
		t.Error("narrow view (<110) should NOT contain EXAMPLES sidebar")
	}
}

func TestNewSession_View_WideTerminal_SeedsOverridesFromDefaults(t *testing.T) {
	m := newNewSessionModel()
	m.SetSize(140, 40)
	m.SetDefaults(config.ResolvedSettings{
		PlanModel:         "claude-opus-4-8",
		AgentModel:        "claude-sonnet-4-6",
		BypassPermissions: true,
	})
	m.Open(ViewDashboard)

	view := m.View()

	if !strings.Contains(view, "claude-opus-4-8") {
		t.Error("view should contain seeded PlanModel value")
	}
	if !strings.Contains(view, "claude-sonnet-4-6") {
		t.Error("view should contain seeded AgentModel value")
	}
	if !strings.Contains(view, "[x]") {
		t.Error("view should show bypass permissions as enabled [x]")
	}
}

func TestNewSession_EnterSubmitsRaw(t *testing.T) {
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
	if submit.planFirst {
		t.Error("plain enter is the raw path; planFirst must be false")
	}
	if submit.context != contextWorktree {
		t.Errorf("default context = %v, want contextWorktree", submit.context)
	}
	if !strings.Contains(submit.prompt, "dark mode") {
		t.Errorf("prompt = %q", submit.prompt)
	}
	if m.active {
		t.Error("model should deactivate on submit")
	}
}

func TestNewSession_CtrlPSubmitsPlanFirst(t *testing.T) {
	m := newNewSessionModel()
	m.SetSize(120, 40)
	m.Open(ViewDashboard)
	m.textarea.SetValue("trivial fix")

	m, cmd := m.Update(tea.KeyPressMsg{Code: 'p', Mod: tea.ModCtrl})
	if cmd == nil {
		t.Fatal("expected cmd from ctrl+p")
	}
	got := cmd()
	submit, ok := got.(promptModalSubmitMsg)
	if !ok {
		t.Fatalf("got %T, want promptModalSubmitMsg", got)
	}
	if !submit.planFirst {
		t.Error("ctrl+p MUST set planFirst=true")
	}
	if submit.prompt != "trivial fix" {
		t.Errorf("prompt = %q", submit.prompt)
	}
}

func TestNewSession_CtrlPEmptyPromptIsNoop(t *testing.T) {
	m := newNewSessionModel()
	m.SetSize(120, 40)
	m.Open(ViewDashboard)
	m.textarea.SetValue("   ")

	m, cmd := m.Update(tea.KeyPressMsg{Code: 'p', Mod: tea.ModCtrl})
	if cmd != nil {
		t.Fatal("ctrl+p with an empty prompt should be a no-op — there is nothing to plan")
	}
	if !m.active {
		t.Error("model should stay active on empty plan-first submit")
	}
}

func TestNewSession_ContextToggleCarriedInSubmit(t *testing.T) {
	m := openModelWithDefaults()
	m.textarea.SetValue("debug the crash")

	// Tab to the Context row (index 0) and cycle to "Current checkout".
	m, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyTab, Text: "tab"})
	m, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyRight, Text: "right"})
	if m.selectedContext() != contextCheckout {
		t.Fatalf("selectedContext = %v, want contextCheckout", m.selectedContext())
	}

	// Back to the textarea and submit.
	for m.overrideFocus != -1 {
		m, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyTab, Text: "tab"})
	}
	m, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter, Text: "enter"})
	_ = m
	if cmd == nil {
		t.Fatal("expected cmd on enter")
	}
	submit, ok := cmd().(promptModalSubmitMsg)
	if !ok {
		t.Fatalf("cmd() = %T, want promptModalSubmitMsg", cmd())
	}
	if submit.context != contextCheckout {
		t.Errorf("submit.context = %v, want contextCheckout", submit.context)
	}
	// The Context row must not leak into the model overrides.
	if submit.overrides.PlanModel != "" || submit.overrides.AgentModel != "" {
		t.Errorf("context toggle leaked into overrides: %+v", submit.overrides)
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

func TestNewSession_EmptyEnterSubmitsBlankREPL(t *testing.T) {
	m := newNewSessionModel()
	m.SetSize(120, 40)
	m.Open(ViewDashboard)
	m.textarea.SetValue("   \n\t")

	m, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter, Text: "enter"})
	if cmd == nil {
		t.Fatal("empty enter must submit — a blank claude REPL is the everyday case")
	}
	submit, ok := cmd().(promptModalSubmitMsg)
	if !ok {
		t.Fatalf("cmd() = %T, want promptModalSubmitMsg", cmd())
	}
	if submit.prompt != "" {
		t.Errorf("prompt = %q, want empty", submit.prompt)
	}
	if submit.planFirst {
		t.Error("empty enter is the raw path; planFirst must be false")
	}
	if m.active {
		t.Error("model should deactivate on submit")
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

func openModelWithDefaults() newSessionModel {
	m := newNewSessionModel()
	m.SetSize(140, 40)
	m.SetDefaults(config.ResolvedSettings{
		PlanModel:  config.KnownModels[0],
		AgentModel: config.KnownAgentModels[0],
	})
	m.Open(ViewDashboard)
	return m
}

func TestNewSession_Tab_MovesFocusFromTextareaToOverrides(t *testing.T) {
	m := openModelWithDefaults()
	if m.overrideFocus != -1 {
		t.Fatalf("initial overrideFocus = %d, want -1 (textarea)", m.overrideFocus)
	}
	m, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyTab, Text: "tab"})
	if m.overrideFocus != 0 {
		t.Errorf("after one tab: overrideFocus = %d, want 0", m.overrideFocus)
	}
	if m.textarea.Focused() {
		t.Error("textarea should be blurred when overrideFocus >= 0")
	}
}

func TestNewSession_TabWalksOverrideFields(t *testing.T) {
	m := openModelWithDefaults()
	n := len(m.overrideFields)
	for i := 0; i < n; i++ {
		m, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyTab, Text: "tab"})
		if m.overrideFocus != i {
			t.Errorf("after %d tabs: overrideFocus = %d, want %d", i+1, m.overrideFocus, i)
		}
	}
	// One more tab past the last field wraps back to textarea.
	m, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyTab, Text: "tab"})
	if m.overrideFocus != -1 {
		t.Errorf("tab past last field: overrideFocus = %d, want -1 (textarea)", m.overrideFocus)
	}
	if !m.textarea.Focused() {
		t.Error("textarea should be focused when overrideFocus == -1")
	}
}

func TestNewSession_ShiftTabReverses(t *testing.T) {
	m := openModelWithDefaults()
	// Start at textarea (overrideFocus=-1), shift+tab wraps to last field.
	m, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyTab, Mod: tea.ModShift, Text: "shift+tab"})
	n := len(m.overrideFields)
	if m.overrideFocus != n-1 {
		t.Errorf("shift+tab from textarea: overrideFocus = %d, want %d (last field)", m.overrideFocus, n-1)
	}
	// shift+tab from last field goes to second-to-last.
	m, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyTab, Mod: tea.ModShift, Text: "shift+tab"})
	if m.overrideFocus != n-2 {
		t.Errorf("shift+tab from last: overrideFocus = %d, want %d", m.overrideFocus, n-2)
	}
}

func TestNewSession_LeftRight_CyclesSelectOption(t *testing.T) {
	m := openModelWithDefaults()
	// Tab to Context (index 0, a select field).
	m, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyTab, Text: "tab"})
	if m.overrideFocus != 0 {
		t.Fatalf("overrideFocus = %d, want 0", m.overrideFocus)
	}
	initial := m.overrideFields[0].selected
	// Right should advance selection.
	m, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyRight, Text: "right"})
	want := (initial + 1) % len(m.overrideFields[0].options)
	if m.overrideFields[0].selected != want {
		t.Errorf("after right: selected = %d, want %d", m.overrideFields[0].selected, want)
	}
	// Left should retreat selection.
	m, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyLeft, Text: "left"})
	if m.overrideFields[0].selected != initial {
		t.Errorf("after left: selected = %d, want %d (initial)", m.overrideFields[0].selected, initial)
	}
}

func TestNewSession_Space_TogglesBypass(t *testing.T) {
	m := openModelWithDefaults()
	// Tab to last field (Bypass Permissions, a toggle).
	n := len(m.overrideFields)
	for i := 0; i < n; i++ {
		m, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyTab, Text: "tab"})
	}
	if m.overrideFocus != n-1 {
		t.Fatalf("expected focus on last field (%d), got %d", n-1, m.overrideFocus)
	}
	before := m.overrideFields[n-1].toggleValue
	m, _ = m.Update(tea.KeyPressMsg{Code: ' ', Text: " "})
	if m.overrideFields[n-1].toggleValue == before {
		t.Error("space on toggle field should flip toggleValue")
	}
}

func TestNewSession_EnterOnOverrideField_DoesNotSubmit(t *testing.T) {
	m := openModelWithDefaults()
	m.textarea.SetValue("my goal")
	// Move focus to first override field.
	m, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyTab, Text: "tab"})
	// Press enter — should NOT emit submit.
	m, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter, Text: "enter"})
	if cmd != nil {
		msg := cmd()
		if _, ok := msg.(promptModalSubmitMsg); ok {
			t.Fatal("enter on override field should NOT emit promptModalSubmitMsg")
		}
	}
	if !m.active {
		t.Error("model should remain active; enter on override field is not a submit")
	}
}

func TestNewSession_EnterOnSelectField_CyclesSelection(t *testing.T) {
	m := openModelWithDefaults()
	// Tab to Plan Model (select field, index 0).
	m, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyTab, Text: "tab"})
	before := m.overrideFields[0].selected
	m, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyEnter, Text: "enter"})
	want := (before + 1) % len(m.overrideFields[0].options)
	if m.overrideFields[0].selected != want {
		t.Errorf("enter on select: selected = %d, want %d", m.overrideFields[0].selected, want)
	}
}

func TestNewSession_SubmitCarriesOverrides_PlanModel(t *testing.T) {
	m := newNewSessionModel()
	m.SetSize(140, 40)
	m.SetDefaults(config.ResolvedSettings{
		PlanModel:  config.KnownModels[0],
		AgentModel: config.KnownAgentModels[0],
	})
	m.Open(ViewDashboard)
	m.textarea.SetValue("my goal")

	// Tab past Context to Plan Model, cycle right to select a different value.
	m, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyTab, Text: "tab"})
	m, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyTab, Text: "tab"})
	m, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyRight, Text: "right"})
	wantPlan := config.KnownModels[1]

	// Tab back to textarea and submit.
	for m.overrideFocus != -1 {
		m, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyTab, Text: "tab"})
	}
	m, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter, Text: "enter"})
	_ = m
	if cmd == nil {
		t.Fatal("expected cmd on enter")
	}
	got, ok := cmd().(promptModalSubmitMsg)
	if !ok {
		t.Fatalf("cmd() = %T, want promptModalSubmitMsg", cmd())
	}
	if got.overrides.PlanModel != wantPlan {
		t.Errorf("overrides.PlanModel = %q, want %q", got.overrides.PlanModel, wantPlan)
	}
}

func TestNewSession_SubmitCarriesOverrides_DefaultsTreatedAsNoOverride(t *testing.T) {
	m := newNewSessionModel()
	m.SetSize(140, 40)
	// Defaults match the first option in each list (no change).
	m.SetDefaults(config.ResolvedSettings{
		PlanModel:  config.KnownModels[0],
		AgentModel: config.KnownAgentModels[0],
	})
	m.Open(ViewDashboard)
	m.textarea.SetValue("my goal")

	// Submit without changing anything.
	m, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter, Text: "enter"})
	_ = m
	if cmd == nil {
		t.Fatal("expected cmd on enter")
	}
	got, ok := cmd().(promptModalSubmitMsg)
	if !ok {
		t.Fatalf("cmd() = %T, want promptModalSubmitMsg", cmd())
	}
	if got.overrides.PlanModel != "" {
		t.Errorf("overrides.PlanModel = %q, want \"\" (no override when equal to default)", got.overrides.PlanModel)
	}
	if got.overrides.AgentModel != "" {
		t.Errorf("overrides.AgentModel = %q, want \"\" (no override when equal to default)", got.overrides.AgentModel)
	}
	if got.overrides.BypassPermissions != nil {
		t.Errorf("overrides.BypassPermissions = %v, want nil (no override when equal to default)", got.overrides.BypassPermissions)
	}
}

func TestNewSession_EscCancelsFromOverrideField(t *testing.T) {
	m := openModelWithDefaults()
	// Move focus to override.
	m, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyTab, Text: "tab"})
	// Esc should cancel.
	m, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	if cmd == nil {
		t.Fatal("esc from override field should emit cancel cmd")
	}
	if _, ok := cmd().(promptModalCancelMsg); !ok {
		t.Fatal("esc from override field should emit promptModalCancelMsg")
	}
	if m.active {
		t.Error("model should deactivate on esc")
	}
}
