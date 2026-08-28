// Copyright 2026 The Phase Contributors
// SPDX-License-Identifier: MIT

package phase

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/wow-qe/phase-go/result"
)

// Cases gain a DAG. Execution follows dependency order (a dependency on
// a later-declared case is normal, exactly as phases already decouple
// execution from declaration); the report stays in declaration order.
// An unmet requirement is a loud Skipped with the cause carried
// structurally, never an omitted case.

type depCase struct {
	stubCase
	deps []CaseRequirement
}

func (c *depCase) DependsOnCases() []CaseRequirement { return c.deps }

func depRunner(t *testing.T, outcome map[string]bool) *Runner {
	t.Helper()
	return mustRunner(t, Config{Defaults: validTiming()},
		&recordingPhase{stubPhase: stubPhase{id: "checks"}, do: func(_ context.Context, run *Run) error {
			if ok, decided := outcome[run.Case().ID()]; decided && !ok {
				run.Record(result.Failed("check", "declared failing by the test"))
				return nil
			}
			run.Record(result.Compared("check", []bool{true}))
			return nil
		}},
	)
}

func TestExecutionFollowsTheCaseDAGReportStaysDeclared(t *testing.T) {
	var completions []string
	r := depRunner(t, nil)
	WithCaseObserver(func(cr CaseReport) { completions = append(completions, cr.CaseID) })(r)
	// "first" is declared first but depends on "second", declared later —
	// normal, not a refusal.
	s := startSession(t, r,
		&depCase{stubCase: stubCase{id: "first"}, deps: []CaseRequirement{{CaseID: "second"}}},
		&stubCase{id: "second"},
	)
	if got := strings.Join(completions, ","); got != "second,first" {
		t.Fatalf("completion order = %q, want dependency order", got)
	}
	cases := s.Cases()
	if cases[0].CaseID != "first" || cases[1].CaseID != "second" {
		t.Fatalf("report order = %s,%s — declaration order is the report's contract", cases[0].CaseID, cases[1].CaseID)
	}
	if cases[0].Status != Passed || cases[1].Status != Passed {
		t.Fatalf("statuses = %s,%s", cases[0].Status, cases[1].Status)
	}
}

func TestUnmetRequirementIsALoudStructuredSkip(t *testing.T) {
	r := depRunner(t, map[string]bool{"provision": false}) // provision FAILS
	s := startSession(t, r,
		&stubCase{id: "provision"},
		&depCase{stubCase: stubCase{id: "billing"},
			deps: []CaseRequirement{{CaseID: "provision", Acceptable: []Status{Passed, Flaked}}}},
	)
	cr := caseReport(t, s, "billing")
	if cr.Status != Skipped || !strings.HasPrefix(cr.Reason, "case dependency: ") {
		t.Fatalf("cr = %+v — an unmet requirement is a loud Skipped", cr)
	}
	df := cr.DependencyFailure
	if df == nil || df.CaseID != "provision" || df.Actual != Failed || len(df.Acceptable) != 2 {
		t.Fatalf("DependencyFailure = %+v — the cause must be structural, not prose to regex", df)
	}
	var buf bytes.Buffer
	if err := s.Report().WriteJSON(&buf); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), `"dependency_failure"`) {
		t.Fatal("dependency_failure missing from the artifact")
	}
}

func TestEmptyAcceptableMeansAnyOutcome(t *testing.T) {
	r := depRunner(t, map[string]bool{"provision": false})
	s := startSession(t, r,
		&stubCase{id: "provision"},
		&depCase{stubCase: stubCase{id: "cleanup"},
			deps: []CaseRequirement{{CaseID: "provision"}}}, // ordering only
	)
	if got := caseReport(t, s, "cleanup").Status; got != Passed {
		t.Fatalf("cleanup = %s — empty Acceptable is ordering-only, any outcome satisfies", got)
	}
}

func TestCaseDependencyRefusals(t *testing.T) {
	r := depRunner(t, nil)
	t.Run("unknown target", func(t *testing.T) {
		_, err := r.Start(context.Background(), []Case{
			&depCase{stubCase: stubCase{id: "x"}, deps: []CaseRequirement{{CaseID: "ghost"}}},
		})
		var le *LoadError
		if !errors.As(err, &le) || le.Code != CaseDependencyUnknown {
			t.Fatalf("err = %v, want %s — a dependency filtered out by a selector is a suite-authoring bug, refused loudly", err, CaseDependencyUnknown)
		}
	})
	t.Run("cycle", func(t *testing.T) {
		_, err := r.Start(context.Background(), []Case{
			&depCase{stubCase: stubCase{id: "a"}, deps: []CaseRequirement{{CaseID: "b"}}},
			&depCase{stubCase: stubCase{id: "b"}, deps: []CaseRequirement{{CaseID: "a"}}},
		})
		var le *LoadError
		if !errors.As(err, &le) || le.Code != CaseDependencyCycle {
			t.Fatalf("err = %v, want %s", err, CaseDependencyCycle)
		}
	})
}

func TestDependencySkipCascades(t *testing.T) {
	// x depends on y (Passed required); y is skipped because its dependency
	// failed. x's requirement reads y's actual status (Skipped) — whatever
	// the dependency reached, generically.
	r := depRunner(t, map[string]bool{"root": false})
	s := startSession(t, r,
		&stubCase{id: "root"},
		&depCase{stubCase: stubCase{id: "mid"},
			deps: []CaseRequirement{{CaseID: "root", Acceptable: []Status{Passed}}}},
		&depCase{stubCase: stubCase{id: "leaf"},
			deps: []CaseRequirement{{CaseID: "mid", Acceptable: []Status{Passed}}}},
	)
	if got := caseReport(t, s, "mid").Status; got != Skipped {
		t.Fatalf("mid = %s", got)
	}
	leaf := caseReport(t, s, "leaf")
	if leaf.Status != Skipped || leaf.DependencyFailure == nil || leaf.DependencyFailure.Actual != Skipped {
		t.Fatalf("leaf = %+v — the cascade must carry mid's actual status", leaf)
	}
}
