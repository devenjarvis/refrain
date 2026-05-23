package songs

import (
	"regexp"
	"strings"
	"testing"
)

var validNameRe = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_-]*$`)

func TestSlug_MatchesValidNameRegex(t *testing.T) {
	for _, tr := range catalog {
		slug := tr.Slug()
		if !validNameRe.MatchString(slug) {
			t.Errorf("Track %q.Slug() = %q which does not match validName regex", tr.Name, slug)
		}
	}
}

func TestSlug_NonEmpty(t *testing.T) {
	for _, tr := range catalog {
		if tr.Slug() == "" {
			t.Errorf("Track %q has empty Slug()", tr.Name)
		}
	}
}

func TestCatalog_AllFieldsNonEmpty(t *testing.T) {
	if len(catalog) == 0 {
		t.Fatal("catalog is empty")
	}
	for i, tr := range catalog {
		if tr.Name == "" {
			t.Errorf("catalog[%d].Name is empty", i)
		}
		if tr.Artist == "" {
			t.Errorf("catalog[%d].Artist is empty (track %q)", i, tr.Name)
		}
		if tr.ISRC == "" {
			t.Errorf("catalog[%d].ISRC is empty (track %q)", i, tr.Name)
		}
	}
}

func TestPick_NonEmpty(t *testing.T) {
	tr := Pick(nil)
	if tr.Name == "" {
		t.Fatal("Pick(nil) returned a track with empty Name")
	}
}

func TestPick_AvoidsCollision(t *testing.T) {
	// Run up to len(catalog) rounds — once we've picked every track the
	// catalog naturally exhausts, so we cap there.
	rounds := 200
	if rounds > len(catalog) {
		rounds = len(catalog)
	}
	existing := make([]string, 0, rounds)
	for i := 0; i < rounds; i++ {
		tr := Pick(existing)
		slug := tr.Slug()
		for _, e := range existing {
			if e == slug {
				t.Errorf("Pick returned slug %q which already exists in the existing list", slug)
			}
		}
		existing = append(existing, slug)
	}
}

// TestSlug_ExactOutput asserts exact input→output mappings so that string-literal
// and boundary mutations in Slug() diverge from the original.
func TestSlug_ExactOutput(t *testing.T) {
	cases := []struct {
		name string
		want string
	}{
		// spaces → hyphens, lowercased
		{"Bohemian Rhapsody", "bohemian-rhapsody"},
		// leading special char replaced then trimmed
		{"!Hello World", "hello-world"},
		// digit-starting slug is valid
		{"99 Problems", "99-problems"},
		// exactly 40 chars: not truncated
		{strings.Repeat("a", 40), strings.Repeat("a", 40)},
		// 41 chars: truncated to exactly 40
		{strings.Repeat("a", 41), strings.Repeat("a", 40)},
		// well over 40 chars: truncated to exactly 40
		{strings.Repeat("a", 50), strings.Repeat("a", 40)},
		// slug starting with 'z': kills #3 (slug[0] > 'z' boundary — >= would reject 'z')
		{"zero", "zero"},
		// slug starting with '0': kills #4 (slug[0] < '0' boundary — <= would reject '0')
		{"007 Goldeneye", "007-goldeneye"},
		// digit + dash: slug[1]='-', kills #42 (index shift makes '-' < '0' true)
		{"0 hello", "0-hello"},
		// digit + letter: slug[1]='z', kills #44 (index shift makes 'z' > '9' true)
		{"0z world", "0z-world"},
	}
	for _, tc := range cases {
		got := Track{Name: tc.name}.Slug()
		if got != tc.want {
			t.Errorf("Track{Name:%q}.Slug() = %q, want %q", tc.name, got, tc.want)
		}
	}
}

// TestSlug_DashAtBoundary validates that the 41-byte window correctly handles
// the edge case where a word boundary dash falls exactly at index 40.
// Without the 41-byte window, the algorithm would cut at the prior dash (at
// index 34) and return "aaaaa-bbbb-cccc-dddd-eeee-ffff-gggg" (35 chars)
// instead of the full 40-char prefix.
func TestSlug_DashAtBoundary(t *testing.T) {
	cases := []struct {
		name string
		want string
	}{
		{
			// Slug: "aaaaa-bbbb-cccc-dddd-eeee-ffff-gggg-hhhh-iiii-jjjj" (50 chars)
			// Dash at index 40 → cut at 40 → 40-char result.
			name: "aaaaa bbbb cccc dddd eeee ffff gggg hhhh iiii jjjj",
			want: "aaaaa-bbbb-cccc-dddd-eeee-ffff-gggg-hhhh",
		},
		{
			// Slug exactly 40 chars — no truncation needed.
			name: "aaaa bbbb cccc dddd eeee ffff gggg hhhh",
			want: "aaaa-bbbb-cccc-dddd-eeee-ffff-gggg-hhhh",
		},
	}
	for _, tc := range cases {
		got := Track{Name: tc.name}.Slug()
		if got != tc.want {
			t.Errorf("Track{Name:%q}.Slug() = %q, want %q", tc.name, got, tc.want)
		}
	}
}

// TestSlug_TruncateTrimsTrailingDash verifies that a dash landing at position 40
// after truncation is removed by TrimRight.
func TestSlug_TruncateTrimsTrailingDash(t *testing.T) {
	// 39 a's + space + "b" → "aaa…a-b" (41 chars) → [:40] → "aaa…a-" → TrimRight → "aaa…a"
	name := strings.Repeat("a", 39) + " b"
	want := strings.Repeat("a", 39)
	got := Track{Name: name}.Slug()
	if got != want {
		t.Errorf("Track{Name:%q}.Slug() = %q, want %q (len %d)", name, got, want, len(got))
	}
}

// TestPick_PanicsOnEmptyCatalog exercises the guard at the top of Pick and checks
// the exact panic message.
func TestPick_PanicsOnEmptyCatalog(t *testing.T) {
	saved := catalog
	catalog = nil
	defer func() { catalog = saved }()
	defer func() {
		r := recover()
		if r == nil {
			t.Error("Pick with empty catalog should panic")
			return
		}
		msg, ok := r.(string)
		if !ok || msg != "songs: catalog is empty" {
			t.Errorf("wrong panic value: %v", r)
		}
	}()
	Pick(nil)
}

func TestPick_WithExistingAvoidsDuplicate(t *testing.T) {
	first := Pick(nil)
	existing := []string{first.Slug()}

	for i := 0; i < 50; i++ {
		tr := Pick(existing)
		if tr.Slug() != first.Slug() {
			return // success
		}
	}
	t.Errorf("Pick kept returning %q even with it in the existing list", first.Slug())
}
