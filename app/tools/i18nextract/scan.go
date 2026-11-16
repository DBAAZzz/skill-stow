package main

import (
	"encoding/json"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"unicode"
)

type sourceFile struct {
	path  string
	data  []byte
	fset  *token.FileSet
	tree  *ast.File
	sites []site
}
type site struct {
	entry      entry
	start, end int
	call       *ast.CallExpr
}

func scanFiles(prefix string, paths []string) ([]entry, error) {
	entries := []entry{}
	keys := map[string]string{}
	seen := map[string]bool{}
	for _, path := range paths {
		path = filepath.Clean(path)
		if skippedFile(path) || seen[path] {
			continue
		}
		seen[path] = true
		source, err := parseSource(path)
		if err != nil {
			return nil, err
		}
		for _, found := range source.sites {
			item := found.entry
			signature, _ := json.Marshal(struct {
				Zh           string
				Placeholders []placeholder
			}{item.Zh, item.Placeholders})
			key, ok := keys[string(signature)]
			if !ok {
				key = fmt.Sprintf("%sMsg%03d", prefix, len(keys)+1)
				keys[string(signature)] = key
			}
			item.Key = key
			entries = append(entries, item)
		}
	}
	return entries, nil
}
func skippedFile(path string) bool {
	return strings.HasSuffix(path, "_test.go") || strings.HasSuffix(path, "_gen.go")
}
func parseSource(path string) (*sourceFile, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	fset := token.NewFileSet()
	tree, err := parser.ParseFile(fset, path, data, parser.ParseComments)
	if err != nil {
		return nil, err
	}
	source := &sourceFile{path: path, data: data, fset: fset, tree: tree}
	var ancestors []ast.Node
	ast.Inspect(tree, func(node ast.Node) bool {
		if node == nil {
			ancestors = ancestors[:len(ancestors)-1]
			return true
		}
		if literal, ok := node.(*ast.BasicLit); ok && literal.Kind == token.STRING {
			value, err := strconv.Unquote(literal.Value)
			if err == nil && hasCJK(value) {
				source.sites = append(source.sites, source.classify(literal, value, ancestors))
			}
		}
		ancestors = append(ancestors, node)
		return true
	})
	return source, nil
}
func hasCJK(s string) bool {
	for _, r := range s {
		if r >= 0x4E00 && r <= 0x9FFF || r >= 0x3000 && r <= 0x303F || r >= 0xFF00 && r <= 0xFFEF {
			return true
		}
	}
	return false
}
func escapeICU(s string) string {
	return strings.NewReplacer("'", "''", "{", "'{'", "}", "'}'").Replace(s)
}
func (s *sourceFile) offset(pos token.Pos) int { return s.fset.PositionFor(pos, false).Offset }
func (s *sourceFile) text(node ast.Node) string {
	return string(s.data[s.offset(node.Pos()):s.offset(node.End())])
}
func contains(node, target ast.Node) bool {
	return node != nil && node.Pos() <= target.Pos() && node.End() >= target.End()
}
func (s *sourceFile) importedCall(call *ast.CallExpr, pkg, name string) bool {
	selector, ok := call.Fun.(*ast.SelectorExpr)
	if !ok || selector.Sel.Name != name {
		return false
	}
	ident, ok := selector.X.(*ast.Ident)
	if !ok || ident.Obj != nil {
		return false
	}
	for _, spec := range s.tree.Imports {
		path, _ := strconv.Unquote(spec.Path.Value)
		alias := filepath.Base(path)
		if spec.Name != nil {
			alias = spec.Name.Name
		}
		if path == pkg && alias == ident.Name {
			return true
		}
	}
	return false
}
func callName(call *ast.CallExpr) string {
	switch f := call.Fun.(type) {
	case *ast.Ident:
		return f.Name
	case *ast.SelectorExpr:
		return f.Sel.Name
	}
	return ""
}
func (s *sourceFile) classify(literal *ast.BasicLit, value string, ancestors []ast.Node) site {
	pos := s.fset.PositionFor(literal.Pos(), false)
	result := site{entry: entry{ID: fmt.Sprintf("%s:%d:%d", s.path, pos.Line, pos.Column), File: s.path, Line: pos.Line, Col: pos.Column, Kind: "literal", GoText: value, Zh: escapeICU(value), Args: []string{}, Placeholders: []placeholder{}}, start: s.offset(literal.Pos()), end: s.offset(literal.End())}
	manual := func(reason string) site { result.entry.Kind = "manual"; result.entry.Reason = reason; return result }
	for _, group := range s.tree.Comments {
		for _, comment := range group.List {
			line := s.fset.PositionFor(comment.End(), false).Line
			if strings.HasPrefix(comment.Text, "//go:") && (line == pos.Line || line == pos.Line-1) {
				return manual("Go directive adjacent to string")
			}
		}
	}
	inFunction := false
	for _, node := range ancestors {
		switch n := node.(type) {
		case *ast.FuncDecl, *ast.FuncLit:
			inFunction = true
		case *ast.GenDecl:
			if n.Tok == token.CONST || n.Tok == token.VAR && !inFunction {
				if !inFunction {
					return manual("package-level const/var declaration")
				}
				return manual("local const requires a constant expression")
			}
		case *ast.Field:
			if n.Tag == literal {
				return manual("struct tag")
			}
		case *ast.ImportSpec:
			return manual("import path")
		case *ast.BinaryExpr:
			if n.Op == token.EQL || n.Op == token.NEQ {
				return manual("comparison operand")
			}
			if n.Op == token.ADD {
				return manual("string concatenation")
			}
		case *ast.IndexExpr:
			if contains(n.Index, literal) {
				return manual("map key")
			}
		case *ast.SwitchStmt:
			if contains(n.Tag, literal) {
				return manual("switch comparison value")
			}
		case *ast.CaseClause:
			for _, expr := range n.List {
				if contains(expr, literal) {
					return manual("switch case value")
				}
			}
		case *ast.KeyValueExpr:
			if contains(n.Key, literal) {
				return manual("map key")
			}
		case *ast.CallExpr:
			if callName(n) == "Key" {
				for _, arg := range n.Args {
					if contains(arg, literal) {
						return manual("Key argument")
					}
				}
			}
		}
	}
	if !inFunction {
		return manual("outside function body")
	}
	// Parentheses around the format literal do not change its role.
	var formatExpr ast.Expr = literal
	for i := len(ancestors) - 1; i >= 0; i-- {
		if paren, ok := ancestors[i].(*ast.ParenExpr); ok {
			formatExpr = paren
			continue
		}
		call, ok := ancestors[i].(*ast.CallExpr)
		if !ok || len(call.Args) == 0 || call.Args[0] != formatExpr {
			break
		}
		if s.importedCall(call, "errors", "New") && len(call.Args) == 1 && !call.Ellipsis.IsValid() {
			result.entry.Kind = "errorsNew"
			break
		}
		isError := s.importedCall(call, "fmt", "Errorf")
		if isError || s.importedCall(call, "fmt", "Sprintf") {
			args := call.Args[1:]
			message := value
			if isError {
				prefix, wraps, err := errorFormat(value)
				if err != nil {
					return manual(err.Error())
				}
				if wraps {
					if len(args) == 0 {
						return manual("Errorf argument count mismatch")
					}
					result.entry.Wrapped = s.text(args[len(args)-1])
					args = args[:len(args)-1]
				}
				message = prefix
			}
			for _, arg := range args {
				result.entry.Args = append(result.entry.Args, s.text(arg))
			}
			zh, placeholders, err := parseFormat(message, args)
			kind, function := "sprintf", "Sprintf"
			if isError {
				kind, function = "errorf", "Errorf"
			}
			if call.Ellipsis.IsValid() {
				return manual("variadic " + function + " arguments")
			}
			if err != nil {
				return manual(strings.ReplaceAll(err.Error(), "Sprintf", function))
			}
			result.entry.Kind = kind
			result.entry.Zh = zh
			result.entry.Placeholders = placeholders
			result.start = s.offset(call.Pos())
			result.end = s.offset(call.End())
			result.call = call
		}
		break
	}
	return result
}

// Only an unmodified trailing %w can be kept outside the translated message.
func errorFormat(value string) (string, bool, error) {
	wraps, wrapAt := 0, -1
	for i := 0; i < len(value); i++ {
		if value[i] != '%' {
			continue
		}
		if i+1 == len(value) {
			return "", false, fmt.Errorf("incomplete format verb")
		}
		i++
		switch value[i] {
		case '%', 'd', 's', 'v':
		case 'w':
			wraps++
			wrapAt = i - 1
		default:
			return "", false, fmt.Errorf("unsupported format verb (flags, width, precision or indexes require manual migration)")
		}
	}
	if wraps > 1 {
		return "", false, fmt.Errorf("multiple %%w verbs require manual migration")
	}
	if wraps == 1 {
		if wrapAt != len(value)-2 {
			return "", false, fmt.Errorf("%%w must be the final format verb at the end of the message")
		}
		return value[:wrapAt], true, nil
	}
	return value, false, nil
}

func parseFormat(value string, args []ast.Expr) (string, []placeholder, error) {
	var out strings.Builder
	placeholders := []placeholder{}
	used := map[string]bool{}
	index := 0
	for i := 0; i < len(value); {
		next := strings.IndexByte(value[i:], '%')
		if next < 0 {
			out.WriteString(escapeICU(value[i:]))
			break
		}
		next += i
		out.WriteString(escapeICU(value[i:next]))
		if next+1 == len(value) {
			return "", nil, fmt.Errorf("incomplete format verb")
		}
		verb := value[next+1]
		i = next + 2
		if verb == '%' {
			out.WriteByte('%')
			continue
		}
		if verb != 'd' && verb != 's' && verb != 'v' {
			return "", nil, fmt.Errorf("unsupported format verb (flags, width, precision or indexes require manual migration)")
		}
		if index >= len(args) {
			return "", nil, fmt.Errorf("Sprintf argument count mismatch")
		}
		name := argumentName(args[index], index)
		base := name
		for suffix := 2; used[name]; suffix++ {
			name = fmt.Sprintf("%s%d", base, suffix)
		}
		used[name] = true
		typ := "String"
		if verb == 'd' {
			typ = "int"
		}
		placeholders = append(placeholders, placeholder{Name: name, Type: typ})
		out.WriteByte('{')
		out.WriteString(name)
		out.WriteByte('}')
		index++
	}
	if index != len(args) {
		return "", nil, fmt.Errorf("Sprintf argument count mismatch")
	}
	return out.String(), placeholders, nil
}
func argumentName(expr ast.Expr, index int) string {
	switch n := expr.(type) {
	case *ast.Ident:
		if n.Name != "_" {
			return lowerCamel(n.Name)
		}
	case *ast.SelectorExpr:
		return lowerCamel(n.Sel.Name)
	case *ast.ParenExpr:
		return argumentName(n.X, index)
	case *ast.CallExpr:
		if ident, ok := n.Fun.(*ast.Ident); ok && ident.Name == "len" && ident.Obj == nil && len(n.Args) == 1 {
			return "count"
		}
	}
	return fmt.Sprintf("p%d", index)
}
func lowerCamel(s string) string {
	runes := []rune(s)
	for i := 0; i < len(runes) && unicode.IsUpper(runes[i]); i++ {
		if i > 0 && i+1 < len(runes) && unicode.IsLower(runes[i+1]) {
			break
		}
		runes[i] = unicode.ToLower(runes[i])
	}
	return string(runes)
}
