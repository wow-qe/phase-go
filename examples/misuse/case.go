// Copyright 2026 The Phase Contributors
// SPDX-License-Identifier: MIT

package misuse

import (
	_ "embed"
	"fmt"

	phase "github.com/wow-qe/phase-go"
	config "github.com/wow-qe/phase-go/x/config"
)

// Manifest is the suite-as-data: declaration in YAML, behavior in Go.
//
//go:embed cases.yaml
var Manifest []byte

// checkoutCase is the consumer factory over the YAML manifest: the
// loader owns declaration, the consumer owns behavior.
// It embeds SpecCase for the declarative surface — Selects, Timing,
// Exclusive, Fixtures, Tags — and adds the domain payload plus case
// dependencies, which are behavior the manifest cannot carry.
type checkoutCase struct {
	*config.SpecCase
	deps []phase.CaseRequirement
}

// Scenario and Entities read the opaque Params the loader passed through.
func (c *checkoutCase) Scenario() string {
	if s, ok := c.Params()["scenario"].(string); ok {
		return s
	}
	return "happy"
}

func (c *checkoutCase) Entities() int {
	if n, ok := c.Params()["entities"].(int); ok {
		return n
	}
	return 1
}

// DependsOnCases implements the optional case-DAG contract.
func (c *checkoutCase) DependsOnCases() []phase.CaseRequirement { return c.deps }

// loadCases builds the suite from the manifest: registry-resolved
// fixtures, consumer wrapping, and the one case dependency the domain
// declares (billing-report waits for the multi-entity flow to pass).
func loadCases(sys *checkoutSystem, manifest []byte) ([]phase.Case, error) {
	specs, err := config.ParseCases(manifest)
	if err != nil {
		return nil, err
	}
	registry := config.Registry{
		"seed-catalog": func() phase.Fixture { return &catalogFixture{sys} },
	}
	var out []phase.Case
	for _, spec := range specs {
		sc, err := spec.Case(registry)
		if err != nil {
			return nil, err
		}
		cc := &checkoutCase{SpecCase: sc}
		if spec.ID == "billing-report" {
			cc.deps = []phase.CaseRequirement{{
				CaseID:     "happy-multi",
				Acceptable: []phase.Status{phase.Passed, phase.Flaked},
			}}
		}
		out = append(out, cc)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("empty manifest")
	}
	return out, nil
}
