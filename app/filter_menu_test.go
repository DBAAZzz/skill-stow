package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"skillmanager/internal/library"
)

func clickMenuButton(t *testing.T, tt *ui.Tester, name string) {
	t.Helper()
	if info, ok := tt.AccessByRole(ui.RoleMenuButton, name); ok {
		tt.ClickAt(info.Bounds.X+info.Bounds.W/2, info.Bounds.Y+info.Bounds.H/2)
		return
	}
	must(t, tt.Click(name))
}

func clickMenuItem(t *testing.T, tt *ui.Tester, name string) {
	t.Helper()
	for _, role := range []ui.Role{ui.RoleMenuItem, ui.RoleMenuItemCheckBox, ui.RoleMenuItemRadio} {
		if info, ok := tt.AccessByRole(role, name); ok {
			tt.ClickAt(info.Bounds.X+info.Bounds.W/2, info.Bounds.Y+info.Bounds.H/2)
			return
		}
	}
	must(t, tt.Click(name))
}

func openFilterMenu(t *testing.T, tt *ui.Tester) {
	t.Helper()
	clickMenuButton(t, tt, "筛选")
}

func openFilterAgentSubmenu(t *testing.T, tt *ui.Tester) {
	t.Helper()
	openFilterMenu(t, tt)
	clickMenuItem(t, tt, "按 Agent")
}

func TestBrowseFilterByAgentAndUnboundHidesNonMatchingSkills(t *testing.T) {
	// Given 浏览「我的」技能，包含 Claude Code 技能、Cursor-only 技能与未启用技能
	a, tt := newTester(t)

	// When 先选中 tailwind-rules，再通过筛选菜单选择「按 Agent > Claude Code」
	must(t, tt.Click("tailwind-rules"))
	tt.Advance(paneDuration)
	openFilterAgentSubmenu(t, tt)
	if !tt.HasText("Claude Code") || !tt.HasText("未启用") {
		t.Fatalf("按 Agent 子菜单应列出可见 Agent 和「未启用」：%q", tt.Texts())
	}
	clickMenuItem(t, tt, "Claude Code")

	// Then 只保留 Claude Code 相关技能，其他 Agent 独占与未启用技能被过滤，被滤掉的选中态同步清空
	if !tt.HasText("systematic-debugging") || !tt.HasText("test-first") {
		t.Fatalf("应保留 Claude Code 技能：%q", tt.Texts())
	}
	if tt.HasText("tailwind-rules") || tt.HasText("spark") {
		t.Fatalf("不应再显示 Cursor 独占或未启用技能：%q", tt.Texts())
	}
	if a.selected != nil {
		t.Fatalf("已选技能被过滤掉时应清空选中态，实际 = %v", a.selected.Name)
	}

	// And 重新打开筛选菜单可以看到当前选中的 Agent 摘要
	openFilterMenu(t, tt)
	if !tt.HasText("Claude Code") {
		t.Fatalf("筛选菜单应回显已选 Agent「Claude Code」：%q", tt.Texts())
	}

	// When 切换为「按 Agent > 未启用」
	clickMenuItem(t, tt, "按 Agent")
	clickMenuItem(t, tt, "未启用")

	// Then 只显示未绑定到任何 Agent 的技能（如 spark），不包含已停用或已启用的技能
	if !tt.HasText("spark") {
		t.Fatalf("应显示未启用技能 spark：%q", tt.Texts())
	}
	if tt.HasText("systematic-debugging") || tt.HasText("git-flow") {
		t.Fatalf("不应显示已绑定或已停用的技能：%q", tt.Texts())
	}
}

func TestBrowseFilterByUpdatesUnusedAndDisabledCombinesAndClears(t *testing.T) {
	// Given 库中有可更新技能（vue-best-practices）、从没用过技能和已停用技能（git-flow）
	a, tt := newTester(t)
	a.upd.report = library.UpdateReport{
		Checked:  time.Now(),
		Complete: true,
		Eligible: 1,
		Items: []library.UpdateItem{
			{Name: "vue-best-practices", State: library.UpdReady},
		},
	}
	tt.Frame()

	// When 勾选「有可用更新」
	openFilterMenu(t, tt)
	clickMenuItem(t, tt, "有可用更新")

	// Then 仅展示有可用更新的技能
	if !tt.HasText("vue-best-practices") {
		t.Fatalf("应展示可更新技能 vue-best-practices：%q", tt.Texts())
	}
	if tt.HasText("test-first") || tt.HasText("git-flow") {
		t.Fatalf("不应展示无可用更新的技能：%q", tt.Texts())
	}

	// When 点「清除筛选」
	openFilterMenu(t, tt)
	clickMenuItem(t, tt, "清除筛选")
	if !tt.HasText("test-first") || !tt.HasText("git-flow") {
		t.Fatalf("清除筛选后应恢复所有技能：%q", tt.Texts())
	}

	// When 勾选「从没用过」
	openFilterMenu(t, tt)
	clickMenuItem(t, tt, "从没用过")

	// Then 有已知调用记录且从未调用的技能保留，已知用过的技能隐藏
	if !tt.HasText("systematic-debugging") {
		t.Fatalf("应展示从没用过的技能 systematic-debugging：%q", tt.Texts())
	}
	if tt.HasText("test-first") || tt.HasText("vue-best-practices") {
		t.Fatalf("不应展示已调用过的技能：%q", tt.Texts())
	}

	// When 叠加勾选「已停用」
	openFilterMenu(t, tt)
	clickMenuItem(t, tt, "已停用")

	// Then 仅保留同时满足「从没用过」与「已停用」的 git-flow
	if !tt.HasText("git-flow") {
		t.Fatalf("应保留同时从没用过且已停用的 git-flow：%q", tt.Texts())
	}
	if tt.HasText("systematic-debugging") {
		t.Fatalf("未停用的技能应被叠加条件排除：%q", tt.Texts())
	}
}

func TestBrowseFilterEmptyStateOffersClearAction(t *testing.T) {
	// Given 启用了会返回 0 个结果的组合筛选（有可用更新 + 已停用）
	a, tt := newTester(t)
	a.upd.report = library.UpdateReport{
		Checked:  time.Now(),
		Complete: true,
		Eligible: 1,
		Items:    []library.UpdateItem{{Name: "vue-best-practices", State: library.UpdReady}},
	}
	tt.Frame()

	openFilterMenu(t, tt)
	clickMenuItem(t, tt, "有可用更新")
	openFilterMenu(t, tt)
	clickMenuItem(t, tt, "已停用")

	// Then 展示带「清除筛选」的空状态提示
	if !tt.HasText("没有符合筛选条件的技能") || !tt.HasText("清除筛选") {
		t.Fatalf("筛选无结果时应提示空状态与清除筛选按钮：%q", tt.Texts())
	}

	// When 点击空状态中的「清除筛选」
	must(t, tt.Click("清除筛选"))

	// Then 恢复正常技能列表
	if !tt.HasText("test-first") || !tt.HasText("vue-best-practices") {
		t.Fatalf("点击清除筛选后应恢复技能列表：%q", tt.Texts())
	}
}

func TestBrowseFilterWorksInKanbanBoardMode(t *testing.T) {
	// Given 用户处于「看板」视图，并将「按 Agent」筛选设为 Claude Code
	_, tt := newTester(t)
	must(t, tt.Click("看板"))
	openFilterAgentSubmenu(t, tt)
	clickMenuItem(t, tt, "Claude Code")

	// Then 看板仅保留匹配的 Claude Code 列，而不展示空白或不相关列
	if !tt.HasText("添加到 Claude Code") {
		t.Fatalf("看板应保留 Claude Code 列：%q", tt.Texts())
	}
	if tt.HasText("添加到 Codex") || tt.HasText("添加到 Cursor") {
		t.Fatalf("按单个 Agent 筛选后不应再展示其他 Agent 列：%q", tt.Texts())
	}
}

func TestFrostedActionMenusOperateAcrossUpdateInspectorAndBoard(t *testing.T) {
	// Scenario 1: 更新页面顶部「…」打开统一毛玻璃菜单并支持进入「技能更新设置…」
	a, tt := newTester(t)
	must(t, tt.Click("技能更新"))
	clickMenuButton(t, tt, "更多更新操作")
	for _, want := range []string{"已忽略的更新", "已停止检查", "其他检查结果", "技能更新设置…"} {
		if !tt.HasText(want) {
			t.Fatalf("更新页「…」菜单缺少 %q：%q", want, tt.Texts())
		}
	}
	tt.Key(0, ui.KeyEscape)

	// Scenario 2: 浏览页卡片右键打开同一套毛玻璃上下文菜单，选中卡片后详情栏「…」也可操作
	must(t, tt.Click("我的"))
	must(t, tt.RightClick("systematic-debugging"))
	for _, want := range []string{"打开", "在 Finder 中显示", "在编辑器中打开", "删除技能…"} {
		if !tt.HasText(want) {
			t.Fatalf("右键菜单缺少 %q：%q", want, tt.Texts())
		}
	}
	tt.Key(0, ui.KeyEscape)

	must(t, tt.Click("systematic-debugging"))
	tt.Advance(paneDuration)
	clickMenuButton(t, tt, "更多操作")
	for _, want := range []string{"在 Finder 中显示", "在编辑器中打开", "删除技能…"} {
		if !tt.HasText(want) {
			t.Fatalf("详情栏「…」菜单缺少 %q：%q", want, tt.Texts())
		}
	}
	tt.Key(0, ui.KeyEscape)

	// Scenario 3: 详情栏「添加到其他 Agent…」同样渲染统一毛玻璃菜单并可添加 Agent
	clickMenuButton(t, tt, "添加到其他 Agent…")
	if !tt.HasText("Codex") {
		t.Fatalf("添加到其他 Agent 菜单应列出 Codex：%q", tt.Texts())
	}
	clickMenuItem(t, tt, "添加到 Codex")
	if !a.lib.Get("systematic-debugging").DiscoveredIn("codex") {
		t.Fatal("应已将 systematic-debugging 添加到 Codex")
	}
}

func TestFrostedSelectButtonsOperateInSettingsGeneralAndUpdates(t *testing.T) {
	// Scenario 1: 设置「通用」标签里的「语言」下拉选择器
	a, _ := newTester(t)
	st := newUITester(a.settingsView, settingsWidth, settingsHeight)
	clickMenuButton(t, st, "语言")
	if !st.HasText("简体中文") || !st.HasText("English") {
		t.Fatalf("语言菜单应使用窗口内统一菜单渲染选项：%q", st.Texts())
	}
	clickMenuItem(t, st, "简体中文")
	if a.lib.Settings.Language != "zh-Hans" {
		t.Fatalf("选择 简体中文 后应更新语言设置，实际 = %q", a.lib.Settings.Language)
	}

	// Scenario 2: 设置「更新」标签里的「网络代理」与「更新后的旧版本」下拉选择器
	a.set.tab = tabUpdates
	a.lib.Settings.UpdateProxyMode = "system"
	a.lib.Settings.UpdateOldVersion = "trash"
	st.Frame()
	clickMenuButton(t, st, "网络代理")
	if !st.HasText("系统代理，可手动覆盖") || !st.HasText("只用手动代理") {
		t.Fatalf("网络代理下拉菜单未展开：%q", st.Texts())
	}
	clickMenuItem(t, st, "只用手动代理")
	if a.lib.Settings.UpdateProxyMode != "manual" {
		t.Fatalf("选择「只用手动代理」后应保存设置，实际 = %q", a.lib.Settings.UpdateProxyMode)
	}

	clickMenuButton(t, st, "更新后的旧版本")
	if !st.HasText("移到废纸篓") || !st.HasText("直接删除") {
		t.Fatalf("旧版本处理下拉菜单未展开：%q", st.Texts())
	}
	clickMenuItem(t, st, "直接删除")
	if a.lib.Settings.UpdateOldVersion != "delete" {
		t.Fatalf("选择「直接删除」后应保存设置，实际 = %q", a.lib.Settings.UpdateOldVersion)
	}
}

func TestArchitectureEnforcesUnifiedFrostedMenuSystem(t *testing.T) {
	// Ensure all non-test Go files route menus & popups through menu.go (MenuBase / ContextMenuBase),
	// never falling back to OS native .Menu() / .ContextMenu() or unstyled ad-hoc PopoverBase().
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	for _, ent := range entries {
		name := ent.Name()
		if ent.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		ast.Inspect(file, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			pos := fset.Position(call.Pos())
			switch sel.Sel.Name {
			case "Menu", "ContextMenu", "PopoverMenu", "PopoverMenuButton":
				t.Errorf("%s:%d: forbidden menu call .%s(); use unified frosted menu helpers in menu.go",
					filepath.Base(pos.Filename), pos.Line, sel.Sel.Name)
			case "PopoverBase", "MenuBase", "ContextMenuBase":
				if filepath.Base(pos.Filename) != "menu.go" {
					t.Errorf("%s:%d: forbidden ui.%s() outside menu.go; unify menus and popups under menu.go",
						filepath.Base(pos.Filename), pos.Line, sel.Sel.Name)
				}
			}
			return true
		})
	}
}

func TestContextMenuAndInspectorHotkeysActOnMenuTarget(t *testing.T) {
	// Given 我的技能视图中，先选中 vue-best-practices
	a, tt := newTester(t)
	must(t, tt.Click("vue-best-practices"))
	tt.Advance(paneDuration)

	// When 右键另一个可删除技能 systematic-debugging 并按下 ⌘⌫
	must(t, tt.RightClick("systematic-debugging"))
	if !tt.HasText("删除技能…") {
		t.Fatalf("应展开右键菜单：%q", tt.Texts())
	}
	tt.Key(ui.Cmd, ui.KeyBackspace)

	// Then 确认框指向右键目标 systematic-debugging（而非之前选中的 vue-best-practices），且右键菜单已关闭
	if !a.del.confirmOpen || !tt.HasText("删除 systematic-debugging？") {
		t.Fatalf("⌘⌫ 应作用于当前右键菜单目标 systematic-debugging，actual confirmOpen=%v texts=%q", a.del.confirmOpen, tt.Texts())
	}
	if tt.HasText("删除技能…") {
		t.Fatalf("触发快捷键后右键菜单应关闭：%q", tt.Texts())
	}
	must(t, tt.Click("取消"))

	// When 取消选中后，右键 systematic-debugging 并按下 Space
	a.selected = nil
	tt.Frame()
	must(t, tt.RightClick("systematic-debugging"))
	tt.Key(0, ui.KeySpace)

	// Then Space 应打开右键菜单对应的 systematic-debugging 并关闭菜单
	if a.reading == nil || a.reading.Name != "systematic-debugging" {
		t.Fatalf("右键菜单打开时按 Space 应打开 systematic-debugging，actual reading=%v", a.reading)
	}
	if tt.HasText("删除技能…") {
		t.Fatalf("按 Space 打开后菜单应关闭：%q", tt.Texts())
	}
	tt.Key(0, ui.KeyEscape)

	// When 先选中可删的 vue-best-practices，再通过搜索展示同页的只读插件技能 docx 并右键它按 ⌘⌫
	must(t, tt.Click("vue-best-practices"))
	tt.Advance(paneDuration)
	a.query = "docx"
	tt.Advance(paneDuration)
	must(t, tt.RightClick("docx"))
	tt.Key(ui.Cmd, ui.KeyBackspace)

	// Then 不应穿透菜单误删背景中选中的 vue-best-practices
	if a.del.confirmOpen {
		t.Fatalf("只读技能右键菜单打开时按 ⌘⌫ 不应触发背景选中项的删除：%q", tt.Texts())
	}
	a.query = ""
	tt.Key(0, ui.KeyEscape)

	// When 选中 vue-best-practices，展开详情栏「…」菜单并按 ⌘⌫
	must(t, tt.Click("vue-best-practices"))
	tt.Advance(paneDuration)
	clickMenuButton(t, tt, "更多操作")
	tt.Key(ui.Cmd, ui.KeyBackspace)

	// Then 确认框弹出且详情栏「…」菜单同步关闭
	if !a.del.confirmOpen || !tt.HasText("删除 vue-best-practices？") {
		t.Fatalf("详情栏菜单打开时 ⌘⌫ 应打开删除确认框：%q", tt.Texts())
	}
	if tt.HasText("删除技能…") {
		t.Fatalf("详情栏菜单在触发 ⌘⌫ 后应关闭：%q", tt.Texts())
	}
}

func TestBatchShortcutAndPickedSyncWithFilterAndScan(t *testing.T) {
	// Given 先开启「有可用更新」筛选，再进入批量操作，且批选 map 中混入了一条已被过滤隐藏的 git-flow
	a, tt := newTester(t)
	a.upd.report = library.UpdateReport{
		Checked:  time.Now(),
		Complete: true,
		Eligible: 2,
		Items: []library.UpdateItem{
			{Name: "vue-best-practices", State: library.UpdReady},
			{Name: "systematic-debugging", State: library.UpdReady},
		},
	}
	tt.Frame()
	openFilterMenu(t, tt)
	clickMenuItem(t, tt, "有可用更新")
	must(t, tt.Click("批量操作"))
	must(t, tt.Click("vue-best-practices"))
	a.batch.picked[a.lib.Get("git-flow").ID] = true

	// When 按 ⌘⌫
	tt.Key(ui.Cmd, ui.KeyBackspace)

	// Then 快捷键确认框只删除可见的 vue-best-practices（共 1 个），不带入被过滤隐藏的 git-flow
	if !a.del.confirmOpen || !tt.HasText("删除 vue-best-practices？") {
		t.Fatalf("批量 ⌘⌫ 应与顶栏筛选后的选择保持一致：%q", tt.Texts())
	}
	must(t, tt.Click("取消"))

	// And 退回正常浏览并清除筛选后再回到批量，之前被过滤隐藏的 git-flow 也不应复活为勾选态
	must(t, tt.Click("退出批量"))
	openFilterMenu(t, tt)
	clickMenuItem(t, tt, "清除筛选")
	must(t, tt.Click("批量操作"))
	if picked(a, "git-flow") {
		t.Fatal("被筛选隐藏的勾选记录应已清理，清除筛选后不应复活")
	}

	// When 退出批量后重开「有可用更新」、进入批量勾选两个可更新项，再由异步更新报告移除 vue-best-practices
	must(t, tt.Click("退出批量"))
	openFilterMenu(t, tt)
	clickMenuItem(t, tt, "有可用更新")
	must(t, tt.Click("批量操作"))
	must(t, tt.Click("vue-best-practices"))
	must(t, tt.Click("systematic-debugging"))
	a.upd.report = library.UpdateReport{
		Checked:  time.Now(),
		Complete: true,
		Eligible: 1,
		Items: []library.UpdateItem{
			{Name: "systematic-debugging", State: library.UpdReady},
		},
	}
	tt.Frame()
	if !tt.HasText("已选 1 项") || !tt.HasText("删除 1 个…") || picked(a, "vue-best-practices") {
		t.Fatalf("异步更新报告改变使已勾选项不可见后应同步清理勾选与顶栏计数：picked=%v texts=%q", a.batch.picked, tt.Texts())
	}
	tt.Key(ui.Cmd, ui.KeyBackspace)
	if !a.del.confirmOpen || !tt.HasText("删除 systematic-debugging？") {
		t.Fatalf("异步报告缩小选择后 ⌘⌫ 应只删除仍可见的 systematic-debugging：%q", tt.Texts())
	}
}

func TestSettingsMenusDoNotLeakAndBlockWatchSyncAfterClosing(t *testing.T) {
	// Given 打开设置「更新」标签并展开「网络代理」下拉菜单
	a, _ := newTester(t)
	a.set.open = true
	a.set.tab = tabUpdates
	a.lib.Settings.UpdateProxyMode = "system"
	st := newUITester(a.settingsView, settingsWidth, settingsHeight)
	st.Frame()
	clickMenuButton(t, st, "网络代理")
	if !a.anyMenuOpen() || !a.isWatchApplyDeferred() {
		t.Fatal("设置下拉菜单展开时应阻塞后台 watch 刷新")
	}

	// When 关闭设置窗口
	a.closeSettings()

	// Then 设置菜单打开状态应同步清空，不再永久阻塞后台 watch 刷新
	if a.anyMenuOpen() || a.isWatchApplyDeferred() {
		t.Fatalf("关闭设置窗口后不应残留开启的菜单阻塞 watch：menus=%v", a.menus)
	}

	// When 重新打开设置并展开下拉框后切换设置标签页
	a.set.open = true
	st.Frame()
	clickMenuButton(t, st, "网络代理")
	must(t, st.Click("通用"))

	// Then 切换设置标签后原标签下拉状态也应清空
	if a.anyMenuOpen() || a.isWatchApplyDeferred() {
		t.Fatalf("切换设置标签后不应残留开启的菜单：menus=%v", a.menus)
	}
}

func TestFilterPrunesCollectionSelectionAndUpdatesCollectionCounts(t *testing.T) {
	// Given 我的技能卡片视图，先选中 anthropics/skills 集合
	a, tt := newTester(t)
	a.upd.report = library.UpdateReport{
		Checked:  time.Now(),
		Complete: true,
		Eligible: 2,
		Items: []library.UpdateItem{
			{Name: "doc-coauthoring", State: library.UpdReady},
			{Name: "tailwind-rules", State: library.UpdReady},
		},
	}
	tt.Frame()
	must(t, tt.Click("anthropics/skills"))
	if a.coll == nil || a.coll.Name != "anthropics/skills" {
		t.Fatalf("应选中 anthropics/skills：coll=%v", a.coll)
	}

	// When 应用「有可用更新」筛选（anthropics/skills 中仅 doc-coauthoring 匹配 1/3，另一节含 tailwind-rules）
	openFilterMenu(t, tt)
	clickMenuItem(t, tt, "有可用更新")

	// Then 筛选后集合卡片角标与分节计数均与工具栏（2 个 · 1 个集合）一致，不混用未筛选总数 3
	if !tt.HasText("2 个 · 1 个集合") || !tt.HasText("1/3") {
		t.Fatalf("筛选态集合卡片计数应体现实匹配 1/3 与工具栏一致：%q", tt.Texts())
	}
	if !tt.HasText("1 · 1 个集合") {
		t.Fatalf("筛选态分节计数应与匹配技能数一致，而非集合总数 3：%q", tt.Texts())
	}

	// When 异步更新报告取消 doc-coauthoring 的可更新状态（仅留集合外技能）
	a.upd.report = library.UpdateReport{
		Checked:  time.Now(),
		Complete: true,
		Eligible: 1,
		Items:    []library.UpdateItem{{Name: "tailwind-rules", State: library.UpdReady}},
	}
	tt.Frame()

	// Then 被过滤隐藏的集合选中态 a.coll 同步清空
	if a.coll != nil {
		t.Fatalf("集合因筛选隐藏时应清空 a.coll，实际 = %v", a.coll.Name)
	}

	// Given 重新恢复 doc-coauthoring 可更新、进入打开的集合页并选中它，随后报告取消它的可更新态
	a.upd.report.Items = append(a.upd.report.Items, library.UpdateItem{Name: "doc-coauthoring", State: library.UpdReady})
	tt.Frame()
	doubleClick(t, tt, "anthropics/skills")
	if !tt.HasText("集合 · 显示 1 / 3 个技能 · 都在通用里") {
		t.Fatalf("打开集合页标题副文案应显示筛选匹配数与总数：%q", tt.Texts())
	}
	must(t, tt.Click("doc-coauthoring"))
	if a.selected == nil || a.inColl == nil {
		t.Fatalf("应在打开的集合内选中 doc-coauthoring：inColl=%v selected=%v", a.inColl, a.selected)
	}
	a.upd.report.Items = []library.UpdateItem{{Name: "tailwind-rules", State: library.UpdReady}}
	tt.Frame()

	// Then 打开的集合内部也同步清理被过滤掉的 a.selected
	if a.selected != nil {
		t.Fatalf("打开的集合内选中项被过滤隐藏时应清空 a.selected，实际 = %v", a.selected.Name)
	}
}

// SNAPSHOT_DIR=… go test -run TestMenuAndFilterSnapshots 输出 sf5-b 与 sf6 真实渲染截图。
func TestMenuAndFilterSnapshots(t *testing.T) {
	dir := os.Getenv("SNAPSHOT_DIR")
	if dir == "" {
		t.Skip("设置 SNAPSHOT_DIR 以输出截图")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}

	seedFilterApp := func(a *app) {
		a.upd.report = library.UpdateReport{
			Checked:  time.Now(),
			Complete: true,
			Eligible: 2,
			Items: []library.UpdateItem{
				{Name: "systematic-debugging", State: library.UpdReady},
				{Name: "vue-best-practices", State: library.UpdReady},
			},
		}
	}

	// 1. sf5-b: 筛选主菜单 + 按 Agent 子菜单展开（蓝色激活筛选态）
	a, tt := newTester(t)
	tt.SetScale(2)
	seedFilterApp(a)
	tt.Frame()
	openFilterMenu(t, tt)
	clickMenuItem(t, tt, "有可用更新")
	openFilterAgentSubmenu(t, tt)
	clickMenuItem(t, tt, "Claude Code")
	tt.Advance(paneDuration)
	openFilterAgentSubmenu(t, tt)
	tt.Advance(250 * time.Millisecond)
	writePNG(t, filepath.Join(dir, "sf5-b.png"), tt)

	// 2. sf5-after: 收起菜单后的已筛选态
	tt.Key(0, ui.KeyEscape)
	tt.Key(0, ui.KeyEscape)
	tt.Advance(250 * time.Millisecond)
	writePNG(t, filepath.Join(dir, "sf5-after.png"), tt)

	// 3. sf6-update: 更新页右上角「…」菜单
	aUpd := newApp(library.Sample())
	updateFixture(aUpd)
	ttUpd := newUITester(aUpd.view, 1280, 800)
	ttUpd.SetScale(2)
	ttUpd.Frame()
	clickMenuButton(t, ttUpd, "更多更新操作")
	ttUpd.Advance(250 * time.Millisecond)
	writePNG(t, filepath.Join(dir, "sf6-update.png"), ttUpd)

	// 4. sf6-inspector: 详情抽屉右上角「…」菜单 + 添加到其他 Agent
	_, ttInsp := newTester(t)
	ttInsp.SetScale(2)
	must(t, ttInsp.Click("systematic-debugging"))
	ttInsp.Advance(paneDuration)
	clickMenuButton(t, ttInsp, "更多操作")
	ttInsp.Advance(250 * time.Millisecond)
	writePNG(t, filepath.Join(dir, "sf6-inspector.png"), ttInsp)
	ttInsp.Key(0, ui.KeyEscape)
	clickMenuButton(t, ttInsp, "添加到其他 Agent…")
	ttInsp.Advance(250 * time.Millisecond)
	writePNG(t, filepath.Join(dir, "sf6-inspector-add.png"), ttInsp)

	// 5. sf6-settings: 设置「更新」标签网络代理下拉 + 「通用」标签语言下拉
	aSet := newApp(library.Sample())
	aSet.set.tab = tabUpdates
	aSet.lib.Settings.UpdateProxyMode = "system"
	aSet.lib.Settings.UpdateOldVersion = "trash"
	ttSet := newUITester(aSet.settingsView, settingsWidth, settingsHeight)
	ttSet.SetScale(2)
	ttSet.Frame()
	clickMenuButton(t, ttSet, "网络代理")
	ttSet.Advance(250 * time.Millisecond)
	writePNG(t, filepath.Join(dir, "sf6-settings.png"), ttSet)

	aGen := newApp(library.Sample())
	aGen.set.tab = tabGeneral
	ttGen := newUITester(aGen.settingsView, settingsWidth, settingsHeight)
	ttGen.SetScale(2)
	ttGen.Frame()
	clickMenuButton(t, ttGen, "语言")
	ttGen.Advance(250 * time.Millisecond)
	writePNG(t, filepath.Join(dir, "sf6-settings-general.png"), ttGen)
}

func TestBrowseFilterIsIndependentPerScopeMineAndBuiltin(t *testing.T) {
	// Given 「我的」与「内置」各自有独立的技能列表
	a, tt := newTester(t)
	a.upd.report = library.UpdateReport{
		Checked:  time.Now(),
		Complete: true,
		Eligible: 1,
		Items: []library.UpdateItem{
			{Name: "vue-best-practices", State: library.UpdReady},
		},
	}

	// When 在「我的」启用「按 Agent > Claude Code」和「有可用更新」
	openFilterAgentSubmenu(t, tt)
	clickMenuItem(t, tt, "Claude Code")
	clickMenuButton(t, tt, "筛选")
	clickMenuItem(t, tt, "有可用更新")
	if !tt.HasText("vue-best-practices") || tt.HasText("tailwind-rules") || tt.HasText("doc-coauthoring") {
		t.Fatalf("「我的」筛选后应只显示匹配技能：%q", tt.Texts())
	}

	// When 切换到「内置」页面
	must(t, tt.Click("内置"))
	tt.Frame()

	// Then 「内置」不应同步「我的」的筛选条件，也不能因「有可用更新」变成筛选空态
	if tt.HasText("没有符合筛选条件的技能") {
		t.Fatalf("切换到「内置」不应继承「我的」筛选而变成空态：%q", tt.Texts())
	}
	for _, name := range []string{"skill-creator", "memory", "cloud-deploy"} {
		if !tt.HasText(name) {
			t.Fatalf("「内置」应显示全部内置技能，缺少 %s：%q", name, tt.Texts())
		}
	}
	openFilterMenu(t, tt)
	if a.agentFilterSummary() != "" {
		t.Fatalf("「内置」按 Agent 摘要应为空，实际 = %q", a.agentFilterSummary())
	}
	if upd, ok := tt.AccessByRole(ui.RoleMenuItemCheckBox, "有可用更新"); !ok || upd.Checked {
		t.Fatalf("「内置」的「有可用更新」不应被勾选：%v ok=%v", upd.Checked, ok)
	}
	if clr, ok := tt.AccessByRole(ui.RoleMenuItem, "清除筛选"); !ok || !clr.Disabled {
		t.Fatalf("「内置」未启用筛选时「清除筛选」应禁用：%v ok=%v", clr.Disabled, ok)
	}
	clickMenuItem(t, tt, "按 Agent")
	if cc, ok := tt.AccessByRole(ui.RoleMenuItemCheckBox, "Claude Code"); !ok || cc.Checked {
		t.Fatalf("「内置」子菜单的 Claude Code 不应被勾选：%v ok=%v", cc.Checked, ok)
	}
	tt.Key(0, ui.KeyEscape)
	tt.Key(0, ui.KeyEscape)

	// When 在「内置」单独选择「按 Agent > WorkBuddy」
	openFilterAgentSubmenu(t, tt)
	clickMenuItem(t, tt, "WorkBuddy")
	if !tt.HasText("memory") || tt.HasText("skill-creator") || tt.HasText("cloud-deploy") {
		t.Fatalf("「内置」按 WorkBuddy 筛选应只显示 memory：%q", tt.Texts())
	}

	// When 切回「我的」，再打开筛选菜单
	must(t, tt.Click("我的"))
	tt.Frame()

	// Then 「我的」保持原本的筛选条件，不受「内置」筛选影响
	if !tt.HasText("vue-best-practices") || tt.HasText("tailwind-rules") || tt.HasText("doc-coauthoring") {
		t.Fatalf("切回「我的」后应保留「我的」独立筛选：%q", tt.Texts())
	}
	openFilterMenu(t, tt)
	if got := a.agentFilterSummary(); got != "Claude Code" {
		t.Fatalf("「我的」筛选菜单应保留 Claude Code 而非 WorkBuddy，实际 = %q", got)
	}
	if upd, ok := tt.AccessByRole(ui.RoleMenuItemCheckBox, "有可用更新"); !ok || !upd.Checked {
		t.Fatalf("「我的」的「有可用更新」应继续保持勾选：%v ok=%v", upd.Checked, ok)
	}
	tt.Key(0, ui.KeyEscape)

	// When 在「内置」点「清除筛选」
	must(t, tt.Click("内置"))
	tt.Frame()
	openFilterMenu(t, tt)
	clickMenuItem(t, tt, "清除筛选")
	if !tt.HasText("skill-creator") || !tt.HasText("memory") || !tt.HasText("cloud-deploy") {
		t.Fatalf("清除「内置」筛选后应恢复显示全部内置技能：%q", tt.Texts())
	}

	// Then 「我的」的筛选依然保持独立存在
	must(t, tt.Click("我的"))
	tt.Frame()
	if !tt.HasText("vue-best-practices") || tt.HasText("tailwind-rules") {
		t.Fatalf("清除「内置」筛选不应清掉「我的」筛选：%q", tt.Texts())
	}
}
