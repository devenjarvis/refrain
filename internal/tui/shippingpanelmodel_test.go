package tui

import (
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/devenjarvis/refrain/internal/agent"
	"github.com/devenjarvis/refrain/internal/github"
)

// newTestShippingSvc returns a stub PanelServices for shipping-panel tests
// plus a state value the test can inspect after Update.
func newTestShippingSvc() (PanelServices, *testShippingSvcState) {
	state := &testShippingSvcState{
		triage: map[string]*feedbackTriageEntry{},
	}
	svc := PanelServices{
		Width:  120,
		Height: 40,
		ManagerFor: func(string) (SessionManager, string) {
			return nil, ""
		},
		PRCache: func(string) *prCacheEntry { return state.pr },
		ClosePanel: func() {
			state.closed = true
		},
		OpenInLaunch: func(*agent.Session) bool {
			return state.openInLaunchResult
		},
		SetError: func(msg string) {
			state.errMsg = msg
		},
		OpenURL: func(string) error {
			state.openedURL = true
			return nil
		},
		MergePRCmd: func(sessionID string, force bool) tea.Cmd {
			state.mergeRequested = true
			state.mergeForce = force
			return func() tea.Msg { return nil }
		},
		FeedbackTriage: func(string) map[string]*feedbackTriageEntry {
			return state.triage
		},
		SetFeedbackVerdict: func(_, key string, v feedbackVerdict) {
			if state.triage[key] == nil {
				state.triage[key] = &feedbackTriageEntry{}
			}
			state.triage[key].Verdict = v
		},
		SetFeedbackNote: func(_, key, note string) {
			if state.triage[key] == nil {
				state.triage[key] = &feedbackTriageEntry{}
			}
			state.triage[key].Note = note
		},
	}
	return svc, state
}

type testShippingSvcState struct {
	pr                 *prCacheEntry
	triage             map[string]*feedbackTriageEntry
	closed             bool
	errMsg             string
	openedURL          bool
	openInLaunchResult bool
	mergeRequested     bool
	mergeForce         bool
}

// TestShippingPanelModel_EscCloses verifies esc closes the panel.
func TestShippingPanelModel_EscCloses(t *testing.T) {
	sess := agent.NewSessionForTest("s1", "ship")
	panel := newShippingPanel(sess, 120, 40)
	svc, state := newTestShippingSvc()

	_, _ = panel.Update(tea.KeyPressMsg{Code: tea.KeyEscape}, svc)
	if !state.closed {
		t.Fatal("expected ClosePanel on esc")
	}
}

// TestShippingPanelModel_MergeBlockedWhenNotReady verifies 'm' surfaces an
// error and does not call MergePRCmd when the PR isn't merge-ready.
func TestShippingPanelModel_MergeBlockedWhenNotReady(t *testing.T) {
	sess := agent.NewSessionForTest("s1", "ship")
	panel := newShippingPanel(sess, 120, 40)
	svc, state := newTestShippingSvc()
	// No PR entry => not merge-ready.

	_, _ = panel.Update(tea.KeyPressMsg{Code: 'm', Text: "m"}, svc)
	if state.mergeRequested {
		t.Error("expected merge to be blocked when PR not ready")
	}
	if state.errMsg == "" {
		t.Error("expected error to be surfaced when not ready")
	}
}

// TestShippingPanelModel_ForceMergeBypasses verifies 'M' bypasses the ready
// gate and calls MergePRCmd with force=true.
func TestShippingPanelModel_ForceMergeBypasses(t *testing.T) {
	sess := agent.NewSessionForTest("s1", "ship")
	panel := newShippingPanel(sess, 120, 40)
	svc, state := newTestShippingSvc()
	state.pr = &prCacheEntry{pr: &github.PRState{Number: 1}}

	_, _ = panel.Update(tea.KeyPressMsg{Code: 'M', Text: "M"}, svc)
	if !state.mergeRequested {
		t.Fatal("expected force-merge to be requested")
	}
	if !state.mergeForce {
		t.Error("expected force=true on force-merge")
	}
}

// TestShippingPanelModel_PKeyOpensPRURL verifies 'p' calls svc.OpenURL when
// a URL is cached.
func TestShippingPanelModel_PKeyOpensPRURL(t *testing.T) {
	sess := agent.NewSessionForTest("s1", "ship")
	panel := newShippingPanel(sess, 120, 40)
	svc, state := newTestShippingSvc()
	state.pr = &prCacheEntry{pr: &github.PRState{URL: "https://example/pr/1"}}

	_, _ = panel.Update(tea.KeyPressMsg{Code: 'p', Text: "p"}, svc)
	if !state.openedURL {
		t.Error("expected OpenURL invoked when PR URL is cached")
	}
}

// shippingPanelWithFeedback returns a panel + svc + state where the cache has
// two feedback items so cursor/verdict/note keys can be exercised.
func shippingPanelWithFeedback() (*shippingPanelModel, PanelServices, *testShippingSvcState) {
	sess := agent.NewSessionForTest("s1", "ship")
	panel := newShippingPanel(sess, 120, 40)
	svc, state := newTestShippingSvc()
	state.pr = &prCacheEntry{
		pr: &github.PRState{Number: 1, URL: "https://example/pr/1"},
		threads: []github.ReviewThread{
			{
				Reviewer: "alice",
				State:    "CHANGES_REQUESTED",
				Body:     "Top-level critique",
				Comments: []github.ReviewComment{
					{ID: 100, Path: "main.go", Line: 42, Body: "inline 1"},
					{ID: 101, Path: "main.go", Line: 50, Body: "inline 2"},
				},
			},
		},
	}
	return panel, svc, state
}

func TestShippingPanelModel_JK_MovesFeedbackCursor(t *testing.T) {
	panel, svc, _ := shippingPanelWithFeedback()
	if panel.FeedbackCursor() != 0 {
		t.Fatalf("test prereq: cursor=%d, want 0", panel.FeedbackCursor())
	}
	_, _ = panel.Update(tea.KeyPressMsg{Code: 'j', Text: "j"}, svc)
	if panel.FeedbackCursor() != 1 {
		t.Errorf("after j cursor=%d, want 1", panel.FeedbackCursor())
	}
	_, _ = panel.Update(tea.KeyPressMsg{Code: 'k', Text: "k"}, svc)
	if panel.FeedbackCursor() != 0 {
		t.Errorf("after k cursor=%d, want 0", panel.FeedbackCursor())
	}
}

func TestShippingPanelModel_JK_ClampsAtBounds(t *testing.T) {
	panel, svc, _ := shippingPanelWithFeedback()
	for range 10 {
		_, _ = panel.Update(tea.KeyPressMsg{Code: 'j', Text: "j"}, svc)
	}
	// 3 items in fixture (1 body + 2 inlines) — cursor should clamp at index 2.
	if panel.FeedbackCursor() != 2 {
		t.Errorf("over-scroll cursor=%d, want 2", panel.FeedbackCursor())
	}
	// k below 0 clamps.
	for range 10 {
		_, _ = panel.Update(tea.KeyPressMsg{Code: 'k', Text: "k"}, svc)
	}
	if panel.FeedbackCursor() != 0 {
		t.Errorf("under-scroll cursor=%d, want 0", panel.FeedbackCursor())
	}
}

func TestShippingPanelModel_JK_ResetsDetailScroll(t *testing.T) {
	panel, svc, _ := shippingPanelWithFeedback()
	_, _ = panel.Update(tea.KeyPressMsg{Code: tea.KeyPgDown}, svc)
	if panel.DetailScroll() == 0 {
		t.Fatal("test prereq: detailScroll should be > 0 after pgdown")
	}
	_, _ = panel.Update(tea.KeyPressMsg{Code: 'j', Text: "j"}, svc)
	if panel.DetailScroll() != 0 {
		t.Errorf("j should reset detailScroll, got %d", panel.DetailScroll())
	}
}

func TestShippingPanelModel_PgDownPgUp_ScrollDetail(t *testing.T) {
	panel, svc, _ := shippingPanelWithFeedback()
	_, _ = panel.Update(tea.KeyPressMsg{Code: tea.KeyPgDown}, svc)
	if panel.DetailScroll() == 0 {
		t.Errorf("detailScroll = 0 after pgdown, want > 0")
	}
	first := panel.DetailScroll()
	_, _ = panel.Update(tea.KeyPressMsg{Code: tea.KeyPgUp}, svc)
	if panel.DetailScroll() >= first {
		t.Errorf("pgup did not reduce detailScroll: was %d, now %d", first, panel.DetailScroll())
	}
}

func TestShippingPanelModel_PgUp_ClampsAtZero(t *testing.T) {
	panel, svc, _ := shippingPanelWithFeedback()
	if panel.DetailScroll() != 0 {
		t.Fatal("test prereq: detailScroll should start 0")
	}
	_, _ = panel.Update(tea.KeyPressMsg{Code: tea.KeyPgUp}, svc)
	if panel.DetailScroll() != 0 {
		t.Errorf("pgup at top should clamp to 0, got %d", panel.DetailScroll())
	}
}

func TestShippingPanelModel_CtrlD_CtrlU_HalfPageScroll(t *testing.T) {
	panel, svc, _ := shippingPanelWithFeedback()
	_, _ = panel.Update(tea.KeyPressMsg{Code: 'd', Mod: tea.ModCtrl}, svc)
	if panel.DetailScroll() == 0 {
		t.Errorf("ctrl+d did not advance detailScroll")
	}
	pre := panel.DetailScroll()
	_, _ = panel.Update(tea.KeyPressMsg{Code: 'u', Mod: tea.ModCtrl}, svc)
	if panel.DetailScroll() >= pre {
		t.Errorf("ctrl+u did not reduce detailScroll: pre=%d post=%d", pre, panel.DetailScroll())
	}
}

func TestShippingPanelModel_AKeyApprovesCursorItem(t *testing.T) {
	panel, svc, state := shippingPanelWithFeedback()
	_, _ = panel.Update(tea.KeyPressMsg{Code: 'a', Text: "a"}, svc)
	// Cursor starts at 0 → the thread body item → key "thread:alice".
	got := state.triage["thread:alice"]
	if got == nil || got.Verdict != feedbackApproved {
		t.Errorf("verdict for thread:alice = %+v, want approved", got)
	}
}

func TestShippingPanelModel_XKeyDisagrees(t *testing.T) {
	panel, svc, state := shippingPanelWithFeedback()
	_, _ = panel.Update(tea.KeyPressMsg{Code: 'x', Text: "x"}, svc)
	got := state.triage["thread:alice"]
	if got == nil || got.Verdict != feedbackDisagreed {
		t.Errorf("verdict = %+v, want disagreed", got)
	}
}

func TestShippingPanelModel_UKeyResetsToNeutral(t *testing.T) {
	panel, svc, state := shippingPanelWithFeedback()
	_, _ = panel.Update(tea.KeyPressMsg{Code: 'a', Text: "a"}, svc)
	_, _ = panel.Update(tea.KeyPressMsg{Code: 'u', Text: "u"}, svc)
	got := state.triage["thread:alice"]
	if got == nil || got.Verdict != feedbackNeutral {
		t.Errorf("verdict after 'a' then 'u' = %+v, want neutral", got)
	}
}

func TestShippingPanelModel_AXU_NoItems_NoOp(t *testing.T) {
	// Without any feedback items, the verdict keys must be no-ops, not
	// crashes or off-by-one writes.
	sess := agent.NewSessionForTest("s1", "ship")
	panel := newShippingPanel(sess, 120, 40)
	svc, state := newTestShippingSvc()
	for _, k := range []rune{'a', 'x', 'u'} {
		_, _ = panel.Update(tea.KeyPressMsg{Code: k, Text: string(k)}, svc)
	}
	if len(state.triage) != 0 {
		t.Errorf("verdict keys should not have written into triage on empty list, got %d entries", len(state.triage))
	}
}

func TestShippingPanelModel_NKey_OpensFeedbackNoteModal(t *testing.T) {
	panel, svc, _ := shippingPanelWithFeedback()
	if panel.NoteActive() {
		t.Fatal("test prereq: note modal should start inactive")
	}
	_, _ = panel.Update(tea.KeyPressMsg{Code: 'n', Text: "n"}, svc)
	if !panel.NoteActive() {
		t.Error("n should open feedback note modal")
	}
}

func TestShippingPanelModel_NKey_NoItems_DoesNotOpenModal(t *testing.T) {
	sess := agent.NewSessionForTest("s1", "ship")
	panel := newShippingPanel(sess, 120, 40)
	svc, _ := newTestShippingSvc()
	_, _ = panel.Update(tea.KeyPressMsg{Code: 'n', Text: "n"}, svc)
	if panel.NoteActive() {
		t.Error("n with no items should not open the note modal")
	}
}

func TestShippingPanelModel_NoteModalInterceptsKeys(t *testing.T) {
	panel, svc, state := shippingPanelWithFeedback()
	_, _ = panel.Update(tea.KeyPressMsg{Code: 'n', Text: "n"}, svc) // open
	if !panel.NoteActive() {
		t.Fatal("test prereq: note modal should be open")
	}
	// Typing 'j' should reach the textarea, NOT advance the feedback cursor.
	cursorBefore := panel.FeedbackCursor()
	_, _ = panel.Update(tea.KeyPressMsg{Code: 'j', Text: "j"}, svc)
	if panel.FeedbackCursor() != cursorBefore {
		t.Error("j while note modal active moved feedback cursor")
	}
	// Esc cancels and does NOT close the shipping panel.
	_, _ = panel.Update(tea.KeyPressMsg{Code: tea.KeyEscape}, svc)
	if state.closed {
		t.Error("esc inside note modal closed the shipping panel")
	}
	if panel.NoteActive() {
		t.Error("esc should close the note modal")
	}
}

func TestShippingPanelModel_NoteModalEnter_SavesNote(t *testing.T) {
	panel, svc, state := shippingPanelWithFeedback()
	_, _ = panel.Update(tea.KeyPressMsg{Code: 'n', Text: "n"}, svc)
	panel.feedbackNote.ta.SetValue("important note")
	_, _ = panel.Update(tea.KeyPressMsg{Code: tea.KeyEnter}, svc)
	if panel.NoteActive() {
		t.Error("enter should close the modal")
	}
	got := state.triage["thread:alice"]
	if got == nil || got.Note != "important note" {
		t.Errorf("triage note = %+v, want %q", got, "important note")
	}
}

func TestShippingPanelModel_TKey_OpensInLaunch(t *testing.T) {
	panel, svc, state := shippingPanelWithFeedback()
	state.openInLaunchResult = true
	_, _ = panel.Update(tea.KeyPressMsg{Code: 't', Text: "t"}, svc)
	if !state.closed {
		t.Error("t should close the shipping panel")
	}
	if state.errMsg != "" {
		t.Errorf("t with successful OpenInLaunch should not error, got %q", state.errMsg)
	}
}

func TestShippingPanelModel_TKey_OpenFail_SetsError(t *testing.T) {
	panel, svc, state := shippingPanelWithFeedback()
	state.openInLaunchResult = false
	_, _ = panel.Update(tea.KeyPressMsg{Code: 't', Text: "t"}, svc)
	if !state.closed {
		t.Error("t should close even when launch fails")
	}
	if state.errMsg == "" {
		t.Error("t with failed OpenInLaunch should surface an error")
	}
}

func TestShippingPanelModel_PKey_NoURL_SetsError(t *testing.T) {
	sess := agent.NewSessionForTest("s1", "ship")
	panel := newShippingPanel(sess, 120, 40)
	svc, state := newTestShippingSvc()
	// No PR entry => no URL.
	_, _ = panel.Update(tea.KeyPressMsg{Code: 'p', Text: "p"}, svc)
	if state.errMsg == "" {
		t.Error("p without a PR URL should surface an error")
	}
	if state.openedURL {
		t.Error("p without a PR URL should not call OpenURL")
	}
}

func TestShippingPanelModel_MergeReady_CallsMergeCmd(t *testing.T) {
	sess := agent.NewSessionForTest("s1", "ship")
	panel := newShippingPanel(sess, 120, 40)
	svc, state := newTestShippingSvc()
	state.pr = &prCacheEntry{
		pr:      &github.PRState{Number: 1, MergeableState: "clean"},
		checks:  &github.CheckStatus{State: "success", Total: 3},
		reviews: &github.ReviewStatus{State: "approved"},
	}
	_, _ = panel.Update(tea.KeyPressMsg{Code: 'm', Text: "m"}, svc)
	if !state.mergeRequested {
		t.Error("m should call MergePRCmd when ready")
	}
	if state.mergeForce {
		t.Error("m should pass force=false")
	}
	if state.errMsg != "" {
		t.Errorf("m when ready should not error, got %q", state.errMsg)
	}
}

func TestShippingPanelModel_ForceMerge_NoPR_SetsError(t *testing.T) {
	sess := agent.NewSessionForTest("s1", "ship")
	panel := newShippingPanel(sess, 120, 40)
	svc, state := newTestShippingSvc()
	// No PR entry.
	_, _ = panel.Update(tea.KeyPressMsg{Code: 'M', Text: "M"}, svc)
	if state.mergeRequested {
		t.Error("force-merge without a PR should not call MergePRCmd")
	}
	if state.errMsg == "" {
		t.Error("expected error when force-merging with no PR")
	}
}

func TestShippingPanelModel_RKey_EmitsFeedbackRequestMsg(t *testing.T) {
	panel, svc, _ := shippingPanelWithFeedback()
	_, cmd := panel.Update(tea.KeyPressMsg{Code: 'r', Text: "r"}, svc)
	if cmd == nil {
		t.Fatal("expected cmd from r")
	}
	msg, ok := cmd().(shippingFeedbackRequestMsg)
	if !ok {
		t.Fatalf("got %T, want shippingFeedbackRequestMsg", cmd())
	}
	if msg.sessionID != "s1" {
		t.Errorf("sessionID = %q, want s1", msg.sessionID)
	}
}

func TestShippingPanelModel_UnknownKey_NoOp(t *testing.T) {
	panel, svc, state := shippingPanelWithFeedback()
	before := struct {
		cursor int
		scroll int
		closed bool
		err    string
		merge  bool
	}{panel.FeedbackCursor(), panel.DetailScroll(), state.closed, state.errMsg, state.mergeRequested}

	for _, k := range []tea.KeyPressMsg{
		{Code: 'z', Text: "z"},
		{Code: 'q', Text: "q"},
		{Code: 'w', Mod: tea.ModCtrl},
	} {
		_, cmd := panel.Update(k, svc)
		if cmd != nil {
			t.Errorf("unknown key %v produced cmd %T, want nil", k, cmd())
		}
	}

	after := struct {
		cursor int
		scroll int
		closed bool
		err    string
		merge  bool
	}{panel.FeedbackCursor(), panel.DetailScroll(), state.closed, state.errMsg, state.mergeRequested}
	if before != after {
		t.Errorf("unknown keys changed state: before=%+v after=%+v", before, after)
	}
}
