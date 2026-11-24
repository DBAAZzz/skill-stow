package library

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"
)

const codex = "codex"

// Codex 的插件不写 installed_plugins.json：
//   - 本地市场（Codex 自带的 openai-bundled 等）装的，登记在 config.toml 的
//     [plugins."名称@市场"]，市场写在 [marketplaces.市场]，清单在市场目录的
//     .agents/plugins/marketplace.json；
//   - 远程目录装的，只在缓存目录里留一个 .codex-remote-plugin-install.json。
//
// 文件都在 ~/.codex/plugins/cache/市场/名称/版本/ 下。
// 是否算内置按 Codex 自己标的安装策略：INSTALLED_BY_DEFAULT 是默认装上的，
// 卸了会再装回来；其余是用户装的。
const (
	codexPluginCache    = "plugins/cache"
	codexRemoteMarker   = ".codex-remote-plugin-install.json"
	codexCatalogDir     = "cache/remote_plugin_catalog"
	codexDefaultInstall = "INSTALLED_BY_DEFAULT"
)

// codexPlugin 是 Codex 装了的一个插件。
type codexPlugin struct {
	id       string // 名称@市场
	remoteID string // 远程目录里的 ID；本地市场的为空
	disabled bool   // config.toml 里写了 enabled = false
}

// codexPluginSkills 读 Codex 装了的插件里的技能。config.toml 和远程安装标记
// 里都有的插件按 id 合成一个，留着远程 ID 好查它的安装策略；停用的插件不算。
func (sc *scanner) codexPluginSkills(a Agent, seen map[string]bool) []*Skill {
	home := filepath.Join(sc.home, a.Home)
	src, _ := os.ReadFile(filepath.Join(home, "config.toml"))
	plugins, markets, _ := parseCodexPlugins(string(src))
	for _, r := range codexRemotePlugins(home) {
		if i := slices.IndexFunc(plugins, func(p codexPlugin) bool { return p.id == r.id }); i >= 0 {
			plugins[i].remoteID = r.remoteID
			continue
		}
		plugins = append(plugins, r)
	}
	catalog := codexCatalogPolicies(home)
	manifests := map[string]map[string]string{}
	var out []*Skill
	for _, p := range plugins {
		name, market, ok := strings.Cut(p.id, "@")
		if !ok || p.disabled {
			continue
		}
		dir := latestVersionDir(filepath.Join(home, codexPluginCache, market, name))
		if dir == "" {
			continue
		}
		policy := catalog[p.remoteID]
		if p.remoteID == "" {
			if _, read := manifests[market]; !read {
				manifests[market] = marketplacePolicies(markets[market])
			}
			policy = manifests[market][name]
		} else if policy == "" {
			// 远程目录的缓存里查不到的，是默认装上、不在可选列表里的插件
			policy = codexDefaultInstall
		}
		src := ViaPlugin
		if policy == codexDefaultInstall {
			src = Builtin
		}
		out = append(out, sc.installedPluginSkills(a, dir, src, &pluginInstall{id: p.id}, seen)...)
	}
	return out
}

// parseCodexPlugins 从 config.toml 读出登记的插件（名称@市场，按出现顺序，
// 带是否停用）和本地市场的目录。读不懂的配置返回错误。
func parseCodexPlugins(src string) (plugins []codexPlugin, markets map[string]string, err error) {
	markets = map[string]string{}
	statements, err := tomlStatements(src)
	if err != nil {
		return nil, markets, err
	}
	var section []string
	current := -1 // section 是 [plugins."名称@市场"] 时它在 plugins 里的下标
	for _, s := range statements {
		if strings.HasPrefix(s, "[") {
			section, current = tomlKeyPath(strings.Trim(s, "[]")), -1
			if len(section) == 2 && section[0] == "plugins" {
				current = slices.IndexFunc(plugins, func(p codexPlugin) bool { return p.id == section[1] })
				if current < 0 {
					plugins = append(plugins, codexPlugin{id: section[1]})
					current = len(plugins) - 1
				}
			}
			continue
		}
		key, value, _ := strings.Cut(s, "=")
		key, value = strings.TrimSpace(key), strings.TrimSpace(value)
		switch {
		case current >= 0 && key == "enabled":
			plugins[current].disabled = value == "false"
		case len(section) == 2 && section[0] == "marketplaces" && key == "source":
			if dir, ok := tomlString(value); ok {
				markets[section[1]] = dir
			}
		}
	}
	return plugins, markets, nil
}

// tomlKeyPath 把点分的键（如 plugins."a@b"）拆成各段，去掉引号。
func tomlKeyPath(key string) []string {
	var out []string
	for rest := strings.TrimSpace(key); rest != ""; {
		var part string
		switch rest[0] {
		case '"', '\'':
			end := tomlQuoteEnd(rest)
			if end < 0 {
				return nil
			}
			part, _ = tomlString(rest[:end+1])
			rest = rest[end+1:]
		default:
			end := strings.IndexByte(rest, '.')
			if end < 0 {
				end = len(rest)
			}
			part, rest = strings.TrimSpace(rest[:end]), rest[end:]
		}
		out = append(out, part)
		rest = strings.TrimPrefix(strings.TrimSpace(rest), ".")
		rest = strings.TrimSpace(rest)
	}
	return out
}

// tomlQuoteEnd 是 s 开头那个带引号的键的闭合引号的下标，找不到返回 -1。
// 基本字符串（双引号）里反斜杠转义下一个字符；字面字符串（单引号）没有转义。
func tomlQuoteEnd(s string) int {
	for i := 1; i < len(s); i++ {
		switch {
		case s[0] == '"' && s[i] == '\\':
			i++
		case s[i] == s[0]:
			return i
		}
	}
	return -1
}

// tomlString 读一个 TOML 字符串值："基本字符串" 或 '字面字符串'。
func tomlString(v string) (string, bool) {
	if len(v) >= 2 && v[0] == '\'' && v[len(v)-1] == '\'' {
		return v[1 : len(v)-1], true
	}
	s, err := strconv.Unquote(v)
	return s, err == nil
}

// codexRemotePlugins 是从远程目录装的插件：缓存目录里有安装标记的。
func codexRemotePlugins(home string) []codexPlugin {
	markers, _ := filepath.Glob(filepath.Join(home, codexPluginCache, "*", "*", codexRemoteMarker))
	slices.Sort(markers)
	var out []codexPlugin
	for _, m := range markers {
		b, err := os.ReadFile(m)
		if err != nil {
			continue
		}
		var mark struct {
			ID string `json:"remote_plugin_id"`
		}
		if json.Unmarshal(b, &mark) != nil || mark.ID == "" {
			continue
		}
		dir := filepath.Dir(m)
		out = append(out, codexPlugin{id: filepath.Base(dir) + "@" + filepath.Base(filepath.Dir(dir)), remoteID: mark.ID})
	}
	return out
}

// codexCatalogPolicies 是远程目录里每个插件（按远程 ID）的安装策略。缓存有好几份，
// 同一个插件以最近拉取的那份为准。
func codexCatalogPolicies(home string) map[string]string {
	files, _ := filepath.Glob(filepath.Join(home, codexCatalogDir, "*.json"))
	type catalog struct {
		FetchedAt string `json:"fetched_at"`
		Plugins   []struct {
			ID     string `json:"id"`
			Policy string `json:"installation_policy"`
		} `json:"plugins"`
	}
	var cats []catalog
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		var c catalog
		if json.Unmarshal(b, &c) == nil {
			cats = append(cats, c)
		}
	}
	// 按拉取时间排，时间读不出的最旧；读不出的之间按原文排，结果才稳定
	slices.SortStableFunc(cats, func(a, b catalog) int {
		ta, errA := time.Parse(time.RFC3339Nano, a.FetchedAt)
		tb, errB := time.Parse(time.RFC3339Nano, b.FetchedAt)
		switch {
		case errA != nil && errB != nil:
			return strings.Compare(a.FetchedAt, b.FetchedAt)
		case errA != nil:
			return -1
		case errB != nil:
			return 1
		}
		return ta.Compare(tb)
	})
	out := map[string]string{}
	for _, c := range cats {
		for _, p := range c.Plugins {
			out[p.ID] = p.Policy
		}
	}
	return out
}

// marketplacePolicies 是本地市场清单里每个插件的安装策略。
func marketplacePolicies(dir string) map[string]string {
	if dir == "" {
		return nil
	}
	b, err := os.ReadFile(filepath.Join(dir, ".agents/plugins/marketplace.json"))
	if err != nil {
		return nil
	}
	var m struct {
		Plugins []struct {
			Name   string `json:"name"`
			Policy struct {
				Installation string `json:"installation"`
			} `json:"policy"`
		} `json:"plugins"`
	}
	if json.Unmarshal(b, &m) != nil {
		return nil
	}
	out := map[string]string{}
	for _, p := range m.Plugins {
		out[p.Name] = p.Policy.Installation
	}
	return out
}

// latestVersionDir 是插件缓存目录下最近改过的版本目录；点开头的是下载中的临时目录，不算。
func latestVersionDir(dir string) string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return ""
	}
	best, bestTime := "", int64(0)
	for _, e := range entries {
		if !e.IsDir() || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		if t := info.ModTime().UnixNano(); best == "" || t > bestTime {
			best, bestTime = filepath.Join(dir, e.Name()), t
		}
	}
	return best
}
