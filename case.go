// Copyright 2026 The Phase Contributors
// SPDX-License-Identifier: MIT

package phase

// CaseStatus is how a real suite survives contact with reality: a case that
// cannot currently run says so, with a reason, and is reported as Skipped —
// not deleted, not commented out, not quietly failing.
type CaseStatus int

const (
	// Active: the case runs.
	Active CaseStatus = iota
	// Quarantined: deliberately excluded from the signal — flaky or under
	// investigation.
	Quarantined
	// Blocked: cannot run until something outside the suite changes
	// (an environment, a product defect, a dependency).
	Blocked
	// Draft: not yet expected to pass; excluded from the signal.
	Draft
)

var caseStatusNames = map[string]CaseStatus{
	"active":      Active,
	"quarantined": Quarantined,
	"blocked":     Blocked,
	"draft":       Draft,
}

func (s CaseStatus) String() string {
	for name, v := range caseStatusNames {
		if v == s {
			return name
		}
	}
	return "invalid"
}

// ParseStatus maps the manifest spelling to a CaseStatus. An unknown string
// is an error, never a default: defaulting is how a typo becomes a case that
// silently always runs (or never does).
func ParseStatus(s string) (CaseStatus, error) {
	v, ok := caseStatusNames[s]
	if !ok {
		// A typed LoadError, not a bare error: the taxonomy promises the Load
		// family maps to exit 2.
		return 0, &LoadError{Code: StatusUnparsable, Subject: s,
			Detail: "valid: active, quarantined, blocked, draft"}
	}
	return v, nil
}

// Case is the consumer's declaration: one of the two interfaces a consumer
// must implement (the other is Interface, the phase contract). However cases
// are loaded — YAML, Go structs, a database — the framework needs only this.
//
// A Case is an immutable input. The framework never writes to it, and a run
// must never end holding a case that differs from what was loaded.
type Case interface {
	// ID is the case's stable name, unique within a session.
	ID() string

	// Status declares whether the case runs at all. Anything but Active is
	// reported as Skipped, with the status as the reason.
	Status() CaseStatus

	// Selects declares whether this case wants the given phase. Declining
	// requires a reason — a skip with no recorded reason is indistinguishable
	// from a check that passed, which is the defect this library exists to
	// prevent. An empty reason on a false return is a LoadError at preflight.
	Selects(ID) (bool, string)

	// Timing returns this case's override for one phase, if it declares one.
	// Overrides carry their justification in the manifest;
	// the framework only needs the values.
	Timing(ID) (Timing, bool)

	// Fixtures are the case's preconditions. Setup runs before the first
	// phase inside the case's scope; Teardown runs after the last phase on
	// every path — success, failure, panic, cancellation.
	Fixtures() []Fixture

	// Exclusive declares the case cannot share the session with others, and
	// why. The reason is required (LoadError otherwise) and is recorded:
	// exclusivity is expensive, and unexplained expense gets copied.
	Exclusive() (bool, string)
}
