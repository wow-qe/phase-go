// Copyright 2026 The Phase Contributors
// SPDX-License-Identifier: MIT

package phase

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/wow-qe/phase-go/result"
)

// A Group is lifecycle scoped to a named subset of
// already-declared phases — DAG-causal (setup causally precedes every
// member; teardown is a completion barrier), never positional.

type groupFixture struct {
	name                  string
	journal               *[]string
	setupErr, teardownErr error
	onSetup               func(*Run)
}

func (f *groupFixture) Setup(_ context.Context, r *Run) error {
	*f.journal = append(*f.journal, f.name+":setup")
	if f.onSetup != nil {
		f.onSetup(r)
	}
	return f.setupErr
}

func (f *groupFixture) Teardown(_ context.Context, r *Run) error {
	*f.journal = append(*f.journal, f.name+":teardown")
	return f.teardownErr
}

func journalPhase(id ID, journal *[]string, deps ...ID) Interface {
	return &recordingPhase{stubPhase: stubPhase{id: id, deps: deps}, do: func(_ context.Context, run *Run) error {
		*journal = append(*journal, string(id))
		run.Record(result.Compared(string(id)+" ok", []bool{true}))
		return nil
	}}
}

func groupRunner(t *testing.T, journal *[]string, g Group, phases ...Interface) *Runner {
	t.Helper()
	p := NewPipeline(phases...).Group(g)
	r, err := NewRunner(p, Config{Defaults: validTiming()})
	if err != nil {
		t.Fatalf("NewRunner: %v", err)
	}
	return r
}

func TestGroupLifecycleWrapsItsMembers(t *testing.T) {
	var j []string
	r := groupRunner(t, &j,
		Group{ID: "settlement", Members: []ID{"settle_wait", "settle_checks"},
			Lifecycle: &groupFixture{name: "g", journal: &j}},
		journalPhase("submit", &j),
		journalPhase("settle_wait", &j, "submit"),
		journalPhase("settle_checks", &j, "settle_wait"),
		journalPhase("audit", &j, "settle_checks"),
	)
	cr := caseReport(t, startSession(t, r, &stubCase{id: "one"}), "one")
	want := "submit | g:setup | settle_wait | settle_checks | g:teardown | audit"
	if got := strings.Join(j, " | "); got != want {
		t.Fatalf("journal = %q, want %q", got, want)
	}
	if cr.Status != Passed {
		t.Fatalf("status = %s", cr.Status)
	}
	if len(cr.Groups) != 1 || cr.Groups[0].GroupID != "settlement" || cr.Groups[0].Status != Passed {
		t.Fatalf("groups = %+v", cr.Groups)
	}
}

func TestGroupSetupSkippedWhenNoMemberRuns(t *testing.T) {
	var j []string
	r := groupRunner(t, &j,
		Group{ID: "settlement", Members: []ID{"settle"},
			Lifecycle: &groupFixture{name: "g", journal: &j}},
		journalPhase("submit", &j),
		&stubPhase{id: "settle", deps: []ID{"submit"}, applies: Skip("not for this case")},
	)
	cr := caseReport(t, startSession(t, r, &stubCase{id: "one"}), "one")
	for _, e := range j {
		if strings.HasPrefix(e, "g:") {
			t.Fatalf("group lifecycle ran for a group with no running member: %v", j)
		}
	}
	if len(cr.Groups) != 1 || cr.Groups[0].Status != NotApplicable || cr.Groups[0].Reason == "" {
		t.Fatalf("a skipped group must be VISIBLE as NotApplicable with a reason; groups = %+v", cr.Groups)
	}
}

func TestGroupSetupFailureErrorsMembersAndPrunesTheirDependents(t *testing.T) {
	var j []string
	r := groupRunner(t, &j,
		Group{ID: "settlement", Members: []ID{"settle_wait", "settle_checks"},
			Lifecycle: &groupFixture{name: "g", journal: &j, setupErr: errors.New("kafka consumer never joined")}},
		journalPhase("submit", &j),
		journalPhase("settle_wait", &j, "submit"),
		journalPhase("settle_checks", &j, "settle_wait"),
		journalPhase("audit", &j, "settle_checks"), // structurally depends on a MEMBER
		journalPhase("independent", &j, "submit"),  // no group relation — must run
	)
	cr := caseReport(t, startSession(t, r, &stubCase{id: "one"}), "one")
	for _, id := range []ID{"settle_wait", "settle_checks"} {
		po := phaseOutcome(t, cr, id)
		if po.Status != Errored || !strings.Contains(po.Reason, `group "settlement" setup failed`) {
			t.Fatalf("member %s = %+v — the world could not be built: Errored, cause named", id, po)
		}
	}
	// The engine lens's correction: a phase depending on a MEMBER must be
	// transitively pruned — execution must not continue past a failed group
	// precondition.
	if po := phaseOutcome(t, cr, "audit"); po.Status != NotApplicable || !strings.Contains(po.Reason, "errored") {
		t.Fatalf("audit = %+v, want pruned with the cause named", po)
	}
	if po := phaseOutcome(t, cr, "independent"); po.Status != Passed {
		t.Fatalf("independent = %+v — non-members continue", po)
	}
	if cr.Status != Errored {
		t.Fatalf("case = %s", cr.Status)
	}
	if len(cr.Groups) != 1 || cr.Groups[0].Status != Errored || !strings.Contains(cr.Groups[0].Reason, "setup failed") {
		t.Fatalf("groups = %+v", cr.Groups)
	}
	// Teardown still ran: setup was ATTEMPTED (fixture precedent — a partial
	// setup may hold resources).
	if got := strings.Join(j, " | "); !strings.Contains(got, "g:teardown") {
		t.Fatalf("teardown must run when setup was attempted; journal = %q", got)
	}
}

func TestGroupTeardownRunsWhenAMemberErrors(t *testing.T) {
	var j []string
	r := groupRunner(t, &j,
		Group{ID: "settlement", Members: []ID{"settle"},
			Lifecycle: &groupFixture{name: "g", journal: &j}},
		&recordingPhase{stubPhase: stubPhase{id: "settle"}, do: func(context.Context, *Run) error {
			return errors.New("adapter down")
		}},
	)
	startSession(t, r, &stubCase{id: "one"})
	if got := strings.Join(j, " | "); got != "g:setup | g:teardown" {
		t.Fatalf("journal = %q — teardown is a completion barrier, member outcome irrelevant", got)
	}
}

func TestGroupTeardownFailureIsVisiblyDistinctFromSetupFailure(t *testing.T) {
	var j []string
	r := groupRunner(t, &j,
		Group{ID: "settlement", Members: []ID{"settle"},
			Lifecycle: &groupFixture{name: "g", journal: &j, teardownErr: errors.New("consumer group not released")}},
		journalPhase("settle", &j),
	)
	cr := caseReport(t, startSession(t, r, &stubCase{id: "one"}), "one")
	if cr.Status != Errored {
		t.Fatalf("case = %s — a leaked resource poisons the NEXT run; it must not read as a pass", cr.Status)
	}
	g := cr.Groups[0]
	if g.Status != Errored || !strings.Contains(g.Reason, "teardown failed") || strings.Contains(g.Reason, "setup failed") {
		t.Fatalf("group = %+v — teardown failure has a different debugging playbook than setup failure", g)
	}
	found := false
	for _, ae := range cr.Errors {
		if strings.Contains(string(ae.Phase), "group:settlement:teardown") {
			found = true
		}
	}
	if !found {
		t.Fatalf("teardown error must be attributed under the reserved teardown ID; errors = %+v", cr.Errors)
	}
}

func TestGroupProducesHandsOffToMembers(t *testing.T) {
	var got string
	var j []string
	key := groupTestKey // package-level Key[string] (Declare must run once)
	r := groupRunner(t, &j,
		Group{ID: "settlement", Members: []ID{"settle"}, Produces: []KeyID{key.ID()},
			Lifecycle: &groupFixture{name: "g", journal: &j, onSetup: func(run *Run) {
				Put(run, key, "topic-A7")
			}}},
		&recordingPhase{stubPhase: stubPhase{id: "settle", requires: []KeyID{key.ID()}}, do: func(_ context.Context, run *Run) error {
			v, err := Get(run, key)
			if err != nil {
				return err
			}
			got = v
			run.Record(result.Compared("topic known", []bool{v != ""}))
			return nil
		}},
	)
	cr := caseReport(t, startSession(t, r, &stubCase{id: "one"}), "one")
	if cr.Status != Passed || got != "topic-A7" {
		t.Fatalf("status=%s got=%q — a member must Require a group-produced key past preflight", cr.Status, got)
	}
}

func TestGroupEvidenceIsAttributedAndCounted(t *testing.T) {
	var j []string
	r := groupRunner(t, &j,
		Group{ID: "settlement", Members: []ID{"settle"},
			Lifecycle: &groupFixture{name: "g", journal: &j, onSetup: func(run *Run) {
				run.Record(result.Compared("broker reachable", []bool{true}))
				run.Observe("consumer group", "qe-settlement")
			}}},
		journalPhase("settle", &j),
	)
	cr := caseReport(t, startSession(t, r, &stubCase{id: "one"}), "one")
	if cr.Groups[0].Recorded != 1 {
		t.Fatalf("group results_recorded = %d, want 1", cr.Groups[0].Recorded)
	}
	var seen []string
	for _, ar := range cr.Results {
		seen = append(seen, string(ar.Phase))
	}
	if !strings.Contains(strings.Join(seen, " "), "group:settlement:setup") {
		t.Fatalf("setup evidence must carry the reserved ID; results phases = %v", seen)
	}
	// And the phase-level counting must not have swallowed it: the member
	// recorded exactly its own result.
	if po := phaseOutcome(t, caseReport(t, startSession(t, r, &stubCase{id: "two"}), "two"), "settle"); po.Results != 1 {
		t.Fatalf("member results_recorded = %d", po.Results)
	}
}

func TestGroupRefusals(t *testing.T) {
	base := func() []Interface {
		return []Interface{&stubPhase{id: "a"}, &stubPhase{id: "b"}}
	}
	cases := map[string]struct {
		pipeline *Pipeline
		code     LoadCode
	}{
		"empty members":          {NewPipeline(base()...).Group(Group{ID: "g"}), EmptyGroup},
		"unknown member":         {NewPipeline(base()...).Group(Group{ID: "g", Members: []ID{"ghost"}}), UnknownGroupMember},
		"duplicate member":       {NewPipeline(base()...).Group(Group{ID: "g", Members: []ID{"a", "a"}}), GroupMemberDuplicate},
		"duplicate group id":     {NewPipeline(base()...).Group(Group{ID: "g", Members: []ID{"a"}}).Group(Group{ID: "g", Members: []ID{"b"}}), DuplicateGroupID},
		"reserved char in group": {NewPipeline(base()...).Group(Group{ID: "g:x", Members: []ID{"a"}}), GroupIDReservedCharacter},
		"reserved char in phase": {NewPipeline(&stubPhase{id: "has:colon"}), GroupIDReservedCharacter},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := NewRunner(tc.pipeline, Config{Defaults: validTiming()})
			var le *LoadError
			if !errors.As(err, &le) || le.Code != tc.code {
				t.Fatalf("err = %v, want LoadError %s", err, tc.code)
			}
		})
	}
}

func TestNonMemberCannotRequireAGroupKey(t *testing.T) {
	key := groupTestKey2
	p := NewPipeline(
		&stubPhase{id: "member"},
		&stubPhase{id: "outsider", requires: []KeyID{key.ID()}},
	).Group(Group{ID: "g", Members: []ID{"member"}, Produces: []KeyID{key.ID()},
		Lifecycle: noopGroupFixture{}})
	_, err := NewRunner(p, Config{Defaults: validTiming()})
	var le *LoadError
	if !errors.As(err, &le) || le.Code != KeyNeverProduced {
		t.Fatalf("err = %v — a non-member requiring a group key must be refused (reach does not include setup)", err)
	}
}

func TestGroupTeardownSurvivesCancellation(t *testing.T) {
	var j []string
	ctx, cancel := context.WithCancel(context.Background())
	r := groupRunner(t, &j,
		Group{ID: "settlement", Members: []ID{"settle"},
			Lifecycle: &groupFixture{name: "g", journal: &j}},
		&recordingPhase{stubPhase: stubPhase{id: "settle"}, do: func(context.Context, *Run) error {
			cancel()
			return nil
		}},
	)
	s, err := r.Start(ctx, []Case{&stubCase{id: "cut"}})
	if err != nil {
		t.Fatal(err)
	}
	_ = s
	if !strings.Contains(strings.Join(j, " "), "g:teardown") {
		t.Fatalf("teardown must run on a detached context; journal = %v", j)
	}
}

func TestGroupsReachTheArtifact(t *testing.T) {
	var j []string
	r := groupRunner(t, &j,
		Group{ID: "settlement", Members: []ID{"settle"},
			Lifecycle: &groupFixture{name: "g", journal: &j}},
		journalPhase("settle", &j),
	)
	var buf strings.Builder
	if err := startSession(t, r, &stubCase{id: "one"}).Report().WriteJSON(&buf); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"groups"`, `"group_id": "settlement"`, `"members"`} {
		if !strings.Contains(buf.String(), want) {
			t.Fatalf("artifact missing %s", want)
		}
	}
}

func TestSettingsSubIsLoudlyRefused(t *testing.T) {
	// The inert Settings.Sub stub is superseded by Group — and superseded
	// LOUDLY: config that still says `sub:` fails at load, never no-ops.
	_, err := NewRunner(NewPipeline(&stubPhase{id: "a"}), Config{
		Defaults: validTiming(),
		Phases:   map[ID]Settings{"a": {Sub: map[ID]Settings{"a.x": {}}}},
	})
	var le *LoadError
	if !errors.As(err, &le) || le.Code != SettingsSubRemoved {
		t.Fatalf("err = %v, want %s", err, SettingsSubRemoved)
	}
}

type noopGroupFixture struct{}

func (noopGroupFixture) Setup(context.Context, *Run) error    { return nil }
func (noopGroupFixture) Teardown(context.Context, *Run) error { return nil }

var (
	groupTestKey  = Declare[string]("group_test_topic")
	groupTestKey2 = Declare[string]("group_test_private")
)

func TestDependingOnSyntheticPlumbingIsRefused(t *testing.T) {
	// A guarded misuse: a literal DependsOn on
	// "group:<id>:setup" would resolve against the internal node and leak
	// its name into pruning reasons. Group causality is membership, never a
	// dependency on plumbing.
	p := NewPipeline(
		&stubPhase{id: "member"},
		&stubPhase{id: "outsider", deps: []ID{"group:g:setup"}},
	).Group(Group{ID: "g", Members: []ID{"member"}, Lifecycle: noopGroupFixture{}})
	_, err := NewRunner(p, Config{Defaults: validTiming()})
	var le *LoadError
	if !errors.As(err, &le) || le.Code != GroupIDReservedCharacter {
		t.Fatalf("err = %v, want %s", err, GroupIDReservedCharacter)
	}
}
