package main

import (
	"context"
	"errors"
	"image/png"
	"os"
	"path/filepath"
	"skillmanager/internal/library"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
)

func updateFixture(a *app) {
	a.lib.Skills = append(a.lib.Skills, &library.Skill{ID: "mine:vite", Name: "vite", Source: library.Mine, Dir: "~/.agents/skills/vite"})
	a.scope = scopeUpdates
	a.upd.report = library.UpdateReport{Checked: time.Now(), Complete: true, Eligible: 3, Items: []library.UpdateItem{
		{Name: "vite", Source: library.SourceID{Type: "github", Repo: "antfu/skills"}, State: library.UpdReady, Reason: "可更新", Commit: "3f8a21c", CommitDate: time.Now()},
		{Name: "vue-best-practices", Source: library.SourceID{Type: "github", Repo: "vuejs-ai/skills"}, State: library.UpdBoth, Reason: "本地改过，且上游有更新"},
		{Name: "test-first", Source: library.SourceID{Type: "github", Repo: "anthropics/skills"}, State: library.UpdCopies, Reason: "有独立副本，需先收成一份"},
		{Name: "private-tools", Source: library.SourceID{Type: "github", Repo: "acme/private-skills"}, State: library.UpdFailed, Reason: "仓库访问不了（可能已删除或转为私有）"},
		{Name: "open.feishu.cn", Names: []string{"lark-doc", "lark-im"}, Source: library.SourceID{Type: "well-known", Repo: "open.feishu.cn"}, State: library.UpdWebsite, Reason: "本应用不自动更新", Command: "npx skills update lark-doc lark-im -g -y"},
	}}
}
func TestUpdatePageSeparatesSafeUpdatesFromConfirmations(t *testing.T) {
	a := newApp(library.Sample())
	updateFixture(a)
	tt := newUITester(a.view, 1280, 800)
	for _, s := range []string{"更新这 1 个技能", "更新前需确认", "需要处理", "检查失败", "由发布网站维护"} {
		if !tt.HasText(s) {
			t.Errorf("missing %s: %v", s, tt.Texts())
		}
	}
	must(t, tt.Click("vite"))
	if a.selected == nil || a.selected.Name != "vite" {
		t.Fatal("skill should open inspector")
	}
}
func TestUpdateFirstVisitDoesNotClaimEverythingIsLatest(t *testing.T) {
	a, tt := newTester(t)
	must(t, tt.Click("技能更新"))
	if !tt.HasText("还没检查过技能更新") || !tt.HasText("检查技能更新") {
		t.Fatal(tt.Texts())
	}
	a.upd.report = library.UpdateReport{Checked: time.Now(), Complete: false}
	tt.Frame()
	if tt.HasText("目前没有需要更新的技能") {
		t.Fatal("partial check claimed completion")
	}
}
func TestUpdateSnapshots(t *testing.T) {
	dir := os.Getenv("SNAPSHOT_DIR")
	if dir == "" {
		t.Skip("SNAPSHOT_DIR")
	}
	for _, w := range []int{1280, 960} {
		a := newApp(library.Sample())
		updateFixture(a)
		tt := newUITester(a.view, w, 900)
		img := tt.Image()
		name := "updates.png"
		if w == 960 {
			name = "updates-narrow.png"
		}
		f, e := os.Create(filepath.Join(dir, name))
		must(t, e)
		must(t, png.Encode(f, img))
		must(t, f.Close())
	}
}

func TestBackgroundUpdateFailureSurvivesQueuedDelivery(t *testing.T) {
	a := newApp(library.Scan(t.TempDir()))
	var queue []func()
	a.bg = func(work func(func(func()))) { work(func(fn func()) { queue = append(queue, fn) }) }
	a.startUpdateWork(taskCheck, func(context.Context, *library.Library, func(func())) updateWorkResult {
		return updateWorkResult{err: errors.New("disk failure")}
	})
	for _, fn := range queue {
		fn()
	}
	if a.upd.err != "disk failure" {
		t.Fatalf("lost error: %q", a.upd.err)
	}
}

func TestIdenticalCopiesOpenExistingConsolidationPage(t *testing.T) {
	a := newApp(library.Sample())
	same := a.lib.Problems().Same
	if len(same) == 0 {
		t.Fatal("fixture missing copies")
	}
	s := same[0]
	a.scope = scopeUpdates
	a.upd.report = library.UpdateReport{Items: []library.UpdateItem{{Name: s.Name, State: library.UpdCopies}}}
	tt := newUITester(a.view, 1280, 800)
	must(t, tt.Click("收成一份"))
	if a.scope != scopeTidy || a.tidy.kind != library.SameCopies {
		t.Fatal("identical copies action did nothing")
	}
}

func TestUpdateEmptySnapshots(t *testing.T) {
	dir := os.Getenv("SNAPSHOT_DIR")
	if dir == "" {
		t.Skip("SNAPSHOT_DIR")
	}
	for _, w := range []int{1280, 960} {
		a := newApp(library.Sample())
		a.scope = scopeUpdates
		tt := newUITester(a.view, w, 800)
		name := "empty-after.png"
		if w == 960 {
			name = "empty-narrow.png"
		}
		f, err := os.Create(filepath.Join(dir, name))
		must(t, err)
		must(t, png.Encode(f, tt.Image()))
		must(t, f.Close())
	}
}

func updateFullFixture(a *app) {
	now := time.Now()
	a.lib.Skills = append(a.lib.Skills,
		&library.Skill{ID: "mine:vite", Name: "vite", Source: library.Mine, Dir: "~/.agents/skills/vite"},
		&library.Skill{ID: "mine:vitest", Name: "vitest", Source: library.Mine, Dir: "~/.agents/skills/vitest"},
		&library.Skill{ID: "mine:kami", Name: "kami", Source: library.Mine, Dir: "~/.agents/skills/kami"},
		&library.Skill{ID: "mine:turborepo", Name: "turborepo", Source: library.Mine, Dir: "~/.agents/skills/turborepo"},
	)
	a.scope = scopeUpdates
	a.upd.report = library.UpdateReport{
		Checked:  now.Add(-3 * time.Minute),
		Complete: false,
		Eligible: 10,
		Reset:    now.Add(18 * time.Minute),
		Items: []library.UpdateItem{
			{Name: "vite", Source: library.SourceID{Type: "github", Repo: "antfu/skills"}, State: library.UpdReady, Reason: "可更新", Commit: "3f8a21c", CommitDate: now.Add(-2 * 24 * time.Hour)},
			{Name: "vitest", Source: library.SourceID{Type: "github", Repo: "antfu/skills"}, State: library.UpdReady, Reason: "可更新", Commit: "91d0e4b", CommitDate: now.Add(-5 * 24 * time.Hour)},
			{Name: "vue-best-practices", Source: library.SourceID{Type: "github", Repo: "vuejs-ai/skills"}, State: library.UpdBoth, Reason: "本地改过，且上游有更新", Commit: "c74b92e", CommitDate: now.Add(-24 * time.Hour)},
			{Name: "test-first", Source: library.SourceID{Type: "github", Repo: "anthropics/skills"}, State: library.UpdCopies, Reason: "有 2 份独立副本，需先收成一份"},
			{Name: "kami", Source: library.SourceID{Type: "github", Repo: "obra/superpowers"}, State: library.UpdMovedOut, Reason: "已从当前库移出"},
			{Name: "turborepo", Source: library.SourceID{Type: "github", Repo: "vercel/turborepo"}, State: library.UpdRemoved, Reason: "上游仓库已删除此技能（404）"},
			{Name: "private-tools", Source: library.SourceID{Type: "github", Repo: "acme/private-skills"}, State: library.UpdFailed, Reason: "仓库访问不了（可能已删除或转为私有）"},
			{Name: "open.feishu.cn", Names: []string{"lark-doc", "lark-im"}, Source: library.SourceID{Type: "well-known", Repo: "open.feishu.cn"}, State: library.UpdWebsite, Command: "npx skills update lark-doc lark-im -g -y"},
			{Name: "agently-mail", Names: []string{"agently-mail"}, Source: library.SourceID{Type: "well-known", Repo: "agently-mail.dev"}, State: library.UpdWebsite, Reason: "安装记录不完整，建议重新安装", Command: "npx skills update agently-mail -g -y"},
		},
	}
}

func findSkillByID(a *app, id string) *library.Skill {
	for _, s := range a.lib.Skills {
		if s.ID == id {
			return s
		}
	}
	return nil
}

func TestUpdateFullGroupsAndSecondaryStates(t *testing.T) {
	a := newApp(library.Sample())
	updateFullFixture(a)
	tt := newUITester(a.view, 1280, 960)
	for _, want := range []string{
		"更新这 2 个技能",
		"上游提交（Commit）",
		"查看差异",
		"vitest",
		"本地改过，且上游",
		"有内容不同的副本，需先收成一份",
		"装回后可更新",
		"保留本地，不再检查更新",
		"删除技能",
		"复制更新命令",
	} {
		if !tt.HasText(want) {
			t.Errorf("missing %q in full update view: %v", want, tt.Texts())
		}
	}
	wantDate := a.upd.report.Items[0].CommitDate.Local().Format("2006-01-02 15:04")
	if !tt.HasText(wantDate) {
		t.Errorf("missing full commit date %q in full update view: %v", wantDate, tt.Texts())
	}

	// upd1-d: 已忽略的更新页
	a.upd.report.Items = append(a.upd.report.Items,
		library.UpdateItem{Name: "vue-best-practices", Source: library.SourceID{Type: "github", Repo: "vuejs-ai/skills"}, State: library.UpdIgnored, Commit: "c74b92e", CommitDate: time.Now()},
	)
	a.upd.group = library.GroupIgnored
	tt = newUITester(a.view, 1280, 800)
	for _, want := range []string{"已忽略的更新", "已忽略", "已保留本地版本，跳过上游", "取消忽略", "返回技能更新"} {
		if !tt.HasText(want) {
			t.Errorf("missing %q in ignored view: %v", want, tt.Texts())
		}
	}
	must(t, tt.Click("返回技能更新"))
	if a.upd.group != "" {
		t.Fatal("back to active group did not switch")
	}

	// upd1-c: 进行中 + 批次状态 + 底部 Toast
	a.upd.task = taskUpdate
	a.upd.cancel = func() {}
	a.upd.progress = library.UpdateProgress{Done: 1, Total: 2, Name: "vitest", Active: true}
	a.upd.batch = map[string]updateBatchStatus{
		"vite":   batchDone,
		"vitest": batchActive,
	}
	a.notify(fromUpdate, toastOK, "已更新 vite。")
	tt = newUITester(a.view, 1280, 900)
	for _, want := range []string{"正在更新 1/2 个技能", "正在更新 1/2", "已更新到", "正在下载新版本…", "已更新 vite。", "GitHub 请求额度已用完"} {
		if !tt.HasText(want) {
			t.Errorf("missing %q in progress view: %v", want, tt.Texts())
		}
	}

	// upd2-a: 更新对比页
	a.upd.task = ""
	a.upd.cancel = nil
	a.dismissToast(fromUpdate)
	s := findSkillByID(a, "mine:vite")
	a.upd.prepared = &library.Prepared{
		Item: library.UpdateItem{
			Name:       "vite",
			Source:     library.SourceID{Type: "github", Repo: "antfu/skills"},
			State:      library.UpdBoth,
			Commit:     "3f8a21c",
			CommitDate: time.Now().Add(-24 * time.Hour),
		},
		Dir: t.TempDir(),
	}
	a.upd.diff = &compareState{
		updating: true,
		skill:    s,
		leftSub:  "~/.agents/skills/vite",
		rightSub: "antfu/skills · 3f8a21c",
		files: []library.FileDiff{
			{
				Path:  "SKILL.md",
				Added: 1, Deleted: 1,
				Rows: []library.DiffRow{
					{Kind: library.Same, Left: 1, Right: 1, LeftText: "# Vite", RightText: "# Vite"},
					{Kind: library.Changed, Left: 2, Right: 2, LeftText: "local note", RightText: "upstream note"},
				},
			},
			{Path: "notes.md", OnlyLeft: true, Deleted: 3},
		},
	}
	tt = newUITester(a.view, 1280, 820)
	for _, want := range []string{"差异文件（2）", "本机", "上游版本", "本地改过", "3f8a21c", "保留本地", "更新", "notes.md", "仅本机", "更新会用此上游版本替换本机目录，并移走仅本机存在的 notes.md", "在 GitHub 查看文件"} {
		if !tt.HasText(want) {
			t.Errorf("missing %q in update compare view: %v", want, tt.Texts())
		}
	}
}

func TestUpdateStateSnapshots(t *testing.T) {
	dir := os.Getenv("SNAPSHOT_DIR")
	if dir == "" {
		t.Skip("SNAPSHOT_DIR")
	}
	save := func(name string, w, h int, a *app) {
		tt := newUITester(a.view, w, h)
		f, err := os.Create(filepath.Join(dir, name))
		must(t, err)
		must(t, png.Encode(f, tt.Image()))
		must(t, f.Close())
	}

	// 1. upd1-b1 full multi-row
	a := newApp(library.Sample())
	updateFullFixture(a)
	a.upd.report.Complete = true
	a.upd.report.Reset = time.Time{}
	save("updates-full.png", 1280, 1080, a)

	// 2. upd1-c progress + toast + quota + inspector
	a2 := newApp(library.Sample())
	updateFullFixture(a2)
	a2.upd.task = taskUpdate
	a2.upd.cancel = func() {}
	a2.upd.progress = library.UpdateProgress{Done: 1, Total: 2, Name: "vitest", Active: true}
	a2.upd.batch = map[string]updateBatchStatus{
		"vite":   batchDone,
		"vitest": batchActive,
	}
	a2.notify(fromUpdate, toastOK, "已更新 vite。")
	a2.selected = findSkillByID(a2, "mine:vite")
	save("updates-progress.png", 1280, 960, a2)

	// 3. upd1-d ignored
	a3 := newApp(library.Sample())
	updateFullFixture(a3)
	a3.upd.report.Complete = true
	a3.upd.report.Reset = time.Time{}
	a3.upd.report.Items = append(a3.upd.report.Items,
		library.UpdateItem{Name: "vue-best-practices", Source: library.SourceID{Type: "github", Repo: "vuejs-ai/skills"}, State: library.UpdIgnored, Commit: "c74b92e", CommitDate: time.Now().Add(-48 * time.Hour)},
	)
	a3.upd.group = library.GroupIgnored
	save("updates-ignored.png", 1280, 800, a3)

	// 4. upd2-a update compare
	a4 := newApp(library.Sample())
	updateFullFixture(a4)
	s := findSkillByID(a4, "mine:vite")
	a4.upd.prepared = &library.Prepared{
		Item: library.UpdateItem{
			Name:       "vite",
			Source:     library.SourceID{Type: "github", Repo: "antfu/skills"},
			State:      library.UpdBoth,
			Commit:     "3f8a21c",
			CommitDate: time.Now().Add(-24 * time.Hour),
		},
		Dir: t.TempDir(),
	}
	a4.upd.diff = &compareState{
		updating: true,
		skill:    s,
		leftSub:  "~/.agents/skills/vite",
		rightSub: "antfu/skills · 3f8a21c",
		files: []library.FileDiff{
			{
				Path:  "SKILL.md",
				Added: 2, Deleted: 1,
				Rows: []library.DiffRow{
					{Kind: library.Same, Left: 1, Right: 1, LeftText: "---", RightText: "---"},
					{Kind: library.Same, Left: 2, Right: 2, LeftText: "name: vite", RightText: "name: vite"},
					{Kind: library.Changed, Left: 3, Right: 3, LeftText: "description: local edit", RightText: "description: upstream v2"},
					{Kind: library.Inserted, Right: 4, RightText: "compatibility: rolldown-vite"},
				},
			},
			{Path: "references/local-note.md", OnlyLeft: true, Deleted: 12},
		},
	}
	save("updates-compare.png", 1280, 820, a4)

	// 5. real-world unchecked state with local handle items + 27 well-known skills
	a5 := newApp(library.Sample())
	a5.scope = scopeUpdates
	a5.upd.report = library.UpdateReport{
		Items: []library.UpdateItem{
			{Name: "skill-creator", Source: library.SourceID{Type: "github", Repo: "anthropics/skills"}, State: library.UpdCopies, Reason: "有独立副本，需先收成一份"},
			{Name: "kami", Source: library.SourceID{Type: "github", Repo: "tw93/kami"}, State: library.UpdMovedOut, Reason: "技能库中找不到，其他位置有同名技能；装回技能库后才能更新"},
			{Name: "code-ablation", Source: library.SourceID{Type: "github", Repo: "DBAAZzz/skills"}, State: library.UpdCopies, Reason: "有独立副本，需先收成一份"},
			{Name: "better-readme", Source: library.SourceID{Type: "github", Repo: "tommy0103/better-readme-skill"}, State: library.UpdMovedOut, Reason: "技能库中找不到，其他位置有同名技能；装回技能库后才能更新"},
			{Name: "agently-mail", Names: []string{"agently-mail"}, Source: library.SourceID{Type: "well-known", Repo: "agent.qq.com"}, State: library.UpdWebsite, Reason: "安装记录不完整，建议重新安装", Command: "npx skills add https://agent.qq.com -g -y"},
			{Name: "open.feishu.cn", Names: make([]string, 27), Source: library.SourceID{Type: "well-known", Repo: "open.feishu.cn"}, State: library.UpdWebsite, Command: "npx skills update -g -y"},
		},
	}
	save("updates-unchecked-local.png", 1120, 820, a5)
}

func TestPrepareFailureUsesToastInsteadOfRowInlineError(t *testing.T) {
	home := t.TempDir()
	a := newApp(library.Scan(home))
	a.scope = scopeUpdates
	it := library.UpdateItem{Name: "vite", Source: library.SourceID{Type: "github", Repo: "antfu/skills"}, State: library.UpdReady, Commit: "3f8a21c", CommitDate: time.Now()}
	a.upd.report = library.UpdateReport{Checked: time.Now(), Complete: true, Eligible: 1, Items: []library.UpdateItem{it}}

	tt := newUITester(a.view, 1280, 800)
	must(t, tt.Click("查看差异"))

	if a.upd.err != "" {
		t.Fatalf("unexpected banner err: %q", a.upd.err)
	}
	if len(a.upd.errors) != 0 {
		t.Fatalf("unexpected row inline errors: %v", a.upd.errors)
	}
	if shownText(a, fromUpdate) == "" || shownKind(a, fromUpdate) != toastError {
		t.Fatalf("expected error toast, got kind=%v text=%q", shownKind(a, fromUpdate), shownText(a, fromUpdate))
	}
	tt.Frame()
	if !tt.HasText(shownText(a, fromUpdate)) {
		t.Fatalf("toast not rendered: %v", tt.Texts())
	}
	must(t, tt.Click("关闭提示"))
	tt.Frame()
	if shownText(a, fromUpdate) != "" {
		t.Fatalf("close button did not dismiss toast: %q", shownText(a, fromUpdate))
	}
}

func TestUpdateSettingsShowsUseGHAuthToggle(t *testing.T) {
	a := newApp(library.Sample())
	a.bg = func(func(func(func()))) {} // 隔离本机 gh CLI 环境
	a.set.tab = tabUpdates
	a.set.open = true

	tt := newUITester(a.settingsView, settingsWidth, 560)
	if !tt.HasText("GitHub 授权") || !tt.HasText("使用本机 gh 登录状态") {
		t.Fatalf("missing gh auth settings: %v", tt.Texts())
	}
	must(t, tt.Click("开关 使用本机 gh 登录状态"))
	if !a.lib.Settings.UseGHAuth {
		t.Fatal("toggle did not enable UseGHAuth")
	}
	tt.Frame()
	if !tt.HasText("正在检查本机 gh 登录状态…") {
		t.Fatalf("missing probing hint: %v", tt.Texts())
	}

	a.set.ghProbing, a.set.ghChecked, a.set.ghOK = false, true, true
	tt.Frame()
	if !tt.HasText("已读取本机 gh 登录凭证") {
		t.Fatalf("missing gh auth status hint: %v", tt.Texts())
	}

	a.set.ghOK, a.set.ghNote = false, "本机 gh 尚未登录"
	tt.Frame()
	if !tt.HasText("当前将回退匿名请求") {
		t.Fatalf("missing fallback warning hint: %v", tt.Texts())
	}
}

func TestSkillUpdatesSettingsTabAndSectionNames(t *testing.T) {
	a := newApp(library.Sample())
	a.bg = func(func(func(func()))) {}
	a.set.tab = tabUpdates
	a.set.open = true

	st := newUITester(a.settingsView, settingsWidth, 560)
	tabZH, ok := st.AccessByRole(ui.RoleButton, "技能更新")
	if !ok {
		t.Fatalf("missing settings tab 技能更新: %v", st.Texts())
	}
	if _, bare := st.AccessByRole(ui.RoleButton, "更新"); bare {
		t.Fatalf("settings tab should not use bare 更新: %v", st.Texts())
	}
	if !st.HasText("网络与旧版本") {
		t.Fatalf("missing section header 网络与旧版本: %v", st.Texts())
	}
	if tabZH.Bounds.W < 80 {
		t.Fatalf("settings tab width = %.1f, want >= 80 for Skill Updates", tabZH.Bounds.W)
	}

	useLocale(t, st, "en")
	st.Frame()
	tabEN, ok := st.AccessByRole(ui.RoleButton, "Skill Updates")
	if !ok {
		t.Fatalf("missing settings tab Skill Updates: %v", st.Texts())
	}
	if _, bare := st.AccessByRole(ui.RoleButton, "Updates"); bare {
		t.Fatalf("settings tab should not use bare Updates: %v", st.Texts())
	}
	if !st.HasText("Network & Old Versions") {
		t.Fatalf("missing section header Network & Old Versions: %v", st.Texts())
	}
	if tabEN.Bounds.W < 80 {
		t.Fatalf("english settings tab width = %.1f, want >= 80", tabEN.Bounds.W)
	}
}

func TestClickingUpdateSourceOpensExpectedWebURL(t *testing.T) {
	a := newApp(library.Sample())
	updateFixture(a)
	var opened []string
	a.openURL = func(u string) { opened = append(opened, u) }

	tt := newUITester(a.view, 1280, 900)
	must(t, tt.Click("antfu/skills"))
	must(t, tt.Click("open.feishu.cn"))
	if len(opened) != 2 || opened[0] != "https://github.com/antfu/skills" || opened[1] != "https://open.feishu.cn" {
		t.Fatalf("opened URLs = %v", opened)
	}
}

func TestRemoteGitDiffOpensCommitAndFileLinksAndSwitchesFiles(t *testing.T) {
	a := newApp(library.Sample())
	updateFixture(a)
	var opened []string
	a.openURL = func(u string) { opened = append(opened, u) }

	a.upd.prepared = &library.Prepared{
		Item: library.UpdateItem{
			Name:       "vite",
			Source:     library.SourceID{Type: "github", Repo: "antfu/skills"},
			State:      library.UpdBoth,
			Commit:     "3f8a21c",
			CommitDate: time.Now().Add(-24 * time.Hour),
			Record:     library.LockEntry{SkillPath: "skills/vite/SKILL.md"},
		},
		Dir: t.TempDir(),
	}
	a.upd.diff = &compareState{
		updating: true,
		skill:    findSkillByID(a, "mine:vite"),
		files: []library.FileDiff{
			{
				Path:    "SKILL.md",
				Added:   1,
				Deleted: 1,
				Rows: []library.DiffRow{
					{Kind: library.Same, Left: 1, Right: 1, LeftText: "# Vite", RightText: "# Vite"},
					{Kind: library.Changed, Left: 2, Right: 2, LeftText: "local note", RightText: "upstream note"},
				},
			},
			{Path: "notes.md", OnlyLeft: true, Deleted: 3},
		},
	}

	tt := newUITester(a.view, 1280, 820)
	must(t, tt.Click("3f8a21c"))
	must(t, tt.Click("在 GitHub 查看文件"))
	if len(opened) != 2 ||
		opened[0] != "https://github.com/antfu/skills/commit/3f8a21c" ||
		opened[1] != "https://github.com/antfu/skills/blob/3f8a21c/skills/vite/SKILL.md" {
		t.Fatalf("opened URLs = %v", opened)
	}

	must(t, tt.Click("notes.md"))
	if !tt.HasText("仅本机（更新后移走）") || tt.HasText("在 GitHub 查看文件") {
		t.Fatalf("local-only file subhead state unexpected: %v", tt.Texts())
	}

	must(t, tt.Click("全文对比"))
	if !a.upd.diff.fullFile {
		t.Fatal("expected fullFile toggle to activate")
	}
}
