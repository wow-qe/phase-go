// Copyright 2026 The Phase Contributors
// SPDX-License-Identifier: MIT

package checkout

import (
	"context"
	"fmt"

	phase "github.com/wow-qe/phase-go"
	"github.com/wow-qe/phase-go/result"
	cmp "github.com/wow-qe/phase-go/x/comparators"
)

// The pipeline: submit → authorize → [settlement group: settle_wait →
// settle_checks] → ledger → refund_audit (When-gated) → audit.

// --- submit: BeforeHook precondition + Transcribe -------------------------

type submitPhase struct{ sys *checkoutSystem }

func (p *submitPhase) ID() phase.ID            { return "submit" }
func (p *submitPhase) DependsOn() []phase.ID   { return nil }
func (p *submitPhase) Produces() []phase.KeyID { return phase.Keys(OrderID) }
func (p *submitPhase) Requires() []phase.KeyID { return nil }
func (p *submitPhase) AppliesTo(phase.Case, phase.Config) phase.Applicability {
	return phase.Applies() // every case submits something
}

// Before probes preconditions against live state (the one stage allowed
// to): catalog seeded, processor healthy. Side-effect-free by contract.
// The two-fact pattern: a violation records evidence and returns the error.
func (p *submitPhase) Before(_ context.Context, r *phase.Run) error {
	if !p.sys.CatalogSeeded() {
		r.Record(result.Failed("catalog seeded before submit", "the fixture must run first").
			WithExpected(true).WithActual(false))
		return fmt.Errorf("precondition violated: catalog not seeded")
	}
	if !p.sys.Healthy() {
		return fmt.Errorf("processor unhealthy") // environment fact: no recorded violation
	}
	return nil
}

func (p *submitPhase) Run(_ context.Context, r *phase.Run) error {
	c := r.Case().(*checkoutCase)
	// Require is the sanctioned adapter-call pattern: the error routes into
	// Fail (the case goes Errored), and "flatten the error into an empty
	// result" is unwritable.
	submitted, err := p.sys.SubmitOrder(c.Scenario(), c.Entities())
	id, ok := phase.Require(r, submitted, err)
	if !ok {
		return nil
	}
	// Transcribe keeps the exchange as structured evidence.
	r.Transcribe("POST /orders", map[string]any{"scenario": c.Scenario(), "entities": c.Entities()},
		map[string]any{"order_id": id})
	phase.Put(r, OrderID, id)
	r.Record(result.Compared("order accepted", []bool{id != ""}).WithActual(id))
	return nil
}

// --- authorize: comparators + AfterHook tally ----------------------------

type authorizePhase struct{ sys *checkoutSystem }

func (p *authorizePhase) ID() phase.ID            { return "authorize" }
func (p *authorizePhase) DependsOn() []phase.ID   { return []phase.ID{"submit"} }
func (p *authorizePhase) Produces() []phase.KeyID { return phase.Keys(AuthCode) }
func (p *authorizePhase) Requires() []phase.KeyID { return phase.Keys(OrderID) }
func (p *authorizePhase) AppliesTo(c phase.Case, _ phase.Config) phase.Applicability {
	return phase.Applies()
}

func (p *authorizePhase) Run(_ context.Context, r *phase.Run) error {
	orderID, err := phase.Get(r, OrderID)
	if err != nil {
		return err
	}
	code, ok := p.sys.Authorize(orderID)
	if !ok {
		r.Record(result.Failed("payment authorized", "processor declined the payment").
			WithExpected("an auth code").WithActual("declined"))
		return nil // a failed comparison, not an environment error
	}
	phase.Put(r, AuthCode, code)
	r.Record(cmp.ValueMatch("auth code shape", "auth-"+orderID, code))
	return nil
}

// After tallies the phase's own work — the conclusions hook.
func (p *authorizePhase) After(_ context.Context, r *phase.Run, po phase.PhaseOutcome) error {
	r.Observe("authorize tally", fmt.Sprintf("landed %s", po.Status))
	return nil
}

// --- the settlement group members ----------------------------------------

// settleWait polls until the eventually-consistent settlement lands
// (WaitUntil: attempts-budgeted, live RetryAttempt heartbeats) and hands
// the settled set forward. Produce-xor-assert: it produces, settle_checks
// asserts.
type settleWaitPhase struct{ sys *checkoutSystem }

func (p *settleWaitPhase) ID() phase.ID          { return "settle_wait" }
func (p *settleWaitPhase) DependsOn() []phase.ID { return []phase.ID{"authorize"} }
func (p *settleWaitPhase) Produces() []phase.KeyID {
	return phase.Keys(SettledEntities)
}
func (p *settleWaitPhase) Requires() []phase.KeyID {
	// StreamCursor is produced by the group's lifecycle setup.
	return phase.Keys(OrderID, StreamCursor)
}
func (p *settleWaitPhase) AppliesTo(c phase.Case, _ phase.Config) phase.Applicability {
	return phase.Applies()
}

func (p *settleWaitPhase) Run(ctx context.Context, r *phase.Run) error {
	orderID, err := phase.Get(r, OrderID)
	if err != nil {
		return err
	}
	if _, err := phase.Get(r, StreamCursor); err != nil {
		return err // the group's resource must be provisioned
	}
	entities, err := phase.WaitUntil(ctx, r, func(context.Context) ([]string, bool, error) {
		got, settled := p.sys.PollSettlement(orderID)
		return got, settled, nil
	})
	if err != nil {
		return err
	}
	phase.Put(r, SettledEntities, entities)
	r.Observe("settled entities", entities)
	return nil
}

// settleChecks asserts over what settle_wait found: per-entity state via
// EachEntity, and a ledger count that needs Tolerate on flappy days.
type settleChecksPhase struct{ sys *checkoutSystem }

func (p *settleChecksPhase) ID() phase.ID            { return "settle_checks" }
func (p *settleChecksPhase) DependsOn() []phase.ID   { return []phase.ID{"settle_wait"} }
func (p *settleChecksPhase) Produces() []phase.KeyID { return nil }
func (p *settleChecksPhase) Requires() []phase.KeyID {
	return phase.Keys(OrderID, SettledEntities)
}
func (p *settleChecksPhase) AppliesTo(c phase.Case, _ phase.Config) phase.Applicability {
	return phase.Applies()
}

func (p *settleChecksPhase) Run(ctx context.Context, r *phase.Run) error {
	orderID, err := phase.Get(r, OrderID)
	if err != nil {
		return err
	}
	entities, err := phase.Get(r, SettledEntities)
	if err != nil {
		return err
	}
	refs := make([]result.EntityRef, len(entities))
	for i, e := range entities {
		refs[i] = result.EntityRef{Kind: "entity", ID: e}
	}
	for _, res := range cmp.EachEntity("entity settled", refs, func(e result.EntityRef) result.Result {
		return cmp.ValueMatch("terminal state", "settled", p.sys.EntityState(e.ID))
	}) {
		r.Record(res)
	}
	// The declared-flaky read: first count can come back one short on
	// flappy days; a pass on retry surfaces the case as Flaked.
	res, err := phase.Tolerate(ctx, r, "ledger indexer lags one read behind settlement", 3,
		func(context.Context) result.Result {
			return cmp.ValueMatch("ledger count", len(entities), p.sys.LedgerCount(orderID))
		})
	_ = res
	return err
}

// --- ledger --------------------------------------------------------------

type ledgerPhase struct{ sys *checkoutSystem }

func (p *ledgerPhase) ID() phase.ID            { return "ledger" }
func (p *ledgerPhase) DependsOn() []phase.ID   { return []phase.ID{"settle_checks"} }
func (p *ledgerPhase) Produces() []phase.KeyID { return nil }
func (p *ledgerPhase) Requires() []phase.KeyID { return phase.Keys(OrderID, SettledEntities) }
func (p *ledgerPhase) AppliesTo(c phase.Case, _ phase.Config) phase.Applicability {
	return phase.Applies()
}

func (p *ledgerPhase) Run(ctx context.Context, r *phase.Run) error {
	orderID, _ := phase.Get(r, OrderID)
	entities, err := phase.Get(r, SettledEntities)
	if err != nil {
		return err
	}
	// PollCompare: fetch-until-equal under the phase's WaitUntil budget —
	// budget exhaustion is a failing result naming the last value seen.
	res, err := cmp.PollCompare(ctx, r, "ledger row count", len(entities),
		func(context.Context) (int, error) { return p.sys.LedgerCount(orderID), nil })
	if err != nil {
		return err
	}
	r.Record(res)
	r.Record(cmp.ContainsAll("every settled entity is ledgered", entities, p.sys.LedgerRows(orderID)))
	return nil
}

// --- refund_audit: the When-gated phase ----------------------------------

// refundAudit runs only when settlement recorded failures — a condition
// over recorded evidence via PriorEvidence, never live state. On green
// paths it declines with a recorded reason (visible NotApplicable).
type refundAuditPhase struct{ sys *checkoutSystem }

func (p *refundAuditPhase) ID() phase.ID            { return "refund_audit" }
func (p *refundAuditPhase) DependsOn() []phase.ID   { return []phase.ID{"settle_checks"} }
func (p *refundAuditPhase) Produces() []phase.KeyID { return nil }
func (p *refundAuditPhase) Requires() []phase.KeyID { return phase.Keys(OrderID) }
func (p *refundAuditPhase) AppliesTo(c phase.Case, _ phase.Config) phase.Applicability {
	return phase.Applies()
}

func (p *refundAuditPhase) When(_ context.Context, r *phase.Run) (bool, string, error) {
	ev, err := r.PriorEvidence("settle_checks")
	if err != nil {
		return false, "", err
	}
	if ev.Failing == 0 {
		return false, "settlement recorded no failures; nothing to refund", nil
	}
	return true, "", nil
}

func (p *refundAuditPhase) Run(_ context.Context, r *phase.Run) error {
	orderID, _ := phase.Get(r, OrderID)
	r.Record(result.Compared("refund issued for failed settlement", []bool{p.sys.Refunded(orderID)}).
		WithExpected(true).WithActual(p.sys.Refunded(orderID)))
	return nil
}

// --- audit: the terminal tally -------------------------------------------

type auditPhase struct{ sys *checkoutSystem }

func (p *auditPhase) ID() phase.ID            { return "audit" }
func (p *auditPhase) DependsOn() []phase.ID   { return []phase.ID{"ledger"} }
func (p *auditPhase) Produces() []phase.KeyID { return nil }
func (p *auditPhase) Requires() []phase.KeyID { return phase.Keys(OrderID) }
func (p *auditPhase) AppliesTo(c phase.Case, _ phase.Config) phase.Applicability {
	return phase.Applies()
}

func (p *auditPhase) Run(_ context.Context, r *phase.Run) error {
	orderID, err := phase.Get(r, OrderID)
	if err != nil {
		return err
	}
	before := p.sys.LedgerRows(orderID)
	r.Record(result.Compared("audit trail complete", []bool{orderID != ""}))
	// Unchanged: the audit pass must be read-only — the ledger it walked is
	// byte-identical after.
	r.Record(cmp.Unchanged("ledger stable during audit", before, p.sys.LedgerRows(orderID)))
	return nil
}

// --- the settlement group lifecycle --------------------------------------

// processorStream is the settlement group's Lifecycle: subscribe before
// any member runs, unsubscribe once every member has landed — always, even
// on failure or cancellation. Setup Puts StreamCursor (Group.Produces).
type processorStream struct{ sys *checkoutSystem }

func (f *processorStream) Setup(_ context.Context, r *phase.Run) error {
	cursor := f.sys.Subscribe()
	phase.Put(r, StreamCursor, cursor)
	r.Observe("stream subscribed", cursor)
	return nil
}

func (f *processorStream) Teardown(_ context.Context, r *phase.Run) error {
	f.sys.Unsubscribe()
	r.Observe("stream unsubscribed", true)
	return nil
}

// --- the catalog fixture (case-scoped) -----------------------------------

type catalogFixture struct{ sys *checkoutSystem }

func (f *catalogFixture) Setup(context.Context, *phase.Run) error {
	f.sys.SeedCatalog()
	return nil
}

func (f *catalogFixture) Teardown(context.Context, *phase.Run) error {
	f.sys.ClearCatalog()
	return nil
}

// settlementGroup is the one group declaration, shared by buildPipeline and
// the tests that rebuild the pipeline around a mutated phase.
func settlementGroup(sys *checkoutSystem) phase.Group {
	return phase.Group{
		ID:        "settlement",
		Members:   []phase.ID{"settle_wait", "settle_checks"},
		Produces:  phase.Keys(StreamCursor),
		Lifecycle: &processorStream{sys},
	}
}

// buildPipeline assembles the whole flow, settlement group included.
func buildPipeline(sys *checkoutSystem) *phase.Pipeline {
	return phase.NewPipeline(
		&submitPhase{sys},
		&authorizePhase{sys},
		&settleWaitPhase{sys},
		&settleChecksPhase{sys},
		&ledgerPhase{sys},
		&refundAuditPhase{sys},
		&auditPhase{sys},
	).Group(settlementGroup(sys))
}
