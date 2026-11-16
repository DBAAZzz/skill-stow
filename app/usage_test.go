package main

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/egoist/mygo/ui"

	"skillmanager/internal/library"
)

// newUsageTester 用够高的窗口，使用情况整页（概览、用过的、没用过的）不用滚动就都在窗口里。
func newUsageTester(t *testing.T) (*app, *ui.Tester) {
	t.Helper()
	a, tt := newTester(t)
	tt.SetSize(1280, 1800)
	return a, tt
}

func TestUsagePageRanksUsedSkillsAndListsUnusedOnes(t *testing.T) {
	a, tt := newUsageTester(t)
	if err := tt.Click("使用情况"); err != nil {
		t.Fatal(err)
	}
	if a.scope != scopeUsage || !tt.HasText("近 8 周调用") || !tt.HasText("库内技能") || !tt.HasText("本周") {
		t.Fatalf("应打开使用情况页，有调用图和库内技能分布：%q", tt.Texts())
	}
	// 日均、有调用的天数、最多的一天各占一格
	for _, want := range []string{"日均", "有调用的天数", "最多的一天"} {
		if !tt.HasText(want) {
			t.Errorf("图下应有「%s」一格：%q", want, tt.Texts())
		}
	}
	// 用过的按次数排；没用过的先列我的：git-flow 启用于 Claude Code 却没被调用，
	// tailwind-rules 只有 Cursor 读得到，没有记录，不列
	if !tt.HasText("test-first") || !tt.HasText("18") || !tt.HasText("git-flow") || tt.HasText("tailwind-rules") {
		t.Errorf("左边列用过的，右边列没用过的：%q", tt.Texts())
	}
	// 一个集合里没用过的收成一行，写出用过了哪个
	if !tt.HasText("open.feishu.cn") || !tt.HasText("6 个里只用过 lark-doc") || tt.HasText("lark-calendar") {
		t.Errorf("集合里没用过的应收成一行：%q", tt.Texts())
	}
	if tt.HasText("skill-creator") {
		t.Error("内置的没用过的在另一页")
	}
	// 插件装的没用过的算在「我的」这一页
	if !tt.HasText("document-skills") {
		t.Errorf("插件装的应列在我的这一页：%q", tt.Texts())
	}
	if err := tt.Click("内置 1"); err != nil {
		t.Fatal(err)
	}
	if !tt.HasText("skill-creator") || tt.HasText("git-flow") {
		t.Errorf("切到内置应只列内置的：%q", tt.Texts())
	}
}

func TestUsagePageOpensACollection(t *testing.T) {
	a, tt := newUsageTester(t)
	if err := tt.Click("使用情况"); err != nil {
		t.Fatal(err)
	}
	if err := tt.Click("open.feishu.cn"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if a.scope != string(library.ScopeMine) || a.inColl == nil || a.inColl.Name != "open.feishu.cn" {
		t.Fatalf("应换到我的并打开集合，scope=%q inColl=%v", a.scope, a.inColl)
	}
}

func TestUsagePageClickingASkillPreviewsItsFilesInThePane(t *testing.T) {
	a, tt := newUsageTester(t)
	if err := tt.Click("使用情况"); err != nil {
		t.Fatal(err)
	}
	// When 点「没用过的」里的单个技能
	if err := tt.Click("git-flow"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	// Then 留在使用情况页，右侧详情栏预览它的文件和使用情况
	if a.scope != scopeUsage || a.selected == nil || a.selected.Name != "git-flow" {
		t.Fatalf("应留在使用情况页并选中 git-flow，scope=%q selected=%v", a.scope, a.selected)
	}
	for _, want := range []string{"文件", "使用情况", "信息"} {
		if !tt.HasText(want) {
			t.Errorf("详情栏应有「%s」：%q", want, tt.Texts())
		}
	}
	// When 按 Esc
	tt.Key(0, ui.KeyEscape)
	tt.Frame()
	if a.selected != nil {
		t.Error("Esc 应收起详情栏")
	}
}

func TestUsagePageShowsTheFirstRowsAndExpandsOnRequest(t *testing.T) {
	a, tt := newUsageTester(t)
	a.scope = scopeUsage
	tt.Frame()
	used := a.lib.Used()
	if len(used) <= usageRowsShown {
		t.Skipf("示例数据只有 %d 个用过的，不需要展开", len(used))
	}
	last := used[len(used)-1].Name
	if tt.HasText(last) {
		t.Fatalf("先只列前 %d 个，不应有 %s", usageRowsShown, last)
	}
	if err := tt.Click(fmt.Sprintf("显示全部 %d 个", len(used))); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !tt.HasText(last) || !tt.HasText("收起") {
		t.Errorf("展开后应列出全部并可以收起：%q", tt.Texts())
	}
}

func TestInspectorShowsHowOftenASkillWasCalled(t *testing.T) {
	a, tt := newTester(t)
	if err := tt.Click("kami"); err != nil {
		t.Fatal(err)
	}
	if !tt.HasText("调用过 4 次") || !tt.HasText("上次是12 天前") {
		t.Errorf("详情栏应显示调用次数和上次时间：%q", tt.Texts())
	}
	// 只有 Cursor 读得到的技能：不知道用没用过。详情栏滑出后网格少一列，
	// 它排到了窗口外，直接选中。
	a.selected = a.lib.Get("tailwind-rules")
	tt.Frame()
	if !tt.HasText("没有记录") {
		t.Errorf("没有记录的 Agent 才读得到的技能应标明没有记录：%q", tt.Texts())
	}
}

// SNAPSHOT_DIR=… go test -run UsageSnapshots 输出使用统计的截图。
func TestUsageSnapshots(t *testing.T) {
	dir := os.Getenv("SNAPSHOT_DIR")
	if dir == "" {
		t.Skip("设置 SNAPSHOT_DIR 以输出截图")
	}
	for name, setup := range map[string]func(a *app){
		"u1-usage":         func(a *app) { a.scope = scopeUsage },
		"u2-usage-builtin": func(a *app) { a.scope, a.unusedTab = scopeUsage, library.Builtin },
		"u3-used":          func(a *app) { a.selected = a.lib.Get("test-first") },
		"u4-preview":       func(a *app) { a.scope, a.selected = scopeUsage, a.lib.Get("git-flow") },
	} {
		a := newApp(library.Sample())
		setup(a)
		tt := newUITester(a.view, 1280, 1400)
		tt.SetScale(2)
		tt.Frame()
		tt.Advance(paneDuration)
		writePNG(t, filepath.Join(dir, name+".png"), tt)
	}
}
