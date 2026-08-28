// Copyright 2026 The Phase Contributors
// SPDX-License-Identifier: MIT

package phasetest

import (
	"context"
	"sync"
	"time"
)

// Clock is a manual-advance time source for phase.WithClock and
// phase.WithSleeper. Phases never read the wall clock (settings.go, wait.go)
// so tests never need to either: a Clock lets a test control exactly what
// "now" is and exactly how much time a wait consumed, without a real sleep
// ever happening.
//
// Safe for concurrent use.
type Clock struct {
	mu  sync.Mutex
	now time.Time
}

// NewClock returns a Clock starting at start.
func NewClock(start time.Time) *Clock {
	return &Clock{now: start}
}

// Now returns the clock's current time. Pass c.Now as phase.WithClock's
// argument.
func (c *Clock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

// Advance moves the clock forward by d.
func (c *Clock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

// Sleeper returns a sleeper for phase.WithSleeper. It does not block: it
// advances the clock by d and returns ctx.Err(), so a consumer's WaitUntil
// test runs a 20×15s budget in microseconds instead of five minutes.
func (c *Clock) Sleeper() func(context.Context, time.Duration) error {
	return func(ctx context.Context, d time.Duration) error {
		// Production sleep does NOT advance time when ctx is already
		// cancelled; the fake clock must match, or elapsed-time assertions
		// around cancellation see a clock that ran further than reality.
		if err := ctx.Err(); err != nil {
			return err
		}
		c.Advance(d)
		return ctx.Err()
	}
}
