# examples/checkout — the flagship, all-features example

One order-checkout flow — submit → authorize → settlement (grouped) →
ledger → refund audit → audit — exercising **every feature the library
ships**, deterministic and offline. This package is the compatibility
canary: if a change to `phase` breaks it, the change is breaking by
definition.

Run it:

```sh
cd examples/checkout
go test -race ./...
```

## Layout

| File | What it holds |
|---|---|
| `system.go` | The fake system under test: eventually-consistent settlement, a one-read ledger flap, a subscription leak detector. |
| `keys.go` | The typed handoff keys (`Declare`). |
| `phases.go` | All seven phases, the settlement group and its lifecycle, the catalog fixture, `buildPipeline`. |
| `case.go` | The consumer case factory over the YAML manifest, plus the one case dependency. |
| `cases.yaml` | The suite as data: tags, declines, timing overrides, quarantine, exclusivity. |
| `suite_test.go` | The proof: dry run, green run under observers, honest red run, mutation gates, sharding, isolated phase unit test. |

## Feature map

Every capability, where it is used, and the test that proves it.

| Feature | Used in | Proven by |
|---|---|---|
| Pipeline & phase DAG (`DependsOn`) | `phases.go` (`buildPipeline`) | every test |
| Typed handoff (`Declare`/`Put`/`Get`, one writer per key) | `keys.go`, `phases.go` | every green run |
| Evidence: `Record`, `Observe`, `Transcribe` | `phases.go` (submit) | `TestSmokeSuiteGreenUnderObservers` |
| `BeforeHook` (precondition probe, two-fact violation) | `phases.go` (submit) | `TestSubmitPhaseInIsolation` |
| `AfterHook` (phase tally) | `phases.go` (authorize) | `TestSmokeSuiteGreenUnderObservers` |
| Groups: members, `Group.Produces`, lifecycle setup/teardown | `phases.go` (settlement / `processorStream`) | smoke (events), leak checks in every run |
| `When` gate over `PriorEvidence` (never live state) | `phases.go` (refund_audit) | `TestExplainPredictsTheRun` (conditional), regression (declined reason) |
| `WaitUntil` (attempt-budgeted settle) | `phases.go` (settle_wait) | smoke (`RetryAttempt` heartbeats), `attempts_used` rows |
| `Tolerate` (declared flake → loud `Flaked`) | `phases.go` (settle_checks) | `TestFullRegressionSequential` |
| Comparators — all five (`ValueMatch`, `EachEntity`, `ContainsAll`, `Unchanged`, `PollCompare`) | `phases.go` (authorize, settle_checks, ledger, audit) | every green run |
| Fixtures via registry + YAML manifest (`x/config`) | `case.go`, `cases.yaml` | every test (`loadCases`) |
| Suites as tags: `SelectByTags`, `ErrNoMatch` | `cases.yaml` tags | `TestSmokeSuiteGreenUnderObservers`, `TestSelectorMatchingNothingRefuses` |
| Case dependencies (`Acceptable`, `DependencyFailure`) | `case.go` (billing-report) | regression (Flaked accepted), `TestUnmetDependencySkipsLoudly`, `TestDependencyOnAbsentCaseIsRefused` |
| Per-case timing override | `cases.yaml` (happy-multi) | `TestExplainPredictsTheRun` |
| Declines with mandatory reasons | `cases.yaml` (declined-payment) | regression (`DeclinedByCase` rows) |
| Case statuses: `quarantined`, `blocked`, `draft` | `cases.yaml` (parked-experiment, blocked-refunds, draft-loyalty) | regression (reasoned skips) |
| Exclusive cases | `cases.yaml` (reconcile) | `TestConcurrentRunKeepsVerdicts` |
| Concurrency knobs (`MaxCaseConcurrency`, `MaxPhaseConcurrency`) | config | `TestConcurrentRunKeepsVerdicts` under `-race` |
| `Explain` (dry run, three-way dispositions) | — | `TestExplainPredictsTheRun` |
| Sanctioned adapter calls: `phase.Require` | `phases.go` (submit) | every run through submit |
| Unified event stream (`WithObserver`, total pairing, heartbeats) | — | `TestSmokeSuiteGreenUnderObservers` |
| Legacy projections: `WithProgress`, `WithCaseObserver` | — | `TestSmokeSuiteGreenUnderObservers` |
| Operator kill-switch: `Config.Phases[id].Enabled` → `Disabled` + `NotVerified` | — | `TestOperatorKillSwitchIsLoudCoverageLoss` |
| Redaction floor (`RedactPatterns`, report **and** emission) | config | `TestSmokeSuiteGreenUnderObservers` |
| Paste-time redaction: `Report.Redact`, `RedactMatching` (deep, structured evidence included) | — | `TestShardedRunsMergeIntoOneReport` |
| Evidence retention cap (`MaxObservationsPerCase`, loud marker) | config | `TestObservationCapIsLoud` |
| Report: `Verify`, `ExitCode`, `WriteJSON` | — | regression, smoke |
| Sharding: `MergeReports` | — | `TestShardedRunsMergeIntoOneReport` |
| `go test` bridge: `phasetest.RunAsSubtests` | — | `TestSmokeSuiteGreenUnderObservers` |
| Mutation gates: `phasetest.Gutted`, `phasetest.AlwaysPass` | — | `TestMutationGateGutted`, `TestMutationGateAlwaysPass` |
| Isolated phase unit test: `phasetest.RunFor` + `InvokePhase` | — | `TestSubmitPhaseInIsolation` |
| Declaration conformance: `phasetest.ConformanceCase` / `ConformanceGroup` | — | `TestDeclarationsConform` |
| Typed vocabulary (`Stage`, `DeclineSource`, `AttemptsUsed`) | — | regression, `TestSubmitPhaseInIsolation` |
| Errored ≠ Failed, statuses derived from evidence | — | regression (`Verify` on every report) |

## The shape of the flow

```
submit ──▶ authorize ──▶ [ settlement group ────────────┐
                          settle_wait ──▶ settle_checks │ Lifecycle:
                          (produces      (EachEntity +  │ subscribe /
                           settled set)   Tolerate)     │ unsubscribe
                         ────────────────────────────────┘
           ──▶ ledger ──▶ refund_audit (When-gated) 
                      └─▶ audit
```

The group's lifecycle `Setup` subscribes to the processor stream and
`Put`s `StreamCursor` (declared in `Group.Produces`); `Teardown`
unsubscribes on every path — the tests assert `ActiveSubscriptions() == 0`
after green, red, and concurrent runs alike.
