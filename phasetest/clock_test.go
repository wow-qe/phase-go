// Copyright 2026 The Phase Contributors
// SPDX-License-Identifier: MIT

package phasetest_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	phase "github.com/wow-qe/phase-go"
	"github.com/wow-qe/phase-go/phasetest"
)

func TestClockNowReturnsStart(t *testing.T) {
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	c := phasetest.NewClock(start)
	if got := c.Now(); !got.Equal(start) {
		t.Fatalf("Now() = %v, want %v", got, start)
	}
}

func TestClockAdvance(t *testing.T) {
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	c := phasetest.NewClock(start)
	c.Advance(5 * time.Minute)
	want := start.Add(5 * time.Minute)
	if got := c.Now(); !got.Equal(want) {
		t.Fatalf("Now() after Advance = %v, want %v", got, want)
	}
}

func TestClockConcurrentAdvanceAndNow(t *testing.T) {
	// Clock must be safe for concurrent use — run under -race.
	c := phasetest.NewClock(time.Unix(0, 0))
	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			c.Advance(time.Second)
		}()
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = c.Now()
		}()
	}
	wg.Wait()
	want := time.Unix(0, 0).Add(100 * time.Second)
	if got := c.Now(); !got.Equal(want) {
		t.Fatalf("Now() after 100 concurrent advances = %v, want %v", got, want)
	}
}

func TestClockSleeperAdvancesInsteadOfBlocking(t *testing.T) {
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	c := phasetest.NewClock(start)
	sleep := c.Sleeper()

	err := sleep(context.Background(), 15*time.Second)
	if err != nil {
		t.Fatalf("sleep() = %v, want nil", err)
	}
	want := start.Add(15 * time.Second)
	if got := c.Now(); !got.Equal(want) {
		t.Fatalf("Now() after Sleeper = %v, want %v", got, want)
	}
}

func TestClockSleeperReturnsCtxErr(t *testing.T) {
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	c := phasetest.NewClock(start)
	sleep := c.Sleeper()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := sleep(ctx, time.Second); !errors.Is(err, context.Canceled) {
		t.Fatalf("sleep() with cancelled ctx = %v, want context.Canceled", err)
	}
	// On an already-cancelled context the clock must NOT advance, matching
	// production sleep (run.go), which returns ctx.Err() without consuming the
	// interval. The prior version of this test asserted the opposite and so
	// encoded the very fidelity gap the review flagged.
	if got := c.Now(); !got.Equal(start) {
		t.Fatalf("Now() after cancelled sleep = %v, want %v (no advance on a cancelled ctx)", got, start)
	}
}

// TestSleeperDrivesWaitUntilInstantly is the required end-to-end proof: a
// real phase.WaitUntil with a 20×15s budget must finish in microseconds when
// driven by phasetest.Clock, and budget exhaustion must still be reported
// correctly (ErrBudgetExhausted, naming the budget).
func TestSleeperDrivesWaitUntilInstantly(t *testing.T) {
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	c := phasetest.NewClock(start)
	stub := &stubCase{id: "case-1"}
	timing := phase.Timing{Attempts: 20, Interval: 15 * time.Second}
	run := phase.NewRunForTesting(stub,
		phase.WithClock(c.Now),
		phase.WithSleeper(c.Sleeper()),
		phase.WithPhase("wait-phase", timing),
	)

	started := time.Now()
	calls := 0
	_, err := phase.WaitUntil(context.Background(), run, func(ctx context.Context) (int, bool, error) {
		calls++
		return 0, false, nil // never satisfied — forces full budget exhaustion
	})
	elapsed := time.Since(started)

	if !errors.Is(err, phase.ErrBudgetExhausted) {
		t.Fatalf("err = %v, want ErrBudgetExhausted", err)
	}
	if calls != 20 {
		t.Fatalf("condition evaluated %d times, want 20", calls)
	}
	if elapsed > 200*time.Millisecond {
		t.Fatalf("WaitUntil took %v real time for a 20×15s budget — the sleeper is blocking instead of advancing the clock", elapsed)
	}
	// 20 attempts sleep BETWEEN attempts only: 19 sleeps of 15s each.
	wantClock := start.Add(19 * 15 * time.Second)
	if got := c.Now(); !got.Equal(wantClock) {
		t.Fatalf("clock ended at %v, want %v (19×15s of advances)", got, wantClock)
	}
}
