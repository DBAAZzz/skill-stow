package main

import (
	"testing"

	"github.com/egoist/mygo/ui"

	"skillmanager/internal/library"
)

// goBack、goForward 点左上角的后退、前进按钮。
func goBack(t *testing.T, tt *ui.Tester) {
	t.Helper()
	must(t, tt.Click(historyLabel(0)))
}

func goForward(t *testing.T, tt *ui.Tester) {
	t.Helper()
	must(t, tt.Click(historyLabel(1)))
}

func TestBackAndForwardRetraceVisitedPagesAndTheirSelection(t *testing.T) {
	// Given 从我的技能打开集合、在集合里打开技能，再去使用情况页
	a, tt := newTester(t)
	doubleClick(t, tt, "open.feishu.cn")
	doubleClick(t, tt, "lark-doc")
	must(t, tt.Click("使用情况"))
	tt.Frame()

	// When 一路后退
	tt.Key(historyKey[0].mods, historyKey[0].key)
	tt.Frame()
	// Then 依次回到阅读视图、集合页（选中 lark-doc）、我的技能（选中集合）
	if a.reading == nil || a.reading.Name != "lark-doc" {
		t.Fatalf("快捷键后退应回到 lark-doc 的阅读视图，reading=%v scope=%q", a.reading, a.scope)
	}
	goBack(t, tt)
	if a.reading != nil || a.inColl == nil || a.inColl.Name != "open.feishu.cn" || a.selected == nil || a.selected.Name != "lark-doc" {
		t.Fatalf("应回到集合页并选中 lark-doc，inColl=%v selected=%v", a.inColl, a.selected)
	}
	goBack(t, tt)
	if a.inColl != nil || a.scope != string(library.ScopeMine) || a.coll == nil || a.coll.Name != "open.feishu.cn" {
		t.Fatalf("应回到我的 Skill 并选中集合，scope=%q inColl=%v coll=%v", a.scope, a.inColl, a.coll)
	}
	goBack(t, tt) // 已是第一页，没有反应
	if a.scope != string(library.ScopeMine) || a.inColl != nil {
		t.Fatalf("第一页再后退不应换页，scope=%q", a.scope)
	}

	// When 一路前进 Then 原路回到使用情况页
	goForward(t, tt)
	goForward(t, tt)
	if a.reading == nil || a.reading.Name != "lark-doc" {
		t.Fatalf("前进两次应回到阅读视图，reading=%v", a.reading)
	}
	goForward(t, tt)
	if a.scope != scopeUsage || a.reading != nil {
		t.Fatalf("再前进应回到使用情况页，scope=%q", a.scope)
	}
}

func TestReturningFromTheReaderStepsBackSoForwardOpensItAgain(t *testing.T) {
	a, tt := newTester(t)
	doubleClick(t, tt, "test-first")
	must(t, tt.Click("返回（Esc）"))
	if a.reading != nil {
		t.Fatal("‹ 应回到浏览")
	}
	goForward(t, tt)
	if a.reading == nil || a.reading.Name != "test-first" {
		t.Fatalf("前进应重新打开 test-first，reading=%v", a.reading)
	}
}

func TestHistorySkipsPagesOfDeletedSkills(t *testing.T) {
	// Given 读过 vue-best-practices 又回到浏览，然后删了它
	a, tt := newTester(t)
	doubleClick(t, tt, "vue-best-practices")
	must(t, tt.Click("返回（Esc）"))
	askToDelete(t, tt, "vue-best-practices")
	must(t, tt.Click("移到废纸篓"))
	selected := a.selected

	// When 前进 Then 没有可去的页面，什么都不变
	goForward(t, tt)
	if a.reading != nil || a.selected != selected {
		t.Fatalf("前进应跳过已删除的 Skill，reading=%v selected=%v（原来 %v）", a.reading, a.selected, selected)
	}
}

func TestHistoryKeepsOnlyTheLatestPages(t *testing.T) {
	a, tt := newTester(t)
	for i := range historyLimit + 10 {
		a.goTo([]string{scopeTidy, scopeUsage}[i%2])
		tt.Frame()
	}
	steps := 0
	for range historyLimit + 10 {
		before := a.scope
		goBack(t, tt)
		if a.scope == before {
			break
		}
		steps++
	}
	if steps != historyLimit-1 {
		t.Fatalf("应能后退 %d 步，实际 %d 步", historyLimit-1, steps)
	}
}

func TestBackAfterSwitchingInTheSidebarReturnsToTheSourcePage(t *testing.T) {
	// Given 在我的技能，点侧边栏去整理
	a, tt := newTester(t)
	must(t, tt.Click("整理"))
	// When 后退 Then 回到我的技能，前进又回到整理
	goBack(t, tt)
	if a.scope != string(library.ScopeMine) {
		t.Fatalf("后退应回到我的 Skill，scope=%q", a.scope)
	}
	goForward(t, tt)
	if a.scope != scopeTidy {
		t.Fatalf("前进应回到整理，scope=%q", a.scope)
	}

	// Given 读着 test-first 时点侧边栏去整理
	goBack(t, tt)
	doubleClick(t, tt, "test-first")
	must(t, tt.Click("整理"))
	// When 后退 Then 回到 test-first 的阅读视图
	goBack(t, tt)
	if a.scope != string(library.ScopeMine) || a.reading == nil || a.reading.Name != "test-first" {
		t.Fatalf("后退应回到 test-first 的阅读视图，scope=%q reading=%v", a.scope, a.reading)
	}
}

func TestReturningToAPageDoesNotBringBackBatchPicking(t *testing.T) {
	// Given 在使用情况页进了批量删除
	a, tt := newUsageTester(t)
	must(t, tt.Click("使用情况"))
	must(t, tt.Click("批量删除"))
	if !a.del.picking {
		t.Fatal("应进入批量删除")
	}
	// When 后退再前进 Then 回到使用情况页，不在批量删除里
	goBack(t, tt)
	goForward(t, tt)
	if a.scope != scopeUsage || a.del.picking {
		t.Fatalf("回来后不应仍在批量删除，scope=%q picking=%v", a.scope, a.del.picking)
	}
}
