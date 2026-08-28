// Copyright 2026 The Phase Contributors
// SPDX-License-Identifier: MIT

package phase

import "fmt"

// contain is the ONE containment primitive: every entry
// into consumer code - phases, hooks, conditions, fixture and group
// lifecycles, observer callbacks - runs under it, so a consumer bug is
// that case's evidence, never the batch's crash. The six historical copies
// of this recover pattern collapse here; grep for contain( to enumerate
// every consumer entry point.
func contain(what string, f func() error) (err error) {
	defer func() {
		if v := recover(); v != nil {
			err = fmt.Errorf("panic in %s: %v", what, v)
		}
	}()
	return f()
}
