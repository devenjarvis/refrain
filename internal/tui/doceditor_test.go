package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/devenjarvis/refrain/internal/tui/mdrender/testutil"
	"github.com/muesli/termenv"
)

func TestDocEditor_ContentWidth(t *testing.T) {
	tests := []struct {
		width       int
		wantMeasure int
	}{
		// textareaWidth(60)=58 < docEditorMaxMeasure(72) → full width
		{60, 58},
		// textareaWidth(80)=78 > 72 → capped at 72
		{80, 72},
		// textareaWidth(120)=118 > 72 → capped at 72
		{120, 72},
	}
	for _, tc := range tests {
		d := newDocEditor(tc.width, 24)
		if got := d.ContentWidth(); got != tc.wantMeasure {
			t.Errorf("width=%d: ContentWidth()=%d, want %d", tc.width, got, tc.wantMeasure)
		}
	}
}

func TestDocEditor_DisplayLeftPad(t *testing.T) {
	tests := []struct {
		width   int
		wantPad int
	}{
		// textareaWidth(60)=58: 58 <= 72, no centering
		{60, 0},
		// textareaWidth(80)=78: (78-72)/2 = 3
		{80, 3},
		// textareaWidth(120)=118: (118-72)/2 = 23
		{120, 23},
	}
	for _, tc := range tests {
		d := newDocEditor(tc.width, 24)
		if got := d.DisplayLeftPad(); got != tc.wantPad {
			t.Errorf("width=%d: DisplayLeftPad()=%d, want %d", tc.width, got, tc.wantPad)
		}
	}
}

func TestDocEditor_ClampScroll(t *testing.T) {
	tests := []struct {
		name       string
		totalLines int
		initial    int
		want       int
	}{
		{"past end", 100, 200, 100},
		{"negative", 100, -5, 0},
		{"in range", 100, 50, 50},
		{"at end", 100, 100, 100},
		{"zero total", 0, 5, 0},
	}
	for _, tc := range tests {
		d := newDocEditor(80, 24)
		d.scrollOff = tc.initial
		d.ClampScroll(tc.totalLines)
		if d.scrollOff != tc.want {
			t.Errorf("%s: scrollOff=%d, want %d", tc.name, d.scrollOff, tc.want)
		}
	}
}

func TestDocEditor_ScrollWindow(t *testing.T) {
	lines := []string{"a", "b", "c", "d", "e"}
	tests := []struct {
		name      string
		scrollOff int
		bodyH     int
		want      string
	}{
		// scrollOff=0, bodyH=3 → lines[0:3]
		{"from start", 0, 3, "a\nb\nc"},
		// scrollOff=2, bodyH=2 → lines[2:4]
		{"mid scroll", 2, 2, "c\nd"},
		// scrollOff=3, bodyH=3, len=5 → start=max(0,5-3)=2 → lines[2:5]
		{"clamp at end", 3, 3, "c\nd\ne"},
		// scrollOff=10, bodyH=3, len=5 → start=max(0,5-3)=2 → lines[2:5]
		{"overflow scroll", 10, 3, "c\nd\ne"},
		// full view
		{"full view", 0, 5, "a\nb\nc\nd\ne"},
	}
	for _, tc := range tests {
		d := newDocEditor(80, 24)
		d.scrollOff = tc.scrollOff
		if got := d.ScrollWindow(lines, tc.bodyH); got != tc.want {
			t.Errorf("%s: ScrollWindow=%q, want %q", tc.name, got, tc.want)
		}
	}
}

func TestDocEditor_RenderLines_NonEmpty(t *testing.T) {
	lipgloss.SetColorProfile(termenv.TrueColor)
	d := newDocEditor(80, 24)
	d.SetValue("# Hello\n\nSome content here\n")
	lines := d.RenderLines()
	if len(lines) == 0 {
		t.Fatal("RenderLines returned no lines")
	}
	stripped := testutil.StripANSI(strings.Join(lines, "\n"))
	if !strings.Contains(stripped, "Hello") {
		t.Errorf("RenderLines output did not contain 'Hello':\n%s", stripped)
	}
	if !strings.Contains(stripped, "Some content here") {
		t.Errorf("RenderLines output did not contain body text:\n%s", stripped)
	}
}
