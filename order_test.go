// Copyright 2026 The Phase Contributors
// SPDX-License-Identifier: MIT

package phase

import (
	"context"
	"strings"
	"testing"

	"github.com/wow-qe/phase-go/result"
)

// Evidence renders in
// deterministic TOPOLOGICAL order, not completion order. Sequentially the
// two coincide phase-by-phase; the gap A1 left open is evidence recorded
// through a stashed handle while a LATER phase runs — append order put it
// under the later phase's chronology, and under real concurrency append
// order is lock-acquisition order, which silently breaks invariant 6.

func TestEvidenceRendersInTopologicalOrderNotCompletionOrder(t *testing.T) {
	var fromA *Run
	r := mustRunner(t, Config{Defaults: validTiming()},
		&recordingPhase{stubPhase: stubPhase{id: "a"}, do: func(_ context.Context, run *Run) error {
			fromA = run
			run.Record(result.Compared("a first", []bool{true}))
			return nil
		}},
		&recordingPhase{stubPhase: stubPhase{id: "b", deps: []ID{"a"}}, do: func(_ context.Context, run *Run) error {
			run.Record(result.Compared("b own", []bool{true}))
			fromA.Record(result.Compared("a late", []bool{true})) // completion order says after b; topology says with a
			return nil
		}},
	)
	cr := caseReport(t, startSession(t, r, &stubCase{id: "ordered"}), "ordered")
	var names []string
	for _, ar := range cr.Results {
		names = append(names, ar.Result.Name)
	}
	want := "a first,a late,b own"
	if got := strings.Join(names, ","); got != want {
		t.Fatalf("evidence order = %q, want %q — rank sorts, stably, with within-phase call order preserved", got, want)
	}
}

func TestGroupLifecycleEvidenceSortsWithItsPosition(t *testing.T) {
	var j []string
	r := groupRunner(t, &j,
		Group{ID: "g", Members: []ID{"m"},
			Lifecycle: &groupFixture{name: "g", journal: &j, onSetup: func(run *Run) {
				run.Record(result.Compared("setup check", []bool{true}))
			}}},
		journalPhase("pre", &j),
		journalPhase("m", &j, "pre"),
		journalPhase("post", &j, "m"),
	)
	cr := caseReport(t, startSession(t, r, &stubCase{id: "one"}), "one")
	var names []string
	for _, ar := range cr.Results {
		names = append(names, ar.Result.Name)
	}
	// setup's evidence sorts at the synthetic node's own topological
	// position - causally before its member's evidence, wherever unrelated
	// phases fall.
	idx := map[string]int{}
	for i, n := range names {
		idx[n] = i
	}
	if idx["setup check"] >= idx["m ok"] {
		t.Fatalf("evidence order = %v — group setup evidence must precede its member's", names)
	}
}
