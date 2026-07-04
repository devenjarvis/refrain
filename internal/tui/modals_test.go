package tui

import (
	"testing"

	"github.com/devenjarvis/refrain/internal/agent"
)

// TestModals_ZeroValue verifies the zero-valued Modals is usable: no overlay
// active, every accessor returns nil.
func TestModals_ZeroValue(t *testing.T) {
	var m Modals
	if m.Current() != focusList {
		t.Errorf("Current() = %d, want focusList", m.Current())
	}
	if !m.IsList() {
		t.Error("IsList() = false, want true")
	}
	if m.Review() != nil {
		t.Error("Review() should be nil on zero value")
	}
	if m.PRPanel() != nil {
		t.Error("Shipping() should be nil on zero value")
	}
	if m.PlanEditor() != nil {
		t.Error("PlanEditor() should be nil on zero value")
	}
	if m.Config() != nil {
		t.Error("Config() should be nil on zero value")
	}
	if m.LaunchAgent() != nil {
		t.Error("LaunchAgent() should be nil on zero value")
	}
	if m.LaunchSession() != nil {
		t.Error("LaunchSession() should be nil on zero value")
	}
}

// TestModals_OpenReview_SetsCurrentAndPointer verifies the invariant: when
// focusReview is opened, Is(focusReview) is true, Review() returns the panel,
// and every other typed accessor returns nil.
func TestModals_OpenReview_SetsCurrentAndPointer(t *testing.T) {
	var m Modals
	rp := &reviewPanelModel{}
	m.OpenReview(rp)

	if !m.Is(focusReview) {
		t.Error("Is(focusReview) should be true after OpenReview")
	}
	if m.IsList() {
		t.Error("IsList() should be false after OpenReview")
	}
	if m.Review() != rp {
		t.Error("Review() should return the opened panel")
	}
	// Every other accessor must return nil while review is active.
	if m.PRPanel() != nil {
		t.Error("Shipping() should be nil when focusReview is current")
	}
	if m.PlanEditor() != nil {
		t.Error("PlanEditor() should be nil when focusReview is current")
	}
	if m.Config() != nil {
		t.Error("Config() should be nil when focusReview is current")
	}
	if m.LaunchAgent() != nil {
		t.Error("LaunchAgent() should be nil when focusReview is current")
	}
	if m.LaunchSession() != nil {
		t.Error("LaunchSession() should be nil when focusReview is current")
	}
}

// TestModals_Close_NilsEveryModel verifies that after opening each modal in
// turn, Close() returns focus to the list AND every owned pointer is nil.
// This is the bug the type was introduced to make impossible.
func TestModals_Close_NilsEveryModel(t *testing.T) {
	cases := []struct {
		name string
		open func(*Modals)
	}{
		{"review", func(m *Modals) { m.OpenReview(&reviewPanelModel{}) }},
		{"shipping", func(m *Modals) { m.OpenPRPanel(&prPanelModel{}) }},
		{"planEditor", func(m *Modals) { m.OpenPlanEditor(&planEditorModel{}) }},
		{"config", func(m *Modals) { m.OpenConfig(&configForm{}, "/repo") }},
		{"launch", func(m *Modals) { m.OpenLaunch(&agent.Session{}, &agent.Agent{}, "") }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var m Modals
			tc.open(&m)
			m.Close()

			if m.Current() != focusList {
				t.Errorf("Current() = %d after Close, want focusList", m.Current())
			}
			if m.Review() != nil {
				t.Error("Review() not nil after Close")
			}
			if m.PRPanel() != nil {
				t.Error("Shipping() not nil after Close")
			}
			if m.PlanEditor() != nil {
				t.Error("PlanEditor() not nil after Close")
			}
			if m.Config() != nil {
				t.Error("Config() not nil after Close")
			}
			if m.ConfigRepoPath() != "" {
				t.Errorf("ConfigRepoPath() = %q after Close, want empty", m.ConfigRepoPath())
			}
			if m.LaunchAgent() != nil {
				t.Error("LaunchAgent() not nil after Close")
			}
			if m.LaunchSession() != nil {
				t.Error("LaunchSession() not nil after Close")
			}
		})
	}
}

// TestModals_Close_NilsInternalState verifies Close clears the internal
// pointers (not just the typed accessors which already gate on focus).
// Without this, a future bug could leave a stale model attached after focus
// changed but Close wasn't called.
func TestModals_Close_NilsInternalState(t *testing.T) {
	var m Modals
	m.OpenReview(&reviewPanelModel{})
	m.Close()
	if m.review != nil {
		t.Error("internal review pointer should be nil after Close")
	}
	m.OpenPRPanel(&prPanelModel{})
	m.Close()
	if m.prPanel != nil {
		t.Error("internal shipping pointer should be nil after Close")
	}
	m.OpenPlanEditor(&planEditorModel{})
	m.Close()
	if m.planEditor != nil {
		t.Error("internal planEditor pointer should be nil after Close")
	}
	m.OpenConfig(&configForm{}, "/repo")
	m.Close()
	if m.config != nil || m.configRepo != "" {
		t.Error("internal config state should be empty after Close")
	}
	m.OpenLaunch(&agent.Session{}, &agent.Agent{}, "")
	m.Close()
	if m.launchAgent != nil || m.launchSess != nil {
		t.Error("internal launch refs should be nil after Close")
	}
}

// TestModals_OpenReplacesPrevious verifies that opening one modal cleanly
// supersedes any prior modal -- no stale pointers, only the newest is
// accessible.
func TestModals_OpenReplacesPrevious(t *testing.T) {
	var m Modals
	rp := &reviewPanelModel{}
	sp := &prPanelModel{}

	m.OpenReview(rp)
	m.OpenPRPanel(sp)

	if m.Current() != focusPRPanel {
		t.Errorf("Current() = %d, want focusPRPanel", m.Current())
	}
	if m.Review() != nil {
		t.Error("Review() should be nil after OpenPRPanel replaces it")
	}
	if m.PRPanel() != sp {
		t.Error("Shipping() should return the most recently opened panel")
	}
	// Internal state: the prior review pointer must also be cleared.
	if m.review != nil {
		t.Error("internal review pointer should be nil after replacement")
	}
}

// TestModals_TableInvariant verifies the core invariant across every focus
// state: after each Open*, exactly one typed accessor returns non-nil and
// every other returns nil.
func TestModals_TableInvariant(t *testing.T) {
	cases := []struct {
		name       string
		open       func(*Modals)
		wantFocus  panelFocus
		wantNonNil string
	}{
		{
			name:       "review",
			open:       func(m *Modals) { m.OpenReview(&reviewPanelModel{}) },
			wantFocus:  focusReview,
			wantNonNil: "review",
		},
		{
			name:       "shipping",
			open:       func(m *Modals) { m.OpenPRPanel(&prPanelModel{}) },
			wantFocus:  focusPRPanel,
			wantNonNil: "shipping",
		},
		{
			name:       "planEditor",
			open:       func(m *Modals) { m.OpenPlanEditor(&planEditorModel{}) },
			wantFocus:  focusPlanEditor,
			wantNonNil: "planEditor",
		},
		{
			name:       "config",
			open:       func(m *Modals) { m.OpenConfig(&configForm{}, "/r") },
			wantFocus:  focusConfig,
			wantNonNil: "config",
		},
		{
			name:       "launch",
			open:       func(m *Modals) { m.OpenLaunch(&agent.Session{}, &agent.Agent{}, "") },
			wantFocus:  focusLaunch,
			wantNonNil: "launch",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var m Modals
			tc.open(&m)
			if m.Current() != tc.wantFocus {
				t.Errorf("Current() = %d, want %d", m.Current(), tc.wantFocus)
			}
			accessors := map[string]bool{
				"review":     m.Review() != nil,
				"shipping":   m.PRPanel() != nil,
				"planEditor": m.PlanEditor() != nil,
				"config":     m.Config() != nil,
				"launch":     m.LaunchAgent() != nil && m.LaunchSession() != nil,
			}
			for name, gotNonNil := range accessors {
				wantNonNil := name == tc.wantNonNil
				if gotNonNil != wantNonNil {
					t.Errorf("accessor %q non-nil = %v, want %v", name, gotNonNil, wantNonNil)
				}
			}
		})
	}
}

// TestModals_CompareAndSetReview verifies the snapshot-and-swap defends
// against a concurrent Close() that nilled the model during panel.Update.
func TestModals_CompareAndSetReview(t *testing.T) {
	var m Modals
	original := &reviewPanelModel{}
	fresh := &reviewPanelModel{}
	m.OpenReview(original)

	// Happy path: pointer matches, swap succeeds.
	if !m.CompareAndSetReview(original, fresh) {
		t.Fatal("CompareAndSetReview should succeed when pointer matches")
	}
	if m.Review() != fresh {
		t.Error("Review() should return fresh after successful swap")
	}

	// Concurrent close during Update: pointer no longer matches.
	m.Close()
	if m.CompareAndSetReview(fresh, &reviewPanelModel{}) {
		t.Error("CompareAndSetReview should fail when focus changed")
	}
	if m.Review() != nil {
		t.Error("Review() should remain nil after failed CAS")
	}
}

// TestModals_CompareAndSetShipping mirrors the review CAS test for shipping.
func TestModals_CompareAndSetShipping(t *testing.T) {
	var m Modals
	original := &prPanelModel{}
	fresh := &prPanelModel{}
	m.OpenPRPanel(original)

	if !m.CompareAndSetPRPanel(original, fresh) {
		t.Fatal("CompareAndSetPRPanel should succeed when pointer matches")
	}
	if m.PRPanel() != fresh {
		t.Error("Shipping() should return fresh after successful swap")
	}

	m.Close()
	if m.CompareAndSetPRPanel(fresh, &prPanelModel{}) {
		t.Error("CompareAndSetPRPanel should fail when focus changed")
	}
}

// TestModals_SetLaunchAgent allows retargeting the focused agent without
// reopening the modal, but only while focusLaunch is current.
func TestModals_SetLaunchAgent(t *testing.T) {
	var m Modals
	sess := &agent.Session{}
	a1 := &agent.Agent{}
	a2 := &agent.Agent{}

	m.OpenLaunch(sess, a1, "")
	if m.LaunchAgent() != a1 {
		t.Fatal("LaunchAgent should return a1 after OpenLaunch")
	}

	m.SetLaunchAgent(a2)
	if m.LaunchAgent() != a2 {
		t.Error("SetLaunchAgent should retarget while focused")
	}
	if m.LaunchSession() != sess {
		t.Error("SetLaunchAgent should not affect the session pointer")
	}

	// After Close, SetLaunchAgent must be a no-op.
	m.Close()
	m.SetLaunchAgent(a1)
	if m.LaunchAgent() != nil {
		t.Error("SetLaunchAgent should be a no-op when focusLaunch is not current")
	}
}

// TestModals_ConfigRepoPath verifies the repo path is carried with the config
// form and cleared on close.
func TestModals_ConfigRepoPath(t *testing.T) {
	var m Modals
	m.OpenConfig(&configForm{}, "/path/to/repo")
	if got := m.ConfigRepoPath(); got != "/path/to/repo" {
		t.Errorf("ConfigRepoPath() = %q, want %q", got, "/path/to/repo")
	}
	m.Close()
	if got := m.ConfigRepoPath(); got != "" {
		t.Errorf("ConfigRepoPath() after Close = %q, want empty", got)
	}
}
