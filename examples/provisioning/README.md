# provisioning — a worked `phase` consumer

This package is `phase`'s compatibility canary: it imports `phase` and
`phase/phasetest` through their public API only, exactly as a real consumer
would, and CI fails the build if a change to the library breaks it. There is
no `main` — the package is exercised entirely by its own tests.

## What it shows

A fake provisioning service — a request arrives on a queue, fans out into
per-entity rows in a database, is submitted to an external provider, and
successes are recorded in a settlement ledger — run through a six-phase
`phase` pipeline:

```
submit -> discover -> settle_wait -> settle_checks -> provider_side
                                                     -> ledger
```

* **`fakes.go`** — in-memory fakes for the four systems (queue, store,
  provider control plane, ledger), wired together in `System` so that
  publishing a request synchronously fills in the rest — the example
  demonstrates the framework's contract, not concurrency.
* **`keys.go`** — the typed values phases hand forward: `RequestID`,
  `Items`, `SettledRows`.
* **`phases.go`** — the six `phase.Interface` implementations (five phase
  *types* plus one built with `phase.Func`, to show both styles) and the
  `Pipeline` / `PipelineWithSettleChecks` assembly functions.
* **`case.go`** — this consumer's `Case` type: a request body, per-entity
  expected outcomes, a map of deliberately-skipped phases with reasons, and
  an optional injected provider fault, seeded and cleared by a `Fixture`.
* **`suite_test.go`** — the demonstration itself, as five tests (below).

`settle` is split into two phases — `settle_wait` (waits, produces
`SettledRows`) and `settle_checks` (asserts, produces nothing) — rather than
the single `settle` DESIGN §21 sketches. `phasetest.Gutted` refuses to gut a
phase that produces a handoff key (gutting a producer would starve its
dependents instead of measuring assertion coverage), so a phase whose only
job is asserting has to be separable from the one that waits. See
`TestMutationGateGoesRed`.

## How to run

```sh
go test ./examples/...          # from the repo root
go test ./... -race -v          # from this directory
```

## The five-minute walkthrough: declare, red, green

1. **Declare.** A `Case` is a plain struct: a name, a body, and what each
   entity is expected to become —

   ```go
   red := NewCase("declares-success-but-provider-rejects",
       []ItemExpectation{{EntityID: "r1", State: "succeeded"}},
       "active", nil, "r1", sys.Provider) // fault targets r1
   ```

   The last two arguments are the fixture: `"r1"` tells the case to inject a
   provider fault for entity `r1`, via `Setup`/`Teardown` on a
   `phase.Fixture` (`case.go`'s `seedFault`) — scoped to this run only.

2. **Red.** The case *declares* `r1` will succeed; the injected fault makes
   the fake provider actually reject it. Run it —

   ```go
   session, _ := runner.Start(ctx, []phase.Case{red})
   report := session.Report()
   ```

   — and `report.Cases[0].Status == phase.Failed`,
   `FailedIn == "settle_checks"`, `report.ExitCode() == 1`, and the failing
   `ResultView` carries `Expected: "succeeded"` / `Actual: "failed"`. See
   `TestRedRunIsHonest`.

3. **Green.** Remove the fault (or declare the outcome the fake will
   actually produce) and the same case passes: `Status == phase.Passed`,
   `ExitCode() == 0`. See `TestGreenRun`.

The other two tests round out the guarantees this canary exists to pin:
`TestDisabledPhaseIsVisible` shows an operator-disabled phase reporting
`Disabled` (not silently skipped) and named in `report.NotVerified`;
`TestMutationGateGoesRed` guts `settle_checks` with `phasetest.Gutted` and
shows the case that was Failed above stops being Failed — proof that the
green run in step 3 depended on `settle_checks` actually asserting, not on
a pipeline that would pass regardless; `TestDeterministicReports` runs the
green suite twice and asserts the two `WriteJSON` reports are byte-identical
once the session id and timestamps are stripped.
