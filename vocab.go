// Copyright 2026 The Phase Contributors
// SPDX-License-Identifier: MIT

package phase

// This file closes several stringly-typed conventions into typed,
// exhaustively-checked vocabularies: Stage, DeclineSource and SourceKind
// are each a fixed set of values, validated at Verify. Wire representations
// are unchanged — these close the sets, they do not rename the values.

// Stage names which hook stage produced a non-ordinary phase outcome.
// Closed set; Verify refuses anything else. Empty means an ordinary Run
// outcome.
type Stage string

const (
	StageBefore    Stage = "before"
	StageAfter     Stage = "after"
	StageCondition Stage = "condition"
)

func validStage(s Stage) bool {
	switch s {
	case "", StageBefore, StageAfter, StageCondition:
		return true
	}
	return false
}

// DeclineSource says structurally why a phase never ran — the axis Stage
// does not carry (Stage: which stage of an attempted phase spoke;
// DeclineSource: why there was no attempt). The prose Reason stays for
// humans; aggregation filters on this, never on prefixes.
type DeclineSource string

const (
	DeclinedByCase       DeclineSource = "case"        // Case.Selects said no
	DeclinedByPhase      DeclineSource = "phase"       // AppliesTo said no
	DeclinedByCondition  DeclineSource = "condition"   // When said no
	DeclinedByConfig     DeclineSource = "config"      // operator kill-switch
	DeclinedByDependency DeclineSource = "dependency"  // pruned: a dependency errored
	DeclinedByGroupSetup DeclineSource = "group_setup" // its group's world could not be built
)

func validDeclineSource(d DeclineSource) bool {
	switch d {
	case "", DeclinedByCase, DeclinedByPhase, DeclinedByCondition,
		DeclinedByConfig, DeclinedByDependency, DeclinedByGroupSetup:
		return true
	}
	return false
}

// SourceKind types evidence attribution — the machine-readable half of the
// "group:<id>:setup" string convention, which stays on the legacy Phase
// field for compatibility readers.
type SourceKind string

const (
	SourcePhase         SourceKind = "phase"
	SourceGroupSetup    SourceKind = "group_setup"
	SourceGroupTeardown SourceKind = "group_teardown"
	SourceFixture       SourceKind = "fixture"
	SourceSession       SourceKind = "session"
)

// EvidenceSource is typed attribution: which kind of machinery recorded a
// piece of evidence, and that machinery's own identity (a phase ID, a
// group ID, empty for fixtures/session events).
type EvidenceSource struct {
	Kind SourceKind `json:"kind"`
	ID   ID         `json:"id,omitempty"`
}
