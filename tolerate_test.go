// Copyright 2026 The Phase Contributors
// SPDX-License-Identifier: MIT

package phase

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/wow-qe/phase-go/result"
)

// Flaked was a vocabulary word with no producer — declared, counted and
// Verify-guarded, assigned nowhere. Tolerate is the producer, built to
// The four clauses: a stated reason, a bounded retry, every attempt
// recorded as evidence, and "passed on attempt 3" never laundered into
// "passed".

func fastTolerantTiming() Timing {
	return Timing{Attempts: 3, Interval: time.Millisecond, Timeout: time.Minute}
}

func tolerantPhase(id ID, reason string, attempts int, check func(context.Context) result.Result) Interface {
	return &recordingPhase{stubPhase: stubPhase{id: id}, do: func(ctx context.Context, run *Run) error {
		_, err := Tolerate(ctx, run, reason, attempts, check)
		return err
	}}
}

func TestTolerateCleanFirstPassIsJustAPass(t *testing.T) {
	calls := 0
	r := mustRunner(t, Config{Defaults: fastTolerantTiming()},
		tolerantPhase("settle", "provider is eventually consistent", 3, func(context.Context) result.Result {
			calls++
			return result.Compared("row count", []bool{true})
		}),
	)
	cr := caseReport(t, startSession(t, r, &stubCase{id: "steady"}), "steady")
	if cr.Status != Passed {
		t.Fatalf("status = %s, want Passed — a first-attempt pass is not a flake", cr.Status)
	}
	if calls != 1 {
		t.Fatalf("check ran %d times, want 1", calls)
	}
	if len(cr.Observations) != 0 {
		t.Fatalf("a clean pass must leave no tolerance trail; observations = %v", cr.Observations)
	}
}

func TestToleratePassOnRetryIsFlakedWithTheTrail(t *testing.T) {
	calls := 0
	r := mustRunner(t, Config{Defaults: fastTolerantTiming()},
		tolerantPhase("settle", "provider is eventually consistent", 3, func(context.Context) result.Result {
			calls++
			if calls < 3 {
				return result.Failed("row count", "saw 0 rows, expected 1")
			}
			return result.Compared("row count", []bool{true})
		}),
	)
	cr := caseReport(t, startSession(t, r, &stubCase{id: "eventually"}), "eventually")
	if cr.Status != Flaked {
		t.Fatalf("status = %s, want Flaked — passed only on attempt 3", cr.Status)
	}
	if !strings.Contains(cr.Reason, "attempt 3") {
		t.Fatalf("reason = %q, want it to say which attempt passed", cr.Reason)
	}
	// The final judgement is a PASSING result (Verify's Flaked rule: evidence
	// of passing, no failing comparisons)...
	if len(cr.Results) != 1 || !cr.Results[0].Result.Passed {
		t.Fatalf("results = %+v, want exactly the final passing result", cr.Results)
	}
	// ...and the failed attempts are evidence, not verdicts: observations.
	if len(cr.Observations) != 2 {
		t.Fatalf("observations = %v, want one per failed attempt", cr.Observations)
	}
	for i, ob := range cr.Observations {
		if !strings.Contains(ob.Name, "tolerated") {
			t.Fatalf("observation %d = %+v, want it named as a tolerated attempt", i, ob)
		}
	}
}

func TestTolerateExhaustedRecordsTheFinalFailure(t *testing.T) {
	calls := 0
	r := mustRunner(t, Config{Defaults: fastTolerantTiming()},
		tolerantPhase("settle", "provider is eventually consistent", 3, func(context.Context) result.Result {
			calls++
			return result.Failed("row count", "saw 0 rows, expected 1")
		}),
	)
	cr := caseReport(t, startSession(t, r, &stubCase{id: "hopeless"}), "hopeless")
	if cr.Status != Failed {
		t.Fatalf("status = %s, want Failed — tolerance is bounded", cr.Status)
	}
	if calls != 3 {
		t.Fatalf("check ran %d times, want the declared 3", calls)
	}
	if cr.FailedIn != "settle" {
		t.Fatalf("FailedIn = %q, want settle", cr.FailedIn)
	}
	if !strings.Contains(cr.Results[len(cr.Results)-1].Result.Reason, "3 tolerant attempt") {
		t.Fatalf("final failure = %+v, want it to name the exhausted tolerance", cr.Results)
	}
}

func TestTolerateWithoutAReasonRefusesToRun(t *testing.T) {
	// Tolerant comes WITH a stated reason. A tolerance nobody justified
	// is a silent flake-swallower; refuse it as a failing result, loudly.
	calls := 0
	r := mustRunner(t, Config{Defaults: fastTolerantTiming()},
		tolerantPhase("settle", "", 3, func(context.Context) result.Result {
			calls++
			return result.Compared("row count", []bool{true})
		}),
	)
	cr := caseReport(t, startSession(t, r, &stubCase{id: "unjustified"}), "unjustified")
	if cr.Status != Failed || calls != 0 {
		t.Fatalf("status = %s calls = %d, want Failed without running the check", cr.Status, calls)
	}
}

func TestTolerateWithoutABoundRefusesToRun(t *testing.T) {
	// attempts < 2 is either meaningless (1 = no tolerance) or unbounded
	// ambition (0, negative). Both are misdeclarations, not defaults to clamp.
	calls := 0
	r := mustRunner(t, Config{Defaults: fastTolerantTiming()},
		tolerantPhase("settle", "flaky provider", 1, func(context.Context) result.Result {
			calls++
			return result.Compared("row count", []bool{true})
		}),
	)
	cr := caseReport(t, startSession(t, r, &stubCase{id: "unbounded"}), "unbounded")
	if cr.Status != Failed || calls != 0 {
		t.Fatalf("status = %s calls = %d, want Failed without running the check", cr.Status, calls)
	}
}

func TestARealFailureOutranksAFlake(t *testing.T) {
	// Derivation order: a completed failing comparison in another phase still
	// fails the case; the flake does not soften it.
	calls := 0
	r := mustRunner(t, Config{Defaults: fastTolerantTiming()},
		tolerantPhase("settle", "eventually consistent", 2, func(context.Context) result.Result {
			calls++
			if calls < 2 {
				return result.Failed("row count", "saw 0")
			}
			return result.Compared("row count", []bool{true})
		}),
		&recordingPhase{stubPhase: stubPhase{id: "verify", deps: []ID{"settle"}}, do: func(_ context.Context, run *Run) error {
			run.Record(result.Failed("state", "expected frozen, saw open"))
			return nil
		}},
	)
	cr := caseReport(t, startSession(t, r, &stubCase{id: "mixed"}), "mixed")
	if cr.Status != Failed {
		t.Fatalf("status = %s, want Failed — a real failure outranks a flake", cr.Status)
	}
}

func TestCancellationMidToleranceIsErroredNotFailed(t *testing.T) {
	// Cancellation during the retry
	// loop recorded a fabricated final failing result, so an operator kill
	// (CI timeout, deploy interrupt) read as a product defect — inverting
	// "cancellation is Errored, never Failed". An interrupted tolerance is
	// not a concluded one: no attempt's failure carries verdict weight until
	// the budget finishes adjudicating.
	ctx, cancel := context.WithCancel(context.Background())
	calls := 0
	r := mustRunner(t, Config{Defaults: fastTolerantTiming()},
		tolerantPhase("settle", "eventually consistent", 5, func(context.Context) result.Result {
			calls++
			cancel() // the kill lands during the first inter-attempt sleep
			return result.Failed("row count", "saw 0 rows, expected 1")
		}),
	)
	s, err := r.Start(ctx, []Case{&stubCase{id: "interrupted"}})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	cr := caseReport(t, s, "interrupted")
	if cr.Status != Errored {
		t.Fatalf("status = %s, want Errored — an operator kill is not a product defect", cr.Status)
	}
	if cr.FailedIn != "" {
		t.Fatalf("FailedIn = %q — no phase completed a failing judgement", cr.FailedIn)
	}
	if calls != 1 {
		t.Fatalf("calls = %d, want 1 — cancellation stops the loop", calls)
	}
	// The evidence is not dropped: the interrupted attempt and the
	// interruption itself are on the record, with the ACTUAL attempt count.
	var names []string
	for _, ob := range cr.Observations {
		names = append(names, ob.Name)
	}
	joined := strings.Join(names, " | ")
	if !strings.Contains(joined, "attempt 1 of 5") || !strings.Contains(joined, "interrupted") {
		t.Fatalf("observations = %q, want the attempt trail and the interruption named", joined)
	}
	if strings.Contains(joined, "5 tolerant attempts") {
		t.Fatalf("observations = %q — must not claim the full budget ran", joined)
	}
}

func TestAFlakedCaseSurvivesVerifyAndPassesCI(t *testing.T) {
	calls := 0
	r := mustRunner(t, Config{Defaults: fastTolerantTiming()},
		tolerantPhase("settle", "eventually consistent", 2, func(context.Context) result.Result {
			calls++
			if calls < 2 {
				return result.Failed("row count", "saw 0")
			}
			return result.Compared("row count", []bool{true})
		}),
	)
	rep := startSession(t, r, &stubCase{id: "eventually"}).Report()
	if err := rep.Verify(); err != nil {
		t.Fatalf("a producer-made Flaked case must satisfy Verify's Flaked rule: %v", err)
	}
	if rep.Summary.Flaked != 1 {
		t.Fatalf("summary = %+v, want Flaked:1", rep.Summary)
	}
	if got := rep.ExitCode(); got != 0 {
		t.Fatalf("exit = %d, want 0 — flakes are surfaced, not CI failures (exit-code contract)", got)
	}
}
