package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"skillmanager/internal/library"
)

// hover 把指针移到 text 上。
func hover(t *testing.T, tt *ui.Tester, text string) {
	t.Helper()
	r, ok := tt.Find(text)
	if !ok {
		t.Fatalf("找不到 %q", text)
	}
	tt.Move(r.X+r.W/2, r.Y+r.H/2)
	tt.Frame()
}

// askToDelete 从详情栏的「…」菜单选「删除技能…」。
func askToDelete(t *testing.T, tt *ui.Tester, name string) {
	t.Helper()
	must(t, tt.Click(name))
	tt.Advance(paneDuration) // 详情栏滑进来
	clickMenuButton(t, tt, "更多操作")
	clickMenuItem(t, tt, "删除 Skill…")
	tt.Frame()
}

func TestUserSeesWhichAgentsLoseTheSkillBeforeDeleting(t *testing.T) {
	// Given 选中了 Claude Code 链接、Codex 读技能库的 vue-best-practices
	a, tt := newTester(t)

	// When 从详情栏的「…」菜单选「删除技能…」
	askToDelete(t, tt, "vue-best-practices")

	// Then 确认框先列出原件路径和链接路径，再紧凑列出受影响的 Agent，去掉「同上」和逐行扫描说明
	for _, want := range []string{
		"删除 vue-best-practices？",
		"原件会移到废纸篓，指向它的 3 个链接一并删除。",
		"涉及的文件",
		"~/.agents/skills/vue-best-practices",
		"~/.claude/skills/vue-best-practices",
		"原件",
		"链接",
		"以下 5 个 Agent 将无法使用",
		"Claude Code",
		"Codex",
		"Cursor",
		"移到废纸篓",
		"取消",
	} {
		if !tt.HasText(want) {
			t.Errorf("确认框缺少 %q：%q", want, tt.Texts())
		}
	}
	for _, forbid := range []string{"同上", "读的是 Skill 库，原件移走后就没有了"} {
		if tt.HasText(forbid) {
			t.Errorf("确认框不应再出现旧的冗长提示 %q：%q", forbid, tt.Texts())
		}
	}

	// When 直接按回车
	tt.Key(0, ui.KeyEnter)

	// Then 不会删掉
	if a.lib.Get("vue-best-practices") == nil {
		t.Error("回车不应直接删除")
	}
}

func TestUserCanBringADeletedSkillBackFromTheToast(t *testing.T) {
	// Given 删除了 vue-best-practices
	a, tt := newTester(t)
	askToDelete(t, tt, "vue-best-practices")
	must(t, tt.Click("移到废纸篓"))

	// Then 它不见了，提示条写着移到了废纸篓，选中移到别的技能上
	if _, shown := tt.Find("vue-best-practices"); a.lib.Get("vue-best-practices") != nil || shown {
		t.Fatal("删除后还在")
	}
	if !tt.HasText("已把 vue-best-practices 移到废纸篓") {
		t.Errorf("应出现提示条：%q", tt.Texts())
	}
	if a.selected == nil {
		t.Error("选中应移到下一个 Skill")
	}

	// When 点提示条上的「撤销」
	must(t, tt.Click("撤销"))

	// Then 它回来了，提示条收起
	if _, shown := tt.Find("vue-best-practices"); a.lib.Get("vue-best-practices") == nil || !shown {
		t.Error("撤销后没回来")
	}
	if tt.HasText("已把 vue-best-practices 移到废纸篓") {
		t.Error("撤销后提示条应收起")
	}
}

func TestUserUndoesTheLastDeletionWithCmdZ(t *testing.T) {
	// Given 删除了 vue-best-practices
	a, tt := newTester(t)
	askToDelete(t, tt, "vue-best-practices")
	must(t, tt.Click("移到废纸篓"))

	// When 按 ⌘Z
	tt.Key(ui.Cmd, ui.KeyZ)

	// Then 它回来了
	if a.lib.Get("vue-best-practices") == nil {
		t.Error("⌘Z 应撤销删除")
	}
}

func TestHoveringMyCardOffersPreviewRevealAndDelete(t *testing.T) {
	// Given 浏览我的技能，未选中任何卡片
	a, tt := newTester(t)

	// When 指针停在 vue-best-practices 的卡片上
	hover(t, tt, "vue-best-practices")

	// Then 卡片底部换成「预览」「在 Finder 中显示」「删除」
	for _, want := range []string{"预览", "在 Finder 中显示", "删除"} {
		if !tt.HasText(want) {
			t.Errorf("悬停时缺少 %q", want)
		}
	}

	// When 点「删除」
	must(t, tt.Click("删除"))
	tt.Advance(paneDuration)

	// Then 弹出确认框，且不选中卡片、不唤起右侧详情抽屉
	if !tt.HasText("删除 vue-best-practices？") {
		t.Errorf("应弹出确认框：%q", tt.Texts())
	}
	if a.selected != nil {
		t.Errorf("点悬停的「删除」不应选中卡片或唤起详情栏：selected = %q", a.selected.Name)
	}
}

func TestClickingHoverDeleteKeepsExistingSelectionUnchanged(t *testing.T) {
	// Given 已选中 brainstorming（详情栏显示它）
	a, tt := newTester(t)
	must(t, tt.Click("brainstorming"))
	tt.Advance(paneDuration)
	if a.selected == nil || a.selected.Name != "brainstorming" {
		t.Fatalf("应先选中 brainstorming，实际 selected = %v", a.selected)
	}

	// When 指针停在另一张卡片 vue-best-practices 上并点「删除」
	hover(t, tt, "vue-best-practices")
	must(t, tt.Click("删除"))
	tt.Advance(paneDuration)

	// Then 弹出 vue-best-practices 的删除确认框，原选中项保持 brainstorming 不变
	if !tt.HasText("删除 vue-best-practices？") {
		t.Errorf("应弹出确认框：%q", tt.Texts())
	}
	if a.selected == nil || a.selected.Name != "brainstorming" {
		t.Errorf("点另一张卡片的「删除」不应改变原选中项：selected = %v", a.selected)
	}
}

func TestBuiltinSkillsOfferNoWayToDelete(t *testing.T) {
	// Given 浏览内置技能
	_, tt := newTester(t)
	must(t, tt.Click("内置"))

	// When 指针停在 skill-creator 上、右键它、选中它
	hover(t, tt, "skill-creator")
	if tt.HasText("删除") {
		t.Error("内置卡片悬停时不应有「删除」")
	}
	must(t, tt.RightClick("skill-creator"))
	if !tt.HasText("打开") {
		t.Fatalf("内置 Skill 右键菜单应打开：%q", tt.Texts())
	}
	if tt.HasText("删除 Skill…") {
		t.Error("内置 Skill 的右键菜单不应有删除")
	}
	tt.Key(0, ui.KeyEscape)
	must(t, tt.Click("skill-creator"))

	// Then 详情栏也没有「…」菜单
	if _, ok := tt.Find("更多操作"); ok {
		t.Error("内置 Skill 的详情栏不应有「…」菜单")
	}
}

func TestDeletingASkillWithCopiesLeadsToComparingFirst(t *testing.T) {
	// Given test-first 另有 3 份同名副本，其中有内容不同的
	a, tt := newTester(t)

	// When 右键它选「删除技能…」
	must(t, tt.RightClick("test-first"))
	clickMenuItem(t, tt, "删除 Skill…")
	tt.Frame()

	// Then 提示先去对比，不给删
	if !tt.HasText("test-first 还有 3 份同名副本") || !tt.HasText("对比副本…") || tt.HasText("移到废纸篓") {
		t.Fatalf("应提示先去对比：%q", tt.Texts())
	}
	// 并列出副本在哪、内容和这份一不一样
	if !tt.HasText("内容不同") || !tt.HasText("内容相同") {
		t.Errorf("应列出各份副本和内容异同：%q", tt.Texts())
	}

	// When 点「对比副本…」
	must(t, tt.Click("对比副本…"))

	// Then 打开对比页，技能还在
	if a.cmp == nil {
		t.Error("应打开对比页")
	}
	if a.lib.Get("test-first") == nil {
		t.Error("不应删掉")
	}
}

func TestUserDeletesSeveralUnusedSkillsAndUndoesThemAtOnce(t *testing.T) {
	// Given 在使用情况页，点「批量删除」，勾了 git-flow 和 dataviz
	a, tt := newUsageTester(t)
	must(t, tt.Click("使用情况"))
	must(t, tt.Click("批量删除"))
	must(t, tt.Click("git-flow"))
	must(t, tt.Click("dataviz"))
	if !tt.HasText("已选 2 个") {
		t.Fatalf("应显示已选 2 个：%q", tt.Texts())
	}

	// When 点「删除…」并确认
	must(t, tt.Click("删除…"))
	if !tt.HasText("删除 2 个 Skill？") {
		t.Fatalf("应弹出批量确认框：%q", tt.Texts())
	}
	must(t, tt.Click("移到废纸篓"))

	// Then 两个都删了，退出选择
	if a.lib.Get("git-flow") != nil || a.lib.Get("dataviz") != nil {
		t.Fatal("应两个都删掉")
	}
	if tt.HasText("已选") {
		t.Error("删完应退出选择")
	}

	// When 按 ⌘Z
	tt.Key(ui.Cmd, ui.KeyZ)

	// Then 两个都回来了
	if a.lib.Get("git-flow") == nil || a.lib.Get("dataviz") == nil {
		t.Error("一次撤销应两个都放回")
	}
}

func TestBuiltinUnusedSkillsCannotBeSelectedForDeletion(t *testing.T) {
	// Given 在使用情况页的「没用过的」，切到内置那一页
	_, tt := newUsageTester(t)
	must(t, tt.Click("使用情况"))
	must(t, tt.Click("内置 1"))

	// When 点「批量删除」
	must(t, tt.Click("批量删除"))

	// Then 回到我的那一页去勾，内置的 skill-creator 不在可勾的列表里
	if !tt.HasText("已选 0 个") || !tt.HasText("git-flow") || tt.HasText("skill-creator") {
		t.Errorf("批量删除只勾我的 Skill：%q", tt.Texts())
	}
}

func TestUserReachesBulkDeleteWithoutScrollingInADefaultWindow(t *testing.T) {
	// Given 默认大小的窗口打开使用情况页，「没用过的」在「用过的」下面、窗口外
	_, tt := newTester(t)
	must(t, tt.Click("使用情况"))
	tt.Advance(time.Second)

	// When 点工具栏上的「批量删除」（Click 点不到窗口外的按钮）
	must(t, tt.Click("批量删除"))
	tt.Advance(time.Second)

	// Then 「没用过的」滚了上来，能直接勾，选择条在窗口里
	must(t, tt.Click("git-flow"))
	if !tt.HasText("已选 1 个") {
		t.Fatalf("应勾上 git-flow：%q", tt.Texts())
	}
	must(t, tt.Click("删除…"))
	if !tt.HasText("删除 git-flow？") && !tt.HasText("删除 1 个 Skill？") {
		t.Errorf("应弹出确认框：%q", tt.Texts())
	}
}

func TestUserLeavesBulkDeleteWithEscape(t *testing.T) {
	// Given 在使用情况页勾了 git-flow
	a, tt := newUsageTester(t)
	must(t, tt.Click("使用情况"))
	must(t, tt.Click("批量删除"))
	must(t, tt.Click("git-flow"))

	// When 按 Esc
	tt.Key(0, ui.KeyEscape)

	// Then 退出选择，勾的也清掉
	if a.del.picking || len(a.del.picked) > 0 || !tt.HasText("批量删除") {
		t.Error("Esc 应退出选择")
	}
}

func TestBatchDeleteConfirmShowsEachSkillPathAndAffectedAgents(t *testing.T) {
	// Given 准备批量删除 git-flow、dataviz、design-lab
	a, tt := newTester(t)

	// When 发起批量删除
	a.requestDelete(a.lib.Get("git-flow"), a.lib.Get("dataviz"), a.lib.Get("design-lab"))
	tt.Frame()

	// Then 确认框列出每个技能的原件路径、附加链接数和受影响 Agent 汇总
	for _, want := range []string{
		"删除 3 个 Skill？",
		"原件会移到废纸篓，指向它们的 1 个链接一并删除。",
		"将移到废纸篓的 Skill",
		"~/.claude/skills/git-flow",
		"~/.claude/skills/dataviz",
		"~/.agents/skills/design-lab",
		"另有 1 个链接",
		"以下 4 个 Agent 将受影响",
	} {
		if !tt.HasText(want) {
			t.Errorf("批量删除确认框缺少 %q：%q", want, tt.Texts())
		}
	}
}

func TestDeleteConfirmHighlightsGlobalProjectLinkAndOutsideOriginal(t *testing.T) {
	// Given 一个带全局链接的项目技能 release-checklist
	a, tt := newTester(t)
	var projSkill *library.Skill
	for _, s := range a.lib.Skills {
		if s.Source == library.Project && s.Name == "release-checklist" {
			projSkill = s
			break
		}
	}
	if projSkill == nil {
		t.Fatal("未找到示例项目 Skill release-checklist")
	}

	// When 发起删除
	a.requestDelete(projSkill)
	tt.Frame()

	// Then 确认框列出项目原件路径和全局链接路径，并给出全局影响说明
	for _, want := range []string{
		"~/code/storefront/.agents/skills/release-checklist",
		"~/.claude/skills/release-checklist",
		"全局链接",
		"删掉全局链接后，Claude Code 在任何项目里都读不到它。",
	} {
		if !tt.HasText(want) {
			t.Errorf("项目 Skill 确认框缺少 %q：%q", want, tt.Texts())
		}
	}

	// Given 一个原件在技能目录之外的技能
	s := a.lib.Get("design-lab")
	s.Real = "/tmp/outside-skills/design-lab"
	a.lib.Home = "/Users/someone"

	// When 发起删除
	a.requestDelete(s)
	tt.Frame()

	// Then 原件路径下方出现目录外风险提示
	for _, want := range []string{
		"原件位于 Skill 目录之外，会整个移到废纸篓。",
		"/tmp/outside-skills/design-lab",
		"原件在 Skill 目录之外，请确认这不是仍在使用的文件夹",
	} {
		if !tt.HasText(want) {
			t.Errorf("外部原件确认框缺少 %q：%q", want, tt.Texts())
		}
	}
}

// SNAPSHOT_DIR=… go test -run DeleteSnapshots 输出删除相关界面的截图。
func TestDeleteSnapshots(t *testing.T) {
	dir := os.Getenv("SNAPSHOT_DIR")
	if dir == "" {
		t.Skip("设置 SNAPSHOT_DIR 以输出截图")
	}
	for name, steps := range map[string]func(a *app, tt *ui.Tester){
		"del-hover": func(_ *app, tt *ui.Tester) { hover(t, tt, "vue-best-practices") },
		"del-pane":  func(_ *app, tt *ui.Tester) { must(t, tt.Click("vue-best-practices")); tt.Advance(paneDuration) },
		"del-confirm": func(a *app, _ *ui.Tester) {
			a.requestDelete(a.lib.Get("vue-best-practices"))
		},
		"del-copies": func(a *app, _ *ui.Tester) { a.requestDelete(a.lib.Get("test-first")) },
		"del-toast": func(a *app, _ *ui.Tester) {
			a.requestDelete(a.lib.Get("vue-best-practices"))
			a.doDelete()
		},
		"del-pick": func(_ *app, tt *ui.Tester) {
			must(t, tt.Click("使用情况"))
			tt.Advance(time.Second) // 等用量载入、布局稳定再找按钮
			must(t, tt.Click("批量删除"))
			tt.Advance(time.Second) // 等「没用过的」滚上来
			must(t, tt.Click("git-flow"))
			must(t, tt.Click("dataviz"))
		},
		"del-batch": func(a *app, tt *ui.Tester) {
			a.scope = scopeUsage
			a.requestDelete(a.lib.Get("git-flow"), a.lib.Get("dataviz"), a.lib.Get("design-lab"))
		},
		"del-project": func(a *app, _ *ui.Tester) {
			for _, s := range a.lib.Skills {
				if s.Source == library.Project && s.Name == "release-checklist" {
					a.requestDelete(s)
					return
				}
			}
		},
		"del-outside": func(a *app, _ *ui.Tester) {
			s := a.lib.Get("design-lab")
			s.Real = "/Users/someone/Documents/my-skills/design-lab"
			a.lib.Home = "/Users/someone"
			a.requestDelete(s)
		},
	} {
		a, tt := newTester(t)
		tt.SetScale(2)
		tt.Frame()
		steps(a, tt)
		tt.Frame()
		tt.Advance(paneDuration)
		writePNG(t, filepath.Join(dir, name+".png"), tt)
	}
}

func TestDeleteDialogRefreshesLinkPreviewWhenReopened(t *testing.T) {
	home := t.TempDir()
	source := filepath.Join(home, ".agents", "skills", "handbook")
	must(t, os.MkdirAll(source, 0755))
	must(t, os.WriteFile(filepath.Join(source, "SKILL.md"), []byte("# Handbook"), 0644))
	dir := filepath.Join(home, ".claude", "skills")
	must(t, os.MkdirAll(dir, 0755))
	link := filepath.Join(dir, "handbook")
	must(t, os.Symlink(source, link))
	a := newApp(library.Scan(home))
	s := a.lib.Get("handbook")
	if s == nil {
		t.Fatal("missing handbook")
	}
	tt := newUITester(a.view, 1280, 800)
	a.requestDelete(s)
	tt.Frame()
	if !tt.HasText(a.lib.Display(link)) {
		t.Fatal("expected the linked path in the preview")
	}
	must(t, os.Remove(link))
	tt.Frame()
	if !tt.HasText(a.lib.Display(link)) {
		t.Fatal("redrawing changed the open preview")
	}
	must(t, tt.Click("取消"))
	a.requestDelete(s)
	tt.Frame()
	if tt.HasText(a.lib.Display(link)) {
		t.Fatal("reopened preview contains a removed link")
	}
}
