// Copyright 2026 The Phase Contributors
// SPDX-License-Identifier: MIT

package phase

// Applicability is a phase's answer to "does this phase apply to this case".
//
// It may be computed from the case declaration and from configuration. It may
// NOT be computed from live system state: "the request already reached a
// terminal state, so skip the progress assertion" is the framework choosing
// what to test based on what the system did — the defect, not the feature.
// The contract cannot make that unrepresentable, so it is stated here, tested
// in phasetest.ConformanceCase, and enforced in review.
type Applicability struct {
	Applies bool
	Reason  string // required when !Applies; recorded in the report
}

// Applies says the phase runs for this case.
func Applies() Applicability { return Applicability{Applies: true} }

// Skip declines with a reason. An empty reason is a LoadError at preflight —
// a skip nobody can explain is indistinguishable from a check that passed.
func Skip(reason string) Applicability { return Applicability{Applies: false, Reason: reason} }
