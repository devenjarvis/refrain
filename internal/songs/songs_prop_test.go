package songs

import (
	"strings"
	"testing"

	"github.com/devenjarvis/refrain/internal/proptest"
	"pgregory.net/rapid"
)

// Track.Slug output never exceeds 40 bytes.
func TestTrackSlug_LengthAtMost40(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		name := genTrackName(t)
		out := Track{Name: name}.Slug()
		if len(out) > 40 {
			t.Fatalf("Track{Name:%q}.Slug() = %q (len %d), want ≤ 40", name, out, len(out))
		}
	})
}

// Track.Slug output never has a leading or trailing dash.
func TestTrackSlug_NoLeadingOrTrailingDash(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		name := genTrackName(t)
		out := Track{Name: name}.Slug()
		if out == "" {
			return
		}
		if strings.HasPrefix(out, "-") || strings.HasSuffix(out, "-") {
			t.Fatalf("Track{Name:%q}.Slug() = %q has leading or trailing dash", name, out)
		}
	})
}

// Track.Slug output contains only valid slug characters when non-empty.
func TestTrackSlug_ValidChars(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		name := genTrackName(t)
		out := Track{Name: name}.Slug()
		if out == "" {
			return
		}
		if !proptest.IsValidSlug(out) {
			t.Fatalf("Track{Name:%q}.Slug() = %q contains invalid characters", name, out)
		}
	})
}

// Track.Slug output first character is [a-z0-9] when non-empty.
func TestTrackSlug_FirstCharAlphanumeric(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		name := genTrackName(t)
		out := Track{Name: name}.Slug()
		if out == "" {
			return
		}
		c := out[0]
		if !((c >= 'a' && c <= 'z') || (c >= '0' && c <= '9')) {
			t.Fatalf("Track{Name:%q}.Slug() = %q first char %q is not [a-z0-9]", name, out, c)
		}
	})
}

// Track.Slug is idempotent: wrapping the slug in another Track and calling Slug again yields the same result.
func TestTrackSlug_Idempotent(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		name := genTrackName(t)
		once := Track{Name: name}.Slug()
		twice := Track{Name: once}.Slug()
		if once != twice {
			t.Fatalf("Track.Slug not idempotent: Slug(%q)=%q but Slug(%q)=%q", name, once, once, twice)
		}
	})
}

// Every catalog entry produces a non-empty slug matching ValidSlugRe.
func TestCatalog_AllSlugsMatchValidSlugRe(t *testing.T) {
	for _, tr := range catalog {
		slug := tr.Slug()
		if slug == "" {
			t.Errorf("catalog track %q produced empty slug", tr.Name)
			continue
		}
		if !proptest.IsValidSlug(slug) {
			t.Errorf("catalog track %q slug %q does not match ValidSlugRe", tr.Name, slug)
		}
	}
}
