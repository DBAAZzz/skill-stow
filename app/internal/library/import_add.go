package library

import (
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
	ImportSame                            // 已经有，内容一样：已添加 · 最新
	ImportConflict                        // 已有同名，内容或来源不同：不覆盖
	ImportInvalid                         // 不合规矩（名称、大小、链接……）
)

// ImportCheck 是检查结果；Existing 是已有的那一项。
type ImportCheck struct {
	State    ImportState
	Existing string
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
		return ImportCheck{State: ImportConflict, Existing: target}
	}
	for _, k := range links {
		if _, err := os.Lstat(k.path); err == nil {
			return ImportCheck{State: ImportConflict, Existing: k.path}
		}
	}
	return ImportCheck{State: ImportNew}
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
	Place    string // 添加后文件所在的目录
	Conflict string // 按下添加时目标位置已有同名，这是已有的那一项
	Err      error
}

// ErrImportPending：大仓库里这个技能还没下载完。
var ErrImportPending = textErr(locales.ImportPending)

// AddImport 把 names 里的技能装到 p。每个技能独立成败，按 names 的顺序返回结果。
func (l *Library) AddImport(b *ImportBatch, names []string, p ImportPlacement) []ImportOutcome {
	updateJob.Lock() // 和检查、更新串行：都要写应用状态
	defer updateJob.Unlock()
	var out []ImportOutcome
	for _, n := range names {
		sk := b.Skill(n)
		if sk == nil {
			out = append(out, ImportOutcome{Name: n, Err: errors.New(locales.ImportSkillNotFound())})
			continue
		}
		o := l.addImport(b, sk, p)
		o.Name = n
		out = append(out, o)
	}
	return out
}

func (l *Library) addImport(b *ImportBatch, sk *ImportSkill, p ImportPlacement) (out ImportOutcome) {
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
	for _, q := range append([]string{target}, linkPaths(links)...) {
		if exists(q) {
			return ImportOutcome{Conflict: q}
		}
	}
	for _, k := range links {
		if a, _ := l.agentByID(k.agent); !a.hasHome(l.Home) {
			return ImportOutcome{Err: errors.New(locales.ImportAgentNotFound(a.DisplayName()))}
		}
	}

	var undo []func()
	defer func() {
		if out.Err != nil || out.Conflict != "" {
			for i := len(undo) - 1; i >= 0; i-- {
				undo[i]()
			}
		}
	}()

	// 换入：复制到暂存、核对，再不覆盖地改名进去
	placed, err := l.placeImport(sk, target)
	if errors.Is(err, os.ErrExist) {
		return ImportOutcome{Conflict: target}
	}
	if err != nil {
		return ImportOutcome{Err: fmt.Errorf("%s%w", locales.ImportWriteFailedNoFiles(l.Display(filepath.Dir(target))), err)}
	}
	undo = append(undo, func() {
		if fi, err := os.Lstat(target); err == nil && os.SameFile(fi, placed) {
			os.RemoveAll(target)
		}
	})
	real, err := realPath(target)
	if err != nil {
		return ImportOutcome{Err: err}
	}

	// 建链接，记进本应用建的链接（以后「移除」只删自己建的）
	if len(links) > 0 {
		l.loadNativeData()
		if l.nativeErr != nil {
			return ImportOutcome{Err: l.nativeErr}
		}
		for _, k := range links {
			dir := filepath.Dir(k.path)
			if err := os.MkdirAll(dir, 0o755); err != nil {
				return ImportOutcome{Err: err}
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
			lp := k.path
			undo = append(undo, func() {
				if r, err := realPath(lp); err == nil && r == real && isLink(lp) {
					os.Remove(lp)
				}
			})
			l.nativeData.Links[k.path] = real
		}
		if err := l.saveNativeData(); err != nil {
			return ImportOutcome{Err: err}
		}
		undo = append(undo, func() {
			l.loadNativeData()
			if l.nativeErr == nil {
				for _, k := range links {
					delete(l.nativeData.Links, k.path)
				}
				l.saveNativeData()
			}
		})
	}

	// 安装记录：技能库模式的 GitHub 和网站来源，按 npx skills 的写法
	src := b.Source
	recorded := ""
	if p.Library && (src.Kind == ImportGitHub || src.Kind == ImportWebsite) {
		rec := lockRecordFor(b, sk)
		recorded = rec.SkillFolderHash
		prev, err := addInstallRecord(l.Home, sk.Name, rec)
		if err != nil {
			if errors.Is(err, ErrRecordUnusable) {
				err = errors.New(locales.ImportInvalidInstallRecord(l.Display(lockFile(l.Home))))
			}
			return ImportOutcome{Err: err}
		}
		undo = append(undo, func() {
			if prev != nil {
				replaceInstallRecord(l.Home, sk.Name, *prev)
			} else {
				dropInstallRecord(l.Home, sk.Name)
			}
		})
	}

	// 应用记录（和更新的应用状态同一份）
	mark := importMark{Name: sk.Name, Place: target, Library: p.Library, Links: linkPaths(links), Agents: slices.Clone(p.Agents),
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
	st, err := editUpdates(l.Home, func(st *updateStore) error {
		st.Imports = slices.DeleteFunc(st.Imports, func(m importMark) bool { return samePlace(m.Place, target) })
		st.Imports = append(st.Imports, mark)
		if recorded != "" {
			// 装进来的就是基线：「更新」页不用再从记录推导
			st.setBaseline(mark.Source, path.Join(sk.RepoPath, "SKILL.md"), target, recorded, sk.Tree)
		}
		return nil
	})
	if err != nil {
		return ImportOutcome{Err: fmt.Errorf("%s%w", locales.ImportStateWriteFailed(), err)}
	}
	l.updates, l.updatesErr = st, nil
	return ImportOutcome{Added: true, Place: target}
}

func linkPaths(links []importLink) []string {
	var out []string
	for _, k := range links {
		out = append(out, k.path)
	}
	return out
}

// placeImport 把暂存的技能复制一份、核对后不覆盖地改名成 target，返回放进去
// 的目录的信息（撤销时认它）。暂存和目标不在同一磁盘时，在目标旁边建隐藏
// 的临时目录再改名。
func (l *Library) placeImport(sk *ImportSkill, target string) (fs.FileInfo, error) {
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return nil, err
	}
	try := func(parent string, prefix string) (fs.FileInfo, error) {
		if err := os.MkdirAll(parent, 0o700); err != nil {
			return nil, err
		}
		tmp, err := os.MkdirTemp(parent, prefix)
		if err != nil {
			return nil, err
		}
		defer os.RemoveAll(tmp)
		dir := filepath.Join(tmp, sk.Name)
		if err := copyDir(sk.Dir, dir); err != nil {
			return nil, err
		}
		if h, err := TreeHash(dir); err != nil || h != sk.Tree {
			return nil, errors.New(locales.ImportCopiedContentMismatch())
		}
		if err := syncTree(dir); err != nil {
			return nil, err
		}
		if err := renameNoReplace(dir, target); err != nil {
			return nil, err
		}
		syncDirectory(filepath.Dir(target))
		return os.Lstat(target)
	}
	fi, err := try(importStaging(l.Home), "add-")
	if errors.Is(err, syscall.EXDEV) {
		fi, err = try(filepath.Dir(target), ".skill-manager-add-")
	}
	return fi, err
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
