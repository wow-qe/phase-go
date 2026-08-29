// Copyright 2026 The Phase Contributors
// SPDX-License-Identifier: MIT

package config

import "testing"

// FuzzParseCases: arbitrary manifest bytes must produce a typed error or a
// valid spec list — never a panic — and the strict-parsing contract means
// no input is silently half-accepted.
func FuzzParseCases(f *testing.F) {
	f.Add([]byte("cases:\n  - id: a\n    tags: [x]\n"))
	f.Add([]byte("cases: []\n"))
	f.Add([]byte("cases:\n  - id: a\n    declines:\n      p: \"\"\n"))
	f.Add([]byte("&a [*a]\n"))
	f.Add([]byte("cases:\n  - id: a\n    timing:\n      p: {attempts: -1}\n"))
	f.Fuzz(func(t *testing.T, data []byte) {
		specs, err := ParseCases(data)
		if err != nil {
			return
		}
		for _, sp := range specs {
			if sp.ID == "" {
				t.Fatal("accepted a case with an empty id")
			}
		}
	})
}
