// Copyright 2026 The Phase Contributors
// SPDX-License-Identifier: MIT

package phase

import (
	"context"
	"fmt"
)

// Optional per-phase hooks, discovered by type
// assertion — the required Interface contract does not grow. Both hooks
// receive the phase's OWN bound *Run: same attribution, same resolved
// Timing, and everything they Record/Observe/Fail rides the existing
// evidence machinery — there is no separate hook status channel, by
// design: a hook that records a failing result can never be silently
// swallowed into a green case, whatever its own error return says.

// BeforeHook probes preconditions. It runs after SettleDelay (probing an
// unsettled system fails spuriously) and before Run. Unlike AppliesTo it
// MAY read live system state — that is its purpose — but it must be
// side-effect-free on the system under test: acquire-then-verify belongs
// in a Group's Lifecycle, which has the teardown guarantee Before lacks.
//
// An error means the phase's premises are unmet: Run is not called, After
// is not called, the outcome is Errored ("before: " + reason, Stage
// "before"), and dependents prune exactly as for any phase error.
//
// The two-fact pattern for a precondition VIOLATION (a product fact, not
// environment noise): Record the failing result WITH evidence, then return
// the error — the case derives Failed through the existing single-writer
// path. An environment failure just returns the error: Errored.
//
// Waiting for a precondition is WaitUntil called from inside Before, on
// the same Timing budget — never a hook-level retry (the three retry kinds
// stay three).
type BeforeHook interface {
	Before(context.Context, *Run) error
}

// AfterHook concludes: tally, phase-specific conclusions, per-phase
// cleanup that does not need to survive cancellation (cleanup that must
// belongs to Group/Fixture teardown — After sees the live ctx).
//
// After runs on EVERY path where Before succeeded — pass, fail, error,
// panic in Run — and receives the candidate outcome (its Results/Failing
// counts are computed later and read as zero here). Its error folds into
// the SAME outcome row before it lands: an Errored row stays Errored
// (After never heals a failed Run); a Passed row flips to Errored
// ("after: " + reason, Stage "after"). Exactly one outcome lands per
// phase, after After has had its say.
type AfterHook interface {
	After(context.Context, *Run, PhaseOutcome) error
}

// runOneBefore and runOneAfter contain consumer panics exactly as
// runOnePhase does: a hook bug is that case's evidence, never the batch's
// crash.

func runOneBefore(ctx context.Context, h BeforeHook, run *Run) error {
	return contain("before hook", func() error { return h.Before(ctx, run) })
}

func runOneAfter(ctx context.Context, h AfterHook, run *Run, po PhaseOutcome) error {
	return contain("after hook", func() error { return h.After(ctx, run, po) })
}

// executePhase runs one phase's Before→Run→After arc on its bound view and
// returns the single outcome row to land — the runner's path and
// phasetest.InvokePhase's, so consumer tests exercise exactly what the
// runner executes.
func executePhase(ctx context.Context, ph Interface, pv *Run) PhaseOutcome {
	id := ph.ID()
	if bh, ok := ph.(BeforeHook); ok {
		preFailing := pv.failingRecorded()
		if err := runOneBefore(ctx, bh, pv); err != nil {
			// The two-fact split, kept clean at the ERRORS layer too (QE
			// catch): a precondition VIOLATION recorded its failing result -
			// a product fact - and must not ALSO page whoever watches
			// cr.Errors for environment trouble. Only an unrecorded failure
			// is environment noise.
			if pv.failingRecorded() == preFailing {
				pv.Fail(fmt.Errorf("phase %s before: %w", id, err))
			}
			return PhaseOutcome{ID: id, Status: Errored, Reason: "before: " + err.Error(), Stage: "before"}
		}
	}
	po := PhaseOutcome{ID: id, Status: Passed}
	if err := runOnePhase(ctx, ph, pv); err != nil {
		pv.Fail(fmt.Errorf("phase %s: %w", id, err))
		po = PhaseOutcome{ID: id, Status: Errored, Reason: err.Error()}
	}
	if ah, ok := ph.(AfterHook); ok {
		if err := runOneAfter(ctx, ah, pv, po); err != nil {
			pv.Fail(fmt.Errorf("phase %s after: %w", id, err))
			if po.Status == Passed {
				// After's failure is the row's failure. An already-Errored
				// row keeps its original cause — After never heals, and its
				// error is on the record via Fail above.
				po = PhaseOutcome{ID: id, Status: Errored, Reason: "after: " + err.Error(), Stage: "after"}
			}
		}
	}
	return po
}
