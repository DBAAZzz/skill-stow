package library

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"sync"
	"time"

	"skillmanager/locales"
)

// npx skills 的全局安装记录（.skill-lock.json）：每个技能一条，记着从哪里
// 装的、装的是哪个版本。本应用只认 version 3；别的版本只读，不写。

var (
	// ErrRecordUnusable：安装记录不是 version 3、解析不了或已经不在，不能写。
	ErrRecordUnusable = textErr(locales.RecordUnusable)
	// ErrRecordChanged：要改的那一条和读到时不一样了（被其他程序改过、删掉或换了来源）。
	ErrRecordChanged = textErr(locales.RecordChanged)
)

// lockVersion 是本应用能写的安装记录版本。
const lockVersion = 3

// 一份安装记录由所有技能共用；串行执行完整的读、改、写。
var recordMu sync.Mutex

// lockFile 是 npx skills 的全局安装记录：设了 XDG_STATE_HOME 且那里有记录时
// 用那份（和 npx skills 一样），否则 ~/.agents/.skill-lock.json。
func lockFile(home string) string {
	if state := os.Getenv("XDG_STATE_HOME"); state != "" {
		if p := filepath.Join(state, "skills", ".skill-lock.json"); exists(p) {
			return p
		}
	}
	return filepath.Join(home, ".agents", ".skill-lock.json")
}

// LockEntry 是安装记录里的一条。
type LockEntry struct {
	Name            string // 键：技能目录名
	Source          string // 如 antfu/skills、open.feishu.cn
	SourceType      string // github、well-known、gitlab、git、local、download
	SourceURL       string
	SourceBaseURL   string // well-known 的入口
	SkillPath       string // 在仓库里的路径，如 skills/vite/SKILL.md
	SkillFolderHash string // github：目录的 Git tree；根目录安装的是提交 ID
	Ref             string
	InstalledAt     time.Time
	UpdatedAt       time.Time
	Raw             string // 这一条的原文
	// Place 只在应用记录的条目上有：「指定的 Agent」模式装的技能不在技能库，
	// 这是它文件所在的目录（import_add.go 的 appEntries）。安装记录的条目为空。
	Place string
}

// InstallRecords 是读到的安装记录。
type InstallRecords struct {
	File    string
	Version int
	Entries []LockEntry // 按文件里的顺序
}

// Get 按名称找一条，没有为 nil。
func (r *InstallRecords) Get(name string) *LockEntry {
	if r == nil {
		return nil
	}
	for i := range r.Entries {
		if r.Entries[i].Name == name {
			return &r.Entries[i]
		}
	}
	return nil
}

// Names 是全部条目的名称，按文件里的顺序。
func (r *InstallRecords) Names() []string {
	var out []string
	if r != nil {
		for _, e := range r.Entries {
			out = append(out, e.Name)
		}
	}
	return out
}

// Writable 报告这份记录能不能写：只写 version 3。
func (r *InstallRecords) Writable() error {
	if r == nil || r.Version != lockVersion {
		return ErrRecordUnusable
	}
	return nil
}

// ReadInstallRecords 读安装记录。没有记录文件时返回 nil, nil；解析不了时
// 返回错误。
func ReadInstallRecords(home string) (*InstallRecords, error) {
	file := lockFile(home)
	src, err := os.ReadFile(file)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return parseRecords(file, string(src))
}

func parseRecords(file, src string) (*InstallRecords, error) {
	doc, err := parseJSON(src)
	if err != nil {
		return nil, err
	}
	var head struct {
		Version json.Number `json:"version"`
	}
	if err := json.Unmarshal([]byte(doc.clean), &head); err != nil {
		return nil, err
	}
	r := &InstallRecords{File: file}
	if v, err := strconv.Atoi(head.Version.String()); err == nil {
		r.Version = v
	}
	skills, err := doc.at([]string{"skills"})
	if err != nil || skills == nil {
		return r, err
	}
	if skills.kind != '{' {
		return nil, errors.New(locales.UpdateInstallSkillsNotObject())
	}
	for _, m := range skills.members {
		raw := src[m.value.start:m.value.end]
		var f struct {
			Source          string    `json:"source"`
			SourceType      string    `json:"sourceType"`
			SourceURL       string    `json:"sourceUrl"`
			SourceBaseURL   string    `json:"sourceBaseUrl"`
			SkillPath       string    `json:"skillPath"`
			SkillFolderHash string    `json:"skillFolderHash"`
			Ref             string    `json:"ref"`
			InstalledAt     time.Time `json:"installedAt"`
			UpdatedAt       time.Time `json:"updatedAt"`
		}
		// 认不出的字段（类型不对等）留空，不猜
		json.Unmarshal([]byte(doc.clean[m.value.start:m.value.end]), &f)
		r.Entries = append(r.Entries, LockEntry{
			Name: m.key, Source: f.Source, SourceType: f.SourceType, SourceURL: f.SourceURL, SourceBaseURL: f.SourceBaseURL,
			SkillPath: f.SkillPath, SkillFolderHash: f.SkillFolderHash, Ref: f.Ref,
			InstalledAt: f.InstalledAt, UpdatedAt: f.UpdatedAt, Raw: raw,
		})
	}
	return r, nil
}

// sameEntry 报告两条记录的内容相同（不看空白和键的写法）。ignore 里的字段不比。
func sameEntry(a, b string, ignore ...string) bool {
	var x, y map[string]any
	if json.Unmarshal([]byte(a), &x) != nil || json.Unmarshal([]byte(b), &y) != nil {
		return false
	}
	for _, k := range ignore {
		delete(x, k)
		delete(y, k)
	}
	return reflect.DeepEqual(x, y)
}

// setRecordVersion 把 name 这一条的 skillFolderHash 改成 hash、updatedAt 改成
// updatedAt，别的字段、别的条目和顶层一概不动。写之前重读：记录不能写时
// 返回 ErrRecordUnusable；这一条已经不是 orig 时返回 ErrRecordChanged——
// 除非它已经是要写的结果（只差 updatedAt），那就当作写过了。写完读回来核对。
// 返回写进去的这一条的原文。
func setRecordVersion(home, name, orig, hash, updatedAt string) (string, error) {
	recordMu.Lock()
	defer recordMu.Unlock()
	file := lockFile(home)
	src, err := os.ReadFile(file)
	if errors.Is(err, fs.ErrNotExist) {
		return "", ErrRecordUnusable
	}
	if err != nil {
		return "", err
	}
	recs, err := parseRecords(file, string(src))
	if err != nil {
		return "", ErrRecordUnusable
	}
	if err := recs.Writable(); err != nil {
		return "", err
	}
	cur := recs.Get(name)
	if cur == nil {
		return "", ErrRecordChanged
	}
	want, err := withRecordVersion(cur.Raw, hash, updatedAt)
	if err != nil {
		return "", err
	}
	if !sameEntry(cur.Raw, orig) {
		if expected, err := withRecordVersion(orig, hash, updatedAt); err == nil && sameEntry(cur.Raw, expected, "updatedAt") {
			return cur.Raw, nil
		}
		return "", ErrRecordChanged
	}
	out, err := editJSON(string(src), []string{"skills", name}, &want)
	if err != nil {
		return "", err
	}
	if err := atomicConfigWrite(file, out); err != nil {
		return "", err
	}
	real, err := filepath.EvalSymlinks(file)
	if err != nil {
		return "", err
	}
	if err = syncDirectory(filepath.Dir(real)); err != nil {
		return "", err
	}
	back, err := ReadInstallRecords(home)
	if err != nil || back.Get(name) == nil || !sameEntry(back.Get(name).Raw, want) {
		return "", fmt.Errorf("%s%w", locales.UpdateWriteVerificationFailed(), ErrRecordChanged)
	}
	return want, nil
}

// withRecordVersion 是改了 skillFolderHash、updatedAt 的这一条原文，缩进和其他字段照旧。
func withRecordVersion(raw, hash, updatedAt string) (string, error) {
	h, t := strconv.Quote(hash), strconv.Quote(updatedAt)
	// 原文是一个对象；用 editJSON 只换这两个值，没有的字段补在最后
	doc := `{"x": ` + raw + `}`
	doc, err := editJSON(doc, []string{"x", "skillFolderHash"}, &h)
	if err != nil {
		return "", err
	}
	if doc, err = editJSON(doc, []string{"x", "updatedAt"}, &t); err != nil {
		return "", err
	}
	d, err := parseJSON(doc)
	if err != nil {
		return "", err
	}
	n, err := d.at([]string{"x"})
	if err != nil || n == nil {
		return "", errors.New(locales.UpdateInstallEntryNotObject())
	}
	return doc[n.start:n.end], nil
}
