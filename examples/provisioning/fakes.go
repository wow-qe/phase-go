// Copyright 2026 The Phase Contributors
// SPDX-License-Identifier: MIT

package provisioning

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
)

// This file is the fake provisioning stack the example's phases exercise: a
// queue, a database-backed store, an external provider's control plane, and
// a settlement ledger. Each is deliberately simple — the example
// demonstrates the framework's contract, not a realistic async system, so
// every fake resolves synchronously. A production adapter would poll, retry and
// eventually become consistent; phase.WaitUntil is written to tolerate that,
// and these fakes just don't need it to.

// Row is one entity's state as the store would report it.
type Row struct {
	EntityID string
	State    string // "succeeded" or "failed" — always terminal in this fake
}

// QueueMessage is what Publish hands to the queue's processor.
type QueueMessage struct {
	Topic string
	Key   string
	Body  []byte
}

// Queue is an in-memory fake of a publish/subscribe queue.
type Queue struct {
	mu        sync.Mutex
	published []QueueMessage
	process   func(QueueMessage)
}

// NewQueue returns an empty Queue with no processor installed.
func NewQueue() *Queue { return &Queue{} }

// OnPublish installs the callback invoked synchronously after every publish
// — the fake's stand-in for the queue's own background delivery.
func (q *Queue) OnPublish(fn func(QueueMessage)) { q.process = fn }

// Publish records the message and, if a processor is installed, hands it
// off before returning — simulating an async fan-out without any actual
// concurrency (synchronously is fine here).
func (q *Queue) Publish(ctx context.Context, topic, key string, body []byte) error {
	q.mu.Lock()
	msg := QueueMessage{Topic: topic, Key: key, Body: body}
	q.published = append(q.published, msg)
	proc := q.process
	q.mu.Unlock()
	if proc != nil {
		proc(msg)
	}
	return nil
}

// Published returns every message handed to Publish, in publish order.
func (q *Queue) Published() []QueueMessage {
	q.mu.Lock()
	defer q.mu.Unlock()
	out := make([]QueueMessage, len(q.published))
	copy(out, q.published)
	return out
}

// Store is an in-memory fake of the provisioning database: rows keyed by
// the request (correlation) id that produced them.
type Store struct {
	mu   sync.Mutex
	rows map[string][]Row
}

// NewStore returns an empty Store.
func NewStore() *Store { return &Store{rows: map[string][]Row{}} }

// Seed replaces the rows for a request id — the queue's processor calls
// this once it has decided every entity's terminal state.
func (s *Store) Seed(requestID string, rows []Row) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.rows[requestID] = append([]Row(nil), rows...)
}

// Rows returns the current rows for a request id, or none if discovery
// hasn't happened yet.
func (s *Store) Rows(ctx context.Context, requestID string) ([]Row, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Row(nil), s.rows[requestID]...), nil
}

// Submission is one call the fake provider control plane received.
type Submission struct {
	Scope    string
	EntityID string
}

// Provider is an in-memory fake of the external provider's control plane:
// it records every submission per scope, counts calls per scope, and can be
// told to fail a specific entity via an injected fault.
type Provider struct {
	mu      sync.Mutex
	calls   map[string]int
	submits map[string][]Submission
	fault   map[string]bool // "scope|entityID" -> fail
}

// NewProvider returns a Provider with no faults injected.
func NewProvider() *Provider {
	return &Provider{
		calls:   map[string]int{},
		submits: map[string][]Submission{},
		fault:   map[string]bool{},
	}
}

// FailEntity injects a fault: the next Submit for this scope and entity
// fails. Called from the example's fixture (case.go), never from a phase —
// a phase only ever observes the provider, it never drives it.
func (p *Provider) FailEntity(scope, entityID string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.fault[scope+"|"+entityID] = true
}

// ClearFaults removes every fault injected for a scope. Called from a
// fixture's Teardown, so one case's injected failure never leaks into the
// next case sharing this Provider.
func (p *Provider) ClearFaults(scope string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	prefix := scope + "|"
	for k := range p.fault {
		if strings.HasPrefix(k, prefix) {
			delete(p.fault, k)
		}
	}
}

// Submit records the call and fails it if a fault was injected for this
// scope and entity.
func (p *Provider) Submit(scope, entityID string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.calls[scope]++
	p.submits[scope] = append(p.submits[scope], Submission{Scope: scope, EntityID: entityID})
	if p.fault[scope+"|"+entityID] {
		return fmt.Errorf("provider: entity %q rejected for scope %q (injected fault)", entityID, scope)
	}
	return nil
}

// Submissions returns every call received for a scope, in call order.
func (p *Provider) Submissions(scope string) []Submission {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]Submission, len(p.submits[scope]))
	copy(out, p.submits[scope])
	return out
}

// CallCount returns how many times Submit was called for a scope.
func (p *Provider) CallCount(scope string) int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.calls[scope]
}

// LedgerRow is one settled entity's entry in the ledger.
type LedgerRow struct {
	RequestID string
	EntityID  string
}

// Ledger is an in-memory fake of the settlement ledger: one row per entity
// the provider accepted.
type Ledger struct {
	mu   sync.Mutex
	rows map[string][]LedgerRow
}

// NewLedger returns an empty Ledger.
func NewLedger() *Ledger { return &Ledger{rows: map[string][]LedgerRow{}} }

// Record adds a ledger row for an accepted entity.
func (l *Ledger) Record(requestID, entityID string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.rows[requestID] = append(l.rows[requestID], LedgerRow{RequestID: requestID, EntityID: entityID})
}

// Rows returns the ledger rows recorded for a request id.
func (l *Ledger) Rows(ctx context.Context, requestID string) ([]LedgerRow, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := make([]LedgerRow, len(l.rows[requestID]))
	copy(out, l.rows[requestID])
	return out, nil
}

// System wires the four fakes together exactly as the phases in phases.go
// expect: a published request becomes rows in the store, one Submit call
// per entity to the provider, and a ledger row for every entity the
// provider accepted. It is the example's stand-in for "a provisioning
// service" — the async product the pipeline in phases.go tests.
type System struct {
	Queue    *Queue
	Store    *Store
	Provider *Provider
	Ledger   *Ledger
}

// NewSystem returns a System with the queue's processor already installed.
func NewSystem() *System {
	sys := &System{
		Queue:    NewQueue(),
		Store:    NewStore(),
		Provider: NewProvider(),
		Ledger:   NewLedger(),
	}
	sys.Queue.OnPublish(sys.process)
	return sys
}

// process is the queue's simulated delivery: decode the entity IDs the
// request fans out into, submit each to the provider, ledger the ones the
// provider accepts, and seed the store with every entity's terminal state —
// all synchronously, in one pass.
func (s *System) process(msg QueueMessage) {
	ids := decodeItemIDs(msg.Body)
	requestID := msg.Key

	rows := make([]Row, len(ids))
	for i, id := range ids {
		if err := s.Provider.Submit(requestID, id); err != nil {
			rows[i] = Row{EntityID: id, State: "failed"}
			continue
		}
		rows[i] = Row{EntityID: id, State: "succeeded"}
		s.Ledger.Record(requestID, id)
	}
	s.Store.Seed(requestID, rows)
}

// EncodeItems is the wire format submit puts on the queue: the entity IDs a
// request fans out into, as a JSON array.
func EncodeItems(entityIDs []string) []byte {
	b, err := json.Marshal(entityIDs)
	if err != nil {
		panic(fmt.Sprintf("provisioning: EncodeItems: %v", err)) // entityIDs is always []string; unreachable
	}
	return b
}

// decodeItemIDs is EncodeItems's inverse, used by the queue's processor.
func decodeItemIDs(body []byte) []string {
	var ids []string
	_ = json.Unmarshal(body, &ids) // malformed body decodes to no entities, never a panic mid-fan-out
	return ids
}
