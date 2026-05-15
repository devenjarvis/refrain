package diff_test

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/devenjarvis/refrain/internal/diffmodel"
	"github.com/devenjarvis/refrain/internal/tui/diff"
)

// stripped returns s with all ANSI escape sequences removed so substring
// assertions can ignore chroma coloring.
func stripped(s string) string { return ansi.Strip(s) }

func parseOrFail(t *testing.T, raw string) *diffmodel.File {
	t.Helper()
	m, err := diffmodel.Parse(raw)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(m.Files) == 0 {
		t.Fatal("no files parsed")
	}
	return &m.Files[0]
}

func TestRender_UnifiedGoFile(t *testing.T) {
	raw := "diff --git a/foo.go b/foo.go\n" +
		"index abc..def 100644\n" +
		"--- a/foo.go\n" +
		"+++ b/foo.go\n" +
		"@@ -1,3 +1,4 @@\n" +
		" package main\n" +
		" \n" +
		"+// added\n" +
		" func main() {}\n"
	f := parseOrFail(t, raw)
	r := diff.NewRenderer(f)

	out := r.Render(80, false)
	if out == "" {
		t.Fatal("empty render")
	}
	// Should contain the hunk header text.
	if !strings.Contains(out, "@@ -1,3 +1,4 @@") {
		t.Errorf("missing hunk header:\n%s", out)
	}
	// Should contain the added line text.
	if !strings.Contains(out, "// added") {
		t.Errorf("missing added line:\n%s", out)
	}
	// No row may exceed the terminal width.
	for i, line := range strings.Split(out, "\n") {
		if w := ansi.StringWidth(line); w > 80 {
			t.Errorf("line %d width %d > 80", i, w)
		}
	}
}

func TestRender_SideBySidePairedEdit(t *testing.T) {
	raw := "diff --git a/foo.go b/foo.go\n" +
		"index abc..def 100644\n" +
		"--- a/foo.go\n" +
		"+++ b/foo.go\n" +
		"@@ -1,2 +1,2 @@\n" +
		"-func Foo()\n" +
		"+func Foo(opts ...Opt)\n" +
		" body\n"
	f := parseOrFail(t, raw)
	r := diff.NewRenderer(f)

	out := r.Render(120, true)
	if out == "" {
		t.Fatal("empty render")
	}
	plain := stripped(out)
	// Both sides' content should appear.
	if !strings.Contains(plain, "func Foo()") {
		t.Errorf("missing old line:\n%s", plain)
	}
	if !strings.Contains(plain, "opts ...Opt") {
		t.Errorf("missing new line fragment:\n%s", plain)
	}
	// The central separator must appear on every body row.
	sepCount := 0
	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(line, "│") {
			sepCount++
		}
	}
	if sepCount < 2 {
		t.Errorf("expected at least 2 rows with separator, got %d:\n%s", sepCount, out)
	}
}

func TestRender_BinaryFile(t *testing.T) {
	raw := "diff --git a/image.png b/image.png\n" +
		"index abc1234..def5678 100644\n" +
		"Binary files a/image.png and b/image.png differ\n"
	f := parseOrFail(t, raw)
	r := diff.NewRenderer(f)

	out := r.Render(80, true)
	if !strings.Contains(out, "Binary file") {
		t.Errorf("binary marker missing: %q", out)
	}
	if !strings.Contains(out, "image.png") {
		t.Errorf("path missing: %q", out)
	}
}

func TestRender_WrapsLongLine(t *testing.T) {
	// Unified mode wraps long lines and emits WrapMarker; side-by-side uses truncation instead.
	long := strings.Repeat("x", 200)
	raw := "diff --git a/foo.txt b/foo.txt\n" +
		"index abc..def 100644\n" +
		"--- a/foo.txt\n" +
		"+++ b/foo.txt\n" +
		"@@ -1 +1 @@\n" +
		"-old\n" +
		"+" + long + "\n"
	f := parseOrFail(t, raw)
	r := diff.NewRenderer(f)

	out := r.Render(80, false)
	if out == "" {
		t.Fatal("empty render")
	}
	// Every visible line must fit in 80 cells.
	for i, line := range strings.Split(out, "\n") {
		if w := ansi.StringWidth(line); w > 80 {
			t.Errorf("unified line %d width %d > 80: %q", i, w, line)
		}
	}
	// There must be wrap markers somewhere.
	if !strings.Contains(out, diffmodel.WrapMarker) {
		t.Errorf("expected wrap marker in output, got:\n%s", out)
	}
}

func TestRender_SideBySideNoWrapMarker(t *testing.T) {
	long := strings.Repeat("x", 200)
	raw := "diff --git a/foo.txt b/foo.txt\n" +
		"index abc..def 100644\n" +
		"--- a/foo.txt\n" +
		"+++ b/foo.txt\n" +
		"@@ -1 +1 @@\n" +
		"-old\n" +
		"+" + long + "\n"
	f := parseOrFail(t, raw)
	r := diff.NewRenderer(f)

	out := r.Render(120, true)
	if out == "" {
		t.Fatal("empty render")
	}
	if strings.Contains(out, diffmodel.WrapMarker) {
		t.Errorf("side-by-side must not contain WrapMarker:\n%s", out)
	}
	for i, line := range strings.Split(out, "\n") {
		if w := ansi.StringWidth(line); w > 120 {
			t.Errorf("line %d width %d > 120", i, w)
		}
	}
}

func TestRender_SideBySideTabExpansion(t *testing.T) {
	raw := "diff --git a/foo.go b/foo.go\n" +
		"index abc..def 100644\n" +
		"--- a/foo.go\n" +
		"+++ b/foo.go\n" +
		"@@ -1,2 +1,2 @@\n" +
		"-\tfunc Old()\n" +
		"+\tfunc New()\n" +
		" \tbody\n"
	f := parseOrFail(t, raw)
	r := diff.NewRenderer(f)

	out := r.Render(120, true)
	if out == "" {
		t.Fatal("empty render")
	}
	if strings.Contains(stripped(out), "\t") {
		t.Errorf("output should not contain raw tabs after expansion")
	}
	for i, line := range strings.Split(out, "\n") {
		if w := ansi.StringWidth(line); w > 120 {
			t.Errorf("line %d width %d > 120 (tab expansion may be missing)", i, w)
		}
	}
}

func TestRender_RenamedFile(t *testing.T) {
	raw := "diff --git a/old.md b/new.md\n" +
		"similarity index 92%\n" +
		"rename from old.md\n" +
		"rename to new.md\n" +
		"index abc..def 100644\n" +
		"--- a/old.md\n" +
		"+++ b/new.md\n" +
		"@@ -1 +1 @@\n" +
		"-old content\n" +
		"+new content\n"
	f := parseOrFail(t, raw)
	if f.Status != diffmodel.StatusRenamed {
		t.Fatalf("expected Renamed status, got %v", f.Status)
	}
	r := diff.NewRenderer(f)

	out := r.Render(120, true)
	plain := stripped(out)
	if !strings.Contains(plain, "old content") {
		t.Errorf("missing old content:\n%s", plain)
	}
	if !strings.Contains(plain, "new content") {
		t.Errorf("missing new content:\n%s", plain)
	}
}

func TestRender_Cache(t *testing.T) {
	raw := "diff --git a/foo.go b/foo.go\n" +
		"index abc..def 100644\n" +
		"--- a/foo.go\n" +
		"+++ b/foo.go\n" +
		"@@ -1 +1 @@\n" +
		"-a\n" +
		"+b\n"
	f := parseOrFail(t, raw)
	r := diff.NewRenderer(f)
	a := r.Render(80, false)
	b := r.Render(80, false)
	if a != b {
		t.Error("cache miss: same inputs should produce identical output")
	}
	c := r.Render(120, false)
	if a == c {
		t.Error("different width should produce different output")
	}
}

func TestRender_SideBySideFallsBackUnderThreshold(t *testing.T) {
	raw := "diff --git a/foo.go b/foo.go\n" +
		"index abc..def 100644\n" +
		"--- a/foo.go\n" +
		"+++ b/foo.go\n" +
		"@@ -1 +1 @@\n" +
		"-a\n" +
		"+b\n"
	f := parseOrFail(t, raw)
	r := diff.NewRenderer(f)
	narrow := r.Render(80, true)   // asks SxS, gets unified.
	unified := r.Render(80, false) // explicit unified.
	if narrow != unified {
		t.Error("SxS request under threshold should match unified render")
	}
}
