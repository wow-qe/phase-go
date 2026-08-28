// Copyright 2026 The Phase Contributors
// SPDX-License-Identifier: MIT

// Package phase is a library for phase-wise end-to-end testing: it runs
// CASES through an ordered pipeline of PHASES against a real (or faked)
// system, collects evidence, and derives verdicts from that evidence —
// never from control flow.
//
// Its founding rule, enforced in the type system: a result over zero
// comparisons is a failure, never a pass ([result.Compared] refuses the
// empty set), because all([]) is true in every language and the resulting
// defect is a suite that reports green while checking nothing. The same
// posture runs through everything else: every skip carries a reason, an
// interrupted run is Errored rather than Failed, and [Report.Verify]
// re-checks the report's own consistency before you trust it.
//
// The core objects:
//
//   - [Interface] — one phase: an ID, declared wiring (DependsOn,
//     Produces, Requires), an applicability gate, and a Run method.
//     Optional contracts add a Before/After hook pair ([BeforeHook],
//     [AfterHook]) and an evidence-gated condition ([When]).
//   - [Pipeline] — the ordered set of phases, with [Group] scoping
//     members under a shared setup/teardown lifecycle.
//   - [Case] — one journey through the pipeline: identity, status,
//     per-phase selection with mandatory reasons, timing overrides,
//     fixtures and exclusivity. Optional contracts add suite tags
//     ([Tagged]) and case-level dependencies ([CaseDependency]).
//   - [Runner] — validates everything up front ([Runner.Preflight],
//     surfacing every misdeclaration as a typed [LoadError]), predicts
//     without executing ([Runner.Explain]), and executes
//     ([Runner.Start]) with optional bounded concurrency.
//   - [Run] — the handle a phase works through: typed key handoff
//     ([Declare], [Put], [Get]), evidence ([Run.Record], [Run.Observe],
//     [Run.Transcribe]), waiting ([WaitUntil]) and declared-flaky
//     tolerance ([Tolerate]).
//   - [Report] — per-case rows with attributed evidence, verified by
//     [Report.Verify], merged across CI shards by [MergeReports], and
//     redacted via [Config.RedactKeys], [Config.RedactPatterns],
//     [Report.Redact] and [Report.RedactMatching].
//   - [Event] — the unified, read-only, serialized observation stream
//     ([WithObserver]), redacted at emission.
//
// Start with the README's quick-start example, then examples/provisioning
// (a small end-to-end walkthrough), examples/checkout (every feature
// mapped to the test proving it) and examples/misuse (every misuse
// answered by a documented refusal). The companion phasetest package
// holds the consumer test kit; x/config loads case manifests from YAML;
// x/comparators ships common comparison helpers.
//
// The API is pre-stable: until v1.0.0, minor versions may include
// breaking changes, each recorded in CHANGELOG.md.
package phase
