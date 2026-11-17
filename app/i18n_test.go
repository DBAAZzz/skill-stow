package main

import (
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"unicode"

	"github.com/egoist/mygo/i18n"
	"github.com/egoist/mygo/ui"
)

// testLocale 是测试里界面的语言；空为源语言（简体中文），和开发机的系统语言
// 无关——不然系统是英文的机器上，按中文找按钮的测试全会失败。
var testLocale atomic.Value

func TestMain(m *testing.M) {
	testLocale.Store("")
	i18n.SetLocaleFunc(func() string { return testLocale.Load().(string) })
	applyLanguage = func(id string) { testLocale.Store(id) }
	applyTheme = func(string) {} // 真的换外观要 mygo 在跑，测试里不碰系统
	os.Exit(m.Run())
}

// useLocale 让这个测试的界面用 tag 的语言（mygo 自己的控件也跟着换），
// 测试结束换回源语言。
func useLocale(t *testing.T, tt *ui.Tester, tag string) {
	t.Helper()
	testLocale.Store(tag)
	tt.SetLocale(tag)
	t.Cleanup(func() { testLocale.Store("") })
}

func TestUserSwitchesTheInterfaceToEnglishInSettings(t *testing.T) {
	// Given 在设置里，界面是中文
	a, _ := newTester(t)
	t.Cleanup(func() { testLocale.Store("") })
	st := newUITester(a.settingsView, settingsWidth, settingsHeight)

	// When 在「语言」里选 English
	clickMenuButton(t, st, "语言")
	clickMenuItem(t, st, "English")
	st.Frame()

	// Then 存进设置，下一帧就用英文说：选择器的名字和「跟随系统」都换了
	if a.lib.Settings.Language != "en" {
		t.Errorf("应存下 en：%q", a.lib.Settings.Language)
	}
	clickMenuButton(t, st, "Language")
	if !st.HasText("System Default") {
		t.Errorf("语言菜单应换成英文：%q", st.Texts())
	}
}

func TestSystemLanguagePicksChineseEnglishOrFallsBackToEnglish(t *testing.T) {
	// mygo 在系统语言都对不上时用 appLanguages 的第一个
	shown := func(preferred ...string) string {
		if m := i18n.Match(preferred, appLanguages); m != "" {
			return m
		}
		return appLanguages[0]
	}
	for _, c := range []struct {
		preferred []string
		want      string
	}{
		{[]string{"zh-Hans-CN", "en-US"}, "zh-Hans"},
		{[]string{"en-GB"}, "en"},
		{[]string{"de-DE"}, "en"},
		{[]string{"ja-JP", "zh-Hans-CN"}, "zh-Hans"},
	} {
		if got := shown(c.preferred...); got != c.want {
			t.Errorf("系统语言 %v 应显示 %s，实际 %s", c.preferred, c.want, got)
		}
	}
}

// ——— 架构约束 ———

// i18nExempt 是不算界面文案的文件：里面的中文是数据，不是给人看的界面。
var i18nExempt = map[string]string{
	"internal/library/sample.go": "示例数据，模拟用户自己的技能名和简介",
}

// cjk 报告 s 里有没有要翻译的中文：汉字、中文标点、全角字符。
func cjk(s string) bool {
	return strings.ContainsFunc(s, func(r rune) bool {
		return unicode.Is(unicode.Han, r) || r >= 0x3000 && r <= 0x303f || r >= 0xff00 && r <= 0xffef
	})
}

// cjkLiterals 是 dir 下非测试、非生成的 Go 文件里含中文的字符串字面量，
// 按文件给出所在行；同一行带 //i18n:ignore 注释的不算（如语言的自称）。
func cjkLiterals(t *testing.T, dir string) map[string][]int {
	t.Helper()
	files, err := filepath.Glob(filepath.Join(dir, "*.go"))
	if err != nil {
		t.Fatal(err)
	}
	out := map[string][]int{}
	for _, path := range files {
		if strings.HasSuffix(path, "_test.go") || strings.HasSuffix(path, "_gen.go") {
			continue
		}
		fset := token.NewFileSet()
		f, err := parser.ParseFile(fset, path, nil, parser.ParseComments)
		if err != nil {
			t.Fatal(err)
		}
		ignored := map[int]bool{}
		for _, g := range f.Comments {
			for _, cm := range g.List {
				if strings.HasPrefix(cm.Text, "//i18n:ignore") {
					ignored[fset.Position(cm.Pos()).Line] = true
				}
			}
		}
		ast.Inspect(f, func(n ast.Node) bool {
			lit, ok := n.(*ast.BasicLit)
			if !ok || lit.Kind != token.STRING {
				return true
			}
			s, err := strconv.Unquote(lit.Value)
			if err != nil || !cjk(s) {
				return true
			}
			if line := fset.Position(lit.Pos()).Line; !ignored[line] {
				out[path] = append(out[path], line)
			}
			return true
		})
	}
	return out
}

func TestUIStringsLiveInTheCatalogs(t *testing.T) {
	found := map[string][]int{}
	for _, dir := range []string{".", "internal/library"} {
		for path, lines := range cjkLiterals(t, dir) {
			found[filepath.ToSlash(path)] = lines
		}
	}
	for path, lines := range found {
		if _, ok := i18nExempt[path]; !ok {
			t.Errorf("%s:%d 写死了中文：界面文案放进 locales/zh-Hans.json，用生成的函数说（共 %d 处）", path, lines[0], len(lines))
		}
	}
}

func TestEveryMessageHasAnEnglishTranslation(t *testing.T) {
	keys := func(path string) []string {
		b, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		var m map[string]any
		if err := json.Unmarshal(b, &m); err != nil {
			t.Fatalf("%s：%v", path, err)
		}
		var out []string
		for k := range m {
			if !strings.HasPrefix(k, "@") {
				out = append(out, k)
			}
		}
		slices.Sort(out)
		return out
	}
	en := keys("locales/en.json")
	for _, k := range keys("locales/zh-Hans.json") {
		if _, ok := slices.BinarySearch(en, k); !ok {
			t.Errorf("locales/en.json 缺 %q 的翻译", k)
		}
	}
}
