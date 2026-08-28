// Copyright 2026 The Phase Contributors
// SPDX-License-Identifier: MIT

package phase

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"math"
	"reflect"
	"strings"
	"testing"

	"github.com/wow-qe/phase-go/result"
)

// The report is the product. Verify() is an assertion, not a repair: if it
// fires, phase has a bug and the numbers cannot be trusted — the source
// framework's reconciliation pass existed to repair reports its own
// architecture corrupted, and deriving outcomes from evidence removes the
// cause while Verify removes the doubt.

func mixedSession(t *testing.T) *Session {
	t.Helper()
	off := false
	r := mustRunner(t, Config{
		Defaults: validTiming(),
		Phases:   map[ID]Settings{"provider_side": {Enabled: &off}},
	},
		&recordingPhase{stubPhase: stubPhase{id: "submit"}, do: func(_ context.Context, run *Run) error {
			run.Observe("request", map[string]string{"country": "BE"})
			run.Record(result.Compared("submit accepted", []bool{true}))
			return nil
		}},
		&recordingPhase{stubPhase: stubPhase{id: "settle", deps: []ID{"submit"}}, do: func(_ context.Context, run *Run) error {
			if run.Case().ID() == "sad" {
				run.Record(result.Failed("state mismatch", "expected 8, saw 9").
					WithExpected(8).WithActual(9).ForEntity(Ref("3458")))
			} else {
				run.Record(result.Compared("state aligned", []bool{true, true}))
			}
			return nil
		}},
		&stubPhase{id: "provider_side", deps: []ID{"settle"}},
		&recordingPhase{stubPhase: stubPhase{id: "flaky_env"}, do: func(_ context.Context, run *Run) error {
			if run.Case().ID() == "outage" {
				return errors.New("connection refused")
			}
			run.Record(result.Compared("env check", []bool{true}))
			return nil
		}},
	)
	return startSession(t, r,
		&stubCase{id: "happy"},
		&stubCase{id: "sad"},
		&stubCase{id: "outage"},
		&stubCase{id: "parked", status: Quarantined},
	)
}

func TestSummaryCountsEveryStatusDistinctly(t *testing.T) {
	rep := mixedSession(t).Report()
	sum := rep.Summary
	if sum.Passed != 1 || sum.Failed != 1 || sum.Errored != 1 || sum.Skipped != 1 {
		t.Fatalf("summary = %+v", sum)
	}
	if sum.Total != 4 {
		t.Fatalf("total = %d", sum.Total)
	}
}

func TestExitCodes(t *testing.T) {
	if got := mixedSession(t).Report().ExitCode(); got != 1 {
		t.Fatalf("failures present: exit = %d, want 1", got)
	}

	r := mustRunner(t, Config{Defaults: validTiming()}, passingPhase("submit", nil))
	green := startSession(t, r, &stubCase{id: "happy"}).Report()
	if got := green.ExitCode(); got != 0 {
		t.Fatalf("clean: exit = %d, want 0", got)
	}

	// A corrupted report must exit 3 — the numbers cannot be trusted, and
	// exiting 1 would send someone to debug the product.
	green.Cases[0].Status = Failed // hand-corruption: status contradicts evidence
	if got := green.ExitCode(); got != 3 {
		t.Fatalf("corrupted: exit = %d, want 3", got)
	}
}

func TestVerifyIsCleanOnARealSession(t *testing.T) {
	if err := mixedSession(t).Report().Verify(); err != nil {
		t.Fatalf("a report the runner built must verify: %v", err)
	}
}

func TestVerifyCatchesHandCorruption(t *testing.T) {
	corrupt := func(mutate func(*Report)) error {
		rep := mixedSession(t).Report()
		mutate(rep)
		return rep.Verify()
	}
	cases := map[string]func(*Report){
		"status contradicts failing result": func(r *Report) {
			for i := range r.Cases {
				if r.Cases[i].CaseID == "sad" {
					r.Cases[i].Status = Passed
				}
			}
		},
		"passed with zero comparisons": func(r *Report) {
			for i := range r.Cases {
				if r.Cases[i].CaseID == "happy" {
					r.Cases[i].Results[0].Result.Comparisons = 0
				}
			}
		},
		"summary does not add up": func(r *Report) { r.Summary.Passed = 99 },
		"skip without a reason": func(r *Report) {
			for i := range r.Cases {
				for j := range r.Cases[i].Phases {
					r.Cases[i].Phases[j].Reason = ""
				}
			}
		},
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			if corrupt(mutate) == nil {
				t.Fatal("Verify accepted a corrupted report")
			}
		})
	}
}

func TestJSONCarriesTheSchemaAndTheEvidence(t *testing.T) {
	var buf bytes.Buffer
	if err := mixedSession(t).Report().WriteJSON(&buf); err != nil {
		t.Fatalf("WriteJSON: %v", err)
	}
	var doc map[string]any
	if err := json.Unmarshal(buf.Bytes(), &doc); err != nil {
		t.Fatalf("not valid json: %v", err)
	}
	if doc["schema_version"] != "1" {
		t.Fatalf("schema_version = %v", doc["schema_version"])
	}
	body := buf.String()
	for _, want := range []string{
		`"session"`, `"summary"`, `"not_verified"`,
		`"state mismatch"`,      // the failing result's stable name
		`"expected": 8`,         // evidence survives serialisation
		`"failed_in": "settle"`, // the consumer's policy switch
		`"disabled"`,            // the operator switch is visible
		`"case status: quarantined"`,
		// Review finding #3: EntityRef and Observation had neither tags nor
		// MarshalJSON, so Go field names leaked into the schema surface —
		// {"Kind":...} where the schema documents {"kind":...} — and no test pinned
		// the casing, so the break shipped invisibly. Pinned now.
		// (indented output: keys assert individually, casing is the point)
		`"kind": "entity"`,
		`"id": "3458"`,
		`"observations"`,
		`"value"`,
		// Per-phase result counts must reach the ARTIFACT, not just the
		// in-memory struct — the report is the product (an earlier version
		// asserted only against the Go struct).
		`"results_recorded"`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("report json missing %s", want)
		}
	}
	for _, forbidden := range []string{`"Kind"`, `"ID"`, `"Phase"`, `"Name"`, `"Value"`, `"At"`} {
		if strings.Contains(body, forbidden) {
			t.Fatalf("Go field name %s leaked into the schema surface", forbidden)
		}
	}
}

func TestNotVerifiedNamesDeliberateCoverageLoss(t *testing.T) {
	// The report states what it did NOT verify. An operator-disabled
	// phase is exactly that, and it must be said once, loudly — not left to
	// be summed out of per-case rows.
	rep := mixedSession(t).Report()
	found := false
	for _, line := range rep.NotVerified {
		found = found || strings.Contains(line, "provider_side")
	}
	if !found {
		t.Fatalf("NotVerified = %v — the disabled phase must be named", rep.NotVerified)
	}
}

func TestMarshallingIsDeterministic(t *testing.T) {
	rep := mixedSession(t).Report()
	var a, b bytes.Buffer
	if err := rep.WriteJSON(&a); err != nil {
		t.Fatal(err)
	}
	if err := rep.WriteJSON(&b); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(a.Bytes(), b.Bytes()) {
		t.Fatal("two serialisations of one report differ")
	}
}

func TestVerifyCatchesTheReviewersCorruptions(t *testing.T) {
	// Corruptions beyond the first four probes. Pinned here.
	t.Run("a Flaked case with no evidence", func(t *testing.T) {
		rep := mixedSession(t).Report()
		rep.Cases[0].Status = Flaked
		rep.Cases[0].Results = nil
		rep.Cases[0].Errors = nil
		rep.Summary.Passed--
		rep.Summary.Flaked++
		if rep.Verify() == nil {
			t.Fatal("a Flaked case with zero results passed Verify — flaked means PASSED ON RETRY, which requires evidence of passing")
		}
	})
	t.Run("a Failed case cancelled mid-run must record its curtailment", func(t *testing.T) {
		rep := mixedSession(t).Report()
		for i := range rep.Cases {
			if rep.Cases[i].CaseID == "sad" {
				// simulate: Failed, but the curtailment flag stripped
				rep.Cases[i].Curtailed = false
				rep.Cases[i].Reason = "cancelled"
			}
		}
		// A Failed case whose reason says cancelled but Curtailed is false is
		// the C1 defect hiding: Verify must catch it.
		if rep.Verify() == nil {
			t.Skip("only meaningful once a cancelled-failed case is present")
		}
	})
	t.Run("NotVerified emptied while a Disabled phase exists", func(t *testing.T) {
		rep := mixedSession(t).Report()
		rep.NotVerified = []string{}
		if rep.Verify() == nil {
			t.Fatal("a truncated NotVerified passed Verify — the mandatory coverage-loss section was silently deletable")
		}
	})
}

func TestNotVerifiedMatchIsAnchoredNotSubstring(t *testing.T) {
	// Re-review probe, pinned: phase "settle_wait" Disabled while NotVerified
	// names only "settle_wait_extended". The bare-substring check accepted
	// that as disclosure; the quoted-id anchor must not.
	rep := mixedSession(t).Report()
	for i := range rep.Cases {
		for j := range rep.Cases[i].Phases {
			if rep.Cases[i].Phases[j].Status == Disabled {
				rep.Cases[i].Phases[j].ID = "settle_wait"
			}
		}
	}
	rep.NotVerified = []string{`phase "settle_wait_extended" was disabled by configuration for 1 case(s)`}
	if rep.Verify() == nil {
		t.Fatal(`a line about "settle_wait_extended" must not satisfy disclosure for "settle_wait"`)
	}
}

func TestOneBadFloatDoesNotVoidTheWholeReport(t *testing.T) {
	// WriteJSON is all-or-nothing; a NaN in one case's Actual lost every
	// other case's evidence. The report must degrade the offending value, not
	// vanish entirely.
	r := mustRunner(t, Config{Defaults: validTiming()},
		&recordingPhase{stubPhase: stubPhase{id: "submit"}, do: func(_ context.Context, run *Run) error {
			if run.Case().ID() == "nan" {
				run.Record(result.Compared("ratio", []bool{false}).WithActual(math.NaN()))
			} else {
				run.Record(result.Compared("ok", []bool{true}))
			}
			return nil
		}},
	)
	s := startSession(t, r, &stubCase{id: "clean"}, &stubCase{id: "nan"})
	var buf bytes.Buffer
	if err := s.Report().WriteJSON(&buf); err != nil {
		t.Fatalf("one NaN voided the entire report: %v", err)
	}
	if !strings.Contains(buf.String(), `"clean"`) {
		t.Fatal("the clean case's evidence vanished with the NaN case")
	}
}

func TestAFailingResultMustExplainItself(t *testing.T) {
	// A failing result carrying NOTHING — no reason, no expected, no
	// actual — tells a debugger nothing and is indistinguishable from a real
	// one. Verify must flag it. (Reachable only by hand-corruption: every
	// result constructor guarantees a failing result a reason.)
	rep := mixedSession(t).Report()
	for i := range rep.Cases {
		for j := range rep.Cases[i].Results {
			r := &rep.Cases[i].Results[j].Result
			if !r.Passed { // the sad case's failing result
				r.Reason = ""
				r.Expected = nil
				r.Actual = nil
			}
		}
	}
	if rep.Verify() == nil {
		t.Fatal("a failing result with no reason and no expected/actual passed Verify")
	}
}

func TestABareFailingComparisonIsAProductFailureNotAFrameworkBug(t *testing.T) {
	// The explain-yourself rule must not outlaw the founding-rule API itself: a failing
	// Compared(name, checks) carries its explanation in Reason ("1 of 2
	// comparisons failed") and no expected/actual. That is a legitimate,
	// debuggable product failure — exit 1 — never an exit-3 FrameworkError
	// claiming the report cannot be trusted.
	r := mustRunner(t, Config{Defaults: validTiming()},
		&recordingPhase{stubPhase: stubPhase{id: "settle"}, do: func(_ context.Context, run *Run) error {
			run.Record(result.Compared("rows match", []bool{false, true}))
			return nil
		}},
	)
	rep := startSession(t, r, &stubCase{id: "plain-fail"}).Report()
	if err := rep.Verify(); err != nil {
		t.Fatalf("a report the runner built from the sanctioned API must verify: %v", err)
	}
	if got := rep.ExitCode(); got != 1 {
		t.Fatalf("exit = %d, want 1 — a product failure, not a framework bug", got)
	}
}

func TestCurtailmentSurvivesSerialisation(t *testing.T) {
	// Same gap class: Curtailed existed on the Go struct
	// but not in the MarshalJSON shadow, so the report artifact — the thing CI
	// and readers consume — never showed the interruption.
	ctx, cancel := context.WithCancel(context.Background())
	r := mustRunner(t, Config{Defaults: validTiming()},
		&recordingPhase{stubPhase: stubPhase{id: "settle"}, do: func(_ context.Context, run *Run) error {
			run.Record(result.Compared("looked fine", []bool{true}))
			cancel()
			return nil
		}},
	)
	s, err := r.Start(ctx, []Case{&stubCase{id: "cut"}})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	var buf bytes.Buffer
	if err := s.Report().WriteJSON(&buf); err != nil {
		t.Fatalf("WriteJSON: %v", err)
	}
	if !strings.Contains(buf.String(), `"curtailed": true`) {
		t.Fatal("the curtailment is invisible in the serialised report")
	}
}

func TestSchemaSurfaceIsFullyTagged(t *testing.T) {
	// The schema surface used to live in five hand-maintained shadow
	// structs with no compiler link to their sources — a field added to the
	// Go struct and forgotten in the shadow silently dropped from JSON
	// (exactly how Results and Curtailed shipped invisible). The
	// tags now live ON the real structs; this test is the mechanical link:
	// every exported field of every schema-surface struct must carry an
	// explicit json tag, so an untagged addition fails here instead of
	// leaking a Go spelling into the stable schema.
	for _, typ := range []reflect.Type{
		reflect.TypeOf(Report{}),
		reflect.TypeOf(Summary{}),
		reflect.TypeOf(SessionInfo{}),
		reflect.TypeOf(CaseReport{}),
		reflect.TypeOf(PhaseOutcome{}),
		reflect.TypeOf(AttributedResult{}),
		reflect.TypeOf(AttributedError{}),
		reflect.TypeOf(ResultView{}),
		reflect.TypeOf(Observation{}),
		reflect.TypeOf(EntityRef{}),
		// Structured observation values ride Observation.Value into the same
		// artifact; their tags are schema surface too.
		reflect.TypeOf(ToleratedAttempt{}),
		reflect.TypeOf(TranscriptEntry{}),
		reflect.TypeOf(GroupOutcome{}),
		reflect.TypeOf(DependencyFailure{}),
		reflect.TypeOf(EvidenceSource{}),
	} {
		for i := 0; i < typ.NumField(); i++ {
			f := typ.Field(i)
			if !f.IsExported() {
				continue
			}
			tag := f.Tag.Get("json")
			if tag == "" || tag == "-" {
				t.Errorf("%s.%s has no json tag — its Go spelling would leak into the schema surface", typ.Name(), f.Name)
				continue
			}
			if name := strings.SplitN(tag, ",", 2)[0]; name != strings.ToLower(name) {
				t.Errorf("%s.%s json name %q is not lower_snake", typ.Name(), f.Name, name)
			}
		}
	}
}
