// Copyright 2026 The Phase Contributors
// SPDX-License-Identifier: MIT

package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func readFixture(t *testing.T, name string) []byte {
	path := filepath.Join("testdata", name)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("failed to read fixture %s: %v", path, err)
	}
	return data
}

func TestCaptureBadSchema(t *testing.T) {
	report := readFixture(t, "wrong_schema.json")
	_, err := captureSnapshot(report)
	if err == nil {
		t.Fatal("expected error for schema_version != 1, got nil")
	}
	if err.Error() != "schema_version mismatch: report has \"2\", want \"1\"" {
		t.Errorf("wrong error message: %v", err)
	}
}

func TestCaptureEmptyProjection(t *testing.T) {
	report := readFixture(t, "empty.json")
	_, err := captureSnapshot(report)
	if err == nil {
		t.Fatal("expected error for empty projection, got nil")
	}
	if err.Error() != "refusing to pin a snapshot that asserts nothing" {
		t.Errorf("wrong error message: %v", err)
	}
}

func TestCaptureSuccess(t *testing.T) {
	report := readFixture(t, "basic.json")
	snap, err := captureSnapshot(report)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Validate the snapshot structure
	if snap.Cases == nil || len(snap.Cases) != 2 {
		t.Fatalf("expected 2 cases, got %d", len(snap.Cases))
	}

	// Check first case
	if snap.Cases[0].ID != "case_one" {
		t.Errorf("first case ID: got %q, want case_one", snap.Cases[0].ID)
	}
	if snap.Cases[0].Status != "passed" {
		t.Errorf("first case status: got %q, want passed", snap.Cases[0].Status)
	}
	if len(snap.Cases[0].Phases) != 1 {
		t.Fatalf("first case expected 1 phase, got %d", len(snap.Cases[0].Phases))
	}
	if len(snap.Cases[0].Phases[0].Results) != 1 {
		t.Fatalf("first phase expected 1 result, got %d", len(snap.Cases[0].Phases[0].Results))
	}
	if snap.Cases[0].Phases[0].Results[0].Name != "check_one" {
		t.Errorf("result name: got %q, want check_one", snap.Cases[0].Phases[0].Results[0].Name)
	}
	if !snap.Cases[0].Phases[0].Results[0].Passed {
		t.Errorf("first result should pass")
	}

	// Check second case
	if snap.Cases[1].Status != "failed" {
		t.Errorf("second case status: got %q, want failed", snap.Cases[1].Status)
	}
	if snap.Cases[1].FailedIn != "phase_b" {
		t.Errorf("second case failed_in: got %q, want phase_b", snap.Cases[1].FailedIn)
	}
	if snap.Cases[1].Phases[0].Results[0].Passed {
		t.Errorf("second result should fail but is passed")
	}
}

func TestCaptureJSON(t *testing.T) {
	report := readFixture(t, "basic.json")
	snap, _ := captureSnapshot(report)
	data, err := json.MarshalIndent(snap, "", "  ")
	if err != nil {
		t.Fatalf("failed to marshal: %v", err)
	}
	if bytes.Count(data, []byte("\n")) < 5 {
		t.Errorf("snapshot JSON too short, got %d lines", bytes.Count(data, []byte("\n")))
	}
}

func TestCompareBadSchema(t *testing.T) {
	report := readFixture(t, "wrong_schema.json")
	snapshot := readFixture(t, "basic.snapshot.json")
	_, err := compareSnapshots(report, snapshot)
	if err == nil {
		t.Fatal("expected error for schema_version != 1")
	}
}

func TestCompareEmptyProjection(t *testing.T) {
	report := readFixture(t, "empty.json")
	snapshot := readFixture(t, "basic.snapshot.json")
	_, err := compareSnapshots(report, snapshot)
	if err == nil {
		t.Fatal("expected error for empty projection")
	}
}

func TestCompareClean(t *testing.T) {
	report := readFixture(t, "basic.json")
	snapshot := readFixture(t, "basic.snapshot.json")
	diffs, err := compareSnapshots(report, snapshot)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(diffs) != 0 {
		t.Fatalf("expected clean comparison, got %d differences", len(diffs))
	}
}

func TestCompareFlipped(t *testing.T) {
	report := readFixture(t, "flipped.json")
	snapshot := readFixture(t, "basic.snapshot.json")
	diffs, err := compareSnapshots(report, snapshot)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(diffs) == 0 {
		t.Fatal("expected at least one difference for flipped result")
	}
	// Should report the specific change in case_one/phase_a/check_one
	found := false
	for _, diff := range diffs {
		if bytes.Contains(diff, []byte("case_one")) && bytes.Contains(diff, []byte("check_one")) {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected difference mentioning case_one/check_one, got: %v", diffs)
	}
}

func TestCompareRemoved(t *testing.T) {
	report := readFixture(t, "removed.json")
	snapshot := readFixture(t, "basic.snapshot.json")
	diffs, err := compareSnapshots(report, snapshot)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(diffs) == 0 {
		t.Fatal("expected at least one difference for removed result")
	}
	// Should report REMOVED for check_one
	found := false
	for _, diff := range diffs {
		if bytes.Contains(diff, []byte("REMOVED")) && bytes.Contains(diff, []byte("check_one")) {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected REMOVED difference for check_one")
	}
}

func TestCompareNewCase(t *testing.T) {
	report := readFixture(t, "basic.json")
	// Create snapshot with only one case
	snap := Snapshot{
		Cases: []CaseSnapshot{
			{
				ID:     "case_one",
				Status: "passed",
				Phases: []PhaseSnapshot{
					{
						ID:     "phase_a",
						Status: "passed",
						Results: []ResultSnapshot{
							{
								Name:   "check_one",
								Entity: EntitySnapshot{Kind: "entity", ID: "ent_1"},
								Passed: true,
							},
						},
					},
				},
			},
		},
	}
	snapshotData, _ := json.Marshal(snap)
	diffs, err := compareSnapshots(report, snapshotData)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(diffs) == 0 {
		t.Fatal("expected at least one difference for new case")
	}
	// Should report ADDED for case_two results
	found := false
	for _, diff := range diffs {
		if bytes.Contains(diff, []byte("ADDED")) && bytes.Contains(diff, []byte("case_two")) {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected ADDED difference for case_two")
	}
}

func TestSummarizeClean(t *testing.T) {
	msg := summarizeDiff(nil, 2)
	if msg != "snapshot clean (2 entries)" {
		t.Errorf("clean summary: got %q, want 'snapshot clean (2 entries)'", msg)
	}
}

func TestSummarizeDifferences(t *testing.T) {
	diffs := make([][]byte, 3)
	for i := range diffs {
		diffs[i] = []byte("line " + string(rune('0'+i)))
	}
	msg := summarizeDiff(diffs, 2)
	if msg == "snapshot clean (2 entries)" {
		t.Errorf("should not be clean with differences")
	}
	if bytes.Count([]byte(msg), []byte("\n")) != 2 {
		t.Errorf("expected 2 difference lines in output")
	}
}

func TestSummarizeManyDifferences(t *testing.T) {
	diffs := make([][]byte, 45)
	for i := range diffs {
		diffs[i] = []byte("line " + string(rune('0'+(i%10))))
	}
	msg := summarizeDiff(diffs, 10)
	if !bytes.Contains([]byte(msg), []byte("... and 5 more")) {
		t.Errorf("expected truncation message, got: %s", msg)
	}
}

func TestControlBytesInNamesAreSanitized(t *testing.T) {
	// Result names/case IDs flow raw into stdout via
	// formatDiff. A newline+ANSI name forges fake diff lines in CI logs.
	line := formatDiff("CHANGED", "case-1\n\x1b[32mFAKE clean", "verify", "check\x1b[0m", EntitySnapshot{}, false)
	for _, b := range line {
		if b == '\n' || b == '\x1b' || b == '\r' {
			t.Fatalf("control byte 0x%02x survived into a diff line: %q", b, line)
		}
	}
}

func TestDuplicateJSONKeysRefusedOnCapture(t *testing.T) {
	// Go's last-wins lets a report's second "cases"
	// block override what a reader sees first, poisoning the baseline.
	dup := []byte(`{"schema_version":"1","cases":[{"id":"a","status":"passed","phases":[],"results":[]}],"cases":[{"id":"a","status":"failed","phases":[],"results":[]}],"summary":{},"not_verified":[]}`)
	if _, err := captureSnapshot(dup); err == nil {
		t.Fatal("a report with duplicate top-level keys must be refused, not last-wins-accepted")
	}
}

func TestValueEqualToKeyIsNotADuplicate(t *testing.T) {
	// Guard against a toggle bug: a string
	// VALUE equal to a key name must not read as a duplicate key.
	ok := []byte(`{"schema_version":"1","cases":[{"id":"status","status":"status","phases":[{"id":"p","status":"passed"}],"results":[{"phase":"p","result":{"name":"status","passed":true,"comparisons":1}}]}],"summary":{},"not_verified":[]}`)
	if _, err := captureSnapshot(ok); err != nil {
		t.Fatalf("legitimate report false-rejected: %v", err)
	}
}

func TestWeakenedAssertionIsDrift(t *testing.T) {
	// A check that drops from 3 comparisons to 1 —
	// two assertions deleted — has the same name/entity/passed and would
	// compare "snapshot clean". The snapshot must key on comparisons too.
	strong := []byte(`{"schema_version":"1","cases":[{"id":"c","status":"passed","phases":[{"id":"p","status":"passed"}],"results":[{"phase":"p","result":{"name":"check","passed":true,"comparisons":3}}]}],"summary":{},"not_verified":[]}`)
	weak := []byte(`{"schema_version":"1","cases":[{"id":"c","status":"passed","phases":[{"id":"p","status":"passed"}],"results":[{"phase":"p","result":{"name":"check","passed":true,"comparisons":1}}]}],"summary":{},"not_verified":[]}`)
	snap, err := captureSnapshot(strong)
	if err != nil {
		t.Fatal(err)
	}
	snapJSON, err := json.MarshalIndent(snap, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	diffs, err := compareSnapshots(weak, snapJSON)
	if err != nil {
		t.Fatal(err)
	}
	if len(diffs) == 0 {
		t.Fatal("a check that lost 2 of 3 comparisons compared clean — the snapshot is blind to weakened assertions")
	}
}

func TestPoisonedBaselineIsRefusedOnCompare(t *testing.T) {
	// Compare's baseline is untrusted too.
	report := []byte(`{"schema_version":"1","cases":[{"id":"c","status":"failed","phases":[{"id":"p","status":"failed"}],"results":[{"phase":"p","result":{"name":"x","passed":false,"comparisons":1}}]}],"summary":{},"not_verified":[]}`)
	poisoned := []byte(`{"results":[{"case_id":"c","phase":"p","name":"x","passed":true}],"results":[{"case_id":"c","phase":"p","name":"x","passed":false}]}`)
	if _, err := compareSnapshots(report, poisoned); err == nil {
		t.Fatal("a dup-key baseline must be refused, not last-wins-trusted into swallowing a regression")
	}
}
