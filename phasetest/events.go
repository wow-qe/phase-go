// Copyright 2026 The Phase Contributors
// SPDX-License-Identifier: MIT

package phasetest

import (
	"sync"

	phase "github.com/wow-qe/phase-go"
)

// EventRecorder is the in-memory sink for the unified event stream — the
// observer analogue of SpyRecorder.
type EventRecorder struct {
	mu     sync.Mutex
	events []phase.Event
}

// RecordEvents returns a recorder and the RunnerOption that feeds it.
//
//	rec, opt := phasetest.RecordEvents()
//	r, _ := phase.NewRunner(p, cfg)
//	opt(r) // or pass opt to NewRunner
func RecordEvents() (*EventRecorder, phase.RunnerOption) {
	rec := &EventRecorder{}
	return rec, phase.WithObserver(func(ev phase.Event) {
		rec.mu.Lock()
		defer rec.mu.Unlock()
		rec.events = append(rec.events, ev)
	})
}

// Events returns everything recorded, in delivery order.
func (r *EventRecorder) Events() []phase.Event {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]phase.Event(nil), r.events...)
}

// Kinds returns just the kind sequence — the shape trace assertions check.
func (r *EventRecorder) Kinds() []phase.EventKind {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]phase.EventKind, len(r.events))
	for i, ev := range r.events {
		out[i] = ev.Kind()
	}
	return out
}
