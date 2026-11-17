package library

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/format/packfile"
	"github.com/go-git/go-git/v5/plumbing/storer"
)

// 提交和目录树走 git smart HTTP 协议 v2（https://git-scm.com/docs/protocol-v2），
// 和 git clone 用的是同一个端点：不占 api.github.com 每小时 60 次的匿名额度，
// 也不依赖本机 git。
//
//   - ls-refs 把分支、标签、HEAD 解析成提交 ID。
//   - fetch 只要一层历史（deepen 1），并按 filter 去掉用不到的对象：查提交
//     用 tree:0（只回提交对象，约 1KB），取目录树用 blob:none（提交 + 全部
//     目录树，不含文件内容）。目录树是 GitHub 存的原件，哈希与 API、与 npx
//     skills 写进锁文件的 skillFolderHash 一致。
//
// 文件内容仍按 skills.sh → raw → codeload 压缩包的顺序取，见 import_read.go。

// 上限是变量，测试里调小。
var (
	// maxPackBytes 是一次 fetch 响应的上限。只含目录树的包一般几十 KB，超过
	// 这个数的仓库按「目录树不完整」处理。
	maxPackBytes = 64 << 20
	// maxTreeEntries 是展开目录树的条目上限，超过时按不完整处理。包里的对象
	// 数（目录树 + 一个提交）也受它限制。
	maxTreeEntries = 200_000
	// maxInflatedBytes 是包解开后全部对象（含 delta 展开后的大小）的上限：
	// 压缩率高的包能在 maxPackBytes 以内解出远大于它的内容。
	maxInflatedBytes = 128 << 20
)

// errPackTooBig：fetch 响应或解开后的包超出上限。
var errPackTooBig = errors.New("git pack exceeds the size limit")

// gitRef 是 ls-refs 回的一个引用。
type gitRef struct {
	Name   string // HEAD、refs/heads/main、refs/tags/v1
	SHA    string // 附注标签是剥离后的提交 ID
	Target string // HEAD 指向的分支（symref-target）
}

func (g *GitHub) Commit(ctx context.Context, repo, ref string) (Commit, error) {
	sha, err := g.resolveRef(ctx, repo, ref)
	if errors.Is(err, errShortSHA) {
		// git 协议只认完整的提交 ID；简写的交给 API 补全
		return g.apiCommit(ctx, repo, ref)
	}
	if err != nil {
		return Commit{}, err
	}
	objs, err := g.fetchObjects(ctx, repo, sha, "tree:0")
	if errors.Is(err, errPackTooBig) {
		return Commit{}, &RemoteError{Kind: RemoteOther, Err: err}
	}
	if err != nil {
		return Commit{}, err
	}
	return commitFrom(objs, sha)
}

func (g *GitHub) Tree(ctx context.Context, repo, commit string) (Tree, error) {
	objs, err := g.fetchObjects(ctx, repo, commit, "blob:none")
	if errors.Is(err, errPackTooBig) {
		return Tree{Truncated: true}, nil
	}
	if err != nil {
		return Tree{}, err
	}
	c, err := commitFrom(objs, commit)
	if err != nil {
		return Tree{}, err
	}
	var t Tree
	var walk func(dir, sha string) error
	walk = func(dir, sha string) error {
		o, ok := objs[sha]
		if !ok || o.typ != plumbing.TreeObject {
			return &RemoteError{Kind: RemoteOther, Err: fmt.Errorf("tree %s missing from pack", sha)}
		}
		items, err := parseTreeObject(o.data)
		if err != nil {
			return &RemoteError{Kind: RemoteOther, Err: err}
		}
		for _, it := range items {
			if len(t.Entries) >= maxTreeEntries {
				t.Truncated = true
				return nil
			}
			p := path.Join(dir, it.name)
			switch it.mode {
			case "40000":
				t.Entries = append(t.Entries, TreeEntry{p, "040000", "tree", it.sha, 0})
				if err := walk(p, it.sha); err != nil || t.Truncated {
					return err
				}
			case "160000":
				t.Entries = append(t.Entries, TreeEntry{p, it.mode, "commit", it.sha, 0})
			default:
				t.Entries = append(t.Entries, TreeEntry{p, it.mode, "blob", it.sha, -1})
			}
		}
		return nil
	}
	if err := walk("", c.Tree); err != nil {
		return Tree{}, err
	}
	return t, nil
}

// RefNames 是仓库里以 prefix 开头的分支名和标签名（不带 refs/heads/、refs/tags/）。
func (g *GitHub) RefNames(ctx context.Context, repo, prefix string) ([]string, error) {
	refs, err := g.lsRefs(ctx, repo, "refs/heads/"+prefix, "refs/tags/"+prefix)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, r := range refs {
		if n, ok := strings.CutPrefix(r.Name, "refs/heads/"); ok {
			out = append(out, n)
		} else if n, ok := strings.CutPrefix(r.Name, "refs/tags/"); ok {
			out = append(out, n)
		}
	}
	return out, nil
}

// errShortSHA：ref 不是分支或标签，看着像简写的提交 ID。
var errShortSHA = errors.New("abbreviated commit id")

// resolveRef 把 ref（空为默认分支）解析成完整的提交 ID。同名时分支优先于标签，
// 和 GitHub 网页一致。
func (g *GitHub) resolveRef(ctx context.Context, repo, ref string) (string, error) {
	if isSHA(ref) {
		return strings.ToLower(ref), nil
	}
	var want []string
	switch {
	case ref == "" || ref == "HEAD":
		want = []string{"HEAD"}
	case strings.HasPrefix(ref, "refs/"):
		want = []string{ref}
	default:
		want = []string{"refs/heads/" + ref, "refs/tags/" + ref}
	}
	refs, err := g.lsRefs(ctx, repo, want...)
	if err != nil {
		return "", err
	}
	// ref-prefix 是前缀匹配（main 也会带回 main-old），这里按全名挑
	for _, w := range want {
		for _, r := range refs {
			if r.Name == w {
				return r.SHA, nil
			}
		}
	}
	if len(ref) >= 4 && len(ref) < 40 && isHex(ref) {
		return "", errShortSHA
	}
	return "", &RemoteError{Kind: RemoteNotFound}
}

func isHex(s string) bool {
	for _, c := range s {
		if !('0' <= c && c <= '9' || 'a' <= c && c <= 'f' || 'A' <= c && c <= 'F') {
			return false
		}
	}
	return true
}

// lsRefs 列出名字以 prefixes 之一开头的引用。
func (g *GitHub) lsRefs(ctx context.Context, repo string, prefixes ...string) ([]gitRef, error) {
	var req bytes.Buffer
	gitCommand(&req, "ls-refs")
	writePkt(&req, "symrefs\n")
	writePkt(&req, "peel\n")
	for _, p := range prefixes {
		writePkt(&req, "ref-prefix "+p+"\n")
	}
	req.WriteString("0000")
	body, err := g.upload(ctx, repo, req.Bytes())
	if err != nil {
		return nil, err
	}
	r := pktReader{b: body}
	var refs []gitRef
	for {
		line, kind, err := r.next()
		if err != nil {
			return nil, err
		}
		if kind == pktFlush {
			return refs, nil
		}
		f := strings.Fields(string(line))
		if len(f) < 2 || f[0] == "unborn" || !isSHA(f[0]) {
			continue // 空仓库的 HEAD 是 unborn
		}
		ref := gitRef{Name: f[1], SHA: f[0]}
		for _, attr := range f[2:] {
			if v, ok := strings.CutPrefix(attr, "symref-target:"); ok {
				ref.Target = v
			} else if v, ok := strings.CutPrefix(attr, "peeled:"); ok {
				ref.SHA = v
			}
		}
		refs = append(refs, ref)
	}
}

// packObject 是包里的一个对象（已展开 delta）。
type packObject struct {
	typ  plumbing.ObjectType
	data []byte
}

// fetchObjects 取提交 sha 及 filter 留下的对象（只要这一层，不要历史）。
func (g *GitHub) fetchObjects(ctx context.Context, repo, sha, filter string) (map[string]packObject, error) {
	var req bytes.Buffer
	gitCommand(&req, "fetch")
	writePkt(&req, "no-progress\n")
	writePkt(&req, "ofs-delta\n")
	writePkt(&req, "deepen 1\n")
	writePkt(&req, "filter "+filter+"\n")
	writePkt(&req, "want "+sha+"\n")
	writePkt(&req, "done\n")
	req.WriteString("0000")
	body, err := g.upload(ctx, repo, req.Bytes())
	if err != nil {
		return nil, err
	}
	pack, err := packFromResponse(body)
	if err != nil {
		return nil, err
	}
	return parsePack(pack)
}

// upload 向仓库的 git-upload-pack 端点发一个协议 v2 请求，返回整个响应。
func (g *GitHub) upload(ctx context.Context, repo string, body []byte) ([]byte, error) {
	base := g.Git
	if base == "" {
		base = "https://github.com"
	}
	tok := strings.TrimSpace(g.Token)
	resp, err := g.send(ctx, func() (*http.Request, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/"+repo+".git/git-upload-pack", bytes.NewReader(body))
		if err != nil {
			return nil, err
		}
		req.Header.Set("Git-Protocol", "version=2")
		req.Header.Set("Content-Type", "application/x-git-upload-pack-request")
		req.Header.Set("Accept", "application/x-git-upload-pack-result")
		if tok != "" {
			req.SetBasicAuth("x-access-token", tok)
		}
		return req, nil
	})
	var re *RemoteError
	if tok == "" && errors.As(err, &re) && re.Kind == RemoteAuth {
		// 匿名访问不存在或私有的仓库，git 端点回 401 而不是 404
		return nil, &RemoteError{Kind: RemoteNotFound, Status: re.Status}
	}
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, int64(maxPackBytes)+1))
	if err != nil {
		return nil, g.wrap(ctx, err)
	}
	if len(b) > maxPackBytes {
		return nil, errPackTooBig
	}
	return b, nil
}

// gitCommand 写请求的开头：命令和能力，到分隔符为止。
func gitCommand(b *bytes.Buffer, cmd string) {
	writePkt(b, "command="+cmd+"\n")
	writePkt(b, "agent=skill-manager\n")
	writePkt(b, "object-format=sha1\n")
	b.WriteString("0001")
}

func writePkt(b *bytes.Buffer, s string) { fmt.Fprintf(b, "%04x%s", len(s)+4, s) }

// pkt-line 的特殊包。
const (
	pktData = iota
	pktFlush
	pktDelim
	pktEnd
)

// pktReader 按 pkt-line 读响应。
type pktReader struct{ b []byte }

// next 是下一个包。数据包去掉了长度头；ERR 包按服务端报错返回。
func (r *pktReader) next() ([]byte, int, error) {
	if len(r.b) < 4 {
		return nil, 0, errBadGitResponse
	}
	n, err := strconv.ParseUint(string(r.b[:4]), 16, 16)
	if err != nil {
		return nil, 0, errBadGitResponse
	}
	switch n {
	case 0, 1, 2:
		r.b = r.b[4:]
		return nil, []int{pktFlush, pktDelim, pktEnd}[n], nil
	case 3:
		return nil, 0, errBadGitResponse
	}
	if int(n) > len(r.b) {
		return nil, 0, errBadGitResponse
	}
	line := r.b[4:n]
	r.b = r.b[n:]
	if msg, ok := bytes.CutPrefix(line, []byte("ERR ")); ok {
		return nil, 0, gitServerError(string(bytes.TrimSpace(msg)))
	}
	return line, pktData, nil
}

var errBadGitResponse = &RemoteError{Kind: RemoteOther, Err: errors.New("malformed git protocol response")}

// gitServerError 归类服务端的报错：要的提交不存在算找不到。
func gitServerError(msg string) error {
	kind := RemoteOther
	if strings.Contains(msg, "not our ref") {
		kind = RemoteNotFound
	}
	return &RemoteError{Kind: kind, Err: errors.New(msg)}
}

// packFromResponse 从 fetch 响应里拼出包：跳过 shallow-info 等段，packfile 段
// 按 sideband 分流，1 是包数据，2 是进度，3 是错误。
func packFromResponse(body []byte) ([]byte, error) {
	r := pktReader{b: body}
	inPack := false
	var pack bytes.Buffer
	for {
		line, kind, err := r.next()
		if err != nil {
			return nil, err
		}
		switch {
		case kind == pktFlush || kind == pktEnd:
			if !inPack {
				return nil, errBadGitResponse
			}
			return pack.Bytes(), nil
		case kind == pktDelim:
			inPack = false
		case !inPack:
			inPack = string(line) == "packfile\n"
		case len(line) == 0:
			return nil, errBadGitResponse
		case line[0] == 1:
			pack.Write(line[1:])
		case line[0] == 3:
			return nil, gitServerError(string(bytes.TrimSpace(line[1:])))
		}
	}
}

// parsePack 解开包里的全部对象，按对象 ID 存放。包末尾的校验和由解析器核对。
func parsePack(pack []byte) (map[string]packObject, error) {
	if err := checkPackBudget(pack); err != nil {
		return nil, err
	}
	store := packStore{}
	p, err := packfile.NewParserWithStorage(packfile.NewScanner(bytes.NewReader(pack)), store)
	if err == nil {
		_, err = p.Parse()
	}
	if err != nil {
		return nil, &RemoteError{Kind: RemoteOther, Err: err}
	}
	objs := make(map[string]packObject, len(store))
	for h, o := range store {
		r, err := o.Reader()
		if err != nil {
			return nil, err
		}
		b, err := io.ReadAll(r)
		r.Close()
		if err != nil {
			return nil, err
		}
		objs[h.String()] = packObject{o.Type(), b}
		delete(store, h) // 边拷边放，内存里不同时留两份
	}
	return objs, nil
}

// checkPackBudget 在解开包之前只读对象头，估出解开后要占的内存：普通对象按
// 头里声明的大小（go-git 保证解出来不超过它），delta 再加上它声明的展开后
// 大小。对象数或总量超限时返回 errPackTooBig。
func checkPackBudget(pack []byte) error {
	s := packfile.NewScanner(bytes.NewReader(pack))
	_, count, err := s.Header()
	if err != nil {
		return &RemoteError{Kind: RemoteOther, Err: err}
	}
	if int64(count) > int64(maxTreeEntries)+1 {
		return errPackTooBig
	}
	var total int64
	for range count {
		h, err := s.NextObjectHeader()
		if err != nil {
			return &RemoteError{Kind: RemoteOther, Err: err}
		}
		total += h.Length
		if h.Type.IsDelta() {
			target, err := deltaTargetSize(s)
			if err != nil {
				return &RemoteError{Kind: RemoteOther, Err: err}
			}
			total += target
		}
		if total > int64(maxInflatedBytes) {
			return errPackTooBig
		}
	}
	return nil
}

// deltaTargetSize 读完当前的 delta（大小已按头里声明的计入预算），返回它
// 开头第二个变长整数：展开后的大小。
func deltaTargetSize(s *packfile.Scanner) (int64, error) {
	var head headWriter
	if _, _, err := s.NextObject(&head); err != nil {
		return 0, err
	}
	b := head.b
	var size uint64
	for range 2 { // 先是原对象大小，再是展开后大小
		size = 0
		for shift := 0; ; shift += 7 {
			if len(b) == 0 || shift > 56 {
				return 0, errors.New("malformed delta header")
			}
			c := b[0]
			b = b[1:]
			size |= uint64(c&0x7f) << shift
			if c&0x80 == 0 {
				break
			}
		}
	}
	if size > 1<<62 {
		return 0, errors.New("delta target too large")
	}
	return int64(size), nil
}

// headWriter 只留下写进来的前 20 个字节。
type headWriter struct{ b []byte }

func (w *headWriter) Write(p []byte) (int, error) {
	if n := 20 - len(w.b); n > 0 {
		w.b = append(w.b, p[:min(n, len(p))]...)
	}
	return len(p), nil
}

// commitFrom 是包里提交 sha 的目录树和提交时间。
func commitFrom(objs map[string]packObject, sha string) (Commit, error) {
	o, ok := objs[sha]
	if !ok || o.typ != plumbing.CommitObject {
		return Commit{}, &RemoteError{Kind: RemoteOther, Err: fmt.Errorf("commit %s missing from pack", sha)}
	}
	c := Commit{SHA: sha}
	var author time.Time
	for _, line := range strings.Split(string(o.data), "\n") {
		if line == "" {
			break // 头部结束，后面是提交说明
		}
		key, val, _ := strings.Cut(line, " ")
		switch key {
		case "tree":
			c.Tree = val
		case "committer":
			c.Date = signatureTime(val)
		case "author":
			author = signatureTime(val)
		}
	}
	if c.Date.IsZero() {
		c.Date = author
	}
	if !isSHA(c.Tree) {
		return Commit{}, &RemoteError{Kind: RemoteOther, Err: fmt.Errorf("commit %s has no tree", sha)}
	}
	return c, nil
}

// signatureTime 取「名字 <邮箱> 秒数 时区」里的时间。
func signatureTime(sig string) time.Time {
	f := strings.Fields(sig[strings.LastIndexByte(sig, '>')+1:])
	if len(f) < 1 {
		return time.Time{}
	}
	n, err := strconv.ParseInt(f[0], 10, 64)
	if err != nil {
		return time.Time{}
	}
	return time.Unix(n, 0)
}

type treeItem struct{ mode, name, sha string }

// parseTreeObject 解开目录树对象：每项是「模式 空格 名字 NUL 20 字节 ID」。
func parseTreeObject(b []byte) ([]treeItem, error) {
	var out []treeItem
	for len(b) > 0 {
		sp := bytes.IndexByte(b, ' ')
		nul := bytes.IndexByte(b, 0)
		if sp <= 0 || nul < sp || len(b) < nul+21 {
			return nil, errors.New("malformed tree object")
		}
		name := string(b[sp+1 : nul])
		if name == "" || name == "." || name == ".." || strings.Contains(name, "/") {
			return nil, errors.New("malformed tree entry name")
		}
		out = append(out, treeItem{string(b[:sp]), name, fmt.Sprintf("%x", b[nul+1:nul+21])})
		b = b[nul+21:]
	}
	return out, nil
}

// packStore 是解析包时用的内存对象库：go-git 的解析器只通过它交出对象内容。
type packStore map[plumbing.Hash]plumbing.EncodedObject

func (s packStore) NewEncodedObject() plumbing.EncodedObject { return &plumbing.MemoryObject{} }

func (s packStore) SetEncodedObject(o plumbing.EncodedObject) (plumbing.Hash, error) {
	s[o.Hash()] = o
	return o.Hash(), nil
}

func (s packStore) EncodedObject(t plumbing.ObjectType, h plumbing.Hash) (plumbing.EncodedObject, error) {
	if o, ok := s[h]; ok && (t == plumbing.AnyObject || o.Type() == t) {
		return o, nil
	}
	return nil, plumbing.ErrObjectNotFound
}

func (s packStore) IterEncodedObjects(t plumbing.ObjectType) (storer.EncodedObjectIter, error) {
	var out []plumbing.EncodedObject
	for _, o := range s {
		if t == plumbing.AnyObject || o.Type() == t {
			out = append(out, o)
		}
	}
	return storer.NewEncodedObjectSliceIter(out), nil
}

func (s packStore) HasEncodedObject(h plumbing.Hash) error {
	if _, ok := s[h]; ok {
		return nil
	}
	return plumbing.ErrObjectNotFound
}

func (s packStore) EncodedObjectSize(h plumbing.Hash) (int64, error) {
	if o, ok := s[h]; ok {
		return o.Size(), nil
	}
	return 0, plumbing.ErrObjectNotFound
}

func (s packStore) AddAlternate(string) error { return nil }
