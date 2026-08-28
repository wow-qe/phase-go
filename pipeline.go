// Copyright 2026 The Phase Contributors
// SPDX-License-Identifier: MIT

package phase

import "context"

// Interface is the phase contract — one of the two interfaces a consumer
// implements (the other is Case). A phase declares its identity, its wiring
// (dependencies, handoff keys) and its applicability; the Runner owns
// everything else.
//
// Named Interface after the standard library's precedent (sort.Interface):
// the package name already says "phase", and phase.Phase stutters.
type Interface interface {
	// ID is the phase's stable name: it appears in configuration, in
	// dependency declarations, and in every report.
	ID() ID

	// DependsOn lists the phases that must complete first. These are the
	// CODE-declared prerequisites; configuration may add ordering on top but
	// can never remove one of these (see resolve()).
	DependsOn() []ID

	// Produces declares the handoff keys this phase Puts. Preflight refuses
	// a pipeline where two phases produce one key.
	Produces() []KeyID

	// Requires declares the handoff keys this phase Gets. Preflight refuses
	// a pipeline where a required key is not produced within this phase's
	// transitive dependencies — the load-time cure for zero-value handoff
	// reads.
	Requires() []KeyID

	// AppliesTo declares whether this phase runs for the given case. It may
	// read the case declaration and configuration; it may NOT read live
	// system state. Declining requires a reason.
	AppliesTo(Case, Config) Applicability

	// Run does the work: observe through adapters, decide through results,
	// hand values forward through keys.
	Run(context.Context, *Run) error
}

// Func adapts plain values into an Interface, as http.HandlerFunc adapts a
// function into an http.Handler — for the common case where a full type is
// ceremony.
type Func struct {
	PhaseID ID
	Deps    []ID
	Puts    []KeyID
	Gets    []KeyID
	Applies func(Case, Config) Applicability
	Do      func(context.Context, *Run) error
}

func (f Func) ID() ID            { return f.PhaseID }
func (f Func) DependsOn() []ID   { return f.Deps }
func (f Func) Produces() []KeyID { return f.Puts }
func (f Func) Requires() []KeyID { return f.Gets }

func (f Func) AppliesTo(c Case, cfg Config) Applicability {
	if f.Applies == nil {
		return Applies()
	}
	return f.Applies(c, cfg)
}

func (f Func) Run(ctx context.Context, r *Run) error {
	if f.Do == nil {
		return nil
	}
	return f.Do(ctx, r)
}

// Pipeline is the consumer's assembled business process: their phases, in
// declaration order. Assembly is deliberately dumb — every validation lives
// in NewRunner, so there is exactly one place a pipeline can be refused and
// one error surface to test.
type Pipeline struct {
	phases []Interface
	groups []Group // registered via Pipeline.Group; validated at NewRunner
}

// NewPipeline collects the consumer's phases. Validation happens at
// NewRunner, where the configuration needed to judge the pipeline is present.
func NewPipeline(phases ...Interface) *Pipeline {
	return &Pipeline{phases: phases}
}
