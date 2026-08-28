// Copyright 2026 The Phase Contributors
// SPDX-License-Identifier: MIT

package phase

import (
	"context"
	"fmt"
)

// When is an optional guard evaluated at the phase's turn — after
// dependency pruning and group setup, before timing resolution and hooks —
// over evidence already recorded in this case: handoff keys via Get, prior
// phases' recorded truth via PriorEvidence. Never live system state — the
// same documented-not-typed restriction as AppliesTo, and whatever
// mechanical enforcement AppliesTo ever gains, When gains identically.
//
// The three returns are three different facts:
//
//	ok=true            → the phase runs.
//	ok=false + reason  → a recorded NotApplicable ("condition: " + reason).
//	                     An empty reason is replaced with a loud placeholder,
//	                     never preserved silently.
//	err != nil         → the condition itself broke (a missing key, a bug):
//	                     Errored, dependents prune — an error is not a
//	                     decline, exactly as an error is not a failed
//	                     comparison.
//
// "Run only if y passed" is a When reading PriorEvidence(y) — one
// mechanism, not a second dependency vocabulary.
type When interface {
	When(context.Context, *Run) (ok bool, reason string, err error)
}

// runOneCondition contains a panicking When exactly as every consumer call
// is contained.
func runOneCondition(ctx context.Context, w When, run *Run) (ok bool, reason string, err error) {
	err = contain("condition", func() error {
		var e error
		ok, reason, e = w.When(ctx, run)
		return e
	})
	if err != nil && !ok {
		reason = "" // a panic mid-condition leaves no trustworthy reason
	}
	return ok, reason, err
}

// PriorEvidenceSummary is what a phase actually left on the record — a
// live scan, not a cached status: a phase that returned cleanly while
// recording a failing result reads Passed in its outcome row, and a guard
// deciding on that stale signal would gate wrongly at exactly the moment
// gating matters.
type PriorEvidenceSummary struct {
	Recorded int  // results the phase recorded
	Failing  int  // of those, how many failed
	Errored  bool // the phase (or its hooks) recorded environment errors
}

// PriorEvidence reports what phase id recorded so far in this case. It is
// restricted by construction to the calling phase's transitive DependsOn
// set — the DAG edge must exist before the read is even possible, so
// ordering safety needs no separate validation pass. Any other id returns
// an error.
func (r *Run) PriorEvidence(id ID) (PriorEvidenceSummary, error) {
	if r.depScope == nil || !r.depScope[id] {
		return PriorEvidenceSummary{}, fmt.Errorf(
			"phase %q: PriorEvidence(%q) is outside the transitive DependsOn set — declare the dependency; ordering is what makes the read safe", r.phase, id)
	}
	r.core.mu.Lock()
	defer r.core.mu.Unlock()
	var out PriorEvidenceSummary
	for _, ar := range r.core.results {
		if ar.Phase == id {
			out.Recorded++
			if !ar.Result.Passed() {
				out.Failing++
			}
		}
	}
	for _, ae := range r.core.errs {
		if ae.Phase == id {
			out.Errored = true
			break
		}
	}
	return out, nil
}
