package library

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

// writeRaw 写一份原样的 SKILL.md（frontmatter 由调用方自己写）。
func writeRaw(t *testing.T, dir, md string) {
	t.Helper()
	must(t, os.MkdirAll(dir, 0o755))
	must(t, os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(md), 0o644))
}

func writeFile(t *testing.T, p, content string) {
	t.Helper()
	must(t, os.MkdirAll(filepath.Dir(p), 0o755))
	must(t, os.WriteFile(p, []byte(content), 0o644))
}

func incomplete(l *Library) []string {
	var out []string
	for _, s := range l.Problems().Incomplete {
		out = append(out, s.Name)
	}
	return out
}

func TestSkillLinkingToMissingFileIsListedAsIncomplete(t *testing.T) {
	// Given 我的技能 test-first 链到 references/lint-rules.md，但目录里只有 bdd.md
	home, h := linkHome(t)
	writeSkill(t, h(".agents/skills/test-first"),
		"\n详见 [BDD](references/bdd.md)、[lint 规则](references/lint-rules.md#规则)。\n")
	writeFile(t, h(".agents/skills/test-first/references/bdd.md"), "# bdd\n")

	// When 扫描
	l := Scan(home)

	// Then 整理里列出它，只报缺的那一个文件
	if got := incomplete(l); !slices.Equal(got, []string{"test-first"}) {
		t.Fatalf("不完整的技能：%v", got)
	}
	want := []Defect{{MissingFile, "references/lint-rules.md"}}
	if got := l.Get("test-first").Defects; !slices.Equal(got, want) {
		t.Errorf("问题：%v，应为 %v", got, want)
	}
}

func TestReferencesThatAreNotSkillFilesAreIgnored(t *testing.T) {
	// Given 正文里有代码块中的示例路径、行内代码、网址、锚点、绝对路径、占位符、
	// 编码过的文件名，和反引号里的项目路径
	home, h := linkHome(t)
	writeSkill(t, h(".agents/skills/demo"), "\n"+
		"```md\n[示例](references/example.md)\n```\n"+
		"行内 `[x](scripts/x.py)`，项目里的 `src/app.ts`。\n"+
		"[官网](https://example.com/a.md) [邮件](mailto:a@b.c) [本节](#用法)\n"+
		"[绝对](/etc/hosts.md) [家目录](~/notes.md) [占位]({baseDir}/x.md) [变量](${CLAUDE_PLUGIN_ROOT}/x.md) [省略](…)\n"+
		"[有空格](references/my%20file.md) <https://example.com>\n")
	writeFile(t, h(".agents/skills/demo/references/my file.md"), "x")

	// When 扫描
	l := Scan(home)

	// Then 不算问题
	if got := l.Get("demo").Defects; len(got) != 0 {
		t.Errorf("不该报：%v", got)
	}
}

func TestLinkToSiblingSkillThatIsNotInstalledNamesTheSkill(t *testing.T) {
	// Given lark-drive 链到旁边的 lark-markdown、lark-base 两个技能，和一份搬来的
	// 文档路径 ../useMediaQuery/index.md；只装了 lark-base
	home, h := linkHome(t)
	writeSkill(t, h(".agents/skills/lark-drive"),
		"\n先读 [markdown](../lark-markdown/SKILL.md) 和 [base](../lark-base/SKILL.md)，"+
			"另见 [useMediaQuery](../useMediaQuery/index.md)。\n")
	writeSkill(t, h(".agents/skills/lark-base"), "")

	// When 扫描
	l := Scan(home)

	// Then 明说是技能的，说缺 lark-markdown 这个技能；不确定是技能的按缺文件报
	want := []Defect{{MissingSkill, "lark-markdown"}, {MissingFile, "../useMediaQuery/index.md"}}
	if got := l.Get("lark-drive").Defects; !slices.Equal(got, want) {
		t.Errorf("问题：%v，应为 %v", got, want)
	}
}

func TestDanglingLinkInsideSkillIsReported(t *testing.T) {
	// Given 技能目录里 scripts/run.sh 是指向不存在文件的链接
	home, h := linkHome(t)
	writeSkill(t, h(".agents/skills/runner"), "")
	must(t, os.MkdirAll(h(".agents/skills/runner/scripts"), 0o755))
	mustLink(t, "../../../shared/run.sh", h(".agents/skills/runner/scripts/run.sh"))

	// When 扫描
	l := Scan(home)

	// Then 报这个链接
	want := []Defect{{DanglingLink, "scripts/run.sh"}}
	if got := l.Get("runner").Defects; !slices.Equal(got, want) {
		t.Errorf("问题：%v，应为 %v", got, want)
	}
}

func TestBrokenFrontmatterIsReported(t *testing.T) {
	// Given 一个技能开头的 --- 没有闭合，另一个 frontmatter 里没写 name
	home, h := linkHome(t)
	writeRaw(t, h(".agents/skills/open"), "---\nname: open\ndescription: x\n\n# 正文\n")
	writeRaw(t, h(".agents/skills/nameless"), "\ufeff---\ndescription: 有简介\n---\n# 正文\n")

	// When 扫描
	l := Scan(home)

	// Then 各报各的
	if got := l.Get("open").Defects; !slices.Equal(got, []Defect{{OpenFrontmatter, ""}}) {
		t.Errorf("open：%v", got)
	}
	if got := l.Get("nameless").Defects; !slices.Equal(got, []Defect{{NoName, ""}}) {
		t.Errorf("nameless：%v", got)
	}
	if l.Get("nameless").MissingDescription() {
		t.Error("带 BOM 的 SKILL.md 也应读到简介")
	}
}

func TestBuiltinSkillsAreNotListedInTidy(t *testing.T) {
	// Given 插件技能链到不存在的文件（用户改不了）
	home := fakeHome(t)
	h := func(p string) string { return filepath.Join(home, p) }
	writeSkill(t, h(".claude/plugins/cache/m/pua/1.0/skills/pua"), "\n[x](references/gone.md)\n")

	// When 扫描
	l := Scan(home)

	// Then 整理里不列
	if slices.Contains(incomplete(l), "pua") {
		t.Error("内置技能不应列进整理")
	}
}

func TestCollectWarnsAboutReferencesThatBreakAfterCopying(t *testing.T) {
	// Given 项目技能 notes-sync 链到项目里的 ../shared/api.md（存在）和自己的 references/a.md
	home := fakeHome(t)
	h := func(p string) string { return filepath.Join(home, p) }
	dir := h("code/storefront/.claude/skills/notes-sync")
	writeSkill(t, dir, "\n[api](../shared/api.md) [a](references/a.md)\n")
	writeFile(t, filepath.Join(dir, "references/a.md"), "a")
	writeFile(t, h("code/storefront/.claude/skills/shared/api.md"), "api")
	l := Scan(home)
	s := stockDocs(t, l, h)

	// When 收进技能库之前问会断什么
	got := l.CollectBreaks(s)

	// Then 现在还在、复制后会断的只有 ../shared/api.md；现在没问题，不算不完整
	if !slices.Equal(got, []string{"../shared/api.md"}) {
		t.Errorf("复制后会断：%v", got)
	}
	if len(s.Defects) != 0 {
		t.Errorf("现在不该有问题：%v", s.Defects)
	}
}

func TestEncodedPlaceholdersAndAbsolutePathsAreIgnored(t *testing.T) {
	// Given 链接里的占位符和绝对路径是 URL 编码过的
	home, h := linkHome(t)
	writeSkill(t, h(".agents/skills/demo"), "\n[x](%7BbaseDir%7D/file.md) [y](%2Fetc%2Fhosts) [z](%24%7BROOT%7D/a.md)\n")

	// When 扫描
	l := Scan(home)

	// Then 和没编码时一样不算
	if got := l.Get("demo").Defects; len(got) != 0 {
		t.Errorf("不该报：%v", got)
	}
}

func TestEmptyFrontmatterIsClosedButHasNoName(t *testing.T) {
	// Given frontmatter 是空的一段
	home, h := linkHome(t)
	writeRaw(t, h(".agents/skills/empty"), "---\n---\n# 正文\n")

	// When 扫描
	l := Scan(home)

	// Then 报没写 name，不报没闭合
	if got := l.Get("empty").Defects; !slices.Equal(got, []Defect{{NoName, ""}}) {
		t.Errorf("问题：%v", got)
	}
}

func TestCollectDoesNotWarnAboutReferencesBackIntoTheSkill(t *testing.T) {
	// Given 项目技能用 ../notes-sync/… 绕一圈链到自己目录里的文件，目录里的链接也一样
	home := fakeHome(t)
	h := func(p string) string { return filepath.Join(home, p) }
	dir := h("code/storefront/.claude/skills/notes-sync")
	writeSkill(t, dir, "\n[a](../notes-sync/references/a.md)\n")
	writeFile(t, filepath.Join(dir, "references/a.md"), "a")
	mustLink(t, "../../notes-sync/references/a.md", filepath.Join(dir, "references/b.md"))
	l := Scan(home)
	s := stockDocs(t, l, h)

	// When 收进技能库之前问会断什么
	got := l.CollectBreaks(s)

	// Then 什么都不会断：这些文件跟着一起复制
	if len(got) != 0 {
		t.Errorf("不该提示：%v", got)
	}
}
