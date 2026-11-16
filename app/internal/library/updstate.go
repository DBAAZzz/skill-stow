package library

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"
)

// 更新的应用状态：updates.json，和 settings.json 同一个目录。安装基线、
// 忽略的上游版本、停止检查、上次检查的结果、更新事务都在这里，不写进
// npx skills 的安装记录。

// SourceID 是来源身份：来源类型、仓库（或 well-known 的域名）和 ref。
// 基线、忽略和停止检查都按它区分，来源一换旧标记就不作用。
type SourceID struct {
	Type string `json:"type"`
	Repo string `json:"repo"`
	Ref  string `json:"ref,omitempty"`
}

// String 是给人看的来源：「antfu/skills」，有 ref 时「antfu/skills@dev」。
func (s SourceID) String() string {
	if s.Ref != "" {
		return s.Repo + "@" + s.Ref
	}
	return s.Repo
}

// skillDir 是技能在仓库里的目录：skills/vite/SKILL.md → skills/vite，根目录
// 的 SKILL.md → 空。反斜杠按 / 算。
func skillDir(skillPath string) string {
	p := strings.ReplaceAll(skillPath, `\`, "/")
	if base := path.Base(p); strings.EqualFold(base, "SKILL.md") {
		p = path.Dir(p)
	}
	p = path.Clean("/" + p)
	return strings.TrimPrefix(p, "/")
}

// baselineMark 是应用记下的安装基线：这个来源、这个路径、装在这里、记录写着
// Recorded 时，装进来的是 Tree。任何一项变了就不算数，重新推导。
type baselineMark struct {
	Source   SourceID `json:"source"`
	Path     string   `json:"path"`
	Place    string   `json:"place"`
	Recorded string   `json:"recorded"`
	Tree     string   `json:"tree"`
}

// ignoreMark 是「保留本地」：装在这里的、这个来源这个路径的技能，这个上游目录版本不再提示。
type ignoreMark struct {
	Source SourceID  `json:"source"`
	Path   string    `json:"path"`
	Place  string    `json:"place,omitempty"` // 技能所在的目录；空的只管技能库（见 at）
	Tree   string    `json:"tree"`
	Name   string    `json:"name"`
	Commit string    `json:"commit,omitempty"`
	At     time.Time `json:"at"`
}

// stopMark 是「保留本地，不再检查更新」：这个来源装在这里的技能不再联网检查。
type stopMark struct {
	Source SourceID  `json:"source"`
	Place  string    `json:"place"`
	Name   string    `json:"name"`
	At     time.Time `json:"at"`
}

// updateStore 是 updates.json 的内容。
type updateStore struct {
	Baselines    []baselineMark `json:"baselines,omitempty"`
	Ignored      []ignoreMark   `json:"ignored,omitempty"`
	Stopped      []stopMark     `json:"stopped,omitempty"`
	Check        *checkRecord   `json:"check,omitempty"`
	Transactions []*transaction `json:"transactions,omitempty"`
	Imports      []importMark   `json:"imports,omitempty"` // 从「添加技能」装进来的（import_add.go）
}

func updatesFile(home string) string { return filepath.Join(appDir(home), "updates.json") }

// stagingDir 是下载和交换用的暂存目录。
func stagingDir(home string) string { return filepath.Join(appDir(home), "staging") }

// loadUpdates 读应用状态。没有文件时是空的；读不了时报错——里面可能有
// 没做完的更新，不能当成空的覆盖掉。
func loadUpdates(home string) (*updateStore, error) {
	st := &updateStore{}
	b, err := os.ReadFile(updatesFile(home))
	if errors.Is(err, fs.ErrNotExist) {
		return st, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(b, st); err != nil {
		return nil, err
	}
	return st, nil
}

// updatesMu 串起 updates.json 的所有写入。
var updatesMu sync.Mutex

// save 写下应用状态（临时文件 + 刷盘 + 改名）。
//
// Imports 不以 st 为准：更新流程手里的 st 是扫描时读的，可能早于期间的
// 添加、删除、撤销；Imports 只经 editUpdates 改，这里保留盘上的。
func (st *updateStore) save(home string) error {
	updatesMu.Lock()
	defer updatesMu.Unlock()
	if disk, err := loadUpdates(home); err == nil {
		st.Imports = disk.Imports
	}
	return st.write(home)
}

// editUpdates 在锁内读最新的 updates.json、交给 fn 改、写回，返回写下的内容。
// fn 出错时不写。改 Imports 一律走这里。
func editUpdates(home string, fn func(st *updateStore) error) (*updateStore, error) {
	updatesMu.Lock()
	defer updatesMu.Unlock()
	st, err := loadUpdates(home)
	if err != nil {
		return nil, err
	}
	if err := fn(st); err != nil {
		return nil, err
	}
	return st, st.write(home)
}

func (st *updateStore) write(home string) error {
	b, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return err
	}
	if err := atomicConfigWrite(updatesFile(home), string(b)+"\n"); err != nil {
		return err
	}
	return syncDirectory(appDir(home))
}

func samePlace(a, b string) bool { return filepath.Clean(a) == filepath.Clean(b) }

func (st *updateStore) baseline(src SourceID, skillPath, place, recorded string) (string, bool) {
	p := skillDir(skillPath)
	for _, b := range st.Baselines {
		if b.Source == src && b.Path == p && samePlace(b.Place, place) && b.Recorded == recorded {
			return b.Tree, true
		}
	}
	return "", false
}

// setBaseline 记下基线；同一个来源、路径、位置的旧基线换掉。
func (st *updateStore) setBaseline(src SourceID, skillPath, place, recorded, tree string) {
	p := skillDir(skillPath)
	st.Baselines = slices.DeleteFunc(st.Baselines, func(b baselineMark) bool {
		return b.Source == src && b.Path == p && samePlace(b.Place, place)
	})
	st.Baselines = append(st.Baselines, baselineMark{src, p, filepath.Clean(place), recorded, tree})
}

// ignoreAt 报告忽略记录管不管装在 place 的技能。没记位置的是加入「添加
// 技能」之前写的，那时只有技能库里的技能能更新，只管技能库。
func (m ignoreMark) at(place, root string) bool {
	if m.Place == "" {
		return samePlace(filepath.Dir(place), root)
	}
	return samePlace(m.Place, place)
}

func (st *updateStore) ignored(src SourceID, skillPath, place, root, tree string) bool {
	p := skillDir(skillPath)
	return slices.ContainsFunc(st.Ignored, func(m ignoreMark) bool {
		return m.Source == src && m.Path == p && m.Tree == tree && m.at(place, root)
	})
}

func (st *updateStore) ignore(src SourceID, skillPath, place, root, tree string) *ignoreMark {
	if !st.ignored(src, skillPath, place, root, tree) {
		st.Ignored = append(st.Ignored, ignoreMark{Source: src, Path: skillDir(skillPath), Place: filepath.Clean(place), Tree: tree, At: time.Now()})
	}
	p := skillDir(skillPath)
	i := slices.IndexFunc(st.Ignored, func(m ignoreMark) bool {
		return m.Source == src && m.Path == p && m.Tree == tree && m.at(place, root)
	})
	return &st.Ignored[i]
}

func (st *updateStore) unignore(src SourceID, skillPath, place, root, tree string) {
	p := skillDir(skillPath)
	st.Ignored = slices.DeleteFunc(st.Ignored, func(m ignoreMark) bool {
		return m.Source == src && m.Path == p && m.Tree == tree && m.at(place, root)
	})
}

func (st *updateStore) stopped(src SourceID, place string) bool {
	return slices.ContainsFunc(st.Stopped, func(m stopMark) bool { return m.Source == src && samePlace(m.Place, place) })
}

func (st *updateStore) stop(src SourceID, place, name string) {
	if !st.stopped(src, place) {
		st.Stopped = append(st.Stopped, stopMark{src, filepath.Clean(place), name, time.Now()})
	}
}

func (st *updateStore) resume(src SourceID, place string) {
	st.Stopped = slices.DeleteFunc(st.Stopped, func(m stopMark) bool { return m.Source == src && samePlace(m.Place, place) })
}

// checkRecord 是上次检查：什么时候、查完没有、各技能查到什么。只存查到的
// 上游情况，本机内容每次重新算，不当成文件没变的证明。
type checkRecord struct {
	At       time.Time      `json:"at"`
	Complete bool           `json:"complete"` // 所有该查的仓库都查完了
	Repos    []repoResult   `json:"repos,omitempty"`
	Skills   []skillUpdates `json:"skills,omitempty"`
}

// repoResult 是一个仓库（+ ref）的检查结果。
type repoResult struct {
	Source     SourceID  `json:"source"`
	Commit     string    `json:"commit,omitempty"`
	CommitDate time.Time `json:"commitDate,omitempty"`
	At         time.Time `json:"at"`
	Done       bool      `json:"done"`            // 这一次查完了（成功或明确失败）
	Fail       string    `json:"fail,omitempty"`  // 失败原因
	Reset      time.Time `json:"reset,omitempty"` // 额度恢复时间
}

// skillUpdates 是一条安装记录在上游查到的情况。
type skillUpdates struct {
	Blocked    UpdateState `json:"blocked,omitempty"`
	Reason     string      `json:"reason,omitempty"`
	Recorded   string      `json:"recorded"`
	Files      []TreeEntry `json:"files,omitempty"`
	Name       string      `json:"name"`
	Source     SourceID    `json:"source"`
	Path       string      `json:"path"`
	Commit     string      `json:"commit,omitempty"`
	CommitDate time.Time   `json:"commitDate,omitempty"`
	Upstream   string      `json:"upstream,omitempty"` // 上游这个目录的 tree 哈希
	Found      upFound     `json:"found"`
	Fail       string      `json:"fail,omitempty"`
	Base       string      `json:"base,omitempty"` // 推导出的安装基线（可能是基线未知时为空）
}

// upFound 是上游有没有这个技能。
type upFound string

const (
	foundSame      upFound = "same"      // 旧路径还在
	foundRemoved   upFound = "removed"   // 完整读到仓库，旧路径不在，也没有同名的
	foundMoved     upFound = "moved"     // 旧路径不在，有唯一同名的
	foundAmbiguous upFound = "ambiguous" // 旧路径不在，同名的不止一个
	foundFailed    upFound = "failed"    // 没查成
)

// transaction 是一次更新的事务记录，崩溃后按它和实际的目录、记录恢复。
type transaction struct {
	ID                               string `json:"id"`
	Item                             UpdateItem
	Stage                            string
	TargetID, StageID                directoryID
	RecordFile, RecordHash, RecordAt string
	Policy, Status                   string
}
