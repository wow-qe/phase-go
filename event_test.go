// Copyright 2026 The Phase Contributors
// SPDX-License-Identifier: MIT

package phase

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/wow-qe/phase-go/result"
)

// The event stream is the consumer-facing lifecycle contract: a fixed
// delivery order for every kind of event.

type eventSink struct {
	events []Event
}

func (s *eventSink) opt() RunnerOption {
	return WithObserver(func(ev Event) { s.events = append(s.events, ev) })
}

func (s *eventSink) kinds() string {
	out := make([]string, len(s.events))
	for i, ev := range s.events {
		out[i] = ev.Kind().String()
	}
	return strings.Join(out, ",")
}

func TestTheCanonicalEventTrace(t *testing.T) {
	sink := &eventSink{}
	var j []string
	r := groupRunner(t, &j,
		Group{ID: "g", Members: []ID{"m"},
			Lifecycle: &groupFixture{name: "g", journal: &j}},
		journalPhase("m", &j),
		&stubPhase{id: "declined", deps: []ID{"m"}, applies: Skip("not here")},
	)
	sink.opt()(r)
	startSession(t, r, &stubCase{id: "one", fixtures: []Fixture{noopFix{}}})
	want := strings.Join([]string{
		"session_started",
		"case_started",
		"fixture_setup_started", "fixture_setup_finished",
		"group_setup_started", "group_setup_finished", // fires as m reaches execution — causally before its start
		"phase_started", "phase_finished", // m
		"group_teardown_started", "group_teardown_finished",
		"phase_started", "phase_finished", // declined: adjacent pair — pairing is total
		"fixture_teardown_started", "fixture_teardown_finished",
		"case_finished",
		"session_finished",
	}, ",")
	if got := sink.kinds(); got != want {
		t.Fatalf("trace =\n%s\nwant\n%s", got, want)
	}
}

func TestPairingIsTotal(t *testing.T) {
	sink := &eventSink{}
	off := false
	r := mustRunner(t, Config{Defaults: validTiming(), Phases: map[ID]Settings{"disabled": {Enabled: &off}}},
		passingPhase("runs", nil),
		&stubPhase{id: "declined", applies: Skip("no")},
		&stubPhase{id: "disabled"},
	)
	sink.opt()(r)
	startSession(t, r, &stubCase{id: "one"})
	starts, finishes := map[ID]int{}, map[ID]int{}
	for _, ev := range sink.events {
		switch e := ev.(type) {
		case PhaseStartedEvent:
			starts[e.Phase]++
		case PhaseFinishedEvent:
			finishes[e.Outcome.ID]++
		}
	}
	for _, id := range []ID{"runs", "declined", "disabled"} {
		if starts[id] != 1 || finishes[id] != 1 {
			t.Fatalf("%s: starts=%d finishes=%d — every phase pairs exactly once", id, starts[id], finishes[id])
		}
	}
}

func TestEventPayloadsAreRedactedAtEmission(t *testing.T) {
	// The live stream is safe by default, exactly as the artifact is.
	sink := &eventSink{}
	r := mustRunner(t, Config{
		Defaults:       validTiming(),
		RedactPatterns: []string{`Bearer [A-Za-z0-9._-]+`},
	},
		&recordingPhase{stubPhase: stubPhase{id: "leaky"}, do: func(_ context.Context, run *Run) error {
			run.Observe("header", "Bearer sk-live-42")
			run.Record(result.Compared("ok", []bool{true}))
			return nil
		}},
	)
	sink.opt()(r)
	startSession(t, r, &stubCase{id: "one"})
	for _, ev := range sink.events {
		if e, ok := ev.(CaseFinishedEvent); ok {
			blob := fmt.Sprintf("%+v", e.Report)
			if strings.Contains(blob, "sk-live-42") {
				t.Fatal("the secret reached the live stream before the report's scrub — the emission side channel")
			}
			return
		}
	}
	t.Fatal("no CaseFinished event seen")
}

func TestRetryAttemptsAreLive(t *testing.T) {
	sink := &eventSink{}
	r := mustRunner(t, Config{Defaults: Timing{Attempts: 5, Interval: time.Millisecond}},
		&recordingPhase{stubPhase: stubPhase{id: "settle"}, do: func(ctx context.Context, run *Run) error {
			calls := 0
			_, err := WaitUntil(ctx, run, func(context.Context) (int, bool, error) {
				calls++
				return calls, calls == 3, nil
			})
			if err != nil {
				return err
			}
			run.Record(result.Compared("settled", []bool{true}))
			return nil
		}},
	)
	sink.opt()(r)
	startSession(t, r, &stubCase{id: "one"})
	var polls []string
	for _, ev := range sink.events {
		if e, ok := ev.(RetryAttemptEvent); ok {
			polls = append(polls, fmt.Sprintf("%s:%d/%d", e.Retry, e.Attempt, e.Of))
		}
	}
	// Attempts 1 and 2 were not-done; the third succeeded (no heartbeat
	// after success — the silence that follows is the signal).
	if got := strings.Join(polls, ","); got != "poll:1/5,poll:2/5" {
		t.Fatalf("retry heartbeats = %q", got)
	}
}

func TestSlowObserverDoesNotEatTheTimeoutBudget(t *testing.T) {
	// Observability must never change test outcomes.
	sink := WithObserver(func(ev Event) {
		if ev.Kind() == RetryAttempt {
			time.Sleep(40 * time.Millisecond) // each heartbeat costs more than the whole budget
		}
	})
	r := mustRunner(t, Config{Defaults: Timing{Attempts: 4, Interval: time.Millisecond, Timeout: 60 * time.Millisecond}},
		&recordingPhase{stubPhase: stubPhase{id: "settle"}, do: func(ctx context.Context, run *Run) error {
			calls := 0
			_, err := WaitUntil(ctx, run, func(context.Context) (int, bool, error) {
				calls++
				return calls, calls == 3, nil
			})
			if err != nil {
				return err
			}
			run.Record(result.Compared("settled", []bool{true}))
			return nil
		}},
	)
	sink(r)
	cr := caseReport(t, startSession(t, r, &stubCase{id: "one"}), "one")
	if cr.Status != Passed {
		t.Fatalf("status = %s (%s) — a slow observer caused a budget failure", cr.Status, cr.Reason)
	}
}

func TestObserversDispatchInRegistrationOrderAndPanicsAreContained(t *testing.T) {
	var order []string
	r := mustRunner(t, Config{Defaults: validTiming()}, passingPhase("p", nil))
	WithObserver(func(ev Event) {
		if ev.Kind() == SessionStarted {
			order = append(order, "first")
		}
	})(r)
	WithObserver(func(ev Event) {
		if ev.Kind() == SessionStarted {
			order = append(order, "second")
			panic("observer bug")
		}
	})(r)
	s := startSession(t, r, &stubCase{id: "one"})
	if strings.Join(order, ",") != "first,second" {
		t.Fatalf("order = %v", order)
	}
	if len(s.ObserverErrors()) == 0 {
		t.Fatal("the contained panic must surface")
	}
	if caseReport(t, s, "one").Status != Passed {
		t.Fatal("verdicts untouched")
	}
}

type noopFix struct{}

func (noopFix) Setup(context.Context, *Run) error    { return nil }
func (noopFix) Teardown(context.Context, *Run) error { return nil }

func TestPhaseFinishedReasonIsRedactedAtEmission(t *testing.T) {
	// PhaseOutcome.Reason is where raw adapter error text lands; every
	// event, including PhaseFinished, must apply redactString to it.
	dsn := "postgres://qe:s3cr3t@db.internal/x"
	r := mustRunner(t, Config{
		Defaults:       validTiming(),
		RedactPatterns: []string{`postgres://[^\s]+`},
	},
		&recordingPhase{stubPhase: stubPhase{id: "submit"}, do: func(context.Context, *Run) error {
			return fmt.Errorf("dial tcp: %s: connection refused", dsn)
		}},
	)
	var reason string
	WithObserver(func(ev Event) {
		if e, ok := ev.(PhaseFinishedEvent); ok && e.Outcome.ID == "submit" {
			reason = e.Outcome.Reason
		}
	})(r)
	startSession(t, r, &stubCase{id: "one"})
	if strings.Contains(reason, "s3cr3t") {
		t.Fatalf("the DSN reached PhaseFinished live: %q", reason)
	}
	if !strings.Contains(reason, "connection refused") {
		t.Fatalf("the non-secret text must survive: %q", reason)
	}
}
