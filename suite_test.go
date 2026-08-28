// Copyright 2026 The Phase Contributors
// SPDX-License-Identifier: MIT

package phase

import (
	"errors"
	"strings"
	"testing"
)

// A Suite is a tag selector, not a type. One case
// belongs to many suites by carrying many tags; selection happens before
// Start and touches no engine surface.

type taggedCase struct {
	stubCase
	tags []string
}

func (c *taggedCase) Tags() []string { return c.tags }

func suiteCases() []Case {
	return []Case{
		&taggedCase{stubCase: stubCase{id: "checkout"}, tags: []string{"smoke", "payments"}},
		&taggedCase{stubCase: stubCase{id: "refund"}, tags: []string{"payments", "slow"}},
		&taggedCase{stubCase: stubCase{id: "audit"}, tags: []string{"regression"}},
		&stubCase{id: "untagged"},
	}
}

func ids(cs []Case) string {
	out := make([]string, len(cs))
	for i, c := range cs {
		out[i] = c.ID()
	}
	return strings.Join(out, ",")
}

func TestSelectByTagsExpressions(t *testing.T) {
	cases := map[string]string{
		"smoke":                       "checkout",
		"payments":                    "checkout,refund",
		"payments && !slow":           "checkout",
		"smoke || regression":         "checkout,audit",
		"(smoke || slow) && payments": "checkout,refund",
		"!payments":                   "audit,untagged", // an untagged case matches pure negation
	}
	for expr, want := range cases {
		t.Run(expr, func(t *testing.T) {
			got, err := SelectByTags(suiteCases(), expr)
			if err != nil {
				t.Fatalf("SelectByTags(%q): %v", expr, err)
			}
			if ids(got) != want {
				t.Fatalf("%q selected %q, want %q (declaration order preserved)", expr, ids(got), want)
			}
		})
	}
}

func TestZeroMatchIsATypedRefusal(t *testing.T) {
	_, err := SelectByTags(suiteCases(), "smoke && slow")
	if !errors.Is(err, ErrNoMatch) {
		t.Fatalf("err = %v, want ErrNoMatch — a selector matching nothing must never become a green empty run", err)
	}
}

func TestMalformedExpressionIsRefused(t *testing.T) {
	for _, expr := range []string{"", "&& smoke", "smoke &&", "(smoke", "smoke ! fast", "smoke & fast"} {
		if _, err := SelectByTags(suiteCases(), expr); err == nil || errors.Is(err, ErrNoMatch) {
			t.Fatalf("expr %q must be refused as malformed, got %v", expr, err)
		}
	}
}

func TestOneCaseBelongsToManySuites(t *testing.T) {
	smoke, err1 := SelectByTags(suiteCases(), "smoke")
	pay, err2 := SelectByTags(suiteCases(), "payments")
	if err1 != nil || err2 != nil {
		t.Fatal(err1, err2)
	}
	if !strings.Contains(ids(smoke), "checkout") || !strings.Contains(ids(pay), "checkout") {
		t.Fatal("one case, many suites: checkout must appear in both selections")
	}
}
