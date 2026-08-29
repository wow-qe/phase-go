// Copyright 2026 The Phase Contributors
// SPDX-License-Identifier: MIT

package phase

import (
	"bytes"
	"context"
	"encoding/json"
	"regexp"
	"testing"

	"github.com/wow-qe/phase-go/result"
)

// Fuzz targets for the invariant-rich surfaces. Each seed corpus runs as
// part of the ordinary test suite; `make fuzz` explores further.

// FuzzSelectByTags: the expression parser must never panic, and a
// well-formed expression must evaluate without error against any tag set.
func FuzzSelectByTags(f *testing.F) {
	for _, seed := range []string{
		"smoke", "smoke && !slow", "(a || b) && !c", "a&&", "!(", ")", "a || (b && c)", "", "a b", "!!!a",
	} {
		f.Add(seed)
	}
	cases := []Case{
		&taggedCase{stubCase: stubCase{id: "one"}, tags: []string{"smoke", "a"}},
		&taggedCase{stubCase: stubCase{id: "two"}, tags: []string{"b"}},
	}
	f.Fuzz(func(t *testing.T, expr string) {
		selected, err := SelectByTags(cases, expr)
		if err != nil {
			return // refusal is the contract for malformed or matchless input
		}
		if len(selected) == 0 {
			t.Fatalf("nil error with zero selection for %q — ErrNoMatch must cover it", expr)
		}
	})
}

// FuzzVerifyReportJSON: arbitrary bytes decoded as a report must never
// panic Verify — corruption is answered with an error, not a crash.
func FuzzVerifyReportJSON(f *testing.F) {
	rep := freshReportForFuzz()
	var buf bytes.Buffer
	_ = rep.WriteJSON(&buf)
	f.Add(buf.Bytes())
	f.Add([]byte(`{}`))
	f.Add([]byte(`{"schema_version":"1","cases":[{"id":"a","status":"passed"}]}`))
	f.Add([]byte(`{"schema_version":"1","summary":{"total":9}}`))
	f.Fuzz(func(t *testing.T, data []byte) {
		var r Report
		if err := json.Unmarshal(data, &r); err != nil {
			return
		}
		_ = r.Verify() // must not panic; any verdict is acceptable
	})
}

// FuzzRedactMatching: pattern redaction over evidence built from arbitrary
// strings must never panic, and must be idempotent — a second pass changes
// nothing.
func FuzzRedactMatching(f *testing.F) {
	f.Add("postgres://user:secret@host/db", "plain value")
	f.Add("", "")
	f.Add("secret\x00control\r\n", "nested")
	re := regexp.MustCompile(`secret[a-z0-9]*`)
	f.Fuzz(func(t *testing.T, a, b string) {
		rep := reportWith(t, a, b)
		rep.RedactMatching(re)
		var once bytes.Buffer
		if err := rep.WriteJSON(&once); err != nil {
			t.Fatalf("WriteJSON after redaction: %v", err)
		}
		rep.RedactMatching(re)
		var twice bytes.Buffer
		_ = rep.WriteJSON(&twice)
		if !bytes.Equal(once.Bytes(), twice.Bytes()) {
			t.Fatal("redaction is not idempotent")
		}
	})
}

func reportWith(t *testing.T, a, b string) *Report {
	t.Helper()
	r := mustRunner(t, Config{Defaults: validTiming()},
		&recordingPhase{stubPhase: stubPhase{id: "p"}, do: func(_ context.Context, run *Run) error {
			run.Observe("obs", map[string]any{a: []any{b, map[string]any{"k": a}}})
			run.Record(result.Compared("c", []bool{true}).WithExpected(a).WithActual([]string{b}))
			run.Fail(nil)
			return nil
		}},
	)
	return startSession(t, r, &stubCase{id: "one"}).Report()
}

func freshReportForFuzz() *Report {
	// A minimal valid report produced by a real run, used as a seed.
	r, err := NewRunner(NewPipeline(passingPhaseForFuzz()), Config{Defaults: Timing{Attempts: 1}})
	if err != nil {
		panic(err)
	}
	s, err := r.Start(context.Background(), []Case{&stubCase{id: "seed"}})
	if err != nil {
		panic(err)
	}
	return s.Report()
}

func passingPhaseForFuzz() Interface {
	return &recordingPhase{stubPhase: stubPhase{id: "p"}, do: func(_ context.Context, run *Run) error {
		run.Record(result.Compared("ok", []bool{true}))
		return nil
	}}
}
