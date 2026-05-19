package tui

import (
	"context"
	"fmt"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/devenjarvis/refrain/internal/agent"
	"github.com/devenjarvis/refrain/internal/git"
	"github.com/devenjarvis/refrain/internal/github"
)

func (a App) handlePRDraftReady(msg prDraftReadyMsg) (tea.Model, tea.Cmd) {
	a.prDraftInFlight = false
	a.prDraftSessionID = ""
	if msg.err != nil {
		a.setError("PR draft failed: " + msg.err.Error())
		return a, nil
	}
	// Store context for the CreatePR call that follows confirmation.
	a.prModalSessionID = msg.sessionID
	a.prModalRepoPath = msg.repoPath
	a.prModalOwner = msg.owner
	a.prModalRepo = msg.repo
	a.prModalHead = msg.head
	a.prModalBase = msg.base
	a.prModalTransitionShipping = msg.transitionShipping
	resolved := a.resolvedCache[msg.repoPath]
	cmd := a.prComposeModal.Open(msg.title, msg.body, resolved.PRDraftByDefault)
	return a, cmd
}

func (a App) handlePRCreated(msg prCreatedMsg) (tea.Model, tea.Cmd) {
	if msg.err != nil {
		a.setError("create PR failed: " + msg.err.Error())
		return a, nil
	}
	repoPath := msg.repoPath
	if repoPath == "" {
		repoPath = a.repoPathForSession(msg.sessionID)
	}
	if repoPath == "" {
		a.setError("create PR succeeded but session repo unknown; refresh to see it")
		return a, nil
	}
	key := cacheKey(repoPath, msg.sessionID)
	a.prCache[key] = &prCacheEntry{pr: msg.pr}
	if msg.transitionShipping {
		sess := a.sessionByIDInRepo(repoPath, msg.sessionID)
		if sess != nil {
			sess.SetLifecyclePhase(agent.LifecycleShipping)
			if rp := a.modals.Review(); rp != nil && rp.SessionID() == msg.sessionID {
				a.closeModal()
			}
		}
	}
	a.updateDashboardPRCache()
	// Re-arm a burst poll so the new PR is discovered quickly.
	if ps := a.prPollStates[key]; ps != nil {
		ps.burstUntil = time.Now().Add(PRPollBurstAfterCreate)
	}
	// Auto-open in browser if configured.
	resolved := a.resolvedCache[repoPath]
	if resolved.AutoOpenPRInBrowser && msg.pr != nil && msg.pr.URL != "" {
		if err := openURL(msg.pr.URL); err != nil {
			a.setError(err.Error())
		}
	}
	return a, nil
}

func (a App) handlePRPoll(msg prPollMsg) (tea.Model, tea.Cmd) {
	a.prPollsInFlight--
	if a.prPollsInFlight < 0 {
		a.prPollsInFlight = 0
	}
	repoPath := msg.repoPath
	if repoPath == "" {
		repoPath = a.repoPathForSession(msg.sessionID)
	}
	// Without a repoPath we can't safely key the cache or look up the session
	// — drop the result rather than risk a cross-repo clobber.
	if repoPath == "" {
		return a, nil
	}
	key := cacheKey(repoPath, msg.sessionID)
	ps := a.prPollStates[key]
	if ps != nil {
		ps.inFlight = false
	}
	// Fetch failed: preserve cache so a transient error doesn't blank the UI.
	if msg.err != nil {
		return a, nil
	}
	// Lookup succeeded with no PR. Apply a 2-consecutive-nil grace period
	// before evicting the cache: a single nil is common during the rename
	// gap (branch pushed under old name, remote SHA not yet updated) or a
	// rapid force-push window. Two in a row means the PR is genuinely gone.
	if msg.pr == nil {
		if _, had := a.prCache[key]; had {
			// ps is always non-nil here: pollAllSessions initialises it before
			// dispatching a poll, and prPollMsg can only arrive after dispatch.
			// The nil guard is defensive; if ps were nil we skip the grace period
			// and evict immediately rather than dereference.
			if ps != nil {
				ps.consecutiveNilPolls++
				if ps.consecutiveNilPolls < 2 {
					return a, nil
				}
			}
			delete(a.prCache, key)
			if ps != nil {
				ps.lastCheckState = ""
				ps.consecutiveNilPolls = 0
			}
			a.updateDashboardPRCache()
		}
		return a, nil
	}
	// Successful poll: reset the nil counter.
	if ps != nil {
		ps.consecutiveNilPolls = 0
	}
	a.prCache[key] = &prCacheEntry{
		pr:      msg.pr,
		checks:  msg.checks,
		reviews: msg.reviews,
		threads: msg.threads,
		stack:   msg.stack,
	}
	// Arm a short burst so the unknown → known transition resolves promptly.
	// Use max semantics to preserve a longer push burst that may already be active.
	if ps != nil && (msg.pr.MergeableState == "" || msg.pr.MergeableState == "unknown") {
		if newBurst := time.Now().Add(PRPollBurstUnknownMergeable); newBurst.After(ps.burstUntil) {
			ps.burstUntil = newBurst
		}
	}
	// Auto-promote to Shipping when an open PR is discovered externally.
	if msg.pr.State == "open" {
		sess := a.sessionByIDInRepo(repoPath, msg.sessionID)
		if sess != nil {
			switch sess.LifecyclePhase() {
			case agent.LifecycleInProgress, agent.LifecycleReadyForReview, agent.LifecycleInReview:
				sess.SetLifecyclePhase(agent.LifecycleShipping)
				if rp := a.modals.Review(); rp != nil && rp.SessionID() == msg.sessionID {
					a.closeModal()
				}
			}
		}
	}
	// Detect PR merge/close and trigger async session cleanup.
	var cmds []tea.Cmd
	if msg.pr.State == "merged" || msg.pr.State == "closed" {
		if mgr := a.managers[repoPath]; mgr != nil {
			if sess := mgr.GetSession(msg.sessionID); sess != nil {
				if sess.LifecyclePhase() == agent.LifecycleShipping {
					sessID := msg.sessionID
					if !a.closingSessions[key] {
						sess.SetLifecyclePhase(agent.LifecycleComplete)
						// Close the shipping panel if this session is currently open in it.
						if sp := a.modals.Shipping(); sp != nil && sp.SessionID() == sessID {
							a.closeModal()
						}
						var agentIDs []string
						for _, ag := range sess.Agents() {
							agentIDs = append(agentIDs, ag.ID)
							a.closingAgents[agentCacheKey(repoPath, ag.ID)] = true
						}
						a.closingSessions[key] = true
						cmds = append(cmds, func() tea.Msg {
							return killResultMsg{
								scope:     killScopeSession,
								repoPath:  repoPath,
								sessionID: sessID,
								agentIDs:  agentIDs,
								err:       filterNotFound(mgr.KillSession(sessID)),
							}
						})
					}
				}
			}
		}
	}
	// Detect check state transitions and fire notifications.
	if ps != nil && msg.checks != nil {
		prevState := ps.lastCheckState
		newState := msg.checks.State
		if prevState == "pending" && (newState == "success" || newState == "failure") {
			// Flash the session row.
			ps.flashUntil = time.Now().Add(PRCIFlashDuration)
			if newState == "success" {
				ps.flashColor = "success"
			} else {
				ps.flashColor = "error"
			}
			// Play audio notification, gated by the session's repo AudioEnabled setting
			// (same gate as the idle-transition notification above).
			if a.audioPlayer != nil && a.resolvedCache[repoPath].AudioEnabled {
				a.audioPlayer.Play()
			}
		}
		ps.lastCheckState = newState
	}
	a.updateDashboardPRCache()
	return a, tea.Batch(cmds...)
}

func (a App) handleMergePR(msg mergePRMsg) (tea.Model, tea.Cmd) {
	if msg.err != nil {
		a.setError("merge failed: " + msg.err.Error())
		return a, nil
	}
	a.closeModal()
	repoPath := msg.repoPath
	if repoPath == "" {
		repoPath = a.activeRepo
	}
	mgr := a.managers[repoPath]
	if mgr == nil {
		return a, nil
	}
	sess := mgr.GetSession(msg.sessionID)
	sessKey := cacheKey(repoPath, msg.sessionID)
	if sess == nil || a.closingSessions[sessKey] {
		return a, nil
	}
	sess.SetLifecyclePhase(agent.LifecycleComplete)
	agents := sess.Agents()
	agentIDs := make([]string, 0, len(agents))
	for _, ag := range agents {
		agentIDs = append(agentIDs, ag.ID)
		a.closingAgents[agentCacheKey(repoPath, ag.ID)] = true
	}
	sessID := msg.sessionID
	a.closingSessions[sessKey] = true
	return a, func() tea.Msg {
		return killResultMsg{
			scope:     killScopeSession,
			repoPath:  repoPath,
			sessionID: sessID,
			agentIDs:  agentIDs,
			err:       filterNotFound(mgr.KillSession(sessID)),
		}
	}
}

func (a *App) pollAllSessions() []tea.Cmd {
	const (
		maxConcurrent    = MaxConcurrentPRPolls
		shaCheckInterval = PRSHACheckInterval
	)

	var cmds []tea.Cmd
	now := time.Now()

outer:
	for _, repo := range a.cfg.Repos {
		mgr := a.managers[repo.Path]
		if mgr == nil {
			continue
		}
		for _, sess := range mgr.ListSessions() {
			if a.prPollsInFlight >= maxConcurrent {
				break outer
			}

			key := cacheKey(repo.Path, sess.ID)
			ps := a.prPollStates[key]
			if ps == nil {
				ps = &prSessionState{}
				a.prPollStates[key] = ps
			}
			if ps.inFlight {
				continue
			}

			// Determine adaptive polling interval.
			interval := a.prPollInterval(repo.Path, sess.ID, ps)
			if now.Sub(ps.lastPoll) < interval {
				// Push detection runs for every session — including those with a
				// cached PR — so new commits, force-pushes, and rewrites get
				// picked up promptly instead of waiting the 30s stable interval.
				// Throttled to once per shaCheckInterval so git rev-parse does
				// not block the Bubble Tea main goroutine on every tick.
				if now.Sub(ps.lastSHACheck) < shaCheckInterval {
					continue
				}
				ps.lastSHACheck = now

				// Detect external branch renames (e.g. `git branch -m`) before
				// querying the remote — a stale in-memory branch name causes
				// getRemoteSHA to always return "" and drops the 30s stable poll.
				if actualBranch := getCurrentHeadBranch(sess.Worktree.Path); actualBranch != "" && actualBranch != sess.Branch() {
					mgr.ReconcileExternalBranchRename(sess.ID, actualBranch)
					// Skip remote SHA check this tick; EventBranchRenamed will
					// arm the burst and the next tick has the correct branch.
					continue
				}

				sha := getRemoteSHA(repo.Path, sess.Branch())
				if sha == "" || sha == ps.lastRemoteSHA {
					continue
				}
				ps.lastRemoteSHA = sha
				// SHA changed — arm a burst so the next minute of polls runs
				// on the short (2s) cadence, then fall through to schedule an
				// immediate poll.
				ps.burstUntil = now.Add(PRPollBurstAfterCreate)
			}

			ps.lastPoll = now
			ps.inFlight = true
			a.prPollsInFlight++
			fetchThreads := sess.LifecyclePhase() == agent.LifecycleShipping
			cachedPRNumber := a.cachedPRNumberForFallback(repo.Path, sess)
			cmds = append(cmds, a.refreshPRStatusForSession(sess.ID, sess.Branch(), repo.Path, sess.Worktree.Path, fetchThreads, cachedPRNumber))
		}
	}
	return cmds
}

// cachedPRNumberForFallback returns the cached PR number for a Shipping session
// so the poll cmd can call resolveMergedFallback when the open-only lookup
// returns nil. Returns 0 for non-Shipping sessions, preserving today's
// 2-consecutive-nil eviction behaviour for Building/Reviewing sessions.
func (a *App) cachedPRNumberForFallback(repoPath string, sess *agent.Session) int {
	if sess.LifecyclePhase() != agent.LifecycleShipping {
		return 0
	}
	entry := a.prCache[cacheKey(repoPath, sess.ID)]
	if entry == nil || entry.pr == nil {
		return 0
	}
	return entry.pr.Number
}

// prPollInterval returns the adaptive polling interval for a session.
func (a *App) prPollInterval(repoPath, sessionID string, ps *prSessionState) time.Duration {
	// Event-driven burst (branch rename, new push): poll aggressively for a
	// short window so state transitions become visible within ~2s.
	if ps != nil && time.Now().Before(ps.burstUntil) {
		return PRPollDuringBurst
	}
	entry := a.prCache[cacheKey(repoPath, sessionID)]
	// No PR found yet but branch may have been pushed.
	if entry == nil || entry.pr == nil {
		if ps.lastRemoteSHA != "" {
			return PRPollAfterPush // branch pushed, waiting for PR
		}
		return PRPollStable // stable, no activity
	}
	// PR exists — adapt based on check state.
	if entry.checks != nil && entry.checks.State == "pending" {
		return PRPollCIPending
	}
	return PRPollStable
}

// getRemoteSHA runs `git rev-parse origin/<branch>` to detect pushes.
// Returns empty string on any error.
func getRemoteSHA(repoPath, branch string) string {
	out, err := exec.Command("git", "-C", repoPath, "rev-parse", "origin/"+branch).Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// getCurrentHeadBranch returns the branch name that HEAD points to in the given
// worktree. Returns "" on any error or when HEAD is detached (rev-parse returns
// "HEAD"). Mirrors the getLocalHeadSHA pattern.
func getCurrentHeadBranch(worktreePath string) string {
	if worktreePath == "" {
		return ""
	}
	out, err := exec.Command("git", "-C", worktreePath, "rev-parse", "--abbrev-ref", "HEAD").Output()
	if err != nil {
		return ""
	}
	branch := strings.TrimSpace(string(out))
	if branch == "HEAD" {
		return ""
	}
	return branch
}

// getLocalHeadSHA returns the local HEAD SHA for a worktree. Used as a
// fallback when getRemoteSHA returns "" (branch not yet pushed under the
// current name after a rename). Silent on error.
func getLocalHeadSHA(worktreePath string) string {
	if worktreePath == "" {
		return ""
	}
	out, err := exec.Command("git", "-C", worktreePath, "rev-parse", "HEAD").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// refreshPRStatusForSession returns a Cmd that polls PR, check, and review status for a single session.
// worktreePath is used as a fallback SHA source when the branch hasn't been pushed under its current name.
// cachedPRNumber, when > 0 and the session is in LifecycleShipping, enables a merged-fallback lookup
// via resolveMergedFallback so externally-merged/closed PRs are detected and the session is cleaned up.
func (a *App) refreshPRStatusForSession(sessionID, branch, repoPath, worktreePath string, fetchThreads bool, cachedPRNumber int) tea.Cmd {
	// Guard: ensure the caller passed the repo that actually owns this session.
	// This catches programming errors (e.g. passing cfg.Repos[0].Path for a
	// session that belongs to a different repo) before the poll fires. With
	// composite-keyed caches, an internal mismatch here would route a poll
	// result to the wrong (repoPath, sessionID) bucket — fail loud rather
	// than silently corrupt a repo's PR cache.
	if a.sessionByIDInRepo(repoPath, sessionID) == nil {
		mismatchErr := fmt.Errorf("internal: refreshPRStatus: repoPath %q does not own session %s", repoPath, sessionID)
		return func() tea.Msg { return prPollMsg{sessionID: sessionID, repoPath: repoPath, err: mismatchErr} }
	}
	ghClient := a.ghClient
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), PRPollClientTimeout)
		defer cancel()

		rawURL, err := git.GetRemoteURL(repoPath)
		if err != nil {
			return prPollMsg{sessionID: sessionID, repoPath: repoPath, err: err}
		}
		owner, repo, err := github.ParseRemoteURL(rawURL)
		if err != nil {
			return prPollMsg{sessionID: sessionID, repoPath: repoPath, err: err}
		}

		// Prefer SHA-based lookup: invariant to branch renames, so a PR opened
		// under a random refrain/<adj>-<noun> name (before Haiku rename finishes)
		// is still discovered after the rename. Fall back to branch lookup when
		// the commit hasn't been pushed or SHA lookup returns no PR.
		var pr *github.PRState
		sha := getRemoteSHA(repoPath, branch)
		if sha != "" {
			pr, _ = ghClient.GetPRBySHA(ctx, owner, repo, sha)
		}
		// If the remote SHA is missing (branch not yet pushed under current name
		// after a rename), try the local HEAD SHA — the commit may have been
		// pushed before the rename and GitHub still associates the PR with it.
		if pr == nil && sha == "" {
			if localSHA := getLocalHeadSHA(worktreePath); localSHA != "" {
				pr, _ = ghClient.GetPRBySHA(ctx, owner, repo, localSHA)
			}
		}
		if pr == nil {
			var err error
			pr, err = ghClient.GetPR(ctx, owner, repo, branch)
			if err != nil {
				return prPollMsg{sessionID: sessionID, repoPath: repoPath, err: err}
			}
		}
		// Shipping sessions: if the open-only lookups all returned nil, check
		// whether the cached PR was merged or closed externally.
		if pr == nil && cachedPRNumber > 0 {
			pr = resolveMergedFallback(ctx, owner, repo, cachedPRNumber, ghClient.RefreshPR)
		}
		var checks *github.CheckStatus
		var reviews *github.ReviewStatus
		var threads []github.ReviewThread
		var stack []*prCacheEntry
		if pr != nil {
			var err error
			// Prefer SHA for checks when available — matches what CI ran against.
			checkRef := branch
			if sha != "" {
				checkRef = sha
			}
			checks, err = ghClient.GetChecks(ctx, owner, repo, checkRef)
			if err != nil {
				return prPollMsg{sessionID: sessionID, repoPath: repoPath, err: err}
			}
			reviews, err = ghClient.GetReviews(ctx, owner, repo, pr.Number)
			if err != nil {
				return prPollMsg{sessionID: sessionID, repoPath: repoPath, err: err}
			}
			// Threads are only needed for the shipping panel — skip the fetch for
			// building/reviewing sessions to avoid doubling review API calls.
			if fetchThreads {
				threads, _ = ghClient.GetReviewThreads(ctx, owner, repo, pr.Number)
			}

			// Walk up the base-branch chain for stacked PR support (best-effort,
			// max 3 levels). Stop when the base targets a trunk branch, no PR is
			// found, or a branch is revisited (cycle guard).
			defaultBranches := map[string]bool{"main": true, "master": true, "develop": true}
			visited := map[string]bool{pr.HeadBranch: true}
			cur := pr
			for i := 0; i < 3; i++ {
				baseBranch := cur.BaseBranch
				if baseBranch == "" || defaultBranches[baseBranch] || visited[baseBranch] {
					break
				}
				basePR, _ := ghClient.GetPR(ctx, owner, repo, baseBranch)
				if basePR == nil {
					break
				}
				// Post-fetch cycle check catches diamond topologies where two PRs
				// share a base and HeadBranch != baseBranch used for lookup.
				if visited[basePR.HeadBranch] {
					break
				}
				visited[basePR.HeadBranch] = true
				entry := &prCacheEntry{pr: basePR}
				entry.checks, _ = ghClient.GetChecks(ctx, owner, repo, basePR.HeadBranch)
				entry.reviews, _ = ghClient.GetReviews(ctx, owner, repo, basePR.Number)
				stack = append(stack, entry)
				cur = basePR
			}
		}

		return prPollMsg{
			sessionID: sessionID,
			repoPath:  repoPath,
			pr:        pr,
			checks:    checks,
			reviews:   reviews,
			threads:   threads,
			stack:     stack,
		}
	}
}

// entryThreads safely returns threads from a prCacheEntry (nil-safe).
func entryThreads(entry *prCacheEntry) []github.ReviewThread {
	if entry == nil {
		return nil
	}
	return entry.threads
}

// setFeedbackVerdict lazily allocates the per-session triage map and sets the
// verdict on the item with the given key. For feedbackNeutral with an empty
// note, the entry is deleted to keep the map clean.
func (a *App) mergePRCmd(sessionID, repoPath string) tea.Cmd {
	return a.mergePRCmdWithMode(sessionID, repoPath, false)
}

func (a *App) forceMergePRCmd(sessionID, repoPath string) tea.Cmd {
	return a.mergePRCmdWithMode(sessionID, repoPath, true)
}

func (a *App) mergePRCmdWithMode(sessionID, repoPath string, force bool) tea.Cmd {
	if repoPath == "" {
		repoPath = a.activeRepo
	}
	ghClient := a.ghClient
	if ghClient == nil {
		return func() tea.Msg {
			return mergePRMsg{sessionID: sessionID, repoPath: repoPath, err: fmt.Errorf("GitHub client not available")}
		}
	}
	entry := a.prCache[cacheKey(repoPath, sessionID)]
	if entry == nil || entry.pr == nil {
		return func() tea.Msg {
			return mergePRMsg{sessionID: sessionID, repoPath: repoPath, err: fmt.Errorf("no PR cached")}
		}
	}
	if repoPath == "" {
		return func() tea.Msg {
			return mergePRMsg{sessionID: sessionID, repoPath: repoPath, err: fmt.Errorf("session repo not found")}
		}
	}
	method := a.resolvedCache[repoPath].MergeMethod
	switch method {
	case "merge", "squash", "rebase":
	case "":
		method = "squash"
	default:
		return func() tea.Msg {
			return mergePRMsg{sessionID: sessionID, repoPath: repoPath, err: fmt.Errorf("invalid merge_method %q: must be merge, squash, or rebase", method)}
		}
	}
	prNum := entry.pr.Number
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), PRPollClientTimeout)
		defer cancel()
		rawURL, err := git.GetRemoteURL(repoPath)
		if err != nil {
			return mergePRMsg{sessionID: sessionID, repoPath: repoPath, err: err}
		}
		owner, repo, err := github.ParseRemoteURL(rawURL)
		if err != nil {
			return mergePRMsg{sessionID: sessionID, repoPath: repoPath, err: err}
		}

		fresh, err := ghClient.RefreshPR(ctx, owner, repo, prNum)
		if err != nil {
			return mergePRMsg{sessionID: sessionID, repoPath: repoPath, err: fmt.Errorf("refreshing PR state: %w", err)}
		}
		if fresh == nil {
			return mergePRMsg{sessionID: sessionID, repoPath: repoPath, err: fmt.Errorf("PR #%d no longer exists", prNum)}
		}
		if fresh.State != "open" {
			return mergePRMsg{sessionID: sessionID, repoPath: repoPath, err: fmt.Errorf("PR #%d is %s, cannot merge", prNum, fresh.State)}
		}
		if !force && fresh.MergeableState != "clean" {
			return mergePRMsg{sessionID: sessionID, repoPath: repoPath, err: fmt.Errorf("PR mergeable state is %q (was %q when gate ran); refresh and retry", fresh.MergeableState, entry.pr.MergeableState)}
		}

		if err := ghClient.MergePR(ctx, owner, repo, prNum, method); err != nil {
			return mergePRMsg{sessionID: sessionID, repoPath: repoPath, err: err}
		}
		return mergePRMsg{sessionID: sessionID, repoPath: repoPath}
	}
}

// activeAgentCount returns the count of live non-shell agents across all repos.
// Used to enforce the soft concurrent-agent limit in focus mode. Defers to
// Manager.AgentCount, which already excludes shells and exited (Done/Error)
// agents — keeping the "live" definition in one place so all three call sites
// (quit guard, soft cap, repo-picker counts) can't drift apart.
func (a *App) startPRDraftCmd(sess *agent.Session, repoPath string, transitionShipping bool) tea.Cmd {
	if sess == nil || sess.Worktree == nil {
		return func() tea.Msg {
			return prDraftReadyMsg{err: fmt.Errorf("no worktree for session")}
		}
	}
	branch := sess.Branch()
	worktreePath := sess.Worktree.Path
	sessionID := sess.ID

	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), PRPollParentTimeout)
		defer cancel()

		// Resolve owner/repo from the parent repo's remote URL.
		rawURL, err := git.GetRemoteURL(repoPath)
		if err != nil {
			return prDraftReadyMsg{sessionID: sessionID, err: fmt.Errorf("get remote url: %w", err)}
		}
		owner, repo, err := github.ParseRemoteURL(rawURL)
		if err != nil {
			return prDraftReadyMsg{sessionID: sessionID, err: fmt.Errorf("parse remote url: %w", err)}
		}

		// Determine base branch (local, no network).
		base := sess.Worktree.BaseBranch
		if base == "" {
			base = "main"
		}
		worktree := sess.Worktree

		// Push and draft concurrently; total latency = max(push, drafter).
		// innerCtx is cancelled by push failure so a fast push error (e.g. auth
		// failure) immediately aborts the expensive Haiku subprocess rather than
		// waiting out the full 90s parent timeout.
		innerCtx, innerCancel := context.WithCancel(ctx)
		defer innerCancel()

		var (
			pushErr  error
			draftErr error
			draft    *agent.PRDraft
			wg       sync.WaitGroup
		)

		wg.Add(2)
		go func() {
			defer wg.Done()
			if err := git.Push(worktreePath, branch); err != nil {
				pushErr = fmt.Errorf("push branch: %w", err)
				innerCancel()
			}
		}()
		go func() {
			defer wg.Done()
			commits := ""
			if cs, logErr := git.LogCommitsAgainstBase(worktree); logErr == nil && len(cs) > 0 {
				var sb strings.Builder
				for _, c := range cs {
					sb.WriteString(c.Subject)
					sb.WriteString("\n")
					if c.Body != "" {
						sb.WriteString(c.Body)
						sb.WriteString("\n")
					}
				}
				commits = strings.TrimSpace(sb.String())
			}

			diffstat := ""
			if stats, statsErr := git.GetDiffStats(repoPath, worktree); statsErr == nil {
				diffstat = fmt.Sprintf("%d file(s) changed, +%d -%d lines",
					stats.Files, stats.Insertions, stats.Deletions)
			}

			template := git.FindPRTemplate(worktreePath)

			taskPrompt := sess.TaskSummary()
			if taskPrompt == "" {
				taskPrompt = sess.GetDisplayName()
			}

			drafter := agent.DefaultPRDrafter()
			var err error
			draft, err = drafter(innerCtx, commits, diffstat, taskPrompt, template)
			if err != nil {
				draftErr = fmt.Errorf("draft PR: %w", err)
			}
		}()
		wg.Wait()

		// If push failed, drafter may have been cancelled via innerCtx — return
		// only the push error; the drafter error is expected noise in that case.
		if pushErr != nil {
			return prDraftReadyMsg{sessionID: sessionID, err: pushErr}
		}
		if draftErr != nil {
			return prDraftReadyMsg{sessionID: sessionID, err: draftErr}
		}

		return prDraftReadyMsg{
			sessionID:          sessionID,
			title:              draft.Title,
			body:               draft.Body,
			owner:              owner,
			repo:               repo,
			head:               branch,
			base:               base,
			repoPath:           repoPath,
			transitionShipping: transitionShipping,
		}
	}
}

// submitPRComposeModal handles a prComposeSubmitMsg by calling CreatePR and
// emitting a prCreatedMsg.
func (a *App) submitPRComposeModal(msg prComposeSubmitMsg) (tea.Model, tea.Cmd) {
	ghClient := a.ghClient
	if ghClient == nil {
		return a, func() tea.Msg {
			return prCreatedMsg{sessionID: a.prModalSessionID, repoPath: a.prModalRepoPath, err: fmt.Errorf("GitHub auth not available")}
		}
	}
	owner := a.prModalOwner
	repo := a.prModalRepo
	head := a.prModalHead
	base := a.prModalBase
	sessionID := a.prModalSessionID
	repoPath := a.prModalRepoPath
	transitionShipping := a.prModalTransitionShipping
	return a, func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		pr, err := ghClient.CreatePR(ctx, owner, repo, head, base, msg.title, msg.body, msg.draft)
		if err != nil {
			return prCreatedMsg{sessionID: sessionID, repoPath: repoPath, err: err}
		}
		return prCreatedMsg{sessionID: sessionID, repoPath: repoPath, pr: pr, transitionShipping: transitionShipping}
	}
}

// openURL opens the given URL in the system's default browser. Fire-and-forget.
// Declared as a var so tests can swap in a no-op to avoid launching a real browser.
var openURL = func(url string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", url)
	case "windows":
		// Escape embedded quotes to avoid shell injection via cmd.exe.
		safeURL := strings.ReplaceAll(url, `"`, `%22`)
		cmd = exec.Command("cmd", "/c", "start", `"`+safeURL+`"`)
	default:
		cmd = exec.Command("xdg-open", url)
	}
	return cmd.Start()
}
