// Copyright 2026 The Phase Contributors
// SPDX-License-Identifier: MIT

package misuse

import (
	"context"
	"testing"
	"time"

	phase "github.com/wow-qe/phase-go"
)

// The misdeclaration doubles: minimal Phase and Case implementations whose
// wiring the tests deliberately get wrong. The REAL flow stays in
// phases.go/case.go, correct — every defect here is seeded per-test.

type sabotagePhase struct {
	id       phase.ID
	deps     []phase.ID
	produces []phase.KeyID
	requires []phase.KeyID
	run      func(context.Context, *phase.Run) error
}

func (p *sabotagePhase) ID() phase.ID            { return p.id }
func (p *sabotagePhase) DependsOn() []phase.ID   { return p.deps }
func (p *sabotagePhase) Produces() []phase.KeyID { return p.produces }
func (p *sabotagePhase) Requires() []phase.KeyID { return p.requires }
func (p *sabotagePhase) AppliesTo(phase.Case, phase.Config) phase.Applicability {
	return phase.Applies()
}
func (p *sabotagePhase) Run(ctx context.Context, r *phase.Run) error {
	if p.run != nil {
		return p.run(ctx, r)
	}
	return nil
}

// sabotageCase is a Case whose contract answers are field-driven so a test
// can hand the engine exactly one wrong answer at a time.
type sabotageCase struct {
	id        string
	status    phase.CaseStatus
	selects   func(phase.ID) (bool, string)
	timing    map[phase.ID]phase.Timing
	fixtures  []phase.Fixture
	exclusive bool
	exReason  string
}

func (c *sabotageCase) ID() string               { return c.id }
func (c *sabotageCase) Status() phase.CaseStatus { return c.status }
func (c *sabotageCase) Selects(id phase.ID) (bool, string) {
	if c.selects != nil {
		return c.selects(id)
	}
	return true, ""
}
func (c *sabotageCase) Timing(id phase.ID) (phase.Timing, bool) {
	t, ok := c.timing[id]
	return t, ok
}
func (c *sabotageCase) Fixtures() []phase.Fixture { return c.fixtures }
func (c *sabotageCase) Exclusive() (bool, string) { return c.exclusive, c.exReason }
func (c *sabotageCase) Scenario() string          { return "happy" }
func (c *sabotageCase) Entities() int             { return 1 }

// wantLoad asserts err is a *phase.LoadError carrying exactly the given
// code — refusals are pinned by their machine-readable vocabulary, never
// by message prose.
func wantLoad(t *testing.T, err error, code phase.LoadCode) *phase.LoadError {
	t.Helper()
	if err == nil {
		t.Fatalf("want LoadError %q, got nil — the misdeclaration was accepted", code)
	}
	le, ok := err.(*phase.LoadError)
	if !ok {
		t.Fatalf("want *phase.LoadError %q, got %T: %v", code, err, err)
	}
	if le.Code != code {
		t.Fatalf("LoadError code = %q, want %q (subject %q: %s)", le.Code, code, le.Subject, le.Detail)
	}
	return le
}

func sane() phase.Config {
	return phase.Config{Defaults: phase.Timing{Attempts: 5, Interval: time.Millisecond}}
}

// misuseSuite loads the correct manifest against a fresh system.
func misuseSuite(t *testing.T) (*checkoutSystem, []phase.Case) {
	t.Helper()
	sys := newCheckoutSystem()
	cases, err := loadCases(sys, Manifest)
	if err != nil {
		t.Fatalf("loadCases: %v", err)
	}
	return sys, cases
}

func onlyCase(t *testing.T, cases []phase.Case, id string) []phase.Case {
	t.Helper()
	for _, c := range cases {
		if c.ID() == id {
			return []phase.Case{c}
		}
	}
	t.Fatalf("case %q not in manifest", id)
	return nil
}

func mustMisuseRunner(t *testing.T, sys *checkoutSystem, cfg phase.Config, opts ...phase.RunnerOption) *phase.Runner {
	t.Helper()
	r, err := phase.NewRunner(buildPipeline(sys), cfg, opts...)
	if err != nil {
		t.Fatalf("NewRunner on the CORRECT pipeline refused: %v", err)
	}
	return r
}

func run(t *testing.T, r *phase.Runner, cases []phase.Case) *phase.Report {
	t.Helper()
	s, err := r.Start(context.Background(), cases)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	rep := s.Report()
	if verr := rep.Verify(); verr != nil {
		t.Fatalf("report failed Verify — under sabotage the REPORT must stay internally consistent: %v", verr)
	}
	return rep
}

func rowOf(t *testing.T, rep *phase.Report, caseID string) phase.CaseReport {
	t.Helper()
	for _, cr := range rep.Cases {
		if cr.CaseID == caseID {
			return cr
		}
	}
	t.Fatalf("case %q missing from report", caseID)
	return phase.CaseReport{}
}

func outcomeOf(t *testing.T, cr phase.CaseReport, id phase.ID) phase.PhaseOutcome {
	t.Helper()
	for _, po := range cr.Phases {
		if po.ID == id {
			return po
		}
	}
	t.Fatalf("phase %q missing from case %q", id, cr.CaseID)
	return phase.PhaseOutcome{}
}
