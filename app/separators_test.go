package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"testing"
)

// dotSeparators 是被拿来把几段信息拼成一行的点号。界面上不用它们：并排的
// 几段用 metaLine 或各自一个控件、状态用 Tag；只能交出一个字符串的地方
// （Tooltip、菜单项、读屏标签、提示）把整句写进 locales，标点由译文决定；
// 句子里的列举用 locales.ListSeparator()。
const dotSeparators = "·•‧⋅・"

const dotSeparatorAdvice = "界面上并排的几段用 metaLine 或各自一个控件，状态用 Tag；" +
	"只能交出一个字符串的整句写进 locales；列举用 locales.ListSeparator()"

func hasDotSeparator(s string) bool { return strings.ContainsAny(s, dotSeparators) }

func TestUITextDoesNotJoinInformationWithDots(t *testing.T) {
	for _, dir := range []string{".", "internal/library"} {
		for path, lines := range literalsMatching(t, dir, hasDotSeparator, "//separator:ignore", true) {
			for _, line := range lines {
				t.Errorf("%s:%d 用点号拼接信息：%s（确实不是界面文字时，同一行加 //separator:ignore 说明原因）",
					filepath.ToSlash(path), line, dotSeparatorAdvice)
			}
		}
	}
	for _, path := range []string{"locales/zh-Hans.json", "locales/en.json"} {
		b, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		var catalog map[string]any
		if err := json.Unmarshal(b, &catalog); err != nil {
			t.Fatal(err)
		}
		var keys []string
		for k, v := range catalog {
			if text, ok := v.(string); ok && !strings.HasPrefix(k, "@") && hasDotSeparator(text) {
				keys = append(keys, k)
			}
		}
		sort.Strings(keys)
		for _, k := range keys {
			t.Errorf("%s 的 %s 用点号拼接信息：%s", path, k, dotSeparatorAdvice)
		}
	}
}

func TestDotSeparatorScanCatchesEveryLiteralForm(t *testing.T) {
	dir := t.TempDir()
	src := "package x\n\n" +
		"var a = \"a · b\"\n" + // 第 3 行：直接写
		"var b = \"a \\u00b7 b\"\n" + // 第 4 行：转义
		"var c = \"a\" + string('·') + \"b\"\n" + // 第 5 行：rune
		"var d = \"a · b\" //separator:ignore 不是界面文字\n" + // 第 6 行：豁免
		"// 注释里的 · 不扫\n" +
		"var e = \"a, b\"\n"
	if err := os.WriteFile(filepath.Join(dir, "x.go"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	got := literalsMatching(t, dir, hasDotSeparator, "//separator:ignore", true)[filepath.Join(dir, "x.go")]
	if want := []int{3, 4, 5}; !slices.Equal(got, want) {
		t.Errorf("扫到的行 = %v，想要 %v", got, want)
	}
}
