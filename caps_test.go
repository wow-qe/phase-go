// Copyright 2026 The Phase Contributors
// SPDX-License-Identifier: MIT

package phase

import (
	"context"
	"strings"
	"testing"

	"github.com/wow-qe/phase-go/result"
)

// One capability table (stageCaps) governs what each stage may do; it is
// authoritative, so a stage attempting an undeclared capability (recording,
// observing, Put, prior-evidence access) is a framework violation, not a
// silently permitted no-op.

func TestGroupTeardownCannotPut(t *testing.T) {
	// Nothing runs after teardown to consume a Put — the matrix denies it,
	// where the old ad-hoc gates left it silently unrestricted.
	key := capsTeardownKey
	var j []string
	r := groupRunner(t, &j,
		Group{ID: "g", Members: []ID{"m"},
			Lifecycle: &teardownPutter{journal: &j, key: key}},
		journalPhase("m", &j),
	)
	cr := caseReport(t, startSession(t, r, &stubCase{id: "one"}), "one")
	if cr.Status != Errored {
		t.Fatalf("status = %s — an undeclared capability use is a framework violation, loudly", cr.Status)
	}
	found := false
	for _, ae := range cr.Errors {
		if strings.Contains(ae.Err, "not permitted") {
			found = true
		}
	}
	if !found {
		t.Fatalf("the violation must name itself; errors = %+v", cr.Errors)
	}
	// The group outcome must also report the failure, so "which group had
	// trouble" never requires cross-referencing the error arrays.
	g := cr.Groups[0]
	if g.Status != Errored || !strings.Contains(g.Reason, "teardown") {
		t.Fatalf("group outcome = %+v — a lifecycle capability violation IS a lifecycle failure", g)
	}
}

type teardownPutter struct {
	journal *[]string
	key     Key[string]
}

func (f *teardownPutter) Setup(context.Context, *Run) error { return nil }
func (f *teardownPutter) Teardown(_ context.Context, r *Run) error {
	Put(r, f.key, "too late")
	return nil
}

func TestWhenCannotPut(t *testing.T) {
	key := capsWhenKey
	r := mustRunner(t, Config{Defaults: validTiming()},
		&conditionalPhase{stubPhase: stubPhase{id: "gate", produces: []KeyID{key.ID()}},
			when: func(_ context.Context, run *Run) (bool, string, error) {
				Put(run, key, "smuggled")
				return true, "", nil
			},
			do: func(_ context.Context, run *Run) error {
				run.Record(result.Compared("ok", []bool{true}))
				return nil
			},
		},
	)
	cr := caseReport(t, startSession(t, r, &stubCase{id: "one"}), "one")
	po := phaseOutcome(t, cr, "gate")
	if po.Status != Errored || po.Stage != StageCondition {
		t.Fatalf("outcome = %+v — a condition writing the handoff store is the same violation as recording", po)
	}
}

func TestCapabilityMatrixMatchesTheDocumentedTable(t *testing.T) {
	// stageCaps is the source the docs render from; this asserts its shape
	// so a change to a stage's capabilities is caught by a test.
	want := map[stageKind]capability{
		stageFixtureSetup:    capRecord | capObserve | capPut | capGet,
		stageFixtureTeardown: capRecord | capObserve | capGet, // same no-downstream rationale as group teardown
		stageSession:         capRecord | capObserve | capGet,
		stageGroupSetup:      capRecord | capObserve | capPut | capGet,
		stageGroupTeardown:   capRecord | capObserve | capGet,
		stageWhen:            capObserve | capGet | capPriorEvidence,
		stageExec:            capRecord | capObserve | capPut | capGet | capPriorEvidence,
	}
	for st, caps := range want {
		if stageCaps[st] != caps {
			t.Fatalf("stage %v caps = %b, want %b — update the table AND its consumers, not just a call site", st, stageCaps[st], caps)
		}
	}
	if len(stageCaps) != len(want) {
		t.Fatalf("stageCaps has %d stages, want %d", len(stageCaps), len(want))
	}
}

var (
	capsTeardownKey = Declare[string]("caps_teardown_key")
	capsWhenKey     = Declare[string]("caps_when_key")
)
