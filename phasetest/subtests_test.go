// Copyright 2026 The Phase Contributors
// SPDX-License-Identifier: MIT

package phasetest_test

import (
	"context"
	"strings"
	"testing"
	"time"

	phase "github.com/wow-qe/phase-go"
	"github.com/wow-qe/phase-go/phasetest"
	"github.com/wow-qe/phase-go/result"
)

// Without this bridge, one Start() collapses N cases into one Go test result — `go
// test -run` could not target a case and JUnit saw "1 test". RunAsSubtests
// bridges each CaseReport to its own subtest; ReportCaseOutcome is the
// bridge's per-case verdict logic, exported so it is testable against a
// fake TB (a failing case inside a real subtest would fail this very test).

func mixedOutcomeSession(t *testing.T) *phase.Session {
	t.Helper()
	p := phase.NewPipeline(
		phase.Func{PhaseID: "checks", Do: func(ctx context.Context, r *phase.Run) error {
			switch r.Case().ID() {
			case "sad":
				r.Record(result.Failed("state mismatch", "expected 8, saw 9"))
			case "broken":
				return context.DeadlineExceeded
			default:
				r.Record(result.Compared("ok", []bool{true}))
			}
			return nil
		}},
	)
	runner, err := phase.NewRunner(p, phase.Config{Defaults: phase.Timing{Attempts: 1, Interval: time.Millisecond}})
	if err != nil {
		t.Fatalf("NewRunner: %v", err)
	}
	s, err := runner.Start(context.Background(), []phase.Case{
		&stubCase{id: "happy"},
		&stubCase{id: "sad"},
		&stubCase{id: "broken"},
		&stubCase{id: "parked", status: phase.Quarantined},
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	return s
}

func outcomeFor(t *testing.T, s *phase.Session, id string) *fakeTB {
	t.Helper()
	for _, cr := range s.Cases() {
		if cr.CaseID == id {
			tb := &fakeTB{}
			func() {
				defer func() {
					if r := recover(); r != nil {
						if _, ok := r.(fakeFatal); !ok {
							panic(r)
						}
					}
				}()
				phasetest.ReportCaseOutcome(tb, cr)
			}()
			return tb
		}
	}
	t.Fatalf("case %s missing", id)
	return nil
}

func TestSubtestBridgeFailsAFailedCaseWithTheEvidence(t *testing.T) {
	tb := outcomeFor(t, mixedOutcomeSession(t), "sad")
	if len(tb.errs) == 0 {
		t.Fatal("a Failed case must fail its subtest")
	}
	joined := strings.Join(tb.errs, " | ")
	for _, want := range []string{"state mismatch", "expected 8, saw 9", "checks"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("subtest failure %q missing %q — the evidence must reach the go test output", joined, want)
		}
	}
}

func TestSubtestBridgeErrorsAnErroredCase(t *testing.T) {
	tb := outcomeFor(t, mixedOutcomeSession(t), "broken")
	if len(tb.errs) == 0 {
		t.Fatal("an Errored case must fail its subtest")
	}
	if !strings.Contains(strings.Join(tb.errs, " "), "deadline") {
		t.Fatalf("the error evidence must be in the output: %v", tb.errs)
	}
}

func TestSubtestBridgeSkipsASkippedCaseWithTheReason(t *testing.T) {
	tb := outcomeFor(t, mixedOutcomeSession(t), "parked")
	if !tb.skipped {
		t.Fatal("a Skipped case must skip its subtest, not pass it")
	}
	if !strings.Contains(strings.Join(tb.logs, " "), "quarantined") {
		t.Fatalf("the skip reason must be visible: %v", tb.logs)
	}
}

func TestSubtestBridgePassesAPassingCaseSilently(t *testing.T) {
	tb := outcomeFor(t, mixedOutcomeSession(t), "happy")
	if len(tb.errs) != 0 || tb.skipped {
		t.Fatalf("a Passed case must pass: errs=%v skipped=%v", tb.errs, tb.skipped)
	}
}

func TestRunAsSubtestsCreatesOneSubtestPerCase(t *testing.T) {
	// The real *testing.T path, exercised on a session with no failing cases
	// (a failing case would fail this test run — that path is covered above
	// through the fake).
	p := phase.NewPipeline(phase.Func{PhaseID: "checks", Do: func(ctx context.Context, r *phase.Run) error {
		r.Record(result.Compared("ok", []bool{true}))
		return nil
	}})
	runner, err := phase.NewRunner(p, phase.Config{Defaults: phase.Timing{Attempts: 1, Interval: time.Millisecond}})
	if err != nil {
		t.Fatalf("NewRunner: %v", err)
	}
	s, err := runner.Start(context.Background(), []phase.Case{&stubCase{id: "one"}, &stubCase{id: "two"}})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	phasetest.RunAsSubtests(t, s)
}
