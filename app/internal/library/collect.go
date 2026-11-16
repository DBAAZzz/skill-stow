package library

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"

	"skillmanager/locales"
)

// KeepLinks 记下这些全局链接是有意建的：不再算作需要处理，链接不动。
func (l *Library) KeepLinks(s *Skill) error {
	if !slices.Contains(l.Settings.KeptLinks, s.Real) {
		l.Settings.KeptLinks = append(l.Settings.KeptLinks, s.Real)
	}
	s.KeptLinks = true
	return l.SaveSettings()
}

// CollectGains 是收进技能库后会多读到这个技能的 Agent（直接读技能库、
// 原来没有全局链接到它的）。
func (l *Library) CollectGains(s *Skill) []string {
	var out []string
	for _, id := range l.readers[l.Root] {
		if !s.EnabledIn(id) {
			out = append(out, id)
		}
	}
	return out
}

// CollectTarget 是收进技能库后这份技能的位置。
func (l *Library) CollectTarget(s *Skill) string { return filepath.Join(l.Root, s.Name) }

// Collect 把项目技能复制进技能库，再把全局链接改指向那份；项目里的原件不动。
// 技能库里已有同名项时拒绝。调用方随后重新扫描。
func (l *Library) Collect(s *Skill) error {
	if s.Source != Project || len(s.globalLinks) == 0 {
		return errors.New(locales.CollectRequiresGlobalLink())
	}
	if !l.disk {
		return errors.New(locales.CollectSampleUnavailable())
	}
	dst := l.CollectTarget(s)
	if _, err := os.Lstat(dst); err == nil {
		return errors.New(locales.CollectNameExists(s.Name))
	}
	if err := os.MkdirAll(l.Root, 0o755); err != nil {
		return err
	}
	// 先复制到同目录的临时位置再改名：中途失败不会在技能库里留下半份
	tmp, err := os.MkdirTemp(l.Root, ".collect-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)
	staged := filepath.Join(tmp, s.Name)
	if err := copyDir(s.Real, staged); err != nil {
		return err
	}
	if err := os.Rename(staged, dst); err != nil {
		return err
	}
	real, err := realPath(dst)
	if err != nil {
		return err
	}
	var errs []error
	for _, p := range s.globalLinks {
		errs = append(errs, repoint(p, real))
	}
	return errors.Join(errs...)
}

// repoint 把链接 p 改指向 real；p 已经不是链接时不碰。macOS 上是一次原子
// 改名；Windows 上要先删旧链接，中间有一段 p 不存在的窗口。
func repoint(p, real string) error {
	if !isLink(p) {
		return nil
	}
	dir := filepath.Dir(p)
	from := dir
	if r, err := realPath(dir); err == nil {
		from = r
	}
	target, err := filepath.Rel(from, real)
	if err != nil {
		target = real
	}
	tmp := p + ".skill-manager-tmp"
	os.Remove(tmp)
	if err := makeLink(target, tmp); err != nil {
		return err
	}
	if os.Rename(tmp, p) == nil {
		return nil
	}
	// Windows 上改名不能覆盖已有的目录项（符号链接和目录联接都算）：只能先删
	// 旧链接再放新的。旧的删掉后改名还失败，新链接就是唯一的入口，留着它，
	// 让用户能手动改回去。
	if err := os.Remove(p); err != nil {
		os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, p); err != nil {
		return fmt.Errorf("%s%w", locales.CollectLinkRenameFailed(p, tmp), err)
	}
	return nil
}
