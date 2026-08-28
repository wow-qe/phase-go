// Copyright 2026 The Phase Contributors
// SPDX-License-Identifier: MIT

package phasetest_test

import (
	"errors"
	"sync"
	"testing"

	phase "github.com/wow-qe/phase-go"
	"github.com/wow-qe/phase-go/phasetest"
	"github.com/wow-qe/phase-go/result"
)

func TestSpyRecorderCapturesResults(t *testing.T) {
	s := &phasetest.SpyRecorder{}
	pass := result.Compared("check-a", []bool{true, true})
	fail := result.Failed("check-b", "expected 1, saw 2")
	s.Record(pass)
	s.Record(fail)

	got := s.Results()
	if len(got) != 2 {
		t.Fatalf("Results() = %d entries, want 2", len(got))
	}
	if got[0].Name() != "check-a" || got[1].Name() != "check-b" {
		t.Fatalf("Results() = %+v, order/content wrong", got)
	}
}

func TestSpyRecorderCapturesObservations(t *testing.T) {
	s := &phasetest.SpyRecorder{}
	s.Observe("rows", []string{"a", "b"})
	s.Observe("status-code", 200)

	got := s.Observations()
	if len(got) != 2 {
		t.Fatalf("Observations() = %d entries, want 2", len(got))
	}
	if got[0].Name != "rows" || got[1].Name != "status-code" {
		t.Fatalf("Observations() = %+v, order/content wrong", got)
	}
	// Phase field may be empty here: SpyRecorder is not run-attributed.
	if got[0].Phase != "" {
		t.Fatalf("Observations()[0].Phase = %q, want empty", got[0].Phase)
	}
}

func TestSpyRecorderCapturesErrors(t *testing.T) {
	s := &phasetest.SpyRecorder{}
	e1 := errors.New("connection refused")
	e2 := errors.New("timeout")
	s.Fail(e1)
	s.Fail(e2)
	s.Fail(nil) // must be ignored, matching *phase.Run.Fail

	got := s.Errors()
	if len(got) != 2 {
		t.Fatalf("Errors() = %d entries, want 2 (nil must be ignored)", len(got))
	}
	if got[0] != e1 || got[1] != e2 {
		t.Fatalf("Errors() = %v, order/content wrong", got)
	}
}

func TestSpyRecorderFailedResults(t *testing.T) {
	s := &phasetest.SpyRecorder{}
	s.Record(result.Compared("passing", []bool{true}))
	s.Record(result.Failed("failing-1", "reason 1"))
	s.Record(result.Compared("also-passing", []bool{true, true}))
	s.Record(result.Failed("failing-2", "reason 2"))

	got := s.FailedResults()
	if len(got) != 2 {
		t.Fatalf("FailedResults() = %d entries, want 2", len(got))
	}
	for _, r := range got {
		if r.Passed() {
			t.Fatalf("FailedResults() contains a passing result: %+v", r)
		}
	}
	if got[0].Name() != "failing-1" || got[1].Name() != "failing-2" {
		t.Fatalf("FailedResults() = %+v, order/content wrong", got)
	}
}

func TestSpyRecorderIsConcurrencySafe(t *testing.T) {
	s := &phasetest.SpyRecorder{}
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			s.Record(result.Compared("concurrent", []bool{true}))
			s.Observe("tick", 1)
			s.Fail(errors.New("boom"))
		}()
	}
	wg.Wait()
	if got := len(s.Results()); got != 50 {
		t.Fatalf("Results() = %d, want 50", got)
	}
	if got := len(s.Observations()); got != 50 {
		t.Fatalf("Observations() = %d, want 50", got)
	}
	if got := len(s.Errors()); got != 50 {
		t.Fatalf("Errors() = %d, want 50", got)
	}
}

var _ phase.Recorder = (*phasetest.SpyRecorder)(nil) // compile-time proof
