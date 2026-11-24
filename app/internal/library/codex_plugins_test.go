package library

import (
	"maps"
	"os"
	"path/filepath"
	"runtime"
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

func TestCodexUninstallFailsWhenTheConfigCannotBeRead(t *testing.T) {
	// Given codex plugin remove 成功退出，但 config.toml 坏了，看不出插件还在不在
	home := codexHome(t)
	fakeCodex(t, home)
	config := filepath.Join(home, ".codex/config.toml")
	l := Scan(home)
	fakeRun(t, func(pluginCmd) (string, error) {
		return `{"pluginId":"alpha@local-m"}`, os.WriteFile(config, []byte("[plugins.\"alpha@local-m\"]\nenabled = \"oops\n"), 0o644)
	})

	// When 卸载 alpha
	err := l.UninstallPlugin(codexSkill(l, "alpha-a"))

	// Then 不算卸载成功，报出读配置的错误
	if err == nil {
		t.Fatal("config.toml 读不懂时不应算卸载成功")
	}
	if _, readErr := codexPluginInstalled(filepath.Join(home, ".codex"), "alpha@local-m"); readErr == nil || err.Error() != readErr.Error() {
		t.Errorf("应报出读配置的错误：%v", err)
	}
}

func TestCodexPluginInstalledChecksConfigAndRemoteMarker(t *testing.T) {
	home := filepath.Join(codexHome(t), ".codex")
	for _, c := range []struct {
		id   string
		want bool
	}{
		{"alpha@local-m", true},  // config.toml 里有
		{"gamma@remote-m", true}, // 缓存里有远程安装标记
		{"canva@remote-m", false},
		{"gamma", false}, // 不带市场的 id 不去拼空的市场目录
	} {
		if got, err := codexPluginInstalled(home, c.id); err != nil || got != c.want {
			t.Errorf("%s 登记着 = %v（%v），应为 %v", c.id, got, err, c.want)
		}
	}

	// Given 没有 config.toml：只看缓存
	must(t, os.Remove(filepath.Join(home, "config.toml")))
	if got, err := codexPluginInstalled(home, "alpha@local-m"); err != nil || got {
		t.Errorf("没有 config.toml 时 alpha 不算登记着：%v %v", got, err)
	}

	// Given config.toml 读不了（是个目录）
	must(t, os.Mkdir(filepath.Join(home, "config.toml"), 0o755))
	if _, err := codexPluginInstalled(home, "alpha@local-m"); err == nil {
		t.Error("config.toml 读不了时应返回错误")
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

// codexTargets 是 npm 包里各平台原生程序所在的 vendor 目录，按 GOOS/GOARCH。
var codexTargets = map[string]string{
	"darwin/arm64": "aarch64-apple-darwin",
	"darwin/amd64": "x86_64-apple-darwin",
	"linux/arm64":  "aarch64-unknown-linux-musl",
	"linux/amd64":  "x86_64-unknown-linux-musl",
}

// npmCodexPackage 造一个 npm 包 @openai/codex，vendor 下带 targets 这些平台的原生程序。
func npmCodexPackage(t *testing.T, root string, targets ...string) string {
	t.Helper()
	pkg := filepath.Join(root, "lib/node_modules/@openai/codex")
	for _, target := range targets {
		native := filepath.Join(pkg, "vendor", target, "codex/codex")
		must(t, os.MkdirAll(filepath.Dir(native), 0o755))
		must(t, os.WriteFile(native, []byte("bin"), 0o755))
	}
	return pkg
}

func TestNpmCodexNativePicksTheBinaryForThisPlatform(t *testing.T) {
	// Given 包里带了 Mac 两种架构和 Linux 的原生程序
	pkg := npmCodexPackage(t, t.TempDir(), slices.Collect(maps.Values(codexTargets))...)
	for platform, target := range codexTargets {
		goos, goarch, _ := strings.Cut(platform, "/")
		// Then 选这个系统和架构的那个
		if got, want := npmCodexNativeFor(pkg, goos, goarch), filepath.Join(pkg, "vendor", target, "codex/codex"); got != want {
			t.Errorf("%s 选了 %q，应为 %q", platform, got, want)
		}
	}
}

func TestNpmCodexNativeSkipsBinariesForOtherPlatforms(t *testing.T) {
	// Given 包里只有 Apple 芯片的原生程序
	root := t.TempDir()
	pkg := npmCodexPackage(t, root, "aarch64-apple-darwin")

	// Then Intel Mac 和 Linux 上不选它
	for _, platform := range [][2]string{{"darwin", "amd64"}, {"linux", "arm64"}} {
		if got := npmCodexNativeFor(pkg, platform[0], platform[1]); got != "" {
			t.Errorf("%s/%s 不应选别的平台的程序：%q", platform[0], platform[1], got)
		}
	}

	// Given 再放一个目录名不写平台的
	npmCodexPackage(t, root, "universal")
	// Then 没有对得上的时退而用它
	if got, want := npmCodexNativeFor(pkg, "darwin", "amd64"), filepath.Join(pkg, "vendor/universal/codex/codex"); got != want {
		t.Errorf("没有对得上的平台时应用不写平台的：%q，应为 %q", got, want)
	}
}

func TestCodexCandidatesTryTheNewestNvmNodeFirst(t *testing.T) {
	home := t.TempDir()
	t.Setenv("PATH", "")
	for _, v := range []string{"v9.11.2", "v22.1.0", "v18.20.4", "v22.10.0"} {
		bin := filepath.Join(home, ".nvm/versions/node", v, "bin/codex")
		must(t, os.MkdirAll(filepath.Dir(bin), 0o755))
		must(t, os.WriteFile(bin, nil, 0o755))
	}
	var got []string
	for _, c := range codexCandidates(home) {
		if strings.Contains(c, ".nvm") {
			got = append(got, nodeVersionOf(c))
		}
	}
	if want := []string{"v22.10.0", "v22.1.0", "v18.20.4", "v9.11.2"}; !slices.Equal(got, want) {
		t.Errorf("nvm 里的 codex 按 %v 的顺序试，应为 %v", got, want)
	}
}

func TestFindCodexUsesTheNativeBinaryOfAnNpmInstall(t *testing.T) {
	target, ok := codexTargets[runtime.GOOS+"/"+runtime.GOARCH]
	if !ok {
		t.Skipf("Codex 的 npm 包不带 %s/%s 的原生程序", runtime.GOOS, runtime.GOARCH)
	}
	// Given npm 装的 codex：bin/codex 链到包里的 codex.js，原生程序在平台包里，
	// 平台包里还有别的架构的
	root, _ := filepath.EvalSymlinks(t.TempDir())
	pkg := filepath.Join(root, "lib/node_modules/@openai/codex")
	var native string
	for _, tg := range slices.Sorted(maps.Values(codexTargets)) {
		f := filepath.Join(pkg, "node_modules/@openai/codex-multi/vendor", tg, "bin/codex")
		must(t, os.MkdirAll(filepath.Dir(f), 0o755))
		must(t, os.WriteFile(f, []byte("bin"), 0o755))
		if tg == target {
			native = f
		}
	}
	must(t, os.MkdirAll(filepath.Join(pkg, "bin"), 0o755))
	must(t, os.WriteFile(filepath.Join(pkg, "bin/codex.js"), []byte("#!/usr/bin/env node\n"), 0o755))
	must(t, os.MkdirAll(filepath.Join(root, "bin"), 0o755))
	must(t, os.Symlink(filepath.Join(pkg, "bin/codex.js"), filepath.Join(root, "bin/codex")))
	old := codexCandidates
	codexCandidates = func(string) []string { return []string{filepath.Join(root, "bin/codex")} }
	t.Cleanup(func() { codexCandidates = old })

	// Then 用这台机器能跑的原生程序，不用要 node 才能跑的脚本
	if got := findCodexBinary(root); got != native {
		t.Errorf("找到 %q，应为 %q", got, native)
	}
}

func TestDisablingACodexBuiltinSkillWritesCodexConfig(t *testing.T) {
	// Given Codex 的系统技能 imagegen
	home := codexHome(t)
	config := filepath.Join(home, ".codex/config.toml")
	before, _ := os.ReadFile(config)
	l := Scan(home)
	img := codexSkill(l, "imagegen")
	if !img.CanToggleBuiltin() || !img.State(codex).Enabled {
		t.Fatalf("imagegen 应能停用、现在启用着：%+v", img.State(codex))
	}

	// When 停用它
	must(t, l.Enable(img, codex, false))

	// Then config.toml 里按路径记下停用，重新扫描读出已停用
	after, _ := os.ReadFile(config)
	if !strings.Contains(string(after), "enabled = false") || !strings.Contains(string(after), filepath.Join(img.Real, "SKILL.md")) {
		t.Errorf("config.toml 应按路径停用 imagegen：\n%s", after)
	}
	if img = codexSkill(Scan(home), "imagegen"); img.State(codex).Enabled || !img.AllDisabled() {
		t.Errorf("重新扫描后 imagegen 应是停用的：%+v", img.State(codex))
	}

	// When 再启用它
	l = Scan(home)
	must(t, l.Enable(codexSkill(l, "imagegen"), codex, true))

	// Then config.toml 回到原样
	if restored, _ := os.ReadFile(config); string(restored) != string(before) {
		t.Errorf("启用后 config.toml 应回到原样：\n%s", restored)
	}
}

func TestOtherBuiltinSkillsStayReadOnly(t *testing.T) {
	// Given WorkBuddy 预装的插件技能
	home := pluginHome(t, "user", "")
	l := Scan(home)
	docx := byName(l, "tencent-docx")

	// Then 不能在这里停用
	if docx.CanToggleBuiltin() || l.Enable(docx, "workbuddy", false) == nil {
		t.Error("只有 Codex 的内置技能能在这里停用")
	}
}

func pluginIDs(plugins []codexPlugin) []string {
	var out []string
	for _, p := range plugins {
		out = append(out, p.id)
	}
	return out
}

func TestParseCodexPluginsReadsQuotedKeys(t *testing.T) {
	plugins, markets, err := parseCodexPlugins(codexConfig("/m") + "\n[plugins]\nfoo = 1\n[plugins.\"x@y\".nested]\na = 1\n")
	if err != nil || !slices.Equal(pluginIDs(plugins), []string{"alpha@local-m", "beta@local-m"}) || markets["local-m"] != "/m" {
		t.Errorf("插件 %v，市场 %v，错误 %v", plugins, markets, err)
	}
}

func TestParseCodexPluginsReportsUnreadableConfig(t *testing.T) {
	if _, _, err := parseCodexPlugins("[plugins.\"alpha@local-m\"]\nenabled = \"oops\n"); err == nil {
		t.Error("读不懂的 config.toml 应返回错误")
	}
}

func TestTomlKeyPathHandlesEscapedQuotes(t *testing.T) {
	for key, want := range map[string][]string{
		`plugins."a\"b@m"`:        {"plugins", `a"b@m`},
		`plugins."a\\"`:           {"plugins", `a\`},
		`plugins.'a\b@m'`:         {"plugins", `a\b@m`},
		`plugins . "x.y@m" . sub`: {"plugins", "x.y@m", "sub"},
	} {
		if got := tomlKeyPath(key); !slices.Equal(got, want) {
			t.Errorf("tomlKeyPath(%s) = %q，应为 %q", key, got, want)
		}
	}
	if got := tomlKeyPath(`plugins."a\"`); got != nil {
		t.Errorf("没闭合的引号应读不出：%q", got)
	}
}

func TestDisabledCodexPluginsAreNotScanned(t *testing.T) {
	// Given 用户在 config.toml 里停用了插件 alpha
	home := codexHome(t)
	config := filepath.Join(home, ".codex/config.toml")
	b, _ := os.ReadFile(config)
	must(t, os.WriteFile(config, []byte(strings.Replace(string(b), "[plugins.\"alpha@local-m\"]\nenabled = true", "[plugins.\"alpha@local-m\"]\nenabled = false # 停用", 1)), 0o644))

	// Then 它的技能不算装着、启用着，其他插件照旧
	l := Scan(home)
	if codexSkill(l, "alpha-a") != nil || codexSkill(l, "alpha-b") != nil {
		t.Error("停用的插件的技能不应扫进来")
	}
	if codexSkill(l, "beta-a") == nil {
		t.Error("没停用的插件照旧扫进来")
	}

	// And 卸载后的核对仍认为它登记着：表还在 config.toml 里
	if still, err := codexPluginInstalled(filepath.Join(home, ".codex"), "alpha@local-m"); err != nil || !still {
		t.Errorf("停用的插件仍算登记着：still=%v err=%v", still, err)
	}
}

func TestCodexPluginInConfigAndRemoteMarkerKeepsItsRemotePolicy(t *testing.T) {
	// Given 远程目录默认装上的 delta 同时登记在 config.toml 里
	home := codexHome(t)
	config := filepath.Join(home, ".codex/config.toml")
	b, _ := os.ReadFile(config)
	must(t, os.WriteFile(config, append(b, "\n[plugins.\"delta@remote-m\"]\nenabled = true\n"...), 0o644))

	// Then 按远程目录的安装策略算内置，不因 config.toml 里那条当成用户装的
	l := Scan(home)
	delta := codexSkill(l, "delta-a")
	if delta == nil || delta.Source != Builtin {
		t.Fatalf("delta-a 应是内置的：%+v", delta)
	}
	if l.CanUninstallPlugin(delta) {
		t.Error("默认装上的插件不应能卸载")
	}
}

func TestCodexCatalogPoliciesFollowTheLatestFetchTime(t *testing.T) {
	home := t.TempDir()
	dir := filepath.Join(home, codexCatalogDir)
	must(t, os.MkdirAll(dir, 0o755))
	for name, c := range map[string]string{
		// 字符串比 .5Z 排在 Z 前面、+08:00 排在 Z 后面，按时间却相反
		"a.json": `{"fetched_at":"2026-05-01T00:00:00.5Z","plugins":[{"id":"r1","installation_policy":"NEWEST"}]}`,
		"b.json": `{"fetched_at":"2026-05-01T00:00:00Z","plugins":[{"id":"r1","installation_policy":"OLDER"}]}`,
		"c.json": `{"fetched_at":"2026-05-01T07:00:00+08:00","plugins":[{"id":"r2","installation_policy":"OLD_ZONE"}]}`,
		"d.json": `{"fetched_at":"2026-04-30T23:30:00Z","plugins":[{"id":"r2","installation_policy":"NEW_ZONE"}]}`,
		"e.json": `{"fetched_at":"","plugins":[{"id":"r1","installation_policy":"NO_TIME"},{"id":"r3","installation_policy":"ONLY"}]}`,
		"f.json": `{"fetched_at":"昨天","plugins":[{"id":"r1","installation_policy":"BAD_TIME"}]}`,
	} {
		must(t, os.WriteFile(filepath.Join(dir, name), []byte(c), 0o644))
	}
	got := codexCatalogPolicies(home)
	for id, want := range map[string]string{"r1": "NEWEST", "r2": "NEW_ZONE", "r3": "ONLY"} {
		if got[id] != want {
			t.Errorf("%s 的策略 %q，应为 %q", id, got[id], want)
		}
	}
}

func TestRescansWhenCodexInstallsARemotePlugin(t *testing.T) {
	// Given Codex 从远程目录装插件时只在缓存的市场目录下新建目录
	home := codexHome(t)
	plan := Scan(home).WatchPlan()

	// Then 监听整个缓存目录：已有的市场和第一次从新市场装时新建的市场都在里面
	cache := filepath.Join(home, ".codex", codexPluginCache)
	if !slices.Contains(plan.Dirs, cache) {
		t.Errorf("应监听 Codex 插件缓存目录：%v", plan.Dirs)
	}
	for _, m := range []string{"local-m", "remote-m", "new-market"} {
		if !pathUnderAny(filepath.Join(cache, m, "p"), plan.Dirs) {
			t.Errorf("Codex 插件缓存的 %s 应在监听范围里：%v", m, plan.Dirs)
		}
	}
}

func TestWaitsForCodexPluginCacheToAppear(t *testing.T) {
	// Given Codex 还没装过插件，缓存目录不存在
	home, _ := filepath.EvalSymlinks(t.TempDir())
	must(t, os.MkdirAll(filepath.Join(home, ".codex"), 0o755))
	must(t, os.WriteFile(filepath.Join(home, ".codex/config.toml"), []byte("model = \"gpt-5\"\n"), 0o644))
	plan := Scan(home).WatchPlan()

	// Then 等 plugins 目录出现，出现后重扫再改成监听缓存目录
	want := WatchCreate{Dir: filepath.Join(home, ".codex"), Names: []string{"plugins"}}
	if !slices.ContainsFunc(plan.Creates, func(c WatchCreate) bool { return c.Dir == want.Dir && slices.Contains(c.Names, "plugins") }) {
		t.Errorf("应在 %s 等 plugins 出现：%+v", want.Dir, plan.Creates)
	}
}
