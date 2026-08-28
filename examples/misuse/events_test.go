// Copyright 2026 The Phase Contributors
// SPDX-License-Identifier: MIT

package misuse

import (
	"bytes"
	"context"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	phase "github.com/wow-qe/phase-go"
	"github.com/wow-qe/phase-go/phasetest"
)

// The event stream under concurrent load: a session mixing failures,
// declines, skips, and a permanent flake, run with both concurrency knobs
// on — every delivery guarantee must hold the same as on a passing run.

func TestEventStreamGuaranteesHoldUnderChaos(t *testing.T) {
	sys, cases := misuseSuite(t)
	sys.corruptAuthCode = true
	sys.permanentLedgerFlap = true

	rec, observe := phasetest.RecordEvents()
	var inFlight, overlaps int32
	serialProbe := phase.WithObserver(func(phase.Event) {
		if !atomic.CompareAndSwapInt32(&inFlight, 0, 1) {
			atomic.AddInt32(&overlaps, 1)
		}
		time.Sleep(50 * time.Microsecond)
		atomic.StoreInt32(&inFlight, 0)
	})

	cfg := sane()
	cfg.MaxCaseConcurrency = 3
	cfg.MaxPhaseConcurrency = 2
	cfg.RedactPatterns = []string{`auth-[a-z0-9-]+`}
	r := mustMisuseRunner(t, sys, cfg, observe, serialProbe)
	s, err := r.Start(context.Background(), cases)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	rep := s.Report()
	if verr := rep.Verify(); verr != nil {
		t.Fatalf("Verify under chaos: %v", verr)
	}

	// 1. Serialized delivery: the callback was never entered concurrently,
	// even with both concurrency knobs on.
	if n := atomic.LoadInt32(&overlaps); n != 0 {
		t.Fatalf("observer entered concurrently %d time(s) — delivery must be serialized", n)
	}

	events := rec.Events()
	// 2. Session brackets survive a red day.
	if events[0].Kind() != phase.SessionStarted || events[len(events)-1].Kind() != phase.SessionFinished {
		t.Fatal("the stream must stay bracketed by session events under chaos")
	}
	// 3. Pairing is total per case and phase: every Started has exactly one
	// Finished, gate-declined and failing phases included.
	type key struct {
		caseID string
		ph     phase.ID
	}
	started, finished := map[key]int{}, map[key]int{}
	for _, ev := range events {
		switch e := ev.(type) {
		case phase.PhaseStartedEvent:
			started[key{e.CaseID(), e.Phase}]++
		case phase.PhaseFinishedEvent:
			finished[key{e.CaseID(), e.Outcome.ID}]++
		}
	}
	for k, n := range started {
		if finished[k] != n {
			t.Fatalf("%v: %d started, %d finished — pairing must never orphan", k, n, finished[k])
		}
	}
	for _, cr := range rep.Cases {
		if cr.Status == phase.Skipped {
			continue
		}
		for _, po := range cr.Phases {
			if started[key{cr.CaseID, po.ID}] != 1 {
				t.Fatalf("case %s phase %s: started %d time(s), want exactly 1 (Reached covers declines)",
					cr.CaseID, po.ID, started[key{cr.CaseID, po.ID}])
			}
		}
	}
	// 4. The tolerance heartbeat fired for the permanent flap.
	var tolerance int
	for _, ev := range events {
		if e, ok := ev.(phase.RetryAttemptEvent); ok && e.Retry == "tolerance" {
			tolerance++
		}
	}
	if tolerance == 0 {
		t.Fatal("the permanently-flapping tolerance must heartbeat on the stream")
	}
	// 5. Emission redaction holds under mixed pass/fail traffic: the
	// corrupt comparison carries the real auth code in Expected — it must
	// never reach an event unredacted.
	for _, ev := range events {
		if cf, ok := ev.(phase.CaseFinishedEvent); ok {
			var buf bytes.Buffer
			evRep := phase.Report{Cases: []phase.CaseReport{cf.Report}}
			_ = evRep.WriteJSON(&buf)
			if strings.Contains(buf.String(), "auth-ord") {
				t.Fatalf("case %s leaked an auth code onto the live stream", cf.Report.CaseID)
			}
		}
	}
}

func TestPanickingObserverIsContainedAndOthersStillHear(t *testing.T) {
	sys, cases := misuseSuite(t)
	var survivorHeard int32
	bomb := phase.WithObserver(func(phase.Event) { panic("observer bug") })
	survivor := phase.WithObserver(func(phase.Event) { atomic.AddInt32(&survivorHeard, 1) })

	r := mustMisuseRunner(t, sys, sane(), bomb, survivor)
	s, err := r.Start(context.Background(), onlyCase(t, cases, "happy-single"))
	if err != nil {
		t.Fatalf("Start must survive a panicking observer: %v", err)
	}
	rep := s.Report()

	// The verdict is untouched; the sabotage is surfaced, not swallowed.
	if got := rowOf(t, rep, "happy-single").Status; got != phase.Passed {
		t.Fatalf("case = %v — an observer bug must never alter outcomes", got)
	}
	if atomic.LoadInt32(&survivorHeard) == 0 {
		t.Fatal("the second observer went deaf — containment must be per-observer, not per-dispatch")
	}
	if len(s.ObserverErrors()) == 0 {
		t.Fatal("the contained panics must surface on ObserverErrors")
	}
	if len(rep.Diagnostics) == 0 {
		t.Fatal("the report must carry the degraded-observability diagnostic")
	}
	if err := rep.Verify(); err != nil {
		t.Fatalf("Verify: %v", err)
	}
}
