// Copyright 2026 The Phase Contributors
// SPDX-License-Identifier: MIT

// Command boundcheck enforces the repository's package-boundary rules:
// each package has one responsibility and one allowed dependency
// direction, and a contribution cannot erode that quietly. The rules are
// structural (who may import whom), not stylistic.
//
// Standard-library imports are always permitted. Everything else must
// match an allowed prefix for the directory being checked.
package main

import (
	"fmt"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

const rootModule = "github.com/wow-qe/phase-go"

// rules maps a directory (relative to the repo root; "." is the root
// package) to the non-stdlib import prefixes it may use. Directions
// encoded: result and internal/dag sit at the bottom (stdlib only); the
// root engine may use only those two; phasetest and the extension modules
// depend inward on the engine; commands may use anything in-repo.
var rules = map[string][]string{
	".":                {rootModule + "/result", rootModule + "/internal/dag"},
	"result":           {},
	"internal/dag":     {},
	"phasetest":        {rootModule, rootModule + "/result"},
	"x/config":         {rootModule, rootModule + "/result", "gopkg.in/yaml.v3"},
	"x/comparators":    {rootModule, rootModule + "/result", "github.com/google/go-cmp"},
	"cmd/snapdiff":     {rootModule, rootModule + "/result"},
	"cmd/commentcheck": {},
	"cmd/apidump":      {},
	"cmd/boundcheck":   {},
}

func main() {
	root := "."
	if len(os.Args) > 1 {
		root = os.Args[1]
	}
	violations := checkAll(root, rules)
	for _, v := range violations {
		fmt.Println(v)
	}
	if len(violations) > 0 {
		fmt.Printf("boundcheck: %d violation(s)\n", len(violations))
		os.Exit(1)
	}
}

func checkAll(root string, rules map[string][]string) []string {
	dirs := make([]string, 0, len(rules))
	for d := range rules {
		dirs = append(dirs, d)
	}
	sort.Strings(dirs)
	var out []string
	for _, dir := range dirs {
		self := rootModule
		if dir != "." {
			self = rootModule + "/" + dir
		}
		out = append(out, checkDir(filepath.Join(root, dir), dir, self, rules[dir])...)
	}
	return out
}

func checkDir(path, name, self string, allowed []string) []string {
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
		for _, imp := range af.Imports {
			p, _ := strconv.Unquote(imp.Path.Value)
			if p == self { // an external test package importing its own package
				continue
			}
			if ok := importAllowed(p, allowed); !ok {
				out = append(out, fmt.Sprintf("%s/%s: imports %q — not in the allowed set for %s", name, fn, p, name))
			}
		}
	}
	return out
}

// importAllowed permits the standard library (first path element has no
// dot) and any path matching an allowed prefix exactly or as a
// subdirectory.
func importAllowed(path string, allowed []string) bool {
	first := path
	if i := strings.IndexByte(path, '/'); i >= 0 {
		first = path[:i]
	}
	if !strings.Contains(first, ".") {
		return true // standard library
	}
	for _, a := range allowed {
		if path == a || strings.HasPrefix(path, a+"/") {
			return true
		}
	}
	return false
}
