package library

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// codexHome 造一个装了 Codex 的假 home：
//   - 本地市场 local-m 里，用户装的 alpha（技能 alpha-a、alpha-b）和默认装上的 beta（技能 beta-a）；
//   - 远程目录装的 gamma（目录缓存里是可选的，技能 gamma-a）和 delta（缓存里查不到，技能 delta-a）；
//   - 缓存里有、但没登记的 canva（技能 canva-a）；
//   - 系统技能 imagegen。
func codexHome(t *testing.T) string {
	t.Helper()
	root, _ := filepath.EvalSymlinks(t.TempDir())
	h := func(p string) string { return filepath.Join(root, ".codex", p) }
	skill := func(dir string, names ...string) {
		for _, n := range names {
			writeSkill(t, filepath.Join(dir, "skills", n), "")
		}
	}
	market := h("markets/local-m")
	must(t, os.MkdirAll(filepath.Join(market, ".agents/plugins"), 0o755))
	must(t, os.WriteFile(filepath.Join(market, ".agents/plugins/marketplace.json"), []byte(`{"name":"local-m","plugins":[
		{"name":"alpha","policy":{"installation":"AVAILABLE"}},
		{"name":"beta","policy":{"installation":"INSTALLED_BY_DEFAULT"}}]}`), 0o644))
	skill(h("plugins/cache/local-m/alpha/1.0.0"), "alpha-a", "alpha-b")
	skill(h("plugins/cache/local-m/beta/2.0.0"), "beta-a")
	for name, id := range map[string]string{"gamma": "r-gamma", "delta": "r-delta"} {
		dir := h("plugins/cache/remote-m/" + name)
		skill(filepath.Join(dir, "0.1.0"), name+"-a")
		must(t, os.WriteFile(filepath.Join(dir, codexRemoteMarker), []byte(`{"schema_version":1,"remote_plugin_id":"`+id+`"}`), 0o644))
	}
	skill(h("plugins/cache/remote-m/canva/abc"), "canva-a")
	must(t, os.MkdirAll(h(codexCatalogDir), 0o755))
	must(t, os.WriteFile(filepath.Join(h(codexCatalogDir), "old.json"), []byte(`{"fetched_at":"2026-01-01T00:00:00Z","plugins":[{"id":"r-gamma","installation_policy":"NOT_AVAILABLE"}]}`), 0o644))
	must(t, os.WriteFile(filepath.Join(h(codexCatalogDir), "new.json"), []byte(`{"fetched_at":"2026-11-01T00:00:00Z","plugins":[{"id":"r-gamma","installation_policy":"AVAILABLE"}]}`), 0o644))
	writeSkill(t, h("skills/.system/imagegen"), "")
	must(t, os.WriteFile(h("config.toml"), []byte(codexConfig(market)), 0o644))
	return root
}

func codexConfig(market string) string {
	return `model = "gpt-5"

[marketplaces.local-m]
source_type = "local"
source = "` + jsonStr(market) + `"

[plugins."alpha@local-m"]
enabled = true

[plugins.'beta@local-m']
enabled = true
`
}

func codexSkill(l *Library, name string) *Skill {
	s := byName(l, name)
	if s == nil || s.Provider != codex {
		return nil
	}
	return s
}

func TestCodexPluginsAreSortedByTheirInstallPolicy(t *testing.T) {
	// Given Codex 装了本地市场和远程目录的插件，各有用户装的和默认装上的
	l := Scan(codexHome(t))

	// Then 用户装的算插件安装，默认装上的和系统技能算内置，只有缓存没登记的不算
	for name, want := range map[string]Source{
		"alpha-a": ViaPlugin, "alpha-b": ViaPlugin, "gamma-a": ViaPlugin,
		"beta-a": Builtin, "delta-a": Builtin, "imagegen": Builtin,
	} {
		s := codexSkill(l, name)
		if s == nil {
			t.Errorf("没扫到 Codex 的 %s：%v", name, names(l))
			continue
		}
		if s.Source != want {
			t.Errorf("%s 应是%s，实际%s", name, want.Label(), s.Source.Label())
		}
	}
	if codexSkill(l, "canva-a") != nil {
		t.Error("缓存里有但没登记的插件不算装了")
	}
	if got := len(l.PluginSkills(codexSkill(l, "alpha-a"))); got != 2 {
		t.Errorf("alpha 插件应带 2 个技能，实际 %d", got)
	}
}

// fakeCodex 让 findCodexBinary 找到假 home 里的一个可执行文件。
func fakeCodex(t *testing.T, home string) string {
	t.Helper()
	bin := filepath.Join(home, "bin/codex")
	must(t, os.MkdirAll(filepath.Dir(bin), 0o755))
	must(t, os.WriteFile(bin, []byte("#!/bin/sh\n"), 0o755))
	old := codexCandidates
	codexCandidates = func(string) []string { return []string{bin} }
	t.Cleanup(func() { codexCandidates = old })
	return bin
}

func TestUninstallingACodexPluginRunsCodexPluginRemove(t *testing.T) {
	// Given 用户装的 Codex 插件 alpha
	home := codexHome(t)
	bin := fakeCodex(t, home)
	config := filepath.Join(home, ".codex/config.toml")
	calls := fakeRun(t, func(pluginCmd) (string, error) {
		// 像 codex 那样把登记从 config.toml 里拿掉
		b, _ := os.ReadFile(config)
		s := strings.Replace(string(b), "[plugins.\"alpha@local-m\"]\nenabled = true\n", "", 1)
		return `{"pluginId":"alpha@local-m"}`, os.WriteFile(config, []byte(s), 0o644)
	})
	l := Scan(home)
	alpha := codexSkill(l, "alpha-a")

	// When 卸载它
	if !l.CanUninstallPlugin(alpha) {
		t.Fatal("Codex 用户装的插件应能在这里卸载")
	}
	if err := l.UninstallPlugin(alpha); err != nil {
		t.Fatal(err)
	}

	// Then 执行 codex plugin remove，确认框里写的也是它
	want := []string{"plugin", "remove", "alpha@local-m", "--json"}
	if len(*calls) != 1 || (*calls)[0].bin != bin || !slices.Equal((*calls)[0].args, want) {
		t.Errorf("执行了 %+v，应为 %s %v", *calls, bin, want)
	}
	if got := l.UninstallCommand(alpha); got != "codex plugin remove alpha@local-m" {
		t.Errorf("确认框里的命令 %q", got)
	}
}

func TestCodexUninstallFailsWhenThePluginIsStillRegistered(t *testing.T) {
	// Given codex plugin remove 对没卸掉的插件也照样成功退出
	home := codexHome(t)
	fakeCodex(t, home)
	fakeRun(t, func(pluginCmd) (string, error) { return `{"pluginId":"gamma@remote-m"}`, nil })
	l := Scan(home)

	// When 卸载远程装的 gamma，缓存里的安装标记还在
	err := l.UninstallPlugin(codexSkill(l, "gamma-a"))

	// Then 按磁盘判断没卸掉
	if err == nil || err.Error() != "命令执行完了，但插件仍然登记为已安装。" {
		t.Errorf("得到 %v", err)
	}
}

func TestCodexBuiltinPluginsCannotBeUninstalled(t *testing.T) {
	l := Scan(codexHome(t))
	for _, name := range []string{"beta-a", "delta-a", "imagegen"} {
		if l.CanUninstallPlugin(codexSkill(l, name)) {
			t.Errorf("%s 是内置的，不应能卸载", name)
		}
	}
}

func TestFindCodexUsesTheNativeBinaryOfAnNpmInstall(t *testing.T) {
	// Given npm 装的 codex：bin/codex 链到包里的 codex.js，原生程序在平台包里
	root, _ := filepath.EvalSymlinks(t.TempDir())
	pkg := filepath.Join(root, "lib/node_modules/@openai/codex")
	native := filepath.Join(pkg, "node_modules/@openai/codex-darwin-arm64/vendor/aarch64-apple-darwin/bin/codex")
	must(t, os.MkdirAll(filepath.Dir(native), 0o755))
	must(t, os.WriteFile(native, []byte("bin"), 0o755))
	must(t, os.MkdirAll(filepath.Join(pkg, "bin"), 0o755))
	must(t, os.WriteFile(filepath.Join(pkg, "bin/codex.js"), []byte("#!/usr/bin/env node\n"), 0o755))
	must(t, os.MkdirAll(filepath.Join(root, "bin"), 0o755))
	must(t, os.Symlink(filepath.Join(pkg, "bin/codex.js"), filepath.Join(root, "bin/codex")))
	old := codexCandidates
	codexCandidates = func(string) []string { return []string{filepath.Join(root, "bin/codex")} }
	t.Cleanup(func() { codexCandidates = old })

	// Then 用原生程序，不用要 node 才能跑的脚本
	if got := findCodexBinary(root); got != native {
		t.Errorf("找到 %q，应为 %q", got, native)
	}
}

func TestParseCodexPluginsReadsQuotedKeys(t *testing.T) {
	plugins, markets := parseCodexPlugins(codexConfig("/m") + "\n[plugins]\nfoo = 1\n[plugins.\"x@y\".nested]\na = 1\n")
	if !slices.Equal(plugins, []string{"alpha@local-m", "beta@local-m"}) || markets["local-m"] != "/m" {
		t.Errorf("插件 %v，市场 %v", plugins, markets)
	}
}

func TestRescansWhenCodexInstallsARemotePlugin(t *testing.T) {
	// Given Codex 从远程目录装插件时只在缓存的市场目录下新建目录
	home := codexHome(t)
	plan := Scan(home).WatchPlan()

	// Then 缓存里的每个市场目录都在监听里
	for _, m := range []string{"local-m", "remote-m"} {
		if !slices.Contains(plan.Dirs, filepath.Join(home, ".codex", codexPluginCache, m)) {
			t.Errorf("应监听 Codex 插件缓存的 %s：%v", m, plan.Dirs)
		}
	}
}
