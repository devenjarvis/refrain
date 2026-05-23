package agent

import (
	"strings"
	"testing"

	"github.com/devenjarvis/refrain/internal/proptest"
	"pgregory.net/rapid"
)

// slugify output never exceeds 40 bytes.
func TestSlugify_LengthAtMost40(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		s := arbitrarySlugInput(t)
		out := slugify(s)
		if len(out) > 40 {
			t.Fatalf("slugify(%q) = %q (len %d), want ≤ 40", s, out, len(out))
		}
	})
}

// slugify output never has a leading or trailing dash.
func TestSlugify_NoLeadingOrTrailingDash(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		s := arbitrarySlugInput(t)
		out := slugify(s)
		if out == "" {
			return
		}
		if strings.HasPrefix(out, "-") || strings.HasSuffix(out, "-") {
			t.Fatalf("slugify(%q) = %q has leading or trailing dash", s, out)
		}
	})
}

// slugify output contains only lowercase alphanumerics and dashes when non-empty.
func TestSlugify_ValidChars(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		s := arbitrarySlugInput(t)
		out := slugify(s)
		if out == "" {
			return
		}
		if !proptest.IsValidSlug(out) {
			t.Fatalf("slugify(%q) = %q contains invalid characters", s, out)
		}
	})
}

// slugify output first character is [a-z0-9] when non-empty.
func TestSlugify_FirstCharAlphanumeric(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		s := arbitrarySlugInput(t)
		out := slugify(s)
		if out == "" {
			return
		}
		c := out[0]
		if !((c >= 'a' && c <= 'z') || (c >= '0' && c <= '9')) {
			t.Fatalf("slugify(%q) = %q first char %q is not [a-z0-9]", s, out, c)
		}
	})
}

// slugify is idempotent: applying it twice produces the same result as once.
func TestSlugify_Idempotent(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		s := arbitrarySlugInput(t)
		once := slugify(s)
		twice := slugify(once)
		if once != twice {
			t.Fatalf("slugify not idempotent: slugify(%q)=%q but slugify(%q)=%q", s, once, once, twice)
		}
	})
}

// RandomName always returns a name matching the valid-name pattern.
func TestRandomName_MatchesValidNameRegex_Property(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		// Generate an arbitrary list of existing names (may be empty).
		n := rapid.IntRange(0, 20).Draw(t, "n")
		existing := make([]string, n)
		for i := range existing {
			existing[i] = RandomName(existing[:i])
		}
		name := RandomName(existing)
		if !proptest.IsValidName(name) {
			t.Fatalf("RandomName(%v) = %q does not match ValidNameRe", existing, name)
		}
	})
}
