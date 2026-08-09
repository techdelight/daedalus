// Copyright (C) 2026 Techdelight BV

package control

// The in-flight claim, made leak-proof.
//
// A claim marks a Task as having a long operation running in this process, so
// that (a) a second operation on the same Task is refused rather than queued
// behind an hour-long lock, and (b) the reconcile loop can tell live work from
// crashed work. Both matter only because the service lock is deliberately NOT
// held across runner.Run / verifier.Verify / reviewer.Review.
//
// WHY THIS FILE EXISTS. The Sprint-58 audit found the same bug three times: a
// claim (or a mutex) released by a bare statement rather than a `defer`, which a
// panic then unwound straight past. Each was fixed individually, and each fix
// left the NEXT one one bare statement away — five hand-written `defer` sites,
// all correct, none enforced. The fix for a bug class is not five correct sites;
// it is making the incorrect site impossible to write.
//
// So the claim is no longer something a caller takes and remembers to release.
// It is a scope: `withClaim` hands the body a claimed Task and releases it on
// every exit — return, error, or panic — because the release is a `defer` inside
// the helper, where a caller cannot forget it or write it any other way. There is
// no exported way to take a claim without one.

// claimedFunc is the body of a claimed operation. It runs with s.mu HELD and the
// Task claimed; it may release and re-take s.mu internally (that is the whole
// point — see unlockedDuring), and the claim survives regardless.
type claimedFunc func() error

// withClaim runs fn with `taskID` claimed for op, releasing the claim on every
// exit path including a panic. s.mu MUST be held on entry and is held on exit.
//
// A Task that already has a claim is refused with ReasonOperationInFlight rather
// than blocked; fn is not called.
func (s *Service) withClaim(taskID string, op inflightOp, fn claimedFunc) error {
	if err := s.beginOp(taskID, op); err != nil {
		return err
	}
	// The ONLY release path. Not a statement a caller writes, so not a statement a
	// caller can forget, misplace, or have a panic skip.
	defer delete(s.inflight, taskID)
	return fn()
}

// unlockedDuring runs fn with s.mu RELEASED and re-takes it before returning,
// including when fn panics.
//
// The closure-with-defer shape is load-bearing, not stylistic: a bare
// Unlock()/…/Lock() pair leaves the mutex unlocked if the body panics, and
// because net/http recovers a handler panic the daemon would then stay up and
// silently deadlock on the next request — the worst available failure mode. This
// helper is the only way this package leaves the lock, so that shape is written
// once and cannot be got wrong at a call site.
func (s *Service) unlockedDuring(fn func()) {
	s.mu.Unlock()
	defer s.mu.Lock()
	fn()
}

// claimJobID reports the job id currently claimed for a task, if any. s.mu must
// be held.
func (s *Service) claimJobID(taskID string) (string, bool) {
	op, ok := s.inflight[taskID]
	return op.jobID, ok
}
