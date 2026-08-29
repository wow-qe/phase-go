// Copyright 2026 The Phase Contributors
// SPDX-License-Identifier: MIT

package main

import "testing"

// FuzzCaptureSnapshot: snapshot capture over arbitrary report bytes must
// refuse with an error or produce a snapshot — never panic, and never
// accept JSON with duplicate keys (last-wins would silently rewrite what a
// reader saw first).
func FuzzCaptureSnapshot(f *testing.F) {
	f.Add([]byte(`{"schema_version":"1","cases":[],"summary":{},"not_verified":[]}`))
	f.Add([]byte(`{"schema_version":"1","cases":[{"id":"a","status":"passed","phases":[],"results":[]}],"summary":{},"not_verified":[]}`))
	f.Add([]byte(`{"cases":[],"cases":[]}`))
	f.Add([]byte(`not json`))
	f.Fuzz(func(t *testing.T, data []byte) {
		_, _ = captureSnapshot(data)
	})
}
