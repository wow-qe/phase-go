// Copyright 2026 The Phase Contributors
// SPDX-License-Identifier: MIT

package provisioning

import (
	"context"
	"fmt"

	phase "github.com/wow-qe/phase-go"
	"github.com/wow-qe/phase-go/result"
)

// The pipeline's shape: submit -> discover -> settle_wait -> settle_checks
// -> {provider_side, ledger}. Settle could be one phase;
// phasetest.Gutted refuses to gut a producer (it would starve dependents
// instead of measuring assertion coverage), so a phase whose only job is
// asserting has to be separate from the one that waits and produces the
// handoff value. Hence settle_wait (produces SettledRows) and
// settle_checks (produces nothing, asserts everything) — see
// suite_test.go's TestMutationGateGoesRed.
//
// provider_side and ledger both depend on settle_checks rather than
// settle_wait, purely for a deterministic FailedIn in the reports below:
// phase order is otherwise a lexical tie-break (internal/dag.Sort) among
// phases whose dependencies are equally satisfied, and this example wants
// "the assertion phase failed" to always name settle_checks first.

// --- submit ------------------------------------------------------------

// queue is the adapter submit needs. Declared here, at the point of use,
// not in the framework, and not in fakes.go either: the
// phase owns the shape of the dependency it requires, the fake merely
// happens to satisfy it.
type queue interface {
	Publish(ctx context.Context, topic, key string, body []byte) error
}

// submit publishes the request and hands its correlation id forward as
// RequestID. It never asserts anything: a phase that only sets up state
// records no results, and that is fine — the founding rule is about
// cases, not phases.
type submit struct{ q queue }

func (p *submit) ID() phase.ID            { return "submit" }
func (p *submit) DependsOn() []phase.ID   { return nil }
func (p *submit) Produces() []phase.KeyID { return phase.Keys(RequestID) }
func (p *submit) Requires() []phase.KeyID { return nil }

func (p *submit) AppliesTo(phase.Case, phase.Config) phase.Applicability { return phase.Applies() }

func (p *submit) Run(ctx context.Context, r *phase.Run) error {
	c := r.Case().(*Case)
	key := r.Scope().Correlation
	if err := p.q.Publish(ctx, "requests", key, c.Body); err != nil {
		return err // an error is not a failed result
	}
	phase.Put(r, RequestID, key)
	return nil
}

// --- discover ------------------------------------------------------------

// store is the adapter discover and settle_wait need.
type store interface {
	Rows(ctx context.Context, requestID string) ([]Row, error)
}

// discover waits for the request's rows to appear, then checks that every
// entity the case declared was in fact discovered. It does not judge
// entities' state — that is settle_checks's job, once the state is final.
type discover struct{ db store }

func (p *discover) ID() phase.ID            { return "discover" }
func (p *discover) DependsOn() []phase.ID   { return []phase.ID{"submit"} }
func (p *discover) Produces() []phase.KeyID { return phase.Keys(Items) }
func (p *discover) Requires() []phase.KeyID { return phase.Keys(RequestID) }

func (p *discover) AppliesTo(phase.Case, phase.Config) phase.Applicability { return phase.Applies() }

func (p *discover) Run(ctx context.Context, r *phase.Run) error {
	id, err := phase.Get(r, RequestID)
	if err != nil {
		return err // fails loudly if submit never produced it
	}

	// Wait for a condition, never a duration.
	rows, err := phase.WaitUntil(ctx, r, func(ctx context.Context) ([]Row, bool, error) {
		rows, err := p.db.Rows(ctx, id)
		return rows, len(rows) > 0, err
	})
	if err != nil {
		return err
	}

	r.Observe("rows", rows) // raw evidence, kept alongside the judgement below

	byID := make(map[string]Row, len(rows))
	for _, row := range rows {
		byID[row.EntityID] = row
	}

	c := r.Case().(*Case)
	for _, want := range c.ExpectedItems {
		actual := "missing"
		if _, found := byID[want.EntityID]; found {
			actual = want.EntityID
		}
		r.Record(result.Compared("entity discovered", []bool{actual == want.EntityID}).
			WithExpected(want.EntityID).WithActual(actual).
			ForEntity(phase.Ref(want.EntityID)))
	}

	refs := make([]phase.EntityRef, len(rows))
	for i, row := range rows {
		refs[i] = phase.Ref(row.EntityID)
	}
	phase.Put(r, Items, refs)
	return nil
}

// --- settle_wait / settle_checks -----------------------------------------

// settleWait waits for every discovered entity to reach a terminal state
// and hands the settled rows forward. It asserts nothing — that split is
// what makes settle_checks safely guttable (see the file comment above).
type settleWait struct{ db store }

func (p *settleWait) ID() phase.ID            { return "settle_wait" }
func (p *settleWait) DependsOn() []phase.ID   { return []phase.ID{"discover"} }
func (p *settleWait) Produces() []phase.KeyID { return phase.Keys(SettledRows) }
func (p *settleWait) Requires() []phase.KeyID { return phase.Keys(RequestID) }

func (p *settleWait) AppliesTo(phase.Case, phase.Config) phase.Applicability { return phase.Applies() }

func (p *settleWait) Run(ctx context.Context, r *phase.Run) error {
	id, err := phase.Get(r, RequestID)
	if err != nil {
		return err
	}

	rows, err := phase.WaitUntil(ctx, r, func(ctx context.Context) ([]Row, bool, error) {
		rows, err := p.db.Rows(ctx, id)
		if err != nil {
			return nil, false, err
		}
		return rows, allTerminal(rows), nil
	})
	if err != nil {
		return err
	}

	phase.Put(r, SettledRows, rows)
	return nil
}

func allTerminal(rows []Row) bool {
	if len(rows) == 0 {
		return false
	}
	for _, row := range rows {
		if row.State != "succeeded" && row.State != "failed" {
			return false
		}
	}
	return true
}

// settleChecks compares every entity's settled state against the case's
// declared expectation. It produces nothing — Produces() is empty on
// purpose, which is what lets phasetest.Gutted wrap it (Gutted refuses any
// phase that produces a handoff key).
type settleChecks struct{}

func (p *settleChecks) ID() phase.ID            { return "settle_checks" }
func (p *settleChecks) DependsOn() []phase.ID   { return []phase.ID{"settle_wait"} }
func (p *settleChecks) Produces() []phase.KeyID { return nil }
func (p *settleChecks) Requires() []phase.KeyID { return phase.Keys(SettledRows) }

func (p *settleChecks) AppliesTo(phase.Case, phase.Config) phase.Applicability {
	return phase.Applies()
}

func (p *settleChecks) Run(ctx context.Context, r *phase.Run) error {
	rows, err := phase.Get(r, SettledRows)
	if err != nil {
		return err
	}
	byID := make(map[string]Row, len(rows))
	for _, row := range rows {
		byID[row.EntityID] = row
	}

	c := r.Case().(*Case)
	for _, want := range c.ExpectedItems {
		row, found := byID[want.EntityID]
		actual := "missing"
		if found {
			actual = row.State
		}
		r.Record(result.Compared("entity settled state", []bool{found && row.State == want.State}).
			WithExpected(want.State).WithActual(actual).
			ForEntity(phase.Ref(want.EntityID))) // expected and actual always
	}
	return nil
}

// --- provider_side ---------------------------------------------------------

// providerAdmin is the control-plane adapter provider_side needs.
type providerAdmin interface {
	Submissions(scope string) []Submission
	CallCount(scope string) int
}

// providerSide asserts the provider actually received every entity the
// request fanned out into, scoped to this case's correlation value, and
// that it received exactly one call per entity — not zero, not a retry
// storm.
type providerSide struct{ admin providerAdmin }

func (p *providerSide) ID() phase.ID            { return "provider_side" }
func (p *providerSide) DependsOn() []phase.ID   { return []phase.ID{"settle_checks"} }
func (p *providerSide) Produces() []phase.KeyID { return nil }
func (p *providerSide) Requires() []phase.KeyID { return phase.Keys(RequestID, Items) }

func (p *providerSide) AppliesTo(phase.Case, phase.Config) phase.Applicability {
	return phase.Applies()
}

func (p *providerSide) Run(ctx context.Context, r *phase.Run) error {
	id, err := phase.Get(r, RequestID)
	if err != nil {
		return err
	}
	items, err := phase.Get(r, Items)
	if err != nil {
		return err
	}

	subs := p.admin.Submissions(id) // scoped to this case's correlation value

	// Evidence is entity-keyed, not scope-keyed: the correlation value
	// (Submission.Scope) is internal routing, already implied by which
	// case this evidence is attributed to, and carrying it into the
	// report would make two otherwise-identical runs diff on nothing but
	// a random allocated id (see suite_test.go's TestDeterministicReports).
	seen := make(map[string]bool, len(subs))
	receivedIDs := make([]string, 0, len(subs))
	for _, s := range subs {
		seen[s.EntityID] = true
		receivedIDs = append(receivedIDs, s.EntityID)
	}
	r.Observe("provider_received_entity_ids", receivedIDs)

	got := make([]bool, 0, len(items))
	for _, item := range items {
		got = append(got, seen[item.ID])
	}
	r.Record(result.Compared("provider received every entity", got).
		WithExpected(items).WithActual(receivedIDs))

	count := p.admin.CallCount(id)
	r.Record(result.Compared("provider call count matches entity count", []bool{count == len(items)}).
		WithExpected(len(items)).WithActual(count))
	return nil
}

// --- ledger ------------------------------------------------------------

// ledgerReader is the adapter the ledger phase needs.
type ledgerReader interface {
	Rows(ctx context.Context, requestID string) ([]LedgerRow, error)
}

// newLedgerPhase builds the ledger phase via phase.Func rather than a named
// type — the compact style, for the phase with the least
// ceremony to spare. It asserts an invariant of the system, not of the
// case's declaration: every entity that actually settled succeeded has a
// ledger row, and every one that actually failed does not. That is
// deliberately independent of what the case predicted (settle_checks
// already checks the prediction) — see suite_test.go's
// TestMutationGateGoesRed for why that independence matters.
func newLedgerPhase(sheet ledgerReader) phase.Interface {
	return phase.Func{
		PhaseID: "ledger",
		Deps:    []phase.ID{"settle_checks"},
		Gets:    phase.Keys(RequestID, SettledRows),
		Do: func(ctx context.Context, r *phase.Run) error {
			id, err := phase.Get(r, RequestID)
			if err != nil {
				return err
			}
			settled, err := phase.Get(r, SettledRows)
			if err != nil {
				return err
			}

			rows, err := sheet.Rows(ctx, id)
			if err != nil {
				return err
			}
			// Entity-keyed evidence, not scope-keyed: LedgerRow.RequestID is
			// the same internal correlation value discussed in provider_side
			// above, and is dropped from the observation for the same reason.
			ledgered := make(map[string]bool, len(rows))
			ledgeredIDs := make([]string, 0, len(rows))
			for _, row := range rows {
				ledgered[row.EntityID] = true
				ledgeredIDs = append(ledgeredIDs, row.EntityID)
			}
			r.Observe("ledger_entity_ids", ledgeredIDs)

			for _, row := range settled {
				wantLedgered := row.State == "succeeded"
				r.Record(result.Compared("ledger row presence matches settlement outcome",
					[]bool{ledgered[row.EntityID] == wantLedgered}).
					WithExpected(fmt.Sprintf("ledgered=%v", wantLedgered)).
					WithActual(fmt.Sprintf("ledgered=%v", ledgered[row.EntityID])).
					ForEntity(phase.Ref(row.EntityID)))
			}
			return nil
		},
	}
}

// --- assembly ------------------------------------------------------------

// Pipeline assembles the example's phases against sys, wired exactly as a
// real provisioning consumer would.
func Pipeline(sys *System) *phase.Pipeline {
	return PipelineWithSettleChecks(sys, &settleChecks{})
}

// PipelineWithSettleChecks is Pipeline with the settle_checks phase
// injectable, so a caller can substitute phasetest.Gutted(&settleChecks{})
// without hand-rebuilding the other five phases. See
// suite_test.go's TestMutationGateGoesRed.
func PipelineWithSettleChecks(sys *System, settleChecksPhase phase.Interface) *phase.Pipeline {
	return phase.NewPipeline(
		&submit{q: sys.Queue},
		&discover{db: sys.Store},
		&settleWait{db: sys.Store},
		settleChecksPhase,
		&providerSide{admin: sys.Provider},
		newLedgerPhase(sys.Ledger),
	)
}
