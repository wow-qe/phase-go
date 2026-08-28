// Copyright 2026 The Phase Contributors
// SPDX-License-Identifier: MIT

package phase

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/wow-qe/phase-go/result"
)

// All evidence was held in memory for the whole session, unbounded —
// 1.8 GiB peak at 1k cases × 30 phases × 5 obs × 100 rows, an OOM wall in a
// 2Gi CI container. Two reliefs, both explicit: a per-case observation cap
// whose truncation is LOUD (a silent cap is a default, and defaults are how
// evidence disappears), and a case observer that streams each finished
// CaseReport so a consumer can write-and-drop instead of retaining.

func observantPhase(id ID, n int) Interface {
	return &recordingPhase{stubPhase: stubPhase{id: id}, do: func(_ context.Context, run *Run) error {
		for i := 0; i < n; i++ {
			run.Observe(fmt.Sprintf("row %d", i), i)
		}
		run.Record(result.Compared("saw rows", []bool{true}))
		return nil
	}}
}

func TestObservationCapTruncatesLoudly(t *testing.T) {
	r := mustRunner(t, Config{Defaults: validTiming(), MaxObservationsPerCase: 3},
		observantPhase("collect", 10),
	)
	cr := caseReport(t, startSession(t, r, &stubCase{id: "chatty"}), "chatty")
	// 3 kept + exactly one truncation marker naming the loss.
	if len(cr.Observations) != 4 {
		t.Fatalf("observations = %d, want 3 kept + 1 marker", len(cr.Observations))
	}
	marker := cr.Observations[3]
	if !strings.Contains(marker.Name, "retention") {
		t.Fatalf("marker = %+v, want it named as a retention truncation", marker)
	}
	if !strings.Contains(fmt.Sprint(marker.Value), "7") {
		t.Fatalf("marker value = %v, want the dropped count (7)", marker.Value)
	}
	if cr.Status != Passed {
		t.Fatalf("status = %s — truncation is not a failure", cr.Status)
	}
}

func TestNoObservationCapByDefault(t *testing.T) {
	r := mustRunner(t, Config{Defaults: validTiming()}, observantPhase("collect", 10))
	cr := caseReport(t, startSession(t, r, &stubCase{id: "chatty"}), "chatty")
	if len(cr.Observations) != 10 {
		t.Fatalf("observations = %d, want all 10 — zero means unlimited, not zero", len(cr.Observations))
	}
}

func TestCaseObserverStreamsEachFinishedCase(t *testing.T) {
	var streamed []string
	r := mustRunner(t, Config{Defaults: validTiming()}, observantPhase("collect", 2))
	WithCaseObserver(func(cr CaseReport) { streamed = append(streamed, cr.CaseID+":"+cr.Status.String()) })(r)
	s := startSession(t, r, &stubCase{id: "one"}, &stubCase{id: "two"})
	if len(s.Cases()) != 2 {
		t.Fatalf("session cases = %d", len(s.Cases()))
	}
	want := []string{"one:passed", "two:passed"}
	if len(streamed) != 2 || streamed[0] != want[0] || streamed[1] != want[1] {
		t.Fatalf("streamed = %v, want %v — each case delivered as it finishes, in order", streamed, want)
	}
}

// TransitiveDeps was a fresh DFS per phase per case, unconditionally —
// O(cases × phases²) on chains. The reach sets are now computed once at
// NewRunner; this pins that the memoized sets match the graph.
func TestReachSetsMatchTheGraph(t *testing.T) {
	r := mustRunner(t, Config{Defaults: validTiming()},
		&stubPhase{id: "a"},
		&stubPhase{id: "b", deps: []ID{"a"}},
		&stubPhase{id: "c", deps: []ID{"a"}},
		&stubPhase{id: "d", deps: []ID{"b", "c"}},
	)
	want := map[ID]string{"a": "", "b": "a", "c": "a", "d": "a,b,c"}
	for id, expect := range want {
		reach := r.transitiveDeps(id)
		var got []ID
		for k := range reach {
			got = append(got, k)
		}
		sortIDs(got)
		parts := make([]string, len(got))
		for i, g := range got {
			parts[i] = string(g)
		}
		if joined := strings.Join(parts, ","); joined != expect {
			t.Fatalf("reach(%s) = %q, want %q", id, joined, expect)
		}
	}
}

func TestPruningReasonIsDeterministicWithManyErroredDeps(t *testing.T) {
	// firstErroredDep iterates the errored set now; map order is
	// random, so the pick must tie-break deterministically or the recorded
	// pruning reason would differ run to run (invariant 6).
	r := mustRunner(t, Config{Defaults: validTiming()},
		&stubPhase{id: "a"}, &stubPhase{id: "b"},
		&stubPhase{id: "c", deps: []ID{"a", "b"}},
	)
	errored := map[ID]bool{"a": true, "b": true}
	for i := 0; i < 50; i++ {
		dep, hit := r.firstErroredDep("c", errored)
		if !hit || dep != "a" {
			t.Fatalf("iteration %d: dep = %q, want deterministic %q", i, dep, "a")
		}
	}
}
