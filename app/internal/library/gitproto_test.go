package library

import (
	"bytes"
	"compress/zlib"
	"context"
	"crypto/sha1"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-git/go-git/v5/plumbing"
)

// fakeGit 是内存里的 git 端点：按协议 v2 回 ls-refs 和 fetch。
type fakeGit struct {
	mu         sync.Mutex
	refs       []gitRef
	objects    map[string]packObject
	requests   []string // "ls-refs <前缀…>"、"fetch <filter> <want>"
	deltaTrees bool     // 第二个起的目录树按 ofs-delta 编码
	reply      func(w http.ResponseWriter, r *http.Request) bool
}

func newFakeGit() *fakeGit { return &fakeGit{objects: map[string]packObject{}} }

// serve 起一个只有 git 端点的上游；API 和 raw 的请求都算测试失败。
func (f *fakeGit) serve(t *testing.T) *GitHub {
	t.Helper()
	g := testGitHub(t, func(w http.ResponseWriter, r *http.Request) {
		if f.reply != nil && f.reply(w, r) {
			return
		}
		if !strings.HasSuffix(r.URL.Path, ".git/git-upload-pack") || r.Method != http.MethodPost {
			t.Errorf("不该请求 %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
			return
		}
		if r.Header.Get("Git-Protocol") != "version=2" {
			t.Errorf("没带 Git-Protocol: version=2")
		}
		body, _ := io.ReadAll(r.Body)
		f.handle(t, w, body)
	})
	return g
}

func (f *fakeGit) handle(t *testing.T, w http.ResponseWriter, body []byte) {
	t.Helper()
	cmd, args := "", []string{}
	rd := pktReader{b: body}
	for {
		line, kind, err := rd.next()
		if err != nil || kind == pktFlush {
			break
		}
		s := strings.TrimSuffix(string(line), "\n")
		if c, ok := strings.CutPrefix(s, "command="); ok {
			cmd = c
		} else if kind == pktData && !strings.Contains(s, "=") {
			args = append(args, s)
		}
	}
	var out bytes.Buffer
	f.mu.Lock()
	defer f.mu.Unlock()
	switch cmd {
	case "ls-refs":
		var prefixes []string
		for _, a := range args {
			if p, ok := strings.CutPrefix(a, "ref-prefix "); ok {
				prefixes = append(prefixes, p)
			}
		}
		f.requests = append(f.requests, "ls-refs "+strings.Join(prefixes, " "))
		for _, r := range f.refs {
			if !slices.ContainsFunc(prefixes, func(p string) bool { return strings.HasPrefix(r.Name, p) }) {
				continue
			}
			line := r.SHA + " " + r.Name
			if r.Target != "" {
				line += " symref-target:" + r.Target
			}
			writePkt(&out, line+"\n")
		}
		out.WriteString("0000")
	case "fetch":
		var want, filter string
		for _, a := range args {
			if v, ok := strings.CutPrefix(a, "want "); ok {
				want = v
			} else if v, ok := strings.CutPrefix(a, "filter "); ok {
				filter = v
			}
		}
		f.requests = append(f.requests, "fetch "+filter+" "+want)
		c, ok := f.objects[want]
		if !ok || c.typ != plumbing.CommitObject {
			writePkt(&out, "ERR upload-pack: not our ref "+want)
			break
		}
		objs := []packObject{c}
		if filter == "blob:none" {
			tree, _ := commitFrom(f.objects, want)
			objs = append(objs, f.trees(tree.Tree)...)
		}
		writePkt(&out, "shallow-info\n")
		writePkt(&out, "shallow "+want+"\n")
		out.WriteString("0001")
		writePkt(&out, "packfile\n")
		pack := encodePack(objs, f.deltaTrees)
		for len(pack) > 0 {
			n := min(len(pack), 1000)
			writePkt(&out, "\x01"+string(pack[:n]))
			pack = pack[n:]
		}
		out.WriteString("0000")
	default:
		t.Errorf("未知命令 %q", cmd)
	}
	w.Write(out.Bytes())
}

// trees 是 sha 及其下所有目录树对象。
func (f *fakeGit) trees(sha string) []packObject {
	o := f.objects[sha]
	out := []packObject{o}
	items, _ := parseTreeObject(o.data)
	for _, it := range items {
		if it.mode == "40000" {
			out = append(out, f.trees(it.sha)...)
		}
	}
	return out
}

func (f *fakeGit) add(typ plumbing.ObjectType, data []byte) string {
	id := hex.EncodeToString(gitObject(typ.String(), data))
	f.objects[id] = packObject{typ, data}
	return id
}

// commit 发一个提交：files 是仓库里的全部文件（路径以 * 结尾的是可执行文件，
// 内容以 -> 开头的是链接），返回提交 ID。
func (f *fakeGit) commit(files map[string]string, date time.Time) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	type node struct {
		files map[string][2]string // 名字 → 模式、内容
		dirs  map[string]*node
	}
	newNode := func() *node { return &node{map[string][2]string{}, map[string]*node{}} }
	root := newNode()
	for p, body := range files {
		mode := "100644"
		if strings.HasSuffix(p, "*") {
			p, mode = strings.TrimSuffix(p, "*"), "100755"
		}
		if t, ok := strings.CutPrefix(body, "->"); ok {
			mode, body = "120000", t
		}
		n := root
		parts := strings.Split(p, "/")
		for _, d := range parts[:len(parts)-1] {
			if n.dirs[d] == nil {
				n.dirs[d] = newNode()
			}
			n = n.dirs[d]
		}
		n.files[parts[len(parts)-1]] = [2]string{mode, body}
	}
	var write func(n *node) string
	write = func(n *node) string {
		type item struct{ sort, line string }
		var items []item
		for name, fb := range n.files {
			id, _ := hex.DecodeString(f.add(plumbing.BlobObject, []byte(fb[1])))
			items = append(items, item{name, fb[0] + " " + name + "\x00" + string(id)})
		}
		for name, d := range n.dirs {
			id, _ := hex.DecodeString(write(d))
			items = append(items, item{name + "/", "40000 " + name + "\x00" + string(id)})
		}
		sort.Slice(items, func(i, j int) bool { return items[i].sort < items[j].sort })
		var b strings.Builder
		for _, it := range items {
			b.WriteString(it.line)
		}
		return f.add(plumbing.TreeObject, []byte(b.String()))
	}
	tree := write(root)
	sig := fmt.Sprintf("A U Thor <a@example.com> %d +0800", date.Unix())
	return f.add(plumbing.CommitObject, []byte("tree "+tree+"\nauthor "+sig+"\ncommitter "+sig+"\n\nmsg\n"))
}

func (f *fakeGit) setRef(name, sha string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.refs = slices.DeleteFunc(f.refs, func(r gitRef) bool { return r.Name == name })
	f.refs = append(f.refs, gitRef{Name: name, SHA: sha})
}

func (f *fakeGit) calls() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.requests)
}

// encodePack 按 git 的包格式编码对象；delta 为真时第二个起的目录树编码成
// 相对第一个目录树的 ofs-delta（全部是插入指令）。
func encodePack(objs []packObject, delta bool) []byte {
	var b bytes.Buffer
	b.WriteString("PACK")
	binary.Write(&b, binary.BigEndian, uint32(2))
	binary.Write(&b, binary.BigEndian, uint32(len(objs)))
	header := func(typ int, size int) {
		c := byte(typ<<4) | byte(size&15)
		size >>= 4
		for size > 0 {
			b.WriteByte(c | 0x80)
			c, size = byte(size&0x7f), size>>7
		}
		b.WriteByte(c)
	}
	deflate := func(data []byte) {
		zw := zlib.NewWriter(&b)
		zw.Write(data)
		zw.Close()
	}
	varint := func(dst *bytes.Buffer, n int) {
		for n >= 0x80 {
			dst.WriteByte(byte(n) | 0x80)
			n >>= 7
		}
		dst.WriteByte(byte(n))
	}
	base, baseLen := -1, 0
	for _, o := range objs {
		off := b.Len()
		if delta && o.typ == plumbing.TreeObject && base >= 0 {
			var d bytes.Buffer
			varint(&d, baseLen)
			varint(&d, len(o.data))
			for rest := o.data; len(rest) > 0; {
				n := min(len(rest), 127)
				d.WriteByte(byte(n))
				d.Write(rest[:n])
				rest = rest[n:]
			}
			header(int(plumbing.OFSDeltaObject), d.Len())
			// 相对偏移：大端 7 位一组，除最后一组外每组先减一
			rel := off - base
			enc := []byte{byte(rel & 0x7f)}
			for rel >>= 7; rel > 0; rel >>= 7 {
				rel--
				enc = append([]byte{byte(rel&0x7f) | 0x80}, enc...)
			}
			b.Write(enc)
			deflate(d.Bytes())
			continue
		}
		if o.typ == plumbing.TreeObject && base < 0 {
			base, baseLen = off, len(o.data)
		}
		header(int(o.typ), len(o.data))
		deflate(o.data)
	}
	sum := sha1.Sum(b.Bytes())
	b.Write(sum[:])
	return b.Bytes()
}

// localTree 把 files 写进临时目录，返回目录（TreeHash 按本地文件算）。
func localTree(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for p, body := range files {
		mode := os.FileMode(0o644)
		if strings.HasSuffix(p, "*") {
			p, mode = strings.TrimSuffix(p, "*"), 0o755
		}
		if target, ok := strings.CutPrefix(body, "->"); ok {
			must(t, os.MkdirAll(filepath.Dir(filepath.Join(dir, p)), 0o755))
			must(t, os.Symlink(target, filepath.Join(dir, p)))
			continue
		}
		write(t, dir, p, body, mode)
	}
	return dir
}

var sampleRepo = map[string]string{
	"README.md":                     "# skills\n",
	"skills/vite/SKILL.md":          "---\nname: vite\n---\n",
	"skills/vite/scripts/run.sh*":   "#!/bin/sh\necho hi\n",
	"skills/vite/refs/guide.md":     "guide\n",
	"skills/vite/latest":            "->refs/guide.md",
	"skills/vue/SKILL.md":           "---\nname: vue\n---\n",
	"skills/vue-router/SKILL.md":    "---\nname: vue-router\n---\n",
	"skills/vue-router/a.b/note.md": "排序：vue-router 在 vue/ 之前\n",
}

func TestGitProtocolReadsTheCommitAndTreeWithoutTheAPI(t *testing.T) {
	for _, delta := range []bool{false, true} {
		t.Run(fmt.Sprintf("delta=%v", delta), func(t *testing.T) {
			f := newFakeGit()
			f.deltaTrees = delta
			date := time.Date(2026, 11, 10, 8, 0, 0, 0, time.UTC)
			sha := f.commit(sampleRepo, date)
			f.refs = []gitRef{{Name: "HEAD", SHA: sha, Target: "refs/heads/main"}, {Name: "refs/heads/main", SHA: sha}}
			g := f.serve(t)
			ctx := context.Background()

			c, err := g.Commit(ctx, "antfu/skills", "")
			must(t, err)
			dir := localTree(t, sampleRepo)
			root, err := TreeHash(dir)
			must(t, err)
			if c.SHA != sha || c.Tree != root || !c.Date.Equal(date) {
				t.Fatalf("commit = %+v，want tree %s date %v", c, root, date)
			}
			tr, err := g.Tree(ctx, "antfu/skills", c.SHA)
			must(t, err)
			if tr.Truncated {
				t.Fatal("不该不完整")
			}
			byPath := map[string]TreeEntry{}
			for _, e := range tr.Entries {
				byPath[e.Path] = e
			}
			for _, d := range []string{"skills", "skills/vite", "skills/vite/scripts", "skills/vue-router/a.b"} {
				h, err := TreeHash(filepath.Join(dir, d))
				must(t, err)
				if e := byPath[d]; e.Type != "tree" || e.Mode != "040000" || e.SHA != h {
					t.Errorf("%s = %+v，want tree %s", d, e, h)
				}
			}
			for p, want := range map[string]TreeEntry{
				"skills/vite/SKILL.md":       {Mode: "100644", SHA: blobID([]byte(sampleRepo["skills/vite/SKILL.md"]))},
				"skills/vite/scripts/run.sh": {Mode: "100755", SHA: blobID([]byte(sampleRepo["skills/vite/scripts/run.sh*"]))},
				"skills/vite/latest":         {Mode: "120000", SHA: blobID([]byte("refs/guide.md"))},
			} {
				e := byPath[p]
				if e.Type != "blob" || e.Mode != want.Mode || e.SHA != want.SHA || e.Size != -1 {
					t.Errorf("%s = %+v，want %s %s，大小未知", p, e, want.Mode, want.SHA)
				}
			}
			if len(tr.Entries) != 15 {
				t.Errorf("条目 %d 个，want 15：%+v", len(tr.Entries), tr.Entries)
			}
			want := []string{"ls-refs HEAD", "fetch tree:0 " + sha, "fetch blob:none " + sha}
			if got := f.calls(); !slices.Equal(got, want) {
				t.Errorf("请求 %q，want %q", got, want)
			}
		})
	}
}

func TestGitProtocolTreeMatchesTheRecordedAPITree(t *testing.T) {
	const sha = "57217cf86ba38148a57af9e90d14a4c049558330"
	fetch, err := os.ReadFile("testdata/gitproto/vercel-labs-skills-57217cf.fetch")
	must(t, err)
	api, err := os.ReadFile("testdata/gitproto/vercel-labs-skills-57217cf.api-tree")
	must(t, err)
	g := testGitHub(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/vercel-labs/skills.git/git-upload-pack" {
			http.NotFound(w, r)
			return
		}
		w.Write(fetch)
	})
	tr, err := g.Tree(context.Background(), "vercel-labs/skills", sha)
	must(t, err)
	var got []string
	for _, e := range tr.Entries {
		got = append(got, e.Mode+" "+e.Type+" "+e.SHA+" "+e.Path)
	}
	want := strings.Split(strings.TrimSpace(string(api)), "\n")
	if !slices.Equal(got, want) {
		t.Errorf("和 API 的目录树不一致：%d 条 vs %d 条", len(got), len(want))
		for i := range min(len(got), len(want)) {
			if got[i] != want[i] {
				t.Fatalf("第 %d 条：%q，API %q", i, got[i], want[i])
			}
		}
	}
}

func TestGitProtocolResolvesBranchesTagsAndCommitIDs(t *testing.T) {
	f := newFakeGit()
	main := f.commit(map[string]string{"SKILL.md": "main"}, time.Unix(1e9, 0))
	old := f.commit(map[string]string{"SKILL.md": "old"}, time.Unix(1e9, 0))
	tag := f.commit(map[string]string{"SKILL.md": "tag"}, time.Unix(1e9, 0))
	slash := f.commit(map[string]string{"SKILL.md": "slash"}, time.Unix(1e9, 0))
	f.refs = []gitRef{
		{Name: "HEAD", SHA: main, Target: "refs/heads/main"},
		{Name: "refs/heads/main-old", SHA: old},
		{Name: "refs/heads/main", SHA: main},
		{Name: "refs/heads/feature/x", SHA: slash},
		{Name: "refs/tags/v1", SHA: tag}, // 附注标签：ls-refs 的 peeled 已在 SHA 里
	}
	g := f.serve(t)
	ctx := context.Background()
	for ref, want := range map[string]string{
		"":                    main,
		"main":                main,
		"main-old":            old,
		"v1":                  tag,
		"feature/x":           slash,
		"refs/heads/main-old": old,
		strings.ToUpper(old):  old,
	} {
		c, err := g.Commit(ctx, "a/b", ref)
		if err != nil || c.SHA != want {
			t.Errorf("Commit(%q) = %s %v，want %s", ref, c.SHA, err, want)
		}
	}
	_, err := g.Commit(ctx, "a/b", "nope")
	var re *RemoteError
	if !errors.As(err, &re) || re.Kind != RemoteNotFound {
		t.Errorf("没有的分支：%v", err)
	}
	_, err = g.Commit(ctx, "a/b", strings.Repeat("1", 40))
	if !errors.As(err, &re) || re.Kind != RemoteNotFound {
		t.Errorf("没有的提交（服务端回 ERR not our ref）：%v", err)
	}
	names, err := g.RefNames(ctx, "a/b", "feature/")
	if err != nil || !slices.Equal(names, []string{"feature/x"}) {
		t.Errorf("RefNames = %q %v", names, err)
	}
}

func TestGitProtocolAsksTheAPIOnlyForAbbreviatedCommitIDs(t *testing.T) {
	f := newFakeGit()
	f.refs = []gitRef{{Name: "refs/heads/main", SHA: strings.Repeat("a", 40)}}
	apiCalls := 0
	f.reply = func(w http.ResponseWriter, r *http.Request) bool {
		if r.URL.Path != "/repos/a/b/commits/abc1234" {
			return false
		}
		apiCalls++
		fmt.Fprint(w, `{"sha":"abc1234000000000000000000000000000000000","commit":{"tree":{"sha":"t1"},"committer":{"date":"2026-11-10T08:00:00Z"}}}`)
		return true
	}
	g := f.serve(t)
	c, err := g.Commit(context.Background(), "a/b", "abc1234")
	if err != nil || c.SHA != "abc1234000000000000000000000000000000000" || apiCalls != 1 {
		t.Errorf("简写提交 ID：%+v %v，API 调了 %d 次", c, err, apiCalls)
	}
}

func TestGitProtocolFailuresAreClassified(t *testing.T) {
	kind := func(err error) RemoteKind {
		var re *RemoteError
		if !errors.As(err, &re) {
			t.Fatalf("不是 RemoteError：%v", err)
		}
		return re.Kind
	}
	status := func(code int) *GitHub {
		return testGitHub(t, func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(code) })
	}

	if k := kind(status(http.StatusUnauthorized).lsRefsErr()); k != RemoteNotFound {
		t.Errorf("匿名 401 是仓库不存在或私有：%v", k)
	}
	g := status(http.StatusUnauthorized)
	g.Token = "expired"
	if k := kind(g.lsRefsErr()); k != RemoteAuth {
		t.Errorf("带凭证的 401 是凭证失效：%v", k)
	}
	if k := kind(status(http.StatusBadGateway).lsRefsErr()); k != RemoteNetwork {
		t.Errorf("502 是网络：%v", k)
	}
	for name, body := range map[string]string{
		"截断的长度":       "00",
		"长度越界":        "00ffabc",
		"sideband 报错": "0011shallow-info\n" + "0001" + "000dpackfile\n" + "000e\x03disk full" + "0000",
		"没有包":         "0000",
		"坏包":          "000dpackfile\n" + "0009\x01PACK" + "0000",
	} {
		g := testGitHub(t, func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, body) })
		if _, err := g.Tree(context.Background(), "a/b", strings.Repeat("1", 40)); kind(err) != RemoteOther {
			t.Errorf("%s：%v", name, err)
		}
	}
	srv := httptest.NewServer(http.NotFoundHandler())
	srv.Close()
	g = NewGitHub("")
	g.Git = srv.URL
	if _, err := g.Commit(context.Background(), "a/b", ""); kind(err) != RemoteNetwork {
		t.Errorf("连不上：%v", err)
	}
}

// lsRefsErr 发一次 ls-refs，只要错误。
func (g *GitHub) lsRefsErr() error {
	_, err := g.lsRefs(context.Background(), "a/b", "HEAD")
	return err
}

func TestGitProtocolSendsTheTokenAsBasicAuth(t *testing.T) {
	f := newFakeGit()
	sha := f.commit(map[string]string{"SKILL.md": "x"}, time.Unix(1e9, 0))
	f.refs = []gitRef{{Name: "HEAD", SHA: sha}}
	var user, pass string
	f.reply = func(w http.ResponseWriter, r *http.Request) bool {
		user, pass, _ = r.BasicAuth()
		return false
	}
	g := f.serve(t)
	g.Token = "gho_secret"
	if _, err := g.Commit(context.Background(), "a/b", ""); err != nil {
		t.Fatal(err)
	}
	if user != "x-access-token" || pass != "gho_secret" {
		t.Errorf("Basic 认证 = %q:%q", user, pass)
	}
}

// withLimit 把上限临时调成 v。
func withLimit(t *testing.T, p *int, v int) {
	orig := *p
	*p = v
	t.Cleanup(func() { *p = orig })
}

func TestGitProtocolMarksATreeWithTooManyEntriesIncomplete(t *testing.T) {
	// Given 一个目录树对象里有 5 项，条目上限是 3
	f := newFakeGit()
	sha := f.commit(map[string]string{"a": "1", "b": "2", "c": "3", "d": "4", "e": "5"}, time.Unix(1e9, 0))
	g := f.serve(t)
	withLimit(t, &maxTreeEntries, 3)

	tr, err := g.Tree(context.Background(), "a/b", sha)

	// Then 展开到上限为止，标成不完整
	if err != nil || !tr.Truncated || len(tr.Entries) != 3 {
		t.Errorf("条目超限：%d 条 truncated=%v %v", len(tr.Entries), tr.Truncated, err)
	}
}

func TestGitProtocolMarksOversizedPacksIncompleteBeforeUnpacking(t *testing.T) {
	f := newFakeGit()
	sha := f.commit(sampleRepo, time.Unix(1e9, 0)) // 1 个提交 + 7 个目录树
	g := f.serve(t)
	ctx := context.Background()

	t.Run("对象太多", func(t *testing.T) {
		withLimit(t, &maxTreeEntries, 3)
		if tr, err := g.Tree(ctx, "a/b", sha); err != nil || !tr.Truncated || len(tr.Entries) != 0 {
			t.Errorf("%+v %v", tr, err)
		}
	})
	t.Run("响应太大", func(t *testing.T) {
		withLimit(t, &maxPackBytes, 100)
		if tr, err := g.Tree(ctx, "a/b", sha); err != nil || !tr.Truncated || len(tr.Entries) != 0 {
			t.Errorf("%+v %v", tr, err)
		}
	})
	t.Run("压缩率高、解开后太大", func(t *testing.T) {
		// 提交说明里塞 16 MiB 同一个字节：压缩后只有几十 KB
		c := f.objects[sha]
		big := f.add(c.typ, append(slices.Clone(c.data), bytes.Repeat([]byte("x"), 16<<20)...))
		withLimit(t, &maxInflatedBytes, 1<<20)
		if tr, err := g.Tree(ctx, "a/b", big); err != nil || !tr.Truncated {
			t.Errorf("目录树：%+v %v", tr, err)
		}
		var re *RemoteError
		if _, err := g.Commit(ctx, "a/b", big); !errors.As(err, &re) || !errors.Is(err, errPackTooBig) {
			t.Errorf("提交：%v", err)
		}
	})
}

func TestGitProtocolBudgetsDeltasByTheirDeclaredTargetSize(t *testing.T) {
	// Given 一个只有几字节的 delta，声明展开后有 1 TiB
	base := []byte("100644 a\x00" + strings.Repeat("\x01", 20))
	var delta bytes.Buffer
	for _, n := range []uint64{uint64(len(base)), 1 << 40} {
		for n >= 0x80 {
			delta.WriteByte(byte(n) | 0x80)
			n >>= 7
		}
		delta.WriteByte(byte(n))
	}
	var b bytes.Buffer
	b.WriteString("PACK")
	binary.Write(&b, binary.BigEndian, uint32(2))
	binary.Write(&b, binary.BigEndian, uint32(2))
	b.WriteByte(0x80 | byte(plumbing.TreeObject)<<4 | byte(len(base)&15))
	b.WriteByte(byte(len(base) >> 4))
	zw := zlib.NewWriter(&b)
	zw.Write(base)
	zw.Close()
	b.WriteByte(byte(plumbing.REFDeltaObject)<<4 | byte(delta.Len()))
	b.Write(gitObject("tree", base))
	zw = zlib.NewWriter(&b)
	zw.Write(delta.Bytes())
	zw.Close()
	sum := sha1.Sum(b.Bytes())
	b.Write(sum[:])

	// Then 解开前就按超限拒绝，不去展开
	if _, err := parsePack(b.Bytes()); !errors.Is(err, errPackTooBig) {
		t.Errorf("err = %v", err)
	}
}

func TestGitProtocolRejectsUnsafeTreeNames(t *testing.T) {
	id := strings.Repeat("\x01", 20)
	for _, name := range []string{"..", ".", "a/b", ""} {
		if _, err := parseTreeObject([]byte("100644 " + name + "\x00" + id)); err == nil {
			t.Errorf("名字 %q 应报错", name)
		}
	}
	if _, err := parseTreeObject([]byte("100644 ok\x00short")); err == nil {
		t.Error("截断的条目应报错")
	}
}
