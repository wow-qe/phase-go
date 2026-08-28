// Copyright 2026 The Phase Contributors
// SPDX-License-Identifier: MIT

// Package result defines the outcome of a comparison, with the evidence that
// it happened.
//
// The rule this package exists for:
//
//	A result over zero comparisons is a failure, never a pass — and it must
//	name what it failed to compare.
//
// `all([])` is true in every language, and the resulting defect is a suite
// that reports green while checking nothing. The framework this design
// generalises from patched that shape locally three times and it returned
// each time, because each fix was an instance rather than the class. Here the
// class is inexpressible: every field is unexported, so no struct literal can
// construct a pass, and the only path to one — Compared — refuses the empty
// set.
//
// The package imports nothing outside the standard library and knows nothing
// about phases, so a consumer's comparator can depend on it alone.
package result

import "fmt"

// EntityRef identifies one of the entities a request fans out into. It lives
// in this package (rather than the root) because results carry it and this
// package must not import the root; the root re-exports it as phase.EntityRef.
type EntityRef struct {
	Kind string `json:"kind"`
	ID   string `json:"id"`
}

// Result is the outcome of one named comparison.
//
// The name is the result's stable identity — the same string on every run,
// for every entity. Volatile values (identifiers, timestamps, the values
// themselves) belong in expected/actual/reason, which snapshot comparison
// deliberately does not key on. Name and evidence are separate fields so that
// discipline is possible.
//
// The zero value is a failure with no name; it is useless but harmless, and
// deliberately not a pass.
type Result struct {
	passed      bool
	name        string
	entity      EntityRef
	expected    any
	actual      any
	reason      string
	comparisons int
}

// Compared is the only way to produce a passing Result: the verdict of a set
// of comparisons, refusing the empty set.
func Compared(name string, checks []bool) Result {
	if len(checks) == 0 {
		return Result{
			name:   name,
			reason: fmt.Sprintf("nothing was compared for %s; zero comparisons is a failure, not a pass", name),
		}
	}
	failed := 0
	for _, ok := range checks {
		if !ok {
			failed++
		}
	}
	r := Result{
		name:        name,
		comparisons: len(checks),
		passed:      failed == 0,
	}
	if failed > 0 {
		r.reason = fmt.Sprintf("%d of %d comparisons failed for %s", failed, len(checks), name)
	}
	return r
}

// Failed is an explicit failure. Always legal — refusing to pass never needs
// justifying — but it does need explaining: an empty reason is replaced with a
// loud placeholder rather than preserved, so the omission is visible in the
// report instead of silent.
func Failed(name, reason string) Result {
	if reason == "" {
		reason = "(no reason was given — the caller failed this check without saying why)"
	}
	return Result{name: name, reason: reason, comparisons: 1}
}

// WithExpected returns a copy carrying the declared value. Value semantics:
// a Result is evidence, not a draft, so builder methods never mutate the
// receiver and never alter the outcome.
func (r Result) WithExpected(v any) Result { r.expected = v; return r }

// WithActual returns a copy carrying the observed value.
func (r Result) WithActual(v any) Result { r.actual = v; return r }

// ForEntity returns a copy attributed to one entity.
func (r Result) ForEntity(e EntityRef) Result { r.entity = e; return r }

// Passed reports the outcome.
func (r Result) Passed() bool { return r.passed }

// Name is the result's stable identity. See the type comment.
func (r Result) Name() string { return r.name }

// Reason says why the result failed. Empty on a pass — a pass needs no
// justification.
func (r Result) Reason() string { return r.reason }

// Expected is the declared value, if the comparator attached it.
func (r Result) Expected() any { return r.expected }

// Actual is the observed value, if the comparator attached it.
func (r Result) Actual() any { return r.actual }

// Entity is the entity this result concerns, if attributed.
func (r Result) Entity() EntityRef { return r.entity }

// Comparisons is how many comparisons produced this result. It is zero only
// on the refused-empty-set failure and on the zero value; it can never be
// zero on a pass.
func (r Result) Comparisons() int { return r.comparisons }
