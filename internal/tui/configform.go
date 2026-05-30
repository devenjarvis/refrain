package tui

import (
	"strings"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/lipgloss"
)

// fieldKind distinguishes toggle fields from text input fields.
type fieldKind int

const (
	fieldToggle fieldKind = iota
	fieldText
	fieldSelect
	fieldAction
)

// formField is a single field in a config form.
type formField struct {
	label       string
	kind        fieldKind
	toggleValue bool            // for fieldToggle
	textInput   textinput.Model // for fieldText
	editing     bool            // true when text field has cursor active
	options     []string        // for fieldSelect
	selected    int             // for fieldSelect
	actionHint  string          // for fieldAction: right-aligned summary text
}

// configForm composes toggle and text input fields into a navigable form.
type configForm struct {
	fields  []formField
	focused int
	width   int
}

// configFormSaveMsg is emitted when the user saves the form (ctrl+s).
type configFormSaveMsg struct{}

// configFormCancelMsg is emitted when the user cancels (esc with no text editing).
type configFormCancelMsg struct{}

// configFormActionMsg is emitted when the user presses enter on a fieldAction
// row. Routed by label so a single form can carry multiple sub-editor entry
// points without cross-wiring the model layer.
type configFormActionMsg struct{ Label string }

// newConfigForm creates a form with the given fields. Width controls text input sizing.
func newConfigForm(fields []formField, width int) configForm {
	f := configForm{fields: fields, width: width}
	if len(f.fields) > 0 {
		f.focusField(0)
	}
	return f
}

// addToggle appends a boolean toggle field.
func addToggle(fields []formField, label string, value bool) []formField {
	return append(fields, formField{
		label:       label,
		kind:        fieldToggle,
		toggleValue: value,
	})
}

// addSelect appends a select field cycling through a fixed option list.
// selected is the initial option index (clamped to a valid range).
func addSelect(fields []formField, label string, options []string, selected int) []formField {
	if len(options) == 0 {
		options = []string{""}
	}
	if selected < 0 || selected >= len(options) {
		selected = 0
	}
	return append(fields, formField{
		label:    label,
		kind:     fieldSelect,
		options:  options,
		selected: selected,
	})
}

// addAction appends a non-editable action row. Pressing enter on it emits a
// configFormActionMsg{Label: label}, intended to launch a sub-editor for
// values that don't fit a scalar widget (e.g. a list of structs). The hint
// renders right-aligned and may carry a summary like "3 configured  ↵ edit".
func addAction(fields []formField, label, hint string) []formField {
	return append(fields, formField{
		label:      label,
		kind:       fieldAction,
		actionHint: hint,
	})
}

// addTextInput appends a text input field.
func addTextInput(fields []formField, label, value, placeholder string, width int) []formField {
	ti := textinput.New()
	ti.Placeholder = placeholder
	ti.SetValue(value)
	ti.CharLimit = 256
	if width > 0 {
		ti.SetWidth(width)
	}
	return append(fields, formField{
		label:     label,
		kind:      fieldText,
		textInput: ti,
	})
}

func (f *configForm) focusField(idx int) {
	if idx < 0 || idx >= len(f.fields) {
		return
	}
	// Blur previous
	if f.focused >= 0 && f.focused < len(f.fields) {
		old := &f.fields[f.focused]
		if old.kind == fieldText {
			old.textInput.Blur()
			old.editing = false
		}
	}
	f.focused = idx
}

// Update handles key events for the form.
func (f *configForm) Update(msg tea.Msg) tea.Cmd {
	if len(f.fields) == 0 {
		return nil
	}

	switch msg := msg.(type) {
	case tea.KeyPressMsg:
		field := &f.fields[f.focused]

		// If editing a text field, delegate most keys to textinput.
		if field.editing {
			switch msg.String() {
			case "esc":
				field.textInput.Blur()
				field.editing = false
				return nil
			case "enter":
				field.textInput.Blur()
				field.editing = false
				return nil
			}
			var cmd tea.Cmd
			field.textInput, cmd = field.textInput.Update(msg)
			return cmd
		}

		// Navigation and actions when not editing.
		switch msg.String() {
		case "j", "down":
			if f.focused < len(f.fields)-1 {
				f.focusField(f.focused + 1)
			}
			return nil
		case "k", "up":
			if f.focused > 0 {
				f.focusField(f.focused - 1)
			}
			return nil
		case "right", "l":
			if field.kind == fieldSelect && len(field.options) > 0 {
				field.selected = (field.selected + 1) % len(field.options)
			}
			return nil
		case "left", "h":
			if field.kind == fieldSelect && len(field.options) > 0 {
				field.selected = (field.selected - 1 + len(field.options)) % len(field.options)
			}
			return nil
		case "enter", " ", "space":
			switch field.kind {
			case fieldToggle:
				field.toggleValue = !field.toggleValue
			case fieldText:
				field.editing = true
				field.textInput.Focus()
			case fieldSelect:
				if len(field.options) > 0 {
					field.selected = (field.selected + 1) % len(field.options)
				}
			case fieldAction:
				label := field.label
				return func() tea.Msg { return configFormActionMsg{Label: label} }
			}
			return nil
		case "ctrl+s":
			return func() tea.Msg { return configFormSaveMsg{} }
		case "esc":
			return func() tea.Msg { return configFormCancelMsg{} }
		}
	}

	return nil
}

// View renders the form as a vertical list of labeled fields.
func (f configForm) View() string {
	if len(f.fields) == 0 {
		return StyleSubtle.Render("No settings available")
	}

	labelStyle := lipgloss.NewStyle().Width(22).Foreground(ColorText)
	focusedLabelStyle := StyleActive.Bold(true).Width(22)
	toggleOn := StyleSuccess.Render("[x]")
	toggleOff := StyleSubtle.Render("[ ]")

	rows := make([]string, 0, len(f.fields))
	for i, field := range f.fields {
		ls := labelStyle
		cursor := "  "
		if i == f.focused {
			ls = focusedLabelStyle
			cursor = StyleActive.Render("> ")
		}

		label := ls.Render(field.label)
		var value string

		switch field.kind {
		case fieldToggle:
			if field.toggleValue {
				value = toggleOn
			} else {
				value = toggleOff
			}
		case fieldText:
			value = field.textInput.View()
		case fieldSelect:
			chevronStyle := StyleSubtle
			if i == f.focused {
				chevronStyle = StyleActive
			}
			opt := ""
			if len(field.options) > 0 {
				opt = field.options[field.selected]
			}
			value = chevronStyle.Render("< ") + opt + chevronStyle.Render(" >")
		case fieldAction:
			value = StyleSubtle.Render(field.actionHint)
		}

		rows = append(rows, cursor+label+" "+value)
	}

	return strings.Join(rows, "\n")
}

// toggleValue returns the value of a toggle field by label.
func (f configForm) toggleValue(label string) bool {
	for _, field := range f.fields {
		if field.label == label && field.kind == fieldToggle {
			return field.toggleValue
		}
	}
	return false
}

// textValue returns the value of a text field by label.
func (f configForm) textValue(label string) string {
	for _, field := range f.fields {
		if field.label == label && field.kind == fieldText {
			return field.textInput.Value()
		}
	}
	return ""
}

// selectValue returns the currently selected option text for a select field.
func (f configForm) selectValue(label string) string {
	for _, field := range f.fields {
		if field.label == label && field.kind == fieldSelect {
			if field.selected >= 0 && field.selected < len(field.options) {
				return field.options[field.selected]
			}
			return ""
		}
	}
	return ""
}

// optionIndex returns the index of value in options, or 0 if not present.
// Used to compute the initial selection for a fieldSelect.
func optionIndex(options []string, value string) int {
	for i, o := range options {
		if o == value {
			return i
		}
	}
	return 0
}
