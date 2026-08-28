// Copyright 2026 The Phase Contributors
// SPDX-License-Identifier: MIT

package phasetest

import (
	"testing"

	phase "github.com/wow-qe/phase-go"
)

// conformanceProbeIDs is what ConformanceCase probes Selects and Timing
// with: a few representative-looking IDs, plus one the case has surely
// never heard of. An unknown phase must still get a reasoned answer — a
// case that only handles IDs it recognises and panics or answers blank for
// anything else is exactly the defect this check exists to catch.
var conformanceProbeIDs = []phase.ID{
	"setup",
	"discover",
	"assert",
	"teardown",
	unknownProbeID,
}

const unknownProbeID phase.ID = "phasetest/__unknown_probe_the_case_has_never_heard_of__"

// ConformanceCase fails t with a named violation for each way c breaks the
// phase.Case contract:
//
//   - ID() is empty
//   - Status().String() == "invalid"
//   - Selects returns (false, "") for any probed phase ID
//   - Exclusive() returns (true, "")
//   - Fixtures() contains a nil entry
//   - Timing(id) returns ok=true with an all-zero Timing
//
// Every violation is reported (via t.Error), not just the first, so a
// broken Case shows every failing check in one run.
func ConformanceCase(t testing.TB, c phase.Case) {
	t.Helper()
	if c == nil {
		t.Fatal("phasetest.ConformanceCase: case is nil")
	}

	if c.ID() == "" {
		t.Error("ConformanceCase: ID() returned an empty string — a case must have a stable, non-empty identity")
	}

	if got := c.Status().String(); got == "invalid" {
		t.Errorf("ConformanceCase: Status() returned an unparseable CaseStatus (String() == %q)", got)
	}

	for _, id := range conformanceProbeIDs {
		if ok, reason := c.Selects(id); !ok && reason == "" {
			t.Errorf("ConformanceCase: Selects(%q) returned (false, \"\") — declining requires a reason, and an unknown phase must still get a reasoned answer", id)
		}
	}

	if exclusive, reason := c.Exclusive(); exclusive && reason == "" {
		t.Error("ConformanceCase: Exclusive() returned (true, \"\") — exclusivity is expensive and must be explained")
	}

	for i, f := range c.Fixtures() {
		if f == nil {
			t.Errorf("ConformanceCase: Fixtures()[%d] is nil", i)
		}
	}

	for _, id := range conformanceProbeIDs {
		if timing, ok := c.Timing(id); ok && timing == (phase.Timing{}) {
			t.Errorf("ConformanceCase: Timing(%q) returned ok=true with an all-zero Timing — an override that overrides nothing is a declaration mistake", id)
		}
	}
}
