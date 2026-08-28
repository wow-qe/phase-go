// Copyright 2026 The Phase Contributors
// SPDX-License-Identifier: MIT

package phasetest

import (
	"context"
	"fmt"

	phase "github.com/wow-qe/phase-go"
	"github.com/wow-qe/phase-go/result"
)

// Gutted returns a phase with inner's identity and wiring — ID, DependsOn,
// Requires, AppliesTo — whose Run records NOTHING and returns nil. It is the
// mutation gate as a library: replace an assertion phase with
// Gutted(phase) and re-run the suite. If the suite stays green, nothing was
// testing what that phase asserts, and the suite's green is not evidence.
//
// Gutted REFUSES (panics with a clear message) a phase whose Produces() is
// non-empty: gutting a producer would starve dependents into loud, unrelated
// failures, and the gate must measure assertion coverage, not wiring.
func Gutted(inner phase.Interface) phase.Interface {
	if produces := inner.Produces(); len(produces) > 0 {
		panic(fmt.Sprintf(
			"phasetest.Gutted: phase %q produces %v — gutting a producer would starve its dependents into unrelated failures instead of measuring assertion coverage; Gutted is only for phases that assert, not phases that hand values forward",
			inner.ID(), produces))
	}
	return guttedPhase{inner: inner}
}

// guttedPhase wraps a phase, keeping its identity and wiring but replacing
// Run with a no-op. It is unexported: consumers get one only through Gutted,
// which enforces the producer refusal.
type guttedPhase struct {
	inner phase.Interface
}

func (g guttedPhase) ID() phase.ID          { return g.inner.ID() }
func (g guttedPhase) DependsOn() []phase.ID { return g.inner.DependsOn() }

// Produces is always empty: Gutted refuses any inner phase for which it
// would not be.
func (g guttedPhase) Produces() []phase.KeyID { return g.inner.Produces() }

func (g guttedPhase) Requires() []phase.KeyID { return g.inner.Requires() }

func (g guttedPhase) AppliesTo(c phase.Case, cfg phase.Config) phase.Applicability {
	return g.inner.AppliesTo(c, cfg)
}

// Run records nothing and does nothing: the wrapped phase's Run is never
// invoked.
func (g guttedPhase) Run(ctx context.Context, r *phase.Run) error { return nil }

var _ phase.Interface = guttedPhase{}

// AlwaysPass is the second mutation gate: it runs the wrapped phase for real
// — wiring, handoffs and all — but flips every result it records into a pass
// (name, entity and evidence preserved; the verdict forced). Where Gutted
// answers "does the suite notice a phase that records NOTHING?", AlwaysPass
// answers "does this case's verdict actually ride on THIS phase's
// comparisons?": wrap one phase, re-run a case that must fail, and if it
// flips green, that phase's judgement was the one doing the work.
//
// Unlike Gutted, producer phases are fine here: the phase genuinely runs,
// so its Puts still happen and the pipeline's handoff graph stays honest.
func AlwaysPass(inner phase.Interface) phase.Interface {
	return alwaysPassPhase{inner: inner}
}

type alwaysPassPhase struct {
	inner phase.Interface
}

func (a alwaysPassPhase) ID() phase.ID          { return a.inner.ID() }
func (a alwaysPassPhase) DependsOn() []phase.ID { return a.inner.DependsOn() }

func (a alwaysPassPhase) Produces() []phase.KeyID { return a.inner.Produces() }

func (a alwaysPassPhase) Requires() []phase.KeyID { return a.inner.Requires() }

func (a alwaysPassPhase) AppliesTo(c phase.Case, cfg phase.Config) phase.Applicability {
	return a.inner.AppliesTo(c, cfg)
}

// Before forwards the wrapped phase's Before (if any), installing the flip
// FIRST so a hook-recorded failure cannot escape the mutation. After
// forwards likewise; the runner discovers both through the wrapper, so a
// hook-bearing phase under AlwaysPass still runs its full arc.
func (a alwaysPassPhase) Before(ctx context.Context, r *phase.Run) error {
	installFlip(r)
	if bh, ok := a.inner.(phase.BeforeHook); ok {
		return bh.Before(ctx, r)
	}
	return nil
}

func (a alwaysPassPhase) After(ctx context.Context, r *phase.Run, po phase.PhaseOutcome) error {
	if ah, ok := a.inner.(phase.AfterHook); ok {
		return ah.After(ctx, r, po)
	}
	return nil
}

func (a alwaysPassPhase) Run(ctx context.Context, r *phase.Run) error {
	installFlip(r)
	return a.inner.Run(ctx, r)
}

func installFlip(r *phase.Run) {
	phase.InterceptRecords(r, func(res result.Result) result.Result {
		forced := result.Compared(res.Name(), []bool{true}).ForEntity(res.Entity())
		if e := res.Expected(); e != nil {
			forced = forced.WithExpected(e)
		}
		if v := res.Actual(); v != nil {
			forced = forced.WithActual(v)
		}
		return forced
	})
}
