// Copyright 2026 The Phase Contributors
// SPDX-License-Identifier: MIT

package phase

import (
	"fmt"
	"io"
)

// ProgressEvent is one landing in the run's visible heartbeat: a 20×15s
// settle is otherwise silent for five minutes — indistinguishable from a
// hang. Stage is "started" or "finished"; Status is meaningful only when
// Stage is "finished". Every phase outcome emits a finished event — a skip
// is a landing too, because a silent skip reads exactly like the hang the
// event exists to rule out.
type ProgressEvent struct {
	CaseID string
	Phase  ID
	Stage  string // "started" | "finished"
	Status Status // the phase's outcome; meaningful only for "finished"
}

// WithProgress installs a progress callback. Under concurrency the event
// may originate on a worker goroutine, but the callback is never entered
// concurrently (the runner serializes every invocation) — a slow callback
// slows the run, it never races it.
func WithProgress(f func(ProgressEvent)) RunnerOption {
	return func(r *Runner) { r.progress = f }
}

// LogProgress is the default human-readable sink: one line per event to w
// (stderr, typically).
//
//	phase submit started            [case checkout-declined]
//	phase submit finished: passed   [case checkout-declined]
func LogProgress(w io.Writer) func(ProgressEvent) {
	return func(ev ProgressEvent) {
		// A log sink is best-effort by definition: a full stderr must not
		// alter outcomes, so write errors are deliberately dropped.
		if ev.Stage == "started" {
			_, _ = fmt.Fprintf(w, "phase %s started            [case %s]\n", ev.Phase, ev.CaseID)
			return
		}
		_, _ = fmt.Fprintf(w, "phase %s finished: %s   [case %s]\n", ev.Phase, ev.Status, ev.CaseID)
	}
}
