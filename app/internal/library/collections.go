package library

import (
	"cmp"
	"errors"

	"path/filepath"
	"slices"
	"strings"
	"time"

	"skillmanager/locales"
)

// SetKind 是技能集合是怎么认出来的。
type SetKind int

const (
	SetPlugin SetKind = iota // 内置：同一个插件带的
	SetSource                // npx skills 从同一个来源装的（~/.agents/.skill-lock.json）
	SetFolder                // 链接到同一个文件夹里的
)

// Collection 是一起装上的一组技能（技能集合）：界面上收成一张叠卡，
// 开关对整组生效。只按确实一起装的认：插件、npx skills 的来源、链接
// 进来的同一个文件夹；不按目录或名称前缀猜。至少两个技能才算。
type Collection struct {
	ID        string
	Name      string
	Kind      SetKind
	Provider  string    // 插件：哪个 Agent 的
	Dir       string    // 文件夹：真实目录
	Installed time.Time // npx skills：最早装上的时间
	Skills    []*Skill  // 按名称
}

// Shared 报告集合里的技能都在通用目录里。
func (c *Collection) Shared() bool {
	for _, s := range c.Skills {
		if !s.Shared {
			return false
		}
	}
	return true
}

// ReadOnly 报告集合不能在这里启用或停用（插件带的）。
func (c *Collection) ReadOnly() bool { return c.Kind == SetPlugin }

// EnabledIn 是集合里有几个技能 Agent 会读到。
func (c *Collection) EnabledIn(agent string) int {
	n := 0
	for _, s := range c.Skills {
		if s.EnabledIn(agent) {
			n++
		}
	}
	return n
}

// Preview 是集合的前几个技能名，「a、b、c 等」。
func (c *Collection) Preview(n int) string {
	var names []string
	for _, s := range c.Skills[:min(n, len(c.Skills))] {
		names = append(names, s.Name)
	}
	text := strings.Join(names, locales.ListSeparator())
	if len(c.Skills) > n {
		text = locales.ListEtc(text)
	}
	return text
}

// DependedBy 是集合里另有几个技能的简介提到它：它们把请求交给它处理，
// 或者要先用它。
func (c *Collection) DependedBy(s *Skill) int {
	n := 0
	for _, o := range c.Skills {
		if o != s && mentions(o.Description, s.Name) {
			n++
		}
	}
	return n
}

// mentions 报告 text 里出现了完整的 name：lark-vc-agent 不算提到 lark-vc。
func mentions(text, name string) bool {
	word := func(b byte) bool {
		return b == '-' || b == '_' || b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z' || b >= '0' && b <= '9'
	}
	for i := 0; ; {
		j := strings.Index(text[i:], name)
		if j < 0 {
			return false
		}
		start, end := i+j, i+j+len(name)
		if (start == 0 || !word(text[start-1])) && (end == len(text) || !word(text[end])) {
			return true
		}
		i = start + 1
	}
}

// installed 是 npx skills 记下的一个技能从哪里装的。
type installed struct {
	Source string
	At     time.Time
}

// lockSources 是安装记录里每个技能的来源和安装时间。
func lockSources(recs *InstallRecords) map[string]installed {
	out := map[string]installed{}
	for _, e := range recs.entries() {
		out[e.Name] = installed{e.Source, e.InstalledAt}
	}
	return out
}

// collect 认出技能集合，记在 Collections 和各技能的 Collection 上。依次：
// 插件带的内置技能按插件；通用目录里 npx skills 从同一个来源装的；
// 链接到同一个文件夹（不是 Agent 的技能目录）里的。大的集合在前。
func (l *Library) collect(lock map[string]installed) {
	var sets []*Collection
	byID := map[string]*Collection{}
	add := func(id, name string, kind SetKind, s *Skill) *Collection {
		c := byID[id]
		if c == nil {
			c = &Collection{ID: id, Name: name, Kind: kind}
			byID[id] = c
			sets = append(sets, c)
		}
		c.Skills = append(c.Skills, s)
		return c
	}
	for _, s := range l.Skills {
		s.Collection = nil
		switch {
		case s.Plugin != "": // 插件装的和 Agent 预装的插件
			add("plugin:"+s.Provider+"/"+s.Plugin, s.Plugin, SetPlugin, s).Provider = s.Provider
		case s.Source != Mine:
		case s.Shared && lock[s.Name].Source != "":
			in := lock[s.Name]
			c := add("source:"+in.Source, in.Source, SetSource, s)
			if !in.At.IsZero() && (c.Installed.IsZero() || in.At.Before(c.Installed)) {
				c.Installed = in.At
			}
		default:
			if dir, name := l.linkedFolder(s); dir != "" {
				add("folder:"+dir, name, SetFolder, s).Dir = dir
			}
		}
	}
	l.Collections = nil
	for _, c := range sets {
		if len(c.Skills) < 2 {
			continue
		}
		slices.SortFunc(c.Skills, func(a, b *Skill) int { return strings.Compare(a.Name, b.Name) })
		for _, s := range c.Skills {
			s.Collection = c
		}
		l.Collections = append(l.Collections, c)
	}
	slices.SortStableFunc(l.Collections, func(a, b *Collection) int {
		return cmp.Or(cmp.Compare(len(b.Skills), len(a.Skills)), strings.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name)))
	})
}

// linkedFolder 是技能经链接从哪个文件夹来：Agent 目录里的那一项是链接
// （或整组链接里的一个），指向的不在任何 Agent 的技能目录里。name 是
// 集合的名称：整组链接用链接的名字，否则用文件夹名。
func (l *Library) linkedFolder(s *Skill) (dir, name string) {
	for _, p := range s.places {
		if p.path == p.real && !p.nested {
			continue
		}
		dir := filepath.Dir(p.real)
		if dir == l.Root || len(l.readers[dir]) > 0 || l.agentDir(dir) {
			continue
		}
		if p.nested {
			return dir, filepath.Base(filepath.Dir(p.path))
		}
		return dir, folderName(dir)
	}
	return "", ""
}

// agentDir 报告 dir 是某个 Agent（含没装的）的全局技能目录或早年的目录。
func (l *Library) agentDir(dir string) bool {
	for _, a := range Catalog {
		if filepath.Join(l.Home, a.Skills) == dir {
			return true
		}
	}
	for _, d := range legacyDirs {
		if filepath.Join(l.Home, d) == dir {
			return true
		}
	}
	return false
}

// folderName 是文件夹集合的名称：文件夹名，叫 skills 的带上上一级。
func folderName(dir string) string {
	if base := filepath.Base(dir); base != "skills" {
		return base
	}
	return filepath.Base(filepath.Dir(dir)) + "/skills"
}

// CollectionByID 按 ID 找集合。
func (l *Library) CollectionByID(id string) *Collection {
	for _, c := range l.Collections {
		if c.ID == id {
			return c
		}
	}
	return nil
}

// EnableAll is unavailable until group operations can report every native result.
func (l *Library) EnableAll(c *Collection, agent string, on bool) error {
	return errors.New(locales.CollectionToggleIndividually())
}
