// Copyright 2026 The Phase Contributors
// SPDX-License-Identifier: MIT

package config

import (
	"fmt"
	"os"
	"sort"
	"strings"

	phase "github.com/wow-qe/phase-go"
	"gopkg.in/yaml.v3"
)

// Case manifests: the declarative half of a case as YAML, so a quality
// engineer who is not Go-fluent can author one. This is scaffolding for a
// consumer factory — spec + fixture-name registry — never a generic Case
// decoder: behaviour (adapters, comparators, params semantics) stays in the
// consumer's code; declaration becomes data. Every refusal is a typed
// *phase.LoadError at load time, because a manifest typo that silently does
// nothing is the defect class this library exists against.

// CaseSpec is one case's declarative surface, as loaded.
type CaseSpec struct {
	ID        string
	Status    phase.CaseStatus
	Declines  map[phase.ID]string // phase → reason this case selects out of it
	Timing    map[phase.ID]phase.Timing
	Exclusive string         // reason; empty = not exclusive
	Fixtures  []string       // names, resolved through a Registry
	Tags      []string       // suite tags (phase.Tagged / phase.SelectByTags)
	Params    map[string]any // consumer payload; opaque to the loader
}

// Registry maps fixture names to constructors — the consumer's half of the
// factory: it owns which fixtures exist and what the names mean.
type Registry map[string]func() phase.Fixture

// LoadCases reads a case manifest file. File errors are environment
// problems (wrapped os errors); everything declarative is a LoadError.
func LoadCases(path string) ([]CaseSpec, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("config: %w", err)
	}
	return ParseCases(data)
}

// ParseCases is LoadCases for bytes already in hand.
func ParseCases(data []byte) ([]CaseSpec, error) {
	var root yaml.Node
	if err := yaml.Unmarshal(data, &root); err != nil {
		return nil, fmt.Errorf("config: %w", err)
	}
	if len(root.Content) == 0 || root.Content[0].Tag == "!!null" {
		return nil, nil
	}
	doc := root.Content[0]
	if err := refuseDuplicateKeys(doc, ""); err != nil {
		return nil, err
	}
	if doc.Kind != yaml.MappingNode {
		return nil, &phase.LoadError{Code: phase.UnknownConfigKey, Subject: "(document)",
			Detail: "top level must be a mapping with a cases list"}
	}
	var specs []CaseSpec
	for key, val := range pairs(doc) {
		switch key.Value {
		case "cases":
			if val.Kind != yaml.SequenceNode {
				return nil, &phase.LoadError{Code: phase.UnknownConfigKey, Subject: "cases",
					Detail: "cases must be a list"}
			}
			seen := map[string]bool{}
			for i, item := range val.Content {
				spec, err := decodeCase(item, fmt.Sprintf("cases[%d]", i))
				if err != nil {
					return nil, err
				}
				if seen[spec.ID] {
					return nil, &phase.LoadError{Code: phase.DuplicateCaseID, Subject: spec.ID,
						Detail: "two cases in the manifest share this ID; they would be indistinguishable in the report"}
				}
				seen[spec.ID] = true
				specs = append(specs, spec)
			}
		default:
			return nil, unknownKey(key.Value, key.Value)
		}
	}
	return specs, nil
}

func decodeCase(n *yaml.Node, path string) (CaseSpec, error) {
	spec := CaseSpec{Status: phase.Active}
	if n.Kind != yaml.MappingNode {
		return spec, &phase.LoadError{Code: phase.UnknownConfigKey, Subject: path,
			Detail: "a case must be a mapping"}
	}
	for key, val := range pairs(n) {
		switch key.Value {
		case "id":
			spec.ID = val.Value
		case "status":
			st, err := phase.ParseStatus(val.Value)
			if err != nil {
				return spec, err
			}
			spec.Status = st
		case "declines":
			if val.Kind != yaml.MappingNode {
				return spec, unknownKey(path+".declines", "declines must map phase → reason")
			}
			spec.Declines = map[phase.ID]string{}
			for pk, pv := range pairs(val) {
				if pv.Value == "" {
					return spec, &phase.LoadError{Code: phase.SkipWithoutReason, Subject: spec.ID,
						Detail: fmt.Sprintf("%s.declines.%s: a decline with no reason is indistinguishable from a check that passed", path, pk.Value)}
				}
				spec.Declines[phase.ID(pk.Value)] = pv.Value
			}
		case "timing":
			if val.Kind != yaml.MappingNode {
				return spec, unknownKey(path+".timing", "timing must map phase → timing")
			}
			spec.Timing = map[phase.ID]phase.Timing{}
			for pk, pv := range pairs(val) {
				var t phase.Timing
				if err := decodeTiming(pv, path+".timing."+pk.Value, &t); err != nil {
					return spec, err
				}
				spec.Timing[phase.ID(pk.Value)] = t
			}
		case "exclusive":
			spec.Exclusive = val.Value
		case "fixtures":
			if err := val.Decode(&spec.Fixtures); err != nil {
				return spec, unknownKey(path+".fixtures", "fixtures must be a list of names")
			}
		case "tags":
			if err := val.Decode(&spec.Tags); err != nil {
				return spec, unknownKey(path+".tags", "tags must be a list of names")
			}
		case "params":
			if err := val.Decode(&spec.Params); err != nil {
				return spec, unknownKey(path+".params", "params must be a mapping")
			}
		default:
			return spec, unknownKey(path, key.Value)
		}
	}
	if spec.ID == "" {
		return spec, &phase.LoadError{Code: phase.CaseIDMissing, Subject: path,
			Detail: "every case needs an id — an anonymous case cannot be found in a report"}
	}
	return spec, nil
}

// Case resolves the spec against the registry into a runnable phase.Case.
// An unknown fixture name refuses with the available names listed — a typo
// that silently dropped a fixture would corrupt every case after it.
func (s CaseSpec) Case(reg Registry) (*SpecCase, error) {
	fixtures := make([]phase.Fixture, 0, len(s.Fixtures))
	for _, name := range s.Fixtures {
		ctor, ok := reg[name]
		if !ok {
			known := make([]string, 0, len(reg))
			for k := range reg {
				known = append(known, k)
			}
			sort.Strings(known)
			return nil, &phase.LoadError{Code: phase.UnknownFixture, Subject: s.ID,
				Detail: fmt.Sprintf("fixture %q is not registered; available: %s", name, strings.Join(known, ", "))}
		}
		fixtures = append(fixtures, ctor())
	}
	return &SpecCase{spec: s, fixtures: fixtures}, nil
}

// SpecCase is a CaseSpec bound to its fixtures — the loader's half of the
// consumer factory. Consumers embed it (or wrap it) and read Params() for
// their payload; behaviour never lives in the manifest.
type SpecCase struct {
	spec     CaseSpec
	fixtures []phase.Fixture
}

func (c *SpecCase) ID() string               { return c.spec.ID }
func (c *SpecCase) Status() phase.CaseStatus { return c.spec.Status }

func (c *SpecCase) Selects(id phase.ID) (bool, string) {
	if reason, declined := c.spec.Declines[id]; declined {
		return false, reason
	}
	return true, ""
}

func (c *SpecCase) Timing(id phase.ID) (phase.Timing, bool) {
	t, ok := c.spec.Timing[id]
	return t, ok
}

func (c *SpecCase) Fixtures() []phase.Fixture { return c.fixtures }

func (c *SpecCase) Exclusive() (bool, string) {
	return c.spec.Exclusive != "", c.spec.Exclusive
}

// Params is the consumer payload, verbatim from the manifest.
func (c *SpecCase) Params() map[string]any { return c.spec.Params }

// Tags implements phase.Tagged, so a manifest-authored case joins suites
// (phase.SelectByTags) like any hand-written one.
func (c *SpecCase) Tags() []string { return c.spec.Tags }
