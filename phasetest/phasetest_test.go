// Copyright 2026 The Phase Contributors
// SPDX-License-Identifier: MIT

package phasetest_test

import (
	"context"
	"errors"
	"testing"
	"time"

	phase "github.com/wow-qe/phase-go"
	"github.com/wow-qe/phase-go/phasetest"
)

func TestRunForWiresCaseAndScope(t *testing.T) {
	c := &stubCase{id: "case-42"}
	timing := phase.Timing{Attempts: 3, Interval: time.Millisecond}
	run, clock := phasetest.RunFor(t, c, "phase-a", timing)

	if run == nil {
		t.Fatal("RunFor returned a nil *phase.Run")
	}
	if clock == nil {
		t.Fatal("RunFor returned a nil *phasetest.Clock")
	}
	if run.Case() != c {
		t.Fatalf("run.Case() = %v, want the case passed to RunFor", run.Case())
	}
	if run.Scope().CaseID != "case-42" {
		t.Fatalf("run.Scope().CaseID = %q, want %q", run.Scope().CaseID, "case-42")
	}
}

func TestRunForPositionsThePhaseAndTiming(t *testing.T) {
	// WaitUntil reads its budget from the phase Run's currentTiming(), which
	// only WithPhase(id, timing) installs. RunFor must configure the run with
	// exactly that phase and timing, verified here by driving a real
	// WaitUntil against it.
	c := &stubCase{id: "case-1"}
	timing := phase.Timing{Attempts: 3, Interval: time.Millisecond}
	run, _ := phasetest.RunFor(t, c, "wait-phase", timing)

	calls := 0
	_, err := phase.WaitUntil(context.Background(), run, func(ctx context.Context) (int, bool, error) {
		calls++
		return 0, false, nil
	})
	if !errors.Is(err, phase.ErrBudgetExhausted) {
		t.Fatalf("err = %v, want ErrBudgetExhausted", err)
	}
	if calls != 3 {
		t.Fatalf("condition evaluated %d times, want 3 (Attempts from the Timing given to RunFor)", calls)
	}
}

func TestRunForClockCanBeAdvancedByTheTest(t *testing.T) {
	// The returned Clock is the one wired as the sleeper: advancing time
	// happens via the sleeper, never by blocking, and a 20×15s budget must
	// still resolve instantly through it.
	c := &stubCase{id: "case-1"}
	timing := phase.Timing{Attempts: 20, Interval: 15 * time.Second}
	run, clock := phasetest.RunFor(t, c, "settle", timing)

	before := clock.Now()
	started := time.Now()
	_, err := phase.WaitUntil(context.Background(), run, func(ctx context.Context) (int, bool, error) {
		return 0, false, nil
	})
	elapsed := time.Since(started)

	if !errors.Is(err, phase.ErrBudgetExhausted) {
		t.Fatalf("err = %v, want ErrBudgetExhausted", err)
	}
	if elapsed > 200*time.Millisecond {
		t.Fatalf("WaitUntil took %v real time for a 20×15s budget", elapsed)
	}
	wantAfter := before.Add(19 * 15 * time.Second)
	if got := clock.Now(); !got.Equal(wantAfter) {
		t.Fatalf("clock.Now() = %v, want %v — RunFor's clock must be the sleeper's clock", got, wantAfter)
	}
}
