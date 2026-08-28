# Architecture

Phase is a library for phase-wise E2E testing of asynchronous, multi-store systems:
one request fans out across a queue, a database, a service API, an external provider,
and correctness must be verified in all of them, in stages. Phase supplies the engine —
ordering, applicability, timing, handoff, results, reporting. The consumer supplies the
phases, adapters, fixtures, and case type.

This file is the implementation-facing summary and MUST stay
consistent with it.

## Package layout

One public package at the root — `phase.Runner`, `phase.Run` read without stutter (the
result type lives in its own `result` package: `result.Result`), and
import cycles are structurally impossible. One file per concern; the file list is the
separation of concerns:

```
phase.go          package doc; ID, KeyID, EntityRef, Ref     [shared vocabulary]
case.go           Case, CaseStatus, ParseStatus              [declaration]
fixture.go        Fixture (Setup/Teardown)                   [declaration]
keys.go           Key[T], Declare, Keys, Put, Get            [identity / handoff]
scope.go          Scope, ScopeAllocator, defaultAllocate     [identity]
settings.go       Settings, Timing, Config, Validate         [configuration]
applicability.go  Applicability, Applies, Skip               [selection]
pipeline.go       Pipeline, NewPipeline                      [assembly]
preflight.go      Preflight (case-level refusals)            [validation]
runner.go         Runner, NewRunner, orchestration loop      [construction + execution]
session.go        Session, CaseReport, PhaseOutcome          [execution state]
run.go            Run views over runCore; Recorder; Require  [per-case state]
wait.go           WaitUntil                                  [waiting]
tolerate.go       Tolerate — the Flaked producer (§7.3)      [tolerance]
hooks.go          BeforeHook, AfterHook, executePhase        [phase lifecycle]
group.go          Group, Pipeline.Group, group lifecycle     [scoped lifecycle]
when.go           When guards, PriorEvidence                 [conditions]
casedep.go        CaseDependency, case DAG, DependencyFailure [case lifecycle]
suite.go          Tagged, SelectByTags, ErrNoMatch           [selection]
progress.go       ProgressEvent, WithProgress, LogProgress   [observability]
redact.go         Report.Redact / RedactMatching             [trust boundary]
merge.go          MergeReports (shard merge)                 [output]
status.go         Status enum                                [judgement vocabulary]
report.go         Report, Summary, Verify, ExitCode          [output]
errors.go         LoadError codes, FrameworkError, sentinels [errors]
testhooks.go      NewRunForTesting + RunOptions — the one    [test seam]
                  sanctioned engine/phasetest crossing
doc.go            package documentation                      [docs]

result/           Result and its invariant — importable alone
phasetest/        public test kit: Clock, SpyRecorder, RunFor,
                  ConformanceCase, Gutted + AlwaysPass (mutation gates)
internal/dag/     topo sort + cycle detection, pure
x/config/         YAML loading: Config + case manifests      (separate module)
x/comparators/    ContainsAll, ValueMatch, EachEntity,
                  Unchanged, PollCompare                     (separate module)
cmd/snapdiff/     report snapshot capture/compare
examples/         checkout (flagship, all features), misuse (adversarial: every misuse answered) and provisioning (minimal) — the compatibility and misuse canaries, tested in CI

planned, not yet in the tree (concepts, deliberately without filenames —
a planned row naming a file that later exists reads as a duplicate):
- Renderer extension seam (DESIGN §3.4; report output beyond WriteJSON)
- internal/norm: deterministic ordering, volatile-value fencing
- x/junit: JUnit renderer (separate module)
- x/adapters: kafka, postgres, http reference adapters + conformance suites
```

Dependency rule (mechanically checkable): `result` imports stdlib only; the root package
imports stdlib + `result` + `internal/*`; `x/*` and `cmd/*` may import the root; the core
never imports `x/*`. The core `go.mod` carries no third-party dependency — if it grows a
driver, it has stopped being a library and become a framework with opinions about someone
else's stack.

## Data flow

```
Case ─→ Preflight ─→ Setup(fixtures) ─→ per phase: AppliesTo → Run → results
                                              │ (values handed forward via typed keys)
                                              └→ Teardown (always) ─→ Report
```

Values discovered at runtime move between phases only through declared typed keys
(`Declare`/`Put`/`Get`); `Get` on an unproduced key is an error, never a zero value.
Cases are immutable inputs — a run never ends holding a case that differs from what was
loaded.

## Invariants (each backed by a test, several by the mutation gate)

1. A `Result` cannot represent "passed with zero comparisons" — enforced in the type.
2. An error is not a failed comparison: `Fail(err)` and `Record(result)` are distinct.
3. One writer of a case's outcome: derived from recorded evidence, set nowhere else.
4. Applicability is declared, never inferred from live system state; every skip carries
   a recorded reason.
5. Every `Requires()` key is produced within the phase's transitive dependencies —
   validated at load, before anything runs.
6. Two runs of unchanged code produce byte-identical reports after normalisation.
7. Teardown runs on success, failure, panic, and cancellation.
8. The report states what was NOT verified, not only what passed.

## Failure modes and error families

`LoadError` (mis-declared suite; nothing ran; exit 2) · ordinary errors during a case
(status `Errored`, suite continues; exit 1 if anything failed) · `FrameworkError`,
which carries TWO distinct fates by where it surfaces: CONTAINED (a stage-capability
or Produces violation caught mid-run — recorded as that case's evidence, case
`Errored`, report stays internally consistent, exit 1, same family as any contained
panic) versus UNCONTAINED (`Report.Verify()` itself fails — the numbers cannot be
trusted, exit 3). `Report.Verify()` is an assertion, not a repair: if it fires, the
framework has a bug. The split is pinned by
`examples/misuse` (`TestContainedViolationKeepsTheReportTrustworthy` vs the tamper
battery).

## Concurrency model

The `Runner` owns every goroutine it starts; `Start` returns only after they exit.
`Recorder` is concurrency-safe within a `Run`; evidence is rank-stamped at record time
and emitted in deterministic TOPOLOGICAL order at case completion — invariant 6 holds
whatever the completion interleaving. Context cancellation marks in-flight cases
`Errored` (`cancelled`), never `Failed`; fixture and group teardown run on detached
contexts bounded by `Timing.Timeout`. A panic in any consumer call (phase, hook,
condition, lifecycle) is contained to its case.

Both concurrency knobs default to sequential — an unconfigured consumer runs today's
exact behaviour. `Config.MaxPhaseConcurrency > 1` overlaps same-DAG-level phases within
a case: a side-effect-free step computes each phase's row; rows and errored-map entries
apply at each level barrier in rank order. `Config.MaxCaseConcurrency > 1` runs cases in
a bounded pool over the case DAG, all bookkeeping on one scheduler goroutine; dispatch
order is deterministic, `Exclusive()` cases drain the pool and run alone,
`WithCaseObserver`/`WithProgress` fire in completion order (their value), and
`Session.Cases()` keeps declaration order via indexed writes.

## Lifecycles (documented, not reified — no new enum constants)

- **Phase** (per case): pending → {cancelled | disabled | case-declined |
  phase-declined | group-setup-failed | pruned | condition-declined} or
  → [group setup fires if first member] → When → SettleDelay → Before → Run → After →
  {Passed | Errored}, landing exactly one `PhaseOutcome` (Stage marks a hook-produced
  outcome; `results_recorded`/`failing_recorded` carry the evidence counts).
- **Phases/Group** (per case): pending → setting-up (first member reaching execution)
  → active → tearing-down (every member landed, any outcome) →
  {Passed | setup-failed | teardown-failed}, reported as one `GroupOutcome`
  (NotApplicable "no member ran" when setup never fired).
- **Case** (per session): declared → preflighted → {status-skipped |
  dependency-skipped (structural `dependency_failure`)} → scope-allocated →
  fixtures-up → phases → fixtures-down → verdict
  {Passed | Failed | Errored | Flaked | NotApplicable}.

## Trust boundaries

Consumer phases and adapters are untrusted code: panics are contained, their errors are
`Errored` not `Failed`, and nothing they return is trusted to be a comparison unless it
arrives as a `Result`. Observations and transcripts can carry secrets — `Redact` and
`Config.RedactKeys` exist so reports are safe to attach to tickets. Reference data may be
resolved from the system under test (binding names to ids); expectations may never be
derived from it.


## Capability matrix (generated from stageCaps — TestArchitectureTablesMatchTheCode)

<!-- generated:capabilities begin -->
| Stage | Record | Observe | Put | Get | PriorEvidence |
|---|---|---|---|---|---|
| execution | yes | yes | yes | yes | yes |
| condition | — | yes | — | yes | yes |
| group setup | yes | yes | yes | yes | — |
| group teardown | yes | yes | — | yes | — |
| fixture setup | yes | yes | yes | yes | — |
| fixture teardown | yes | yes | — | yes | — |
| session | yes | yes | — | yes | — |
<!-- generated:capabilities end -->

## Group lifecycle (generated from groupTransitions)

<!-- generated:group-lifecycle begin -->
- pending → setting-up
- setting-up → active | setup-failed
- active → tearing-down
- setup-failed → tearing-down
- tearing-down → done
<!-- generated:group-lifecycle end -->

## Event catalog (generated from EventKind)

<!-- generated:events begin -->
session_started, session_finished, case_started, case_finished, fixture_setup_started, fixture_setup_finished, fixture_teardown_started, fixture_teardown_finished, group_setup_started, group_setup_finished, group_teardown_started, group_teardown_finished, phase_started, phase_finished, retry_attempt
<!-- generated:events end -->

## Recorded decisions

Decisions written down so they are not re-litigated one review cycle at
a time:

- **Produce xor assert (A6, blessed rule).** A phase either PRODUCES facts
  (observes, Puts handoff keys) or ASSERTS judgements (Records results) —
  never both. The example's `settle_wait`/`settle_checks` split is the
  canonical form, not an accident: it is what makes mutation-gate coverage
  provable — `Gutted` can remove exactly the assertions without starving
  dependents, and `AlwaysPass` flips exactly the judgements. A stated design
  rule enforced by review and by `Gutted`'s refusal of producer phases, not
  by the type system.

- **YAML case loading is a consumer factory (A8).** A YAML case manifest
  loader is structurally a per-consumer factory plus a fixture-name registry
  — the consumer owns the case type, the fixture set and their names, so no
  generic `Case` decoder can exist in the engine or in `x/config`. `x/config`
  stays YAML→`phase.Config` only; the Q2 loader will ship as scaffolding for
  consumer factories (registry + decode helpers), never as a universal
  decoder.

- **Multi-module release tags in lockstep (A9).** The root, `x/config` and
  `x/comparators` are separate modules; a root `vX.Y.Z` tag alone leaves the
  extension modules unpublishable. Every release tags ALL THREE — `vX.Y.Z`,
  `x/config/vX.Y.Z` and `x/comparators/vX.Y.Z` — at the same commit; the
  release workflow REFUSES to publish unless the full lockstep tag set is
  present on that commit, and creates one GitHub release per root tag. The
  extension modules' `require` lines must name the released root version
  (their `replace` directives are in-repo development aids a consumer never
  inherits) — see docs/RELEASING.md.

## Compatibility contract

Pre-1.0: breaking changes only at minor versions, with CHANGELOG migration notes.
The report's `schema_version` is independent of the module version; `snapdiff` refuses
cross-version comparison. Go ≥ 1.25. Generics appear exactly where they delete `any`
from consumer code (`Key[T]`, `Get`, `WaitUntil`, `Require`). `examples/` is the
compatibility canary: breaking it is a breaking change by definition.
