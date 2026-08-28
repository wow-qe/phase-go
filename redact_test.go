// Copyright 2026 The Phase Contributors
// SPDX-License-Identifier: MIT

package phase

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"reflect"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/wow-qe/phase-go/result"
)

// The QE workflow IS pasting reports into tickets. Redaction has to
// exist before any report leaves a laptop — and it has to actually reach
// every evidence carrier: Observation values, Result Expected/Actual, and
// raw error strings (DSNs and tokens ride adapter errors).

func secretSession(t *testing.T) *Session {
	t.Helper()
	r := mustRunner(t, Config{Defaults: validTiming()},
		&recordingPhase{stubPhase: stubPhase{id: "submit"}, do: func(_ context.Context, run *Run) error {
			run.Observe("request", map[string]any{
				"country": "BE",
				"headers": map[string]any{"Authorization": "Bearer sk-live-12345"},
			})
			run.Record(result.Compared("accepted", []bool{true}).
				WithExpected(map[string]any{"password": "hunter2", "rows": 3}).
				WithActual(map[string]any{"password": "hunter2", "rows": 3}))
			run.Fail(fmt.Errorf("postgres://qe:s3cr3t@db.internal:5432/x: connection refused"))
			return nil
		}},
	)
	return startSession(t, r, &stubCase{id: "leaky"})
}

func TestRedactRemovesSecretValuesByKeyAtAnyDepth(t *testing.T) {
	rep := secretSession(t).Report()
	rep.Redact("authorization", "password")
	cr := rep.Cases[0]
	blob := fmt.Sprintf("%v", cr)
	for _, secret := range []string{"sk-live-12345", "hunter2"} {
		if strings.Contains(blob, secret) {
			t.Fatalf("secret %q survived Redact", secret)
		}
	}
	// Redaction is surgical: siblings survive, and the redaction is visible.
	if !strings.Contains(blob, "BE") || !strings.Contains(blob, "3") {
		t.Fatal("non-secret evidence was destroyed alongside the secrets")
	}
	if !strings.Contains(blob, "[REDACTED]") {
		t.Fatal("redaction must be visible as redaction, not silent absence")
	}
}

func TestRedactMatchingScrubsErrorStringsAndReasons(t *testing.T) {
	rep := secretSession(t).Report()
	rep.RedactMatching(regexp.MustCompile(`postgres://[^\s]+`))
	blob := fmt.Sprintf("%v", rep.Cases[0].Errors)
	if strings.Contains(blob, "s3cr3t") {
		t.Fatalf("DSN survived RedactMatching in errors: %s", blob)
	}
	if !strings.Contains(blob, "connection refused") {
		t.Fatal("the non-secret part of the error must survive")
	}
}

func TestConfigRedactKeysAppliesAtReportBuild(t *testing.T) {
	// The ARCHITECTURE trust-boundary doc promised Config.RedactKeys before
	// anything read it — the "documented, read by nothing, inert" shape.
	// Now it is real: keys named in config are redacted in every Report().
	r := mustRunner(t, Config{Defaults: validTiming(), RedactKeys: []string{"authorization"}},
		&recordingPhase{stubPhase: stubPhase{id: "submit"}, do: func(_ context.Context, run *Run) error {
			run.Observe("request", map[string]any{"Authorization": "Bearer sk-live-999"})
			run.Record(result.Compared("ok", []bool{true}))
			return nil
		}},
	)
	rep := startSession(t, r, &stubCase{id: "auto"}).Report()
	if blob := fmt.Sprintf("%v", rep.Cases[0].Observations); strings.Contains(blob, "sk-live-999") {
		t.Fatalf("Config.RedactKeys did not apply at Report(): %s", blob)
	}
}

func TestUnredactableValueIsReplacedWhole(t *testing.T) {
	// A value that cannot be inspected (unmarshallable) cannot be verified
	// clean — the safe failure is to redact all of it, loudly.
	r := mustRunner(t, Config{Defaults: validTiming()},
		&recordingPhase{stubPhase: stubPhase{id: "submit"}, do: func(_ context.Context, run *Run) error {
			run.Observe("weird", make(chan int)) // json.Marshal fails on channels
			run.Record(result.Compared("ok", []bool{true}))
			return nil
		}},
	)
	rep := startSession(t, r, &stubCase{id: "opaque"}).Report()
	rep.Redact("anything")
	v := fmt.Sprint(rep.Cases[0].Observations[0].Value)
	if !strings.Contains(v, "REDACTED") {
		t.Fatalf("uninspectable value survived redaction: %v", v)
	}
}

func TestRedactMatchingScrubsPhaseOutcomeReasons(t *testing.T) {
	// A regression this test pins: land() copies the RAW adapter
	// error into PhaseOutcome.Reason, and no redaction path visited it — the
	// same DSN scrubbed in cr.Errors survived, duplicated, in cr.Phases.
	r := mustRunner(t, Config{Defaults: validTiming()},
		&recordingPhase{stubPhase: stubPhase{id: "submit"}, do: func(_ context.Context, run *Run) error {
			return fmt.Errorf("postgres://qe:s3cr3t@db.internal:5432/x: connection refused")
		}},
	)
	rep := startSession(t, r, &stubCase{id: "leaky"}).Report()
	rep.RedactMatching(regexp.MustCompile(`postgres://[^\s]+`))
	for _, po := range rep.Cases[0].Phases {
		if strings.Contains(po.Reason, "s3cr3t") {
			t.Fatalf("secret survived in PhaseOutcome.Reason: %q", po.Reason)
		}
	}
	var buf bytes.Buffer
	if err := rep.WriteJSON(&buf); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(buf.String(), "s3cr3t") {
		t.Fatal("secret reaches the JSON artifact after RedactMatching")
	}
}

func TestConfigRedactPatternsMakeTheAutomaticPathScrubStrings(t *testing.T) {
	// The automatic path must be safe by default for string carriers too:
	// key-based redaction cannot reach an error string, so Config grows
	// RedactPatterns, applied at every Report() alongside RedactKeys.
	r := mustRunner(t, Config{
		Defaults:       validTiming(),
		RedactPatterns: []string{`postgres://[^\s]+`, `Bearer [A-Za-z0-9._-]+`},
	},
		&recordingPhase{stubPhase: stubPhase{id: "submit"}, do: func(_ context.Context, run *Run) error {
			run.Observe("header", "Bearer sk-live-777")
			return fmt.Errorf("postgres://qe:s3cr3t@db.internal:5432/x: connection refused")
		}},
	)
	rep := startSession(t, r, &stubCase{id: "auto"}).Report()
	var buf bytes.Buffer
	if err := rep.WriteJSON(&buf); err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{"s3cr3t", "sk-live-777"} {
		if strings.Contains(buf.String(), secret) {
			t.Fatalf("secret %q reached the artifact without any hand call — the automatic path missed it", secret)
		}
	}
}

func TestInvalidRedactPatternIsRefusedAtConstruction(t *testing.T) {
	// A pattern that never compiled is a redaction that never ran — the
	// silent-config defect class; refuse it before anything executes.
	_, err := NewRunner(NewPipeline(&stubPhase{id: "submit"}),
		Config{Defaults: validTiming(), RedactPatterns: []string{"("}})
	var le *LoadError
	if !errors.As(err, &le) {
		t.Fatalf("err = %v, want *LoadError", err)
	}
}

func TestBareStringEvidenceValuesAreScrubbed(t *testing.T) {
	// The bare-string carrier: a DSN in WithActual (a bare string, no
	// map key to match) escaped RedactPatterns on BOTH surfaces. Pinned on
	// both: the report artifact and the live stream.
	dsn := "postgres://qe:s3cr3t@db.internal/x"
	r := mustRunner(t, Config{
		Defaults:       validTiming(),
		RedactPatterns: []string{`postgres://[^\s]+`},
	},
		&recordingPhase{stubPhase: stubPhase{id: "leaky"}, do: func(_ context.Context, run *Run) error {
			run.Record(result.Failed("conn check", "refused").WithExpected("reachable").WithActual(dsn))
			return nil
		}},
	)
	var streamed string
	WithObserver(func(ev Event) {
		if e, ok := ev.(CaseFinishedEvent); ok {
			streamed = fmt.Sprintf("%+v", e.Report)
		}
	})(r)
	s := startSession(t, r, &stubCase{id: "one"})
	var buf bytes.Buffer
	if err := s.Report().WriteJSON(&buf); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(buf.String(), "s3cr3t") {
		t.Fatal("the DSN reached the report artifact")
	}
	if strings.Contains(streamed, "s3cr3t") {
		t.Fatal("the DSN reached the live stream")
	}
	if !strings.Contains(buf.String(), "[REDACTED]") {
		t.Fatal("redaction must be visible as redaction")
	}
}

func TestEveryStringCarrierOnCaseReportIsScrubbed(t *testing.T) {
	// Reason-shaped carriers were found one at a time. This
	// test makes "is the surface exhausted" MECHANICAL: it walks CaseReport
	// by reflection for every string-typed field, requires each to be in
	// the covered set, and FAILS when a future field appears that
	// redactCasePattern was never taught about.
	covered := map[string]bool{
		"CaseReport.CaseID":                     false, // identity, framework-controlled
		"CaseReport.Correlation":                false, // framework-allocated
		"CaseReport.Reason":                     true,
		"CaseReport.FailedIn":                   false, // a phase ID
		"CaseReport.Phases.ID":                  false,
		"CaseReport.Phases.Reason":              true,
		"CaseReport.Phases.Stage":               false, // closed enum
		"CaseReport.Phases.DeclineSource":       false, // closed enum
		"CaseReport.Groups.GroupID":             false,
		"CaseReport.Groups.Reason":              true, // the fourth Reason-shaped carrier
		"CaseReport.Groups.Members":             false,
		"CaseReport.Results.Phase":              false,
		"CaseReport.Results.Source.Kind":        false,
		"CaseReport.Results.Source.ID":          false,
		"CaseReport.Results.Result.Name":        false, // stable identity: snapshot keys ride it
		"CaseReport.Results.Result.Reason":      true,
		"CaseReport.Results.Result.Expected":    true, // any-typed: string case scrubbed by pattern, map case by keys
		"CaseReport.Results.Result.Actual":      true,
		"CaseReport.Observations.Value":         true,
		"CaseReport.Errors.Phase":               false,
		"CaseReport.Errors.Source.Kind":         false,
		"CaseReport.Errors.Source.ID":           false,
		"CaseReport.Errors.Err":                 true,
		"CaseReport.Observations.Phase":         false,
		"CaseReport.Observations.Source.Kind":   false,
		"CaseReport.Observations.Source.ID":     false,
		"CaseReport.Observations.Name":          true,
		"CaseReport.DependencyFailure.CaseID":   false, // identity
		"CaseReport.Results.Result.Entity.Kind": false, // identity
		"CaseReport.Results.Result.Entity.ID":   false, // identity
	}
	var walk func(t2 reflect.Type, path string)
	seen := map[string]bool{}
	walk = func(t2 reflect.Type, path string) {
		switch t2.Kind() {
		case reflect.Pointer, reflect.Slice, reflect.Array:
			walk(t2.Elem(), path)
		case reflect.Struct:
			if t2 == reflect.TypeOf(time.Time{}) {
				return
			}
			for i := 0; i < t2.NumField(); i++ {
				f := t2.Field(i)
				if !f.IsExported() {
					continue
				}
				walk(f.Type, path+"."+f.Name)
			}
		case reflect.String:
			seen[path] = true
			if _, known := covered[path]; !known {
				t.Errorf("NEW string carrier %s: teach redactCasePattern about it (or record why it cannot carry a secret) and add it here", path)
			}
		case reflect.Interface:
			// Any-typed fields are exactly where
			// consumer-supplied values live (two of the four found carriers).
			// They cannot be walked statically, so each needs conscious
			// sign-off here just like string fields.
			seen[path] = true
			if _, known := covered[path]; !known {
				t.Errorf("NEW any-typed carrier %s: it holds consumer-supplied values - teach the redaction machinery about it and add it here", path)
			}
		}
	}
	walk(reflect.TypeOf(CaseReport{}), "CaseReport")
	for path := range covered {
		if !seen[path] {
			t.Errorf("covered set lists %s but the struct no longer has it", path)
		}
	}
	// And behaviorally: plant a distinct secret in every scrubbed carrier,
	// scrub, assert all gone in one pass.
	re := regexp.MustCompile(`SECRET[0-9]+`)
	cr := CaseReport{
		Reason: "SECRET1",
		Phases: []PhaseOutcome{{ID: "p", Reason: "SECRET2"}},
		Groups: []GroupOutcome{{GroupID: "g", Reason: "SECRET3"}},
		Results: []AttributedResult{{Phase: "p", Result: ResultView{
			Name: "n", Reason: "SECRET4", Expected: "SECRET5", Actual: "SECRET6"}}},
		Errors:       []AttributedError{{Phase: "p", Err: "SECRET7"}},
		Observations: []Observation{{Phase: "p", Name: "SECRET8", Value: "SECRET9"}},
	}
	redactCasePattern(&cr, re)
	if blob := fmt.Sprintf("%+v", cr); strings.Contains(blob, "SECRET") {
		t.Fatalf("a planted secret survived the one-pass scrub: %s", blob)
	}
}

func TestRedactMatchingReachesStructuredEvidence(t *testing.T) {
	// Found by the flagship example (the canary doing its job): a secret
	// riding INSIDE structured evidence — a slice element, a map key or
	// value, an EntityRef — survived RedactMatching, which only scrubbed
	// bare-string carriers. Free text is free text at any depth.
	r := mustRunner(t, Config{Defaults: validTiming()},
		&recordingPhase{stubPhase: stubPhase{id: "submit"}, do: func(_ context.Context, run *Run) error {
			run.Record(result.Compared("rows", []bool{true}).
				ForEntity(result.EntityRef{Kind: "db", ID: "postgres://user:s3cr3t@host/x"}).
				WithExpected([]string{"postgres://user:s3cr3t@host/a"}).
				WithActual(map[string]any{
					"postgres://user:s3cr3t@host/key": "postgres://user:s3cr3t@host/val",
				}))
			run.Observe("endpoints", []string{"postgres://user:s3cr3t@host/obs"})
			return nil
		}},
	)
	rep := startSession(t, r, &stubCase{id: "deep"}).Report()
	rep.RedactMatching(regexp.MustCompile(`postgres://[^\s"]+`))
	var buf bytes.Buffer
	if err := rep.WriteJSON(&buf); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(buf.String(), "s3cr3t") {
		t.Fatalf("a structured carrier leaked the secret past RedactMatching:\n%s", buf.String())
	}
	if !strings.Contains(buf.String(), redacted) {
		t.Fatal("redaction must leave its visible marker")
	}
}
