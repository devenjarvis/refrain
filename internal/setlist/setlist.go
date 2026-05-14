// Package setlist persists a JSONL log of songs that have "played" for
// Refrain sessions. Each newly created session appends one entry to
// ~/.refrain/setlist.jsonl so the user can later browse a setlist of past
// sessions.
package setlist

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"github.com/devenjarvis/refrain/internal/config"
)

// Entry is a single record in the setlist log.
type Entry struct {
	PlayedAt  time.Time `json:"playedAt"`
	Name      string    `json:"name"`
	Artist    string    `json:"artist"`
	ISRC      string    `json:"isrc"`
	Slug      string    `json:"slug"`
	Repo      string    `json:"repo,omitempty"`
	SessionID string    `json:"sessionId,omitempty"`
}

// Path returns the absolute path to the setlist JSONL file inside ~/.refrain.
func Path() (string, error) {
	dir, err := config.RefrainDir()
	if err != nil {
		return "", fmt.Errorf("setlist: resolving refrain dir: %w", err)
	}
	return filepath.Join(dir, "setlist.jsonl"), nil
}

// Append serialises e as JSON and appends it as a new line to the setlist
// file, creating the parent directory and file if needed.
func Append(e Entry) error {
	path, err := Path()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("setlist: creating parent dir: %w", err)
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("setlist: opening file: %w", err)
	}
	defer func() { _ = f.Close() }()

	data, err := json.Marshal(e)
	if err != nil {
		return fmt.Errorf("setlist: marshalling entry: %w", err)
	}
	data = append(data, '\n')
	if _, err := f.Write(data); err != nil {
		return fmt.Errorf("setlist: writing entry: %w", err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("setlist: closing file: %w", err)
	}
	return nil
}

// Load reads all entries from the setlist file. Malformed JSON lines are
// skipped silently so a partially written or hand-edited file does not
// surface as an error. If the file does not exist, Load returns (nil, nil).
func Load() ([]Entry, error) {
	path, err := Path()
	if err != nil {
		return nil, err
	}
	f, err := os.Open(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("setlist: opening file: %w", err)
	}
	defer func() { _ = f.Close() }()

	var entries []Entry
	scanner := bufio.NewScanner(f)
	// Allow long lines just in case an entry grows past the default 64KiB.
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var e Entry
		if err := json.Unmarshal(line, &e); err != nil {
			// Tolerate malformed lines silently.
			continue
		}
		entries = append(entries, e)
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("setlist: scanning file: %w", err)
	}
	return entries, nil
}
