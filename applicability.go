// Copyright 2026 The Phase Contributors
// SPDX-License-Identifier: MIT

package phase

// Applicability is a phase's answer to "does this phase apply to this case".
//
// It may be computed from the case declaration and from configuration. It may
// not be computed from live system state: deciding what to test based on
// what the system already did — e.g. "the request already reached a
// terminal state, so skip the progress assertion" — biases the check toward
// passing. The type system cannot make that unrepresentable, so the
// restriction is stated here as a contract.
type Applicability struct {
	Applies bool
	Reason  string // required when !Applies; recorded in the report
}

// Applies says the phase runs for this case.
func Applies() Applicability { return Applicability{Applies: true} }

// Skip declines with a reason. An empty reason is a LoadError at preflight —
// a skip nobody can explain is indistinguishable from a check that passed.
func Skip(reason string) Applicability { return Applicability{Applies: false, Reason: reason} }
