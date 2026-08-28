// Copyright 2026 The Phase Contributors
// SPDX-License-Identifier: MIT

package phase

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/wow-qe/phase-go/result"
)

// The stringly conventions become typed
// vocabulary — Stage and DeclineSource as closed sets Verify can check,
// EvidenceSource as typed attribution beside the legacy string, and
// AttemptsUsed as the bounded settle-cost summary.

func TestStageIsAClosedSetVerifyChecks(t *testing.T) {
	r := mustRunner(t, Config{Defaults: validTiming()},
		&hookedPhase{stubPhase: stubPhase{id: "settle"},
			before: func(context.Context, *Run) error { return errors.New("queue down") }},
	)
	rep := startSession(t, r, &stubCase{id: "one"}).Report()
	if got := rep.Cases[0].Phases[0].Stage; got != StageBefore {
		t.Fatalf("stage = %v, want the typed StageBefore", got)
	}
	rep.Cases[0].Phases[0].Stage = Stage("bogus")
	if rep.Verify() == nil {
		t.Fatal("an unknown stage passed Verify — the set must be closed")
	}
}

func TestDeclineSourceIsStructural(t *testing.T) {
	off := false
	r := mustRunner(t, Config{
		Defaults: validTiming(),
		Phases:   map[ID]Settings{"disabled_ph": {Enabled: &off}},
	},
		passingPhase("run_ph", nil),
		&stubPhase{id: "declined_ph", applies: Skip("not this case")},
		&stubPhase{id: "disabled_ph"},
		&conditionalPhase{stubPhase: stubPhase{id: "cond_ph"},
			when: func(context.Context, *Run) (bool, string, error) { return false, "evidence says no", nil }},
		&recordingPhase{stubPhase: stubPhase{id: "errs_ph"}, do: func(context.Context, *Run) error {
			return errors.New("adapter down")
		}},
		&stubPhase{id: "pruned_ph", deps: []ID{"errs_ph"}},
	)
	s := startSession(t, r, &stubCase{
		id: "mixed",
		selects: func(id ID) (bool, string) {
			if id == "case_declined_ph" {
				return false, "not in scope"
			}
			return true, ""
		},
	})
	cr := caseReport(t, s, "mixed")
	want := map[ID]DeclineSource{
		"run_ph":      "",
		"declined_ph": DeclinedByPhase,
		"disabled_ph": DeclinedByConfig,
		"cond_ph":     DeclinedByCondition,
		"errs_ph":     "",
		"pruned_ph":   DeclinedByDependency,
	}
	for _, po := range cr.Phases {
		if exp, ok := want[po.ID]; ok && po.DeclineSource != exp {
			t.Fatalf("%s decline_source = %q, want %q (aggregation must never regex prose)", po.ID, po.DeclineSource, exp)
		}
	}
}

func TestCaseDeclineSourceIsStructural(t *testing.T) {
	r := mustRunner(t, Config{Defaults: validTiming()},
		passingPhase("p", nil), &stubPhase{id: "q", deps: []ID{"p"}, applies: Skip("nope")},
	)
	s := startSession(t, r, &stubCase{
		id:      "sel",
		selects: func(id ID) (bool, string) { return id != "q", "declined by this case" },
	})
	po := phaseOutcome(t, caseReport(t, s, "sel"), "q")
	if po.DeclineSource != DeclinedByCase {
		t.Fatalf("decline_source = %q, want %q", po.DeclineSource, DeclinedByCase)
	}
}

func TestGroupSetupFailureDeclineSource(t *testing.T) {
	var j []string
	r := groupRunner(t, &j,
		Group{ID: "g", Members: []ID{"m1", "m2"},
			Lifecycle: &groupFixture{name: "g", journal: &j, setupErr: errors.New("no broker")}},
		journalPhase("m1", &j), journalPhase("m2", &j, "m1"),
	)
	cr := caseReport(t, startSession(t, r, &stubCase{id: "one"}), "one")
	for _, id := range []ID{"m1", "m2"} {
		if po := phaseOutcome(t, cr, id); po.DeclineSource != DeclinedByGroupSetup {
			t.Fatalf("%s decline_source = %q, want %q", id, po.DeclineSource, DeclinedByGroupSetup)
		}
	}
}

func TestEvidenceCarriesTypedSource(t *testing.T) {
	var j []string
	r := groupRunner(t, &j,
		Group{ID: "settlement", Members: []ID{"m"},
			Lifecycle: &groupFixture{name: "g", journal: &j, onSetup: func(run *Run) {
				run.Record(result.Compared("broker up", []bool{true}))
			}}},
		journalPhase("m", &j),
	)
	cr := caseReport(t, startSession(t, r, &stubCase{id: "one"}), "one")
	var kinds []string
	for _, ar := range cr.Results {
		kinds = append(kinds, string(ar.Source.Kind)+":"+string(ar.Source.ID))
	}
	joined := strings.Join(kinds, ",")
	if !strings.Contains(joined, "group_setup:settlement") {
		t.Fatalf("sources = %q — group evidence must carry typed attribution, not only the legacy string", joined)
	}
	if !strings.Contains(joined, "phase:m") {
		t.Fatalf("sources = %q — phase evidence carries its typed source too", joined)
	}
	var buf bytes.Buffer
	if err := startSession(t, r, &stubCase{id: "two"}).Report().WriteJSON(&buf); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), `"source"`) {
		t.Fatal("typed source missing from the artifact")
	}
}

func TestAttemptsUsedIsTheBoundedSettleSummary(t *testing.T) {
	r := mustRunner(t, Config{Defaults: Timing{Attempts: 5, Interval: time.Millisecond}},
		&recordingPhase{stubPhase: stubPhase{id: "settle"}, do: func(ctx context.Context, run *Run) error {
			calls := 0
			_, err := WaitUntil(ctx, run, func(context.Context) (int, bool, error) {
				calls++
				return calls, calls == 3, nil
			})
			if err != nil {
				return err
			}
			run.Record(result.Compared("settled", []bool{true}))
			return nil
		}},
	)
	po := phaseOutcome(t, caseReport(t, startSession(t, r, &stubCase{id: "one"}), "one"), "settle")
	if po.AttemptsUsed != 3 {
		t.Fatalf("attempts_used = %d, want 3 — the how-long-did-settling-take signal, bounded, no transcript", po.AttemptsUsed)
	}
}

func TestAttemptsUsedCountsTolerateToo(t *testing.T) {
	calls := 0
	r := mustRunner(t, Config{Defaults: Timing{Attempts: 5, Interval: time.Millisecond}},
		&recordingPhase{stubPhase: stubPhase{id: "settle"}, do: func(ctx context.Context, run *Run) error {
			_, err := Tolerate(ctx, run, "flaky provider", 4, func(context.Context) result.Result {
				calls++
				if calls < 2 {
					return result.Failed("check", "not yet")
				}
				return result.Compared("check", []bool{true})
			})
			return err
		}},
	)
	po := phaseOutcome(t, caseReport(t, startSession(t, r, &stubCase{id: "one"}), "one"), "settle")
	if po.AttemptsUsed != 2 {
		t.Fatalf("attempts_used = %d, want 2", po.AttemptsUsed)
	}
}
