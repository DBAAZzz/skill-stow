package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"skillmanager/internal/library"
)

func TestNativeInspectorControlsOnlyDiscoverableSupportedAgents(t *testing.T) {
	a, tt := newTester(t)
	s := a.lib.Get("vue-best-practices")
	a.selected = s
	tt.Frame()
	tt.Advance(paneDuration)
	if !tt.HasText("所有 Agent") || !tt.HasText("始终启用") {
		t.Fatal(tt.Texts())
	}
	must(t, tt.Click("开关 Codex"))
	if s.EnabledIn("codex") || !s.DiscoveredIn("codex") {
		t.Fatal("toggle removed discovery")
	}
	must(t, tt.Click("开关 所有 Agent"))
	if !s.EnabledIn("codex") {
		t.Fatal("partial master must enable all")
	}
	must(t, tt.Click("开关 所有 Agent"))
	if s.EnabledIn("codex") || s.EnabledIn("claude-code") || !s.EnabledIn("cursor") {
		t.Fatal("master included unsupported Agent")
	}
}

func TestNativeMasterToggleLeavesHiddenAgentsEnabled(t *testing.T) {
	a, tt := newTester(t)
	s := a.lib.Get("vue-best-practices")
	must(t, a.lib.SetHidden("codex", true))
	a.selected = s
	tt.Frame()
	tt.Advance(paneDuration)
	must(t, tt.Click("开关 所有 Agent"))
	if s.EnabledIn("claude-code") || !s.EnabledIn("codex") {
		t.Fatal("master toggle changed hidden Agent", s.States)
	}
}
func TestNameImpactConfirmationCancelsAndCombines(t *testing.T) {
	a, tt := newTester(t)
	s := a.lib.Get("test-first")
	a.selected = s
	a.requestEnable(s, []string{"claude-code", "codex"}, false)
	tt.Frame()
	if !tt.HasText("一起停用") {
		t.Fatal("missing impact confirmation")
	}
	must(t, tt.Click("取消"))
	if !s.EnabledIn("codex") {
		t.Fatal("cancel changed state")
	}
	a.requestEnable(s, []string{"claude-code", "codex"}, false)
	tt.Frame()
	must(t, tt.Click("一起停用"))
	if s.EnabledIn("codex") {
		t.Fatal("confirmed batch not applied")
	}
}
func TestDisabledSkillRemainsInBoardAndCollectionHasNoSwitch(t *testing.T) {
	a, tt := newTester(t)
	s := a.lib.Get("git-flow")
	a.enable(s, "claude-code", false)
	a.mode = viewBoard
	tt.Frame()
	if !tt.HasText("已停用") || !tt.HasText("git-flow") {
		t.Fatal("disabled skill disappeared")
	}
	a.mode = viewCards
	a.coll = a.lib.Collections[1]
	tt.Frame()
	tt.Advance(paneDuration)
	if _, ok := tt.Find("为 Claude Code 启用整个集合"); ok {
		t.Fatal("collection switch still available")
	}
	_ = ui.KeyEscape
}

func TestBoardMenuOnlyTogglesItsOwnAgent(t *testing.T) {
	a, tt := newTester(t)
	a.mode = viewBoard
	tt.Frame()
	target := "Claude Code · interface-craft 操作"
	rect, ok := tt.Find(target)
	if !ok {
		t.Fatal("missing card menu")
	}
	tt.Move(rect.X+rect.W/2, rect.Y+rect.H/2)
	tt.Frame()
	must(t, tt.Click(target))
	clickMenuItem(t, tt, "在 Claude Code 中停用")
	s := a.lib.Get("interface-craft")
	if s.EnabledIn("claude-code") || !s.EnabledIn("codebuddy") {
		t.Fatal("menu changed another column")
	}
}
func TestAddMenuCreatesUnsupportedAgentAndOffersRemoval(t *testing.T) {
	a, tt := newTester(t)
	s := a.lib.Get("systematic-debugging")
	a.selected = s
	tt.Frame()
	tt.Advance(paneDuration)
	must(t, tt.Click("添加到其他 Agent…"))
	must(t, tt.Click("添加到 WorkBuddy"))
	tt.Frame()
	if !s.DiscoveredIn("workbuddy") {
		t.Fatal("not added")
	}
	if tt.HasText("始终启用") {
		t.Errorf("刚加上的 Agent 只有「移除」，不写状态词：%q", tt.Texts())
	}
	if tt.HasText("暂不支持在这里停用。") {
		t.Error("状态词已经写在那一行，提示区不应再说一遍")
	}
	must(t, tt.Click("移除"))
	if s.DiscoveredIn("workbuddy") {
		t.Fatal("not removed")
	}
}

func TestInspectorTipsOnlyExplainWhatTheStateWordCannot(t *testing.T) {
	a, tt := newTester(t)
	a.selected = a.lib.Get("test-first")
	tt.Frame()
	tt.Advance(paneDuration)
	// Gemini 需要重启：只补充状态词说不了的（立即生效的办法、项目里仍停用）；
	// OpenCode 读不了配置：写原因，可以重试
	for _, want := range []string{"可在会话中执行 /skills reload 立即生效。stock-plan 里仍停用。", "无法读取 OpenCode 配置，请检查格式后重试。", "重试"} {
		if !tt.HasText(want) {
			t.Errorf("提示区应有 %q：%q", want, tt.Texts())
		}
	}
	if tt.HasText("暂不支持在这里停用。") || tt.HasText("需要重启 Gemini CLI 才会生效。") {
		t.Errorf("和那一行状态词重复的话不应出现：%q", tt.Texts())
	}
}

func TestSharedBoardCardKeepsAgentControlsInInspector(t *testing.T) {
	a, tt := newTester(t)
	a.mode, a.query = viewBoard, "code-review"
	tt.Frame()
	if n := countText(tt, "code-review"); n != 2 {
		t.Fatalf("expected one shared card, got %d name labels", n)
	}
	must(t, tt.Click("code-review"))
	tt.Frame()
	tt.Advance(paneDuration)
	must(t, tt.Click("开关 Codex"))
	s := a.lib.Get("code-review")
	if s.EnabledIn("codex") || !s.DiscoveredIn("codex") || !s.EnabledIn("claude-code") {
		t.Fatal("shared card inspector changed discovery or another Agent")
	}
}

// zcodeApp 是真实磁盘上的一份：kami 只经 ~/.zcode/skills 里的软链被 ZCode 读到。
func zcodeApp(t *testing.T) (a *app, tt *ui.Tester, home string) {
	t.Helper()
	home, _ = filepath.EvalSymlinks(t.TempDir())
	t.Setenv("HOME", home)
	real := filepath.Join(home, ".claude/skills/kami")
	must(t, os.MkdirAll(real, 0o755))
	must(t, os.WriteFile(filepath.Join(real, "SKILL.md"), []byte("---\nname: kami\ndescription: x\n---\n"), 0o644))
	must(t, os.WriteFile(filepath.Join(home, ".claude/settings.json"), []byte("{}\n"), 0o600))
	must(t, os.MkdirAll(filepath.Join(home, ".zcode/cli"), 0o755))
	must(t, os.WriteFile(filepath.Join(home, ".zcode/cli/config.json"), []byte("{}\n"), 0o600))
	a = newApp(library.Scan(home))
	return a, newUITester(a.view, 1280, 800), home
}

func TestZCodeInspectorTogglesWithSwitchAndStillOffersRemove(t *testing.T) {
	a, tt, home := zcodeApp(t)
	s := a.lib.Get("kami")
	must(t, a.lib.AddToAgent(s, "zcode"))
	a.selected = s
	tt.Frame()
	tt.Advance(paneDuration)
	if _, ok := tt.Find("开关 ZCode"); !ok {
		t.Fatalf("ZCode 应有开关：%q", tt.Texts())
	}
	if !tt.HasText("移除") || tt.HasText("始终启用") {
		t.Fatalf("这里加上的链接仍可单独移除，不再写「始终启用」：%q", tt.Texts())
	}
	must(t, tt.Click("开关 ZCode"))
	if s.EnabledIn("zcode") || !s.DiscoveredIn("zcode") {
		t.Fatalf("开关只停用、不删链接：%+v", s.State("zcode"))
	}
	if _, err := os.Lstat(filepath.Join(home, ".zcode/skills/kami")); err != nil {
		t.Fatal("停用删了链接")
	}
	b, _ := os.ReadFile(filepath.Join(home, ".zcode/cli/config.json"))
	if !strings.Contains(string(b), `"enable": false`) {
		t.Fatalf("没写进 ZCode 配置：%s", b)
	}
	tt.Frame()
	must(t, tt.Click("开关 ZCode"))
	if !s.EnabledIn("zcode") {
		t.Fatalf("再拨一次应开启：%+v", s.State("zcode"))
	}
	tt.Frame()
	must(t, tt.Click("移除"))
	if s.DiscoveredIn("zcode") {
		t.Fatal("移除没删掉链接")
	}
}

func TestZCodeMasterSwitchOffLocksTheInspectorSwitch(t *testing.T) {
	a, _, home := zcodeApp(t)
	must(t, os.MkdirAll(filepath.Join(home, ".zcode/skills"), 0o755))
	must(t, os.Symlink(filepath.Join(home, ".claude/skills/kami"), filepath.Join(home, ".zcode/skills/kami")))
	must(t, os.WriteFile(filepath.Join(home, ".zcode/cli/config.json"), []byte(`{"skills":{"enabled":false}}`), 0o600))
	a.lib = library.Scan(home)
	tt := newUITester(a.view, 1280, 800)
	a.selected = a.lib.Get("kami")
	tt.Frame()
	tt.Advance(paneDuration)
	if !tt.HasText("ZCode 设置里关闭了全部技能，要先在 ZCode 里打开技能功能。") {
		t.Fatalf("要说明为什么拨不动：%q", tt.Texts())
	}
	_ = tt.Click("开关 ZCode")
	if a.lib.Get("kami").EnabledIn("zcode") {
		t.Fatal("总开关关着时不能报启用")
	}
}

func TestZCodeProjectSkillShowsItIsDisabledInZCode(t *testing.T) {
	a, _, home := zcodeApp(t)
	dir := filepath.Join(home, "code/app")
	skill := filepath.Join(dir, ".zcode/skills/deploy")
	must(t, os.MkdirAll(skill, 0o755))
	must(t, os.WriteFile(filepath.Join(skill, "SKILL.md"), []byte("---\nname: deploy\ndescription: x\n---\n"), 0o644))
	must(t, a.lib.AddProjects([]string{dir}))
	must(t, os.WriteFile(filepath.Join(home, ".zcode/cli/config.json"),
		[]byte(`{"skills":{"`+filepath.Join(skill, "SKILL.md")+`":{"enable":false}}}`), 0o600))
	a.lib = library.Scan(home)
	tt := newUITester(a.view, 1280, 800)
	for _, s := range a.lib.Skills {
		if s.Source == library.Project && s.Name == "deploy" {
			a.selected = s
		}
	}
	tt.Frame()
	tt.Advance(paneDuration)
	if !tt.HasText("ZCode") || !tt.HasText("已停用") {
		t.Fatalf("读得到不等于启用：项目技能要写出在 ZCode 里已停用：%q", tt.Texts())
	}
}

func TestZCodeProjectSkillWithMalformedProjectConfigShowsUnknownAndWhy(t *testing.T) {
	a, _, home := zcodeApp(t)
	dir := filepath.Join(home, "code/app")
	skill := filepath.Join(dir, ".zcode/skills/deploy")
	must(t, os.MkdirAll(skill, 0o755))
	must(t, os.WriteFile(filepath.Join(skill, "SKILL.md"), []byte("---\nname: deploy\ndescription: x\n---\n"), 0o644))
	must(t, a.lib.AddProjects([]string{dir}))
	must(t, os.WriteFile(filepath.Join(dir, "zcode.json"), []byte("{broken"), 0o600))
	a.lib = library.Scan(home)
	tt := newUITester(a.view, 1280, 800)
	for _, s := range a.lib.Skills {
		if s.Source == library.Project && s.Name == "deploy" {
			a.selected = s
		}
	}
	tt.Frame()
	tt.Advance(paneDuration)
	if !tt.HasText("未知") || tt.HasText("已停用") {
		t.Fatalf("项目配置读不懂：ZCode 那一行要写未知：%q", tt.Texts())
	}
	if !hasTextPrefix(tt, "无法读取 app 的项目配置。") {
		t.Fatalf("要看得到原因，不能只藏在悬停提示里：%q", tt.Texts())
	}
	if !tt.HasText("重试") {
		t.Fatalf("读不了的可以重试：%q", tt.Texts())
	}
}

func hasTextPrefix(tt *ui.Tester, prefix string) bool {
	for _, text := range tt.Texts() {
		if strings.HasPrefix(text, prefix) {
			return true
		}
	}
	return false
}

// TestZCodeSnapshots 用 SNAPSHOT_DIR 出浅色、深色截图核对：开关 + 移除、停用、
// 总开关关着、项目技能里已停用。
func TestZCodeSnapshots(t *testing.T) {
	dir := os.Getenv("SNAPSHOT_DIR")
	if dir == "" {
		t.Skip("SNAPSHOT_DIR")
	}
	for _, dark := range []bool{false, true} {
		theme := "light"
		if dark {
			theme = "dark"
		}
		shot := func(name string, a *app) {
			tt := ui.NewTesterWith(a.view, 1280, 800, ui.TesterOptions{Dark: dark})
			tt.Frame()
			tt.Advance(paneDuration)
			writePNG(t, filepath.Join(dir, "zcode-"+name+"-"+theme+".png"), tt)
		}
		a, _, home := zcodeApp(t)
		s := a.lib.Get("kami")
		must(t, a.lib.AddToAgent(s, "zcode"))
		a.selected = s
		shot("on", a)
		must(t, a.lib.Enable(s, "zcode", false))
		shot("off", a)
		must(t, os.WriteFile(filepath.Join(home, ".zcode/cli/config.json"), []byte(`{"features":{"skill":false}}`), 0o600))
		a.lib = library.Scan(home)
		a.selected = a.lib.Get("kami")
		shot("master-off", a)

		a, _, home = zcodeApp(t)
		proj := filepath.Join(home, "code/app")
		skill := filepath.Join(proj, ".zcode/skills/deploy")
		must(t, os.MkdirAll(skill, 0o755))
		must(t, os.WriteFile(filepath.Join(skill, "SKILL.md"), []byte("---\nname: deploy\ndescription: x\n---\n"), 0o644))
		must(t, a.lib.AddProjects([]string{proj}))
		must(t, os.WriteFile(filepath.Join(home, ".zcode/cli/config.json"),
			[]byte(`{"skills":{"`+filepath.Join(skill, "SKILL.md")+`":{"enable":false}}}`), 0o600))
		a.lib = library.Scan(home)
		for _, s := range a.lib.Skills {
			if s.Source == library.Project {
				a.selected = s
			}
		}
		shot("project", a)
		must(t, os.WriteFile(filepath.Join(proj, "zcode.json"), []byte("{broken"), 0o600))
		a.lib = library.Scan(home)
		for _, s := range a.lib.Skills {
			if s.Source == library.Project {
				a.selected = s
			}
		}
		shot("project-unknown", a)
	}
}

func TestBoardMoreButtonIconIsVerticallyCentered(t *testing.T) {
	a, tt := newTester(t)
	a.mode = viewBoard
	tt.SetScale(2)
	tt.Frame()
	tt.Advance(350 * time.Millisecond) // 等 modePane 入场位移结束
	tt.Frame()

	target := "Claude Code · interface-craft 操作"
	rect, ok := tt.Find(target)
	if !ok {
		t.Fatal("missing card menu")
	}
	tt.Move(rect.X+rect.W/2, rect.Y+rect.H/2)
	tt.Frame()
	must(t, tt.Click(target))
	tt.Advance(250 * time.Millisecond)
	tt.Frame()

	rect, ok = tt.Find(target)
	if !ok {
		t.Fatal("missing open card menu trigger")
	}
	img := tt.Image()
	const scale = 2
	x0 := int(rect.X * scale)
	y0 := int(rect.Y * scale)
	x1 := int((rect.X + rect.W) * scale)
	y1 := int((rect.Y + rect.H) * scale)
	bg := img.RGBAAt((x0+x1)/2, y0+4)
	minY, maxY := y1, y0
	dots := 0
	for y := y0 + 2; y < y1-2; y++ {
		for x := x0 + 4; x < x1-4; x++ {
			c := img.RGBAAt(x, y)
			// 图标像素明显深于按钮背景（colInkSelected 叠白底约 ~237）
			if int(bg.R)-int(c.R) > 60 {
				dots++
				if y < minY {
					minY = y
				}
				if y > maxY {
					maxY = y
				}
			}
		}
	}
	if dots == 0 {
		t.Fatalf("未在按钮区域 %+v 内检测到「…」图标像素（bg=%v）", rect, bg)
	}
	topPad := minY - y0
	botPad := (y1 - 1) - maxY
	diff := topPad - botPad
	if diff < 0 {
		diff = -diff
	}
	if diff > 2 {
		t.Fatalf("看板卡片「…」图标未垂直居中：dotsY=[%d..%d] inside pxY=[%d..%d), topPad=%dpx, botPad=%dpx",
			minY, maxY, y0, y1, topPad, botPad)
	}
}
