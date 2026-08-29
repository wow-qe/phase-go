# Phase Go

[![CI](https://github.com/wow-qe/phase-go/actions/workflows/ci.yaml/badge.svg)](https://github.com/wow-qe/phase-go/actions/workflows/ci.yaml)
[![CodeQL](https://github.com/wow-qe/phase-go/actions/workflows/codeql.yaml/badge.svg)](https://github.com/wow-qe/phase-go/actions/workflows/codeql.yaml)
[![Go Reference](https://pkg.go.dev/badge/github.com/wow-qe/phase-go.svg)](https://pkg.go.dev/github.com/wow-qe/phase-go)
[![License: MIT](https://img.shields.io/badge/License-MIT-blue.svg)](LICENSE)

Phase is a library for phase-wise E2E testing of asynchronous, multi-store
systems: one request fans out across a queue, a database, a service API, an
external provider — and "did it work?" cannot be answered from any single
response. Phase supplies the engine (ordering, applicability, timing, handoff,
results, reporting); you supply the phases, adapters, fixtures and case type.

Its founding rule, enforced in the type system: **a result over zero
comparisons is a failure, never a pass** — because `all([])` is true in every
language, and the resulting defect is a suite that reports green while
checking nothing.

See `examples/checkout` for the flagship all-features consumer (its README
maps every feature to the test that proves it), `examples/provisioning` for
a minimal complete consumer, and `docs/ARCHITECTURE.md` for the
implementation summary. Where prose and an example disagree, **the example
is the reference** — it compiles in CI against every change; prose does
not.

## Requirements

- Go 1.25.8 or newer for the library itself (the floor tracks the oldest
  patch release without known stdlib vulnerabilities reachable from this
  code; CI tests the 1.25 and 1.26 series — newer releases are expected
  to work but are not gated until CI covers them)
- GNU Make (optional, but recommended for consistent local commands)
- For the extended local checks: `golangci-lint`, `govulncheck`, and
  `gitleaks`. These tools analyze Go source and must be **built with (or
  released for) the same Go toolchain you run** — a binary built for an
  older Go fails on newer source trees. Install `govulncheck` with
  `go install golang.org/x/vuln/cmd/govulncheck@latest` so it always
  matches your toolchain, and keep `golangci-lint` on a release that
  supports your Go version (CI pins one; see `.github/workflows/ci.yaml`).

## Quick start

Verify the repository:

```sh
go test ./...
make check
```

## Getting started as a consumer

A suite is four things: **phases** (steps with declared wiring), **cases**
(what runs through those steps), a **runner** (validates and executes), and
a **report** (evidence-derived verdicts). The smallest real assembly:

```go
package quickstart

import (
	"context"
	"testing"
	"time"

	phase "github.com/wow-qe/phase-go"
	"github.com/wow-qe/phase-go/result"
)

// A typed handoff key: one writer, declared wiring, Get fails loudly.
var greeting = phase.Declare[string]("greeting")

// A phase declares its identity and wiring, then does its work on a Run.
type produce struct{}

func (produce) ID() phase.ID            { return "produce" }
func (produce) DependsOn() []phase.ID   { return nil }
func (produce) Produces() []phase.KeyID { return phase.Keys(greeting) }
func (produce) Requires() []phase.KeyID { return nil }
func (produce) AppliesTo(phase.Case, phase.Config) phase.Applicability {
	return phase.Applies()
}
func (produce) Run(_ context.Context, r *phase.Run) error {
	phase.Put(r, greeting, "hello")
	return nil
}

type check struct{}

func (check) ID() phase.ID            { return "check" }
func (check) DependsOn() []phase.ID   { return []phase.ID{"produce"} }
func (check) Produces() []phase.KeyID { return nil }
func (check) Requires() []phase.KeyID { return phase.Keys(greeting) }
func (check) AppliesTo(phase.Case, phase.Config) phase.Applicability {
	return phase.Applies()
}
func (check) Run(_ context.Context, r *phase.Run) error {
	got, err := phase.Get(r, greeting)
	if err != nil {
		return err
	}
	// Evidence, not asserts: the case's verdict derives from what is recorded.
	r.Record(result.Compared("greeting", []bool{got == "hello"}).
		WithExpected("hello").WithActual(got))
	return nil
}

// A case answers the engine's questions about one run-through.
type simpleCase struct{ id string }

func (c simpleCase) ID() string                           { return c.id }
func (simpleCase) Status() phase.CaseStatus               { return phase.Active }
func (simpleCase) Selects(phase.ID) (bool, string)        { return true, "" }
func (simpleCase) Timing(phase.ID) (phase.Timing, bool)   { return phase.Timing{}, false }
func (simpleCase) Fixtures() []phase.Fixture              { return nil }
func (simpleCase) Exclusive() (bool, string)              { return false, "" }

func TestQuickstart(t *testing.T) {
	r, err := phase.NewRunner(
		phase.NewPipeline(produce{}, check{}),
		phase.Config{Defaults: phase.Timing{Attempts: 3, Interval: 100 * time.Millisecond}},
	)
	if err != nil {
		t.Fatal(err) // misdeclared wiring refuses HERE, before anything runs
	}
	s, err := r.Start(context.Background(), []phase.Case{simpleCase{id: "smoke"}})
	if err != nil {
		t.Fatal(err)
	}
	rep := s.Report()
	if rep.ExitCode() != 0 {
		t.Fatalf("summary: %+v", rep.Summary)
	}
}
```

From here: `examples/provisioning` is the small end-to-end walkthrough,
`examples/checkout` maps every feature to the test proving it, and
`examples/misuse` shows how every misuse is refused.

Import path:

```go
import phase "github.com/wow-qe/phase-go"
```

## Repository layout

- Root package `phase` — the engine: one file per concern (`runner.go`,
  `preflight.go`, `run.go`, `keys.go`, `report.go`, `wait.go`, …)
- `result/` — `Result` and its invariant, importable alone
- `phasetest/` — the consumer test kit (Clock, SpyRecorder, RunFor,
  ConformanceCase, Gutted)
- `x/config/` — strict YAML loading (separate module; the core is stdlib-only)
- `cmd/snapdiff/` — report snapshot capture/compare
- `examples/checkout/` — the flagship all-features example; the compatibility canary
- `examples/provisioning/` — a minimal complete consumer
- `examples/misuse/` — the sabotage twin: every misuse answered loudly, by test
- `docs/` — `ARCHITECTURE.md` (implementation summary), `QUALITY.md`, `RELEASING.md`

## Status

The API is pre-stable. Until a `v1.0.0` release, minor versions may include
breaking changes; every such change must be called out in the changelog.

## License

Phase Go is available under the [MIT License](LICENSE).
