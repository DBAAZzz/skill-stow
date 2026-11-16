package library

import (
	"encoding/json"
	"errors"

	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"skillmanager/locales"
)

// Settings 是设置弹窗里的选项，存在 settings.json（和 projects.json 同一个目录）。
type Settings struct {
	UpdateOldVersion string   `json:"updateOldVersion,omitempty"`
	UpdateProxy      string   `json:"updateProxy,omitempty"`
	UpdateProxyMode  string   `json:"updateProxyMode,omitempty"`
	UseGHAuth        bool     `json:"useGhAuth,omitempty"`
	HiddenAgents     []string `json:"hiddenAgents,omitempty"`    // 关掉的 Agent：不出现在看板和详情栏里
	AgentDirs        []string `json:"agentDirs,omitempty"`       // 手动添加的技能目录（绝对路径），各算一个 Agent
	AutoDiscover     bool     `json:"autoDiscover"`              // 从 Agent 的使用记录里找项目
	MergeWorktrees   bool     `json:"mergeWorktrees"`            // git worktree 合并到主仓库显示
	Language         string   `json:"language,omitempty"`        // 空为跟随系统
	Theme            string   `json:"theme,omitempty"`           // 外观："light"、"dark"，空为跟随系统
	KeptLinks        []string `json:"keptLinks,omitempty"`       // 有意保留全局链接的项目技能（真实目录）
	SidebarHidden    bool     `json:"sidebarHidden,omitempty"`   // 主窗口的侧边栏收起了
	QuietTidy        bool     `json:"quietTidy,omitempty"`       // 完成整理时不放提示音
	ImportListWidth  float32  `json:"importListWidth,omitempty"` // 添加技能页左侧技能列表的宽度，0 为默认
	WelcomeVersion   int      `json:"welcomeVersion,omitempty"`  // 看过的欢迎面板版本，比当前的小就在启动时再显示
	// 最近在阅读视图里打开的技能，新的在前，⌘K 空输入时列出
	RecentSkills []RecentSkill `json:"recentSkills,omitempty"`
}

// RecentSkill 是打开过的一个技能和打开的时间。
type RecentSkill struct {
	ID string    `json:"id"`
	At time.Time `json:"at"`
}

// maxRecentSkills 是记住几个最近打开的技能。
const maxRecentSkills = 8

// NoteOpened 把技能记为最近打开的，排到最前，再写下设置。
func (l *Library) NoteOpened(s *Skill, at time.Time) {
	r := slices.DeleteFunc(slices.Clone(l.Settings.RecentSkills), func(r RecentSkill) bool { return r.ID == s.ID })
	r = append([]RecentSkill{{ID: s.ID, At: at}}, r...)
	l.Settings.RecentSkills = r[:min(len(r), maxRecentSkills)]
	l.SaveSettings()
}

// DefaultSettings 是没有 settings.json 时的设置。
func DefaultSettings() Settings { return Settings{AutoDiscover: true, MergeWorktrees: true} }

func settingsFile(home string) string { return filepath.Join(appDir(home), "settings.json") }

func loadSettings(home string) Settings {
	s := DefaultSettings()
	if b, err := os.ReadFile(settingsFile(home)); err == nil {
		json.Unmarshal(b, &s)
	}
	return s
}

// SaveSettings 写下 l.Settings。示例数据只改内存。
func (l *Library) SaveSettings() error {
	if !l.disk {
		return nil
	}
	return writeJSON(settingsFile(l.Home), l.Settings)
}

func writeJSON(file string, v any) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		return err
	}
	return os.WriteFile(file, b, 0o644)
}

// ——— Agent 管理 ———

// Visible 是在界面里显示的 Agent：装了的，去掉设置里关掉的。
func (l *Library) Visible() []Agent {
	return slices.DeleteFunc(slices.Clone(l.Agents), func(a Agent) bool { return l.Hidden(a.ID) })
}

// Hidden 报告 Agent 在设置里被关掉了。
func (l *Library) Hidden(id string) bool { return slices.Contains(l.Settings.HiddenAgents, id) }

// SetHidden 在界面里显示或隐藏一个 Agent，不动它的目录。
func (l *Library) SetHidden(id string, hidden bool) error {
	l.Settings.HiddenAgents = slices.DeleteFunc(l.Settings.HiddenAgents, func(h string) bool { return h == id })
	if hidden {
		l.Settings.HiddenAgents = append(l.Settings.HiddenAgents, id)
	}
	return l.SaveSettings()
}

// SkillCount 是 Agent 读得到的「我的」技能数。
func (l *Library) SkillCount(id string) int {
	n := 0
	for _, s := range l.Skills {
		if s.Source == Mine && s.EnabledIn(id) {
			n++
		}
	}
	return n
}

// 手动添加的技能目录各算一个 Agent，ID 是这个前缀加目录。
const customPrefix = "dir:"

// IsCustom 报告 Agent 是手动添加的技能目录。
func IsCustom(id string) bool { return strings.HasPrefix(id, customPrefix) }

func customAgentName(dir string) string {
	name := filepath.Base(dir)
	if name == "skills" {
		name = filepath.Base(filepath.Dir(dir))
	}
	return strings.TrimPrefix(name, ".")
}

// customAgent 把手动添加的目录当成一个 Agent：名称取目录名，
// 「~/.foo/skills」叫 foo。
func customAgent(home, dir string) Agent {
	rel, err := filepath.Rel(home, dir)
	if err != nil {
		rel = dir
	}
	return Agent{ID: customPrefix + dir, Name: customAgentName(dir), Home: rel, Skills: rel}
}

// AddAgentDir 记下一个手动添加的技能目录，重新扫描后它算一个 Agent。
func (l *Library) AddAgentDir(dir string) error {
	if !l.disk {
		return errors.New(locales.SettingsSampleSkillDirectoryUnavailable())
	}
	dir = filepath.Clean(dir)
	if dir == l.Root {
		return errors.New(locales.SettingsSharedDirectoryAlreadyAdded(l.Display(dir)))
	}
	for _, a := range l.Agents {
		if filepath.Join(l.Home, a.Skills) == dir {
			return errors.New(locales.SettingsAgentDirectoryAlreadyAdded(l.Display(dir), a.DisplayName()))
		}
	}
	if !slices.Contains(l.Settings.AgentDirs, dir) {
		l.Settings.AgentDirs = append(l.Settings.AgentDirs, dir)
	}
	return l.SaveSettings()
}

// RemoveAgentDir 去掉一个手动添加的技能目录，目录本身不动。
func (l *Library) RemoveAgentDir(id string) error {
	dir := strings.TrimPrefix(id, customPrefix)
	l.Settings.AgentDirs = slices.DeleteFunc(l.Settings.AgentDirs, func(d string) bool { return d == dir })
	return l.SaveSettings()
}

// ——— 检查链接 ———

// LinkReport 是「检查并修复」的结果。
type LinkReport struct {
	Checked int      // 各 Agent 目录里的链接
	Fixed   []string // 断掉、已重新连到技能库的
	Broken  []string // 断掉、技能库里也没有同名技能，连不上的
}

// CheckLinks 检查装了的 Agent 目录里的链接：断掉的，技能库里有同名技能
// 就改成指向它，没有的列出来。只改断掉的链接，不碰目录和好的链接。
func (l *Library) CheckLinks() (LinkReport, error) {
	var r LinkReport
	if !l.disk {
		return r, nil
	}
	var dirs []string
	for _, a := range l.Agents {
		for _, d := range a.dirs() {
			if abs := filepath.Join(l.Home, d); abs != l.Root && !slices.Contains(dirs, abs) {
				dirs = append(dirs, abs)
			}
		}
	}
	var errs []error
	for _, dir := range dirs {
		items, _ := os.ReadDir(dir)
		for _, it := range items {
			p := filepath.Join(dir, it.Name())
			if !isLink(p) {
				continue
			}
			r.Checked++
			if _, err := os.Stat(p); err == nil {
				continue
			}
			target := filepath.Join(l.Root, it.Name())
			if !isSkill(target) {
				r.Broken = append(r.Broken, p)
				continue
			}
			rel, err := filepath.Rel(dir, target)
			if err != nil {
				rel = target
			}
			if err := os.Remove(p); err != nil {
				errs = append(errs, err)
				continue
			}
			if err := makeLink(rel, p); err != nil {
				errs = append(errs, err)
				continue
			}
			r.Fixed = append(r.Fixed, p)
		}
	}
	return r, errors.Join(errs...)
}

// ——— 项目从哪来 ———

// Discovery 是从 Agent 的使用记录里找项目的结果（设置 · 项目）。
type Discovery struct {
	Sources    []DiscoverySource
	Total      int // 合并去重后的项目数
	WithSkills int // 其中有技能的
	Took       time.Duration
}

// DiscoverySource 是一处使用记录。
type DiscoverySource struct {
	Name  string
	Agent string // 图标用的 Agent ID；VS Code 系编辑器是 "vscode"
	Path  string // 记录在哪（显示用）
	Count int    // 从中找到的项目，-1 是没有找到使用记录
	Note  string // 补充说明，如找到了哪些编辑器
}

// ManualProjects 是手动添加的文件夹，按添加的先后，含没有技能的。
func (l *Library) ManualProjects() []*ProjectInfo { return l.manual }

// RemoveProjects 去掉手动添加的文件夹，文件夹本身不动。
func (l *Library) RemoveProjects(dirs []string) error {
	if !l.disk {
		return errors.New(locales.SettingsSampleProjectRemoveUnavailable())
	}
	remove := map[string]bool{}
	for _, d := range dirs {
		remove[canonicalProjectDir(d)] = true
	}
	// 旧记录可能还是规范化之前的写法，比较时两边都规范化
	list := slices.DeleteFunc(readManual(l.Home), func(m manualProject) bool { return remove[canonicalProjectDir(m.Dir)] })
	return writeManual(l.Home, list)
}

// ProjectSkillDirs 是在项目里会检查的技能目录（相对项目根）。
func ProjectSkillDirs() []string { return slices.Clone(projectSkillDirs) }

// gitMain 是 git worktree 所在的主仓库：worktree 里的 .git 是一个文件，
// 写着「gitdir: <主仓库>/.git/worktrees/<名称>」。不是 worktree 时返回空。
func gitMain(dir string) string {
	b, err := os.ReadFile(filepath.Join(dir, ".git"))
	if err != nil {
		return "" // 没有 .git，或 .git 是目录（主仓库本身）
	}
	gitdir, ok := strings.CutPrefix(strings.TrimSpace(string(b)), "gitdir:")
	if !ok {
		return ""
	}
	gitdir = strings.TrimSpace(gitdir)
	if !filepath.IsAbs(gitdir) {
		gitdir = filepath.Join(dir, gitdir)
	}
	sep := string(filepath.Separator)
	i := strings.LastIndex(gitdir, sep+".git"+sep+"worktrees"+sep)
	if i <= 0 {
		return "" // 子模块等
	}
	return filepath.Clean(gitdir[:i])
}
