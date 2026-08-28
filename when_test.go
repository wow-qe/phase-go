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

// When is a guard over evidence already recorded in this case — never
// live state — evaluated at the phase's turn, after pruning and group
// setup, before timing/hooks. Declining is a recorded NotApplicable; a
// condition that itself breaks is Errored, never a decline (an error is
// not a decline, as an error is not a failed comparison).

type conditionalPhase struct {
	stubPhase
	do   func(context.Context, *Run) error
	when func(context.Context, *Run) (bool, string, error)
}

func (c *conditionalPhase) Run(ctx context.Context, r *Run) error {
	if c.do != nil {
		return c.do(ctx, r)
	}
	r.Record(result.Compared(string(c.id)+" ok", []bool{true}))
	return nil
}

func (c *conditionalPhase) When(ctx context.Context, r *Run) (bool, string, error) {
	return c.when(ctx, r)
}

func TestWhenDeclineIsARecordedNotApplicable(t *testing.T) {
	ran := false
	r := mustRunner(t, Config{Defaults: validTiming()},
		&recordingPhase{stubPhase: stubPhase{id: "discover"}, do: func(_ context.Context, run *Run) error {
			run.Record(result.Compared("rows found", []bool{false})) // found nothing
			return nil
		}},
		&conditionalPhase{stubPhase: stubPhase{id: "refund_check", deps: []ID{"discover"}},
			when: func(_ context.Context, run *Run) (bool, string, error) {
				ev, err := run.PriorEvidence("discover")
				if err != nil {
					return false, "", err
				}
				if ev.Failing > 0 {
					return false, "discovery found nothing to refund", nil
				}
				return true, "", nil
			},
			do: func(context.Context, *Run) error { ran = true; return nil },
		},
	)
	cr := caseReport(t, startSession(t, r, &stubCase{id: "empty"}), "empty")
	if ran {
		t.Fatal("a declined phase must not run")
	}
	po := phaseOutcome(t, cr, "refund_check")
	if po.Status != NotApplicable || !strings.HasPrefix(po.Reason, "condition: ") ||
		!strings.Contains(po.Reason, "nothing to refund") {
		t.Fatalf("outcome = %+v — a When decline is recorded with its reason", po)
	}
	if cr.Status != Failed {
		t.Fatalf("case = %s (discover's failing result still governs the verdict)", cr.Status)
	}
}

func TestWhenErrorIsErroredAndPrunes(t *testing.T) {
	r := mustRunner(t, Config{Defaults: validTiming()},
		&conditionalPhase{stubPhase: stubPhase{id: "gate"},
			when: func(context.Context, *Run) (bool, string, error) {
				return false, "", errors.New("key never produced")
			},
		},
		&recordingPhase{stubPhase: stubPhase{id: "after_gate", deps: []ID{"gate"}}, do: func(_ context.Context, run *Run) error {
			t.Fatal("dependent of an errored condition must not run")
			return nil
		}},
	)
	cr := caseReport(t, startSession(t, r, &stubCase{id: "broken"}), "broken")
	po := phaseOutcome(t, cr, "gate")
	if po.Status != Errored || !strings.HasPrefix(po.Reason, "condition: ") || po.Stage != "condition" {
		t.Fatalf("outcome = %+v — the condition ITSELF broke: Errored, stage marked", po)
	}
	if dep := phaseOutcome(t, cr, "after_gate"); dep.Status != NotApplicable {
		t.Fatalf("dependent = %+v, want pruned", dep)
	}
	if len(cr.Errors) == 0 {
		t.Fatal("a broken condition is environment trouble — it must reach cr.Errors")
	}
}

func TestWhenPanicIsContained(t *testing.T) {
	r := mustRunner(t, Config{Defaults: validTiming()},
		&conditionalPhase{stubPhase: stubPhase{id: "gate"},
			when: func(context.Context, *Run) (bool, string, error) { panic("condition bug") },
		},
	)
	s := startSession(t, r, &stubCase{id: "buggy"}, &stubCase{id: "next"})
	if len(s.Cases()) != 2 {
		t.Fatal("a When panic must not take down the batch")
	}
	if po := phaseOutcome(t, caseReport(t, s, "buggy"), "gate"); po.Status != Errored {
		t.Fatalf("outcome = %+v", po)
	}
}

func TestWhenDeclineWithoutAReasonIsLoud(t *testing.T) {
	r := mustRunner(t, Config{Defaults: validTiming()},
		&conditionalPhase{stubPhase: stubPhase{id: "gate"},
			when: func(context.Context, *Run) (bool, string, error) { return false, "", nil },
		},
	)
	po := phaseOutcome(t, caseReport(t, startSession(t, r, &stubCase{id: "mute"}), "mute"), "gate")
	if po.Status != NotApplicable || !strings.Contains(po.Reason, "no reason") {
		t.Fatalf("outcome = %+v — an unexplained decline must be visibly unexplained, never silent", po)
	}
}

func TestPriorEvidenceIsRestrictedToTransitiveDeps(t *testing.T) {
	var got error
	r := mustRunner(t, Config{Defaults: validTiming()},
		passingPhase("unrelated", nil),
		&conditionalPhase{stubPhase: stubPhase{id: "gate"}, // no dep on unrelated
			when: func(_ context.Context, run *Run) (bool, string, error) {
				_, got = run.PriorEvidence("unrelated")
				return true, "", nil
			},
		},
	)
	startSession(t, r, &stubCase{id: "scoped"})
	if got == nil {
		t.Fatal("PriorEvidence outside the transitive DependsOn set must refuse — the DAG edge must exist before the read is possible")
	}
}

func TestPriorEvidenceIsALiveScanNotAStatusCache(t *testing.T) {
	// A phase that runs clean but records a failing result lands
	// PhaseOutcome{Passed}. A cached status would tell When "y passed";
	// the live scan over recorded evidence tells the truth.
	var ev PriorEvidenceSummary
	r := mustRunner(t, Config{Defaults: validTiming()},
		&recordingPhase{stubPhase: stubPhase{id: "y"}, do: func(_ context.Context, run *Run) error {
			run.Record(result.Failed("y check", "expected 1, saw 0"))
			return nil // clean return: row says Passed, results say failing
		}},
		&conditionalPhase{stubPhase: stubPhase{id: "x", deps: []ID{"y"}},
			when: func(_ context.Context, run *Run) (bool, string, error) {
				ev, _ = run.PriorEvidence("y")
				return true, "", nil
			},
		},
	)
	startSession(t, r, &stubCase{id: "truth"})
	if ev.Recorded != 1 || ev.Failing != 1 || ev.Errored {
		t.Fatalf("evidence = %+v, want the recorded truth (1 recorded, 1 failing)", ev)
	}
}

func TestWhenThatRecordsWhileDecliningIsErroredNotSilent(t *testing.T) {
	// A When that calls Record before declining must not produce a
	// NotApplicable row carrying evidence invisible to Verify: a
	// condition reads recorded evidence, it does not write it — folding
	// to Errored keeps the row and the evidence consistent.
	r := mustRunner(t, Config{Defaults: validTiming()},
		&conditionalPhase{stubPhase: stubPhase{id: "leaky"},
			when: func(_ context.Context, run *Run) (bool, string, error) {
				run.Record(result.Failed("phantom", "recorded from a condition"))
				return false, "declining anyway", nil
			},
		},
	)
	s := startSession(t, r, &stubCase{id: "one"})
	po := phaseOutcome(t, caseReport(t, s, "one"), "leaky")
	if po.Status != NotApplicable {
		// good — must not be NotApplicable
	} else {
		t.Fatalf("outcome = %+v — phantom evidence under a did-not-run row", po)
	}
	// Preventive refusal, not fold-and-count: the
	// phantom never lands at all (see TestWhenRecordIsRefusedNotFolded).
	if po.Status != Errored || po.Stage != "condition" || po.Results != 0 {
		t.Fatalf("outcome = %+v, want Errored/condition with the phantom refused", po)
	}
	if err := s.Report().Verify(); err != nil {
		t.Fatalf("the folded report must verify: %v", err)
	}
}

func TestVerifyCatchesPhantomEvidenceUnderASkip(t *testing.T) {
	rep := mixedSession(t).Report()
	for i := range rep.Cases {
		for j := range rep.Cases[i].Phases {
			if st := rep.Cases[i].Phases[j].Status; st == NotApplicable || st == Disabled {
				rep.Cases[i].Phases[j].Results = 2 // hand-corruption: "did not run" with results
			}
		}
	}
	if rep.Verify() == nil {
		t.Fatal("a NotApplicable row carrying recorded results passed Verify")
	}
}

func TestWhenThatRecordsWhileApprovingIsAlsoErrored(t *testing.T) {
	// The rule is absolute, so the
	// approve path enforces it too — condition evidence must never silently
	// merge into the phase's own stream.
	ran := false
	r := mustRunner(t, Config{Defaults: validTiming()},
		&conditionalPhase{stubPhase: stubPhase{id: "leaky"},
			when: func(_ context.Context, run *Run) (bool, string, error) {
				run.Record(result.Compared("phantom", []bool{true}))
				return true, "", nil
			},
			do: func(context.Context, *Run) error { ran = true; return nil },
		},
	)
	po := phaseOutcome(t, caseReport(t, startSession(t, r, &stubCase{id: "one"}), "one"), "leaky")
	if ran || po.Status != Errored || po.Stage != "condition" {
		t.Fatalf("ran=%v outcome=%+v — a recording condition is refused on every outcome", ran, po)
	}
}
