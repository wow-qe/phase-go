// Copyright 2026 The Phase Contributors
// SPDX-License-Identifier: MIT

package phase

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/wow-qe/phase-go/result"
)

// Start is where every teardown-and-containment guarantee lives: teardown on every path, panic
// containment, cancellation as Errored-not-Failed, dependent-phase pruning,
// and a SINGLE derivation of each case's outcome from its recorded evidence.

// recordingPhase runs a callback and journals the visit.
type recordingPhase struct {
	stubPhase
	visits *[]ID
	do     func(context.Context, *Run) error
}

func (p *recordingPhase) Run(ctx context.Context, r *Run) error {
	if p.visits != nil {
		*p.visits = append(*p.visits, p.id)
	}
	if p.do == nil {
		return nil
	}
	return p.do(ctx, r)
}

func passingPhase(id ID, visits *[]ID, deps ...ID) *recordingPhase {
	return &recordingPhase{
		stubPhase: stubPhase{id: id, deps: deps},
		visits:    visits,
		do: func(_ context.Context, r *Run) error {
			r.Record(result.Compared(string(id)+" check", []bool{true}))
			return nil
		},
	}
}

func startSession(t *testing.T, r *Runner, cases ...Case) *Session {
	t.Helper()
	s, err := r.Start(context.Background(), cases)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	return s
}

func caseReport(t *testing.T, s *Session, id string) CaseReport {
	t.Helper()
	for _, cr := range s.Cases() {
		if cr.CaseID == id {
			return cr
		}
	}
	t.Fatalf("no report for case %q", id)
	return CaseReport{}
}

// --- the happy path ---------------------------------------------------------

func TestPhasesRunInDependencyOrderAndTheCasePasses(t *testing.T) {
	var visits []ID
	r := mustRunner(t, Config{Defaults: validTiming()},
		passingPhase("settle", &visits, "discover"),
		passingPhase("submit", &visits),
		passingPhase("discover", &visits, "submit"),
	)
	s := startSession(t, r, &stubCase{id: "happy"})
	if got := [3]ID{visits[0], visits[1], visits[2]}; got != [3]ID{"submit", "discover", "settle"} {
		t.Fatalf("visit order %v", visits)
	}
	cr := caseReport(t, s, "happy")
	if cr.Status != Passed {
		t.Fatalf("status = %s", cr.Status)
	}
}

// --- skips, in their three distinct flavours --------------------------------

func TestCaseSelectingOutIsNotApplicableWithTheReason(t *testing.T) {
	var visits []ID
	r := mustRunner(t, Config{Defaults: validTiming()},
		passingPhase("submit", &visits),
		passingPhase("provider_side", &visits),
	)
	s := startSession(t, r, &stubCase{
		id: "negative",
		selects: func(id ID) (bool, string) {
			if id == "provider_side" {
				return false, "rejected at ingest; never reaches the provider"
			}
			return true, ""
		},
	})
	cr := caseReport(t, s, "negative")
	po := phaseOutcome(t, cr, "provider_side")
	if po.Status != NotApplicable || po.Reason == "" {
		t.Fatalf("outcome = %+v", po)
	}
	// The reason must name its source — a case-declined skip and a
	// phase-declined skip are different audit facts.
	if !strings.HasPrefix(po.Reason, "case declined: ") {
		t.Fatalf("reason = %q, want the 'case declined: ' source prefix", po.Reason)
	}
	for _, v := range visits {
		if v == "provider_side" {
			t.Fatal("a deselected phase must not run")
		}
	}
	if cr.Status != Passed {
		t.Fatalf("the case still passes on its remaining evidence; status = %s", cr.Status)
	}
}

func TestOperatorDisabledPhaseIsDisabledNotNotApplicable(t *testing.T) {
	// Deliberate coverage loss must be visible AS deliberate: an
	// operator switch and a case declaration are different facts.
	off := false
	var visits []ID
	r := mustRunner(t, Config{
		Defaults: validTiming(),
		Phases:   map[ID]Settings{"provider_side": {Enabled: &off}},
	},
		passingPhase("submit", &visits),
		passingPhase("provider_side", &visits),
	)
	s := startSession(t, r, &stubCase{id: "happy"})
	po := phaseOutcome(t, caseReport(t, s, "happy"), "provider_side")
	if po.Status != Disabled {
		t.Fatalf("status = %s, want Disabled", po.Status)
	}
}

func TestAppliesToDecliningIsNotApplicable(t *testing.T) {
	r := mustRunner(t, Config{Defaults: validTiming()},
		&recordingPhase{stubPhase: stubPhase{id: "submit"}, do: func(_ context.Context, run *Run) error {
			run.Record(result.Compared("submit check", []bool{true}))
			return nil
		}},
		&stubPhase{id: "verify_frozen", applies: Skip("case does not declare no-transition")},
	)
	s := startSession(t, r, &stubCase{id: "happy"})
	po := phaseOutcome(t, caseReport(t, s, "happy"), "verify_frozen")
	if po.Status != NotApplicable || po.Reason == "" {
		t.Fatalf("outcome = %+v", po)
	}
	if !strings.HasPrefix(po.Reason, "phase declined: ") {
		t.Fatalf("reason = %q, want the 'phase declined: ' source prefix", po.Reason)
	}
}

// --- failure, error, panic, cancellation ------------------------------------

func TestAFailedResultFailsTheCaseAndNamesThePhase(t *testing.T) {
	var visits []ID
	r := mustRunner(t, Config{Defaults: validTiming()},
		passingPhase("submit", &visits),
		&recordingPhase{stubPhase: stubPhase{id: "settle", deps: []ID{"submit"}}, visits: &visits,
			do: func(_ context.Context, run *Run) error {
				run.Record(result.Failed("state mismatch", "expected 8, saw 9"))
				return nil
			}},
		passingPhase("ledger", &visits, "settle"),
	)
	s := startSession(t, r, &stubCase{id: "sad"})
	cr := caseReport(t, s, "sad")
	if cr.Status != Failed {
		t.Fatalf("status = %s", cr.Status)
	}
	if cr.FailedIn != "settle" {
		t.Fatalf("FailedIn = %q — the consumer's policy switches on this", cr.FailedIn)
	}
	// A failed comparison is a completed judgement, not a reason to stop
	// gathering evidence: later phases still run.
	if visits[len(visits)-1] != "ledger" {
		t.Fatalf("later phases must still run; visits = %v", visits)
	}
}

func TestAPhaseErrorMarksErroredAndPrunesDependents(t *testing.T) {
	var visits []ID
	r := mustRunner(t, Config{Defaults: validTiming()},
		&recordingPhase{stubPhase: stubPhase{id: "discover"}, visits: &visits,
			do: func(context.Context, *Run) error { return errors.New("connection refused") }},
		passingPhase("settle", &visits, "discover"), // depends on the erroring phase
		passingPhase("audit", &visits),              // independent
	)
	s := startSession(t, r, &stubCase{id: "outage"})
	cr := caseReport(t, s, "outage")
	if cr.Status != Errored {
		t.Fatalf("an environment error is Errored, never Failed; got %s", cr.Status)
	}
	po := phaseOutcome(t, cr, "settle")
	if po.Status != NotApplicable || po.Reason == "" {
		t.Fatalf("a dependent of an errored phase is pruned WITH a reason; got %+v", po)
	}
	found := false
	for _, v := range visits {
		found = found || v == "audit"
	}
	if !found {
		t.Fatal("an INDEPENDENT phase still runs — evidence is not abandoned wholesale")
	}
}

func TestAPanicIsContainedToItsCase(t *testing.T) {
	r := mustRunner(t, Config{Defaults: validTiming()},
		&recordingPhase{stubPhase: stubPhase{id: "submit"}, do: func(context.Context, *Run) error {
			panic("consumer bug")
		}},
	)
	s := startSession(t, r, &stubCase{id: "panicky"}, &stubCase{id: "innocent"})
	if caseReport(t, s, "panicky").Status != Errored {
		t.Fatal("a panicking case is Errored")
	}
	if caseReport(t, s, "innocent").Status != Errored {
		// innocent has zero results (its only phase... ) — careful: innocent
		// runs the same phase, which panics for it too in this construction.
		t.Skip("covered below with distinct phases")
	}
}

func TestAPanicDoesNotTakeDownTheBatch(t *testing.T) {
	calls := 0
	r := mustRunner(t, Config{Defaults: validTiming()},
		&recordingPhase{stubPhase: stubPhase{id: "submit"}, do: func(_ context.Context, run *Run) error {
			calls++
			if calls == 1 {
				panic("consumer bug in case one")
			}
			run.Record(result.Compared("submit check", []bool{true}))
			return nil
		}},
	)
	s := startSession(t, r, &stubCase{id: "panicky"}, &stubCase{id: "innocent"})
	if caseReport(t, s, "panicky").Status != Errored {
		t.Fatal("panicky must be Errored")
	}
	if got := caseReport(t, s, "innocent").Status; got != Passed {
		t.Fatalf("the batch continues; innocent = %s", got)
	}
}

func TestCancellationIsErroredNeverFailed(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	r := mustRunner(t, Config{Defaults: validTiming()},
		&recordingPhase{stubPhase: stubPhase{id: "submit"}, do: func(context.Context, *Run) error {
			cancel() // cancelled mid-case
			return nil
		}},
	)
	s, err := r.Start(ctx, []Case{&stubCase{id: "inflight"}, &stubCase{id: "unstarted"}})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if got := caseReport(t, s, "inflight").Status; got != Errored {
		t.Fatalf("in-flight on cancel = %s, want Errored — a cancelled suite must not read as a product failure", got)
	}
	if got := caseReport(t, s, "unstarted").Status; got != Errored {
		t.Fatalf("unstarted on cancel = %s, want Errored(cancelled before start)", got)
	}
}

// --- fixtures ---------------------------------------------------------------

type journalFixture struct {
	name      string
	journal   *[]string
	failSetup bool
}

func (f *journalFixture) Setup(_ context.Context, _ *Run) error {
	*f.journal = append(*f.journal, "setup:"+f.name)
	if f.failSetup {
		return errors.New(f.name + " setup failed")
	}
	return nil
}
func (f *journalFixture) Teardown(_ context.Context, _ *Run) error {
	*f.journal = append(*f.journal, "teardown:"+f.name)
	return nil
}

func TestFixturesSetupInOrderTeardownInReverseAlways(t *testing.T) {
	var journal []string
	r := mustRunner(t, Config{Defaults: validTiming()},
		&recordingPhase{stubPhase: stubPhase{id: "submit"}, do: func(context.Context, *Run) error {
			panic("even a panic must not skip teardown")
		}},
	)
	startSession(t, r, &stubCase{id: "seeded", fixtures: []Fixture{
		&journalFixture{name: "db", journal: &journal},
		&journalFixture{name: "mock", journal: &journal},
	}})
	want := []string{"setup:db", "setup:mock", "teardown:mock", "teardown:db"}
	if len(journal) != 4 {
		t.Fatalf("journal = %v", journal)
	}
	for i := range want {
		if journal[i] != want[i] {
			t.Fatalf("journal = %v, want %v", journal, want)
		}
	}
}

func TestASetupFailureSkipsPhasesButTearsDownWhatWasBuilt(t *testing.T) {
	var journal []string
	var visits []ID
	r := mustRunner(t, Config{Defaults: validTiming()},
		passingPhase("submit", &visits),
	)
	s := startSession(t, r, &stubCase{id: "seeded", fixtures: []Fixture{
		&journalFixture{name: "db", journal: &journal},
		&journalFixture{name: "mock", journal: &journal, failSetup: true},
		&journalFixture{name: "never", journal: &journal},
	}})
	if len(visits) != 0 {
		t.Fatal("a case whose world could not be built must not run phases")
	}
	// db and mock were attempted; never was not. Teardown runs for the two
	// that were, in reverse.
	want := []string{"setup:db", "setup:mock", "teardown:mock", "teardown:db"}
	for i := range want {
		if journal[i] != want[i] {
			t.Fatalf("journal = %v, want %v", journal, want)
		}
	}
	if caseReport(t, s, "seeded").Status != Errored {
		t.Fatal("a setup failure is Errored — the case did not fail its assertions")
	}
}

// --- case-level statuses and the vacuous-case rule --------------------------

func TestNonActiveCasesAreSkippedWithTheStatusAsReason(t *testing.T) {
	r := mustRunner(t, Config{Defaults: validTiming()}, passingPhase("submit", nil))
	s := startSession(t, r,
		&stubCase{id: "q", status: Quarantined},
		&stubCase{id: "b", status: Blocked},
		&stubCase{id: "d", status: Draft},
	)
	for _, id := range []string{"q", "b", "d"} {
		if got := caseReport(t, s, id).Status; got != Skipped {
			t.Fatalf("%s = %s, want Skipped", id, got)
		}
	}
}

func TestACaseWithZeroResultsIsNotAPass(t *testing.T) {
	// The case-level form of the library's founding rule: a case whose every
	// phase declined recorded nothing, and "passed having asserted nothing"
	// is the defect. It reports NotApplicable — a visible coverage gap.
	r := mustRunner(t, Config{Defaults: validTiming()},
		&stubPhase{id: "submit", applies: Skip("not for this case")},
	)
	s := startSession(t, r, &stubCase{id: "vacuous"})
	if got := caseReport(t, s, "vacuous").Status; got != NotApplicable {
		t.Fatalf("zero evidence = %s, want NotApplicable — never Passed", got)
	}
}

// --- timing -----------------------------------------------------------------

func TestPerCaseTimingOverridesThePhaseResolution(t *testing.T) {
	var seen Timing
	r := mustRunner(t, Config{Defaults: validTiming()},
		&recordingPhase{stubPhase: stubPhase{id: "settle"}, do: func(_ context.Context, run *Run) error {
			_, seen = run.currentTiming()
			run.Record(result.Compared("x", []bool{true}))
			return nil
		}},
	)
	startSession(t, r, &stubCase{
		id:     "slow",
		timing: map[ID]Timing{"settle": {Attempts: 40}},
	})
	if seen.Attempts != 40 {
		t.Fatalf("Attempts = %d, want the per-case override", seen.Attempts)
	}
	if seen.Interval != validTiming().Interval {
		t.Fatalf("Interval = %v, want inherited", seen.Interval)
	}
}

func phaseOutcome(t *testing.T, cr CaseReport, id ID) PhaseOutcome {
	t.Helper()
	for _, po := range cr.Phases {
		if po.ID == id {
			return po
		}
	}
	t.Fatalf("no outcome for phase %q in case %q", id, cr.CaseID)
	return PhaseOutcome{}
}

// --- review-gate findings, pinned before fixing -----------------------------

// Package-level on purpose: Declare panics on re-registration BY DESIGN, and a
// Declare inside a test body panics under -count=2 — the re-review caught this
// test aborting the whole binary on its second run, violating the convention
// keys_run_test.go already established.
var lyingKey = Declare[string]("undeclared_by_anyone")

type panickyTeardown struct{}

func (f *panickyTeardown) Setup(context.Context, *Run) error { return nil }
func (f *panickyTeardown) Teardown(context.Context, *Run) error {
	panic("consumer bug in teardown")
}

func TestATeardownPanicDoesNotTakeDownTheBatch(t *testing.T) {
	// Review finding #1: teardownFixtures had no recovery, unlike runOnePhase
	// — so a panicking Teardown propagated out of Start and aborted every
	// remaining case, contradicting runCase's own "a batch must survive any
	// single case".
	r := mustRunner(t, Config{Defaults: validTiming()}, passingPhase("submit", nil))
	s := startSession(t, r,
		&stubCase{id: "leaky", fixtures: []Fixture{&panickyTeardown{}}},
		&stubCase{id: "innocent"},
	)
	if got := caseReport(t, s, "leaky").Status; got != Errored {
		t.Fatalf("leaky = %s, want Errored — a teardown panic is evidence, not a crash", got)
	}
	if got := caseReport(t, s, "innocent").Status; got != Passed {
		t.Fatalf("innocent = %s — the batch must survive", got)
	}
}

func TestCaseReportFinishedIsActuallySet(t *testing.T) {
	// Review finding #2: `defer func() { cr.Finished = ... }()` mutated the
	// local AFTER `return cr` had copied it out — dead code, masked by the
	// determinism test normalising Finished away before comparing.
	r := mustRunner(t, Config{Defaults: validTiming()}, passingPhase("submit", nil))
	s := startSession(t, r, &stubCase{id: "happy"})
	cr := caseReport(t, s, "happy")
	if cr.Finished.IsZero() {
		t.Fatal("Finished is the zero time — the deferred write never reached the returned value")
	}
	if cr.Finished.Before(cr.Started) {
		t.Fatalf("Finished %v before Started %v", cr.Finished, cr.Started)
	}
}

func TestPutOfAnUndeclaredKeyIsRefused(t *testing.T) {
	// Review finding #4: tryPut enforced "one writer per key" but never that
	// the writer DECLARED the key in Produces() — so a phase could lie about
	// its wiring and preflight's graph would silently diverge from runtime.
	r := mustRunner(t, Config{Defaults: validTiming()},
		&recordingPhase{stubPhase: stubPhase{id: "submit", produces: []KeyID{}},
			do: func(_ context.Context, run *Run) error {
				Put(run, lyingKey, "smuggled")
				return nil
			}},
	)
	s := startSession(t, r, &stubCase{id: "liar"})
	cr := caseReport(t, s, "liar")
	if cr.Status != Errored {
		t.Fatalf("status = %s — a phase writing a key it never declared must not pass", cr.Status)
	}
	if _, err := Get(NewRunForTesting(nil), lyingKey); err == nil {
		t.Fatal("sanity: the key should not leak anywhere")
	}
}

func TestASetupPanicIsContainedToo(t *testing.T) {
	// Found by symmetry while fixing the teardown gap: Setup had the
	// identical uncontained-panic hole Teardown had.
	r := mustRunner(t, Config{Defaults: validTiming()}, passingPhase("submit", nil))
	s := startSession(t, r,
		&stubCase{id: "explosive", fixtures: []Fixture{&panickySetup{}}},
		&stubCase{id: "innocent"},
	)
	if got := caseReport(t, s, "explosive").Status; got != Errored {
		t.Fatalf("explosive = %s, want Errored", got)
	}
	if got := caseReport(t, s, "innocent").Status; got != Passed {
		t.Fatalf("innocent = %s — the batch must survive", got)
	}
}

type panickySetup struct{}

func (panickySetup) Setup(context.Context, *Run) error    { panic("consumer bug in setup") }
func (panickySetup) Teardown(context.Context, *Run) error { return nil }

// --- cancellation vs a real failing result --------------------------------

func TestCancellationAfterAFailureKeepsFailedButRecordsCurtailment(t *testing.T) {
	// A genuine guarantee-conflict: a CI deadline landing AFTER
	// a phase found a real failing result. Reporting Errored would HIDE the
	// defect, so the case stays Failed — but the curtailment must be visible.
	ctx, cancel := context.WithCancel(context.Background())
	r := mustRunner(t, Config{Defaults: validTiming()},
		&recordingPhase{stubPhase: stubPhase{id: "settle"}, do: func(_ context.Context, run *Run) error {
			run.Record(result.Failed("state mismatch", "expected 8, saw 9"))
			cancel() // deadline lands after the defect was found
			return nil
		}},
	)
	s, err := r.Start(ctx, []Case{&stubCase{id: "sad-and-cut"}})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	cr := caseReport(t, s, "sad-and-cut")
	if cr.Status != Failed {
		t.Fatalf("status = %s, want Failed — a real defect must not be hidden behind a cancellation", cr.Status)
	}
	if !cr.Curtailed {
		t.Fatal("the case was cancelled mid-run; Curtailed must record it")
	}
}

func TestCancellationBeforeAnyFailureIsErrored(t *testing.T) {
	// The other half of the rule: cancelled with only passing results (or none)
	// is Errored, not a clean pass — a cancelled run is not a verdict.
	ctx, cancel := context.WithCancel(context.Background())
	r := mustRunner(t, Config{Defaults: validTiming()},
		&recordingPhase{stubPhase: stubPhase{id: "settle"}, do: func(_ context.Context, run *Run) error {
			run.Record(result.Compared("looked fine so far", []bool{true}))
			cancel()
			return nil
		}},
	)
	s, _ := r.Start(ctx, []Case{&stubCase{id: "cut-clean"}})
	cr := caseReport(t, s, "cut-clean")
	if cr.Status != Errored {
		t.Fatalf("status = %s, want Errored — a cancelled run with no failure is not a Passed verdict", cr.Status)
	}
	if !cr.Curtailed {
		t.Fatal("Curtailed must be set")
	}
}

// --- C7/C8: preflight refuses duplicate IDs and nil cases -------------------

func TestDuplicateCaseIDRefused(t *testing.T) {
	r := mustRunner(t, Config{Defaults: validTiming()}, &stubPhase{id: "submit"})
	err := r.Preflight([]Case{&stubCase{id: "dup"}, &stubCase{id: "dup"}})
	_ = wantCode(t, err, DuplicateCaseID)
}

func TestNilCaseRefusedNotPanicked(t *testing.T) {
	r := mustRunner(t, Config{Defaults: validTiming()}, &stubPhase{id: "submit"})
	err := r.Preflight([]Case{nil})
	_ = wantCode(t, err, NilCase)
}

// --- C6: a returned Report must not alias the Session's evidence ------------

func TestReportDoesNotAliasSessionEvidence(t *testing.T) {
	r := mustRunner(t, Config{Defaults: validTiming()},
		&recordingPhase{stubPhase: stubPhase{id: "submit"}, do: func(_ context.Context, run *Run) error {
			run.Record(result.Compared("x", []bool{true}).WithActual("original"))
			return nil
		}},
	)
	s := startSession(t, r, &stubCase{id: "happy"})
	rep1 := s.Report()
	rep1.Cases[0].Results[0].Result.Actual = "MUTATED BY CONSUMER"
	rep2 := s.Report()
	if rep2.Cases[0].Results[0].Result.Actual == "MUTATED BY CONSUMER" {
		t.Fatal("mutating a returned Report corrupted the Session — evidence is aliased")
	}
}

// --- a phase that ran but asserted nothing must be visible ----------------

func TestAPhaseThatAssertedNothingIsVisibleInItsOutcome(t *testing.T) {
	// The founding defect, one level down: a phase whose Run returns nil having
	// recorded zero results reports Passed, byte-identical to one that
	// asserted. Assertion coverage can erode phase-by-phase invisibly. The
	// PhaseOutcome must record how many results the phase produced.
	r := mustRunner(t, Config{Defaults: validTiming()},
		&recordingPhase{stubPhase: stubPhase{id: "asserts"}, do: func(_ context.Context, run *Run) error {
			run.Record(result.Compared("real check", []bool{true}))
			return nil
		}},
		&recordingPhase{stubPhase: stubPhase{id: "asserts_nothing", deps: []ID{"asserts"}}, do: func(_ context.Context, run *Run) error {
			return nil // ran, recorded nothing
		}},
	)
	s := startSession(t, r, &stubCase{id: "eroding"})
	cr := caseReport(t, s, "eroding")
	real := phaseOutcome(t, cr, "asserts")
	empty := phaseOutcome(t, cr, "asserts_nothing")
	if real.Results == 0 {
		t.Fatal("a phase that asserted must report a nonzero result count")
	}
	if empty.Results != 0 {
		t.Fatalf("asserts_nothing recorded %d results, want 0", empty.Results)
	}
	// The two must NOT be indistinguishable: an asserting phase and an empty
	// one carry different, inspectable outcomes.
	if real.Results == empty.Results {
		t.Fatal("an asserting phase and an empty phase are indistinguishable in the report")
	}
}
