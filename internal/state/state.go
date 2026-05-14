package state

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// saveMu serializes Save/Load/Remove so concurrent callers can't race on the
// final os.Rename — the rename itself is atomic, but two interleaved calls
// could still let an older snapshot win over a newer one.
var saveMu sync.Mutex

// RefrainState is the top-level persisted state for session recovery.
type RefrainState struct {
	Version  int            `json:"version"`
	SavedAt  time.Time      `json:"savedAt"`
	Sessions []SessionState `json:"sessions"`
}

// SessionState captures the state of a single refrain session (worktree).
type SessionState struct {
	ID             string       `json:"id"`
	Name           string       `json:"name"`
	DisplayName    string       `json:"displayName,omitempty"`
	WorktreePath   string       `json:"worktreePath"`
	Branch         string       `json:"branch"`
	BaseBranch     string       `json:"baseBranch"`
	OwnsBranch     bool         `json:"ownsBranch"`
	HasClaudeName  bool         `json:"hasClaudeName,omitempty"`
	LifecyclePhase string       `json:"lifecyclePhase,omitempty"`
	OriginalPrompt string       `json:"originalPrompt,omitempty"`
	DoneAt         *time.Time   `json:"doneAt,omitempty"`
	Agents         []AgentState `json:"agents"`
}

// AgentState captures the state of a single agent within a session.
type AgentState struct {
	ID              string `json:"id"`
	Name            string `json:"name"`
	DisplayName     string `json:"displayName,omitempty"`
	Task            string `json:"task,omitempty"`
	ClaudeSessionID string `json:"claudeSessionId,omitempty"`
}

// statePath returns the path to the state file for a given repo.
func statePath(repoPath string) string {
	return filepath.Join(repoPath, ".refrain", "state.json")
}

// Save persists the RefrainState to disk using atomic temp+rename. Calls are
// serialized by a package-level mutex so concurrent writes can't race on the
// final rename.
func Save(repoPath string, s *RefrainState) error {
	saveMu.Lock()
	defer saveMu.Unlock()

	path := statePath(repoPath)
	dir := filepath.Dir(path)

	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("state: creating dir %s: %w", dir, err)
	}

	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return fmt.Errorf("state: marshalling state: %w", err)
	}

	tmp, err := os.CreateTemp(dir, "state-*.json.tmp")
	if err != nil {
		return fmt.Errorf("state: creating temp file: %w", err)
	}
	tmpName := tmp.Name()

	defer func() {
		if err != nil {
			_ = os.Remove(tmpName)
		}
	}()

	if _, writeErr := tmp.Write(data); writeErr != nil {
		_ = tmp.Close()
		err = fmt.Errorf("state: writing temp file: %w", writeErr)
		return err
	}

	if closeErr := tmp.Close(); closeErr != nil {
		err = fmt.Errorf("state: closing temp file: %w", closeErr)
		return err
	}

	if renameErr := os.Rename(tmpName, path); renameErr != nil {
		err = fmt.Errorf("state: renaming temp file to %s: %w", path, renameErr)
		return err
	}

	return nil
}

// Load reads the RefrainState from disk. Returns (nil, nil) if the file does not exist.
func Load(repoPath string) (*RefrainState, error) {
	saveMu.Lock()
	defer saveMu.Unlock()

	path := statePath(repoPath)

	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("state: reading %s: %w", path, err)
	}

	var s RefrainState
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, fmt.Errorf("state: unmarshalling state: %w", err)
	}

	return &s, nil
}

// Remove deletes the state file. It is idempotent: returns nil if the file does not exist.
func Remove(repoPath string) error {
	saveMu.Lock()
	defer saveMu.Unlock()

	path := statePath(repoPath)

	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("state: removing %s: %w", path, err)
	}

	return nil
}
