// Copyright 2026 The Phase Contributors
// SPDX-License-Identifier: MIT

package phase

import (
	"context"
	"strings"
	"testing"

	"github.com/wow-qe/phase-go/result"
)

// Containment and refusal guarantees.

func TestObserverPanicIsContainedAndSurfaced(t *testing.T) {
	// A panicking consumer callback is contained: the run completes, and
	// the degradation is loud - on the session and in the report - never
	// silently detached.
	r := mustRunner(t, Config{Defaults: validTiming()}, passingPhase("submit", nil))
	WithProgress(func(ProgressEvent) { panic("dashboard bug") })(r)
	WithCaseObserver(func(CaseReport) { panic("poster bug") })(r)
	s := startSession(t, r, &stubCase{id: "one"}, &stubCase{id: "two"})
	if len(s.Cases()) != 2 {
		t.Fatal("a panicking observer must not take down the batch")
	}
	for _, cr := range s.Cases() {
		if cr.Status != Passed {
			t.Fatalf("%s = %s — observer trouble must not alter verdicts", cr.CaseID, cr.Status)
		}
	}
	if len(s.ObserverErrors()) == 0 {
		t.Fatal("degraded observability must be surfaced, not swallowed")
	}
	rep := s.Report()
	found := false
	for _, line := range rep.Diagnostics {
		if strings.Contains(line, "observer") {
			found = true
		}
	}
	if !found {
		t.Fatalf("the report must say live observability was degraded; diagnostics = %v", rep.Diagnostics)
	}
}

func TestHookPutIsRestrictedToDeclaredProduces(t *testing.T) {
	// A Before hook must not Put a key the phase never declared.
	key := smuggleKey
	r := mustRunner(t, Config{Defaults: validTiming()},
		&hookedPhase{stubPhase: stubPhase{id: "smuggler"}, // declares NO Produces
			before: func(_ context.Context, run *Run) error {
				Put(run, key, "contraband")
				return nil
			},
			do: func(_ context.Context, run *Run) error {
				run.Record(result.Compared("ok", []bool{true}))
				return nil
			},
		},
	)
	cr := caseReport(t, startSession(t, r, &stubCase{id: "one"}), "one")
	if cr.Status != Errored {
		t.Fatalf("status = %s — an undeclared Put from a hook must be the same framework violation it is from Run", cr.Status)
	}
}

func TestWhenRecordIsRefusedNotFolded(t *testing.T) {
	// Refusal is preventive, not corrective: a condition's record never
	// lands in the evidence at all - a folded-but-counted phantom would be
	// indistinguishable from real Run evidence.
	r := mustRunner(t, Config{Defaults: validTiming()},
		&conditionalPhase{stubPhase: stubPhase{id: "leaky"},
			when: func(_ context.Context, run *Run) (bool, string, error) {
				run.Record(result.Failed("phantom", "recorded from a condition"))
				return false, "declining anyway", nil
			},
		},
	)
	cr := caseReport(t, startSession(t, r, &stubCase{id: "one"}), "one")
	po := phaseOutcome(t, cr, "leaky")
	if po.Status != Errored || po.Stage != "condition" {
		t.Fatalf("outcome = %+v", po)
	}
	if po.Results != 0 {
		t.Fatalf("results_recorded = %d — the phantom must never exist, not be relabeled", po.Results)
	}
	for _, ar := range cr.Results {
		if ar.Result.Name == "phantom" {
			t.Fatal("the phantom result reached the evidence")
		}
	}
	if cr.FailedIn != "" {
		t.Fatalf("FailedIn = %q — a refused record must not fail the case", cr.FailedIn)
	}
}

func TestNegativeConcurrencyKnobsAreRefused(t *testing.T) {
	// The same load-time discipline TimingInvalid applies to intervals.
	for _, cfg := range []Config{
		{Defaults: validTiming(), MaxPhaseConcurrency: -1},
		{Defaults: validTiming(), MaxCaseConcurrency: -2},
	} {
		if _, err := NewRunner(NewPipeline(&stubPhase{id: "a"}), cfg); err == nil {
			t.Fatalf("negative concurrency %+v accepted — a nonsense knob must refuse at load", cfg)
		}
	}
}

var smuggleKey = Declare[string]("smuggle_key")
