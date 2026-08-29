// Copyright 2026 The Phase Contributors
// SPDX-License-Identifier: MIT

package phase

import "sort"

// Explain answers "what would this run do" without executing anything —
// a dry-run projection of the plan. It subsumes Preflight: its first act
// is the same validation Start performs, so every LoadError surfaces
// here, statically.
//
// Each phase's disposition is a three-way answer — will-run, declined (with
// the same structured DeclineSource the report uses), or conditional: a
// When-gated phase's answer is not knowable statically, and Explain reports
// that instead of claiming false precision.

type PlanDisposition string

const (
	PlanWillRun     PlanDisposition = "will_run"
	PlanDeclined    PlanDisposition = "declined"
	PlanConditional PlanDisposition = "conditional" // gated by When: not statically predictable
)

// Plan is the static projection of one Start call.
type Plan struct {
	CaseOrder []string   // execution order (topological when case dependencies exist)
	Cases     []CasePlan // declaration order — the report's own ordering contract
}

type CasePlan struct {
	ID        string
	Exclusive bool
	DependsOn []CaseRequirement
	Skipped   bool   // status-declared (Quarantined/Blocked/Draft)
	Reason    string // for Skipped
	Phases    []PhasePlan
}

type PhasePlan struct {
	ID            ID
	Level         int // DAG depth: same-level phases may overlap under MaxPhaseConcurrency
	Group         ID  // the group this phase belongs to, if any
	Timing        Timing
	Disposition   PlanDisposition
	DeclineSource DeclineSource // set when Disposition is declined
	Reason        string
}

// Explain validates exactly as Start would, then projects the plan. It
// evaluates only the static gates (Enabled, Selects, AppliesTo — all
// declarative by contract); a phase implementing When is conditional.
func (r *Runner) Explain(cases []Case) (*Plan, error) {
	if err := r.Preflight(cases); err != nil {
		return nil, err
	}
	plan := &Plan{}
	for _, idx := range caseOrder(cases) {
		plan.CaseOrder = append(plan.CaseOrder, cases[idx].ID())
	}
	for _, c := range cases {
		cp := CasePlan{ID: c.ID(), DependsOn: caseDeps(c)}
		cp.Exclusive, _ = c.Exclusive()
		if st := c.Status(); st != Active {
			cp.Skipped = true
			cp.Reason = "case status: " + st.String()
			plan.Cases = append(plan.Cases, cp)
			continue
		}
		for _, ph := range r.phases {
			id := ph.ID()
			pp := PhasePlan{ID: id, Level: r.levelOf[id]}
			for gi, g := range r.groups {
				_ = gi
				for _, m := range g.Members {
					if m == id {
						pp.Group = g.ID
					}
				}
			}
			timing := r.resolvedTiming(id)
			if override, ok := c.Timing(id); ok {
				timing = resolveTiming(timing, override)
			}
			pp.Timing = timing
			switch {
			case func() bool { en := r.config.Phases[id].Enabled; return en != nil && !*en }():
				pp.Disposition, pp.DeclineSource, pp.Reason = PlanDeclined, DeclinedByConfig, "disabled by configuration"
			case func() bool { sel, _ := c.Selects(id); return !sel }():
				_, reason := c.Selects(id)
				pp.Disposition, pp.DeclineSource, pp.Reason = PlanDeclined, DeclinedByCase, reason
			case !ph.AppliesTo(c, r.config).Applies:
				pp.Disposition, pp.DeclineSource, pp.Reason = PlanDeclined, DeclinedByPhase, ph.AppliesTo(c, r.config).Reason
			default:
				if _, conditional := ph.(When); conditional {
					pp.Disposition = PlanConditional
					pp.Reason = "gated by a condition over recorded evidence — not statically predictable"
				} else {
					pp.Disposition = PlanWillRun
				}
			}
			cp.Phases = append(cp.Phases, pp)
		}
		sort.SliceStable(cp.Phases, func(i, j int) bool {
			return r.rankOf[cp.Phases[i].ID] < r.rankOf[cp.Phases[j].ID]
		})
		plan.Cases = append(plan.Cases, cp)
	}
	return plan, nil
}
