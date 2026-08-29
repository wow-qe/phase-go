// Copyright 2026 The Phase Contributors
// SPDX-License-Identifier: MIT

package phase

import (
	"context"
	"fmt"
	"strings"
	"sync"
)

// Group is lifecycle scoped to a named subset of already-declared phases.
// Pipeline is the plural — the place phases are declared; a Group never
// declares phases, it references them by ID, so there is exactly one
// source of truth for what exists.
//
// Semantics are DAG-causal, never positional (positions stop existing under
// parallelism): Setup causally precedes every member — it is a synthetic
// dependency node, so pruning, handoff validation and ordering ride the
// existing machinery — and Teardown is a completion barrier that fires once
// every member has a landed outcome of any kind, always if Setup was
// attempted, on a detached context (a cancelled run must not leak the
// resources Setup acquired into the next one). Setup never fires for a case
// in which no member reaches execution; the group then reports
// NotApplicable, visibly.
//
// Setup runs restricted to the declared Produces — a member may Require a
// group-provisioned key and preflight validates it through the same
// producer/reach machinery as any phase handoff. Teardown is unrestricted
// (cleanup and tally; nothing runs after it to consume a Put).
type Group struct {
	ID ID // reserved namespace: must not contain ':'

	// Members are phase IDs already declared in the Pipeline — metadata,
	// never a second declaration site.
	Members []ID

	// Produces are the handoff keys Setup Puts, validated at NewRunner by
	// the same single-writer producer map as phase Produces.
	Produces []KeyID

	// Lifecycle carries Setup/Teardown with the Fixture contract's
	// already-tested guarantees (panic containment, teardown-on-every-path,
	// detached teardown context). Nil means the group is pure structure.
	Lifecycle Fixture
}

// Group registers a group on the pipeline. Chainable; NewPipeline's
// signature is untouched.
func (p *Pipeline) Group(g Group) *Pipeline {
	p.groups = append(p.groups, g)
	return p
}

func setupID(g ID) ID    { return ID("group:" + string(g) + ":setup") }
func teardownID(g ID) ID { return ID("group:" + string(g) + ":teardown") }

// validateGroups refuses mis-declared groups at NewRunner and returns the
// per-runner group table. Every refusal is a typed LoadError: a
// misdeclared group must fail construction rather than be silently
// ignored.
func (r *Runner) validateGroups(groups []Group) error {
	for id := range r.byID {
		if strings.Contains(string(id), ":") {
			return &LoadError{Code: GroupIDReservedCharacter, Subject: string(id),
				Detail: "phase IDs must not contain ':' — the namespace is reserved for group attribution"}
		}
	}
	seen := map[ID]bool{}
	for _, g := range groups {
		if strings.Contains(string(g.ID), ":") {
			return &LoadError{Code: GroupIDReservedCharacter, Subject: string(g.ID),
				Detail: "group IDs must not contain ':'"}
		}
		if seen[g.ID] {
			return &LoadError{Code: DuplicateGroupID, Subject: string(g.ID),
				Detail: "two groups carry this ID"}
		}
		seen[g.ID] = true
		if len(g.Members) == 0 {
			return &LoadError{Code: EmptyGroup, Subject: string(g.ID),
				Detail: "a group with no members scopes nothing; declare its members"}
		}
		mseen := map[ID]bool{}
		for _, m := range g.Members {
			if _, ok := r.byID[m]; !ok {
				return &LoadError{Code: UnknownGroupMember, Subject: string(g.ID),
					Detail: fmt.Sprintf("member %q is not a phase in the pipeline", m)}
			}
			if mseen[m] {
				return &LoadError{Code: GroupMemberDuplicate, Subject: string(g.ID),
					Detail: fmt.Sprintf("member %q listed twice", m)}
			}
			mseen[m] = true
		}
	}
	return nil
}

// groupRun is one group's per-case lifecycle state. The mutex matters: under
// phase-level concurrency two members can be "the first to reach
// execution" simultaneously, and check-then-set booleans become TOCTOU
// races. Holding mu across Setup also gives members the happens-before
// they need: no member proceeds until Setup's outcome is decided.
type groupRun struct {
	g        *Group
	tearRank int

	mu        sync.Mutex
	remaining int
	state     *machine[groupState] // the checked lifecycle
	setupErr  error
	tearErr   error
}

// ensureSetup fires the group's Setup exactly once per case, lazily, the
// moment the first member actually reaches execution — so a case in which
// every member is declined, disabled or pruned never pays for (or appears
// to have) a lifecycle it did not use.
func (r *Runner) ensureSetup(ctx context.Context, gr *groupRun, run *Run, caseID string) {
	gr.mu.Lock()
	defer gr.mu.Unlock()
	if gr.state.state() != groupPending {
		return
	}
	if err := gr.state.to(groupSettingUp); err != nil {
		panic(err) // internal invariant violation; the machine exists to surface it
	}
	sid := setupID(gr.g.ID)
	sv := run.bound(sid, Timing{}, gr.g.Produces, true)
	sv.rank = r.rankOf[sid]
	sv.source = EvidenceSource{Kind: SourceGroupSetup, ID: gr.g.ID}
	sv.stage = stageGroupSetup
	r.emitEvent(GroupEvent{eventBase: r.eventBaseFor(caseID, GroupSetupStarted), GroupID: gr.g.ID})
	if gr.g.Lifecycle != nil {
		gr.setupErr = runOneSetup(ctx, gr.g.Lifecycle, sv)
	}
	// A capability violation during setup is a setup failure even when the
	// consumer's function returned nil - the GroupOutcome must say which
	// group had trouble without the reader cross-referencing the case's
	// error arrays.
	if gr.setupErr == nil && sv.capViolations > 0 {
		gr.setupErr = fmt.Errorf("setup used a capability its stage does not have")
	}
	if gr.setupErr != nil {
		sv.Fail(fmt.Errorf("group %q setup: %w", gr.g.ID, gr.setupErr))
	}
	next := groupActive
	if gr.setupErr != nil {
		next = groupSetupFailed
	}
	if err := gr.state.to(next); err != nil {
		panic(err)
	}
	r.emitEvent(GroupEvent{eventBase: r.eventBaseFor(caseID, GroupSetupFinished),
		GroupID: gr.g.ID, Err: r.redactString(errString(gr.setupErr))})
}

// memberLanded decrements the group's completion barrier; when every member
// has a landed outcome of any kind and Setup was attempted, Teardown fires —
// always, contained, on a context detached from cancellation and bounded by
// the configured default Timeout, exactly as fixture teardown.
func (r *Runner) memberLanded(gr *groupRun, run *Run, caseID string) {
	gr.mu.Lock()
	gr.remaining--
	st := gr.state.state()
	fire := gr.remaining == 0 && (st == groupActive || st == groupSetupFailed)
	if fire {
		if err := gr.state.to(groupTearingDown); err != nil {
			gr.mu.Unlock()
			panic(err)
		}
	}
	gr.mu.Unlock()
	if !fire {
		return
	}
	tid := teardownID(gr.g.ID)
	tv := run.bound(tid, Timing{}, nil, false)
	tv.rank = gr.tearRank
	tv.source = EvidenceSource{Kind: SourceGroupTeardown, ID: gr.g.ID}
	tv.stage = stageGroupTeardown
	dctx := context.WithoutCancel(context.Background())
	if t := r.config.Defaults.Timeout; t > 0 {
		var cancel context.CancelFunc
		dctx, cancel = context.WithTimeout(dctx, t)
		defer cancel()
	}
	r.emitEvent(GroupEvent{eventBase: r.eventBaseFor(caseID, GroupTeardownStarted), GroupID: gr.g.ID})
	var tearErr error
	if gr.g.Lifecycle != nil {
		tearErr = runOneTeardown(dctx, gr.g.Lifecycle, tv)
	}
	if tearErr == nil && tv.capViolations > 0 {
		tearErr = fmt.Errorf("teardown used a capability its stage does not have")
	}
	if tearErr != nil {
		tv.Fail(fmt.Errorf("group %q teardown: %w", gr.g.ID, tearErr))
	}
	gr.mu.Lock()
	gr.tearErr = tearErr
	if err := gr.state.to(groupDone); err != nil {
		gr.mu.Unlock()
		panic(err)
	}
	gr.mu.Unlock()
	r.emitEvent(GroupEvent{eventBase: r.eventBaseFor(caseID, GroupTeardownFinished),
		GroupID: gr.g.ID, Err: r.redactString(errString(tearErr))})
}

// outcome derives the group's report row. Setup and teardown failures stay
// distinguishable — "this run never got its preconditions" and "a later run
// may be poisoned" have different debugging playbooks.
func (gr *groupRun) outcome() GroupOutcome {
	gr.mu.Lock()
	defer gr.mu.Unlock()
	out := GroupOutcome{GroupID: gr.g.ID, Members: append([]ID(nil), gr.g.Members...)}
	switch {
	case gr.state.state() == groupPending:
		out.Status = NotApplicable
		out.Reason = "no member ran"
	case gr.setupErr != nil:
		out.Status = Errored
		out.Reason = "setup failed: " + gr.setupErr.Error()
	case gr.tearErr != nil:
		out.Status = Errored
		out.Reason = "teardown failed: " + gr.tearErr.Error()
	default:
		out.Status = Passed
	}
	return out
}

// setupFailure reads the setup outcome under the same lock that decided it.
func (gr *groupRun) setupFailure() error {
	gr.mu.Lock()
	defer gr.mu.Unlock()
	return gr.setupErr
}

// failedGroupOf returns the first (declaration order) of the phase's groups
// whose setup was attempted and failed, or nil.
func failedGroupOf(groupIdxs []int, groupRuns []*groupRun) *groupRun {
	for _, gi := range groupIdxs {
		gr := groupRuns[gi]
		if gr.setupFailure() != nil {
			return gr
		}
	}
	return nil
}
