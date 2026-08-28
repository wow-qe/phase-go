// Copyright 2026 The Phase Contributors
// SPDX-License-Identifier: MIT

package phase

import (
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"context"
	"github.com/wow-qe/phase-go/result"
)

// The group lifecycle is a typed, checked state
// machine (the one entity with a documented TOCTOU-prone spot), and the
// assembly line is pinned as a TRACE — exact in sequential mode, multiset +
// partial-order under concurrency.

func TestGroupMachineRefusesIllegalTransitions(t *testing.T) {
	m := newMachine(groupPending, groupTransitions)
	if err := m.to(groupActive); err == nil {
		t.Fatal("pending→active skips setting-up — the table must refuse it")
	}
	for _, s := range []groupState{groupSettingUp, groupActive, groupTearingDown, groupDone} {
		if err := m.to(s); err != nil {
			t.Fatalf("legal transition to %v refused: %v", s, err)
		}
	}
	if err := m.to(groupSettingUp); err == nil {
		t.Fatal("done is terminal — the machine must refuse to reopen")
	}
}

func TestGroupLifecycleRidesTheMachine(t *testing.T) {
	// The booleans are gone: the machine's states drive the outcome, and
	// the existing behavioral suite (setup-once, teardown-always, barrier)
	// must hold unchanged on top of it. This test pins the states' mapping.
	var j []string
	r := groupRunner(t, &j,
		Group{ID: "g", Members: []ID{"m"},
			Lifecycle: &groupFixture{name: "g", journal: &j}},
		journalPhase("m", &j),
	)
	cr := caseReport(t, startSession(t, r, &stubCase{id: "one"}), "one")
	if cr.Groups[0].Status != Passed {
		t.Fatalf("groups = %+v", cr.Groups)
	}
	if got := strings.Join(j, " | "); got != "g:setup | m | g:teardown" {
		t.Fatalf("journal = %q", got)
	}
}

func TestConcurrentTraceHoldsThePartialOrder(t *testing.T) {
	// The concurrent half: exact traces are impossible under overlap, but
	// the multiset (every event exactly once) and the causal partial order
	// must hold on every run.
	for i := 0; i < 5; i++ {
		var setups atomic.Int32
		meet := make(chan struct{})
		lc := &countingLifecycle{setups: &setups}
		p := NewPipeline(
			rendezvousPhase("m1", meet),
			rendezvousPhase("m2", meet),
			&recordingPhase{stubPhase: stubPhase{id: "audit", deps: []ID{"m1", "m2"}}, do: func(_ context.Context, run *Run) error {
				run.Record(result.Compared("audit", []bool{true}))
				return nil
			}},
		).Group(Group{ID: "g", Members: []ID{"m1", "m2"}, Lifecycle: lc})
		r, err := NewRunner(p, Config{Defaults: Timing{Attempts: 3, Interval: time.Millisecond}, MaxPhaseConcurrency: 2})
		if err != nil {
			t.Fatal(err)
		}
		sink := &eventSink{}
		sink.opt()(r)
		startSession(t, r, &stubCase{id: "one"})

		count := map[string]int{}
		pos := map[string]int{}
		for idx, ev := range sink.events {
			key := ev.Kind().String()
			switch e := ev.(type) {
			case PhaseStartedEvent:
				key += ":" + string(e.Phase)
			case PhaseFinishedEvent:
				key += ":" + string(e.Outcome.ID)
			case GroupEvent:
				key += ":" + string(e.GroupID)
			}
			count[key]++
			pos[key] = idx
		}
		// Multiset: every expected event exactly once.
		for _, k := range []string{
			"session_started", "case_started",
			"group_setup_started:g", "group_setup_finished:g",
			"phase_started:m1", "phase_finished:m1",
			"phase_started:m2", "phase_finished:m2",
			"group_teardown_started:g", "group_teardown_finished:g",
			"phase_started:audit", "phase_finished:audit",
			"case_finished", "session_finished",
		} {
			if count[k] != 1 {
				t.Fatalf("run %d: event %q count = %d, want exactly 1", i, k, count[k])
			}
		}
		// Partial order: causality survives interleaving.
		orders := [][2]string{
			{"session_started", "case_started"},
			{"group_setup_finished:g", "phase_finished:m1"},
			{"group_setup_finished:g", "phase_finished:m2"},
			{"phase_finished:m1", "group_teardown_started:g"},
			{"phase_finished:m2", "group_teardown_started:g"},
			{"group_teardown_finished:g", "case_finished"},
			{"case_finished", "session_finished"},
		}
		for _, o := range orders {
			if pos[o[0]] >= pos[o[1]] {
				t.Fatalf("run %d: %q must precede %q (got %d vs %d)", i, o[0], o[1], pos[o[0]], pos[o[1]])
			}
		}
		if setups.Load() != 1 {
			t.Fatalf("run %d: setup fired %d times", i, setups.Load())
		}
	}
}
