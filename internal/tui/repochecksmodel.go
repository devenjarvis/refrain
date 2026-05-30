package tui

import (
	"fmt"
	"strings"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/lipgloss"
	"github.com/devenjarvis/refrain/internal/config"
)

// repoChecksSaveMsg is emitted when the user accepts their changes to the
// validation checks list (ctrl+s from list mode). The receiver is responsible
// for copying Checks into the pending buffer that the parent repo settings
// form will later persist.
type repoChecksSaveMsg struct {
	Checks []config.ValidationCheck
}

// repoChecksCancelMsg is emitted when the user discards their changes (esc
// from list mode).
type repoChecksCancelMsg struct{}

// repoChecksInput identifies which of the two text inputs has focus while a
// row is in edit mode.
type repoChecksInput int

const (
	repoChecksInputName repoChecksInput = iota
	repoChecksInputCommand
)

// repoChecksModel is the list editor for a repo's validation checks. It owns
// only the in-memory edit buffer; persistence is performed by the parent repo
// settings form after the user saves both layers (`ctrl+s` in this view, then
// `ctrl+s` in the repo settings form).
type repoChecksModel struct {
	repoName    string
	checks      []config.ValidationCheck
	cursor      int
	editing     bool
	editIdx     int
	activeInput repoChecksInput
	nameInput   textinput.Model
	cmdInput    textinput.Model
	width       int
	height      int
}

// newRepoChecksModel seeds the editor from an existing checks list. The
// caller must pass a fresh copy if it wants edits to be discardable on cancel.
func newRepoChecksModel(repoName string, checks []config.ValidationCheck) repoChecksModel {
	cp := make([]config.ValidationCheck, len(checks))
	copy(cp, checks)
	return repoChecksModel{
		repoName: repoName,
		checks:   cp,
	}
}

// SetSize informs the editor of the space it has to render in.
func (m *repoChecksModel) SetSize(w, h int) {
	m.width = w
	m.height = h
}

// Update handles a key event for the editor. It returns the next editor state
// and any command to run (CONVENTIONS.md §3).
func (m repoChecksModel) Update(msg tea.Msg) (repoChecksModel, tea.Cmd) {
	key, ok := msg.(tea.KeyPressMsg)
	if !ok {
		return m, nil
	}

	var cmd tea.Cmd
	if m.editing {
		cmd = m.updateEditing(key)
	} else {
		cmd = m.updateList(key)
	}
	return m, cmd
}

func (m *repoChecksModel) updateList(msg tea.KeyPressMsg) tea.Cmd {
	switch msg.String() {
	case "j", "down":
		if len(m.checks) > 0 && m.cursor < len(m.checks)-1 {
			m.cursor++
		}
		return nil
	case "k", "up":
		if m.cursor > 0 {
			m.cursor--
		}
		return nil
	case "a":
		m.checks = append(m.checks, config.ValidationCheck{})
		m.cursor = len(m.checks) - 1
		m.beginEdit(m.cursor)
		return nil
	case "e", "enter":
		if len(m.checks) > 0 {
			m.beginEdit(m.cursor)
		}
		return nil
	case "d":
		if len(m.checks) > 0 {
			m.checks = append(m.checks[:m.cursor], m.checks[m.cursor+1:]...)
			if m.cursor >= len(m.checks) {
				m.cursor = len(m.checks) - 1
			}
			if m.cursor < 0 {
				m.cursor = 0
			}
		}
		return nil
	case "ctrl+s":
		out := make([]config.ValidationCheck, len(m.checks))
		copy(out, m.checks)
		return func() tea.Msg { return repoChecksSaveMsg{Checks: out} }
	case "esc":
		return func() tea.Msg { return repoChecksCancelMsg{} }
	}
	return nil
}

func (m *repoChecksModel) updateEditing(msg tea.KeyPressMsg) tea.Cmd {
	switch msg.String() {
	case "tab":
		m.switchInput(repoChecksInputCommand)
		return nil
	case "shift+tab":
		m.switchInput(repoChecksInputName)
		return nil
	case "esc":
		// Abandon this row's in-flight edits; if it's a freshly-appended
		// blank row drop it so the user isn't left with an empty entry.
		if m.checks[m.editIdx].Name == "" && m.checks[m.editIdx].Command == "" {
			m.checks = append(m.checks[:m.editIdx], m.checks[m.editIdx+1:]...)
			if m.cursor >= len(m.checks) {
				m.cursor = len(m.checks) - 1
			}
			if m.cursor < 0 {
				m.cursor = 0
			}
		}
		m.endEdit()
		return nil
	case "enter", "ctrl+s":
		m.commitEdit()
		m.endEdit()
		return nil
	}
	var cmd tea.Cmd
	if m.activeInput == repoChecksInputName {
		m.nameInput, cmd = m.nameInput.Update(msg)
	} else {
		m.cmdInput, cmd = m.cmdInput.Update(msg)
	}
	return cmd
}

func (m *repoChecksModel) beginEdit(idx int) {
	m.editing = true
	m.editIdx = idx
	m.activeInput = repoChecksInputName

	m.nameInput = textinput.New()
	m.nameInput.CharLimit = 64
	m.nameInput.SetWidth(40)
	m.nameInput.SetValue(m.checks[idx].Name)
	m.nameInput.Focus()

	m.cmdInput = textinput.New()
	m.cmdInput.CharLimit = 512
	m.cmdInput.SetWidth(40)
	m.cmdInput.SetValue(m.checks[idx].Command)
}

func (m *repoChecksModel) endEdit() {
	m.editing = false
	m.nameInput.Blur()
	m.cmdInput.Blur()
}

func (m *repoChecksModel) switchInput(target repoChecksInput) {
	m.activeInput = target
	if target == repoChecksInputName {
		m.nameInput.Focus()
		m.cmdInput.Blur()
	} else {
		m.cmdInput.Focus()
		m.nameInput.Blur()
	}
}

func (m *repoChecksModel) commitEdit() {
	m.checks[m.editIdx].Name = strings.TrimSpace(m.nameInput.Value())
	m.checks[m.editIdx].Command = strings.TrimSpace(m.cmdInput.Value())
}

// View renders the editor body (no surrounding border — that's the overlay's
// job).
func (m repoChecksModel) View() string {
	subtitle := StyleSubtle.Render("Commands run with sh -c against the worktree during the review panel's Checks tab.")
	var body string
	if len(m.checks) == 0 {
		body = StyleSubtle.Render("No checks yet — press a to add the first one.")
	} else {
		rows := make([]string, 0, len(m.checks)*3)
		for i, c := range m.checks {
			cursor := "  "
			nameStyle := lipgloss.NewStyle().Foreground(ColorText)
			cmdStyle := StyleSubtle
			if i == m.cursor && !m.editing {
				cursor = StyleActive.Render("› ")
				nameStyle = nameStyle.Bold(true)
			}

			if m.editing && i == m.editIdx {
				nameLabel := StyleSubtle.Render("Name    ")
				cmdLabel := StyleSubtle.Render("Command ")
				if m.activeInput == repoChecksInputName {
					nameLabel = StyleActive.Render("Name    ")
				} else {
					cmdLabel = StyleActive.Render("Command ")
				}
				rows = append(
					rows,
					StyleActive.Render("› ")+fmt.Sprintf("%d.", i+1),
					"    "+nameLabel+m.nameInput.View(),
					"    "+cmdLabel+m.cmdInput.View(),
				)
				continue
			}

			name := c.Name
			if name == "" {
				name = StyleSubtle.Italic(true).Render("(unnamed)")
			} else {
				name = nameStyle.Render(name)
			}
			cmd := c.Command
			if cmd == "" {
				cmd = StyleSubtle.Italic(true).Render("(no command)")
			} else {
				cmd = cmdStyle.Render(cmd)
			}
			rows = append(
				rows,
				cursor+fmt.Sprintf("%d. ", i+1)+name,
				"    "+cmd,
			)
		}
		body = strings.Join(rows, "\n")
	}

	// Row-edit mode has its own hint (statusbar shows list-mode keys).
	if m.editing {
		hint := StyleSubtle.Render("tab switch field  enter save row  esc cancel row")
		return strings.Join([]string{subtitle, "", body, "", hint}, "\n")
	}
	return strings.Join([]string{subtitle, "", body}, "\n")
}

// Checks returns a copy of the current in-memory list.
func (m *repoChecksModel) Checks() []config.ValidationCheck {
	out := make([]config.ValidationCheck, len(m.checks))
	copy(out, m.checks)
	return out
}

// repoChecksHint returns the right-aligned summary text rendered next to the
// "Validation Checks" action row in the repo settings form.
func repoChecksHint(checks []config.ValidationCheck) string {
	switch len(checks) {
	case 0:
		return "none configured  ↵ edit"
	case 1:
		return "1 configured  ↵ edit"
	default:
		return fmt.Sprintf("%d configured  ↵ edit", len(checks))
	}
}
