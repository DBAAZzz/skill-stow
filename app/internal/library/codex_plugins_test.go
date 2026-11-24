package library

import (
	"os"
	"path/filepath"
	"slices"
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
