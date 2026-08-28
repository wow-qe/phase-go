// Copyright 2026 The Phase Contributors
// SPDX-License-Identifier: MIT

package phase

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

// WaitUntil is the reason phases never sleep: a fixed sleep is simultaneously
// too slow on a fast machine and too short on a loaded one. The budget is in
// ATTEMPTS, per case (a shared wall-clock budget starves cases that
// were merely scheduled late), and exhaustion is a failure whose reason names
// the budget — never a timeout swallowed as "nothing found".

func waitRun(t Timing) (*Run, *[]time.Duration) {
	r := newTestRun()
	r.phase, r.timing = "settle", t
	var slept []time.Duration
	r.core.sleep = func(ctx context.Context, d time.Duration) error {
		slept = append(slept, d)
		return ctx.Err()
	}
	return r, &slept
}

func TestWaitReturnsWhenTheConditionHolds(t *testing.T) {
	r, slept := waitRun(Timing{Attempts: 5, Interval: 15 * time.Second})
	calls := 0
	got, err := WaitUntil(context.Background(), r, func(ctx context.Context) (string, bool, error) {
		calls++
		return "row-9", calls == 3, nil
	})
	if err != nil || got != "row-9" {
		t.Fatalf("got %q, %v", got, err)
	}
	if calls != 3 {
		t.Fatalf("condition evaluated %d times, want 3", calls)
	}
	// Sleeps BETWEEN attempts: two, not three. Sleeping after success wastes
	// interval × cases per run for nothing.
	if len(*slept) != 2 || (*slept)[0] != 15*time.Second {
		t.Fatalf("slept %v", *slept)
	}
}

func TestBudgetExhaustionNamesTheBudget(t *testing.T) {
	r, _ := waitRun(Timing{Attempts: 4, Interval: 15 * time.Second})
	_, err := WaitUntil(context.Background(), r, func(ctx context.Context) (int, bool, error) {
		return 0, false, nil
	})
	if !errors.Is(err, ErrBudgetExhausted) {
		t.Fatalf("err = %v, want ErrBudgetExhausted", err)
	}
	for _, part := range []string{"4", "15s", "settle"} {
		if !strings.Contains(err.Error(), part) {
			t.Fatalf("exhaustion must read 'gave up after 4×15s in settle'; got %q missing %q", err, part)
		}
	}
}

func TestConditionErrorAbortsImmediately(t *testing.T) {
	// An error from the condition is a transport/adapter fact, not "not yet".
	// Retrying it here would blur the poll/transport distinction and
	// hide a real outage behind a patient loop.
	r, slept := waitRun(Timing{Attempts: 5, Interval: time.Second})
	calls := 0
	_, err := WaitUntil(context.Background(), r, func(ctx context.Context) (int, bool, error) {
		calls++
		return 0, false, errors.New("connection refused")
	})
	if err == nil || errors.Is(err, ErrBudgetExhausted) {
		t.Fatalf("err = %v", err)
	}
	if calls != 1 || len(*slept) != 0 {
		t.Fatalf("must abort on first error: calls=%d slept=%v", calls, *slept)
	}
}

func TestCancellationSurfacesAsContextError(t *testing.T) {
	// A cancelled suite must not read as a product failure. WaitUntil
	// returns ctx.Err() so the runner can mark the case Errored("cancelled").
	r, _ := waitRun(Timing{Attempts: 5, Interval: time.Second})
	ctx, cancel := context.WithCancel(context.Background())
	calls := 0
	_, err := WaitUntil(ctx, r, func(ctx context.Context) (int, bool, error) {
		calls++
		cancel() // cancelled while waiting
		return 0, false, nil
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	if calls != 1 {
		t.Fatalf("no further attempts after cancellation; calls = %d", calls)
	}
}

func TestUnvalidatedTimingIsAFrameworkError(t *testing.T) {
	// Preflight guarantees Attempts >= 1. Reaching here without it means the
	// runner skipped validation — a bug in phase, not a condition to clamp:
	// a silent clamp is a default, and defaults are how coverage disappears.
	r, _ := waitRun(Timing{Attempts: 0, Interval: time.Second})
	_, err := WaitUntil(context.Background(), r, func(ctx context.Context) (int, bool, error) {
		t.Fatal("condition must not run under invalid timing")
		return 0, false, nil
	})
	var fe *FrameworkError
	if !errors.As(err, &fe) {
		t.Fatalf("err = %v, want *FrameworkError", err)
	}
}

func TestTimeoutCutsOffASingleBlockingCondition(t *testing.T) {
	// C2 rework: the first fix only checked the deadline BEFORE each attempt,
	// so one cond call that blocked past Timeout was still unbounded. The
	// condition's context must carry the deadline, and its expiry must read as
	// the named budget — not as a bare context error.
	r := newTestRun()
	r.phase, r.timing = "settle", Timing{Attempts: 3, Interval: time.Hour, Timeout: 30 * time.Millisecond}
	calls := 0
	start := time.Now()
	_, err := WaitUntil(context.Background(), r, func(ctx context.Context) (int, bool, error) {
		calls++
		select {
		case <-ctx.Done():
			return 0, false, ctx.Err()
		case <-time.After(2 * time.Second): // escape hatch so a broken cut-off fails instead of deadlocking
			return 0, false, errors.New("cond was never cancelled")
		}
	})
	if !errors.Is(err, ErrBudgetExhausted) || !strings.Contains(err.Error(), "timeout") {
		t.Fatalf("err = %v, want the named Timeout backstop", err)
	}
	if calls != 1 {
		t.Fatalf("calls = %d, want 1 — the timeout bounds the call, not just the next attempt", calls)
	}
	if time.Since(start) > time.Second {
		t.Fatal("cond was not cut off at Timeout")
	}
}

func TestTimeoutBoundsAHangingCondition(t *testing.T) {
	// Timing.Timeout was documented as the wall-clock backstop and read
	// nowhere. A condition that ignores ctx and never returns done must be
	// bounded by Timeout.
	r := newTestRun()
	r.phase, r.timing = "settle", Timing{Attempts: 1000000, Interval: time.Hour, Timeout: 50 * time.Millisecond}
	var advanced time.Duration
	base := time.Unix(1756300000, 0)
	r.core.now = func() time.Time { return base.Add(advanced) }
	r.core.sleep = func(ctx context.Context, d time.Duration) error { advanced += d; return nil }
	_, err := WaitUntil(context.Background(), r, func(context.Context) (int, bool, error) {
		return 0, false, nil // never done, never errors, ignores ctx
	})
	if err == nil {
		t.Fatal("a hanging condition must be bounded by Timing.Timeout")
	}
}
