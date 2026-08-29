// Copyright 2026 The Phase Contributors
// SPDX-License-Identifier: MIT

package main

import (
	"os"
	"path/filepath"
	"testing"
)

// The gate must discriminate: a compliant fixture passes, a violating
// fixture is caught, and the stdlib heuristic neither blocks the standard
// library nor waves through look-alike module paths.

func writeFixture(t *testing.T, dir, name, src string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestCompliantFixturePasses(t *testing.T) {
	root := t.TempDir()
	writeFixture(t, filepath.Join(root, "engine"), "a.go",
		"package engine\n\nimport (\n\t\"fmt\"\n\n\t\"example.com/mod/result\"\n)\n\nvar _ = fmt.Sprint(result.X)\n")
	got := checkAll(root, map[string][]string{"engine": {"example.com/mod/result"}})
	if len(got) != 0 {
		t.Fatalf("compliant fixture flagged: %v", got)
	}
}

func TestViolatingFixtureIsCaught(t *testing.T) {
	root := t.TempDir()
	writeFixture(t, filepath.Join(root, "result"), "a.go",
		"package result\n\nimport \"example.com/mod/engine\"\n\nvar _ = engine.X\n")
	got := checkAll(root, map[string][]string{"result": {}})
	if len(got) != 1 {
		t.Fatalf("upward import not caught: %v", got)
	}
}

func TestStdlibAlwaysAllowedAndLookalikesAreNot(t *testing.T) {
	if !importAllowed("encoding/json", nil) {
		t.Fatal("stdlib blocked")
	}
	if importAllowed("evil.example.com/encoding/json", nil) {
		t.Fatal("dotted module path treated as stdlib")
	}
	if !importAllowed("example.com/mod/result/sub", []string{"example.com/mod/result"}) {
		t.Fatal("subdirectory of an allowed prefix blocked")
	}
	if importAllowed("example.com/mod/resultx", []string{"example.com/mod/result"}) {
		t.Fatal("prefix match leaked past the path boundary")
	}
}

func TestRepositoryRulesHoldOnTheRealTree(t *testing.T) {
	// The live gate over the actual repository: run from the repo root two
	// levels up from this package.
	if got := checkAll("../..", rules); len(got) != 0 {
		t.Fatalf("boundary violations in the repository: %v", got)
	}
}
