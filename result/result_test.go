// Copyright 2026 The Phase Contributors
// SPDX-License-Identifier: MIT

package result_test

import (
	"strings"
	"testing"

	"github.com/wow-qe/phase-go/result"
)

// The rule this package exists for: a result over zero comparisons is a
// failure, never a pass — and it must name what it failed to compare.
//
// `all([])` is true in every language, and the resulting defect is a suite
// that reports green while checking nothing. The source framework patched
// that shape locally three times and it returned each time. Here it is
// inexpressible: unexported fields mean no literal can bypass Compared.

func TestZeroComparisonsCannotPass(t *testing.T) {
	r := result.Compared("private offer states", nil)
	if r.Passed() {
		t.Fatal("a result over a nil comparison set must not pass")
	}
	r = result.Compared("private offer states", []bool{})
	if r.Passed() {
		t.Fatal("a result over an empty comparison set must not pass")
	}
	if !strings.Contains(r.Reason(), "private offer states") {
		t.Fatalf("the refusal must name what was not compared; got %q", r.Reason())
	}
	if r.Comparisons() != 0 {
		t.Fatalf("comparisons = %d, want 0", r.Comparisons())
	}
}

func TestRealComparisonsDiscriminate(t *testing.T) {
	tests := []struct {
		name   string
		checks []bool
		passed bool
	}{
		{"all pass", []bool{true, true, true}, true},
		{"one fails", []bool{true, false, true}, false},
		{"all fail", []bool{false, false}, false},
		{"single pass", []bool{true}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := result.Compared("item state", tt.checks)
			if r.Passed() != tt.passed {
				t.Fatalf("Passed() = %v, want %v", r.Passed(), tt.passed)
			}
			if r.Comparisons() != len(tt.checks) {
				t.Fatalf("Comparisons() = %d, want %d", r.Comparisons(), len(tt.checks))
			}
		})
	}
}

func TestFailureReasonCountsTheFailures(t *testing.T) {
	r := result.Compared("item state", []bool{true, false, false})
	if r.Passed() {
		t.Fatal("must fail")
	}
	for _, want := range []string{"2", "3", "item state"} {
		if !strings.Contains(r.Reason(), want) {
			t.Fatalf("reason %q should contain %q", r.Reason(), want)
		}
	}
}

func TestAPassingResultHasNoReason(t *testing.T) {
	if got := result.Compared("x", []bool{true}).Reason(); got != "" {
		t.Fatalf("a pass needs no justification; got %q", got)
	}
}

func TestFailedIsAlwaysLegal(t *testing.T) {
	r := result.Failed("state mismatch", "expected CloudSucceeded, saw CloudFailed")
	if r.Passed() {
		t.Fatal("Failed must not pass")
	}
	if r.Name() != "state mismatch" {
		t.Fatalf("Name() = %q", r.Name())
	}
	if r.Reason() == "" {
		t.Fatal("an explicit failure must carry its reason")
	}
	// Refusing to pass never needs a comparison count to justify it, but it
	// counts as one decision made.
	if r.Comparisons() != 1 {
		t.Fatalf("Comparisons() = %d, want 1", r.Comparisons())
	}
}

func TestFailedRequiresAReason(t *testing.T) {
	r := result.Failed("state mismatch", "")
	if r.Passed() {
		t.Fatal("must not pass")
	}
	// A failure with no reason is half a check: the constructor supplies a
	// loud placeholder rather than silence, so the report shows the omission.
	if r.Reason() == "" {
		t.Fatal("an empty reason must be replaced, not preserved")
	}
}

func TestBuilderCarriesEvidence(t *testing.T) {
	ref := result.EntityRef{Kind: "entity", ID: "3458"}
	r := result.Compared("item state", []bool{false}).
		WithExpected("CloudSucceeded").
		WithActual("CloudFailed").
		ForEntity(ref)
	if r.Expected() != "CloudSucceeded" || r.Actual() != "CloudFailed" {
		t.Fatalf("evidence lost: expected=%v actual=%v", r.Expected(), r.Actual())
	}
	if r.Entity() != ref {
		t.Fatalf("entity lost: %v", r.Entity())
	}
}

func TestBuilderIsValueSemantics(t *testing.T) {
	base := result.Compared("item state", []bool{true})
	derived := base.WithExpected("a").WithActual("b")
	if base.Expected() != nil || base.Actual() != nil {
		t.Fatal("With* must not mutate the receiver — a result is evidence, not a draft")
	}
	if derived.Expected() != "a" {
		t.Fatal("derived result must carry the evidence")
	}
	// The outcome never changes on the way through the builder.
	if base.Passed() != derived.Passed() {
		t.Fatal("builder methods must not alter the outcome")
	}
}

func TestNameIsStableIdentity(t *testing.T) {
	// The name is the stable identity a snapshot diff keys on. The volatile
	// values live in expected/actual/reason — never in the name. The type
	// cannot enforce prose discipline, but it CAN keep name and evidence in
	// separate fields so the discipline is possible; this test pins that the
	// name survives the builder untouched.
	r := result.Compared("item state", []bool{false}).WithExpected(3458).WithActual(9999)
	if r.Name() != "item state" {
		t.Fatalf("Name() = %q, want the exact constructor argument", r.Name())
	}
}
