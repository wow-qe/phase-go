// Copyright 2026 The Phase Contributors
// SPDX-License-Identifier: MIT

package misuse

import (
	"errors"
	"strings"
	"testing"

	phase "github.com/wow-qe/phase-go"
)

// A merged report must be exactly as trustworthy as its shards, so every
// way the combination could misrepresent them is a refusal.

func TestMergeOfNothingRefuses(t *testing.T) {
	if rep, err := phase.MergeReports(); err == nil || rep != nil {
		t.Fatalf("(%v, %v) — a merge of nothing must not fabricate an empty green report", rep, err)
	}
}

func TestMergeRefusesSchemaMismatch(t *testing.T) {
	a, b := greenReport(t), rerun(t, func(*checkoutSystem) {}, "happy-multi")
	b.Schema = "0"
	rep, err := phase.MergeReports(a, b)
	if err == nil || rep != nil || !strings.Contains(err.Error(), "schema") {
		t.Fatalf("(%v, %v) — two vocabularies must not share one document", rep, err)
	}
}

func TestMergeRefusesATamperedShard(t *testing.T) {
	a, b := greenReport(t), rerun(t, func(*checkoutSystem) {}, "happy-multi")
	b.Cases[0].Phases[0].Stage = phase.Stage("bogus")
	rep, err := phase.MergeReports(a, b)
	if err == nil || rep != nil {
		t.Fatal("a corrupted shard must not blend into a merged report")
	}
	// The refusal reuses Verify itself: the same invariant a standalone
	// Verify would name must be in the chain — merge must not reimplement
	// (and silently weaken) the report's own integrity rules.
	var fe *phase.FrameworkError
	if !errors.As(err, &fe) || fe.Invariant != "stages are a closed set" {
		t.Fatalf("err = %v, want the standalone Verify invariant in the chain", err)
	}
}

func TestMergeRefusesDuplicateCaseIDs(t *testing.T) {
	a, b := greenReport(t), greenReport(t) // same case in both shards
	rep, err := phase.MergeReports(a, b)
	if err == nil || rep != nil {
		t.Fatal("indistinguishable rows must refuse to merge")
	}
	if !strings.Contains(err.Error(), "happy-single") {
		t.Fatalf("err = %v, want the colliding case named", err)
	}
}
