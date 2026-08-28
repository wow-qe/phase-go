// Copyright 2026 The Phase Contributors
// SPDX-License-Identifier: MIT

package phasetest

import (
	"context"
	"testing"
	"time"

	phase "github.com/wow-qe/phase-go"
)

// RunFor builds a *phase.Run for one case via phase.NewRunForTesting, wired
// with a Clock — returned so the test can Advance it — and positioned at the
// given phase with the given timing, exactly as the Runner would before
// invoking that phase.
//
// Uses testing.TB only for Helper/Fatal.
func RunFor(t testing.TB, c phase.Case, id phase.ID, timing phase.Timing) (*phase.Run, *Clock) {
	t.Helper()
	if c == nil {
		t.Fatal("phasetest.RunFor: case is nil")
	}
	clock := NewClock(time.Now())
	run := phase.NewRunForTesting(c,
		phase.WithClock(clock.Now),
		phase.WithSleeper(clock.Sleeper()),
		phase.WithPhase(id, timing),
	)
	return run, clock
}

// InvokePhase exercises ph's full Before→Run→After arc on run, exactly as
// the Runner would (same fold rules, same panic containment), returning the
// outcome row the Runner would land. Use it to unit-test hook-bearing
// phases without spinning up a Runner.
func InvokePhase(ctx context.Context, ph phase.Interface, run *phase.Run) phase.PhaseOutcome {
	return phase.ExecutePhaseForTesting(ctx, ph, run)
}
