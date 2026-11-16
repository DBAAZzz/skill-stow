// Package library 是技能库的数据层：技能、来源、Agent 与启用关系。
// 界面只通过这里读写。Scan 读本机磁盘，Sample 是开发和测试用的示例。
package library

import (
	"errors"

	"path/filepath"
	"slices"
	"strings"
	"time"

	"skillmanager/locales"
)

// Source 是技能的来源。
type Source int

const (
	Mine    Source = iota // 我的：用户自己的，可以启用 / 停用
	Builtin               // 内置：Agent 自带，用户没装过，只读
	Project               // 项目：项目目录里的，只读
	// ViaPlugin 是用户用 Agent 的插件命令装的（如 Claude Code 的 /plugin
	// install）：算在「我的」里，但由 Agent 管，只读，要卸载得整个插件卸载
	ViaPlugin
)

// Sources 是界面上来源的排列顺序。
var Sources = []Source{Mine, ViaPlugin, Builtin, Project}

func (s Source) Label() string {
	switch s {
	case ViaPlugin:
		return locales.LibraryPluginInstall()
	case Builtin:
		return locales.SourceBuiltin()
	case Project:
		return locales.SourceProject()
	}
	return locales.NavMine()
}

// File 是技能目录下的一个文件，Path 相对技能目录。
type File struct {
	Path    string
	Size    int
	Content string
	Binary  bool // 太大、不是文本或读不了，没有读内容
	Exec    bool // 有执行权限
	Link    bool // 是链接，Content 是「→ 目标」（只在对比副本时列出）
	// Unreadable 是读不了（只在对比副本时标出）：和任何一边都算不一样
	Unreadable bool
}

// TooLarge 报告文件是因为太大才没读内容。
func (f File) TooLarge() bool { return f.Binary && f.Size > maxFileSize }

// Read 是一个 Agent 从哪里读到技能。
type Read struct {
	Agent  string
	Dir    string // 读到它的技能目录：全局的是绝对路径，项目里的相对项目根
	Global bool   // 在任何项目里都读到；false 是只在这个项目里工作时
}

// place 是「我的」技能在一个全局目录里的那一项。
type place struct {
	dir    string // 所在的全局技能目录
	path   string // 这一项本身（目录或链接）
	real   string
	link   bool // path 是链接
	nested bool // 整组链接里的一个，没有自己的那一项
}

// Skill 是一个技能。「我的」技能按名称合成一个：各全局目录里同名的
// 链接或拷贝都算它，真实目录不同的算副本（Copies）。
type Skill struct {
	ID          string // 库内唯一：我的按名称，内置和项目按真实目录
	Name        string
	Description string
	Source      Source
	Provider    string   // 内置和插件装的技能由哪个 Agent 提供（Agent ID）
	Plugin      string   // 来自哪个插件：插件装的，和 Agent 预装的插件
	Project     string   // 项目技能所在的项目目录名
	Available   []string // 可以发现的 Agent，停用后保留
	States      map[string]AgentState
	targets     map[string]string
	nativeNames map[string]string
	Enabled     []string    // 我的：原生配置允许的 Agent；项目：全局链接它的
	Copies      int         // 电脑上另有几份同名副本
	SameName    bool        // 项目技能：「我的」里有同名的技能，Agent 可能读到的是另一份
	Shared      bool        // 我的技能：在技能库（通用目录）里，读它的 Agent 都读到
	Dir         string      // 技能目录（绝对路径；示例数据是 ~/ 开头）
	Real        string      // Dir 解析链接后的真实目录
	ProjectDir  string      // 项目技能所在项目的完整路径
	Reads       []Read      // 项目技能：谁会读到（项目里的目录 + 全局链接）
	Files       []File      // 第一个是 SKILL.md；真实数据在打开阅读视图时才读（LoadFiles）
	Collection  *Collection // 所在的技能集合，不在集合里为 nil
	Defects     []Defect    // 技能里说有、磁盘上没有的东西（integrity.go）

	// Links 是 Agent 自己的技能目录里的那一项；Via 是 Agent 没有自己的
	// 那一项、经别的目录（通用目录等）读到它时，那个目录。
	Links       map[string]string
	Via         map[string]string
	places      []place
	elsewhere   []place   // 没装的 Agent 的目录和早年的目录里的同名副本
	versions    []Version // 有副本时，按内容分成的几种，基准在前
	copied      []string  // 读到另一份副本的 Agent：不在 Enabled 里，开关拨不动
	redundant   []string  // 读通用目录、自己目录里又链到同一份的 Agent
	globalLinks []string  // 项目技能：全局目录里指向它的链接
	KeptLinks   bool      // 项目技能：全局链接是有意建的，不再提醒
}

// ReadOnly 报告技能是否只读：内置、插件装的与项目技能不能在这里启用或修改。
func (s *Skill) ReadOnly() bool { return s.Source != Mine }

// Origin 是一句话的来源说明，用在详情栏和阅读视图的标题下。
func (s *Skill) Origin() string {
	switch s.Source {
	case ViaPlugin:
		return locales.OriginPlugin(AgentName(s.Provider), s.Plugin)
	case Builtin:
		if s.Plugin != "" {
			return locales.OriginBuiltinPlugin(AgentName(s.Provider), s.Plugin)
		}
		return locales.OriginBuiltin(AgentName(s.Provider))
	case Project:
		return locales.OriginProject(s.Project)
	}
	return locales.LibraryMySkills()
}

func (s *Skill) EnabledIn(agent string) bool { return slices.Contains(s.Enabled, agent) }

// SetEnabled 启用或停用：只改启用关系，不动技能本身（示例数据用）。
func (s *Skill) SetEnabled(agent string, on bool) {
	if s.ReadOnly() {
		return
	}
	if s.Available == nil {
		s.Available = slices.Clone(s.Enabled)
	}
	if on && !slices.Contains(s.Available, agent) {
		s.Available = append(s.Available, agent)
	}
	st := s.State(agent)
	st.Discoverable = slices.Contains(s.Available, agent)
	st.Enabled = on
	st.Kind = StateEnabled
	st.Message = ""
	if !on {
		st.Kind = StateDisabled
	}
	if !nativeSupported(agent) {
		st.Kind = StateUnsupported
	}
	if s.States == nil {
		s.States = map[string]AgentState{}
	}
	s.States[agent] = st
	if on && !s.EnabledIn(agent) {
		s.Enabled = append(s.Enabled, agent)
	} else if !on {
		s.Enabled = slices.DeleteFunc(s.Enabled, func(id string) bool { return id == agent })
	}
	slices.SortFunc(s.Enabled, func(a, b string) int { return catalogIndex(nil, a) - catalogIndex(nil, b) })
}

func (l *Library) catalogIndex(id string) int {
	return catalogIndex(l.custom, id)
}

func catalogIndex(custom []Agent, id string) int {
	if i := slices.IndexFunc(Catalog, func(a Agent) bool { return a.ID == id }); i >= 0 {
		return i
	}
	if i := slices.IndexFunc(custom, func(a Agent) bool { return a.ID == id }); i >= 0 {
		return len(Catalog) + i
	}
	return len(Catalog) + len(custom)
}

// Readers 是会读到项目技能的 Agent，去重，项目里读到的在前。
func (s *Skill) Readers(global bool) []string {
	var out []string
	for _, r := range s.Reads {
		if r.Global == global && !slices.Contains(out, r.Agent) {
			out = append(out, r.Agent)
		}
	}
	return out
}

// markSameNames 标出和「我的」技能同名的项目技能。
func (l *Library) markSameNames() {
	mine := map[string]bool{}
	for _, s := range l.Skills {
		if s.Source == Mine {
			mine[s.Name] = true
		}
	}
	for _, s := range l.Skills {
		s.SameName = s.Source == Project && mine[s.Name]
	}
}

// Redundant 是读通用目录、自己的目录里又有一个链接指向同一份的 Agent：
// 那个链接是多余的，Tidy 删掉它。
func (s *Skill) Redundant() []string { return s.redundant }

// MissingDescription 报告技能没有简介：Agent 可能不会调用它。
func (s *Skill) MissingDescription() bool { return strings.TrimSpace(s.Description) == "" }

// Scope 是侧边栏选中的范围。
type Scope string

const (
	ScopeAll     Scope = "all"
	ScopeMine    Scope = "mine"
	ScopeBuiltin Scope = "builtin"
	ScopeProject Scope = "project"
)

func (sc Scope) Title() string {
	switch sc {
	case ScopeMine:
		return locales.LibraryMySkills()
	case ScopeBuiltin:
		return locales.LibraryBuiltinSkills()
	case ScopeProject:
		return locales.LibraryProjectSkills()
	}
	return locales.LibraryAllSkills()
}

func (sc Scope) match(s *Skill) bool {
	switch sc {
	case ScopeMine:
		return s.Source == Mine || s.Source == ViaPlugin
	case ScopeBuiltin:
		return s.Source == Builtin
	case ScopeProject:
		return s.Source == Project
	}
	return true
}

// ProjectInfo 是一个项目：Agent 用过的，或用户手动加的目录。
type ProjectInfo struct {
	Name, Dir string
	Used      time.Time    // 最近一次被 Agent 用到
	Manual    bool         // 用户手动加的
	Missing   bool         // 手动加的文件夹已经不在了
	Dirs      []ProjectDir // 有技能的技能目录，按 projectSkillDirs 的顺序
	Skills    []*Skill     // 同一份文件只算一个，按名称
}

// ProjectDir 是项目里的一个技能目录和其中的技能。
type ProjectDir struct {
	Rel    string // 相对项目根，如 .claude/skills
	Skills []*Skill
}

// Linked 是被 Agent 全局链接出去的技能：在任何项目里都会被读到，
// 项目删掉或改名后这些链接会失效。
func (p *ProjectInfo) Linked() []*Skill {
	var out []*Skill
	for _, s := range p.Skills {
		if len(s.Enabled) > 0 && !s.KeptLinks {
			out = append(out, s)
		}
	}
	return out
}

// Library 是全部技能。
type Library struct {
	spokenLang     string                 // spokenCache 是哪种语言的读法
	spokenCache    map[*Skill]spokenSkill // 名称和简介的读法，按读音搜索时转写，库换了就没了
	updates        *updateStore
	updatesErr     error
	recordsErr     error
	Root           string  // 技能库目录：~/.agents/skills
	Home           string  // 用户主目录；示例数据为空
	Agents         []Agent // 本机装了的 Agent，按 Catalog 的顺序
	custom         []Agent // 本次扫描到的自定义技能目录 Agent
	Skills         []*Skill
	Collections    []*Collection  // 技能集合，大的在前
	Projects       []*ProjectInfo // 有技能的项目和手动加的项目，最近用过的在前
	Broken         []string       // 指向不存在目录的链接
	Settings       Settings       // 设置弹窗里的选项
	Discovery      Discovery      // 项目是从哪些使用记录里找到的
	manual         []*ProjectInfo // 手动添加的文件夹，含没有技能的
	disk           bool           // 来自 Scan：启用 / 停用会改磁盘
	usageLog       *UsageLog      // 会话记录里的技能调用，nil 是还没读
	usage          map[*Skill]*Usage
	calls          []call // 对到技能上的调用，按时间
	readers        map[string][]string
	native         map[string]*nativeConfig
	nativeData     nativeData
	nativeErr      error
	geminiProjects map[string]*nativeConfig
	zcodeProjects  map[string][]zcodeLayer // 项目目录 → 要叠在用户配置上的 ZCode 项目配置
	zcodeChains    map[string][]string     // 项目目录 → 会放 ZCode 项目配置的目录（含没有配置的项目）
	records        *InstallRecords         // npx skills 的全局安装记录；没有或读不了为 nil
}

// Display 把主目录换成 ~，用于界面显示路径。分隔符统一成 /。
func (l *Library) Display(p string) string { return ShortPath(l.Home, p) }

// ShortPath 把 p 里的主目录 home 换成 ~，分隔符统一成 /；home 为空或 p 不在
// home 下时只换分隔符。
func ShortPath(home, p string) string {
	if home == "" {
		return filepath.ToSlash(p)
	}
	rel, err := filepath.Rel(home, p)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return filepath.ToSlash(p)
	}
	if rel == "." {
		return "~"
	}
	return "~/" + filepath.ToSlash(rel)
}

// DirReaders 是会读一个全局技能目录的 Agent（本机装了的）。
func (l *Library) DirReaders(dir string) []string { return l.readers[dir] }

// SharedReaders 是直接读技能库（通用目录）的 Agent：技能放进去它们就都读到。
func (l *Library) SharedReaders() []string { return l.readers[l.Root] }

// ReadsShared 报告 Agent 直接读通用目录。
func (l *Library) ReadsShared(agent string) bool { return slices.Contains(l.SharedReaders(), agent) }

// Separate 是技能单独启用的 Agent：在通用目录里的技能，去掉直接读通用
// 目录的那些，剩下的是在自己目录里建了链接的。
func (l *Library) Separate(s *Skill) []string {
	if !s.Shared {
		return s.Enabled
	}
	var out []string
	for _, id := range s.Enabled {
		if !l.ReadsShared(id) {
			out = append(out, id)
		}
	}
	return out
}

// Tidy 删掉 Redundant 的链接。只删指向同一份的符号链接，技能本身不动。
func (l *Library) Tidy(s *Skill) error {
	if !l.disk {
		s.redundant = nil
		return nil
	}
	err := l.tidy(s)
	l.resolve(s)
	return err
}

// Enable 只控制已经可发现的技能，不创建或删除链接。
func (l *Library) Enable(s *Skill, agent string, on bool) error {
	if s.ReadOnly() {
		return errors.New(locales.LibraryReadOnlyToggle())
	}
	if !s.DiscoveredIn(agent) {
		return errors.New(locales.LibraryAddToAgentFirst())
	}
	if !nativeSupported(agent) {
		return errors.New(locales.LibraryAgentDisableUnsupported(AgentName(agent)))
	}
	if s.State(agent).Blocked {
		return errors.New(locales.NativeToggleOverridden())
	}
	if !l.disk {
		s.SetEnabled(agent, on)
		return nil
	}
	err := l.setNative(s, agent, on)
	if err != nil {
		st := s.State(agent)
		if st.Kind != StateUnknown {
			st.Kind = StateFailed
		}
		st.Message = err.Error()
		st.RetryOn = on
		s.States[agent] = st
	}
	return err
}

// Find 返回范围内、名称或简介包含 query 的技能，保持库内顺序。
func (l *Library) Find(sc Scope, query string) []*Skill {
	var out []*Skill
	for _, s := range l.Skills {
		if sc.match(s) {
			out = append(out, s)
		}
	}
	return Filter(out, query)
}

// Filter 返回名称或简介包含 query 的技能。
func Filter(skills []*Skill, query string) []*Skill {
	q := strings.ToLower(strings.TrimSpace(query))
	if q == "" {
		return skills
	}
	var out []*Skill
	for _, s := range skills {
		if strings.Contains(strings.ToLower(s.Name), q) || strings.Contains(strings.ToLower(s.Description), q) {
			out = append(out, s)
		}
	}
	return out
}

// Count 返回范围内的技能数。
func (l *Library) Count(sc Scope) int { return len(l.Find(sc, "")) }

// Get 按名称找技能，同名时返回第一个（「我的」排在前面）。
func (l *Library) Get(name string) *Skill {
	for _, s := range l.Skills {
		if s.Name == name {
			return s
		}
	}
	return nil
}

// ByID 按 ID 找技能。
func (l *Library) ByID(id string) *Skill {
	for _, s := range l.Skills {
		if s.ID == id {
			return s
		}
	}
	return nil
}

// Project 按完整路径找项目。
func (l *Library) Project(dir string) *ProjectInfo {
	for _, p := range l.Projects {
		if p.Dir == dir {
			return p
		}
	}
	return nil
}
