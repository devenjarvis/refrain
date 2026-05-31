package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/charmbracelet/x/ansi"
	"github.com/devenjarvis/refrain/internal/agent"
	"github.com/devenjarvis/refrain/internal/github"
)

// testShippingSvcState records the side effects a shipping-panel test wants to
// assert on. Post-§3-fold (panel.go), reference-typed handles are injected via
// shippingDeps at construction and recorded here; the scalar effects that used
// to run through service closures (close, error, open-URL, open-in-launch) now
// flow as messages the panel returns, so those are asserted on the emitted
// tea.Cmd via runCmdAll/findMsg rather than recorded here.
type testShippingSvcState struct {
	pr             *prCacheEntry
	triage         map[string]*feedbackTriageEntry
	mergeRequested bool
	mergeForce     bool
}

// newTestShippingDeps returns a stub shippingDeps for shipping-panel tests plus
// a state value the test can inspect after Update. The PR cache and feedback
// triage map are read through the deps closures; verdict/note setters and the
// merge cmd record into state.
func newTestShippingDeps() (shippingDeps, *testShippingSvcState) {
	state := &testShippingSvcState{
		triage: map[string]*feedbackTriageEntry{},
	}
	deps := shippingDeps{
		PRCache: func(string, string) *prCacheEntry { return state.pr },
		FeedbackTriage: func(string, string) map[string]*feedbackTriageEntry {
			return state.triage
		},
		SetFeedbackVerdict: func(_, _, key string, v feedbackVerdict) {
			if state.triage[key] == nil {
				state.triage[key] = &feedbackTriageEntry{}
			}
			state.triage[key].Verdict = v
		},
		SetFeedbackNote: func(_, _, key, note string) {
			if state.triage[key] == nil {
				state.triage[key] = &feedbackTriageEntry{}
			}
			state.triage[key].Note = note
		},
		MergePRCmd: func(sessionID, repoPath string, force bool) tea.Cmd {
			state.mergeRequested = true
			state.mergeForce = force
			return func() tea.Msg { return mergePRMsg{sessionID: sessionID, repoPath: repoPath} }
		},
	}
	return deps, state
}

// cmdClosesShipping reports whether cmd yields a panelCloseMsg.
func cmdClosesShipping(t *testing.T, cmd tea.Cmd) bool {
	t.Helper()
	if cmd == nil {
		return false
	}
	_, ok := findMsg[panelCloseMsg](runCmdAll(t, cmd))
	return ok
}

// cmdShippingError returns the error text from a setErrorMsg in cmd's output, or "".
func cmdShippingError(t *testing.T, cmd tea.Cmd) string {
	t.Helper()
	if cmd == nil {
		return ""
	}
	if m, ok := findMsg[setErrorMsg](runCmdAll(t, cmd)); ok {
		return m.text
	}
	return ""
}

// TestShippingPanelModel_EscCloses verifies esc emits panelCloseMsg.
func TestShippingPanelModel_EscCloses(t *testing.T) {
	sess := agent.NewSessionForTest("s1", "ship")
	deps, _ := newTestShippingDeps()
	panel := newShippingPanel(sess, "", 120, 40, deps)

	_, cmd := panel.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	if !cmdClosesShipping(t, cmd) {
		t.Fatal("expected panelCloseMsg on esc")
	}
}

// TestShippingPanelModel_MergeBlockedWhenNotReady verifies 'm' surfaces an
// error and does not call MergePRCmd when the PR isn't merge-ready.
func TestShippingPanelModel_MergeBlockedWhenNotReady(t *testing.T) {
	sess := agent.NewSessionForTest("s1", "ship")
	deps, state := newTestShippingDeps()
	panel := newShippingPanel(sess, "", 120, 40, deps)
	// No PR entry => not merge-ready.

	_, cmd := panel.Update(tea.KeyPressMsg{Code: 'm', Text: "m"})
	if state.mergeRequested {
		t.Error("expected merge to be blocked when PR not ready")
	}
	if cmdShippingError(t, cmd) == "" {
		t.Error("expected error to be surfaced when not ready")
	}
}

// TestShippingPanelModel_ForceMergeBypasses verifies 'M' bypasses the ready
// gate and calls MergePRCmd with force=true.
func TestShippingPanelModel_ForceMergeBypasses(t *testing.T) {
	sess := agent.NewSessionForTest("s1", "ship")
	deps, state := newTestShippingDeps()
	panel := newShippingPanel(sess, "", 120, 40, deps)
	state.pr = &prCacheEntry{pr: &github.PRState{Number: 1}}

	_, cmd := panel.Update(tea.KeyPressMsg{Code: 'M', Text: "M"})
	_ = runCmdAll(t, cmd)
	if !state.mergeRequested {
		t.Fatal("expected force-merge to be requested")
	}
	if !state.mergeForce {
		t.Error("expected force=true on force-merge")
	}
}

// TestShippingPanelModel_PKeyOpensPRURL verifies 'p' emits an open-URL request
// when a URL is cached.
func TestShippingPanelModel_PKeyOpensPRURL(t *testing.T) {
	sess := agent.NewSessionForTest("s1", "ship")
	deps, state := newTestShippingDeps()
	panel := newShippingPanel(sess, "", 120, 40, deps)
	state.pr = &prCacheEntry{pr: &github.PRState{URL: "https://example/pr/1"}}

	_, cmd := panel.Update(tea.KeyPressMsg{Code: 'p', Text: "p"})
	if _, ok := findMsg[openURLResultMsg](runCmdAll(t, cmd)); !ok {
		t.Error("expected an open-URL request when PR URL is cached")
	}
}

// shippingPanelWithFeedback returns a panel + deps + state where the cache has
// three feedback items so cursor/verdict/note keys can be exercised.
func shippingPanelWithFeedback() (*shippingPanelModel, shippingDeps, *testShippingSvcState) {
	sess := agent.NewSessionForTest("s1", "ship")
	deps, state := newTestShippingDeps()
	panel := newShippingPanel(sess, "", 120, 40, deps)
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
	return panel, deps, state
}

func TestShippingPanelModel_JK_MovesFeedbackCursor(t *testing.T) {
	panel, _, _ := shippingPanelWithFeedback()
	if panel.FeedbackCursor() != 0 {
		t.Fatalf("test prereq: cursor=%d, want 0", panel.FeedbackCursor())
	}
	_, _ = panel.Update(tea.KeyPressMsg{Code: 'j', Text: "j"})
	if panel.FeedbackCursor() != 1 {
		t.Errorf("after j cursor=%d, want 1", panel.FeedbackCursor())
	}
	_, _ = panel.Update(tea.KeyPressMsg{Code: 'k', Text: "k"})
	if panel.FeedbackCursor() != 0 {
		t.Errorf("after k cursor=%d, want 0", panel.FeedbackCursor())
	}
}

func TestShippingPanelModel_JK_ClampsAtBounds(t *testing.T) {
	panel, _, _ := shippingPanelWithFeedback()
	for range 10 {
		_, _ = panel.Update(tea.KeyPressMsg{Code: 'j', Text: "j"})
	}
	// 3 items in fixture (1 body + 2 inlines) — cursor should clamp at index 2.
	if panel.FeedbackCursor() != 2 {
		t.Errorf("over-scroll cursor=%d, want 2", panel.FeedbackCursor())
	}
	// k below 0 clamps.
	for range 10 {
		_, _ = panel.Update(tea.KeyPressMsg{Code: 'k', Text: "k"})
	}
	if panel.FeedbackCursor() != 0 {
		t.Errorf("under-scroll cursor=%d, want 0", panel.FeedbackCursor())
	}
}

func TestShippingPanelModel_JK_ResetsDetailScroll(t *testing.T) {
	panel, _, _ := shippingPanelWithFeedback()
	_, _ = panel.Update(tea.KeyPressMsg{Code: tea.KeyPgDown})
	if panel.DetailScroll() == 0 {
		t.Fatal("test prereq: detailScroll should be > 0 after pgdown")
	}
	_, _ = panel.Update(tea.KeyPressMsg{Code: 'j', Text: "j"})
	if panel.DetailScroll() != 0 {
		t.Errorf("j should reset detailScroll, got %d", panel.DetailScroll())
	}
}

func TestShippingPanelModel_PgDownPgUp_ScrollDetail(t *testing.T) {
	panel, _, _ := shippingPanelWithFeedback()
	_, _ = panel.Update(tea.KeyPressMsg{Code: tea.KeyPgDown})
	if panel.DetailScroll() == 0 {
		t.Errorf("detailScroll = 0 after pgdown, want > 0")
	}
	first := panel.DetailScroll()
	_, _ = panel.Update(tea.KeyPressMsg{Code: tea.KeyPgUp})
	if panel.DetailScroll() >= first {
		t.Errorf("pgup did not reduce detailScroll: was %d, now %d", first, panel.DetailScroll())
	}
}

func TestShippingPanelModel_PgUp_ClampsAtZero(t *testing.T) {
	panel, _, _ := shippingPanelWithFeedback()
	if panel.DetailScroll() != 0 {
		t.Fatal("test prereq: detailScroll should start 0")
	}
	_, _ = panel.Update(tea.KeyPressMsg{Code: tea.KeyPgUp})
	if panel.DetailScroll() != 0 {
		t.Errorf("pgup at top should clamp to 0, got %d", panel.DetailScroll())
	}
}

func TestShippingPanelModel_CtrlD_CtrlU_HalfPageScroll(t *testing.T) {
	panel, _, _ := shippingPanelWithFeedback()
	_, _ = panel.Update(tea.KeyPressMsg{Code: 'd', Mod: tea.ModCtrl})
	if panel.DetailScroll() == 0 {
		t.Errorf("ctrl+d did not advance detailScroll")
	}
	pre := panel.DetailScroll()
	_, _ = panel.Update(tea.KeyPressMsg{Code: 'u', Mod: tea.ModCtrl})
	if panel.DetailScroll() >= pre {
		t.Errorf("ctrl+u did not reduce detailScroll: pre=%d post=%d", pre, panel.DetailScroll())
	}
}

func TestShippingPanelModel_AKeyApprovesCursorItem(t *testing.T) {
	panel, _, state := shippingPanelWithFeedback()
	_, _ = panel.Update(tea.KeyPressMsg{Code: 'a', Text: "a"})
	// Cursor starts at 0 → the thread body item → key "thread:alice".
	got := state.triage["thread:alice"]
	if got == nil || got.Verdict != feedbackApproved {
		t.Errorf("verdict for thread:alice = %+v, want approved", got)
	}
}

func TestShippingPanelModel_XKeyDisagrees(t *testing.T) {
	panel, _, state := shippingPanelWithFeedback()
	_, _ = panel.Update(tea.KeyPressMsg{Code: 'x', Text: "x"})
	got := state.triage["thread:alice"]
	if got == nil || got.Verdict != feedbackDisagreed {
		t.Errorf("verdict = %+v, want disagreed", got)
	}
}

func TestShippingPanelModel_UKeyResetsToNeutral(t *testing.T) {
	panel, _, state := shippingPanelWithFeedback()
	_, _ = panel.Update(tea.KeyPressMsg{Code: 'a', Text: "a"})
	_, _ = panel.Update(tea.KeyPressMsg{Code: 'u', Text: "u"})
	got := state.triage["thread:alice"]
	if got == nil || got.Verdict != feedbackNeutral {
		t.Errorf("verdict after 'a' then 'u' = %+v, want neutral", got)
	}
}

func TestShippingPanelModel_AXU_NoItems_NoOp(t *testing.T) {
	// Without any feedback items, the verdict keys must be no-ops, not
	// crashes or off-by-one writes.
	sess := agent.NewSessionForTest("s1", "ship")
	deps, state := newTestShippingDeps()
	panel := newShippingPanel(sess, "", 120, 40, deps)
	for _, k := range []rune{'a', 'x', 'u'} {
		_, _ = panel.Update(tea.KeyPressMsg{Code: k, Text: string(k)})
	}
	if len(state.triage) != 0 {
		t.Errorf("verdict keys should not have written into triage on empty list, got %d entries", len(state.triage))
	}
}

func TestShippingPanelModel_NKey_OpensFeedbackNoteModal(t *testing.T) {
	panel, _, _ := shippingPanelWithFeedback()
	if panel.NoteActive() {
		t.Fatal("test prereq: note modal should start inactive")
	}
	_, _ = panel.Update(tea.KeyPressMsg{Code: 'n', Text: "n"})
	if !panel.NoteActive() {
		t.Error("n should open feedback note modal")
	}
}

func TestShippingPanelModel_NKey_NoItems_DoesNotOpenModal(t *testing.T) {
	sess := agent.NewSessionForTest("s1", "ship")
	deps, _ := newTestShippingDeps()
	panel := newShippingPanel(sess, "", 120, 40, deps)
	_, _ = panel.Update(tea.KeyPressMsg{Code: 'n', Text: "n"})
	if panel.NoteActive() {
		t.Error("n with no items should not open the note modal")
	}
}

func TestShippingPanelModel_NoteModalInterceptsKeys(t *testing.T) {
	panel, _, _ := shippingPanelWithFeedback()
	_, _ = panel.Update(tea.KeyPressMsg{Code: 'n', Text: "n"}) // open
	if !panel.NoteActive() {
		t.Fatal("test prereq: note modal should be open")
	}
	// Typing 'j' should reach the textarea, NOT advance the feedback cursor.
	cursorBefore := panel.FeedbackCursor()
	_, _ = panel.Update(tea.KeyPressMsg{Code: 'j', Text: "j"})
	if panel.FeedbackCursor() != cursorBefore {
		t.Error("j while note modal active moved feedback cursor")
	}
	// Esc cancels and does NOT close the shipping panel.
	_, cmd := panel.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	if cmdClosesShipping(t, cmd) {
		t.Error("esc inside note modal closed the shipping panel")
	}
	if panel.NoteActive() {
		t.Error("esc should close the note modal")
	}
}

func TestShippingPanelModel_NoteModalEnter_SavesNote(t *testing.T) {
	panel, _, state := shippingPanelWithFeedback()
	_, _ = panel.Update(tea.KeyPressMsg{Code: 'n', Text: "n"})
	panel.feedbackNote.ta.SetValue("important note")
	// Enter closes the modal synchronously and emits a feedbackNoteSubmitMsg;
	// the note persists one Update cycle later when that msg is handled.
	_, cmd := panel.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if panel.NoteActive() {
		t.Error("enter should close the modal")
	}
	if cmd == nil {
		t.Fatal("enter should emit a submit cmd")
	}
	submit, ok := cmd().(feedbackNoteSubmitMsg)
	if !ok {
		t.Fatalf("enter cmd yielded %T, want feedbackNoteSubmitMsg", cmd())
	}
	_, _ = panel.Update(submit)
	got := state.triage["thread:alice"]
	if got == nil || got.Note != "important note" {
		t.Errorf("triage note = %+v, want %q", got, "important note")
	}
}

func TestShippingPanelModel_TKey_OpensInLaunch(t *testing.T) {
	panel, _, _ := shippingPanelWithFeedback()
	_, cmd := panel.Update(tea.KeyPressMsg{Code: 't', Text: "t"})
	msgs := runCmdAll(t, cmd)
	if _, ok := findMsg[panelCloseMsg](msgs); !ok {
		t.Error("t should close the shipping panel")
	}
	req, ok := findMsg[openAgentTerminalRequestMsg](msgs)
	if !ok {
		t.Fatal("t should request the agent terminal")
	}
	if req.fallbackError == "" {
		t.Error("t should carry a fallbackError so App surfaces an error when no agents")
	}
}

func TestShippingPanelModel_TKey_OpenFail_SetsError(t *testing.T) {
	// 't' is exit-the-panel intent: it always closes and requests the terminal
	// with a fallbackError. The launch-failure path (no agents) is applied by
	// App.Update's handler, not the panel — here we assert the panel hands off
	// the fallbackError so that path can fire.
	panel, _, _ := shippingPanelWithFeedback()
	_, cmd := panel.Update(tea.KeyPressMsg{Code: 't', Text: "t"})
	msgs := runCmdAll(t, cmd)
	if _, ok := findMsg[panelCloseMsg](msgs); !ok {
		t.Error("t should close even when launch fails")
	}
	req, ok := findMsg[openAgentTerminalRequestMsg](msgs)
	if !ok {
		t.Fatal("t should request the agent terminal")
	}
	if req.fallbackError == "" {
		t.Error("t with failed OpenInLaunch should surface an error via fallbackError")
	}
}

func TestShippingPanelModel_PKey_NoURL_SetsError(t *testing.T) {
	sess := agent.NewSessionForTest("s1", "ship")
	deps, _ := newTestShippingDeps()
	panel := newShippingPanel(sess, "", 120, 40, deps)
	// No PR entry => no URL.
	_, cmd := panel.Update(tea.KeyPressMsg{Code: 'p', Text: "p"})
	msgs := runCmdAll(t, cmd)
	if _, ok := findMsg[setErrorMsg](msgs); !ok {
		t.Error("p without a PR URL should surface an error")
	}
	if _, ok := findMsg[openURLResultMsg](msgs); ok {
		t.Error("p without a PR URL should not open a URL")
	}
}

func TestShippingPanelModel_MergeReady_CallsMergeCmd(t *testing.T) {
	sess := agent.NewSessionForTest("s1", "ship")
	deps, state := newTestShippingDeps()
	panel := newShippingPanel(sess, "", 120, 40, deps)
	state.pr = &prCacheEntry{
		pr:      &github.PRState{Number: 1, MergeableState: "clean"},
		checks:  &github.CheckStatus{State: "success", Total: 3},
		reviews: &github.ReviewStatus{State: "approved"},
	}
	_, cmd := panel.Update(tea.KeyPressMsg{Code: 'm', Text: "m"})
	_ = runCmdAll(t, cmd)
	if !state.mergeRequested {
		t.Error("m should call MergePRCmd when ready")
	}
	if state.mergeForce {
		t.Error("m should pass force=false")
	}
	if cmdShippingError(t, cmd) != "" {
		t.Errorf("m when ready should not error, got %q", cmdShippingError(t, cmd))
	}
}

func TestShippingPanelModel_ForceMerge_NoPR_SetsError(t *testing.T) {
	sess := agent.NewSessionForTest("s1", "ship")
	deps, state := newTestShippingDeps()
	panel := newShippingPanel(sess, "", 120, 40, deps)
	// No PR entry.
	_, cmd := panel.Update(tea.KeyPressMsg{Code: 'M', Text: "M"})
	if state.mergeRequested {
		t.Error("force-merge without a PR should not call MergePRCmd")
	}
	if cmdShippingError(t, cmd) == "" {
		t.Error("expected error when force-merging with no PR")
	}
}

func TestShippingPanelModel_RKey_EmitsFeedbackRequestMsg(t *testing.T) {
	panel, _, _ := shippingPanelWithFeedback()
	_, cmd := panel.Update(tea.KeyPressMsg{Code: 'r', Text: "r"})
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
	panel, _, state := shippingPanelWithFeedback()
	before := struct {
		cursor int
		scroll int
		merge  bool
	}{panel.FeedbackCursor(), panel.DetailScroll(), state.mergeRequested}

	for _, k := range []tea.KeyPressMsg{
		{Code: 'z', Text: "z"},
		{Code: 'q', Text: "q"},
		{Code: 'w', Mod: tea.ModCtrl},
	} {
		_, cmd := panel.Update(k)
		if cmd != nil {
			t.Errorf("unknown key %v produced cmd %T, want nil", k, cmd())
		}
	}

	after := struct {
		cursor int
		scroll int
		merge  bool
	}{panel.FeedbackCursor(), panel.DetailScroll(), state.mergeRequested}
	if before != after {
		t.Errorf("unknown keys changed state: before=%+v after=%+v", before, after)
	}
}

// TestShippingPanel_MergeKey_UsesPinnedRepoPath verifies that 'm' passes the
// panel's pinned repoPath to MergePRCmd, not a first-match lookup.
func TestShippingPanel_MergeKey_UsesPinnedRepoPath(t *testing.T) {
	sess := agent.NewSessionForTest("session-1", "ship")
	deps, state := newTestShippingDeps()
	var recordedRepoPath string
	deps.MergePRCmd = func(sessionID, repoPath string, force bool) tea.Cmd {
		state.mergeRequested = true
		state.mergeForce = force
		recordedRepoPath = repoPath
		return func() tea.Msg { return nil }
	}
	panel := newShippingPanel(sess, "/repoB", 120, 40, deps)
	state.pr = &prCacheEntry{
		pr:      &github.PRState{Number: 1, MergeableState: "clean"},
		checks:  &github.CheckStatus{State: "success", Total: 1},
		reviews: &github.ReviewStatus{State: "approved"},
	}
	_, _ = panel.Update(tea.KeyPressMsg{Code: 'm', Text: "m"})
	if !state.mergeRequested {
		t.Fatal("m should call MergePRCmd when ready")
	}
	if recordedRepoPath != "/repoB" {
		t.Errorf("MergePRCmd received repoPath=%q, want /repoB (pinned)", recordedRepoPath)
	}
}

// TestShippingPanel_FeedbackKey_EmitsRepoPath verifies that 'r' embeds the
// panel's pinned repoPath in the emitted shippingFeedbackRequestMsg.
func TestShippingPanel_FeedbackKey_EmitsRepoPath(t *testing.T) {
	sess := agent.NewSessionForTest("session-1", "ship")
	deps, _ := newTestShippingDeps()
	panel := newShippingPanel(sess, "/repoB", 120, 40, deps)
	_, cmd := panel.Update(tea.KeyPressMsg{Code: 'r', Text: "r"})
	if cmd == nil {
		t.Fatal("expected cmd from r")
	}
	msg, ok := cmd().(shippingFeedbackRequestMsg)
	if !ok {
		t.Fatalf("got %T, want shippingFeedbackRequestMsg", cmd())
	}
	if msg.repoPath != "/repoB" {
		t.Errorf("repoPath = %q, want /repoB (pinned)", msg.repoPath)
	}
}

// TestShippingPanel_View_FooterOnLastRow verifies that renderShippingPanel always
// returns exactly height rows with the ESC hint on the last row.
func TestShippingPanel_View_FooterOnLastRow(t *testing.T) {
	const w, h = 120, 40
	assertFooter := func(t *testing.T, view string) {
		t.Helper()
		n := strings.Count(view, "\n") + 1
		if n != h {
			t.Errorf("View() returned %d lines, want %d", n, h)
		}
		lines := strings.Split(view, "\n")
		last := ansi.Strip(lines[len(lines)-1])
		if !strings.Contains(last, "ESC") {
			t.Errorf("last line %q does not contain 'ESC' hint", last)
		}
	}

	t.Run("loading state (entry nil)", func(t *testing.T) {
		sess := agent.NewSessionForTest("s1", "ship")
		deps, _ := newTestShippingDeps()
		// state.pr is nil → PRCache returns nil → loading path
		panel := newShippingPanel(sess, "", w, h, deps)
		assertFooter(t, panel.View())
	})

	t.Run("populated entry no feedback", func(t *testing.T) {
		sess := agent.NewSessionForTest("s2", "ship")
		deps, state := newTestShippingDeps()
		state.pr = &prCacheEntry{pr: &github.PRState{Number: 42, Title: "fix bug"}}
		panel := newShippingPanel(sess, "", w, h, deps)
		assertFooter(t, panel.View())
	})

	t.Run("populated entry with feedback", func(t *testing.T) {
		sess := agent.NewSessionForTest("s3", "ship")
		deps, state := newTestShippingDeps()
		state.pr = &prCacheEntry{
			pr: &github.PRState{Number: 43, Title: "feat"},
			threads: []github.ReviewThread{
				{Reviewer: "alice", State: "CHANGES_REQUESTED", Body: "please fix this thing"},
			},
		}
		panel := newShippingPanel(sess, "", w, h, deps)
		assertFooter(t, panel.View())
	})
}
