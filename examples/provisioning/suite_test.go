// Copyright 2026 The Phase Contributors
// SPDX-License-Identifier: MIT

package provisioning

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	phase "github.com/wow-qe/phase-go"
	"github.com/wow-qe/phase-go/phasetest"
)

// This file is the example's demonstration: a green suite, a red run that
// stays honest about it, a deliberately disabled phase staying visible as
// such, the mutation gate that proves the green run meant something, and
// determinism across two otherwise-identical runs. Read it in that order —
// it is the five-minute declaration -> red -> green walkthrough the root
// README promises (see this package's own README.md).

func defaultConfig() phase.Config {
	return phase.Config{Defaults: phase.Timing{Attempts: 3, Interval: time.Millisecond, Timeout: time.Second}}
}

func newRunner(t *testing.T, pipeline *phase.Pipeline, cfg phase.Config) *phase.Runner {
	t.Helper()
	r, err := phase.NewRunner(pipeline, cfg)
	if err != nil {
		t.Fatalf("NewRunner: %v", err)
	}
	return r
}

// --- TestGreenRun ------------------------------------------------------

func TestGreenRun(t *testing.T) {
	sys := NewSystem()
	r := newRunner(t, Pipeline(sys), defaultConfig())

	// A single entity, nothing unusual: submitted, discovered, settled
	// succeeded, seen by the provider, ledgered.
	happy := NewCase("happy-single-entity",
		[]ItemExpectation{{EntityID: "e1", State: "succeeded"}},
		"active", nil, "", nil)

	// Two entities, one of each terminal outcome — and the case DECLARES
	// entity two's failure, so the case still passes: a per-entity
	// failure the case predicted is not a case failure.
	mixed := NewCase("mixed-entities-one-fails-as-expected",
		[]ItemExpectation{
			{EntityID: "m1", State: "succeeded"},
			{EntityID: "m2", State: "failed"},
		},
		"active", nil, "m2", sys.Provider)

	// A negative case: modeled as a request that never reaches the
	// provider or the ledger, so it skips both phases WITH reasons rather
	// than letting them run against data that was never going to be
	// meaningful.
	negative := NewCase("negative-skips-provider-and-ledger",
		[]ItemExpectation{{EntityID: "n1", State: "failed"}},
		"active",
		map[phase.ID]string{
			"provider_side": "example: this scenario models a request rejected before it ever reaches the provider",
			"ledger":        "example: a rejected request never settles, so there is nothing to check in the ledger",
		},
		"n1", sys.Provider)

	for _, c := range []*Case{happy, mixed, negative} {
		phasetest.ConformanceCase(t, c)
	}

	session, err := r.Start(context.Background(), []phase.Case{happy, mixed, negative})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}

	report := session.Report()
	if err := report.Verify(); err != nil {
		t.Fatalf("Verify: %v — the framework's own report is inconsistent", err)
	}
	if got := report.ExitCode(); got != 0 {
		t.Fatalf("ExitCode = %d, want 0", got)
	}

	want := phase.Summary{Total: 3, Passed: 3}
	if report.Summary != want {
		t.Fatalf("Summary = %+v, want %+v", report.Summary, want)
	}
	for _, cr := range report.Cases {
		if cr.Status != phase.Passed {
			t.Errorf("case %q status = %s, want Passed", cr.CaseID, cr.Status)
		}
	}
}

// --- TestRedRunIsHonest --------------------------------------------------

func TestRedRunIsHonest(t *testing.T) {
	sys := NewSystem()
	r := newRunner(t, Pipeline(sys), defaultConfig())

	// The case DECLARES success. The fixture injects a fault that makes
	// the fake provider actually reject this entity — so the declaration
	// and reality disagree, and the case must say so.
	red := NewCase("declares-success-but-provider-rejects",
		[]ItemExpectation{{EntityID: "r1", State: "succeeded"}},
		"active", nil, "r1", sys.Provider)

	session, err := r.Start(context.Background(), []phase.Case{red})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	report := session.Report()
	if err := report.Verify(); err != nil {
		t.Fatalf("Verify: %v", err)
	}

	cr := report.Cases[0]
	if cr.Status != phase.Failed {
		t.Fatalf("status = %s, want Failed", cr.Status)
	}
	if cr.FailedIn != "settle_checks" {
		t.Fatalf("FailedIn = %q, want %q", cr.FailedIn, "settle_checks")
	}
	if got := report.ExitCode(); got != 1 {
		t.Fatalf("ExitCode = %d, want 1", got)
	}

	found := false
	for _, ar := range cr.Results {
		if ar.Phase != cr.FailedIn || ar.Result.Passed {
			continue
		}
		found = true
		if ar.Result.Expected != "succeeded" {
			t.Fatalf("Expected = %v, want %q", ar.Result.Expected, "succeeded")
		}
		if ar.Result.Actual != "failed" {
			t.Fatalf("Actual = %v, want %q", ar.Result.Actual, "failed")
		}
	}
	if !found {
		t.Fatal("no failing ResultView attributed to FailedIn")
	}
}

// --- TestDisabledPhaseIsVisible -------------------------------------------

func TestDisabledPhaseIsVisible(t *testing.T) {
	sys := NewSystem()
	off := false
	cfg := defaultConfig()
	cfg.Phases = map[phase.ID]phase.Settings{"provider_side": {Enabled: &off}}
	r := newRunner(t, Pipeline(sys), cfg)

	happy := NewCase("happy-single-entity",
		[]ItemExpectation{{EntityID: "e1", State: "succeeded"}},
		"active", nil, "", nil)

	session, err := r.Start(context.Background(), []phase.Case{happy})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	report := session.Report()
	if err := report.Verify(); err != nil {
		t.Fatalf("Verify: %v", err)
	}

	cr := report.Cases[0]
	found := false
	for _, po := range cr.Phases {
		if po.ID != "provider_side" {
			continue
		}
		found = true
		if po.Status != phase.Disabled {
			t.Fatalf("provider_side status = %s, want Disabled", po.Status)
		}
	}
	if !found {
		t.Fatal("no outcome recorded for provider_side")
	}

	named := false
	for _, msg := range report.NotVerified {
		if strings.Contains(msg, "provider_side") {
			named = true
		}
	}
	if !named {
		t.Fatalf("NotVerified = %v, want an entry naming provider_side", report.NotVerified)
	}
}

// --- TestMutationGateGoesRed -----------------------------------------------

// TestMutationGateGoesRed is the DoD's mutation gate: it proves the suite's
// green is not decorative by removing the one phase that would have caught
// this case's defect and checking that the case stops reporting Failed.
func TestMutationGateGoesRed(t *testing.T) {
	// A case whose declared expectation is simply WRONG: the entity is
	// never faulted, so it actually succeeds, but the case declares it
	// should fail. Only settle_checks compares declared-vs-actual state;
	// provider_side and ledger both assert what actually happened, so
	// neither one reacts to this — the isolation the gate needs.
	buildCase := func() *Case {
		return NewCase("wrong-declared-expectation",
			[]ItemExpectation{{EntityID: "g1", State: "failed"}},
			"active", nil, "", nil)
	}

	// Baseline: settle_checks present and doing its job. The suite must be
	// red, or this test proves nothing.
	baselineSys := NewSystem()
	baselineRunner := newRunner(t, Pipeline(baselineSys), defaultConfig())
	baselineSession, err := baselineRunner.Start(context.Background(), []phase.Case{buildCase()})
	if err != nil {
		t.Fatalf("Start (baseline): %v", err)
	}
	baseline := baselineSession.Report().Cases[0]
	if baseline.Status != phase.Failed {
		t.Fatalf("baseline status = %s, want Failed — settle_checks should have caught the mismatch", baseline.Status)
	}
	if baseline.FailedIn != "settle_checks" {
		t.Fatalf("baseline FailedIn = %q, want %q", baseline.FailedIn, "settle_checks")
	}

	// Mutation: same identity and wiring (phasetest.Gutted preserves both),
	// Run does nothing. Nothing else in the pipeline checks the case's
	// declared expectation against what actually happened, so the same
	// case must now report Passed.
	guttedSys := NewSystem()
	guttedPipeline := PipelineWithSettleChecks(guttedSys, phasetest.Gutted(&settleChecks{}))
	guttedRunner := newRunner(t, guttedPipeline, defaultConfig())
	guttedSession, err := guttedRunner.Start(context.Background(), []phase.Case{buildCase()})
	if err != nil {
		t.Fatalf("Start (gutted): %v", err)
	}
	gutted := guttedSession.Report().Cases[0]
	if gutted.Status != phase.Passed {
		t.Fatalf("gutted status = %s, want Passed — the baseline's red depended on settle_checks actually asserting", gutted.Status)
	}
}

// --- TestDeterministicReports ----------------------------------------------

func TestDeterministicReports(t *testing.T) {
	build := func() (*phase.Report, error) {
		sys := NewSystem()
		r, err := phase.NewRunner(Pipeline(sys), defaultConfig())
		if err != nil {
			return nil, err
		}
		happy := NewCase("happy-single-entity",
			[]ItemExpectation{{EntityID: "e1", State: "succeeded"}},
			"active", nil, "", nil)
		session, err := r.Start(context.Background(), []phase.Case{happy})
		if err != nil {
			return nil, err
		}
		return session.Report(), nil
	}

	rep1, err := build()
	if err != nil {
		t.Fatalf("build 1: %v", err)
	}
	rep2, err := build()
	if err != nil {
		t.Fatalf("build 2: %v", err)
	}

	var buf1, buf2 bytes.Buffer
	if err := rep1.WriteJSON(&buf1); err != nil {
		t.Fatalf("WriteJSON 1: %v", err)
	}
	if err := rep2.WriteJSON(&buf2); err != nil {
		t.Fatalf("WriteJSON 2: %v", err)
	}

	norm1 := normalizeReport(t, buf1.Bytes())
	norm2 := normalizeReport(t, buf2.Bytes())
	if !bytes.Equal(norm1, norm2) {
		t.Fatalf("normalized reports differ:\n--- run 1 ---\n%s\n--- run 2 ---\n%s", norm1, norm2)
	}
}

// normalizeReport strips the fields two independently-run but otherwise
// identical sessions can never agree on byte-for-byte — the session id and
// every timestamp — so what remains can be diffed directly. Round-tripping
// through map[string]any rather than the typed Report also means both
// sides re-marshal with Go's sorted-map-key ordering, so the comparison
// does not depend on WriteJSON's own field order matching itself.
func normalizeReport(t *testing.T, raw []byte) []byte {
	t.Helper()
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("normalizeReport: unmarshal: %v", err)
	}
	if session, ok := doc["session"].(map[string]any); ok {
		session["id"] = ""
		session["started"] = ""
		session["finished"] = ""
	}
	if cases, ok := doc["cases"].([]any); ok {
		for _, c := range cases {
			cm, ok := c.(map[string]any)
			if !ok {
				continue
			}
			cm["started"] = ""
			cm["finished"] = ""
			// The correlation id is allocated from crypto/rand per run —
			// volatile by design, normalised like the session id.
			delete(cm, "correlation")
			if obs, ok := cm["observations"].([]any); ok {
				for _, o := range obs {
					if om, ok := o.(map[string]any); ok {
						// Schema key is "at" since the report fixed its JSON
						// surface; setting the old "At" here both missed the
						// real timestamp AND injected a phantom key - which
						// is exactly how the casing break was proven
						// had shipped invisibly.
						om["at"] = ""
					}
				}
			}
		}
	}
	out, err := json.Marshal(doc)
	if err != nil {
		t.Fatalf("normalizeReport: marshal: %v", err)
	}
	return out
}
