package library

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeRemote 是内存里的上游：publish 把一组文件发成仓库的新提交。
type fakeRemote struct {
	mu          sync.Mutex
	repos       map[string]*fakeRepo
	seq         int
	calls       []string                           // "commit repo ref"、"tree repo sha"、"file repo sha path"、"pathcommit repo path"
	fail        map[string]error                   // 按仓库：所有请求都失败
	pathCommits map[string]Commit                  // "repo\x00path" → Commit
	pathFail    error                              // 按目录查提交一律失败（API 额度用完）
	unsized     bool                               // 目录树不带文件大小（git 协议）
	edit        func(path string, b []byte) []byte // 下载时改内容（篡改用例）
}

type fakeRepo struct {
	head    string
	commits map[string]*fakeCommit
}

type fakeCommit struct {
	c    Commit
	tree Tree
	dir  string
}

func newFakeRemote() *fakeRemote {
	return &fakeRemote{repos: map[string]*fakeRepo{}, fail: map[string]error{}, pathCommits: map[string]Commit{}}
}

// publish 发一个新提交：files 是仓库里的全部文件（路径 → 内容，路径以 * 结尾的是可执行文件）。
func (f *fakeRemote) publish(t *testing.T, repo string, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for p, body := range files {
		mode := os.FileMode(0o644)
		if strings.HasSuffix(p, "*") {
			p, mode = strings.TrimSuffix(p, "*"), 0o755
		}
		if strings.HasPrefix(body, "->") {
			must(t, os.MkdirAll(filepath.Dir(filepath.Join(dir, p)), 0o755))
			must(t, os.Symlink(strings.TrimPrefix(body, "->"), filepath.Join(dir, p)))
			continue
		}
		write(t, dir, p, body, mode)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.seq++
	sha := fmt.Sprintf("%040x", f.seq*7919)
	root, err := TreeHash(dir)
	must(t, err)
	var tr Tree
	must(t, filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || p == dir {
			return err
		}
		rel, _ := filepath.Rel(dir, p)
		rel = filepath.ToSlash(rel)
		fi, _ := os.Lstat(p)
		switch {
		case fi.Mode()&os.ModeSymlink != 0:
			target, _ := os.Readlink(p)
			tr.Entries = append(tr.Entries, TreeEntry{rel, "120000", "blob", blobID([]byte(target)), int64(len(target))})
		case d.IsDir():
			h, err := TreeHash(p)
			must(t, err)
			tr.Entries = append(tr.Entries, TreeEntry{rel, "040000", "tree", h, 0})
		default:
			b, _ := os.ReadFile(p)
			mode := "100644"
			if fi.Mode()&0o100 != 0 {
				mode = "100755"
			}
			tr.Entries = append(tr.Entries, TreeEntry{rel, mode, "blob", blobID(b), int64(len(b))})
		}
		return nil
	}))
	sort.Slice(tr.Entries, func(i, j int) bool { return tr.Entries[i].Path < tr.Entries[j].Path })
	r := f.repos[repo]
	if r == nil {
		r = &fakeRepo{commits: map[string]*fakeCommit{}}
		f.repos[repo] = r
	}
	r.head = sha
	r.commits[sha] = &fakeCommit{Commit{sha, root, time.Date(2026, 11, 10, 8, 0, 0, 0, time.UTC)}, tr, dir}
	return sha
}

// upstreamTree 是仓库当前提交里这个目录的 tree 哈希。
func (f *fakeRemote) upstreamTree(repo, dir string) string {
	r := f.repos[repo]
	c := r.commits[r.head]
	if dir == "" {
		return c.c.Tree
	}
	for _, e := range c.tree.Entries {
		if e.Path == dir {
			return e.SHA
		}
	}
	return ""
}

func (f *fakeRemote) log(s string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, s)
	repo := strings.Fields(s)[1]
	return f.fail[repo]
}

func (f *fakeRemote) commit(repo, ref string) (*fakeCommit, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	r := f.repos[repo]
	if r == nil {
		return nil, &RemoteError{Kind: RemoteNotFound, Status: 404}
	}
	if ref == "" {
		ref = r.head
	}
	c := r.commits[ref]
	if c == nil {
		return nil, &RemoteError{Kind: RemoteNotFound, Status: 404}
	}
	return c, nil
}

func (f *fakeRemote) Commit(ctx context.Context, repo, ref string) (Commit, error) {
	if err := f.log("commit " + repo + " " + ref); err != nil {
		return Commit{}, err
	}
	if err := ctx.Err(); err != nil {
		return Commit{}, err
	}
	c, err := f.commit(repo, ref)
	if err != nil {
		return Commit{}, err
	}
	return c.c, nil
}

func (f *fakeRemote) Tree(ctx context.Context, repo, sha string) (Tree, error) {
	if err := f.log("tree " + repo + " " + sha); err != nil {
		return Tree{}, err
	}
	c, err := f.commit(repo, sha)
	if err != nil {
		return Tree{}, err
	}
	if f.unsized {
		t := Tree{Truncated: c.tree.Truncated, Entries: slices.Clone(c.tree.Entries)}
		for i := range t.Entries {
			if t.Entries[i].Type == "blob" {
				t.Entries[i].Size = -1
			}
		}
		return t, nil
	}
	return c.tree, nil
}

func (f *fakeRemote) PathCommit(ctx context.Context, repo, ref, subpath string) (Commit, error) {
	if err := f.log("pathcommit " + repo + " " + subpath); err != nil {
		return Commit{}, err
	}
	if err := ctx.Err(); err != nil {
		return Commit{}, err
	}
	f.mu.Lock()
	pc, ok := f.pathCommits[repo+"\x00"+subpath]
	fail := f.pathFail
	f.mu.Unlock()
	if fail != nil {
		return Commit{}, fail
	}
	if ok {
		return pc, nil
	}
	return f.Commit(ctx, repo, ref)
}

func (f *fakeRemote) File(ctx context.Context, repo, sha, p string) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := f.log("file " + repo + " " + sha + " " + p); err != nil {
		return nil, err
	}
	c, err := f.commit(repo, sha)
	if err != nil {
		return nil, err
	}
	b, err := os.ReadFile(filepath.Join(c.dir, filepath.FromSlash(p)))
	if err != nil {
		return nil, &RemoteError{Kind: RemoteNotFound, Status: 404}
	}
	if f.edit != nil {
		b = f.edit(p, b)
	}
	return b, nil
}

func (f *fakeRemote) count(prefix string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, c := range f.calls {
		if strings.HasPrefix(c, prefix) {
			n++
		}
	}
	return n
}

// rec 是安装记录里的一条。
type rec struct {
	name, source, typ, path, hash string
	extra                         map[string]any
}

func gh(name, repo, path, hash string) rec { return rec{name, repo, "github", path, hash, nil} }

// writeRecords 写一份 version 3 的安装记录。
func writeRecords(t *testing.T, h func(string) string, recs ...rec) {
	t.Helper()
	writeRecordsVersion(t, h, 3, recs...)
}

func writeRecordsVersion(t *testing.T, h func(string) string, version int, recs ...rec) {
	t.Helper()
	var b strings.Builder
	fmt.Fprintf(&b, "{\n  \"version\": %d,\n  \"skills\": {", version)
	for i, r := range recs {
		e := map[string]any{"source": r.source, "sourceType": r.typ, "sourceUrl": "https://github.com/" + r.source + ".git",
			"installedAt": "2026-08-20T01:02:03.000Z", "updatedAt": "2026-08-20T01:02:03.000Z"}
		if r.path != "" {
			e["skillPath"] = r.path
		}
		if r.hash != "" || r.typ == "github" {
			e["skillFolderHash"] = r.hash
		}
		for k, v := range r.extra {
			e[k] = v
		}
		body, _ := json.MarshalIndent(e, "    ", "  ")
		if i > 0 {
			b.WriteString(",")
		}
		fmt.Fprintf(&b, "\n    %q: %s", r.name, body)
	}
	b.WriteString("\n  }\n}\n")
	must(t, os.MkdirAll(h(".agents"), 0o755))
	must(t, os.WriteFile(h(".agents/.skill-lock.json"), []byte(b.String()), 0o644))
}

// install 把 files 写成技能库里的 name（路径以 * 结尾的是可执行文件）。
func install(t *testing.T, h func(string) string, name string, files map[string]string) string {
	t.Helper()
	dir := h(".agents/skills/" + name)
	for p, body := range files {
		mode := os.FileMode(0o644)
		if strings.HasSuffix(p, "*") {
			p, mode = strings.TrimSuffix(p, "*"), 0o755
		}
		write(t, dir, p, body, mode)
	}
	return dir
}

func skillMD(name, body string) string {
	return "---\nname: " + name + "\ndescription: x\n---\n" + body
}

func treeOf(t *testing.T, dir string) string {
	t.Helper()
	h, err := TreeHash(dir)
	must(t, err)
	return h
}

// updatesHome 是更新用例的主目录（隔离 XDG_STATE_HOME 和应用目录）。
func updatesHome(t *testing.T, agents ...string) (string, func(string) string) {
	t.Helper()
	home, h := linkHome(t, agents...)
	t.Setenv("HOME", home)
	t.Setenv("XDG_STATE_HOME", "")
	return home, h
}

func checkNow(t *testing.T, home string, r Remote) (*Library, UpdateReport) {
	t.Helper()
	l := Scan(home)
	must(t, l.CheckUpdates(context.Background(), r, nil))
	return l, l.Updates()
}

func itemNamed(t *testing.T, rep UpdateReport, name string) UpdateItem {
	t.Helper()
	for _, it := range rep.Items {
		if it.Name == name || it.Source.Repo == name {
			return it
		}
	}
	t.Fatalf("没有 %s：%+v", name, rep.Items)
	return UpdateItem{}
}
