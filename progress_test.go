// Copyright 2026 The Phase Contributors
// SPDX-License-Identifier: MIT

package phase

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/wow-qe/phase-go/result"
)

// A 20×15s settle used to be silent for five minutes — indistinguishable
// from a hang. Progress events fire around each phase, on the Start
// goroutine, so an operator watching stderr sees phases begin and land.

func TestProgressEventsFireAroundEachPhase(t *testing.T) {
	var seen []string
	r := mustRunner(t, Config{Defaults: validTiming()},
		passingPhase("submit", nil),
		&stubPhase{id: "provider", deps: []ID{"submit"}, applies: Skip("not this case")},
	)
	WithProgress(func(ev ProgressEvent) {
		e := fmt.Sprintf("%s/%s/%s", ev.CaseID, ev.Phase, ev.Stage)
		if ev.Stage == "finished" {
			e += "/" + ev.Status.String()
		}
		seen = append(seen, e)
	})(r)
	startSession(t, r, &stubCase{id: "one"})
	want := []string{
		"one/submit/started",
		"one/submit/finished/passed",
		// a skip is a landing too — a silent skip would read as a hang
		"one/provider/finished/not_applicable",
	}
	if fmt.Sprint(seen) != fmt.Sprint(want) {
		t.Fatalf("events = %v, want %v", seen, want)
	}
}

func TestLogProgressWritesHumanReadableLines(t *testing.T) {
	var buf bytes.Buffer
	r := mustRunner(t, Config{Defaults: validTiming()}, passingPhase("submit", nil))
	WithProgress(LogProgress(&buf))(r)
	startSession(t, r, &stubCase{id: "one"})
	out := buf.String()
	for _, want := range []string{"one", "submit", "started", "passed"} {
		if !strings.Contains(out, want) {
			t.Fatalf("progress log %q missing %q", out, want)
		}
	}
}

// Correlation and transcript. Scope.Correlation is the thread a reader
// pulls to join the report with the system's own logs — it must reach the
// artifact. And Transcribe is the sanctioned way to keep an adapter
// exchange as evidence, structured, not fmt.Sprintf'd into prose.

func TestCorrelationReachesTheReportArtifact(t *testing.T) {
	r := mustRunner(t, Config{Defaults: validTiming()}, passingPhase("submit", nil))
	s := startSession(t, r, &stubCase{id: "one"})
	cr := caseReport(t, s, "one")
	if cr.Correlation == "" {
		t.Fatal("Scope.Correlation must reach the CaseReport")
	}
	var buf bytes.Buffer
	if err := s.Report().WriteJSON(&buf); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), `"correlation"`) {
		t.Fatal("correlation missing from the JSON artifact")
	}
}

func TestTranscribeKeepsTheExchangeStructured(t *testing.T) {
	r := mustRunner(t, Config{Defaults: validTiming()},
		&recordingPhase{stubPhase: stubPhase{id: "submit"}, do: func(_ context.Context, run *Run) error {
			run.Transcribe("POST /provision", map[string]any{"count": 2}, map[string]any{"accepted": true})
			run.Record(result.Compared("accepted", []bool{true}))
			return nil
		}},
	)
	cr := caseReport(t, startSession(t, r, &stubCase{id: "one"}), "one")
	if len(cr.Observations) != 1 {
		t.Fatalf("observations = %v", cr.Observations)
	}
	ob := cr.Observations[0]
	if !strings.Contains(ob.Name, "POST /provision") {
		t.Fatalf("transcript name = %q", ob.Name)
	}
	entry, ok := ob.Value.(TranscriptEntry)
	if !ok {
		t.Fatalf("value = %T, want TranscriptEntry — structured, not prose", ob.Value)
	}
	if entry.Op != "POST /provision" || entry.Request == nil || entry.Response == nil {
		t.Fatalf("entry = %+v", entry)
	}
}
