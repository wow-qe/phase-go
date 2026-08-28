// Copyright 2026 The Phase Contributors
// SPDX-License-Identifier: MIT

package phasetest_test

import (
	"fmt"
	"strings"
	"sync"
	"testing"

	phase "github.com/wow-qe/phase-go"
	"github.com/wow-qe/phase-go/phasetest"
)

// fakeTB is a minimal testing.TB shim that captures failures instead of
// failing the real *testing.T — needed to test ConformanceCase's behaviour
// (which calls t.Error/t.Fatal on violations) without those calls tearing
// down this very test.
type fakeTB struct {
	testing.TB // embedded (nil) only to satisfy the interface's unexported method

	mu      sync.Mutex
	errs    []string
	logs    []string
	fatal   bool
	helped  bool
	skipped bool
}

func (f *fakeTB) Helper() { f.helped = true }

func (f *fakeTB) Log(args ...any) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.logs = append(f.logs, fmt.Sprint(args...))
}

func (f *fakeTB) Logf(format string, args ...any) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.logs = append(f.logs, fmt.Sprintf(format, args...))
}

func (f *fakeTB) Skip(args ...any) {
	f.mu.Lock()
	f.logs = append(f.logs, fmt.Sprint(args...))
	f.skipped = true
	f.mu.Unlock()
	panic(fakeFatal{}) // Skip stops the subtest body, as testing.T does via Goexit
}

func (f *fakeTB) Skipf(format string, args ...any) {
	f.mu.Lock()
	f.logs = append(f.logs, fmt.Sprintf(format, args...))
	f.skipped = true
	f.mu.Unlock()
	panic(fakeFatal{})
}

func (f *fakeTB) Error(args ...any) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.errs = append(f.errs, fmt.Sprint(args...))
}

func (f *fakeTB) Errorf(format string, args ...any) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.errs = append(f.errs, fmt.Sprintf(format, args...))
}

func (f *fakeTB) Fatal(args ...any) {
	f.mu.Lock()
	f.errs = append(f.errs, fmt.Sprint(args...))
	f.fatal = true
	f.mu.Unlock()
	panic(fakeFatal{})
}

func (f *fakeTB) Fatalf(format string, args ...any) {
	f.mu.Lock()
	f.errs = append(f.errs, fmt.Sprintf(format, args...))
	f.fatal = true
	f.mu.Unlock()
	panic(fakeFatal{})
}

// fakeFatal is the sentinel fakeTB.Fatal panics with, so the harness below
// can recover exactly that and nothing else — mirroring how testing.T.Fatal
// unwinds only the calling goroutine via runtime.Goexit.
type fakeFatal struct{}

// runConformance runs ConformanceCase against c on a fakeTB and returns the
// captured violations, recovering the Fatal sentinel so a nil-case (or other
// Fatal) path does not crash this test binary.
func runConformance(c phase.Case) *fakeTB {
	tb := &fakeTB{}
	func() {
		defer func() {
			if r := recover(); r != nil {
				if _, ok := r.(fakeFatal); !ok {
					panic(r) // not ours — a real bug, let it surface
				}
			}
		}()
		phasetest.ConformanceCase(tb, c)
	}()
	return tb
}

func containsSubstring(errs []string, substr string) bool {
	for _, e := range errs {
		if strings.Contains(e, substr) {
			return true
		}
	}
	return false
}

func TestConformanceCasePassesAWellBehavedCase(t *testing.T) {
	tb := runConformance(goodCase())
	if len(tb.errs) != 0 {
		t.Fatalf("well-behaved case produced violations: %v", tb.errs)
	}
}

func TestConformanceCaseCatchesEmptyID(t *testing.T) {
	c := goodCase()
	c.id = ""
	tb := runConformance(c)
	if !containsSubstring(tb.errs, "ID") {
		t.Fatalf("violations = %v, want one naming ID()", tb.errs)
	}
}

func TestConformanceCaseCatchesInvalidStatus(t *testing.T) {
	c := goodCase()
	c.status = phase.CaseStatus(999) // not in the enum — String() == "invalid"
	tb := runConformance(c)
	if !containsSubstring(tb.errs, "Status") {
		t.Fatalf("violations = %v, want one naming Status()", tb.errs)
	}
}

func TestConformanceCaseCatchesSelectsWithoutReason(t *testing.T) {
	c := goodCase()
	c.selects = func(phase.ID) (bool, string) { return false, "" }
	tb := runConformance(c)
	if !containsSubstring(tb.errs, "Selects") {
		t.Fatalf("violations = %v, want one naming Selects()", tb.errs)
	}
}

func TestConformanceCaseCatchesExclusiveWithoutReason(t *testing.T) {
	c := goodCase()
	c.exclusive = func() (bool, string) { return true, "" }
	tb := runConformance(c)
	if !containsSubstring(tb.errs, "Exclusive") {
		t.Fatalf("violations = %v, want one naming Exclusive()", tb.errs)
	}
}

func TestConformanceCaseCatchesNilFixture(t *testing.T) {
	c := goodCase()
	c.fixtures = []phase.Fixture{noopFixture{}, nil}
	tb := runConformance(c)
	if !containsSubstring(tb.errs, "Fixtures") {
		t.Fatalf("violations = %v, want one naming Fixtures()", tb.errs)
	}
}

func TestConformanceCaseCatchesZeroTimingOverride(t *testing.T) {
	c := goodCase()
	c.timing = func(phase.ID) (phase.Timing, bool) { return phase.Timing{}, true }
	tb := runConformance(c)
	if !containsSubstring(tb.errs, "Timing") {
		t.Fatalf("violations = %v, want one naming Timing()", tb.errs)
	}
}

func TestConformanceCaseProbesAnUnknownPhaseID(t *testing.T) {
	// A case that only answers for phases it happens to recognise, and
	// panics/empty-reasons for anything else, must be caught: an unknown
	// phase must still get a reasoned answer.
	c := goodCase()
	known := map[phase.ID]bool{"setup": true, "discover": true}
	c.selects = func(id phase.ID) (bool, string) {
		if known[id] {
			return false, "known phase, declined for test reasons"
		}
		return false, "" // unknown phase gets no reason — the violation
	}
	tb := runConformance(c)
	if !containsSubstring(tb.errs, "Selects") {
		t.Fatalf("violations = %v, want a Selects() violation for the unknown probe", tb.errs)
	}
}
