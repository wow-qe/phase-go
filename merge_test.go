// Copyright 2026 The Phase Contributors
// SPDX-License-Identifier: MIT

package phase

import (
	"strings"
	"testing"
)

// Sharding runs suites across parallel CI jobs and merges the reports.
// A merge must refuse anything that would make the combined report
// misrepresent its inputs: schema mismatch, duplicate case IDs, or a
// shard that does not verify.

func shard(t *testing.T, ids ...string) *Report {
	t.Helper()
	r := mustRunner(t, Config{Defaults: validTiming()}, passingPhase("submit", nil))
	cases := make([]Case, len(ids))
	for i, id := range ids {
		cases[i] = &stubCase{id: id}
	}
	return startSession(t, r, cases...).Report()
}

func TestMergeReportsCombinesShards(t *testing.T) {
	a, b := shard(t, "one", "two"), shard(t, "three")
	m, err := MergeReports(a, b)
	if err != nil {
		t.Fatalf("merge: %v", err)
	}
	if m.Summary.Total != 3 || m.Summary.Passed != 3 {
		t.Fatalf("summary = %+v", m.Summary)
	}
	if got := []string{m.Cases[0].CaseID, m.Cases[1].CaseID, m.Cases[2].CaseID}; got[0] != "one" || got[2] != "three" {
		t.Fatalf("case order = %v, want shard order preserved", got)
	}
	if err := m.Verify(); err != nil {
		t.Fatalf("merged report must verify: %v", err)
	}
	if !strings.Contains(m.Session.ID, a.Session.ID) || !strings.Contains(m.Session.ID, b.Session.ID) {
		t.Fatalf("merged session id %q must name its shards", m.Session.ID)
	}
}

func TestMergeRefusesDuplicateCaseIDs(t *testing.T) {
	if _, err := MergeReports(shard(t, "one"), shard(t, "one")); err == nil {
		t.Fatal("two shards claiming one case ID must refuse — the rows would be indistinguishable")
	}
}

func TestMergeRefusesACorruptShard(t *testing.T) {
	a, b := shard(t, "one"), shard(t, "two")
	b.Summary.Passed = 99
	if _, err := MergeReports(a, b); err == nil {
		t.Fatal("a shard that does not verify must not blend into a trusted merge")
	}
}

func TestMergeRefusesSchemaMismatch(t *testing.T) {
	a, b := shard(t, "one"), shard(t, "two")
	b.Schema = "0"
	if _, err := MergeReports(a, b); err == nil {
		t.Fatal("cross-schema merge must refuse")
	}
}

func TestMergeRefusesNothing(t *testing.T) {
	if _, err := MergeReports(); err == nil {
		t.Fatal("merging zero reports is a merge of nothing — refuse, never fabricate an empty green report")
	}
}
