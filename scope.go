// Copyright 2026 The Phase Contributors
// SPDX-License-Identifier: MIT

package phase

import (
	"crypto/rand"
	"encoding/hex"
)

// Scope makes one case's traffic distinguishable in every shared system it
// touches. The framework allocates it — cases do not hand-pick identifiers,
// because hand-picked identifiers collide, and a collision produces a false
// report of a product defect: the most expensive failure there is, because
// someone investigates the product.
//
// Correlation is deliberately separate from Keys. Scope keys make traffic
// distinguishable in shared state (counters, resets, searches). The
// correlation value threads one submission through systems that never share
// a scope — it is how a queued message is later found in a database row and
// a ledger entry.
type Scope struct {
	RunID       string
	CaseID      string
	Correlation string
	Keys        map[string]string
}

// ScopeAllocator lets a consumer whose domain constrains identifiers supply
// its own allocation. Optional: the default allocator generates collision-free
// values from crypto/rand. Whatever allocates, preflight still checks the
// resulting scopes for collisions across the session — framework-allocated
// fields are the only ones the check can fully promise; for
// consumer-set keys it detects duplicates at load, which is the whole of what
// it can do.
type ScopeAllocator interface {
	Allocate(c Case) (Scope, error)
}

// defaultAllocate generates collision-free scope values from crypto/rand.
// Consumers whose domains constrain identifiers replace it via ScopeAllocator.
func defaultAllocate(c Case) (Scope, error) {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return Scope{}, err
	}
	return Scope{
		CaseID:      c.ID(),
		Correlation: hex.EncodeToString(buf),
		Keys:        map[string]string{},
	}, nil
}
