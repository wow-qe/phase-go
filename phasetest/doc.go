// Copyright 2026 The Phase Contributors
// SPDX-License-Identifier: MIT

// Package phasetest is the consumer's test kit: it lets a phase.Case or a
// phase.Interface implementation be exercised without a live system.
//
// It imports only the root phase package, package result, and the standard
// library — the same no-dependency discipline the root package holds itself
// to — so pulling in phasetest never drags anything unexpected into a
// consumer's test binary.
//
// # Mutation gates
//
// The mutation-gate idea: replace a phase with something that cannot
// possibly do its job, re-run the suite, and if the suite stays green, the
// suite was never exercising what that phase asserts. Both ways to gut a
// phase's job ship as wrappers:
//
//   - Gutted stops the phase from recording anything at all — "does the
//     suite notice a phase that asserts nothing?"
//   - AlwaysPass runs the phase for real but flips every result it records
//     into a pass — "does this case's verdict actually ride on this phase's
//     comparisons?"
//
// AlwaysPass rides phase.InterceptRecords, the engine's sanctioned
// recorder-interception seam (testhooks.go): the interception is bound to
// the wrapped phase's own recording handle, so it matches production wiring
// exactly and cannot leak into other phases' evidence.
package phasetest
