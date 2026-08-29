// Copyright 2026 The Phase Contributors
// SPDX-License-Identifier: MIT

package phase

import (
	"bytes"
	"context"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/wow-qe/phase-go/result"
)

// Ownership at the public boundaries: values handed to consumers are
// detached, and values received from consumers are snapshotted. A caller
// on either side of the boundary cannot mutate engine state afterwards.

func evidenceRunner(t *testing.T) *Runner {
	t.Helper()
	return mustRunner(t, Config{Defaults: validTiming()},
		&recordingPhase{stubPhase: stubPhase{id: "submit"}, do: func(_ context.Context, run *Run) error {
			run.Observe("request", map[string]any{"payload": "original"})
			run.Record(result.Compared("ok", []bool{true}).
				WithExpected(map[string]any{"want": "original"}).
				WithActual([]string{"original"}))
			return nil
		}},
	)
}

func TestReturnedReportsDetachNestedEvidence(t *testing.T) {
	s := startSession(t, evidenceRunner(t), &stubCase{id: "one"})
	got := s.Cases()[0]
	got.Observations[0].Value.(map[string]any)["payload"] = "MUTATED"
	got.Results[0].Result.Expected.(map[string]any)["want"] = "MUTATED"

	var buf bytes.Buffer
	if err := s.Report().WriteJSON(&buf); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(buf.String(), "MUTATED") {
		t.Fatal("a consumer mutated session evidence through a returned clone")
	}
}

func TestEventPayloadsAreDetachedPerObserver(t *testing.T) {
	r := evidenceRunner(t)
	var second CaseReport
	WithObserver(func(ev Event) { // the hostile observer mutates its payload
		if cf, ok := ev.(CaseFinishedEvent); ok {
			if len(cf.Report.Observations) > 0 {
				if m, ok := cf.Report.Observations[0].Value.(map[string]any); ok {
					m["payload"] = "MUTATED"
				}
			}
		}
	})(r)
	WithObserver(func(ev Event) {
		if cf, ok := ev.(CaseFinishedEvent); ok {
			second = cf.Report
		}
	})(r)
	s := startSession(t, r, &stubCase{id: "one"})

	if m, ok := second.Observations[0].Value.(map[string]any); ok && m["payload"] == "MUTATED" {
		t.Fatal("observer 2 saw observer 1's mutation — payloads must be independent")
	}
	var buf bytes.Buffer
	_ = s.Report().WriteJSON(&buf)
	if strings.Contains(buf.String(), "MUTATED") {
		t.Fatal("an observer mutated retained session evidence")
	}
}

func TestRunnerSnapshotsItsConfig(t *testing.T) {
	on := true
	cfg := Config{
		Defaults: validTiming(),
		Phases:   map[ID]Settings{"submit": {Enabled: &on}},
	}
	r := mustRunner(t, cfg, passingPhase("submit", nil))

	// Hostile post-construction mutations of everything reachable.
	on = false
	off := false
	cfg.Phases["submit"] = Settings{Enabled: &off}
	cfg.Defaults.Attempts = 0

	po := phaseOutcome(t, caseReport(t, startSession(t, r, &stubCase{id: "one"}), "one"), "submit")
	if po.Status != Passed {
		t.Fatalf("submit = %v — mutating the caller's Config after NewRunner changed the runner", po.Status)
	}
}

func TestRunnerSnapshotsPipelineAndGroups(t *testing.T) {
	members := []ID{"m"}
	phases := []Interface{
		&recordingPhase{stubPhase: stubPhase{id: "m"}, do: func(_ context.Context, run *Run) error {
			run.Record(result.Compared("ok", []bool{true}))
			return nil
		}},
	}
	p := NewPipeline(phases...).Group(Group{ID: "g", Members: members, Lifecycle: &noopGroupFixture{}})
	r, err := NewRunner(p, Config{Defaults: validTiming()})
	if err != nil {
		t.Fatal(err)
	}

	members[0] = "hijacked" // mutate the caller-owned member slice
	phases[0] = nil         // and the caller-owned phase slice

	cr := caseReport(t, startSession(t, r, &stubCase{id: "one"}), "one")
	if len(cr.Groups) != 1 || len(cr.Groups[0].Members) != 1 || cr.Groups[0].Members[0] != "m" {
		t.Fatalf("groups = %+v — mutating the caller's slices after construction changed the runner", cr.Groups)
	}
	if cr.Status != Passed {
		t.Fatalf("status = %v", cr.Status)
	}
}

func TestScopeKeysAreCopiedOnRead(t *testing.T) {
	run := NewRunForTesting(&stubCase{id: "one"},
		WithScope(Scope{CaseID: "one", Keys: map[string]string{"tenant": "original"}}))
	run.Scope().Keys["tenant"] = "MUTATED"
	if got := run.Scope().Keys["tenant"]; got != "original" {
		t.Fatalf("Keys[tenant] = %q — Scope() must not alias internal state", got)
	}
}

func TestCancelledConcurrentRunsLeakNoGoroutines(t *testing.T) {
	// Cancellation-heavy concurrent execution must not strand workers: after
	// Start returns and a settling pause, the goroutine count returns to its
	// baseline.
	baseline := runtime.NumGoroutine()
	for i := 0; i < 5; i++ {
		r := mustRunner(t, Config{Defaults: Timing{Attempts: 1000, Interval: time.Millisecond}, MaxCaseConcurrency: 3, MaxPhaseConcurrency: 2},
			&recordingPhase{stubPhase: stubPhase{id: "wait"}, do: func(ctx context.Context, run *Run) error {
				_, err := WaitUntil(ctx, run, func(context.Context) (int, bool, error) { return 0, false, nil })
				return err
			}},
		)
		ctx, cancel := context.WithCancel(context.Background())
		go func() { time.Sleep(5 * time.Millisecond); cancel() }()
		if _, err := r.Start(ctx, []Case{&stubCase{id: "a"}, &stubCase{id: "b"}, &stubCase{id: "c"}, &stubCase{id: "d"}}); err != nil {
			t.Fatal(err)
		}
		cancel()
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if runtime.NumGoroutine() <= baseline+2 {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("goroutines: baseline %d, now %d — workers leaked past cancellation", baseline, runtime.NumGoroutine())
}
