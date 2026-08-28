// Copyright 2026 The Phase Contributors
// SPDX-License-Identifier: MIT

package phase

import "fmt"

// The typed lifecycle machine — used only where a real bug class exists,
// per entity, not everywhere. groupRun is that entity (a TOCTOU-prone
// spot); runCore's sealed stays the named one-way-gate idiom; linear
// single-goroutine flows stay linear. Transition tables are data: the docs
// render from them.

type machine[S comparable] struct {
	cur   S
	legal map[S][]S
}

func newMachine[S comparable](start S, legal map[S][]S) *machine[S] {
	return &machine[S]{cur: start, legal: legal}
}

// to transitions or refuses: an illegal transition is a bug in phase, not
// a condition to handle — the zero-comparisons discipline, applied to control flow.
func (m *machine[S]) to(next S) error {
	for _, ok := range m.legal[m.cur] {
		if ok == next {
			m.cur = next
			return nil
		}
	}
	return &FrameworkError{Invariant: "lifecycle transitions",
		Detail: fmt.Sprintf("illegal transition %v -> %v", m.cur, next)}
}

func (m *machine[S]) state() S { return m.cur }

// groupState is the group lifecycle, reified as an explicit state.
type groupState uint8

const (
	groupPending groupState = iota
	groupSettingUp
	groupActive
	groupTearingDown
	groupDone
	groupSetupFailed
)

func (s groupState) String() string {
	switch s {
	case groupPending:
		return "pending"
	case groupSettingUp:
		return "setting-up"
	case groupActive:
		return "active"
	case groupTearingDown:
		return "tearing-down"
	case groupDone:
		return "done"
	case groupSetupFailed:
		return "setup-failed"
	}
	return "unknown"
}

// groupTransitions is the rendered table. Teardown runs from both
// active and setup-failed (setup was attempted: resources may be held).
var groupTransitions = map[groupState][]groupState{
	groupPending:     {groupSettingUp},
	groupSettingUp:   {groupActive, groupSetupFailed},
	groupActive:      {groupTearingDown},
	groupSetupFailed: {groupTearingDown},
	groupTearingDown: {groupDone},
}
