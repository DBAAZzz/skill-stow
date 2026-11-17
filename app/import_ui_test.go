package main

import (
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"skillmanager/internal/library"
)

// importApp 是添加技能用例的应用：临时主目录，装了 Claude Code 和 Codex。
func importApp(t *testing.T) (*app, *ui.Tester, func(string) string) {
	t.Helper()
	home, _ := filepath.EvalSymlinks(t.TempDir())
	t.Setenv("HOME", home)
	t.Setenv("XDG_STATE_HOME", "")
	h := func(p string) string { return filepath.Join(home, p) }
	for _, d := range []string{".claude", ".codex"} {
		must(t, os.MkdirAll(h(d), 0o755))
		must(t, os.WriteFile(filepath.Join(h(d), "config.json"), []byte("{}"), 0o644))
	}
	must(t, os.MkdirAll(h(".agents/skills"), 0o755))
	a := newApp(library.Scan(home))
	return a, newUITester(a.view, 1280, 800), h
}

func writeTestSkill(t *testing.T, dir, desc, body string) {
	t.Helper()
	must(t, os.MkdirAll(dir, 0o755))
	must(t, os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte("---\nname: "+filepath.Base(dir)+"\ndescription: "+desc+"\n---\n"+body), 0o644))
}

func TestImportPageIsReachableFromTheSidebarAndMySkills(t *testing.T) {
	a, tt := newTester(t)
	must(t, tt.Click("添加技能"))
	if a.scope != scopeImport {
		t.Fatalf("工具栏「添加技能」应打开添加页：%s", a.scope)
	}
	for _, s := range []string{"选取文件夹或 .zip 压缩包", "选取…", "在 skills.sh 浏览", "读取"} {
		if !tt.HasText(s) {
			t.Errorf("缺 %q：%v", s, tt.Texts())
		}
	}
	a.scope = string(library.ScopeMine)
	tt.Frame()
	if !tt.HasText("添加技能") {
		t.Error("侧边栏和「我的技能」工具栏都应有「添加技能」")
	}
}

func TestUnsupportedSourcesExplainWhatToDoInstead(t *testing.T) {
	a, tt := newTester(t)
	a.openImport()
	a.startImport("https://gitlab.com/a/b")
	tt.Frame()
	if !tt.HasText("读不了这种来源。") {
		t.Errorf("GitLab：%v", tt.Texts())
	}
	for _, text := range tt.Texts() {
		if strings.Contains(text, "期") {
			t.Errorf("界面上不该出现分期的说法：%q", text)
		}
	}
	a.startImport("hello")
	tt.Frame()
	if a.imp.err == nil || a.imp.batch != nil {
		t.Error("认不出的输入应提示，不进入选择页")
	}
}

func TestClipboardLinkIsOfferedForPasting(t *testing.T) {
	a, tt := newTester(t)
	a.openImport()
	a.offerClipboard("npx skills add https://github.com/vercel-labs/skills --skill find-skills -g -y")
	tt.Frame()
	if !tt.HasText("剪贴板里有技能") || !tt.HasText("find-skills") {
		t.Fatalf("剪贴板提示：%v", tt.Texts())
	}
	a.offerClipboard("随便一段话")
	if a.imp.clip != "" {
		t.Error("不能识别的文字不提示")
	}
}

func TestDroppingAFolderAnywhereOpensThePickerAndAddsOnlyWhatIsChecked(t *testing.T) {
	a, tt, h := importApp(t)
	src := h("Downloads/pack")
	writeTestSkill(t, filepath.Join(src, "release-notes"), "写发布说明", "# notes")
	writeTestSkill(t, filepath.Join(src, "deploy"), "部署脚本", "# deploy")
	must(t, os.WriteFile(filepath.Join(src, "deploy", "run.sh"), []byte("#!/bin/sh"), 0o755))

	// 在「我的技能」页拖进来：切到添加技能页，进入选择，不直接安装
	if !tt.DropFiles(700, 400, src) {
		t.Fatal("窗口应接住拖进来的文件夹")
	}
	tt.Frame()
	if a.scope != scopeImport || a.imp.batch == nil {
		t.Fatalf("应进入选择页：scope=%s err=%v", a.scope, a.imp.err)
	}
	if exists := func(p string) bool { _, err := os.Lstat(p); return err == nil }; exists(h(".agents/skills/deploy")) {
		t.Fatal("拖入不能直接安装")
	}
	for _, s := range []string{"2 个技能", "全选可添加的 2 个", "deploy", "release-notes"} {
		if !tt.HasText(s) {
			t.Errorf("缺 %q：%v", s, tt.Texts())
		}
	}
	must(t, tt.Click("勾选 deploy"))
	if !tt.HasText("添加 1 个技能") {
		t.Fatalf("主按钮：%v", tt.Texts())
	}
	must(t, tt.Click("deploy"))
	for _, want := range []string{"可执行文件", "包含 1 个可执行文件", "run.sh", "查看"} {
		if !tt.HasText(want) {
			t.Errorf("可执行文件提示缺 %q：%v", want, tt.Texts())
		}
	}
	must(t, tt.Click("添加 1 个技能"))
	if _, err := os.Stat(h(".agents/skills/deploy/run.sh")); err != nil {
		t.Fatalf("deploy 应放进技能库：%v", err)
	}
	if _, err := os.Stat(h(".agents/skills/release-notes")); err == nil {
		t.Error("没勾的不能添加")
	}
	for _, s := range []string{"已添加", "继续添加", "在「我的技能」中查看", "已放进 ~/.agents/skills/deploy。"} {
		if !tt.HasText(s) {
			t.Errorf("缺 %q：%v", s, tt.Texts())
		}
	}
	if a.lib.Get("deploy") == nil {
		t.Error("添加后应重新扫描，「我的技能」里能看到")
	}
	must(t, tt.Click("在「我的技能」中查看"))
	if a.scope != string(library.ScopeMine) || a.selected == nil || a.selected.Name != "deploy" {
		t.Errorf("应去「我的技能」选中 deploy：%s %v", a.scope, a.selected)
	}
}

func TestAnExistingSkillIsShownAsAConflictWithACompare(t *testing.T) {
	a, tt, h := importApp(t)
	writeTestSkill(t, h(".agents/skills/vue"), "我的 vue", "mine")
	writeTestSkill(t, h("Downloads/vue"), "别人的 vue", "theirs")
	a.rescan()
	a.importPaths([]string{h("Downloads/vue")})
	tt.Frame()
	for _, s := range []string{"已有同名", "技能库里已经有 vue，内容和这份不一致。", "对比差异", "你的", "要添加的"} {
		if !tt.HasText(s) {
			t.Errorf("缺 %q：%v", s, tt.Texts())
		}
	}
	if tt.HasText("不会覆盖你的那份") {
		t.Errorf("同名冲突横幅不应保留冗余的不会覆盖提示：%v", tt.Texts())
	}
	if len(a.importAddable()) != 0 {
		t.Error("已有同名的不会自动勾上，要用户确认替换")
	}
	must(t, tt.Click("对比差异"))
	if a.imp.cmp == nil || len(a.imp.cmp.files) == 0 || !tt.HasText("你的和要添加的有 1 个文件不同") {
		t.Fatalf("对比页：%v", tt.Texts())
	}
	tt.Key(0, ui.KeyEscape)
	if a.imp.cmp != nil {
		t.Error("Esc 回到选择页")
	}
}

func TestAgentModePlacesTheSkillInTheChosenAgentOnly(t *testing.T) {
	a, tt, h := importApp(t)
	writeTestSkill(t, h("Downloads/slidev"), "演示文稿", "# slidev")
	a.importPaths([]string{h("Downloads/slidev")})
	tt.Frame()
	if !a.imp.picked["slidev"] {
		t.Fatal("只有一个技能时默认勾上")
	}
	must(t, tt.Click("技能库（通用）"))
	must(t, tt.Click("指定的 Agent…"))
	if a.imp.place.Library || len(a.imp.place.Agents) != 1 || a.imp.place.Agents[0] != "claude-code" {
		t.Fatalf("装到 = %+v", a.imp.place)
	}
	tt.Frame()
	must(t, tt.Click("添加 1 个技能"))
	if fi, err := os.Lstat(h(".claude/skills/slidev")); err != nil || !fi.IsDir() {
		t.Fatalf("应放进 Claude Code 自己的目录：%v", err)
	}
	if _, err := os.Lstat(h(".agents/skills/slidev")); err == nil {
		t.Error("不能放进技能库")
	}
}

func TestImportSnapshots(t *testing.T) {
	dir := os.Getenv("SNAPSHOT_DIR")
	if dir == "" {
		t.Skip("SNAPSHOT_DIR")
	}
	shot := func(tt *ui.Tester, name string) {
		tt.Advance(2 * time.Second)
		f, err := os.Create(filepath.Join(dir, name))
		must(t, err)
		must(t, png.Encode(f, tt.Image()))
		must(t, f.Close())
	}
	a, tt, h := importApp(t)
	writeTestSkill(t, h(".agents/skills/vue"), "我的 vue", "mine")
	for _, d := range []string{".cursor", ".kiro", ".config/opencode", ".codebuddy"} {
		must(t, os.MkdirAll(h(d), 0o755))
		must(t, os.WriteFile(filepath.Join(h(d), "config.json"), []byte("{}"), 0o644))
	}
	a.rescan()
	a.openImport()
	a.imp.input = "antfu/skills"
	a.offerClipboard("npx skills add https://github.com/vercel-labs/skills --skill find-skills")
	tt.Frame()
	shot(tt, "import-home.png")

	a.imp.clip = ""
	a.imp.err = &library.ImportError{Msg: "连不上 GitHub。", Hint: "检查网络；用代理的话，看看设置里的代理地址", Network: true}
	tt.Frame()
	shot(tt, "import-error.png")

	a.imp.err = nil
	a.imp.cancel = func() {}
	a.imp.reading = "antfu/skills"
	a.imp.prog = library.ImportProgress{Commit: "3f8a21c0000000000000000000000000000000000", Bytes: 1258291, Found: []string{"vite", "vitest", "unocss", "nuxt"}, Done: 4, Total: 9}
	tt.Frame()
	shot(tt, "import-reading.png")
	a.imp.cancel = nil

	src := h("Downloads/pack")
	writeTestSkill(t, filepath.Join(src, "slidev"), "用 Markdown 写演示文稿：布局、主题、代码高亮、演讲者模式与导出 PDF。", "# Slidev\n\nSlidev is a slides maker for developers.\n\n## When to use\n\n- The user asks for a talk.\n\n```sh\npnpm create slidev\n```\n")
	must(t, os.MkdirAll(filepath.Join(src, "slidev/scripts"), 0o755))
	must(t, os.WriteFile(filepath.Join(src, "slidev/scripts/export.sh"), []byte("#!/bin/sh\n"), 0o755))
	writeTestSkill(t, filepath.Join(src, "vue"), "Vue 3 组合式 API", "theirs")
	writeTestSkill(t, filepath.Join(src, "unocss"), "UnoCSS 原子化 CSS 的写法、预设与配置", "x")
	must(t, os.MkdirAll(filepath.Join(src, "notes"), 0o755))
	must(t, os.WriteFile(filepath.Join(src, "notes/SKILL.md"), []byte("---\nname: web-design-guidelines\n---\n没有简介"), 0o644))
	a.importPaths([]string{src})
	a.setImportPicked("slidev", true)
	a.setImportPicked("unocss", true)
	a.imp.focus = "slidev"
	tt.Frame()
	shot(tt, "import-pick.png")

	a.imp.focus = "vue"
	tt.Frame()
	shot(tt, "import-conflict.png")
	a.setImportReplace("vue", true)
	tt.Frame()
	shot(tt, "import-conflict-replace.png")
	a.setImportReplace("vue", false)

	must(t, tt.Click("谁会读到"))
	t.Logf("谁会读到（技能库）：%v", tt.Texts())
	shot(tt, "import-readers.png")
	tt.Key(0, ui.KeyEscape)
	tt.Frame()
	must(t, tt.Click("技能库（通用）"))
	shot(tt, "import-place.png")
	tt.Key(0, ui.KeyEscape)

	a.imp.focus = "slidev"
	a.addImports()
	tt.Frame()
	shot(tt, "import-done.png")

	a.discardImport()
	a.setPlacement(library.ImportPlacement{Agents: []string{"claude-code", "kiro"}})
	writeTestSkill(t, h("Downloads/deck"), "演示", "x")
	a.importPaths([]string{h("Downloads/deck")})
	tt.Frame()
	must(t, tt.Click("谁会读到"))
	t.Logf("谁会读到（指定 Agent）：%v", tt.Texts())
	shot(tt, "import-agents.png")
}

// queueBackground 让后台任务排队，由测试决定何时完成。
func queueBackground(a *app) *[]func(post func(func())) {
	var jobs []func(post func(func()))
	a.bg = func(w func(post func(func()))) { jobs = append(jobs, w) }
	return &jobs
}

func runNow(jobs *[]func(post func(func()))) {
	for _, w := range *jobs {
		w(func(fn func()) { fn() })
	}
	*jobs = nil
}

func TestAnAddFinishingAfterTheSourceChangedLeavesTheNewSourceAlone(t *testing.T) {
	// Given 第一次读入 x 并按下添加，还没完成就换了来源，又读入另一个 x
	a, _, h := importApp(t)
	src := h("Downloads/x")
	writeTestSkill(t, src, "x", "old")
	old, err := library.ReadLocalImport(a.lib.Home, []string{src})
	must(t, err)
	a.openImport()
	a.showBatch(old)
	jobs := queueBackground(a)
	a.addImports()
	a.discardImport()
	if !exists(old.Dir) {
		t.Fatal("添加还在用的暂存不能先删")
	}
	writeTestSkill(t, src, "x", "new")
	next, err := library.ReadLocalImport(a.lib.Home, []string{src})
	must(t, err)
	defer next.Discard()
	a.showBatch(next)

	// When 第一次的添加完成
	runNow(jobs)

	// Then 新来源的 x 仍可添加；旧暂存在添加结束后删掉
	if got := a.importAddable(); len(got) != 1 || len(a.imp.results) != 0 {
		t.Errorf("可添加 = %v，结果 = %v", got, a.imp.results)
	}
	if !exists(h(".agents/skills/x")) {
		t.Error("第一次的添加应完成")
	}
	if exists(old.Dir) {
		t.Error("旧暂存应在添加结束后删掉")
	}
}

func TestAnOldDownloadFailingDoesNotUncheckTheNewSource(t *testing.T) {
	a, _, h := importApp(t)
	src := h("Downloads/x")
	writeTestSkill(t, src, "x", "old")
	old, err := library.ReadLocalImport(a.lib.Home, []string{src})
	must(t, err)
	a.openImport()
	a.showBatch(old)
	a.imp.picked = map[string]bool{}
	sk := old.Skill("x")
	sk.Partial = true
	sk.Dir = filepath.Join(h("not-a-dir"), "x") // 补齐时必然失败
	must(t, os.WriteFile(h("not-a-dir"), []byte("file"), 0o644))
	jobs := queueBackground(a)
	a.setImportPicked("x", true)
	a.discardImport()
	next, err := library.ReadLocalImport(a.lib.Home, []string{src})
	must(t, err)
	defer next.Discard()
	a.showBatch(next)
	runNow(jobs)
	if !a.imp.picked["x"] || a.imp.fetchErr["x"] != "" {
		t.Errorf("新来源的 x 被改了：勾选 %v，错误 %q", a.imp.picked["x"], a.imp.fetchErr["x"])
	}
}

func TestOnlyTheSecondOfTwoSameNamedSkillsIsDisabled(t *testing.T) {
	a, _, h := importApp(t)
	src := h("Downloads/dup")
	for _, d := range []string{"one", "two"} {
		must(t, os.MkdirAll(filepath.Join(src, d), 0o755))
		must(t, os.WriteFile(filepath.Join(src, d, "SKILL.md"), []byte("---\nname: dup\ndescription: "+d+"\n---\n"), 0o644))
	}
	b, err := library.ReadLocalImport(a.lib.Home, []string{src})
	must(t, err)
	defer b.Discard()
	a.openImport()
	a.showBatch(b)
	first, second := b.Skills[0], b.Skills[1]
	if a.imp.checks[a.importKey(first)].State != library.ImportNew {
		t.Errorf("第一个应能添加：%+v", a.imp.checks[a.importKey(first)])
	}
	if a.imp.checks[a.importKey(second)].State != library.ImportInvalid {
		t.Errorf("第二个应不能添加：%+v", a.imp.checks[a.importKey(second)])
	}
	if a.importByKey(a.importKey(second)) != second {
		t.Error("第二个应能单独预览")
	}
}

func exists(p string) bool {
	_, err := os.Lstat(p)
	return err == nil
}

func TestOnlyTheNamedSkillIsListedWithAWayToShowTheRest(t *testing.T) {
	a, tt, h := importApp(t)
	src := h("Downloads/x")
	writeTestSkill(t, src, "x", "body")
	b, err := library.ReadLocalImport(a.lib.Home, []string{src})
	must(t, err)
	defer b.Discard()
	b.Hidden = 85 // 带 --skill 读 GitHub 时，仓库里没读的技能
	a.openImport()
	a.showBatch(b)
	tt.Frame()
	if !tt.HasText("显示全部 86 个") {
		t.Errorf("应能显示全部：%v", tt.Texts())
	}
	a.discardImport()
	tt.Frame()
	if a.imp.batch != nil || !tt.HasText("从链接添加") {
		t.Errorf("放弃后应回到输入页：%v", tt.Texts())
	}
}

func TestImportPreviewSnapshots(t *testing.T) {
	dir := os.Getenv("SNAPSHOT_DIR")
	if dir == "" {
		t.Skip("SNAPSHOT_DIR")
	}
	shot := func(tt *ui.Tester, name string) {
		tt.Advance(2 * time.Second)
		f, err := os.Create(filepath.Join(dir, name))
		must(t, err)
		must(t, png.Encode(f, tt.Image()))
		must(t, f.Close())
	}
	a, tt, h := importApp(t)
	src := h("Downloads/slidev")
	must(t, os.MkdirAll(filepath.Join(src, "references"), 0o755))
	must(t, os.MkdirAll(filepath.Join(src, "scripts"), 0o755))
	must(t, os.MkdirAll(filepath.Join(src, "assets"), 0o755))
	must(t, os.WriteFile(filepath.Join(src, "SKILL.md"), []byte("---\nname: slidev\ndescription: 用 Markdown 写演示文稿：布局、主题、代码高亮、演讲者模式与导出 PDF。\nallowed-tools: Bash(pnpm slidev *)\n---\n# Slidev\n\nSlidev is a slides maker for developers.\n\n## Quick start\n\n```sh\npnpm create slidev\n```\n"), 0o644))
	must(t, os.WriteFile(filepath.Join(src, "references/layouts.md"), []byte("# Layouts\n\nUse `layout: two-cols` per slide.\n"), 0o644))
	must(t, os.WriteFile(filepath.Join(src, "scripts/export.sh"), []byte("#!/usr/bin/env bash\n# 把 slides.md 导出成 PDF\nset -euo pipefail\n\nDECK=\"${1:-slides.md}\"\nmkdir -p dist\npnpm slidev export \"$DECK\" --format pdf\necho \"done\"\n"), 0o755))
	img := image.NewRGBA(image.Rect(0, 0, 320, 180))
	for y := 0; y < 180; y++ {
		for x := 0; x < 320; x++ {
			img.Set(x, y, color.RGBA{uint8(30 + x/8), uint8(60 + y/3), 120, 255})
		}
	}
	f, err := os.Create(filepath.Join(src, "assets/cover.png"))
	must(t, err)
	must(t, png.Encode(f, img))
	must(t, f.Close())

	a.openImport()
	a.importPaths([]string{src})
	tt.Frame()
	b := a.imp.batch
	if b == nil {
		t.Fatalf("没读到：%v", a.imp.err)
	}
	scanned := time.Date(2026, 9, 2, 14, 18, 0, 0, time.UTC)
	b.CommitDate = time.Date(2026, 10, 21, 9, 0, 0, 0, time.UTC)
	b.Skills[0].Audit = []library.AuditResult{
		{Provider: "ath", Risk: "safe", At: scanned},
		{Provider: "socket", Risk: "critical", At: scanned, Alerts: 1},
		{Provider: "snyk", Risk: "low", At: scanned},
	}
	tt.Frame()
	shot(tt, "preview-skillmd.png")
	t.Logf("SKILL.md 页：%v", tt.Texts())

	a.imp.tab = 1
	tt.Frame()
	shot(tt, "preview-files-skillmd.png")
	for i, name := range []string{"scripts/export.sh", "assets/cover.png", "references/layouts.md"} {
		for j, f := range a.importFilesOf(b.Skills[0]) {
			if f.Path == name {
				a.imp.file = j
			}
		}
		tt.Frame()
		shot(tt, fmt.Sprintf("preview-files-%d.png", i))
	}
	a.imp.raw = true
	tt.Frame()
	shot(tt, "preview-files-raw.png")

	// r7 定稿 3 态真实渲染快照（sk-a 单文件含风险、sk-c 原文模式、sk-b 多文件折叠+表格+本地图）
	a2, tt2, h2 := importApp(t)
	pack := h2("Downloads/r7-pack")
	avatarDir := filepath.Join(pack, "ai-avatar-video")
	must(t, os.MkdirAll(avatarDir, 0o755))
	must(t, os.WriteFile(filepath.Join(avatarDir, "SKILL.md"), []byte(strings.Join([]string{
		"---",
		"name: ai-avatar-video",
		"description: Create AI avatar and talking head videos via inference.sh CLI. Recommended: P-Video-Avatar (fastest, cheapest, built-in TTS).",
		"allowed-tools: Bash(belt *)",
		"---",
		"> Install the belt CLI skill: `npx skills add belt-sh/cli`",
		"",
		"[![AI Avatar](https://img.shields.io/badge/inference.sh-belt-blue)](https://inference.sh)",
		"",
		"# AI Avatar & Talking Head Videos",
		"",
		"Create AI avatars and talking head videos via [inference.sh](https://inference.sh) CLI.",
		"",
		"## Quick Start",
		"",
		"> Requires inference.sh CLI (`belt`). [Install instructions](https://inference.sh/docs/cli)",
		"",
		"```bash",
		"belt login",
		"",
		"# Recommended: P-Video-Avatar (fastest, cheapest, built-in TTS)",
		"belt app run pruna/p-video-avatar --input '{",
		`  "image": "https://portrait.jpg",`,
		`  "voice_script": "Hello, welcome to our product demo!",`,
		`  "voice": "Zephyr (Female)"`,
		"}'",
		"```",
		"",
		"## Available Models",
		"",
		"- **P-Video-Avatar** — 综合速度与成本最优，内置 TTS 语音合成",
		"- **OmniHuman** — 适合全身动作与自然手势口型同步",
		"- **Fabric / PixVerse** — 适合高分辨率特写与多语种对口型",
	}, "\n")), 0o644))

	slidevDir := filepath.Join(pack, "slidev")
	must(t, os.MkdirAll(filepath.Join(slidevDir, "scripts"), 0o755))
	must(t, os.MkdirAll(filepath.Join(slidevDir, "assets"), 0o755))
	must(t, os.WriteFile(filepath.Join(slidevDir, "scripts/export.sh"), []byte("#!/bin/sh\necho export\n"), 0o755))
	must(t, os.WriteFile(filepath.Join(slidevDir, "new-deck.mjs"), []byte("#!/usr/bin/env node\nconsole.log('deck')\n"), 0o755))
	must(t, copyFileTest(filepath.Join(src, "assets/cover.png"), filepath.Join(slidevDir, "assets/cover.png")))
	must(t, os.WriteFile(filepath.Join(slidevDir, "SKILL.md"), []byte(strings.Join([]string{
		"---",
		"name: slidev",
		"description: 用 Markdown 写演示文稿：布局、主题、代码高亮、演讲者模式与导出 PDF。",
		"allowed-tools: Bash(pnpm slidev *)",
		"---",
		"# Slidev",
		"",
		"Slidev is a slides maker for developers. Write slides in one Markdown file, separated by `---`.",
		"",
		"## When to use",
		"",
		"- The user asks for a technical talk, deck, or workshop slides from code or notes.",
		"- Existing `slides.md` needs layout tuning, theme setup, or PDF export.",
		"",
		"## Frontmatter & Layouts",
		"",
		"| 字段 | 常用值 | 说明 |",
		"| --- | --- | --- |",
		"| `layout` | `cover` / `two-cols` | 控制单页版式与分栏对齐 |",
		"| `theme` | `seriph` / `apple-basic` | 指定全局配色与字体主题 |",
		"",
		"### Quick commands",
		"",
		"```sh",
		"pnpm create slidev",
		"pnpm slidev slides.md --open",
		"```",
		"",
		"![Slidev Cover](assets/cover.png)",
	}, "\n")), 0o644))

	a2.openImport()
	a2.importPaths([]string{pack})
	tt2.Frame()
	b2 := a2.imp.batch
	b2.CommitDate = time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	b2.Skill("ai-avatar-video").Audit = []library.AuditResult{
		{Provider: "socket", Risk: "critical", Alerts: 1, At: scanned},
		{Provider: "snyk", Risk: "low", At: scanned},
		{Provider: "ath", Risk: "safe", At: scanned},
	}
	b2.Skill("slidev").Audit = []library.AuditResult{
		{Provider: "ath", Risk: "safe", At: scanned},
		{Provider: "socket", Risk: "safe", At: scanned},
		{Provider: "snyk", Risk: "low", At: scanned},
	}
	a2.imp.focus = "ai-avatar-video"
	tt2.Frame()
	shot(tt2, "r7-ska.png")

	must(t, tt2.Click("查看原文"))
	tt2.Frame()
	shot(tt2, "r7-skc.png")

	must(t, tt2.Click("slidev"))
	tt2.Frame()
	shot(tt2, "r7-skb.png")

	tt2.SetSize(1600, 900)
	must(t, tt2.Click("ai-avatar-video"))
	tt2.Frame()
	shot(tt2, "r7-wide-ska.png")

	must(t, tt2.Click("查看原文"))
	tt2.Frame()
	shot(tt2, "r7-wide-skc.png")
}

func copyFileTest(src, dst string) error {
	b, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	return os.WriteFile(dst, b, 0o644)
}

func TestImportTagsShowEachSkillsState(t *testing.T) {
	a, tt, h := importApp(t)
	writeTestSkill(t, h(".agents/skills/existing"), "已有技能", "mine")
	a.rescan()

	src := h("Downloads/tag-pack")
	writeTestSkill(t, filepath.Join(src, "risky"), "含高危扫描的技能", "# risky")
	writeTestSkill(t, filepath.Join(src, "existing"), "同名技能", "theirs")
	writeTestSkill(t, filepath.Join(src, "fresh"), "正常新技能", "# fresh")
	must(t, os.MkdirAll(filepath.Join(src, "nodesc"), 0o755))
	must(t, os.WriteFile(filepath.Join(src, "nodesc/SKILL.md"), []byte("---\nname: web-design-guidelines\n---\nno desc"), 0o644))

	a.openImport()
	a.importPaths([]string{src})
	b := a.imp.batch
	if b == nil {
		t.Fatalf("没读到：%v", a.imp.err)
	}
	b.Skill("risky").Audit = []library.AuditResult{{Provider: "socket", Risk: "critical", Alerts: 1}}
	a.imp.focus = "risky"
	tt.Frame()

	for _, want := range []string{"扫描有风险", "已有同名", "缺简介", "新"} {
		if !tt.HasText(want) {
			t.Errorf("列表/预览缺少标签 %q：%v", want, tt.Texts())
		}
	}

	// 添加后应显示「已添加」
	a.imp.picked = map[string]bool{"fresh": true}
	a.imp.focus = "fresh"
	a.addImports()
	tt.Frame()
	if !tt.HasText("已添加") {
		t.Errorf("添加后缺少「已添加」标签：%v", tt.Texts())
	}
}

func TestSkillMarkdownHidesUnresolvableBadgesKeepsLocalImages(t *testing.T) {
	dir := t.TempDir()
	must(t, os.MkdirAll(filepath.Join(dir, "assets"), 0o755))
	must(t, os.WriteFile(filepath.Join(dir, "assets/cover.png"), []byte("png"), 0o644))

	src := strings.Join([]string{
		"[![CI](https://img.shields.io/badge/ci-pass-green)](https://example.com)",
		"",
		"![Remote](https://example.com/missing.png)",
		"",
		"<!-- hidden html comment -->",
		"",
		"<p align=\"center\"><img src=\"https://example.com/banner.png\"></p>",
		"",
		"> Install via `npx skills add demo`",
		"",
		"Use [docs](https://example.com) and local preview:",
		"",
		"![Cover](assets/cover.png)",
		"",
		"```sh",
		"echo ok",
		"```",
		"",
	}, "\n")

	got := cleanSkillMarkdownBody(src, dir)
	for _, hidden := range []string{"img.shields.io", "missing.png", "hidden html comment", "banner.png"} {
		if strings.Contains(got, hidden) {
			t.Errorf("应隐藏不可解析的外链图片/注释 %q，实际：\n%s", hidden, got)
		}
	}
	for _, kept := range []string{"Install via", "[docs](https://example.com)", "![Cover](assets/cover.png)", "echo ok"} {
		if !strings.Contains(got, kept) {
			t.Errorf("应保留正文/本地图片/链接 %q，实际：\n%s", kept, got)
		}
	}
}

func TestImportDetailSupportsRawToggleAndCollapsedSafeAudit(t *testing.T) {
	a, tt, h := importApp(t)
	src := h("Downloads/r7-pack")

	// 1) 单文件含风险 + allowed-tools + 外链徽章
	riskyDir := filepath.Join(src, "ai-avatar-video")
	must(t, os.MkdirAll(riskyDir, 0o755))
	must(t, os.WriteFile(filepath.Join(riskyDir, "SKILL.md"), []byte(strings.Join([]string{
		"---",
		"name: ai-avatar-video",
		"description: Create AI avatar videos via CLI.",
		"allowed-tools: Bash(belt *)",
		"---",
		"[![Avatar Badge](https://img.shields.io/badge/belt-cli-blue)](https://inference.sh)",
		"",
		"> Install belt CLI",
		"",
		"# AI Avatar Videos",
		"",
		"Create avatars via [inference.sh](https://inference.sh).",
		"",
		"```bash",
		"belt login",
		"```",
	}, "\n")), 0o644))

	// 2) 多文件无风险 + allowed-tools + 可执行脚本
	safeDir := filepath.Join(src, "slidev")
	must(t, os.MkdirAll(filepath.Join(safeDir, "scripts"), 0o755))
	must(t, os.WriteFile(filepath.Join(safeDir, "scripts/export.sh"), []byte("#!/bin/sh\necho ok\n"), 0o755))
	must(t, os.WriteFile(filepath.Join(safeDir, "SKILL.md"), []byte("---\nname: slidev\ndescription: 演示文稿\nallowed-tools: Bash(pnpm slidev *)\n---\n# Slidev Docs\n\nBody text.\n"), 0o644))

	a.openImport()
	a.importPaths([]string{src})
	b := a.imp.batch
	if b == nil {
		t.Fatalf("没读到：%v", a.imp.err)
	}
	scanned := time.Date(2026, 9, 2, 12, 0, 0, 0, time.UTC)
	b.CommitDate = time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC) // 早于扫描，不显示过时提示
	b.Skill("ai-avatar-video").Audit = []library.AuditResult{
		{Provider: "socket", Risk: "critical", Alerts: 1, At: scanned},
		{Provider: "snyk", Risk: "low", At: scanned},
		{Provider: "ath", Risk: "safe", At: scanned},
	}
	b.Skill("slidev").Audit = []library.AuditResult{
		{Provider: "ath", Risk: "safe", At: scanned},
		{Provider: "socket", Risk: "safe", At: scanned},
		{Provider: "snyk", Risk: "low", At: scanned},
	}

	// 验证单文件渲染态（sk-a）
	a.imp.focus = "ai-avatar-video"
	tt.Frame()
	for _, want := range []string{"ai-avatar-video", "查看原文", "安全审计", "工具权限", "Bash(belt *)", "AI Avatar Videos"} {
		if !tt.HasText(want) {
			t.Errorf("sk-a 缺少 %q：%v", want, tt.Texts())
		}
	}
	if tt.HasText("Avatar Badge") || tt.HasText("本应用不扫描文件") {
		t.Errorf("不应渲染外链徽章占位框或冗余脚注：%v", tt.Texts())
	}
	// 「查看原文」切到原文模式
	must(t, tt.Click("查看原文"))
	if !a.imp.skillRaw || !tt.HasText("原文模式") {
		t.Fatalf("应切到原文模式：raw=%v texts=%v", a.imp.skillRaw, tt.Texts())
	}
	if tt.HasText("安全审计") || tt.HasText("工具权限") {
		t.Errorf("原文模式应直接显示完整只读编辑器，不渲染检查表：%v", tt.Texts())
	}
	// 再点「原文模式」切回渲染态
	must(t, tt.Click("原文模式"))
	if a.imp.skillRaw || !tt.HasText("查看原文") || !tt.HasText("安全审计") {
		t.Fatalf("应切回渲染态：%v", tt.Texts())
	}

	// 验证切换技能时自动重置原文模式 + 多文件无风险审计默认折叠（sk-b）
	must(t, tt.Click("查看原文"))
	must(t, tt.Click("slidev"))
	if a.imp.skillRaw {
		t.Error("切换技能后应默认回到渲染态")
	}
	for _, want := range []string{"查看原文", "安全审计", "没发现问题", "3 家", "文件与权限", "Bash(pnpm slidev *)", "包含 1 个可执行文件", "查看"} {
		if !tt.HasText(want) {
			t.Errorf("sk-b 缺少 %q：%v", want, tt.Texts())
		}
	}
	if tt.HasText("Socket") {
		t.Errorf("无风险审计默认应折叠成一行「没发现问题」：%v", tt.Texts())
	}
	// 点击「没发现问题」展开审计明细
	must(t, tt.Click("没发现问题"))
	if !tt.HasText("Socket") || !tt.HasText("Gen Agent Trust Hub") {
		t.Errorf("点击后应展开全部 3 家审计明细：%v", tt.Texts())
	}
	// 点击可执行文件右侧「查看」跳入文件页并选中 scripts/export.sh
	must(t, tt.Click("查看"))
	if a.imp.tab != 1 || a.importFilesOf(b.Skill("slidev"))[a.imp.file].Path != "scripts/export.sh" {
		t.Errorf("应跳到文件页并选中 scripts/export.sh：tab=%d file=%d", a.imp.tab, a.imp.file)
	}
}

func TestAWebsiteSourceIsReadAndAddedLikeNpx(t *testing.T) {
	a, tt, h := importApp(t)
	md := "---\nname: lesson\ndescription: 写课件\n---\n正文\n"
	files := map[string]string{
		"/docs/.well-known/skills/index.json":      `{"skills":[{"name":"lesson","description":"写课件","files":["SKILL.md"]}]}`,
		"/docs/.well-known/skills/lesson/SKILL.md": md,
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, ok := files[r.URL.Path]
		if !ok {
			http.NotFound(w, r)
			return
		}
		io.WriteString(w, body)
	}))
	defer srv.Close()

	a.openImport()
	a.startImport(srv.URL + "/docs")
	for deadline := time.Now().Add(5 * time.Second); a.imp.batch == nil && a.imp.err == nil && time.Now().Before(deadline); {
		tt.Advance(50 * time.Millisecond)
	}
	tt.Frame()
	if a.imp.batch == nil {
		t.Fatalf("没读到：%v", a.imp.err)
	}
	for _, want := range []string{"well-known 0.1.0", "网站可以随时改内容，没有历史记录", "打开网站", "添加 1 个技能"} {
		if !tt.HasText(want) {
			t.Errorf("缺少 %q：%v", want, tt.Texts())
		}
	}
	if dir := os.Getenv("SNAPSHOT_DIR"); dir != "" {
		tt.Advance(2 * time.Second)
		f, err := os.Create(filepath.Join(dir, "website-source.png"))
		must(t, err)
		must(t, png.Encode(f, tt.Image()))
		must(t, f.Close())
	}
	a.addImports()
	for deadline := time.Now().Add(5 * time.Second); a.imp.adding && time.Now().Before(deadline); {
		tt.Advance(50 * time.Millisecond)
	}
	tt.Frame()
	if !exists(h(".agents/skills/lesson/SKILL.md")) {
		t.Fatalf("应加进技能库：%v", tt.Texts())
	}
	raw, err := os.ReadFile(h(".agents/.skill-lock.json"))
	must(t, err)
	var lock struct {
		Skills map[string]map[string]string `json:"skills"`
	}
	must(t, json.Unmarshal(raw, &lock))
	if e := lock.Skills["lesson"]; e["sourceType"] != "well-known" || e["sourceBaseUrl"] != srv.URL+"/docs" {
		t.Errorf("安装记录 = %v", e)
	}
}

func TestImportDetailKeepsTheFileTotalWhenTheFilesTabShowsFewer(t *testing.T) {
	// Given 要添加的技能里有个隐藏文件：会一起装上，但「文件」页签不列它
	a, tt, h := importApp(t)
	src := h("Downloads/deploy")
	writeTestSkill(t, src, "部署脚本", "# deploy")
	must(t, os.WriteFile(filepath.Join(src, "README.md"), []byte("# readme"), 0o644))
	must(t, os.WriteFile(filepath.Join(src, ".config"), []byte("x"), 0o644))

	// When 打开它的详情
	a.importPaths([]string{src})
	tt.Frame()

	// Then 页签写可预览的 2 个，路径旁仍写出一共 3 个文件
	wantTexts(t, tt, "文件 2", "3 个文件")
}
