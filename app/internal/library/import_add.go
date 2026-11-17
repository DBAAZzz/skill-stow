package library

import (
	"cmp"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"slices"

	"strings"
	"syscall"
	"time"

	"skillmanager/locales"
)

// 添加：只在本机复制。每个技能独立：再查一次同名 → 复制到暂存并核对 →
// 不覆盖地改名进目标位置 → 建链接 → 写安装记录（技能库模式）和应用记录。
// 任何一步失败，撤掉这个技能已经做的，不影响同批其他技能。
//
// 替换：用户看过已有的同名项、确认要换掉时才做。核对已有的还是确认时的样子 →
// 新版和目标位置上的原子交换 → 链接位置上的旧链接挪开再建 → 写记录 → 最后
// 把换下来的移到废纸篓。移不进废纸篓就整个换回去，不会只换一半。

// ImportPlacement 是装到哪里。
type ImportPlacement struct {
	// Library：放进技能库 ~/.agents/skills，Agents 是另外建链接的 Agent。
	// 否则是「指定的 Agent」：文件放进 Agents[0] 自己的目录，其余建链接。
	Library bool
	Agents  []string
}

// ImportState 是一个技能相对装到的位置是什么情况。
type ImportState int

const (
	ImportNew      ImportState = iota + 1 // 目标位置没有同名，能添加
	ImportSame                            // 已经有，内容一样：已是最新
	ImportConflict                        // 已有同名，内容或来源不同：不覆盖，用户确认后可替换
	ImportInvalid                         // 不合规矩（名称、大小、链接……）
)

// ImportCheck 是检查结果；Existing 是已有的那一项。
//
// 冲突时 Replaces 是替换会移到废纸篓的各项（目标位置在前）；已经指向目标
// 位置的链接不在里面，替换后照样指向新版。Blocked 非空时不能替换，是原因。
// 要替换就把这次检查原样交给 AddImport：添加前会核对这些项还是检查时的样子。
type ImportCheck struct {
	State    ImportState
	Existing string
	Replaces []ImportExisting
	Blocked  string
	stamp    string // Replaces 里各项检查时的样子
}

// ImportExisting 是替换时要移到废纸篓的一项。Link 为真时它是链接：只移走
// 链接本身，它指向的内容不动。
type ImportExisting struct {
	Path string
	Link bool
}

// SameAs 报告两次检查看到的已有项是不是同一批、同一个样子：用户确认替换后
// 重新检查时，变了就要重新确认。
func (c ImportCheck) SameAs(o ImportCheck) bool {
	return c.State == o.State && c.Existing == o.Existing && c.Blocked == o.Blocked && c.stamp == o.stamp &&
		slices.Equal(c.Replaces, o.Replaces)
}

// importLink 是要建的一个链接。
type importLink struct{ agent, path string }

// importTargets 是技能 name 放在哪里、要建哪些链接。
func (l *Library) importTargets(name string, p ImportPlacement) (target string, links []importLink, err error) {
	if p.Library {
		target = filepath.Join(l.Root, name)
	} else {
		if len(p.Agents) == 0 {
			return "", nil, errors.New(locales.ImportChooseAgent())
		}
		a, ok := l.agentByID(p.Agents[0])
		if !ok {
			return "", nil, errors.New(locales.ImportUnknownAgentArgument(p.Agents[0]))
		}
		target = filepath.Join(l.Home, a.Skills, name)
	}
	seen := []string{filepath.Dir(target)}
	for i, id := range p.Agents {
		if !p.Library && i == 0 {
			continue
		}
		a, ok := l.agentByID(id)
		if !ok {
			return "", nil, errors.New(locales.ImportUnknownAgentId(id))
		}
		dir := filepath.Join(l.Home, a.Skills)
		if slices.Contains(seen, dir) {
			continue
		}
		seen = append(seen, dir)
		links = append(links, importLink{id, filepath.Join(dir, name)})
	}
	return target, links, nil
}

// CheckImport 查一个技能装到 p 时的情况。
func (l *Library) CheckImport(sk *ImportSkill, p ImportPlacement) ImportCheck {
	if sk.Problem != "" {
		return ImportCheck{State: ImportInvalid}
	}
	target, links, err := l.importTargets(sk.Name, p)
	if err != nil {
		return ImportCheck{State: ImportNew}
	}
	if _, err := os.Lstat(target); err == nil {
		real, err := realPath(target)
		if err == nil && sk.Tree != "" {
			if h, err := TreeHash(real); err == nil && h == sk.Tree {
				return ImportCheck{State: ImportSame, Existing: target}
			}
		}
		return l.conflictCheck(target, target, links)
	}
	for _, k := range links {
		if _, err := os.Lstat(k.path); err == nil {
			return l.conflictCheck(k.path, target, links)
		}
	}
	return ImportCheck{State: ImportNew}
}

// conflictCheck 是已有同名时的检查结果：替换要换掉哪些、能不能换，以及它们
// 现在的样子。
func (l *Library) conflictCheck(existing, target string, links []importLink) ImportCheck {
	c := ImportCheck{State: ImportConflict, Existing: existing}
	var stamp []string
	add := func(p string) {
		c.Replaces = append(c.Replaces, ImportExisting{Path: p, Link: isLink(p)})
		f, err := fingerprint(p)
		if err != nil && c.Blocked == "" {
			// 认不清它现在的样子，就没法保证确认之后它没被改过
			c.Blocked = locales.ImportReplaceUnreadable(l.Display(p))
		}
		stamp = append(stamp, f)
	}
	if exists(target) {
		add(target)
	}
	for _, k := range links {
		if !exists(k.path) || pointsAt(k.path, target) {
			continue
		}
		if !isLink(k.path) && c.Blocked == "" {
			// Agent 自己目录里的一份不归这次添加管，替换不能顺手扔掉它
			c.Blocked = locales.ImportReplaceOwnCopy(AgentName(k.agent), l.Display(k.path))
		}
		add(k.path)
	}
	if !canSwapDirectories {
		c.Blocked = locales.ImportReplaceMacOnly()
	}
	c.stamp = strings.Join(stamp, "\n")
	return c
}

// fingerprint 是 p 现在的样子：链接看指向哪里，目录和文件看内容。读不全时
// 返回错误，不拿大小、修改时间凑数：原地改了内容它们可能都不变。
func fingerprint(p string) (string, error) {
	fi, err := os.Lstat(p)
	switch {
	case err != nil:
		return "", err
	case isLink(p):
		to, err := os.Readlink(p)
		return "L " + to, err
	case fi.IsDir():
		h, err := TreeHash(p)
		return "D " + h, err
	case fi.Mode().IsRegular():
		b, err := os.ReadFile(p)
		return fmt.Sprintf("F %x", sha256.Sum256(b)), err
	}
	return "", fmt.Errorf("%s: %v", p, fi.Mode().Type())
}

// pointsAt 报告 link 是不是直接指向 target 这个位置（只看第一跳：target
// 自己是链接时，指向 target 的才算，指向 target 所指内容的不算）。
func pointsAt(link, target string) bool {
	to, err := os.Readlink(link)
	if err != nil {
		return false
	}
	if !filepath.IsAbs(to) {
		// 相对路径相对链接所在的真实目录解释（目录本身可能是链接）
		dir := filepath.Dir(link)
		if r, err := realPath(dir); err == nil {
			dir = r
		}
		to = filepath.Join(dir, to)
	}
	return samePlace(underRealDir(to), underRealDir(target))
}

// underRealDir 把 p 的上级目录换成真实路径，p 本身是链接也不解开。
func underRealDir(p string) string {
	if dir, err := realPath(filepath.Dir(p)); err == nil {
		return filepath.Join(dir, filepath.Base(p))
	}
	return filepath.Clean(p)
}

// ImportTargetDir 是技能 name 装到 p 时文件所在的目录。
func (l *Library) ImportTargetDir(name string, p ImportPlacement) string {
	t, _, _ := l.importTargets(name, p)
	return t
}

// ImportReaders 是装到 p 后谁会读到：chosen 是直接读到或用户选的，also 是
// 本来就读这些目录、顺带也会读到的（不能单独排除）。都只列本机装了的 Agent。
func (l *Library) ImportReaders(p ImportPlacement) (chosen, also []string) {
	installed := func(id string) bool { return slices.ContainsFunc(l.Agents, func(a Agent) bool { return a.ID == id }) }
	if p.Library {
		chosen = slices.Clone(l.SharedReaders())
		for _, id := range p.Agents {
			if installed(id) && !slices.Contains(chosen, id) {
				chosen = append(chosen, id)
			}
		}
		return chosen, nil
	}
	for _, id := range p.Agents {
		if installed(id) && !slices.Contains(chosen, id) {
			chosen = append(chosen, id)
		}
	}
	for _, id := range p.Agents {
		a, ok := l.agentByID(id)
		if !ok {
			continue
		}
		for _, r := range l.DirReaders(filepath.Join(l.Home, a.Skills)) {
			if !slices.Contains(chosen, r) && !slices.Contains(also, r) {
				also = append(also, r)
			}
		}
	}
	return chosen, also
}

// ImportLinkable 是技能库模式下能另外建链接的 Agent：本机装了、不直接读技能库的。
func (l *Library) ImportLinkable() []string {
	var out []string
	for _, a := range l.Agents {
		if !l.ReadsShared(a.ID) && a.Skills != sharedDir {
			out = append(out, a.ID)
		}
	}
	return out
}

// ImportPlaceable 是「指定的 Agent」模式能选的 Agent：本机装了、有自己目录的
// （技能目录就是技能库的 Agent 不列，选它等于放进技能库）。
func (l *Library) ImportPlaceable() []string {
	var out []string
	for _, a := range l.Agents {
		if a.Skills != sharedDir {
			out = append(out, a.ID)
		}
	}
	return out
}

// AgentSkillsDir 是 Agent 自己的全局技能目录（绝对路径）。
func (l *Library) AgentSkillsDir(id string) string {
	if a, ok := l.agentByID(id); ok {
		return filepath.Join(l.Home, a.Skills)
	}
	return ""
}

// InstalledIn 是更新条目所在的 Agent：「指定的 Agent」模式装进 Agent 目录
// 的技能返回那个 Agent 的名称；技能库里的返回空。
func (l *Library) InstalledIn(it UpdateItem) string {
	if it.Record.Place == "" {
		return ""
	}
	for _, a := range l.Agents {
		if a.Skills != "" && samePlace(filepath.Dir(it.Record.Place), filepath.Join(l.Home, a.Skills)) {
			return AgentName(a.ID)
		}
	}
	return filepath.Base(filepath.Dir(filepath.Dir(it.Record.Place)))
}

// ImportOutcome 是一个技能的添加结果。
type ImportOutcome struct {
	Name     string
	Added    bool
	Replaced bool   // 换掉了已有的同名项，旧的在废纸篓
	Place    string // 添加后文件所在的目录
	Conflict string // 按下添加时目标位置已有同名，这是已有的那一项
	Stale    bool   // Conflict 是确认替换后已有的那一项又变了：没替换，要重新确认
	Err      error
}

// ErrImportPending：大仓库里这个技能还没下载完。
var ErrImportPending = textErr(locales.ImportPending)

// AddImport 把 names 里的技能装到 p。每个技能独立成败，按 names 的顺序返回结果。
// replace 里有的技能是用户确认要替换的，值是确认时的那次 CheckImport：已有
// 的那一项还是那时的样子才替换，否则返回 Conflict 且 Stale；已经不在了就照常添加。
func (l *Library) AddImport(b *ImportBatch, names []string, p ImportPlacement, replace map[string]ImportCheck) []ImportOutcome {
	updateJob.Lock() // 和检查、更新串行：都要写应用状态
	defer updateJob.Unlock()
	var out []ImportOutcome
	for _, n := range names {
		sk := b.Skill(n)
		if sk == nil {
			out = append(out, ImportOutcome{Name: n, Err: errors.New(locales.ImportSkillNotFound())})
			continue
		}
		var confirmed *ImportCheck
		if c, ok := replace[n]; ok {
			confirmed = &c
		}
		start := time.Now()
		o := l.addImport(b, sk, p, confirmed)
		o.Name = n
		out = append(out, o)
		l.Log(operationEvent("import", "add", n, time.Since(start), o.Err))
	}
	return out
}

func (l *Library) addImport(b *ImportBatch, sk *ImportSkill, p ImportPlacement, confirmed *ImportCheck) (out ImportOutcome) {
	switch {
	case !l.disk:
		return ImportOutcome{Err: errors.New(locales.ImportSampleUnavailable())}
	case sk.Problem != "":
		return ImportOutcome{Err: errors.New(sk.Problem)}
	case sk.Partial:
		return ImportOutcome{Err: ErrImportPending}
	}
	release, err := lockSkill(sk.Name)
	if err != nil {
		return ImportOutcome{Err: err}
	}
	defer release()
	target, links, err := l.importTargets(sk.Name, p)
	if err != nil {
		return ImportOutcome{Err: err}
	}

	// 要替换的：已有的必须还是用户确认时看到的样子
	var replace *ImportCheck
	if confirmed != nil {
		now := l.CheckImport(sk, p)
		switch {
		case now.State == ImportNew: // 已有的那一项已经不在了，照常添加
		case now.State != ImportConflict || !now.SameAs(*confirmed):
			return ImportOutcome{Conflict: cmp.Or(now.Existing, target), Stale: true}
		case now.Blocked != "":
			return ImportOutcome{Err: errors.New(now.Blocked)}
		default:
			replace = &now
		}
	}
	replacing := func(p string) bool {
		return replace != nil && slices.ContainsFunc(replace.Replaces, func(e ImportExisting) bool { return e.Path == p })
	}
	// 替换时已经指向目标位置的链接不动，换完照样指向新版
	kept := func(p string) bool { return replace != nil && p != target && pointsAt(p, target) }
	for _, q := range append([]string{target}, linkPaths(links)...) {
		if exists(q) && !replacing(q) && !kept(q) {
			return ImportOutcome{Conflict: q}
		}
	}
	for _, k := range links {
		if a, _ := l.agentByID(k.agent); !a.hasHome(l.Home) {
			return ImportOutcome{Err: errors.New(locales.ImportAgentNotFound(a.DisplayName()))}
		}
	}

	// 替换时换下来的旧版、旧链接先放进各自的临时目录（replace-*），最后一步才移到
	// 废纸篓。它们是用户唯一的那一份：只有确认已经换回原处、或已经进了废纸篓，
	// 才删装它的临时目录；撤回不成时留着，告诉用户在哪里。
	var undo []func()
	var heldItems []*heldItem
	defer func() {
		undone := out.Err != nil || out.Conflict != ""
		if undone {
			for i := len(undo) - 1; i >= 0; i-- {
				undo[i]()
			}
		}
		// 撤回不完整的要说出来：旧版留在哪、哪个旧链接没能照原样建回去
		var notes []string
		var kept []string
		for _, h := range heldItems {
			switch {
			case !h.safe():
				kept = append(kept, l.Display(h.at))
			case undone && h.trashed && !h.restored:
				notes = append(notes, locales.ImportReplaceLinkNotRestored(l.Display(h.from), h.link))
			}
			if h.safe() {
				os.RemoveAll(h.tmp)
			}
		}
		if len(kept) > 0 {
			notes = append(notes, locales.ImportReplaceOldKeptAt(strings.Join(kept, locales.ListSeparator())))
		}
		for _, msg := range notes {
			if out.Err != nil {
				out.Err = fmt.Errorf("%w\n%s", out.Err, msg)
			} else {
				out.Err = errors.New(msg)
			}
			out.Added, out.Replaced, out.Conflict, out.Stale = false, false, "", false
		}
	}()

	// 换入：复制到暂存、核对，再不覆盖地改名进去（替换时和已有的原子交换）
	swap := replacing(target)
	placed, old, tmp, err := l.placeImport(sk, target, swap)
	if old != "" {
		heldItems = append(heldItems, &heldItem{home: l.Home, from: target, at: old, tmp: tmp})
	}
	if errors.Is(err, os.ErrExist) {
		return ImportOutcome{Conflict: target}
	}
	if err != nil {
		return ImportOutcome{Err: fmt.Errorf("%s%w", locales.ImportWriteFailedNoFiles(l.Display(filepath.Dir(target))), err)}
	}
	if swap {
		h := heldItems[0]
		undo = append(undo, func() {
			// 目标位置还是我们换进去的：原子换回；不是了（被别的工具动过）就
			// 放回空出来的位置，再不行移到废纸篓
			if fi, err := os.Lstat(target); err == nil && os.SameFile(fi, placed) && swapDirectories(h.at, target) == nil {
				h.restored = true
				return
			}
			h.putBack()
		})
	} else {
		undo = append(undo, func() {
			if fi, err := os.Lstat(target); err == nil && os.SameFile(fi, placed) {
				os.RemoveAll(target)
			}
		})
	}
	real, err := realPath(target)
	if err != nil {
		return ImportOutcome{Err: err}
	}

	// 建链接，记进本应用建的链接（以后「移除」只删自己建的）。替换时链接位置
	// 上的旧链接先挪开，最后和旧版一起移到废纸篓。
	var made []importLink
	if len(links) > 0 {
		l.loadNativeData()
		if l.nativeErr != nil {
			return ImportOutcome{Err: l.nativeErr}
		}
		prev := map[string]*string{} // 改之前本应用建的链接里这一项的值，没有为 nil
		for _, k := range links {
			if kept(k.path) {
				continue
			}
			dir := filepath.Dir(k.path)
			if err := os.MkdirAll(dir, 0o755); err != nil {
				return ImportOutcome{Err: err}
			}
			if replacing(k.path) {
				h, err := l.moveAside(k.path)
				if err != nil {
					return ImportOutcome{Err: err}
				}
				heldItems = append(heldItems, h)
				undo = append(undo, h.putBack)
			}
			from := dir
			if r, err := realPath(dir); err == nil {
				from = r
			}
			rel, err := filepath.Rel(from, real)
			if err != nil {
				rel = real
			}
			if err := makeLink(rel, k.path); err != nil {
				if exists(k.path) {
					return ImportOutcome{Conflict: k.path}
				}
				return ImportOutcome{Err: err}
			}
			// 撤回时认链接本身（同一个文件、同样的指向），不解析它指向哪里：
			// 新版被别的工具挪走时它悬空了，但仍是这次建的，要删掉好让旧链接回来
			lp := k.path
			ours, err := os.Lstat(lp)
			if err != nil {
				return ImportOutcome{Err: err}
			}
			pointer, _ := os.Readlink(lp)
			undo = append(undo, func() {
				if fi, err := os.Lstat(lp); err == nil && os.SameFile(fi, ours) {
					if to, err := os.Readlink(lp); err == nil && to == pointer {
						os.Remove(lp)
					}
				}
			})
			if was, ok := l.nativeData.Links[k.path]; ok {
				prev[k.path] = &was
			} else {
				prev[k.path] = nil
			}
			l.nativeData.Links[k.path] = real
			made = append(made, k)
		}
		if len(made) > 0 {
			if err := l.saveNativeData(); err != nil {
				return ImportOutcome{Err: err}
			}
			undo = append(undo, func() {
				l.loadNativeData()
				if l.nativeErr == nil {
					for p, was := range prev {
						if was != nil {
							l.nativeData.Links[p] = *was
						} else {
							delete(l.nativeData.Links, p)
						}
					}
					l.saveNativeData()
				}
			})
		}
	}

	// 安装记录：技能库模式的 GitHub 和网站来源，按 npx skills 的写法。替换技能库
	// 里的旧版、新版又不写记录时，去掉旧版那条：不然「更新」会拿旧来源覆盖新版。
	src := b.Source
	recorded := ""
	unusable := func(err error) error {
		if errors.Is(err, ErrRecordUnusable) {
			return errors.New(locales.ImportInvalidInstallRecord(l.Display(lockFile(l.Home))))
		}
		return err
	}
	if p.Library && (src.Kind == ImportGitHub || src.Kind == ImportWebsite) {
		rec := lockRecordFor(b, sk)
		recorded = rec.SkillFolderHash
		prev, err := addInstallRecord(l.Home, sk.Name, rec)
		if err != nil {
			return ImportOutcome{Err: unusable(err)}
		}
		undo = append(undo, func() {
			if prev != nil {
				replaceInstallRecord(l.Home, sk.Name, *prev)
			} else {
				dropInstallRecord(l.Home, sk.Name)
			}
		})
	} else if p.Library && swap {
		prev, err := dropInstallRecord(l.Home, sk.Name)
		if err != nil {
			return ImportOutcome{Err: unusable(err)}
		}
		undo = append(undo, func() {
			if prev != nil {
				restoreInstallRecord(l.Home, sk.Name, *prev)
			}
		})
	}

	// 应用记录（和更新的应用状态同一份）
	mark := importMark{Name: sk.Name, Place: target, Library: p.Library, Links: linkPaths(made), Agents: slices.Clone(p.Agents),
		Tree: sk.Tree, At: time.Now()}
	switch src.Kind {
	case ImportGitHub:
		mark.Source = SourceID{Type: "github", Repo: src.Repo, Ref: src.Ref}
		mark.Path, mark.Commit = sk.RepoPath, b.Commit
	case ImportWebsite:
		mark.Source = SourceID{Type: "well-known", Repo: b.Site}
		mark.Path = sk.SourceURL
	case ImportLocal:
		mark.Source = SourceID{Type: "local", Repo: sk.RepoPath}
	}
	if recorded == "" && mark.tracked(l.Root) {
		recorded = mark.entry().SkillFolderHash
	}
	// 撤回时要写回的：这个位置上原来的应用记录和被换掉的基线
	var oldMarks []importMark
	var oldBases []baselineMark
	var newBase baselineMark
	based := false
	st, err := editUpdates(l.Home, func(st *updateStore) error {
		st.Imports = slices.DeleteFunc(st.Imports, func(m importMark) bool {
			hit := samePlace(m.Place, target)
			if hit {
				oldMarks = append(oldMarks, m)
			}
			return hit
		})
		st.Imports = append(st.Imports, mark)
		if recorded != "" {
			// 装进来的就是基线：「更新」页不用再从记录推导
			skillPath := path.Join(sk.RepoPath, "SKILL.md")
			for _, b := range st.Baselines {
				if b.Source == mark.Source && b.Path == skillDir(skillPath) && samePlace(b.Place, target) {
					oldBases = append(oldBases, b)
				}
			}
			st.setBaseline(mark.Source, skillPath, target, recorded, sk.Tree)
			newBase, based = st.Baselines[len(st.Baselines)-1], true
		}
		return nil
	})
	if err != nil {
		return ImportOutcome{Err: fmt.Errorf("%s%w", locales.ImportStateWriteFailed(), err)}
	}
	l.updates, l.updatesErr = st, nil
	undo = append(undo, func() {
		st, err := editUpdates(l.Home, func(st *updateStore) error {
			st.Imports = slices.DeleteFunc(st.Imports, func(m importMark) bool { return samePlace(m.Place, target) })
			st.Imports = append(st.Imports, oldMarks...)
			if based {
				st.Baselines = slices.DeleteFunc(st.Baselines, func(b baselineMark) bool { return b == newBase })
				st.Baselines = append(st.Baselines, oldBases...)
			}
			return nil
		})
		if err == nil {
			l.updates, l.updatesErr = st, nil
		}
	})

	// 最后一步：换下来的移到废纸篓，旧链接在前、旧版最后。任何一项移不进去
	// 就整个撤回：已经进了废纸篓的旧链接照原样重建，旧版换回原处。
	for i := len(heldItems) - 1; i >= 0; i-- {
		h := heldItems[i]
		if err := Trash(l.Home, h.at); err != nil {
			return ImportOutcome{Err: fmt.Errorf("%s%w", locales.ImportReplaceTrashFailed(), err)}
		}
		h.trashed = true
	}
	return ImportOutcome{Added: true, Replaced: replace != nil && len(replace.Replaces) > 0, Place: target}
}

// heldItem 是替换时换下来、还没进废纸篓的一项：from 是它原来的位置，at 是
// 它现在的位置，tmp 是装它的临时目录。link 非空时它是链接，值是它指向哪里：
// 进了废纸篓又要撤回时照着重建。
type heldItem struct {
	home, from, at, tmp, link string
	trashed, restored         bool
	moved                     bool // 撤回时没能放回原处，已移到废纸篓
}

// safe 报告它已经不在临时目录里了（放回原处或进了废纸篓），临时目录可以删。
func (h *heldItem) safe() bool { return h.trashed || h.restored || h.moved }

// putBack 撤回时把它放回原处；原处已被占了就移到废纸篓。都不成时留在临时目录。
func (h *heldItem) putBack() {
	switch {
	case h.restored || h.moved:
	case h.trashed:
		// 只有链接会在撤回前进废纸篓（旧版最后才移）：照原样重建一个
		if h.link != "" && !exists(h.from) && makeLink(h.link, h.from) == nil {
			h.restored = true
		}
	case !exists(h.from) && renameNoReplace(h.at, h.from) == nil:
		h.restored = true
	case Trash(h.home, h.at) == nil:
		h.moved = true
	}
}

// moveAside 把要替换的旧链接挪进它自己的临时目录。名字不变，移到废纸篓后
// 看得出是哪个技能。
func (l *Library) moveAside(p string) (*heldItem, error) {
	to, err := os.Readlink(p)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(importStaging(l.Home), 0o700); err != nil {
		return nil, err
	}
	tmp, err := os.MkdirTemp(importStaging(l.Home), replacePrefix)
	if err != nil {
		return nil, err
	}
	at := filepath.Join(tmp, filepath.Base(p))
	if err := os.Rename(p, at); err != nil {
		os.RemoveAll(tmp)
		return nil, err
	}
	return &heldItem{home: l.Home, from: p, at: at, tmp: tmp, link: to}, nil
}

// replacePrefix 是替换时装旧版、旧链接的临时目录的前缀。启动时清暂存不删它们，
// 只移到废纸篓：里面可能是用户唯一的旧版。
const replacePrefix = "replace-"

func linkPaths(links []importLink) []string {
	var out []string
	for _, k := range links {
		out = append(out, k.path)
	}
	return out
}

// placeImport 把暂存的技能复制一份、核对后换进 target，返回放进去的目录的
// 信息（撤销时认它）。swap 为假时不覆盖地改名进去，target 已有东西时返回
// os.ErrExist；为真时和 target 上已有的那一项原子交换，old 是换下来的那一项
// 现在的位置，tmp 是装它的临时目录，由调用方在它离开后删掉。old 非空时即使
// 返回错误，旧版也还在 old。暂存和目标不在同一磁盘时，在目标旁边建隐藏的
// 临时目录再改名。
func (l *Library) placeImport(sk *ImportSkill, target string, swap bool) (placed fs.FileInfo, old, tmp string, err error) {
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return nil, "", "", err
	}
	try := func(parent string, prefix string) (fs.FileInfo, string, string, error) {
		if err := os.MkdirAll(parent, 0o700); err != nil {
			return nil, "", "", err
		}
		tmp, err := os.MkdirTemp(parent, prefix)
		if err != nil {
			return nil, "", "", err
		}
		fail := func(err error) (fs.FileInfo, string, string, error) {
			os.RemoveAll(tmp)
			return nil, "", "", err
		}
		dir := filepath.Join(tmp, sk.Name)
		if err := copyDir(sk.Dir, dir); err != nil {
			return fail(err)
		}
		if h, err := TreeHash(dir); err != nil || h != sk.Tree {
			return fail(errors.New(locales.ImportCopiedContentMismatch()))
		}
		if err := syncTree(dir); err != nil {
			return fail(err)
		}
		if !swap {
			if err := renameNoReplace(dir, target); err != nil {
				return fail(err)
			}
			syncDirectory(filepath.Dir(target))
			fi, err := os.Lstat(target)
			os.RemoveAll(tmp)
			return fi, "", "", err
		}
		if err := swapDirectories(dir, target); err != nil {
			return fail(err)
		}
		syncDirectory(filepath.Dir(target))
		fi, err := os.Lstat(target)
		if err != nil {
			// 换进去了却认不出来：换回去，当没发生；换不回去旧版就还在 dir
			if swapDirectories(dir, target) == nil {
				return fail(err)
			}
			return nil, dir, tmp, err
		}
		return fi, dir, tmp, nil
	}
	prefix, nearby := "add-", ".skill-manager-add-"
	if swap {
		prefix, nearby = replacePrefix, ".skill-manager-"+replacePrefix
	}
	fi, old, tmp, err := try(importStaging(l.Home), prefix)
	if errors.Is(err, syscall.EXDEV) {
		fi, old, tmp, err = try(filepath.Dir(target), nearby)
	}
	return fi, old, tmp, err
}

// syncTree 把目录里的文件和目录都刷到磁盘：中途断电时，换进去的要么没有，要么完整。
func syncTree(dir string) error {
	return filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.Type()&fs.ModeSymlink != 0 {
			return err
		}
		f, err := os.Open(p)
		if err != nil {
			return err
		}
		defer f.Close()
		return f.Sync()
	})
}

// ——— 安装记录 ———

// lockRecord 是写进安装记录的一条，字段和顺序与 skills@1.8.1 的 addSkillToLock
// 一致：GitHub 有 ref、skillPath；网站的 skillFolderHash 是空字符串，另有
// sourceBaseUrl、wellKnownDigest。
type lockRecord struct {
	Source          string `json:"source"`
	SourceType      string `json:"sourceType"`
	SourceURL       string `json:"sourceUrl"`
	Ref             string `json:"ref,omitempty"`
	SkillPath       string `json:"skillPath,omitempty"`
	SkillFolderHash string `json:"skillFolderHash"`
	SourceBaseURL   string `json:"sourceBaseUrl,omitempty"`
	WellKnownDigest string `json:"wellKnownDigest,omitempty"`
	InstalledAt     string `json:"installedAt"`
	UpdatedAt       string `json:"updatedAt"`
}

// jsTime 是 JavaScript 的 toISOString 写法：毫秒、UTC、Z。
func jsTime(t time.Time) string { return t.UTC().Format("2006-01-02T15:04:05.000Z") }

func lockRecordFor(b *ImportBatch, sk *ImportSkill) lockRecord {
	now := jsTime(time.Now())
	if b.Source.Kind == ImportWebsite {
		return lockRecord{Source: b.Site, SourceType: "well-known", SourceURL: sk.SourceURL,
			SourceBaseURL: b.BaseURL, WellKnownDigest: sk.Digest, InstalledAt: now, UpdatedAt: now}
	}
	r := lockRecord{
		Source: b.Source.Repo, SourceType: "github", SourceURL: "https://github.com/" + b.Source.Repo + ".git",
		Ref: b.Source.Ref, SkillPath: path.Join(sk.RepoPath, "SKILL.md"), SkillFolderHash: sk.Tree,
		InstalledAt: now, UpdatedAt: now,
	}
	if sk.RepoPath == "" {
		// 仓库根目录的技能：npx skills 记的是提交 ID
		r.SkillFolderHash = b.Commit
	}
	return r
}

// addInstallRecord 加一条安装记录。没有记录文件时新建 version 3。已有同名
// 的（技能库里没有文件的残留记录）换成新的，installedAt 沿用原值。返回原来
// 那条的原文，没有为 nil。记录不是 version 3 或解析不了时返回 ErrRecordUnusable。
func addInstallRecord(home, name string, r lockRecord) (*string, error) {
	recordMu.Lock()
	defer recordMu.Unlock()
	file := lockFile(home)
	src, err := os.ReadFile(file)
	if errors.Is(err, fs.ErrNotExist) {
		src = []byte("{\n  \"version\": 3,\n  \"skills\": {}\n}\n")
	} else if err != nil {
		return nil, err
	}
	if err := writableRecords(src); err != nil {
		return nil, err
	}
	doc, err := parseJSON(string(src))
	if err != nil {
		return nil, ErrRecordUnusable
	}
	prev, err := doc.raw([]string{"skills", name})
	if err != nil {
		return nil, err
	}
	if prev != nil {
		var old struct {
			InstalledAt string `json:"installedAt"`
		}
		if json.Unmarshal([]byte(*prev), &old) == nil && old.InstalledAt != "" {
			r.InstalledAt = old.InstalledAt
		}
	}
	unit := jsonIndent(doc)
	body, err := json.MarshalIndent(r, unit+unit, unit)
	if err != nil {
		return nil, err
	}
	raw := string(body)
	out, err := editJSON(string(src), []string{"skills", name}, &raw)
	if err != nil {
		return nil, err
	}
	if err := atomicConfigWrite(file, out); err != nil {
		return nil, err
	}
	back, err := ReadInstallRecords(home)
	if err != nil || back.Get(name) == nil || !sameEntry(back.Get(name).Raw, raw) {
		return prev, fmt.Errorf("%s%w", locales.UpdateWriteVerificationFailed(), ErrRecordChanged)
	}
	return prev, nil
}

// replaceInstallRecord 把 name 这一条换回 raw（撤销添加时用）。
func replaceInstallRecord(home, name, raw string) error {
	recordMu.Lock()
	defer recordMu.Unlock()
	src, err := os.ReadFile(lockFile(home))
	if err != nil {
		return err
	}
	out, err := editJSON(string(src), []string{"skills", name}, &raw)
	if err != nil {
		return err
	}
	return atomicConfigWrite(lockFile(home), out)
}

// ——— 应用记录 ———

// importMark 是从「添加技能」装进来的一个技能：来源、装在哪、建了哪些链接。
// 技能库模式的 GitHub、网站技能另有安装记录，这里只补它表达不了的。
type importMark struct {
	Name    string    `json:"name"`
	Source  SourceID  `json:"source"`
	Path    string    `json:"path,omitempty"`   // GitHub：仓库里的目录；网站：SKILL.md 或压缩包的网址
	Commit  string    `json:"commit,omitempty"` // GitHub：装的是哪个提交
	Tree    string    `json:"tree"`             // 装进去的目录哈希
	Place   string    `json:"place"`            // 文件所在的目录
	Library bool      `json:"library,omitempty"`
	Agents  []string  `json:"agents,omitempty"`
	Links   []string  `json:"links,omitempty"` // 本次建的链接
	At      time.Time `json:"at"`
}

// appRecordFile 是更新事务里「用的是应用记录」的 RecordFile：这类技能更新
// 后只改应用记录，不碰安装记录。
const appRecordFile = "app:imports"

// tracked 报告这条应用记录要由「更新」页按应用记录追踪：「指定的 Agent」
// 模式装进 Agent 目录的 GitHub 技能。技能库里的按安装记录追踪，不在这里。
func (m importMark) tracked(root string) bool {
	return !m.Library && m.Source.Type == "github" && !samePlace(filepath.Dir(m.Place), root)
}

// entry 把应用记录写成和安装记录一样的条目，「更新」页统一处理。Raw 是
// 这条记录的身份：来源、目录、版本、位置任何一项变了，旧的检查结果和对比都不算数。
func (m importMark) entry() LockEntry {
	raw, _ := json.Marshal(struct {
		Source SourceID `json:"source"`
		Path   string   `json:"path"`
		Commit string   `json:"commit"`
		Tree   string   `json:"tree"`
		Place  string   `json:"place"`
	}{m.Source, m.Path, m.Commit, m.Tree, filepath.Clean(m.Place)})
	hash := m.Tree
	if m.Path == "" {
		hash = m.Commit // 和安装记录一样：仓库根目录的技能记提交 ID
	}
	return LockEntry{Name: m.Name, Source: m.Source.Repo, SourceType: "github", SourceURL: "https://github.com/" + m.Source.Repo + ".git",
		SkillPath: path.Join(m.Path, "SKILL.md"), SkillFolderHash: hash, Ref: m.Source.Ref, InstalledAt: m.At, UpdatedAt: m.At,
		Raw: string(raw), Place: m.Place}
}

// appEntries 是要由「更新」页按应用记录追踪的条目。
func (st *updateStore) appEntries(root string) []LockEntry {
	var out []LockEntry
	for _, m := range st.Imports {
		if m.tracked(root) {
			out = append(out, m.entry())
		}
	}
	return out
}

// setImportVersion 把 it 对应的应用记录改成更新后的版本，返回新的条目原文。
// 记录已经不是检查时那条（被删、换了来源）时返回 ErrRecordChanged。
func (st *updateStore) setImportVersion(it UpdateItem, tree, commit string) (string, error) {
	for i := range st.Imports {
		m := &st.Imports[i]
		if !samePlace(m.Place, it.Record.Place) {
			continue
		}
		if m.entry().Raw == it.Record.Raw {
			m.Tree, m.Commit = tree, commit
			return m.entry().Raw, nil
		}
		if m.Tree == tree && m.Commit == commit {
			return m.entry().Raw, nil // 已经写过（恢复时重做收尾）
		}
	}
	return "", ErrRecordChanged
}

// dropImportMarks 去掉这些位置上的应用记录（删除技能时），返回去掉的，撤销时写回。
func (l *Library) dropImportMarks(places []string) ([]importMark, error) {
	var dropped []importMark
	st, err := editUpdates(l.Home, func(st *updateStore) error {
		st.Imports = slices.DeleteFunc(st.Imports, func(m importMark) bool {
			hit := slices.ContainsFunc(places, func(p string) bool { return samePlace(p, m.Place) })
			if hit {
				dropped = append(dropped, m)
			}
			return hit
		})
		return nil
	})
	if err != nil {
		return nil, err
	}
	l.updates, l.updatesErr = st, nil
	return dropped, nil
}

// restoreImportMarks 写回删除时去掉的应用记录；同一位置又有了记录时不覆盖。
func (l *Library) restoreImportMarks(marks []importMark) error {
	if len(marks) == 0 {
		return nil
	}
	st, err := editUpdates(l.Home, func(st *updateStore) error {
		for _, m := range marks {
			if !slices.ContainsFunc(st.Imports, func(x importMark) bool { return samePlace(x.Place, m.Place) }) {
				st.Imports = append(st.Imports, m)
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	l.updates, l.updatesErr = st, nil
	return nil
}

// RecentImport 是最近添加的一个技能。
type RecentImport struct {
	Name   string
	Source SourceID // Type 是 github、well-known 或 local；local 的 Repo 是本机路径
	Place  string
	At     time.Time
}

// RecentImports 是最近添加的技能，新的在前，最多 n 个；文件已经不在的不列。
func (l *Library) RecentImports(n int) []RecentImport {
	var out []RecentImport
	for _, m := range l.updateState().Imports {
		if exists(m.Place) {
			out = append(out, RecentImport{m.Name, m.Source, m.Place, m.At})
		}
	}
	slices.SortStableFunc(out, func(a, b RecentImport) int { return b.At.Compare(a.At) })
	if len(out) > n {
		out = out[:n]
	}
	return out
}

// ImportedFrom 是从「添加技能」装进来时记下的来源；不是从这里装的返回 false。
func (l *Library) ImportedFrom(place string) (SourceID, bool) {
	for _, m := range l.updateState().Imports {
		if samePlace(m.Place, place) {
			return m.Source, true
		}
	}
	return SourceID{}, false
}

// String 用在给人看的地方：本机来源显示路径。
func (r RecentImport) Label(home string) string {
	if r.Source.Type == "local" {
		return ShortPath(home, r.Source.Repo)
	}
	return r.Source.String()
}

// importLabel 把 SourceID 写成一句话，供错误提示用。
func importLabel(s SourceID) string {
	if s.Type == "local" {
		return filepath.Base(s.Repo)
	}
	return strings.TrimSpace(s.String())
}
