package main

import (
	"fmt"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"skillmanager/internal/library"
	"skillmanager/locales"
)

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func newTester(t *testing.T) (*app, *ui.Tester) {
	t.Helper()
	a := newApp(library.Sample())
	return a, newUITester(a.view, 1280, 800)
}

func doubleClick(t *testing.T, tt *ui.Tester, text string) {
	t.Helper()
	r, ok := tt.Find(text)
	if !ok {
		t.Fatalf("找不到 %q", text)
	}
	x, y := r.X+r.W/2, r.Y+r.H/2
	for range 2 {
		tt.Press(x, y)
		tt.Release(x, y)
	}
}

func TestDefaultIsMineAsCards(t *testing.T) {
	_, tt := newTester(t)
	for _, want := range []string{"我的技能", "卡片", "test-first"} {
		if !tt.HasText(want) {
			t.Errorf("缺少 %q", want)
		}
	}
	if tt.HasText("skill-creator") {
		t.Error("默认是我的，不列内置技能")
	}
	if tt.HasText("单独启用") {
		t.Error("没选中时不应出现详情栏")
	}
}

func TestClickingACardShowsItsDetails(t *testing.T) {
	a, tt := newTester(t)
	if err := tt.Click("vue-best-practices"); err != nil {
		t.Fatal(err)
	}
	if a.selected == nil || a.selected.Name != "vue-best-practices" {
		t.Fatalf("selected = %v", a.selected)
	}
	if !tt.HasText("所有 Agent") || !tt.HasText("~/.agents/skills/vue-best-practices") {
		t.Errorf("详情栏未出现：%q", tt.Texts())
	}
	tt.Key(0, ui.KeyEscape)
	if a.selected != nil {
		t.Error("Esc 应取消选中")
	}
}

func TestDoubleClickOpensReaderAndEscGoesBack(t *testing.T) {
	a, tt := newTester(t)
	doubleClick(t, tt, "test-first")
	if a.reading == nil {
		t.Fatal("双击应进入阅读视图")
	}
	for _, want := range []string{"已启用于", "电脑上还有 3 份同名副本", "工作流", "references"} {
		if !tt.HasText(want) {
			t.Errorf("阅读视图缺少 %q", want)
		}
	}
	if err := tt.Click("test-levels.md"); err != nil {
		t.Fatal(err)
	}
	if !tt.HasText("测试级别") {
		t.Error("点击文件应显示它的内容")
	}
	tt.Key(0, ui.KeyEscape)
	if a.reading != nil || a.selected == nil {
		t.Error("Esc 应回到浏览，并保留选中")
	}
}

func TestSpaceOpensTheSelectedSkill(t *testing.T) {
	a, tt := newTester(t)
	if err := tt.Click("code-review"); err != nil {
		t.Fatal(err)
	}
	tt.Key(0, ui.KeySpace)
	if a.reading == nil || a.reading.Name != "code-review" {
		t.Fatalf("reading = %v", a.reading)
	}
}

func TestBoardShowsSharedSkillsOnlyInSharedColumn(t *testing.T) {
	a, tt := newTester(t)
	a.mode = viewBoard
	tt.Frame()
	for _, want := range []string{"通用", "Claude Code", "Codex", "已停用", "未启用", "添加更多技能"} {
		if !tt.HasText(want) {
			t.Errorf("看板缺少 %q", want)
		}
	}
	if tt.HasText("skill-creator") {
		t.Error("看板只排我的技能")
	}
	// 每张卡片的名称同时出现在可访问标签和正文中。
	if n := countText(tt, "code-review"); n != 2 {
		t.Errorf("通用技能应只显示一张卡片，实际有 %d 个名称标签", n)
	}
	if n := countText(tt, "stock-analysis"); n != 4 {
		t.Errorf("通用之外的技能应保留两张 Agent 卡片，实际有 %d 个名称标签", n)
	}
	if _, ok := tt.Find("Claude Code 中 code-review 的操作"); ok {
		t.Error("通用技能不应出现在 Agent 列中")
	}
}

func countText(tt *ui.Tester, text string) int {
	n := 0
	for _, s := range tt.Texts() {
		if s == text {
			n++
		}
	}
	return n
}

func TestCardsSplitShared(t *testing.T) {
	_, tt := newTester(t)
	for _, want := range []string{"通用", "通用之外", "都会读到"} {
		if !tt.HasText(want) {
			t.Errorf("卡片缺少 %q", want)
		}
	}
}

func TestInspectorTidiesRedundantLinks(t *testing.T) {
	a, tt := newTester(t)
	if err := tt.Click("test-first"); err != nil {
		t.Fatal(err)
	}
	tt.Advance(paneDuration) // 详情栏滑进来
	hint := "Cursor 自己的目录里也有一个指向它的链接，和通用目录重复。"
	for _, want := range []string{"所有 Agent", "始终启用", hint} {
		if !tt.HasText(want) {
			t.Errorf("详情栏缺少 %q：%q", want, tt.Texts())
		}
	}
	if _, ok := tt.Find("开关 Codex"); !ok {
		t.Error("读通用目录的 Codex 应有原生开关")
	}
	tt.Scroll(1190, 580, 0, 350)
	if err := tt.Click("清理"); err != nil {
		t.Fatal(err)
	}
	if tt.HasText(hint) || a.err != "" {
		t.Errorf("清理后提示应消失：%q", a.err)
	}
}

func TestTidy(t *testing.T) {
	a, tt := newTester(t)
	if tt.HasText("全部技能") || tt.HasText("需要处理") || a.scope != string(library.ScopeMine) {
		t.Errorf("侧边栏不再有全部技能、需要处理，默认选中我的：%q", a.scope)
	}
	if err := tt.Click("整理"); err != nil {
		t.Fatal(err)
	}
	total := a.lib.Problems().Total()
	for _, want := range []string{fmt.Sprintf("%d 个问题", total), "内容相同的副本", "内容不同的副本", "重复的链接", "断掉的链接", "没写简介", "项目技能被全局链接", "全部收成一份", "brainstorming"} {
		if _, ok := tt.Access(want); !ok && !tt.HasText(want) {
			t.Errorf("整理页缺少 %q：%q", want, tt.Texts())
		}
	}
	if err := tt.Click("收成一份"); err != nil {
		t.Fatal(err)
	}
	if tt.HasText("全部收成一份") || !strings.Contains(shownText(a, fromTidy), "已收成一份") {
		t.Errorf("收成一份后这一类应消失，顶部提示收好了：%q", shownText(a, fromTidy))
	}
	if got := a.lib.Problems().Total(); got != total-1 {
		t.Errorf("还剩 %d 个问题，应为 %d", got, total-1)
	}
	if err := tt.Click("断掉的链接"); err != nil {
		t.Fatal(err)
	}
	for _, b := range []string{"重新连上", "删掉链接"} {
		if err := tt.Click(b); err != nil {
			t.Fatal(err)
		}
	}
	if tt.HasText("断掉的链接") {
		t.Error("处理完的一类应从左边消失")
	}
}

func TestMergingAllShowsProgressUntilItIsDone(t *testing.T) {
	a, tt := newTester(t)
	var work func(post func(func()))
	a.bg = func(w func(post func(func()))) { work = w }
	must(t, tt.Click("整理"))
	must(t, tt.Click("全部收成一份"))

	// 后台还没动：按钮变成进度胶囊，标题下面说正在收哪个，不能再点
	tt.Frame()
	for _, want := range []string{"已收成一份 0 / 1", "正在收 brainstorming…", "正在收成一份"} {
		if _, ok := tt.Access(want); !ok && !tt.HasText(want) {
			t.Errorf("收的时候应显示 %q：%q", want, tt.Texts())
		}
	}
	if tt.HasText("全部收成一份") {
		t.Error("收的时候不应还能点「全部收成一份」")
	}
	if info, ok := tt.Access("重新检测"); !ok || !info.Disabled {
		t.Error("收的时候「重新检测」应点不了")
	}

	work(func(fn func()) { fn() })
	tt.Frame()
	if a.tidy.work != nil || !strings.Contains(shownText(a, fromTidy), "brainstorming 已收成一份") || shownKind(a, fromTidy) != toastOK {
		t.Errorf("收完顶部应提示收好了：%q", shownText(a, fromTidy))
	}
	if tt.HasText("内容相同的副本") {
		t.Errorf("收完后这一类应消失：%q", tt.Texts())
	}
}

func TestTidyDone(t *testing.T) {
	a, tt := newTester(t)
	a.scope = scopeTidy
	p := a.lib.Problems()
	a.mergeAll(p.Same)
	for _, s := range p.Diff {
		a.merge(s, 0, true)
	}
	for _, e := range p.Extra {
		must(t, a.lib.DropLink(e.Skill, e.Agent))
	}
	for _, b := range p.Broken {
		must(t, a.lib.RemoveBroken(b.Path))
	}
	for _, s := range p.Global {
		must(t, a.lib.Unglobal(s))
	}
	for _, s := range p.NoDesc {
		s.Description = "补上了"
	}
	for _, s := range p.Incomplete {
		s.Defects = nil // 在编辑器里补上了
	}
	if _, err := a.lib.ClearRecords(p.Stale...); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !tt.HasText("没有发现问题") {
		t.Errorf("都处理完应显示没有发现问题：%q", tt.Texts())
	}
}

// 技能链到不存在的文件：整理页列出来，详情栏写明缺什么。
func TestIncompleteSkillIsShownInTidyAndInspector(t *testing.T) {
	a, tt := newTester(t)
	a.scope = scopeTidy
	a.tidy.kind = library.Incomplete
	tt.Frame()
	if !tt.HasText("文件不完整") || !tt.HasText("缺 references/lint-rules.md") {
		t.Fatalf("整理页应列出文件不完整的技能：%q", tt.Texts())
	}
	a.scope = string(library.ScopeMine)
	a.selected = a.lib.Get("test-first")
	tt.Frame()
	tt.Advance(paneDuration)
	tt.Frame()
	if !tt.HasText("缺 references/lint-rules.md") {
		t.Errorf("详情栏应写明缺的文件：%q", tt.Texts())
	}
}

// 同名副本保持可对比和合并；原生启停不再被副本锁住。
func TestCopiesAndCompare(t *testing.T) {
	a, tt := newTester(t)
	a.selected = a.lib.Get("test-first")
	tt.Frame()
	tt.Advance(paneDuration) // 详情栏滑进来
	tt.Frame()
	if !tt.HasText("电脑上还有 3 份同名副本") || !tt.HasText("开关 Claude Code") {
		t.Fatalf("应提示副本，并允许原生控制 Claude Code：%q", tt.Texts())
	}
	if !a.selected.CopyIn("claude-code") {
		t.Error("应保留 Claude Code 的独立副本信息")
	}
	if err := tt.Click("对比副本 ›"); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"test-first 的副本", "基准", "SKILL.md", "references", "test-levels.md", "仅基准", "保留这个版本"} {
		if !tt.HasText(want) {
			t.Errorf("对比页缺少 %q：%q", want, tt.Texts())
		}
	}
	if err := tt.Click("references/test-levels.md"); err != nil {
		t.Fatal(err)
	}
	if !tt.HasText("references/test-levels.md") {
		t.Fatalf("点击目录树文件后应切换到对应文件子标题：%q", tt.Texts())
	}
	if err := tt.Click("保留这个版本"); err != nil {
		t.Fatal(err)
	}
	if a.cmp != nil || a.selected == nil || a.notice == nil {
		t.Fatalf("保留后应回到详情栏：cmp=%v notice=%v", a.cmp, a.notice)
	}
	tt.Frame()
	if !tt.HasText("已收成一份") || tt.HasText("开关暂时不能用") {
		t.Errorf("回到详情栏应是绿色提示：%q", tt.Texts())
	}
	if a.selected.CopyIn("claude-code") {
		t.Error("收成一份后 Claude Code 的开关应能用")
	}
}

func TestIdenticalCopies(t *testing.T) {
	a, tt := newTester(t)
	a.selected = a.lib.Get("brainstorming")
	tt.Frame()
	tt.Advance(paneDuration)
	tt.Frame()
	if err := tt.Click("收成一份"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !tt.HasText("已收成一份") || !tt.HasText("2 处换成链接。") {
		t.Errorf("内容一样的直接收成一份：%q", tt.Texts())
	}
}

func TestProjectPageAndInspector(t *testing.T) {
	a, tt := newTester(t)
	if err := tt.Click("stock-plan"); err != nil {
		t.Fatal(err)
	}
	if p := a.project(); p == nil || p.Name != "stock-plan" {
		t.Fatalf("scope = %q", a.scope)
	}
	for _, want := range []string{"项目技能", "2 个技能", "添加技能", "release-checklist", "全局链接"} {
		if !tt.HasText(want) {
			t.Errorf("项目页缺少 %q：%q", want, tt.Texts())
		}
	}
	if tt.HasText("只读") {
		t.Error("项目页不应再显示只读标签")
	}
	if err := tt.Click("release-checklist"); err != nil {
		t.Fatal(err)
	}
	tt.Advance(paneDuration) // 详情栏滑进来
	for _, want := range []string{"谁会读到", "在 stock-plan 里工作时", "在任何项目里（全局链接）", "被 1 个 Agent 全局链接"} {
		if !tt.HasText(want) {
			t.Errorf("详情栏缺少 %q", want)
		}
	}
	if tt.HasText("单独启用") || tt.HasText("启用于") {
		t.Error("项目技能不应有开关")
	}
	if err := tt.Click("查看…"); err != nil {
		t.Fatal(err)
	}
	if !a.linkedOpen || !tt.HasText("1 个技能被全局链接到 stock-plan") {
		t.Errorf("「查看…」应打开全局链接弹窗：%q", tt.Texts())
	}
	if err := tt.Click("暂不处理"); err != nil {
		t.Fatal(err)
	}
	if a.linkedOpen {
		t.Error("「暂不处理」应关掉弹窗")
	}
}

func TestProjectBoardHasAColumnPerSkillDir(t *testing.T) {
	a, tt := newTester(t)
	a.scope, a.mode = projectScope(projectNamed(a, "stock-plan")), viewBoard
	tt.Frame()
	for _, want := range []string{"Claude Code", ".claude/skills", "release-checklist", "api-conventions"} {
		if !tt.HasText(want) {
			t.Errorf("项目看板缺少 %q", want)
		}
	}
}

func TestSidebarListsRecentProjectsAndFoldsTheRest(t *testing.T) {
	a, tt := newTester(t)
	for i := range 10 {
		a.lib.Projects = append(a.lib.Projects, &library.ProjectInfo{Name: fmt.Sprintf("extra-%d", i), Dir: fmt.Sprintf("~/code/extra-%d", i)})
	}
	total := len(a.lib.Projects)
	tt.Frame()
	shown := a.lib.Projects[recentProjects-1].Name
	hidden := a.lib.Projects[recentProjects].Name
	more := fmt.Sprintf("显示全部 %d 个", total)
	if !tt.HasText(shown) || tt.HasText(hidden) || !tt.HasText(more) {
		t.Fatalf("应列出最近 %d 个项目：%q", recentProjects, tt.Texts())
	}
	if err := tt.Click(more); err != nil {
		t.Fatal(err)
	}
	if !tt.HasText("extra-9") || !tt.HasText("收起") {
		t.Errorf("展开后应列出全部项目：%q", tt.Texts())
	}
	// 点「项目」分组标题收起项目列表，再点展开
	if err := tt.Click("项目"); err != nil {
		t.Fatal(err)
	}
	if a.projectsOpen || tt.HasText("extra-0") {
		t.Error("点「项目」应收起项目列表")
	}
	if err := tt.Click("项目"); err != nil {
		t.Fatal(err)
	}
	if !a.projectsOpen || !tt.HasText("extra-0") {
		t.Error("再点「项目」应展开项目列表")
	}
}

func TestSettingsWindow(t *testing.T) {
	a, tt := newTester(t)
	if err := tt.Click("设置（⌘,）"); err != nil {
		t.Fatal(err)
	}
	if !a.set.open {
		t.Fatal("齿轮应打开设置")
	}
	// 设置是单独的窗口，用自己的 Tester
	st := newUITester(a.settingsView, settingsWidth, settingsHeight)
	for _, want := range []string{"技能库", "~/.agents/skills", "20 个技能", "在 Finder 中显示", "检查并修复", "主题", "浅色", "跟随系统", "语言"} {
		if !st.HasText(want) {
			t.Errorf("通用缺少 %q：%q", want, st.Texts())
		}
	}
	if st.HasText("更改…") {
		t.Errorf("通用不应显示不可实现的「更改…」按钮：%q", st.Texts())
	}
	if tt.HasText("检查并修复") {
		t.Error("设置不应画在主窗口里")
	}
	if err := st.Click("检查并修复"); err != nil {
		t.Fatal(err)
	}
	if !st.HasText("检查了 0 个链接，全部正常。") {
		t.Errorf("应显示检查结果：%q", a.set.linkResult)
	}

	// Agent 管理：关掉 WorkBuddy 后看板里没有它的列
	if err := st.Click("Agent 管理"); err != nil {
		t.Fatal(err)
	}
	st.Frame()
	for _, want := range []string{"本机装了 7 个 Agent", "在界面里显示", "~/.workbuddy/skills", "刚刚检测", "手动添加技能目录…"} {
		if !st.HasText(want) {
			t.Errorf("Agent 管理缺少 %q：%q", want, st.Texts())
		}
	}
	if err := st.Click("在界面里显示 WorkBuddy"); err != nil {
		t.Fatal(err)
	}
	if !a.lib.Hidden("workbuddy") {
		t.Fatal("开关应隐藏 WorkBuddy")
	}

	if err := st.Click("项目"); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"自动发现", "77 个项目", "合并去重后 212 个项目，其中", "手动添加的文件夹", "v3-waterfall",
		"没有技能，不会显示", "git worktree 合并到主仓库显示"} {
		if !st.HasText(want) {
			t.Errorf("项目缺少 %q：%q", want, st.Texts())
		}
	}
	for _, gone := range []string{".claude · .agents"} {
		if st.HasText(gone) {
			t.Errorf("目录不应再用点号连成一行：%q", gone)
		}
	}
	for _, want := range []string{"在项目里查看的目录", ".claude/skills/", "还有 20 个…", "添加文件夹…"} {
		if !st.HasText(want) {
			t.Errorf("项目缺少 %q：%q", want, st.Texts())
		}
	}
	// 每一行自己的移除按钮（示例数据不能真的移除，显示原因）
	if err := st.Click("移除 v3-waterfall"); err != nil {
		t.Fatal(err)
	}
	if !st.HasText("示例数据不能移除项目") {
		t.Errorf("应显示移除失败的原因：%q", a.set.err)
	}
	st.Scroll(480, 300, 0, 280)
	if err := st.Click("还有 20 个…"); err != nil {
		t.Fatal(err)
	}
	if !st.HasText(".agent/skills/") {
		t.Error("展开后应列出全部目录")
	}

	if err := st.Click("技能更新"); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"网络与旧版本", "GitHub 授权"} {
		if !st.HasText(want) {
			t.Errorf("技能更新设置缺少 %q：%q", want, st.Texts())
		}
	}

	st.Key(ui.Cmd, ui.KeyW)
	if a.set.open {
		t.Fatal("⌘W 应关掉设置")
	}
	a.mode = viewBoard
	tt.Frame()
	if tt.HasText("WorkBuddy") {
		t.Error("关掉的 Agent 不应出现在看板里")
	}
	tt.Key(ui.Cmd, ui.KeyComma)
	if !a.set.open {
		t.Error("⌘, 应打开设置")
	}
}

func TestCollections(t *testing.T) {
	a, tt := newTester(t)
	a.scope = string(library.ScopeMine)
	tt.Frame()
	// 插件装的（document-skills）也在「我的」里，单独一组、只读
	for _, want := range []string{"36 个", "4 个集合", "20", "3 个集合", "插件安装", "document-skills", "open.feishu.cn", "lark-calendar、lark-doc、lark-im、lark-meeting、lark-minutes、lark-vc", "anthropics/skills", "2/3", "1/3"} {
		if !tt.HasText(want) {
			t.Errorf("卡片缺少 %q：%q", want, tt.Texts())
		}
	}
	if countText(tt, "lark-doc") != 0 {
		t.Error("集合里的技能应收进叠卡")
	}

	// 选中：详情栏是整组的开关
	if err := tt.Click("anthropics/skills"); err != nil {
		t.Fatal(err)
	}
	tt.Advance(paneDuration)
	for _, want := range []string{"1 月 25 日安装", "3/3 已启用", "2/3 已启用", "包含", "打开集合，查看全部 3 个 ›", "用 npx skills 安装"} {
		if !tt.HasText(want) {
			t.Errorf("详情栏缺少 %q：%q", want, tt.Texts())
		}
	}
	coll := a.coll
	if coll == nil || coll.Name != "anthropics/skills" {
		t.Fatalf("coll = %v", coll)
	}
	if _, ok := tt.Find("为 Claude Code 启用整个集合"); ok {
		t.Fatal("集合第一期不提供操作开关")
	}
	if coll.EnabledIn("claude-code") != 2 {
		t.Fatal("查看状态不应改变启用数量")
	}

	// 打开：只列集合里的技能，依赖多的标出来；Esc 回来，集合仍选中
	doubleClick(t, tt, "open.feishu.cn")
	if a.inColl == nil || countText(tt, "lark-doc") == 0 || !tt.HasText("6 个技能") || !tt.HasText("都在通用里") || !tt.HasText("2 个技能依赖它") {
		t.Fatalf("双击应打开集合：%q", tt.Texts())
	}
	if tt.HasText("test-first") {
		t.Error("打开的集合里只有它的技能")
	}
	tt.Key(0, ui.KeyEscape)
	if a.inColl != nil || a.coll == nil || a.coll.Name != "open.feishu.cn" {
		t.Errorf("Esc 应回到集合外并选中它：%v %v", a.inColl, a.coll)
	}

	// 搜索时不收（集合卡会预览 lark-calendar，直接列技能时它不该出现）
	a.coll, a.query = nil, "lark-doc"
	a.mode = viewCards
	tt.Frame()
	if countText(tt, "lark-doc") == 0 || countText(tt, "lark-calendar") != 0 {
		t.Errorf("搜索时应直接列出技能：%q", tt.Texts())
	}
}

// projectNamed 按名称找项目，找不到时返回 nil。
func projectNamed(a *app, names ...string) *library.ProjectInfo {
	for _, n := range names {
		for _, p := range a.lib.Projects {
			if p.Name == n {
				return p
			}
		}
	}
	return nil
}

// SNAPSHOT_DIR=… go test -run Snapshots 输出各页 2x 截图。
func TestSnapshots(t *testing.T) {
	dir := os.Getenv("SNAPSHOT_DIR")
	if dir == "" {
		t.Skip("设置 SNAPSHOT_DIR 以输出截图")
	}
	shots := []struct {
		name  string
		setup func(a *app)
	}{
		{"1-cards", func(a *app) {}},
		{"2-cards-selected", func(a *app) { a.selected = a.lib.Get("test-first") }},
		{"4-board", func(a *app) { a.mode = viewBoard; a.scope = string(library.ScopeMine) }},
		{"4-board-open", func(a *app) {
			a.mode, a.scope = viewBoard, string(library.ScopeMine)
			for _, ag := range a.lib.Agents {
				a.sharedOpen[ag.ID] = true
			}
		}},
		{"5-reader", func(a *app) { a.open(a.lib.Get("test-first")) }},
		{"6-project", func(a *app) {
			if p := projectNamed(a, "stock-plan"); p != nil {
				a.scope = projectScope(p)
			}
		}},
		{"7-project-selected", func(a *app) {
			p := projectNamed(a, "stock-plan")
			if p != nil && len(p.Skills) > 0 {
				a.scope, a.selected = projectScope(p), p.Skills[0]
			}
		}},
		{"8-project-linked", func(a *app) {
			if p := projectNamed(a, "stock-plan"); p != nil {
				a.scope = projectScope(p)
				a.showLinked(p)
			}
		}},
		{"c1-a-sets", func(a *app) { a.scope = string(library.ScopeMine) }},
		{"c1-b-open", func(a *app) {
			a.scope = string(library.ScopeMine)
			if len(a.lib.Collections) > 0 {
				a.openSet(a.lib.Collections[0])
			}
		}},
		{"c1-c-selected", func(a *app) {
			a.scope = string(library.ScopeMine)
			if len(a.lib.Collections) > 1 {
				a.coll = a.lib.Collections[1]
			}
		}},
		{"c1-e-all", func(a *app) {}},
		{"t1-tidy", func(a *app) { a.scope = scopeTidy }},
		{"t2-tidy-diff", func(a *app) { a.scope, a.tidy.kind = scopeTidy, library.DiffCopies }},
		{"t3-tidy-extra", func(a *app) { a.scope, a.tidy.kind = scopeTidy, library.ExtraLinks }},
		{"t4-tidy-broken", func(a *app) { a.scope, a.tidy.kind = scopeTidy, library.BrokenLinks }},
		{"t5-tidy-nodesc", func(a *app) { a.scope, a.tidy.kind = scopeTidy, library.NoDescription }},
		{"t6-tidy-global", func(a *app) { a.scope, a.tidy.kind = scopeTidy, library.GlobalLinked }},
		{"t8-tidy-records", func(a *app) { a.scope, a.tidy.kind = scopeTidy, library.StaleRecords }},
		{"t9-tidy-incomplete", func(a *app) { a.scope, a.tidy.kind = scopeTidy, library.Incomplete }},
		{"t7-collect-confirm", func(a *app) {
			a.scope, a.tidy.kind = scopeTidy, library.GlobalLinked
			if g := a.lib.Problems().Global; len(g) > 0 {
				a.collect, a.collectOpen = g[0], true
			}
		}},
		{"d1-compare", func(a *app) {
			if s := a.lib.Get("test-first"); s != nil {
				a.openCompare(s, false)
			}
		}},
		{"f1-b-copies", func(a *app) { a.selected = a.lib.Get("test-first") }},
		{"f1-d2-merged", func(a *app) {
			if a.lib.Home == "" { // 只在示例数据上收成一份：真实数据只读
				a.merge(a.lib.Get("brainstorming"), 0, false)
			}
		}},
		{"k1-palette-empty", func(a *app) {
			a.open(a.lib.Get("test-first"))
			a.closeReader()
			a.openPalette()
		}},
		{"k2-palette-test", func(a *app) { a.openPalette(); a.pal.query = "test" }},
		{"k3-palette-update", func(a *app) { a.openPalette(); a.pal.query = "更新" }},
		{"k4-palette-none", func(a *app) { a.openPalette(); a.pal.query = "kubernetes 部署" }},
		{"k5-palette-pinyin", func(a *app) { a.openPalette(); a.pal.query = "ceshi" }},
		{"9-project-board", func(a *app) {
			if p := projectNamed(a, "wechat", "seedmux"); p != nil {
				a.scope, a.mode = projectScope(p), viewBoard
			}
		}},
		{"pj-place-submenu", func(a *app) {
			setupProjectImportSnapshot(t, a, "stock-plan")
			a.menuEntry("import.place").open = true
		}},
		{"pj-readers", func(a *app) {
			setupProjectImportSnapshot(t, a, "stock-plan")
			a.imp.place.Agents = []string{"claude-code", "codex"}
			a.menuEntry("import.readers").open = true
		}},
		{"pj-done", func(a *app) {
			setupProjectImportSnapshot(t, a, "stock-plan")
			p := projectNamed(a, "stock-plan")
			a.imp.picked = map[string]bool{}
			a.imp.results = map[string]library.ImportOutcome{
				"release-checklist": {Name: "release-checklist", Added: true, Place: filepath.Join(p.Dir, ".agents/skills/release-checklist")},
			}
		}},
		{"pj-conflict-blocked", func(a *app) {
			setupProjectImportSnapshot(t, a, "stock-plan")
			p := projectNamed(a, "stock-plan")
			a.imp.checks["release-checklist"] = library.ImportCheck{
				State:    library.ImportConflict,
				Existing: filepath.Join(p.Dir, ".claude/skills/release-checklist"),
				Blocked:  locales.ImportProjectReplaceOwnCopy(".claude/skills", "Claude Code"),
			}
		}},
		{"pj-project-board", func(a *app) {
			if p := projectNamed(a, "stock-plan", "seedmux"); p != nil {
				a.scope, a.mode = projectScope(p), viewBoard
			}
		}},
		{"pj-card-hover", func(a *app) {
			if p := projectNamed(a, "stock-plan"); p != nil {
				a.scope, a.mode = projectScope(p), viewCards
			}
		}},
		{"pj-delete", func(a *app) {
			if p := projectNamed(a, "stock-plan"); p != nil && len(p.Skills) > 0 {
				a.scope = projectScope(p)
				a.requestDelete(p.Skills[0])
			}
		}},
	}
	// SNAPSHOT_REAL=1 时用本机的真实数据（只读）
	load := library.Sample
	if os.Getenv("SNAPSHOT_REAL") != "" {
		home, _ := os.UserHomeDir()
		load = func() *library.Library { return library.Scan(home) }
	}
	for _, s := range shots {
		a := newApp(load())
		s.setup(a)
		tt := newUITester(a.view, 1280, 800)
		tt.SetScale(2)
		tt.Frame()
		if s.name == "pj-place-submenu" {
			if r, ok := tt.Find("放进某个项目，只在这个项目里生效"); ok {
				tt.Move(r.X+r.W/2, r.Y+r.H/2)
				tt.Advance(300 * time.Millisecond)
				tt.Frame()
			}
			if !tt.HasText("添加项目…") {
				tt.Key(0, ui.KeyDown)
				tt.Key(0, ui.KeyDown)
				tt.Key(0, ui.KeyDown)
				tt.Key(0, ui.KeyRight)
				tt.Advance(250 * time.Millisecond)
				tt.Frame()
			}
		}
		if s.name == "pj-card-hover" {
			if r, ok := tt.Find("release-checklist"); ok {
				tt.Move(r.X+r.W/2, r.Y+r.H/2)
				tt.Advance(250 * time.Millisecond)
				tt.Frame()
			}
		}
		if a.pal.open { // 等弹窗淡入完
			tt.Advance(paletteTransition.Duration * 2)
			tt.Frame()
		}
		if a.reading != nil { // 等阅读页的简介和正文淡入完
			tt.Advance(time.Second)
		}
		writePNG(t, filepath.Join(dir, s.name+".png"), tt)
	}
	// 设置窗口四个标签，固定 760×540
	for i, name := range []string{"s1-general", "s2-agents", "s3-projects", "s4-updates"} {
		a := newApp(load())
		a.set.tab = i
		tt := newUITester(a.settingsView, settingsWidth, settingsHeight)
		tt.SetScale(2)
		tt.Frame()
		writePNG(t, filepath.Join(dir, name+".png"), tt)
	}
	// 首次启动的开场：第一幕；第二幕停在按钮（拨到落牌之后，带延迟的姿势
	// 第一次摆也要播完，再播 2.6 秒）；第三幕飞到一半、快落定
	a := newApp(load())
	a.wel.open = true
	tt := newUITester(a.view, 1280, 800)
	tt.SetScale(2)
	tt.Frame()
	tt.Advance(1350 * time.Millisecond)
	writePNG(t, filepath.Join(dir, "w1-welcome-hero.png"), tt)
	a.wel.start = tt.Now().Add(-msd(welReady + 450 - 2600))
	tt.Frame()
	tt.Advance(2600 * time.Millisecond)
	writePNG(t, filepath.Join(dir, "w2-welcome-ready.png"), tt)
	tt.Key(0, ui.KeyEnter)
	tt.Advance(620 * time.Millisecond)
	writePNG(t, filepath.Join(dir, "w3-welcome-stow.png"), tt)
	tt.Advance(700 * time.Millisecond)
	writePNG(t, filepath.Join(dir, "w4-welcome-landed.png"), tt)
}

func setupProjectImportSnapshot(t *testing.T, a *app, projName string) {
	t.Helper()
	p := projectNamed(a, projName)
	if p == nil {
		return
	}
	a.scope = scopeImport
	root := t.TempDir()
	skillDir := filepath.Join(root, "pack", "release-checklist")
	must(t, os.MkdirAll(skillDir, 0o755))
	md := "---\nname: release-checklist\ndescription: 发版前的检查项与回滚步骤\n---\n\n# release-checklist\n\n发版前逐项核对，任一步失败即按回滚步骤恢复。\n\n## 发布前检查\n\n1. 确认所有集成测试与端到端检查通过。\n2. 核对版本号、变更日志与迁移脚本。\n3. 确认回滚预案与监控告警已就绪。\n"
	must(t, os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(md), 0o644))
	b, err := library.ReadLocalImport(t.TempDir(), []string{skillDir})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(b.Discard)
	a.imp.batch = b
	a.imp.place = library.ImportPlacement{Project: p.Dir, Agents: a.lib.ImportProjectDefaultAgents()}
	a.imp.placed = true
	a.imp.picked = map[string]bool{"release-checklist": true}
	a.imp.checks = map[string]library.ImportCheck{"release-checklist": {State: library.ImportNew}}
	a.imp.focus = "release-checklist"
}

// snapshotDark 为真（SNAPSHOT_DARK=1）时测试窗口一开始就是深色外观，截图
// 测试出的是深色版：SNAPSHOT_DARK=1 SNAPSHOT_DIR=… go test -run Snapshot .
var snapshotDark = os.Getenv("SNAPSHOT_DARK") != ""

// newUITester 是 ui.NewTester，按 snapshotDark 定深浅色。
func newUITester(view func(*ui.Context), width, height int) *ui.Tester {
	return ui.NewTesterWith(view, width, height, ui.TesterOptions{Dark: snapshotDark})
}

func writePNG(t *testing.T, file string, tt *ui.Tester) {
	f, err := os.Create(file)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := png.Encode(f, tt.Image()); err != nil {
		t.Fatal(err)
	}
}

// TitleBarHiddenInset 只在 macOS 生效，其它平台降级成普通隐藏标题栏，三个
// 窗口按钮移到右上角占 138 DIP。顶栏要给它们让位，否则搜索框等右侧控件
// 被压住；侧栏顶部那 52px 是给左上角红绿灯留的，此时应收起来。
func TestToolbarClearsWindowButtons(t *testing.T) {
	const buttons, width = 138, 1280
	rightOf := func(tt *ui.Tester, label string) float32 {
		t.Helper()
		r, ok := tt.Find(label)
		if !ok {
			t.Fatalf("找不到 %q", label)
		}
		return r.X + r.W
	}

	a, tt := newTester(t)
	plain := rightOf(tt, "搜索名称或简介")

	tt.SetTitleBar(ui.TitleBar{Height: titleBarHeight, Right: buttons})
	if got := rightOf(tt, "搜索名称或简介"); got > width-buttons || got > plain-100 {
		t.Fatalf("浏览顶栏没给窗口按钮让位：右边界 %.0f -> %.0f", plain, got)
	}
	// 侧栏顶部那条放收起按钮，不再收成呼吸间距；按钮不压住下面的菜单
	toggle, ok := tt.Find(sidebarToggleLabel(false))
	if !ok {
		t.Fatal("找不到收起侧边栏的按钮")
	}
	if r, ok := tt.Find("整理"); !ok || toggle.Y+toggle.H > r.Y {
		t.Fatalf("收起按钮（底边 %.0f）压住了侧栏第一行（Y %.0f）", toggle.Y+toggle.H, r.Y)
	}

	// 阅读视图自己画了一条顶栏，同样要让位。
	a.open(a.lib.Get("test-first"))
	tt.Frame()
	if got := rightOf(tt, "在编辑器中打开"); got > width-buttons {
		t.Fatalf("阅读视图顶栏没给窗口按钮让位：右边界 %.0f", got)
	}
}

func TestRevealAndOpenFindSampleDirsUnderHome(t *testing.T) {
	// 示例数据的目录用 filepath.Join 拼，Windows 上是 ~\…
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip(err)
	}
	if got, want := expandHome(filepath.Join("~", ".claude", "skills")), filepath.Join(home, ".claude", "skills"); got != want {
		t.Errorf("应展开为 %q，得到 %q", want, got)
	}
	if got := expandHome("~other/x"); got != "~other/x" {
		t.Errorf("~ 后面不是分隔符时不展开，得到 %q", got)
	}
}

func TestCardScrollIsolatedAcrossPages(t *testing.T) {
	a := newApp(library.Sample())
	tt := newUITester(a.view, 900, 300)
	a.cardScroll().Y = 10000
	tt.Frame()
	if a.cardScroll().Y <= 0 {
		t.Fatalf("浏览页应可向下滚动，得到 Y = %v", a.cardScroll().Y)
	}
	browseY := a.cardScroll().Y

	var openFeishu *library.Collection
	for _, c := range a.lib.Collections {
		if c.Name == "open.feishu.cn" {
			openFeishu = c
			break
		}
	}
	if openFeishu == nil {
		t.Fatal("找不到 open.feishu.cn 集合")
	}
	a.openSet(openFeishu)
	tt.Frame()
	if a.cardScroll().Y != 0 {
		t.Fatalf("进集合页应从顶部开始，实际 Y = %v（浏览页 Y = %v）", a.cardScroll().Y, browseY)
	}

	a.closeSet()
	tt.Frame()
	if a.cardScroll().Y != browseY {
		t.Fatalf("返回浏览页应保留原滚动位置 %v，实际 Y = %v", browseY, a.cardScroll().Y)
	}

	if p := projectNamed(a, "stock-plan"); p != nil {
		a.scope = projectScope(p)
		tt.Frame()
		if a.cardScroll().Y != 0 {
			t.Fatalf("切到项目页应从顶部开始，实际 Y = %v", a.cardScroll().Y)
		}
	}
}
