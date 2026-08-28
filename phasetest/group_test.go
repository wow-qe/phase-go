// Copyright 2026 The Phase Contributors
// SPDX-License-Identifier: MIT

package phasetest_test

import (
	"context"
	"testing"

	phase "github.com/wow-qe/phase-go"
	"github.com/wow-qe/phase-go/phasetest"
)

var topicKey = phase.Declare[string]("phasetest_group_topic")

type provisioningLifecycle struct{ tornDown bool }

func (l *provisioningLifecycle) Setup(_ context.Context, r *phase.Run) error {
	phase.Put(r, topicKey, "topic-42")
	return nil
}

func (l *provisioningLifecycle) Teardown(context.Context, *phase.Run) error {
	l.tornDown = true
	return nil
}

func TestRunGroupSetupExercisesTheLifecycle(t *testing.T) {
	l := &provisioningLifecycle{}
	g := phase.Group{ID: "settlement", Members: []phase.ID{"settle"},
		Produces: []phase.KeyID{topicKey.ID()}, Lifecycle: l}
	run, err := phasetest.RunGroupSetup(t, g, &stubCase{id: "c"})
	if err != nil {
		t.Fatalf("setup: %v", err)
	}
	v, err := phase.Get(run, topicKey)
	if err != nil || v != "topic-42" {
		t.Fatalf("handoff = %q, %v", v, err)
	}
	if err := phasetest.RunGroupTeardown(t, g, run); err != nil || !l.tornDown {
		t.Fatalf("teardown: %v tornDown=%v", err, l.tornDown)
	}
}

func TestConformanceGroupCatchesMisdeclarations(t *testing.T) {
	cases := map[string]phase.Group{
		"empty id":         {Members: []phase.ID{"a"}},
		"reserved char":    {ID: "g:x", Members: []phase.ID{"a"}},
		"no members":       {ID: "g"},
		"duplicate member": {ID: "g", Members: []phase.ID{"a", "a"}},
	}
	for name, g := range cases {
		t.Run(name, func(t *testing.T) {
			tb := &fakeTB{}
			phasetest.ConformanceGroup(tb, g)
			if len(tb.errs) == 0 {
				t.Fatalf("ConformanceGroup accepted a mis-declared group: %+v", g)
			}
		})
	}
	tb := &fakeTB{}
	phasetest.ConformanceGroup(tb, phase.Group{ID: "g", Members: []phase.ID{"a", "b"}})
	if len(tb.errs) != 0 {
		t.Fatalf("well-declared group refused: %v", tb.errs)
	}
}
