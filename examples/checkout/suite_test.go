// Copyright 2026 The Phase Contributors
// SPDX-License-Identifier: MIT

package checkout

import (
	"bytes"
	"context"
	"errors"
	"regexp"
	"strings"
	"testing"
	"time"

	phase "github.com/wow-qe/phase-go"
	"github.com/wow-qe/phase-go/phasetest"
	config "github.com/wow-qe/phase-go/x/config"
)

// This file exercises the feature map in README.md end-to-end against the
// checkout flow: a dry run (Explain), a green run under observers, an
// honest red run, both mutation gates, sharding, and a hook-bearing phase
// unit-tested in isolation.

func fastConfig() phase.Config {
	return phase.Config{Defaults: phase.Timing{Attempts: 5, Interval: time.Millisecond}}
}

func newSuite(t *testing.T) (*checkoutSystem, []phase.Case) {
	t.Helper()
	sys := newCheckoutSystem()
	cases, err := loadCases(sys, Manifest)
	if err != nil {
		t.Fatalf("loadCases: %v", err)
	}
	return sys, cases
}

func newRunner(t *testing.T, sys *checkoutSystem, cfg phase.Config, opts ...phase.RunnerOption) *phase.Runner {
	t.Helper()
	r, err := phase.NewRunner(buildPipeline(sys), cfg, opts...)
	if err != nil {
		t.Fatalf("NewRunner: %v", err)
	}
	return r
}

func pick(t *testing.T, cases []phase.Case, ids ...string) []phase.Case {
	t.Helper()
	var out []phase.Case
	for _, id := range ids {
		found := false
		for _, c := range cases {
			if c.ID() == id {
				out, found = append(out, c), true
				break
			}
		}
		if !found {
			t.Fatalf("case %q not in the manifest", id)
		}
	}
	return out
}

func report(t *testing.T, r *phase.Runner, cases []phase.Case) *phase.Report {
	t.Helper()
	s, err := r.Start(context.Background(), cases)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	return s.Report()
}

func caseRow(t *testing.T, rep *phase.Report, id string) phase.CaseReport {
	t.Helper()
	for _, cr := range rep.Cases {
		if cr.CaseID == id {
			return cr
		}
	}
	t.Fatalf("case %q not in the report", id)
	return phase.CaseReport{}
}

func phaseRow(t *testing.T, cr phase.CaseReport, id phase.ID) phase.PhaseOutcome {
	t.Helper()
	for _, po := range cr.Phases {
		if po.ID == id {
			return po
		}
	}
	t.Fatalf("phase %q not in case %q", id, cr.CaseID)
	return phase.PhaseOutcome{}
}

// --- Explain: the dry run predicts everything static -----------------------

func TestExplainPredictsTheRun(t *testing.T) {
	sys, cases := newSuite(t)
	plan, err := newRunner(t, sys, fastConfig()).Explain(cases)
	if err != nil {
		t.Fatalf("Explain: %v", err)
	}

	// Case DAG: billing-report depends on happy-multi, so execution order
	// keeps the producer first.
	pos := map[string]int{}
	for i, id := range plan.CaseOrder {
		pos[id] = i
	}
	if pos["happy-multi"] >= pos["billing-report"] {
		t.Fatalf("case order %v — happy-multi must run before its dependent", plan.CaseOrder)
	}

	byID := map[string]phase.CasePlan{}
	for _, cp := range plan.Cases {
		byID[cp.ID] = cp
	}

	// Quarantine is a declared skip, visible in the plan.
	if cp := byID["parked-experiment"]; !cp.Skipped || !strings.Contains(cp.Reason, "quarantined") {
		t.Fatalf("parked-experiment plan = %+v, want skipped as quarantined", cp)
	}
	// Exclusivity and case dependencies surface on the plan row.
	if !byID["reconcile"].Exclusive {
		t.Fatal("reconcile must plan as exclusive")
	}
	if deps := byID["billing-report"].DependsOn; len(deps) != 1 || deps[0].CaseID != "happy-multi" {
		t.Fatalf("billing-report DependsOn = %+v", deps)
	}

	find := func(cp phase.CasePlan, id phase.ID) phase.PhasePlan {
		for _, pp := range cp.Phases {
			if pp.ID == id {
				return pp
			}
		}
		t.Fatalf("phase %q missing from plan of %q", id, cp.ID)
		return phase.PhasePlan{}
	}

	// The YAML timing override reaches the plan; group membership shows.
	sw := find(byID["happy-multi"], "settle_wait")
	if sw.Timing.Attempts != 10 {
		t.Fatalf("happy-multi settle_wait attempts = %d, want the manifest's 10", sw.Timing.Attempts)
	}
	if sw.Group != "settlement" {
		t.Fatalf("settle_wait group = %q, want settlement", sw.Group)
	}

	// Declines are predicted with their structural source.
	if pp := find(byID["declined-payment"], "settle_wait"); pp.Disposition != phase.PlanDeclined || pp.DeclineSource != phase.DeclinedByCase {
		t.Fatalf("declined-payment settle_wait plan = %+v", pp)
	}
	// A When-gated phase is honestly conditional, not falsely predicted.
	if pp := find(byID["happy-single"], "refund_audit"); pp.Disposition != phase.PlanConditional {
		t.Fatalf("refund_audit disposition = %q, want conditional", pp.Disposition)
	}
}

// --- the green run: suites, observers, redaction, subtests ------------------

func TestSmokeSuiteGreenUnderObservers(t *testing.T) {
	sys, cases := newSuite(t)
	smoke, err := phase.SelectByTags(cases, "smoke")
	if err != nil {
		t.Fatalf("SelectByTags: %v", err)
	}
	if len(smoke) != 1 || smoke[0].ID() != "happy-single" {
		t.Fatalf("smoke selection = %d case(s), want exactly happy-single", len(smoke))
	}

	rec, observe := phasetest.RecordEvents()
	cfg := fastConfig()
	cfg.RedactPatterns = []string{`auth-[a-z0-9-]+`}
	// The frozen legacy projections ride the same stream: WithProgress
	// and WithCaseObserver must keep working beside WithObserver.
	var progress []phase.ProgressEvent
	var observed []phase.CaseReport
	r := newRunner(t, sys, cfg, observe,
		phase.WithProgress(func(ev phase.ProgressEvent) { progress = append(progress, ev) }),
		phase.WithCaseObserver(func(cr phase.CaseReport) { observed = append(observed, cr) }))
	s, err := r.Start(context.Background(), smoke)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	phasetest.RunAsSubtests(t, s)
	rep := s.Report()
	if rep.Summary.Passed != 1 || rep.ExitCode() != 0 {
		t.Fatalf("summary = %+v, exit %d — the smoke path must be green", rep.Summary, rep.ExitCode())
	}
	// The group teardown ran on the green path: no leaked subscription.
	if n := sys.ActiveSubscriptions(); n != 0 {
		t.Fatalf("%d stream subscription(s) leaked past the group teardown", n)
	}

	// The unified event stream: session brackets, total pairing, group
	// lifecycle, and the live poll heartbeat.
	kinds := rec.Kinds()
	if kinds[0] != phase.SessionStarted || kinds[len(kinds)-1] != phase.SessionFinished {
		t.Fatalf("stream must be bracketed by session events, got %v", kinds)
	}
	count := map[phase.EventKind]int{}
	for _, k := range kinds {
		count[k]++
	}
	if count[phase.PhaseStarted] != 7 || count[phase.PhaseFinished] != 7 {
		t.Fatalf("pairing: %d started / %d finished, want 7/7 (every phase, declined ones included)",
			count[phase.PhaseStarted], count[phase.PhaseFinished])
	}
	if count[phase.GroupSetupFinished] == 0 || count[phase.GroupTeardownFinished] == 0 {
		t.Fatal("the settlement group's lifecycle must appear on the stream")
	}
	if count[phase.RetryAttempt] == 0 {
		t.Fatal("the eventually-consistent settle must heartbeat RetryAttempt events")
	}

	// The legacy projections saw the same run. The progress view's own
	// contract: "started" only for phases that actually execute (the
	// When-declined refund_audit never starts there), "finished" for all 7,
	// and the group's setup/teardown appear as synthetic group: rows.
	var started, finished, groupRows int
	for _, ev := range progress {
		if strings.HasPrefix(string(ev.Phase), "group:") {
			groupRows++
			continue
		}
		switch ev.Stage {
		case "started":
			started++
		case "finished":
			finished++
		}
	}
	if started != 6 || finished != 7 || groupRows != 4 {
		t.Fatalf("progress projection saw started=%d finished=%d group=%d, want 6/7/4", started, finished, groupRows)
	}
	if len(observed) != 1 || observed[0].CaseID != "happy-single" {
		t.Fatalf("case-observer projection saw %d report(s), want happy-single once", len(observed))
	}

	// Redaction holds on both surfaces: the artifact and the live stream.
	var buf bytes.Buffer
	if err := rep.WriteJSON(&buf); err != nil {
		t.Fatalf("WriteJSON: %v", err)
	}
	if strings.Contains(buf.String(), "auth-ord") {
		t.Fatal("an auth code reached the report despite RedactPatterns")
	}
	for _, ev := range rec.Events() {
		if cf, ok := ev.(phase.CaseFinishedEvent); ok {
			var evBuf bytes.Buffer
			evRep := phase.Report{Cases: []phase.CaseReport{cf.Report}}
			_ = evRep.WriteJSON(&evBuf)
			if strings.Contains(evBuf.String(), "auth-ord") {
				t.Fatal("an auth code reached the event stream — emission must redact too")
			}
		}
	}
}

func TestSelectorMatchingNothingRefuses(t *testing.T) {
	_, cases := newSuite(t)
	if _, err := phase.SelectByTags(cases, "no-such-tag"); !errors.Is(err, phase.ErrNoMatch) {
		t.Fatalf("err = %v, want ErrNoMatch — running nothing green is the founding defect", err)
	}
}

// --- the honest red run: failures, declines, flakes, quarantine -------------

func TestFullRegressionSequential(t *testing.T) {
	sys, cases := newSuite(t)
	sys.flapLedgerCount = true // the declared flake: first ledger read one short
	rep := report(t, newRunner(t, sys, fastConfig()), cases)

	if err := rep.Verify(); err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if rep.ExitCode() != 1 {
		t.Fatalf("exit = %d, want 1 — declined-payment must fail the run", rep.ExitCode())
	}

	// With case dependencies present, execution order is the deterministic
	// DAG order (sorted ready-set), so happy-multi is the first case to read
	// the ledger: it absorbs the flap and is loudly Flaked — never silently
	// Passed.
	if got := caseRow(t, rep, "happy-multi").Status; got != phase.Flaked {
		t.Fatalf("happy-multi = %v, want Flaked (Tolerate passed on a retry)", got)
	}
	if got := caseRow(t, rep, "happy-single").Status; got != phase.Passed {
		t.Fatalf("happy-single = %v, want Passed", got)
	}

	// The red case: a recorded product failure in authorize, and every
	// declined phase visible with its structural source and reason.
	declined := caseRow(t, rep, "declined-payment")
	if declined.Status != phase.Failed || declined.FailedIn != "authorize" {
		t.Fatalf("declined-payment = %v in %q, want Failed in authorize", declined.Status, declined.FailedIn)
	}
	for _, id := range []phase.ID{"settle_wait", "settle_checks", "ledger", "refund_audit", "audit"} {
		po := phaseRow(t, declined, id)
		if po.Status != phase.NotApplicable || po.DeclineSource != phase.DeclinedByCase || po.Reason == "" {
			t.Fatalf("%s = %+v, want a reasoned NotApplicable declined by the case", id, po)
		}
	}

	// The case dependency was satisfied — happy-multi flaked, and the
	// requirement declared Acceptable: [Passed, Flaked] — so the dependent
	// ran and passed.
	if got := caseRow(t, rep, "billing-report").Status; got != phase.Passed {
		t.Fatalf("billing-report = %v, want Passed", got)
	}
	if got := caseRow(t, rep, "reconcile").Status; got != phase.Passed {
		t.Fatalf("reconcile = %v, want Passed", got)
	}

	// Every non-Active case status is a skip that says why.
	for id, word := range map[string]string{
		"parked-experiment": "quarantined",
		"blocked-refunds":   "blocked",
		"draft-loyalty":     "draft",
	} {
		cr := caseRow(t, rep, id)
		if cr.Status != phase.Skipped || !strings.Contains(cr.Reason, word) {
			t.Fatalf("%s = %v (%q), want Skipped as %s", id, cr.Status, cr.Reason, word)
		}
	}

	// Teardown discipline holds across mixed outcomes.
	if n := sys.ActiveSubscriptions(); n != 0 {
		t.Fatalf("%d stream subscription(s) leaked", n)
	}
}

// --- concurrency: both knobs on, exclusive case honoured --------------------

func TestConcurrentRunKeepsVerdicts(t *testing.T) {
	sys, cases := newSuite(t)
	cfg := fastConfig()
	cfg.MaxCaseConcurrency = 2
	cfg.MaxPhaseConcurrency = 2
	rep := report(t, newRunner(t, sys, cfg), cases)

	if err := rep.Verify(); err != nil {
		t.Fatalf("Verify: %v", err)
	}
	want := map[string]phase.Status{
		"happy-single":      phase.Passed,
		"happy-multi":       phase.Passed,
		"declined-payment":  phase.Failed,
		"billing-report":    phase.Passed,
		"reconcile":         phase.Passed, // exclusive: drains, runs alone
		"parked-experiment": phase.Skipped,
		"blocked-refunds":   phase.Skipped,
		"draft-loyalty":     phase.Skipped,
	}
	for id, exp := range want {
		if got := caseRow(t, rep, id).Status; got != exp {
			t.Fatalf("%s = %v under concurrency, want %v — verdicts must not depend on scheduling", id, got, exp)
		}
	}
	if n := sys.ActiveSubscriptions(); n != 0 {
		t.Fatalf("%d stream subscription(s) leaked under concurrency", n)
	}
}

// --- case dependencies: the refusal and the loud skip -----------------------

func TestDependencyOnAbsentCaseIsRefused(t *testing.T) {
	sys, cases := newSuite(t)
	_, err := newRunner(t, sys, fastConfig()).Explain(pick(t, cases, "billing-report"))
	if err == nil || !strings.Contains(err.Error(), "happy-multi") {
		t.Fatalf("err = %v — a dependency on a case not in the run must be refused by name", err)
	}
}

func TestUnmetDependencySkipsLoudly(t *testing.T) {
	mini := []byte(`
cases:
  - id: parent
    fixtures: [seed-catalog]
    declines:
      settle_wait: "declined orders never settle"
      settle_checks: "declined orders never settle"
      ledger: "no settlement, no ledger rows"
      refund_audit: "refunds are the processor's own flow"
      audit: "audit covers settled orders only"
    params: {scenario: declined, entities: 1}
  - id: child
    fixtures: [seed-catalog]
    params: {scenario: happy, entities: 1}
`)
	sys := newCheckoutSystem()
	specs, err := config.ParseCases(mini)
	if err != nil {
		t.Fatalf("ParseCases: %v", err)
	}
	reg := config.Registry{"seed-catalog": func() phase.Fixture { return &catalogFixture{sys} }}
	parent, err := specs[0].Case(reg)
	if err != nil {
		t.Fatal(err)
	}
	child, err := specs[1].Case(reg)
	if err != nil {
		t.Fatal(err)
	}
	cases := []phase.Case{
		&checkoutCase{SpecCase: parent},
		&checkoutCase{SpecCase: child, deps: []phase.CaseRequirement{
			{CaseID: "parent", Acceptable: []phase.Status{phase.Passed}},
		}},
	}

	rep := report(t, newRunner(t, sys, fastConfig()), cases)
	cr := caseRow(t, rep, "child")
	if cr.Status != phase.Skipped || cr.DependencyFailure == nil {
		t.Fatalf("child = %v (depfail %v), want Skipped with a structured DependencyFailure", cr.Status, cr.DependencyFailure)
	}
	if df := cr.DependencyFailure; df.CaseID != "parent" || df.Actual != phase.Failed {
		t.Fatalf("DependencyFailure = %+v, want parent/Failed", df)
	}
}

// --- the mutation gates: is the suite's green actually evidence? ------------

// gutted rebuilds the pipeline with the ledger assertions removed. The suite
// stays green: nothing else covers what ledger asserts, and the visible
// zero on its outcome row is how that gap becomes noticeable.
func TestMutationGateGutted(t *testing.T) {
	sys, cases := newSuite(t)
	p := phase.NewPipeline(
		&submitPhase{sys},
		&authorizePhase{sys},
		&settleWaitPhase{sys},
		&settleChecksPhase{sys},
		phasetest.Gutted(&ledgerPhase{sys}),
		&refundAuditPhase{sys},
		&auditPhase{sys},
	).Group(settlementGroup(sys))
	r, err := phase.NewRunner(p, fastConfig())
	if err != nil {
		t.Fatalf("NewRunner: %v", err)
	}
	rep := report(t, r, pick(t, cases, "happy-single"))
	cr := caseRow(t, rep, "happy-single")
	if cr.Status != phase.Passed {
		t.Fatalf("gutted run = %v — the gate expects green to survive, exposing the coverage gap", cr.Status)
	}
	if po := phaseRow(t, cr, "ledger"); po.Results != 0 {
		t.Fatalf("gutted ledger recorded %d result(s), want the loud 0", po.Results)
	}

	// Control: un-gutted, the phase records real evidence.
	sys2, cases2 := newSuite(t)
	rep2 := report(t, newRunner(t, sys2, fastConfig()), pick(t, cases2, "happy-single"))
	if po := phaseRow(t, caseRow(t, rep2, "happy-single"), "ledger"); po.Results == 0 {
		t.Fatal("control run: ledger must record evidence")
	}
}

// TestMutationGateAlwaysPass checks which phase's judgement a red verdict
// rides on: forcing authorize's results to pass flips declined-payment
// green, showing authorize was the phase doing the work.
func TestMutationGateAlwaysPass(t *testing.T) {
	sys, cases := newSuite(t)
	baseline := report(t, newRunner(t, sys, fastConfig()), pick(t, cases, "declined-payment"))
	if got := caseRow(t, baseline, "declined-payment").Status; got != phase.Failed {
		t.Fatalf("baseline = %v, want Failed", got)
	}

	sys2, cases2 := newSuite(t)
	p := phase.NewPipeline(
		&submitPhase{sys2},
		phasetest.AlwaysPass(&authorizePhase{sys2}),
		&settleWaitPhase{sys2},
		&settleChecksPhase{sys2},
		&ledgerPhase{sys2},
		&refundAuditPhase{sys2},
		&auditPhase{sys2},
	).Group(settlementGroup(sys2))
	r, err := phase.NewRunner(p, fastConfig())
	if err != nil {
		t.Fatalf("NewRunner: %v", err)
	}
	rep := report(t, r, pick(t, cases2, "declined-payment"))
	if got := caseRow(t, rep, "declined-payment").Status; got != phase.Passed {
		t.Fatalf("mutated run = %v, want Passed — the flip proves authorize's judgement carried the verdict", got)
	}
}

// --- sharding: two CI jobs, one trustworthy report --------------------------

func TestShardedRunsMergeIntoOneReport(t *testing.T) {
	sys1, all1 := newSuite(t)
	rep1 := report(t, newRunner(t, sys1, fastConfig()), pick(t, all1, "happy-single", "declined-payment"))

	sys2, all2 := newSuite(t)
	rep2 := report(t, newRunner(t, sys2, fastConfig()), pick(t, all2, "happy-multi", "billing-report"))

	merged, err := phase.MergeReports(rep1, rep2)
	if err != nil {
		t.Fatalf("MergeReports: %v", err)
	}
	if merged.Summary.Total != 4 || merged.Summary.Passed != 3 || merged.Summary.Failed != 1 {
		t.Fatalf("merged summary = %+v, want 4 total / 3 passed / 1 failed", merged.Summary)
	}
	if merged.ExitCode() != 1 {
		t.Fatalf("merged exit = %d, want the shard failure to survive the merge", merged.ExitCode())
	}

	// The hand-called, paste-time redaction forms (beside the config-driven
	// floor): scrub a named key and a pattern, and the marker stays visible —
	// a redacted value reads [REDACTED], it never silently vanishes.
	merged.Redact("order_id")
	merged.RedactMatching(regexp.MustCompile(`ord-\d+`))
	var buf bytes.Buffer
	if err := merged.WriteJSON(&buf); err != nil {
		t.Fatalf("WriteJSON: %v", err)
	}
	out := buf.String()
	if strings.Contains(out, "ord-0") {
		t.Fatal("an order id survived hand-called redaction")
	}
	if !strings.Contains(out, "[REDACTED]") {
		t.Fatal("redaction must leave its visible marker, never a silent hole")
	}
}

// --- the operator kill-switch: deliberate, loud coverage loss ---------------

func TestOperatorKillSwitchIsLoudCoverageLoss(t *testing.T) {
	sys, cases := newSuite(t)
	off := false
	cfg := fastConfig()
	cfg.Phases = map[phase.ID]phase.Settings{"audit": {Enabled: &off}}
	rep := report(t, newRunner(t, sys, cfg), pick(t, cases, "happy-single"))

	po := phaseRow(t, caseRow(t, rep, "happy-single"), "audit")
	if po.Status != phase.Disabled || po.DeclineSource != phase.DeclinedByConfig {
		t.Fatalf("audit = %+v, want Disabled by configuration", po)
	}
	// The report's NotVerified section names the loss once and loudly —
	// deliberate coverage loss must never sum silently out of per-case rows.
	found := false
	for _, line := range rep.NotVerified {
		if strings.Contains(line, `"audit"`) {
			found = true
		}
	}
	if !found {
		t.Fatalf("NotVerified = %v — the disabled phase must be named", rep.NotVerified)
	}
	if rep.ExitCode() != 0 {
		t.Fatal("a disabled phase is visible coverage loss, not a CI failure")
	}
}

// --- declaration conformance: the contracts checked in-unit -----------------

func TestDeclarationsConform(t *testing.T) {
	sys, cases := newSuite(t)
	for _, c := range cases {
		phasetest.ConformanceCase(t, c)
	}
	phasetest.ConformanceGroup(t, settlementGroup(sys))
}

// --- the evidence retention cap: bounded, never silent ----------------------

func TestObservationCapIsLoud(t *testing.T) {
	sys, cases := newSuite(t)
	cfg := fastConfig()
	cfg.MaxObservationsPerCase = 2
	rep := report(t, newRunner(t, sys, cfg), pick(t, cases, "happy-single"))

	cr := caseRow(t, rep, "happy-single")
	if len(cr.Observations) != 3 { // the 2 retained + the marker
		t.Fatalf("kept %d observation(s), want 2 + the truncation marker", len(cr.Observations))
	}
	last := cr.Observations[len(cr.Observations)-1]
	if !strings.Contains(last.Name, "retention limit") || !strings.Contains(last.Value.(string), "dropped") {
		t.Fatalf("marker = %+v — the cap must say exactly what it cost", last)
	}
}

// --- unit-testing one hook-bearing phase, no Runner -------------------------

func TestSubmitPhaseInIsolation(t *testing.T) {
	sys, cases := newSuite(t)
	c := pick(t, cases, "happy-single")[0]
	timing := phase.Timing{Attempts: 3, Interval: time.Millisecond}

	// Unseeded: the Before hook refuses — the row is Errored in the before
	// stage (Run never invoked). The recorded violation is the two-fact
	// pattern's product half: in a full run the case derives Failed from it.
	run, _ := phasetest.RunFor(t, c, "submit", timing)
	po := phasetest.InvokePhase(context.Background(), &submitPhase{sys}, run)
	if po.Status != phase.Errored || po.Stage != phase.StageBefore {
		t.Fatalf("unseeded submit = %v in stage %q, want Errored in before", po.Status, po.Stage)
	}
	if !strings.Contains(po.Reason, "catalog not seeded") {
		t.Fatalf("reason = %q, want the precondition named", po.Reason)
	}

	// Seeded: the same arc passes. (Results/Failing counts on the row are
	// computed by the Runner at landing, so InvokePhase reads them as zero —
	// the status and stage are the row's facts here.)
	sys.SeedCatalog()
	run2, _ := phasetest.RunFor(t, c, "submit", timing)
	po2 := phasetest.InvokePhase(context.Background(), &submitPhase{sys}, run2)
	if po2.Status != phase.Passed || po2.Stage != "" {
		t.Fatalf("seeded submit = %+v, want a plain Passed row", po2)
	}
}
