// Copyright 2026 The Phase Contributors
// SPDX-License-Identifier: MIT

package comparators_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	phase "github.com/wow-qe/phase-go"
	"github.com/wow-qe/phase-go/phasetest"
	"github.com/wow-qe/phase-go/result"
	cmp "github.com/wow-qe/phase-go/x/comparators"
)

// The comparisons every consumer was about to hand-roll, built once on
// the result package's invariant: a comparison over nothing is a failure
// with a reason, never a pass.

func TestContainsAllPassesWhenEverythingIsThere(t *testing.T) {
	r := cmp.ContainsAll("ledger rows", []string{"a", "b"}, []string{"b", "a", "c"})
	if !r.Passed() || r.Comparisons() != 2 {
		t.Fatalf("r = %+v", r)
	}
}

func TestContainsAllNamesMissingAndExtra(t *testing.T) {
	r := cmp.ContainsAll("ledger rows", []string{"a", "b"}, []string{"b", "x"})
	if r.Passed() {
		t.Fatal("missing element must fail")
	}
	if !strings.Contains(r.Reason(), "missing") || !strings.Contains(r.Reason(), "a") {
		t.Fatalf("reason %q must name what is missing", r.Reason())
	}
}

func TestContainsAllOfNothingIsAFailure(t *testing.T) {
	// The founding rule holds in every comparator: wanting nothing and
	// checking nothing must never read as a pass.
	if r := cmp.ContainsAll("ledger rows", []string{}, []string{"a"}); r.Passed() {
		t.Fatal("containing all of the empty set is zero comparisons — a failure, not a pass")
	}
}

func TestValueMatchDiffsOnMismatch(t *testing.T) {
	type row struct{ State, Region string }
	r := cmp.ValueMatch("row", row{"settled", "eu"}, row{"open", "eu"})
	if r.Passed() {
		t.Fatal("mismatch must fail")
	}
	if !strings.Contains(r.Reason(), "settled") || !strings.Contains(r.Reason(), "open") {
		t.Fatalf("reason %q must carry the diff", r.Reason())
	}
	if r.Expected() == nil || r.Actual() == nil {
		t.Fatal("evidence must be attached")
	}
}

func TestValueMatchPasses(t *testing.T) {
	if r := cmp.ValueMatch("row", 42, 42); !r.Passed() || r.Comparisons() != 1 {
		t.Fatalf("r = %+v", r)
	}
}

func TestEachEntityIsOneResultPerEntity(t *testing.T) {
	ents := []result.EntityRef{{Kind: "entity", ID: "e1"}, {Kind: "entity", ID: "e2"}}
	rs := cmp.EachEntity("settled", ents, func(e result.EntityRef) result.Result {
		return cmp.ValueMatch("state", "settled", map[string]string{"e1": "settled", "e2": "open"}[e.ID])
	})
	if len(rs) != 2 {
		t.Fatalf("results = %d", len(rs))
	}
	if !rs[0].Passed() || rs[1].Passed() {
		t.Fatalf("rs = %+v", rs)
	}
	if rs[1].Entity().ID != "e2" {
		t.Fatalf("attribution lost: %+v", rs[1].Entity())
	}
}

func TestEachEntityOverNoEntitiesIsAFailure(t *testing.T) {
	rs := cmp.EachEntity("settled", nil, func(result.EntityRef) result.Result {
		t.Fatal("check must not run over zero entities")
		return result.Result{}
	})
	if len(rs) != 1 || rs[0].Passed() {
		t.Fatalf("zero entities must yield one failing result, got %+v", rs)
	}
	if !strings.Contains(rs[0].Reason(), "zero entities") {
		t.Fatalf("reason %q must say why", rs[0].Reason())
	}
}

func TestPollCompareWaitsForTheValue(t *testing.T) {
	c := phasetest.NewClock(time.Unix(1756400000, 0))
	run := phase.NewRunForTesting(nil,
		phase.WithClock(c.Now), phase.WithSleeper(c.Sleeper()),
		phase.WithPhase("settle", phase.Timing{Attempts: 5, Interval: time.Second}))
	calls := 0
	r, err := cmp.PollCompare(context.Background(), run, "state", "settled", func(ctx context.Context) (string, error) {
		calls++
		if calls < 3 {
			return "open", nil
		}
		return "settled", nil
	})
	if err != nil || !r.Passed() {
		t.Fatalf("r=%+v err=%v", r, err)
	}
	if calls != 3 {
		t.Fatalf("calls = %d", calls)
	}
}

func TestPollCompareBudgetExhaustionFailsWithTheLastValue(t *testing.T) {
	c := phasetest.NewClock(time.Unix(1756400000, 0))
	run := phase.NewRunForTesting(nil,
		phase.WithClock(c.Now), phase.WithSleeper(c.Sleeper()),
		phase.WithPhase("settle", phase.Timing{Attempts: 3, Interval: time.Second}))
	r, err := cmp.PollCompare(context.Background(), run, "state", "settled", func(ctx context.Context) (string, error) {
		return "open", nil
	})
	if err != nil {
		t.Fatalf("budget exhaustion is a FAILING RESULT, not an error: %v", err)
	}
	if r.Passed() {
		t.Fatal("never matched must fail")
	}
	for _, want := range []string{"open", "settled", "3"} {
		if !strings.Contains(r.Reason(), want) {
			t.Fatalf("reason %q must carry last value, want and budget (%q)", r.Reason(), want)
		}
	}
}

func TestPollCompareTransportErrorIsAnError(t *testing.T) {
	c := phasetest.NewClock(time.Unix(1756400000, 0))
	run := phase.NewRunForTesting(nil,
		phase.WithClock(c.Now), phase.WithSleeper(c.Sleeper()),
		phase.WithPhase("settle", phase.Timing{Attempts: 3, Interval: time.Second}))
	boom := errors.New("connection refused")
	_, err := cmp.PollCompare(context.Background(), run, "state", "settled", func(ctx context.Context) (string, error) {
		return "", boom
	})
	if !errors.Is(err, boom) {
		t.Fatalf("a transport error is an error, not 'not yet': %v", err)
	}
}
