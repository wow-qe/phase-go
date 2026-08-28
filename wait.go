// Copyright 2026 The Phase Contributors
// SPDX-License-Identifier: MIT

package phase

import (
	"context"
	"fmt"
	"time"
)

// WaitUntil evaluates cond until it reports done, the attempt budget runs
// out, the condition errors, or ctx is cancelled. It is the reason phases
// never sleep: a fixed sleep is simultaneously too slow on a fast machine and
// too short on a loaded one — the definition of a flaky test that also
// wastes time.
//
// The budget comes from the current phase's resolved Timing — attempts per
// case, never a shared wall clock (a shared ceiling once starved nine cases
// that were merely scheduled late). The semantics, one per outcome:
//
//   - done            → the value, nil. Sleeps happen BETWEEN attempts only.
//   - budget spent    → ErrBudgetExhausted, wrapped to read
//     "gave up after 4×15s in settle" — a failure that names its budget,
//     never a timeout swallowed as "nothing found".
//   - cond errors     → that error, immediately. An error is a transport or
//     adapter fact, not "not yet"; patient retries here would hide a real
//     outage (the three retries — poll, tolerance, transport — are distinct,
//     and this loop is only the first).
//   - ctx cancelled   → ctx.Err(), so the runner marks the case
//     Errored("cancelled"), never Failed.
//
// Timing.Timeout, when set, bounds the whole wait AND each cond call: cond
// receives a context carrying the remaining budget, and its expiry surfaces
// as the named budget error. The cut-off is cooperative — Go cannot preempt
// a goroutine — so cond MUST pass its context through to whatever blocks
// (the same contract every HTTP/DB/gRPC call already keeps); a cond that
// ignores its context is beyond any framework's reach.
//
// Timing.Attempts < 1 is a *FrameworkError: preflight guarantees validity,
// so reaching this without it is a bug in phase — and clamping would be a
// silent default, which is how coverage disappears.
func WaitUntil[T any](ctx context.Context, r *Run, cond func(context.Context) (T, bool, error)) (T, error) {
	var zero T
	phaseID, timing := r.currentTiming()
	if timing.Attempts < 1 {
		return zero, &FrameworkError{
			Invariant: "validated timing",
			Detail: fmt.Sprintf("WaitUntil in phase %q with Attempts=%d — preflight should have refused this",
				phaseID, timing.Attempts),
		}
	}
	// Timeout is the wall-clock backstop the docs always promised and the
	// code never read. Two enforcement points, because two different things go
	// slow: the deadline on the injected clock bounds the ATTEMPT LOOP (many
	// slow-but-returning calls), and a deadline context handed to cond bounds
	// EACH CALL, so a single cond that blocks past the budget is cut off
	// mid-call rather than caught before an attempt that never comes. The
	// cut-off is cooperative — Go cannot preempt a goroutine, so a cond that
	// ignores its context is beyond any framework's reach; that is a contract
	// on cond, stated on this function. Zero Timeout means unbounded (attempts
	// still cap the loop).
	var deadline time.Time
	if timing.Timeout > 0 {
		deadline = r.now().Add(timing.Timeout)
	}
	for attempt := 1; ; attempt++ {
		r.attemptsUsed++ // the bounded settle-cost summary on the outcome row
		if err := ctx.Err(); err != nil {
			return zero, err
		}
		if !deadline.IsZero() && !r.now().Before(deadline) {
			return zero, fmt.Errorf("phase %s exceeded its %s timeout after %d attempt(s): %w",
				phaseID, timing.Timeout, attempt-1, ErrBudgetExhausted)
		}
		condCtx, cancel := ctx, context.CancelFunc(func() {})
		if !deadline.IsZero() {
			condCtx, cancel = context.WithTimeout(ctx, deadline.Sub(r.now()))
		}
		v, done, err := cond(condCtx)
		cancel()
		if done && err == nil {
			return v, nil // success at the buzzer still counts
		}
		if condCtx.Err() == context.DeadlineExceeded && ctx.Err() == nil {
			// The budget expired during the call. Whatever cond returned on the
			// way out (usually its context error), the fact that matters is the
			// named budget — never a bare "context deadline exceeded".
			return zero, fmt.Errorf("phase %s exceeded its %s timeout during attempt %d: %w",
				phaseID, timing.Timeout, attempt, ErrBudgetExhausted)
		}
		if err != nil {
			return zero, err
		}
		if r.core.retrySink != nil {
			// Emission time is never charged against the Timeout budget
			// - a slow observer must not cause ErrBudgetExhausted.
			t0 := r.now()
			r.core.retrySink(phaseID, "poll", attempt, timing.Attempts, "")
			if !deadline.IsZero() {
				deadline = deadline.Add(r.now().Sub(t0))
			}
		}
		if attempt == timing.Attempts {
			return zero, fmt.Errorf("gave up after %d×%s in %s: %w",
				timing.Attempts, timing.Interval, phaseID, ErrBudgetExhausted)
		}
		if err := r.sleep(ctx, timing.Interval); err != nil {
			return zero, err
		}
	}
}
