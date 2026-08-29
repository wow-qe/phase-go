// Copyright 2026 The Phase Contributors
// SPDX-License-Identifier: MIT

// Command commentcheck enforces the repository's comment standard: comments
// document what code does, the invariant it enforces, and why it matters —
// never development history, internal process vocabulary, or rhetoric.
//
// Three complementary methods, so a miss by one is caught by another:
//
//  1. Broad pattern scans: comments (extracted via go/parser, so string
//     literals are never false positives) are matched against banned
//     pattern classes — process/actor vocabulary, internal finding codes,
//     history narration, rhetorical emphasis, and AI-assistant terms.
//  2. Semantic verification: bracketed symbol references in package doc
//     comments must resolve to identifiers declared in that package, and a
//     stale-claims ledger fails the build when prose asserts behavior the
//     code no longer has (each entry names the current fact).
//  3. Independent re-checks after integration: the tool always scans the
//     full tracked tree, never a diff, and runs in both `make check` and
//     CI, so a merge, revert, or concurrent edit that reintroduces a
//     pattern fails the next gate regardless of which change introduced it.
//
// A finding is suppressed only when a comment group contains a standalone
// directive line consisting of the allow marker; allowances are counted in
// the summary so they stay visible.
package main

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
)

// patternRules are the broad scans. Each rule names its class so a failure
// message teaches the standard, not just the match.
var patternRules = []struct {
	class string
	re    *regexp.Regexp
}{
	{"process-actor", regexp.MustCompile(`(?i)\breview gate\b|\bengine lens\b|\bre-review\b|\bgate probe\b|\breview finding\b|\bcold review\b|\breviewer\b|\barbitrat`)},
	{"process-actor", regexp.MustCompile(`\bQE (rejection|sweep|probe|review|acceptance|demanded|catch)\b`)},
	{"process-actor", regexp.MustCompile(`(?i)\bcensus\b|\bwatchlist\b|\bsilent-failure hunter\b|\bacceptance ledger\b`)},
	{"finding-code", regexp.MustCompile(`\b(SEC|LP)\d+\b|\bA-[a-n]\b|\bDoD\b|(?i)\bdefinition of done\b`)},
	{"finding-code", regexp.MustCompile(`(^|[^A-Za-z0-9_])[ABCSQPVGR]\d{1,2}[ab]?: `)},
	{"history", regexp.MustCompile(`(?i)\bused to\b|\bpreviously\b|\ban earlier (version|fix|implementation)\b|\bthe (first|old) (landing|implementation|version)\b|\bwas two booleans\b|\bsource framework\b|\bshouldn't happen\b`)},
	{"rhetoric", regexp.MustCompile(`(?i)\bloudly\b|\bnobody\b|\bsomeone\b|\bkill-switch\b|\bon fire\b|\bred day\b|\brap sheet\b|\bpoisons?\b|\bpoisoning\b|\bcanary\b|\bfor free\b|\briding on\b|\bbefore you trust\b`)},
	{"rhetoric", regexp.MustCompile(`\b(lie|lies|lied)\b`)},
	{"ai-term", regexp.MustCompile(`(?i)\bclaude\b|\banthropic\b|\bopenai\b|\bchatgpt\b|\bcopilot\b|\bgpt-\d\b|\bllm\b`)},
	{"emphasis-caps", regexp.MustCompile(`(^|[^A-Za-z_./"&])(ONE|EVERY|LOUD|WRONG|NOTHING|NEVER|ALWAYS|MUST|BOTH|WHOLE|REFUSES|DECLARED|RECORDED|ONLY|LIVE|STAYS|WHOSE|PROOF|EXACT|REAL|FIRST)($|[^A-Za-z_./&])`)},
}

// staleClaims is the semantic ledger: prose that contradicts current
// behavior. Every entry states the current fact so the failure message
// corrects the reader. When behavior changes, the corresponding entry
// changes with it — in the same commit.
var staleClaims = []struct {
	re   *regexp.Regexp
	fact string
}{
	{regexp.MustCompile(`(?i)only wall-clock lever`), "concurrency knobs exist (Config.MaxPhaseConcurrency / MaxCaseConcurrency); sharding is not the only lever"},
	{regexp.MustCompile(`(?i)execution is (globally )?sequential\b[^;,.]*`), "execution is sequential only by default; the concurrency knobs change it"},
	{regexp.MustCompile(`(?i)sequentially in the order given`), "execution follows the case DAG and configured concurrency; reports retain declaration order"},
	{regexp.MustCompile(`(?i)cases stay in execution order`), "reports retain declaration order"},
	{regexp.MustCompile(`(?i)bootstrap stage|public APIs will be added`), "the public API exists; doc.go documents it"},
	{regexp.MustCompile(`(?i)redaction ships in|redaction is deferred`), "redaction is implemented for reports and event emission"},
	{regexp.MustCompile(`(?i)sub-phases: same position`), "Settings.Sub is rejected at construction; Pipeline.Group is the mechanism"},
}

const allowMarker = "commentcheck:allow"

// hasAllowDirective reports whether the group contains the marker as a
// standalone directive line. Prose that merely mentions the marker (this
// file's own documentation, for example) does not suppress scanning.
func hasAllowDirective(cg *ast.CommentGroup) bool {
	for _, c := range cg.List {
		if strings.TrimSpace(strings.TrimPrefix(c.Text, "//")) == allowMarker {
			return true
		}
	}
	return false
}

var docLink = regexp.MustCompile(`\[([A-Z][A-Za-z0-9_]*)(?:\.[A-Z][A-Za-z0-9_]*)?\]`)

type finding struct {
	pos   token.Position
	class string
	line  string
}

type docRef struct {
	pkg  string
	name string
	pos  token.Position
}

func main() {
	root := "."
	if len(os.Args) > 1 {
		root = os.Args[1]
	}
	files, err := trackedGoFiles(root)
	if err != nil {
		fmt.Fprintln(os.Stderr, "commentcheck:", err)
		os.Exit(2)
	}

	var findings []finding
	var refs []docRef
	decls := map[string]map[string]bool{} // dir+package -> declared identifiers
	allowed := 0

	for _, path := range files {
		fs := token.NewFileSet()
		af, err := parser.ParseFile(fs, path, nil, parser.ParseComments)
		if err != nil {
			fmt.Fprintf(os.Stderr, "commentcheck: parse %s: %v\n", path, err)
			os.Exit(2)
		}
		key := filepath.Dir(path) + ":" + af.Name.Name
		findings = append(findings, scanComments(fs, af, &allowed)...)
		collectDecls(af, decls, key)
		refs = append(refs, collectDocRefs(fs, af, key)...)
	}

	// Second pass: package-doc [Symbol] links must resolve within their
	// package. Run after the walk so declarations from every file of the
	// package are known.
	for _, r := range refs {
		if !decls[r.pkg][r.name] {
			findings = append(findings, finding{r.pos, "doc-link",
				fmt.Sprintf("[%s] does not resolve to a declaration in package %s", r.name, r.pkg)})
		}
	}

	for _, fd := range findings {
		fmt.Printf("%s: [%s] %s\n", fd.pos, fd.class, strings.TrimSpace(fd.line))
	}
	if allowed > 0 {
		fmt.Printf("commentcheck: %d explicit allowance(s) in effect\n", allowed)
	}
	if len(findings) > 0 {
		fmt.Printf("commentcheck: %d finding(s)\n", len(findings))
		os.Exit(1)
	}
}

// trackedGoFiles lists git-tracked .go files so ignored or generated trees
// are never scanned and the gate sees exactly what ships.
func trackedGoFiles(root string) ([]string, error) {
	out, err := exec.Command("git", "-C", root, "ls-files", "*.go").Output()
	if err != nil {
		return nil, fmt.Errorf("git ls-files: %w", err)
	}
	var files []string
	for _, l := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if l != "" {
			files = append(files, filepath.Join(root, l))
		}
	}
	return files, nil
}

func scanComments(fs *token.FileSet, af *ast.File, allowed *int) []finding {
	var out []finding
	for _, cg := range af.Comments {
		if hasAllowDirective(cg) {
			*allowed++
			continue
		}
		for _, c := range cg.List {
			for _, ln := range strings.Split(c.Text, "\n") {
				for _, r := range patternRules {
					if r.re.MatchString(ln) {
						out = append(out, finding{fs.Position(c.Pos()), r.class, ln})
					}
				}
				for _, sc := range staleClaims {
					if sc.re.MatchString(ln) {
						out = append(out, finding{fs.Position(c.Pos()), "stale-claim (fact: " + sc.fact + ")", ln})
					}
				}
			}
		}
	}
	return out
}

func collectDecls(af *ast.File, decls map[string]map[string]bool, key string) {
	m := decls[key]
	if m == nil {
		m = map[string]bool{}
		decls[key] = m
	}
	for _, d := range af.Decls {
		switch dd := d.(type) {
		case *ast.GenDecl:
			for _, sp := range dd.Specs {
				switch s := sp.(type) {
				case *ast.TypeSpec:
					m[s.Name.Name] = true
				case *ast.ValueSpec:
					for _, n := range s.Names {
						m[n.Name] = true
					}
				}
			}
		case *ast.FuncDecl:
			m[dd.Name.Name] = true
		}
	}
}

// collectDocRefs gathers [Symbol] links from the package doc comment only:
// that is the pkg.go.dev landing surface, where a dangling reference is
// most damaging.
func collectDocRefs(fs *token.FileSet, af *ast.File, key string) []docRef {
	if af.Doc == nil {
		return nil
	}
	var refs []docRef
	for _, m := range docLink.FindAllStringSubmatch(af.Doc.Text(), -1) {
		refs = append(refs, docRef{key, m[1], fs.Position(af.Doc.Pos())})
	}
	return refs
}
