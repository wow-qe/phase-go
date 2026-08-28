// Copyright 2026 The Phase Contributors
// SPDX-License-Identifier: MIT

package phase

import (
	"fmt"
	"strings"
	"time"
)

// MergeReports combines shard reports into one: sharding across CI jobs
// is the only wall-clock lever while execution is sequential, and the merged
// report must be exactly as trustworthy as its inputs. So the merge REFUSES
// anything that would make the combination lie:
//
//   - zero inputs (a merge of nothing must not fabricate an empty green
//     report);
//   - a schema mismatch (two vocabularies, one document);
//   - a shard that fails Verify (corruption must not blend in);
//   - duplicate case IDs across shards (indistinguishable rows — the same
//     rule Preflight enforces within one run).
//
// Cases keep shard order; NotVerified lines are unioned without duplicates;
// the merged session is named after its shards and spans their extremes.
// The result is Verified before it is returned.
func MergeReports(reports ...*Report) (*Report, error) {
	if len(reports) == 0 {
		return nil, fmt.Errorf("merge: no reports given — refusing to fabricate an empty report")
	}
	for i, r := range reports {
		if r.Schema != reports[0].Schema {
			return nil, fmt.Errorf("merge: shard %d carries schema %q, shard 0 carries %q", i, r.Schema, reports[0].Schema)
		}
		if err := r.Verify(); err != nil {
			return nil, fmt.Errorf("merge: shard %d (session %s) does not verify: %w", i, r.Session.ID, err)
		}
	}

	merged := &Report{Schema: reports[0].Schema, NotVerified: []string{}}
	var ids []string
	started, finished := time.Time{}, time.Time{}
	seenCase := map[string]string{} // case id -> owning session
	seenNV := map[string]bool{}
	for _, r := range reports {
		ids = append(ids, r.Session.ID)
		if started.IsZero() || r.Session.Started.Before(started) {
			started = r.Session.Started
		}
		if r.Session.Finished.After(finished) {
			finished = r.Session.Finished
		}
		for _, cr := range r.Cases {
			if owner, dup := seenCase[cr.CaseID]; dup {
				return nil, fmt.Errorf("merge: case %q appears in sessions %s and %s — the rows would be indistinguishable",
					cr.CaseID, owner, r.Session.ID)
			}
			seenCase[cr.CaseID] = r.Session.ID
			merged.Cases = append(merged.Cases, cr.clone())
			merged.Summary.Total++
			switch cr.Status {
			case Passed:
				merged.Summary.Passed++
			case Failed:
				merged.Summary.Failed++
			case Skipped:
				merged.Summary.Skipped++
			case NotApplicable:
				merged.Summary.NotApplicable++
			case Disabled:
				merged.Summary.Disabled++
			case Errored:
				merged.Summary.Errored++
			case Flaked:
				merged.Summary.Flaked++
			}
		}
		for _, line := range r.NotVerified {
			if !seenNV[line] {
				seenNV[line] = true
				merged.NotVerified = append(merged.NotVerified, line)
			}
		}
	}
	merged.Session = SessionInfo{ID: "merge(" + strings.Join(ids, "+") + ")", Started: started, Finished: finished}
	if err := merged.Verify(); err != nil {
		return nil, fmt.Errorf("merge: combined report does not verify — this is a bug in MergeReports: %w", err)
	}
	return merged, nil
}
