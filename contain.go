// Copyright 2026 The Phase Contributors
// SPDX-License-Identifier: MIT

package phase

import "fmt"

// contain is the one containment primitive: every entry into consumer code
// — phases, hooks, conditions, fixture and group lifecycles, observer
// callbacks — runs under it, so a consumer bug becomes that case's
// evidence, never the batch's crash.
func contain(what string, f func() error) (err error) {
	defer func() {
		if v := recover(); v != nil {
			err = fmt.Errorf("panic in %s: %v", what, v)
		}
	}()
	return f()
}
