// Copyright 2026 The Phase Contributors
// SPDX-License-Identifier: MIT

package phasetest_test

import (
	"context"

	phase "github.com/wow-qe/phase-go"
)

// stubCase is a minimal, well-behaved phase.Case for tests that need a real
// Case but do not care about its details. Every field has a safe default;
// tests that need to provoke a specific ConformanceCase violation override
// exactly the relevant func field.
type stubCase struct {
	id        string
	status    phase.CaseStatus
	selects   func(phase.ID) (bool, string)
	timing    func(phase.ID) (phase.Timing, bool)
	fixtures  []phase.Fixture
	exclusive func() (bool, string)
}

func (c *stubCase) ID() string { return c.id }

func (c *stubCase) Status() phase.CaseStatus { return c.status }

func (c *stubCase) Selects(id phase.ID) (bool, string) {
	if c.selects != nil {
		return c.selects(id)
	}
	return true, ""
}

func (c *stubCase) Timing(id phase.ID) (phase.Timing, bool) {
	if c.timing != nil {
		return c.timing(id)
	}
	return phase.Timing{}, false
}

func (c *stubCase) Fixtures() []phase.Fixture { return c.fixtures }

func (c *stubCase) Exclusive() (bool, string) {
	if c.exclusive != nil {
		return c.exclusive()
	}
	return false, ""
}

var _ phase.Case = (*stubCase)(nil)

// goodCase returns a stubCase that satisfies every ConformanceCase check —
// the baseline each violation test perturbs in exactly one way.
func goodCase() *stubCase {
	return &stubCase{
		id:     "well-behaved-case",
		status: phase.Active,
		selects: func(phase.ID) (bool, string) {
			return false, "not selected by this well-behaved stub"
		},
		timing: func(phase.ID) (phase.Timing, bool) {
			return phase.Timing{}, false
		},
		fixtures: []phase.Fixture{noopFixture{}},
		exclusive: func() (bool, string) {
			return false, ""
		},
	}
}

// noopFixture is a harmless, non-nil phase.Fixture.
type noopFixture struct{}

func (noopFixture) Setup(ctx context.Context, r *phase.Run) error    { return nil }
func (noopFixture) Teardown(ctx context.Context, r *phase.Run) error { return nil }

var _ phase.Fixture = noopFixture{}
