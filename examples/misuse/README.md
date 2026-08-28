# examples/misuse — the sabotage twin

The same checkout flow as `examples/checkout`, deliberately fed **wrong
data and wrong declarations, one defect at a time** — and every defect is
answered by the framework's documented response. This package is the
misuse canary: if any seeded defect here stops being answered loudly, a
silent-failure bug has entered the framework.

The catalog was assembled from three independent angles (product-gap,
silent-failure, engine-contract) and each entry pins the response **structurally**
— LoadError codes, Verify invariants, typed statuses and decline sources,
error sentinels — never message prose where a typed fact exists.

Run it:

```sh
cd examples/misuse
go test -race ./...
```

## What the hunt found

Built explicitly as an adversarial hunt against the framework. Outcome:

- **No new framework defect.** Every seeded wrongness — 60+ entries across
  six batteries — was answered per contract.
- **Two suspected defect classes ruled out by construction**: a group-teardown error
  can NOT coexist with a `Passed` case (`TestGroupTeardownErrorCannotCoexistWithPassed`),
  and a contained capability violation leaves a trustworthy report — exit 1
  with the violation on the record, not a spurious exit 3
  (`TestContainedViolationKeepsTheReportTrustworthy` pins the taxonomy ruling).
- **One honest limit pinned as a boundary test**: a `WaitUntil` cond that
  swallows its own context deadline and lies "done" is beyond any
  framework's reach (Go cannot preempt); `TestCondSwallowingItsDeadlineIsBeyondTheFramework`
  exists so that boundary is a tested fact, not folklore.
- (The sibling flagship example DID catch a real bug while being built —
  deep-structure pattern redaction — proving the canary method works.)

## The batteries

| File | Angle | Highlights |
|---|---|---|
| `refusals_test.go` | Construction/preflight refusals | every `LoadCode` incl. the engine's top-ranked untested seams: non-member requiring a group key (and its permitted counterpart), phase-vs-group producer collision, nil-case-beats-dependency-cycle ordering, `:`-namespace, manifest strictness, fixture-typo alternatives |
| `data_test.go` | Wrong system data through the correct pipeline | corrupt auth code (diff with both values), never-settling system (budget NAMED, `ErrBudgetExhausted` sentinel, dependents `DeclinedByDependency`, teardown still runs), lost ledger row (missing entity named), unsettled entity (per-entity rows never silently short; When-gated refund audit reacts), permanent flap (Failed, never Flaked, full tolerance trail), panicking store, mid-audit mutation, environment-vs-product Before split |
| `composition_test.go` | The seams between subsystems | When writing in both directions (violation beats the condition's own answer), teardown `Put` failing the group's OWN row, fixture-teardown denial, undeclared/double `Put`, Before recording passing evidence then erroring, cancellation mid-settle (`Errored` never `Failed`, `Curtailed`, no fabricated results, no leaked subscription), the cond-that-lies boundary |
| `discipline_test.go` | Founding-rule direct hits | zero-comparison result, silent phase (loud 0), all-silent case cannot pass, unjustified/unbounded `Tolerate` refuse BEFORE evaluating (`checkCalls==0`), buzzer-pass = `Flaked` with full trail, the three budget-error shapes discriminated (`gave up after N×`, `timeout after ... attempt(s)`, `timeout during attempt`), out-of-scope `PriorEvidence` refused with the cure named |
| `tamper_test.go` | Hand-corrupted reports vs `Verify()` | 15 corruptions, each caught by its exact `FrameworkError.Invariant`, each mapping to exit 3 |
| `events_test.go` | The stream under chaos | serialized delivery under both concurrency knobs, total pairing per case×phase on a red day, tolerance heartbeats, emission redaction while everything is on fire, panicking observer contained per-observer (the second observer stays subscribed; `ObserverErrors` + `Diagnostics` surfaced) |
| `containment_test.go` | Panic containment site-by-site | Run / Before / After (fold rules: flips only Passed rows, never masks the original cause) / When / fixture setup (predecessors still torn down) / group setup (members `DeclinedByGroupSetup`) — every one with a bystander case whose verdict must be untouched |
| `merge_test.go` | Merge refusals | zero shards, schema mismatch, tampered shard (the SAME standalone `Verify` invariant surfaces through the merge — no reimplemented, weakened check), duplicate case IDs named |

## Sabotage knobs (`system.go`)

`corruptAuthCode` · `neverSettle` · `dropLedgerRow` · `wrongEntityState` ·
`permanentLedgerFlap` · `panicInSubmit` · `mutateDuringAudit` · `healthy=false`
— each seeds exactly one kind of wrong data, so each test reads as
"one lie in, one documented answer out."
