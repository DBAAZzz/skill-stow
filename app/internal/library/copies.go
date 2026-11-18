package library

import (
	"cmp"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"skillmanager/locales"
)

// Copy 是「我的」技能在电脑上的一处：一个技能目录里的那一项。
type Copy struct {
	Dir     string   // 所在的技能目录
	Path    string   // 这一项本身（目录或链接）
	Real    string   // 解析链接后的真实目录
	Link    bool     // Path 是链接
	Nested  bool     // 整组链接里的一个，没有自己的那一项
	Owner   string   // 目录是哪个 Agent 的；技能库、早年的目录为空
	Readers []string // 装了的、会读这个目录的 Agent
}

// Version 是一种内容：内容完全相同的几处算一种。
type Version struct {
	Places   []Copy
	Real     string    // 代表这种内容的真实目录，技能库那份优先
	Modified time.Time // 里面的文件最近一次修改
	Shared   bool      // 技能库里的是这一种
	files    []File    // 示例数据的内容；真实数据从 Real 读
	hash     string
	custom   []Agent
}

// Agents 是读到这种内容的 Agent，去重，按 Catalog 的顺序。
func (v *Version) Agents() []string {
	var out []string
	for _, c := range v.Places {
		for _, id := range c.Readers {
			if !slices.Contains(out, id) {
				out = append(out, id)
			}
		}
	}
	slices.SortFunc(out, func(a, b string) int { return catalogIndex(v.custom, a) - catalogIndex(v.custom, b) })
	return out
}

// Versions 是技能的副本按内容分成的几种，基准在前：技能库里的那种，
// 不在技能库时是第一份的那种。没有副本时为空。
func (s *Skill) Versions() []Version { return s.versions }

// Identical 报告技能有副本，而且内容都一样。
func (s *Skill) Identical() bool { return s.Copies > 0 && len(s.versions) == 1 }

// CopyDirs 是同名副本的真实目录，不含这一份。
func (s *Skill) CopyDirs() []string {
	var out []string
	for _, v := range s.versions {
		for _, p := range v.Places {
			if p.Real != s.Real && !slices.Contains(out, p.Real) {
				out = append(out, p.Real)
			}
		}
	}
	return out
}

// Places 是技能在电脑上的全部几处（含链接到同一份的）。
func (s *Skill) Places() int {
	n := 0
	for _, v := range s.versions {
		n += len(v.Places)
	}
	return n
}

// LinkedFrom 是没有副本时，另有几处链接指向这同一份。
func (s *Skill) LinkedFrom() int { return max(len(s.places)-1, 0) }

// CopyIn 报告 Agent 读到的是另一份同名的副本（在它自己的目录里，或经
// 别的目录），不是这一份：不算启用，开关拨不动，收成一份后才能用。
func (s *Skill) CopyIn(agent string) bool { return slices.Contains(s.copied, agent) }

// Copied 是读到另一份副本的 Agent。
func (s *Skill) Copied() []string { return s.copied }

// findVersions 把技能的各处按内容分组。只在有副本时做，要读文件。
func (l *Library) findVersions(s *Skill) {
	s.versions = nil
	if s.Copies <= 0 || !l.disk {
		return
	}
	hashes := map[string]string{}
	byHash := map[string]int{}
	for _, p := range slices.Concat(s.places, s.elsewhere) {
		h, ok := hashes[p.real]
		if !ok {
			// 严格比：隐藏文件、缓存、执行位、链接目标都算。读不了的那份
			// 自成一种，不能说成和别的一样，要人来对比处理
			var err error
			if h, err = TreeHash(p.real); err != nil {
				h = "unreadable:" + p.real
			}
			hashes[p.real] = h
		}
		i, ok := byHash[h]
		if !ok {
			i = len(s.versions)
			byHash[h] = i
			s.versions = append(s.versions, Version{Real: p.real, Modified: modified(p.real), hash: h, custom: l.custom})
		}
		v := &s.versions[i]
		if p.dir == l.Root {
			v.Real, v.Shared = p.real, true
		}
		v.Places = append(v.Places, l.copyOf(p))
	}
	l.sortVersions(s)
}

// sortVersions 让基准排第一，其余按处数多少。
func (l *Library) sortVersions(s *Skill) {
	base := func(v Version) int {
		if v.Shared || !slices.ContainsFunc(s.versions, func(v Version) bool { return v.Shared }) && v.Real == s.Real {
			return 0
		}
		return 1
	}
	slices.SortStableFunc(s.versions, func(a, b Version) int {
		return cmp.Or(base(a)-base(b), len(b.Places)-len(a.Places))
	})
}

func (l *Library) copyOf(p place) Copy {
	return Copy{Dir: p.dir, Path: p.path, Real: p.real, Link: p.link, Nested: p.nested, Owner: l.dirOwner(p.dir), Readers: l.readers[p.dir]}
}

// DirOwner 是以这个目录为自己的技能目录的 Agent，没有时为空。
func (l *Library) DirOwner(dir string) string { return l.dirOwner(dir) }

// dirOwner 是以这个目录为自己的技能目录的 Agent（装没装都算）。技能库和
// 早年的目录没有主人。
func (l *Library) dirOwner(dir string) string {
	if dir == l.Root {
		return ""
	}
	for _, a := range slices.Concat(Catalog, l.custom) {
		if filepath.Join(l.Home, a.Skills) == dir {
			return a.ID
		}
	}
	return ""
}

// skipped 是不算技能内容的文件：隐藏文件、依赖、Python 运行脚本时自己
// 生成的缓存。缓存不跳过的话，Agent 跑过一次脚本的那份就成了「另一种内容」。
func skipped(name string) bool {
	return strings.HasPrefix(name, ".") || name == "node_modules" || name == "__pycache__"
}

// modified 是目录里文件最近一次修改的时间。
func modified(dir string) time.Time {
	var t time.Time
	filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || p != dir && skipped(d.Name()) {
			if err == nil && d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if fi, err := d.Info(); err == nil && !d.IsDir() && fi.ModTime().After(t) {
			t = fi.ModTime()
		}
		return nil
	})
	return t
}

// VersionFiles 是一种内容的文件，SKILL.md 在前。和阅读视图不同，隐藏
// 文件、缓存也列出来：对比副本要看到全部不同。
func (l *Library) VersionFiles(v *Version) []File {
	if !l.disk {
		return v.files
	}
	return readFilesStrict(v.Real)
}

// ——— 收成一份 ———

// Trash 把文件或目录移到废纸篓。默认实现只用于测试和非 macOS，app 启动时
// 换成系统 TrashItem（main.go:31，能「放回原处」）；默认实现用 os.Rename
// 移进主目录的 .Trash，跨卷 rename 会失败。
var Trash = func(home, path string) error {
	dir := filepath.Join(home, ".Trash")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	dst := filepath.Join(dir, filepath.Base(path))
	for i := 2; ; i++ {
		if _, err := os.Lstat(dst); errors.Is(err, fs.ErrNotExist) {
			break
		}
		dst = filepath.Join(dir, fmt.Sprintf("%s %d", filepath.Base(path), i))
	}
	return os.Rename(path, dst)
}

// MergeResult 是收成一份做了什么。
type MergeResult struct {
	Linked  int // 换成指向技能库的链接
	Trashed int // 拷贝移到废纸篓
	Dropped int // 删掉的多余链接
	Kept    int // 没动：早年的目录、整组链接里的
}

// Add 把 o 记进 r：一次收好几个技能时合计。
func (r *MergeResult) Add(o MergeResult) {
	r.Linked += o.Linked
	r.Trashed += o.Trashed
	r.Dropped += o.Dropped
	r.Kept += o.Kept
}

// String 是一句话的结果，如「5 处换成链接，10 处移到废纸篓，2 处没动。」
func (r MergeResult) String() string {
	var parts []string
	if r.Linked > 0 {
		parts = append(parts, locales.CopiesMergeLinked(r.Linked))
	}
	if r.Trashed > 0 {
		parts = append(parts, locales.CopiesMergeTrashed(r.Trashed))
	}
	if r.Dropped > 0 {
		parts = append(parts, locales.CopiesMergeDropped(r.Dropped))
	}
	if r.Kept > 0 {
		parts = append(parts, locales.CopiesMergeKept(r.Kept))
	}
	if len(parts) == 0 {
		return ""
	}
	return locales.SentenceEnd(strings.Join(parts, locales.ClauseSeparator()))
}

// 收成一份时对一处做什么。
const (
	stepNone  = iota // 已经是指向技能库的链接
	stepLink         // 换成指向技能库的链接：主人不读技能库
	stepTrash        // 移到废纸篓：主人本来就读技能库
	stepKeep         // 不动：没有主人的目录，或整组链接里的
)

// mergeStep 是收成一份时对技能库以外的一处做什么。
func (l *Library) mergeStep(c Copy, shared string) int {
	switch {
	case c.Real == shared && c.Link:
		return stepNone
	case c.Nested || c.Owner == "":
		return stepKeep
	}
	if a, ok := l.agentByID(c.Owner); ok && slices.Contains(a.dirs(), sharedDir) {
		return stepTrash
	}
	return stepLink
}

// CanMerge 报告收成一份会改动什么：剩下的副本都在动不了的地方时，
// 点了也没用。
func (l *Library) CanMerge(s *Skill) bool {
	if len(s.versions) == 0 {
		return false
	}
	if !slices.ContainsFunc(s.versions, func(v Version) bool { return v.Shared }) || len(s.versions) > 1 {
		return true
	}
	shared := s.versions[0].Real
	for _, v := range s.versions {
		for _, c := range v.Places {
			if c.Dir != l.Root {
				if st := l.mergeStep(c, shared); st == stepLink || st == stepTrash {
					return true
				}
			}
		}
	}
	return false
}

// Merge 把技能的副本收成一份，留下第 keep 种内容（Versions 的下标）：
// 它放进技能库（技能库原来那份不一样时，原来那份移到废纸篓）；各 Agent
// 目录里的副本，主人不读技能库的换成指向技能库的链接，读的移到废纸篓；
// 早年的目录和整组链接里的不动。移走的都进废纸篓，不直接删除。
func (l *Library) Merge(s *Skill, keep int) (_ MergeResult, err error) {
	defer l.track("tidy", "merge", s.Name)(&err)
	var r MergeResult
	if s.Source != Mine || keep < 0 || keep >= len(s.versions) {
		return r, errors.New(locales.CopiesNothingToMerge())
	}
	release, err := lockSkill(s.Name)
	if err != nil {
		return r, err
	}
	defer release()
	kept := s.versions[keep]
	lib := filepath.Join(l.Root, s.Name)
	var libCopy *Copy
	for _, v := range s.versions {
		for i := range v.Places {
			if v.Places[i].Dir == l.Root {
				libCopy = &v.Places[i]
			}
		}
	}
	if libCopy != nil {
		lib = libCopy.Path
		if libCopy.Nested && !kept.Shared {
			return r, errors.New(locales.CopiesGroupedSkillProtected(s.Name))
		}
	}
	shared := kept.Real
	if kept.Shared {
		shared = libCopy.Real
	}

	if !l.disk {
		for _, v := range s.versions {
			for _, c := range v.Places {
				if c.Dir == l.Root {
					if !v.Shared || v.hash != kept.hash {
						r.count(c)
					}
					continue
				}
				switch l.mergeStep(c, shared) {
				case stepLink:
					r.Linked++
				case stepTrash:
					r.count(c)
				case stepKeep:
					r.Kept++
				}
			}
		}
		s.Copies, s.versions, s.copied, s.Shared = 0, nil, nil, true
		return r, nil
	}

	// 先把要留的内容放进技能库，再动别处：要留的那份可能就在别处
	if !kept.Shared {
		if libCopy != nil {
			if err := l.remove(*libCopy); err != nil {
				return r, err
			}
			r.count(*libCopy)
		}
		if err := copyDir(kept.Real, lib); err != nil {
			return r, err
		}
		shared = lib
	}
	if real, err := realPath(lib); err == nil {
		shared = real
	}

	var errs []error
	for _, v := range s.versions {
		for _, c := range v.Places {
			if c.Dir == l.Root {
				continue
			}
			if real, err := realPath(c.Path); err == nil && real == shared {
				continue // 已经指向技能库
			}
			switch l.mergeStep(c, shared) {
			case stepKeep:
				r.Kept++
			case stepTrash:
				if err := l.remove(c); err != nil {
					errs = append(errs, err)
					continue
				}
				r.count(c)
			case stepLink:
				if err := l.remove(c); err != nil {
					errs = append(errs, err)
					continue
				}
				target, err := filepath.Rel(c.Dir, lib)
				if err != nil {
					target = lib
				}
				if err := makeLink(target, c.Path); err != nil {
					errs = append(errs, err)
					continue
				}
				r.Linked++
			}
		}
	}
	return r, errors.Join(errs...)
}

// count 记下移走了一处：拷贝进了废纸篓，链接是删掉。
func (r *MergeResult) count(c Copy) {
	if c.Link {
		r.Dropped++
	} else {
		r.Trashed++
	}
}

// remove 移走一处：链接直接删掉（只删链接本身，不碰它指向的目录），
// 拷贝移到废纸篓。
func (l *Library) remove(c Copy) error {
	if isLink(c.Path) {
		return os.Remove(c.Path)
	}
	if err := Trash(l.Home, c.Path); err != nil {
		return fmt.Errorf("%s%w", locales.CopiesTrashFailed(l.Display(c.Path)), err)
	}
	return nil
}

// copyDir 把 src 目录原样复制到 dst（dst 不能已经存在）。里面的链接照抄成链接。
func copyDir(src, dst string) error {
	if _, err := os.Lstat(dst); err == nil {
		return errors.New(locales.CopiesDestinationExists(dst))
	}
	return filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, p)
		to := filepath.Join(dst, rel)
		info, err := d.Info()
		if err != nil {
			return err
		}
		switch {
		case isLink(p):
			target, err := os.Readlink(p)
			if err != nil {
				return err
			}
			return makeLink(target, to)
		case d.IsDir():
			return os.MkdirAll(to, info.Mode().Perm()|0o700)
		}
		in, err := os.Open(p)
		if err != nil {
			return err
		}
		defer in.Close()
		out, err := os.OpenFile(to, os.O_CREATE|os.O_WRONLY|os.O_EXCL, info.Mode().Perm())
		if err != nil {
			return err
		}
		if _, err := io.Copy(out, in); err != nil {
			out.Close()
			return err
		}
		if err := out.Close(); err != nil {
			return err
		}
		return os.Chtimes(to, info.ModTime(), info.ModTime())
	})
}
