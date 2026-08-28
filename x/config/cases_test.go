// Copyright 2026 The Phase Contributors
// SPDX-License-Identifier: MIT

package config_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	phase "github.com/wow-qe/phase-go"
	"github.com/wow-qe/phase-go/result"
	config "github.com/wow-qe/phase-go/x/config"
)

// A quality engineer who is not Go-fluent authors cases in YAML. The loader
// is scaffolding for a consumer factory — spec + fixture-name registry —
// never a generic Case decoder: behavior stays with the consumer,
// declaration becomes data. Every refusal is a typed LoadError at load
// time, because a manifest typo that silently does nothing is the defect
// class this whole library exists against.

const manifest = `
cases:
  - id: checkout-declined
    status: active
    declines:
      provider_side: "rejected at ingest; never reaches the provider"
    timing:
      settle:
        attempts: 40
        interval: 15s
    exclusive: "mutates the shared ledger"
    fixtures: [seed-catalogue]
    params:
      country: BE
  - id: parked
    status: quarantined
    tags: [smoke, quarantine-review]
`

type nopFixture struct{}

func (nopFixture) Setup(context.Context, *phase.Run) error    { return nil }
func (nopFixture) Teardown(context.Context, *phase.Run) error { return nil }

func testRegistry() config.Registry {
	return config.Registry{"seed-catalogue": func() phase.Fixture { return nopFixture{} }}
}

func TestParseCasesReadsTheDeclarativeSurface(t *testing.T) {
	specs, err := config.ParseCases([]byte(manifest))
	if err != nil {
		t.Fatalf("ParseCases: %v", err)
	}
	if len(specs) != 2 {
		t.Fatalf("specs = %d", len(specs))
	}
	s := specs[0]
	if s.ID != "checkout-declined" || s.Status != phase.Active {
		t.Fatalf("spec = %+v", s)
	}
	if s.Declines["provider_side"] == "" {
		t.Fatal("decline reason lost")
	}
	if s.Timing["settle"].Attempts != 40 || s.Timing["settle"].Interval != 15*time.Second {
		t.Fatalf("timing = %+v", s.Timing["settle"])
	}
	if s.Exclusive != "mutates the shared ledger" || s.Params["country"] != "BE" {
		t.Fatalf("spec = %+v", s)
	}
	if specs[1].Status != phase.Quarantined {
		t.Fatalf("second spec status = %v", specs[1].Status)
	}
}

func TestSpecCaseCarriesTagsIntoSuiteSelection(t *testing.T) {
	specs, err := config.ParseCases([]byte(manifest))
	if err != nil {
		t.Fatal(err)
	}
	var cases []phase.Case
	for _, s := range specs {
		c, err := s.Case(testRegistry())
		if err != nil {
			t.Fatal(err)
		}
		cases = append(cases, c)
	}
	picked, err := phase.SelectByTags(cases, "smoke")
	if err != nil {
		t.Fatalf("SelectByTags: %v", err)
	}
	if len(picked) != 1 || picked[0].ID() != "parked" {
		t.Fatalf("picked = %v — manifest tags must drive suite selection", picked)
	}
}

func TestSpecCaseImplementsTheCaseContract(t *testing.T) {
	specs, err := config.ParseCases([]byte(manifest))
	if err != nil {
		t.Fatal(err)
	}
	c, err := specs[0].Case(testRegistry())
	if err != nil {
		t.Fatalf("Case: %v", err)
	}
	if selected, reason := c.Selects("provider_side"); selected || !strings.Contains(reason, "ingest") {
		t.Fatalf("Selects = %v %q", selected, reason)
	}
	if selected, reason := c.Selects("anything_else"); !selected || reason != "" {
		t.Fatalf("undeclined phase must select: %v %q", selected, reason)
	}
	if tm, ok := c.Timing("settle"); !ok || tm.Attempts != 40 {
		t.Fatalf("Timing = %+v %v", tm, ok)
	}
	if _, ok := c.Timing("submit"); ok {
		t.Fatal("no override declared for submit")
	}
	if ex, reason := c.Exclusive(); !ex || reason == "" {
		t.Fatalf("Exclusive = %v %q", ex, reason)
	}
	if len(c.Fixtures()) != 1 {
		t.Fatalf("fixtures = %v", c.Fixtures())
	}
}

func TestSpecCaseRunsThroughARealRunner(t *testing.T) {
	specs, err := config.ParseCases([]byte(manifest))
	if err != nil {
		t.Fatal(err)
	}
	c, err := specs[0].Case(testRegistry())
	if err != nil {
		t.Fatal(err)
	}
	p := phase.NewPipeline(phase.Func{PhaseID: "submit", Do: func(ctx context.Context, r *phase.Run) error {
		// the consumer reads its payload back off the spec case
		params := r.Case().(*config.SpecCase).Params()
		r.Record(result.Compared("country declared", []bool{params["country"] == "BE"}))
		return nil
	}})
	runner, err := phase.NewRunner(p, phase.Config{Defaults: phase.Timing{Attempts: 1, Interval: time.Millisecond}})
	if err != nil {
		t.Fatal(err)
	}
	s, err := runner.Start(context.Background(), []phase.Case{c})
	if err != nil {
		t.Fatal(err)
	}
	if got := s.Cases()[0].Status; got != phase.Passed {
		t.Fatalf("status = %s", got)
	}
}

func TestCaseManifestRefusals(t *testing.T) {
	refusals := map[string]string{
		"missing id": `
cases:
  - status: active
`,
		"duplicate id": `
cases:
  - id: one
  - id: one
`,
		"decline without reason": `
cases:
  - id: one
    declines:
      settle: ""
`,
		"unknown key": `
cases:
  - id: one
    exclusivity: "typo of exclusive"
`,
		"unparsable status": `
cases:
  - id: one
    status: enabledish
`,
	}
	for name, doc := range refusals {
		t.Run(name, func(t *testing.T) {
			_, err := config.ParseCases([]byte(doc))
			var le *phase.LoadError
			if !errors.As(err, &le) {
				t.Fatalf("err = %v, want a typed *phase.LoadError", err)
			}
		})
	}
}

func TestUnknownFixtureNameIsATypedRefusal(t *testing.T) {
	specs, err := config.ParseCases([]byte(`
cases:
  - id: one
    fixtures: [no-such-fixture]
`))
	if err != nil {
		t.Fatal(err)
	}
	_, err = specs[0].Case(testRegistry())
	var le *phase.LoadError
	if !errors.As(err, &le) {
		t.Fatalf("err = %v, want *phase.LoadError — a fixture typo that silently does nothing is the defect class", err)
	}
	if !strings.Contains(err.Error(), "no-such-fixture") || !strings.Contains(err.Error(), "seed-catalogue") {
		t.Fatalf("refusal %q must name the typo and the available names", err)
	}
}
