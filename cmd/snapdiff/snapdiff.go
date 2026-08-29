// Copyright 2026 The Phase Contributors
// SPDX-License-Identifier: MIT

package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

// Snapshot is the normalized projection of a report, keyed on stable fields only.
type Snapshot struct {
	Cases []CaseSnapshot `json:"cases"`
}

type CaseSnapshot struct {
	ID       string          `json:"id"`
	Status   string          `json:"status"`
	FailedIn string          `json:"failed_in,omitempty"`
	Phases   []PhaseSnapshot `json:"phases"`
}

type PhaseSnapshot struct {
	ID      string           `json:"id"`
	Status  string           `json:"status"`
	Results []ResultSnapshot `json:"results"`
}

type ResultSnapshot struct {
	Name        string         `json:"name"`
	Entity      EntitySnapshot `json:"entity"`
	Passed      bool           `json:"passed"`
	Comparisons int            `json:"comparisons"`
}

type EntitySnapshot struct {
	Kind string `json:"kind"`
	ID   string `json:"id"`
}

// rawReport is the structure of the JSON report for reading.
type rawReport struct {
	SchemaVersion string    `json:"schema_version"`
	Cases         []rawCase `json:"cases"`
}

type rawCase struct {
	ID       string      `json:"id"`
	Status   string      `json:"status"`
	FailedIn string      `json:"failed_in"`
	Phases   []rawPhase  `json:"phases"`
	Results  []rawResult `json:"results"`
	Started  string      `json:"started"`
	Finished string      `json:"finished"`
}

type rawPhase struct {
	ID     string `json:"id"`
	Status string `json:"status"`
	Reason string `json:"reason"`
}

type rawResult struct {
	Phase  string        `json:"phase"`
	Result rawResultData `json:"result"`
}

type rawResultData struct {
	Name        string    `json:"name"`
	Entity      rawEntity `json:"entity"`
	Passed      bool      `json:"passed"`
	Reason      string    `json:"reason"`
	Expected    any       `json:"expected"`
	Actual      any       `json:"actual"`
	Comparisons int       `json:"comparisons"`
}

type rawEntity struct {
	Kind string `json:"kind"`
	ID   string `json:"id"`
}

// captureSnapshot reads a report and returns a normalized snapshot.
// Refuses (returns error) if schema_version != "1" or projection is empty.
func captureSnapshot(reportJSON []byte) (*Snapshot, error) {
	// Go's json is last-wins on duplicate keys, so a report with two
	// "cases" blocks pins the second while a reader sees the first. Reject
	// any duplicate object key before trusting the decode.
	if err := refuseDuplicateJSONKeys(json.NewDecoder(bytes.NewReader(reportJSON))); err != nil {
		return nil, err
	}
	var rep rawReport
	if err := json.Unmarshal(reportJSON, &rep); err != nil {
		return nil, fmt.Errorf("invalid report JSON: %w", err)
	}

	if rep.SchemaVersion != "1" {
		return nil, fmt.Errorf("schema_version mismatch: report has %q, want %q", rep.SchemaVersion, "1")
	}

	snap := &Snapshot{}

	// Process each case and build snapshot
	for _, rc := range rep.Cases {
		// Build a map of phase ID -> phase results for this case
		phaseResults := make(map[string][]ResultSnapshot)

		// Extract results by phase
		for _, rr := range rc.Results {
			entity := EntitySnapshot{
				Kind: rr.Result.Entity.Kind,
				ID:   rr.Result.Entity.ID,
			}
			if entity.Kind == "" {
				entity.Kind = "entity"
			}
			result := ResultSnapshot{
				Name:        rr.Result.Name,
				Entity:      entity,
				Passed:      rr.Result.Passed,
				Comparisons: rr.Result.Comparisons,
			}
			phaseResults[rr.Phase] = append(phaseResults[rr.Phase], result)
		}

		// Build phases, extracting statuses from raw phases
		phaseStatuses := make(map[string]string)
		for _, rp := range rc.Phases {
			phaseStatuses[rp.ID] = rp.Status
		}

		// Create phase snapshots in the order they appear in the report
		var phases []PhaseSnapshot
		seenPhases := make(map[string]bool)
		for _, rp := range rc.Phases {
			if seenPhases[rp.ID] {
				continue // Skip duplicates
			}
			seenPhases[rp.ID] = true
			phases = append(phases, PhaseSnapshot{
				ID:      rp.ID,
				Status:  rp.Status,
				Results: phaseResults[rp.ID], // may be nil, converted to [] by JSON
			})
		}

		caseSnap := CaseSnapshot{
			ID:     rc.ID,
			Status: rc.Status,
			Phases: phases,
		}
		if rc.FailedIn != "" {
			caseSnap.FailedIn = rc.FailedIn
		}

		snap.Cases = append(snap.Cases, caseSnap)
	}

	// Check that projection is not empty
	if len(snap.Cases) == 0 || !hasAnyResults(snap) {
		return nil, fmt.Errorf("refusing to pin a snapshot that asserts nothing")
	}

	return snap, nil
}

// hasAnyResults checks if the snapshot has any results across all cases/phases.
func hasAnyResults(snap *Snapshot) bool {
	for _, cs := range snap.Cases {
		for _, ps := range cs.Phases {
			if len(ps.Results) > 0 {
				return true
			}
		}
	}
	return false
}

// compareSnapshots compares a current report against a snapshot.
// Returns a list of differences (each as a formatted line).
func compareSnapshots(reportJSON []byte, snapshotJSON []byte) ([][]byte, error) {
	// Parse current report
	current, err := captureSnapshot(reportJSON)
	if err != nil {
		return nil, err
	}

	// Parse expected snapshot. The baseline
	// is untrusted input just like the report - a poisoned dup-key baseline
	// whose hidden last-wins block matches a failing report makes compare
	// report clean on a genuine regression. Guard it the same way.
	if err := refuseDuplicateJSONKeys(json.NewDecoder(bytes.NewReader(snapshotJSON))); err != nil {
		return nil, fmt.Errorf("baseline snapshot: %w", err)
	}
	var expected Snapshot
	if err := json.Unmarshal(snapshotJSON, &expected); err != nil {
		return nil, fmt.Errorf("invalid snapshot JSON: %w", err)
	}

	// Compare case by case
	var diffs [][]byte
	caseMap := make(map[string]*CaseSnapshot)
	for i := range expected.Cases {
		caseMap[expected.Cases[i].ID] = &expected.Cases[i]
	}

	// Expected cases already encountered
	seenCases := make(map[string]bool)

	// Process current cases
	for _, currentCase := range current.Cases {
		seenCases[currentCase.ID] = true

		expectedCase, exists := caseMap[currentCase.ID]
		if !exists {
			// New case in current report
			for _, phase := range currentCase.Phases {
				for _, result := range phase.Results {
					diffs = append(diffs, formatDiff("ADDED", currentCase.ID, phase.ID, result.Name, result.Entity, result.Passed))
				}
			}
			// A new case has no prior status; the branch is kept for completeness
			continue
		}

		// Case exists; compare status and results
		if currentCase.Status != expectedCase.Status {
			diffs = append(diffs, []byte(fmt.Sprintf("case %q status changed: %s -> %s", currentCase.ID, expectedCase.Status, currentCase.Status)))
		}
		if currentCase.FailedIn != expectedCase.FailedIn {
			if currentCase.FailedIn == "" && expectedCase.FailedIn != "" {
				diffs = append(diffs, []byte(fmt.Sprintf("case %q no longer failing in any phase", currentCase.ID)))
			} else if currentCase.FailedIn != "" && expectedCase.FailedIn == "" {
				diffs = append(diffs, []byte(fmt.Sprintf("case %q now failing in %q", currentCase.ID, currentCase.FailedIn)))
			} else {
				diffs = append(diffs, []byte(fmt.Sprintf("case %q failed_in changed: %s -> %s", currentCase.ID, expectedCase.FailedIn, currentCase.FailedIn)))
			}
		}

		// Compare phases and results within this case
		phaseDiffs := comparePhases(currentCase.ID, currentCase.Phases, expectedCase.Phases)
		diffs = append(diffs, phaseDiffs...)
	}

	// Check for removed cases
	for _, expectedCase := range expected.Cases {
		if !seenCases[expectedCase.ID] {
			for _, phase := range expectedCase.Phases {
				for _, result := range phase.Results {
					diffs = append(diffs, formatDiff("REMOVED", expectedCase.ID, phase.ID, result.Name, result.Entity, result.Passed))
				}
			}
		}
	}

	return diffs, nil
}

// comparePhases compares phases within a case, aligned by phase ID.
func comparePhases(caseID string, currentPhases []PhaseSnapshot, expectedPhases []PhaseSnapshot) [][]byte {
	var diffs [][]byte

	phaseMap := make(map[string]*PhaseSnapshot)
	for i := range expectedPhases {
		phaseMap[expectedPhases[i].ID] = &expectedPhases[i]
	}

	seenPhases := make(map[string]bool)

	// Process current phases
	for _, currentPhase := range currentPhases {
		seenPhases[currentPhase.ID] = true

		expectedPhase, exists := phaseMap[currentPhase.ID]
		if !exists {
			// New phase; all its results are added
			for _, result := range currentPhase.Results {
				diffs = append(diffs, formatDiff("ADDED", caseID, currentPhase.ID, result.Name, result.Entity, result.Passed))
			}
			continue
		}

		// Phase exists; compare status and results
		if currentPhase.Status != expectedPhase.Status {
			diffs = append(diffs, []byte(fmt.Sprintf("%s/%s status changed: %s -> %s", caseID, currentPhase.ID, expectedPhase.Status, currentPhase.Status)))
		}

		// Compare results by (name, entity) key
		resultDiffs := compareResults(caseID, currentPhase.ID, currentPhase.Results, expectedPhase.Results)
		diffs = append(diffs, resultDiffs...)
	}

	// Check for removed phases
	for _, expectedPhase := range expectedPhases {
		if !seenPhases[expectedPhase.ID] {
			for _, result := range expectedPhase.Results {
				diffs = append(diffs, formatDiff("REMOVED", caseID, expectedPhase.ID, result.Name, result.Entity, result.Passed))
			}
		}
	}

	return diffs
}

// resultKey uniquely identifies a result by (name, entity.kind, entity.id).
type resultKey struct {
	name       string
	entityKind string
	entityID   string
}

func (rs ResultSnapshot) key() resultKey {
	return resultKey{rs.Name, rs.Entity.Kind, rs.Entity.ID}
}

// compareResults aligns results by (name, entity) key and compares passed.
func compareResults(caseID, phaseID string, currentResults []ResultSnapshot, expectedResults []ResultSnapshot) [][]byte {
	var diffs [][]byte

	resultMap := make(map[resultKey]*ResultSnapshot)
	for i := range expectedResults {
		resultMap[expectedResults[i].key()] = &expectedResults[i]
	}

	seenKeys := make(map[resultKey]bool)

	// Process current results
	for _, currentResult := range currentResults {
		key := currentResult.key()
		seenKeys[key] = true

		expectedResult, exists := resultMap[key]
		if !exists {
			// New result
			diffs = append(diffs, formatDiff("ADDED", caseID, phaseID, currentResult.Name, currentResult.Entity, currentResult.Passed))
			continue
		}

		// Result exists; compare passed AND comparison count (a check that
		// silently drops assertions keeps the same name/entity/passed).
		if currentResult.Passed != expectedResult.Passed || currentResult.Comparisons != expectedResult.Comparisons {
			diffs = append(diffs, formatDiff("CHANGED", caseID, phaseID, currentResult.Name, currentResult.Entity, currentResult.Passed))
		}
	}

	// Check for removed results
	for _, expectedResult := range expectedResults {
		if !seenKeys[expectedResult.key()] {
			diffs = append(diffs, formatDiff("REMOVED", caseID, phaseID, expectedResult.Name, expectedResult.Entity, expectedResult.Passed))
		}
	}

	return diffs
}

// sanitize strips control bytes (newlines, ANSI escapes, NUL, CR) from any
// report-derived string before it reaches stdout. An attacker-controlled
// result name could otherwise forge fake, colored diff lines in CI logs.
func sanitize(s string) string {
	return strings.Map(func(r rune) rune {
		if r == '\t' {
			return ' '
		}
		if r < 0x20 || r == 0x7f {
			return '?'
		}
		return r
	}, s)
}

// formatDiff formats a single difference line. Every report-derived field is
// sanitized; the fixed op/passed tokens are trusted.
func formatDiff(op, caseID, phaseID, resultName string, entity EntitySnapshot, passed bool) []byte {
	return []byte(fmt.Sprintf("%s %s/%s/%s [%s:%s] passed=%v", op,
		sanitize(caseID), sanitize(phaseID), sanitize(resultName),
		sanitize(entity.Kind), sanitize(entity.ID), passed))
}

// summarizeDiff formats the final summary message.
// Shows up to 40 differences, then "... and K more".
func summarizeDiff(diffs [][]byte, entryCount int) string {
	if len(diffs) == 0 {
		return fmt.Sprintf("snapshot clean (%d entries)", entryCount)
	}

	var buf bytes.Buffer
	limit := 40
	if len(diffs) > limit {
		for i := 0; i < limit; i++ {
			buf.Write(diffs[i])
			buf.WriteByte('\n')
		}
		fmt.Fprintf(&buf, "... and %d more", len(diffs)-limit)
	} else {
		for i, diff := range diffs {
			buf.Write(diff)
			if i < len(diffs)-1 {
				buf.WriteByte('\n')
			}
		}
	}
	return buf.String()
}

// refuseDuplicateJSONKeys walks the token stream and errors on any object with
// a repeated key, at every nesting level. It uses an explicit frame stack: an
// object frame alternates key/value, an array frame does not, so a string
// value equal to a key name is never mistaken for a duplicate key.
func refuseDuplicateJSONKeys(dec *json.Decoder) error {
	type frame struct {
		isObject bool
		wantKey  bool // object frames only: is the next string a key?
		seen     map[string]bool
	}
	var stack []*frame
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return fmt.Errorf("invalid report JSON: %w", err)
		}
		// A string that is a key must be checked before the frame's wantKey
		// flips; a value or non-string just advances the toggle.
		if s, ok := tok.(string); ok && len(stack) > 0 {
			top := stack[len(stack)-1]
			if top.isObject && top.wantKey {
				if top.seen[s] {
					return fmt.Errorf("duplicate JSON key %q: a report that says two different things about one key cannot be trusted as a baseline", s)
				}
				top.seen[s] = true
			}
		}
		switch t := tok.(type) {
		case json.Delim:
			switch t {
			case '{':
				stack = append(stack, &frame{isObject: true, wantKey: true, seen: map[string]bool{}})
				continue
			case '[':
				stack = append(stack, &frame{isObject: false})
				continue
			case '}', ']':
				stack = stack[:len(stack)-1]
			}
		}
		// After any completed value (scalar or a closed composite), an object
		// frame toggles back toward expecting a key.
		if len(stack) > 0 {
			top := stack[len(stack)-1]
			if top.isObject {
				top.wantKey = !top.wantKey
			}
		}
	}
}
