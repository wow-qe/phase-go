// Copyright 2026 The Phase Contributors
// SPDX-License-Identifier: MIT

package phase

import "fmt"

// ONE capability table says what each stage of the
// machinery may do with a Run handle. It replaces gates that grew
// field-by-field and disagreed about polarity (allowed==nil meant
// unrestricted Put while depScope==nil meant PriorEvidence refused).
// The table is CODE: the doc renders from it and a test pins it.
//
// Fail is deliberately ungated — it IS the violation channel.

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
