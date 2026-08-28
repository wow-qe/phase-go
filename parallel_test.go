// Copyright 2026 The Phase Contributors
// SPDX-License-Identifier: MIT

package phase

import (
	"context"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/wow-qe/phase-go/result"
)

// Two knobs, defaulting to today's sequential
// behaviour. Same-DAG-level phases may overlap within a case; cases may
// overlap within a session, honouring Exclusive() and the case DAG. The
// REPORT stays deterministic whatever the interleaving.

// rendezvousPhase blocks until its partner arrives — the test deadlocks in
// ~2s (via the escape hatch) unless the two genuinely run concurrently.
func rendezvousPhase(id ID, meet chan struct{}, deps ...ID) Interface {
	return &recordingPhase{stubPhase: stubPhase{id: id, deps: deps}, do: func(ctx context.Context, run *Run) error {
		select {
		case meet <- struct{}{}:
		case <-meet:
		case <-time.After(2 * time.Second):
			return fmt.Errorf("rendezvous timeout: %s never met its partner — phases did not overlap", id)
		}
		run.Record(result.Compared(string(id)+" ok", []bool{true}))
		return nil
	}}
}

func TestSameLevelPhasesOverlapWhenConfigured(t *testing.T) {
	meet := make(chan struct{})
	r := mustRunner(t, Config{Defaults: validTiming(), MaxPhaseConcurrency: 2},
		rendezvousPhase("a", meet),
		rendezvousPhase("b", meet),
	)
	cr := caseReport(t, startSession(t, r, &stubCase{id: "pair"}), "pair")
	if cr.Status != Passed {
		t.Fatalf("status = %s (%s)", cr.Status, cr.Reason)
	}
	// Determinism: rows and evidence in topological/declaration order,
	// whatever completion order was.
	if cr.Phases[0].ID != "a" || cr.Phases[1].ID != "b" {
		t.Fatalf("row order = %s,%s — deterministic, not completion", cr.Phases[0].ID, cr.Phases[1].ID)
	}
	var names []string
	for _, ar := range cr.Results {
		names = append(names, ar.Result.Name)
	}
	if got := strings.Join(names, ","); got != "a ok,b ok" {
		t.Fatalf("evidence order = %q", got)
	}
}

func TestPhaseConcurrencyRespectsTheDAG(t *testing.T) {
	// b depends on a: they must NOT overlap even at concurrency 2, and b
	// must see a's handoff.
	key := parallelKey
	var got string
	r := mustRunner(t, Config{Defaults: validTiming(), MaxPhaseConcurrency: 2},
		&recordingPhase{stubPhase: stubPhase{id: "a", produces: []KeyID{key.ID()}}, do: func(_ context.Context, run *Run) error {
			Put(run, key, "from-a")
			run.Record(result.Compared("a ok", []bool{true}))
			return nil
		}},
		&recordingPhase{stubPhase: stubPhase{id: "b", deps: []ID{"a"}, requires: []KeyID{key.ID()}}, do: func(_ context.Context, run *Run) error {
			v, err := Get(run, key)
			if err != nil {
				return err
			}
			got = v
			run.Record(result.Compared("b ok", []bool{true}))
			return nil
		}},
	)
	cr := caseReport(t, startSession(t, r, &stubCase{id: "chained"}), "chained")
	if cr.Status != Passed || got != "from-a" {
		t.Fatalf("status=%s got=%q", cr.Status, got)
	}
}

func TestGroupSetupFiresOnceUnderConcurrentMembers(t *testing.T) {
	var setups atomic.Int32
	meet := make(chan struct{})
	lc := &countingLifecycle{setups: &setups}
	p := NewPipeline(
		rendezvousPhase("m1", meet),
		rendezvousPhase("m2", meet),
	).Group(Group{ID: "g", Members: []ID{"m1", "m2"}, Lifecycle: lc})
	r, err := NewRunner(p, Config{Defaults: validTiming(), MaxPhaseConcurrency: 2})
	if err != nil {
		t.Fatal(err)
	}
	cr := caseReport(t, startSession(t, r, &stubCase{id: "one"}), "one")
	if cr.Status != Passed {
		t.Fatalf("status = %s (%s)", cr.Status, cr.Reason)
	}
	if n := setups.Load(); n != 1 {
		t.Fatalf("setup fired %d times under concurrent members, want exactly 1", n)
	}
}

type countingLifecycle struct{ setups *atomic.Int32 }

func (l *countingLifecycle) Setup(context.Context, *Run) error {
	l.setups.Add(1)
	return nil
}
func (l *countingLifecycle) Teardown(context.Context, *Run) error { return nil }

func TestCasesOverlapWhenConfigured(t *testing.T) {
	meet := make(chan struct{})
	r := mustRunner(t, Config{Defaults: validTiming(), MaxCaseConcurrency: 2},
		&recordingPhase{stubPhase: stubPhase{id: "checks"}, do: func(ctx context.Context, run *Run) error {
			select {
			case meet <- struct{}{}:
			case <-meet:
			case <-time.After(2 * time.Second):
				return fmt.Errorf("cases did not overlap")
			}
			run.Record(result.Compared("ok", []bool{true}))
			return nil
		}},
	)
	s := startSession(t, r, &stubCase{id: "one"}, &stubCase{id: "two"})
	cases := s.Cases()
	if cases[0].CaseID != "one" || cases[1].CaseID != "two" {
		t.Fatalf("report order = %s,%s — declaration order is the contract", cases[0].CaseID, cases[1].CaseID)
	}
	if cases[0].Status != Passed || cases[1].Status != Passed {
		t.Fatalf("statuses: %s (%s), %s (%s)", cases[0].Status, cases[0].Reason, cases[1].Status, cases[1].Reason)
	}
}

func TestExclusiveCaseRunsAlone(t *testing.T) {
	var active, maxSeen, activeDuringExclusive atomic.Int32
	track := func(exclusive bool) func(context.Context, *Run) error {
		return func(_ context.Context, run *Run) error {
			n := active.Add(1)
			defer active.Add(-1)
			for {
				m := maxSeen.Load()
				if n <= m || maxSeen.CompareAndSwap(m, n) {
					break
				}
			}
			if exclusive {
				activeDuringExclusive.Store(n)
			}
			time.Sleep(10 * time.Millisecond)
			run.Record(result.Compared("ok", []bool{true}))
			return nil
		}
	}
	exclusivity := map[string]bool{"solo": true}
	r := mustRunner(t, Config{Defaults: validTiming(), MaxCaseConcurrency: 3},
		&recordingPhase{stubPhase: stubPhase{id: "checks"}, do: func(ctx context.Context, run *Run) error {
			return track(exclusivity[run.Case().ID()])(ctx, run)
		}},
	)
	s := startSession(t, r,
		&stubCase{id: "a"}, &stubCase{id: "b"}, &stubCase{id: "c"},
		&stubCase{id: "solo", exclusive: true, exclWhy: "mutates the shared ledger"},
		&stubCase{id: "d"}, &stubCase{id: "e"},
	)
	for _, cr := range s.Cases() {
		if cr.Status != Passed {
			t.Fatalf("%s = %s (%s)", cr.CaseID, cr.Status, cr.Reason)
		}
	}
	if maxSeen.Load() < 2 {
		t.Fatal("no overlap at all — the pool never parallelised")
	}
	if got := activeDuringExclusive.Load(); got != 1 {
		t.Fatalf("exclusive case saw %d active cases, want exactly itself", got)
	}
}

func TestCaseDependencyHoldsUnderConcurrency(t *testing.T) {
	var order []string
	var mu = make(chan struct{}, 1)
	mu <- struct{}{}
	record := func(id string) {
		<-mu
		order = append(order, id)
		mu <- struct{}{}
	}
	r := mustRunner(t, Config{Defaults: validTiming(), MaxCaseConcurrency: 3},
		&recordingPhase{stubPhase: stubPhase{id: "checks"}, do: func(_ context.Context, run *Run) error {
			time.Sleep(5 * time.Millisecond)
			record(run.Case().ID())
			run.Record(result.Compared("ok", []bool{true}))
			return nil
		}},
	)
	startSession(t, r,
		&depCase{stubCase: stubCase{id: "dependent"}, deps: []CaseRequirement{{CaseID: "base", Acceptable: []Status{Passed}}}},
		&stubCase{id: "base"},
		&stubCase{id: "bystander"},
	)
	pos := map[string]int{}
	for i, id := range order {
		pos[id] = i
	}
	if pos["base"] >= pos["dependent"] {
		t.Fatalf("order = %v — the dependent must wait for its dependency's verdict", order)
	}
}

var parallelKey = Declare[string]("parallel_test_key")
