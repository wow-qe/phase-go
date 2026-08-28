// Copyright 2026 The Phase Contributors
// SPDX-License-Identifier: MIT

package phase

import (
	"context"
	"errors"
	"testing"
	"time"
)

// Preflight refuses every way a suite can be mis-declared, with a machine-
// readable code, before anything executes. One test per code exercises
// every LoadCode through this door.
//
// (One exception: StatusUnparsable belongs to the consumer's loader —
// ParseStatus refuses it there, tested in phase_test.go — because by the
// time a Case reaches Preflight its status is already typed.)

// stubPhase implements Interface with declared wiring and no behaviour.
type stubPhase struct {
	id       ID
	deps     []ID
	produces []KeyID
	requires []KeyID
	applies  Applicability
}

func (p *stubPhase) ID() ID            { return p.id }
func (p *stubPhase) DependsOn() []ID   { return p.deps }
func (p *stubPhase) Produces() []KeyID { return p.produces }
func (p *stubPhase) Requires() []KeyID { return p.requires }
func (p *stubPhase) AppliesTo(Case, Config) Applicability {
	if p.applies == (Applicability{}) {
		return Applies()
	}
	return p.applies
}
func (p *stubPhase) Run(context.Context, *Run) error { return nil }

// stubCase implements Case with configurable answers.
type stubCase struct {
	id        string
	status    CaseStatus
	selects   func(ID) (bool, string)
	timing    map[ID]Timing
	fixtures  []Fixture
	exclusive bool
	exclWhy   string
}

func (c *stubCase) ID() string         { return c.id }
func (c *stubCase) Status() CaseStatus { return c.status }
func (c *stubCase) Selects(id ID) (bool, string) {
	if c.selects == nil {
		return true, ""
	}
	return c.selects(id)
}
func (c *stubCase) Timing(id ID) (Timing, bool) {
	t, ok := c.timing[id]
	return t, ok
}
func (c *stubCase) Fixtures() []Fixture       { return c.fixtures }
func (c *stubCase) Exclusive() (bool, string) { return c.exclusive, c.exclWhy }

func validTiming() Timing {
	return Timing{Attempts: 3, Interval: time.Second, Timeout: time.Minute}
}

func mustRunner(t *testing.T, cfg Config, phases ...Interface) *Runner {
	t.Helper()
	r, err := NewRunner(NewPipeline(phases...), cfg)
	if err != nil {
		t.Fatalf("NewRunner: %v", err)
	}
	return r
}

func wantCode(t *testing.T, err error, code LoadCode) *LoadError {
	t.Helper()
	var le *LoadError
	if !errors.As(err, &le) {
		t.Fatalf("want *LoadError(%s), got %v", code, err)
	}
	if le.Code != code {
		t.Fatalf("code = %s, want %s (err: %v)", le.Code, code, le)
	}
	return le
}

// --- structural refusals: NewRunner ---------------------------------------

func TestDuplicatePhaseIDRefused(t *testing.T) {
	_, err := NewRunner(NewPipeline(
		&stubPhase{id: "submit"}, &stubPhase{id: "submit"},
	), Config{Defaults: validTiming()})
	_ = wantCode(t, err, DuplicatePhaseID)
}

func TestUnknownDependencyRefused(t *testing.T) {
	_, err := NewRunner(NewPipeline(
		&stubPhase{id: "settle", deps: []ID{"discover"}},
	), Config{Defaults: validTiming()})
	le := wantCode(t, err, UnknownDependency)
	if le.Subject != "settle" {
		t.Fatalf("subject = %q", le.Subject)
	}
}

func TestDependencyCycleRefused(t *testing.T) {
	_, err := NewRunner(NewPipeline(
		&stubPhase{id: "a", deps: []ID{"b"}},
		&stubPhase{id: "b", deps: []ID{"a"}},
	), Config{Defaults: validTiming()})
	_ = wantCode(t, err, DependencyCycle)
}

func TestKeyNeverProducedRefused(t *testing.T) {
	// settle requires a key produced only by a phase it does not depend on:
	// with no ordering guarantee the value may not exist when settle runs, so
	// the wiring is refused at load time rather than risking a zero-value
	// read at run time.
	_, err := NewRunner(NewPipeline(
		&stubPhase{id: "discover", produces: []KeyID{"request_id"}},
		&stubPhase{id: "settle", requires: []KeyID{"request_id"}}, // no dep!
	), Config{Defaults: validTiming()})
	le := wantCode(t, err, KeyNeverProduced)
	if le.Subject != "settle" {
		t.Fatalf("subject = %q", le.Subject)
	}
}

func TestKeyProducedInTransitiveDepIsFine(t *testing.T) {
	_, err := NewRunner(NewPipeline(
		&stubPhase{id: "submit", produces: []KeyID{"request_id"}},
		&stubPhase{id: "discover", deps: []ID{"submit"}},
		&stubPhase{id: "settle", deps: []ID{"discover"}, requires: []KeyID{"request_id"}},
	), Config{Defaults: validTiming()})
	if err != nil {
		t.Fatalf("transitive production must satisfy: %v", err)
	}
}

func TestDuplicateKeyProducerRefused(t *testing.T) {
	_, err := NewRunner(NewPipeline(
		&stubPhase{id: "a", produces: []KeyID{"request_id"}},
		&stubPhase{id: "b", produces: []KeyID{"request_id"}},
	), Config{Defaults: validTiming()})
	_ = wantCode(t, err, DuplicateKeyProducer)
}

// --- configuration refusals: NewRunner -------------------------------------

func TestUnknownPhaseInConfigRefused(t *testing.T) {
	_, err := NewRunner(NewPipeline(&stubPhase{id: "submit"}), Config{
		Defaults: validTiming(),
		Phases:   map[ID]Settings{"submitt": {}}, // typo'd — or deleted without cleanup
	})
	le := wantCode(t, err, UnknownPhaseInConfig)
	if le.Subject != "submitt" {
		t.Fatalf("subject = %q", le.Subject)
	}
}

func TestResolvedTimingMustBeRunnable(t *testing.T) {
	// Zero attempts after inheritance means WaitUntil could never run. The
	// clamp alternative is a silent default; refusal names the phase.
	_, err := NewRunner(NewPipeline(&stubPhase{id: "submit"}), Config{
		Defaults: Timing{Attempts: 0, Interval: time.Second},
	})
	_ = wantCode(t, err, TimingInvalid)
}

func TestConfigTimingOverridesFieldWise(t *testing.T) {
	r := mustRunner(t, Config{
		Defaults: validTiming(),
		Phases:   map[ID]Settings{"submit": {Timing: Timing{Attempts: 10}}},
	}, &stubPhase{id: "submit"})
	got := r.resolvedTiming("submit")
	if got.Attempts != 10 {
		t.Fatalf("Attempts = %d, want the override", got.Attempts)
	}
	if got.Interval != time.Second {
		t.Fatalf("Interval = %v, want inherited from Defaults", got.Interval)
	}
}

func TestDependenciesUnionCodeAndConfig(t *testing.T) {
	// Code declares a phase's true prerequisites; config may add ordering
	// (an operator serialising two phases) but can never remove one — a
	// config that could delete a code-declared dependency could reorder a
	// pipeline into nonsense without touching a line of it.
	_, err := NewRunner(NewPipeline(
		&stubPhase{id: "a"},
		&stubPhase{id: "b"}, // no code dep
	), Config{
		Defaults: validTiming(),
		Phases:   map[ID]Settings{"b": {DependsOn: []ID{"missing"}}},
	})
	_ = wantCode(t, err, UnknownDependency) // config-added deps are validated too
}

// --- case refusals: Preflight ----------------------------------------------

func TestSkipWithoutReasonRefused(t *testing.T) {
	r := mustRunner(t, Config{Defaults: validTiming()}, &stubPhase{id: "submit"})
	err := r.Preflight([]Case{&stubCase{
		id:      "negative_bad_country",
		selects: func(ID) (bool, string) { return false, "" },
	}})
	le := wantCode(t, err, SkipWithoutReason)
	if le.Subject != "negative_bad_country" {
		t.Fatalf("subject = %q", le.Subject)
	}
}

func TestExclusiveWithoutReasonRefused(t *testing.T) {
	r := mustRunner(t, Config{Defaults: validTiming()}, &stubPhase{id: "submit"})
	err := r.Preflight([]Case{&stubCase{id: "big", exclusive: true}})
	_ = wantCode(t, err, ExclusiveWithoutReason)
}

func TestNilFixtureRefused(t *testing.T) {
	r := mustRunner(t, Config{Defaults: validTiming()}, &stubPhase{id: "submit"})
	err := r.Preflight([]Case{&stubCase{id: "seeded", fixtures: []Fixture{nil}}})
	_ = wantCode(t, err, FixtureNil)
}

func TestScopeCollisionRefused(t *testing.T) {
	r := mustRunner(t, Config{Defaults: validTiming()}, &stubPhase{id: "submit"})
	r.allocator = allocatorFunc(func(c Case) (Scope, error) {
		return Scope{CaseID: c.ID(), Correlation: "SAME"}, nil // consumer allocator collides
	})
	err := r.Preflight([]Case{&stubCase{id: "a"}, &stubCase{id: "b"}})
	_ = wantCode(t, err, ScopeCollision)
}

func TestACleanSuitePassesPreflight(t *testing.T) {
	r := mustRunner(t, Config{Defaults: validTiming()},
		&stubPhase{id: "submit", produces: []KeyID{"request_id"}},
		&stubPhase{id: "settle", deps: []ID{"submit"}, requires: []KeyID{"request_id"}},
	)
	err := r.Preflight([]Case{
		&stubCase{id: "happy"},
		&stubCase{id: "negative", selects: func(id ID) (bool, string) {
			if id == "settle" {
				return false, "rejected at ingest; never settles"
			}
			return true, ""
		}},
		&stubCase{id: "big", exclusive: true, exclWhy: "arrangement cannot be scoped"},
	})
	if err != nil {
		t.Fatalf("clean suite refused: %v", err)
	}
}

func TestDefaultScopesNeverCollide(t *testing.T) {
	r := mustRunner(t, Config{Defaults: validTiming()}, &stubPhase{id: "submit"})
	cases := make([]Case, 50)
	for i := range cases {
		cases[i] = &stubCase{id: string(rune('a'+i%26)) + string(rune('0'+i/26))}
	}
	if err := r.Preflight(cases); err != nil {
		t.Fatalf("default allocator collided: %v", err)
	}
}

func TestWithScopeAllocatorInjectsTheConsumersAllocator(t *testing.T) {
	// WithScopeAllocator must wire a caller-provided allocator into
	// Preflight; documenting an extension point without a working option
	// to set it leaves the API inert.
	called := 0
	r, err := NewRunner(NewPipeline(&stubPhase{id: "submit"}),
		Config{Defaults: validTiming()},
		WithScopeAllocator(allocatorFunc(func(c Case) (Scope, error) {
			called++
			return Scope{CaseID: c.ID(), Correlation: "fixed-" + c.ID()}, nil
		})),
	)
	if err != nil {
		t.Fatalf("NewRunner: %v", err)
	}
	if err := r.Preflight([]Case{&stubCase{id: "a"}, &stubCase{id: "b"}}); err != nil {
		t.Fatalf("Preflight: %v", err)
	}
	if called == 0 {
		t.Fatal("the injected allocator was never consulted")
	}
}
