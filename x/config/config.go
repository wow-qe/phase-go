// Copyright 2026 The Phase Contributors
// SPDX-License-Identifier: MIT

// Package config loads phase.Config from YAML files. It lives outside the
// core module so the core keeps its no-dependency rule; the YAML dependency
// stops here.
//
// Unknown keys are a load-time error (*phase.LoadError, code
// unknown_config_key): a typo in a config file must stop the run, because a
// silently-dropped key is how an operator kill-switch becomes inert
// (unknown keys are load-time errors).
package config

import (
	"fmt"
	"os"
	"strconv"
	"time"

	phase "github.com/wow-qe/phase-go"
	"gopkg.in/yaml.v3"
)

// Load reads a YAML file into a phase.Config. A missing or unreadable file is
// a wrapped os error, not a LoadError — the file not existing is an
// environment problem, not a declaration problem. An empty file is a zero
// Config and nil error.
func Load(path string) (phase.Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return phase.Config{}, fmt.Errorf("config: %w", err)
	}
	return Parse(data)
}

// Parse is Load for bytes already in hand.
func Parse(data []byte) (phase.Config, error) {
	var root yaml.Node
	if err := yaml.Unmarshal(data, &root); err != nil {
		return phase.Config{}, fmt.Errorf("config: %w", err)
	}
	if len(root.Content) == 0 {
		return phase.Config{}, nil // empty document
	}
	doc := root.Content[0]
	if doc.Tag == "!!null" {
		return phase.Config{}, nil // e.g. a file of only comments
	}
	// Duplicate keys anywhere in the document are refused before anything is
	// decoded. yaml.v3 is last-wins by default, which means an operator can
	// edit the WRONG duplicate and nothing says so — the same silent-config
	// class as an unknown key, so it gets the same answer. One recursive
	// check at the root covers every present and future section.
	if err := refuseDuplicateKeys(doc, ""); err != nil {
		return phase.Config{}, err
	}
	if doc.Kind != yaml.MappingNode {
		return phase.Config{}, &phase.LoadError{
			Code:    phase.UnknownConfigKey,
			Subject: "(document)",
			Detail:  "top level must be a mapping of defaults/phases",
		}
	}

	var cfg phase.Config
	for key, val := range pairs(doc) {
		switch key.Value {
		case "defaults":
			if err := decodeTiming(val, "defaults", &cfg.Defaults); err != nil {
				return phase.Config{}, err
			}
		case "phases":
			m, err := decodePhases(val)
			if err != nil {
				return phase.Config{}, err
			}
			cfg.Phases = m
		default:
			return phase.Config{}, unknownKey(key.Value, key.Value)
		}
	}
	return cfg, nil
}

// pairs iterates a mapping node's key/value node pairs.
func pairs(m *yaml.Node) func(yield func(*yaml.Node, *yaml.Node) bool) {
	return func(yield func(*yaml.Node, *yaml.Node) bool) {
		for i := 0; i+1 < len(m.Content); i += 2 {
			if !yield(m.Content[i], m.Content[i+1]) {
				return
			}
		}
	}
}

func unknownKey(path, key string) *phase.LoadError {
	return &phase.LoadError{
		Code:    phase.UnknownConfigKey,
		Subject: path,
		Detail:  fmt.Sprintf("unknown config key %q", key),
	}
}

func decodePhases(n *yaml.Node) (map[phase.ID]phase.Settings, error) {
	if n.Tag == "!!null" {
		return nil, nil
	}
	if n.Kind != yaml.MappingNode {
		return nil, unknownKey("phases", "phases")
	}
	out := make(map[phase.ID]phase.Settings, len(n.Content)/2)
	for key, val := range pairs(n) {
		s, err := decodeSettings(val, "phases."+key.Value, key.Value, true)
		if err != nil {
			return nil, err
		}
		out[phase.ID(key.Value)] = s
	}
	return out, nil
}

// decodeSettings maps one phase (or sub-phase) node. keyPath is the full YAML
// path used in unknown-key Subjects (e.g. "phases.discover"); timingPath is
// the "<phase>.<field>" prefix used in timing Subjects (e.g. "discover").
// allowSub is false one level down: sub-phases nest exactly one level, so a
// `sub` key inside a sub node is refused as unknown here — the core enforces
// depth semantics later.
func decodeSettings(n *yaml.Node, keyPath, timingPath string, allowSub bool) (phase.Settings, error) {
	var s phase.Settings
	if n.Tag == "!!null" {
		return s, nil
	}
	if n.Kind != yaml.MappingNode {
		return s, unknownKey(keyPath, keyPath)
	}
	for key, val := range pairs(n) {
		switch k := key.Value; k {
		case "attempts", "interval", "timeout", "settle_delay":
			if err := setTimingField(val, timingPath, k, &s.Timing); err != nil {
				return phase.Settings{}, err
			}
		case "enabled":
			var b bool
			if err := val.Decode(&b); err != nil {
				return phase.Settings{}, fmt.Errorf("config: %s.enabled: %w", keyPath, err)
			}
			s.Enabled = &b
		case "optional":
			if err := val.Decode(&s.Optional); err != nil {
				return phase.Settings{}, fmt.Errorf("config: %s.optional: %w", keyPath, err)
			}
		case "depends_on":
			var deps []phase.ID
			if err := val.Decode(&deps); err != nil {
				return phase.Settings{}, fmt.Errorf("config: %s.depends_on: %w", keyPath, err)
			}
			s.DependsOn = deps
		case "sub":
			if !allowSub {
				return phase.Settings{}, unknownKey(keyPath+".sub", "sub")
			}
			if val.Tag == "!!null" {
				continue
			}
			if val.Kind != yaml.MappingNode {
				return phase.Settings{}, unknownKey(keyPath+".sub", "sub")
			}
			s.Sub = make(map[phase.ID]phase.Settings, len(val.Content)/2)
			for subKey, subVal := range pairs(val) {
				sub, err := decodeSettings(subVal,
					keyPath+".sub."+subKey.Value,
					timingPath+"."+subKey.Value,
					false)
				if err != nil {
					return phase.Settings{}, err
				}
				s.Sub[phase.ID(subKey.Value)] = sub
			}
		default:
			return phase.Settings{}, unknownKey(keyPath+"."+k, k)
		}
	}
	return s, nil
}

// decodeTiming maps a bare timing mapping (the `defaults` node).
func decodeTiming(n *yaml.Node, path string, t *phase.Timing) error {
	if n.Tag == "!!null" {
		return nil
	}
	if n.Kind != yaml.MappingNode {
		return unknownKey(path, path)
	}
	for key, val := range pairs(n) {
		switch k := key.Value; k {
		case "attempts", "interval", "timeout", "settle_delay":
			if err := setTimingField(val, path, k, t); err != nil {
				return err
			}
		default:
			return unknownKey(path+"."+k, k)
		}
	}
	return nil
}

func setTimingField(val *yaml.Node, prefix, field string, t *phase.Timing) error {
	subject := prefix + "." + field
	timingErr := func(detail string) *phase.LoadError {
		return &phase.LoadError{Code: phase.TimingInvalid, Subject: subject, Detail: detail}
	}
	if val.Kind != yaml.ScalarNode {
		return timingErr("expected a scalar value")
	}
	if field == "attempts" {
		n, err := strconv.Atoi(val.Value)
		if err != nil {
			return timingErr(fmt.Sprintf("not an integer: %q", val.Value))
		}
		if n < 0 {
			return timingErr(fmt.Sprintf("negative: %d", n))
		}
		t.Attempts = n
		return nil
	}
	d, err := time.ParseDuration(val.Value)
	if err != nil {
		return timingErr(fmt.Sprintf("not a duration: %q", val.Value))
	}
	if d < 0 {
		return timingErr(fmt.Sprintf("negative: %s", d))
	}
	switch field {
	case "interval":
		t.Interval = d
	case "timeout":
		t.Timeout = d
	case "settle_delay":
		t.SettleDelay = d
	}
	return nil
}

// refuseDuplicateKeys walks every mapping in the document and errors on a
// repeated key. Aliases are resolved before comparison so an anchor cannot
// smuggle a duplicate past the check.
// maxConfigNodes bounds the total nodes refuseDuplicateKeys will visit. A real
// phase config is dozens of nodes; 100k is far above any honest document and
// far below where an alias-expansion bomb does damage.
const maxConfigNodes = 100_000

func refuseDuplicateKeys(n *yaml.Node, path string) error {
	// An anchor fan-out (a1: [*a0,*a0], a2: [*a1,*a1], ...) makes one
	// physical node reachable exponentially many times. yaml.v3's own
	// alias-ratio guard only fires inside .Decode(); this walk runs before it.
	// A visited-set collapses the shared DAG back to its true size, and the
	// budget is a hard backstop against anything the visited-set misses.
	budget := maxConfigNodes
	return refuseDuplicateKeysBounded(n, path, make(map[*yaml.Node]bool), &budget)
}

func refuseDuplicateKeysBounded(n *yaml.Node, path string, visited map[*yaml.Node]bool, budget *int) error {
	if *budget <= 0 {
		return &phase.LoadError{Code: phase.UnknownConfigKey, Subject: path,
			Detail: "config is too large or too deeply aliased to validate; refusing to expand it"}
	}
	*budget--
	if n.Kind == yaml.AliasNode {
		n = n.Alias
	}
	// A node reached a second time via aliases needs no re-walk: its structure
	// (and any duplicate key within it) was already judged the first time.
	if visited[n] {
		return nil
	}
	visited[n] = true
	switch n.Kind {
	case yaml.MappingNode:
		seen := make(map[string]bool, len(n.Content)/2)
		for i := 0; i+1 < len(n.Content); i += 2 {
			key, val := n.Content[i], n.Content[i+1]
			at := key.Value
			if path != "" {
				at = path + "." + key.Value
			}
			if seen[key.Value] {
				return &phase.LoadError{
					Code:    phase.UnknownConfigKey,
					Subject: at,
					Detail:  "duplicate key: yaml is last-wins, so one of these edits silently does nothing",
				}
			}
			seen[key.Value] = true
			if err := refuseDuplicateKeysBounded(val, at, visited, budget); err != nil {
				return err
			}
		}
	case yaml.SequenceNode:
		for _, item := range n.Content {
			if err := refuseDuplicateKeysBounded(item, path, visited, budget); err != nil {
				return err
			}
		}
	}
	return nil
}
