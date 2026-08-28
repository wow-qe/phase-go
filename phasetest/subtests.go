// Copyright 2026 The Phase Contributors
// SPDX-License-Identifier: MIT

package phasetest

import (
	"testing"

	phase "github.com/wow-qe/phase-go"
)

// RunAsSubtests bridges a Session to go test: one subtest per case, named by
// the case's ID. Without it, one Start() collapses N cases into a
// single Go test result — `go test -run` cannot target a case, and JUnit
// sees "1 test". With it:
//
//	s, err := runner.Start(ctx, cases)
//	...
//	phasetest.RunAsSubtests(t, s)
//
// gives `go test -run TestSuite/checkout-declined`, per-case timing, and
// per-case CI rows for free.
func RunAsSubtests(t *testing.T, s *phase.Session) {
	t.Helper()
	for _, cr := range s.Cases() {
		cr := cr
		t.Run(cr.CaseID, func(t *testing.T) {
			ReportCaseOutcome(t, cr)
		})
	}
}

// ReportCaseOutcome translates one CaseReport into testing verdicts — the
// bridge's per-case logic, exported so it is testable (and usable with a
// custom TB). Failed and Errored fail the test with the recorded evidence;
// Skipped, NotApplicable and Disabled skip with the reason; Flaked passes
// but logs the flake — "passed on attempt 3" stays visible in test output.
func ReportCaseOutcome(tb testing.TB, cr phase.CaseReport) {
	tb.Helper()
	switch cr.Status {
	case phase.Failed:
		for _, ar := range cr.Results {
			if ar.Result.Passed {
				continue
			}
			tb.Errorf("phase %s: %s: %s (expected %v, actual %v)",
				ar.Phase, ar.Result.Name, ar.Result.Reason, ar.Result.Expected, ar.Result.Actual)
		}
		for _, ae := range cr.Errors {
			tb.Errorf("phase %s: %s", ae.Phase, ae.Err)
		}
	case phase.Errored:
		if cr.Reason != "" {
			tb.Errorf("case errored: %s", cr.Reason)
		}
		for _, ae := range cr.Errors {
			tb.Errorf("phase %s: %s", ae.Phase, ae.Err)
		}
		if cr.Reason == "" && len(cr.Errors) == 0 {
			tb.Error("case errored with no recorded reason — this itself should not happen (Verify flags it)")
		}
	case phase.Skipped, phase.NotApplicable, phase.Disabled:
		tb.Skipf("%s: %s", cr.Status, cr.Reason)
	case phase.Flaked:
		tb.Logf("flaked: %s", cr.Reason)
	}
}
