package library

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// fakeRun 换掉真的 Agent 命令行，记下每次调用；run 决定它做什么、返回什么。
func fakeRun(t *testing.T, run func(c pluginCmd) (string, error)) *[]pluginCmd {
	t.Helper()
	var calls []pluginCmd
	old := runPluginCommand
	runPluginCommand = func(_ context.Context, c pluginCmd) ([]byte, []byte, error) {
		calls = append(calls, c)
		out, err := "", error(nil)
		if run != nil {
			out, err = run(c)
		}
		return []byte(out), nil, err
	}
	t.Cleanup(func() { runPluginCommand = old })
	return &calls
}

// pluginHome 造一个假 home：Claude Code 装了插件 pua（p10、p11 两个技能），
// claude 命令放在 ~/.local/bin；WorkBuddy 装了用户插件 find-skills 和内置插件
// tencent-docx，WorkBuddy 应用在 ~/Applications 下。
func pluginHome(t *testing.T, scope, project string) string {
	t.Helper()
	root, _ := filepath.EvalSymlinks(t.TempDir())
	h := func(p string) string { return filepath.Join(root, p) }
	install := h(".claude/plugins/cache/pua-skills/pua/3.0.0")
	writeSkill(t, filepath.Join(install, "skills/p10"), "")
	writeSkill(t, filepath.Join(install, "skills/p11"), "")
	entry := `{"installPath":"` + jsonStr(install) + `","scope":"` + scope + `"`
	if project != "" {
		entry += `,"projectPath":"` + jsonStr(project) + `"`
	}
	must(t, os.MkdirAll(h(".claude/plugins"), 0o755))
	must(t, os.WriteFile(h(".claude/plugins/installed_plugins.json"), []byte(`{"plugins":{"pua@pua-skills":[`+entry+`}]}}`), 0o644))
	must(t, os.MkdirAll(h(".local/bin"), 0o755))
	must(t, os.WriteFile(h(".local/bin/claude"), []byte("#!/bin/sh\n"), 0o755))

	find := h(".workbuddy/plugins/cache/codebuddy-plugins-official/find-skills/1.0.0")
	docx := h(".workbuddy/plugins/cache/workbuddy-builtin/tencent-docx/0.3.0")
	writeSkill(t, filepath.Join(find, "skills/find-skills"), "")
	writeSkill(t, filepath.Join(docx, "skills/tencent-docx"), "")
	must(t, os.WriteFile(h(".workbuddy/plugins/installed_plugins.json"), []byte(`{"version":2,"plugins":{`+
		`"find-skills@codebuddy-plugins-official":[{"scope":"user","installPath":"`+jsonStr(find)+`"}],`+
		`"tencent-docx@workbuddy-builtin":[{"scope":"user","installPath":"`+jsonStr(docx)+`"}]}}`), 0o644))
	must(t, os.WriteFile(h(".workbuddy/config.json"), []byte("{}"), 0o644))
	app := h("Applications/WorkBuddy.app/Contents")
	must(t, os.MkdirAll(filepath.Join(app, "MacOS"), 0o755))
	must(t, os.WriteFile(filepath.Join(app, "MacOS/Electron"), []byte("#!/bin/sh\n"), 0o755))
	must(t, os.MkdirAll(filepath.Join(app, "Resources/app.asar.unpacked/cli/bin"), 0o755))
	must(t, os.WriteFile(filepath.Join(app, "Resources/app.asar.unpacked/cli/bin/codebuddy"), []byte("#!/usr/bin/env node\n"), 0o755))

	t.Setenv("PATH", "")
	old := workBuddyApps
	workBuddyApps = func(home string) []string { return []string{filepath.Join(home, "Applications/WorkBuddy.app")} }
	t.Cleanup(func() { workBuddyApps = old })
	return root
}

func byName(l *Library, name string) *Skill {
	i := slices.IndexFunc(l.Skills, func(s *Skill) bool { return s.Name == name })
	if i < 0 {
		return nil
	}
	return l.Skills[i]
}

func TestUninstallingAClaudePluginRunsItsCommandForTheWholePlugin(t *testing.T) {
	// Given Claude Code 用户范围装了插件 pua，带 p10、p11 两个技能
	home := pluginHome(t, "user", "")
	calls := fakeRun(t, func(pluginCmd) (string, error) { return `{"outcome":"uninstalled"}`, nil })
	l := Scan(home)
	p10 := byName(l, "p10")

	// When 从 p10 卸载它所在的插件
	if !l.CanUninstallPlugin(p10) {
		t.Fatal("Claude Code 用户装的插件应能在这里卸载")
	}
	var got []string
	for _, s := range l.PluginSkills(p10) {
		got = append(got, s.Name)
	}
	if !slices.Equal(got, []string{"p10", "p11"}) {
		t.Errorf("一起卸掉的技能 %v，应为 p10、p11", got)
	}
	if err := l.UninstallPlugin(p10); err != nil {
		t.Fatal(err)
	}

	// Then 在主目录下用 PATH 外找到的 claude 按注册名和范围卸载
	want := []string{"plugin", "uninstall", "pua@pua-skills", "--scope", "user", "--json"}
	if len(*calls) != 1 || (*calls)[0].bin != filepath.Join(home, ".local/bin/claude") || (*calls)[0].dir != home || !slices.Equal((*calls)[0].args, want) {
		t.Errorf("执行了 %+v，应在 %s 里执行 claude %v", *calls, home, want)
	}
	if got := l.UninstallCommand(p10); got != "claude plugin uninstall pua@pua-skills --scope user" {
		t.Errorf("确认框里的命令 %q", got)
	}
}

func TestUninstallingAProjectPluginRunsInThatProject(t *testing.T) {
	// Given 插件 pua 装在某个项目的 project 范围
	project := t.TempDir()
	home := pluginHome(t, "project", project)
	calls := fakeRun(t, nil)
	l := Scan(home)

	// When 卸载它
	must(t, l.UninstallPlugin(byName(l, "p10")))

	// Then 命令在那个项目里、按 project 范围执行
	if len(*calls) != 1 || (*calls)[0].dir != project || !slices.Contains((*calls)[0].args, "project") {
		t.Errorf("执行了 %+v，应在 %s 里按 project 范围卸载", *calls, project)
	}
}

func TestUninstallReportsWhenClaudeIsMissing(t *testing.T) {
	// Given 本机找不到 claude 命令
	home := pluginHome(t, "user", "")
	must(t, os.Remove(filepath.Join(home, ".local/bin/claude")))
	calls := fakeRun(t, nil)
	l := Scan(home)

	// When 卸载插件
	err := l.UninstallPlugin(byName(l, "p10"))

	// Then 报错且不执行命令
	if err == nil || len(*calls) != 0 {
		t.Errorf("找不到 claude 时应报错：err=%v calls=%v", err, *calls)
	}
}

// dropFromRegistry 像 codebuddy 命令行卸载成功那样，把插件从登记文件里拿掉。
func dropFromRegistry(t *testing.T, home, id string) {
	t.Helper()
	p := filepath.Join(home, ".workbuddy/plugins/installed_plugins.json")
	b, err := os.ReadFile(p)
	must(t, err)
	s := string(b)
	i := strings.Index(s, `"`+id+`"`)
	j := strings.Index(s[i:], "],") + i + 2
	must(t, os.WriteFile(p, []byte(s[:i]+s[j:]), 0o644))
}

func TestUninstallingAWorkBuddyPluginRunsItsBundledCli(t *testing.T) {
	// Given WorkBuddy 里用户装了 find-skills
	home := pluginHome(t, "user", "")
	calls := fakeRun(t, func(pluginCmd) (string, error) {
		dropFromRegistry(t, home, "find-skills@codebuddy-plugins-official")
		return "Uninstalling plugin(s): find-skills@codebuddy-plugins-official...\n✔ Uninstalled\n", nil
	})
	l := Scan(home)
	find := byName(l, "find-skills")

	// When 卸载它
	if !l.CanUninstallPlugin(find) {
		t.Fatal("WorkBuddy 用户装的插件应能在这里卸载")
	}
	if err := l.UninstallPlugin(find); err != nil {
		t.Fatal(err)
	}

	// Then 用 WorkBuddy 的 Electron 跑它自带的 codebuddy，配置目录指到 ~/.workbuddy
	app := filepath.Join(home, "Applications/WorkBuddy.app/Contents")
	if len(*calls) != 1 {
		t.Fatalf("应执行一次命令：%+v", *calls)
	}
	c := (*calls)[0]
	wantArgs := []string{filepath.Join(app, "Resources/app.asar.unpacked/cli/bin/codebuddy"), "plugin", "uninstall", "find-skills@codebuddy-plugins-official", "--scope", "user"}
	if c.bin != filepath.Join(app, "MacOS/Electron") || !slices.Equal(c.args, wantArgs) {
		t.Errorf("执行了 %s %v", c.bin, c.args)
	}
	for _, want := range []string{"ELECTRON_RUN_AS_NODE=1", "CODEBUDDY_CONFIG_DIR=" + filepath.Join(home, ".workbuddy"), "CODEBUDDY_FORCE_LITE_WB_BUNDLE=1"} {
		if !slices.Contains(c.env, want) {
			t.Errorf("环境变量缺少 %s：%v", want, c.env)
		}
	}
	if got := l.UninstallCommand(find); got != "codebuddy plugin uninstall find-skills@codebuddy-plugins-official --scope user" {
		t.Errorf("确认框里的命令 %q", got)
	}
}

func TestWorkBuddyUninstallFailsWhenThePluginIsStillRegistered(t *testing.T) {
	for _, c := range []struct {
		name, stdout, want string
	}{
		{"用命令说的原因", "Uninstalling plugin(s): find-skills@codebuddy-plugins-official...\n✘ Failed to uninstall plugin(s): Marketplace codebuddy-plugins-official is not found.\n", "Marketplace codebuddy-plugins-official is not found."},
		{"命令什么也没说", "", "命令执行完了，但插件仍然登记为已安装。"},
	} {
		t.Run(c.name, func(t *testing.T) {
			// Given codebuddy 命令行卸载失败时退出码也是 0，登记文件没变
			home := pluginHome(t, "user", "")
			fakeRun(t, func(pluginCmd) (string, error) { return c.stdout, nil })
			l := Scan(home)

			// When 卸载 find-skills
			err := l.UninstallPlugin(byName(l, "find-skills"))

			// Then 按登记文件判断没卸掉
			if err == nil || err.Error() != c.want {
				t.Errorf("得到 %v，应为 %q", err, c.want)
			}
		})
	}
}

func TestWorkBuddyUninstallReportsWhenTheAppIsMissing(t *testing.T) {
	// Given 找不到 WorkBuddy 应用
	home := pluginHome(t, "user", "")
	must(t, os.RemoveAll(filepath.Join(home, "Applications")))
	calls := fakeRun(t, nil)
	l := Scan(home)

	// When 卸载 find-skills
	err := l.UninstallPlugin(byName(l, "find-skills"))

	// Then 报错且不执行命令
	if err == nil || len(*calls) != 0 {
		t.Errorf("找不到 WorkBuddy 时应报错：err=%v calls=%v", err, *calls)
	}
}

func TestBuiltinPluginsCannotBeUninstalledHere(t *testing.T) {
	// Given WorkBuddy 预装的 tencent-docx
	home := pluginHome(t, "user", "")
	calls := fakeRun(t, nil)
	l := Scan(home)
	docx := byName(l, "tencent-docx")

	// When 想卸载它
	err := l.UninstallPlugin(docx)

	// Then 不能，也不执行命令：卸了 WorkBuddy 也会再装回来
	if l.CanUninstallPlugin(docx) || err == nil || len(*calls) != 0 {
		t.Errorf("内置插件不应能在这里卸载：can=%v err=%v calls=%v", l.CanUninstallPlugin(docx), err, *calls)
	}
}

func TestUninstallResultUsesTheCommandsOwnMessage(t *testing.T) {
	failed := []byte(`{"command":"uninstall","outcome":"failed","plugin":"x@y","scope":"user","message":"Plugin \"x@y\" not found in installed plugins","failureCode":"not_installed"}`)
	for _, c := range []struct {
		name           string
		stdout, stderr string
		runErr         error
		want           string
	}{
		{"成功", `{"command":"uninstall","outcome":"uninstalled"}`, "", nil, ""},
		{"失败时用命令的说明", string(failed), "", errors.New("exit status 1"), `Plugin "x@y" not found in installed plugins`},
		{"没有 JSON 时用标准错误", "", "boom\n", errors.New("exit status 1"), "boom"},
		{"什么都没有时用退出码", "", "", errors.New("exit status 2"), "exit status 2"},
	} {
		t.Run(c.name, func(t *testing.T) {
			err := uninstallResult([]byte(c.stdout), []byte(c.stderr), c.runErr)
			got := ""
			if err != nil {
				got = err.Error()
			}
			if got != c.want {
				t.Errorf("得到 %q，应为 %q", got, c.want)
			}
		})
	}
}
