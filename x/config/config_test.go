// Copyright 2026 The Phase Contributors
// SPDX-License-Identifier: MIT

package config

import (
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	phase "github.com/wow-qe/phase-go"
)

// wantLoadError asserts err is a *phase.LoadError with the given code and
// subject, and returns it.
func wantLoadError(t *testing.T, err error, code phase.LoadCode, subject string) *phase.LoadError {
	t.Helper()
	if err == nil {
		t.Fatalf("want *phase.LoadError{Code: %s, Subject: %s}, got nil", code, subject)
	}
	var le *phase.LoadError
	if !errors.As(err, &le) {
		t.Fatalf("want *phase.LoadError, got %T: %v", err, err)
	}
	if le.Code != code {
		t.Errorf("Code = %s, want %s (err: %v)", le.Code, code, le)
	}
	if le.Subject != subject {
		t.Errorf("Subject = %q, want %q (err: %v)", le.Subject, subject, le)
	}
	return le
}

func TestLoadWorkedExample(t *testing.T) {
	got, err := Load(filepath.Join("testdata", "worked_example.yaml"))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	no := false
	want := phase.Config{
		Defaults: phase.Timing{
			Attempts: 3,
			Interval: 5 * time.Second,
			Timeout:  60 * time.Second,
		},
		Phases: map[phase.ID]phase.Settings{
			"submit": {},
			"discover": {
				DependsOn: []phase.ID{"submit"},
				Timing:    phase.Timing{Attempts: 10, Interval: 2 * time.Second},
			},
			"progress": {
				DependsOn: []phase.ID{"discover"},
				Sub: map[phase.ID]phase.Settings{
					"verify_frozen": {Optional: true},
				},
			},
			"provider_side": {
				DependsOn: []phase.ID{"settle"},
				Enabled:   &no,
			},
			"ledger": {
				Timing: phase.Timing{SettleDelay: 10 * time.Second},
			},
		},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Load mismatch:\n got  %#v\n want %#v", got, want)
	}

	// The nil-vs-false distinction on Enabled, asserted explicitly: absent
	// inherits, false is the operator kill-switch.
	if got.Phases["submit"].Enabled != nil {
		t.Errorf("submit.Enabled = %v, want nil (absent must stay nil)", *got.Phases["submit"].Enabled)
	}
	if e := got.Phases["provider_side"].Enabled; e == nil {
		t.Error("provider_side.Enabled = nil, want *false")
	} else if *e {
		t.Error("provider_side.Enabled = *true, want *false")
	}

	// Sub-phase presence.
	sub, ok := got.Phases["progress"].Sub["verify_frozen"]
	if !ok {
		t.Fatal("progress.Sub missing verify_frozen")
	}
	if !sub.Optional {
		t.Error("verify_frozen.Optional = false, want true")
	}
}

func TestParseEnabledTrue(t *testing.T) {
	got, err := Parse([]byte("phases:\n  p: {enabled: true}\n"))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if e := got.Phases["p"].Enabled; e == nil || !*e {
		t.Errorf("p.Enabled = %v, want *true", e)
	}
}

func TestParseUnknownKeys(t *testing.T) {
	tests := []struct {
		name    string
		yaml    string
		subject string
	}{
		{
			name:    "top level",
			yaml:    "defaults: {attempts: 3}\nadapters:\n  queue: {poll_timeout: 1s}\n",
			subject: "adapters",
		},
		{
			name:    "inside defaults",
			yaml:    "defaults: {attemptz: 3}\n",
			subject: "defaults.attemptz",
		},
		{
			name:    "inside a phase",
			yaml:    "phases:\n  discover: {attemptz: 10}\n",
			subject: "phases.discover.attemptz",
		},
		{
			name:    "inside a sub-phase",
			yaml:    "phases:\n  progress:\n    sub:\n      verify_frozen: {optionnal: true}\n",
			subject: "phases.progress.sub.verify_frozen.optionnal",
		},
		{
			name:    "sub nested under a sub-phase",
			yaml:    "phases:\n  progress:\n    sub:\n      verify_frozen:\n        sub:\n          deeper: {}\n",
			subject: "phases.progress.sub.verify_frozen.sub",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Parse([]byte(tt.yaml))
			wantLoadError(t, err, phase.UnknownConfigKey, tt.subject)
		})
	}
}

func TestParseTimingInvalid(t *testing.T) {
	tests := []struct {
		name    string
		yaml    string
		subject string
	}{
		{
			name:    "bad duration string",
			yaml:    "phases:\n  discover: {interval: fast}\n",
			subject: "discover.interval",
		},
		{
			name:    "negative duration",
			yaml:    "phases:\n  discover: {interval: -2s}\n",
			subject: "discover.interval",
		},
		{
			name:    "bad duration in defaults",
			yaml:    "defaults: {timeout: soon}\n",
			subject: "defaults.timeout",
		},
		{
			name:    "negative settle_delay",
			yaml:    "phases:\n  ledger: {settle_delay: -10s}\n",
			subject: "ledger.settle_delay",
		},
		{
			name:    "bad duration in a sub-phase",
			yaml:    "phases:\n  progress:\n    sub:\n      verify_frozen: {interval: quick}\n",
			subject: "progress.verify_frozen.interval",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Parse([]byte(tt.yaml))
			wantLoadError(t, err, phase.TimingInvalid, tt.subject)
		})
	}
}

func TestLoadEmptyFile(t *testing.T) {
	got, err := Load(filepath.Join("testdata", "empty.yaml"))
	if err != nil {
		t.Fatalf("Load(empty) = %v, want nil error", err)
	}
	if !reflect.DeepEqual(got, phase.Config{}) {
		t.Errorf("Load(empty) = %#v, want zero Config", got)
	}
}

func TestParseEmptyBytes(t *testing.T) {
	got, err := Parse(nil)
	if err != nil {
		t.Fatalf("Parse(nil) = %v, want nil error", err)
	}
	if !reflect.DeepEqual(got, phase.Config{}) {
		t.Errorf("Parse(nil) = %#v, want zero Config", got)
	}
}

func TestLoadMissingFile(t *testing.T) {
	_, err := Load(filepath.Join("testdata", "does_not_exist.yaml"))
	if err == nil {
		t.Fatal("Load(missing) = nil error, want wrapped os error")
	}
	var le *phase.LoadError
	if errors.As(err, &le) {
		t.Errorf("Load(missing) = LoadError %v; a missing file is an environment problem, not a declaration problem", le)
	}
	if !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("Load(missing) = %v, want errors.Is(err, fs.ErrNotExist)", err)
	}
}

func TestDuplicateKeysAreRefused(t *testing.T) {
	// yaml.v3 is last-wins on duplicates, which means an operator edits the
	// wrong one and nothing says so. Found by a gate probe, not by this
	// suite's first version — recorded here so it cannot regress.
	cases := map[string]string{
		"in a phase":   "phases:\n  submit: {attempts: 3, attempts: 5}\n",
		"at top level": "defaults: {attempts: 1}\ndefaults: {attempts: 2}\n",
		"in defaults":  "defaults: {interval: 1s, interval: 2s}\n",
	}
	for name, src := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := Parse([]byte(src))
			var le *phase.LoadError
			if !errors.As(err, &le) {
				t.Fatalf("duplicate key must be a LoadError, got %v", err)
			}
			if le.Code != phase.UnknownConfigKey {
				t.Fatalf("code = %s", le.Code)
			}
		})
	}
}

func TestAliasBombIsRefusedNotExpanded(t *testing.T) {
	// RefuseDuplicateKeys resolved
	// aliases and recursed with no visited-set, so a sub-1KB anchor fan-out
	// drove hours of CPU before any semantic validation. A visited-set + node
	// budget must refuse it in bounded time.
	var b strings.Builder
	b.WriteString("phases:\n")
	b.WriteString("  a0: &a0 [x, x]\n")
	for i := 1; i <= 12; i++ {
		fmt.Fprintf(&b, "  a%d: &a%d [", i, i)
		for j := 0; j < 10; j++ {
			fmt.Fprintf(&b, "*a%d,", i-1)
		}
		b.WriteString("]\n")
	}
	done := make(chan error, 1)
	go func() { _, err := Parse([]byte(b.String())); done <- err }()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("an alias bomb must be refused, not accepted")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("alias bomb was still expanding after 2s — the visited-set/budget did not bound it")
	}
}

func TestLegitimateAliasesStillWork(t *testing.T) {
	// The guard must not break honest anchor reuse.
	cfg, err := Parse([]byte("defaults: &d {attempts: 3, interval: 5s}\nphases:\n  submit: {}\n"))
	if err != nil {
		t.Fatalf("legitimate anchor refused: %v", err)
	}
	if cfg.Defaults.Attempts != 3 {
		t.Fatalf("anchor value lost: %+v", cfg.Defaults)
	}
}
