package main

import (
	"bytes"
	"encoding/json"
	"go/ast"
	"go/format"
	"go/parser"
	"go/token"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestApplyImportGroups(t *testing.T) {
	for _, tc := range []struct{ name, imports, body, want string }{
		{"module group", "import (\n\"fmt\"\n\n// Local dependencies.\n_ \"skillmanager/aaa\"\n_ \"skillmanager/zzz\" // Keep trailing comment.\n)\n", `fmt.Println("标题")`, "\"skillmanager/aaa\"\n\t\"skillmanager/locales\"\n\t_ \"skillmanager/zzz\""},
		{"standard group", "import (\n\"fmt\"\n)\n", `fmt.Println("标题")`, "\"fmt\"\n\n\t\"skillmanager/locales\""},
		{"single import", "import \"fmt\"\n", `fmt.Println("标题")`, "\"fmt\"\n\n\t\"skillmanager/locales\""},
		{"no imports", "", `println("标题")`, `"skillmanager/locales"`},
		{"aliased new import", "import (\n\"fmt\"\n)\n", `locales := 1; fmt.Println(locales, "标题")`, "\"fmt\"\n\n\ti18nlocales2 \"skillmanager/locales\""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			source, catalog, plan := fixture(t, "package sample\n"+tc.imports+"func f() { "+tc.body+" }\n")
			if err := apply(t, source, catalog, plan); err != nil {
				t.Fatal(err)
			}
			got := read(t, source)
			if !strings.Contains(got, tc.want) {
				t.Fatalf("want group %q, got:\n%s", tc.want, got)
			}
			tree, err := parser.ParseFile(token.NewFileSet(), source, got, parser.ParseComments)
			if err != nil {
				t.Fatal(err)
			}
			count := 0
			for _, decl := range tree.Decls {
				if d, ok := decl.(*ast.GenDecl); ok && d.Tok == token.IMPORT {
					count++
				}
			}
			if count != 1 {
				t.Fatalf("expected one import declaration, got %d:\n%s", count, got)
			}
			formatted, err := format.Source([]byte(got))
			if err != nil || !bytes.Equal(formatted, []byte(got)) {
				t.Fatal("not gofmt clean")
			}
			if tc.name == "module group" && (!strings.Contains(got, "// Local dependencies.") || !strings.Contains(got, "// Keep trailing comment.")) {
				t.Fatal(got)
			}
		})
	}
}

func TestScanErrorsGolden(t *testing.T) {
	got, err := call(t, "scan", "-prefix", "error", "testdata/errors.go")
	if err != nil {
		t.Fatal(err)
	}
	want := read(t, "testdata/errors.golden.json")
	if string(got) != want {
		t.Fatalf("error scan differs:\n%s", got)
	}
}

func TestApplyErrorsPreservesWrapping(t *testing.T) {
	source, catalog, plan := fixture(t, `package sample
import f "fmt"
import e "errors"
var sentinel = e.New("sentinel")
func plain() error { return e.New("失败") }
func formatted(n int) error { return f.Errorf("失败 %d", n) }
func wrapped(n int) error { return f.Errorf("失败 %d: %w", n, sentinel) }
`)
	if err := apply(t, source, catalog, plan); err != nil {
		t.Fatal(err)
	}
	got := read(t, source)
	for _, want := range []string{`e.New(locales.TestMsg001())`, `e.New(locales.TestMsg002(n))`, `f.Errorf("%s%w", locales.TestMsg003(n), sentinel)`} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %s:\n%s", want, got)
		}
	}
	write(t, filepath.Join(catalog, "locales.go"), `package locales
import "fmt"
func TestMsg001() string { return "失败" }
func TestMsg002(n int) string { return fmt.Sprintf("失败 %d", n) }
func TestMsg003(n int) string { return fmt.Sprintf("失败 %d: ", n) }
`)
	write(t, filepath.Join(filepath.Dir(source), "runtime_test.go"), `package sample
import ("errors"; "testing")
func TestErrors(t *testing.T) {
 if plain().Error() != "失败" || formatted(2).Error() != "失败 2" { t.Fatal("incorrect localized error") }
 err:=wrapped(3)
 if !errors.Is(err,sentinel) || err.Error()!="失败 3: sentinel" { t.Fatal(err) }
}
`)
	cmd := exec.Command("go", "test", "./...")
	cmd.Dir = filepath.Dir(source)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("rewritten errors: %v\n%s", err, out)
	}
}

func TestApplyErrorfAddsErrorsAndRemovesFmt(t *testing.T) {
	source, catalog, plan := fixture(t, "package sample\nimport \"fmt\"\nfunc f(n int) error {return fmt.Errorf(\"失败 %d\",n)}\n")
	if err := apply(t, source, catalog, plan); err != nil {
		t.Fatal(err)
	}
	got := read(t, source)
	if strings.Contains(got, `"fmt"`) || !strings.Contains(got, `"errors"`) || !strings.Contains(got, `errors.New(locales.TestMsg001(n))`) {
		t.Fatal(got)
	}
	if !strings.Contains(got, "\"errors\"\n\n\t\"skillmanager/locales\"") {
		t.Fatal(got)
	}
}

func TestApplyRejectsChangedWrappedArgument(t *testing.T) {
	source, catalog, plan := fixture(t, "package sample\nimport \"fmt\"\nfunc f(err error) error {return fmt.Errorf(\"失败: %w\",err)}\n")
	plan[0]["wrapped"] = "differentError"
	before := read(t, source)
	if err := apply(t, source, catalog, plan); err == nil {
		t.Fatal("changed wrapped argument accepted")
	}
	if read(t, source) != before {
		t.Fatal("changed source on conflict")
	}
}

func TestScanEscapedWrapIsNotWrapping(t *testing.T) {
	source, _, _ := fixture(t, "package sample\nimport \"fmt\"\nfunc f() error {return fmt.Errorf(\"失败 %%w\")}\n")
	data, err := call(t, "scan", source)
	if err != nil {
		t.Fatal(err)
	}
	var got []map[string]any
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}
	if got[0]["kind"] != "errorf" || got[0]["zh"] != "失败 %w" || got[0]["wrapped"] != nil {
		t.Fatal(string(data))
	}
}

func TestApplyErrorfImportAliasCompiles(t *testing.T) {
	source, catalog, plan := fixture(t, `package sample
import "fmt"
func f(errors string) error { return fmt.Errorf("失败 %s", errors) }
`)
	if err := apply(t, source, catalog, plan); err != nil {
		t.Fatal(err)
	}
	got := read(t, source)
	for _, want := range []string{`i18nerrors2 "errors"`, `i18nerrors2.New(locales.TestMsg001(errors))`} {
		if !strings.Contains(got, want) {
			t.Fatal(got)
		}
	}
	write(t, filepath.Join(catalog, "locales.go"), "package locales\nfunc TestMsg001(errors string) string {return \"失败 \"+errors}\n")
	write(t, filepath.Join(filepath.Dir(source), "runtime_test.go"), "package sample\nimport \"testing\"\nfunc TestError(t *testing.T) {if f(\"value\").Error()!=\"失败 value\" {t.Fatal(\"wrong error\")}}\n")
	cmd := exec.Command("go", "test", "./...")
	cmd.Dir = filepath.Dir(source)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("rewritten aliased errors: %v\n%s", err, out)
	}
}

func TestApplyNestedErrorWrappers(t *testing.T) {
	source, catalog, plan := fixture(t, `package sample
import "fmt"
import "errors"
func f() error { return fmt.Errorf("外层: %w", errors.New("内层")) }
`)
	if err := apply(t, source, catalog, plan); err != nil {
		t.Fatal(err)
	}
	got := read(t, source)
	if !strings.Contains(got, `fmt.Errorf("%s%w", locales.TestMsg001(), errors.New(locales.TestMsg002()))`) {
		t.Fatal(got)
	}
}
