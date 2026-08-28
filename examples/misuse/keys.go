// Copyright 2026 The Phase Contributors
// SPDX-License-Identifier: MIT

package misuse

import phase "github.com/wow-qe/phase-go"

// Typed handoff: one writer per key, declared wiring, Get fails rather
// than returning a zero value.
var (
	// OrderID: submit produces, everything downstream requires.
	OrderID = phase.Declare[string]("checkout_order_id")
	// AuthCode: authorize produces.
	AuthCode = phase.Declare[string]("checkout_auth_code")
	// StreamCursor is produced by the settlement group's lifecycle setup —
	// the group-Produces feature: members may require it, outsiders are
	// refused at preflight.
	StreamCursor = phase.Declare[string]("checkout_stream_cursor")
	// SettledEntities: settle_wait produces (the produce-xor-assert split:
	// settle_checks asserts over this, never re-queries the store).
	SettledEntities = phase.Declare[[]string]("checkout_settled_entities")
)
