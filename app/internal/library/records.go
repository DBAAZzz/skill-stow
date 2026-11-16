package library

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"time"

	"skillmanager/locales"
)

// 整理：已删除技能的安装记录。技能文件夹已经哪里都没有了，npx skills 的
// 安装记录里还有它；不清掉的话 npx skills update 会把它装回来。

// StaleRecord 是同一个来源下、技能已经删掉的几条安装记录：整理页的一行。
type StaleRecord struct {
	Source    string    // 记录的 source：GitHub 仓库或 well-known 的域名
	Names     []string  // 按记录文件里的顺序
	Installed time.Time // 这一组里最早的安装时间
}

// InstallRecord 返回一个技能的安装记录；没有记录（手动拷贝的技能）时
// ok 为 false。
func (l *Library) InstallRecord(name string) (entry LockEntry, ok bool) {
	if l.records == nil {
		return LockEntry{}, false
	}
	e := l.records.Get(name)
	if e == nil {
		return LockEntry{}, false
	}
	return *e, true
}

// staleRecords 按来源合并已删除技能的记录，来源按第一次出现的顺序。
func (l *Library) staleRecords() []StaleRecord {
	var out []StaleRecord
	for _, e := range l.records.entries() {
		if l.hasSkill(e.Name) {
			continue
		}
		i := slices.IndexFunc(out, func(r StaleRecord) bool { return r.Source == e.Source })
		if i < 0 {
			i = len(out)
			out = append(out, StaleRecord{Source: e.Source})
		}
		r := &out[i]
		r.Names = append(r.Names, e.Name)
		if !e.InstalledAt.IsZero() && (r.Installed.IsZero() || e.InstalledAt.Before(r.Installed)) {
			r.Installed = e.InstalledAt
		}
	}
	return out
}

// hasSkill 报告扫描到了这个名字的「我的」技能：技能库、各 Agent 的目录、
// 手动添加的目录里都算，含链接。
func (l *Library) hasSkill(name string) bool {
	return slices.ContainsFunc(l.Skills, func(s *Skill) bool {
		return s.Name == name && (s.Source == Mine || s.Source == ViaPlugin)
	})
}

// onDiskNow 重新看一眼磁盘：技能库和各全局目录里有没有这个名字（断掉的链接不算）。
func (l *Library) onDiskNow(name string) bool {
	dirs := []string{l.Root}
	for dir := range l.readers {
		dirs = append(dirs, dir)
	}
	for _, a := range slices.Concat(Catalog, l.custom) {
		dirs = append(dirs, filepath.Join(l.Home, a.Skills))
	}
	for _, d := range legacyDirs {
		dirs = append(dirs, filepath.Join(l.Home, d))
	}
	for _, d := range dirs {
		if _, err := os.Stat(filepath.Join(d, name)); err == nil {
			return true
		}
	}
	return false
}

// ClearRecords 从安装记录里去掉这几行的条目，不动任何技能文件或链接。每行
// 写之前重读，再确认一遍那个技能确实不在了：期间装回来的跳过。一行失败
// 不影响其他行。返回去掉了几条。
func (l *Library) ClearRecords(rows ...StaleRecord) (int, error) {
	n := 0
	var errs []error
	for _, r := range rows {
		var names []string
		for _, name := range r.Names {
			if !l.hasSkill(name) && (!l.disk || !l.onDiskNow(name)) {
				names = append(names, name)
			}
		}
		if len(names) == 0 {
			continue
		}
		release, err := holdSkills(names)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s%w", locales.UpdateInstallRecordCleanupFailed(), err))
			continue
		}
		if l.disk {
			err := dropRecords(l.Home, names)
			if err != nil {
				release()
				errs = append(errs, errors.New(locales.UpdateInstallRecordCleanupReason(recordErr(err))))
				continue
			}
		}
		release()
		l.records.drop(names)
		n += len(names)
	}
	return n, errors.Join(errs...)
}

// recordErr 是写安装记录失败的原因，给人看的说法。
func recordErr(err error) string {
	switch {
	case errors.Is(err, fs.ErrPermission):
		return locales.UpdateInstallRecordNotWritable()
	case errors.Is(err, ErrRecordUnusable):
		return locales.UpdateInstallRecordUnknownVersion()
	}
	return err.Error()
}

// dropRecords 从安装记录里去掉这些条目，一次写入；别的内容和格式不动。
func dropRecords(home string, names []string) error {
	recordMu.Lock()
	defer recordMu.Unlock()
	file := lockFile(home)
	src, err := os.ReadFile(file)
	if err != nil {
		return err
	}
	if err := writableRecords(src); err != nil {
		return err
	}
	out := string(src)
	for _, name := range names {
		if out, err = editJSON(out, []string{"skills", name}, nil); err != nil {
			return err
		}
	}
	return atomicConfigWrite(file, out)
}

func (r *InstallRecords) entries() []LockEntry {
	if r == nil {
		return nil
	}
	return r.Entries
}

// drop 从读到的记录里拿掉这些条目（写盘之后同步内存）。
func (r *InstallRecords) drop(names []string) {
	if r != nil {
		r.Entries = slices.DeleteFunc(r.Entries, func(e LockEntry) bool { return slices.Contains(names, e.Name) })
	}
}
