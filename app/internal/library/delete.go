package library

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"skillmanager/locales"
)

var (
	// ErrHasCopies：电脑上还有同名副本，删一份别的 Agent 仍会读到另一份。
	ErrHasCopies = textErr(locales.DeleteHasCopies)
	// ErrUndoExpired：这次删除已经放下了，为撤销留的那份已清掉。
	ErrUndoExpired = textErr(locales.DeleteUndoExpired)

	errReadOnlyDelete = textErr(locales.DeleteReadOnly)
)

// ImpactKind 是删除后一个 Agent 读不到技能的原因。
type ImpactKind int

const (
	ImpactLink     ImpactKind = iota + 1 // 删掉它目录里指向技能的链接
	ImpactShared                         // 它读的目录里原件移走了（如技能库）
	ImpactOriginal                       // 原件就在它自己的目录里
)

// Impact 是删除一个技能对一个 Agent 的影响。
type Impact struct {
	Agent string
	Kind  ImpactKind
	Dir   string // ImpactShared 时它读的目录
}

// Deletion 记着一次删除做了什么，撤销时按它放回去。
type Deletion struct {
	Names []string // 删掉的技能，按删除的顺序
	items []*deleted
	spent bool // 撤销过或放下了
}

// deleted 是一次删除里的一个技能。
type deleted struct {
	skill    *Skill  // 删除前的样子：撤销时按它写回停用记录、放回列表
	at       listing // 在列表和集合里的位置
	stage    string  // 撤销用的那份所在的临时目录；示例数据为空
	links    []removedLink
	lock     *string      // npx skills 安装记录里它那一条的原文，没有为 nil
	marks    []importMark // 「添加技能」记下的来源（import_add.go），撤销时写回
	disabled []string     // 本应用停用过它的 Agent：撤销时再停用
	restored bool         // 这项已经撤销放回
}

type removedLink struct{ path, target string }

// RealOutside 报告 s 的真实目录是否落在任何已知技能目录（技能库、各 Agent 目录、
// 早年目录）之外；在外时返回真实目录路径。
func (l *Library) RealOutside(s *Skill) (string, bool) {
	if s == nil || s.Real == "" {
		return "", false
	}
	real := filepath.Clean(s.Real)
	if r, err := realPath(real); err == nil {
		real = r
	}
	homes := []string{l.Home}
	if l.Home != "" {
		if rh, err := realPath(l.Home); err == nil && rh != l.Home {
			homes = append(homes, rh)
		}
	}
	var dirs []string
	if l.Root != "" {
		dirs = append(dirs, l.Root)
	}
	joinHome := func(home, d string) string {
		if filepath.IsAbs(d) {
			return d
		}
		return filepath.Join(home, d)
	}
	for _, home := range homes {
		if home == "" {
			continue
		}
		dirs = append(dirs, filepath.Join(home, libraryDir))
		for _, a := range slices.Concat(Catalog, l.custom) {
			for _, d := range a.dirs() {
				dirs = append(dirs, joinHome(home, d))
			}
		}
		for _, d := range legacyDirs {
			dirs = append(dirs, joinHome(home, d))
		}
	}
	for _, dir := range dirs {
		if isUnderDir(real, filepath.Clean(dir)) {
			return "", false
		}
	}
	return s.Real, true
}

func isUnderDir(p, dir string) bool {
	rel, err := filepath.Rel(dir, p)
	if err != nil {
		return false
	}
	return rel != "." && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// CanDelete 报告技能能不能删除，不能时返回原因：只有「我的」技能能删；
// 有同名副本时返回 ErrHasCopies，要先收成一份。
func (l *Library) CanDelete(s *Skill) error {
	switch {
	case s.ReadOnly():
		return errReadOnlyDelete
	case s.Copies > 0:
		return ErrHasCopies
	}
	return nil
}

// DeleteImpact 是删除 s 后读不到它的 Agent，各自因为什么，按 Agent 的顺序。
func (l *Library) DeleteImpact(s *Skill) []Impact {
	agents := s.Available
	if len(agents) == 0 {
		agents = s.Enabled // 示例数据没有 Available
	}
	var out []Impact
	for _, id := range agents {
		im := Impact{Agent: id, Kind: ImpactLink}
		switch p := s.Links[id]; {
		case p != "":
			if l.disk && !isLink(p) {
				im.Kind = ImpactOriginal
			}
		case s.Via[id] != "":
			im.Kind, im.Dir = ImpactShared, s.Via[id]
		}
		out = append(out, im)
	}
	return out
}

// Delete 删除这些技能：原件移到废纸篓，删掉指向它的链接，去掉 npx skills 的
// 安装记录和本应用写进 Agent 配置的停用记录。有一个不能删就一个都不删。
// 原件移进废纸篓之后的步骤出错时，照样返回 Deletion，可以撤销。
func (l *Library) Delete(skills ...*Skill) (_ *Deletion, err error) {
	defer l.track("delete", "delete", skillNames(skills))(&err)
	var names []string
	for _, s := range skills {
		if err := l.CanDelete(s); err != nil {
			return nil, fmt.Errorf("%s%w", locales.LibrarySkillErrorPrefix(s.Name), err)
		}
		names = append(names, s.Name)
	}
	release, err := holdSkills(names)
	if err != nil {
		return nil, err
	}
	defer release()
	d := &Deletion{}
	var errs []error
	for _, s := range skills {
		item, err := l.deleteOne(s)
		if item != nil {
			d.items = append(d.items, item)
			d.Names = append(d.Names, s.Name)
		}
		if err != nil {
			errs = append(errs, fmt.Errorf("%s%w", locales.LibrarySkillErrorPrefix(s.Name), err))
		}
	}
	if len(d.items) == 0 {
		return nil, errors.Join(errs...)
	}
	return d, errors.Join(errs...)
}

// deleteOne 删一个技能。原件没能移进废纸篓时什么都不改，返回 nil。
func (l *Library) deleteOne(s *Skill) (*deleted, error) {
	if !l.disk {
		return &deleted{skill: s, at: l.unlist(s)}, nil
	}
	links := l.linksTo(s)
	disabled := l.ownDisables(s)
	// 先留一份撤销用的，再移原件：mygo 不告诉移到了废纸篓的哪里，
	// macOS 也不让普通应用读废纸篓，撤销只能用自己留的这份
	stage, err := os.MkdirTemp("", "skill-manager-undo-*")
	if err != nil {
		return nil, err
	}
	if err := copyDir(s.Real, filepath.Join(stage, s.Name)); err != nil {
		os.RemoveAll(stage)
		return nil, err
	}
	if err := Trash(l.Home, s.Real); err != nil {
		os.RemoveAll(stage)
		return nil, fmt.Errorf("%s%w", locales.DeleteTrashFailed(), err)
	}

	item := &deleted{skill: s, stage: stage, disabled: disabled}
	var errs []error
	for _, ln := range links {
		if err := os.Remove(ln.path); err != nil {
			errs = append(errs, err)
			continue
		}
		item.links = append(item.links, ln)
	}
	places := []string{s.Real, s.Dir}
	for _, p := range s.places {
		places = append(places, p.path)
	}
	marks, err := l.dropImportMarks(places)
	item.marks = marks
	if err != nil {
		errs = append(errs, fmt.Errorf("%s%w", locales.DeleteAddRecordRemovalFailed(), err))
	}
	if s.Shared {
		raw, err := dropInstallRecord(l.Home, s.Name)
		item.lock = raw
		if err != nil {
			errs = append(errs, fmt.Errorf("%s%w", locales.DeleteInstallRecordRemovalFailed(), err))
		}
	}
	for _, id := range disabled {
		if err := l.setNative(s, id, true); err != nil {
			errs = append(errs, fmt.Errorf("%s%w", locales.DeleteDisabledRecordRemovalFailed(AgentName(id)), err))
		}
	}
	item.at = l.unlist(s)
	return item, errors.Join(errs...)
}

// linksTo 是全局目录里指向 s 原件的链接。整组链接里的那一项不算：它不是
// 自己的链接，删了会删到整组。
func (l *Library) linksTo(s *Skill) []removedLink {
	var out []removedLink
	for _, p := range slices.Concat(s.places, s.elsewhere) {
		if p.nested || p.path == s.Real || !isLink(p.path) {
			continue
		}
		if real, err := realPath(p.path); err != nil || real != s.Real {
			continue
		}
		if target, err := os.Readlink(p.path); err == nil {
			out = append(out, removedLink{p.path, target})
		}
	}
	return out
}

// ownDisables 是本应用停用过 s 的 Agent：用户自己在配置里写的停用不算。
func (l *Library) ownDisables(s *Skill) []string {
	l.loadNativeData()
	var out []string
	for _, id := range s.Available {
		if !nativeSupported(id) || s.State(id).Enabled {
			continue
		}
		name, path := s.nativeTarget(id)
		if _, ours := l.nativeData.Restore[nativeKey(id, name, path)]; ours {
			out = append(out, id)
		}
	}
	return out
}

// Undo 把一次删除放回去，后删的先放。原位置已经有同名的就不覆盖，报错。
// 部分失败时只标记已放回的项，再次 Undo 只重试未放回的项。
func (l *Library) Undo(d *Deletion) (err error) {
	defer l.track("delete", "undo", "")(&err)
	if d.spent {
		return ErrUndoExpired
	}
	var errs []error
	allDone := true
	for i := len(d.items) - 1; i >= 0; i-- {
		item := d.items[i]
		if item.restored {
			continue
		}
		if err := l.undoOne(item); err != nil {
			errs = append(errs, fmt.Errorf("%s%w", locales.LibrarySkillErrorPrefix(item.skill.Name), err))
			allDone = false
		} else {
			item.restored = true
		}
	}
	if allDone {
		d.spent = true
	}
	return errors.Join(errs...)
}

func (l *Library) undoOne(item *deleted) error {
	s := item.skill
	if !l.disk {
		l.relist(s, item.at)
		return nil
	}
	kept := filepath.Join(item.stage, s.Name)
	if !exists(kept) {
		return ErrUndoExpired
	}
	if exists(s.Real) {
		return errors.New(locales.DeleteRestoreNameExists(l.Display(s.Real)))
	}
	if err := os.MkdirAll(filepath.Dir(s.Real), 0o755); err != nil {
		return err
	}
	if err := os.Rename(kept, s.Real); err != nil {
		// 临时目录在别的卷上时改名不了，复制回去
		if err := copyDir(kept, s.Real); err != nil {
			return err
		}
	}
	os.RemoveAll(item.stage)
	l.relist(s, item.at)

	var errs []error
	for _, ln := range item.links {
		if exists(ln.path) {
			errs = append(errs, errors.New(locales.DeleteRestoreLinkNameExists(l.Display(ln.path))))
			continue
		}
		if err := makeLink(ln.target, ln.path); err != nil {
			errs = append(errs, err)
		}
	}
	if item.lock != nil {
		if err := restoreInstallRecord(l.Home, s.Name, *item.lock); err != nil {
			errs = append(errs, fmt.Errorf("%s%w", locales.DeleteInstallRecordRestoreFailed(), err))
		}
	}
	for _, id := range item.disabled {
		if err := l.setNative(s, id, false); err != nil {
			errs = append(errs, fmt.Errorf("%s%w", locales.DeleteDisableAgainFailed(AgentName(id)), err))
		}
	}
	if err := l.restoreImportMarks(item.marks); err != nil {
		errs = append(errs, fmt.Errorf("%s%w", locales.DeleteAddRecordRestoreFailed(), err))
	}
	return errors.Join(errs...)
}

// Discard 放下这次删除：清掉为撤销留的那份，之后不能再撤销。
func (d *Deletion) Discard() {
	for _, item := range d.items {
		if item.stage != "" {
			os.RemoveAll(item.stage)
		}
	}
	d.spent = true
}

func exists(p string) bool {
	_, err := os.Lstat(p)
	return err == nil
}

// ——— 列表里的位置 ———

// listing 是技能在列表和集合里的位置，放回时用。
type listing struct {
	index     int
	coll      *Collection
	collIndex int // 集合因为只剩一个而撤掉时，它在 Collections 里的位置；没撤为 -1
}

// unlist 把技能从列表和它所在的集合里拿掉。集合只剩一个技能时不再算集合，
// 和扫描时的规则一样。
func (l *Library) unlist(s *Skill) listing {
	at := listing{index: slices.Index(l.Skills, s), coll: s.Collection, collIndex: -1}
	if at.index >= 0 {
		l.Skills = slices.Delete(l.Skills, at.index, at.index+1)
	}
	if c := s.Collection; c != nil {
		c.Skills = slices.DeleteFunc(c.Skills, func(k *Skill) bool { return k == s })
		if len(c.Skills) < 2 {
			at.collIndex = slices.Index(l.Collections, c)
			l.Collections = slices.DeleteFunc(l.Collections, func(k *Collection) bool { return k == c })
			for _, k := range c.Skills {
				k.Collection = nil
			}
		}
	}
	return at
}

// relist 把 unlist 拿掉的技能放回原来的位置。
func (l *Library) relist(s *Skill, at listing) {
	if at.index >= 0 {
		l.Skills = slices.Insert(l.Skills, min(at.index, len(l.Skills)), s)
	}
	c := at.coll
	if c == nil {
		return
	}
	c.Skills = append(c.Skills, s)
	slices.SortFunc(c.Skills, func(a, b *Skill) int { return strings.Compare(a.Name, b.Name) })
	if at.collIndex >= 0 {
		l.Collections = slices.Insert(l.Collections, min(at.collIndex, len(l.Collections)), c)
	}
	for _, k := range c.Skills {
		k.Collection = c
	}
}

// ——— npx skills 的安装记录 ———

// dropInstallRecord 从安装记录里去掉 name 这一条，只动这一条。返回去掉的
// 原文；没有安装记录或没有这一条时返回 nil。
func dropInstallRecord(home, name string) (*string, error) {
	recordMu.Lock()
	defer recordMu.Unlock()
	src, err := os.ReadFile(lockFile(home))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	path := []string{"skills", name}
	if err := writableRecords(src); err != nil {
		return nil, err
	}
	doc, err := parseJSON(string(src))
	if err != nil {
		return nil, err
	}
	raw, err := doc.raw(path)
	if raw == nil || err != nil {
		return nil, err
	}
	out, err := editJSON(string(src), path, nil)
	if err != nil {
		return nil, err
	}
	return raw, atomicConfigWrite(lockFile(home), out)
}

// restoreInstallRecord 把去掉的那一条写回。npx skills 又记了一条时不覆盖。
func restoreInstallRecord(home, name, raw string) error {
	recordMu.Lock()
	defer recordMu.Unlock()
	src, err := os.ReadFile(lockFile(home))
	if errors.Is(err, fs.ErrNotExist) {
		src = []byte("{\n  \"version\": 3,\n  \"skills\": {}\n}\n")
	} else if err != nil {
		return err
	}
	path := []string{"skills", name}
	if err := writableRecords(src); err != nil {
		return err
	}
	doc, err := parseJSON(string(src))
	if err != nil {
		return err
	}
	if current, err := doc.raw(path); current != nil || err != nil {
		return err
	}
	out, err := editJSON(string(src), path, &raw)
	if err != nil {
		return err
	}
	return atomicConfigWrite(lockFile(home), out)
}

// writableRecords 报告这份记录原文能不能写：只写 version 3。
func writableRecords(src []byte) error {
	recs, err := parseRecords("", string(src))
	if err != nil {
		return ErrRecordUnusable
	}
	return recs.Writable()
}
