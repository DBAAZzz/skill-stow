package main

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/format"
	"go/parser"
	"go/token"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"unicode"
)

const catalogImport = "skillmanager/locales"

type replacement struct {
	site site
	item entry
}
type fileChanges struct {
	source *sourceFile
	edits  []replacement
}
type pendingFile struct {
	path string
	data []byte
}

func functionName(key string) (string, error) {
	if !token.IsIdentifier(key) || key == "_" {
		return "", fmt.Errorf("invalid catalog key %q: expected a Go identifier", key)
	}
	runes := []rune(key)
	runes[0] = unicode.ToUpper(runes[0])
	if !unicode.IsUpper(runes[0]) {
		return "", fmt.Errorf("catalog key %q cannot produce an exported function", key)
	}
	return string(runes), nil
}
func applyEntries(entries []entry, catalogDir string, stdout io.Writer) error {
	zhPath := filepath.Join(catalogDir, "zh-Hans.json")
	enPath := filepath.Join(catalogDir, "en.json")
	zh, err := loadCatalog(zhPath, "zh-Hans")
	if err != nil {
		return err
	}
	en, err := loadCatalog(enPath, "en")
	if err != nil {
		return err
	}
	names := map[string]string{}
	for _, field := range zh {
		if strings.HasPrefix(field.name, "@") {
			continue
		}
		name, err := functionName(field.name)
		if err != nil {
			return err
		}
		if key, ok := names[name]; ok && key != field.name {
			return fmt.Errorf("keys %q and %q generate the same function", key, field.name)
		}
		names[name] = field.name
	}
	changes := map[string]*fileChanges{}
	order := []string{}
	seen := map[string]bool{}
	newKeys := 0
	newEnglish := 0
	for _, item := range entries {
		if item.Kind == "manual" || item.Kind == "skip" {
			continue
		}
		if item.Kind != "literal" && item.Kind != "sprintf" && item.Kind != "errorsNew" && item.Kind != "errorf" {
			return fmt.Errorf("%s: unknown kind %q", item.ID, item.Kind)
		}
		name, err := functionName(item.Key)
		if err != nil {
			return err
		}
		if key, ok := names[name]; ok && key != item.Key {
			return fmt.Errorf("keys %q and %q generate the same function", key, item.Key)
		}
		names[name] = item.Key
		phNames := map[string]bool{}
		for _, p := range item.Placeholders {
			if !token.IsIdentifier(p.Name) || p.Name == "_" || phNames[p.Name] {
				return fmt.Errorf("%s: invalid or duplicate placeholder %q", item.ID, p.Name)
			}
			phNames[p.Name] = true
			if p.Type != "int" && p.Type != "String" && p.Type != "double" {
				return fmt.Errorf("%s: invalid placeholder type %q", item.ID, p.Type)
			}
		}
		path, err := filepath.Abs(item.File)
		if err != nil {
			return err
		}
		if skippedFile(path) {
			return fmt.Errorf("%s: test/generated files cannot be applied", item.ID)
		}
		changesForFile, ok := changes[path]
		if !ok {
			source, err := parseSource(item.File)
			if err != nil {
				return err
			}
			changesForFile = &fileChanges{source: source}
			changes[path] = changesForFile
			order = append(order, path)
		}
		id := fmt.Sprintf("%s:%d:%d", item.File, item.Line, item.Col)
		if item.ID != id {
			return fmt.Errorf("%s: id does not match file/line/col", item.ID)
		}
		siteID := fmt.Sprintf("%s:%d:%d", path, item.Line, item.Col)
		if seen[siteID] {
			return fmt.Errorf("%s: duplicate plan location", item.ID)
		}
		seen[siteID] = true
		var found *site
		for i := range changesForFile.source.sites {
			candidate := &changesForFile.source.sites[i]
			if candidate.entry.Line == item.Line && candidate.entry.Col == item.Col {
				found = candidate
				break
			}
		}
		if found == nil || found.entry.GoText != item.GoText || found.entry.Kind != item.Kind {
			return fmt.Errorf("%s: source changed or classification is unsafe; scan again", item.ID)
		}
		if !reflect.DeepEqual(item.Args, found.entry.Args) || !reflect.DeepEqual(item.Placeholders, found.entry.Placeholders) || item.Zh != found.entry.Zh || item.Wrapped != found.entry.Wrapped {
			return fmt.Errorf("%s: source arguments, wrapped error, placeholders or zh differ from scan; only edit key/en/description/kind", item.ID)
		}
		added, err := mergeMessage(&zh, item, item.Zh, true)
		if err != nil {
			return err
		}
		if added {
			newKeys++
		}
		if item.En != nil {
			added, err := mergeMessage(&en, item, *item.En, false)
			if err != nil {
				return err
			}
			if added {
				newEnglish++
			}
		}
		changesForFile.edits = append(changesForFile.edits, replacement{*found, item})
	}
	if len(order) == 0 {
		_, err := fmt.Fprintln(stdout, "No source replacements; added 0 keys.")
		return err
	}
	pending := []pendingFile{}
	for _, path := range order {
		change := changes[path]
		rewritten, err := rewriteSource(change)
		if err != nil {
			return fmt.Errorf("%s: %w", path, err)
		}
		pending = append(pending, pendingFile{path, rewritten})
	}
	zhData, err := zh.encoded()
	if err != nil {
		return err
	}
	enData, err := en.encoded()
	if err != nil {
		return err
	}
	pending = append(pending, pendingFile{zhPath, zhData}, pendingFile{enPath, enData})
	// Validation and formatting are deliberately finished before any filesystem writes.
	if err := writePrepared(pending); err != nil {
		return err
	}
	for _, path := range order {
		fmt.Fprintf(stdout, "%s: replaced %d occurrences\n", changes[path].source.path, len(changes[path].edits))
	}
	fmt.Fprintf(stdout, "Added %d source keys, %d English translations.\n", newKeys, newEnglish)
	return nil
}

func rewriteSource(change *fileChanges) ([]byte, error) {
	s := change.source
	alias, addImport, err := importAlias(s.tree, catalogImport, "locales")
	if err != nil {
		return nil, err
	}
	errorsAlias, addErrors := "", false
	for _, edit := range change.edits {
		if edit.item.Kind == "errorf" && edit.item.Wrapped == "" {
			errorsAlias, addErrors, err = importAlias(s.tree, "errors", "errors")
			if err != nil {
				return nil, err
			}
			break
		}
	}
	sort.Slice(change.edits, func(i, j int) bool {
		a, b := change.edits[i].site, change.edits[j].site
		if a.start == b.start {
			return a.end > b.end
		}
		return a.start < b.start
	})
	// Nested strings in Sprintf arguments are rendered recursively, so an outer
	// replacement never discards an inner migration.
	for i, outer := range change.edits {
		for _, inner := range change.edits[i+1:] {
			if inner.site.start >= outer.site.end {
				break
			}
			valid := false
			if outer.site.call != nil {
				for _, arg := range outer.site.call.Args[1:] {
					if s.offset(arg.Pos()) <= inner.site.start && s.offset(arg.End()) >= inner.site.end {
						valid = true
						break
					}
				}
			}
			if !valid {
				return nil, fmt.Errorf("overlapping replacements at %s and %s", outer.item.ID, inner.item.ID)
			}
		}
	}
	var render func(int, int) string
	render = func(start, end int) string {
		result := string(s.data[start:end])
		// Find the outermost edits in this interval, then apply byte offsets backwards.
		edits := []replacement{}
		after := start
		for _, edit := range change.edits {
			if edit.site.start < after || edit.site.end > end {
				continue
			}
			edits = append(edits, edit)
			after = edit.site.end
		}
		for i := len(edits) - 1; i >= 0; i-- {
			edit := edits[i]
			args := []string{}
			if edit.site.call != nil {
				messageArgs := edit.site.call.Args[1:]
				if edit.item.Wrapped != "" {
					messageArgs = messageArgs[:len(messageArgs)-1]
				}
				for _, arg := range messageArgs {
					args = append(args, render(s.offset(arg.Pos()), s.offset(arg.End())))
				}
			}
			name, _ := functionName(edit.item.Key)
			text := alias + "." + name + "(" + strings.Join(args, ", ") + ")"
			if edit.item.Kind == "errorf" {
				if edit.item.Wrapped == "" {
					text = errorsAlias + ".New(" + text + ")"
				} else {
					wrapped := edit.site.call.Args[len(edit.site.call.Args)-1]
					text = s.text(edit.site.call.Fun) + "(\"%s%w\", " + text + ", " + render(s.offset(wrapped.Pos()), s.offset(wrapped.End())) + ")"
				}
			}
			left := edit.site.start - start
			right := edit.site.end - start
			result = result[:left] + text + result[right:]
		}
		return result
	}
	rewritten := []byte(render(0, len(s.data)))
	fset := token.NewFileSet()
	tree, err := parser.ParseFile(fset, s.path, rewritten, parser.ParseComments)
	if err != nil {
		return nil, err
	}
	removeUnusedFmt(tree)

	var buf bytes.Buffer
	if err := format.Node(&buf, fset, tree); err != nil {
		return nil, err
	}
	data, err := format.Source(buf.Bytes())
	if err != nil {
		return nil, err
	}
	if addErrors {
		data, err = insertImport(data, "errors", errorsAlias)
		if err != nil {
			return nil, err
		}
	}
	if addImport {
		data, err = insertImport(data, catalogImport, alias)
		if err != nil {
			return nil, err
		}
	}
	return format.Source(data)
}
func importAlias(tree *ast.File, importPath, defaultAlias string) (string, bool, error) {
	existing := ""
	for _, spec := range tree.Imports {
		path, _ := strconv.Unquote(spec.Path.Value)
		if path == importPath {
			existing = defaultAlias
			if spec.Name != nil {
				existing = spec.Name.Name
			}
			if existing == "." || existing == "_" {
				return "", false, fmt.Errorf("%s import uses %q; give it a named import before applying", importPath, existing)
			}
		}
	}
	occupied := map[string]bool{}
	ast.Inspect(tree, func(node ast.Node) bool {
		if ident, ok := node.(*ast.Ident); ok {
			occupied[ident.Name] = true
		}
		return true
	})
	if existing != "" {
		shadowed := false
		ast.Inspect(tree, func(node ast.Node) bool {
			if ident, ok := node.(*ast.Ident); ok && ident.Name == existing && ident.Obj != nil {
				shadowed = true
			}
			return true
		})
		if shadowed {
			return "", false, fmt.Errorf("%s import alias %q is shadowed; rename the local binding before applying", importPath, existing)
		}
		return existing, false, nil
	}
	alias := defaultAlias
	for suffix := 2; occupied[alias]; suffix++ {
		alias = fmt.Sprintf("i18n%s%d", defaultAlias, suffix)
	}
	return alias, true, nil
}
func removeUnusedFmt(tree *ast.File) {
	remove := map[*ast.ImportSpec]bool{}
	for _, spec := range tree.Imports {
		path, _ := strconv.Unquote(spec.Path.Value)
		if path != "fmt" {
			continue
		}
		alias := "fmt"
		if spec.Name != nil {
			alias = spec.Name.Name
		}
		if alias == "_" || alias == "." {
			continue
		}
		used := false
		ast.Inspect(tree, func(node ast.Node) bool {
			if sel, ok := node.(*ast.SelectorExpr); ok {
				if ident, ok := sel.X.(*ast.Ident); ok && ident.Name == alias && ident.Obj == nil {
					used = true
				}
			}
			return true
		})
		if !used {
			remove[spec] = true
		}
	}
	declarations := []ast.Decl{}
	for _, decl := range tree.Decls {
		if group, ok := decl.(*ast.GenDecl); ok && group.Tok == token.IMPORT {
			specs := []ast.Spec{}
			for _, spec := range group.Specs {
				if !remove[spec.(*ast.ImportSpec)] {
					specs = append(specs, spec)
				}
			}
			group.Specs = specs
			if len(specs) == 0 {
				continue
			}
		}
		declarations = append(declarations, decl)
	}
	tree.Decls = declarations
}
func writePrepared(files []pendingFile) error {
	// Stage all bytes first: permission/disk errors during staging cannot alter sources.
	temporary := make([]string, len(files))
	defer func() {
		for _, name := range temporary {
			if name != "" {
				os.Remove(name)
			}
		}
	}()
	for i, file := range files {
		if err := os.MkdirAll(filepath.Dir(file.path), 0755); err != nil {
			return err
		}
		mode := os.FileMode(0644)
		if info, err := os.Stat(file.path); err == nil {
			mode = info.Mode().Perm()
		} else if !os.IsNotExist(err) {
			return err
		}
		f, err := os.CreateTemp(filepath.Dir(file.path), ".i18nextract-*")
		if err != nil {
			return err
		}
		temporary[i] = f.Name()
		if err := f.Chmod(mode); err != nil {
			f.Close()
			return err
		}
		if _, err := f.Write(file.data); err != nil {
			f.Close()
			return err
		}
		if err := f.Close(); err != nil {
			return err
		}
	}
	for i, file := range files {
		if err := os.Rename(temporary[i], file.path); err != nil {
			return fmt.Errorf("writing %s (earlier files may already be applied): %w", file.path, err)
		}
		temporary[i] = ""
	}
	return nil
}
