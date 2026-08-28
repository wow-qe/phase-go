// Copyright 2026 The Phase Contributors
// SPDX-License-Identifier: MIT

package phase_test

import (
	"errors"
	"testing"

	phase "github.com/wow-qe/phase-go"
	"github.com/wow-qe/phase-go/result"
)

func TestRefDefaultsTheKind(t *testing.T) {
	t.Parallel()

	ref := phase.Ref("3458")
	if ref.Kind != "entity" || ref.ID != "3458" {
		t.Fatalf("Ref() = %+v", ref)
	}
}

func TestEntityRefIsResultsType(t *testing.T) {
	t.Parallel()

	// The alias matters: a comparator importing only package result and a
	// phase importing the root must be talking about the same type, or every
	// consumer writes conversions at the boundary.
	// The compile-time fact under test: Ref returns result's own type —
	// the func literal's return only compiles while that stays true.
	var _ = func() result.EntityRef { return phase.Ref("x") }
	fromResult := phase.Ref("x")
	if fromResult.ID != "x" {
		t.Fatalf("alias broken: %+v", fromResult)
	}
}

func TestStatusStringsAreStable(t *testing.T) {
	t.Parallel()

	// These spellings appear in the report JSON (schema_version 1); changing
	// one is a report-schema break, not a cosmetic edit.
	want := map[phase.Status]string{
		phase.Passed:        "passed",
		phase.Failed:        "failed",
		phase.Skipped:       "skipped",
		phase.NotApplicable: "not_applicable",
		phase.Disabled:      "disabled",
		phase.Errored:       "errored",
		phase.Flaked:        "flaked",
	}
	for s, name := range want {
		if s.String() != name {
			t.Fatalf("%d.String() = %q, want %q", s, s.String(), name)
		}
	}
}

func TestParseStatusRefusesUnknownSpellings(t *testing.T) {
	t.Parallel()

	for _, ok := range []string{"active", "quarantined", "blocked", "draft"} {
		if _, err := phase.ParseStatus(ok); err != nil {
			t.Fatalf("ParseStatus(%q) = %v", ok, err)
		}
	}
	// Defaulting an unknown status is how a typo becomes a case that silently
	// always runs. It errors instead.
	if _, err := phase.ParseStatus("confirmed"); err == nil {
		t.Fatal("the retired spelling must not parse")
	}
	// The refusal must be a typed *LoadError carrying StatusUnparsable,
	// so the error-taxonomy's "Load family -> exit 2" contract is
	// actually reachable for this code path.
	var le *phase.LoadError
	if _, err := phase.ParseStatus("confirmed"); !errors.As(err, &le) || le.Code != phase.StatusUnparsable {
		t.Fatalf("ParseStatus must return *LoadError{StatusUnparsable}, got %v", err)
	}
	if _, err := phase.ParseStatus(""); err == nil {
		t.Fatal("empty must not parse")
	}
}

func TestSkipConstructorCarriesTheReason(t *testing.T) {
	t.Parallel()

	a := phase.Skip("negative case: rejected at ingest, never reaches the provider")
	if a.Applies || a.Reason == "" {
		t.Fatalf("Skip() = %+v", a)
	}
	if !phase.Applies().Applies {
		t.Fatal("Applies() must apply")
	}
}
