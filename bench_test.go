// Copyright 2026 The Phase Contributors
// SPDX-License-Identifier: MIT

package phase

import (
	"bytes"
	"context"
	"fmt"
	"testing"

	"github.com/wow-qe/phase-go/result"
)

// Baseline benchmarks for the hot paths. Published for tracking; not yet
// gated — thresholds come after stable baselines exist.

func benchPipeline(n int) *Pipeline {
	phases := make([]Interface, n)
	for i := range phases {
		id := ID(fmt.Sprintf("p%03d", i))
		var deps []ID
		if i > 0 {
			deps = []ID{ID(fmt.Sprintf("p%03d", i-1))}
		}
		phases[i] = &recordingPhase{stubPhase: stubPhase{id: id, deps: deps},
			do: func(_ context.Context, run *Run) error {
				run.Record(result.Compared("ok", []bool{true}))
				return nil
			}}
	}
	return NewPipeline(phases...)
}

func BenchmarkNewRunner100Phases(b *testing.B) {
	p := benchPipeline(100)
	cfg := Config{Defaults: validTiming()}
	b.ReportAllocs()
	for b.Loop() {
		if _, err := NewRunner(p, cfg); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkStartSequential100Cases(b *testing.B) {
	r, err := NewRunner(benchPipeline(10), Config{Defaults: validTiming()})
	if err != nil {
		b.Fatal(err)
	}
	cases := make([]Case, 100)
	for i := range cases {
		cases[i] = &stubCase{id: fmt.Sprintf("c%03d", i)}
	}
	b.ReportAllocs()
	for b.Loop() {
		if _, err := r.Start(context.Background(), cases); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkEvidenceRecording(b *testing.B) {
	run := NewRunForTesting(&stubCase{id: "bench"})
	res := result.Compared("ok", []bool{true}).WithActual("v")
	b.ReportAllocs()
	for b.Loop() {
		run.Record(res)
	}
}

func BenchmarkReportWriteJSON(b *testing.B) {
	r, _ := NewRunner(benchPipeline(20), Config{Defaults: validTiming()})
	s, err := r.Start(context.Background(), []Case{&stubCase{id: "one"}, &stubCase{id: "two"}})
	if err != nil {
		b.Fatal(err)
	}
	rep := s.Report()
	b.ReportAllocs()
	for b.Loop() {
		var buf bytes.Buffer
		if err := rep.WriteJSON(&buf); err != nil {
			b.Fatal(err)
		}
	}
}
