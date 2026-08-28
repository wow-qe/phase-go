// Copyright 2026 The Phase Contributors
// SPDX-License-Identifier: MIT

package phase

import "context"

// Fixture is a case precondition with a managed lifecycle. The CONTENT of a
// fixture is the consumer's (what to seed, which fault to inject); the
// LIFECYCLE cannot be: the framework guarantees ordering, scoping, and that
// Teardown runs.
//
// Guarantees (tested):
//   - Setup runs before the case's first phase, inside the case's Scope.
//   - Setups run in declaration order; Teardowns in reverse order.
//   - Teardown runs on every path — success, failure, panic, cancellation —
//     on a context detached from any cancelled one, bounded by the phase
//     defaults' Timeout.
//   - A Setup failure is recorded as its own status, not as a case failure:
//     a case whose world could not be built did not fail its assertions.
type Fixture interface {
	Setup(ctx context.Context, r *Run) error
	Teardown(ctx context.Context, r *Run) error
}
