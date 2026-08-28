// Copyright 2026 The Phase Contributors
// SPDX-License-Identifier: MIT

package provisioning

import (
	"context"
	"fmt"

	phase "github.com/wow-qe/phase-go"
)

// ItemExpectation is one entity the request is declared to fan out into,
// and the terminal state it is declared to reach.
type ItemExpectation struct {
	EntityID string
	State    string // "succeeded" or "failed"
}

// idsOf is the entity IDs a set of expectations names, in order — what
// Case.Body encodes for the queue to fan out.
func idsOf(items []ItemExpectation) []string {
	ids := make([]string, len(items))
	for i, it := range items {
		ids[i] = it.EntityID
	}
	return ids
}

// Case is the consumer's declaration: a provisioning
// request, what each entity it fans out into is expected to become, which
// phases it opts out of and why, and — for cases that want the fake
// provider to reject a specific entity — which one.
type Case struct {
	Name          string
	Body          []byte
	ExpectedItems []ItemExpectation
	PhaseSkips    map[phase.ID]string
	State         string // parsed via phase.ParseStatus, see Status() below
	Fault         string // entity ID the provider fixture should reject; "" for none

	// admin is the provider control plane the Fault fixture injects into.
	// Unexported: this package's own tests set it directly (white-box), as
	// a real consumer would wire its own admin client at case-construction
	// time rather than exposing it on every case.
	admin *Provider
}

// NewCase builds a Case whose Body is derived from items, so a caller never
// has to keep Body and ExpectedItems in sync by hand.
func NewCase(name string, items []ItemExpectation, state string, skips map[phase.ID]string, fault string, admin *Provider) *Case {
	return &Case{
		Name:          name,
		Body:          EncodeItems(idsOf(items)),
		ExpectedItems: items,
		PhaseSkips:    skips,
		State:         state,
		Fault:         fault,
		admin:         admin,
	}
}

func (c *Case) ID() string { return c.Name }

// Status parses c.State on every call rather than caching it at
// construction. phase.ParseStatus(string) returns (CaseStatus, error), so
// an unparseable status is never silently defaulted. An unparseable State
// here is a load-time bug in this example's own case data, not a runtime
// condition to recover from, so it panics — the same posture phase.Declare
// takes for a duplicate key.
func (c *Case) Status() phase.CaseStatus {
	st, err := phase.ParseStatus(c.State)
	if err != nil {
		panic(fmt.Sprintf("provisioning: case %q: %v", c.Name, err))
	}
	return st
}

func (c *Case) Exclusive() (bool, string) { return false, "" }

// Selects declines a phase only when PhaseSkips names it, and always with
// the recorded reason — an unexplained skip is a LoadError at preflight
// (case.go's own doc comment on the Case interface).
func (c *Case) Selects(id phase.ID) (bool, string) {
	if reason, skipped := c.PhaseSkips[id]; skipped {
		return false, reason
	}
	return true, ""
}

func (c *Case) Timing(phase.ID) (phase.Timing, bool) { return phase.Timing{}, false }

// Fixtures seeds a provider fault when the case declares one:
// Setup injects it, scoped to this run; Teardown clears it, so a
// case's injected failure never outlives the run that asked for it.
func (c *Case) Fixtures() []phase.Fixture {
	if c.Fault == "" {
		return nil
	}
	return []phase.Fixture{&seedFault{admin: c.admin, entityID: c.Fault}}
}

// seedFault is the injection fixture: Setup injects, Teardown
// clears, both scoped to the run's allocated Scope rather than to the case
// itself, because the scope — not the case — is what the fake provider (and
// a real one) can actually key state on.
type seedFault struct {
	admin    *Provider
	entityID string
}

func (f *seedFault) Setup(ctx context.Context, r *phase.Run) error {
	f.admin.FailEntity(r.Scope().Correlation, f.entityID)
	return nil
}

func (f *seedFault) Teardown(ctx context.Context, r *phase.Run) error {
	f.admin.ClearFaults(r.Scope().Correlation)
	return nil
}
