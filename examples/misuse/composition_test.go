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

// Composition sabotage: the seams between subsystems, where every real
// defect of this program's history actually lived. Each test seeds one
// misuse of a Run handle in a stage that forbids it, or one crossing of
// two features nobody crossed before, and pins the documented answer.

// conditionalSabotage adds a When gate to a sabotagePhase — a separate
// type, so plain sabotagePhases never accidentally become conditional.
type conditionalSabotage struct {
	sabotagePhase
	when func(context.Context, *phase.Run) (bool, string, error)
}

func (p *conditionalSabotage) When(ctx context.Context, r *phase.Run) (bool, string, error) {
	return p.when(ctx, r)
}

// wonkyLifecycle is a Fixture whose two halves the tests script freely.
type wonkyLifecycle struct {
	setup    func(context.Context, *phase.Run) error
	teardown func(context.Context, *phase.Run) error
}

func (f *wonkyLifecycle) Setup(ctx context.Context, r *phase.Run) error {
	if f.setup != nil {
		return f.setup(ctx, r)
	}
	return nil
}

func (f *wonkyLifecycle) Teardown(ctx context.Context, r *phase.Run) error {
	if f.teardown != nil {
		return f.teardown(ctx, r)
	}
	return nil
}

func passingPhase(id phase.ID, deps ...phase.ID) *sabotagePhase {
	return &sabotagePhase{id: id, deps: deps, run: func(_ context.Context, r *phase.Run) error {
		r.Record(result.Compared(string(id)+" ok", []bool{true}))
		return nil
	}}
}

func plainCase(id string) *sabotageCase { return &sabotageCase{id: id} }

func smallRunner(t *testing.T, p *phase.Pipeline, opts ...phase.RunnerOption) *phase.Runner {
	t.Helper()
	r, err := phase.NewRunner(p, sane(), opts...)
	if err != nil {
		t.Fatalf("NewRunner: %v", err)
	}
	return r
}

// --- A condition may read the record, never write it ---------------------

func TestWhenWritingIsAViolationInBothDirections(t *testing.T) {
	// Both directions: a When that misuses the handle and then claims
	// "run me", and one that claims "decline me". The violation must win
	// over the condition's own answer BOTH times.
	for name, when := range map[string]func(context.Context, *phase.Run) (bool, string, error){
		"records then approves": func(_ context.Context, r *phase.Run) (bool, string, error) {
			r.Record(result.Compared("smuggled", []bool{true}))
			return true, "", nil
		},
		"puts then declines": func(_ context.Context, r *phase.Run) (bool, string, error) {
			phase.Put(r, StreamCursor, "hijack")
			return false, "looks fine, skip me", nil
		},
	} {
		t.Run(name, func(t *testing.T) {
			gated := &conditionalSabotage{sabotagePhase: sabotagePhase{id: "gated", deps: []phase.ID{"base"}}, when: when}
			rep := run(t, smallRunner(t, phase.NewPipeline(passingPhase("base"), gated)),
				[]phase.Case{plainCase("one")})
			po := outcomeOf(t, rowOf(t, rep, "one"), "gated")
			if po.Status != phase.Errored || po.Stage != phase.StageCondition {
				t.Fatalf("gated = %+v — the capability violation must override the condition's own answer", po)
			}
			if !strings.Contains(po.Reason, "capability") {
				t.Fatalf("reason = %q, want the violation named", po.Reason)
			}
		})
	}
}

// --- teardown stages may not Put -----------------------------------------

func TestGroupTeardownPutFailsTheGroupRowItself(t *testing.T) {
	// Proven from outside the engine: the lifecycle's own
	// Teardown returns nil, but the smuggled Put must fail the GROUP ROW
	// independently — never a Passed group row beside a quiet violation.
	g := phase.Group{ID: "g", Members: []phase.ID{"m"}, Lifecycle: &wonkyLifecycle{
		teardown: func(_ context.Context, r *phase.Run) error {
			phase.Put(r, StreamCursor, "too late")
			return nil // the consumer's own code claims success
		},
	}}
	rep := run(t, smallRunner(t, phase.NewPipeline(passingPhase("m")).Group(g)),
		[]phase.Case{plainCase("one")})
	cr := rowOf(t, rep, "one")
	if len(cr.Groups) != 1 || cr.Groups[0].Status != phase.Errored {
		t.Fatalf("groups = %+v — the teardown violation must fail the group's own row", cr.Groups)
	}
	if cr.Groups[0].Reason == "" {
		t.Fatal("the failed group row must say why")
	}
	if cr.Status == phase.Passed {
		t.Fatalf("case = %v — a case cannot be Passed over a violated teardown", cr.Status)
	}
}

func TestFixtureTeardownPutIsDenied(t *testing.T) {
	c := &sabotageCase{id: "one", fixtures: []phase.Fixture{&wonkyLifecycle{
		teardown: func(_ context.Context, r *phase.Run) error {
			phase.Put(r, StreamCursor, "nothing downstream can read this")
			return nil
		},
	}}}
	rep := run(t, smallRunner(t, phase.NewPipeline(passingPhase("m"))), []phase.Case{c})
	cr := rowOf(t, rep, "one")
	if cr.Status == phase.Passed {
		t.Fatalf("case = %v, want the fixture-teardown violation on the verdict", cr.Status)
	}
	var named bool
	for _, ae := range cr.Errors {
		if strings.Contains(ae.Err, "fixture teardown") {
			named = true
		}
	}
	if !named {
		t.Fatalf("errors = %+v, want the denied stage named", cr.Errors)
	}
}

// --- Put discipline inside the execution stage ---------------------------

func TestUndeclaredPutIsARecordedViolation(t *testing.T) {
	smuggler := &sabotagePhase{id: "smuggler", run: func(_ context.Context, r *phase.Run) error {
		phase.Put(r, StreamCursor, "undeclared") // not in Produces()
		r.Record(result.Compared("work", []bool{true}))
		return nil
	}}
	rep := run(t, smallRunner(t, phase.NewPipeline(smuggler)), []phase.Case{plainCase("one")})
	cr := rowOf(t, rep, "one")
	if cr.Status != phase.Errored {
		t.Fatalf("case = %v — an undeclared Put must not pass quietly", cr.Status)
	}
	var named bool
	for _, ae := range cr.Errors {
		if strings.Contains(ae.Err, "Produces") {
			named = true
		}
	}
	if !named {
		t.Fatalf("errors = %+v, want the Produces mismatch named", cr.Errors)
	}
}

func TestSecondPutOfOneKeyIsAViolation(t *testing.T) {
	greedy := &sabotagePhase{id: "greedy", produces: phase.Keys(OrderID),
		run: func(_ context.Context, r *phase.Run) error {
			phase.Put(r, OrderID, "first")
			phase.Put(r, OrderID, "second")
			r.Record(result.Compared("work", []bool{true}))
			return nil
		}}
	rep := run(t, smallRunner(t, phase.NewPipeline(greedy)), []phase.Case{plainCase("one")})
	cr := rowOf(t, rep, "one")
	if cr.Status != phase.Errored {
		t.Fatalf("case = %v — one writer per key holds at runtime too", cr.Status)
	}
}

// --- the Before two-fact split, sabotaged --------------------------------

func TestBeforeRecordingOnlyPassingEvidenceStillErrors(t *testing.T) {
	// A hook that records something irrelevant (passing) and THEN hits real
	// trouble: the passing record must not satisfy the two-fact probe — the
	// error still lands on the environment channel, and the recorded
	// evidence survives.
	hooked := &beforeSabotage{sabotagePhase: sabotagePhase{id: "hooked"},
		before: func(_ context.Context, r *phase.Run) error {
			r.Record(result.Compared("irrelevant probe", []bool{true}))
			return context.DeadlineExceeded
		}}
	rep := run(t, smallRunner(t, phase.NewPipeline(hooked)), []phase.Case{plainCase("one")})
	cr := rowOf(t, rep, "one")
	po := outcomeOf(t, cr, "hooked")
	if po.Status != phase.Errored || po.Stage != phase.StageBefore {
		t.Fatalf("hooked = %+v, want Errored in before", po)
	}
	if len(cr.Errors) == 0 {
		t.Fatal("a passing record must not suppress the environment error")
	}
	if len(cr.Results) != 1 || !cr.Results[0].Result.Passed {
		t.Fatalf("results = %+v — recorded evidence survives even when the phase errors", cr.Results)
	}
}

type beforeSabotage struct {
	sabotagePhase
	before func(context.Context, *phase.Run) error
}

func (p *beforeSabotage) Before(ctx context.Context, r *phase.Run) error { return p.before(ctx, r) }

// --- cancellation racing the settlement machinery ------------------------

func TestCancellationMidSettleIsErroredNeverFailed(t *testing.T) {
	sys, cases := misuseSuite(t)
	sys.neverSettle = true // park the case inside settle_wait's poll loop
	cfg := sane()
	cfg.Defaults.Attempts = 100000
	r := mustMisuseRunner(t, sys, cfg)

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(20 * time.Millisecond)
		cancel()
	}()
	s, err := r.Start(ctx, onlyCase(t, cases, "happy-single"))
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	rep := s.Report()
	if verr := rep.Verify(); verr != nil {
		t.Fatalf("Verify after cancellation: %v", verr)
	}
	cr := rowOf(t, rep, "happy-single")
	if cr.Status != phase.Errored || cr.FailedIn != "" {
		t.Fatalf("case = %v (FailedIn %q) — an interruption is never a product failure", cr.Status, cr.FailedIn)
	}
	if !cr.Curtailed {
		t.Fatal("the interruption must reach the artifact as Curtailed")
	}
	for _, ar := range cr.Results {
		if !ar.Result.Passed {
			t.Fatalf("no fabricated failing result may come from an interrupted wait: %+v", ar.Result)
		}
	}
	// The group's teardown ran despite the cancellation.
	if n := sys.ActiveSubscriptions(); n != 0 {
		t.Fatalf("%d subscription(s) leaked past cancellation", n)
	}
}

// --- SF probe: a teardown ERROR against an otherwise green case ----------

func TestGroupTeardownErrorCannotCoexistWithPassed(t *testing.T) {
	// The contradiction this test rules out: every member
	// passes, the lifecycle's Teardown returns an error. A report reading
	// "case Passed" beside "group Errored" would be internally
	// contradictory — the teardown failure must reach the verdict.
	g := phase.Group{ID: "g", Members: []phase.ID{"m"}, Lifecycle: &wonkyLifecycle{
		teardown: func(context.Context, *phase.Run) error {
			return context.DeadlineExceeded
		},
	}}
	rep := run(t, smallRunner(t, phase.NewPipeline(passingPhase("m")).Group(g)),
		[]phase.Case{plainCase("one")})
	cr := rowOf(t, rep, "one")
	if len(cr.Groups) != 1 || cr.Groups[0].Status != phase.Errored {
		t.Fatalf("groups = %+v, want the teardown error on the group row", cr.Groups)
	}
	if cr.Status == phase.Passed {
		t.Fatalf("case = %v beside an Errored group row — internally contradictory report", cr.Status)
	}
}

// --- SF boundary demo: a cond that lies about its own deadline -----------

func TestCondSwallowingItsDeadlineIsBeyondTheFramework(t *testing.T) {
	// wait.go documents its one honest limit: Go cannot preempt a
	// goroutine, so a cond that ignores/swallows its context is beyond any
	// framework's reach. This test EXISTS to pin that boundary as real —
	// the lie is accepted — so nobody mistakes the documented limit for a
	// checkable guarantee.
	liar := &sabotagePhase{id: "liar", run: func(ctx context.Context, r *phase.Run) error {
		v, err := phase.WaitUntil(ctx, r, func(condCtx context.Context) (string, bool, error) {
			<-condCtx.Done()                       // sees the deadline...
			return "stale-cached-value", true, nil // ...and lies anyway
		})
		if err != nil {
			return err
		}
		r.Record(result.Compared("settled", []bool{true}).WithActual(v))
		return nil
	}}
	cfg := sane()
	cfg.Defaults.Timeout = 10 * time.Millisecond
	r, err := phase.NewRunner(phase.NewPipeline(liar), cfg)
	if err != nil {
		t.Fatal(err)
	}
	s, err := r.Start(context.Background(), []phase.Case{plainCase("one")})
	if err != nil {
		t.Fatal(err)
	}
	cr := rowOf(t, s.Report(), "one")
	if cr.Status != phase.Passed {
		t.Fatalf("case = %v — the framework cannot catch a cond that swallows its context; if this ever starts failing, the boundary moved and wait.go's docs must move with it", cr.Status)
	}
}
