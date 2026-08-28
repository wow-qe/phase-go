// Copyright 2026 The Phase Contributors
// SPDX-License-Identifier: MIT

package phase

import (
	"context"
	"time"

	"github.com/wow-qe/phase-go/result"
)

// This file is the sanctioned seam between the engine and phasetest: the few
// constructions consumer TESTS need that production code must not have. They
// are exported functions rather than back doors so the boundary is visible,
// documented, and greppable.

// RunOption configures a test-constructed Run.
type RunOption func(*Run)

// WithClock substitutes the time source (phasetest.Clock). Phases never read
// the wall clock; in tests, nothing should.
func WithClock(now func() time.Time) RunOption {
	return func(r *Run) { r.core.now = now }
}

// WithSleeper substitutes the sleeper WaitUntil uses, so a consumer's test
// drives waiting manually instead of actually sleeping.
func WithSleeper(sleep func(context.Context, time.Duration) error) RunOption {
	return func(r *Run) { r.core.sleep = sleep }
}

// WithScope sets the scope a test Run carries.
func WithScope(s Scope) RunOption {
	return func(r *Run) { r.scope = s }
}

// WithPhase installs the phase attribution and resolved timing, as the Runner
// would before invoking a phase (A1: on the handle, at construction).
func WithPhase(id ID, t Timing) RunOption {
	return func(r *Run) { r.phase, r.timing = id, t }
}

// NewRunForTesting constructs a Run outside the Runner — for phasetest and
// for consumers unit-testing a phase against fakes. Production code
// constructs runs only through Runner.Start; the name is deliberately
// awkward in production code review.
func NewRunForTesting(c Case, opts ...RunOption) *Run {
	r := newRun(c, Scope{CaseID: idOrEmpty(c), Correlation: "test"})
	for _, opt := range opts {
		opt(r)
	}
	return r
}

func idOrEmpty(c Case) string {
	if c == nil {
		return ""
	}
	return c.ID()
}

// InterceptRecords installs a transform applied to every result THIS handle
// records, before it reaches the ledger. It is the recorder-wrapping seam the
// AlwaysPass mutation gate needs (phasetest); view-scoped, so the mutation
// cannot leak past the phase deliberately being gutted. Like everything in
// this file, it has no production caller — the name is deliberately awkward
// in production code review.
func InterceptRecords(r *Run, f func(result.Result) result.Result) {
	r.intercept = f
}

// ExecutePhaseForTesting runs one phase's Before→Run→After arc exactly as
// the runner would — same fold, same containment — returning the single
// outcome row. It exists for phasetest.InvokePhase, so consumer tests
// exercise the very code path production runs.
func ExecutePhaseForTesting(ctx context.Context, ph Interface, r *Run) PhaseOutcome {
	return executePhase(ctx, ph, r)
}
