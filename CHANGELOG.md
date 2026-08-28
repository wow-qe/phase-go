# Changelog

All notable changes to this project are documented here. The format follows
Keep a Changelog; versions follow SemVer (pre-1.0: breaking changes only at
minor bumps, each with a migration note).

## [Unreleased] — v0.1.0 candidate

The first public release. Everything below ships together; the three
modules (`phase-go`, `x/config`, `x/comparators`) release in lockstep at
this version (see `docs/RELEASING.md`).

### Added — core engine
- `result`: `Result` with the founding invariant — a result over zero
  comparisons is a failure, never a pass; unexported fields make the
  invariant unbypassable from outside the package.
- Root package `phase`: typed handoff keys (`Declare`/`Put`/`Get` — `Get`
  fails rather than returning a zero value; `Put` is checked against the
  phase's declared `Produces()` and the stage's capabilities);
  `Interface`/`Func`/`Pipeline`; `NewRunner` with full structural
  validation (typed `LoadError` refusal codes for every misdeclaration);
  `Preflight`; `Start`/`Session` with teardown-on-every-path, panic
  containment at every consumer-code site (phase, hooks, condition,
  fixtures, group lifecycle, observers), dependent-phase pruning with
  recorded reasons, cancellation as `Errored` never `Failed` (with the
  `Curtailed` marker), and a single derivation of case status from
  evidence; `WaitUntil` with per-case attempt budgets and a wall-clock
  `Timeout` backstop that names its budget; `Tolerate` — the only producer
  of `Flaked` — with mandatory justification, bounded attempts and a full
  evidence trail; `Report` (JSON schema v1) with `Verify()` as an
  assertion and one `ExitCode()` mapping (0 green / 1 failed-or-errored /
  2 load refusal / 3 report inconsistent).

### Added — phase & case contracts
- Phase `BeforeHook`/`AfterHook` (optional, type-asserted): precondition
  probes and conclusions on the phase's own bound view; the two-fact
  pattern (record the violation, return the error) keeps `cr.Errors` an
  environment-only channel. `PhaseOutcome` carries `stage` and
  `failing_recorded`.
- `Pipeline.Group` + `Group{ID, Members, Produces, Lifecycle}`: DAG-causal
  scoped lifecycle (setup as a synthetic dependency node, teardown as a
  completion barrier, `cr.Groups` visibility, reserved `group:<id>:setup`/
  `:teardown` evidence attribution).
- `When` guards (optional): conditions over recorded evidence with
  `Run.PriorEvidence` (live scan, transitive-deps-scoped).
- `CaseDependency` (optional): a case DAG — execution follows dependencies
  deterministically, the report keeps declaration order, unmet
  requirements are loud Skipped rows with structural `dependency_failure`.
- Suites as tags: `Tagged` + `SelectByTags` (boolean tag expressions) with
  `ErrNoMatch` — a selector matching nothing refuses rather than running
  nothing green.

### Added — lifecycle & observability
- The unified event stream: a closed `Event` catalog (session, case,
  fixture, group, phase and retry-heartbeat events) via `WithObserver` —
  serialized delivery, read-only payloads, total start/finish pairing,
  redacted at emission, observer panics contained and surfaced on
  `Session.ObserverErrors()` and `Report.Diagnostics`. `WithProgress` and
  `WithCaseObserver` remain as frozen projections of this stream.
- Typed vocabulary: `Stage`, `DeclineSource`, `EvidenceSource`,
  `AttemptsUsed` — closed sets `Verify()` checks, so aggregation never
  parses prose.
- Stage-capability enforcement: one table says what each stage (execution,
  condition, group/fixture setup and teardown, session) may do with a
  `Run`; violations are recorded `FrameworkError`s that fail the owning
  row loudly.
- `Runner.Explain`: the dry run — validates exactly as `Start` would, then
  projects per-case, per-phase dispositions (will-run / declined with its
  structural source / honestly conditional).
- Concurrency: `Config.MaxPhaseConcurrency` (same-DAG-level phases) and
  `Config.MaxCaseConcurrency` (whole cases; `Exclusive()` drains and runs
  alone). Default remains fully sequential; reports stay deterministic.
- Redaction: `Config.RedactKeys`/`Config.RedactPatterns` apply to every
  report AND every emitted event; `Report.Redact`/`Report.RedactMatching`
  for paste-time scrubbing. Pattern redaction walks structured evidence
  (slice elements, map keys/values, entity refs) — a redacted value reads
  `[REDACTED]`, never silently vanishes.
- `MergeReports` for CI sharding: refuses empty input, schema mismatches,
  shards that fail `Verify()`, and duplicate case IDs.
- `Config.MaxObservationsPerCase`: bounded evidence retention with a loud
  truncation marker naming exactly what the cap cost.

### Added — test kit, extensions, examples
- `phasetest`: `Clock`, `SpyRecorder`, `RunFor`, `InvokePhase`,
  `ConformanceCase`/`ConformanceGroup`, `RecordEvents`, `RunAsSubtests`,
  and the two mutation gates `Gutted` and `AlwaysPass`.
- `x/config` (separate module): strict YAML case-manifest and config
  loading — unknown keys, bad durations, empty-reason declines and
  unknown fixtures are load-time refusals naming the exact YAML path.
- `x/comparators` (separate module): `ValueMatch`, `EachEntity`,
  `ContainsAll`, `Unchanged`, `PollCompare`.
- `cmd/snapdiff`: snapshot capture/compare over report JSON.
- `examples/provisioning`: the minimal end-to-end walkthrough.
- `examples/checkout`: the flagship — every shipped feature mapped to the
  test that proves it (see its README's feature table).
- `examples/misuse`: the sabotage twin — 60+ intentional defects, each
  answered by the framework's documented refusal or loud failure.

### Changed / BREAKING (pre-1.0 minor-bump policy)
- Phase IDs (and declared dependencies) may no longer contain `:` — the
  namespace is reserved for group attribution
  (`group_id_reserved_character`). Migration: rename `settle:wait`-style
  IDs (e.g. to `settle_wait`).
- `Settings.Sub` — decoded but never read by the engine — is now refused
  loudly (`settings_sub_removed`). Migration: declare a `Pipeline.Group`.

### Known limits (recorded, not implied away)
- A `WaitUntil` condition that ignores its context is beyond the
  framework's reach (Go cannot preempt); the budget cut-off is
  cooperative. Pinned as a boundary test in `examples/misuse`.
- When a producer phase declines as `NotApplicable`, a dependent reaches
  `Get` and becomes `Errored` with `ErrKeyNotProduced` — loud, but not yet
  a named-pruning reason. Recorded follow-up.
- Fixtures shared across concurrently running cases must be
  scope-partitioned by the consumer (see `Config.MaxCaseConcurrency`
  docs); the examples model this with a lease-counted fixture.
- `go doc` is the authoritative API reference.

## [0.0.0] — scaffold
- Initial repository engineering baseline (CI, CodeQL, govulncheck,
  scorecard, gitleaks, DCO, REUSE).
