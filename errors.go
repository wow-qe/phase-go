// Copyright 2026 The Phase Contributors
// SPDX-License-Identifier: MIT

package phase

import (
	"errors"
	"fmt"
)

// Three error families, and the family is in the type — because which family
// an error belongs to decides what the consumer should do, and a decision
// that important must not depend on parsing a message.
//
//	LoadError      the suite is mis-declared; nothing ran         → fix, exit 2
//	ordinary error environment/adapter trouble during a case      → Errored, continue
//	FrameworkError phase itself broke an invariant                → distrust report, exit 3

// LoadCode is a machine-readable reason Preflight refused to start.
type LoadCode string

// Everything Preflight can refuse, enumerated. Each has at least one test
// that provokes it.
const (
	DependencyCycle          LoadCode = "dependency_cycle"
	UnknownDependency        LoadCode = "unknown_dependency"
	KeyNeverProduced         LoadCode = "key_never_produced"
	DuplicateKeyProducer     LoadCode = "duplicate_key_producer"
	UnknownConfigKey         LoadCode = "unknown_config_key"
	UnknownPhaseInConfig     LoadCode = "unknown_phase_in_config"
	SkipWithoutReason        LoadCode = "skip_without_reason"
	TimingInvalid            LoadCode = "timing_invalid"
	ScopeCollision           LoadCode = "scope_collision"
	StatusUnparsable         LoadCode = "status_unparsable"
	ExclusiveWithoutReason   LoadCode = "exclusive_without_reason"
	FixtureNil               LoadCode = "fixture_nil"
	DuplicatePhaseID         LoadCode = "duplicate_phase_id"
	DuplicateCaseID          LoadCode = "duplicate_case_id"
	NilCase                  LoadCode = "nil_case"
	CaseIDMissing            LoadCode = "case_id_missing"
	UnknownFixture           LoadCode = "unknown_fixture"
	RedactPatternInvalid     LoadCode = "redact_pattern_invalid"
	DuplicateGroupID         LoadCode = "duplicate_group_id"
	GroupIDReservedCharacter LoadCode = "group_id_reserved_character"
	EmptyGroup               LoadCode = "empty_group"
	UnknownGroupMember       LoadCode = "unknown_group_member"
	GroupMemberDuplicate     LoadCode = "group_member_duplicate"
	SettingsSubRemoved       LoadCode = "settings_sub_removed"
	CaseDependencyUnknown    LoadCode = "case_dependency_unknown"
	EmptySuite               LoadCode = "empty_suite"
	NilPhase                 LoadCode = "nil_phase"
	CaseDependencyCycle      LoadCode = "case_dependency_cycle"
)

// LoadError means the suite is mis-declared. Nothing has run; nothing will
// until the declaration is fixed. Maps to exit code 2.
type LoadError struct {
	Code    LoadCode
	Subject string // the phase, case, key or config path at fault
	Detail  string
}

func (e *LoadError) Error() string {
	return fmt.Sprintf("load: %s: %s: %s", e.Code, e.Subject, e.Detail)
}

// FrameworkError means an invariant of this library was violated. It has
// two distinct fates, decided by where it surfaces. CONTAINED: raised
// during a run (a stage-capability or Produces violation), it is recorded
// as that case's evidence — the case is Errored, the report stays
// internally consistent, exit code 1. UNCONTAINED: returned by
// Report.Verify, meaning the report itself is inconsistent — exit code 3,
// the one signal that the numbers cannot be trusted. ExitCode() is the
// single authority for that mapping.
type FrameworkError struct {
	Invariant string
	Detail    string
}

func (e *FrameworkError) Error() string {
	return fmt.Sprintf("framework invariant violated (%s): %s — this is a bug in phase, not in your suite", e.Invariant, e.Detail)
}

// Sentinels for the run-time paths that callers branch on.
var (
	// ErrKeyNotProduced: a phase asked for a handoff value that no earlier
	// phase produced in this run. "Discovery never ran" and "discovery found
	// nothing" are different facts; a zero value here would conflate them.
	ErrKeyNotProduced = errors.New("phase: key not produced in this run")

	// ErrBudgetExhausted: WaitUntil ran out of attempts. The wrapping error
	// names the budget, so the failure reads "gave up after 20×15s", never
	// "nothing found".
	ErrBudgetExhausted = errors.New("phase: wait budget exhausted")
)
