package tui

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/devenjarvis/refrain/internal/agent"
	"github.com/devenjarvis/refrain/internal/github"
)

// shippingPanelModel owns key/view dispatch for the shipping panel (PR
// status, CI failures, review threads, merge gate). Per-panel state
// (cursor, scroll, feedback-note modal) lives here; feedbackTriage stays on
// App because it survives panel close/reopen.
type shippingPanelModel struct {
	session        *agent.Session
	repoPath       string
	feedbackCursor int
	detailScroll   int
	feedbackNote   feedbackNoteModal

	// deps carries the App-level reference handles the panel reaches through
	// for lookups and cmd factories. Bound once at construction (§3 fold).
	deps shippingDeps

	width, height int
}

// shippingDeps holds the reference-typed App handles the shipping panel needs.
// Bound at construction to the App's maps/pointers (never to App itself) so the
// closures stay live across App value-copies (App.Update is a value receiver).
type shippingDeps struct {
	PRCache            func(repoPath, sessionID string) *prCacheEntry
	FeedbackTriage     func(repoPath, sessionID string) map[string]*feedbackTriageEntry
	SetFeedbackVerdict func(repoPath, sessionID, itemKey string, v feedbackVerdict)
	SetFeedbackNote    func(repoPath, sessionID, itemKey, note string)
	MergePRCmd         func(sessionID, repoPath string, force bool) tea.Cmd
}

// feedbackVerdict is the user's disposition on a single feedback item.
type feedbackVerdict int

const (
	feedbackNeutral   feedbackVerdict = iota
	feedbackApproved                  // user agreed; item will be addressed
	feedbackDisagreed                 // user disputes; item framed as advisory
)

// feedbackTriageEntry holds the verdict and optional guidance note for one item.
type feedbackTriageEntry struct {
	Verdict feedbackVerdict
	Note    string
}

// feedbackItem is a flattened view of a single piece of review feedback.
type feedbackItem struct {
	Reviewer  string
	State     string
	Path      string
	Line      int
	Body      string
	CommentID int64
	IsInline  bool
}

// feedbackItems flattens review threads into an ordered slice of feedback items.
// For each thread: one body item (when non-empty), then one item per inline comment.
func feedbackItems(threads []github.ReviewThread) []feedbackItem {
	var items []feedbackItem
	for _, t := range threads {
		if strings.TrimSpace(t.Body) != "" {
			items = append(items, feedbackItem{
				Reviewer: t.Reviewer,
				State:    t.State,
				Body:     t.Body,
				IsInline: false,
			})
		}
		for _, c := range t.Comments {
			items = append(items, feedbackItem{
				Reviewer:  t.Reviewer,
				State:     t.State,
				Path:      c.Path,
				Line:      c.Line,
				Body:      c.Body,
				CommentID: c.ID,
				IsInline:  true,
			})
		}
	}
	return items
}

// feedbackItemKey returns the stable triage map key for an item.
// Inline comments use "comment:<id>"; thread bodies use "thread:<reviewer>".
// The reviewer key is unique because GetReviewThreads produces at most one
// ReviewThread per reviewer (latest-review dedup + seen guard).
func feedbackItemKey(item feedbackItem) string {
	if item.IsInline {
		return fmt.Sprintf("comment:%d", item.CommentID)
	}
	return "thread:" + item.Reviewer
}

// newShippingPanel constructs a shipping panel for sess. repoPath pins which
// repo's manager is used for merge and feedback key handlers, preventing
// multi-repo session-ID collisions from routing operations to the wrong repo.
// The nested feedbackNote modal is initialised but inactive until the user
// presses 'n'.
func newShippingPanel(sess *agent.Session, repoPath string, width, height int, deps shippingDeps) *shippingPanelModel {
	note := newFeedbackNoteModal()
	note.SetSize(width, height+1)
	return &shippingPanelModel{
		session:      sess,
		repoPath:     repoPath,
		feedbackNote: note,
		width:        width,
		height:       height,
		deps:         deps,
	}
}

// SessionID returns the bound session's ID or "" when unbound.
func (m *shippingPanelModel) SessionID() string {
	if m == nil || m.session == nil {
		return ""
	}
	return m.session.ID
}

// Session returns the bound session, or nil.
func (m *shippingPanelModel) Session() *agent.Session {
	if m == nil {
		return nil
	}
	return m.session
}

// FeedbackCursor exposes the cursor row for tests and View rendering.
func (m *shippingPanelModel) FeedbackCursor() int {
	if m == nil {
		return 0
	}
	return m.feedbackCursor
}

// DetailScroll exposes the detail-pane scroll for tests and View rendering.
func (m *shippingPanelModel) DetailScroll() int {
	if m == nil {
		return 0
	}
	return m.detailScroll
}

// SetSize updates layout dimensions and forwards to the nested modal.
func (m *shippingPanelModel) SetSize(w, h int) {
	if m == nil {
		return
	}
	m.width = w
	m.height = h
	m.feedbackNote.SetSize(w, h+1)
}

// NoteActive reports whether the feedback-note modal is currently active.
// App's View uses this to overlay the modal above the panel.
func (m *shippingPanelModel) NoteActive() bool {
	return m != nil && m.feedbackNote.Active()
}

// NoteView returns the rendered feedback-note modal for overlaying.
func (m *shippingPanelModel) NoteView() string {
	if m == nil {
		return ""
	}
	return m.feedbackNote.View()
}
