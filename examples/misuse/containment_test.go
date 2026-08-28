// Copyright 2026 The Phase Contributors
// SPDX-License-Identifier: MIT

package misuse

import (
	"context"
	"strings"
	"testing"

	phase "github.com/wow-qe/phase-go"
	"github.com/wow-qe/phase-go/result"
)

// Panic containment, site by site: a consumer bug is that case's evidence,
// never the batch's crash. Every test runs a bystander case beside the
// faulted one and checks that the bystander's verdict is untouched.

type afterSabotage struct {
	sabotagePhase
	after func(context.Context, *phase.Run, phase.PhaseOutcome) error
}

func (p *afterSabotage) After(ctx context.Context, r *phase.Run, po phase.PhaseOutcome) error {
	return p.after(ctx, r, po)
}

// bomb selects the sabotage only for the "victim" case, so the same
// pipeline serves victim and bystander.
func victimOnly(r *phase.Run, do func()) {
	if r.Case().ID() == "victim" {
		do()
	}
}

func twoCases() []phase.Case {
	return []phase.Case{plainCase("victim"), plainCase("bystander")}
}

func assertBystanderPassed(t *testing.T, rep *phase.Report) {
	t.Helper()
	if got := rowOf(t, rep, "bystander").Status; got != phase.Passed {
		t.Fatalf("bystander = %v — containment leaked across cases", got)
	}
}

func TestPanicInRunIsThatCasesEvidence(t *testing.T) {
	ph := &sabotagePhase{id: "p", run: func(_ context.Context, r *phase.Run) error {
		victimOnly(r, func() { panic("consumer bug in Run") })
		r.Record(result.Compared("ok", []bool{true}))
		return nil
	}}
	rep := run(t, smallRunner(t, phase.NewPipeline(ph)), twoCases())
	cr := rowOf(t, rep, "victim")
	if cr.Status != phase.Errored {
		t.Fatalf("victim = %v, want Errored", cr.Status)
	}
	if len(cr.Errors) == 0 || !strings.Contains(cr.Errors[0].Err, "panic") {
		t.Fatalf("errors = %+v, want the panic on the record", cr.Errors)
	}
	assertBystanderPassed(t, rep)
}

func TestPanicInBeforeLandsInTheBeforeStage(t *testing.T) {
	ph := &beforeSabotage{sabotagePhase: sabotagePhase{id: "p"},
		before: func(_ context.Context, r *phase.Run) error {
			victimOnly(r, func() { panic("consumer bug in Before") })
			return nil
		}}
	// The phase must still assert for the bystander to pass.
	ph.sabotagePhase.run = func(_ context.Context, r *phase.Run) error {
		r.Record(result.Compared("ok", []bool{true}))
		return nil
	}
	rep := run(t, smallRunner(t, phase.NewPipeline(ph)), twoCases())
	po := outcomeOf(t, rowOf(t, rep, "victim"), "p")
	if po.Status != phase.Errored || po.Stage != phase.StageBefore {
		t.Fatalf("victim p = %+v, want Errored in before", po)
	}
	assertBystanderPassed(t, rep)
}

func TestPanicInAfterFlipsOnlyPassedRows(t *testing.T) {
	ph := &afterSabotage{sabotagePhase: sabotagePhase{id: "p",
		run: func(_ context.Context, r *phase.Run) error {
			r.Record(result.Compared("ok", []bool{true}))
			return nil
		}},
		after: func(_ context.Context, r *phase.Run, _ phase.PhaseOutcome) error {
			victimOnly(r, func() { panic("consumer bug in After") })
			return nil
		}}
	rep := run(t, smallRunner(t, phase.NewPipeline(ph)), twoCases())
	po := outcomeOf(t, rowOf(t, rep, "victim"), "p")
	if po.Status != phase.Errored || po.Stage != phase.StageAfter {
		t.Fatalf("victim p = %+v, want the After panic folded into the row", po)
	}
	assertBystanderPassed(t, rep)
}

func TestPanicInAfterNeverMasksTheRunsOwnError(t *testing.T) {
	// An already-Errored row keeps its original cause; After's panic joins
	// the error ledger without rewriting history.
	ph := &afterSabotage{sabotagePhase: sabotagePhase{id: "p",
		run: func(_ context.Context, r *phase.Run) error {
			if r.Case().ID() == "victim" {
				return context.DeadlineExceeded // the real, first cause
			}
			r.Record(result.Compared("ok", []bool{true}))
			return nil
		}},
		after: func(_ context.Context, r *phase.Run, _ phase.PhaseOutcome) error {
			victimOnly(r, func() { panic("cleanup bug") })
			return nil
		}}
	rep := run(t, smallRunner(t, phase.NewPipeline(ph)), twoCases())
	po := outcomeOf(t, rowOf(t, rep, "victim"), "p")
	if po.Status != phase.Errored || po.Stage == phase.StageAfter {
		t.Fatalf("victim p = %+v — After must never rewrite an already-errored row", po)
	}
	if !strings.Contains(po.Reason, "deadline") {
		t.Fatalf("reason = %q, want the ORIGINAL cause kept", po.Reason)
	}
	assertBystanderPassed(t, rep)
}

func TestPanicInWhenIsTheConditionsFailure(t *testing.T) {
	gated := &conditionalSabotage{sabotagePhase: sabotagePhase{id: "gated", deps: []phase.ID{"base"}},
		when: func(_ context.Context, r *phase.Run) (bool, string, error) {
			victimOnly(r, func() { panic("consumer bug in When") })
			return true, "", nil
		}}
	gated.sabotagePhase.run = func(_ context.Context, r *phase.Run) error {
		r.Record(result.Compared("gated ok", []bool{true}))
		return nil
	}
	rep := run(t, smallRunner(t, phase.NewPipeline(passingPhase("base"), gated)), twoCases())
	po := outcomeOf(t, rowOf(t, rep, "victim"), "gated")
	if po.Status != phase.Errored || po.Stage != phase.StageCondition {
		t.Fatalf("victim gated = %+v, want Errored in the condition stage", po)
	}
	assertBystanderPassed(t, rep)
}

func TestPanicInFixtureSetupStillTearsDownItsPredecessors(t *testing.T) {
	journal := []string{}
	first := &wonkyLifecycle{
		setup:    func(context.Context, *phase.Run) error { journal = append(journal, "first setup"); return nil },
		teardown: func(context.Context, *phase.Run) error { journal = append(journal, "first teardown"); return nil },
	}
	second := &wonkyLifecycle{
		setup: func(context.Context, *phase.Run) error { panic("consumer bug in fixture Setup") },
	}
	victim := &sabotageCase{id: "victim", fixtures: []phase.Fixture{first, second}}
	rep := run(t, smallRunner(t, phase.NewPipeline(passingPhase("m"))),
		[]phase.Case{victim, plainCase("bystander")})
	if got := rowOf(t, rep, "victim").Status; got != phase.Errored {
		t.Fatalf("victim = %v, want Errored from the contained fixture panic", got)
	}
	if len(journal) != 2 || journal[1] != "first teardown" {
		t.Fatalf("journal = %v — the successfully-set-up fixture must still tear down", journal)
	}
	assertBystanderPassed(t, rep)
}

func TestPanicInGroupSetupPrunesMembersLoudly(t *testing.T) {
	g := phase.Group{ID: "g", Members: []phase.ID{"m"}, Lifecycle: &wonkyLifecycle{
		setup: func(_ context.Context, r *phase.Run) error {
			victimOnly(r, func() { panic("consumer bug in group Setup") })
			return nil
		},
	}}
	rep := run(t, smallRunner(t, phase.NewPipeline(passingPhase("m")).Group(g)), twoCases())
	cr := rowOf(t, rep, "victim")
	if len(cr.Groups) != 1 || cr.Groups[0].Status != phase.Errored {
		t.Fatalf("groups = %+v, want the panic on the group row", cr.Groups)
	}
	if po := outcomeOf(t, cr, "m"); po.DeclineSource != phase.DeclinedByGroupSetup {
		t.Fatalf("member = %+v, want DeclinedByGroupSetup", po)
	}
	assertBystanderPassed(t, rep)
}

// --- a contained violation under the error-family taxonomy -----------------

func TestContainedViolationKeepsTheReportTrustworthy(t *testing.T) {
	// A capability violation (recorded as a FrameworkError string in
	// cr.Errors) does not force exit code 3: exit 3 means the report's
	// numbers cannot be trusted (Verify failed). A contained consumer
	// misuse is case evidence in a fully consistent report instead: the
	// case is Errored, the violation is on the record, Verify passes, and
	// the run exits 1.
	smuggler := &sabotagePhase{id: "smuggler", run: func(_ context.Context, r *phase.Run) error {
		phase.Put(r, StreamCursor, "undeclared")
		r.Record(result.Compared("work", []bool{true}))
		return nil
	}}
	rep := run(t, smallRunner(t, phase.NewPipeline(smuggler)), []phase.Case{plainCase("one")})
	if err := rep.Verify(); err != nil {
		t.Fatalf("Verify: %v — containment must leave a consistent report", err)
	}
	if rep.ExitCode() != 1 {
		t.Fatalf("exit = %d, want 1: an Errored case in a trustworthy report", rep.ExitCode())
	}
}
