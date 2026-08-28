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

// Evidence is attributed to the phase whose Run handle recorded it, not to
// whichever phase happens to be "current" at record time. Each phase records
// through its own bound view, so a goroutine, a stashed *Run, or a future
// parallel scheduler cannot misattribute evidence by outliving its phase:
// where the evidence came from is a property of the handle, not the clock.
// Recording through a handle after its case has produced a verdict panics
// instead of silently dropping the evidence.

func TestEvidenceIsAttributedToTheRecordingPhaseNotTheCurrentOne(t *testing.T) {
	var fromA *Run
	r := mustRunner(t, Config{Defaults: validTiming()},
		&recordingPhase{stubPhase: stubPhase{id: "a"}, do: func(_ context.Context, run *Run) error {
			fromA = run // outlives the phase; recording still binds to it
			run.Record(result.Compared("a's own check", []bool{true}))
			return nil
		}},
		&recordingPhase{stubPhase: stubPhase{id: "b", deps: []ID{"a"}}, do: func(_ context.Context, run *Run) error {
			// Records through A's stashed handle while B is "current".
			fromA.Record(result.Compared("late evidence from a", []bool{true}))
			run.Record(result.Compared("b's own check", []bool{true}))
			return nil
		}},
	)
	cr := caseReport(t, startSession(t, r, &stubCase{id: "interleaved"}), "interleaved")
	byName := map[string]ID{}
	for _, ar := range cr.Results {
		byName[ar.Result.Name] = ar.Phase
	}
	if got := byName["late evidence from a"]; got != "a" {
		t.Fatalf("late evidence attributed to %q, want %q — attribution must ride the handle, not the current-phase clock", got, "a")
	}
	if got := byName["b's own check"]; got != "b" {
		t.Fatalf("b's evidence attributed to %q", got)
	}
	// Count integrity under cross-handle recording: the late result belongs
	// to a's ledger, so neither phase outcome may double- or zero-count it.
	if po := phaseOutcome(t, cr, "b"); po.Results != 1 {
		t.Fatalf("b recorded 1 result via its own handle; outcome says %d", po.Results)
	}
}

func TestRecordingAfterTheCaseCompletesPanicsLoudly(t *testing.T) {
	// Evidence recorded after a case's verdict has been derived must not
	// vanish into a buffer nobody would ever read. Mirroring testing.T's
	// choice for Log-after-test, a loud panic beats a silently incomplete
	// report.
	var escaped *Run
	r := mustRunner(t, Config{Defaults: validTiming()},
		&recordingPhase{stubPhase: stubPhase{id: "a"}, do: func(_ context.Context, run *Run) error {
			escaped = run
			run.Record(result.Compared("in time", []bool{true}))
			return nil
		}},
	)
	startSession(t, r, &stubCase{id: "done"})
	defer func() {
		v := recover()
		if v == nil {
			t.Fatal("recording into a completed case must panic, not silently drop the evidence")
		}
		if !strings.Contains(fmt.Sprint(v), "completed") {
			t.Fatalf("panic %v, want it to name the completed case", v)
		}
	}()
	escaped.Record(result.Compared("too late", []bool{true}))
}
