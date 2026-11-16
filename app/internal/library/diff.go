package library

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// 对比副本：两种内容逐个文件、逐行对比。

// RowKind 是对比里一行的情况。
type RowKind int

const (
	Same     RowKind = iota // 两边一样
	Changed                 // 两边都有，内容改了
	Deleted                 // 只在左边（基准）
	Inserted                // 只在右边
)

// DiffRow 是并排对比的一行。行号从 1 起，0 是这一边没有这一行。
type DiffRow struct {
	Kind        RowKind
	Left, Right int
	LeftText    string
	RightText   string
}

// FileDiff 是一个有差异的文件。
type FileDiff struct {
	Path           string
	Rows           []DiffRow
	Added, Deleted int  // 右边比左边多的行、少的行
	OnlyLeft       bool // 只在基准里
	OnlyRight      bool // 只在另一种里
	Binary         bool // 至少一边没读内容（见 File.Binary），只知道不一样
	ModeOnly       bool // 内容一样，只是执行权限不同
	Left, Right    File // 两边的文件，没有的一边是零值
}

// Compare 逐个文件对比两种内容，只返回有差异的文件，SKILL.md 在前。
func (l *Library) Compare(base, other *Version) []FileDiff {
	left, right := l.VersionFiles(base), l.VersionFiles(other)
	byPath := func(files []File) map[string]File {
		m := map[string]File{}
		for _, f := range files {
			m[f.Path] = f
		}
		return m
	}
	lm, rm := byPath(left), byPath(right)
	var paths []string
	for _, f := range slices.Concat(left, right) {
		if !slices.Contains(paths, f.Path) {
			paths = append(paths, f.Path)
		}
	}
	slices.SortStableFunc(paths, func(a, b string) int { return strings.Compare(fileOrder(a), fileOrder(b)) })
	var out []FileDiff
	for _, p := range paths {
		lf, inL := lm[p]
		rf, inR := rm[p]
		if inL && inR && l.sameFile(base, other, lf, rf) {
			continue
		}
		d := FileDiff{Path: p, OnlyLeft: !inR, OnlyRight: !inL, Binary: lf.Binary || rf.Binary, Left: lf, Right: rf}
		if inL && inR && lf.Exec != rf.Exec {
			lf.Exec, rf.Exec = false, false
			d.ModeOnly = l.sameFile(base, other, lf, rf)
		}
		if !d.Binary {
			d.Rows = DiffLines(lf.Content, rf.Content)
			for _, r := range d.Rows {
				switch r.Kind {
				case Changed:
					d.Added++
					d.Deleted++
				case Deleted:
					d.Deleted++
				case Inserted:
					d.Added++
				}
			}
		}
		out = append(out, d)
	}
	return out
}

// sameFile 报告两种内容里的同一个文件是否一样。没读内容的文件 Content
// 都是空的，大小一样时要回到磁盘上逐字节比。
func (l *Library) sameFile(base, other *Version, lf, rf File) bool {
	if lf.Unreadable || rf.Unreadable || lf.Exec != rf.Exec || lf.Link != rf.Link {
		return false
	}
	if lf.Binary != rf.Binary || lf.Size != rf.Size || lf.Content != rf.Content {
		return false
	}
	if !lf.Binary || !l.disk {
		return true
	}
	return sameBytes(filepath.Join(base.Real, lf.Path), filepath.Join(other.Real, rf.Path))
}

// sameBytes 逐块比较两个文件的字节；任何一个读不了都算不一样。
func sameBytes(a, b string) bool {
	fa, err := os.Open(a)
	if err != nil {
		return false
	}
	defer fa.Close()
	fb, err := os.Open(b)
	if err != nil {
		return false
	}
	defer fb.Close()
	ba, bb := make([]byte, 32<<10), make([]byte, 32<<10)
	for {
		na, ea := io.ReadFull(fa, ba)
		nb, eb := io.ReadFull(fb, bb)
		if na != nb || !bytes.Equal(ba[:na], bb[:nb]) {
			return false
		}
		if ea != nil || eb != nil {
			// 两边都读到结尾才算一样；读出错（不是 EOF）算不一样
			return isEOF(ea) && isEOF(eb)
		}
	}
}

func isEOF(err error) bool { return err == io.EOF || err == io.ErrUnexpectedEOF }

// DiffLines 逐行对比两段文字，并排排好：删掉的和加上的成对放在同一行
// （Changed），多出来的一边留空。
func DiffLines(a, b string) []DiffRow {
	al, bl := splitLines(a), splitLines(b)
	// 先去掉相同的开头和结尾，中间再求最长公共子序列
	pre := 0
	for pre < len(al) && pre < len(bl) && al[pre] == bl[pre] {
		pre++
	}
	suf := 0
	for suf < len(al)-pre && suf < len(bl)-pre && al[len(al)-1-suf] == bl[len(bl)-1-suf] {
		suf++
	}
	var rows []DiffRow
	for i := 0; i < pre; i++ {
		rows = append(rows, DiffRow{Same, i + 1, i + 1, al[i], bl[i]})
	}
	ma, mb := al[pre:len(al)-suf], bl[pre:len(bl)-suf]
	var dels, ins []int // 还没配对的删掉的、加上的行（下标）
	flush := func() {
		n := max(len(dels), len(ins))
		for k := 0; k < n; k++ {
			r := DiffRow{}
			switch {
			case k < len(dels) && k < len(ins):
				r = DiffRow{Changed, dels[k] + 1, ins[k] + 1, al[dels[k]], bl[ins[k]]}
			case k < len(dels):
				r = DiffRow{Deleted, dels[k] + 1, 0, al[dels[k]], ""}
			default:
				r = DiffRow{Inserted, 0, ins[k] + 1, "", bl[ins[k]]}
			}
			rows = append(rows, r)
		}
		dels, ins = nil, nil
	}
	for _, op := range lcs(ma, mb) {
		i, j := op.a+pre, op.b+pre
		switch op.kind {
		case Same:
			flush()
			rows = append(rows, DiffRow{Same, i + 1, j + 1, al[i], bl[j]})
		case Deleted:
			dels = append(dels, i)
		case Inserted:
			ins = append(ins, j)
		}
	}
	flush()
	for k := suf; k > 0; k-- {
		i, j := len(al)-k, len(bl)-k
		rows = append(rows, DiffRow{Same, i + 1, j + 1, al[i], bl[j]})
	}
	return rows
}

func splitLines(s string) []string {
	if s == "" {
		return nil
	}
	return strings.Split(strings.TrimSuffix(s, "\n"), "\n")
}

type lcsOp struct {
	kind RowKind
	a, b int
}

// 中间部分太长时不求公共子序列，整段算改了。
const maxLCSCells = 4_000_000

// lcs 是把 a 变成 b 的一串操作：相同、删掉、加上。删掉的排在加上的前面。
func lcs(a, b []string) []lcsOp {
	n, m := len(a), len(b)
	var ops []lcsOp
	if n*m > maxLCSCells {
		for i := range a {
			ops = append(ops, lcsOp{Deleted, i, 0})
		}
		for j := range b {
			ops = append(ops, lcsOp{Inserted, 0, j})
		}
		return ops
	}
	// t[i][j] 是 a[i:] 和 b[j:] 的最长公共子序列长度
	t := make([][]int32, n+1)
	for i := range t {
		t[i] = make([]int32, m+1)
	}
	for i := n - 1; i >= 0; i-- {
		for j := m - 1; j >= 0; j-- {
			if a[i] == b[j] {
				t[i][j] = t[i+1][j+1] + 1
			} else {
				t[i][j] = max(t[i+1][j], t[i][j+1])
			}
		}
	}
	i, j := 0, 0
	for i < n && j < m {
		switch {
		case a[i] == b[j]:
			ops = append(ops, lcsOp{Same, i, j})
			i++
			j++
		case t[i+1][j] >= t[i][j+1]:
			ops = append(ops, lcsOp{Deleted, i, j})
			i++
		default:
			ops = append(ops, lcsOp{Inserted, i, j})
			j++
		}
	}
	for ; i < n; i++ {
		ops = append(ops, lcsOp{Deleted, i, j})
	}
	for ; j < m; j++ {
		ops = append(ops, lcsOp{Inserted, i, j})
	}
	return ops
}

// InlineChange 是一行改了的部分：两边去掉相同的开头和结尾，剩下的
// 按字（rune）算，返回左右两边改了的那段 [start, end)。
func InlineChange(a, b string) (aStart, aEnd, bStart, bEnd int) {
	ar, br := []rune(a), []rune(b)
	pre := 0
	for pre < len(ar) && pre < len(br) && ar[pre] == br[pre] {
		pre++
	}
	suf := 0
	for suf < len(ar)-pre && suf < len(br)-pre && ar[len(ar)-1-suf] == br[len(br)-1-suf] {
		suf++
	}
	return pre, len(ar) - suf, pre, len(br) - suf
}
