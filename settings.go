// Copyright 2026 The Phase Contributors
// SPDX-License-Identifier: MIT

package phase

import "time"

// Timing bounds a phase's waiting. Budgets are in attempts, per case: a
// shared wall-clock ceiling across cases penalizes a case merely scheduled
// late, so wall-clock is only a backstop and must never be the binding
// constraint in a healthy run.
type Timing struct {
	// Attempts is how many times a WaitUntil condition may be evaluated.
	Attempts int
	// Interval separates attempts.
	Interval time.Duration
	// Timeout is the wall-clock backstop for the whole phase, including
	// teardown's ceiling on cancellation.
	Timeout time.Duration
	// SettleDelay is the one explicit exception to "wait for a condition,
	// not a duration": a downstream system that is eventually-consistent
	// with no observable readiness signal. It is named as a delay so it
	// stays visible as debt.
	SettleDelay time.Duration
}

// Settings is one phase's declared configuration node.
type Settings struct {
	// Enabled: nil inherits; false is the operator disable switch ("don't test
	// the provider today"), reported as Disabled — deliberate coverage loss,
	// visible as such.
	Enabled *bool
	// DependsOn orders this phase after others. Cycles and unknown IDs are
	// LoadErrors at preflight.
	DependsOn []ID
	// Sub is retained only for decoding compatibility: any non-empty value
	// is rejected at construction (settings_sub_removed). Scoped lifecycle
	// belongs to Pipeline.Group.
	Sub map[ID]Settings
	// Timing overrides the inherited defaults where set (zero fields
	// inherit).
	Timing Timing
	// Optional: a case not selecting this phase is NotApplicable rather
	// than an error.
	Optional bool
}

// Config is the resolved configuration the Runner is constructed with. The
// core validates the struct; parsing files into it lives in x/config (or the
// consumer's loader), so the core keeps its no-dependency rule. Unknown keys
// are the loader's problem and must be a load-time error there.
type Config struct {
	// Defaults seed every phase's Timing; per-phase and per-case values
	// override field-wise.
	Defaults Timing
	// Phases configures each phase by ID. An entry naming a phase the
	// Pipeline does not contain is a LoadError (unknown_phase_in_config) —
	// configuration for a phase that does not exist is either a typo or a
	// leftover from a removed phase, and both should stop the
	// run.
	Phases map[ID]Settings
	// RedactKeys names the keys whose values are redacted in every Report()
	// this configuration produces - the always-on floor for
	// secrets with known names (authorization, password, set-cookie).
	// Report.Redact / RedactMatching cover the names only known at paste
	// time. Case-insensitive.
	RedactKeys []string
	// RedactPatterns are regular expressions scrubbed from every string
	// carrier in every Report() (error strings, case and phase-outcome
	// reasons, result reasons, string observation values, observation
	// names). Key-based RedactKeys cannot reach free text; a DSN in a
	// connection error rides exactly there. Invalid patterns are refused at
	// NewRunner - a redaction that never compiled is a redaction that never
	// ran, silently.
	RedactPatterns []string
	// MaxPhaseConcurrency lets same-DAG-level phases of one case overlap.
	// 0 or 1 means sequential (the default). Before raising it: two
	// same-level phases sharing one adapter instance become concurrent
	// callers of it; audit for goroutine-safety first.
	MaxPhaseConcurrency int
	// MaxCaseConcurrency lets cases overlap, honouring Exclusive() (an
	// exclusive case drains the pool and runs alone) and case dependencies.
	// 0 or 1 means sequential (the default). Before raising it: every
	// Fixture and adapter must partition by Scope, or a shared client that
	// worked sequentially becomes a race that reads as a flaky product
	// defect. Mark anything uncertain Exclusive() until proven safe. Note
	// also: WithCaseObserver and WithProgress fire in completion order
	// under concurrency (that is their value); only Session.Cases()/Report
	// keep declaration order. Aggregate poll rates multiply — flake rates
	// can rise from backend contention.
	MaxCaseConcurrency int
	// MaxObservationsPerCase bounds evidence retention (unbounded
	// retention is an OOM wall in CI containers). Zero means unlimited.
	// Truncation is loud: the report carries a marker observation naming
	// exactly how many observations were dropped — a silent cap is a
	// default, and defaults are how evidence disappears.
	MaxObservationsPerCase int
}
