// Copyright 2026 The Phase Contributors
// SPDX-License-Identifier: MIT

package phase

import (
	"errors"
	"sync"
	"testing"

	"github.com/wow-qe/phase-go/result"
)

// Typed keys are how one phase hands a value to a later phase. Three
// properties hold: Get fails rather than returning a zero value for a
// key that was never produced, at most one phase may Put a given key
// per run, and the whole store is typed.

var (
	testRequestID = Declare[string]("test_request_id")
	testItems     = Declare[[]int]("test_items")
)

func newTestRun() *Run { return newRun(nil, Scope{}) }

func TestGetFailsWhenNeverProduced(t *testing.T) {
	r := newTestRun()
	v, err := Get(r, testRequestID)
	if err == nil {
		t.Fatal("Get on an unproduced key must error — a zero value here is the all([]) defect wearing different clothes")
	}
	if !errors.Is(err, ErrKeyNotProduced) {
		t.Fatalf("err = %v, want ErrKeyNotProduced", err)
	}
	if v != "" {
		t.Fatalf("on error the zero value is returned explicitly, got %q", v)
	}
}

func TestPutThenGetRoundTrips(t *testing.T) {
	r := newTestRun()
	Put(r, testRequestID, "req-42")
	got, err := Get(r, testRequestID)
	if err != nil || got != "req-42" {
		t.Fatalf("got %q, %v", got, err)
	}
	Put(r, testItems, []int{3, 1})
	items, err := Get(r, testItems)
	if err != nil || len(items) != 2 {
		t.Fatalf("items = %v, %v", items, err)
	}
}

func TestDuplicatePutIsAFrameworkError(t *testing.T) {
	// One writer per key. If a value legitimately changes, that is a second
	// key with a name saying so — never an overwrite.
	r := newTestRun()
	Put(r, testRequestID, "first")
	err := tryPut(r, testRequestID, "second")
	var fe *FrameworkError
	if !errors.As(err, &fe) {
		t.Fatalf("duplicate Put must be a *FrameworkError, got %v", err)
	}
	got, _ := Get(r, testRequestID)
	if got != "first" {
		t.Fatalf("the first value must survive, got %q", got)
	}
}

func TestDeclareRefusesDuplicateNames(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("duplicate Declare must panic at init — it is a programming error, not a runtime condition")
		}
	}()
	Declare[string]("test_request_id") // already declared at package init above
}

func TestFailAndRecordAreDistinctFacts(t *testing.T) {
	// "The query blew up" and "the comparison failed" are different facts; a
	// framework that flattens them reports an outage as a product failure.
	r := newTestRun()
	r.Fail(errors.New("connection refused"))
	r.Record(result.Failed("state mismatch", "expected 8, saw 9"))
	if len(r.snapshotErrors()) != 1 {
		t.Fatalf("errors = %d, want 1", len(r.snapshotErrors()))
	}
	if len(r.snapshotResults()) != 1 {
		t.Fatalf("results = %d, want 1", len(r.snapshotResults()))
	}
}

func TestObserveKeepsWhatWasSeen(t *testing.T) {
	// A Result says what was decided; an Observation says what was seen.
	// Keeping only the first is how a report becomes unfalsifiable.
	r := newTestRun()
	r.Observe("rows", []string{"a", "b"})
	obs := r.snapshotObservations()
	if len(obs) != 1 || obs[0].Name != "rows" {
		t.Fatalf("observations = %+v", obs)
	}
}

func TestRecorderIsConcurrencySafe(t *testing.T) {
	// Recorder is safe for concurrent use within one Run. Run under
	// -race, this is the test that proves it.
	r := newTestRun()
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r.Record(result.Compared("concurrent check", []bool{true}))
			r.Observe("tick", 1)
		}()
	}
	wg.Wait()
	if got := len(r.snapshotResults()); got != 50 {
		t.Fatalf("results = %d, want 50", got)
	}
}

func TestRequireRoutesErrorsToFailNotResults(t *testing.T) {
	r := newTestRun()
	v, ok := Require(r, "value", error(nil))
	if !ok || v != "value" {
		t.Fatalf("Require on success = %q, %v", v, ok)
	}
	v, ok = Require(r, "ignored", errors.New("boom"))
	if ok {
		t.Fatal("Require on error must report not-ok")
	}
	if v != "" {
		t.Fatalf("Require on error returns the zero value, got %q", v)
	}
	if len(r.snapshotErrors()) != 1 || len(r.snapshotResults()) != 0 {
		t.Fatal("the error must land in Fail, never in Results")
	}
}

var _ Recorder = (*Run)(nil) // *Run satisfies Recorder — compile-time proof
