package agent

import "context"

// asyncJob bundles the fields that travel together for an in-flight async
// task on a Session: a running flag, an optional cancel function, an
// optional error from the last attempt, and optional retry-attempt progress.
//
// It does NOT carry its own mutex. Every method assumes the caller holds
// the owning Session's s.mu — either write-locked (tryStart, finish,
// setErr, setAttempt) or read-locked (running, err, attempt). Doing it
// this way preserves the existing atomicity of cross-job checks (e.g.
// "revising is blocked while drafting is in flight") without nested locks.
//
// The shape is intentionally a superset: jobs that don't need a cancel
// func leave it nil; jobs that don't track retries leave attempt/max at
// zero. Storing zero-valued fields costs little; the win is one named
// pattern in place of four hand-rolled state machines on Session.
type asyncJob struct {
	running    bool
	cancel     context.CancelFunc
	err        error
	attempt    int
	maxAttempt int
}

// tryStart marks the job running and stores cancel. Returns false if the
// job is already running or if blocked is true. The blocked predicate is
// evaluated by the caller (typically reading another asyncJob.running)
// while it holds s.mu, so the cross-job exclusivity check is atomic with
// the start.
func (j *asyncJob) tryStart(cancel context.CancelFunc, blocked bool) bool {
	if j.running || blocked {
		return false
	}
	j.running = true
	j.cancel = cancel
	return true
}

// finish clears the running flag and retry state, returning the stored
// cancel func for the caller to invoke AFTER releasing s.mu. Returning the
// cancel rather than calling it here keeps the lock-then-cancel pattern
// every existing finishX method already followed (cancel may take locks
// in subprocess teardown paths, so calling it under s.mu risks deadlock).
//
// Does NOT clear err: the last error survives finish so the UI can render
// a failure badge after the goroutine has exited.
func (j *asyncJob) finish() context.CancelFunc {
	cancel := j.cancel
	j.running = false
	j.cancel = nil
	j.attempt = 0
	j.maxAttempt = 0
	return cancel
}
