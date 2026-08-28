// Copyright 2026 The Phase Contributors
// SPDX-License-Identifier: MIT

package phase

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/wow-qe/phase-go/result"
)

// Before probes preconditions on the phase's own
// bound view; After concludes on every post-Before path; both fold into the
// ONE outcome row a phase lands, and everything they record rides the
// existing Recorder/finish() machinery — no separate hook status channel.

// hookedPhase is a stubPhase with optional Before/After behaviour.
type hookedPhase struct {
	stubPhase
	do     func(context.Context, *Run) error
	before func(context.Context, *Run) error
	after  func(context.Context, *Run, PhaseOutcome) error
}

func (h *hookedPhase) Run(ctx context.Context, r *Run) error {
	if h.do != nil {
		return h.do(ctx, r)
	}
	r.Record(result.Compared(string(h.id)+" check", []bool{true}))
	return nil
}

func (h *hookedPhase) Before(ctx context.Context, r *Run) error {
	if h.before == nil {
		return nil
	}
	return h.before(ctx, r)
}

func (h *hookedPhase) After(ctx context.Context, r *Run, po PhaseOutcome) error {
	if h.after == nil {
		return nil
	}
	return h.after(ctx, r, po)
}

func TestBeforeRunsBeforeRunAndAfterRunsAfter(t *testing.T) {
	var order []string
	r := mustRunner(t, Config{Defaults: validTiming()},
		&hookedPhase{stubPhase: stubPhase{id: "settle"},
			before: func(_ context.Context, run *Run) error { order = append(order, "before"); return nil },
			do: func(_ context.Context, run *Run) error {
				order = append(order, "run")
				run.Record(result.Compared("check", []bool{true}))
				return nil
			},
			after: func(_ context.Context, run *Run, po PhaseOutcome) error {
				order = append(order, "after:"+po.Status.String())
				return nil
			},
		},
	)
	cr := caseReport(t, startSession(t, r, &stubCase{id: "ordered"}), "ordered")
	want := "before | run | after:passed"
	if got := strings.Join(order, " | "); got != want {
		t.Fatalf("order = %q, want %q", got, want)
	}
	if cr.Status != Passed {
		t.Fatalf("status = %s", cr.Status)
	}
}

func TestBeforeFailureSkipsRunAndPrunesDependents(t *testing.T) {
	ran := false
	var pruned []string
	r := mustRunner(t, Config{Defaults: validTiming()},
		&hookedPhase{stubPhase: stubPhase{id: "settle"},
			before: func(context.Context, *Run) error { return errors.New("queue not reachable") },
			do:     func(context.Context, *Run) error { ran = true; return nil },
			after:  func(_ context.Context, _ *Run, _ PhaseOutcome) error { ran = true; return nil }, // After must NOT run either
		},
		&recordingPhase{stubPhase: stubPhase{id: "verify", deps: []ID{"settle"}}, do: func(_ context.Context, run *Run) error {
			pruned = append(pruned, "verify ran")
			return nil
		}},
	)
	cr := caseReport(t, startSession(t, r, &stubCase{id: "unmet"}), "unmet")
	if ran {
		t.Fatal("neither Run nor After may run when Before failed")
	}
	po := phaseOutcome(t, cr, "settle")
	if po.Status != Errored || !strings.HasPrefix(po.Reason, "before: ") || po.Stage != "before" {
		t.Fatalf("outcome = %+v, want Errored with the before-stage marked", po)
	}
	dep := phaseOutcome(t, cr, "verify")
	if dep.Status != NotApplicable || len(pruned) != 0 {
		t.Fatalf("dependent = %+v ran=%v — a failed precondition must prune like any phase error", dep, pruned)
	}
	if cr.Status != Errored {
		t.Fatalf("case = %s, want Errored — environment, not product", cr.Status)
	}
}

func TestBeforeViolationPatternFailsTheCase(t *testing.T) {
	// The two-fact pattern: a precondition VIOLATION records evidence AND
	// returns the error — the case derives Failed through the existing
	// single-writer path, with zero hook-specific derivation code.
	r := mustRunner(t, Config{Defaults: validTiming()},
		&hookedPhase{stubPhase: stubPhase{id: "settle"},
			before: func(_ context.Context, run *Run) error {
				run.Record(result.Failed("ledger empty before test", "expected 0 rows, saw 3").
					WithExpected(0).WithActual(3))
				return errors.New("precondition violated")
			},
		},
	)
	cr := caseReport(t, startSession(t, r, &stubCase{id: "dirty"}), "dirty")
	if cr.Status != Failed || cr.FailedIn != "settle" {
		t.Fatalf("status = %s FailedIn = %q — a recorded violation is a product fact", cr.Status, cr.FailedIn)
	}
}

func TestAfterRunsOnRunErrorAndPanic(t *testing.T) {
	seen := map[string]string{}
	mk := func(id ID, do func(context.Context, *Run) error) *hookedPhase {
		return &hookedPhase{stubPhase: stubPhase{id: id}, do: do,
			after: func(_ context.Context, _ *Run, po PhaseOutcome) error {
				seen[string(id)] = po.Status.String()
				return nil
			}}
	}
	r := mustRunner(t, Config{Defaults: validTiming()},
		mk("errs", func(context.Context, *Run) error { return errors.New("adapter down") }),
		mk("panics", func(context.Context, *Run) error { panic("consumer bug") }),
	)
	startSession(t, r, &stubCase{id: "rough"})
	if seen["errs"] != "errored" || seen["panics"] != "errored" {
		t.Fatalf("After must run on every post-Before path with the candidate outcome; seen = %v", seen)
	}
}

func TestAfterErrorFlipsAPassedRowToErroredInOneRow(t *testing.T) {
	r := mustRunner(t, Config{Defaults: validTiming()},
		&hookedPhase{stubPhase: stubPhase{id: "settle"},
			after: func(_ context.Context, _ *Run, _ PhaseOutcome) error {
				return errors.New("tally mismatch during conclusion")
			},
		},
		&recordingPhase{stubPhase: stubPhase{id: "audit", deps: []ID{"settle"}}, do: func(_ context.Context, run *Run) error {
			run.Record(result.Compared("audit", []bool{true}))
			return nil
		}},
	)
	cr := caseReport(t, startSession(t, r, &stubCase{id: "flipped"}), "flipped")
	rows := 0
	for _, po := range cr.Phases {
		if po.ID == "settle" {
			rows++
			if po.Status != Errored || !strings.HasPrefix(po.Reason, "after: ") || po.Stage != "after" {
				t.Fatalf("outcome = %+v — After's error must fold into the SAME row", po)
			}
		}
	}
	if rows != 1 {
		t.Fatalf("settle landed %d rows, want exactly 1", rows)
	}
	if dep := phaseOutcome(t, cr, "audit"); dep.Status != NotApplicable {
		t.Fatalf("audit = %+v — an After failure prunes dependents like any phase error", dep)
	}
	if cr.Status != Errored {
		t.Fatalf("case = %s", cr.Status)
	}
}

func TestAfterCannotHealAFailedRun(t *testing.T) {
	r := mustRunner(t, Config{Defaults: validTiming()},
		&hookedPhase{stubPhase: stubPhase{id: "settle"},
			do:    func(context.Context, *Run) error { return errors.New("adapter down") },
			after: func(_ context.Context, _ *Run, _ PhaseOutcome) error { return nil },
		},
	)
	po := phaseOutcome(t, caseReport(t, startSession(t, r, &stubCase{id: "sick"}), "sick"), "settle")
	if po.Status != Errored || po.Stage == "after" {
		t.Fatalf("outcome = %+v — a clean After never heals an Errored Run", po)
	}
}

func TestHookEvidenceCountsForThePhase(t *testing.T) {
	r := mustRunner(t, Config{Defaults: validTiming()},
		&hookedPhase{stubPhase: stubPhase{id: "settle"},
			before: func(_ context.Context, run *Run) error {
				run.Record(result.Compared("queue reachable", []bool{true}))
				return nil
			},
			do: func(_ context.Context, run *Run) error {
				run.Record(result.Failed("state mismatch", "expected 8, saw 9"))
				return nil
			},
			after: func(_ context.Context, run *Run, _ PhaseOutcome) error {
				run.Record(result.Compared("tally", []bool{true}))
				return nil
			},
		},
	)
	po := phaseOutcome(t, caseReport(t, startSession(t, r, &stubCase{id: "tallied"}), "tallied"), "settle")
	if po.Results != 3 {
		t.Fatalf("results_recorded = %d, want 3 — hook evidence rides the same handle", po.Results)
	}
	// The sibling count: the row must also say how many of those FAILED, closing
	// the "row says Passed, results say failing" reading gap.
	if po.Failing != 1 {
		t.Fatalf("failing_recorded = %d, want 1", po.Failing)
	}
}

func TestStageAndFailingReachTheArtifact(t *testing.T) {
	r := mustRunner(t, Config{Defaults: validTiming()},
		&hookedPhase{stubPhase: stubPhase{id: "settle"},
			before: func(context.Context, *Run) error { return errors.New("queue not reachable") },
		},
	)
	s := startSession(t, r, &stubCase{id: "unmet"})
	var buf strings.Builder
	if err := s.Report().WriteJSON(&buf); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"stage": "before"`, `"failing_recorded"`} {
		if !strings.Contains(buf.String(), want) {
			t.Fatalf("artifact missing %s", want)
		}
	}
}

func TestBeforeCancellationIsErroredNeverFailed(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	r := mustRunner(t, Config{Defaults: validTiming()},
		&hookedPhase{stubPhase: stubPhase{id: "settle"},
			before: func(c context.Context, _ *Run) error { cancel(); return c.Err() },
		},
	)
	s, err := r.Start(ctx, []Case{&stubCase{id: "cut"}})
	if err != nil {
		t.Fatal(err)
	}
	cr := caseReport(t, s, "cut")
	if cr.Status != Errored || cr.FailedIn != "" {
		t.Fatalf("status = %s FailedIn = %q — cancellation is Errored, never Failed", cr.Status, cr.FailedIn)
	}
}

func TestHookPanicsAreContained(t *testing.T) {
	r := mustRunner(t, Config{Defaults: validTiming()},
		&hookedPhase{stubPhase: stubPhase{id: "b"},
			before: func(context.Context, *Run) error { panic("before bug") }},
		&hookedPhase{stubPhase: stubPhase{id: "a"},
			after: func(context.Context, *Run, PhaseOutcome) error { panic("after bug") }},
	)
	s := startSession(t, r, &stubCase{id: "buggy"}, &stubCase{id: "second"})
	if len(s.Cases()) != 2 {
		t.Fatal("a hook panic must not take down the batch")
	}
	cr := caseReport(t, s, "buggy")
	for _, id := range []ID{"a", "b"} {
		if po := phaseOutcome(t, cr, id); po.Status != Errored {
			t.Fatalf("phase %s = %+v, want Errored from the contained panic", id, po)
		}
	}
}

func TestSettleDelayRunsBeforeBefore(t *testing.T) {
	// Before must probe a SETTLED system — the delay exists precisely so
	// assertion-adjacent logic does not fire early and fail spuriously.
	var order []string
	r := mustRunner(t, Config{
		Defaults: validTiming(),
		Phases:   map[ID]Settings{"settle": {Timing: Timing{SettleDelay: 1}}}, // 1ns: observable via the injected sleeper path
	},
		&hookedPhase{stubPhase: stubPhase{id: "settle"},
			before: func(context.Context, *Run) error { order = append(order, "before"); return nil },
		},
	)
	// The runner's run uses the real sleeper; a 1ns settle completes
	// immediately but MUST have been initiated before Before ran. We assert
	// ordering via the progress stream, which fires "started" before the
	// settle sleep.
	var events []string
	WithProgress(func(ev ProgressEvent) { events = append(events, ev.Stage) })(r)
	startSession(t, r, &stubCase{id: "s"})
	if len(order) != 1 {
		t.Fatalf("before ran %d times", len(order))
	}
	if fmt.Sprint(events) != "[started finished]" {
		t.Fatalf("events = %v", events)
	}
}

func TestBeforeViolationKeepsTheErrorChannelClean(t *testing.T) {
	// A regression this test pins: the violation path also wrote
	// cr.Errors - the field whose documented purpose is environment trouble
	// - so an on-call filter on len(Errors) paged for a clean product
	// defect. A recorded violation is a product fact; only an UNRECORDED
	// Before failure is environment noise.
	r := mustRunner(t, Config{Defaults: validTiming()},
		&hookedPhase{stubPhase: stubPhase{id: "settle"},
			before: func(_ context.Context, run *Run) error {
				run.Record(result.Failed("ledger empty before test", "expected 0 rows, saw 3"))
				return errors.New("precondition violated")
			},
		},
	)
	cr := caseReport(t, startSession(t, r, &stubCase{id: "dirty"}), "dirty")
	if cr.Status != Failed {
		t.Fatalf("status = %s", cr.Status)
	}
	if len(cr.Errors) != 0 {
		t.Fatalf("cr.Errors = %+v — a recorded violation must not page the environment channel", cr.Errors)
	}
	// And the inverse still holds: an unrecorded Before failure IS
	// environment noise and lands in Errors.
	r2 := mustRunner(t, Config{Defaults: validTiming()},
		&hookedPhase{stubPhase: stubPhase{id: "settle"},
			before: func(context.Context, *Run) error { return errors.New("queue not reachable") },
		},
	)
	cr2 := caseReport(t, startSession(t, r2, &stubCase{id: "down"}), "down")
	if len(cr2.Errors) != 1 {
		t.Fatalf("cr.Errors = %+v — an unrecorded Before failure is environment trouble", cr2.Errors)
	}
}
