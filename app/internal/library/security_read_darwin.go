package library

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"golang.org/x/sys/unix"

	"skillmanager/internal/security"
)

// 安全扫描的受控读取（只做 macOS）。
//
// 每次解析只有一段文件名，都相对已经打开的目录句柄：目录用
// O_DIRECTORY|O_NOFOLLOW|O_NONBLOCK 打开，普通文件用 O_NOFOLLOW|O_NONBLOCK
// 打开，打开后用 fstat 核对设备号与 inode 还是先前看到的那个对象。因此
// 读到的字节一定来自该条目本身；被并发换成链接、管道或别的文件时记为
// “扫描期间有变化”，不会跟着走，也不会因为管道卡住。

// SecuritySupported 报告本平台能不能做本地安全检查。
func SecuritySupported() bool { return true }

// readLimits 是一次读取的上限（初值，基准验证后再调）。
type readLimits struct {
	entries int
	depth   int
	file    int64
	total   int64
}

var defaultLimits = readLimits{entries: 2000, depth: 32, file: 1 << 20, total: 20 << 20}

// metaEntry 是元信息清单的一项：只看类型、大小、修改时间、权限、链接目标，
// 不读正文。另记 inode 和状态变化时间：修改时间能被改回去，这两个不能，
// 换了文件或改过内容一定对不上。
type metaEntry struct {
	path  string
	kind  byte // f 普通文件、d 目录、l 链接、s 其他
	size  int64
	mtime int64
	ctime int64
	ino   uint64
	perm  uint32
	link  string
}

func metaDigestOf(entries []metaEntry) string {
	sort.Slice(entries, func(i, j int) bool { return entries[i].path < entries[j].path })
	h := sha256.New()
	field := func(s string) {
		var n [8]byte
		binary.BigEndian.PutUint64(n[:], uint64(len(s)))
		h.Write(n[:])
		h.Write([]byte(s))
	}
	for _, e := range entries {
		field(string(e.kind))
		field(e.path)
		field(strconv.FormatInt(e.size, 10))
		field(strconv.FormatInt(e.mtime, 10))
		field(strconv.FormatInt(e.ctime, 10))
		field(strconv.FormatUint(e.ino, 10))
		field(strconv.FormatUint(uint64(e.perm), 8))
		field(e.link)
	}
	return hex.EncodeToString(h.Sum(nil))
}

var errWalkLimit = errors.New("walk limit")

// walker 在目录句柄上逐级遍历。read 为假时只收集元信息。
type walker struct {
	ctx   context.Context
	lim   readLimits
	read  bool
	bad   bool // 只看元信息时遇到读不了的条目：清单不可信
	n     int
	total int64
	meta  []metaEntry
	in    security.Input
}

type fileID struct {
	dev int32
	ino uint64
}

func idOf(st *unix.Stat_t) fileID { return fileID{st.Dev, st.Ino} }

func openDir(parent int, name string) (int, error) {
	for {
		fd, err := unix.Openat(parent, name, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
		if err != unix.EINTR {
			return fd, err
		}
	}
}

// openRoot 打开 Skill 根目录。根目录本身允许是链接（用户看到的目录可以
// 指向实际的 Skill），这里跟随它；根以内的链接一律不跟。
func openRoot(root string) (int, unix.Stat_t, error) {
	var st unix.Stat_t
	fd, err := unix.Open(root, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
	if err != nil {
		return -1, st, err
	}
	if err := unix.Fstat(fd, &st); err != nil {
		unix.Close(fd)
		return -1, st, err
	}
	return fd, st, nil
}

func (w *walker) dir(fd int, rel string, depth int) error {
	d := os.NewFile(uintptr(fd), rel)
	// os.File 拿走 fd 的所有权，下面关它
	defer d.Close()
	if err := w.ctx.Err(); err != nil {
		return err
	}
	// 分批读目录项：一层里塞了海量条目时，到上限就停，不先全读进内存
	var names []string
	for {
		if err := w.ctx.Err(); err != nil {
			return err
		}
		batch, err := d.Readdirnames(256)
		names = append(names, batch...)
		if w.n+len(names) > w.lim.entries {
			w.hole("", security.GapFileLimit)
			return errWalkLimit
		}
		if err == io.EOF || err == nil && len(batch) == 0 {
			break
		}
		if err != nil {
			w.hole(rel, security.GapUnreadable)
			return nil
		}
	}
	sort.Strings(names)
	for _, name := range names {
		if err := w.ctx.Err(); err != nil {
			return err
		}
		p := name
		if rel != "" {
			p = rel + "/" + name
		}
		w.n++
		var st unix.Stat_t
		if err := unix.Fstatat(fd, name, &st, unix.AT_SYMLINK_NOFOLLOW); err != nil {
			w.hole(p, security.GapUnreadable)
			w.meta = append(w.meta, metaEntry{path: p, kind: '?'})
			continue
		}
		e := metaEntry{path: p, mtime: st.Mtim.Nano(), ctime: st.Ctim.Nano(), ino: st.Ino, perm: uint32(st.Mode & 0o7777)}
		switch st.Mode & unix.S_IFMT {
		case unix.S_IFDIR:
			e.kind = 'd'
			w.meta = append(w.meta, e)
			if depth+1 > w.lim.depth {
				w.hole(p, security.GapDepthLimit)
				return errWalkLimit
			}
			sub, err := openDir(fd, name)
			if err != nil {
				w.hole(p, changedOr(err, security.GapUnreadable))
				continue
			}
			var got unix.Stat_t
			if err := unix.Fstat(sub, &got); err != nil || idOf(&got) != idOf(&st) {
				unix.Close(sub)
				w.hole(p, security.GapChanged)
				continue
			}
			if err := w.dir(sub, p, depth+1); err != nil {
				return err
			}
		case unix.S_IFLNK:
			e.kind = 'l'
			e.link = readlinkat(fd, name)
			w.meta = append(w.meta, e)
			if w.read {
				w.in.Files = append(w.in.Files, security.File{Path: p, Kind: security.Symlink, Link: e.link, Escape: escapes(rel, e.link)})
			}
		case unix.S_IFREG:
			e.kind = 'f'
			e.size = st.Size
			w.meta = append(w.meta, e)
			if w.read {
				w.file(fd, name, p, &st)
			}
		default:
			e.kind = 's'
			w.meta = append(w.meta, e)
			if w.read {
				w.in.Files = append(w.in.Files, security.File{Path: p, Kind: security.Special, Mode: fs.FileMode(st.Mode & 0o777)})
			}
		}
	}
	return nil
}

func (w *walker) file(dirfd int, name, p string, st *unix.Stat_t) {
	f := security.File{Path: p, Kind: security.Regular, Mode: fs.FileMode(st.Mode & 0o777)}
	defer func() { w.in.Files = append(w.in.Files, f) }()
	if st.Size > w.lim.file {
		f.Gap = security.GapByteLimit
		return
	}
	if w.total+st.Size > w.lim.total {
		f.Gap = security.GapByteLimit
		return
	}
	var fd int
	var err error
	for {
		fd, err = unix.Openat(dirfd, name, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
		if err != unix.EINTR {
			break
		}
	}
	if err != nil {
		f.Gap = changedOr(err, security.GapUnreadable)
		return
	}
	h := os.NewFile(uintptr(fd), p)
	defer h.Close()
	var got unix.Stat_t
	if err := unix.Fstat(fd, &got); err != nil || got.Mode&unix.S_IFMT != unix.S_IFREG || idOf(&got) != idOf(st) {
		f.Gap = security.GapChanged
		return
	}
	// 读的量按单文件上限和剩余总量中小的那个算：读的时候文件在长也不会超
	limit := min(w.lim.file, w.lim.total-w.total)
	b, err := io.ReadAll(io.LimitReader(h, limit+1))
	w.total += int64(len(b))
	if err != nil {
		f.Gap = security.GapUnreadable
		return
	}
	if int64(len(b)) > limit {
		f.Gap = security.GapByteLimit
		return
	}
	// 读完再核对一次：读的过程中被改写的，读到的字节不代表任何一个版本
	var after unix.Stat_t
	if err := unix.Fstat(fd, &after); err != nil || after.Size != int64(len(b)) || after.Mtim != got.Mtim || after.Ctim != got.Ctim {
		f.Gap = security.GapChanged
		return
	}
	f.Data = b
}

func (w *walker) hole(p string, g security.Gap) {
	if !w.read {
		w.bad = true
	}
	if w.read {
		w.in.Gaps = append(w.in.Gaps, security.Hole{Path: p, Reason: g})
	}
}

// changedOr 把“被换成了链接或别的类型”识别成扫描期间变化。
func changedOr(err error, other security.Gap) security.Gap {
	if errors.Is(err, unix.ELOOP) || errors.Is(err, unix.ENOTDIR) || errors.Is(err, unix.ENOENT) {
		return security.GapChanged
	}
	return other
}

func readlinkat(dirfd int, name string) string {
	buf := make([]byte, 4096)
	n, err := unix.Readlinkat(dirfd, name, buf)
	if err != nil {
		return ""
	}
	return string(buf[:n])
}

// escapes 报告链接能否确定指向根目录外：绝对路径，或相对路径走出了根。
func escapes(dir, target string) bool {
	if target == "" {
		return false
	}
	if filepath.IsAbs(target) {
		return true
	}
	j := path.Clean(path.Join(dir, target))
	return j == ".." || strings.HasPrefix(j, "../")
}

// readSkill 读出整个 Skill 目录，同时生成元信息清单。读完后再取一次清单，
// 两次不同（或根目录被换掉）说明读的时候目录在变，记为“扫描期间有变化”。
// 返回的清单摘要只在完整读取时非空。
func readSkill(ctx context.Context, root string, lim readLimits) (security.Input, string, error) {
	fd, rootSt, err := openRoot(root)
	if err != nil {
		return security.Input{}, "", err
	}
	w := &walker{ctx: ctx, lim: lim, read: true}
	werr := w.dir(fd, "", 0)
	if werr != nil && !errors.Is(werr, errWalkLimit) {
		w.in.Gaps = append(w.in.Gaps, security.Hole{Reason: gapOfCtx(werr)})
		return w.in, "", nil
	}
	before := metaDigestOf(w.meta)
	after, id, _, err := metaWalk(ctx, root, lim)
	if err != nil || after != before || id != idOf(&rootSt) {
		w.in.Gaps = append(w.in.Gaps, security.Hole{Reason: security.GapChanged})
		return w.in, "", nil
	}
	if werr != nil {
		return w.in, "", nil // 超限：清单不完整，不能拿来判断“没变”
	}
	return w.in, before, nil
}

func gapOfCtx(err error) security.Gap {
	if errors.Is(err, context.DeadlineExceeded) {
		return security.GapDeadline
	}
	return security.GapCancelled
}

func metaWalk(ctx context.Context, root string, lim readLimits) (string, fileID, bool, error) {
	fd, st, err := openRoot(root)
	if err != nil {
		return "", fileID{}, false, err
	}
	w := &walker{ctx: ctx, lim: lim}
	if err := w.dir(fd, "", 0); err != nil {
		return "", fileID{}, false, err
	}
	return metaDigestOf(w.meta), idOf(&st), w.bad, nil
}

var errUnverifiable = errors.New("security: metadata unverifiable")

// metaDigest 只看元信息、不读正文地算出目录的清单摘要。目录读不了、
// 有条目读不了或超限时返回错误：结果只能算“无法核实”。
func metaDigest(ctx context.Context, root string, lim readLimits) (string, error) {
	d, _, bad, err := metaWalk(ctx, root, lim)
	if err != nil {
		return "", err
	}
	if bad {
		return "", errUnverifiable
	}
	return d, nil
}
