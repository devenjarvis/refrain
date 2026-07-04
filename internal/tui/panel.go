package tui

import (
	tea "charm.land/bubbletea/v2"
)

// PanelModel is the contract every overlay panel implements. Panels are owned
// by App as nil-when-inactive pointer fields. They consume keypresses,
// resizes, and their own messages, and they ask to close by returning a
// tea.Cmd that yields a panelCloseMsg.
//
// Panels reach app-level state through a per-panel deps struct (reviewDeps,
// prPanelDeps) bound at construction to App's maps/pointers, and mutate App
// scalars only by emitting messages that App.Update handles (§3/§4). The
// interface is therefore the same shape as Component (component.go) — Update
// takes only a message; conformance is by shape, not a literal type assertion.
//
// Receiver-kind deviation: review/PR panels keep POINTER receivers (they
// are nil-able pointer fields in Modals and carry per-panel mutable state),
// whereas the value-typed Components in component.go use value receivers.
// Conformance to §3 is by Update/View shape (dropping the services arg), not by
// receiver kind.
type PanelModel interface {
	Update(msg tea.Msg) (PanelModel, tea.Cmd)
	View() string
	SetSize(w, h int)
}
