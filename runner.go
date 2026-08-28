// Copyright 2026 The Phase Contributors
// SPDX-License-Identifier: MIT

package phase

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"math"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/wow-qe/phase-go/internal/dag"
)

// Runner is the engine: it validates the assembly at construction, refuses
// mis-declared suites at Preflight, and (in Start) executes runs. It is the
// entity; Session and Run are its state at two levels.
type Runner struct {
	phases         []Interface // topological order, deterministic
	byID           map[ID]Interface
	resolved       map[ID]Timing // Defaults overlaid by per-phase Settings
	deps           map[ID][]ID   // effective deps: code ∪ config
	config         Config
	allocator      ScopeAllocator
	reach          map[ID]map[ID]bool  // transitive deps, computed once here, never re-walked
	observer       func(CaseReport)    // streams each finished case; nil = retained-only
	progress       func(ProgressEvent) // per-phase heartbeat; nil = silent
	progressMu     sync.Mutex          // progress may be emitted from concurrent goroutines; also guards observerErrs
	observerErrs   []error             // contained progress-callback panics, drained into the Session at Start's end
	eventObservers []func(Event)       // the unified stream's subscribers, dispatched in registration order
	redactRe       []*regexp.Regexp    // Config.RedactPatterns, compiled and validated at construction
	groups         []Group             // registered groups, declaration order
	memberOf       map[ID][]int        // phase ID -> indices into groups
	rankOf         map[ID]int          // topological rank per node (phases + synthetic), stamped onto bound views
	levelOf        map[ID]int          // DAG depth per node; same-level phases may overlap when configured
	tearRank       []int               // per group, 2*maxMemberRank+1 - teardown evidence sorts right after its last member
}

// allocatorFunc adapts a function into a ScopeAllocator.
type allocatorFunc func(Case) (Scope, error)

func (f allocatorFunc) Allocate(c Case) (Scope, error) { return f(c) }

// NewRunner validates the pipeline against the configuration and returns a
// Runner whose phase order is fixed and deterministic. Everything structural
// is refused HERE — duplicate IDs, unknown or cyclic dependencies, unproduced
// keys, config for phases that do not exist, unrunnable timing — so that
// Preflight only ever judges cases, and Start only ever executes.
// RunnerOption configures a Runner at construction — the extension seam for
// consumers whose environments constrain what the defaults assume.
type RunnerOption func(*Runner)

// WithCaseObserver streams each finished CaseReport to the consumer as it
// completes: a CI writer can persist-and-drop instead of waiting on the
// whole retained Session, and a progress display can show cases landing.
// The observer receives a deep copy — mutating it cannot corrupt the
// Session — and runs on the Start goroutine between cases: a slow observer
// slows the run, it never races it.
func WithCaseObserver(f func(CaseReport)) RunnerOption {
	return func(r *Runner) { r.observer = f }
}

// WithScopeAllocator substitutes the consumer's scope allocation. scope.go
// documented this capability before anything injected it — the "documented,
// read by nothing, and inert" defect shape — found while building the
// example, fixed with this option.
func WithScopeAllocator(a ScopeAllocator) RunnerOption {
	return func(r *Runner) {
		if a != nil {
			r.allocator = a
		}
	}
}

func NewRunner(p *Pipeline, cfg Config, opts ...RunnerOption) (*Runner, error) {
	r := &Runner{
		byID:     make(map[ID]Interface, len(p.phases)),
		resolved: make(map[ID]Timing, len(p.phases)),
		deps:     make(map[ID][]ID, len(p.phases)),
		config:   cfg,
	}

	// Identity: every phase exactly once.
	for _, ph := range p.phases {
		if _, dup := r.byID[ph.ID()]; dup {
			return nil, &LoadError{Code: DuplicatePhaseID, Subject: string(ph.ID()),
				Detail: "two phases in the pipeline carry this ID"}
		}
		r.byID[ph.ID()] = ph
	}

	// Configuration may only speak about phases that exist. A config entry
	// for a missing phase is a typo or a phase someone deleted without
	// cleaning up — and a typo'd entry is an operator switch that silently
	// does nothing, the exact defect class this library was built against.
	for id := range cfg.Phases {
		if _, ok := r.byID[id]; !ok {
			return nil, &LoadError{Code: UnknownPhaseInConfig, Subject: string(id),
				Detail: "configuration names a phase the pipeline does not contain"}
		}
	}

	// Settings.Sub was decoded and read by nothing — the inert-mechanism
	// defect. It is superseded by Group and refused LOUDLY: config that
	// still says sub: fails here, never silently no-ops.
	for id, st := range cfg.Phases {
		if len(st.Sub) > 0 {
			return nil, &LoadError{Code: SettingsSubRemoved, Subject: string(id),
				Detail: "Settings.Sub is superseded by Pipeline.Group; declare a Group instead"}
		}
	}

	// Effective dependencies: code ∪ config. Code declares a phase's true
	// prerequisites; configuration may ADD ordering (an operator serialising
	// two phases) but can never remove one — a config that could delete a
	// code-declared dependency could reorder the pipeline into nonsense
	// without touching a line of it.
	for id, ph := range r.byID {
		seen := map[ID]bool{}
		for _, d := range ph.DependsOn() {
			// A guarded misuse: a declared dependency on the
			// reserved namespace would resolve against a group's internal
			// synthetic node and leak its name into pruning reasons. Group
			// causality is declared via Membership, never by naming plumbing.
			if strings.Contains(string(d), ":") {
				return nil, &LoadError{Code: GroupIDReservedCharacter, Subject: string(id),
					Detail: fmt.Sprintf("depends on %q: the ':' namespace is internal — group ordering comes from membership, not from depending on a group's plumbing", d)}
			}
			if !seen[d] {
				seen[d] = true
				r.deps[id] = append(r.deps[id], d)
			}
		}
		for _, d := range cfg.Phases[id].DependsOn {
			if strings.Contains(string(d), ":") {
				return nil, &LoadError{Code: GroupIDReservedCharacter, Subject: string(id),
					Detail: fmt.Sprintf("config depends_on %q: the ':' namespace is internal", d)}
			}
			if !seen[d] {
				seen[d] = true
				r.deps[id] = append(r.deps[id], d)
			}
		}
	}

	// Groups: validate, then wire each group's synthetic setup node into
	// the graph — an edge to every member, so pruning, ordering and handoff
	// validation ride the machinery that already exists. The synthetic node
	// lives ONLY in the graph structures, never in r.phases (double-execution
	// risk).
	if err := r.validateGroups(p.groups); err != nil {
		return nil, err
	}
	r.groups = p.groups
	r.memberOf = map[ID][]int{}
	synthetic := map[ID]bool{}
	for gi := range r.groups {
		g := &r.groups[gi]
		sid := setupID(g.ID)
		synthetic[sid] = true
		for _, m := range g.Members {
			r.deps[m] = append(r.deps[m], sid)
			r.memberOf[m] = append(r.memberOf[m], gi)
		}
	}

	// Order: deterministic topological sort; cycles and unknown deps refuse.
	// Nodes come from the PHASE SET, not the deps map: a phase with no
	// dependencies has no deps entry, and building nodes from the map made
	// such a phase vanish from the graph - so a dependency ON it read as
	// unknown. Caught by the clean-suite test on first run.
	nodes := make([]dag.Node, 0, len(r.byID)+len(synthetic))
	for id := range r.byID {
		ds := r.deps[id]
		strs := make([]string, len(ds))
		for i, d := range ds {
			strs[i] = string(d)
		}
		nodes = append(nodes, dag.Node{ID: string(id), DependsOn: strs})
	}
	for sid := range synthetic {
		nodes = append(nodes, dag.Node{ID: string(sid)})
	}
	order, err := dag.Sort(nodes)
	if err != nil {
		switch e := err.(type) {
		case *dag.UnknownDependencyError:
			return nil, &LoadError{Code: UnknownDependency, Subject: e.From,
				Detail: fmt.Sprintf("depends on %q, which is not in the pipeline", e.To)}
		case *dag.CycleError:
			return nil, &LoadError{Code: DependencyCycle,
				Subject: fmt.Sprintf("%v", e.Cycle),
				Detail:  "phases depend on each other in a loop; nothing can run first"}
		default:
			return nil, &FrameworkError{Invariant: "dag input", Detail: err.Error()}
		}
	}
	r.rankOf = make(map[ID]int, len(order))
	for i, id := range order {
		r.rankOf[ID(id)] = 2 * (i + 1) // ranks are even; odd slots host group teardowns
		if synthetic[ID(id)] {
			continue // graph-only: the group trigger executes its lifecycle, never this loop
		}
		r.phases = append(r.phases, r.byID[ID(id)])
	}
	r.levelOf = make(map[ID]int, len(order))
	for _, id := range order {
		lvl := 0
		for _, d := range r.deps[ID(id)] {
			if l := r.levelOf[d] + 1; l > lvl {
				lvl = l
			}
		}
		r.levelOf[ID(id)] = lvl
	}

	r.tearRank = make([]int, len(r.groups))
	for gi, g := range r.groups {
		maxRank := 0
		for _, m := range g.Members {
			if rk := r.rankOf[m]; rk > maxRank {
				maxRank = rk
			}
		}
		r.tearRank[gi] = maxRank + 1
	}

	// Reach sets once, in topological order — reach(id) = ∪ over its
	// deps d of {d} ∪ reach(d). The per-phase-per-case fresh DFS this
	// replaces was O(cases × phases²) on chained graphs.
	r.reach = make(map[ID]map[ID]bool, len(r.phases))
	for _, ph := range r.phases {
		id := ph.ID()
		out := map[ID]bool{}
		for _, d := range r.deps[id] {
			out[d] = true
			for dd := range r.reach[d] {
				out[dd] = true
			}
		}
		r.reach[id] = out
	}

	// Handoff graph: one producer per key, and every requirement satisfied
	// within the phase's transitive dependencies — the load-time cure for
	// zero-value handoff reads.
	producer := map[KeyID]ID{}
	for _, ph := range r.phases {
		for _, k := range ph.Produces() {
			if prev, dup := producer[k]; dup {
				return nil, &LoadError{Code: DuplicateKeyProducer, Subject: string(k),
					Detail: fmt.Sprintf("produced by both %q and %q; one writer per key", prev, ph.ID())}
			}
			producer[k] = ph.ID()
		}
	}
	for _, g := range r.groups {
		for _, k := range g.Produces {
			if prev, dup := producer[k]; dup {
				return nil, &LoadError{Code: DuplicateKeyProducer, Subject: string(k),
					Detail: fmt.Sprintf("produced by both %q and group %q; one writer per key", prev, g.ID)}
			}
			producer[k] = setupID(g.ID)
		}
	}
	for _, ph := range r.phases {
		reach := r.transitiveDeps(ph.ID())
		for _, k := range ph.Requires() {
			from, ok := producer[k]
			if !ok || !reach[from] {
				return nil, &LoadError{Code: KeyNeverProduced, Subject: string(ph.ID()),
					Detail: fmt.Sprintf("requires key %q, which no phase in its dependency chain produces", k)}
			}
		}
	}

	// Timing: Defaults overlaid field-wise by per-phase Settings; the
	// resolved value must be runnable. Refusal, not clamping — a silent
	// clamp is a default, and defaults are how coverage disappears.
	for _, ph := range r.phases {
		t := resolveTiming(cfg.Defaults, cfg.Phases[ph.ID()].Timing)
		if t.Attempts < 1 || t.Interval < 0 || t.Timeout < 0 || t.SettleDelay < 0 {
			return nil, &LoadError{Code: TimingInvalid, Subject: string(ph.ID()),
				Detail: fmt.Sprintf("resolved timing is not runnable: %+v", t)}
		}
		r.resolved[ph.ID()] = t
	}

	// A nonsense concurrency knob refuses at load, exactly as
	// TimingInvalid refuses a negative interval.
	if cfg.MaxPhaseConcurrency < 0 || cfg.MaxCaseConcurrency < 0 {
		return nil, &LoadError{Code: TimingInvalid, Subject: "concurrency",
			Detail: fmt.Sprintf("MaxPhaseConcurrency=%d MaxCaseConcurrency=%d: negative concurrency is not a thing", cfg.MaxPhaseConcurrency, cfg.MaxCaseConcurrency)}
	}

	// Redaction patterns must compile HERE - a pattern that fails at
	// paste time is a redaction that silently never ran.
	for _, p := range cfg.RedactPatterns {
		re, err := regexp.Compile(p)
		if err != nil {
			return nil, &LoadError{Code: RedactPatternInvalid, Subject: p,
				Detail: fmt.Sprintf("redact pattern does not compile: %v", err)}
		}
		r.redactRe = append(r.redactRe, re)
	}

	r.allocator = allocatorFunc(defaultAllocate)
	for _, opt := range opts {
		opt(r)
	}
	return r, nil
}

// resolveTiming overlays override onto base field-wise; zero fields inherit.
func resolveTiming(base, override Timing) Timing {
	out := base
	if override.Attempts != 0 {
		out.Attempts = override.Attempts
	}
	if override.Interval != 0 {
		out.Interval = override.Interval
	}
	if override.Timeout != 0 {
		out.Timeout = override.Timeout
	}
	if override.SettleDelay != 0 {
		out.SettleDelay = override.SettleDelay
	}
	return out
}

// resolvedTiming exposes a phase's effective timing to the run layer.
func (r *Runner) resolvedTiming(id ID) Timing { return r.resolved[id] }

// transitiveDeps is the set of phases reachable through effective deps —
// the reach set memoized at NewRunner. Read-only: callers must not mutate.
func (r *Runner) transitiveDeps(id ID) map[ID]bool { return r.reach[id] }

// emitEvent is the ONE dispatch chokepoint of the unified stream:
// serialized (the callback is never entered concurrently), contained (an
// observer panic is degraded observability, never a crash), and it also
// drives the frozen legacy projections so WithProgress/WithCaseObserver
// remain exact projections of the stream, not parallel paths.
func (r *Runner) emitEvent(ev Event) {
	r.progressMu.Lock()
	defer r.progressMu.Unlock()
	for _, obs := range r.eventObservers {
		obs := obs
		if err := contain("event observer callback", func() error { obs(ev); return nil }); err != nil {
			r.observerErrs = append(r.observerErrs, err)
		}
	}
	r.projectLegacy(ev)
}

// projectLegacy reproduces the historical WithProgress/WithCaseObserver
// behavior from the stream. Frozen: started only for phases that reach
// execution; group lifecycle as the pseudo-ID strings; the case observer
// receives the full report clone (now redacted — the one deliberate
// improvement, closing the same leak on the legacy surface).
func (r *Runner) projectLegacy(ev Event) {
	if r.progress != nil {
		var pe *ProgressEvent
		switch e := ev.(type) {
		case PhaseStartedEvent:
			if e.Reached {
				pe = &ProgressEvent{CaseID: e.CaseID(), Phase: e.Phase, Stage: "started", Status: Passed}
			}
		case PhaseFinishedEvent:
			pe = &ProgressEvent{CaseID: e.CaseID(), Phase: e.Outcome.ID, Stage: "finished", Status: e.Outcome.Status}
		case GroupEvent:
			st := Passed
			if e.Err != "" {
				st = Errored
			}
			switch e.Kind() {
			case GroupSetupStarted:
				pe = &ProgressEvent{CaseID: e.CaseID(), Phase: setupID(e.GroupID), Stage: "started", Status: Passed}
			case GroupSetupFinished:
				pe = &ProgressEvent{CaseID: e.CaseID(), Phase: setupID(e.GroupID), Stage: "finished", Status: st}
			case GroupTeardownStarted:
				pe = &ProgressEvent{CaseID: e.CaseID(), Phase: teardownID(e.GroupID), Stage: "started", Status: Passed}
			case GroupTeardownFinished:
				pe = &ProgressEvent{CaseID: e.CaseID(), Phase: teardownID(e.GroupID), Stage: "finished", Status: st}
			}
		}
		if pe != nil {
			if err := contain("progress callback", func() error { r.progress(*pe); return nil }); err != nil {
				r.observerErrs = append(r.observerErrs, err)
			}
		}
	}
	if r.observer != nil {
		if e, ok := ev.(CaseFinishedEvent); ok {
			if err := contain("case observer callback", func() error { r.observer(e.Report.clone()); return nil }); err != nil {
				r.observerErrs = append(r.observerErrs, err)
			}
		}
	}
}

// redactedCaseClone deep-clones and redacts a case report for emission:
// the live stream is safe by default, exactly as the artifact is.
func (r *Runner) redactedCaseClone(cr CaseReport) CaseReport {
	c := cr.clone()
	if len(r.config.RedactKeys) > 0 {
		match := make(map[string]bool, len(r.config.RedactKeys))
		for _, k := range r.config.RedactKeys {
			match[strings.ToLower(k)] = true
		}
		redactCaseKeys(&c, match)
	}
	for _, re := range r.redactRe {
		redactCasePattern(&c, re)
	}
	return c
}

// redactString applies the configured patterns to a bare string payload.
func (r *Runner) redactString(s string) string {
	for _, re := range r.redactRe {
		s = re.ReplaceAllString(s, "[REDACTED]")
	}
	return s
}

func (r *Runner) eventBaseFor(caseID string, k EventKind) eventBase {
	return eventBase{kind: k, caseID: caseID, at: time.Now()}
}

// lateRank orders evidence recorded after every phase - fixture teardown,
// session-cancellation - deterministically last.
const lateRank = int(^uint(0) >> 1)

// Start executes the cases, sequentially in the order given, and returns the
// finished Session. Preflight runs first — defensively, even if the consumer
// already called it — because executing a mis-declared suite produces a
// report nobody should trust.
//
// The guarantees, each pinned by a test in runner_test.go:
//
//   - phases run in the deterministic dependency order fixed at NewRunner;
//   - every phase gets exactly one PhaseOutcome per case, whatever happened;
//   - a failed result fails the case but does NOT stop evidence-gathering;
//   - a phase error marks the case Errored and prunes only its DEPENDENTS,
//     each with a recorded reason — independent phases still run;
//   - a panic in a consumer phase is contained to its case;
//   - cancellation is Errored, never Failed;
//   - fixtures set up in order, tear down in reverse, on every path;
//   - the case's status is derived in exactly one place, from the evidence.
func (r *Runner) Start(ctx context.Context, cases []Case) (*Session, error) {
	if err := r.Preflight(cases); err != nil {
		return nil, err
	}
	s := &Session{id: newSessionID(), started: time.Now(),
		redactKeys: r.config.RedactKeys, redactPatterns: r.redactRe}
	r.emitEvent(SessionStartedEvent{eventBase: r.eventBaseFor("", SessionStarted),
		SessionID: s.id, CaseCount: len(cases)})
	// Execution follows the case DAG (declaration order when no case
	// declares dependencies - byte-identical to before); the REPORT keeps
	// declaration order via indexed writes, whatever the execution order.
	s.cases = make([]CaseReport, len(cases))
	if r.config.MaxCaseConcurrency > 1 && len(cases) > 1 {
		r.runCasePool(ctx, cases, s)
	} else {
		done := map[string]Status{}
		for _, idx := range caseOrder(cases) {
			c := cases[idx]
			r.emitEvent(CaseStartedEvent{eventBase: r.eventBaseFor(c.ID(), CaseStarted), DeclarationIndex: idx})
			cr := r.caseOrSkip(ctx, c, done)
			done[c.ID()] = cr.Status
			s.cases[idx] = cr
			r.emitEvent(CaseFinishedEvent{eventBase: r.eventBaseFor(c.ID(), CaseFinished),
				Report: r.redactedCaseClone(cr)})
		}
	}
	r.emitEvent(SessionFinishedEvent{eventBase: r.eventBaseFor("", SessionFinished), SessionID: s.id})
	r.progressMu.Lock()
	s.observerErrs = append(s.observerErrs, r.observerErrs...)
	r.observerErrs = nil
	r.progressMu.Unlock()
	s.finished = time.Now()
	return s, nil
}

// caseOrSkip runs the case, or synthesises the loud dependency-skip report
// when a requirement is unmet - one derivation for both drivers. The done
// map is only ever read on the calling (scheduler) goroutine.
func (r *Runner) caseOrSkip(ctx context.Context, c Case, done map[string]Status) CaseReport {
	if cr, unmet := dependencySkip(c, done); unmet {
		return cr
	}
	return r.runCase(ctx, c)
}

// dependencySkip evaluates the case's requirements against completed
// verdicts and, when unmet, builds the loud structural skip report.
func dependencySkip(c Case, done map[string]Status) (CaseReport, bool) {
	req, actual, unmet := unmetRequirement(caseDeps(c), done)
	if !unmet {
		return CaseReport{}, false
	}
	return CaseReport{CaseID: c.ID(), Status: Skipped,
		Started: time.Now(), Finished: time.Now(),
		Reason: fmt.Sprintf("case dependency: %q did not reach an acceptable status (was %s)", req.CaseID, actual),
		DependencyFailure: &DependencyFailure{CaseID: req.CaseID,
			Acceptable: append([]Status(nil), req.Acceptable...), Actual: actual},
	}, true
}

// runCasePool is the case scheduler: a bounded pool over the case DAG.
// Dispatch order is deterministic (lowest declaration index among ready
// cases); completion order is not - WithCaseObserver fires in completion
// order (that is its value), Session.Cases() keeps declaration order via
// the indexed writes. An Exclusive() case drains the pool, runs alone, and
// the pool refills after it - its whole point is that nothing else is
// mid-flight while it mutates what it declared exclusive access to.
// All bookkeeping lives on this one goroutine; workers only run cases.
func (r *Runner) runCasePool(ctx context.Context, cases []Case, s *Session) {
	n := len(cases)
	limit := r.config.MaxCaseConcurrency
	indeg := make([]int, n)
	byID := make(map[string]int, n)
	for i, c := range cases {
		byID[c.ID()] = i
	}
	dependents := make(map[int][]int)
	for i, c := range cases {
		for _, req := range caseDeps(c) {
			di := byID[req.CaseID]
			indeg[i]++
			dependents[di] = append(dependents[di], i)
		}
	}
	var ready []int
	for i, d := range indeg {
		if d == 0 {
			ready = append(ready, i)
		}
	}
	sort.Ints(ready)

	type caseDone struct {
		idx int
		cr  CaseReport
	}
	results := make(chan caseDone)
	done := map[string]Status{}
	inFlight := 0
	solo := false // an exclusive case is running; dispatch nothing beside it

	dispatch := func() {
		for !solo && len(ready) > 0 && inFlight < limit {
			idx := ready[0]
			c := cases[idx]
			if excl, _ := c.Exclusive(); excl {
				if inFlight > 0 {
					return // drain first; this stays at the head of the queue
				}
				solo = true
			}
			ready = ready[1:]
			inFlight++
			// The dependency check reads done HERE, on the scheduler
			// goroutine - workers never touch shared bookkeeping.
			r.emitEvent(CaseStartedEvent{eventBase: r.eventBaseFor(c.ID(), CaseStarted), DeclarationIndex: idx})
			if cr, unmet := dependencySkip(c, done); unmet {
				solo = false
				go func(idx int, cr CaseReport) { results <- caseDone{idx: idx, cr: cr} }(idx, cr)
				continue
			}
			go func(idx int, c Case) {
				results <- caseDone{idx: idx, cr: r.runCase(ctx, c)}
			}(idx, c)
			if solo {
				return
			}
		}
	}

	for completed := 0; completed < n; completed++ {
		dispatch()
		d := <-results
		inFlight--
		solo = false
		done[cases[d.idx].ID()] = d.cr.Status
		s.cases[d.idx] = d.cr
		r.emitEvent(CaseFinishedEvent{eventBase: r.eventBaseFor(cases[d.idx].ID(), CaseFinished),
			Report: r.redactedCaseClone(d.cr)})
		for _, dep := range dependents[d.idx] {
			indeg[dep]--
			if indeg[dep] == 0 {
				ready = append(ready, dep)
			}
		}
		sort.Ints(ready)
	}
}

func errString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

func newSessionID() string {
	buf := make([]byte, 8)
	_, _ = rand.Read(buf)
	return hex.EncodeToString(buf)
}

// runCase executes one case end to end. It never returns an error: whatever
// happens is evidence in the CaseReport, because a batch must survive any
// single case.
func (r *Runner) runCase(ctx context.Context, c Case) (cr CaseReport) {
	// Named result on purpose: the previous
	// `cr := ...; defer func() { cr.Finished = ... }(); return cr` wrote to
	// the local AFTER the return value was copied out — dead code, masked in
	// the determinism tests because they normalise Finished away. A deferred
	// write only reaches the caller through a NAMED result.
	cr = CaseReport{CaseID: c.ID(), Started: time.Now()}
	defer func() { cr.Finished = time.Now() }()

	// A non-active case is Skipped with its declared status as the reason —
	// not deleted, not quietly failing.
	if st := c.Status(); st != Active {
		cr.Status = Skipped
		cr.Reason = "case status: " + st.String()
		return cr
	}

	if err := ctx.Err(); err != nil {
		cr.Status = Errored
		cr.Reason = "cancelled before start"
		return cr
	}

	scope, err := r.allocator.Allocate(c)
	if err != nil {
		cr.Status = Errored
		cr.Reason = fmt.Sprintf("scope allocation: %v", err)
		return cr
	}
	run := newRun(c, scope)
	run.core.obsLimit = r.config.MaxObservationsPerCase
	if len(r.eventObservers) > 0 {
		// Engine follow-up: no observers means no sink - an unconfigured
		// consumer pays nothing per retry attempt (RetryAttempt has no
		// legacy projection, so nothing else consumes it).
		run.core.retrySink = func(phase ID, retryKind string, attempt, of int, lastErr string) {
			r.emitEvent(RetryAttemptEvent{eventBase: r.eventBaseFor(c.ID(), RetryAttempt),
				Phase: phase, Retry: retryKind, Attempt: attempt, Of: of, LastErr: r.redactString(lastErr)})
		}
	}
	cr.Correlation = scope.Correlation // the thread joining this report to the system's own logs

	// Fixtures: set up in order; tear down in reverse, ALWAYS — a cancelled
	// or panicking run that leaks fixtures poisons the next run (and
	// the source framework's reset-at-start-of-next-batch defect).
	//
	// Teardown is an EXPLICIT call before finish, not a defer: the review
	// gate's probe showed that a deferred teardown runs after finish() has
	// already derived the case's status, so a teardown failure or panic
	// could never reach the outcome — recorded evidence that no verdict
	// consumed. Panic-safety without the defer holds because every consumer
	// call between here and teardown is individually contained (runOnePhase,
	// runOneSetup, runOneTeardown).
	built := r.setupFixtures(ctx, c, run, &cr)

	if cr.Status != Errored { // setup succeeded: run the phases
		r.runPhases(ctx, c, run, &cr)
		// Cancellation noticed only after the walk (e.g. during the final
		// phase) still interrupts the case: its assertions cannot be
		// considered complete, and an interrupted case reporting
		// NotApplicable — or worse, Passed — would hide the interruption.
		if err := ctx.Err(); err != nil {
			lv := run.bound("", Timing{}, nil, false)
			lv.rank = lateRank
			lv.source = EvidenceSource{Kind: SourceSession}
			lv.stage = stageSession
			lv.Fail(fmt.Errorf("session cancelled: %w", err))
			cr.Curtailed = true
			if cr.Reason == "" {
				cr.Reason = "cancelled"
			}
		}
	}

	r.teardownFixtures(built, run, &cr)
	// Finish drains AND seals the ledger under one lock — the verdict and
	// the closing are atomic, so no straggler can slip evidence in between.
	// A goroutine recording after this panics loudly instead of silently
	// dropping evidence the report will never show.
	r.finish(run, &cr)
	return cr
}

// setupFixtures runs Setup in order, stopping at the first failure. It
// returns the fixtures whose Setup was ATTEMPTED — those are the ones whose
// Teardown must run.
func (r *Runner) setupFixtures(ctx context.Context, c Case, run *Run, cr *CaseReport) []Fixture {
	var built []Fixture
	for i, f := range c.Fixtures() {
		// Defence-in-depth: even though Preflight validated a snapshot, a
		// non-idempotent Case could return a nil here; treat it as a setup
		// error, never a nil-deref panic.
		if f == nil {
			cr.Status = Errored
			cr.Reason = fmt.Sprintf("fixture %d became nil after preflight", i)
			run.Fail(fmt.Errorf("fixture %d is nil at setup", i))
			break
		}
		built = append(built, f)
		r.emitEvent(FixtureEvent{eventBase: r.eventBaseFor(c.ID(), FixtureSetupStarted), Index: i})
		err := runOneSetup(ctx, f, run)
		r.emitEvent(FixtureEvent{eventBase: r.eventBaseFor(c.ID(), FixtureSetupFinished), Index: i,
			Err: r.redactString(errString(err))})
		if err != nil {
			// A case whose world could not be built did not fail its
			// assertions: Errored, with the fixture named.
			cr.Status = Errored
			cr.Reason = fmt.Sprintf("fixture %d setup: %v", i, err)
			run.Fail(fmt.Errorf("fixture %d setup: %w", i, err))
			break
		}
	}
	return built
}

// teardownFixtures runs Teardown in reverse on a context detached from any
// cancellation, bounded by the defaults' Timeout when one is set.
func (r *Runner) teardownFixtures(built []Fixture, run *Run, cr *CaseReport) {
	ctx := context.WithoutCancel(context.Background())
	if t := r.config.Defaults.Timeout; t > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, t)
		defer cancel()
	}
	tv := run.bound("", Timing{}, nil, false)
	tv.rank = lateRank // fixture teardown evidence sorts after every phase, deterministically
	tv.source = EvidenceSource{Kind: SourceFixture}
	tv.stage = stageFixtureTeardown
	for i := len(built) - 1; i >= 0; i-- {
		r.emitEvent(FixtureEvent{eventBase: r.eventBaseFor(run.c.ID(), FixtureTeardownStarted), Index: i})
		err := runOneTeardown(ctx, built[i], tv)
		r.emitEvent(FixtureEvent{eventBase: r.eventBaseFor(run.c.ID(), FixtureTeardownFinished), Index: i,
			Err: r.redactString(errString(err))})
		if err != nil {
			tv.Fail(fmt.Errorf("fixture %d teardown: %w", i, err))
		}
	}
}

// runOneSetup contains a panicking Setup for the same reason runOneTeardown
// does — both sides of the fixture lifecycle need the identical containment.
func runOneSetup(ctx context.Context, f Fixture, run *Run) error {
	return contain("setup", func() error { return f.Setup(ctx, run) })
}

// runOneTeardown contains a panicking Teardown exactly as runOnePhase contains
// a panicking phase. The asymmetry matters: a consumer bug in
// Teardown propagated out of Start uncaught and aborted every remaining case
// — contradicting runCase's own "a batch must survive any single case".
func runOneTeardown(ctx context.Context, f Fixture, run *Run) error {
	return contain("teardown", func() error { return f.Teardown(ctx, run) })
}

// runPhases walks the pipeline in its fixed order, producing exactly one
// PhaseOutcome per phase.
func (r *Runner) runPhases(ctx context.Context, c Case, run *Run, cr *CaseReport) {
	// Phases whose dependency (transitively) errored are pruned WITH a
	// reason; independent phases still run — evidence is not abandoned
	// wholesale because one adapter hiccuped.
	errored := map[ID]bool{}

	// Per-case group lifecycle state, keyed both by declaration index
	// and by the synthetic setup-node ID the pruning walk reports.
	groupRuns := make([]*groupRun, len(r.groups))
	bySetup := map[ID]*groupRun{}
	for i := range r.groups {
		groupRuns[i] = &groupRun{g: &r.groups[i], remaining: len(r.groups[i].Members), tearRank: r.tearRank[i],
			state: newMachine(groupPending, groupTransitions)}
		bySetup[setupID(r.groups[i].ID)] = groupRuns[i]
	}

	// Every phase outcome lands through here: appended once, and announced
	// once - a skip is a landing too. A member's landing (whatever the
	// outcome) advances its groups' completion barriers.
	land := func(po PhaseOutcome) {
		cr.Phases = append(cr.Phases, po)
		// The EVENT copy's Reason is redacted at emission
		// (raw adapter error text is the most common secret carrier) - the
		// same treatment Retry/Fixture/Group error strings already get. The
		// stored row keeps the raw text; Report() redacts it at build, as
		// always.
		evOut := po
		evOut.Reason = r.redactString(evOut.Reason)
		r.emitEvent(PhaseFinishedEvent{eventBase: r.eventBaseFor(c.ID(), PhaseFinished), Outcome: evOut})
		for _, gi := range r.memberOf[po.ID] {
			r.memberLanded(groupRuns[gi], run, c.ID())
		}
	}

	// step evaluates ONE phase's full pipeline - gates, group setup, When,
	// timing, hooks, Run - and returns the single row to land plus any extra
	// errored-map entries. It writes NOTHING shared: under level-parallelism
	// many steps run at once, reading errored/groupRuns safely (the
	// level barrier guarantees no concurrent writer), and the driver applies
	// rows and marks in deterministic rank order.
	step := func(ph Interface) (po PhaseOutcome, marks []ID, started bool) {
		id := ph.ID()
		// A view bound to THIS phase - evidence from any branch below
		// is stamped by the handle, not by a mutable current-phase field.
		pv := run.bound(id, Timing{}, nil, false)
		pv.rank = r.rankOf[id]

		if err := ctx.Err(); err != nil {
			pv.Fail(fmt.Errorf("phase %s: %w", id, err))
			return PhaseOutcome{ID: id, Status: Errored, Reason: "cancelled"}, nil, started
		}

		// Operator kill-switch: deliberate coverage loss, visible as such.
		if en := r.config.Phases[id].Enabled; en != nil && !*en {
			return PhaseOutcome{ID: id, Status: Disabled, Reason: "disabled by configuration",
				DeclineSource: DeclinedByConfig}, nil, started
		}

		// The case's declaration (mark the SOURCE).
		if selected, reason := c.Selects(id); !selected {
			return PhaseOutcome{ID: id, Status: NotApplicable, Reason: "case declined: " + reason,
				DeclineSource: DeclinedByCase}, nil, started
		}

		// The phase's own declaration - case and config only, never live
		// state: a contract stated, not enforceable in the type system.
		if a := ph.AppliesTo(c, r.config); !a.Applies {
			return PhaseOutcome{ID: id, Status: NotApplicable, Reason: "phase declined: " + a.Reason,
				DeclineSource: DeclinedByPhase}, nil, started
		}

		// A MEMBER of a group whose setup already failed is not "premise
		// declined" - its world could not be built (engine correction):
		// Errored, cause named, joining the errored map transitively.
		if gr := failedGroupOf(r.memberOf[id], groupRuns); gr != nil {
			return PhaseOutcome{ID: id, Status: Errored,
				Reason:        fmt.Sprintf("group %q setup failed: %v", gr.g.ID, gr.setupFailure()),
				DeclineSource: DeclinedByGroupSetup}, nil, started
		}

		// Pruning: an errored dependency means unmet premises - a recorded
		// outcome with the cause named, never a silent skip.
		if dep, hit := r.firstErroredDep(id, errored); hit {
			if _, isSynthetic := bySetup[dep]; isSynthetic {
				if real, ok := r.firstErroredRealDep(id, errored, bySetup); ok {
					dep = real
				}
			}
			return PhaseOutcome{ID: id, Status: NotApplicable,
				Reason:        fmt.Sprintf("not run: dependency %q errored", dep),
				DeclineSource: DeclinedByDependency}, nil, started
		}

		// The member is about to actually execute - fire its groups'
		// Setup lazily, exactly once per case, before any guard or hook that
		// might read group-provisioned facts.
		for _, gi := range r.memberOf[id] {
			gr := groupRuns[gi]
			r.ensureSetup(ctx, gr, run, c.ID())
			if err := gr.setupFailure(); err != nil {
				return PhaseOutcome{ID: id, Status: Errored,
					Reason:        fmt.Sprintf("group %q setup failed: %v", gr.g.ID, err),
					DeclineSource: DeclinedByGroupSetup}, []ID{setupID(gr.g.ID)}, started
			}
		}

		// The condition gate - over recorded evidence only.
		if w, ok := ph.(When); ok {
			wv := run.bound(id, Timing{}, nil, false)
			wv.rank = r.rankOf[id]
			wv.depScope = r.reach[id]
			wv.stage = stageWhen // the condition row of the capability table
			condOK, reason, err := runOneCondition(ctx, w, wv)
			if wv.capViolations > 0 {
				// The violation IS the outcome, whatever the condition
				// returned: it broke its contract, and the refusal is already
				// on the environment channel.
				return PhaseOutcome{ID: id, Status: Errored, Stage: StageCondition,
					Reason: "condition: used a capability its stage does not have — a condition reads the record, it does not write it"}, nil, started
			}
			if err != nil {
				wv.Fail(fmt.Errorf("phase %s condition: %w", id, err))
				return PhaseOutcome{ID: id, Status: Errored, Reason: "condition: " + err.Error(), Stage: "condition"}, nil, started
			}
			if !condOK {
				if reason == "" {
					reason = "(no reason was given — the condition declined without saying why)"
				}
				return PhaseOutcome{ID: id, Status: NotApplicable, Reason: "condition: " + reason,
					DeclineSource: DeclinedByCondition}, nil, started
			}
		}

		timing := r.resolvedTiming(id)
		if override, ok := c.Timing(id); ok {
			timing = resolveTiming(timing, override)
		}
		// The full view for execution: attribution, resolved timing, Put
		// restricted to declared Produces, PriorEvidence scoped to deps.
		pv = run.bound(id, timing, ph.Produces(), true)
		pv.rank = r.rankOf[id]
		pv.depScope = r.reach[id]

		started = true
		r.emitEvent(PhaseStartedEvent{eventBase: r.eventBaseFor(c.ID(), PhaseStarted),
			Phase: id, Reached: true, Timing: timing})

		if timing.SettleDelay > 0 {
			// The one honest exception to condition-based waiting.
			if err := pv.sleep(ctx, timing.SettleDelay); err != nil {
				pv.Fail(fmt.Errorf("phase %s: %w", id, err))
				return PhaseOutcome{ID: id, Status: Errored, Reason: "cancelled"}, nil, started
			}
		}

		po = executePhase(ctx, ph, pv)
		po.AttemptsUsed = pv.attemptsUsed
		return po, nil, started
	}

	// apply is the single writer of errored and the single caller of land -
	// sequentially inline, under parallelism at each level barrier, always
	// in deterministic order.
	apply := func(po PhaseOutcome, marks []ID, started bool) {
		if !started {
			// Pairing is TOTAL: a gate-declined phase still gets its
			// Started, adjacent to its Finished, so span pairing never orphans.
			r.emitEvent(PhaseStartedEvent{eventBase: r.eventBaseFor(c.ID(), PhaseStarted),
				Phase: po.ID, Reached: false})
		}
		for _, m := range marks {
			errored[m] = true
		}
		if po.Status == Errored {
			errored[po.ID] = true
		}
		land(po)
	}

	if r.config.MaxPhaseConcurrency > 1 {
		// Level-parallel: same-DAG-level phases cannot depend on each other,
		// so their gate reads (errored, group state) are stable for the whole
		// level; rows and marks apply at the barrier, in rank order.
		limit := r.config.MaxPhaseConcurrency
		var level []Interface
		flush := func() {
			if len(level) == 0 {
				return
			}
			pos := make([]PhaseOutcome, len(level))
			mks := make([][]ID, len(level))
			strt := make([]bool, len(level))
			sem := make(chan struct{}, limit)
			var wg sync.WaitGroup
			for i, ph := range level {
				wg.Add(1)
				sem <- struct{}{}
				go func(i int, ph Interface) {
					defer wg.Done()
					defer func() { <-sem }()
					pos[i], mks[i], strt[i] = step(ph)
				}(i, ph)
			}
			wg.Wait()
			for i := range level {
				apply(pos[i], mks[i], strt[i])
			}
			level = level[:0]
		}
		cur := -1
		for _, ph := range r.phases {
			if lvl := r.levelOf[ph.ID()]; lvl != cur {
				flush()
				cur = lvl
			}
			level = append(level, ph)
		}
		flush()
	} else {
		for _, ph := range r.phases {
			po, marks, started := step(ph)
			apply(po, marks, started)
		}
	}
	// Derived at one point from attribution rather than live deltas: how
	// many results each phase recorded, counted by the handle that recorded
	// them — correct even for evidence recorded through a stashed handle
	// while a later phase was running.
	counts, failing := map[ID]int{}, map[ID]int{}
	for _, ar := range run.snapshotResults() {
		counts[ar.Phase]++
		if !ar.Result.Passed() {
			failing[ar.Phase]++
		}
	}
	for i := range cr.Phases {
		cr.Phases[i].Results = counts[cr.Phases[i].ID]
		cr.Phases[i].Failing = failing[cr.Phases[i].ID]
	}
	// One visible row per registered group, declaration order - a group
	// that did nothing says so, and its lifecycle's own evidence is counted
	// under the zero-is-a-fact rule.
	for _, gr := range groupRuns {
		out := gr.outcome()
		out.Recorded = counts[setupID(gr.g.ID)] + counts[teardownID(gr.g.ID)]
		cr.Groups = append(cr.Groups, out)
	}
}

// runOnePhase contains a consumer panic to its case: their bug must not take
// down the batch, and the panic value becomes evidence rather than a crash.
func runOnePhase(ctx context.Context, ph Interface, run *Run) error {
	// The ID-in-message outlier is normalized: callers wrap with the
	// phase ID; the row carries it as a field regardless.
	return contain("phase", func() error { return ph.Run(ctx, run) })
}

// firstErroredRealDep is firstErroredDep restricted to non-synthetic IDs -
// used when the recorded pruning reason must name a real phase.
func (r *Runner) firstErroredRealDep(id ID, errored map[ID]bool, synthetic map[ID]*groupRun) (ID, bool) {
	reach := r.reach[id]
	var hit ID
	found := false
	for dep := range errored {
		if _, isSyn := synthetic[dep]; isSyn {
			continue
		}
		if reach[dep] && (!found || dep < hit) {
			hit, found = dep, true
		}
	}
	return hit, found
}

// firstErroredDep reports whether any transitive dependency of id errored.
func (r *Runner) firstErroredDep(id ID, errored map[ID]bool) (ID, bool) {
	if len(errored) == 0 {
		return "", false // the common case: nothing errored, nothing to walk
	}
	// Iterate the errored set - almost always
	// tiny - with an O(1) reach lookup, instead of walking the possibly-huge
	// reach set. Bounds the cost by |errored|, not |reach(id)|. Ties break
	// lexicographically so the recorded pruning reason is deterministic
	// (map iteration order never was).
	reach := r.reach[id]
	var hit ID
	found := false
	for dep := range errored {
		if reach[dep] && (!found || dep < hit) {
			hit, found = dep, true
		}
	}
	return hit, found
}

// finish derives the case's status — the SINGLE writer — and copies the
// run's evidence into the report.
//
// Derivation order, most informative fact first:
//
//	failed result > recorded error > passed results > nothing at all
//
// A failed comparison is a completed judgement about the product and outranks
// environment noise. An error with no failed comparison is Errored. Passing
// results with no failures is Passed. And ZERO results is NotApplicable,
// never Passed — the case-level form of the founding rule: a case that
// asserted nothing must not report success.
func (r *Runner) finish(run *Run, cr *CaseReport) {
	results, observations, errs, flakes := run.core.drain()
	for _, ar := range results {
		cr.Results = append(cr.Results, AttributedResult{Phase: ar.Phase, Source: ar.Source, Result: view(ar)})
		if !ar.Result.Passed() && cr.FailedIn == "" {
			cr.FailedIn = ar.Phase
		}
	}
	cr.Observations = observations
	for _, ae := range errs {
		cr.Errors = append(cr.Errors, AttributedError{Phase: ae.Phase, Source: ae.Source, Err: ae.Err.Error()})
	}

	if cr.Status == Skipped || cr.Status == Errored {
		return // already decided by status/setup/cancellation paths
	}
	switch {
	case cr.FailedIn != "":
		// A completed failing comparison is a real defect and outranks the
		// cancellation. The case stays Failed; Curtailed (set above) keeps the
		// interruption visible so the two guarantees do not conflict silently.
		cr.Status = Failed
	case cr.Curtailed:
		// Cancelled with no failing result: not a clean pass. The run did not
		// finish, so its silence is not a verdict.
		cr.Status = Errored
	case len(cr.Errors) > 0:
		cr.Status = Errored
	case len(cr.Results) > 0:
		// A pass earned only on a tolerated retry is Flaked, never
		// laundered into Passed — "passed on attempt 3" is a different fact.
		if fl := flakes; len(fl) > 0 {
			cr.Status = Flaked
			cr.Reason = strings.Join(fl, "; ")
		} else {
			cr.Status = Passed
		}
	default:
		cr.Status = NotApplicable
		cr.Reason = "no phase recorded any result for this case; nothing was asserted"
	}
}

func view(ar attributedResult) ResultView {
	return ResultView{
		Name:        ar.Result.Name(),
		Entity:      ar.Result.Entity(),
		Passed:      ar.Result.Passed(),
		Reason:      ar.Result.Reason(),
		Expected:    jsonSafe(ar.Result.Expected()),
		Actual:      jsonSafe(ar.Result.Actual()),
		Comparisons: ar.Result.Comparisons(),
	}
}

// jsonSafe replaces a value encoding/json cannot marshal (NaN/Inf floats) with
// a descriptive string. C3: one non-finite float in one case's evidence used
// to make WriteJSON fail and lose EVERY case's evidence with it - the report
// is the product, and it must degrade a value, never vanish.
func jsonSafe(v any) any {
	switch f := v.(type) {
	case float64:
		if math.IsNaN(f) || math.IsInf(f, 0) {
			return fmt.Sprintf("non-finite float: %v", f)
		}
	case float32:
		g := float64(f)
		if math.IsNaN(g) || math.IsInf(g, 0) {
			return fmt.Sprintf("non-finite float: %v", f)
		}
	}
	return v
}

// sortIDs is a shared helper for deterministic iteration over ID-keyed maps.
func sortIDs(ids []ID) {
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
}
