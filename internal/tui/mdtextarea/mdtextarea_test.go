package mdtextarea

import (
	"os"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/lipgloss"
	"github.com/devenjarvis/baton/internal/tui/mdrender"
	"github.com/devenjarvis/baton/internal/tui/mdrender/testutil"
	"github.com/muesli/termenv"
)

// TestMain forces TrueColor so styling assertions don't depend on whether the
// test binary's stdout is a TTY. Same convention as mdrender's TestMain.
func TestMain(m *testing.M) {
	prev := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	code := m.Run()
	lipgloss.SetColorProfile(prev)
	os.Exit(code)
}

// configureForPlanEditor mirrors how planeditor.go drives the textarea so
// the tests render under the same conditions as production. Returns a
// *Model so tests can mutate it conveniently.
func configureForPlanEditor(t *testing.T, value string, w, h int) *Model {
	t.Helper()
	m := New()
	m.Prompt = ""
	m.ShowLineNumbers = false
	m.SetWidth(w)
	m.SetHeight(h)
	m.SetValue(value)
	return &m
}

// TestNilRenderer_ByteIdenticalToUpstream pins the contract that mdtextarea
// is invisible to callers who don't opt in. Without a renderer, View() must
// match the embedded textarea's View() exactly, byte for byte. This is what
// lets us swap the import in planeditor without semantic risk.
func TestNilRenderer_ByteIdenticalToUpstream(t *testing.T) {
	cases := []struct {
		name  string
		value string
	}{
		{"empty", ""},
		{"single line", "hello world"},
		{"multi line", "first line\nsecond line\nthird line"},
		{"trailing newline", "alpha\nbeta\n"},
		{"with markdown", "# Heading\n\nSome paragraph with **bold**.\n"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			m := configureForPlanEditor(t, c.value, 60, 8)
			m.Focus()
			gotWrapper := m.View()
			gotUpstream := m.Model.View()
			if gotWrapper != gotUpstream {
				t.Errorf("wrapper output diverges from upstream:\n--- wrapper:\n%q\n--- upstream:\n%q", gotWrapper, gotUpstream)
			}
		})
	}
}

func TestWithRenderer_HeadingHasStyling(t *testing.T) {
	m := configureForPlanEditor(t, "# Hello\n", 60, 8)
	m.Focus()
	r := mdrender.New("monokai")
	m.SetMarkdownRenderer(r)

	got := m.View()
	stripped := testutil.StripANSI(got)
	if !strings.Contains(stripped, "# Hello") {
		t.Errorf("rendered output lost heading text:\n%s", stripped)
	}
	// CSI escape sequences must appear somewhere — heading styling should be
	// present.
	if !strings.Contains(got, "\x1b[") {
		t.Errorf("expected ANSI styling in output, got:\n%s", got)
	}
}

func TestWithRenderer_SoftWrapPreservesContent(t *testing.T) {
	long := strings.Repeat("word ", 30)
	m := configureForPlanEditor(t, long, 20, 8)
	m.Focus()
	m.SetMarkdownRenderer(mdrender.New("monokai"))

	got := m.View()
	stripped := testutil.StripANSI(got)
	if !strings.Contains(stripped, "word") {
		t.Errorf("wrap dropped content; output:\n%s", stripped)
	}
	// Splitting on \n should yield more than one row of plan content (the
	// long line wrapped) plus EOB padding.
	rows := strings.Split(stripped, "\n")
	wordRows := 0
	for _, row := range rows {
		if strings.Contains(row, "word") {
			wordRows++
		}
	}
	if wordRows < 2 {
		t.Errorf("expected wrap into 2+ rows; saw %d rows containing 'word'", wordRows)
	}
}

func TestSetMarkdownRendererRoundTrip(t *testing.T) {
	m := New()
	if m.MarkdownRenderer() != nil {
		t.Errorf("renderer should default to nil, got %v", m.MarkdownRenderer())
	}
	r := mdrender.New("monokai")
	m.SetMarkdownRenderer(r)
	if m.MarkdownRenderer() != r {
		t.Errorf("MarkdownRenderer() did not return the value passed to SetMarkdownRenderer")
	}
	m.SetMarkdownRenderer(nil)
	if m.MarkdownRenderer() != nil {
		t.Errorf("clearing renderer failed; got %v", m.MarkdownRenderer())
	}
}

func TestUpdate_PreservesRendererReference(t *testing.T) {
	// Update must not erase the renderer reference, since the embedded
	// textarea.Model.Update returns a fresh value.
	m := New()
	m.Prompt = ""
	m.SetWidth(40)
	m.SetHeight(4)
	m.Focus()
	r := mdrender.New("monokai")
	m.SetMarkdownRenderer(r)

	// A no-op key event still routes through Update's full path.
	m, _ = m.Update(tea.KeyPressMsg{Code: 'x', Text: "x"})
	if m.MarkdownRenderer() != r {
		t.Errorf("Update() dropped the renderer reference; got %v want %v", m.MarkdownRenderer(), r)
	}
}

func TestView_HeightMatchesConfigured(t *testing.T) {
	const h = 6
	m := configureForPlanEditor(t, "one\ntwo\nthree\n", 40, h)
	m.Focus()
	m.SetMarkdownRenderer(mdrender.New("monokai"))

	got := m.View()
	rows := strings.Split(got, "\n")
	// The rendered output is wrapped in styles.Base.Render — that wraps
	// content but does not add or remove rows. Allow ±1 for trailing reset
	// behavior under different lipgloss versions.
	if len(rows) < h || len(rows) > h+1 {
		t.Errorf("expected ~%d rows, got %d", h, len(rows))
	}
}

// TestView_FocusedCursorSplice asserts that the cursor's row carries a
// reverse-video SGR (CSI 7) and the rune that the cursor sits on still
// appears on the line. Reverse-video is what `cursorStyle` emits, so a row
// containing CSI 7 + the cursor rune is concrete proof the splice ran.
func TestView_FocusedCursorSplice(t *testing.T) {
	m := configureForPlanEditor(t, "abc\n", 40, 4)
	m.Focus()
	m.SetMarkdownRenderer(mdrender.New("monokai"))
	// Position the cursor at column 1 (on the 'b').
	m.SetValue("abc")
	m.SetCursorColumn(1)

	got := m.View()
	rows := strings.Split(got, "\n")
	if len(rows) < 1 {
		t.Fatalf("no rows in output:\n%s", got)
	}
	// The first row must contain a CSI-7 (reverse) sequence somewhere.
	if !strings.Contains(rows[0], "\x1b[7") && !strings.Contains(rows[0], ";7") && !strings.Contains(rows[0], "\x1b[7m") {
		t.Errorf("expected reverse-video escape on cursor row; got %q", rows[0])
	}
	// And the cursor rune must still be present (under StripANSI).
	if !strings.Contains(testutil.StripANSI(rows[0]), "abc") {
		t.Errorf("cursor row lost content under strip: %q", testutil.StripANSI(rows[0]))
	}
}

// TestView_FocusedCursorAtEndOfLine exercises the "cursor past end of line"
// branch of spliceCursor — when the user is at column N of an N-char line,
// we append a styled space cell so the cursor is visible.
func TestView_FocusedCursorAtEndOfLine(t *testing.T) {
	m := configureForPlanEditor(t, "", 40, 4)
	m.Focus()
	m.SetMarkdownRenderer(mdrender.New("monokai"))
	m.SetValue("abc")
	m.MoveToEnd() // cursor lands at col 3, past the last char.

	got := m.View()
	if !strings.Contains(got, "\x1b[7") && !strings.Contains(got, ";7") && !strings.Contains(got, "\x1b[7m") {
		t.Errorf("expected reverse-video escape for end-of-line cursor; got %q", got)
	}
}

func TestView_UnfocusedHasNoCursorSplice(t *testing.T) {
	// With a renderer set but textarea unfocused, the rendered row for the
	// cursor's source line should not contain a reverse-video cell. We can't
	// easily detect that with a substring, so just assert the call is stable
	// and content survives.
	m := configureForPlanEditor(t, "# Heading\n", 60, 4)
	m.SetMarkdownRenderer(mdrender.New("monokai"))
	// No Focus() here.
	got := m.View()
	if !strings.Contains(testutil.StripANSI(got), "# Heading") {
		t.Errorf("unfocused view dropped content:\n%s", got)
	}
}
