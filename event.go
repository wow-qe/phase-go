// Copyright 2026 The Phase Contributors
// SPDX-License-Identifier: MIT

package phase

import "time"

// The unified event stream: one read-only, ordered, retrospective stream is
// the consumer's lifecycle contract — the closed set below, nothing else.
// Delivery guarantees:
//
//  1. Synchronous and serialized: the callback is never entered
//     concurrently, under either concurrency knob.
//  2. Read-only payloads: value types or deep clones; an observer cannot
//     alter outcomes, by construction.
//  3. Redacted at emission: payloads carrying evidence or error strings
//     pass the same RedactKeys/RedactPatterns as the report — the live
//     stream is safe by default, not just the artifact.
//  4. Pairing is total: every Started has exactly one Finished and vice
//     versa — PhaseStarted fires for every phase, gate-declined ones
//     included (Reached says whether it actually executed), so span
//     pairing never orphans.
//  5. Emission time is never charged against Timing budgets.
//  6. Observer panics are contained, surfaced on Session.ObserverErrors()
//     and the report's Diagnostics — never fatal, never a silent detach.
//  7. WithProgress and WithCaseObserver are frozen projections of this
//     stream; new capability lands here.
type EventKind int

const (
	SessionStarted EventKind = iota
	SessionFinished
	CaseStarted
	CaseFinished
	FixtureSetupStarted
	FixtureSetupFinished
	FixtureTeardownStarted
	FixtureTeardownFinished
	GroupSetupStarted
	GroupSetupFinished
	GroupTeardownStarted
	GroupTeardownFinished
	PhaseStarted
	PhaseFinished
	RetryAttempt

	numEventKinds // sentinel: keeps range-based enumeration (docs render) self-maintaining
)

func (k EventKind) String() string {
	switch k {
	case SessionStarted:
		return "session_started"
	case SessionFinished:
		return "session_finished"
	case CaseStarted:
		return "case_started"
	case CaseFinished:
		return "case_finished"
	case FixtureSetupStarted:
		return "fixture_setup_started"
	case FixtureSetupFinished:
		return "fixture_setup_finished"
	case FixtureTeardownStarted:
		return "fixture_teardown_started"
	case FixtureTeardownFinished:
		return "fixture_teardown_finished"
	case GroupSetupStarted:
		return "group_setup_started"
	case GroupSetupFinished:
		return "group_setup_finished"
	case GroupTeardownStarted:
		return "group_teardown_started"
	case GroupTeardownFinished:
		return "group_teardown_finished"
	case PhaseStarted:
		return "phase_started"
	case PhaseFinished:
		return "phase_finished"
	case RetryAttempt:
		return "retry_attempt"
	}
	return "unknown"
}

// Event is the closed stream element: only this package implements it, so
// the catalog cannot grow behind the contract's back.
type Event interface {
	Kind() EventKind
	CaseID() string // "" for session-scoped events
	At() time.Time
	sealed()
}

type eventBase struct {
	kind   EventKind
	caseID string
	at     time.Time
}

func (e eventBase) Kind() EventKind { return e.kind }
func (e eventBase) CaseID() string  { return e.caseID }
func (e eventBase) At() time.Time   { return e.at }
func (e eventBase) sealed()         {}

// SessionStartedEvent fires once execution begins (preflight passed).
// CaseCount lets "3 of N done" render immediately.
type SessionStartedEvent struct {
	eventBase
	SessionID string
	CaseCount int
}

// SessionFinishedEvent fires when Start is about to return. Deliberately
// not a Summary duplicate — the report owns the math.
type SessionFinishedEvent struct {
	eventBase
	SessionID string
}

// CaseStartedEvent fires when a case begins executing. DeclarationIndex is
// the stable dashboard position — execution order may differ (case DAG,
// concurrency).
type CaseStartedEvent struct {
	eventBase
	DeclarationIndex int
}

// CaseFinishedEvent carries the full report of the finished case — a deep
// clone, redacted at emission.
type CaseFinishedEvent struct {
	eventBase
	Report CaseReport
}

// FixtureEvent covers all four fixture kinds: which fixture (by index, in
// declaration order) and, on the finished kinds, its error if any.
type FixtureEvent struct {
	eventBase
	Index int
	Err   string // redacted; only on *Finished kinds
}

// GroupEvent covers all four group lifecycle kinds.
type GroupEvent struct {
	eventBase
	GroupID ID
	Err     string // redacted; only on *Finished kinds
}

// PhaseStartedEvent fires for every phase — pairing is total. Reached says
// whether the phase actually executes (false: it was gate-declined and its
// PhaseFinished follows immediately); Timing is resolved only when Reached.
type PhaseStartedEvent struct {
	eventBase
	Phase   ID
	Reached bool
	Timing  Timing
}

// PhaseFinishedEvent carries the landed outcome — one event for every way
// a phase can land; filter on Outcome.Status/Stage/DeclineSource.
type PhaseFinishedEvent struct {
	eventBase
	Outcome PhaseOutcome
}

// RetryAttemptEvent is the live heartbeat inside a poll or tolerance loop —
// the "silent 20×15s settle" problem, closed at its last level. Kind keeps
// the retry taxonomy distinct; Of is 0 for unbounded strategies.
type RetryAttemptEvent struct {
	eventBase
	Phase   ID
	Retry   string // "poll" (WaitUntil) | "tolerance" (Tolerate)
	Attempt int
	Of      int
	LastErr string // redacted; the attempt's failure text, if any
}

// WithObserver subscribes to the unified stream. Multiple observers append
// and are dispatched in registration order.
func WithObserver(f func(Event)) RunnerOption {
	return func(r *Runner) { r.eventObservers = append(r.eventObservers, f) }
}
