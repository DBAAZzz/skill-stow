package main

import (
	"html/template"
	"slices"

	"skillmanager/internal/library"
)

// agentView 是 Agent 页面要用的数据，全部由 library.Catalog 推出来。
type agentView struct {
	ID, Name string
	Home     string
	Skills   string
	Global   []folderView // 会读的全局目录，自己的在前
	Project  []folderView // 在项目里会读的目录，相对项目根
	Builtin  []string
	Plugins  string
	// ReadsShared 报告它在全局读通用目录 ~/.agents/skills
	ReadsShared   bool
	ProjectShared bool
	Native        *nativeConfig // 能在应用里按技能启停时才有
	PathKeyed     bool          // 按 SKILL.md 的路径记停用，不会连带别处的同名技能
	BuiltinToggle bool          // 内置技能删了会回来，只能在配置里停用
	Usage         bool          // 应用能从它的会话记录里统计调用次数
	SharesWith    []*agentView  // 和它读同一个全局目录的其他 Agent
	Icon          template.HTML // 和应用里一样的方块图标
}

type folderView struct {
	Path    string
	Readers []*agentView // 读这个目录的其他 Agent（同为全局或同为项目里）
}

// nativeConfig 是按技能停用时要改的配置文件，以及停用 pdf-tools 时写进去的内容。
type nativeConfig struct {
	File, Syntax, Snippet string
}

// nativeConfigs 和 internal/library/native*.go 写的内容一致；测试核对它覆盖的
// Agent 正好是 library.NativeToggleSupported 的那些。
var nativeConfigs = map[string]*nativeConfig{
	"claude-code": {File: "~/.claude/settings.json", Syntax: "json", Snippet: `{
  "skillOverrides": {
    "pdf-tools": "off"
  }
}`},
	"codex": {File: "~/.codex/config.toml", Syntax: "toml", Snippet: `[[skills.config]]
path = "/Users/you/.agents/skills/pdf-tools/SKILL.md"
enabled = false`},
	"gemini": {File: "~/.gemini/settings.json", Syntax: "json", Snippet: `{
  "skills": {
    "disabled": ["pdf-tools"]
  }
}`},
	"opencode": {File: "~/.config/opencode/opencode.json", Syntax: "json", Snippet: `{
  "permission": {
    "skill": {
      "pdf-tools": "deny"
    }
  }
}`},
	"zcode": {File: "~/.zcode/cli/config.json", Syntax: "json", Snippet: `{
  "skills": {
    "/Users/you/.agents/skills/pdf-tools/SKILL.md": { "enable": false }
  }
}`},
}

// usageAgents 是应用能从会话记录统计 skill 调用次数的 Agent。
var usageAgents = []string{"claude-code", "codex"}

func agentViews(localNames map[string]string) []*agentView {
	var views []*agentView
	for _, a := range library.Catalog {
		v := &agentView{
			ID: a.ID, Name: a.Name, Home: a.Home, Skills: a.Skills,
			Builtin: a.Builtin, Plugins: a.Plugins,
			Native: nativeConfigs[a.ID], Usage: slices.Contains(usageAgents, a.ID),
			// 与 library 的 pathKeyed、codexBuiltin 一致
			PathKeyed: a.ID == "codex" || a.ID == "zcode", BuiltinToggle: a.ID == "codex" && len(a.Builtin) > 0,
		}
		if n := localNames[a.ID]; n != "" {
			v.Name = n
		}
		for _, dir := range globalDirs(a) {
			v.Global = append(v.Global, folderView{Path: dir})
		}
		for _, dir := range a.Project {
			v.Project = append(v.Project, folderView{Path: dir})
		}
		v.ReadsShared = slices.Contains(globalDirs(a), library.SharedDir)
		v.ProjectShared = slices.Contains(a.Project, library.SharedDir)
		views = append(views, v)
	}
	for i, a := range library.Catalog {
		v := views[i]
		for j := range v.Global {
			for k, other := range library.Catalog {
				if other.ID != a.ID && slices.Contains(globalDirs(other), v.Global[j].Path) {
					v.Global[j].Readers = append(v.Global[j].Readers, views[k])
					if !slices.Contains(v.SharesWith, views[k]) {
						v.SharesWith = append(v.SharesWith, views[k])
					}
				}
			}
		}
		for j := range v.Project {
			for k, other := range library.Catalog {
				if other.ID != a.ID && slices.Contains(other.Project, v.Project[j].Path) {
					v.Project[j].Readers = append(v.Project[j].Readers, views[k])
				}
			}
		}
	}
	return views
}

// globalDirs 是 Agent 在全局会读的全部技能目录，自己的在前，不重复。
func globalDirs(a library.Agent) []string {
	dirs := []string{a.Skills}
	for _, d := range a.Reads {
		if !slices.Contains(dirs, d) {
			dirs = append(dirs, d)
		}
	}
	return dirs
}

func nativeAgentsOf(agents []*agentView) []*agentView {
	var out []*agentView
	for _, a := range agents {
		if a.Native != nil {
			out = append(out, a)
		}
	}
	return out
}

// sharedReaders 是在全局读通用目录 ~/.agents/skills 的 Agent。
func sharedReaders(agents []*agentView) []*agentView {
	var out []*agentView
	for _, a := range agents {
		if a.ReadsShared {
			out = append(out, a)
		}
	}
	return out
}

// crossRead 是一个 Agent 在全局还会读的、别的 Agent 自己的目录（不含通用目录）。
type crossRead struct {
	Agent *agentView
	Dirs  []string
}

func crossReads(agents []*agentView) []crossRead {
	own := map[string]bool{}
	for _, a := range agents {
		own[a.Skills] = true
	}
	var out []crossRead
	for _, a := range agents {
		var dirs []string
		for _, f := range a.Global[1:] {
			if f.Path != library.SharedDir && own[f.Path] {
				dirs = append(dirs, f.Path)
			}
		}
		if len(dirs) > 0 {
			out = append(out, crossRead{Agent: a, Dirs: dirs})
		}
	}
	return out
}
