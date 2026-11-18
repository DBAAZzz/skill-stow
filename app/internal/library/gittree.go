package library

import (
	"bytes"
	"crypto/sha1"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"skillmanager/internal/security"
	"skillmanager/locales"
)

// 技能目录的版本标识：和 Git 一样的目录哈希（tree SHA-1）。路径、原始
// 字节、可执行位、链接目标都算进去，不跳过隐藏文件，不转换换行。GitHub
// 来源的安装记录和上游目录树用的就是它。

// TreeHash 是目录的 Git tree 哈希。读不了的文件、认不出的文件类型都报错，
// 不跳过——跳过了就可能把不同的内容说成一样。空目录和 Git 一样不算。
func TreeHash(dir string) (string, error) {
	sum, _, err := treeHash(dir)
	return hex.EncodeToString(sum), err
}

func treeHash(dir string) (sum []byte, nonEmpty bool, err error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, false, err
	}
	type item struct {
		sort []byte
		line []byte
	}
	var items []item
	for _, e := range entries {
		p := filepath.Join(dir, e.Name())
		fi, err := os.Lstat(p)
		if err != nil {
			return nil, false, err
		}
		name := []byte(e.Name())
		var mode string
		var h []byte
		sort := name
		switch {
		case fi.Mode()&os.ModeSymlink != 0:
			target, err := os.Readlink(p)
			if err != nil {
				return nil, false, err
			}
			mode, h = "120000", gitObject("blob", []byte(target))
		case fi.IsDir():
			sub, ok, err := treeHash(p)
			if err != nil {
				return nil, false, err
			}
			if !ok {
				continue
			}
			mode, h, sort = "40000", sub, append(slices.Clone(name), '/')
		case fi.Mode().IsRegular():
			b, err := os.ReadFile(p)
			if err != nil {
				return nil, false, err
			}
			mode = "100644"
			if fi.Mode()&0o100 != 0 {
				mode = "100755"
			}
			h = gitObject("blob", b)
		default:
			return nil, false, errors.New(locales.UpdateUnsupportedFileType(p))
		}
		line := append([]byte(mode+" "), name...)
		line = append(append(line, 0), h...)
		items = append(items, item{sort, line})
	}
	slices.SortFunc(items, func(a, b item) int { return bytes.Compare(a.sort, b.sort) })
	var body []byte
	for _, it := range items {
		body = append(body, it.line...)
	}
	return gitObject("tree", body), len(items) > 0, nil
}

// gitObject 是 Git 对象的 SHA-1：「类型 长度\0内容」。
func gitObject(kind string, data []byte) []byte {
	h := sha1.New()
	h.Write([]byte(kind + " " + strconv.Itoa(len(data)) + "\x00"))
	h.Write(data)
	return h.Sum(nil)
}

// blobID 是文件内容的 Git blob 哈希，十六进制。
func blobID(data []byte) string { return hex.EncodeToString(gitObject("blob", data)) }

// inputTree 用安全检查读到的那份快照算出 Git tree 哈希，和 TreeHash 对同样
// 内容的结果相同。检查的字节和要装的内容靠它对上：两次分别读目录，中间被
// 换了也对不上。快照里有没读到正文的文件或特殊文件时算不出，返回 false。
func inputTree(in security.Input) (string, bool) {
	type node struct {
		files map[string][]byte // 名称 → 「模式 名称\0哈希」那一行
		dirs  map[string]*node
	}
	newNode := func() *node { return &node{files: map[string][]byte{}, dirs: map[string]*node{}} }
	root := newNode()
	for _, f := range in.Files {
		parts := strings.Split(f.Path, "/")
		n := root
		for _, d := range parts[:len(parts)-1] {
			if n.dirs[d] == nil {
				n.dirs[d] = newNode()
			}
			n = n.dirs[d]
		}
		name := parts[len(parts)-1]
		var mode string
		var h []byte
		switch {
		case f.Kind == security.Symlink && f.Link != "":
			mode, h = "120000", gitObject("blob", []byte(f.Link))
		case f.Kind == security.Regular && (f.Data != nil && f.Gap == "" || f.Blob != nil):
			mode = "100644"
			if f.Mode&0o100 != 0 {
				mode = "100755"
			}
			h = f.Blob
			if h == nil {
				h = gitObject("blob", f.Data)
			}
		default:
			return "", false
		}
		n.files[name] = append(append([]byte(mode+" "+name), 0), h...)
	}
	var hash func(n *node) ([]byte, bool)
	hash = func(n *node) ([]byte, bool) {
		type item struct{ sort, line []byte }
		var items []item
		for name, line := range n.files {
			items = append(items, item{[]byte(name), line})
		}
		for name, sub := range n.dirs {
			h, ok := hash(sub)
			if !ok {
				continue // Git 不记空目录
			}
			items = append(items, item{[]byte(name + "/"), append(append([]byte("40000 "+name), 0), h...)})
		}
		slices.SortFunc(items, func(a, b item) int { return bytes.Compare(a.sort, b.sort) })
		var body []byte
		for _, it := range items {
			body = append(body, it.line...)
		}
		return gitObject("tree", body), len(items) > 0
	}
	if len(in.Gaps) > 0 {
		return "", false
	}
	sum, _ := hash(root)
	return hex.EncodeToString(sum), true
}
