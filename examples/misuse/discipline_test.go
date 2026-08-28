// Copyright 2026 The Phase Contributors
// SPDX-License-Identifier: MIT

package misuse

import (
	"context"
	"strings"
	"testing"
	"time"

	phase "github.com/wow-qe/phase-go"
	"github.com/wow-qe/phase-go/result"
)

// Evidence-discipline sabotage: direct hits on the founding rule and its
// satellites. A result over zero comparisons, a phase that asserts
// nothing, an unjustified tolerance, an unbounded one — each must be
// answered in the report, never absorbed.

func TestZeroComparisonsIsAFailureNeverAPass(t *testing.T) {
	empty := &sabotagePhase{id: "empty", run: func(_ context.Context, r *phase.Run) error {
		r.Record(result.Compared("all(empty) trap", []bool{}))
		return nil
	}}
	rep := run(t, smallRunner(t, phase.NewPipeline(empty)), []phase.Case{plainCase("one")})
	cr := rowOf(t, rep, "one")
	if cr.Status != phase.Failed || cr.FailedIn != "empty" {
		t.Fatalf("case = %v in %q — the founding defect must fail loudly", cr.Status, cr.FailedIn)
	}
	rv := cr.Results[0].Result
	if rv.Passed || rv.Comparisons != 0 {
		t.Fatalf("result = %+v, want a failing row exposing its zero comparisons", rv)
	}
}

func TestPhaseAssertingNothingIsVisible(t *testing.T) {
	// One silent phase among asserting ones: its zero is a published fact.
	silent := &sabotagePhase{id: "silent", deps: []phase.ID{"base"}}
	rep := run(t, smallRunner(t, phase.NewPipeline(passingPhase("base"), silent)),
		[]phase.Case{plainCase("one")})
	if po := outcomeOf(t, rowOf(t, rep, "one"), "silent"); po.Results != 0 {
		t.Fatalf("silent phase Results = %d, want the loud 0", po.Results)
	}
}

func TestCaseWhereNothingAssertsCannotPass(t *testing.T) {
	// EVERY phase silent: there is no evidence at all, and no evidence is
	// never a pass.
	rep := run(t, smallRunner(t, phase.NewPipeline(
		&sabotagePhase{id: "a"}, &sabotagePhase{id: "b", deps: []phase.ID{"a"}},
	)), []phase.Case{plainCase("one")})
	cr := rowOf(t, rep, "one")
	if cr.Status == phase.Passed {
		t.Fatalf("case = %v over ZERO recorded results — the founding defect at case scope", cr.Status)
	}
	if cr.Reason == "" {
		t.Fatal("the no-evidence outcome must explain itself")
	}
}

func TestUnjustifiedToleranceRefusesToRun(t *testing.T) {
	checkCalls := 0
	ph := &sabotagePhase{id: "impatient", run: func(ctx context.Context, r *phase.Run) error {
		_, err := phase.Tolerate(ctx, r, "", 3, func(context.Context) result.Result {
			checkCalls++
			return result.Compared("x", []bool{true})
		})
		return err
	}}
	rep := run(t, smallRunner(t, phase.NewPipeline(ph)), []phase.Case{plainCase("one")})
	if checkCalls != 0 {
		t.Fatalf("check ran %d time(s) — an unjustified tolerance must refuse BEFORE evaluating", checkCalls)
	}
	if cr := rowOf(t, rep, "one"); cr.Status != phase.Failed {
		t.Fatalf("case = %v, want the refusal recorded as a failure", cr.Status)
	}
}

func TestUnboundedToleranceRefusesToRun(t *testing.T) {
	checkCalls := 0
	ph := &sabotagePhase{id: "unbounded", run: func(ctx context.Context, r *phase.Run) error {
		_, err := phase.Tolerate(ctx, r, "declared flaky", 1, func(context.Context) result.Result {
			checkCalls++
			return result.Compared("x", []bool{true})
		})
		return err
	}}
	rep := run(t, smallRunner(t, phase.NewPipeline(ph)), []phase.Case{plainCase("one")})
	if checkCalls != 0 {
		t.Fatalf("check ran %d time(s) — attempts=1 is not a bounded retry", checkCalls)
	}
	if cr := rowOf(t, rep, "one"); cr.Status != phase.Failed {
		t.Fatalf("case = %v, want the refusal recorded as a failure", cr.Status)
	}
}

func TestLastAttemptPassIsFlakedWithTheFullTrail(t *testing.T) {
	// The Flaked/Failed boundary, exactly at the buzzer: pass on attempt 4
	// of 4. Flaked — never Passed — with every failed attempt kept as an
	// observation and exactly one final judgement.
	calls := 0
	ph := &sabotagePhase{id: "edge", run: func(ctx context.Context, r *phase.Run) error {
		_, err := phase.Tolerate(ctx, r, "heals on the last try", 4, func(context.Context) result.Result {
			calls++
			if calls < 4 {
				return result.Failed("edge check", "not yet").WithExpected(4).WithActual(calls)
			}
			return result.Compared("edge check", []bool{true})
		})
		return err
	}}
	rep := run(t, smallRunner(t, phase.NewPipeline(ph)), []phase.Case{plainCase("one")})
	cr := rowOf(t, rep, "one")
	if cr.Status != phase.Flaked {
		t.Fatalf("case = %v, want Flaked — a buzzer pass is not a plain pass", cr.Status)
	}
	if len(cr.Results) != 1 || !cr.Results[0].Result.Passed {
		t.Fatalf("results = %+v, want exactly the one final judgement", cr.Results)
	}
	var trail int
	for _, ob := range cr.Observations {
		if strings.Contains(ob.Name, "tolerated failure") {
			trail++
		}
	}
	if trail != 3 {
		t.Fatalf("trail = %d, want all 3 failed attempts kept as evidence", trail)
	}
}

// --- the three budget-error shapes must not impersonate each other -------

// The sentinel trap: all three exhaustion shapes satisfy
// errors.Is(ErrBudgetExhausted), so a sentinel-only check would pass even
// if the WRONG one of the three fired. Each test pins the discriminating
// message shape.

func budgetErr(t *testing.T, timing phase.Timing, cond func(context.Context) (int, bool, error)) string {
	t.Helper()
	ph := &sabotagePhase{id: "waiting", run: func(ctx context.Context, r *phase.Run) error {
		_, err := phase.WaitUntil(ctx, r, cond)
		return err
	}}
	r, err := phase.NewRunner(phase.NewPipeline(ph), phase.Config{Defaults: timing})
	if err != nil {
		t.Fatal(err)
	}
	s, err := r.Start(context.Background(), []phase.Case{plainCase("one")})
	if err != nil {
		t.Fatal(err)
	}
	cr := rowOf(t, s.Report(), "one")
	if cr.Status != phase.Errored || len(cr.Errors) == 0 {
		t.Fatalf("case = %v (%d errors), want Errored with the budget on the record", cr.Status, len(cr.Errors))
	}
	return cr.Errors[0].Err
}

func TestAttemptsExhaustionNamesItsBudget(t *testing.T) {
	msg := budgetErr(t, phase.Timing{Attempts: 3, Interval: time.Millisecond},
		func(context.Context) (int, bool, error) { return 0, false, nil })
	if !strings.Contains(msg, "gave up after 3×") {
		t.Fatalf("err = %q, want the attempts×interval budget named", msg)
	}
}

func TestWallClockExpiryBetweenAttemptsSaysAfter(t *testing.T) {
	msg := budgetErr(t,
		phase.Timing{Attempts: 100000, Interval: 20 * time.Millisecond, Timeout: 50 * time.Millisecond},
		func(context.Context) (int, bool, error) { return 0, false, nil })
	if !strings.Contains(msg, "timeout after") || !strings.Contains(msg, "attempt(s)") {
		t.Fatalf("err = %q, want the between-attempts shape (%q)", msg, "exceeded its ... timeout after N attempt(s)")
	}
}

func TestWallClockExpiryMidCallSaysDuring(t *testing.T) {
	msg := budgetErr(t,
		phase.Timing{Attempts: 100000, Interval: time.Millisecond, Timeout: 30 * time.Millisecond},
		func(condCtx context.Context) (int, bool, error) {
			<-condCtx.Done() // block past the deadline, honestly
			return 0, false, condCtx.Err()
		})
	if !strings.Contains(msg, "during attempt") {
		t.Fatalf("err = %q, want the mid-call shape (%q)", msg, "exceeded its ... timeout during attempt N")
	}
}

func TestInterruptedToleranceNamesTheInterruptionNotExhaustion(t *testing.T) {
	// G7, pinned to its literal error text like its siblings: a tolerance
	// cut off mid-retry-sleep is an INTERRUPTION with the actual attempt
	// count — never dressed up as exhaustion ("all N tolerant attempts"),
	// never a fabricated final judgement.
	ph := &sabotagePhase{id: "cut", run: func(ctx context.Context, r *phase.Run) error {
		_, err := phase.Tolerate(ctx, r, "declared flaky", 5, func(context.Context) result.Result {
			return result.Failed("row count", "not yet").WithExpected(1).WithActual(0)
		})
		return err
	}}
	r, err := phase.NewRunner(phase.NewPipeline(ph),
		phase.Config{Defaults: phase.Timing{Attempts: 5, Interval: 200 * time.Millisecond}})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(30 * time.Millisecond) // land inside the first inter-attempt sleep
		cancel()
	}()
	s, err := r.Start(ctx, []phase.Case{plainCase("one")})
	if err != nil {
		t.Fatal(err)
	}
	cr := rowOf(t, s.Report(), "one")
	if cr.Status != phase.Errored || cr.FailedIn != "" {
		t.Fatalf("case = %v (FailedIn %q), want Errored with no product failure", cr.Status, cr.FailedIn)
	}
	if len(cr.Errors) == 0 || !strings.Contains(cr.Errors[0].Err, "interrupted after attempt") {
		t.Fatalf("errors = %+v, want the interruption named with its actual attempt count", cr.Errors)
	}
	for _, ae := range cr.Errors {
		if strings.Contains(ae.Err, "tolerant attempts") {
			t.Fatalf("err = %q — an interruption must never claim exhaustion", ae.Err)
		}
	}
}

// --- PriorEvidence outside the declared dependency scope -----------------

func TestPriorEvidenceOutsideScopeIsAnError(t *testing.T) {
	// The refusal is the ONLY loud thing here: the summary's zero value
	// (Recorded:0, Failing:0) reads exactly like "ran clean" — a consumer
	// who drops the error has silently built a decision on nothing. The
	// framework's half is refusing with the dependency named; the
	// consumer's half is checking the error.
	var scopeErr error
	nosy := &conditionalSabotage{sabotagePhase: sabotagePhase{id: "nosy", deps: []phase.ID{"base"}},
		when: func(_ context.Context, r *phase.Run) (bool, string, error) {
			_, scopeErr = r.PriorEvidence("stranger") // not in DependsOn
			return false, "declined for the demo", nil
		}}
	stranger := passingPhase("stranger")
	rep := run(t, smallRunner(t, phase.NewPipeline(passingPhase("base"), stranger, nosy)),
		[]phase.Case{plainCase("one")})
	if scopeErr == nil || !strings.Contains(scopeErr.Error(), "DependsOn") {
		t.Fatalf("err = %v — the out-of-scope read must be refused with the cure named", scopeErr)
	}
	_ = rep
}
