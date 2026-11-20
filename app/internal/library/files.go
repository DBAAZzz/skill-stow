package library

import (
	"bytes"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"unicode/utf8"
)

const (
	maxFiles    = 300
	maxFileSize = 512 << 10
)

// LoadFiles 读出技能目录下的文件，打开阅读视图时才读。SKILL.md 在最前，
// 其余按路径排；跳过 .DS_Store、.git、node_modules、__pycache__ 等噪音项，
// 其余隐藏文件照常列出。太大或不是文本的文件只列出，不读内容（Binary）。
func (l *Library) LoadFiles(s *Skill) {
	if s.Files != nil || s.Real == "" {
		return
	}
	s.Files = readFiles(s.Real)
}

// readFiles 读出目录下的文件，规则同 LoadFiles。
func readFiles(dir string) []File { return listFiles(dir, false) }

// readFilesStrict 读出目录下的全部文件：不跳过任何噪音项和缓存，记下执行位；
// 链接列成一个文件，内容是它指向哪里；读不了的标出来。对比副本用。
func readFilesStrict(dir string) []File { return listFiles(dir, true) }

func listFiles(dir string, strict bool) []File {
	var files []File
	filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			if strict && p != dir {
				rel, _ := filepath.Rel(dir, p)
				files = append(files, File{Path: filepath.ToSlash(rel), Binary: true, Unreadable: true})
			}
			return nil
		}
		if strict && p != dir && d.Type()&fs.ModeSymlink != 0 {
			rel, _ := filepath.Rel(dir, p)
			target, err := os.Readlink(p)
			f := File{Path: filepath.ToSlash(rel), Content: "→ " + target, Link: true}
			if err != nil {
				f = File{Path: f.Path, Binary: true, Unreadable: true}
			}
			f.Size = len(f.Content)
			files = append(files, f)
			return nil
		}
		if !strict && p != dir && skipped(d.Name()) {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			return nil
		}
		if len(files) >= maxFiles {
			return filepath.SkipAll
		}
		rel, _ := filepath.Rel(dir, p)
		f := File{Path: filepath.ToSlash(rel)}
		if fi, err := d.Info(); err == nil {
			f.Size = int(fi.Size())
			f.Exec = fi.Mode()&0o100 != 0
		}
		// 只读普通文件（链接跟到普通文件也行）：管道、设备之类打开就可能卡住
		if fi, err := os.Stat(p); err != nil || !fi.Mode().IsRegular() {
			f.Binary, f.Unreadable = true, strict
		} else if f.Size <= maxFileSize {
			b, err := os.ReadFile(p)
			switch {
			case err != nil:
				f.Binary, f.Unreadable = true, strict
			case utf8.Valid(b) && !bytes.Contains(b, []byte{0}):
				f.Content = string(b)
			default:
				f.Binary = true
			}
		} else {
			f.Binary = true
		}
		files = append(files, f)
		return nil
	})
	slices.SortStableFunc(files, func(a, b File) int {
		return strings.Compare(fileOrder(a.Path), fileOrder(b.Path))
	})
	if files == nil {
		files = []File{}
	}
	return files
}

// fileOrder 让 SKILL.md 排第一，根目录的文件排在子目录前。
func fileOrder(p string) string {
	switch {
	case p == "SKILL.md":
		return "0"
	case !strings.Contains(p, "/"):
		return "1" + p
	}
	return "2" + p
}
