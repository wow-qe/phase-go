// Copyright 2026 The Phase Contributors
// SPDX-License-Identifier: MIT

package phasetest

import (
	"sync"
	"time"

	phase "github.com/wow-qe/phase-go"
	"github.com/wow-qe/phase-go/result"
)

// SpyRecorder implements phase.Recorder, capturing everything a comparator
// (or a hand-called phase) did, for direct assertion in a unit test — no
// live system, no *phase.Run, no Runner.
//
// The Phase field on captured Observations is always empty: SpyRecorder is
// not attributed to a run's current phase the way *phase.Run is, because
// it is meant to test one comparator in isolation, not a positioned phase.
//
// Safe for concurrent use.
type SpyRecorder struct {
	mu           sync.Mutex
	results      []result.Result
	observations []phase.Observation
	errs         []error
}

// Record captures a decided result.
func (s *SpyRecorder) Record(r result.Result) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.results = append(s.results, r)
}

// Observe captures a raw reading.
func (s *SpyRecorder) Observe(name string, value any) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.observations = append(s.observations, phase.Observation{
		Name: name, Value: value, At: time.Now(),
	})
}

// Fail captures an environment/adapter error. A nil error is ignored,
// matching *phase.Run.Fail.
func (s *SpyRecorder) Fail(err error) {
	if err == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.errs = append(s.errs, err)
}

// Results returns the captured results, in recorded order.
func (s *SpyRecorder) Results() []result.Result {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]result.Result, len(s.results))
	copy(out, s.results)
	return out
}

// Observations returns the captured observations, in recorded order.
func (s *SpyRecorder) Observations() []phase.Observation {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]phase.Observation, len(s.observations))
	copy(out, s.observations)
	return out
}

// Errors returns the captured errors, in recorded order.
func (s *SpyRecorder) Errors() []error {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]error, len(s.errs))
	copy(out, s.errs)
	return out
}

// FailedResults is convenience for the common assertion: the results that
// did not pass, in recorded order.
func (s *SpyRecorder) FailedResults() []result.Result {
	var out []result.Result
	for _, r := range s.Results() {
		if !r.Passed() {
			out = append(out, r)
		}
	}
	return out
}

var _ phase.Recorder = (*SpyRecorder)(nil)
