package main

import (
	"fmt"
	"go/ast"
	"go/format"
	"go/parser"
	"go/token"
	"path"
	"strconv"
	"strings"
)

// Insert text between complete import lines so Go's formatter can sort within
// groups without losing comments or collapsing the blank lines between groups.
func insertImport(data []byte, importPath, alias string) ([]byte, error) {
	fset := token.NewFileSet()
	tree, err := parser.ParseFile(fset, "", data, parser.ParseComments)
	if err != nil {
		return nil, err
	}
	offset := func(p token.Pos) int { return fset.PositionFor(p, false).Offset }
	importText := strconv.Quote(importPath)
	if alias != path.Base(importPath) {
		importText = alias + " " + importText
	}
	isModule := strings.HasPrefix(importPath, "skillmanager/")
	var target *ast.GenDecl
	for _, decl := range tree.Decls {
		group, ok := decl.(*ast.GenDecl)
		if !ok || group.Tok != token.IMPORT {
			continue
		}
		// cgo requires its own import and attached preamble.
		hasC := false
		hasModule := false
		for _, spec := range group.Specs {
			value, _ := strconv.Unquote(spec.(*ast.ImportSpec).Path.Value)
			if value == importPath {
				return data, nil
			}
			hasC = hasC || value == "C"
			hasModule = hasModule || strings.HasPrefix(value, "skillmanager/")
		}
		if hasC {
			continue
		}
		if target == nil || isModule && hasModule {
			target = group
		}
		if isModule && hasModule {
			break
		}
	}
	if target == nil {
		at := lineEnd(data, offset(tree.Name.End()))
		return spliceImport(data, at, at, "\nimport "+importText+"\n")
	}
	if !target.Lparen.IsValid() {
		spec := target.Specs[0].(*ast.ImportSpec)
		end := lineEnd(data, offset(spec.End()))
		replacement := "import (\n" + strings.TrimSpace(string(data[offset(spec.Pos()):end])) + "\n)\n"
		expanded, err := spliceImport(data, offset(target.Pos()), end, replacement)
		if err != nil {
			return nil, err
		}
		return insertImport(expanded, importPath, alias)
	}
	type importGroup struct{ specs []*ast.ImportSpec }
	groups := []importGroup{}
	previousEnd := 0
	for _, raw := range target.Specs {
		spec := raw.(*ast.ImportSpec)
		start := spec.Pos()
		if spec.Doc != nil {
			start = spec.Doc.Pos()
		}
		line := fset.PositionFor(start, false).Line
		if len(groups) == 0 || line > previousEnd+1 {
			groups = append(groups, importGroup{})
		}
		groups[len(groups)-1].specs = append(groups[len(groups)-1].specs, spec)
		end := spec.End()
		if spec.Comment != nil {
			end = spec.Comment.End()
		}
		previousEnd = fset.PositionFor(end, false).Line
	}
	for _, group := range groups {
		matches := true
		for _, spec := range group.specs {
			value, _ := strconv.Unquote(spec.Path.Value)
			if isModule {
				matches = matches && strings.HasPrefix(value, "skillmanager/")
			} else {
				matches = matches && isStandardImport(value)
			}
		}
		if !matches {
			continue
		}
		last := group.specs[len(group.specs)-1]
		end := last.End()
		if last.Comment != nil {
			end = last.Comment.End()
		}
		at := lineEnd(data, offset(end))
		return spliceImport(data, at, at, importText+"\n")
	}
	if isModule || len(target.Specs) == 0 {
		at := lineStart(data, offset(target.Rparen))
		separator := ""
		if len(target.Specs) > 0 {
			separator = "\n"
		}
		return spliceImport(data, at, at, separator+importText+"\n")
	}
	first := target.Specs[0].(*ast.ImportSpec)
	start := first.Pos()
	if first.Doc != nil {
		start = first.Doc.Pos()
	}
	at := lineStart(data, offset(start))
	return spliceImport(data, at, at, importText+"\n\n")
}
func isStandardImport(value string) bool {
	first, _, _ := strings.Cut(value, "/")
	return first != "skillmanager" && !strings.Contains(first, ".")
}
func lineStart(data []byte, at int) int {
	for at > 0 && data[at-1] != '\n' {
		at--
	}
	return at
}
func lineEnd(data []byte, at int) int {
	for at < len(data) && data[at] != '\n' {
		at++
	}
	if at < len(data) {
		at++
	}
	return at
}
func spliceImport(data []byte, start, end int, text string) ([]byte, error) {
	updated := string(data[:start]) + text + string(data[end:])
	formatted, err := format.Source([]byte(updated))
	if err != nil {
		return nil, fmt.Errorf("inserting import: %w", err)
	}
	return formatted, nil
}
