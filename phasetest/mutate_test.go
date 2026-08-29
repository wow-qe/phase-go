// Copyright 2026 The Phase Contributors
// SPDX-License-Identifier: MIT

package phasetest_test

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	phase "github.com/wow-qe/phase-go"
	"github.com/wow-qe/phase-go/phasetest"
	"github.com/wow-qe/phase-go/result"
)

func TestGuttedPreservesIdentityAndWiring(t *testing.T) {
	inner := phase.Func{
		PhaseID: "assert-totals",
		Deps:    []phase.ID{"discover", "settle"},
		Gets:    []phase.KeyID{"discovered_total"},
		Do: func(ctx context.Context, r *phase.Run) error {
			t.Fatal("Gutted must never invoke the wrapped phase's Run")
			return nil
		},
	}

	g := phasetest.Gutted(inner)

	if g.ID() != inner.ID() {
		t.Fatalf("ID() = %q, want %q", g.ID(), inner.ID())
	}
	if !reflect.DeepEqual(g.DependsOn(), inner.DependsOn()) {
		t.Fatalf("DependsOn() = %v, want %v", g.DependsOn(), inner.DependsOn())
	}
	if !reflect.DeepEqual(g.Requires(), inner.Requires()) {
		t.Fatalf("Requires() = %v, want %v", g.Requires(), inner.Requires())
	}
	if len(g.Produces()) != 0 {
		t.Fatalf("Produces() = %v, want empty", g.Produces())
	}
}

func TestGuttedRunRecordsNothing(t *testing.T) {
	ran := false
	g := phasetest.Gutted(phase.Func{
		PhaseID: "assert-totals",
		Do: func(ctx context.Context, r *phase.Run) error {
			ran = true
			return errors.New("the wrapped phase must never run")
		},
	})

	c := &stubCase{id: "case-1"}
	run := phase.NewRunForTesting(c, phase.WithPhase("assert-totals", phase.Timing{}))

	err := g.Run(context.Background(), run)
	if err != nil {
		t.Fatalf("Gutted phase Run() = %v, want nil", err)
	}
	if ran {
		t.Fatal("Gutted phase invoked the wrapped Run — it must record nothing and do nothing")
	}
}

func TestGuttedRefusesAProducer(t *testing.T) {
	inner := phase.Func{
		PhaseID: "discover",
		Puts:    []phase.KeyID{"discovered_total"},
	}
	defer func() {
		if recover() == nil {
			t.Fatal("Gutted must panic when the wrapped phase Produces() something")
		}
	}()
	phasetest.Gutted(inner)
}

// --- AlwaysPass: the second mutation gate -------------------------------

func TestAlwaysPassPreservesIdentityAndWiring(t *testing.T) {
	inner := phase.Func{
		PhaseID: "assert-totals",
		Deps:    []phase.ID{"discover"},
		Gets:    []phase.KeyID{"discovered_total"},
		Puts:    []phase.KeyID{"asserted_total"},
	}
	a := phasetest.AlwaysPass(inner)
	if a.ID() != inner.ID() {
		t.Fatalf("ID() = %q, want %q", a.ID(), inner.ID())
	}
	if !reflect.DeepEqual(a.DependsOn(), inner.DependsOn()) {
		t.Fatalf("DependsOn() = %v", a.DependsOn())
	}
	// Unlike Gutted, AlwaysPass runs the phase — its wiring must survive
	// intact, Produces included, or preflight would refuse the mutated
	// pipeline that the un-mutated one accepts.
	if !reflect.DeepEqual(a.Produces(), inner.Produces()) {
		t.Fatalf("Produces() = %v, want %v", a.Produces(), inner.Produces())
	}
	if !reflect.DeepEqual(a.Requires(), inner.Requires()) {
		t.Fatalf("Requires() = %v", a.Requires())
	}
}

// alwaysPassPipeline runs a two-phase pipeline where "checks" records one
// failing result, with the given wrapper applied to it, and returns the
// case's report.
func alwaysPassSession(t *testing.T, wrap func(phase.Interface) phase.Interface) phase.CaseReport {
	t.Helper()
	checks := phase.Interface(phase.Func{
		PhaseID: "checks",
		Do: func(ctx context.Context, r *phase.Run) error {
			r.Record(result.Failed("state mismatch", "expected 8, saw 9").WithExpected(8).WithActual(9))
			return nil
		},
	})
	if wrap != nil {
		checks = wrap(checks)
	}
	other := phase.Func{
		PhaseID: "other",
		Do: func(ctx context.Context, r *phase.Run) error {
			r.Record(result.Compared("other check", []bool{true}))
			return nil
		},
	}
	p := phase.NewPipeline(checks, other)
	runner, err := phase.NewRunner(p, phase.Config{Defaults: phase.Timing{Attempts: 1, Interval: time.Millisecond}})
	if err != nil {
		t.Fatalf("NewRunner: %v", err)
	}
	s, err := runner.Start(context.Background(), []phase.Case{&stubCase{id: "case-1"}})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	for _, cr := range s.Cases() {
		if cr.CaseID == "case-1" {
			return cr
		}
	}
	t.Fatal("case-1 missing from session")
	return phase.CaseReport{}
}

func TestAlwaysPassFlipsTheWrappedPhasesFailure(t *testing.T) {
	// The gate's meaning: if wrapping one phase in AlwaysPass flips a failing
	// case green, that case's verdict was determined by that phase's
	// comparisons.
	if cr := alwaysPassSession(t, nil); cr.Status != phase.Failed {
		t.Fatalf("unwrapped: status = %s, want Failed", cr.Status)
	}
	cr := alwaysPassSession(t, phasetest.AlwaysPass)
	if cr.Status != phase.Passed {
		t.Fatalf("wrapped: status = %s, want Passed — every result the wrapped phase records must read as a pass", cr.Status)
	}
	// The flip preserves the result's identity and evidence for the reader.
	for _, ar := range cr.Results {
		if ar.Phase == "checks" {
			if !ar.Result.Passed || ar.Result.Name != "state mismatch" {
				t.Fatalf("flipped result = %+v", ar.Result)
			}
		}
	}
}

func TestAlwaysPassDoesNotLeakToOtherPhases(t *testing.T) {
	// Interception is bound to the wrapped phase's own recording handle: a
	// different phase's failing result must still fail the case.
	checksPasses := phase.Func{
		PhaseID: "checks",
		Do: func(ctx context.Context, r *phase.Run) error {
			r.Record(result.Failed("flipped anyway", "gutcheck"))
			return nil
		},
	}
	failingOther := phase.Func{
		PhaseID: "other",
		Do: func(ctx context.Context, r *phase.Run) error {
			r.Record(result.Failed("real failure elsewhere", "expected 1, saw 0"))
			return nil
		},
	}
	p := phase.NewPipeline(phasetest.AlwaysPass(checksPasses), failingOther)
	runner, err := phase.NewRunner(p, phase.Config{Defaults: phase.Timing{Attempts: 1, Interval: time.Millisecond}})
	if err != nil {
		t.Fatalf("NewRunner: %v", err)
	}
	s, err := runner.Start(context.Background(), []phase.Case{&stubCase{id: "case-1"}})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	cr := s.Cases()[0]
	if cr.Status != phase.Failed || cr.FailedIn != "other" {
		t.Fatalf("status = %s, FailedIn = %q — the interception must not leak past its phase", cr.Status, cr.FailedIn)
	}
}

// --- hooks and the mutation gates ---------------------------------------

type hookedFunc struct {
	phase.Func
	beforeRan, afterRan *bool
	beforeRecords       bool
}

func (h hookedFunc) Before(ctx context.Context, r *phase.Run) error {
	*h.beforeRan = true
	if h.beforeRecords {
		r.Record(result.Failed("hook check", "recorded by before"))
	}
	return nil
}

func (h hookedFunc) After(ctx context.Context, r *phase.Run, _ phase.PhaseOutcome) error {
	*h.afterRan = true
	return nil
}

func TestGuttedNeutralisesHooks(t *testing.T) {
	// A phase whose hooks still ran would read as "fully gutted" while its
	// checks fire — the mutation gate would misreport what was removed.
	b, a := false, false
	g := phasetest.Gutted(hookedFunc{
		Func:      phase.Func{PhaseID: "checks"},
		beforeRan: &b, afterRan: &a, beforeRecords: true,
	})
	if _, ok := g.(phase.BeforeHook); ok {
		t.Fatal("Gutted must not surface the wrapped phase's BeforeHook")
	}
	if _, ok := g.(phase.AfterHook); ok {
		t.Fatal("Gutted must not surface the wrapped phase's AfterHook")
	}
	run := phase.NewRunForTesting(&stubCase{id: "c"}, phase.WithPhase("checks", phase.Timing{}))
	_ = g.Run(context.Background(), run)
	if b || a {
		t.Fatal("gutted hooks ran")
	}
}

func TestAlwaysPassForwardsHooksWithInterception(t *testing.T) {
	// AlwaysPass runs the phase for real — hooks included — and its flip
	// must catch hook-recorded results too, or a Before-recorded failure
	// escapes the mutation.
	b, a := false, false
	wrapped := phasetest.AlwaysPass(hookedFunc{
		Func:      phase.Func{PhaseID: "checks"},
		beforeRan: &b, afterRan: &a, beforeRecords: true,
	})
	p := phase.NewPipeline(wrapped)
	runner, err := phase.NewRunner(p, phase.Config{Defaults: phase.Timing{Attempts: 1, Interval: time.Millisecond}})
	if err != nil {
		t.Fatal(err)
	}
	s, err := runner.Start(context.Background(), []phase.Case{&stubCase{id: "c"}})
	if err != nil {
		t.Fatal(err)
	}
	cr := s.Cases()[0]
	if !b || !a {
		t.Fatalf("hooks must run through AlwaysPass: before=%v after=%v", b, a)
	}
	if cr.Status != phase.Passed {
		t.Fatalf("status = %s — the Before-recorded failure escaped the flip", cr.Status)
	}
}

func TestInvokePhaseRunsTheFullArc(t *testing.T) {
	b, a := false, false
	ph := hookedFunc{
		Func: phase.Func{PhaseID: "checks", Do: func(ctx context.Context, r *phase.Run) error {
			r.Record(result.Compared("ok", []bool{true}))
			return nil
		}},
		beforeRan: &b, afterRan: &a,
	}
	run := phase.NewRunForTesting(&stubCase{id: "c"}, phase.WithPhase("checks", phase.Timing{Attempts: 1}))
	po := phasetest.InvokePhase(context.Background(), ph, run)
	if !b || !a {
		t.Fatalf("InvokePhase must run Before and After: %v %v", b, a)
	}
	if po.Status != phase.Passed || po.ID != "checks" {
		t.Fatalf("outcome = %+v", po)
	}
}
