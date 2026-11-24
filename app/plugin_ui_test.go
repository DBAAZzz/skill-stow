package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"

	"skillmanager/internal/library"
)

// pluginTester 打开 Claude Code 插件 document-skills 的集合，让 docx 的卡片露出来。
// 这个集合排在「我的」最下面，窗口要高到不用滚动就点得到。
func pluginTester(t *testing.T) (*app, *ui.Tester) {
	t.Helper()
	a := newApp(library.Sample())
	tt := newUITester(a.view, 1280, 2400)
	doubleClick(t, tt, "document-skills")
	tt.Advance(paneDuration)
	if _, ok := tt.Find("docx"); !ok {
		t.Fatalf("没打开 document-skills：%q", tt.Texts())
	}
	return a, tt
}

func pluginSkillsLeft(a *app) []string {
	var out []string
	for _, s := range a.lib.Skills {
		if s.Source == library.ViaPlugin && s.Plugin == "document-skills" {
			out = append(out, s.Name)
		}
	}
	return out
}

func TestUserUninstallsAClaudePluginFromASkillsContextMenu(t *testing.T) {
	// Given 插件 document-skills 带 pdf、docx、xlsx 三个技能
	a, tt := pluginTester(t)

	// When 右键 docx 选「卸载插件…」
	must(t, tt.RightClick("docx"))
	clickMenuItem(t, tt, "卸载插件…")
	tt.Frame()

	// Then 确认框写出要执行的命令和一起移除的三个技能，默认按钮是取消
	for _, want := range []string{
		"卸载插件 document-skills？",
		"会在本机执行下面的 Claude Code 命令，插件带的 3 个 Skill 和它的命令、Agent、Hook 一起移除。卸载后不能在这里撤销，要再用时在 Claude Code 里重新安装。",
		"claude plugin uninstall document-skills --scope user",
		"pdf", "xlsx", "取消", "卸载",
	} {
		if !tt.HasText(want) {
			t.Errorf("确认框缺少 %q：%q", want, tt.Texts())
		}
	}

	// When 点「卸载」
	must(t, tt.Click("卸载"))
	tt.Frame()

	// Then 插件的技能都不在了，提示条说已卸载
	if left := pluginSkillsLeft(a); len(left) > 0 {
		t.Errorf("卸载后还剩 %v", left)
	}
	if !tt.HasText("已卸载插件 document-skills") {
		t.Errorf("应提示已卸载：%q", tt.Texts())
	}
	if a.plug.open {
		t.Error("卸载完确认框应关上")
	}
}

func TestCancellingThePluginUninstallKeepsThePlugin(t *testing.T) {
	// Given 选中插件里的 docx，详情栏说明后面有「卸载插件…」
	a, tt := pluginTester(t)
	must(t, tt.Click("docx"))
	tt.Advance(paneDuration)
	if !tt.HasText("随插件 document-skills 安装，只在 Claude Code 里生效，这里不能单独停用。不再需要时可以卸载整个插件。") {
		t.Fatalf("详情栏应说明可以卸载整个插件：%q", tt.Texts())
	}

	// When 点「卸载插件…」再取消
	must(t, tt.Click("卸载插件…"))
	tt.Frame()
	if !tt.HasText("卸载插件 document-skills？") {
		t.Fatalf("应弹出确认框：%q", tt.Texts())
	}
	must(t, tt.Click("取消"))
	tt.Frame()

	// Then 插件还在
	if left := pluginSkillsLeft(a); len(left) != 3 {
		t.Errorf("取消后插件技能应还在 3 个：%v", left)
	}
}

func TestBuiltinSkillsOfferNoPluginUninstall(t *testing.T) {
	// Given 浏览内置技能
	_, tt := newTester(t)
	must(t, tt.Click("内置"))

	// When 右键 skill-creator
	must(t, tt.RightClick("skill-creator"))

	// Then 菜单里没有「卸载插件…」
	if tt.HasText("卸载插件…") {
		t.Errorf("内置 Skill 不应能卸载插件：%q", tt.Texts())
	}
}

func TestPluginUninstallSnapshots(t *testing.T) {
	dir := os.Getenv("SNAPSHOT_DIR")
	if dir == "" {
		t.Skip("设置 SNAPSHOT_DIR 以输出截图")
	}
	for name, steps := range map[string]func(a *app, tt *ui.Tester){
		"plug-pane": func(_ *app, tt *ui.Tester) { must(t, tt.Click("docx")) },
		"plug-set":  func(a *app, _ *ui.Tester) { a.coll, a.inColl = a.inColl, nil },
		"plug-confirm": func(a *app, _ *ui.Tester) {
			a.requestUninstallPlugin(a.lib.PluginSkills(a.inColl.Skills[0])[0])
		},
	} {
		a, tt := pluginTester(t)
		tt.SetScale(2)
		steps(a, tt)
		tt.Frame()
		tt.Advance(paneDuration)
		writePNG(t, filepath.Join(dir, name+".png"), tt)
	}
	_, tt := tallTester(t)
	tt.SetScale(2)
	hover(t, tt, "docx、pdf、xlsx")
	tt.Advance(actionsFade)
	writePNG(t, filepath.Join(dir, "plug-hover.png"), tt)
}

// tallTester 是不用滚动就看得到「我的」最下面插件集合的窗口。
func tallTester(t *testing.T) (*app, *ui.Tester) {
	t.Helper()
	a := newApp(library.Sample())
	return a, newUITester(a.view, 1280, 2400)
}

func TestHoveringAPluginCollectionOffersOpenAndUninstall(t *testing.T) {
	// Given 「我的」里有 Claude Code 插件集合 document-skills
	a, tt := tallTester(t)

	// When 指针停在它上面
	hover(t, tt, "docx、pdf、xlsx")
	tt.Advance(actionsFade)

	// Then 底部换成「打开」和「卸载」，点「卸载」弹出卸载整个插件的确认框
	for _, want := range []string{"打开", "卸载"} {
		if _, ok := tt.Find(want); !ok {
			t.Fatalf("插件集合悬停时应有 %q：%q", want, tt.Texts())
		}
	}
	must(t, tt.Click("卸载"))
	tt.Frame()
	if !a.plug.open || !tt.HasText("卸载插件 document-skills？") {
		t.Fatalf("应弹出卸载确认框：%q", tt.Texts())
	}
}

func TestHoveringAPluginCollectionThenOpeningIt(t *testing.T) {
	// Given 指针停在插件集合 document-skills 上
	a, tt := tallTester(t)
	hover(t, tt, "docx、pdf、xlsx")
	tt.Advance(actionsFade)

	// When 点「打开」
	must(t, tt.Click("打开"))
	tt.Advance(paneDuration)

	// Then 进入这个集合
	if a.inColl == nil || a.inColl.Name != "document-skills" {
		t.Errorf("应打开 document-skills，实际 %v", a.inColl)
	}
}

func TestHoveringOtherCollectionsOffersNoUninstall(t *testing.T) {
	// Given 「我的」里 npx 装的集合 open.feishu.cn
	_, tt := tallTester(t)

	// When 指针停在它上面
	hover(t, tt, "open.feishu.cn")
	tt.Advance(actionsFade)

	// Then 没有「卸载」
	if _, ok := tt.Find("卸载"); ok {
		t.Errorf("非插件集合悬停时不应有「卸载」：%q", tt.Texts())
	}
}

// pluginHomeTester 用假 home 打开应用：Claude Code 装了只带 solo 一个技能的插件
// solo-plugin，和带 duo-a、duo-b 两个技能的插件 duo-plugin。
func pluginHomeTester(t *testing.T) (*app, *ui.Tester) {
	t.Helper()
	home, _ := filepath.EvalSymlinks(t.TempDir())
	var entries []string
	for plugin, skills := range map[string][]string{"solo-plugin": {"solo"}, "duo-plugin": {"duo-a", "duo-b"}} {
		dir := filepath.Join(home, ".claude/plugins/cache/market", plugin, "1.0.0")
		for _, name := range skills {
			must(t, os.MkdirAll(filepath.Join(dir, "skills", name), 0o755))
			md := "---\nname: " + name + "\ndescription: 插件里的技能 " + name + "\n---\n"
			must(t, os.WriteFile(filepath.Join(dir, "skills", name, "SKILL.md"), []byte(md), 0o644))
		}
		entries = append(entries, `"`+plugin+`@market":[{"scope":"user","installPath":"`+dir+`"}]`)
	}
	must(t, os.WriteFile(filepath.Join(home, ".claude/plugins/installed_plugins.json"),
		[]byte(`{"version":2,"plugins":{`+strings.Join(entries, ",")+`}}`), 0o644))
	a := newApp(library.Scan(home))
	return a, newUITester(a.view, 1280, 800)
}

func TestHoveringTheOnlySkillOfAPluginOffersUninstall(t *testing.T) {
	// Given 插件 solo-plugin 只带 solo 一个技能，不成集合
	a, tt := pluginHomeTester(t)

	// When 指针停在 solo 的卡片上
	hover(t, tt, "插件里的技能 solo")
	tt.Advance(actionsFade)

	// Then 底部换成预览、在 Finder 中显示和卸载，点卸载弹出卸载整个插件的确认框
	for _, want := range []string{"预览", "在 Finder 中显示", "卸载"} {
		if _, ok := tt.Find(want); !ok {
			t.Fatalf("单技能插件的卡片悬停时应有 %q：%q", want, tt.Texts())
		}
	}
	must(t, tt.Click("卸载"))
	tt.Frame()
	if !a.plug.open || !tt.HasText("卸载插件 solo-plugin？") {
		t.Fatalf("应弹出卸载确认框：%q", tt.Texts())
	}
}

func TestHoveringOneSkillOfAMultiSkillPluginOffersNoUninstall(t *testing.T) {
	// Given 插件 duo-plugin 带 duo-a、duo-b 两个技能
	_, tt := pluginHomeTester(t)
	doubleClick(t, tt, "duo-plugin")
	tt.Advance(paneDuration)

	// When 指针停在其中 duo-a 的卡片上
	hover(t, tt, "插件里的技能 duo-a")
	tt.Advance(actionsFade)

	// Then 没有卸载，免得以为只删 duo-a
	if _, ok := tt.Find("卸载"); ok {
		t.Errorf("多技能插件里单张卡片悬停时不应有「卸载」：%q", tt.Texts())
	}
}
