// Copyright 2026 The Phase Contributors
// SPDX-License-Identifier: MIT

package phasetest

import (
	"context"
	"testing"

	phase "github.com/wow-qe/phase-go"
)

// Group-scoped harness: a group's Setup/Teardown are
// consumer code, and consumers must be able to exercise them without a live
// system or a full Runner — the same purpose RunFor serves for phases.

// RunGroupSetup builds a test Run attributed exactly as the runner would
// attribute the group's setup ("group:<id>:setup"), invokes Setup on it,
// and returns the Run (for Get/handoff assertions) plus Setup's error.
// A nil Lifecycle is a no-op, as in production.
func RunGroupSetup(t testing.TB, g phase.Group, c phase.Case, opts ...phase.RunOption) (*phase.Run, error) {
	t.Helper()
	run := phase.NewRunForTesting(c, append(opts,
		phase.WithPhase(phase.ID("group:"+string(g.ID)+":setup"), phase.Timing{}))...)
	if g.Lifecycle == nil {
		return run, nil
	}
	return run, g.Lifecycle.Setup(context.Background(), run)
}

// RunGroupTeardown mirrors RunGroupSetup for the teardown half, attributed
// under "group:<id>:teardown". Pass the same Run a prior RunGroupSetup
// returned to exercise a full lifecycle against one evidence store.
func RunGroupTeardown(t testing.TB, g phase.Group, run *phase.Run) error {
	t.Helper()
	if g.Lifecycle == nil {
		return nil
	}
	return g.Lifecycle.Teardown(context.Background(), run)
}

// ConformanceGroup checks a Group declaration against the engine's own
// preflight rules, so a mis-declared group fails in the consumer's unit
// tests before it fails at NewRunner: non-empty ID without the reserved
// ':' character, at least one member, no duplicate members. (Whether a
// member exists is a pipeline fact NewRunner alone can check.)
func ConformanceGroup(t testing.TB, g phase.Group) {
	t.Helper()
	if g.ID == "" {
		t.Error("phasetest: Group.ID is empty — an anonymous group cannot be found in a report")
	}
	for _, r := range string(g.ID) {
		if r == ':' {
			t.Errorf("phasetest: Group.ID %q contains ':' — the namespace is reserved for group attribution", g.ID)
		}
	}
	if len(g.Members) == 0 {
		t.Error("phasetest: Group has no members — it scopes nothing")
	}
	seen := map[phase.ID]bool{}
	for _, m := range g.Members {
		if seen[m] {
			t.Errorf("phasetest: Group member %q listed twice", m)
		}
		seen[m] = true
	}
}
