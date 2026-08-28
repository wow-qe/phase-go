// Copyright 2026 The Phase Contributors
// SPDX-License-Identifier: MIT

package phase

import "fmt"

// capability is a table of what each stage of the machinery may do with a
// Run handle: one table, defined as data, so a stage's permissions can be
// read at a glance, rendered into documentation, and pinned by a test.
//
// Fail is deliberately ungated — it is the violation channel itself, not a
// capability that could be revoked.

type capability uint8

const (
	capRecord capability = 1 << iota
	capObserve
	capPut
	capGet
	capPriorEvidence
)

// stageKind names the machinery stage a view is bound for.
type stageKind uint8

const (
	stageExec            stageKind = iota // Before/Run/After share the execution view
	stageWhen                             // the condition gate: reads the record, never writes it
	stageGroupSetup                       // provisions; Put restricted to Group.Produces
	stageGroupTeardown                    // concludes; nothing runs after it to consume a Put
	stageFixtureSetup                     // case-scoped world building
	stageFixtureTeardown                  // case-scoped cleanup; same no-downstream rationale as group teardown
	stageSession                          // session-level events (cancellation)
)

var stageCaps = map[stageKind]capability{
	stageExec:            capRecord | capObserve | capPut | capGet | capPriorEvidence,
	stageWhen:            capObserve | capGet | capPriorEvidence,
	stageGroupSetup:      capRecord | capObserve | capPut | capGet,
	stageGroupTeardown:   capRecord | capObserve | capGet,
	stageFixtureSetup:    capRecord | capObserve | capPut | capGet,
	stageFixtureTeardown: capRecord | capObserve | capGet,
	stageSession:         capRecord | capObserve | capGet,
}

func (s stageKind) String() string {
	switch s {
	case stageExec:
		return "execution"
	case stageWhen:
		return "condition"
	case stageGroupSetup:
		return "group setup"
	case stageGroupTeardown:
		return "group teardown"
	case stageFixtureSetup:
		return "fixture setup"
	case stageFixtureTeardown:
		return "fixture teardown"
	case stageSession:
		return "session"
	}
	return "unknown"
}

// deny records a capability violation: on the environment channel, counted
// on the view so the caller can land it as the outcome.
func (r *Run) deny(what string) {
	r.capViolations++
	r.Fail(&FrameworkError{Invariant: "stage capabilities",
		Detail: fmt.Sprintf("%s is not permitted in the %s stage", what, r.stage)})
}

func (r *Run) can(c capability) bool { return stageCaps[r.stage]&c != 0 }
