package library

import (
	"errors"

	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"skillmanager/locales"
)

// 整理：本机技能的几类问题，和处理它们的办法。

// Issue 是一类问题。
type Issue int

const (
	SameCopies    Issue = iota // 内容相同的副本：收成一份
	DiffCopies                 // 内容不同的副本：对比后选一份
	ExtraLinks                 // 重复的链接：读技能库的 Agent 自己目录里又链了一份
	BrokenLinks                // 断掉的链接
	NoDescription              // 没写简介
	GlobalLinked               // 项目技能被全局链接
	StaleRecords               // 已删除技能的安装记录
	Incomplete                 // 技能里说有、磁盘上没有的文件
)

// Issues 是界面上问题的顺序。
var Issues = []Issue{SameCopies, DiffCopies, ExtraLinks, BrokenLinks, Incomplete, StaleRecords, NoDescription, GlobalLinked}

func (k Issue) Title() string {
	return [...]string{locales.IssueIdenticalCopies(), locales.IssueDifferentCopies(), locales.IssueDuplicateLinks(), locales.IssueBrokenLinks(), locales.IssueMissingDescription(), locales.IssueGloballyLinkedProjectSkills(), locales.IssueDeletedSkillInstallRecords(), locales.IssueIncompleteSkills()}[k]
}

// ExtraLink 是读技能库的 Agent 自己目录里，又指向技能库那份的链接：
// 它会读到两次，删掉照样能用。
type ExtraLink struct {
	Skill *Skill
	Agent string
	Path  string
}

// BrokenLink 是指向不存在目录的链接。
type BrokenLink struct {
	Path   string
	Name   string
	Dir    string
	Relink bool // 在 Agent 的全局目录里，技能库有同名技能：可以重新连上
}

// Problems 是检查出的问题，每类一张表。
type Problems struct {
	Same, Diff []*Skill
	Extra      []ExtraLink
	Broken     []BrokenLink
	NoDesc     []*Skill // 我的和项目里的；内置的改不了
	Global     []*Skill
	Stale      []StaleRecord // 按来源合并的行
	Incomplete []*Skill      // 我的和项目里的；内置的改不了
}

// Count 是一类问题有几行。
func (p Problems) Count(k Issue) int {
	return [...]int{len(p.Same), len(p.Diff), len(p.Extra), len(p.Broken), len(p.NoDesc), len(p.Global), len(p.Stale), len(p.Incomplete)}[k]
}

// Total 是全部问题的行数，侧边栏「整理」的数字。
func (p Problems) Total() int {
	n := 0
	for _, k := range Issues {
		n += p.Count(k)
	}
	return n
}

// Problems 检查技能库里的问题。
func (l *Library) Problems() Problems {
	var p Problems
	for _, s := range l.Skills {
		switch s.Source {
		case Mine:
			switch {
			case s.Identical():
				p.Same = append(p.Same, s)
			case len(s.versions) > 1:
				p.Diff = append(p.Diff, s)
			}
			for _, id := range s.redundant {
				p.Extra = append(p.Extra, ExtraLink{s, id, s.Links[id]})
			}
		case Project:
			if len(s.Enabled) > 0 && !s.KeptLinks {
				p.Global = append(p.Global, s)
			}
		}
		if s.Source == Mine || s.Source == Project {
			if s.MissingDescription() {
				p.NoDesc = append(p.NoDesc, s)
			}
			if len(s.Defects) > 0 {
				p.Incomplete = append(p.Incomplete, s)
			}
		}
	}
	p.Stale = l.staleRecords()
	// 份数多的在前
	slices.SortStableFunc(p.Same, func(a, b *Skill) int { return b.Copies - a.Copies })
	for _, path := range l.Broken {
		dir, name := filepath.Dir(path), filepath.Base(path)
		p.Broken = append(p.Broken, BrokenLink{path, name, dir, l.globalDir(dir) && dir != l.Root && l.inLibrary(name)})
	}
	return p
}

// globalDir 报告 dir 是某个 Agent 的全局技能目录（示例数据的路径以 ~ 开头）。
func (l *Library) globalDir(dir string) bool {
	if !l.disk {
		return slices.ContainsFunc(Catalog, func(a Agent) bool { return filepath.Join("~", a.Skills) == dir })
	}
	return l.agentDir(dir)
}

// inLibrary 报告技能库里有这个名字的技能。
func (l *Library) inLibrary(name string) bool {
	if !l.disk {
		s := l.Get(name)
		return s != nil && s.Source == Mine && s.Shared
	}
	return isSkill(filepath.Join(l.Root, name))
}

// Modified 是技能文件最近一次修改的时间：有副本时取各份里最近的。
func (l *Library) Modified(s *Skill) time.Time {
	var t time.Time
	for _, v := range s.versions {
		if v.Modified.After(t) {
			t = v.Modified
		}
	}
	if t.IsZero() && l.disk {
		t = modified(s.Real)
	}
	return t
}

// DropLink 删掉读技能库的 Agent 自己目录里那个多余的链接。
func (l *Library) DropLink(s *Skill, agent string) error {
	if !slices.Contains(s.redundant, agent) {
		return nil
	}
	if !l.disk {
		s.redundant = slices.DeleteFunc(s.redundant, func(id string) bool { return id == agent })
		return nil
	}
	keep := s.redundant
	s.redundant = []string{agent}
	err := l.tidy(s)
	s.redundant = keep
	l.resolve(s)
	return err
}

// Relink 把断掉的链接改成指向技能库里的同名技能。
func (l *Library) Relink(path string) error {
	name := filepath.Base(path)
	if !l.inLibrary(name) {
		return errors.New(locales.IssueLibrarySkillMissing(name))
	}
	if l.disk {
		if err := l.removeBroken(path); err != nil {
			return err
		}
		dir := filepath.Dir(path)
		target, err := filepath.Rel(dir, filepath.Join(l.Root, name))
		if err != nil {
			target = filepath.Join(l.Root, name)
		}
		if err := makeLink(target, path); err != nil {
			return err
		}
	}
	l.Broken = slices.DeleteFunc(l.Broken, func(p string) bool { return p == path })
	return nil
}

// RemoveBroken 删掉断掉的链接。只删确实断了的链接。
func (l *Library) RemoveBroken(path string) error {
	if l.disk {
		if err := l.removeBroken(path); err != nil {
			return err
		}
	}
	l.Broken = slices.DeleteFunc(l.Broken, func(p string) bool { return p == path })
	return nil
}

func (l *Library) removeBroken(path string) error {
	_, err := os.Lstat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if !isLink(path) {
		return errors.New(locales.IssueNotALink(l.Display(path)))
	}
	if _, err := os.Stat(path); err == nil {
		return errors.New(locales.IssueLinkNotBroken(l.Display(path)))
	}
	return os.Remove(path)
}

// Unglobal 删掉全局目录里指向项目技能的链接：技能只在项目里用。
func (l *Library) Unglobal(s *Skill) error {
	if s.Source != Project {
		return nil
	}
	var errs []error
	if l.disk {
		for _, p := range s.globalLinks {
			if !isLink(p) {
				continue // 不是链接了，不碰
			}
			if err := os.Remove(p); err != nil {
				errs = append(errs, err)
			}
		}
	}
	if err := errors.Join(errs...); err != nil {
		return err
	}
	s.globalLinks, s.Enabled = nil, nil
	s.Reads = slices.DeleteFunc(s.Reads, func(r Read) bool { return r.Global })
	return nil
}

// GlobalDirs 是全局目录里指向项目技能的链接所在的目录（显示用）。
func (s *Skill) GlobalDirs() []string {
	var out []string
	for _, p := range s.globalLinks {
		if d := filepath.Dir(p); !slices.Contains(out, d) {
			out = append(out, d)
		}
	}
	return out
}

// SkillFile 是技能的 SKILL.md，「在编辑器中打开」用。
func (s *Skill) SkillFile() string {
	if strings.HasSuffix(s.Dir, "SKILL.md") {
		return s.Dir
	}
	return filepath.Join(s.Dir, "SKILL.md")
}
