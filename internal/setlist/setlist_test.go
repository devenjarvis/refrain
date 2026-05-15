package setlist_test

import (
	"bufio"
	"encoding/json"
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

// readSetlist reads the entries from the on-disk setlist file. Used by the
// Append round-trip test in lieu of a production Load() helper.
func readSetlist(t *testing.T, path string) []setlist.Entry {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open %s: %v", path, err)
	}
	defer func() { _ = f.Close() }()

	var entries []setlist.Entry
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		var e setlist.Entry
		if err := json.Unmarshal(scanner.Bytes(), &e); err != nil {
			t.Fatalf("unmarshal %q: %v", scanner.Text(), err)
		}
		entries = append(entries, e)
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("scan: %v", err)
	}
	return entries
}

func TestAppend_RoundTrip(t *testing.T) {
	dir := setlistDirInTmp(t)

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

	loaded := readSetlist(t, filepath.Join(dir, "setlist.jsonl"))
	if len(loaded) != len(entries) {
		t.Fatalf("len = %d, want %d", len(loaded), len(entries))
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
		t.Fatalf("Append() error = %v", e)
	}

	path := filepath.Join(dir, "setlist.jsonl")
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("stat %s: %v", path, err)
	}
}
