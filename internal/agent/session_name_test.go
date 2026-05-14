package agent

import (
	"os/exec"
	"strings"
	"testing"

	"github.com/devenjarvis/refrain/internal/git"
)

func TestSlugify(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"Hello World", "hello-world"},
		{"fix: auth bug in login", "fix-auth-bug-in-login"},
		{"  spaces  and  stuff  ", "spaces-and-stuff"},
		{"UPPERCASE", "uppercase"},
		{"a-b-c", "a-b-c"},
		{"123-start", "123-start"},
		{"", ""},
		{"!@#$%", ""},
		// Truncation at the last "-" boundary inside the first 40 chars.
		// The 41-byte search window for this input ends in "...the-forty",
		// so the last "-" lands at index 35 and we cut to "a-very-long-string-that-exceeds-the".
		{"a very long string that exceeds the forty character limit for slugs yes", "a-very-long-string-that-exceeds-the"},
	}

	for _, tc := range tests {
		got := slugify(tc.input)
		if got != tc.want {
			t.Errorf("slugify(%q) = %q, want %q", tc.input, got, tc.want)
		}
	}
}

func TestSlugifyWordBoundaryTruncation(t *testing.T) {
	// When the slug is exactly 41 chars and the byte at index 40 is "-",
	// the cut keeps the full 40 chars (the word ends naturally at the limit).
	// "focus-mode-input-leak-but-also-something" is 40 chars, followed by "-"
	// in the original input — so we keep all 40.
	if got := slugify("focus mode input leak but also something else here"); got != "focus-mode-input-leak-but-also-something" {
		t.Errorf("boundary-aligned 40-char truncation: got %q", got)
	}

	// Mid-word truncation: input collapses to 43 chars where the byte at index
	// 40 is mid-word ("i" of "summari..."), so we trim back to the last "-"
	// inside the first 40 chars.
	if got := slugify("i don t see a task description to summarize"); got != "i-don-t-see-a-task-description-to" {
		t.Errorf("mid-word truncation: got %q", got)
	}

	// Hard-cut fallback: a 50-char run with no "-" in the prefix.
	long := strings.Repeat("x", 50)
	if got := slugify(long); got != strings.Repeat("x", 40) {
		t.Errorf("hard-cut fallback: got %q", got)
	}

	// Slugs at or below 40 chars are unchanged.
	short := "this-slug-fits-within-the-forty-char-cap"
	if got := slugify(short); got != short {
		t.Errorf("under-limit slug should be unchanged: got %q", got)
	}
}

func TestSessionGetDisplayName_Fallback(t *testing.T) {
	s := &Session{
		Name:   "eager-panda",
		agents: make(map[string]*Agent),
	}

	// Should fall back to Name.
	if got := s.GetDisplayName(); got != "eager-panda" {
		t.Errorf("GetDisplayName() = %q, want %q", got, "eager-panda")
	}

	if s.HasDisplayName() {
		t.Error("HasDisplayName() should be false before SetDisplayName")
	}

	s.SetDisplayName("fix-auth-bug")
	if got := s.GetDisplayName(); got != "fix-auth-bug" {
		t.Errorf("GetDisplayName() = %q, want %q", got, "fix-auth-bug")
	}

	if !s.HasDisplayName() {
		t.Error("HasDisplayName() should be true after SetDisplayName")
	}
}

func TestSessionRenameBranch(t *testing.T) {
	repo := setupTestRepo(t)

	wt, err := git.CreateWorktree(repo, "warm-ibis", "", "", "")
	if err != nil {
		t.Fatalf("CreateWorktree: %v", err)
	}
	origPath := wt.Path

	s := newSession("session-1", "warm-ibis", wt)

	if s.HasClaudeName() {
		t.Error("HasClaudeName() should be false before rename")
	}

	actual, err := s.RenameBranch(repo, "refrain/add-dark-mode")
	if err != nil {
		t.Fatalf("RenameBranch: %v", err)
	}
	if actual != "refrain/add-dark-mode" {
		t.Errorf("expected branch %q, got %q", "refrain/add-dark-mode", actual)
	}
	if s.Worktree.Branch != "refrain/add-dark-mode" {
		t.Errorf("Worktree.Branch = %q, want %q", s.Worktree.Branch, "refrain/add-dark-mode")
	}
	if s.Name != "add-dark-mode" {
		t.Errorf("Session.Name = %q, want %q", s.Name, "add-dark-mode")
	}
	if !s.HasClaudeName() {
		t.Error("HasClaudeName() should be true after rename")
	}

	// The on-disk worktree path must NOT be moved during rename — moving it
	// would yank the directory out from under the running Claude process.
	if s.Worktree.Path != origPath {
		t.Errorf("Worktree.Path changed during rename: got %q, want %q", s.Worktree.Path, origPath)
	}

	// Second rename is a no-op.
	second, err := s.RenameBranch(repo, "refrain/second-attempt")
	if err != nil {
		t.Fatalf("second RenameBranch: %v", err)
	}
	if second != "refrain/add-dark-mode" {
		t.Errorf("second rename should be no-op, got %q", second)
	}
	if s.Name != "add-dark-mode" {
		t.Errorf("second rename should not change Name, got %q", s.Name)
	}
}

func TestAgentAutoNamedAsTrack(t *testing.T) {
	repo := setupTestRepo(t)
	wt, err := git.CreateWorktree(repo, "bohemian-rhapsody", "", "", "")
	if err != nil {
		t.Fatalf("CreateWorktree: %v", err)
	}
	s := newSession("session-1", "bohemian-rhapsody", wt)
	bash := func(name string) *exec.Cmd { return exec.Command("bash", "-c", "sleep 5") }

	// First agent with no explicit name gets track-1 / Track 1.
	a1, err := s.AddAgent(Config{Rows: 24, Cols: 80}, bash)
	if err != nil {
		t.Fatalf("AddAgent agent 1: %v", err)
	}
	if a1.Name != "track-1" {
		t.Errorf("agent 1 Name = %q, want track-1", a1.Name)
	}
	if got := a1.GetDisplayName(); got != "Track 1" {
		t.Errorf("agent 1 GetDisplayName = %q, want Track 1", got)
	}

	// Second agent with no explicit name gets track-2 / Track 2.
	a2, err := s.AddAgent(Config{Rows: 24, Cols: 80}, bash)
	if err != nil {
		t.Fatalf("AddAgent agent 2: %v", err)
	}
	if a2.Name != "track-2" {
		t.Errorf("agent 2 Name = %q, want track-2", a2.Name)
	}
	if got := a2.GetDisplayName(); got != "Track 2" {
		t.Errorf("agent 2 GetDisplayName = %q, want Track 2", got)
	}

	// Explicit cfg.Name bypasses track numbering.
	a3, err := s.AddAgent(Config{Name: "my-custom-name", Rows: 24, Cols: 80}, bash)
	if err != nil {
		t.Fatalf("AddAgent agent 3: %v", err)
	}
	if a3.Name != "my-custom-name" {
		t.Errorf("agent 3 Name = %q, want my-custom-name", a3.Name)
	}
}

func TestSessionRenameBranch_FailureLeavesStateUnchanged(t *testing.T) {
	repo := setupTestRepo(t)

	wt, err := git.CreateWorktree(repo, "warm-ibis", "", "", "")
	if err != nil {
		t.Fatalf("CreateWorktree: %v", err)
	}
	s := newSession("session-1", "warm-ibis", wt)
	origBranch := wt.Branch
	origName := s.Name

	// Pin git config so rename would otherwise succeed, then sabotage via an
	// empty target which RenameBranch rejects without touching state.
	_, err = s.RenameBranch(repo, "")
	if err == nil {
		t.Fatal("expected error for empty target")
	}

	if s.Worktree.Branch != origBranch {
		t.Errorf("Worktree.Branch changed on failure: got %q, want %q", s.Worktree.Branch, origBranch)
	}
	if s.Name != origName {
		t.Errorf("Session.Name changed on failure: got %q, want %q", s.Name, origName)
	}
	if s.HasClaudeName() {
		t.Error("HasClaudeName() should stay false on failure")
	}
}

func TestUpdateBranch(t *testing.T) {
	repo := setupTestRepo(t)

	wt, err := git.CreateWorktree(repo, "warm-ibis", "", "", "")
	if err != nil {
		t.Fatalf("CreateWorktree: %v", err)
	}
	s := newSession("session-1", "warm-ibis", wt)

	if s.HasClaudeName() {
		t.Error("HasClaudeName() should be false before UpdateBranch")
	}

	s.UpdateBranch("refrain/fix-login-bug")

	if s.Branch() != "refrain/fix-login-bug" {
		t.Errorf("Branch() = %q, want %q", s.Branch(), "refrain/fix-login-bug")
	}
	if s.CurrentName() != "fix-login-bug" {
		t.Errorf("CurrentName() = %q, want %q", s.CurrentName(), "fix-login-bug")
	}
	if !s.HasClaudeName() {
		t.Error("HasClaudeName() should be true after UpdateBranch")
	}
}

func TestUpdateBranch_PreventsHaikuOverwrite(t *testing.T) {
	repo := setupTestRepo(t)

	wt, err := git.CreateWorktree(repo, "warm-ibis", "", "", "")
	if err != nil {
		t.Fatalf("CreateWorktree: %v", err)
	}
	s := newSession("session-1", "warm-ibis", wt)

	s.UpdateBranch("refrain/external-name")

	// RenameBranch must be a no-op because hasClaudeName is now true.
	actual, err := s.RenameBranch(repo, "refrain/haiku-would-set-this")
	if err != nil {
		t.Fatalf("RenameBranch: %v", err)
	}
	if actual != "refrain/external-name" {
		t.Errorf("RenameBranch should no-op, got %q, want %q", actual, "refrain/external-name")
	}
	if s.Branch() != "refrain/external-name" {
		t.Errorf("Branch() should stay %q after no-op, got %q", "refrain/external-name", s.Branch())
	}
}
