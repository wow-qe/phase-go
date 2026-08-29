// Copyright 2026 The Phase Contributors
// SPDX-License-Identifier: MIT

// Command boundcheck enforces the repository's package-boundary rules:
// each package has one responsibility and one allowed dependency
// direction, and a contribution cannot erode that quietly. The rules are
// structural (who may import whom), not stylistic.
//
// Every directory containing tracked Go files must carry an explicit rule
// or an explicit exemption — a new package without either fails the gate,
// so the rule table cannot silently fall behind the tree. Standard-library
// imports are always permitted; each rule entry is either an exact package
// path or a subtree (path plus everything below it).
package main

import (
	"fmt"
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

const rootModule = "github.com/wow-qe/phase-go"

// perm is one allowed non-stdlib import: exact package, or whole subtree.
type perm struct {
	path    string
	subtree bool
}

func pkg(p string) perm  { return perm{path: p} }
func tree(p string) perm { return perm{path: p, subtree: true} }

// rules maps a directory (relative to the repo root; "." is the root
// package) to its allowed imports. Directions encoded: result and
// internal/dag sit at the bottom (stdlib only); the root engine may use
// only those two; phasetest and the extension modules depend inward on
// exactly the engine and result — never on commands, examples or each
// other; commands may use the engine side but not each other.
var rules = map[string][]perm{
	".":                {pkg(rootModule + "/result"), pkg(rootModule + "/internal/dag")},
	"result":           {},
	"internal/dag":     {},
	"phasetest":        {pkg(rootModule), pkg(rootModule + "/result")},
	"x/config":         {pkg(rootModule), pkg(rootModule + "/result"), pkg("gopkg.in/yaml.v3")},
	"x/comparators":    {pkg(rootModule), pkg(rootModule + "/result"), tree("github.com/google/go-cmp")},
	"cmd/snapdiff":     {pkg(rootModule), pkg(rootModule + "/result")},
	"cmd/commentcheck": {},
	"cmd/apidump":      {},
	"cmd/boundcheck":   {},
}

// exempt names directories that contain Go files but are deliberately
// outside the boundary rules, each with the reason on record. The example
// modules are consumer-side demonstrations resolved through their own
// go.mod files, not part of the engine's dependency graph.
// testOnly grants additional imports to _test.go files only: the
// consumer test kit is legitimately imported by extension tests (that is
// what it is for) while remaining forbidden to their production code.
var testOnly = map[string][]perm{
	"x/config":      {pkg(rootModule + "/phasetest")},
	"x/comparators": {pkg(rootModule + "/phasetest")},
}

var exempt = map[string]string{
	"examples/provisioning": "consumer example (separate module semantics)",
	"examples/checkout":     "consumer example (separate module)",
	"examples/misuse":       "consumer example (separate module)",
}

func main() {
	root := "."
	if len(os.Args) > 1 {
		root = os.Args[1]
	}
	dirs, err := discoverGoDirs(root)
	if err != nil {
		fmt.Fprintln(os.Stderr, "boundcheck:", err)
		os.Exit(2)
	}
	violations := checkAll(root, dirs, rules, exempt)
	for _, v := range violations {
		fmt.Println(v)
	}
	if len(violations) > 0 {
		fmt.Printf("boundcheck: %d violation(s)\n", len(violations))
		os.Exit(1)
	}
}

// discoverGoDirs lists every directory holding tracked Go files, so the
// rule table is checked for completeness against the real tree.
func discoverGoDirs(root string) ([]string, error) {
	out, err := exec.Command("git", "-C", root, "ls-files", "*.go").Output()
	if err != nil {
		return nil, fmt.Errorf("git ls-files: %w", err)
	}
	seen := map[string]bool{}
	for _, f := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if f == "" {
			continue
		}
		d := filepath.ToSlash(filepath.Dir(f))
		seen[d] = true
	}
	dirs := make([]string, 0, len(seen))
	for d := range seen {
		dirs = append(dirs, d)
	}
	sort.Strings(dirs)
	return dirs, nil
}

func checkAll(root string, dirs []string, rules map[string][]perm, exempt map[string]string) []string {
	var out []string
	for _, dir := range dirs {
		if reason, ok := exemptFor(dir, exempt); ok {
			_ = reason
			continue
		}
		allowed, ok := rules[dir]
		if !ok {
			out = append(out, fmt.Sprintf("%s: contains Go files but has no boundary rule — add a rule (or an explicit exemption with its reason) to cmd/boundcheck", dir))
			continue
		}
		self := rootModule
		if dir != "." {
			self = rootModule + "/" + dir
		}
		out = append(out, checkDir(filepath.Join(root, dir), dir, self, allowed, testOnly[dir])...)
	}
	return out
}

// exemptFor matches a directory or any parent against the exemption table.
func exemptFor(dir string, exempt map[string]string) (string, bool) {
	for d := dir; ; {
		if r, ok := exempt[d]; ok {
			return r, true
		}
		i := strings.LastIndexByte(d, '/')
		if i < 0 {
			return "", false
		}
		d = d[:i]
	}
}

func checkDir(path, name, self string, allowed, testExtra []perm) []string {
	entries, err := os.ReadDir(path)
	if err != nil {
		return []string{fmt.Sprintf("%s: %v", name, err)}
	}
	var out []string
	fs := token.NewFileSet()
	for _, e := range entries {
		fn := e.Name()
		if e.IsDir() || !strings.HasSuffix(fn, ".go") {
			continue
		}
		af, err := parser.ParseFile(fs, filepath.Join(path, fn), nil, parser.ImportsOnly)
		if err != nil {
			out = append(out, fmt.Sprintf("%s/%s: %v", name, fn, err))
			continue
		}
		perms := allowed
		if strings.HasSuffix(fn, "_test.go") {
			perms = append(append([]perm(nil), allowed...), testExtra...)
		}
		for _, imp := range af.Imports {
			p, _ := strconv.Unquote(imp.Path.Value)
			if p == self { // an external test package importing its own package
				continue
			}
			if !importAllowed(p, perms) {
				out = append(out, fmt.Sprintf("%s/%s: imports %q — not in the allowed set for %s", name, fn, p, name))
			}
		}
	}
	return out
}

// importAllowed permits the standard library (first path element carries
// no dot) and any path matching a permission: exact entries match only
// their own package, subtree entries also match below the path boundary.
func importAllowed(path string, allowed []perm) bool {
	first := path
	if i := strings.IndexByte(path, '/'); i >= 0 {
		first = path[:i]
	}
	if !strings.Contains(first, ".") {
		return true // standard library
	}
	for _, a := range allowed {
		if path == a.path {
			return true
		}
		if a.subtree && strings.HasPrefix(path, a.path+"/") {
			return true
		}
	}
	return false
}
