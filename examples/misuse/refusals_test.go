// Copyright 2026 The Phase Contributors
// SPDX-License-Identifier: MIT

package misuse

import (
	"strings"
	"testing"

	phase "github.com/wow-qe/phase-go"
	config "github.com/wow-qe/phase-go/x/config"
)

// Construction-time sabotage: every misdeclaration must be refused BEFORE
// anything runs, as a *LoadError pinned by its machine-readable code.
// Ordered by deviation-risk: the least-tested
// combinations first.

// Risk #1: a NON-member requiring a group-produced key, with no dependency
// path through any member. The group's producer is its synthetic setup
// node; an outsider's reach must not contain it.
func TestOutsiderRequiringGroupKeyIsRefused(t *testing.T) {
	sys := newCheckoutSystem()
	outsider := &sabotagePhase{
		id:       "outsider",
		deps:     []phase.ID{"submit"}, // deliberately NOT through the group
		requires: phase.Keys(StreamCursor),
	}
	p := phase.NewPipeline(
		&submitPhase{sys}, &authorizePhase{sys}, &settleWaitPhase{sys},
		&settleChecksPhase{sys}, &ledgerPhase{sys}, &refundAuditPhase{sys},
		&auditPhase{sys}, outsider,
	).Group(settlementGroup(sys))
	_, err := phase.NewRunner(p, sane())
	le := wantLoad(t, err, phase.KeyNeverProduced)
	if le.Subject != "outsider" {
		t.Fatalf("subject = %q, want the requiring phase named", le.Subject)
	}
}

// The counterpart that must be PERMITTED: an outsider whose dependency
// chain passes through a member reaches the group's key legitimately.
func TestOutsiderReachingGroupKeyThroughMemberIsAccepted(t *testing.T) {
	sys := newCheckoutSystem()
	downstream := &sabotagePhase{
		id:       "downstream",
		deps:     []phase.ID{"settle_checks"}, // through a member
		requires: phase.Keys(StreamCursor),
	}
	p := phase.NewPipeline(
		&submitPhase{sys}, &authorizePhase{sys}, &settleWaitPhase{sys},
		&settleChecksPhase{sys}, &ledgerPhase{sys}, &refundAuditPhase{sys},
		&auditPhase{sys}, downstream,
	).Group(settlementGroup(sys))
	if _, err := phase.NewRunner(p, sane()); err != nil {
		t.Fatalf("a member-reaching consumer of the group key must be accepted: %v", err)
	}
}

// Risk #2: the phase-vs-group producer collision — one key, two writers
// across the declaration boundary.
func TestPhaseVersusGroupProducerCollisionIsRefused(t *testing.T) {
	sys := newCheckoutSystem()
	pirate := &sabotagePhase{
		id:       "pirate",
		deps:     []phase.ID{"submit"},
		produces: phase.Keys(StreamCursor), // the group already produces this
	}
	p := phase.NewPipeline(
		&submitPhase{sys}, &authorizePhase{sys}, &settleWaitPhase{sys},
		&settleChecksPhase{sys}, &ledgerPhase{sys}, &refundAuditPhase{sys},
		&auditPhase{sys}, pirate,
	).Group(settlementGroup(sys))
	le := wantLoad(t, refuse(t, p, sane()), phase.DuplicateKeyProducer)
	if !strings.Contains(le.Detail, "group") {
		t.Fatalf("detail = %q, want the group named as the other writer", le.Detail)
	}
}

// Risk #3: the preflight ordering combination — a batch that is BOTH
// nil-poisoned AND dependency-cyclic must surface the nil first, never a
// dag-layer panic.
func TestNilCaseWinsOverDependencyCycle(t *testing.T) {
	sys, cases := misuseSuite(t)
	a := &cyclicCase{id: "a", dep: "b"}
	b := &cyclicCase{id: "b", dep: "a"}
	batch := []phase.Case{onlyCase(t, cases, "happy-single")[0], nil, a, b}
	err := mustMisuseRunner(t, sys, sane()).Preflight(batch)
	wantLoad(t, err, phase.NilCase)
}

func TestCaseDependencyCycleIsRefused(t *testing.T) {
	sys, _ := misuseSuite(t)
	a := &cyclicCase{id: "a", dep: "b"}
	b := &cyclicCase{id: "b", dep: "a"}
	err := mustMisuseRunner(t, sys, sane()).Preflight([]phase.Case{a, b})
	wantLoad(t, err, phase.CaseDependencyCycle)
}

// --- the mechanical A-tier, each pinned by code --------------------------

func TestDuplicatePhaseIDRefused(t *testing.T) {
	sys := newCheckoutSystem()
	p := phase.NewPipeline(&submitPhase{sys}, &submitPhase{sys})
	wantLoad(t, refuse(t, p, sane()), phase.DuplicatePhaseID)
}

func TestUnknownDependencyRefused(t *testing.T) {
	p := phase.NewPipeline(&sabotagePhase{id: "a", deps: []phase.ID{"warehouse"}})
	wantLoad(t, refuse(t, p, sane()), phase.UnknownDependency)
}

func TestDependencyCycleRefused(t *testing.T) {
	p := phase.NewPipeline(
		&sabotagePhase{id: "a", deps: []phase.ID{"b"}},
		&sabotagePhase{id: "b", deps: []phase.ID{"a"}},
	)
	wantLoad(t, refuse(t, p, sane()), phase.DependencyCycle)
}

func TestConfigForUnknownPhaseRefused(t *testing.T) {
	sys := newCheckoutSystem()
	cfg := sane()
	cfg.Phases = map[phase.ID]phase.Settings{"ghost": {}}
	wantLoad(t, refuse(t, buildPipeline(sys), cfg), phase.UnknownPhaseInConfig)
}

func TestReservedColonNamespaceRefused(t *testing.T) {
	// A phase ID with ':' — refused even with no groups declared anywhere.
	p := phase.NewPipeline(&sabotagePhase{id: "evil:phase"})
	wantLoad(t, refuse(t, p, sane()), phase.GroupIDReservedCharacter)
	// Depending on the group's internal plumbing node by name.
	sys := newCheckoutSystem()
	p2 := phase.NewPipeline(
		&submitPhase{sys}, &authorizePhase{sys}, &settleWaitPhase{sys},
		&settleChecksPhase{sys}, &ledgerPhase{sys}, &refundAuditPhase{sys},
		&auditPhase{sys},
		&sabotagePhase{id: "sneaky", deps: []phase.ID{"group:settlement:setup"}},
	).Group(settlementGroup(sys))
	wantLoad(t, refuse(t, p2, sane()), phase.GroupIDReservedCharacter)
}

func TestGroupDeclarationDefectsRefused(t *testing.T) {
	sys := newCheckoutSystem()
	base := func() *phase.Pipeline {
		return phase.NewPipeline(
			&submitPhase{sys}, &authorizePhase{sys}, &settleWaitPhase{sys},
			&settleChecksPhase{sys}, &ledgerPhase{sys}, &refundAuditPhase{sys},
			&auditPhase{sys},
		)
	}
	for _, tc := range []struct {
		name  string
		group phase.Group
		code  phase.LoadCode
	}{
		{"colon in id", phase.Group{ID: "bad:name", Members: []phase.ID{"settle_wait"}}, phase.GroupIDReservedCharacter},
		{"no members", phase.Group{ID: "empty"}, phase.EmptyGroup},
		{"unknown member", phase.Group{ID: "g", Members: []phase.ID{"nonexistent"}}, phase.UnknownGroupMember},
		{"duplicate member", phase.Group{ID: "g", Members: []phase.ID{"settle_wait", "settle_wait"}}, phase.GroupMemberDuplicate},
	} {
		_, err := phase.NewRunner(base().Group(tc.group), sane())
		wantLoad(t, err, tc.code)
	}
	// Two groups sharing an ID.
	_, err := phase.NewRunner(base().
		Group(phase.Group{ID: "settlement", Members: []phase.ID{"settle_wait"}}).
		Group(phase.Group{ID: "settlement", Members: []phase.ID{"settle_checks"}}), sane())
	wantLoad(t, err, phase.DuplicateGroupID)
}

func TestUnrunnableTimingRefused(t *testing.T) {
	sys := newCheckoutSystem()
	cfg := sane()
	cfg.Defaults.Attempts = 0
	wantLoad(t, refuse(t, buildPipeline(sys), cfg), phase.TimingInvalid)

	cfg = sane()
	cfg.MaxCaseConcurrency = -5
	wantLoad(t, refuse(t, buildPipeline(sys), cfg), phase.TimingInvalid)
}

func TestBrokenRedactPatternRefused(t *testing.T) {
	sys := newCheckoutSystem()
	cfg := sane()
	cfg.RedactPatterns = []string{"[invalid("}
	wantLoad(t, refuse(t, buildPipeline(sys), cfg), phase.RedactPatternInvalid)
}

// --- the B-tier: case-shaped misdeclarations -----------------------------

func TestCaseMisdeclarationsRefused(t *testing.T) {
	sys, cases := misuseSuite(t)
	r := mustMisuseRunner(t, sys, sane())
	good := onlyCase(t, cases, "happy-single")[0]

	wantLoad(t, r.Preflight([]phase.Case{good, good}), phase.DuplicateCaseID)
	wantLoad(t, r.Preflight([]phase.Case{
		&sabotageCase{id: "mute", selects: func(id phase.ID) (bool, string) { return id != "ledger", "" }},
	}), phase.SkipWithoutReason)
	wantLoad(t, r.Preflight([]phase.Case{
		&sabotageCase{id: "diva", exclusive: true},
	}), phase.ExclusiveWithoutReason)
	wantLoad(t, r.Preflight([]phase.Case{
		&sabotageCase{id: "hollow", fixtures: []phase.Fixture{nil}},
	}), phase.FixtureNil)
}

// A dependency crossing the suite-selection boundary: the LoadError's
// detail must point at the selector, because that is the mistake the
// operator actually made.
func TestDependencyAcrossSelectionBoundaryIsExplained(t *testing.T) {
	sys, cases := misuseSuite(t)
	err := mustMisuseRunner(t, sys, sane()).Preflight(onlyCase(t, cases, "billing-report"))
	le := wantLoad(t, err, phase.CaseDependencyUnknown)
	if !strings.Contains(le.Detail, "selector") && !strings.Contains(le.Detail, "selection") {
		t.Fatalf("detail = %q — the selection-boundary hint is the actionable part", le.Detail)
	}
}

// --- the manifest tier: x/config strict parsing --------------------------

func TestManifestSabotageRefused(t *testing.T) {
	for _, tc := range []struct {
		name     string
		yaml     string
		wantCode phase.LoadCode // empty: any error acceptable, pin substring instead
		want     string
	}{
		{"missing id", "cases:\n  - tags: [x]\n", phase.CaseIDMissing, ""},
		{"unparsable status", "cases:\n  - id: c\n    status: half-done\n", "", "half-done"},
		{"decline without reason", "cases:\n  - id: c\n    declines:\n      ledger: \"\"\n", phase.SkipWithoutReason, ""},
		{"unknown key", "cases:\n  - id: c\n    surprises: [1]\n", "", "surprises"},
	} {
		_, err := config.ParseCases([]byte(tc.yaml))
		if err == nil {
			t.Fatalf("%s: the manifest defect was accepted", tc.name)
		}
		if tc.wantCode != "" {
			wantLoad(t, err, tc.wantCode)
		}
		if tc.want != "" && !strings.Contains(err.Error(), tc.want) {
			t.Fatalf("%s: err = %v, want it to name %q", tc.name, err, tc.want)
		}
	}
}

func TestUnknownFixtureNamesTheAlternatives(t *testing.T) {
	specs, err := config.ParseCases([]byte("cases:\n  - id: c\n    fixtures: [seed-catalgo]\n"))
	if err != nil {
		t.Fatal(err)
	}
	sys := newCheckoutSystem()
	_, cerr := specs[0].Case(config.Registry{"seed-catalog": func() phase.Fixture { return &catalogFixture{sys} }})
	le := wantLoad(t, cerr, phase.UnknownFixture)
	if !strings.Contains(le.Detail, "seed-catalog") {
		t.Fatalf("detail = %q — the typo's neighbours must be listed", le.Detail)
	}
}

// --- helpers -------------------------------------------------------------

// refuse constructs and demands rejection: no Runner, a non-nil error.
func refuse(t *testing.T, p *phase.Pipeline, cfg phase.Config) error {
	t.Helper()
	r, err := phase.NewRunner(p, cfg)
	if err == nil {
		t.Fatal("the misdeclaration was accepted")
	}
	if r != nil {
		t.Fatal("a refused construction must not also hand back a Runner")
	}
	return err
}

// cyclicCase carries only a case dependency — used for the case-DAG tests.
type cyclicCase struct {
	id  string
	dep string
}

func (c *cyclicCase) ID() string                           { return c.id }
func (c *cyclicCase) Status() phase.CaseStatus             { return phase.Active }
func (c *cyclicCase) Selects(phase.ID) (bool, string)      { return true, "" }
func (c *cyclicCase) Timing(phase.ID) (phase.Timing, bool) { return phase.Timing{}, false }
func (c *cyclicCase) Fixtures() []phase.Fixture            { return nil }
func (c *cyclicCase) Exclusive() (bool, string)            { return false, "" }
func (c *cyclicCase) Scenario() string                     { return "happy" }
func (c *cyclicCase) Entities() int                        { return 1 }
func (c *cyclicCase) DependsOnCases() []phase.CaseRequirement {
	return []phase.CaseRequirement{{CaseID: c.dep, Acceptable: []phase.Status{phase.Passed}}}
}
