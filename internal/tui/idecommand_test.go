package tui

import (
	"reflect"
	"testing"

	"github.com/devenjarvis/baton/internal/editor"
)

func TestAddEditorFields_EmptyStoredShape(t *testing.T) {
	var fields []formField
	fields = addEditorFields(fields, "")
	form := newConfigForm(fields, 30)

	got := form.selectValue(editorFieldLabel)
	detected := editor.Detect()
	if len(detected) > 0 {
		if got != detected[0].Name {
			t.Fatalf("empty stored with detected editors: want %q, got %q", detected[0].Name, got)
		}
	} else if got != editorCustomOption {
		t.Fatalf("empty stored without detected editors: want %q, got %q", editorCustomOption, got)
	}
	if v := form.textValue(editorCustomFieldLabel); v != "" {
		t.Fatalf("custom text should be empty for empty stored; got %q", v)
	}
}

func TestAddEditorFields_LegacyCustomLoad(t *testing.T) {
	var fields []formField
	fields = addEditorFields(fields, "code -n --foo")
	form := newConfigForm(fields, 30)
	if got := form.selectValue(editorFieldLabel); got != editorCustomOption {
		t.Fatalf("expected Custom, got %q", got)
	}
	if got := form.textValue(editorCustomFieldLabel); got != "code -n --foo" {
		t.Fatalf("expected custom text preserved, got %q", got)
	}
}

func TestExtractIDECommand(t *testing.T) {
	tests := []struct {
		name       string
		options    []string
		selected   int
		customText string
		want       string
	}{
		{"custom with text", []string{"Zed", "Custom"}, 1, "code -n", "code -n"},
		{"custom empty", []string{"Zed", "Custom"}, 1, "", ""},
		{"custom trims whitespace", []string{"Custom"}, 0, "  code -n  ", "code -n"},
		{"detected editor", []string{"Zed", "Custom"}, 0, "", `open -a "Zed"`},
		{"detected multi-word", []string{"Visual Studio Code", "Custom"}, 0, "", `open -a "Visual Studio Code"`},
		{"detected ignores custom text", []string{"Zed", "Custom"}, 0, "stale", `open -a "Zed"`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var fields []formField
			fields = addSelect(fields, editorFieldLabel, tc.options, tc.selected)
			fields = addTextInput(fields, editorCustomFieldLabel, tc.customText, "", 30)
			form := newConfigForm(fields, 30)
			if got := extractIDECommand(form); got != tc.want {
				t.Errorf("extractIDECommand = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestSplitIDECommand(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want []string
	}{
		{"empty", "", nil},
		{"whitespace", "   \t ", nil},
		{"simple", "zed -n", []string{"zed", "-n"}},
		{"double quoted", `open -a "Zed"`, []string{"open", "-a", "Zed"}},
		{"double quoted with space", `open -a "Visual Studio Code"`, []string{"open", "-a", "Visual Studio Code"}},
		{"single quoted", `open -a 'Visual Studio Code'`, []string{"open", "-a", "Visual Studio Code"}},
		{"escaped space", `open -a Visual\ Studio\ Code`, []string{"open", "-a", "Visual Studio Code"}},
		{"mixed", `cmd "a b" 'c d' plain`, []string{"cmd", "a b", "c d", "plain"}},
		{"extra whitespace", "  a   b  ", []string{"a", "b"}},
		{"unterminated quote", `open -a "Zed`, []string{"open", "-a", "Zed"}},
		{"trailing backslash", `a\`, []string{`a\`}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := splitIDECommand(tc.in)
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("splitIDECommand(%q) = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
}
