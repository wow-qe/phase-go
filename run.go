// Copyright 2026 The Phase Contributors
// SPDX-License-Identifier: MIT

package phase

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/wow-qe/phase-go/result"
)

// Recorder is where a phase reports what it saw and what it decided. *Run
// satisfies it; phasetest.SpyRecorder satisfies it for consumer unit tests.
//
// The three methods are three different facts, kept distinct on purpose:
//
//	Record   what was decided — a comparison's outcome, with evidence
//	Observe  what was seen — a row, a response, a message, pre-judgement
//	Fail     the environment broke — an error is not a failed comparison
//
// Recording into a case whose verdict has already been derived panics
// (testing.T's choice for Log-after-test, for the same reason): evidence
// silently dropped after completion is an invisible gap in the report.
// A goroutine a phase spawns must finish before the phase returns, or carry
// its own synchronisation.
type Recorder interface {
	Record(result.Result)
	Observe(name string, value any)
	Fail(error)
}

// Observation is a raw reading, kept alongside the judgements made from it.
// A report holding only results is unfalsifiable: when a check fails, the
// reader needs what was seen in the moments before, not only the conclusion.
type Observation struct {
	Phase  ID             `json:"phase"`
	Source EvidenceSource `json:"source"` // typed attribution
	Name   string         `json:"name"`
	Value  any            `json:"value"`
	At     time.Time      `json:"at"`

	rank int // topological rank, unexported - never on the schema surface
}

// runCore is the single shared evidence store for one case — one per run,
// however many phase-bound views exist over it.
type runCore struct {
	mu           sync.Mutex
	sealed       bool // set after finish(): the verdict is derived, the ledger is closed
	results      []attributedResult
	observations []Observation
	errs         []attributedError
	flakes       []string // Tolerate's passed-only-on-retry marks; finish derives Flaked from these
	facts        map[KeyID]any

	obsLimit   int // Config.MaxObservationsPerCase; 0 = unlimited
	droppedObs int // observations refused by the cap; surfaced as a loud marker at finish

	now   func() time.Time                           // injected; phases never read the wall clock
	sleep func(context.Context, time.Duration) error // injected; phases never call time.Sleep

	// retrySink, when set by the runner, receives live retry-attempt
	// heartbeats (RetryAttempt) - purely observability, structurally
	// separate from evidence, and never charged against any budget.
	retrySink func(phase ID, retryKind string, attempt, of int, lastErr string)
}

// Run is one case's journey through the phases: immutable inputs, write-only
// evidence, and the typed handoff store.
//
// A Run handed to a phase is a view bound to that phase at
// construction — attribution is a property of the handle, not of a mutable
// "current phase" field read at record time. A recording that outlives its
// phase (a stashed handle, a goroutine, a parallel scheduler) is therefore
// attributed to the phase that owned the handle, never to whichever phase
// happens to be running. All views share one evidence core; recording is
// safe for concurrent use, and evidence is emitted in deterministic order at
// case completion.
type Run struct {
	c     Case
	scope Scope
	core  *runCore

	phase   ID
	timing  Timing
	rank    int            // this handle's topological rank, stamped into every evidence entry at record time
	allowed map[KeyID]bool // this phase's declared Produces; nil = unrestricted (setup/teardown, test seam)

	// depScope is the phase's transitive DependsOn set (the runner's reach
	// map), gating PriorEvidence by construction. Nil on unbound/test views.
	depScope map[ID]bool

	// source is this handle's typed attribution, stamped into every
	// evidence entry beside the legacy phase string.
	source EvidenceSource

	// attemptsUsed counts WaitUntil polls / Tolerate checks consumed under
	// this handle - the runner copies it onto the outcome row.
	attemptsUsed int

	// stage selects this view's row in the capability table;
	// capViolations counts refused uses so the caller can land the
	// violation as the outcome.
	stage         stageKind
	capViolations int

	// intercept, when set, transforms every result this handle records before
	// it reaches the ledger. It exists for exactly one caller: the AlwaysPass
	// mutation gate (phasetest), installed through the sanctioned
	// InterceptRecords test hook. View-scoped on purpose — an interception
	// must not leak past the phase that was deliberately mutated.
	intercept func(result.Result) result.Result
}

type attributedResult struct {
	Phase  ID
	Source EvidenceSource
	Result result.Result
	rank   int // topological rank; drain sorts by it so evidence order is invariant under completion order
}

type attributedError struct {
	Phase  ID
	Source EvidenceSource
	Err    error
	rank   int
}

// newRun is internal: the Runner constructs runs. Tests construct them too,
// via this same door, so there is exactly one initialisation path.
func newRun(c Case, scope Scope) *Run {
	return &Run{
		c:      c,
		scope:  scope,
		stage:  stageFixtureSetup, // the base view serves fixture setup
		source: EvidenceSource{Kind: SourceFixture},
		core: &runCore{
			facts: make(map[KeyID]any),
			now:   time.Now,
			sleep: func(ctx context.Context, d time.Duration) error {
				t := time.NewTimer(d)
				defer t.Stop()
				select {
				case <-ctx.Done():
					return ctx.Err()
				case <-t.C:
					return nil
				}
			},
		},
	}
}

// bound returns a view over the same evidence core, attributed to the given
// phase with its resolved timing. restrict pins Put to the declared keys;
// without it the view is unrestricted (fixtures, test seam).
func (r *Run) bound(id ID, t Timing, produces []KeyID, restrict bool) *Run {
	view := &Run{c: r.c, scope: r.scope, core: r.core, phase: id, timing: t,
		stage:  stageExec,
		source: EvidenceSource{Kind: SourcePhase, ID: id}}
	if restrict {
		view.allowed = make(map[KeyID]bool, len(produces))
		for _, k := range produces {
			view.allowed[k] = true
		}
	}
	return view
}

// Case is the immutable declaration this run executes.
func (r *Run) Case() Case { return r.c }

// Scope is the identity the framework allocated for this run.
func (r *Run) Scope() Scope { return r.scope }

// now and sleep delegate to the shared core so every view of one run tells
// the same time.
func (r *Run) now() time.Time { return r.core.now() }

func (r *Run) sleep(ctx context.Context, d time.Duration) error { return r.core.sleep(ctx, d) }

// mustBeOpen panics if the case's verdict has already been derived — the
// loud alternative to silently dropping late evidence.
func (c *runCore) mustBeOpen(what string, phase ID) {
	if c.sealed {
		panic(&FrameworkError{
			Invariant: "no evidence after completion",
			Detail: "phase \"" + string(phase) + "\" called " + what + " after the case completed — " +
				"the verdict was already derived from the evidence, so this recording could never be seen. " +
				"A goroutine spawned by a phase must finish before the phase returns.",
		})
	}
}

// drain seals the ledger and returns its contents under one lock
// acquisition: no straggler can slip evidence in between "the verdict reads
// the ledger" and "the ledger closes". Recording after drain panics via
// mustBeOpen.
func (c *runCore) drain() (results []attributedResult, obs []Observation, errs []attributedError, flakes []string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.sealed = true
	results = append([]attributedResult(nil), c.results...)
	obs = append([]Observation(nil), c.observations...)
	// Deterministic topological order, whatever order completion (or,
	// under concurrency, lock acquisition) appended in. Stable, so a single
	// handle's own call order is preserved within its rank.
	sort.SliceStable(results, func(i, j int) bool { return results[i].rank < results[j].rank })
	sort.SliceStable(obs, func(i, j int) bool { return obs[i].rank < obs[j].rank })
	if c.droppedObs > 0 {
		// The cap must never be silent — the reader sees exactly what the
		// retention limit cost them, as evidence, in the report itself.
		obs = append(obs, Observation{
			Name: "evidence retention limit reached",
			Value: fmt.Sprintf("dropped %d observation(s) after the first %d (Config.MaxObservationsPerCase)",
				c.droppedObs, c.obsLimit),
			At: c.now(),
		})
	}
	errs = append([]attributedError(nil), c.errs...)
	sort.SliceStable(errs, func(i, j int) bool { return errs[i].rank < errs[j].rank })
	flakes = append([]string(nil), c.flakes...)
	return results, obs, errs, flakes
}

// Record stores a decided result, attributed to the phase this handle is
// bound to.
func (r *Run) Record(res result.Result) {
	if !r.can(capRecord) {
		r.deny("Record")
		return
	}
	if r.intercept != nil {
		res = r.intercept(res)
	}
	r.core.mu.Lock()
	defer r.core.mu.Unlock()
	r.core.mustBeOpen("Record", r.phase)
	r.core.results = append(r.core.results, attributedResult{Phase: r.phase, Source: r.source, Result: res, rank: r.rank})
}

// Observe stores a raw reading, attributed to the phase this handle is bound
// to.
func (r *Run) Observe(name string, value any) {
	if !r.can(capObserve) {
		r.deny("Observe")
		return
	}
	r.core.mu.Lock()
	defer r.core.mu.Unlock()
	r.core.mustBeOpen("Observe", r.phase)
	if r.core.obsLimit > 0 && len(r.core.observations) >= r.core.obsLimit {
		r.core.droppedObs++ // counted; a marker observation reports the total at finish
		return
	}
	r.core.observations = append(r.core.observations, Observation{
		Phase: r.phase, Source: r.source, Name: name, Value: value, At: r.core.now(), rank: r.rank,
	})
}

// TranscriptEntry is one adapter exchange kept as evidence: request and
// response, structured, so a reader can diff them across attempts instead
// of parsing prose.
type TranscriptEntry struct {
	Op       string `json:"op"`
	Request  any    `json:"request,omitempty"`
	Response any    `json:"response,omitempty"`
}

// Transcribe records one adapter exchange, attributed like any observation.
// It rides the evidence path (and the retention cap) rather than a side
// channel, so the transcript is IN the report a reader already has.
func (r *Run) Transcribe(op string, request, response any) {
	r.Observe("transcript: "+op, TranscriptEntry{Op: op, Request: request, Response: response})
}

// Fail records an environment/adapter error. The case becomes Errored, not
// Failed: reporting an outage as a product failure misdirects debugging toward the
// wrong thing.
func (r *Run) Fail(err error) {
	if err == nil {
		return
	}
	r.core.mu.Lock()
	defer r.core.mu.Unlock()
	r.core.mustBeOpen("Fail", r.phase)
	r.core.errs = append(r.core.errs, attributedError{Phase: r.phase, Source: r.source, Err: err, rank: r.rank})
}

// Require routes an adapter call's error into Fail and tells the phase
// whether to continue. The only sanctioned way to call an adapter inside a
// phase — it makes "flatten the error into an empty result" unwritable:
//
//	rows, err := db.Rows(ctx, q)
//	rows, ok := phase.Require(r, rows, err)
//	if !ok { return nil }
//
// (Go permits f(g()) only when g supplies all of f's arguments, so the
// adapter call cannot be inlined past the Recorder parameter — the
// two-line form above is the pattern.)
//
// Named Require, not Must: in Go a Must* function panics, and this one does
// not. Require is also testify's word, already read as "or stop here".
func Require[T any](r Recorder, v T, err error) (T, bool) {
	if err != nil {
		r.Fail(err)
		var zero T
		return zero, false
	}
	return v, true
}

// markFlake records that a tolerated check passed only on a retry.
// Tolerate-only: it is the single producer of these marks, and finish() is
// their single reader.
func (r *Run) markFlake(desc string) {
	r.core.mu.Lock()
	defer r.core.mu.Unlock()
	r.core.mustBeOpen("Tolerate", r.phase)
	r.core.flakes = append(r.core.flakes, desc)
}

// failingRecorded counts the failing results recorded so far under this
// handle's phase - the executePhase probe that keeps a recorded
// precondition violation out of the environment-error channel.
func (r *Run) failingRecorded() int {
	r.core.mu.Lock()
	defer r.core.mu.Unlock()
	n := 0
	for _, ar := range r.core.results {
		if ar.Phase == r.phase && !ar.Result.Passed() {
			n++
		}
	}
	return n
}

// currentTiming is the resolved Timing of the phase this handle is bound to.
func (r *Run) currentTiming() (ID, Timing) { return r.phase, r.timing }

// Snapshot accessors: copies, in recorded order, for the report layer.

func (r *Run) snapshotResults() []attributedResult {
	r.core.mu.Lock()
	defer r.core.mu.Unlock()
	out := make([]attributedResult, len(r.core.results))
	copy(out, r.core.results)
	return out
}

func (r *Run) snapshotObservations() []Observation {
	r.core.mu.Lock()
	defer r.core.mu.Unlock()
	out := make([]Observation, len(r.core.observations))
	copy(out, r.core.observations)
	return out
}

func (r *Run) snapshotErrors() []attributedError {
	r.core.mu.Lock()
	defer r.core.mu.Unlock()
	out := make([]attributedError, len(r.core.errs))
	copy(out, r.core.errs)
	return out
}
