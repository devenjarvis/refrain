package tui

import tea "charm.land/bubbletea/v2"

// Component is the documented target shape (CONVENTIONS.md §3) that every
// self-contained screen, panel, modal, and picker follows: Update returns the
// next state of the component plus a command, View renders purely from state,
// and SetSize informs the component of the space it has to render in.
//
// Conformance is by shape, not by literal interface. Components follow this
// contract by returning their own concrete type from Update (e.g.
// `func (m configForm) Update(tea.Msg) (configForm, tea.Cmd)`), so there are
// no `var _ Component = …` assertions and no type-assertion-based routing at
// call sites — the parent forwards a message, gets back the concrete type, and
// stores it. This keeps state transitions explicit and copy-safe: Update uses
// value receivers and returns the updated value; View uses a value receiver;
// SetSize uses a pointer receiver because it mutates stored dimensions.
//
// PanelModel (panel.go) is the pointer-receiver sibling of this contract for
// the review/shipping panels: the same Update(tea.Msg)/View() shape, but the
// panels are nil-able pointer fields in Modals. They reach app-level state
// through deps injected at construction and signal App-scalar mutations via
// messages. Phase 5b completed this fold and removed the old per-tick services
// value (CONVENTIONS.md §3).
type Component interface {
	// Update handles a message and returns the next state of this component
	// plus any command to run. It must be pure w.r.t. I/O: side effects go in
	// the returned Cmd, never inline.
	Update(tea.Msg) (Component, tea.Cmd)

	// View renders the component within its current size. Pure: no mutation,
	// no I/O, deterministic from state.
	View() string

	// SetSize informs the component of the space it has to render in. The
	// component must render within (w, h) and be safe at minimum size.
	SetSize(w, h int)
}
