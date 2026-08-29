// Copyright 2026 The Phase Contributors
// SPDX-License-Identifier: MIT

// Package checkout is a consumer example: one order-checkout flow
// exercising every feature the library ships, deterministic and offline.
// It is compiled and tested in CI against every change, so a library
// change that breaks it is a breaking change; the feature map in
// README.md indexes where each capability is exercised.
package checkout

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

	// catalogSeeds is a lease count, not a boolean: under
	// MaxCaseConcurrency several cases hold the catalog at once, and a
	// boolean fixture flag is not safe under concurrent cases — one
	// case's teardown can clear a precondition a concurrent case still
	// needs. Each case's fixture takes and releases its own lease
	// instead.
	catalogSeeds int
	healthy      bool

	orders     map[string]*order
	nextOrder  int
	streamSubs int // active settlement-stream subscriptions

	// settlePollsNeeded: how many settlement polls before entities settle.
	settlePollsNeeded int
	// flapLedgerCount: when true, the first ledger-count read returns one
	// row short (the tolerated flake), correct afterwards.
	flapLedgerCount bool
	flapConsumed    bool
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
	if o.polls < s.settlePollsNeeded {
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
	return append([]string(nil), o.ledgerIDs...)
}

// Refunded reports whether a declined order was refunded (always false in
// this fake — the refund audit demonstrates a When-decline on green paths).
func (s *checkoutSystem) Refunded(orderID string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	o := s.orders[orderID]
	return o != nil && o.refunded
}
