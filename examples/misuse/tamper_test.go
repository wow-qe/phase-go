// Copyright 2026 The Phase Contributors
// SPDX-License-Identifier: MIT

package misuse

import (
	"context"
	"errors"
	"testing"

	phase "github.com/wow-qe/phase-go"
)

// Report tampering: every hand-corruption of an honest report must be
// caught by Verify() as a FrameworkError identified by its Invariant —
// and a report that fails Verify maps to exit 3 ("do not trust these
// numbers"), never a quiet 0 or a misleading 1.

func wantInvariant(t *testing.T, rep *phase.Report, invariant string) {
	t.Helper()
	err := rep.Verify()
	if err == nil {
		t.Fatalf("the corruption was not caught — %q did not fire", invariant)
	}
	var fe *phase.FrameworkError
	if !errors.As(err, &fe) {
		t.Fatalf("Verify returned %T, want *FrameworkError", err)
	}
	if fe.Invariant != invariant {
		t.Fatalf("invariant = %q, want %q (detail: %s)", fe.Invariant, invariant, fe.Detail)
	}
	if rep.ExitCode() != 3 {
		t.Fatalf("exit = %d, want 3 — a report that fails Verify must be marked untrustworthy", rep.ExitCode())
	}
}

func greenReport(t *testing.T) *phase.Report {
	t.Helper()
	sys, cases := misuseSuite(t)
	return run(t, mustMisuseRunner(t, sys, sane()), onlyCase(t, cases, "happy-single"))
}

func TestTamperedReportsAreCaught(t *testing.T) {
	for _, tc := range []struct {
		name      string
		invariant string
		corrupt   func(t *testing.T, rep *phase.Report)
	}{
		{"phantom evidence under a skip", "skips carry no results", func(t *testing.T, rep *phase.Report) {
			po := findPhase(t, rep, "refund_audit") // NotApplicable on the green path
			po.Results = 2
		}},
		{"failing exceeds recorded", "failing within recorded", func(t *testing.T, rep *phase.Report) {
			findPhase(t, rep, "submit").Failing = 99
		}},
		{"unknown stage", "stages are a closed set", func(t *testing.T, rep *phase.Report) {
			findPhase(t, rep, "submit").Stage = phase.Stage("bogus")
		}},
		{"unknown decline source", "decline sources are a closed set", func(t *testing.T, rep *phase.Report) {
			findPhase(t, rep, "refund_audit").DeclineSource = phase.DeclineSource("bogus")
		}},
		{"passed over a failing result", "status derived from evidence", func(t *testing.T, rep *phase.Report) {
			rep.Cases[0].Results[0].Result.Passed = false
		}},
		{"curtailed verdict", "a curtailed run is not a verdict", func(t *testing.T, rep *phase.Report) {
			rep.Cases[0].Curtailed = true
		}},
		{"cooked summary", "summary adds up", func(t *testing.T, rep *phase.Report) {
			rep.Summary.Passed++
			rep.Summary.Total++
		}},
		{"stale schema", "schema version", func(t *testing.T, rep *phase.Report) {
			rep.Schema = "0"
		}},
		{"orphaned group attribution", "group evidence has a group row", func(t *testing.T, rep *phase.Report) {
			rep.Cases[0].Results[0].Source = phase.EvidenceSource{Kind: phase.SourceGroupSetup, ID: "ghost"}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rep := greenReport(t)
			tc.corrupt(t, rep)
			wantInvariant(t, rep, tc.invariant)
		})
	}
}

func TestTamperedSkipAndFailureRowsAreCaught(t *testing.T) {
	// Corruptions that need a red or skipped case to seed.
	sys, cases := misuseSuite(t)
	sys.corruptAuthCode = true
	rep := run(t, mustMisuseRunner(t, sys, sane()),
		append(onlyCase(t, cases, "happy-single"), onlyCase(t, cases, "parked-experiment")...))

	t.Run("failure loses its phase", func(t *testing.T) {
		clone := rerun(t, func(sys *checkoutSystem) { sys.corruptAuthCode = true }, "happy-single")
		clone.Cases[0].FailedIn = ""
		wantInvariant(t, clone, "failures name their phase")
	})
	t.Run("skip loses its reason", func(t *testing.T) {
		for i := range rep.Cases {
			if rep.Cases[i].CaseID == "parked-experiment" {
				rep.Cases[i].Reason = ""
			}
		}
		wantInvariant(t, rep, "skips carry reasons")
	})
}

func TestErroredCaseStrippedOfItsEvidenceIsCaught(t *testing.T) {
	rep := rerun(t, func(sys *checkoutSystem) {
		sys.mu.Lock()
		sys.healthy = false
		sys.mu.Unlock()
	}, "happy-single")
	rep.Cases[0].Errors = nil
	rep.Cases[0].Reason = ""
	wantInvariant(t, rep, "errors carry evidence")
}

func TestFlakedWithoutItsRetryEvidenceIsCaught(t *testing.T) {
	rep := rerun(t, func(sys *checkoutSystem) { sys.flapLedgerCount = true }, "happy-single")
	if rep.Cases[0].Status != phase.Flaked {
		t.Fatalf("seed = %v, want Flaked", rep.Cases[0].Status)
	}
	rep.Cases[0].Results[0].Result.Passed = false
	wantInvariant(t, rep, "flaked means passed on retry")
}

func TestStrippedNotVerifiedIsCaught(t *testing.T) {
	sys, cases := misuseSuite(t)
	off := false
	cfg := sane()
	cfg.Phases = map[phase.ID]phase.Settings{"audit": {Enabled: &off}}
	rep := run(t, mustMisuseRunner(t, sys, cfg), onlyCase(t, cases, "happy-single"))
	rep.NotVerified = []string{}
	wantInvariant(t, rep, "NotVerified names disabled phases")
}

func TestErroredGroupRowStrippedOfReasonIsCaught(t *testing.T) {
	g := phase.Group{ID: "g", Members: []phase.ID{"m"}, Lifecycle: &wonkyLifecycle{
		teardown: func(context.Context, *phase.Run) error { return errors.New("broker unplug failed") },
	}}
	rep := run(t, smallRunner(t, phase.NewPipeline(passingPhase("m")).Group(g)),
		[]phase.Case{plainCase("one")})
	rep.Cases[0].Groups[0].Reason = ""
	wantInvariant(t, rep, "group dispositions carry reasons")
}

// --- helpers -------------------------------------------------------------

func findPhase(t *testing.T, rep *phase.Report, id phase.ID) *phase.PhaseOutcome {
	t.Helper()
	for ci := range rep.Cases {
		for pi := range rep.Cases[ci].Phases {
			if rep.Cases[ci].Phases[pi].ID == id {
				return &rep.Cases[ci].Phases[pi]
			}
		}
	}
	t.Fatalf("phase %q not found", id)
	return nil
}

// rerun produces a fresh single-case report with one sabotage knob set.
func rerun(t *testing.T, knob func(*checkoutSystem), caseID string) *phase.Report {
	t.Helper()
	sys, cases := misuseSuite(t)
	knob(sys)
	return run(t, mustMisuseRunner(t, sys, sane()), onlyCase(t, cases, caseID))
}
