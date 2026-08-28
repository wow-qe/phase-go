// Copyright 2026 The Phase Contributors
// SPDX-License-Identifier: MIT

package phase

import (
	"context"
	"fmt"

	"github.com/wow-qe/phase-go/result"
)

// ToleratedAttempt is the structured evidence one failed tolerance attempt
// leaves behind (an Observation value): which attempt of which budget, what
// the check said, and the values it saw — so a flake-report reader can
// compare what attempt 1 observed against attempt 2, not just read prose.
type ToleratedAttempt struct {
	Attempt  int    `json:"attempt"`
	Of       int    `json:"of"`
	Reason   string `json:"reason"`
	Expected any    `json:"expected,omitempty"`
	Actual   any    `json:"actual,omitempty"`
}

// Tolerate evaluates a check that is DECLARED flaky, up to attempts times,
// and is the only producer of the Flaked status. It records
// its own evidence — do not Record the returned result again. The error is
// non-nil only for cancellation: propagate it (return it from the phase), as
// with WaitUntil, so the runner marks the case Errored, never Failed — an
// interrupted tolerance is not a concluded one, and no attempt's failure
// carries verdict weight until the budget finishes adjudicating.
//
// The four clauses, each load-bearing here:
//
//   - a stated reason: tolerance without a justification is a silent
//     flake-swallower, so an empty reason refuses to run the check at all
//     and records a failing result instead;
//   - a bounded retry: attempts < 2 is either meaningless (1) or unbounded
//     ambition (0, negative) — refused the same way, never clamped;
//   - every attempt recorded as evidence: each failed attempt becomes an
//     Observation carrying a ToleratedAttempt (attempt number, reason,
//     expected/actual). Failed attempts are observations rather than failing
//     results because the case's verdict must come from the FINAL judgement
//     — Verify's Flaked rule demands evidence of passing and zero failing
//     comparisons;
//   - "passed on attempt 3" is a different fact from "passed": a pass on
//     any attempt after the first marks the run, and finish() derives
//     Flaked, never Passed, from that mark.
//
// A first-attempt pass is a plain pass: no trail, no flake. Exhausting the
// whole budget records the last failing result with the exhausted tolerance
// named in its reason — and only true exhaustion says so; an interruption is
// recorded as an interruption, with the actual attempt count. Between
// attempts Tolerate sleeps the current phase's resolved Interval via the
// injected sleeper.
//
// Tolerance retries are the SECOND of the three distinct retry kinds
// : WaitUntil polls "not yet", Tolerate re-judges a completed
// failure it was told to expect, and transport retries belong to adapters.
func Tolerate(ctx context.Context, r *Run, reason string, attempts int, check func(context.Context) result.Result) (result.Result, error) {
	phaseID, timing := r.currentTiming()
	if reason == "" {
		res := result.Failed("tolerance declaration",
			fmt.Sprintf("Tolerate in phase %q with no stated reason — tolerance must be justified", phaseID))
		r.Record(res)
		return res, nil
	}
	if attempts < 2 {
		res := result.Failed("tolerance declaration",
			fmt.Sprintf("Tolerate in phase %q with attempts=%d — a bounded retry needs at least 2", phaseID, attempts))
		r.Record(res)
		return res, nil
	}
	var last result.Result
	for attempt := 1; ; attempt++ {
		r.attemptsUsed++ // counted alongside WaitUntil polls
		last = check(ctx)
		if last.Passed() {
			r.Record(last)
			if attempt > 1 {
				r.markFlake(fmt.Sprintf("%q passed on attempt %d of %d (tolerant: %s)",
					last.Name(), attempt, attempts, reason))
			}
			return last, nil
		}
		r.Observe(fmt.Sprintf("tolerated failure: %s (attempt %d of %d)", last.Name(), attempt, attempts),
			ToleratedAttempt{Attempt: attempt, Of: attempts, Reason: last.Reason(),
				Expected: last.Expected(), Actual: last.Actual()})
		if r.core.retrySink != nil {
			r.core.retrySink(phaseID, "tolerance", attempt, attempts, last.Reason())
		}
		if attempt == attempts {
			break
		}
		if err := r.sleep(ctx, timing.Interval); err != nil {
			// Interrupted, not exhausted: no fabricated final judgement. The
			// trail above keeps what each attempt saw; this records that the
			// adjudication was cut short, with the ACTUAL count; the returned
			// error routes the case to Errored via the phase's return.
			r.Observe(fmt.Sprintf("tolerance interrupted after attempt %d of %d: %s", attempt, attempts, last.Name()),
				fmt.Sprintf("cancelled while waiting to retry (tolerant: %s): %v", reason, err))
			return result.Result{}, fmt.Errorf("tolerance for %q interrupted after attempt %d of %d: %w",
				last.Name(), attempt, attempts, err)
		}
	}
	res := result.Failed(last.Name(),
		fmt.Sprintf("%s — still failing after all %d tolerant attempts (%s)", last.Reason(), attempts, reason))
	if e := last.Expected(); e != nil {
		res = res.WithExpected(e)
	}
	if a := last.Actual(); a != nil {
		res = res.WithActual(a)
	}
	r.Record(res)
	return res, nil
}
