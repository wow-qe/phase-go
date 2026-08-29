// Copyright 2026 The Phase Contributors
// SPDX-License-Identifier: MIT

package main

import (
	"go/parser"
	"go/token"
	"strings"
	"testing"
)

func scan(t *testing.T, src string) []finding {
	t.Helper()
	fs := token.NewFileSet()
	af, err := parser.ParseFile(fs, "probe.go", src, parser.ParseComments)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	allowed := 0
	return scanComments(fs, af, &allowed)
}

func TestPatternClassesAreDetected(t *testing.T) {
	for name, src := range map[string]string{
		"process-actor": "package p\n// the review gate decided this\nvar X int\n",
		"finding-code":  "package p\n// B6a: topological rank\nvar X int\n",
		"history":       "package p\n// this used to be two booleans\nvar X int\n",
		"rhetoric":      "package p\n// refuse it loudly\nvar X int\n",
		"ai-term":       "package p\n// generated with Claude\nvar X int\n",
		"stale-claim":   "package p\n// sharding is the only wall-clock lever\nvar X int\n",
	} {
		if got := scan(t, src); len(got) == 0 {
			t.Errorf("%s: no finding for %q", name, src)
		}
	}
}

func TestCleanTechnicalCommentsPass(t *testing.T) {
	src := "package p\n// Teardown panics are contained to the current case and recorded\n// as errors; remaining cases continue to execute.\nvar X int\n"
	if got := scan(t, src); len(got) != 0 {
		t.Fatalf("clean comment flagged: %+v", got)
	}
}

func TestStringLiteralsAreNeverScanned(t *testing.T) {
	src := "package p\nvar X = \"the review gate said this loudly, per Claude\"\n"
	if got := scan(t, src); len(got) != 0 {
		t.Fatalf("string literal flagged: %+v", got)
	}
}

func TestAllowRequiresStandaloneDirective(t *testing.T) {
	prose := "package p\n// the commentcheck:allow marker suppresses findings loudly\nvar X int\n"
	if got := scan(t, prose); len(got) == 0 {
		t.Fatal("prose mentioning the marker must not suppress scanning")
	}
	directive := "package p\n// refuse it loudly\n//commentcheck:allow\nvar X int\n"
	if got := scan(t, directive); len(got) != 0 {
		t.Fatalf("standalone directive must suppress the group: %+v", got)
	}
}

func TestDocLinksResolvePerDirectoryNotPerPackageName(t *testing.T) {
	// Two distinct main packages: a symbol declared in one must not satisfy
	// a doc link in the other.
	fsA := token.NewFileSet()
	afA, _ := parser.ParseFile(fsA, "a/main.go", "// Command a uses [Widget].\npackage main\nfunc main() {}\n", parser.ParseComments)
	fsB := token.NewFileSet()
	afB, _ := parser.ParseFile(fsB, "b/main.go", "package main\ntype Widget struct{}\nfunc main() {}\n", parser.ParseComments)

	decls := map[string]map[string]bool{}
	collectDecls(afA, decls, "a:main")
	collectDecls(afB, decls, "b:main")
	refs := collectDocRefs(fsA, afA, "a:main")
	if len(refs) != 1 {
		t.Fatalf("refs = %+v, want the [Widget] link", refs)
	}
	if decls[refs[0].pkg][refs[0].name] {
		t.Fatal("a Widget declared in package b must not satisfy a link in package a")
	}
	if !decls["b:main"]["Widget"] {
		t.Fatal("Widget must be recorded under its own directory key")
	}
}

func TestFactAccompaniesEveryStaleClaim(t *testing.T) {
	for _, sc := range staleClaims {
		if strings.TrimSpace(sc.fact) == "" {
			t.Fatalf("stale claim %v has no corrective fact", sc.re)
		}
	}
}
