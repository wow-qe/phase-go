// Copyright 2026 The Phase Contributors
// SPDX-License-Identifier: MIT

package misuse

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	phase "github.com/wow-qe/phase-go"
	"github.com/wow-qe/phase-go/phasetest"
)

// Wrong DATA flowing through the correct pipeline: every knob on the
// sabotaged system must be answered by a loud, attributed, structurally
// pinned response — never a green report, never a vanished row.

func TestCorruptAuthCodeFailsWithBothValues(t *testing.T) {
	sys, cases := misuseSuite(t)
	sys.corruptAuthCode = true
	rep := run(t, mustMisuseRunner(t, sys, sane()), onlyCase(t, cases, "happy-single"))

	cr := rowOf(t, rep, "happy-single")
	if cr.Status != phase.Failed || cr.FailedIn != "authorize" {
		t.Fatalf("case = %v in %q, want Failed in authorize", cr.Status, cr.FailedIn)
	}
	var found bool
	for _, ar := range cr.Results {
		if ar.Result.Name == "auth code shape" && !ar.Result.Passed {
			found = true
			if ar.Result.Expected == nil || ar.Result.Actual == nil {
				t.Fatalf("failing comparison must carry both values: %+v", ar.Result)
			}
			// The go-cmp diff is the mechanism's own signature.
			if !strings.Contains(ar.Result.Reason, "-") || !strings.Contains(ar.Result.Reason, "+") {
				t.Fatalf("reason = %q, want a real diff", ar.Result.Reason)
			}
		}
	}
	if !found {
		t.Fatal("the failing auth comparison is missing from the evidence")
	}
	// Failing is evidence, not a wall: downstream phases still ran and the
	// report still verifies (checked by run()).
	if po := outcomeOf(t, cr, "settle_wait"); po.Status == phase.NotApplicable {
		t.Fatalf("settle_wait = %+v — a recorded failure must not prune the pipeline", po)
	}
}

func TestNeverSettlingSystemNamesItsBudget(t *testing.T) {
	sys, cases := misuseSuite(t)
	sys.neverSettle = true
	rep := run(t, mustMisuseRunner(t, sys, sane()), onlyCase(t, cases, "happy-single"))

	cr := rowOf(t, rep, "happy-single")
	// Budget exhaustion is an ERROR outcome (the wait concluded nothing),
	// and the budget is NAMED — never "nothing found".
	if cr.Status != phase.Errored {
		t.Fatalf("case = %v, want Errored — an exhausted wait concluded nothing", cr.Status)
	}
	if len(cr.Errors) == 0 || !strings.Contains(cr.Errors[0].Err, "gave up after") {
		t.Fatalf("errors = %+v, want the budget named", cr.Errors)
	}
	// Dependents prune structurally, not silently.
	for _, id := range []phase.ID{"settle_checks", "ledger", "audit"} {
		if po := outcomeOf(t, cr, id); po.DeclineSource != phase.DeclinedByDependency {
			t.Fatalf("%s decline_source = %q, want %q", id, po.DeclineSource, phase.DeclinedByDependency)
		}
	}
	// The group's teardown ran anyway.
	if n := sys.ActiveSubscriptions(); n != 0 {
		t.Fatalf("%d subscription(s) leaked past an errored member", n)
	}
}

func TestBudgetErrorCarriesTheSentinel(t *testing.T) {
	// The typed pin: at the call site the exhaustion must be
	// errors.Is-able, not a string.
	sys, cases := misuseSuite(t)
	sys.SeedCatalog()
	sys.neverSettle = true
	id, err := sys.SubmitOrder("happy", 1)
	if err != nil {
		t.Fatal(err)
	}
	sys.Authorize(id)
	c := onlyCase(t, cases, "happy-single")[0]
	runh, _ := phasetest.RunFor(t, c, "settle_wait", phase.Timing{Attempts: 3, Interval: time.Millisecond})
	_, werr := phase.WaitUntil(context.Background(), runh, func(context.Context) ([]string, bool, error) {
		got, done := sys.PollSettlement(id)
		return got, done, nil
	})
	if !errors.Is(werr, phase.ErrBudgetExhausted) {
		t.Fatalf("err = %v, want ErrBudgetExhausted in the chain", werr)
	}
}

func TestLostLedgerRowIsNamed(t *testing.T) {
	sys, cases := misuseSuite(t)
	sys.dropLedgerRow = true
	rep := run(t, mustMisuseRunner(t, sys, sane()), onlyCase(t, cases, "happy-multi"))

	cr := rowOf(t, rep, "happy-multi")
	if cr.Status != phase.Failed || cr.FailedIn != "ledger" {
		t.Fatalf("case = %v in %q, want Failed in ledger", cr.Status, cr.FailedIn)
	}
	var named bool
	for _, ar := range cr.Results {
		if ar.Result.Name == "every settled entity is ledgered" && !ar.Result.Passed {
			// The missing member is named — a reader learns WHICH row vanished.
			if strings.Contains(ar.Result.Reason, "/e3") {
				named = true
			}
		}
	}
	if !named {
		t.Fatal("the missing ledger row must be named in the failing result")
	}
}

func TestUnsettledEntityFailsItsOwnRow(t *testing.T) {
	sys, cases := misuseSuite(t)
	sys.wrongEntityState = true
	rep := run(t, mustMisuseRunner(t, sys, sane()), onlyCase(t, cases, "happy-multi"))

	cr := rowOf(t, rep, "happy-multi")
	if cr.Status != phase.Failed || cr.FailedIn != "settle_checks" {
		t.Fatalf("case = %v in %q, want Failed in settle_checks", cr.Status, cr.FailedIn)
	}
	// EachEntity: every entity keeps its own attributed row — 3 submitted,
	// 3 rows, each failing with ITS EntityRef, none silently dropped.
	var rows, failing int
	for _, ar := range cr.Results {
		if ar.Result.Name == "terminal state" {
			rows++
			if !ar.Result.Passed {
				failing++
				if ar.Result.Entity.ID == "" {
					t.Fatalf("failing per-entity row lost its entity: %+v", ar.Result)
				}
			}
		}
	}
	if rows != 3 || failing != 3 {
		t.Fatalf("per-entity rows = %d (failing %d), want 3/3 — nothing may be silently short", rows, failing)
	}
	// The failure triggers the When-gated refund audit, which finds no
	// refund and records its own failing comparison. The ROW status stays
	// the arc's ("passed" — the phase ran to completion); the failing count
	// is the sibling count that closes the reading gap, and the CASE
	// verdict rides the evidence.
	if po := outcomeOf(t, cr, "refund_audit"); po.DeclineSource != "" || po.Failing != 1 {
		t.Fatalf("refund_audit = %+v, want it to have run and recorded its failing comparison", po)
	}
}

func TestFlakeThatNeverHealsIsAFailureNotAFlake(t *testing.T) {
	sys, cases := misuseSuite(t)
	sys.permanentLedgerFlap = true
	rep := run(t, mustMisuseRunner(t, sys, sane()), onlyCase(t, cases, "happy-single"))

	cr := rowOf(t, rep, "happy-single")
	if cr.Status != phase.Failed || cr.FailedIn != "settle_checks" {
		t.Fatalf("case = %v in %q, want Failed in settle_checks — Flaked must NOT absorb a permanent failure", cr.Status, cr.FailedIn)
	}
	var exhausted bool
	for _, ar := range cr.Results {
		if ar.Result.Name == "ledger count" && !ar.Result.Passed &&
			strings.Contains(ar.Result.Reason, "still failing after all 3 tolerant attempts") {
			exhausted = true
		}
	}
	if !exhausted {
		t.Fatal("the exhausted tolerance must name its budget and its justification")
	}
	// The attempt trail survives as observations — evidence, not verdicts.
	var trail int
	for _, ob := range cr.Observations {
		if strings.Contains(ob.Name, "tolerated failure: ledger count") {
			trail++
		}
	}
	if trail != 3 {
		t.Fatalf("tolerance trail = %d observation(s), want all 3 attempts on the record", trail)
	}
}

func TestPanickingSystemIsContainedToItsCase(t *testing.T) {
	sys, cases := misuseSuite(t)
	sys.panicInSubmit = true
	// TWO cases in one session: the panicking one and a bystander. The
	// panic is the first case's evidence, never the batch's crash — but
	// with the shared system panicking, BOTH cases hit it; the point is the
	// session finishes and reports, it does not crash.
	rep := run(t, mustMisuseRunner(t, sys, sane()),
		append(onlyCase(t, cases, "happy-single"), onlyCase(t, cases, "declined-payment")...))

	for _, id := range []string{"happy-single", "declined-payment"} {
		cr := rowOf(t, rep, id)
		if cr.Status != phase.Errored {
			t.Fatalf("%s = %v, want Errored from the contained panic", id, cr.Status)
		}
		if len(cr.Errors) == 0 || !strings.Contains(cr.Errors[0].Err, "panic") {
			t.Fatalf("%s errors = %+v, want the contained panic on the record", id, cr.Errors)
		}
	}
}

func TestMutationDuringAuditIsCaught(t *testing.T) {
	sys, cases := misuseSuite(t)
	sys.mutateDuringAudit = true
	rep := run(t, mustMisuseRunner(t, sys, sane()), onlyCase(t, cases, "happy-single"))

	cr := rowOf(t, rep, "happy-single")
	if cr.Status != phase.Failed || cr.FailedIn != "audit" {
		t.Fatalf("case = %v in %q, want Failed in audit", cr.Status, cr.FailedIn)
	}
	var caught bool
	for _, ar := range cr.Results {
		if ar.Result.Name == "ledger stable during audit" && !ar.Result.Passed {
			caught = true
			if !strings.Contains(ar.Result.Reason, "ghost") {
				t.Fatalf("the appearing row must show in the diff: %q", ar.Result.Reason)
			}
		}
	}
	if !caught {
		t.Fatal("Unchanged must catch the mid-audit mutation")
	}
}

func TestUnhealthyProcessorIsEnvironmentNotProduct(t *testing.T) {
	sys, cases := misuseSuite(t)
	sys.mu.Lock()
	sys.healthy = false
	sys.mu.Unlock()
	rep := run(t, mustMisuseRunner(t, sys, sane()), onlyCase(t, cases, "happy-single"))

	cr := rowOf(t, rep, "happy-single")
	// The two-fact split's environment half: Errored, stage before, and NO
	// recorded product violation — nobody gets paged for the product.
	if cr.Status != phase.Errored || cr.FailedIn != "" {
		t.Fatalf("case = %v (FailedIn %q), want Errored with no product failure", cr.Status, cr.FailedIn)
	}
	po := outcomeOf(t, cr, "submit")
	if po.Status != phase.Errored || po.Stage != phase.StageBefore {
		t.Fatalf("submit = %+v, want Errored in the before stage", po)
	}
	for _, ar := range cr.Results {
		if !ar.Result.Passed {
			t.Fatalf("environment trouble must not record product violations: %+v", ar.Result)
		}
	}
}
