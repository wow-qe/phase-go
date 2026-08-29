// Copyright 2026 The Phase Contributors
// SPDX-License-Identifier: MIT

package phase

import (
	"fmt"
	"sync"
)

// Typed keys are how one phase hands a value to a later phase.
//
// The alternative — phases mutating a shared case object — allows multiple
// writers for one field and zero-value reads when a producer never ran,
// with "empty" indistinguishable from "never produced". Hence the three
// properties enforced here:
//
//  1. Get fails when the key was never produced; it does not return a zero
//     value. "Discovery never ran" and "discovery found nothing" are
//     different facts.
//  2. One writer per key per run. If a value legitimately changes, that is a
//     second key with a name saying so — never an overwrite.
//  3. The store is typed. Declare[T] at package init; Put/Get are
//     compile-time checked against the key's type.
//
// Preflight additionally validates the graph: every Requires() key must be
// produced by some phase in the transitive DependsOn set, before anything
// runs.

// Key is a typed handle to a handoff value.
type Key[T any] struct{ id KeyID }

// ID returns the untyped identity, for Produces/Requires declarations.
func (k Key[T]) ID() KeyID { return k.id }

var keyRegistry sync.Map // KeyID -> struct{}

// Declare registers a key at package init. Declaring the same name twice
// panics: two packages claiming one key is a programming error visible the
// moment the program starts, not a runtime condition to handle.
func Declare[T any](name string) Key[T] {
	if _, loaded := keyRegistry.LoadOrStore(KeyID(name), struct{}{}); loaded {
		panic(fmt.Sprintf("phase: key %q declared twice — two packages are claiming the same handoff value", name))
	}
	return Key[T]{id: KeyID(name)}
}

// keyed is what Keys accepts: anything with a key identity.
type keyed interface{ ID() KeyID }

// Keys is declaration sugar for Produces() / Requires().
func Keys(ks ...keyed) []KeyID {
	out := make([]KeyID, len(ks))
	for i, k := range ks {
		out[i] = k.ID()
	}
	return out
}

// Put stores a phase's produced value on the run. A duplicate Put of the same
// key in one run is a framework-invariant violation and panics via the run's
// invariant handler — the graph validation should have made it impossible, so
// reaching it means a phase's Produces() declaration was not honored.
func Put[T any](r *Run, k Key[T], v T) {
	if err := tryPut(r, k, v); err != nil {
		r.Fail(err)
	}
}

// tryPut is Put returning the violation instead of recording it — split out
// so the invariant is testable without a full run.
func tryPut[T any](r *Run, k Key[T], v T) error {
	if !r.can(capPut) {
		r.capViolations++
		return &FrameworkError{Invariant: "stage capabilities",
			Detail: fmt.Sprintf("Put is not permitted in the %s stage", r.stage)}
	}
	r.core.mu.Lock()
	defer r.core.mu.Unlock()
	r.core.mustBeOpen("Put", r.phase)
	// A phase may only Put what it declared in Produces(). Without this the
	// preflight graph and runtime reality could silently diverge: a phase
	// could smuggle an undeclared key past validation and still pass.
	if r.allowed != nil && !r.allowed[k.id] {
		return &FrameworkError{
			Invariant: "puts match Produces()",
			Detail:    fmt.Sprintf("phase %q Put key %q, which it did not declare in Produces()", r.phase, k.id),
		}
	}
	if _, exists := r.core.facts[k.id]; exists {
		return &FrameworkError{
			Invariant: "one writer per key",
			Detail:    fmt.Sprintf("key %q was produced twice in one run", k.id),
		}
	}
	r.core.facts[k.id] = v
	return nil
}

// Get retrieves a value an earlier phase produced. If no phase produced it,
// Get returns ErrKeyNotProduced (wrapped with the key's name) and the zero
// value explicitly — never a silently-usable zero.
func Get[T any](r *Run, k Key[T]) (T, error) {
	var zero T
	if !r.can(capGet) {
		return zero, &FrameworkError{Invariant: "stage capabilities",
			Detail: fmt.Sprintf("Get is not permitted in the %s stage", r.stage)}
	}
	r.core.mu.Lock()
	v, ok := r.core.facts[k.id]
	r.core.mu.Unlock()
	if !ok {
		return zero, fmt.Errorf("key %q: %w", k.id, ErrKeyNotProduced)
	}
	typed, ok := v.(T)
	if !ok {
		// Unreachable while Declare is the only key constructor; kept as a
		// defensive check in case that constraint is ever relaxed.
		return zero, &FrameworkError{
			Invariant: "typed handoff",
			Detail:    fmt.Sprintf("key %q holds %T, not the declared type", k.id, v),
		}
	}
	return typed, nil
}
