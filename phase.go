// Copyright 2026 The Phase Contributors
// SPDX-License-Identifier: MIT

package phase

import "github.com/wow-qe/phase-go/result"

// ID identifies a phase. Stable across runs — it appears in configuration,
// in dependency declarations, and in every report.
type ID string

// KeyID identifies a handoff key without its type, for Produces/Requires
// declarations. Typed access goes through Key[T]; see keys.go.
type KeyID string

// EntityRef identifies one of the entities a request fans out into. It is
// defined in package result (which imports only the standard library, so
// comparators can depend on it alone) and re-exported here as the same type.
type EntityRef = result.EntityRef

// Ref is shorthand for an EntityRef of the default kind.
func Ref(id string) EntityRef { return EntityRef{Kind: "entity", ID: id} }
