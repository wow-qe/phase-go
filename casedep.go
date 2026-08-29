// Copyright 2026 The Phase Contributors
// SPDX-License-Identifier: MIT

package phase

import (
	"errors"
	"fmt"
	"strings"

	"github.com/wow-qe/phase-go/internal/dag"
)

func errorsPkgAs(err error, target any) bool { return errors.As(err, target) }

// CaseDependency is an optional interface, so every existing Case
// implementation compiles unchanged. Execution follows the case DAG — a
// dependency on a later-declared case is normal, decoupling execution order
// from declaration order exactly as phase dependencies do — while the
// report keeps declaration order: that is Session.Cases()'s contract,
// preserved by indexed writes.
//
// Case-level data handoff is deliberately absent and stays absent: sharing
// mutable state across isolation boundaries reintroduces the hazard typed
// keys exist to prevent. The sanctioned path already exists at the
// consumer layer — read run A's Report(), build run B's cases from it,
// call Start again.
type CaseDependency interface {
	DependsOnCases() []CaseRequirement
}

// CaseRequirement names a case that must complete first, and which of its
// verdicts satisfy this dependent. Acceptable is an explicit set — Status
// has no ordering, so there is no "at least Passed"; "Passed or Flaked" is
// spelled out (a prerequisite that passed on a tolerated retry is a common,
// legitimate acceptance). An empty Acceptable means ordering only: any
// outcome satisfies.
type CaseRequirement struct {
	CaseID     string
	Acceptable []Status
}

// DependencyFailure is the structural cause on a Skipped-by-dependency case
// (the prose Reason is for humans; aggregation must not regex it). Actual
// carries whatever the dependency reached — including Skipped itself, so
// cascades read generically.
type DependencyFailure struct {
	CaseID     string   `json:"case_id"`
	Acceptable []Status `json:"acceptable,omitempty"`
	Actual     Status   `json:"actual"`
}

// caseDeps extracts the declared requirements, nil for plain cases.
func caseDeps(c Case) []CaseRequirement {
	if cd, ok := c.(CaseDependency); ok {
		return cd.DependsOnCases()
	}
	return nil
}

// validateCaseDeps refuses unknown targets and cycles at Preflight — cases
// exist only there (NewRunner validates pipeline structure; Preflight
// validates cases). A dependency whose target is not in the given set is
// refused with a typed error even though it may exist elsewhere (a tag selector
// filtered it out): a structural dependency crossing a selection boundary
// is a suite-authoring bug, and soft-skipping it would make the selected
// suite report fine while structurally broken.
func validateCaseDeps(cases []Case) error {
	present := map[string]bool{}
	for _, c := range cases {
		present[c.ID()] = true
	}
	nodes := make([]dag.Node, 0, len(cases))
	for _, c := range cases {
		reqs := caseDeps(c)
		n := dag.Node{ID: c.ID()}
		for _, req := range reqs {
			if !present[req.CaseID] {
				return &LoadError{Code: CaseDependencyUnknown, Subject: c.ID(),
					Detail: fmt.Sprintf("depends on case %q, which is not in this run — if a suite selector filtered it out, the dependency crosses the selection boundary", req.CaseID)}
			}
			n.DependsOn = append(n.DependsOn, req.CaseID)
		}
		nodes = append(nodes, n)
	}
	if _, err := dag.Sort(nodes); err != nil {
		var cyc *dag.CycleError
		if ok := errorsPkgAs(err, &cyc); ok {
			return &LoadError{Code: CaseDependencyCycle,
				Subject: strings.Join(cyc.Cycle, " -> "),
				Detail:  "cases depend on each other in a loop; nothing can run first"}
		}
		return &FrameworkError{Invariant: "case dag input", Detail: err.Error()}
	}
	return nil
}

// caseOrder returns execution order: declaration order when no case
// declares dependencies, case-DAG topological order otherwise.
// Deterministic either way.
func caseOrder(cases []Case) []int {
	hasDeps := false
	byID := map[string]int{}
	for i, c := range cases {
		byID[c.ID()] = i
		if len(caseDeps(c)) > 0 {
			hasDeps = true
		}
	}
	order := make([]int, 0, len(cases))
	if !hasDeps {
		for i := range cases {
			order = append(order, i)
		}
		return order
	}
	nodes := make([]dag.Node, 0, len(cases))
	for _, c := range cases {
		n := dag.Node{ID: c.ID()}
		for _, req := range caseDeps(c) {
			n.DependsOn = append(n.DependsOn, req.CaseID)
		}
		nodes = append(nodes, n)
	}
	sorted, err := dag.Sort(nodes) // validated at Preflight; cannot fail here
	if err != nil {
		for i := range cases {
			order = append(order, i)
		}
		return order
	}
	for _, id := range sorted {
		order = append(order, byID[id])
	}
	return order
}

// unmetRequirement reports the first requirement the completed statuses do
// not satisfy, in declaration order of the requirements — deterministic.
func unmetRequirement(reqs []CaseRequirement, done map[string]Status) (CaseRequirement, Status, bool) {
	for _, req := range reqs {
		actual := done[req.CaseID]
		if len(req.Acceptable) == 0 {
			continue // ordering only
		}
		ok := false
		for _, a := range req.Acceptable {
			if actual == a {
				ok = true
				break
			}
		}
		if !ok {
			return req, actual, true
		}
	}
	return CaseRequirement{}, Passed, false
}
