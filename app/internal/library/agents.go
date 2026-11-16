package library

import (
	"os"
	"path/filepath"
	"slices"
	"strings"

	"skillmanager/locales"
)

// Agent 是会读取技能的 AI 工具。目录都相对用户主目录（项目目录相对项目根）。
//
// 一个目录可能被好几个 Agent 读：~/.agents/skills 是通用目录，Codex、
// Cursor、Gemini CLI、GitHub Copilot、OpenCode 都读；Cursor 和 OpenCode
// 还读 ~/.claude/skills。所以「哪些 Agent 会读到一个技能」要看它在哪些
// 目录里，而不是只看 Agent 自己的目录。
type Agent struct {
	ID, Name string
	// Local 是名字在界面语言里的说法，没有就用 Name（只有中文产品名要换）
	Local    func() string
	Home     string   // 装了这个 Agent 才有的目录
	AltHomes []string // 同一产品在别的平台或形态下的主目录（如 IDE 与 CLI 各一套）
	Skills   string   // 自己的全局技能目录：启用就是在这里建链接
	Reads    []string // 还会读的全局目录
	Project  []string // 在项目里工作时会读的目录
	Plugins  string   // 插件目录，有 installed_plugins.json 时从中读内置技能
	Builtin  []string // Agent 自带的内置技能目录：每个子目录是一个技能
	Marks    []string // 装了才有的文件；为空时看 Home 里除 skills 以外有没有别的
}

// 通用目录：遵循 Agent Skills 规范的工具大多会读它。
const sharedDir = ".agents/skills"

// SharedDir 是通用目录，相对主目录或项目根：好几个 Agent 都直接读它。
const SharedDir = sharedDir

// Catalog 是认识的 Agent，按界面顺序（常用的在前）。目录来自各家文档和
// vercel-labs/skills 的 docs/agents.md；Reads 只写查证过的。
var Catalog = []Agent{
	{ID: "claude-code", Name: "Claude Code", Home: ".claude", Skills: ".claude/skills",
		Project: []string{".claude/skills"}, Plugins: ".claude/plugins"},
	{ID: "codex", Name: "Codex", Home: ".codex", Skills: ".codex/skills",
		Reads: []string{sharedDir}, Project: []string{sharedDir},
		Builtin: []string{".codex/skills/.system"}}, // 扫「我的」时跳过点开头的目录，在这里读
	{ID: "cursor", Name: "Cursor", Home: ".cursor", Skills: ".cursor/skills",
		Reads:   []string{sharedDir, ".claude/skills", ".codex/skills"},
		Project: []string{".cursor/skills", sharedDir, ".claude/skills", ".codex/skills"}},
	{ID: "gemini", Name: "Gemini CLI", Home: ".gemini", Skills: ".gemini/skills",
		Reads: []string{sharedDir}, Project: []string{".gemini/skills", sharedDir},
		Marks: []string{".gemini/settings.json", ".gemini/oauth_creds.json", ".gemini/tmp"}},
	{ID: "github-copilot", Name: "GitHub Copilot", Home: ".copilot", Skills: ".copilot/skills",
		Reads: []string{sharedDir}, Project: []string{".github/skills", ".claude/skills", sharedDir}},
	{ID: "opencode", Name: "OpenCode", Home: ".config/opencode", Skills: ".config/opencode/skills",
		Reads: []string{".claude/skills", sharedDir}, Project: []string{".opencode/skills", ".claude/skills", sharedDir}},
	{ID: "windsurf", Name: "Windsurf", Home: ".codeium/windsurf", Skills: ".codeium/windsurf/skills",
		Project: []string{".windsurf/skills"}},
	{ID: "antigravity", Name: "Antigravity", Home: ".gemini/antigravity",
		AltHomes: []string{".gemini/antigravity-cli"},
		Skills:   ".gemini/config/skills",
		Builtin:  []string{".gemini/antigravity/builtin/skills", ".gemini/antigravity-cli/builtin/skills"},
		Project:  []string{sharedDir}},
	{ID: "cline", Name: "Cline", Home: ".cline", Skills: sharedDir, Project: []string{sharedDir}},
	{ID: "amp", Name: "Amp", Home: ".config/amp", Skills: ".config/agents/skills", Project: []string{sharedDir}},
	{ID: "kiro", Name: "Kiro", Home: ".kiro", Skills: ".kiro/skills", Project: []string{".kiro/skills"}},
	{ID: "trae", Name: "Trae", Home: ".trae", Skills: ".trae/skills", Project: []string{".trae/skills"}},
	{ID: "trae-cn", Name: "Trae CN", Home: ".trae-cn", Skills: ".trae-cn/skills", Project: []string{".trae/skills"}},
	{ID: "qwen", Name: "Qwen Code", Home: ".qwen", Skills: ".qwen/skills", Project: []string{".qwen/skills"}},
	{ID: "qoder", Name: "Qoder", Home: ".qoder", Skills: ".qoder/skills", Project: []string{".qoder/skills"}},
	{ID: "codebuddy", Name: "CodeBuddy", Home: ".codebuddy", Skills: ".codebuddy/skills",
		Project: []string{".codebuddy/skills"}, Plugins: ".codebuddy/plugins"},
	{ID: "workbuddy", Name: "WorkBuddy", Home: ".workbuddy", Skills: ".workbuddy/skills",
		Project: []string{".workbuddy/skills"}, Plugins: ".workbuddy/plugins"},
	// ZCode 的用户和项目层都同时读 .zcode/skills 与 .agents/skills（apps/zcode-cli 的 skills 适配器）
	{ID: "zcode", Name: "ZCode", Home: ".zcode", Skills: ".zcode/skills",
		Reads: []string{sharedDir}, Project: []string{".zcode/skills", sharedDir}},
	{ID: "droid", Name: "Droid", Home: ".factory", Skills: ".factory/skills", Project: []string{".factory/skills", sharedDir}},
	{ID: "roo", Name: "Roo Code", Home: ".roo", Skills: ".roo/skills", Project: []string{".roo/skills"}},
	{ID: "kilo", Name: "Kilo Code", Home: ".kilo", Skills: ".kilo/skills", Project: []string{sharedDir}},
	{ID: "goose", Name: "Goose", Home: ".config/goose", Skills: ".config/goose/skills", Project: []string{".goose/skills"}},
	{ID: "pi", Name: "Pi", Home: ".pi", Skills: ".pi/agent/skills", Reads: []string{sharedDir}, Project: []string{".pi/skills", sharedDir}},
	{ID: "kimi", Name: "Kimi Code", Home: ".kimi", Skills: sharedDir, Project: []string{sharedDir}},
	{ID: "qoder-cn", Name: "Qoder CN", Home: ".qoder-cn", Skills: ".qoder-cn/skills", Project: []string{".qoder/skills"}},
	{ID: "iflow", Name: "iFlow CLI", Home: ".iflow", Skills: ".iflow/skills", Project: []string{".iflow/skills"}},
	{ID: "crush", Name: "Crush", Home: ".config/crush", Skills: ".config/crush/skills", Project: []string{".crush/skills"}},
	{ID: "augment", Name: "Augment", Home: ".augment", Skills: ".augment/skills", Project: []string{".augment/skills"}},
	{ID: "continue", Name: "Continue", Home: ".continue", Skills: ".continue/skills", Project: []string{".continue/skills"}},
	{ID: "openhands", Name: "OpenHands", Home: ".openhands", Skills: ".openhands/skills", Project: []string{".openhands/skills"}},
	{ID: "junie", Name: "Junie", Home: ".junie", Skills: ".junie/skills", Project: []string{".junie/skills"}},
	{ID: "lingma", Name: "Lingma", Local: locales.AgentLingma, Home: ".lingma", Skills: ".lingma/skills", Project: []string{".lingma/skills"}},
}

// DisplayName 是界面上的名字：通义灵码这类中文产品名按界面语言说，其余就是 Name。
func (a Agent) DisplayName() string {
	if a.Local != nil {
		return a.Local()
	}
	return a.Name
}

// AgentName 返回 Agent 的名称，未知时返回 id。
func AgentName(id string) string {
	if i := slices.IndexFunc(Catalog, func(a Agent) bool { return a.ID == id }); i >= 0 {
		return Catalog[i].DisplayName()
	}
	if IsCustom(id) {
		return customAgentName(strings.TrimPrefix(id, customPrefix))
	}
	return id
}

func (l *Library) agentByID(id string) (Agent, bool) {
	return agentByID(l.custom, id)
}

func agentByID(custom []Agent, id string) (Agent, bool) {
	if i := slices.IndexFunc(Catalog, func(a Agent) bool { return a.ID == id }); i >= 0 {
		return Catalog[i], true
	}
	if i := slices.IndexFunc(custom, func(a Agent) bool { return a.ID == id }); i >= 0 {
		return custom[i], true
	}
	return Agent{}, false
}

// homes 是这个 Agent 可能的全部主目录，自己的在前。
func (a Agent) homes() []string { return append([]string{a.Home}, a.AltHomes...) }

// hasHome 报告这些主目录里有没有一个存在。
func (a Agent) hasHome(home string) bool {
	for _, h := range a.homes() {
		if _, err := os.Stat(filepath.Join(home, h)); err == nil {
			return true
		}
	}
	return false
}

// installed 报告 home 下装了这个 Agent。只有一个 skills 目录不算：
// npx skills 会给没装的 Agent 也建好 skills 目录。
func (a Agent) installed(home string) bool {
	if len(a.Marks) > 0 {
		for _, m := range a.Marks {
			if _, err := os.Stat(filepath.Join(home, m)); err == nil {
				return true
			}
		}
		return false
	}
	for _, h := range a.homes() {
		items, err := os.ReadDir(filepath.Join(home, h))
		if err != nil {
			continue
		}
		for _, it := range items {
			if n := it.Name(); n != "skills" && n != ".DS_Store" {
				return true
			}
		}
	}
	return false
}

// dirs 是 Agent 会读的全部全局目录，自己的在前。
func (a Agent) dirs() []string { return append([]string{a.Skills}, a.Reads...) }

// ProjectDirAgent 是项目里一个技能目录的主人：目录以它命名的 Agent
// （.claude/skills 是 Claude Code）。通用目录没有主人，返回空。
func ProjectDirAgent(rel string) string {
	if rel == sharedDir {
		return ""
	}
	for _, a := range Catalog {
		if a.Project[0] == rel {
			return a.ID
		}
	}
	for _, a := range Catalog {
		if a.Skills == rel {
			return a.ID
		}
	}
	return ""
}

// ProjectDirName 是项目里一个技能目录的叫法：通用目录，或它的主人。
func ProjectDirName(rel string) string {
	if rel == sharedDir {
		return locales.AgentsSharedDirectory()
	}
	if id := ProjectDirAgent(rel); id != "" {
		return AgentName(id)
	}
	return rel
}
