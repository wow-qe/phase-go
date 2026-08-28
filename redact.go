// Copyright 2026 The Phase Contributors
// SPDX-License-Identifier: MIT

package phase

import (
	"encoding/json"
	"regexp"
	"strings"
)

// The QE workflow includes pasting reports into tickets, and evidence rides
// three carriers a paste exposes: Observation values, Result
// Expected/Actual, and raw adapter error strings (DSNs, tokens).
// Redaction is therefore in-place, total across those carriers, and VISIBLE:
// a redacted value reads "[REDACTED]", never silently vanishes, so the
// reader knows evidence was withheld rather than absent.

const redacted = "[REDACTED]"

// Redact replaces, at any depth, the value under any key whose name matches
// one of the given names (case-insensitive) in every Observation value and
// every Result Expected/Actual. A value that cannot be inspected (its JSON
// marshalling fails) cannot be verified clean, so it is replaced whole —
// the safe failure.
//
// Config.RedactKeys applies this automatically at Report(); this method
// exists for the names only known at paste time.
func (r *Report) Redact(keys ...string) {
	if len(keys) == 0 {
		return
	}
	match := make(map[string]bool, len(keys))
	for _, k := range keys {
		match[strings.ToLower(k)] = true
	}
	for ci := range r.Cases {
		redactCaseKeys(&r.Cases[ci], match)
	}
}

// redactCaseKeys is the per-case half of Redact — shared by the report
// build and by event emission (the live stream is redacted too).
func redactCaseKeys(cr *CaseReport, match map[string]bool) {
	for oi := range cr.Observations {
		cr.Observations[oi].Value = redactValue(cr.Observations[oi].Value, match)
	}
	for ri := range cr.Results {
		v := &cr.Results[ri].Result
		if v.Expected != nil {
			v.Expected = redactValue(v.Expected, match)
		}
		if v.Actual != nil {
			v.Actual = redactValue(v.Actual, match)
		}
	}
}

// RedactMatching replaces every match of re with "[REDACTED]" in the
// string-shaped evidence key-based redaction cannot reach: error strings,
// result reasons, case reasons, and string observation values. Use it for
// secrets that ride free text — DSNs in connection errors, bearer tokens in
// quoted headers.
func (r *Report) RedactMatching(re *regexp.Regexp) {
	if re == nil {
		return
	}
	for ci := range r.Cases {
		redactCasePattern(&r.Cases[ci], re)
	}
}

// redactCasePattern is the per-case half of RedactMatching.
func redactCasePattern(cr *CaseReport, re *regexp.Regexp) {
	scrub := func(s string) string { return re.ReplaceAllString(s, redacted) }
	{
		cr.Reason = scrub(cr.Reason)
		// land() copies the raw adapter error into each
		// PhaseOutcome.Reason — the same string scrubbed in Errors survived
		// here, duplicated, until this loop existed.
		for pi := range cr.Phases {
			cr.Phases[pi].Reason = scrub(cr.Phases[pi].Reason)
		}
		for gi := range cr.Groups {
			cr.Groups[gi].Reason = scrub(cr.Groups[gi].Reason) // the fourth Reason-shaped carrier
		}
		for ei := range cr.Errors {
			cr.Errors[ei].Err = scrub(cr.Errors[ei].Err)
		}
		for ri := range cr.Results {
			v := &cr.Results[ri].Result
			v.Reason = scrub(v.Reason)
			// The entity is a string carrier too: EachEntity puts the system's
			// own identifiers (possibly tokened URLs) right here.
			v.Entity.Kind = scrub(v.Entity.Kind)
			v.Entity.ID = scrub(v.Entity.ID)
			// Free text
			// is free text at ANY depth. A bare-string Expected/Actual leaked
			// first; then the flagship example showed a secret riding inside a
			// slice element or map key/value survived the bare-string check.
			// scrubDeep walks the whole value.
			if v.Expected != nil {
				v.Expected = scrubDeep(v.Expected, scrub)
			}
			if v.Actual != nil {
				v.Actual = scrubDeep(v.Actual, scrub)
			}
		}
		for oi := range cr.Observations {
			cr.Observations[oi].Name = scrub(cr.Observations[oi].Name) // Transcribe puts the op (a URL, possibly tokened) in the name
			if cr.Observations[oi].Value != nil {
				cr.Observations[oi].Value = scrubDeep(cr.Observations[oi].Value, scrub)
			}
		}
	}
}

// scrubDeep applies scrub to every string v contains — the value itself,
// slice elements, map keys and values — normalising non-string values
// through JSON exactly as redactValue does, so both redaction mechanisms
// see the same carrier surface. Unmarshallable values are replaced whole:
// what cannot be inspected cannot be verified clean.
func scrubDeep(v any, scrub func(string) string) any {
	if s, ok := v.(string); ok {
		return scrub(s) // fast path; also keeps the value's type
	}
	raw, err := json.Marshal(v)
	if err != nil {
		return redacted + " (value could not be inspected)"
	}
	var doc any
	if err := json.Unmarshal(raw, &doc); err != nil {
		return redacted + " (value could not be inspected)"
	}
	return scrubDoc(doc, scrub)
}

func scrubDoc(doc any, scrub func(string) string) any {
	switch d := doc.(type) {
	case string:
		return scrub(d)
	case map[string]any:
		// Keys may change: rebuild. Known accepted edge: two distinct
		// keys scrubbing to the same string merge to one entry — a fidelity
		// loss, never a leak, since both values are scrubbed before landing.
		out := make(map[string]any, len(d))
		for k, v := range d {
			out[scrub(k)] = scrubDoc(v, scrub)
		}
		return out
	case []any:
		for i, v := range d {
			d[i] = scrubDoc(v, scrub)
		}
		return d
	default:
		return doc
	}
}

// redactValue normalises v through JSON so arbitrary consumer values (maps,
// structs, slices) walk uniformly, then replaces the value under every
// matching key. Unmarshallable values are replaced whole.
func redactValue(v any, match map[string]bool) any {
	raw, err := json.Marshal(v)
	if err != nil {
		return redacted + " (value could not be inspected)"
	}
	var doc any
	if err := json.Unmarshal(raw, &doc); err != nil {
		return redacted + " (value could not be inspected)"
	}
	return redactDoc(doc, match)
}

func redactDoc(doc any, match map[string]bool) any {
	switch d := doc.(type) {
	case map[string]any:
		for k, v := range d {
			if match[strings.ToLower(k)] {
				d[k] = redacted
			} else {
				d[k] = redactDoc(v, match)
			}
		}
		return d
	case []any:
		for i, v := range d {
			d[i] = redactDoc(v, match)
		}
		return d
	default:
		return doc
	}
}
