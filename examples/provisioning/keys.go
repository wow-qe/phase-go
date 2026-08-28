// Copyright 2026 The Phase Contributors
// SPDX-License-Identifier: MIT

package provisioning

import phase "github.com/wow-qe/phase-go"

// The values phases hand forward, declared once at package init.
// This example adds SettledRows because its settle phase is split in two
// (settle_wait / settle_checks — see phases.go and suite_test.go's
// TestMutationGateGoesRed), and the wait phase needs a typed key to hand
// its result to the assertion phase.
var (
	// RequestID is the correlation value submit allocates and every later
	// phase looks the request up by.
	RequestID = phase.Declare[string]("request_id")

	// Items is the entities the request fanned out into, as discover found
	// them.
	Items = phase.Declare[[]phase.EntityRef]("items")

	// SettledRows is the entities' terminal state, as settle_wait found it.
	// settle_checks reads this rather than querying the store itself, so
	// gutting settle_checks (phasetest.Gutted) removes only the assertions,
	// never the wait.
	SettledRows = phase.Declare[[]Row]("settled_rows")
)
