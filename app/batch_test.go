package main

import (
	"fmt"
	"testing"

	"github.com/egoist/mygo/ui"
)

// picked 报告名为 name 的技能在批量里勾上了没有。
func picked(a *app, name string) bool {
	s := a.lib.Get(name)
	return s != nil && a.batch.picked[s.ID]
}

func TestUserPicksSkillsInBatchModeAndLeavesWithEsc(t *testing.T) {
	// Given 在我的技能的卡片视图
	a, tt := newTester(t)

	// When 点顶栏的「批量操作」
	must(t, tt.Click("批量操作"))

	// Then 进入选择态：顶栏换成选择条，删除还点不了
	for _, want := range []string{"选择要删除的技能", "有同名副本的先去「整理」合并", "全选", "退出批量", "全选这一组"} {
		if !tt.HasText(want) {
			t.Errorf("选择态缺少 %q：%q", want, tt.Texts())
		}
	}

	// When 单击一张卡片
	must(t, tt.Click("vue-best-practices"))

	// Then 它勾上了，不会选中、不会滑出详情栏
	if !picked(a, "vue-best-practices") || a.selected != nil {
		t.Fatalf("单击应勾选而不是选中：picked=%v selected=%v", a.batch.picked, a.selected)
	}
	if !tt.HasText("已选 1 项") || !tt.HasText("删除 1 个…") {
		t.Errorf("选择条应写出已选几项：%q", tt.Texts())
	}

	// When 再点一下
	must(t, tt.Click("vue-best-practices"))

	// Then 取消勾选
	if picked(a, "vue-best-practices") {
		t.Error("再点一下应取消勾选")
	}

	// When 按 Esc
	must(t, tt.Click("vue-best-practices"))
	tt.Key(0, ui.KeyEscape)

	// Then 退出批量，勾选清空，顶栏回到原样
	if a.batch.on || len(a.batch.picked) > 0 || !tt.HasText("批量操作") {
		t.Errorf("Esc 应退出批量：on=%v picked=%v", a.batch.on, a.batch.picked)
	}
}

func TestSkillsWithCopiesCannotBePickedInBatch(t *testing.T) {
	// Given 进入了选择态，test-first 有同名副本
	a, tt := newTester(t)
	must(t, tt.Click("批量操作"))

	// When 点它，再点「全选」
	must(t, tt.Click("test-first"))
	if picked(a, "test-first") {
		t.Fatal("有副本的技能不应勾上")
	}
	must(t, tt.Click("全选"))

	// Then 全选也跳过它
	if picked(a, "test-first") || picked(a, "brainstorming") {
		t.Error("全选不应勾上有副本的技能")
	}
	if !picked(a, "vue-best-practices") {
		t.Error("全选应勾上能删的技能")
	}
}

func TestPickingACollectionDeletesAllItsSkills(t *testing.T) {
	// Given 进入了选择态
	a, tt := newTester(t)
	must(t, tt.Click("批量操作"))

	// When 勾上 anthropics/skills 这个集合和一个技能
	must(t, tt.Click("anthropics/skills"))
	must(t, tt.Click("vue-best-practices"))

	// Then 选择条写出含几个集合、一共几个技能
	coll := a.lib.Get("doc-coauthoring").Collection
	n := len(coll.Skills) + 1
	if !tt.HasText("已选 2 项") || !tt.HasText("（含 1 个集合，共 "+fmt.Sprint(n)+" 个技能）") {
		t.Fatalf("选择条不对：%q", tt.Texts())
	}

	// When 点「删除 N 个…」，确认框里移到废纸篓
	must(t, tt.Click("删除 "+fmt.Sprint(n)+" 个…"))
	if !tt.HasText("删除 " + fmt.Sprint(n) + " 个技能？") {
		t.Fatalf("应弹出确认框：%q", tt.Texts())
	}
	must(t, tt.Click("移到废纸篓"))

	// Then 集合里的技能和勾上的技能都删了，退出批量，可以撤销
	for _, name := range []string{"doc-coauthoring", "vue-best-practices"} {
		if a.lib.Get(name) != nil {
			t.Errorf("%s 应已删除", name)
		}
	}
	if a.batch.on || !tt.HasText("已把 "+fmt.Sprint(n)+" 个技能移到废纸篓") {
		t.Errorf("删完应退出批量并提示：on=%v %q", a.batch.on, tt.Texts())
	}
	must(t, tt.Click("撤销"))
	if a.lib.Get("doc-coauthoring") == nil {
		t.Error("撤销应放回整个集合")
	}
}

func TestBatchIsOfferedOnlyForMySkillCards(t *testing.T) {
	a, tt := newTester(t)
	a.mode = viewBoard
	tt.Frame()
	if tt.HasText("批量操作") {
		t.Error("看板视图不应有批量操作")
	}
	a.mode, a.scope = viewCards, "builtin"
	tt.Frame()
	if tt.HasText("批量操作") {
		t.Error("内置技能是只读的，不应有批量操作")
	}
}

func TestLongNameListsCollapse(t *testing.T) {
	names := []string{"a", "b", "c", "d"}
	if got := nameList(names, 3); got != "a、b、c 等 4 个" {
		t.Errorf("nameList = %q", got)
	}
	if got := nameList(names, 4); got != "a、b、c、d" {
		t.Errorf("nameList = %q", got)
	}
}
