package state

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestRoundTrip(t *testing.T) {
	dir := t.TempDir()

	saved := &RefrainState{
		Version: 1,
		SavedAt: time.Now().Truncate(time.Second),
		Sessions: []SessionState{
			{
				ID:           "sess-1",
				Name:         "warm-xerus",
				DisplayName:  "Warm Xerus",
				WorktreePath: "/tmp/worktrees/warm-xerus",
				Branch:       "refrain/warm-xerus",
				BaseBranch:   "main",
				Agents: []AgentState{
					{
						ID:              "agent-1",
						Name:            "agent-alpha",
						DisplayName:     "Alpha",
						Task:            "implement feature X",
						ClaudeSessionID: "claude-abc123",
					},
					{
						ID:   "agent-2",
						Name: "agent-beta",
					},
				},
			},
			{
				ID:           "sess-2",
				Name:         "dark-drake",
				WorktreePath: "/tmp/worktrees/dark-drake",
				Branch:       "refrain/dark-drake",
				BaseBranch:   "main",
				Agents:       []AgentState{},
			},
		},
	}

	if err := Save(dir, saved); err != nil {
		t.Fatalf("Save: %v", err)
	}

	loaded, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if loaded == nil {
		t.Fatal("Load returned nil")
	}

	if loaded.Version != saved.Version {
		t.Errorf("Version: got %d, want %d", loaded.Version, saved.Version)
	}
	if !loaded.SavedAt.Equal(saved.SavedAt) {
		t.Errorf("SavedAt: got %v, want %v", loaded.SavedAt, saved.SavedAt)
	}
	if len(loaded.Sessions) != len(saved.Sessions) {
		t.Fatalf("Sessions count: got %d, want %d", len(loaded.Sessions), len(saved.Sessions))
	}

	s := loaded.Sessions[0]
	if s.ID != "sess-1" {
		t.Errorf("Session ID: got %q, want %q", s.ID, "sess-1")
	}
	if s.Name != "warm-xerus" {
		t.Errorf("Session Name: got %q, want %q", s.Name, "warm-xerus")
	}
	if s.DisplayName != "Warm Xerus" {
		t.Errorf("Session DisplayName: got %q, want %q", s.DisplayName, "Warm Xerus")
	}
	if s.WorktreePath != "/tmp/worktrees/warm-xerus" {
		t.Errorf("WorktreePath: got %q", s.WorktreePath)
	}
	if s.Branch != "refrain/warm-xerus" {
		t.Errorf("Branch: got %q", s.Branch)
	}
	if s.BaseBranch != "main" {
		t.Errorf("BaseBranch: got %q", s.BaseBranch)
	}
	if len(s.Agents) != 2 {
		t.Fatalf("Agents count: got %d, want 2", len(s.Agents))
	}

	a := s.Agents[0]
	if a.ID != "agent-1" {
		t.Errorf("Agent ID: got %q", a.ID)
	}
	if a.Name != "agent-alpha" {
		t.Errorf("Agent Name: got %q", a.Name)
	}
	if a.DisplayName != "Alpha" {
		t.Errorf("Agent DisplayName: got %q", a.DisplayName)
	}
	if a.Task != "implement feature X" {
		t.Errorf("Agent Task: got %q", a.Task)
	}
	if a.ClaudeSessionID != "claude-abc123" {
		t.Errorf("Agent ClaudeSessionID: got %q", a.ClaudeSessionID)
	}

	// Check omitempty fields are absent for agent-2
	a2 := s.Agents[1]
	if a2.DisplayName != "" {
		t.Errorf("Agent2 DisplayName should be empty, got %q", a2.DisplayName)
	}
	if a2.Task != "" {
		t.Errorf("Agent2 Task should be empty, got %q", a2.Task)
	}

	// Check second session has empty display name (omitempty)
	s2 := loaded.Sessions[1]
	if s2.DisplayName != "" {
		t.Errorf("Session2 DisplayName should be empty, got %q", s2.DisplayName)
	}
}

func TestLoadMissingFile(t *testing.T) {
	dir := t.TempDir()

	loaded, err := Load(dir)
	if err != nil {
		t.Fatalf("Load on missing file should not error, got: %v", err)
	}
	if loaded != nil {
		t.Fatalf("Load on missing file should return nil, got: %+v", loaded)
	}
}

func TestRemoveIdempotent(t *testing.T) {
	dir := t.TempDir()

	saved := &RefrainState{
		Version:  1,
		SavedAt:  time.Now().Truncate(time.Second),
		Sessions: []SessionState{},
	}
	if err := Save(dir, saved); err != nil {
		t.Fatalf("Save: %v", err)
	}

	// First remove should succeed
	if err := Remove(dir); err != nil {
		t.Fatalf("First Remove: %v", err)
	}

	// File should be gone
	if _, err := os.Stat(statePath(dir)); !os.IsNotExist(err) {
		t.Fatalf("File should not exist after Remove")
	}

	// Second remove should also succeed (idempotent)
	if err := Remove(dir); err != nil {
		t.Fatalf("Second Remove: %v", err)
	}
}

func TestSaveCreatesBatonDir(t *testing.T) {
	dir := t.TempDir()
	// Use a subdirectory that doesn't exist yet
	repoPath := filepath.Join(dir, "newrepo")

	saved := &RefrainState{
		Version:  1,
		SavedAt:  time.Now().Truncate(time.Second),
		Sessions: []SessionState{},
	}

	if err := Save(repoPath, saved); err != nil {
		t.Fatalf("Save: %v", err)
	}

	// Verify the .refrain directory was created
	info, err := os.Stat(filepath.Join(repoPath, ".refrain"))
	if err != nil {
		t.Fatalf("Expected .refrain dir to exist: %v", err)
	}
	if !info.IsDir() {
		t.Fatal(".refrain should be a directory")
	}

	// Verify the file exists and is loadable
	loaded, err := Load(repoPath)
	if err != nil {
		t.Fatalf("Load after Save: %v", err)
	}
	if loaded == nil {
		t.Fatal("Load returned nil after Save")
	}
	if loaded.Version != 1 {
		t.Errorf("Version: got %d, want 1", loaded.Version)
	}
}

func TestSessionState_LifecyclePersistence(t *testing.T) {
	dir := t.TempDir()

	doneAt := time.Date(2026, 5, 3, 12, 0, 0, 0, time.UTC)
	original := &RefrainState{
		Version: 1,
		Sessions: []SessionState{
			{
				ID:             "sess-1",
				Name:           "test",
				WorktreePath:   "/tmp/wt",
				Branch:         "main",
				LifecyclePhase: "ready_for_review",
				OriginalPrompt: "fix the auth bug",
				DoneAt:         &doneAt,
			},
		},
	}

	if err := Save(dir, original); err != nil {
		t.Fatalf("Save: %v", err)
	}

	loaded, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if len(loaded.Sessions) != 1 {
		t.Fatalf("expected 1 session, got %d", len(loaded.Sessions))
	}
	s := loaded.Sessions[0]
	if s.LifecyclePhase != "ready_for_review" {
		t.Errorf("LifecyclePhase = %q, want %q", s.LifecyclePhase, "ready_for_review")
	}
	if s.OriginalPrompt != "fix the auth bug" {
		t.Errorf("OriginalPrompt = %q, want %q", s.OriginalPrompt, "fix the auth bug")
	}
	if s.DoneAt == nil || !s.DoneAt.Equal(time.Date(2026, 5, 3, 12, 0, 0, 0, time.UTC)) {
		t.Errorf("DoneAt = %v, want 2026-05-03T12:00:00Z", s.DoneAt)
	}
}

func TestSaveConcurrent(t *testing.T) {
	dir := t.TempDir()

	const writers = 32
	var wg sync.WaitGroup
	wg.Add(writers)
	for i := range writers {
		go func() {
			defer wg.Done()
			s := &RefrainState{
				Version: 1,
				SavedAt: time.Now(),
				Sessions: []SessionState{
					{ID: fmt.Sprintf("sess-%d", i), Name: fmt.Sprintf("name-%d", i), WorktreePath: "/tmp", Branch: "main"},
				},
			}
			if err := Save(dir, s); err != nil {
				t.Errorf("Save: %v", err)
			}
		}()
	}
	wg.Wait()

	loaded, err := Load(dir)
	if err != nil {
		t.Fatalf("Load after concurrent Save: %v", err)
	}
	if loaded == nil || len(loaded.Sessions) != 1 {
		t.Fatalf("expected exactly one session in the final snapshot, got %+v", loaded)
	}

	// No leftover .json.tmp files should remain.
	refrainDir := filepath.Join(dir, ".refrain")
	entries, err := os.ReadDir(refrainDir)
	if err != nil {
		t.Fatalf("read .refrain: %v", err)
	}
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".tmp") {
			t.Errorf("leftover temp file: %s", e.Name())
		}
	}
}

func TestLoadCorruptFile(t *testing.T) {
	dir := t.TempDir()
	refrainDir := filepath.Join(dir, ".refrain")
	if err := os.MkdirAll(refrainDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(refrainDir, "state.json"), []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}

	loaded, err := Load(dir)
	if err == nil {
		t.Fatal("expected error loading corrupt file, got nil")
	}
	if loaded != nil {
		t.Errorf("expected nil state on corrupt load, got %+v", loaded)
	}
	if !strings.Contains(err.Error(), "unmarshalling state") {
		t.Errorf("expected unmarshal error, got: %v", err)
	}
}

func TestSaveDirCreateFailure(t *testing.T) {
	dir := t.TempDir()
	// Make repoPath/.refrain a regular file so MkdirAll fails.
	blocker := filepath.Join(dir, ".refrain")
	if err := os.WriteFile(blocker, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	s := &RefrainState{Version: 1, SavedAt: time.Now()}
	err := Save(dir, s)
	if err == nil {
		t.Fatal("expected error when .refrain is a regular file, got nil")
	}
	if !strings.Contains(err.Error(), "creating dir") {
		t.Errorf("expected dir-creation error, got: %v", err)
	}
}

func TestSessionState_BackwardsCompatibility(t *testing.T) {
	// A state file without lifecycle fields should load with empty defaults.
	dir := t.TempDir()
	raw := `{"version":1,"savedAt":"2026-05-03T00:00:00Z","sessions":[{"id":"s1","name":"test","worktreePath":"/tmp","branch":"main","ownsBranch":true,"agents":[]}]}`
	refrainDir := filepath.Join(dir, ".refrain")
	if err := os.MkdirAll(refrainDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(refrainDir, "state.json"), []byte(raw), 0o644); err != nil {
		t.Fatal(err)
	}
	loaded, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(loaded.Sessions) != 1 {
		t.Fatalf("expected 1 session, got %d", len(loaded.Sessions))
	}
	if loaded.Sessions[0].LifecyclePhase != "" {
		// Empty string means InProgress when parsed — that's the zero value default.
		t.Errorf("expected empty LifecyclePhase for backwards compat, got %q", loaded.Sessions[0].LifecyclePhase)
	}
	if loaded.Sessions[0].Kind != "" {
		// Empty string means "worktree" when parsed — all legacy sessions
		// predate the kind field and are worktree sessions.
		t.Errorf("expected empty Kind for backwards compat, got %q", loaded.Sessions[0].Kind)
	}
}

func TestSessionState_KindPersistence(t *testing.T) {
	dir := t.TempDir()
	s := &RefrainState{
		Version: 1,
		SavedAt: time.Now(),
		Sessions: []SessionState{
			{ID: "s1", Name: "checkout", WorktreePath: "/repo", Branch: "main", Kind: "checkout"},
			{ID: "s2", Name: "worktree", WorktreePath: "/repo/.refrain/worktrees/x", Branch: "refrain/x", Kind: "worktree"},
		},
	}
	if err := Save(dir, s); err != nil {
		t.Fatalf("Save: %v", err)
	}
	loaded, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got := loaded.Sessions[0].Kind; got != "checkout" {
		t.Errorf("Sessions[0].Kind = %q, want checkout", got)
	}
	if got := loaded.Sessions[1].Kind; got != "worktree" {
		t.Errorf("Sessions[1].Kind = %q, want worktree", got)
	}
}
