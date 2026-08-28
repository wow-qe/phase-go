// Copyright 2026 The Phase Contributors
// SPDX-License-Identifier: MIT

package phase

import (
	"context"
	"errors"
	"testing"
)

func TestExplainProjectsTheRunWithoutExecuting(t *testing.T) {
	executed := false
	off := false
	r := mustRunner(t, Config{
		Defaults: validTiming(),
		Phases:   map[ID]Settings{"disabled": {Enabled: &off}},
	},
		&recordingPhase{stubPhase: stubPhase{id: "runs"}, do: func(context.Context, *Run) error {
			executed = true
			return nil
		}},
		&stubPhase{id: "declined", applies: Skip("not this case")},
		&stubPhase{id: "disabled"},
		&conditionalPhase{stubPhase: stubPhase{id: "gated"},
			when: func(context.Context, *Run) (bool, string, error) { return true, "", nil }},
	)
	plan, err := r.Explain([]Case{
		&stubCase{id: "one"},
		&stubCase{id: "parked", status: Quarantined},
	})
	if err != nil {
		t.Fatalf("Explain: %v", err)
	}
	if executed {
		t.Fatal("Explain must execute NOTHING")
	}
	byID := map[ID]PhasePlan{}
	for _, pp := range plan.Cases[0].Phases {
		byID[pp.ID] = pp
	}
	if byID["runs"].Disposition != PlanWillRun {
		t.Fatalf("runs = %+v", byID["runs"])
	}
	if d := byID["declined"]; d.Disposition != PlanDeclined || d.DeclineSource != DeclinedByPhase {
		t.Fatalf("declined = %+v", d)
	}
	if d := byID["disabled"]; d.Disposition != PlanDeclined || d.DeclineSource != DeclinedByConfig {
		t.Fatalf("disabled = %+v", d)
	}
	// The honesty three-way: a When-gated phase is conditional, never a
	// false claim of will-run — even though this one's condition would pass.
	if g := byID["gated"]; g.Disposition != PlanConditional {
		t.Fatalf("gated = %+v — Explain must not claim precision it cannot have", g)
	}
	if !plan.Cases[1].Skipped {
		t.Fatalf("parked = %+v", plan.Cases[1])
	}
}

func TestExplainSubsumesPreflight(t *testing.T) {
	r := mustRunner(t, Config{Defaults: validTiming()}, passingPhase("p", nil))
	_, err := r.Explain([]Case{
		&depCase{stubCase: stubCase{id: "x"}, deps: []CaseRequirement{{CaseID: "ghost"}}},
	})
	var le *LoadError
	if !errors.As(err, &le) || le.Code != CaseDependencyUnknown {
		t.Fatalf("err = %v — Explain's first act is real Preflight, not a weaker copy", err)
	}
}
