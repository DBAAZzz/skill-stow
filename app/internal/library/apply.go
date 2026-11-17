package library

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"skillmanager/locales"
)

var ErrUpdateChanged = textErr(locales.UpdateChanged)
var ErrUnsupportedFiles = noteErr(noteUnsupportedFiles)
var ErrSkillRenamed = noteErr(noteSkillRenamed)

type Prepared struct {
	Item   UpdateItem
	Dir    string
	Target directoryID
}
type UpdateOutcome struct {
	Updated  bool
	Warning  string
	Retained string
}

func (l *Library) PrepareUpdate(ctx context.Context, remote Remote, it UpdateItem) (*Prepared, error) {
	return l.PrepareUpdateWithProgress(ctx, remote, it, nil)
}

// PrepareUpdateWithProgress 准备技能更新内容并在下载阶段汇报逐文件进度。
func (l *Library) PrepareUpdateWithProgress(ctx context.Context, remote Remote, it UpdateItem, progress func(done, total int)) (*Prepared, error) {
	if !updateJob.TryLock() {
		return nil, ErrBusy
	}
	defer updateJob.Unlock()
	return l.prepareUpdate(ctx, remote, it, progress)
}

func (l *Library) prepareUpdate(ctx context.Context, remote Remote, it UpdateItem, progress func(done, total int)) (p *Prepared, err error) {
	defer func() {
		state := UpdateState("")
		if errors.Is(err, ErrSkillRenamed) {
			state = UpdRenamed
		}
		if errors.Is(err, ErrUnsupportedFiles) {
			state = UpdFailed
		}
		if state != "" && l.updateState().Check != nil {
			for i := range l.updates.Check.Skills {
				s := &l.updates.Check.Skills[i]
				if s.Name == it.Name && s.Source == it.Source && s.Upstream == it.Upstream && sameEntry(s.Recorded, it.Record.Raw) {
					s.block(state, err)
				}
			}
			err = errors.Join(err, l.updates.save(l.Home))
		}
	}()

	if !it.CanCompare() {
		return nil, ErrUpdateChanged
	}
	if err = l.validateSnapshot(it); err != nil {
		return nil, err
	}
	id, err := identify(it.Place)
	if err != nil {
		return nil, err
	}
	if err = validTreeFiles(it.Files, it.Path); err != nil {
		return nil, err
	}
	if err = os.MkdirAll(stagingDir(l.Home), 0700); err != nil {
		return nil, err
	}
	dir, err := os.MkdirTemp(stagingDir(l.Home), "update-")
	if err != nil {
		return nil, err
	}
	p = &Prepared{it, dir, id}
	defer func() {
		if err != nil {
			os.RemoveAll(dir)
		}
	}()
	fromCache := copyFromUpstreamCache(l.Home, it, dir)
	if !fromCache {
		if err = downloadUpdateFiles(ctx, remote, it, dir, progress); err != nil {
			return nil, err
		}
	}
	if hash, e := TreeHash(dir); e != nil || hash != it.Upstream {
		return nil, errors.New(locales.UpdateIncompleteDownload())
	}
	a, e := os.ReadFile(filepath.Join(it.Place, "SKILL.md"))
	if e != nil {
		return nil, e
	}
	b, e := os.ReadFile(filepath.Join(dir, "SKILL.md"))
	if e != nil {
		return nil, e
	}
	local, up := ParseSkillMD(string(a)), ParseSkillMD(string(b))
	if local.Name == "" || up.Name != local.Name || up.Description == "" {
		return nil, ErrSkillRenamed
	}
	err = filepath.WalkDir(dir, func(path string, d os.DirEntry, e error) error {
		if e != nil {
			return e
		}
		if d.IsDir() {
			return syncDirectory(path)
		}
		return nil
	})
	if err == nil {
		err = syncDirectory(stagingDir(l.Home))
	}
	if err == nil && !fromCache {
		saveUpstreamCache(l.Home, it.Upstream, dir)
	}
	return p, err
}

const downloadWorkers = 4

func downloadUpdateFiles(ctx context.Context, remote Remote, it UpdateItem, dir string, progress func(done, total int)) error {
	totalFiles := len(it.Files)
	if progress != nil && totalFiles > 0 {
		progress(0, totalFiles)
	}
	if totalFiles == 0 {
		return nil
	}
	dlCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	jobs := make(chan TreeEntry, totalFiles)
	for _, e := range it.Files {
		jobs <- e
	}
	close(jobs)

	var (
		mu       sync.Mutex
		total    int
		done     int
		firstErr error
		wg       sync.WaitGroup
	)
	workers := min(downloadWorkers, totalFiles)
	wg.Add(workers)
	for range workers {
		go func() {
			defer wg.Done()
			for e := range jobs {
				if err := dlCtx.Err(); err != nil {
					return
				}
				b, err := remote.File(dlCtx, it.Source.Repo, it.Commit, e.Path)
				if err != nil {
					mu.Lock()
					if firstErr == nil {
						firstErr = err
						cancel()
					}
					mu.Unlock()
					return
				}
				var fileErr error
				switch {
				case len(b) > maxFileBytes || blobID(b) != e.SHA:
					fileErr = errors.New(locales.UpdateIncompleteDownload())
				case strings.HasPrefix(string(b), "version https://git-lfs.github.com/spec/v1"):
					fileErr = ErrUnsupportedFiles
				}
				mu.Lock()
				if firstErr == nil && fileErr == nil {
					total += len(b)
					if total > maxSkillBytes {
						fileErr = errors.New(locales.UpdateIncompleteDownload())
					}
				}
				if fileErr != nil {
					if firstErr == nil {
						firstErr = fileErr
						cancel()
					}
					mu.Unlock()
					return
				}
				mu.Unlock()

				if err := writeStageTreeFile(dir, relSkillPath(e.Path, it.Path), e.Mode, b); err != nil {
					mu.Lock()
					if firstErr == nil {
						firstErr = err
						cancel()
					}
					mu.Unlock()
					return
				}
				if progress != nil {
					mu.Lock()
					done++
					d := done
					mu.Unlock()
					progress(d, totalFiles)
				}
			}
		}()
	}
	wg.Wait()
	return firstErr
}

func relSkillPath(full, skillPath string) string {
	if skillPath != "" {
		return strings.TrimPrefix(full, skillPath+"/")
	}
	return full
}

func writeStageTreeFile(dir, rel, gitMode string, b []byte) error {
	dest := filepath.Join(dir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(dest), 0755); err != nil {
		return err
	}
	mode := os.FileMode(0644)
	if gitMode == "100755" {
		mode = 0755
	}
	return writeSynced(dest, b, mode)
}

const maxUpstreamCacheEntries = 50

func upstreamCacheDir(home string) string { return filepath.Join(appDir(home), "cache") }

// copyFromUpstreamCache 按目录 Upstream tree 哈希命中本地缓存，避免重复从 GitHub 下载。
func copyFromUpstreamCache(home string, it UpdateItem, dir string) bool {
	if home == "" || !isSHA(it.Upstream) {
		return false
	}
	src := filepath.Join(upstreamCacheDir(home), it.Upstream)
	if h, err := TreeHash(src); err != nil || h != it.Upstream {
		if _, statErr := os.Lstat(src); statErr == nil {
			os.RemoveAll(src)
		}
		return false
	}
	var total int
	for _, e := range it.Files {
		rel := relSkillPath(e.Path, it.Path)
		b, err := os.ReadFile(filepath.Join(src, filepath.FromSlash(rel)))
		if err != nil {
			clearDirContents(dir)
			return false
		}
		total += len(b)
		if len(b) > maxFileBytes || total > maxSkillBytes || blobID(b) != e.SHA || strings.HasPrefix(string(b), "version https://git-lfs.github.com/spec/v1") {
			clearDirContents(dir)
			return false
		}
		if err := writeStageTreeFile(dir, rel, e.Mode, b); err != nil {
			clearDirContents(dir)
			return false
		}
	}
	now := time.Now()
	_ = os.Chtimes(src, now, now)
	return true
}

func saveUpstreamCache(home, upstream, dir string) {
	if home == "" || !isSHA(upstream) {
		return
	}
	root := upstreamCacheDir(home)
	dst := filepath.Join(root, upstream)
	if h, err := TreeHash(dst); err == nil && h == upstream {
		now := time.Now()
		_ = os.Chtimes(dst, now, now)
		return
	}
	if err := os.MkdirAll(root, 0700); err != nil {
		return
	}
	tmp, err := os.MkdirTemp(root, ".tmp-")
	if err != nil {
		return
	}
	err = filepath.WalkDir(dir, func(p string, d fs.DirEntry, e error) error {
		if e != nil || p == dir {
			return e
		}
		rel, e := filepath.Rel(dir, p)
		if e != nil {
			return e
		}
		target := filepath.Join(tmp, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0755)
		}
		info, e := d.Info()
		if e != nil || !info.Mode().IsRegular() {
			return errors.New("cache skip non-regular")
		}
		b, e := os.ReadFile(p)
		if e != nil {
			return e
		}
		mode := os.FileMode(0644)
		if info.Mode()&0100 != 0 {
			mode = 0755
		}
		return writeSynced(target, b, mode)
	})
	if err != nil {
		os.RemoveAll(tmp)
		return
	}
	if h, e := TreeHash(tmp); e != nil || h != upstream {
		os.RemoveAll(tmp)
		return
	}
	_ = os.RemoveAll(dst)
	if err := os.Rename(tmp, dst); err != nil {
		os.RemoveAll(tmp)
		return
	}
	pruneUpstreamCache(root, maxUpstreamCacheEntries)
}

func pruneUpstreamCache(root string, keep int) {
	entries, err := os.ReadDir(root)
	if err != nil {
		return
	}
	type item struct {
		name  string
		mtime time.Time
	}
	var cached []item
	for _, e := range entries {
		if !e.IsDir() || strings.HasPrefix(e.Name(), ".tmp-") {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		cached = append(cached, item{e.Name(), info.ModTime()})
	}
	if len(cached) <= keep {
		return
	}
	slices.SortFunc(cached, func(a, b item) int { return cmp.Compare(b.mtime.UnixNano(), a.mtime.UnixNano()) })
	for _, old := range cached[keep:] {
		_ = os.RemoveAll(filepath.Join(root, old.name))
	}
}

func clearDirContents(dir string) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		_ = os.RemoveAll(filepath.Join(dir, e.Name()))
	}
}
func writeSynced(path string, b []byte, mode os.FileMode) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode)
	if err != nil {
		return err
	}
	_, err = f.Write(b)
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		return err
	}
	return closeErr
}
func syncDirectory(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return f.Sync()
}
func (l *Library) DiscardPrepared(p *Prepared) {
	if p == nil {
		return
	}
	for _, tx := range l.updateState().Transactions {
		if tx.Stage == p.Dir {
			return
		}
	}
	os.RemoveAll(p.Dir)
}
func (l *Library) CompareUpdate(p *Prepared) []FileDiff {
	return l.Compare(&Version{Real: p.Item.Place}, &Version{Real: p.Dir})
}

// 一处复核边界，供下载、替换和对比确认共用。
func (l *Library) validateSnapshot(it UpdateItem) error {
	return l.validateSnapshotWith(Scan(l.Home), it)
}

func (l *Library) validateSnapshotWith(fresh *Library, it UpdateItem) error {
	if fresh.updatesErr != nil {
		return fresh.updatesErr
	}
	for _, tx := range fresh.updateState().Transactions {
		if tx.Item.Name == it.Name && samePlace(tx.Item.Place, it.Place) && tx.Status == "pending" {
			return errors.New(locales.UpdatePreviousIncomplete())
		}
	}
	cur := fresh.entryFor(it)
	if cur == nil || !sameEntry(cur.Raw, it.Record.Raw) {
		return ErrRecordChanged
	}
	now, ok := fresh.localUpdate(*cur)
	if !ok || now.State != UpdUnchecked || now.Local != it.Local {
		return ErrUpdateChanged
	}
	// 使用内存快照也要检查磁盘记录的最新检查版本；外部重新检查不能复用旧对比。
	check := fresh.updateState().Check
	if check == nil {
		return ErrUpdateChanged
	}
	for _, r := range check.Repos {
		if r.Source != it.Source || !r.Done || r.failure() != nil {
			continue
		}
		if r.Commit == it.Commit {
			return nil
		}
		for _, s := range check.Skills {
			if s.Name == it.Name && s.Source == it.Source && s.Path == it.Path &&
				sameEntry(s.Recorded, it.Record.Raw) && s.Commit == it.Commit && s.Upstream == it.Upstream {
				return nil
			}
		}
	}
	return ErrUpdateChanged
}

// ApplyUpdate 应用一个技能的更新。progress 非 nil 时先报「开始下载」，
// 结束时报完成或失败，语义和 ApplyUpdates 的单条事件一致。
func (l *Library) ApplyUpdate(ctx context.Context, remote Remote, it UpdateItem, p *Prepared, progress func(UpdateProgress)) (UpdateOutcome, error) {
	if !updateJob.TryLock() {
		return UpdateOutcome{}, ErrBusy
	}
	defer updateJob.Unlock()
	if progress != nil {
		progress(UpdateProgress{Done: 0, Total: 1, Name: it.Name, Active: true})
	}
	out, err := l.applyUpdate(ctx, remote, it, p)
	if progress != nil {
		failure := ""
		if err != nil {
			failure = err.Error()
		}
		if out.Warning != "" {
			failure = out.Warning
		}
		progress(UpdateProgress{Done: 1, Total: 1, Name: it.Name, Failure: failure})
	}
	return out, err
}

var afterTxSaveHook func()

func (l *Library) applyUpdate(ctx context.Context, remote Remote, it UpdateItem, p *Prepared) (out UpdateOutcome, err error) {
	defer l.track("update", "apply", it.Name)(&err)
	if l.Settings.UpdateOldVersion != "trash" && l.Settings.UpdateOldVersion != "delete" {
		return out, errors.New(locales.UpdateChooseOldVersionAction())
	}
	if !it.CanUpdate() && (p == nil || !it.CanCompare() || it.ReadOnly) {
		return out, ErrUpdateChanged
	}
	release, err := lockSkill(it.Name)
	if err != nil {
		return out, err
	}
	defer release()
	if p == nil {
		p, err = l.prepareUpdate(ctx, remote, it, nil)
		if err != nil {
			return out, err
		}
	}
	defer l.DiscardPrepared(p)
	if p.Item.Commit != it.Commit || p.Item.Local != it.Local || p.Item.Source != it.Source || p.Item.Place != it.Place || p.Item.Upstream != it.Upstream {
		return out, ErrUpdateChanged
	}
	if err = ctx.Err(); err != nil {
		return out, err
	}
	fresh := Scan(l.Home)
	if err = l.validateSnapshotWith(fresh, it); err != nil {
		return out, err
	}
	recordFile := appRecordFile
	if it.Record.Place == "" {
		recs, err := ReadInstallRecords(l.Home)
		if err != nil {
			return out, err
		}
		if err = recs.Writable(); err != nil {
			return out, err
		}
		recordFile = recs.File
	}
	target, err := identify(it.Place)
	if err != nil || target != p.Target {
		return out, ErrUpdateChanged
	}
	stage, err := identify(p.Dir)
	if err != nil {
		return out, err
	}
	if stage.Device != target.Device {
		return out, errors.New(locales.UpdateStagingDifferentDisk())
	}
	hash, err := TreeHash(p.Dir)
	if err != nil || hash != it.Upstream {
		return out, errors.New(locales.UpdateIncompleteDownload())
	}
	recorded := it.Upstream
	if it.Path == "" {
		recorded = it.Commit
	}
	tx := &transaction{ID: filepath.Base(p.Dir), Item: it, Stage: p.Dir, TargetID: target, StageID: stage, RecordFile: recordFile, RecordHash: recorded, RecordAt: time.Now().UTC().Format(time.RFC3339Nano), Policy: l.Settings.UpdateOldVersion, Status: "pending"}
	tx.Item.Files = nil
	st := l.updateState()
	st.Transactions = append(st.Transactions, tx)
	if err = st.save(l.Home); err != nil {
		st.Transactions = st.Transactions[:len(st.Transactions)-1]
		return out, err
	}
	if afterTxSaveHook != nil {
		afterTxSaveHook()
	}
	rollbackTx := func(orig error) error {
		old := st.Transactions
		st.Transactions = slices.DeleteFunc(slices.Clone(old), func(t *transaction) bool { return t.ID == tx.ID })
		if saveErr := st.save(l.Home); saveErr != nil {
			st.Transactions = old
			return errors.Join(orig, saveErr)
		}
		return orig
	}
	if err = ctx.Err(); err != nil {
		return out, rollbackTx(err)
	}
	if err = l.validateBeforeSwap(fresh, tx); err != nil {
		return out, rollbackTx(err)
	}
	if err = swapDirectories(it.Place, p.Dir); err != nil {
		if directoryMatches(it.Place, target, it.Local) {
			return out, rollbackTx(err)
		}
		return out, err
	}
	if err = syncDirectory(filepath.Dir(it.Place)); err != nil {
		return out, err
	}
	if err = syncDirectory(filepath.Dir(p.Dir)); err != nil {
		return out, err
	}
	return l.finishTransaction(tx)
}
func (l *Library) validateBeforeSwap(fresh *Library, tx *transaction) error {
	var cur *LockEntry
	if tx.RecordFile == appRecordFile {
		cur = fresh.entryFor(tx.Item)
	} else {
		recs, err := ReadInstallRecords(l.Home)
		if err != nil {
			return err
		}
		if err = recs.Writable(); err != nil {
			return err
		}
		if recs.File != tx.RecordFile {
			return ErrRecordChanged
		}
		cur = recs.Get(tx.Item.Name)
	}
	if cur == nil || !sameEntry(cur.Raw, tx.Item.Record.Raw) {
		return ErrRecordChanged
	}
	if !directoryMatches(tx.Item.Place, tx.TargetID, tx.Item.Local) || !directoryMatches(tx.Stage, tx.StageID, tx.Item.Upstream) {
		return ErrUpdateChanged
	}
	now, ok := fresh.localUpdate(*cur)
	if !ok || now.State != UpdUnchecked {
		return ErrUpdateChanged
	}
	return nil
}
func directoryMatches(dir string, id directoryID, hash string) bool {
	actual, err := identify(dir)
	if err != nil || actual != id {
		return false
	}
	h, err := TreeHash(dir)
	return err == nil && h == hash
}
func (l *Library) finishTransaction(tx *transaction) (UpdateOutcome, error) {
	out := UpdateOutcome{Retained: tx.Stage}
	if !directoryMatches(tx.Item.Place, tx.StageID, tx.Item.Upstream) || !directoryMatches(tx.Stage, tx.TargetID, tx.Item.Local) {
		return out, ErrUpdateChanged
	}
	st := l.updateState()
	var raw string
	var err error
	if tx.RecordFile == appRecordFile {
		// 「指定的 Agent」模式装的：只改应用记录，不碰安装记录
		var disk *updateStore
		disk, err = editUpdates(l.Home, func(d *updateStore) error {
			raw, err = d.setImportVersion(tx.Item, tx.Item.Upstream, tx.Item.Commit)
			return err
		})
		if err == nil {
			st.Imports = disk.Imports
		}
	} else {
		if lockFile(l.Home) != tx.RecordFile {
			return out, ErrRecordChanged
		}
		raw, err = setRecordVersion(l.Home, tx.Item.Name, tx.Item.Record.Raw, tx.RecordHash, tx.RecordAt)
	}
	if err != nil {
		return out, err
	}
	st.setBaseline(tx.Item.Source, tx.Item.Path, tx.Item.Place, tx.RecordHash, tx.Item.Upstream)
	tx.Status = "complete"
	// 后续只清理旧版，不再重复替换技能；崩溃时由 complete 状态继续清理。
	if err = st.save(l.Home); err != nil {
		tx.Status = "pending"
		return out, err
	}
	l.records, _ = ReadInstallRecords(l.Home)
	if st.Check != nil {
		for i := range st.Check.Skills {
			s := &st.Check.Skills[i]
			if s.Name == tx.Item.Name && s.Source == tx.Item.Source && (tx.RecordFile != appRecordFile || sameEntry(s.Recorded, tx.Item.Record.Raw)) {
				s.Recorded = raw
				s.Base = tx.Item.Upstream
			}
		}
	}
	out.Updated = true
	if err = l.cleanTransaction(tx); err != nil {
		out.Warning = locales.UpdateDoneOldKept(err.Error())
		return out, nil
	}
	out.Retained = ""
	return out, nil
}
func (l *Library) cleanTransaction(tx *transaction) error {
	if _, err := os.Lstat(tx.Stage); !os.IsNotExist(err) {
		if !directoryMatches(tx.Stage, tx.TargetID, tx.Item.Local) {
			return ErrUpdateChanged
		}
		var err error
		if tx.Policy == "trash" {
			err = Trash(l.Home, tx.Stage)
		} else if tx.Policy == "delete" {
			err = os.RemoveAll(tx.Stage)
		} else {
			return errors.New(locales.UpdateOldVersionActionMissing())
		}
		if err != nil {
			return err
		}
	}
	st := l.updateState()
	old := st.Transactions
	st.Transactions = slices.DeleteFunc(slices.Clone(old), func(t *transaction) bool { return t.ID == tx.ID })
	if err := st.save(l.Home); err != nil {
		st.Transactions = old
		return err
	}
	return nil
}

// RetryRecord 与恢复共用同一收尾过程，不重放不匹配的原安装记录。
func (l *Library) RetryRecord(it UpdateItem) error {
	if !updateJob.TryLock() {
		return ErrBusy
	}
	defer updateJob.Unlock()
	for _, tx := range l.updateState().Transactions {
		if tx.Item.Name == it.Name && samePlace(tx.Item.Place, it.Place) && tx.Status == "pending" {
			release, err := lockSkill(it.Name)
			if err != nil {
				return err
			}
			defer release()
			_, err = l.finishTransaction(tx)
			return err
		}
	}
	return errors.New(locales.UpdateNoRecordToComplete())
}

func (l *Library) ApplyUpdates(ctx context.Context, remote Remote, items []UpdateItem, progress func(UpdateProgress)) (int, error) {
	if !updateJob.TryLock() {
		return 0, ErrBusy
	}
	defer updateJob.Unlock()
	n := 0
	var errs []error
	for i, it := range items {
		if ctx.Err() != nil {
			break
		}
		if !it.CanUpdate() {
			continue
		}
		if progress != nil {
			progress(UpdateProgress{Done: i, Total: len(items), Name: it.Name, Active: true})
		}
		out, err := l.applyUpdate(ctx, remote, it, nil)
		if out.Updated {
			n++
		}
		if err != nil {
			errs = append(errs, fmt.Errorf("%s%w", locales.LibrarySkillErrorPrefix(it.Name), err))
		}
		if out.Warning != "" {
			errs = append(errs, errors.New(out.Warning))
		}
		if progress != nil {
			failure := ""
			if err != nil {
				failure = err.Error()
			}
			if out.Warning != "" {
				failure = out.Warning
			}
			progress(UpdateProgress{Done: i + 1, Total: len(items), Name: it.Name, Failure: failure})
		}
	}
	return n, errors.Join(errs...)
}
