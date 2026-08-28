// Copyright 2026 The Phase Contributors
// SPDX-License-Identifier: MIT

package phase

// Status is the outcome vocabulary, carried distinct to the end of the report
// and never collapsed to a boolean on the way. In particular: a skip is not a
// failure, an error is not a failure, and a flake is not a clean pass.
type Status int

const (
	// Passed: every result recorded for the case passed.
	Passed Status = iota
	// Failed: at least one result failed. A claim about the system under
	// test, backed by recorded expected/actual evidence.
	Failed
	// Skipped: the case declared itself not runnable (status, selection).
	// A claim about the suite, not the system.
	Skipped
	// NotApplicable: a phase's AppliesTo declined, with a recorded reason.
	NotApplicable
	// Disabled: an operator turned the phase off in configuration. Distinct
	// from NotApplicable so deliberate coverage loss is visible as such.
	Disabled
	// Errored: the environment or an adapter failed; the case's assertions
	// were not completed. Not a product failure — reporting it as one sends
	// someone to debug the wrong thing.
	Errored
	// Flaked: passed, but only on a tolerated retry. "Passed on attempt 3"
	// is a different fact from "passed", and the report must not launder it.
	Flaked
)

var statusNames = [...]string{
	Passed:        "passed",
	Failed:        "failed",
	Skipped:       "skipped",
	NotApplicable: "not_applicable",
	Disabled:      "disabled",
	Errored:       "errored",
	Flaked:        "flaked",
}

func (s Status) String() string {
	if int(s) < 0 || int(s) >= len(statusNames) {
		return "invalid"
	}
	return statusNames[s]
}
