package tui

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/devenjarvis/refrain/internal/agent"
	"github.com/devenjarvis/refrain/internal/git"
)

func TestRunValidationCheckCmd_PassingCommand(t *testing.T) {
	worktreeDir := t.TempDir()
	cmd := runValidationCheckCmd("s1", "/repo", worktreeDir, 0, 1, "echo hello")
	if cmd == nil {
		t.Fatal("runValidationCheckCmd returned nil cmd")
	}
	msg, ok := cmd().(validationCheckResultMsg)
	if !ok {
		t.Fatalf("cmd() returned %T, want validationCheckResultMsg", cmd())
	}
	if msg.runID != 1 {
		t.Errorf("runID = %d, want 1", msg.runID)
	}
	if msg.sessionID != "s1" {
		t.Errorf("sessionID = %q, want s1", msg.sessionID)
	}
	if msg.checkIndex != 0 {
		t.Errorf("checkIndex = %d, want 0", msg.checkIndex)
	}
	if msg.state != checkPassed {
		t.Errorf("state = %v, want checkPassed", msg.state)
	}
	if msg.exitCode != 0 {
		t.Errorf("exitCode = %d, want 0", msg.exitCode)
	}
	if !strings.Contains(msg.output, "hello") {
		t.Errorf("output = %q, want it to contain 'hello'", msg.output)
	}
}

func TestRunValidationCheckCmd_FailingCommand(t *testing.T) {
	worktreeDir := t.TempDir()
	cmd := runValidationCheckCmd("s1", "/repo", worktreeDir, 1, 2, "exit 42")
	if cmd == nil {
		t.Fatal("runValidationCheckCmd returned nil cmd")
	}
	msg, ok := cmd().(validationCheckResultMsg)
	if !ok {
		t.Fatalf("cmd() returned %T, want validationCheckResultMsg", cmd())
	}
	if msg.runID != 2 {
		t.Errorf("runID = %d, want 2", msg.runID)
	}
	if msg.state != checkFailed {
		t.Errorf("state = %v, want checkFailed", msg.state)
	}
	if msg.exitCode != 42 {
		t.Errorf("exitCode = %d, want 42", msg.exitCode)
	}
}

// reviewGit runs a git command in dir, failing the test on error.
func reviewGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(
		os.Environ(),
		"GIT_AUTHOR_NAME=Test", "GIT_AUTHOR_EMAIL=test@test.com",
		"GIT_COMMITTER_NAME=Test", "GIT_COMMITTER_EMAIL=test@test.com",
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v failed: %v\n%s", args, err, out)
	}
}

// initReviewRepo creates a temp repo on main with one base commit and returns
// its path.
func initReviewRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	reviewGit(t, dir, "init")
	reviewGit(t, dir, "config", "user.email", "test@test.com")
	reviewGit(t, dir, "config", "user.name", "Test")
	reviewGit(t, dir, "config", "commit.gpgsign", "false")
	reviewGit(t, dir, "checkout", "-b", "main")
	if err := os.WriteFile(filepath.Join(dir, "base.txt"), []byte("base\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	reviewGit(t, dir, "add", "base.txt")
	reviewGit(t, dir, "commit", "-m", "base")
	return dir
}

// reviewSessionAt builds a session bound to dir with main as the base branch,
// mirroring a checkout session or an attached branch.
func reviewSessionAt(t *testing.T, dir string) *agent.Session {
	t.Helper()
	sess := agent.NewSessionForTestWithPath("sess-1", "review-me", dir)
	sess.Worktree.BaseBranch = "main"
	return sess
}

// fetchReviewEntry runs fetchReviewDiffCmd synchronously and returns the entry.
func fetchReviewEntry(t *testing.T, sess *agent.Session, dir string) *reviewDiffEntry {
	t.Helper()
	app := NewApp()
	msg, ok := app.fetchReviewDiffCmd(sess, dir)().(reviewDiffMsg)
	if !ok {
		t.Fatal("fetchReviewDiffCmd did not return a reviewDiffMsg")
	}
	if msg.err != nil {
		t.Fatalf("fetchReviewDiffCmd error: %v", msg.err)
	}
	return msg.entry
}

// TestFetchReviewDiffCmd_PlanMode verifies mode 1 of the ledger fallback
// chain: a session with a plan gets plan-task cards with Plan-Task trailer
// grouping, exactly as before the generalization.
func TestFetchReviewDiffCmd_PlanMode(t *testing.T) {
	dir := initReviewRepo(t)
	reviewGit(t, dir, "checkout", "-b", "work")
	if err := os.WriteFile(filepath.Join(dir, "feature.txt"), []byte("hi\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	reviewGit(t, dir, "add", "feature.txt")
	reviewGit(t, dir, "commit", "-m", "feat: one", "-m", "Plan-Task: 1")

	sess := reviewSessionAt(t, dir)
	if err := sess.WritePlan("# Goal\nDo it.\n\n## Tasks\n- [ ] Task one\n- [ ] Task two\n"); err != nil {
		t.Fatalf("WritePlan: %v", err)
	}

	entry := fetchReviewEntry(t, sess, dir)
	if entry.mode != reviewModePlan {
		t.Fatalf("mode = %d, want reviewModePlan", entry.mode)
	}
	cards := entry.ledgerCards()
	if len(cards) != 2 {
		t.Fatalf("expected 2 plan cards, got %d: %+v", len(cards), cards)
	}
	if cards[0].title != "Task one" || cards[1].title != "Task two" {
		t.Errorf("unexpected card titles: %+v", cards)
	}
	if rec := entry.verdicts[1]; rec == nil || rec.state != verdictPending {
		t.Errorf("task 1 (has commit) must be verdictPending, got %+v", entry.verdicts[1])
	}
	if rec := entry.verdicts[2]; rec == nil || rec.state != verdictNoDiff {
		t.Errorf("task 2 (no commit) must be verdictNoDiff, got %+v", entry.verdicts[2])
	}
}

// TestFetchReviewDiffCmd_CommitMode verifies mode 2: a plan-less branch with
// commits gets one card per commit, oldest first, titled by commit subject —
// this is what makes review work on attached foreign branches.
func TestFetchReviewDiffCmd_CommitMode(t *testing.T) {
	dir := initReviewRepo(t)
	reviewGit(t, dir, "checkout", "-b", "work")
	for i, name := range []string{"one", "two"} {
		f := filepath.Join(dir, name+".txt")
		if err := os.WriteFile(f, []byte(name+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		reviewGit(t, dir, "add", name+".txt")
		reviewGit(t, dir, "commit", "-m", "feat: commit "+name)
		_ = i
	}

	sess := reviewSessionAt(t, dir)
	entry := fetchReviewEntry(t, sess, dir)
	if entry.mode != reviewModeCommits {
		t.Fatalf("mode = %d, want reviewModeCommits", entry.mode)
	}
	cards := entry.ledgerCards()
	if len(cards) != 2 {
		t.Fatalf("expected 2 commit cards, got %d: %+v", len(cards), cards)
	}
	if cards[0].title != "feat: commit one" || cards[1].title != "feat: commit two" {
		t.Errorf("cards must be oldest-first, titled by subject: %+v", cards)
	}
	for _, c := range cards {
		if len(c.label) != 7 {
			t.Errorf("card label must be a 7-char short hash, got %q", c.label)
		}
		rec := entry.verdicts[c.index]
		if rec == nil || rec.state != verdictPending {
			t.Errorf("commit card %d must be verdictPending, got %+v", c.index, rec)
		}
		group := entry.groupByCardIndex(c.index)
		if group == nil || group.rawDiff == "" {
			t.Errorf("commit card %d must carry a raw diff", c.index)
		}
	}
	// Card 1's diff must contain only the first commit's file.
	g1 := entry.groupByCardIndex(cards[0].index)
	if !strings.Contains(g1.rawDiff, "one.txt") || strings.Contains(g1.rawDiff, "two.txt") {
		t.Errorf("per-commit diff must be scoped to its commit:\n%s", g1.rawDiff)
	}
}

// TestFetchReviewDiffCmd_CommitModeCapped verifies the ledger cap: a branch
// with more than maxCommitCards commits gets an aggregate "Earlier changes"
// card (AI verdict skipped) followed by per-commit cards for the recent tail.
func TestFetchReviewDiffCmd_CommitModeCapped(t *testing.T) {
	dir := initReviewRepo(t)
	reviewGit(t, dir, "checkout", "-b", "work")
	total := maxCommitCards + 3
	for i := 0; i < total; i++ {
		f := filepath.Join(dir, "file.txt")
		if err := os.WriteFile(f, []byte(strings.Repeat("x", i+1)+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		reviewGit(t, dir, "add", "file.txt")
		reviewGit(t, dir, "commit", "-m", "feat: change "+strings.Repeat("i", i+1))
	}

	sess := reviewSessionAt(t, dir)
	entry := fetchReviewEntry(t, sess, dir)
	if entry.mode != reviewModeCommits {
		t.Fatalf("mode = %d, want reviewModeCommits", entry.mode)
	}
	cards := entry.ledgerCards()
	if len(cards) != maxCommitCards+1 {
		t.Fatalf("expected %d cards (aggregate + %d recent), got %d", maxCommitCards+1, maxCommitCards, len(cards))
	}
	agg := cards[0]
	if !agg.aggregate {
		t.Error("first card must be the aggregate rollup")
	}
	if !strings.Contains(agg.title, "Earlier changes (3 commits)") {
		t.Errorf("aggregate title = %q, want 'Earlier changes (3 commits)'", agg.title)
	}
	if rec := entry.verdicts[agg.index]; rec == nil || rec.state != verdictSkipped {
		t.Errorf("aggregate card must be verdictSkipped, got %+v", entry.verdicts[agg.index])
	}
	if g := entry.groupByCardIndex(agg.index); g == nil || g.rawDiff == "" || len(g.commits) != 3 {
		t.Errorf("aggregate card must carry the rolled-up diff for the 3 earlier commits")
	}
	for _, c := range cards[1:] {
		if c.aggregate {
			t.Errorf("only the first card may be the aggregate: %+v", c)
		}
		if rec := entry.verdicts[c.index]; rec == nil || rec.state != verdictPending {
			t.Errorf("recent commit card %d must be verdictPending", c.index)
		}
	}
}

// TestFetchReviewDiffCmd_FileMode verifies mode 3: no plan and no commits
// (a checkout session with uncommitted work) yields per-file cards with AI
// verdicts disabled.
func TestFetchReviewDiffCmd_FileMode(t *testing.T) {
	dir := initReviewRepo(t)
	// Uncommitted change to a tracked file, plus a staged new file.
	if err := os.WriteFile(filepath.Join(dir, "base.txt"), []byte("base\nchanged\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "new.txt"), []byte("new\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	reviewGit(t, dir, "add", "new.txt")

	sess := reviewSessionAt(t, dir)
	entry := fetchReviewEntry(t, sess, dir)
	if entry.mode != reviewModeFiles {
		t.Fatalf("mode = %d, want reviewModeFiles", entry.mode)
	}
	cards := entry.ledgerCards()
	if len(cards) != 2 {
		t.Fatalf("expected 2 file cards, got %d: %+v", len(cards), cards)
	}
	// Files are path-sorted: base.txt then new.txt.
	if cards[0].title != "base.txt" || cards[1].title != "new.txt" {
		t.Errorf("file cards must be path-sorted with the path as title: %+v", cards)
	}
	if cards[0].label != "[M]" || cards[1].label != "[A]" {
		t.Errorf("file card labels must carry git status: %+v", cards)
	}
	for _, c := range cards {
		rec := entry.verdicts[c.index]
		if rec == nil || rec.state != verdictSkipped {
			t.Errorf("file card %q must be verdictSkipped (manual review), got %+v", c.title, rec)
		}
	}
	// Each card's diff is scoped to its file.
	if g := entry.groupByCardIndex(cards[0].index); g == nil || !strings.Contains(g.rawDiff, "+changed") {
		t.Error("base.txt card must carry its own file diff")
	}
	if g := entry.groupByCardIndex(cards[1].index); g == nil || !strings.Contains(g.rawDiff, "+new") {
		t.Error("new.txt card must carry its own file diff")
	}
}

// TestFetchReviewDiffCmd_CleanTree verifies the degenerate case: no plan, no
// commits, no changes — file mode with zero cards.
func TestFetchReviewDiffCmd_CleanTree(t *testing.T) {
	dir := initReviewRepo(t)
	sess := reviewSessionAt(t, dir)
	entry := fetchReviewEntry(t, sess, dir)
	if entry.mode != reviewModeFiles {
		t.Fatalf("mode = %d, want reviewModeFiles", entry.mode)
	}
	if n := reviewTaskCount(entry); n != 0 {
		t.Errorf("clean tree must yield 0 ledger rows, got %d", n)
	}
}

// TestHandleReviewDiff_FileModeNoDispatch verifies that a file-mode entry
// never dispatches the AI reviewer (§4.6: verdicts disabled in mode 3) and
// that its skipped verdicts are not downgraded to errors.
func TestHandleReviewDiff_FileModeNoDispatch(t *testing.T) {
	dir := t.TempDir()
	sess := agent.NewSessionForTest("sess-1", "checkout")
	reviewer := &capturingReviewer{}
	mgr := newFakeManager(dir, sess)
	mgr.reviewer = reviewer

	app := NewApp()
	app.managers[dir] = mgr

	entry := &reviewDiffEntry{
		mode: reviewModeFiles,
		groups: []taskReviewGroup{
			{taskIndex: 1, files: []git.FileStat{{Path: "a.go", Status: "M"}}, rawDiff: "diff --git a/a.go b/a.go\n"},
		},
		verdicts: map[int]*taskVerdictRecord{1: {state: verdictSkipped}},
	}

	_, cmd := app.handleReviewDiff(reviewDiffMsg{sessionID: "sess-1", repoPath: dir, entry: entry})
	if cmd != nil {
		t.Fatal("file-mode entry must not dispatch reviewer cmds")
	}
	if rec := entry.verdicts[1]; rec.state != verdictSkipped {
		t.Errorf("skipped verdict must stay skipped, got state %d", rec.state)
	}
	if reviewer.captured.TaskText != "" {
		t.Errorf("reviewer must not be called, but got request %+v", reviewer.captured)
	}
}

// TestHandleReviewDiff_CommitModeDispatchesSubject verifies that commit-mode
// cards dispatch the AI reviewer with the commit subject standing in for the
// task text, while the aggregate rollup card is skipped.
func TestHandleReviewDiff_CommitModeDispatchesSubject(t *testing.T) {
	dir := t.TempDir()
	sess := agent.NewSessionForTest("sess-1", "foreign-branch")
	sess.SetOriginalPrompt("review this branch")
	reviewer := &capturingReviewer{}
	mgr := newFakeManager(dir, sess)
	mgr.reviewer = reviewer

	app := NewApp()
	app.managers[dir] = mgr

	entry := &reviewDiffEntry{
		mode: reviewModeCommits,
		groups: []taskReviewGroup{
			{taskIndex: 1, commits: []git.Commit{{Hash: "aaa"}, {Hash: "bbb"}}, rawDiff: "agg diff"},
			{taskIndex: 2, commits: []git.Commit{{Hash: "cccdddd1234", Subject: "fix: tighten auth", Body: "details"}}, rawDiff: "commit diff"},
		},
		verdicts: map[int]*taskVerdictRecord{
			1: {state: verdictSkipped},
			2: {state: verdictPending},
		},
	}

	_, cmd := app.handleReviewDiff(reviewDiffMsg{sessionID: "sess-1", repoPath: dir, entry: entry})
	if cmd == nil {
		t.Fatal("commit-mode entry with a pending card must dispatch a reviewer cmd")
	}
	msg := cmd()
	verdictMsg, ok := msg.(reviewVerdictMsg)
	if !ok {
		t.Fatalf("cmd returned %T, want reviewVerdictMsg (exactly one dispatch)", msg)
	}
	if verdictMsg.taskIndex != 2 {
		t.Errorf("dispatched taskIndex = %d, want 2", verdictMsg.taskIndex)
	}
	if reviewer.captured.TaskText != "fix: tighten auth" {
		t.Errorf("reviewer TaskText = %q, want the commit subject", reviewer.captured.TaskText)
	}
	if reviewer.captured.TaskDetail != "details" {
		t.Errorf("reviewer TaskDetail = %q, want the commit body", reviewer.captured.TaskDetail)
	}
	if rec := entry.verdicts[1]; rec.state != verdictSkipped {
		t.Errorf("aggregate card must stay verdictSkipped, got state %d", rec.state)
	}
}

// TestBuildReviewReworkPrompt_CommitMode verifies commit-mode prompts
// reference commits by hash + subject and never mention the plan contract.
func TestBuildReviewReworkPrompt_CommitMode(t *testing.T) {
	entry := &reviewDiffEntry{
		mode: reviewModeCommits,
		groups: []taskReviewGroup{
			{taskIndex: 1, commits: []git.Commit{{Hash: "aaa"}, {Hash: "bbb"}}},
			{taskIndex: 2, commits: []git.Commit{{Hash: "abcdef01234", Subject: "feat: add cache"}}},
		},
		verdicts: map[int]*taskVerdictRecord{
			1: {state: verdictSkipped, userFlagged: true},
			2: {state: verdictDone, verdict: agent.ReviewVerdict{Kind: agent.VerdictConcerns, Rationale: "cache never invalidated"}},
		},
	}
	prompt := buildReviewReworkPrompt(entry)
	if !strings.Contains(prompt, "The following commits need rework") {
		t.Errorf("prompt missing commit-mode intro: %q", prompt)
	}
	if !strings.Contains(prompt, "## Commit abcdef0: feat: add cache") {
		t.Errorf("prompt missing commit heading: %q", prompt)
	}
	if !strings.Contains(prompt, "## Earlier changes (2 commits)") {
		t.Errorf("prompt missing aggregate heading: %q", prompt)
	}
	if !strings.Contains(prompt, "Rationale: cache never invalidated") {
		t.Errorf("prompt missing rationale: %q", prompt)
	}
	if strings.Contains(prompt, "Plan-Task") || strings.Contains(prompt, "plan.md") {
		t.Errorf("commit-mode prompt must not reference the plan contract: %q", prompt)
	}
}

// TestBuildReviewReworkPrompt_FileMode verifies file-mode prompts list flagged
// files and skip the plan contract.
func TestBuildReviewReworkPrompt_FileMode(t *testing.T) {
	entry := &reviewDiffEntry{
		mode: reviewModeFiles,
		groups: []taskReviewGroup{
			{taskIndex: 1, files: []git.FileStat{{Path: "auth.go", Status: "M"}}},
			{taskIndex: 2, files: []git.FileStat{{Path: "auth_test.go", Status: "M"}}},
		},
		verdicts: map[int]*taskVerdictRecord{
			1: {state: verdictSkipped, userFlagged: true},
			2: {state: verdictSkipped},
		},
	}
	prompt := buildReviewReworkPrompt(entry)
	if !strings.Contains(prompt, "The following files need rework") {
		t.Errorf("prompt missing file-mode intro: %q", prompt)
	}
	if !strings.Contains(prompt, "## File: auth.go") {
		t.Errorf("prompt missing file heading: %q", prompt)
	}
	if strings.Contains(prompt, "auth_test.go") {
		t.Errorf("unflagged file must not appear in the prompt: %q", prompt)
	}
	if !strings.Contains(prompt, "Flagged by you: yes") {
		t.Errorf("prompt missing flag note: %q", prompt)
	}
	if strings.Contains(prompt, "Plan-Task") || strings.Contains(prompt, "plan.md") {
		t.Errorf("file-mode prompt must not reference the plan contract: %q", prompt)
	}
}

// TestBuildReviewReworkPrompt_InstructsTrailer verifies the rework prompt uses
// Plan-Task trailers and not the legacy subject-prefix format.
func TestBuildReviewReworkPrompt_InstructsTrailer(t *testing.T) {
	entry := &reviewDiffEntry{
		tasks: []agent.PlanTask{{Index: 2, Text: "Add widget"}},
		verdicts: map[int]*taskVerdictRecord{
			2: {
				state:       verdictDone,
				verdict:     agent.ReviewVerdict{Kind: agent.VerdictFail, Rationale: "missing validation"},
				userFlagged: true,
			},
		},
	}
	prompt := buildReviewReworkPrompt(entry)

	if !strings.Contains(prompt, "Plan-Task: 2") {
		t.Errorf("rework prompt must instruct Plan-Task: 2 trailer, got: %q", prompt)
	}
	if strings.Contains(prompt, "[task 2]") {
		t.Errorf("rework prompt must NOT contain legacy [task 2] subject prefix, got: %q", prompt)
	}
	if !strings.Contains(prompt, "Flagged by you: yes") {
		t.Errorf("rework prompt must surface 'Flagged by you: yes' for flagged task, got: %q", prompt)
	}
	// The "Other changes" branch must still be explained in the trailing instruction.
	if !strings.Contains(prompt, "Other changes") {
		t.Errorf("rework prompt must still mention 'Other changes', got: %q", prompt)
	}
}
