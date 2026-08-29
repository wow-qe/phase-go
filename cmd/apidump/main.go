// Copyright 2026 The Phase Contributors
// SPDX-License-Identifier: MIT

// Command apidump prints the exported API surface of the repository's
// published packages as one sorted line per declaration. The output is
// committed as a baseline under api/, and the api-check gate regenerates
// and diffs it: an exported symbol cannot appear, change shape or vanish
// without the diff naming it — accidental API change becomes impossible to
// miss, intentional change becomes a reviewed baseline edit plus a
// changelog entry.
package main

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/parser"
	"go/printer"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: apidump <package-dir> [package-dir...]")
		os.Exit(2)
	}
	var lines []string
	for _, dir := range os.Args[1:] {
		ls, err := dumpDir(dir)
		if err != nil {
			fmt.Fprintln(os.Stderr, "apidump:", err)
			os.Exit(2)
		}
		lines = append(lines, ls...)
	}
	sort.Strings(lines)
	for _, l := range lines {
		fmt.Println(l)
	}
}

func dumpDir(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	fs := token.NewFileSet()
	var lines []string
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		af, err := parser.ParseFile(fs, filepath.Join(dir, name), nil, 0)
		if err != nil {
			return nil, err
		}
		if af.Name.Name == "main" {
			continue
		}
		prefix := filepath.ToSlash(dir) + " " + af.Name.Name
		for _, d := range af.Decls {
			lines = append(lines, dumpDecl(fs, prefix, d)...)
		}
	}
	return lines, nil
}

func dumpDecl(fs *token.FileSet, prefix string, d ast.Decl) []string {
	var lines []string
	switch dd := d.(type) {
	case *ast.FuncDecl:
		if !dd.Name.IsExported() {
			return nil
		}
		recv := ""
		if dd.Recv != nil && len(dd.Recv.List) > 0 {
			t := render(fs, dd.Recv.List[0].Type)
			base := strings.TrimPrefix(t, "*")
			if !ast.IsExported(strings.TrimLeft(base, "*[]")) {
				return nil // method on an unexported type is not API
			}
			recv = "(" + t + ") "
		}
		lines = append(lines, fmt.Sprintf("%s: func %s%s%s", prefix, recv, dd.Name.Name, render(fs, dd.Type)[4:]))
	case *ast.GenDecl:
		for _, sp := range dd.Specs {
			switch s := sp.(type) {
			case *ast.TypeSpec:
				if !s.Name.IsExported() {
					continue
				}
				lines = append(lines, fmt.Sprintf("%s: type %s %s", prefix, s.Name.Name, typeShape(fs, s.Type)))
				lines = append(lines, exportedMembers(fs, prefix, s)...)
			case *ast.ValueSpec:
				for _, n := range s.Names {
					if !n.IsExported() {
						continue
					}
					kind := "var"
					if dd.Tok == token.CONST {
						kind = "const"
					}
					typ := ""
					if s.Type != nil {
						typ = " " + render(fs, s.Type)
					}
					lines = append(lines, fmt.Sprintf("%s: %s %s%s", prefix, kind, n.Name, typ))
				}
			}
		}
	}
	return lines
}

// typeShape names the kind without dumping unexported internals.
func typeShape(fs *token.FileSet, e ast.Expr) string {
	switch e.(type) {
	case *ast.StructType:
		return "struct"
	case *ast.InterfaceType:
		return "interface"
	default:
		return render(fs, e)
	}
}

// exportedMembers lists exported struct fields and interface methods —
// the parts of a type consumers can reach and the baseline must pin.
func exportedMembers(fs *token.FileSet, prefix string, s *ast.TypeSpec) []string {
	var lines []string
	switch t := s.Type.(type) {
	case *ast.StructType:
		for _, f := range t.Fields.List {
			ft := render(fs, f.Type)
			if len(f.Names) == 0 { // embedded
				if ast.IsExported(strings.TrimLeft(ft, "*")) {
					lines = append(lines, fmt.Sprintf("%s: field %s.%s (embedded)", prefix, s.Name.Name, ft))
				}
				continue
			}
			for _, n := range f.Names {
				if n.IsExported() {
					lines = append(lines, fmt.Sprintf("%s: field %s.%s %s", prefix, s.Name.Name, n.Name, ft))
				}
			}
		}
	case *ast.InterfaceType:
		for _, m := range t.Methods.List {
			for _, n := range m.Names {
				if n.IsExported() {
					lines = append(lines, fmt.Sprintf("%s: method %s.%s%s", prefix, s.Name.Name, n.Name, render(fs, m.Type)[4:]))
				}
			}
		}
	}
	return lines
}

func render(fs *token.FileSet, n ast.Node) string {
	var buf bytes.Buffer
	_ = printer.Fprint(&buf, fs, n)
	return buf.String()
}
