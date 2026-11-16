package main

import (
	"bytes"
	"encoding/json"
	"go/format"
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func call(t *testing.T, args ...string) ([]byte, error) {
	t.Helper()
	var out, diagnostics bytes.Buffer
	err := run(args, &out, &diagnostics)
	return out.Bytes(), err
}
func TestScanGolden(t *testing.T) {
	got, err := call(t, "scan", "-prefix", "usage", "testdata/scan.go", "testdata/ignored_test.go", "testdata/ignored_gen.go")
	if err != nil {
		t.Fatal(err)
	}
	want, err := os.ReadFile("testdata/scan.golden.json")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("scan differs from golden:\n%s", got)
	}
}
func fixture(t *testing.T, src string) (string, string, []map[string]any) {
	t.Helper()
	dir := t.TempDir()
	source := filepath.Join(dir, "sample.go")
	write(t, source, src)
	write(t, filepath.Join(dir, "go.mod"), "module skillmanager\n\ngo 1.23\n")
	catalog := filepath.Join(dir, "locales")
	if err := os.Mkdir(catalog, 0755); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(catalog, "zh-Hans.json"), `{"@@locale":"zh-Hans","zOld":"旧文案","@zOld":{"description":"旧描述"},"aOld":"较早"}`)
	write(t, filepath.Join(catalog, "en.json"), `{"@@locale":"en","zOld":"Old","aOld":"Earlier"}`)
	data, err := call(t, "scan", "-prefix", "test", source)
	if err != nil {
		t.Fatal(err)
	}
	var plan []map[string]any
	if err := json.Unmarshal(data, &plan); err != nil {
		t.Fatal(err)
	}
	return source, catalog, plan
}
func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
}
func apply(t *testing.T, source, catalog string, plan []map[string]any) error {
	t.Helper()
	data, err := json.Marshal(plan)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(filepath.Dir(source), "plan.json")
	write(t, path, string(data))
	_, err = call(t, "apply", "-plan", path, "-catalog", catalog)
	return err
}
func read(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}
func TestApplyRewritesCallsAndPreservesCatalogOrder(t *testing.T) {
	source, catalog, plan := fixture(t, "package sample\nimport \"fmt\"\nfunc text(n int, name string) string {\n _ = \"你好 <&>\"\n return fmt.Sprintf(\"已选 %d 个 %s\", n, name)\n}\n")
	plan[0]["en"] = "Hello <&>"
	plan[0]["description"] = "问候"
	plan[1]["en"] = "Picked {n} items {name}"
	if err := apply(t, source, catalog, plan); err != nil {
		t.Fatal(err)
	}
	got := read(t, source)
	if _, err := parser.ParseFile(token.NewFileSet(), source, got, parser.AllErrors); err != nil {
		t.Fatal(err)
	}
	formatted, err := format.Source([]byte(got))
	if err != nil || string(formatted) != got {
		t.Fatalf("not gofmt clean: %v", err)
	}
	for _, want := range []string{`"skillmanager/locales"`, `locales.TestMsg001()`, `locales.TestMsg002(n, name)`} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %s in %s", want, got)
		}
	}
	if strings.Contains(got, `"fmt"`) {
		t.Fatal("unused fmt import retained")
	}
	zh := read(t, filepath.Join(catalog, "zh-Hans.json"))
	last := -1
	for _, key := range []string{`"@@locale"`, `"zOld"`, `"@zOld"`, `"aOld"`, `"testMsg001"`, `"@testMsg001"`, `"testMsg002"`, `"@testMsg002"`} {
		pos := strings.Index(zh, key)
		if pos <= last {
			t.Fatalf("catalog order wrong: %s", zh)
		}
		last = pos
	}
	if !strings.Contains(zh, "你好 <&>") || !strings.HasSuffix(zh, "\n") {
		t.Fatal(zh)
	}
	if en := read(t, filepath.Join(catalog, "en.json")); !strings.Contains(en, "Hello <&>") {
		t.Fatal(en)
	}
	write(t, filepath.Join(catalog, "locales.go"), "package locales\nfunc TestMsg001() string { return \"\" }\nfunc TestMsg002(n int, name string) string { return \"\" }\n")
	cmd := exec.Command("go", "test", "./...")
	cmd.Dir = filepath.Dir(source)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("rewritten module: %v\n%s", err, output)
	}
}
func TestApplyConflictsDoNotWriteAnyFiles(t *testing.T) {
	for _, conflict := range []string{"existing text", "existing placeholders", "plan key", "english", "stale source"} {
		t.Run(conflict, func(t *testing.T) {
			source, catalog, plan := fixture(t, "package sample\nfunc f() { println(\"新的\", \"另一条\") }\n")
			switch conflict {
			case "existing text":
				plan[1]["key"] = "zOld"
			case "existing placeholders":
				plan[1]["key"] = "zOld"
				write(t, filepath.Join(catalog, "zh-Hans.json"), `{"zOld":"另一条","@zOld":{"placeholders":{"n":{"type":"int"}}}}`)
			case "plan key":
				plan[1]["key"] = plan[0]["key"]
			case "english":
				plan[1]["key"] = "zOld"
				plan[1]["en"] = "Changed"
				write(t, filepath.Join(catalog, "zh-Hans.json"), `{"zOld":"另一条"}`)
			case "stale source":
				write(t, source, "package sample\nfunc f() { println(\"改了\", \"另一条\") }\n")
			}
			paths := []string{source, filepath.Join(catalog, "zh-Hans.json"), filepath.Join(catalog, "en.json")}
			before := make([]string, len(paths))
			for i, p := range paths {
				before[i] = read(t, p)
			}
			if err := apply(t, source, catalog, plan); err == nil {
				t.Fatal("expected conflict")
			}
			for i, p := range paths {
				if read(t, p) != before[i] {
					t.Fatalf("conflict wrote %s", p)
				}
			}
		})
	}
}
func TestApplySkipAndManualRemainUntouched(t *testing.T) {
	source, catalog, plan := fixture(t, "package sample\nimport \"fmt\"\nconst c = \"常量\"\nfunc f(n int) { fmt.Println(\"跳过\"); _ = fmt.Sprintf(\"共 %d\", n) }\n")
	plan[1]["kind"] = "skip"
	if err := apply(t, source, catalog, plan); err != nil {
		t.Fatal(err)
	}
	got := read(t, source)
	for _, want := range []string{`"常量"`, `"跳过"`, `"fmt"`, `locales.TestMsg003(n)`} {
		if !strings.Contains(got, want) {
			t.Fatal(got)
		}
	}
	if en := read(t, filepath.Join(catalog, "en.json")); strings.Contains(en, "testMsg") {
		t.Fatal(en)
	}
}

func TestApplyNestedExpressionsAndAliasedImports(t *testing.T) {
	source, catalog, plan := fixture(t, "package sample\nimport f \"fmt\"\n// Keep this function comment.\nfunc text(n int) string { return f.Sprintf(\"外层 %s\", f.Sprintf(\"内层 %s %d\", \"名字\", n)) }\n")
	if err := apply(t, source, catalog, plan); err != nil {
		t.Fatal(err)
	}
	got := read(t, source)
	if !strings.Contains(got, `locales.TestMsg001(locales.TestMsg002(locales.TestMsg003(), n))`) {
		t.Fatal(got)
	}
	if strings.Contains(got, `"fmt"`) || !strings.Contains(got, "// Keep this function comment.") {
		t.Fatal(got)
	}
	write(t, filepath.Join(catalog, "locales.go"), "package locales\nfunc TestMsg001(p0 string) string { return p0 }\nfunc TestMsg002(p0 string, n int) string { return p0 }\nfunc TestMsg003() string { return \"\" }\n")
	cmd := exec.Command("go", "test", "./...")
	cmd.Dir = filepath.Dir(source)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("rewritten nested module: %v\n%s", err, output)
	}
}

func TestApplyExistingLocaleAliasAndNameCollision(t *testing.T) {
	for _, tc := range []struct{ name, source, call string }{
		{"existing alias", "package sample\nimport loc \"skillmanager/locales\"\nfunc f() { _ = loc.Existing(); println(\"标题\") }\n", "loc.TestMsg001()"},
		{"local binding", "package sample\nfunc f() { locales := 1; println(locales, \"标题\") }\n", "i18nlocales2.TestMsg001()"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			source, catalog, plan := fixture(t, tc.source)
			if err := apply(t, source, catalog, plan); err != nil {
				t.Fatal(err)
			}
			got := read(t, source)
			if !strings.Contains(got, tc.call) || strings.Count(got, `"skillmanager/locales"`) != 1 {
				t.Fatal(got)
			}
		})
	}
}

func TestApplyCatalogPlaceholderOrderMatters(t *testing.T) {
	source, catalog, plan := fixture(t, "package sample\nimport \"fmt\"\nfunc f(a, b string) string { return fmt.Sprintf(\"名称 %s %s\", a, b) }\n")
	plan[0]["key"] = "existing"
	write(t, filepath.Join(catalog, "zh-Hans.json"), `{"existing":"名称 {a} {b}","@existing":{"placeholders":{"b":{"type":"String"},"a":{"type":"String"}}}}`)
	before := read(t, source)
	if err := apply(t, source, catalog, plan); err == nil {
		t.Fatal("expected placeholder order conflict")
	}
	if read(t, source) != before {
		t.Fatal("conflicting plan wrote source")
	}
}

func TestApplyEnglishPlaceholderConflictIsAtomic(t *testing.T) {
	source, catalog, plan := fixture(t, "package sample\nimport \"fmt\"\nfunc f(n int) string { return fmt.Sprintf(\"共 %d\", n) }\n")
	plan[0]["en"] = "Total {n}"
	write(t, filepath.Join(catalog, "en.json"), `{"testMsg001":"Total {n}","@testMsg001":{"placeholders":{"n":{"type":"String"}}}}`)
	before := read(t, source)
	if err := apply(t, source, catalog, plan); err == nil {
		t.Fatal("expected English placeholder conflict")
	}
	if read(t, source) != before {
		t.Fatal("conflict wrote source")
	}
}

func TestScanFormatNamingAndEscapes(t *testing.T) {
	dir := t.TempDir()
	source := filepath.Join(dir, "names.go")
	write(t, source, "package sample\nimport \"fmt\"\nfunc f(HTTPStatus, count, count2 int, xs []string) {\n _ = fmt.Sprintf(\"计数 %d %d %d %d\", HTTPStatus, count, len(xs), count2)\n _ = fmt.Sprintf(\"进度 100%% {'值'}\")\n _ = fmt.Sprintf(\"变参 %s\", xs...)\n}\n")
	data, err := call(t, "scan", "-prefix", "fmt", source)
	if err != nil {
		t.Fatal(err)
	}
	var got []entry
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Fatal(string(data))
	}
	if got[0].Zh != "计数 {httpStatus} {count} {count2} {count22}" {
		t.Fatal(string(data))
	}
	if got[1].Zh != "进度 100% '{'''值'''}'" || got[1].Kind != "sprintf" {
		t.Fatal(string(data))
	}
	if got[2].Kind != "manual" || !strings.Contains(got[2].Reason, "variadic") {
		t.Fatal(string(data))
	}
}

func TestScanReusesKeysAcrossFilesAndSkipsGenerated(t *testing.T) {
	dir := t.TempDir()
	a := filepath.Join(dir, "a.go")
	b := filepath.Join(dir, "b.go")
	write(t, a, "package sample\nfunc a() { println(\"共同文案\") }\n")
	write(t, b, "package sample\nfunc b() { println(\"共同文案\") }\n")
	data, err := call(t, "scan", "-prefix", "shared", a, b, a, filepath.Join(dir, "nonexistent_gen.go"))
	if err != nil {
		t.Fatal(err)
	}
	var got []entry
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Key != "sharedMsg001" || got[1].Key != "sharedMsg001" {
		t.Fatal(string(data))
	}
}

func TestApplyRejectsUnsafePlanEdits(t *testing.T) {
	for _, field := range []string{"kind", "key", "id", "args", "zh", "placeholders"} {
		t.Run(field, func(t *testing.T) {
			source, catalog, plan := fixture(t, "package sample\nconst c = \"常量\"\nfunc f() { println(\"标题\") }\n")
			switch field {
			case "kind":
				plan[0][field] = "literal"
			case "key":
				plan[1][field] = "not-a-go-name"
			case "id":
				plan[1][field] = "wrong"
			case "args":
				plan[1][field] = []string{"unexpected()"}
			case "zh":
				plan[1][field] = "different"
			case "placeholders":
				plan[1][field] = []map[string]string{{"name": "p0", "type": "int"}}
			}
			before := read(t, source)
			if err := apply(t, source, catalog, plan); err == nil {
				t.Fatal("unsafe plan accepted")
			}
			if read(t, source) != before {
				t.Fatal("unsafe plan wrote source")
			}
		})
	}
}

func TestScanMapLookupAndSwitchTagStayManual(t *testing.T) {
	dir := t.TempDir()
	source := filepath.Join(dir, "keys.go")
	write(t, source, "package sample\nfunc f(m map[string]string) { _ = m[\"映射键\"]; switch \"判断值\" { case m[\"另一个键\"]: println(\"显示\") } }\n")
	data, err := call(t, "scan", source)
	if err != nil {
		t.Fatal(err)
	}
	var got []entry
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}
	if len(got) != 4 {
		t.Fatal(string(data))
	}
	for _, item := range got[:3] {
		if item.Kind != "manual" {
			t.Fatalf("semantic key changed: %s", data)
		}
	}
	if got[3].Kind != "literal" {
		t.Fatal(string(data))
	}
}

func TestGenerateRunsInCatalogParentAfterWriting(t *testing.T) {
	source, catalog, plan := fixture(t, "package sample\nfunc f() { println(\"生成\") }\n")
	data, err := json.Marshal(plan)
	if err != nil {
		t.Fatal(err)
	}
	planPath := filepath.Join(filepath.Dir(source), "plan.json")
	write(t, planPath, string(data))
	bin := t.TempDir()
	logPath := filepath.Join(bin, "generator.log")
	// A process-boundary test double records the executable contract, then fails.
	script := "#!/bin/sh\npwd > \"$I18NEXTRACT_TEST_LOG\"\nprintf '%s\\n' \"$@\" >> \"$I18NEXTRACT_TEST_LOG\"\nexit 3\n"
	goPath := filepath.Join(bin, "go")
	write(t, goPath, script)
	if err := os.Chmod(goPath, 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("I18NEXTRACT_TEST_LOG", logPath)
	_, err = call(t, "apply", "-plan", planPath, "-catalog", catalog, "-generate")
	if err == nil || !strings.Contains(err.Error(), "files applied, but generation failed") {
		t.Fatalf("expected post-apply failure, got %v", err)
	}
	if !strings.Contains(read(t, source), "locales.TestMsg001()") {
		t.Fatal("generate ran before source changes")
	}
	got := strings.Split(strings.TrimSpace(read(t, logPath)), "\n")
	parent, err := filepath.EvalSymlinks(filepath.Dir(source))
	if err != nil {
		t.Fatal(err)
	}
	actualParent, err := filepath.EvalSymlinks(got[0])
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 4 || actualParent != parent || strings.Join(got[1:], " ") != "tool mygo generate" {
		t.Fatalf("wrong generator invocation: %v", got)
	}
}
