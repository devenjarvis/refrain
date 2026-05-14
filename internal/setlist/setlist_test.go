package setlist_test

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/devenjarvis/refrain/internal/setlist"
)

// setlistDirInTmp redirects $HOME into a temp directory so tests never touch
// the real ~/.refrain/. Returns the ~/.refrain directory.
func setlistDirInTmp(t *testing.T) string {
	t.Helper()
	base := t.TempDir()
	t.Setenv("HOME", base)
	return filepath.Join(base, ".refrain")
}

func TestAppend_RoundTrip(t *testing.T) {
	setlistDirInTmp(t)

	entries := []setlist.Entry{
		{
			PlayedAt:  time.Now().UTC().Truncate(time.Second),
			Name:      "Song One",
			Artist:    "Artist A",
			ISRC:      "USRC11111111",
			Slug:      "song-one",
			Repo:      "repo-a",
			SessionID: "sess-1",
		},
		{
			PlayedAt: time.Now().UTC().Add(time.Minute).Truncate(time.Second),
			Name:     "Song Two",
			Artist:   "Artist B",
			ISRC:     "USRC22222222",
			Slug:     "song-two",
		},
		{
			PlayedAt:  time.Now().UTC().Add(2 * time.Minute).Truncate(time.Second),
			Name:      "Song Three",
			Artist:    "Artist C",
			ISRC:      "USRC33333333",
			Slug:      "song-three",
			Repo:      "repo-c",
			SessionID: "sess-3",
		},
	}

	for _, e := range entries {
		if err := setlist.Append(e); err != nil {
			t.Fatalf("Append(%q) error = %v", e.Name, err)
		}
	}

	loaded, err := setlist.Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if len(loaded) != len(entries) {
		t.Fatalf("Load() len = %d, want %d", len(loaded), len(entries))
	}
	for i, want := range entries {
		got := loaded[i]
		if !got.PlayedAt.Equal(want.PlayedAt) {
			t.Errorf("entry[%d] PlayedAt = %v, want %v", i, got.PlayedAt, want.PlayedAt)
		}
		if got.Name != want.Name {
			t.Errorf("entry[%d] Name = %q, want %q", i, got.Name, want.Name)
		}
		if got.Artist != want.Artist {
			t.Errorf("entry[%d] Artist = %q, want %q", i, got.Artist, want.Artist)
		}
		if got.ISRC != want.ISRC {
			t.Errorf("entry[%d] ISRC = %q, want %q", i, got.ISRC, want.ISRC)
		}
		if got.Slug != want.Slug {
			t.Errorf("entry[%d] Slug = %q, want %q", i, got.Slug, want.Slug)
		}
		if got.Repo != want.Repo {
			t.Errorf("entry[%d] Repo = %q, want %q", i, got.Repo, want.Repo)
		}
		if got.SessionID != want.SessionID {
			t.Errorf("entry[%d] SessionID = %q, want %q", i, got.SessionID, want.SessionID)
		}
	}
}

func TestLoad_MissingFile_ReturnsNil(t *testing.T) {
	setlistDirInTmp(t)

	entries, err := setlist.Load()
	if err != nil {
		t.Fatalf("Load() error = %v, want nil", err)
	}
	if entries != nil {
		t.Fatalf("Load() = %v, want nil", entries)
	}
}

func TestLoad_MalformedLineTolerated(t *testing.T) {
	dir := setlistDirInTmp(t)

	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	path := filepath.Join(dir, "setlist.jsonl")

	content := `{"playedAt":"2026-01-01T00:00:00Z","name":"First","artist":"A","isrc":"X","slug":"first"}
this is not json at all {{{
{"playedAt":"2026-01-02T00:00:00Z","name":"Second","artist":"B","isrc":"Y","slug":"second"}
`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	entries, err := setlist.Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("Load() len = %d, want 2", len(entries))
	}
	if entries[0].Name != "First" {
		t.Errorf("entries[0].Name = %q, want %q", entries[0].Name, "First")
	}
	if entries[1].Name != "Second" {
		t.Errorf("entries[1].Name = %q, want %q", entries[1].Name, "Second")
	}
}

func TestAppend_CreatesParentDir(t *testing.T) {
	dir := setlistDirInTmp(t)

	// Confirm ~/.refrain does not exist yet.
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("expected %s to not exist, got err=%v", dir, err)
	}

	e := setlist.Entry{
		PlayedAt: time.Now().UTC().Truncate(time.Second),
		Name:     "Only Song",
		Artist:   "Some Artist",
		ISRC:     "USRC44444444",
		Slug:     "only-song",
	}
	if err := setlist.Append(e); err != nil {
		t.Fatalf("Append() error = %v", err)
	}

	path := filepath.Join(dir, "setlist.jsonl")
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("stat %s: %v", path, err)
	}
}
