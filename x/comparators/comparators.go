// Copyright 2026 The Phase Contributors
// SPDX-License-Identifier: MIT

// Package comparators is the comparison boilerplate every consumer was
// about to hand-roll, built once on the result package's invariant: a
// comparison over nothing is a failure with a reason, never a pass, and a
// failing result names what it saw.
package comparators

import (
	"context"
	"errors"
	"fmt"

	gocmp "github.com/google/go-cmp/cmp"
	phase "github.com/wow-qe/phase-go"
	"github.com/wow-qe/phase-go/result"
)

// ContainsAll passes when every wanted element appears in got (order-free,
// duplicates tolerated). A failure names exactly what is missing; wanting
// nothing is zero comparisons and therefore a failure — the founding rule,
// held in every comparator.
func ContainsAll[T comparable](name string, want, got []T) result.Result {
	if len(want) == 0 {
		return result.Failed(name, "containing all of the empty set is zero comparisons; declare what must be present").
			WithActual(got)
	}
	have := make(map[T]bool, len(got))
	for _, g := range got {
		have[g] = true
	}
	var missing []T
	checks := make([]bool, len(want))
	for i, w := range want {
		checks[i] = have[w]
		if !have[w] {
			missing = append(missing, w)
		}
	}
	r := result.Compared(name, checks).WithExpected(want).WithActual(got)
	if len(missing) > 0 {
		r = result.Failed(name, fmt.Sprintf("missing %v (%d of %d present)", missing, len(want)-len(missing), len(want))).
			WithExpected(want).WithActual(got)
	}
	return r
}

// ValueMatch compares want against got with go-cmp; a mismatch carries the
// diff in the reason and both values as evidence.
func ValueMatch(name string, want, got any) result.Result {
	if diff := gocmp.Diff(want, got); diff != "" {
		return result.Failed(name, "values differ (-want +got):\n"+diff).
			WithExpected(want).WithActual(got)
	}
	return result.Compared(name, []bool{true}).WithExpected(want).WithActual(got)
}

// EachEntity runs check once per entity and attributes each result to its
// entity. ZERO entities yields one failing result: "checked every entity"
// over an empty set is exactly the all([])-is-true defect this library was
// founded against.
func EachEntity(name string, entities []result.EntityRef, check func(result.EntityRef) result.Result) []result.Result {
	if len(entities) == 0 {
		return []result.Result{result.Failed(name,
			"zero entities to check; a judgement over the empty set is a coverage gap, not a pass")}
	}
	out := make([]result.Result, len(entities))
	for i, e := range entities {
		out[i] = check(e).ForEntity(e)
	}
	return out
}

// Unchanged asserts a no-effect expectation: after must equal before. The
// name should say what was NOT supposed to happen ("balance unchanged").
func Unchanged(name string, before, after any) result.Result {
	return ValueMatch(name, before, after)
}

// PollCompare fetches until the value equals want, under the current
// phase's WaitUntil budget. Budget exhaustion is a FAILING RESULT carrying
// the last observed value and the budget — the poll concluded "still
// wrong", which is a judgement — while a transport error or cancellation
// returns as the error it is (poll, tolerance and transport retries
// stay distinct).
func PollCompare[T comparable](ctx context.Context, r *phase.Run, name string, want T, fetch func(context.Context) (T, error)) (result.Result, error) {
	var last T
	_, err := phase.WaitUntil(ctx, r, func(ctx context.Context) (T, bool, error) {
		v, err := fetch(ctx)
		if err != nil {
			return v, false, err
		}
		last = v
		return v, v == want, nil
	})
	if err == nil {
		return result.Compared(name, []bool{true}).WithExpected(want).WithActual(last), nil
	}
	if errors.Is(err, phase.ErrBudgetExhausted) {
		// WaitUntil's error already names the budget ("gave up after 3×1s in
		// settle") — keep it verbatim so the failure reads the same wherever
		// the budget is spent.
		return result.Failed(name,
			fmt.Sprintf("still %v, want %v; %s", last, want, err.Error())).
			WithExpected(want).WithActual(last), nil
	}
	return result.Result{}, err
}
