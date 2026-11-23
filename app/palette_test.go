package main

import (
	"testing"

	"github.com/egoist/mygo/ui"

	"skillmanager/internal/library"
)

func openPaletteAndType(t *testing.T, tt *ui.Tester, query string) {
	t.Helper()
	tt.Key(ui.Cmd, ui.KeyK)
	tt.HoldModifiers(0) // 松开 ⌘
	if !tt.HasText("搜索 Skill") {
		t.Fatalf("⌘K 应打开搜索弹窗：%q", tt.Texts())
	}
	tt.Type(query)
}

func TestCommandKFindsASkillByNameAndEnterOpensIt(t *testing.T) {
	a, tt := newTester(t)
	openPaletteAndType(t, tt, "vue-best")
	if !tt.HasText("vue-best-practices") {
		t.Fatalf("应列出名称命中的 Skill：%q", tt.Texts())
	}
	tt.Key(0, ui.KeyEnter)
	if a.pal.open {
		t.Error("打开 Skill 后弹窗应关掉")
	}
	if a.reading == nil || a.reading.Name != "vue-best-practices" {
		t.Fatalf("↵ 应打开阅读视图，reading = %v", a.reading)
	}
}

func TestCommandKFindsWordsOnlyInTheBodyAndOpensThatFile(t *testing.T) {
	a, tt := newTester(t)
	openPaletteAndType(t, tt, "只验证一件事")
	if !tt.HasText("test-first") {
		t.Fatalf("正文命中的 Skill 应列出：%q", tt.Texts())
	}
	if !tt.HasText("只验证一件事") {
		t.Errorf("说明里应显示命中的那句：%q", tt.Texts())
	}
	tt.Key(0, ui.KeyEnter)
	if a.reading == nil || a.reading.Name != "test-first" {
		t.Fatalf("reading = %v", a.reading)
	}
	if got := a.reading.Files[a.file].Path; got != "references/bdd-scenarios.md" {
		t.Errorf("应打开命中的文件，打开的是 %q", got)
	}
}

func TestCommandKFindsSkillsFromEverySourceWithTheSourceNoted(t *testing.T) {
	a, tt := newTester(t)
	openPaletteAndType(t, tt, "skill-creator")
	if !tt.HasText("skill-creator") || !tt.HasText("内置") {
		t.Fatalf("在「我的」页也应搜到内置 Skill，并标出来源：%q", tt.Texts())
	}
	tt.Key(0, ui.KeyEnter)
	if a.scope != string(library.ScopeBuiltin) || a.reading == nil {
		t.Errorf("打开内置 Skill 应换到内置页的阅读视图：scope=%q reading=%v", a.scope, a.reading)
	}
}

func TestCommandEnterShowsTheSkillInItsListInsteadOfOpeningIt(t *testing.T) {
	a, tt := newTester(t)
	openPaletteAndType(t, tt, "vue-best")
	if tt.HasText("在列表中显示") {
		t.Error("没按住 ⌘ 时选中行只提示 ↵")
	}
	tt.HoldModifiers(ui.Cmd)
	if !tt.HasText("在列表中显示") || !tt.HasText("~/.agents/skills/vue-best-practices") {
		t.Errorf("按住 ⌘ 时选中行应提示「在列表中显示」并给出目录：%q", tt.Texts())
	}
	tt.Key(ui.Cmd, ui.KeyEnter)
	if a.reading != nil {
		t.Error("⌘↵ 不应进入阅读视图")
	}
	if a.selected == nil || a.selected.Name != "vue-best-practices" {
		t.Errorf("⌘↵ 应在列表里选中它，selected = %v", a.selected)
	}
}

func TestArrowKeysMoveTheSelectionAndActionsRunOnEnter(t *testing.T) {
	a, tt := newTester(t)
	openPaletteAndType(t, tt, "整理")
	if !tt.HasText("整理") {
		t.Fatalf("应列出「整理」操作：%q", tt.Texts())
	}
	tt.Key(0, ui.KeyDown)
	tt.Key(0, ui.KeyUp)
	tt.Key(0, ui.KeyEnter)
	if a.scope != scopeTidy {
		t.Errorf("↵ 应跳到整理页，scope = %q", a.scope)
	}
}

func TestEmptyPaletteListsRecentlyOpenedSkillsAndPlaces(t *testing.T) {
	a, tt := newTester(t)
	a.open(a.lib.Get("test-first"))
	a.closeReader()
	tt.Frame()
	tt.Key(ui.Cmd, ui.KeyK)
	tt.HoldModifiers(0)
	for _, want := range []string{"最近", "test-first", "跳转", "检查 Skill 更新", "设置"} {
		if !tt.HasText(want) {
			t.Errorf("空输入时应有 %q：%q", want, tt.Texts())
		}
	}
}

func TestEscapeClosesThePaletteAndNoMatchSaysSo(t *testing.T) {
	a, tt := newTester(t)
	openPaletteAndType(t, tt, "kubernetes 部署")
	if !tt.HasText("没有找到「kubernetes 部署」") {
		t.Errorf("没有结果时应说明：%q", tt.Texts())
	}
	tt.Key(0, ui.KeyEscape)
	if a.pal.open || tt.HasText("搜索 Skill") {
		t.Error("Esc 应关掉弹窗")
	}
}

func TestPinyinFindsChineseSkillsInTheChineseInterface(t *testing.T) {
	a, tt := newTester(t)
	openPaletteAndType(t, tt, "ceshi")
	if !tt.HasText("test-first") {
		t.Fatalf("简体中文界面输入 ceshi 应找到简介里有「测试」的 Skill：%q", tt.Texts())
	}
	tt.Key(0, ui.KeyEnter)
	if a.reading == nil || a.reading.Name != "test-first" {
		t.Errorf("reading = %v", a.reading)
	}
}

func TestCommandKAgainClosesThePalette(t *testing.T) {
	a, tt := newTester(t)
	tt.Key(ui.Cmd, ui.KeyK)
	tt.Key(ui.Cmd, ui.KeyK)
	if a.pal.open {
		t.Error("弹窗开着时再按 ⌘K 应关掉")
	}
}

func TestCommandEnterRevealsASkillInsideACollectionEvenWhenTheToolbarFilters(t *testing.T) {
	a, tt := newTester(t)
	s := a.lib.Get("doc-coauthoring")
	if s == nil || s.Collection == nil {
		t.Fatal("示例数据里 doc-coauthoring 应在一个集合里")
	}
	a.query = "不会匹配任何 Skill"
	openPaletteAndType(t, tt, "doc-coauthoring")
	tt.Key(ui.Cmd, ui.KeyEnter)
	if a.query != "" || a.inColl != s.Collection || a.selected != s {
		t.Errorf("⌘↵ 应清掉工具栏的搜索词并打开它所在的集合：query=%q inColl=%v selected=%v", a.query, a.inColl, a.selected)
	}
	if !tt.HasText("doc-coauthoring") {
		t.Errorf("列表里应看得见它：%q", tt.Texts())
	}
}

func TestCommandKFindsSkillUpdatesInBothLanguages(t *testing.T) {
	a, tt := newTester(t)
	openPaletteAndType(t, tt, "更新 Skill")
	if !tt.HasText("检查 Skill 更新") {
		t.Fatalf("应列出「检查 Skill 更新」操作：%q", tt.Texts())
	}
	tt.Key(0, ui.KeyEscape)

	useLocale(t, tt, "en")
	tt.Key(ui.Cmd, ui.KeyK)
	tt.HoldModifiers(0)
	if !tt.HasText("Search Skills") {
		t.Fatalf("⌘K should open English palette: %q", tt.Texts())
	}
	tt.Type("skill updates")
	if !tt.HasText("Check for Skill Updates") {
		t.Fatalf("English palette should list Check for Skill Updates: %q", tt.Texts())
	}
	tt.Key(0, ui.KeyEnter)
	if a.scope != scopeUpdates {
		t.Fatalf("Enter should navigate to Skill Updates, got %q", a.scope)
	}
}

func TestARescanThatStartedEarlierDoesNotRollBackSettings(t *testing.T) {
	a, _ := newTester(t)
	stale := library.Sample() // 扫描开始时从盘上读的设置
	a.open(a.lib.Get("test-first"))
	a.lib.Settings.Language = "zh-Hans"
	a.applyScan(stale, stale.Updates(), false)
	if a.lib.Settings.Language != "zh-Hans" || len(a.lib.Settings.RecentSkills) == 0 {
		t.Errorf("扫描期间改的设置被退回了：%+v", a.lib.Settings)
	}
}
