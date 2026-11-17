package library

import (
	"context"
	"encoding/hex"
	"errors"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"skillmanager/locales"
)

type UpdateState string
type UpdateGroup string

const (
	GroupReady       UpdateGroup = "ready"
	GroupConfirm     UpdateGroup = "confirm"
	GroupHandle      UpdateGroup = "handle"
	GroupFailed      UpdateGroup = "failed"
	GroupWebsite     UpdateGroup = "website"
	GroupUnsupported UpdateGroup = "unsupported"
	GroupOther       UpdateGroup = "other"
	GroupIgnored     UpdateGroup = "ignored"
	GroupStopped     UpdateGroup = "stopped"
	UpdReady         UpdateState = "ready"
	UpdBoth          UpdateState = "both"
	UpdLatest        UpdateState = "latest"
	UpdLocalOnly     UpdateState = "local"
	UpdUnknownBase   UpdateState = "unknown-base"
	UpdUnchecked     UpdateState = "unchecked"
	UpdCopies        UpdateState = "copies"
	UpdMovedOut      UpdateState = "moved-out"
	UpdRemoved       UpdateState = "removed"
	UpdRelocated     UpdateState = "relocated"
	UpdAmbiguous     UpdateState = "ambiguous"
	UpdFailed        UpdateState = "failed"
	UpdWebsite       UpdateState = "website"
	UpdUnsupported   UpdateState = "unsupported"
	UpdStopped       UpdateState = "stopped"
	UpdIgnored       UpdateState = "ignored"
	UpdLocation      UpdateState = "location"
	UpdRenamed       UpdateState = "renamed"
	UpdPending       UpdateState = "pending"
)

// Label 是分组标题，按界面当前的语言说：可更新、需要处理……
func (g UpdateGroup) Label() string {
	switch g {
	case GroupReady:
		return locales.UpdateGroupReady()
	case GroupConfirm:
		return locales.UpdateGroupConfirm()
	case GroupHandle:
		return locales.UpdateGroupHandle()
	case GroupFailed:
		return locales.UpdateGroupFailed()
	case GroupWebsite:
		return locales.UpdateGroupWebsite()
	case GroupUnsupported:
		return locales.UpdateGroupUnsupported()
	case GroupIgnored:
		return locales.UpdateGroupIgnored()
	case GroupStopped:
		return locales.UpdateGroupStopped()
	}
	return locales.UpdateGroupOther()
}

func (s UpdateState) Group() UpdateGroup {
	switch s {
	case UpdReady:
		return GroupReady
	case UpdBoth, UpdUnknownBase:
		return GroupConfirm
	case UpdCopies, UpdMovedOut, UpdRemoved, UpdRelocated, UpdAmbiguous, UpdLocation, UpdRenamed, UpdPending:
		return GroupHandle
	case UpdFailed:
		return GroupFailed
	case UpdWebsite:
		return GroupWebsite
	case UpdUnsupported:
		return GroupUnsupported
	case UpdIgnored:
		return GroupIgnored
	case UpdStopped:
		return GroupStopped
	default:
		return GroupOther
	}
}

// UpdateItem 是检查时固定的一份快照；替换前重新核对它，不能凭名称更新。
type UpdateItem struct {
	Retained, Transaction         string
	Name                          string
	Names                         []string
	Source                        SourceID
	Path, Place                   string
	State                         UpdateState
	Reason                        Note `json:"-"` // 每次按本机内容和检查结果重新推导，不存
	Command                       string
	Local, Base, Upstream, Commit string
	CommitDate, CheckedAt, Reset  time.Time
	Stale, ReadOnly               bool
	RecordIncomplete              bool // 安装记录缺字段，建议重新安装（界面换图标、给出重装命令）
	Record                        LockEntry
	Files                         []TreeEntry
}

func (it UpdateItem) CanUpdate() bool { return it.State == UpdReady && !it.Stale && !it.ReadOnly }
func (it UpdateItem) CanCompare() bool {
	return !it.Stale && (it.State == UpdBoth || it.State == UpdUnknownBase || it.State == UpdReady)
}

// WebURL 返回可在浏览器打开的上游仓库或来源网站地址（仅允许 http/https）。
func (it UpdateItem) WebURL() string {
	switch it.Source.Type {
	case "github":
		repo := strings.TrimSuffix(strings.TrimSpace(it.Source.Repo), ".git")
		if segs, ok := splitSafePath(repo, 2, true); ok {
			base := "https://github.com/" + strings.Join(segs, "/")
			if ref, ok := splitSafePath(it.Source.Ref, 1, false); ok && len(ref) > 0 {
				return base + "/tree/" + strings.Join(ref, "/")
			}
			return base
		}
	case "gitlab":
		if u := safeHTTPURL(strings.TrimSuffix(it.Record.SourceURL, ".git")); u != "" {
			return u
		}
		repo := strings.TrimSuffix(strings.TrimSpace(it.Source.Repo), ".git")
		if segs, ok := splitSafePath(repo, 2, false); ok {
			return "https://gitlab.com/" + strings.Join(segs, "/")
		}
	case "well-known":
		if u := safeHTTPURL(it.Record.SourceBaseURL); u != "" {
			return u
		}
		if u := safeHTTPURL(it.Record.SourceURL); u != "" {
			return u
		}
		if iconHostOK(it.Source.Repo) {
			return "https://" + strings.TrimSpace(it.Source.Repo)
		}
		return ""
	}
	if u := safeHTTPURL(it.Record.SourceBaseURL); u != "" {
		return u
	}
	return safeHTTPURL(strings.TrimSuffix(it.Record.SourceURL, ".git"))
}

// CommitURL 返回当前比较的 GitHub 提交网页地址；非 GitHub 或无有效 Commit 时返回空。
func (it UpdateItem) CommitURL() string {
	if it.Source.Type != "github" {
		return ""
	}
	repo := strings.TrimSuffix(strings.TrimSpace(it.Source.Repo), ".git")
	segs, ok := splitSafePath(repo, 2, true)
	commit := safeCommitHex(it.Commit)
	if !ok || commit == "" {
		return ""
	}
	return "https://github.com/" + strings.Join(segs, "/") + "/commit/" + commit
}

// FileWebURL 返回上游仓库里某一个技能文件在该提交下的浏览地址；不可定位时返回空。
func (it UpdateItem) FileWebURL(relPath string) string {
	if it.Source.Type != "github" {
		return ""
	}
	repo := strings.TrimSuffix(strings.TrimSpace(it.Source.Repo), ".git")
	segs, ok := splitSafePath(repo, 2, true)
	if !ok {
		return ""
	}
	ref := safeCommitHex(it.Commit)
	if ref == "" {
		if refSegs, ok := splitSafePath(it.Source.Ref, 1, false); ok && len(refSegs) > 0 {
			ref = strings.Join(refSegs, "/")
		} else {
			ref = "HEAD"
		}
	}
	sub := skillDir(it.Record.SkillPath)
	if sub == "" {
		sub = strings.Trim(strings.ReplaceAll(it.Path, `\`, "/"), "/")
	}
	joined := strings.Trim(strings.ReplaceAll(relPath, `\`, "/"), "/")
	if sub != "" {
		joined = sub + "/" + joined
	}
	fileSegs, ok := splitSafePath(joined, 1, false)
	if !ok {
		return ""
	}
	return "https://github.com/" + strings.Join(segs, "/") + "/blob/" + ref + "/" + strings.Join(fileSegs, "/")
}

func safeCommitHex(s string) string {
	s = strings.TrimSpace(s)
	if len(s) < 7 || len(s) > 64 {
		return ""
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')) {
			return ""
		}
	}
	return s
}

func safeHTTPURL(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" {
		return ""
	}
	return u.String()
}

func splitSafePath(s string, minSegs int, exact bool) ([]string, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, false
	}
	parts := strings.Split(s, "/")
	if len(parts) < minSegs || (exact && len(parts) != minSegs) {
		return nil, false
	}
	out := make([]string, len(parts))
	for i, seg := range parts {
		if seg == "" || seg == "." || seg == ".." || strings.ContainsAny(seg, "\\ \t\r\n?#") {
			return nil, false
		}
		out[i] = url.PathEscape(seg)
	}
	return out, true
}

type UpdateReport struct {
	Items    []UpdateItem
	Checked  time.Time
	Complete bool
	Eligible int
	Reset    time.Time
	ReadOnly Note // 非空时更新状态只能看：为什么
}

func (r UpdateReport) Count() int {
	n := 0
	for _, it := range r.Items {
		if !it.Stale && !it.ReadOnly && (it.State == UpdReady || it.State == UpdBoth) {
			n++
		}
	}
	return n
}
func (r UpdateReport) Ready() []UpdateItem {
	return slices.DeleteFunc(slices.Clone(r.Items), func(it UpdateItem) bool { return !it.CanUpdate() })
}

func sourceOf(e LockEntry) SourceID { return SourceID{e.SourceType, e.Source, e.Ref} }

// updateEntries 是更新要看的全部条目：npx skills 的安装记录，加上「指定的
// Agent」模式装进 Agent 目录、只记在应用记录里的 GitHub 技能。
func (l *Library) updateEntries() []LockEntry {
	return append(slices.Clone(l.records.entries()), l.updateState().appEntries(l.Root)...)
}

// placeOf 是条目对应的技能目录：应用记录写着，安装记录的在技能库。
func (l *Library) placeOf(e LockEntry) string {
	if e.Place != "" {
		return e.Place
	}
	return filepath.Join(l.Root, e.Name)
}

// untrackedCopies 是 s 除了 place 自己、以及各有记录追踪的位置以外，
// 还有几份真实目录不同的副本。同名技能一份在技能库（安装记录）、一份在
// Agent 目录（应用记录）时各更新各的，不算要先收成一份的副本。
func (l *Library) untrackedCopies(s *Skill, place string) int {
	own := map[string]bool{}
	for _, p := range append([]string{place}, l.trackedPlaces(s.Name)...) {
		if r, err := realPath(p); err == nil {
			own[r] = true
		}
	}
	others := map[string]bool{}
	for _, p := range slices.Concat(s.places, s.elsewhere) {
		if !own[p.real] {
			others[p.real] = true
		}
	}
	return len(others)
}

// trackedPlaces 是名为 name 的技能里各有记录追踪的位置：安装记录管技能库
// 那份，应用记录管各 Agent 目录里的。
func (l *Library) trackedPlaces(name string) []string {
	var out []string
	if l.records.Get(name) != nil {
		out = append(out, filepath.Join(l.Root, name))
	}
	for _, e := range l.updateState().appEntries(l.Root) {
		if e.Name == name {
			out = append(out, e.Place)
		}
	}
	return out
}

// entryFor 是 it 这一条现在的样子：应用记录按位置找，安装记录按名称找。
func (l *Library) entryFor(it UpdateItem) *LockEntry {
	if it.Record.Place == "" {
		return l.records.Get(it.Name)
	}
	for _, e := range l.updateState().appEntries(l.Root) {
		if samePlace(e.Place, it.Record.Place) {
			return &e
		}
	}
	return nil
}
func (l *Library) updateState() *updateStore {
	if l.updates == nil {
		l.updates = &updateStore{}
	}
	return l.updates
}

// Updates 不联网；按当前本机内容和记录解释上次检查，不把旧基线套到新来源。
func (l *Library) Updates() UpdateReport {
	st := l.updateState()
	rep := UpdateReport{}
	// 没有安装记录文件时没有要按它更新的条目；应用记录的条目不受它影响
	recordsBad := l.recordsErr != nil || (l.records != nil && l.records.Writable() != nil)
	if recordsBad {
		rep.ReadOnly = note(noteRecordUnusable)
	}
	if l.updatesErr != nil {
		rep.ReadOnly = Note{Kind: noteStateUnreadable, Detail: l.updatesErr.Error()}
	}
	if st.Check != nil {
		rep.Checked, rep.Complete = st.Check.At, st.Check.Complete
	}
	for _, tx := range st.Transactions {
		it := tx.Item
		it.State = UpdPending
		it.Reason = note(notePreviousUnfinished)
		if tx.Status == "complete" {
			it.Reason = note(noteCleanupPending)
		}
		if tx.Status == "retained" {
			it.Reason = note(noteRecoveryStopped)
		}
		it.Retained = tx.Stage
		it.Transaction = tx.ID
		rep.Items = append(rep.Items, it)
	}
	for _, e := range l.updateEntries() {
		if slices.ContainsFunc(st.Transactions, func(tx *transaction) bool {
			return tx.Item.Name == e.Name && samePlace(tx.Item.Place, l.placeOf(e)) && tx.Status != "retained"
		}) {
			continue
		}
		it, exists := l.localUpdate(e)
		if !exists {
			continue
		}
		it.ReadOnly = l.updatesErr != nil || (recordsBad && e.Place == "")
		if it.State == UpdWebsite || it.State == UpdUnsupported {
			i := slices.IndexFunc(rep.Items, func(x UpdateItem) bool { return x.Source == it.Source && x.State == it.State && x.Reason == it.Reason })
			if i >= 0 {
				rep.Items[i].Names = append(rep.Items[i].Names, e.Name)
				continue
			}
		}
		if it.State == UpdUnchecked {
			rep.Eligible++
			if st.Check != nil {
				ri := slices.IndexFunc(st.Check.Repos, func(r repoResult) bool { return r.Source == it.Source })
				si := slices.IndexFunc(st.Check.Skills, func(s skillUpdates) bool {
					return s.Name == it.Name && s.Source == it.Source && s.Path == it.Path && sameEntry(s.Recorded, it.Record.Raw)
				})
				if ri >= 0 && si >= 0 {
					l.classifyUpdate(&it, st.Check.Repos[ri], st.Check.Skills[si])
				}
			}
		}
		if !it.Reset.IsZero() {
			rep.Reset = it.Reset
		}
		rep.Items = append(rep.Items, it)
	}
	for i := range rep.Items {
		it := &rep.Items[i]
		if it.State == UpdWebsite && it.Command == "" {
			it.Command = "npx skills update " + shellArgs(it.Names) + " -g -y"
		}
	}
	return rep
}
func (l *Library) localUpdate(e LockEntry) (UpdateItem, bool) {
	it := UpdateItem{Name: e.Name, Names: []string{e.Name}, Source: sourceOf(e), Path: skillDir(e.SkillPath), Place: l.placeOf(e), Record: e, State: UpdUnchecked, Reason: note(noteNotChecked)}
	if !filepath.IsLocal(e.Name) || filepath.Base(e.Name) != e.Name {
		return it, false
	}
	fi, err := os.Lstat(it.Place)
	if os.IsNotExist(err) && e.Place != "" {
		// 应用记录的技能被移走或删了：不检查、不重装
		return it, false
	}
	if os.IsNotExist(err) {
		if !l.hasSkill(e.Name) {
			return it, false
		}
		it.State, it.Reason = UpdMovedOut, note(noteMissingFromLibrary)
		return it, true
	}
	if e.SourceType != "github" {
		it.State, it.Reason = UpdUnsupported, note(noteSourceUnsupported)
		if e.SourceType == "well-known" {
			it.State, it.Reason = UpdWebsite, note(noteNotAutomatic)
			if e.Source == "agent.qq.com" && e.SourceBaseURL == "" {
				it.Reason, it.RecordIncomplete = note(noteIncompleteRecord), true
				it.Command = "npx skills add https://agent.qq.com -g -y"
			}
		}
		return it, true
	}
	if l.updateState().stopped(it.Source, it.Place) {
		it.State, it.Reason = UpdStopped, note(noteChecksStopped)
		return it, true
	}
	if err != nil {
		it.State, it.Reason = UpdFailed, Note{Detail: err.Error()}
		return it, true
	}
	if fi.Mode()&os.ModeSymlink != 0 || !fi.IsDir() || exists(filepath.Join(it.Place, ".git")) {
		it.State, it.Reason = UpdLocation, note(noteLocationUnsupported)
		return it, true
	}
	if s := l.Get(e.Name); s != nil && s.Copies > 0 && l.untrackedCopies(s, it.Place) > 0 {
		it.State, it.Reason = UpdCopies, note(noteCopiesNeedMerge)
		return it, true
	}
	if e.SkillPath == "" || len(strings.Split(e.Source, "/")) != 2 {
		it.State, it.Reason = UpdFailed, note(noteSourcePathMissing)
		return it, true
	}
	it.Local, err = TreeHash(it.Place)
	if err != nil {
		it.State, it.Reason = UpdFailed, Note{Kind: noteLocalUnreadable, Detail: err.Error()}
	}
	return it, true
}
func (l *Library) classifyUpdate(it *UpdateItem, r repoResult, s skillUpdates) {
	it.Commit, it.CommitDate, it.CheckedAt, it.Reset = r.Commit, r.CommitDate, r.At, r.Reset
	if s.Commit != "" {
		it.Commit = s.Commit
	}
	if !s.CommitDate.IsZero() {
		it.CommitDate = s.CommitDate
	}
	it.Stale = !r.Done
	it.Upstream, it.Base, it.Files = s.Upstream, s.Base, s.Files
	if b, ok := l.updateState().baseline(it.Source, it.Path, it.Place, it.Record.SkillFolderHash); ok {
		it.Base = b
	}
	switch {
	case r.failure() != nil:
		it.State, it.Reason = UpdFailed, *r.failure()
	case s.Blocked != "":
		it.State, it.Reason = s.Blocked, s.blockedBy()
	case s.failure() != nil:
		it.State, it.Reason = UpdFailed, *s.failure()
	case s.Found == foundRemoved:
		it.State, it.Reason = UpdRemoved, note(noteUpstreamRemoved)
	case s.Found == foundMoved:
		it.State, it.Reason = UpdRelocated, note(noteUpstreamMoved)
	case s.Found == foundAmbiguous:
		it.State, it.Reason = UpdAmbiguous, note(noteUpstreamAmbiguous)
	case it.Local == it.Upstream:
		it.State, it.Reason = UpdLatest, note(noteAlreadyCurrent)
	case it.Base == "":
		it.State, it.Reason = UpdUnknownBase, note(noteBaselineUnknown)
	case it.Base == it.Upstream:
		it.State, it.Reason = UpdLocalOnly, note(noteLocalChangesOnly)
	case it.Local == it.Base:
		it.State, it.Reason = UpdReady, note(noteAvailable)
	default:
		it.State, it.Reason = UpdBoth, note(noteBothChanged)
	}
	if (it.State == UpdBoth || it.State == UpdUnknownBase) && l.updates.ignored(it.Source, it.Path, it.Place, l.Root, it.Upstream) {
		it.State, it.Reason = UpdIgnored, note(noteLocalVersionKept)
	}
}

// 更新任务串行；每次检查内部最多同时请求三个仓库。界面在后台持有自己的 Library。
var updateJob sync.Mutex

type UpdateProgress struct {
	Done, Total int
	Name        string
	Failure     string
	Active      bool
}

func (l *Library) CheckUpdates(ctx context.Context, remote Remote, progress func(UpdateProgress)) error {
	if !updateJob.TryLock() {
		return ErrBusy
	}
	defer updateJob.Unlock()
	if l.updatesErr != nil {
		return l.updatesErr
	}
	st := l.updateState()
	l.records, l.recordsErr = ReadInstallRecords(l.Home)
	if l.recordsErr != nil {
		// 安装记录读不了只挡按它更新的技能；应用记录追踪的照常检查
		l.records = nil
		if len(st.appEntries(l.Root)) == 0 {
			return l.recordsErr
		}
	}
	old := st.Check
	check := &checkRecord{At: time.Now(), Complete: true}
	if old != nil {
		check.Repos = slices.Clone(old.Repos)
		check.Skills = slices.Clone(old.Skills)
		for i := range check.Repos {
			check.Repos[i].Done = false
		}
	}
	st.Check = check
	var sources []SourceID
	// 本地状态可能已变化；只把通过当前前置检查的条目交给远端。
	var eligible []UpdateItem
	for _, e := range l.updateEntries() {
		if it, ok := l.localUpdate(e); ok && it.State == UpdUnchecked {
			eligible = append(eligible, it)
		}
	}
	sources = nil
	for _, it := range eligible {
		if !slices.Contains(sources, it.Source) {
			sources = append(sources, it.Source)
		}
	}
	if progress != nil {
		progress(UpdateProgress{Done: 0, Total: len(sources), Active: true})
	}
	type result struct {
		repo   repoResult
		skills []skillUpdates
	}
	done := make(chan result, 3)
	next, active, finished := 0, 0, 0
	quota := false
	for next < len(sources) || active > 0 {
		for !quota && ctx.Err() == nil && active < 3 && next < len(sources) {
			src := sources[next]
			next++
			active++
			go func() { r, ss := l.checkRepo(ctx, remote, src, eligible); done <- result{r, ss} }()
		}
		if active == 0 {
			break
		}
		res := <-done
		active--
		finished++
		if res.repo.Done {
			check.Repos = slices.DeleteFunc(check.Repos, func(r repoResult) bool { return r.Source == res.repo.Source })
			check.Repos = append(check.Repos, res.repo)
			check.Skills = slices.DeleteFunc(check.Skills, func(s skillUpdates) bool { return s.Source == res.repo.Source })
			check.Skills = append(check.Skills, res.skills...)
		}
		if res.repo.failure() != nil || !res.repo.Done {
			check.Complete = false
		}
		if !res.repo.Reset.IsZero() {
			quota = true
		}
		if progress != nil {
			progress(UpdateProgress{Done: finished, Total: len(sources), Name: res.repo.Source.String()})
		}
	}
	if next < len(sources) || ctx.Err() != nil {
		check.Complete = false
	}
	for _, it := range l.Updates().Items {
		if !it.Stale && (it.State == UpdLatest || it.Local == it.Base && it.Base != "") {
			tree := it.Base
			if it.State == UpdLatest {
				tree = it.Local
			}
			st.setBaseline(it.Source, it.Path, it.Place, it.Record.SkillFolderHash, tree)
		}
	}
	return st.save(l.Home)
}
func (l *Library) checkRepo(ctx context.Context, remote Remote, src SourceID, items []UpdateItem) (repoResult, []skillUpdates) {
	r := repoResult{Source: src, At: time.Now()}
	var ss []skillUpdates
	c, err := remote.Commit(ctx, src.Repo, src.Ref)
	var tree Tree
	if err == nil {
		tree, err = remote.Tree(ctx, src.Repo, c.SHA)
	}
	if err == nil && tree.Truncated {
		err = noteErr(noteTreeTruncated)
	}
	if ctx.Err() != nil {
		return r, nil
	}
	r.Done = true
	r.Commit, r.CommitDate = c.SHA, c.Date
	if err != nil {
		n := noteOf(err)
		r.Fail, r.Failure = err.Error(), &n
		var re *RemoteError
		if errors.As(err, &re) && re.Kind == RemoteQuota {
			r.Reset = re.Reset
		}
	}
	usePathCommits := l.Settings.UseGHAuth
	if ar, ok := remote.(AuthRemote); ok && !ar.Authenticated() {
		usePathCommits = false
	}
	pc, hasPathCommit := remote.(PathCommitter)
	usePathCommits = usePathCommits && hasPathCommit
	pathCommits := map[string]Commit{}
	pathTried := map[string]bool{}
	for _, it := range items {
		if it.Source != src {
			continue
		}
		s := skillUpdates{Name: it.Name, Source: src, Path: it.Path, Recorded: it.Record.Raw}
		if err == nil {
			s.Found, s.Upstream, s.Files = locateSkill(tree, c.Tree, it.Path, it.Name)
			if s.Found == foundSame {
				if e := validTreeFiles(s.Files, it.Path); e != nil {
					s.fail(e)
				}
				if b, ok := l.updateState().baseline(src, it.Path, it.Place, it.Record.SkillFolderHash); ok {
					s.Base = b
				} else if isSHA(it.Record.SkillFolderHash) {
					s.Base = it.Record.SkillFolderHash
					if it.Path == "" {
						bc, e := remote.Commit(ctx, src.Repo, it.Record.SkillFolderHash)
						s.Base = ""
						if e == nil {
							s.Base = bc.Tree
						}
					}
				}
				if usePathCommits && it.Path != "" && r.Reset.IsZero() {
					if !pathTried[it.Path] {
						pathTried[it.Path] = true
						dc, pe := pc.PathCommit(ctx, src.Repo, c.SHA, it.Path)
						if ctx.Err() != nil {
							return r, nil
						}
						if pe == nil && dc.SHA != "" {
							pathCommits[it.Path] = dc
						} else if pe != nil {
							var re *RemoteError
							if errors.As(pe, &re) && re.Kind == RemoteQuota {
								r.Reset = re.Reset
							}
						}
					}
					if dc, ok := pathCommits[it.Path]; ok {
						s.Commit, s.CommitDate = dc.SHA, dc.Date
					}
				}
			}
		}
		ss = append(ss, s)
	}
	return r, ss
}
func isSHA(s string) bool { _, err := hex.DecodeString(s); return len(s) == 40 && err == nil }
func locateSkill(tree Tree, root, dir, name string) (upFound, string, []TreeEntry) {
	marker := path.Join(dir, "SKILL.md")
	has := false
	hash := root
	var files []TreeEntry
	namesakes := 0
	for _, e := range tree.Entries {
		if e.Path == dir && e.Type == "tree" {
			hash = e.SHA
		}
		if e.Path == marker && e.Type == "blob" {
			has = true
		}
		if path.Base(e.Path) == "SKILL.md" && path.Base(path.Dir(e.Path)) == name {
			namesakes++
		}
		if e.Type != "tree" && (dir == "" || strings.HasPrefix(e.Path, dir+"/")) {
			files = append(files, e)
		}
	}
	if !has {
		switch namesakes {
		case 0:
			return foundRemoved, "", nil
		case 1:
			return foundMoved, "", nil
		default:
			return foundAmbiguous, "", nil
		}
	}
	return foundSame, hash, files
}

// validTreeFiles 检查上游目录树里一个技能的文件能不能装。大小未知（-1，git
// 协议不带）的文件这里不查大小，下载时按实际字节数拦（apply.go、import_read.go）。
func validTreeFiles(files []TreeEntry, dir string) error {
	var size int64
	seen := map[string]bool{}
	if len(files) > 2000 {
		return noteErr(noteTooManyFiles)
	}
	for _, e := range files {
		rel := strings.TrimPrefix(e.Path, dir+"/")
		if dir == "" {
			rel = e.Path
		}
		if !filepath.IsLocal(rel) || strings.Contains(rel, `\`) || path.Clean(rel) != rel || rel == "." {
			return noteErr(noteInvalidUpstreamPath)
		}
		key := strings.ToLower(rel)
		if seen[key] {
			return noteErr(noteFilenameConflict)
		}
		seen[key] = true
		if e.Type != "blob" || e.Mode != "100644" && e.Mode != "100755" {
			return noteErr(noteUnsupportedFiles)
		}
		if e.Size > maxFileBytes {
			return noteErr(noteSingleFileTooLarge)
		}
		size += max(e.Size, 0)
		if size > maxSkillBytes {
			return noteErr(noteSkillTooLarge)
		}
	}
	return nil
}
func shellArgs(args []string) string {
	out := make([]string, len(args))
	for i, s := range args {
		if s != "" && !strings.ContainsAny(s, " \t\n\r'\"`$;&|<>()*?![]{}\\") && !strings.HasPrefix(s, "-") {
			out[i] = s
		} else {
			out[i] = "'" + strings.ReplaceAll(s, "'", "'\\''") + "'"
		}
	}
	return strings.Join(out, " ")
}
