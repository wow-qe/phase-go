// Copyright 2026 The Phase Contributors
// SPDX-License-Identifier: MIT

package phase

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"
)

// SchemaVersion identifies the report's JSON shape. It is a compatibility
// surface independent of the module version: snapshot comparison refuses to
// diff across versions, and it bumps only on breaking report changes.
const SchemaVersion = "1"

// Report is the product of a session. It states what was decided, the
// evidence for each decision, and — as a first-class section — what was not
// verified: a report that silently implies total coverage is the failure this
// library is a reaction to.
type Report struct {
	Schema      string       `json:"schema_version"`
	Session     SessionInfo  `json:"session"`
	Cases       []CaseReport `json:"cases"`
	Summary     Summary      `json:"summary"`
	NotVerified []string     `json:"not_verified"`
	// Diagnostics carries engine-level degradations that altered no verdict
	// but the reader must know about - e.g. contained observer panics:
	// live observability was degraded; recorded so the report says so.
	Diagnostics []string `json:"diagnostics,omitempty"`
}

// SessionInfo identifies which execution produced this report.
type SessionInfo struct {
	ID       string    `json:"id"`
	Started  time.Time `json:"started"`
	Finished time.Time `json:"finished"`
}

// Summary counts each status distinctly — never collapsed toward a boolean,
// because a skip is not a failure and an error is not a failure.
type Summary struct {
	Total         int `json:"total"`
	Passed        int `json:"passed"`
	Failed        int `json:"failed"`
	Skipped       int `json:"skipped"`
	NotApplicable int `json:"not_applicable"`
	Disabled      int `json:"disabled"`
	Errored       int `json:"errored"`
	Flaked        int `json:"flaked"`
}

// Report builds the session's report: counts recomputed from the cases, and
// deliberate coverage loss surfaced by name in NotVerified.
func (s *Session) Report() *Report {
	rep := &Report{
		Schema:  SchemaVersion,
		Session: SessionInfo{ID: s.id, Started: s.started, Finished: s.finished},
		Cases:   s.Cases(),
	}
	disabled := map[ID]int{}
	var disabledOrder []ID
	for _, cr := range rep.Cases {
		rep.Summary.Total++
		switch cr.Status {
		case Passed:
			rep.Summary.Passed++
		case Failed:
			rep.Summary.Failed++
		case Skipped:
			rep.Summary.Skipped++
		case NotApplicable:
			rep.Summary.NotApplicable++
		case Disabled:
			rep.Summary.Disabled++
		case Errored:
			rep.Summary.Errored++
		case Flaked:
			rep.Summary.Flaked++
		}
		for _, po := range cr.Phases {
			if po.Status == Disabled {
				if disabled[po.ID] == 0 {
					disabledOrder = append(disabledOrder, po.ID)
				}
				disabled[po.ID]++
			}
		}
	}
	// What this run did not verify, stated once explicitly rather than left to
	// be summed out of per-case rows.
	for _, id := range disabledOrder {
		rep.NotVerified = append(rep.NotVerified,
			fmt.Sprintf("phase %q was disabled by configuration for %d case(s); nothing it asserts was checked", id, disabled[id]))
	}
	if rep.NotVerified == nil {
		rep.NotVerified = []string{}
	}
	for _, err := range s.observerErrs {
		rep.Diagnostics = append(rep.Diagnostics,
			fmt.Sprintf("an observer callback panicked and was contained; live observability was degraded: %v", err))
	}
	// The configured redaction floor applies to every report this session
	// builds — a report is safe to paste by default, not by remembering.
	rep.Redact(s.redactKeys...)
	for _, re := range s.redactPatterns {
		rep.RedactMatching(re)
	}
	return rep
}

// ExitCode is the mapping from a report to a process result — one
// implementation for every entry point, because a suite that cannot fail a CI
// job is decorative:
//
//	0  nothing failed (skips, not-applicable and disabled do not fail CI —
//	   they are visible in the report and in NotVerified instead)
//	1  at least one case Failed or Errored
//	3  Verify() failed: the report is internally inconsistent and the
//	   numbers cannot be trusted — exiting 1 would misdirect debugging
//	   the product when the bug is in phase
//
// (2 is the LoadError exit, mapped by the entry point before a report
// exists.)
func (r *Report) ExitCode() int {
	if err := r.Verify(); err != nil {
		return 3
	}
	if r.Summary.Failed > 0 || r.Summary.Errored > 0 {
		return 1
	}
	return 0
}

// Verify asserts the report's internal consistency. It is an assertion, not a
// repair: deriving outcomes from evidence (runner.finish) removes the cause
// of an inconsistent report, and Verify removes the doubt. If it returns an
// error, the inconsistency originates in this library, not in the suite
// under test.
func (r *Report) Verify() error {
	if r.Schema != SchemaVersion {
		return &FrameworkError{Invariant: "schema version",
			Detail: fmt.Sprintf("report carries %q, this build writes %q", r.Schema, SchemaVersion)}
	}

	recount := Summary{}
	for i := range r.Cases {
		cr := &r.Cases[i]
		recount.Total++

		var failing, results int
		for _, ar := range cr.Results {
			results++
			if !ar.Result.Passed {
				failing++
			}
			if ar.Result.Passed && ar.Result.Comparisons <= 0 {
				return &FrameworkError{Invariant: "no pass over zero comparisons",
					Detail: fmt.Sprintf("case %q, result %q", cr.CaseID, ar.Result.Name)}
			}
			// A failing result must explain itself — a reason, or
			// expected/actual — or it tells a debugger nothing and is
			// indistinguishable from a real one. Reason counts as evidence
			// because the sanctioned constructors put the explanation there
			// (a failing Compared says "1 of 2 comparisons failed"); demanding
			// structured expected/actual on every failure would classify the
			// founding-rule API's own output as a framework bug. Passing
			// results are exempt — a boolean-flag check has none of the three.
			if !ar.Result.Passed && ar.Result.Reason == "" && ar.Result.Expected == nil && ar.Result.Actual == nil {
				return &FrameworkError{Invariant: "failing results explain themselves",
					Detail: fmt.Sprintf("case %q, result %q failed with no reason and no expected/actual", cr.CaseID, ar.Result.Name)}
			}
		}

		switch cr.Status {
		case Passed:
			recount.Passed++
			if failing > 0 || len(cr.Errors) > 0 || results == 0 {
				return &FrameworkError{Invariant: "status derived from evidence",
					Detail: fmt.Sprintf("case %q is Passed holding %d failing result(s), %d error(s), %d result(s)",
						cr.CaseID, failing, len(cr.Errors), results)}
			}
		case Failed:
			recount.Failed++
			if failing == 0 {
				return &FrameworkError{Invariant: "status derived from evidence",
					Detail: fmt.Sprintf("case %q is Failed with no failing result", cr.CaseID)}
			}
			if cr.FailedIn == "" {
				return &FrameworkError{Invariant: "failures name their phase",
					Detail: fmt.Sprintf("case %q is Failed with no FailedIn", cr.CaseID)}
			}
		case Skipped:
			recount.Skipped++
			if cr.Reason == "" {
				return &FrameworkError{Invariant: "skips carry reasons",
					Detail: fmt.Sprintf("case %q is Skipped with no reason", cr.CaseID)}
			}
		case NotApplicable:
			recount.NotApplicable++
		case Disabled:
			recount.Disabled++
		case Errored:
			recount.Errored++
			if len(cr.Errors) == 0 && cr.Reason == "" {
				return &FrameworkError{Invariant: "errors carry evidence",
					Detail: fmt.Sprintf("case %q is Errored with neither errors nor a reason", cr.CaseID)}
			}
		case Flaked:
			recount.Flaked++
			// Flaked means passed on a tolerated retry, which requires
			// evidence of passing and no failed comparisons. A Flaked case
			// with no results is a status without supporting evidence.
			if results == 0 || failing > 0 {
				return &FrameworkError{Invariant: "flaked means passed on retry",
					Detail: fmt.Sprintf("case %q is Flaked with %d result(s), %d failing", cr.CaseID, results, failing)}
			}
		default:
			return &FrameworkError{Invariant: "known status",
				Detail: fmt.Sprintf("case %q carries status %d", cr.CaseID, cr.Status)}
		}

		// A phase outcome that declined must say why — a skip with no reason
		// is indistinguishable from a check that passed.
		for _, po := range cr.Phases {
			if (po.Status == NotApplicable || po.Status == Disabled) && po.Reason == "" {
				return &FrameworkError{Invariant: "skips carry reasons",
					Detail: fmt.Sprintf("case %q, phase %q: %s with no reason", cr.CaseID, po.ID, po.Status)}
			}
			if po.Failing < 0 || po.Failing > po.Results {
				return &FrameworkError{Invariant: "failing within recorded",
					Detail: fmt.Sprintf("case %q, phase %q: failing_recorded %d outside [0,%d]", cr.CaseID, po.ID, po.Failing, po.Results)}
			}
			// A row that says "did not run" cannot carry recorded results:
			// evidence under a claimed skip is asserting while claiming not
			// to, the inverse of a pass over zero comparisons.
			if (po.Status == NotApplicable || po.Status == Disabled) && po.Results > 0 {
				return &FrameworkError{Invariant: "skips carry no results",
					Detail: fmt.Sprintf("case %q, phase %q: %s with %d recorded result(s)", cr.CaseID, po.ID, po.Status, po.Results)}
			}
			if !validStage(po.Stage) {
				return &FrameworkError{Invariant: "stages are a closed set",
					Detail: fmt.Sprintf("case %q, phase %q carries unknown stage %q", cr.CaseID, po.ID, po.Stage)}
			}
			if !validDeclineSource(po.DeclineSource) {
				return &FrameworkError{Invariant: "decline sources are a closed set",
					Detail: fmt.Sprintf("case %q, phase %q carries unknown decline_source %q", cr.CaseID, po.ID, po.DeclineSource)}
			}
		}

		// Group-attributed evidence and group rows must agree — evidence
		// under a reserved group ID with no GroupOutcome row (or a group
		// disposition with no reason) is a report that does not mean what it
		// says.
		groupRows := map[string]bool{}
		for _, g := range cr.Groups {
			groupRows[string(g.GroupID)] = true
			if (g.Status == NotApplicable || g.Status == Errored) && g.Reason == "" {
				return &FrameworkError{Invariant: "group dispositions carry reasons",
					Detail: fmt.Sprintf("case %q, group %q: %s with no reason", cr.CaseID, g.GroupID, g.Status)}
			}
		}
		// The typed source is authoritative for group attribution — it is
		// never inferred by parsing the legacy "group:<id>:..." phase
		// string.
		checkGroupEvidence := func(src EvidenceSource) error {
			if src.Kind != SourceGroupSetup && src.Kind != SourceGroupTeardown {
				return nil
			}
			if !groupRows[string(src.ID)] {
				return &FrameworkError{Invariant: "group evidence has a group row",
					Detail: fmt.Sprintf("case %q carries evidence for group %q with no GroupOutcome row", cr.CaseID, src.ID)}
			}
			return nil
		}
		for _, ar := range cr.Results {
			if err := checkGroupEvidence(ar.Source); err != nil {
				return err
			}
		}
		for _, ae := range cr.Errors {
			if err := checkGroupEvidence(ae.Source); err != nil {
				return err
			}
		}
	}

	// NotVerified must name every operator-disabled phase: without this
	// cross-check, per-case rows could say Disabled while the top-level
	// claim of coverage loss was truncated away, and the two would
	// silently disagree.
	for i := range r.Cases {
		for _, po := range r.Cases[i].Phases {
			if po.Status != Disabled {
				continue
			}
			named := false
			// Match the quoted id: the emitted line writes %q, so the
			// closing quote anchors the match — "settle_wait" must not
			// satisfy a line about "settle_wait_extended". A bare substring
			// match would.
			quoted := fmt.Sprintf("%q", string(po.ID))
			for _, line := range r.NotVerified {
				if strings.Contains(line, quoted) {
					named = true
					break
				}
			}
			if !named {
				return &FrameworkError{Invariant: "NotVerified names disabled phases",
					Detail: fmt.Sprintf("phase %q is Disabled in case %q but absent from not_verified", po.ID, r.Cases[i].CaseID)}
			}
		}
	}

	// A curtailed run must not read as a clean pass, and a Failed case
	// interrupted mid-run must record the curtailment rather than hide it.
	for i := range r.Cases {
		cr := &r.Cases[i]
		if cr.Curtailed && (cr.Status == Passed || cr.Status == NotApplicable) {
			return &FrameworkError{Invariant: "a curtailed run is not a verdict",
				Detail: fmt.Sprintf("case %q is Curtailed but %s", cr.CaseID, cr.Status)}
		}
	}

	if recount != r.Summary {
		return &FrameworkError{Invariant: "summary adds up",
			Detail: fmt.Sprintf("summary %+v does not match recount %+v", r.Summary, recount)}
	}
	return nil
}

// WriteJSON emits the report. Deterministic for a given report: field order
// is fixed by the struct definitions, cases retain declaration order, and no
// maps reach the marshaller.
func (r *Report) WriteJSON(w io.Writer) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	if err := enc.Encode(r); err != nil {
		return fmt.Errorf("report: %w", err)
	}
	return nil
}

// Status marshals as its string form — the one schema-surface spelling a
// tag cannot express. Every other schema struct carries its json tags on
// the real struct (session.go), compiler-linked and guarded by
// TestSchemaSurfaceIsFullyTagged, which refuses any field added without a
// tag.

func (s Status) MarshalJSON() ([]byte, error) { return json.Marshal(s.String()) }
