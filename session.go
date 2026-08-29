// Copyright 2026 The Phase Contributors
// SPDX-License-Identifier: MIT

package phase

import (
	"regexp"
	"time"
)

// Session is one whole execution — the Runner's state at the outer level,
// as Run is its state at the case level. In prose: say "session"
// for the whole thing, "the run for case X" for one case, never "the run"
// unqualified.
type Session struct {
	id             string
	started        time.Time
	finished       time.Time
	cases          []CaseReport
	redactKeys     []string         // Config.RedactKeys, applied to every Report() built from this session
	redactPatterns []*regexp.Regexp // Config.RedactPatterns, likewise
	observerErrs   []error          // contained observer/progress panics - degraded observability, surfaced in the report
}

// ObserverErrors reports every contained observer/progress-callback panic:
// the run completed and no verdict was touched, but live observability was
// degraded - a fact that must be loud, never a silent detach.
func (s *Session) ObserverErrors() []error {
	return append([]error(nil), s.observerErrs...)
}

// ID identifies this execution: it is how two reports are told apart, and
// what a snapshot comparison keys its provenance on.
func (s *Session) ID() string { return s.id }

// Started and Finished bound the execution.
func (s *Session) Started() time.Time  { return s.started }
func (s *Session) Finished() time.Time { return s.finished }

// Cases returns the per-case reports, in the order the cases were given.
func (s *Session) Cases() []CaseReport {
	out := make([]CaseReport, len(s.cases))
	for i := range s.cases {
		out[i] = s.cases[i].clone()
	}
	return out
}

// clone deep-copies the evidence slices: a shallow copy would share backing
// storage, so a consumer redacting or formatting a returned Report could
// silently corrupt the Session for every later Report() call.
func (c CaseReport) clone() CaseReport {
	c.Phases = append([]PhaseOutcome(nil), c.Phases...)
	c.Groups = append([]GroupOutcome(nil), c.Groups...)
	if c.DependencyFailure != nil {
		df := *c.DependencyFailure
		df.Acceptable = append([]Status(nil), df.Acceptable...)
		c.DependencyFailure = &df
	}
	c.Results = append([]AttributedResult(nil), c.Results...)
	c.Observations = append([]Observation(nil), c.Observations...)
	c.Errors = append([]AttributedError(nil), c.Errors...)
	return c
}

// PhaseOutcome is one phase's disposition for one case. Every phase in the
// pipeline gets exactly one, whatever happened — a phase that did not run
// says so and says why, because a skip with no record is indistinguishable
// from a check that passed.
//
// The json tags on this and the structs below are the stable schema
// surface: they live on the real struct, so the compiler links schema to
// source, and TestSchemaSurfaceIsFullyTagged refuses any untagged addition
// — a field cannot silently go missing from the emitted report.
type PhaseOutcome struct {
	ID     ID     `json:"id"`
	Status Status `json:"status"`
	Reason string `json:"reason,omitempty"` // required for NotApplicable and Disabled; empty otherwise
	// How many results this phase recorded; a phase that ran and asserted
	// nothing (0) must be distinguishable from one that asserted, so the
	// field is never omitted — zero is the fact it exists to expose.
	Results int `json:"results_recorded"`
	// Failing is the sibling count: how many of those results failed — closing
	// the "row says Passed, results say failing" reading gap. Never omitted.
	Failing int `json:"failing_recorded"`
	// Stage marks which hook produced a non-ordinary outcome; empty for
	// ordinary Run outcomes. Typed closed set; Verify refuses others.
	Stage Stage `json:"stage,omitempty"`
	// DeclineSource says structurally why this phase never ran — the
	// prose Reason is for humans, this is for aggregation. Empty when the
	// phase was attempted.
	DeclineSource DeclineSource `json:"decline_source,omitempty"`
	// AttemptsUsed is the bounded settle-cost summary: how many
	// WaitUntil polls / Tolerate checks this phase actually consumed — the
	// signal, without a transcript.
	AttemptsUsed int `json:"attempts_used,omitempty"`
}

// GroupOutcome is one group's disposition for one case: its lifecycle
// status with setup and teardown failures always distinguishable, the
// members it scoped, and how much evidence its own lifecycle recorded
// (zero is a fact, never omitted — the same rule, applied to groups).
type GroupOutcome struct {
	GroupID  ID     `json:"group_id"`
	Status   Status `json:"status"`
	Reason   string `json:"reason,omitempty"`
	Members  []ID   `json:"members"`
	Recorded int    `json:"results_recorded"`
}

// CaseReport is the derived record of one case. Status is computed in
// exactly one place (deriveStatus) from the recorded evidence: a single
// writer, so the status can never diverge from what was recorded.
type CaseReport struct {
	CaseID            string             `json:"id"`
	Correlation       string             `json:"correlation,omitempty"` // Scope.Correlation - joins this report to the system's own logs
	Status            Status             `json:"status"`
	Reason            string             `json:"reason,omitempty"`    // for Skipped/Errored: what happened
	FailedIn          ID                 `json:"failed_in,omitempty"` // first phase that recorded a failing result
	Curtailed         bool               `json:"curtailed,omitempty"` // cancelled mid-flight; the result stands, the interruption reaches the artifact
	Phases            []PhaseOutcome     `json:"phases"`
	Results           []AttributedResult `json:"results"`
	Groups            []GroupOutcome     `json:"groups,omitempty"`             // one row per registered group this case touched (or visibly did not)
	DependencyFailure *DependencyFailure `json:"dependency_failure,omitempty"` // set when Status is Skipped because a case dependency was unmet
	Observations      []Observation      `json:"observations,omitempty"`
	Errors            []AttributedError  `json:"errors,omitempty"`
	Started           time.Time          `json:"started"`
	Finished          time.Time          `json:"finished"`
}

// AttributedResult and AttributedError are the exported forms of the run's
// evidence, attributed to the phase that produced them.
type AttributedResult struct {
	Phase  ID             `json:"phase"`  // legacy string attribution (kept for compat readers)
	Source EvidenceSource `json:"source"` // typed attribution — the authoritative form
	Result ResultView     `json:"result"`
}

type AttributedError struct {
	Phase  ID             `json:"phase"`
	Source EvidenceSource `json:"source"`
	Err    string         `json:"error"`
}

// ResultView is the report-facing projection of result.Result. The result
// package keeps its fields unexported to protect the construction invariant;
// the report needs the values; this is the one sanctioned crossing.
type ResultView struct {
	Name        string    `json:"name"`
	Entity      EntityRef `json:"entity,omitempty"`
	Passed      bool      `json:"passed"`
	Reason      string    `json:"reason,omitempty"`
	Expected    any       `json:"expected,omitempty"`
	Actual      any       `json:"actual,omitempty"`
	Comparisons int       `json:"comparisons"`
}
