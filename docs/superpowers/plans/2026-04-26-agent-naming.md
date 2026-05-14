# Agent Naming Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace random adj-noun agent placeholder names with numbered `Track N` names, and redirect the post-Haiku display-name update from the agent to the session separator.

**Architecture:** Two focused changes: (1) `session.go` auto-names agents `track-N` / `Track N` using the existing `nextAgentNum` counter when no explicit name is given; (2) `manager.go` calls `sess.SetDisplayName(suffix)` instead of `a.SetDisplayName(suffix)` after a successful Haiku rename. Failing tests are updated first (red), then code is changed to make them pass (green).

**Tech Stack:** Go 1.25, existing `internal/agent` package — no new dependencies.

---

### Task 1: Auto-name agents as `track-N` / `Track N` in session.go

**Files:**
- Modify: `internal/agent/session.go:41-110`

- [ ] **Step 1: Write failing test**

Add to `internal/agent/session_name_test.go` (after the existing `TestSessionGetDisplayName_Fallback`):

```go
func TestAgentAutoNamedAsTrack(t *testing.T) {
	repo := setupTestRepo(t)
	wt, err := git.CreateWorktree(repo, "bohemian-rhapsody", "", "", "")
	if err != nil {
		t.Fatalf("CreateWorktree: %v", err)
	}
	s := newSession("session-1", "bohemian-rhapsody", wt)

	// First agent with no explicit name gets track-1 / Track 1.
	a1, err := s.AddAgentDefault(Config{Rows: 24, Cols: 80})
	if err != nil {
		t.Fatalf("AddAgentDefault agent 1: %v", err)
	}
	if a1.Name != "track-1" {
		t.Errorf("agent 1 Name = %q, want track-1", a1.Name)
	}
	if got := a1.GetDisplayName(); got != "Track 1" {
		t.Errorf("agent 1 GetDisplayName = %q, want Track 1", got)
	}

	// Second agent with no explicit name gets track-2 / Track 2.
	a2, err := s.AddAgentDefault(Config{Rows: 24, Cols: 80})
	if err != nil {
		t.Fatalf("AddAgentDefault agent 2: %v", err)
	}
	if a2.Name != "track-2" {
		t.Errorf("agent 2 Name = %q, want track-2", a2.Name)
	}
	if got := a2.GetDisplayName(); got != "Track 2" {
		t.Errorf("agent 2 GetDisplayName = %q, want Track 2", got)
	}

	// Explicit cfg.Name bypasses track numbering.
	a3, err := s.AddAgentDefault(Config{Name: "my-custom-name", Rows: 24, Cols: 80})
	if err != nil {
		t.Fatalf("AddAgentDefault agent 3: %v", err)
	}
	if a3.Name != "my-custom-name" {
		t.Errorf("agent 3 Name = %q, want my-custom-name", a3.Name)
	}
}
```

- [ ] **Step 2: Run test to confirm it fails**

```bash
cd /Users/devenjarvis/Code/refrain/.refrain/worktrees/pure-gold && go test ./internal/agent/ -run TestAgentAutoNamedAsTrack -v
```

Expected: FAIL — `agent 1 Name = "brave-falcon"` or similar random name.

- [ ] **Step 3: Update `AddAgent`, `AddAgentDefault`, and `AddAgentResumed` in session.go**

In `internal/agent/session.go`, replace the three methods with the following (the only change is moving `nextAgentNum++` before the name check, deriving the name from the counter, and setting the display name after creation):

Replace `AddAgent` (lines 41-62):
```go
func (s *Session) AddAgent(cfg Config, cmd *exec.Cmd) (*Agent, error) {
	s.mu.Lock()
	s.nextAgentNum++
	num := s.nextAgentNum
	autoNamed := cfg.Name == ""
	if autoNamed {
		cfg.Name = fmt.Sprintf("track-%d", num)
	}
	id := fmt.Sprintf("%s-agent-%d", s.ID, num)
	s.mu.Unlock()

	a, err := newAgentWithCommand(id, cfg, s.Worktree.Path, cmd)
	if err != nil {
		return nil, err
	}

	if autoNamed && !a.HasDisplayName() {
		a.SetDisplayName(fmt.Sprintf("Track %d", num))
	}

	s.mu.Lock()
	s.agents[id] = a
	s.mu.Unlock()

	return a, nil
}
```

Replace `AddAgentDefault` (lines 64-86):
```go
func (s *Session) AddAgentDefault(cfg Config) (*Agent, error) {
	s.mu.Lock()
	s.nextAgentNum++
	num := s.nextAgentNum
	autoNamed := cfg.Name == ""
	if autoNamed {
		cfg.Name = fmt.Sprintf("track-%d", num)
	}
	id := fmt.Sprintf("%s-agent-%d", s.ID, num)
	socketPath := s.hookSocketPath
	s.mu.Unlock()

	a, err := newAgent(id, cfg, s.Worktree.Path, socketPath)
	if err != nil {
		return nil, err
	}

	if autoNamed && !a.HasDisplayName() {
		a.SetDisplayName(fmt.Sprintf("Track %d", num))
	}

	s.mu.Lock()
	s.agents[id] = a
	s.mu.Unlock()

	return a, nil
}
```

Replace `AddAgentResumed` (lines 88-110):
```go
func (s *Session) AddAgentResumed(cfg Config, claudeSessionID string) (*Agent, error) {
	s.mu.Lock()
	s.nextAgentNum++
	num := s.nextAgentNum
	autoNamed := cfg.Name == ""
	if autoNamed {
		cfg.Name = fmt.Sprintf("track-%d", num)
	}
	id := fmt.Sprintf("%s-agent-%d", s.ID, num)
	socketPath := s.hookSocketPath
	s.mu.Unlock()

	a, err := newResumedAgent(id, cfg, s.Worktree.Path, claudeSessionID, socketPath)
	if err != nil {
		return nil, err
	}

	if autoNamed && !a.HasDisplayName() {
		a.SetDisplayName(fmt.Sprintf("Track %d", num))
	}

	s.mu.Lock()
	s.agents[id] = a
	s.mu.Unlock()

	return a, nil
}
```

- [ ] **Step 4: Run the new test and all agent tests**

```bash
cd /Users/devenjarvis/Code/refrain/.refrain/worktrees/pure-gold && go test -race ./internal/agent/ -v -run TestAgentAutoNamedAsTrack
```
Expected: PASS

```bash
cd /Users/devenjarvis/Code/refrain/.refrain/worktrees/pure-gold && go test -race ./internal/agent/
```
Expected: all PASS (existing tests pass explicit cfg.Names so are unaffected by this change)

- [ ] **Step 5: Commit**

```bash
cd /Users/devenjarvis/Code/refrain/.refrain/worktrees/pure-gold && git add internal/agent/session.go internal/agent/session_name_test.go && git commit -m "feat: auto-name agents track-1, track-2 instead of random adj-noun"
```

---

### Task 2: Redirect post-Haiku display name from agent to session in manager.go

**Files:**
- Modify: `internal/agent/manager.go:259-269`
- Modify: `internal/agent/hook_integration_test.go:590-705`

- [ ] **Step 1: Update integration tests to new expected behavior (they will fail)**

In `internal/agent/hook_integration_test.go`, add the `waitForSessionDisplayName` helper after the existing `waitForAgentDisplayName` helper (around line 915):

```go
// waitForSessionDisplayName polls until Session.GetDisplayName() returns want or
// the deadline elapses. Use this after a successful Haiku rename to confirm the
// session separator updated (the display-name write happens inside the rename
// goroutine, after the branch rename itself).
func waitForSessionDisplayName(t *testing.T, sess *Session, want string, d time.Duration) string {
	t.Helper()
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if got := sess.GetDisplayName(); got == want {
			return got
		}
		time.Sleep(20 * time.Millisecond)
	}
	return sess.GetDisplayName()
}
```

Then update `TestManagerRenamesOnFirstUserPromptSubmit` (starting at line 590). Replace the display-name assertions block (lines 620-640):

**Old block:**
```go
	const want = "investigate-flaky-checkout-test"
	// The agent display-name update happens in the rename goroutine after
	// the namer returns; it's the last write before EventBranchRenamed fires
	// so wait on it.
	if got := waitForAgentDisplayName(t, ag, want, 2*time.Second); got != want {
		t.Fatalf("agent display name: got %q, want %q", got, want)
	}
	if got := sess.Branch(); got != "refrain/"+want {
		t.Errorf("branch: got %q, want refrain/%s", got, want)
	}
	// Session display name stays pinned to the worktree slug — only agents
	// pick up the task-derived name.
	if sess.HasDisplayName() {
		t.Errorf("session display name should not be set after rename, got %q", sess.GetDisplayName())
	}
	if got := sess.CurrentName(); got != originalSessionName {
		t.Errorf("Session.Name: got %q, want unchanged %q", got, originalSessionName)
	}
	if got := sess.GetDisplayName(); got != originalSessionName {
		t.Errorf("session display name: got %q, want %q (worktree slug)", got, originalSessionName)
	}
```

**New block:**
```go
	const want = "investigate-flaky-checkout-test"
	// The session display-name update happens in the rename goroutine after
	// the namer returns; it's the last write before EventBranchRenamed fires
	// so wait on it.
	if got := waitForSessionDisplayName(t, sess, want, 2*time.Second); got != want {
		t.Fatalf("session display name: got %q, want %q", got, want)
	}
	if got := sess.Branch(); got != "refrain/"+want {
		t.Errorf("branch: got %q, want refrain/%s", got, want)
	}
	// Agent display name stays at its original value — agents keep their
	// track identities; only the session separator picks up the task name.
	if got := ag.GetDisplayName(); got != "rename-first" {
		t.Errorf("agent display name changed: got %q, want rename-first", got)
	}
	if got := sess.CurrentName(); got != originalSessionName {
		t.Errorf("Session.Name: got %q, want unchanged %q", got, originalSessionName)
	}
```

Then update `TestManagerSecondUserPromptSubmitDoesNotRename` (starting at line 646). Replace the assertions after the first `sendUserPromptSubmit` call and the second-prompt block.

**Old first-rename assertions (lines 669-675):**
```go
	const want = "first-prompt-wins"
	if got := waitForAgentDisplayName(t, ag, want, 2*time.Second); got != want {
		t.Fatalf("after first prompt: agent = %q, want %q", got, want)
	}
	if got := sess.Branch(); got != "refrain/"+want {
		t.Fatalf("after first prompt: branch = %q, want refrain/%s", got, want)
	}
```

**New first-rename assertions:**
```go
	const want = "first-prompt-wins"
	if got := waitForSessionDisplayName(t, sess, want, 2*time.Second); got != want {
		t.Fatalf("after first prompt: session display name = %q, want %q", got, want)
	}
	if got := sess.Branch(); got != "refrain/"+want {
		t.Fatalf("after first prompt: branch = %q, want refrain/%s", got, want)
	}
	// Agent display name must not have changed.
	if got := ag.GetDisplayName(); got != "rename-second" {
		t.Errorf("agent display name changed: got %q, want rename-second", got)
	}
```

**Old second-prompt assertions (lines 694-704):**
```go
	if got := ag.GetDisplayName(); got != want {
		t.Errorf("agent display name changed: got %q, want %q", got, want)
	}
	// Session display name was never set by the rename, so it falls back to
	// CurrentName() — verify that it didn't get overwritten by a stray write.
	if sess.HasDisplayName() {
		t.Errorf("session display name should remain unset, got %q", sess.GetDisplayName())
	}
```

**New second-prompt assertions:**
```go
	// Agent display name still unchanged after second prompt.
	if got := ag.GetDisplayName(); got != "rename-second" {
		t.Errorf("agent display name changed: got %q, want rename-second", got)
	}
	// Session display name still "first-prompt-wins" — second prompt is a no-op.
	if got := sess.GetDisplayName(); got != want {
		t.Errorf("session display name changed on second prompt: got %q, want %q", got, want)
	}
```

- [ ] **Step 2: Run tests to confirm they fail**

```bash
cd /Users/devenjarvis/Code/refrain/.refrain/worktrees/pure-gold && go test -race ./internal/agent/ -run "TestManagerRenamesOnFirstUserPromptSubmit|TestManagerSecondUserPromptSubmitDoesNotRename" -v
```

Expected: FAIL on display-name assertions (session display name not set, agent still getting the rename).

- [ ] **Step 3: Update manager.go to set session display name instead of agent**

In `internal/agent/manager.go`, replace lines ~259-269 (the block after `m.renameSessionBranch`):

**Old:**
```go
		// The session display name intentionally does NOT update — it stays
		// pinned to the worktree's song slug so the sidebar separator keeps
		// reading like a track on the setlist. Only the agent picks up the
		// task-derived name. Both writes must happen before EventBranchRenamed
		// fires so subscribers (PR scheduler, TUI) see a coherent snapshot.
		a.SetDisplayName(suffix)
		m.emitBranchRenamed(sess, a, newBranch)
```

**New:**
```go
		// The session display name updates to the Haiku-derived task name so
		// the sidebar separator shows what the session is working on. Agents
		// keep their stable track identities (Track 1, Track 2, ...) and are
		// never renamed here. Both writes must happen before EventBranchRenamed
		// fires so subscribers (PR scheduler, TUI) see a coherent snapshot.
		sess.SetDisplayName(suffix)
		m.emitBranchRenamed(sess, a, newBranch)
```

- [ ] **Step 4: Run the targeted tests**

```bash
cd /Users/devenjarvis/Code/refrain/.refrain/worktrees/pure-gold && go test -race ./internal/agent/ -run "TestManagerRenamesOnFirstUserPromptSubmit|TestManagerSecondUserPromptSubmitDoesNotRename" -v
```

Expected: PASS

- [ ] **Step 5: Run the full agent test suite**

```bash
cd /Users/devenjarvis/Code/refrain/.refrain/worktrees/pure-gold && go test -race ./internal/agent/
```

Expected: all PASS

- [ ] **Step 6: Commit**

```bash
cd /Users/devenjarvis/Code/refrain/.refrain/worktrees/pure-gold && git add internal/agent/manager.go internal/agent/hook_integration_test.go && git commit -m "feat: rename session display name (not agent) after Haiku rename"
```

---

### Task 3: Update CLAUDE.md to reflect new naming behavior

**Files:**
- Modify: `CLAUDE.md`

- [ ] **Step 1: Update the branch naming paragraph**

In `CLAUDE.md`, find the sentence inside the **Branch naming** bullet that reads:

```
The session display name intentionally does NOT update — it stays pinned to the worktree's song slug so the sidebar separator keeps reading like a track on the setlist. Only the agent picks up the Haiku-derived name. Both writes must happen before EventBranchRenamed fires so subscribers (PR scheduler, TUI) see a coherent snapshot.
```

Replace with:

```
On success, the session display name (sidebar separator) updates to the Haiku-derived suffix so users can see what the session is working on; agents keep their stable `Track N` identities and are never renamed here. Both writes must happen before EventBranchRenamed fires so subscribers (PR scheduler, TUI) see a coherent snapshot.
```

Also find the sentence:

```
Sessions started on an existing branch via `CreateSessionOnBranch*` set `hasClaudeName=true` at creation so they keep their original branch.
```

This is fine as-is. No change needed.

- [ ] **Step 2: Run all tests one final time**

```bash
cd /Users/devenjarvis/Code/refrain/.refrain/worktrees/pure-gold && go test -race ./...
```

Expected: all PASS

- [ ] **Step 3: Commit**

```bash
cd /Users/devenjarvis/Code/refrain/.refrain/worktrees/pure-gold && git add CLAUDE.md && git commit -m "docs: update branch naming behavior in CLAUDE.md"
```
