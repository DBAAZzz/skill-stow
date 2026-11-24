package library

import (
	"bufio"
	"cmp"
	"encoding/json"
	"errors"

	"io"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"time"

	"skillmanager/locales"
)

// 技能库目录：「我的」技能的原件放在这里。它也是通用目录，Codex、
// Cursor 等直接读它。
const libraryDir = sharedDir

// 早年的目录，没有 Agent 再读，只用来数同名副本。
var legacyDirs = []string{".agent/skills"}

// 记着用过哪些项目的编辑器（VS Code 系），在 ~/Library/Application Support 下。
var editorApps = []string{"Code", "Code - Insiders", "Cursor", "Windsurf", "CodeBuddy", "Trae", "Kiro", "Qoder"}

// projectSkillDirs 是项目里会被某个 Agent 读到的技能目录，按 Catalog 的顺序。
var projectSkillDirs = func() []string {
	var out []string
	for _, a := range Catalog {
		for _, d := range a.Project {
			if !slices.Contains(out, d) {
				out = append(out, d)
			}
		}
	}
	return append(out, legacyDirs...)
}()

// entry 是技能目录里的一项：一个技能目录，或指向技能目录的链接。
type entry struct {
	name   string
	path   string // 这一项本身
	real   string // 解析链接后的真实目录
	link   bool   // path 是链接
	nested bool   // 链接的是一整组技能，这是组里的一个，没有自己的那一项
}

var scanHook func()

// Scan 读 home 下的技能：技能库、本机装了的 Agent 的技能目录和插件、
// Agent 用过的项目。不改技能和 Agent 的文件；读不到的位置跳过，存在却
// 读不了的记进事件日志。应用自己的数据文件坏了时，原件挪到旁边（readJSON）。
func Scan(home string) *Library {
	if scanHook != nil {
		scanHook()
	}
	start := time.Now()
	l := &Library{Root: filepath.Join(home, libraryDir), Home: home, disk: true, readers: map[string][]string{}}
	var moved string
	if l.Settings, moved = loadSettings(home); moved != "" {
		l.recovered = append(l.recovered, moved)
	}
	for _, a := range Catalog {
		if a.installed(home) {
			l.Agents = append(l.Agents, a)
		}
	}
	for _, d := range l.Settings.AgentDirs {
		if fi, err := os.Stat(d); err == nil && fi.IsDir() {
			l.custom = append(l.custom, customAgent(home, d))
		}
	}
	l.Agents = append(l.Agents, l.custom...)
	for _, a := range l.Agents {
		for _, d := range a.dirs() {
			abs := filepath.Join(home, d)
			if !slices.Contains(l.readers[abs], a.ID) {
				l.readers[abs] = append(l.readers[abs], a.ID)
			}
		}
	}
	s := &scanner{lib: l, home: home}
	s.scan()
	recs, err := ReadInstallRecords(home)
	l.records, l.recordsErr = recs, err
	l.updates, l.updatesErr = loadUpdates(home)
	l.collect(lockSources(recs))
	l.markSameNames()
	l.refreshNative()
	l.scanTook = time.Since(start)
	l.logScanProblems()
	return l
}

type scanner struct {
	lib  *Library
	home string
}

// projEntry 是项目技能目录里的一项。
type projEntry struct {
	entry
	project *ProjectInfo
	rel     string
}

// globalLink 是全局目录里指向项目技能的链接。
type globalLink struct{ dir, path string }

func (sc *scanner) scan() {
	l := sc.lib

	// 项目里自己的技能（不是链接出去的）：全局目录里链到这些的是「全局链接」
	projects, auto := sc.projects()
	l.geminiProjects = map[string]*nativeConfig{}
	l.zcodeProjects, l.zcodeChains = map[string][]zcodeLayer{}, map[string][]string{}
	for _, p := range projects {
		l.geminiProjects[p.Dir] = readNativeConfig(filepath.Join(p.Dir, ".gemini/settings.json"), false)
		l.zcodeChains[p.Dir] = zcodeProjectChain(p.Dir)
		if layers := zcodeProjectLayers(l.zcodeChains[p.Dir]); len(layers) > 0 {
			l.zcodeProjects[p.Dir] = layers
		}
	}
	var projEntries []projEntry
	projectOwned := map[string]bool{}
	for _, p := range projects {
		root, err := realPath(p.Dir)
		if err != nil {
			continue
		}
		p.gitScan.dir = root
		for _, rel := range projectSkillDirs {
			entries := sc.list(filepath.Join(p.Dir, rel))
			if len(entries) > 0 {
				p.gitScan.dirs = append(p.gitScan.dirs, rel)
			}
			for _, e := range entries {
				if !isUnderDir(e.real, root) || !isUnderDir(underRealDir(e.path), root) {
					continue
				}
				projEntries = append(projEntries, projEntry{e, p, rel})
				projectOwned[e.real] = true
			}
		}
	}

	builtin := sc.plugins()
	builtinReal := map[string]bool{}
	for _, s := range builtin {
		builtinReal[s.Real] = true
	}

	// 「我的」：技能库和装了的 Agent 会读的全局目录，按名称合并
	global := []string{l.Root}
	for _, a := range l.Agents {
		for _, d := range a.dirs() {
			if abs := filepath.Join(sc.home, d); !slices.Contains(global, abs) {
				global = append(global, abs)
			}
		}
	}
	var mine []*Skill
	byName := map[string]*Skill{}
	globalLinks := map[string][]globalLink{} // 项目技能的真实目录 → 全局链接
	for _, dir := range global {
		for _, e := range sc.list(dir) {
			switch {
			case projectOwned[e.real]:
				globalLinks[e.real] = append(globalLinks[e.real], globalLink{dir, e.path})
			case builtinReal[e.real]:
			default:
				s := byName[e.name]
				if s == nil {
					s = &Skill{ID: "mine:" + e.name, Name: e.name, Source: Mine, Dir: e.path, Real: e.real}
					s.Description = readDescription(e.real)
					byName[e.name] = s
					mine = append(mine, s)
				}
				s.places = append(s.places, place{dir, e.path, e.real, e.link, e.nested})
			}
		}
	}

	// 同名副本：没装的 Agent 的目录和早年的目录里的也算
	var others []string
	for _, a := range Catalog {
		if abs := filepath.Join(sc.home, a.Skills); !slices.Contains(global, abs) && !slices.Contains(others, abs) {
			others = append(others, abs)
		}
	}
	for _, d := range legacyDirs {
		others = append(others, filepath.Join(sc.home, d))
	}
	reals := map[string]map[string]bool{}
	for _, s := range mine {
		reals[s.Name] = map[string]bool{}
		for _, p := range s.places {
			reals[s.Name][p.real] = true
		}
	}
	elsewhere := map[string][]place{} // 这些目录里的同名副本，收成一份时要处理
	for _, d := range others {
		for _, e := range sc.list(d) {
			if r := reals[e.name]; r != nil && !projectOwned[e.real] {
				r[e.real] = true
				elsewhere[e.name] = append(elsewhere[e.name], place{d, e.path, e.real, e.link, e.nested})
			}
		}
	}
	for _, s := range mine {
		s.Copies = len(reals[s.Name]) - 1
		s.elsewhere = elsewhere[s.Name]
		l.resolve(s)
	}

	// 项目：同一份文件经链接出现在几个目录里，只算一个
	projectReaders := map[string][]string{} // 项目里的目录 → 会读它的 Agent
	for _, a := range l.Agents {
		for _, rel := range a.Project {
			projectReaders[rel] = append(projectReaders[rel], a.ID)
		}
	}
	var project []*Skill
	byReal := map[string]*Skill{}
	inDir := map[*ProjectInfo]map[string][]*Skill{}
	for _, e := range projEntries {
		s := byReal[e.real]
		if s == nil {
			s = &Skill{
				ID: "project:" + e.real, Name: e.name, Source: Project,
				Project: e.project.Name, ProjectDir: e.project.Dir, Dir: e.real, Real: e.real,
				Description: readDescription(e.real),
				KeptLinks:   slices.Contains(l.Settings.KeptLinks, e.real),
			}
			for _, gl := range globalLinks[e.real] {
				s.globalLinks = append(s.globalLinks, gl.path)
				for _, ag := range l.readers[gl.dir] {
					s.Reads = append(s.Reads, Read{ag, gl.dir, true})
					if !s.EnabledIn(ag) {
						s.Enabled = append(s.Enabled, ag)
					}
				}
			}
			byReal[e.real] = s
			project = append(project, s)
			e.project.Skills = append(e.project.Skills, s)
		}
		physicalPath := filepath.Join(e.project.gitScan.dir, gitRel(e.project.Dir, e.path))
		e.project.gitScan.installs = append(e.project.gitScan.installs, gitPlace{physicalPath, e.real})
		if e.link && !e.nested {
			s.projectLinks = append(s.projectLinks, e.path)
		}
		for _, ag := range projectReaders[e.rel] {
			r := Read{ag, e.rel, false}
			if !slices.Contains(s.Reads, r) {
				s.Reads = append(s.Reads, r)
			}
		}
		if inDir[e.project] == nil {
			inDir[e.project] = map[string][]*Skill{}
		}
		if !slices.Contains(inDir[e.project][e.rel], s) {
			inDir[e.project][e.rel] = append(inDir[e.project][e.rel], s)
		}
	}
	for _, s := range project {
		// 项目里读到的排在全局链接前
		slices.SortStableFunc(s.Reads, func(a, b Read) int { return cmp.Compare(b2i(a.Global), b2i(b.Global)) })
	}
	nameOrder := func(a, b *Skill) int { return strings.Compare(a.Name, b.Name) }
	for _, p := range projects {
		slices.SortFunc(p.Skills, nameOrder)
		for _, rel := range projectSkillDirs {
			if ss := inDir[p][rel]; len(ss) > 0 {
				slices.SortFunc(ss, nameOrder)
				p.Dirs = append(p.Dirs, ProjectDir{rel, ss})
			}
		}
		// 没有技能的项目不列出（手动添加的在设置里看得到）
		if len(p.Skills) > 0 {
			l.Projects = append(l.Projects, p)
		}
		if auto[p.Dir] && len(p.Skills) > 0 {
			l.Discovery.WithSkills++
		}
	}
	for _, m := range readManual(sc.home) {
		dir := canonicalProjectDir(m.Dir)
		i := slices.IndexFunc(projects, func(p *ProjectInfo) bool { return p.Dir == dir })
		if i >= 0 {
			l.manual = append(l.manual, projects[i])
		} else {
			l.manual = append(l.manual, &ProjectInfo{Name: filepath.Base(dir), Dir: dir, Manual: true, Missing: true})
		}
	}
	slices.SortStableFunc(l.Projects, func(a, b *ProjectInfo) int {
		return cmp.Or(b.Used.Compare(a.Used), strings.Compare(a.Name, b.Name))
	})
	order := map[string]int{}
	for i, p := range l.Projects {
		order[p.Dir] = i
	}

	// 启用得多的在前；内置按 Agent，项目按最近用过
	slices.SortStableFunc(mine, func(a, b *Skill) int {
		return cmp.Or(cmp.Compare(len(b.Enabled), len(a.Enabled)), strings.Compare(a.Name, b.Name))
	})
	slices.SortStableFunc(builtin, func(a, b *Skill) int {
		return cmp.Or(cmp.Compare(sc.lib.catalogIndex(a.Provider), sc.lib.catalogIndex(b.Provider)), strings.Compare(a.Name, b.Name))
	})
	slices.SortStableFunc(project, func(a, b *Skill) int {
		return cmp.Or(cmp.Compare(order[a.ProjectDir], order[b.ProjectDir]), strings.Compare(a.Name, b.Name))
	})
	l.Skills = slices.Concat(mine, builtin, project)
	checkAll(l.Skills)
}

func b2i(b bool) int {
	if b {
		return 1
	}
	return 0
}

// resolve 按技能所在的目录算出哪些 Agent 会读到它：自己的目录里有那一项
// 的记在 Links，经别的目录读到的记在 Via。按真实目录算：读到的是技能库
// 那份（不在技能库时是第一份）才算启用，读到另一份同名的记在 copied。
func (l *Library) resolve(s *Skill) {
	s.Enabled, s.Links, s.Via, s.redundant, s.copied = nil, map[string]string{}, map[string]string{}, nil, nil
	s.Available = []string{}
	s.targets, s.nativeNames = map[string]string{}, map[string]string{}
	shared := ""
	for _, p := range s.places {
		if p.dir == l.Root {
			shared = p.real
		}
	}
	s.Shared = shared != ""
	main := cmp.Or(shared, s.Real)
	for _, a := range l.Agents {
		own := filepath.Join(l.Home, a.Skills)
		for _, d := range a.dirs() {
			abs := filepath.Join(l.Home, d)
			i := slices.IndexFunc(s.places, func(p place) bool { return p.dir == abs })
			if i < 0 {
				continue
			}
			p := s.places[i]
			s.Available = append(s.Available, a.ID)
			s.targets[a.ID] = filepath.Join(p.real, "SKILL.md")
			s.nativeNames[a.ID] = readSkillMD(p.real).Name
			// 技能库不归哪个 Agent：Cline、Kimi 自己的目录就是它，但那一项
			// 别的 Agent 也在读，不能当成它们的链接去停用或清理
			if abs == own && abs != l.Root && !p.nested {
				s.Links[a.ID] = p.path
			} else {
				s.Via[a.ID] = abs
			}
			switch {
			case p.real != main:
				s.copied = append(s.copied, a.ID)
			case s.Links[a.ID] != "" && shared != "" && l.ReadsShared(a.ID):
				// 读技能库的 Agent 自己目录里又链到同一份：多余
				s.redundant = append(s.redundant, a.ID)
				fallthrough
			default:
				s.Enabled = append(s.Enabled, a.ID)
			}
			break
		}
	}
	l.findVersions(s)
	if l.native != nil {
		l.applyNative(s)
	}
}

// list 列出目录里的技能。链接解析到真实目录；坏链接记进 Broken。
// 一项里没有 SKILL.md 而子目录有的，是一组技能，展开一层。
func (sc *scanner) list(dir string) []entry {
	items := sc.readDir(dir)
	var out []entry
	for _, it := range items {
		if strings.HasPrefix(it.Name(), ".") {
			continue
		}
		p := filepath.Join(dir, it.Name())
		link := isLink(p)
		real, err := realPath(p)
		if err != nil {
			if link && !slices.Contains(sc.lib.Broken, p) {
				sc.lib.Broken = append(sc.lib.Broken, p)
			}
			continue
		}
		if fi, err := os.Stat(real); err != nil || !fi.IsDir() {
			continue
		}
		if isSkill(real) {
			out = append(out, entry{it.Name(), p, real, link, false})
			continue
		}
		for _, sub := range sc.readDir(real) {
			r := filepath.Join(real, sub.Name())
			if sub.IsDir() && !strings.HasPrefix(sub.Name(), ".") && isSkill(r) {
				out = append(out, entry{sub.Name(), filepath.Join(p, sub.Name()), r, false, true})
			}
		}
	}
	return out
}

// readDir 列出目录。不存在的跳过；存在却读不了的（没有权限、磁盘出错）
// 也跳过，但记进 Unreadable，否则「技能没出现」时看不出是读失败了。
func (sc *scanner) readDir(dir string) []os.DirEntry {
	items, err := os.ReadDir(dir)
	if err != nil {
		if !errors.Is(err, fs.ErrNotExist) && !errors.Is(err, syscall.ENOTDIR) && !slices.Contains(sc.lib.unreadable, dir) {
			sc.lib.unreadable = append(sc.lib.unreadable, dir)
		}
		return nil
	}
	return items
}

func isSkill(dir string) bool {
	_, err := os.Stat(filepath.Join(dir, "SKILL.md"))
	return err == nil
}

// readDescription 读 SKILL.md 开头的 frontmatter 取简介。
func readDescription(dir string) string {
	return readSkillMD(dir).Description
}

func readSkillMD(dir string) SkillMD {
	f, err := os.Open(filepath.Join(dir, "SKILL.md"))
	if err != nil {
		return SkillMD{}
	}
	defer f.Close()
	b, _ := io.ReadAll(io.LimitReader(f, 32<<10))
	return ParseSkillMD(string(b))
}

// plugins 读插件里的技能和各 Agent 自带的内置技能。插件只认
// installed_plugins.json 里登记的，市场里没装的不算；其中 Agent 预装的
// 是内置（见 preinstalled），其余是用户装的。
func (sc *scanner) plugins() []*Skill {
	seen := map[string]bool{}
	var out []*Skill
	for _, a := range sc.lib.Agents {
		out = append(out, sc.pluginSkills(a, seen)...)
		if a.ID == codex {
			out = append(out, sc.codexPluginSkills(a, seen)...)
		}
		out = append(out, sc.builtinSkills(a, seen)...)
	}
	for _, s := range out {
		if s.codexBuiltin() {
			s.Available = []string{codex} // 只有 Codex 读到，启用状态按它的 config.toml 算
		}
	}
	return out
}

func (sc *scanner) pluginSkills(a Agent, seen map[string]bool) []*Skill {
	if a.Plugins == "" {
		return nil
	}
	b, err := os.ReadFile(filepath.Join(sc.home, a.Plugins, "installed_plugins.json"))
	if err != nil {
		return nil
	}
	var reg struct {
		Plugins map[string][]struct {
			InstallPath string `json:"installPath"`
			Scope       string `json:"scope"`
			ProjectPath string `json:"projectPath"`
		} `json:"plugins"`
	}
	if json.Unmarshal(b, &reg) != nil {
		return nil
	}
	preset := sc.presetPlugins(a)
	var out []*Skill
	names := make([]string, 0, len(reg.Plugins))
	for name := range reg.Plugins {
		names = append(names, name)
	}
	slices.Sort(names)
	for _, name := range names {
		_, market, _ := strings.Cut(name, "@")
		src := ViaPlugin
		if preinstalled(market, preset[name]) {
			src = Builtin
		}
		for _, inst := range reg.Plugins[name] {
			out = append(out, sc.installedPluginSkills(a, inst.InstallPath, src,
				&pluginInstall{id: name, scope: inst.Scope, project: inst.ProjectPath}, seen)...)
		}
	}
	return out
}

// installedPluginSkills 读一个装好的插件里的技能：skills 下的每个子目录，
// 和插件目录本身是技能的情况（目录名是版本号，用插件名）。
func (sc *scanner) installedPluginSkills(a Agent, dir string, src Source, inst *pluginInstall, seen map[string]bool) []*Skill {
	plugin, _, _ := strings.Cut(inst.id, "@")
	dirs := []string{dir}
	if subs, err := os.ReadDir(filepath.Join(dir, "skills")); err == nil {
		for _, d := range subs {
			dirs = append(dirs, filepath.Join(dir, "skills", d.Name()))
		}
	}
	var out []*Skill
	for _, d := range dirs {
		real, err := realPath(d)
		if err != nil || seen[real] || !isSkill(real) {
			continue
		}
		seen[real] = true
		md := readSkillMD(real)
		n := filepath.Base(d)
		if d == dir {
			n = plugin
		}
		out = append(out, &Skill{
			ID: "builtin:" + real, Name: n, Description: md.Description,
			Source: src, Provider: a.ID, Plugin: plugin, Dir: real, Real: real, install: inst,
		})
	}
	return out
}

// preinstalled 报告插件是 Agent 预装的：市场名以 -builtin 结尾（如 WorkBuddy
// 的 workbuddy-builtin），或者 Agent 把它列为预装（presetPlugins）。两样都要看：
// 预装名单里有别的市场的（WorkBuddy 的企业微信插件来自 workbuddy-wecom-unified），
// 而新版本不再预装、留在磁盘上的 -builtin 插件又不在名单里。
func preinstalled(market string, listed bool) bool {
	return listed || strings.HasSuffix(market, "-builtin")
}

// presetPlugins 是 Agent 自己记下的预装插件（plugin@market）：CodeBuddy、
// WorkBuddy 写在插件目录的 configured-plugins.json 里。读不到就是空的，
// 只按市场名判断。
func (sc *scanner) presetPlugins(a Agent) map[string]bool {
	b, err := os.ReadFile(filepath.Join(sc.home, a.Plugins, "configured-plugins.json"))
	if err != nil {
		return nil
	}
	type entry struct {
		Name   string `json:"name"`
		Market string `json:"marketplaceName"`
	}
	var cfg struct {
		Plugins []entry `json:"builtInMarketPlugins"`
		Merge   []entry `json:"builtInMarketPluginsMerge"`
	}
	if json.Unmarshal(b, &cfg) != nil {
		return nil
	}
	out := map[string]bool{}
	for _, e := range slices.Concat(cfg.Plugins, cfg.Merge) {
		if e.Name != "" && e.Market != "" {
			out[e.Name+"@"+e.Market] = true
		}
	}
	return out
}

// builtinSkills 读 Agent 直接放在目录里的自带技能：每个子目录是一个技能，
// 没有插件那样的登记文件（如 Antigravity 的 builtin/skills、Codex 的
// skills/.system）。
func (sc *scanner) builtinSkills(a Agent, seen map[string]bool) []*Skill {
	var out []*Skill
	for _, rel := range a.Builtin {
		root := filepath.Join(sc.home, rel)
		for _, d := range sc.readDir(root) {
			real, err := realPath(filepath.Join(root, d.Name()))
			if err != nil || seen[real] || !isSkill(real) {
				continue
			}
			seen[real] = true
			md := readSkillMD(real)
			out = append(out, &Skill{
				ID: "builtin:" + real, Name: d.Name(), Description: md.Description,
				Source: Builtin, Provider: a.ID, Dir: real, Real: real,
			})
		}
	}
	return out
}

// projects 找项目：不扫整个磁盘，只看 Claude Code、Codex 和 VS Code 系
// 编辑器的使用记录（设置里可以关掉），加上用户手动加的。主目录本身不算
// 项目；git worktree 默认算作它的主仓库。auto 是从使用记录里找到的。
func (sc *scanner) projects() (out []*ProjectInfo, auto map[string]bool) {
	start := time.Now()
	settings := sc.lib.Settings
	used := map[string]time.Time{}
	manual := map[string]bool{}
	auto = map[string]bool{}
	clean := func(p string) string {
		return cleanProjectDir(sc.home, p)
	}
	add := func(p string, t time.Time) {
		if old, ok := used[p]; !ok || t.After(old) {
			used[p] = t
		}
	}
	mtime := func(p string) time.Time {
		if fi, err := os.Stat(p); err == nil {
			return fi.ModTime()
		}
		return time.Time{}
	}
	// 每处使用记录各自去重计数，合并到 used
	type found map[string]time.Time
	record := func(f found, p string, t time.Time) {
		if p = clean(p); p == "" {
			return
		}
		if settings.MergeWorktrees {
			if main := gitMain(p); main != "" {
				if m := clean(main); m != "" {
					p = m
				}
			}
		}
		if old, ok := f[p]; !ok || t.After(old) {
			f[p] = t
		}
	}
	source := func(name, agent, path string, ok bool, f found, note string) {
		count := len(f)
		if !ok {
			count = -1
		}
		sc.lib.Discovery.Sources = append(sc.lib.Discovery.Sources, DiscoverySource{name, agent, path, count, note})
		if !settings.AutoDiscover {
			return
		}
		for p, t := range f {
			add(p, t)
			auto[p] = true
		}
	}

	// Claude Code：~/.claude.json 记着项目，~/.claude/projects 下的会话目录记着最近一次
	claude := found{}
	b, err := os.ReadFile(filepath.Join(sc.home, ".claude.json"))
	if err == nil {
		var cfg struct {
			Projects map[string]json.RawMessage `json:"projects"`
		}
		if json.Unmarshal(b, &cfg) == nil {
			for p := range cfg.Projects {
				record(claude, p, mtime(filepath.Join(sc.home, ".claude", "projects", claudeProjectKey(p))))
			}
		}
	}
	source("Claude Code", "claude-code", "~/.claude.json", err == nil, claude, "")

	// Codex：每个会话文件的第一行记着工作目录
	codex := found{}
	sessions := filepath.Join(sc.home, ".codex", "sessions")
	_, codexErr := os.Stat(sessions)
	filepath.WalkDir(sessions, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(p, ".jsonl") {
			return nil
		}
		f, err := os.Open(p)
		if err != nil {
			return nil
		}
		defer f.Close()
		r := bufio.NewReaderSize(f, 64<<10)
		line, _ := r.ReadSlice('\n')
		var first struct {
			Payload struct {
				Cwd string `json:"cwd"`
			} `json:"payload"`
		}
		if json.Unmarshal(line, &first) == nil {
			var t time.Time
			if fi, err := d.Info(); err == nil {
				t = fi.ModTime()
			}
			record(codex, first.Payload.Cwd, t)
		}
		return nil
	})

	// VS Code 系编辑器：每个打开过的文件夹在 workspaceStorage 里有一个 workspace.json
	editors := found{}
	var editorNames []string
	for _, app := range editorApps {
		files, _ := filepath.Glob(filepath.Join(supportDir(sc.home), app, "User", "workspaceStorage", "*", "workspace.json"))
		if len(files) > 0 {
			editorNames = append(editorNames, app)
		}
		for _, f := range files {
			b, err := os.ReadFile(f)
			if err != nil {
				continue
			}
			var ws struct {
				Folder string `json:"folder"`
			}
			if json.Unmarshal(b, &ws) != nil {
				continue
			}
			if dir := fileURIPath(ws.Folder); dir != "" {
				t := mtime(f)
				if v := mtime(filepath.Join(filepath.Dir(f), "state.vscdb")); v.After(t) {
					t = v
				}
				record(editors, dir, t)
			}
		}
	}
	source(locales.ScanVsCodeEditors(), "vscode", "…/User/workspaceStorage", len(editorNames) > 0, editors, strings.Join(editorNames, locales.ListSeparator()))
	source("Codex", "codex", "~/.codex/sessions", codexErr == nil, codex, "")
	sc.lib.Discovery.Total = len(used)

	manualList, moved := readManualFile(sc.home)
	if moved != "" {
		sc.lib.recovered = append(sc.lib.recovered, moved)
	}
	for _, m := range manualList {
		if p := clean(m.Dir); p != "" {
			add(p, m.Added)
			manual[p] = true
		}
	}

	sc.lib.knownProjects = make(map[string]*ProjectInfo, len(used))
	for p, t := range used {
		project := &ProjectInfo{Name: filepath.Base(p), Dir: p, Used: t, Manual: manual[p]}
		sc.lib.knownProjects[p] = project
		out = append(out, project)
	}
	slices.SortFunc(out, func(a, b *ProjectInfo) int { return strings.Compare(a.Dir, b.Dir) })
	sc.lib.Discovery.Took = time.Since(start)
	return out, auto
}

// fileURIPath 把 file:// URI 转成本机路径，不是 file URI 时返回空。
// Windows 的 file:///C:/… 解出来是 /C:/…，要去掉盘符前的斜杠。
func fileURIPath(uri string) string {
	u, err := url.Parse(uri)
	if err != nil || u.Scheme != "file" {
		return ""
	}
	p := u.Path
	if len(p) > 1 && p[0] == '/' && filepath.VolumeName(p[1:]) != "" {
		p = p[1:]
	}
	return filepath.FromSlash(p)
}

// canonicalProjectDir 是项目目录的规范写法：判断两条记录是不是同一个项目，
// 两边都要先过它。盘符统一成大写，因为同一个目录 VS Code 记成 c:\…，
// Claude Code 记成 C:\…。
func canonicalProjectDir(p string) string {
	p = filepath.Clean(p)
	if v := filepath.VolumeName(p); len(v) == 2 && v[1] == ':' {
		return strings.ToUpper(v) + p[len(v):]
	}
	return p
}

// claudeProjectKey 是 Claude Code 存一个项目的会话用的目录名：
// 路径里字母数字以外的字符都换成 -。
func claudeProjectKey(p string) string {
	return strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' {
			return r
		}
		return '-'
	}, p)
}

// link 在 Agent 自己的技能目录里建一个指向技能的相对链接。
func (l *Library) link(s *Skill, agentID string) error {
	a, ok := l.agentByID(agentID)
	if !ok {
		return errors.New(locales.ScanUnknownAgent(agentID))
	}
	if !a.hasHome(l.Home) {
		return errors.New(locales.ScanAgentDirectoryMissing(a.DisplayName(), l.Display(filepath.Join(l.Home, a.Home))))
	}
	dir := filepath.Join(l.Home, a.Skills)
	dst := filepath.Join(dir, s.Name)
	if _, err := os.Lstat(dst); err == nil {
		if real, err := realPath(dst); err == nil && real == s.Real {
			s.places = append(s.places, place{dir, dst, real, true, false})
			return nil
		}
		return errors.New(locales.ScanAgentSkillExists(a.DisplayName(), s.Name))
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	// 指向真实目录，不指向扫到的那一项：那一项可能也是链接，它被停用删掉
	// 后这里就断了。相对路径从目录的真实位置算，Agent 目录本身可能是链接。
	from := dir
	if real, err := realPath(dir); err == nil {
		from = real
	}
	target, err := filepath.Rel(from, s.Real)
	if err != nil {
		target = s.Real
	}
	if err := makeLink(target, dst); err != nil {
		return err
	}
	s.places = append(s.places, place{dir, dst, s.Real, true, false})
	return nil
}

// unlink 删掉 Agent 自己技能目录里的链接。那里是真实目录（独立拷贝）
// 时拒绝：停用不能删文件。经别的目录读到的也不删，那个目录还有别的 Agent 在读。
func (l *Library) unlink(s *Skill, agentID string) error {
	a, _ := l.agentByID(agentID)
	dst := s.Links[agentID]
	if dst == "" {
		via := s.Via[agentID]
		if via == filepath.Join(l.Home, a.Skills) {
			return errors.New(locales.ScanGroupLinkCannotRemove(a.DisplayName()))
		}
		var others []string
		for _, id := range l.readers[via] {
			if id != agentID {
				others = append(others, AgentName(id))
			}
		}
		if len(others) > 0 {
			return errors.New(locales.DeleteSharedDirOthers(AgentName(agentID), l.Display(via), JoinAnd(others)))
		}
		return errors.New(locales.DeleteSharedDir(AgentName(agentID), l.Display(via)))
	}
	_, err := os.Lstat(dst)
	if errors.Is(err, fs.ErrNotExist) {
		s.dropPlace(dst)
		return nil
	}
	if err != nil {
		return err
	}
	if !isLink(dst) {
		return errors.New(locales.ScanRealDirectoryCannotUnlink(l.Display(filepath.Dir(dst)), s.Name))
	}
	if err := os.Remove(dst); err != nil {
		return err
	}
	s.dropPlace(dst)
	return nil
}

// tidy 删掉读通用目录的 Agent 自己目录里指向同一份的链接。
func (l *Library) tidy(s *Skill) error {
	var errs []error
	for _, id := range s.redundant {
		dst := s.Links[id]
		if !isLink(dst) {
			continue // 不是链接了，不碰
		}
		if err := os.Remove(dst); err != nil {
			errs = append(errs, err)
			continue
		}
		s.dropPlace(dst)
	}
	return errors.Join(errs...)
}

func (s *Skill) dropPlace(path string) {
	s.places = slices.DeleteFunc(s.places, func(p place) bool { return p.path == path })
}

// ——— 手动加的项目 ———

type manualProject struct {
	Dir   string    `json:"dir"`
	Added time.Time `json:"added"`
}

func manualFile(home string) string {
	return filepath.Join(appDir(home), "projects.json")
}

func readManual(home string) []manualProject {
	list, _ := readManualFile(home)
	return list
}

// readManualFile 读手动添加的项目；文件坏了时第二个返回值是挪开的原件。
func readManualFile(home string) ([]manualProject, string) {
	var cfg struct {
		Projects []manualProject `json:"projects"`
	}
	cfg, moved := readJSON(manualFile(home), cfg)
	return cfg.Projects, moved
}

// cleanProjectDir 规范化项目目录；家目录本身、根目录或不存在的目录返回空。
func cleanProjectDir(home, p string) string {
	if p == "" || !filepath.IsAbs(p) {
		return ""
	}
	p = canonicalProjectDir(p)
	if p == canonicalProjectDir(home) || filepath.Dir(p) == p {
		return ""
	}
	if fi, err := os.Stat(p); err != nil || !fi.IsDir() {
		return ""
	}
	return p
}

// ValidProjectDir 报告 p 能不能作为项目目录（不能是家目录本身或根目录），返回规范化后的路径。
func (l *Library) ValidProjectDir(p string) (string, bool) {
	if l == nil {
		return "", false
	}
	c := cleanProjectDir(l.Home, p)
	if c == "" {
		return "", false
	}
	real, err := realPath(c)
	home, _ := realPath(l.Home)
	if err != nil || samePlace(real, home) || filepath.Dir(real) == real {
		return "", false
	}
	return c, true
}

// AddProjects 记下用户手动加的项目目录，重新扫描后列出。
func (l *Library) AddProjects(dirs []string) error {
	if !l.disk {
		return errors.New(locales.ProjectsSampleCantAdd())
	}
	list := readManual(l.Home)
	now := time.Now()
	for _, d := range dirs {
		d = canonicalProjectDir(d)
		if i := slices.IndexFunc(list, func(m manualProject) bool { return canonicalProjectDir(m.Dir) == d }); i >= 0 {
			list[i].Added = now
			continue
		}
		list = append(list, manualProject{d, now})
	}
	return writeManual(l.Home, list)
}

func writeManual(home string, list []manualProject) error {
	return writeJSON(manualFile(home), struct {
		Projects []manualProject `json:"projects"`
	}{list})
}
