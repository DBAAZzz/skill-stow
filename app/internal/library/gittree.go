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
