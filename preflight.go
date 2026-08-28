// Copyright 2026 The Phase Contributors
// SPDX-License-Identifier: MIT

package phase

import "fmt"

// Preflight refuses mis-declared CASES, executing nothing. Structural and
// configuration problems were already refused at NewRunner; what remains is
// everything a case can get wrong: a skip with no reason, exclusivity with no
// justification, a nil fixture, and scopes that collide.
func (r *Runner) Preflight(cases []Case) error {
	seenCorrelation := map[string]string{} // correlation -> case that owns it
	seenID := map[string]bool{}
	for _, c := range cases {
		// A nil Case must be a typed refusal, not a panic mid-batch.
		if c == nil {
			return &LoadError{Code: NilCase, Subject: "<nil>",
				Detail: "the case slice contains a nil Case"}
		}
		// IDs are documented unique within a session; enforce it, or two
		// indistinguishable rows reach the report.
		if seenID[c.ID()] {
			return &LoadError{Code: DuplicateCaseID, Subject: c.ID(),
				Detail: "two cases share this ID; they would be indistinguishable in the report"}
		}
		seenID[c.ID()] = true
		// Every phase is asked, so an unexplained skip is found at second 3,
		// not discovered as missing coverage in a report weeks later.
		for _, ph := range r.phases {
			if selected, reason := c.Selects(ph.ID()); !selected && reason == "" {
				return &LoadError{Code: SkipWithoutReason, Subject: c.ID(),
					Detail: fmt.Sprintf("declines phase %q with no reason; an unexplained skip is indistinguishable from a check that passed", ph.ID())}
			}
		}
		if exclusive, why := c.Exclusive(); exclusive && why == "" {
			return &LoadError{Code: ExclusiveWithoutReason, Subject: c.ID(),
				Detail: "exclusivity is expensive, and unexplained expense gets copied"}
		}
		// Snapshot Fixtures() ONCE - a non-idempotent Case could
		// otherwise return a valid slice to Preflight and a nil-containing one
		// to setupFixtures. The snapshot is what actually runs (see runCase).
		fixtures := c.Fixtures()
		for i, f := range fixtures {
			if f == nil {
				return &LoadError{Code: FixtureNil, Subject: c.ID(),
					Detail: fmt.Sprintf("fixture %d is nil", i)}
			}
		}

		// Scope collisions: with the default allocator this cannot fire; a
		// consumer allocator constrained by its domain can collide, and a
		// collision produces a false product-defect report — the most
		// expensive failure there is. Detecting duplicates here is the whole
		// of what the framework can promise for consumer-set values.
		scope, err := r.allocator.Allocate(c)
		if err != nil {
			return &LoadError{Code: ScopeCollision, Subject: c.ID(),
				Detail: fmt.Sprintf("scope allocation failed: %v", err)}
		}
		if scope.Correlation != "" {
			if owner, taken := seenCorrelation[scope.Correlation]; taken {
				return &LoadError{Code: ScopeCollision, Subject: c.ID(),
					Detail: fmt.Sprintf("correlation %q is already allocated to case %q", scope.Correlation, owner)}
			}
			seenCorrelation[scope.Correlation] = c.ID()
		}
	}
	// The case DAG is validated AFTER the per-case guards - a nil case
	// or duplicate ID must get its own typed refusal, never a panic
	// or a framework-error from the dag layer.
	return validateCaseDeps(cases)
}
