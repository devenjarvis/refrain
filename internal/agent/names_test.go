package agent

import (
	"regexp"
	"strings"
	"testing"
)

var validNameRe = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_-]*$`)

func TestRandomName_NonEmpty(t *testing.T) {
	name := RandomName(nil)
	if name == "" {
		t.Fatal("RandomName(nil) returned empty string")
	}
}

func TestRandomName_MatchesValidNameRegex(t *testing.T) {
	for i := 0; i < 50; i++ {
		name := RandomName(nil)
		if !validNameRe.MatchString(name) {
			t.Errorf("RandomName() returned %q which does not match validName regex", name)
		}
	}
}

func TestRandomName_ContainsDash(t *testing.T) {
	name := RandomName(nil)
	if !strings.Contains(name, "-") {
		t.Errorf("RandomName() returned %q; expected adjective-noun format with dash", name)
	}
}

func TestRandomName_AvoidsCollision(t *testing.T) {
	// Build a nearly-full existing list by generating many names and keeping
	// almost all combinations. We call RandomName many times and ensure
	// none of the returned names appear in the existing list.
	const rounds = 200
	existing := make([]string, 0, rounds)
	for i := 0; i < rounds; i++ {
		n := RandomName(existing)
		for _, e := range existing {
			if e == n {
				t.Errorf("RandomName returned %q which already exists in the existing list", n)
			}
		}
		existing = append(existing, n)
	}
}

// TestSlugify_DashAtBoundary validates that the 41-byte window correctly
// handles the edge case where a word boundary dash falls exactly at index 40.
// Without the 41-byte window, the algorithm would find the previous dash (at
// index 34) and return a 34-char slug instead of the full 40-char prefix.
func TestSlugify_DashAtBoundary(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{
			// Slug: "aaaaa-bbbb-cccc-dddd-eeee-ffff-gggg-hhhh-iiii-jjjj" (50 chars)
			// Dash at index 40 → cut at 40 → 40-char result.
			in:   "aaaaa bbbb cccc dddd eeee ffff gggg hhhh iiii jjjj",
			want: "aaaaa-bbbb-cccc-dddd-eeee-ffff-gggg-hhhh",
		},
		{
			// Slug exactly 40 chars — no truncation needed.
			in:   "aaaa bbbb cccc dddd eeee ffff gggg hhhh",
			want: "aaaa-bbbb-cccc-dddd-eeee-ffff-gggg-hhhh",
		},
	}
	for _, tc := range cases {
		got := slugify(tc.in)
		if got != tc.want {
			t.Errorf("slugify(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestRandomName_WithExistingAvoidsDuplicate(t *testing.T) {
	// Generate a name, then check that calling RandomName with that name as
	// existing eventually produces a different name (if the word lists allow).
	first := RandomName(nil)
	existing := []string{first}

	// Try up to 50 times — with 20+ adjectives and 20+ nouns there are
	// at least 400 combinations so a second distinct name should be easy.
	for i := 0; i < 50; i++ {
		name := RandomName(existing)
		if name != first {
			return // success
		}
	}
	// If all 50 came back as first, either the lists are tiny or something is wrong.
	t.Errorf("RandomName kept returning %q even with it in the existing list", first)
}
