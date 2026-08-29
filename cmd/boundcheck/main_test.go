// Copyright 2026 The Phase Contributors
// SPDX-License-Identifier: MIT

package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The gate must discriminate: compliant trees pass; upward imports,
// cross-extension imports, unlisted packages and boundary-leaking
// prefixes are each caught by a dedicated negative fixture.

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
	got := checkAll(root, []string{"engine"}, map[string][]perm{"engine": {pkg("example.com/mod/result")}}, nil)
	if len(got) != 0 {
		t.Fatalf("compliant fixture flagged: %v", got)
	}
}

func TestUpwardImportIsCaught(t *testing.T) {
	root := t.TempDir()
	writeFixture(t, filepath.Join(root, "result"), "a.go",
		"package result\n\nimport \"example.com/mod/engine\"\n\nvar _ = engine.X\n")
	got := checkAll(root, []string{"result"}, map[string][]perm{"result": {}}, nil)
	if len(got) != 1 {
		t.Fatalf("upward import not caught: %v", got)
	}
}

func TestUnlistedPackageFails(t *testing.T) {
	// A new directory with Go files but no rule and no exemption must fail
	// the gate itself — the rule table cannot silently fall behind the tree.
	root := t.TempDir()
	writeFixture(t, filepath.Join(root, "sneaky"), "a.go", "package sneaky\n")
	got := checkAll(root, []string{"sneaky"}, map[string][]perm{}, nil)
	if len(got) != 1 {
		t.Fatalf("unlisted package not caught: %v", got)
	}
}

func TestExemptedTreeIsSkippedWithReason(t *testing.T) {
	root := t.TempDir()
	writeFixture(t, filepath.Join(root, "examples/demo"), "a.go",
		"package demo\n\nimport \"anything.example.com/at/all\"\n\nvar _ = all.X\n")
	got := checkAll(root, []string{"examples/demo"}, map[string][]perm{},
		map[string]string{"examples/demo": "consumer example"})
	if len(got) != 0 {
		t.Fatalf("exempted tree flagged: %v", got)
	}
}

func TestExactPermissionDoesNotLeakIntoSubpackages(t *testing.T) {
	// An exact permission for the engine must NOT grant the engine's
	// commands, extensions or internals: the audit's finding was that a
	// blanket prefix made every rule broader than its documented direction.
	allowed := []perm{pkg("example.com/mod")}
	if !importAllowed("example.com/mod", allowed) {
		t.Fatal("exact engine import blocked")
	}
	for _, p := range []string{
		"example.com/mod/cmd/tool",
		"example.com/mod/x/other",
		"example.com/mod/internal/dag",
	} {
		if importAllowed(p, allowed) {
			t.Fatalf("exact permission leaked into %q", p)
		}
	}
	if !importAllowed("github.com/google/go-cmp/cmp", []perm{tree("github.com/google/go-cmp")}) {
		t.Fatal("subtree permission blocked its own subpackage")
	}
}

func TestCrossExtensionAndTestkitDirectionsAreClosed(t *testing.T) {
	// Under the repository rule table: x/config must not reach x/comparators, and
	// phasetest must not reach commands or extensions.
	for imp, from := range map[string]string{
		rootModule + "/x/comparators":    "x/config",
		rootModule + "/x/config":         "x/comparators",
		rootModule + "/cmd/snapdiff":     "phasetest",
		rootModule + "/x/config/nothing": "phasetest",
	} {
		if importAllowed(imp, rules[from]) {
			t.Fatalf("%s may import %q under the real rules — direction not closed", from, imp)
		}
	}
	// Explicitly permitted command-side imports keep working.
	if !importAllowed(rootModule, rules["cmd/snapdiff"]) {
		t.Fatal("snapdiff's permitted engine import blocked")
	}
}

func TestStdlibAlwaysAllowedAndLookalikesAreNot(t *testing.T) {
	if !importAllowed("encoding/json", nil) {
		t.Fatal("stdlib blocked")
	}
	if importAllowed("evil.example.com/encoding/json", nil) {
		t.Fatal("dotted module path treated as stdlib")
	}
	if importAllowed("example.com/mod/resultx", []perm{tree("example.com/mod/result")}) {
		t.Fatal("subtree match leaked past the path boundary")
	}
}

func TestRepositoryRulesHoldOnTheRealTree(t *testing.T) {
	dirs, err := discoverGoDirs("../..")
	if err != nil {
		t.Fatal(err)
	}
	if got := checkAll("../..", dirs, rules, exempt); len(got) != 0 {
		t.Fatalf("boundary violations in the repository: %v", got)
	}
}

func TestTestOnlyPermissionsDoNotReachProductionFiles(t *testing.T) {
	// A test-only grant applies to _test.go files alone: the same import
	// from a production file stays a violation.
	root := t.TempDir()
	writeFixture(t, filepath.Join(root, "ext"), "prod.go",
		"package ext\n\nimport _ \"example.com/mod/phasetest\"\n")
	writeFixture(t, filepath.Join(root, "ext"), "ext_test.go",
		"package ext\n\nimport _ \"example.com/mod/phasetest\"\n")
	testOnly["ext"] = []perm{pkg("example.com/mod/phasetest")}
	defer delete(testOnly, "ext")
	got := checkAll(root, []string{"ext"}, map[string][]perm{"ext": {}}, nil)
	if len(got) != 1 {
		t.Fatalf("want exactly the production-file violation, got: %v", got)
	}
}

func TestEmptyExemptionReasonIsRejected(t *testing.T) {
	root := t.TempDir()
	writeFixture(t, filepath.Join(root, "quiet"), "a.go", "package quiet\n")
	got := checkAll(root, []string{"quiet"}, map[string][]perm{},
		map[string]string{"quiet": "  "})
	if len(got) != 1 {
		t.Fatalf("blank exemption reason accepted: %v", got)
	}
}

func TestDiscoveryFindsTrackedPackagesAndFeedsTheGate(t *testing.T) {
	// End-to-end through discovery itself, so a discovery regression can
	// never leave the completeness check vacuously green: a real temporary
	// git repository with nested tracked packages must be enumerated, and
	// an unmapped tracked package must fail via that enumeration.
	root := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", root}, args...)...)
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init", "-q")
	writeFixture(t, filepath.Join(root, "engine"), "a.go", "package engine\n")
	writeFixture(t, filepath.Join(root, "engine", "deep"), "b.go", "package deep\n")
	writeFixture(t, filepath.Join(root, "sneaky"), "c.go", "package sneaky\n")
	run("add", ".")

	dirs, err := discoverGoDirs(root)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"engine", "engine/deep", "sneaky"}
	if len(dirs) != len(want) {
		t.Fatalf("discovered %v, want %v", dirs, want)
	}
	for i := range want {
		if dirs[i] != want[i] {
			t.Fatalf("discovered %v, want %v", dirs, want)
		}
	}
	got := checkAll(root, dirs, map[string][]perm{"engine": {}, "engine/deep": {}}, nil)
	if len(got) != 1 || !strings.Contains(got[0], "sneaky") {
		t.Fatalf("tracked unmapped package not caught through discovery: %v", got)
	}
}
