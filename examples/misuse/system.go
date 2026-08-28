// Copyright 2026 The Phase Contributors
// SPDX-License-Identifier: MIT

// Package misuse is the SABOTAGE TWIN of examples/checkout: the same
// checkout flow, deliberately fed wrong data and wrong declarations, one
// defect at a time — and every defect must be answered by the framework's
// documented response (a LoadError refusal, a loud failing result, an
// Errored-never-Failed, a contained panic, a Verify catch, a named budget
// error). Its README indexes each seeded defect and the response the
// framework owes it. This package is the misuse canary: if a defect here
// stops being answered loudly, a silent-failure bug has entered the
// framework.
package misuse

import (
	"fmt"
	"sync"
)

// checkoutSystem is the fake system under test: an order store, a payment
// processor, a settlement engine that settles entities only after a few
// polls (eventually consistent), a ledger, and a processor event stream
// that must be subscribed/unsubscribed around settlement work.
//
// Deterministic by construction: behavior is keyed off scenario names,
// never randomness or wall clocks.
type checkoutSystem struct {
	mu sync.Mutex

	// catalogSeeds is a LEASE COUNT, not a boolean: under MaxCaseConcurrency
	// several cases hold the catalog at once, and a boolean fixture is the
	// classic non-scope-partitioned trap — one case's teardown clobbers a
	// concurrent case's precondition (a ~2% race caught in review:
	// billing-report failing submit's Before under concurrency). Each
	// case's fixture takes and releases its own lease instead.
	catalogSeeds int
	healthy      bool

	orders     map[string]*order
	nextOrder  int
	streamSubs int // active settlement-stream subscriptions

	// settlePollsNeeded: how many settlement polls before entities settle.
	settlePollsNeeded int
	// flapLedgerCount: when true, the FIRST ledger-count read returns one
	// row short (the tolerated flake), correct afterwards.
	flapLedgerCount bool
	flapConsumed    bool

	// --- sabotage knobs: each seeds ONE kind of wrong data ---------------

	// corruptAuthCode: Authorize returns a malformed code (ValueMatch must
	// fail with a diff naming both values).
	corruptAuthCode bool
	// neverSettle: PollSettlement never completes (WaitUntil must exhaust
	// its budget with the budget NAMED, never "nothing found").
	neverSettle bool
	// dropLedgerRow: LedgerRows omits the last row (ContainsAll must fail
	// naming the missing entity).
	dropLedgerRow bool
	// wrongEntityState: EntityState reports "open" (EachEntity must fail
	// the per-entity row, attributed to that entity).
	wrongEntityState bool
	// permanentLedgerFlap: LedgerCount stays one short forever (Tolerate
	// must EXHAUST, recording the trail and naming the tolerance — a flake
	// that never heals is a failure, not a flake).
	permanentLedgerFlap bool
	// panicInSubmit: SubmitOrder panics (consumer-code panic must be
	// contained as that case's evidence, never the batch's crash).
	panicInSubmit bool
	// mutateDuringAudit: LedgerRows grows a row between audit's two reads
	// (Unchanged must fail with the before/after diff).
	mutateDuringAudit bool
	auditReads        int
}

type order struct {
	id        string
	scenario  string // "happy" | "declined"
	entities  int
	authCode  string
	polls     int
	settled   bool
	refunded  bool
	ledgerIDs []string
}

func newCheckoutSystem() *checkoutSystem {
	return &checkoutSystem{
		orders:            map[string]*order{},
		healthy:           true,
		settlePollsNeeded: 3,
	}
}

func (s *checkoutSystem) SeedCatalog() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.catalogSeeds++
}

func (s *checkoutSystem) ClearCatalog() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.catalogSeeds > 0 {
		s.catalogSeeds--
	}
}

func (s *checkoutSystem) CatalogSeeded() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.catalogSeeds > 0
}

func (s *checkoutSystem) Healthy() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.healthy
}

// Subscribe opens a settlement-stream subscription (the group lifecycle's
// resource). Returns a cursor name the members read from.
func (s *checkoutSystem) Subscribe() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.streamSubs++
	return fmt.Sprintf("cursor-%d", s.streamSubs)
}

func (s *checkoutSystem) Unsubscribe() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.streamSubs--
}

// ActiveSubscriptions is the leak detector the reconcile case asserts on.
func (s *checkoutSystem) ActiveSubscriptions() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.streamSubs
}

// SubmitOrder registers an order; declined scenarios never reach payment.
func (s *checkoutSystem) SubmitOrder(scenario string, entities int) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.panicInSubmit {
		panic("order store index corrupted") // the consumer-code bug the runner must contain
	}
	if s.catalogSeeds == 0 {
		return "", fmt.Errorf("catalog not seeded")
	}
	s.nextOrder++
	id := fmt.Sprintf("ord-%03d", s.nextOrder)
	s.orders[id] = &order{id: id, scenario: scenario, entities: entities}
	return id, nil
}

// Authorize returns an auth code, or a decline for declined scenarios.
func (s *checkoutSystem) Authorize(orderID string) (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	o := s.orders[orderID]
	if o == nil || o.scenario == "declined" {
		return "", false
	}
	o.authCode = "auth-" + o.id
	if s.corruptAuthCode {
		return "bogus-" + o.id, true // wrong shape: ValueMatch must say so
	}
	return o.authCode, true
}

// PollSettlement advances the eventually-consistent settlement: entities
// settle only after settlePollsNeeded polls, and every settled entity is
// ledgered (with the deliberate one-read flap when configured).
func (s *checkoutSystem) PollSettlement(orderID string) ([]string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	o := s.orders[orderID]
	if o == nil || o.authCode == "" {
		return nil, false
	}
	o.polls++
	if s.neverSettle || o.polls < s.settlePollsNeeded {
		return nil, false
	}
	o.settled = true
	if o.ledgerIDs == nil {
		for i := 1; i <= o.entities; i++ {
			o.ledgerIDs = append(o.ledgerIDs, fmt.Sprintf("%s/e%d", o.id, i))
		}
	}
	return append([]string(nil), o.ledgerIDs...), true
}

// EntityState reports one settled entity's terminal state.
func (s *checkoutSystem) EntityState(entityID string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, o := range s.orders {
		for _, e := range o.ledgerIDs {
			if e == entityID {
				if s.wrongEntityState {
					return "open" // the terminal state that never arrived
				}
				if o.settled {
					return "settled"
				}
				return "open"
			}
		}
	}
	return "unknown"
}

// LedgerCount is the tolerated-flake read: when flapping is configured the
// first read comes back one short, then heals — the shape Tolerate exists
// for.
func (s *checkoutSystem) LedgerCount(orderID string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	o := s.orders[orderID]
	if o == nil {
		return 0
	}
	n := len(o.ledgerIDs)
	if s.permanentLedgerFlap {
		return n - 1 // never heals: the "declared flaky" claim was wrong
	}
	if s.flapLedgerCount && !s.flapConsumed {
		s.flapConsumed = true
		return n - 1
	}
	return n
}

// LedgerRows lists the ledger entries for an order.
func (s *checkoutSystem) LedgerRows(orderID string) []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	o := s.orders[orderID]
	if o == nil {
		return nil
	}
	rows := append([]string(nil), o.ledgerIDs...)
	if s.dropLedgerRow && len(rows) > 0 {
		rows = rows[:len(rows)-1] // the indexer lost a row
	}
	if s.mutateDuringAudit {
		s.auditReads++
		// Reads land as: ledger's ContainsAll (1), audit's before (2),
		// audit's after (3) — the ghost appears between audit's two reads.
		if s.auditReads >= 3 {
			rows = append(rows, o.id+"/ghost")
		}
	}
	return rows
}

// Refunded reports whether a declined order was refunded (always false in
// this fake — the refund audit demonstrates a When-decline on green paths).
func (s *checkoutSystem) Refunded(orderID string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	o := s.orders[orderID]
	return o != nil && o.refunded
}
