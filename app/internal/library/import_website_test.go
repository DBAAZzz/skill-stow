package library

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"maps"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
)

// fakeSite 是发布 well-known 索引的网站：files 是网址路径 → 内容。
type fakeSite struct {
	mu     sync.Mutex
	files  map[string]string
	hits   []string
	onFile func(path string) // 请求某个文件时（模拟下载期间网站改版）
	srv    *httptest.Server
}

func newFakeSite(t *testing.T, files map[string]string) *fakeSite {
	s := &fakeSite{files: files}
	s.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		s.hits = append(s.hits, r.URL.Path)
		body, ok := s.files[r.URL.Path]
		hook := s.onFile
		s.mu.Unlock()
		if hook != nil {
			hook(r.URL.Path)
		}
		if !ok {
			http.NotFound(w, r)
			return
		}
		io.WriteString(w, body)
	}))
	t.Cleanup(s.srv.Close)
	return s
}

func (s *fakeSite) set(path, body string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.files[path] = body
}

func indexJSON(t *testing.T, v any) string {
	b, err := json.Marshal(v)
	must(t, err)
	return string(b)
}

func digestOf(b string) string {
	sum := sha256.Sum256([]byte(b))
	return "sha256:" + hex.EncodeToString(sum[:])
}

func readSite(t *testing.T, home string, src ImportSource) (*ImportBatch, error) {
	t.Helper()
	b, err := ReadWebsiteImport(context.Background(), home, http.DefaultClient, src, nil)
	if b != nil {
		t.Cleanup(b.Discard)
	}
	return b, err
}

const lessonMD = "---\nname: lesson\ndescription: 写课件\n---\n正文\n"

// docsV1 是放在 /docs 下的 0.1.0 索引：两个技能。
func docsV1(t *testing.T) map[string]string {
	return map[string]string{
		"/docs/.well-known/skills/index.json": indexJSON(t, map[string]any{"skills": []any{
			map[string]any{"name": "lesson", "description": "写课件", "files": []string{"SKILL.md", "references/style.md"}},
			map[string]any{"name": "quiz", "description": "出题", "files": []string{"SKILL.md"}},
		}}),
		"/docs/.well-known/skills/lesson/SKILL.md":            lessonMD,
		"/docs/.well-known/skills/lesson/references/style.md": "风格",
		"/docs/.well-known/skills/quiz/SKILL.md":              "---\nname: quiz\ndescription: 出题\n---\n",
	}
}

func TestAWebsiteIndexIsReadFileByFile(t *testing.T) {
	home, _ := importHome(t)
	site := newFakeSite(t, docsV1(t))
	src, err := ParseImportInput(site.srv.URL + "/docs")
	must(t, err)
	if src.Kind != ImportWebsite {
		t.Fatalf("应识别为网站来源：%+v", src)
	}
	b, err := readSite(t, home, src)
	must(t, err)

	if got := batchNames(b); !slices.Equal(got, []string{"lesson", "quiz"}) {
		t.Fatalf("技能 = %v", got)
	}
	if b.Protocol != "0.1.0" || b.Site != "127.0.0.1" || b.BaseURL != site.srv.URL+"/docs" {
		t.Errorf("来源 = %s %s %s", b.Protocol, b.Site, b.BaseURL)
	}
	lesson := b.Skill("lesson")
	if body, _ := os.ReadFile(filepath.Join(lesson.Dir, "references/style.md")); string(body) != "风格" || lesson.Problem != "" || lesson.Count != 2 {
		t.Errorf("lesson 应整份下载：%q %+v", body, lesson)
	}
	// 摘要和 npx 的 computeWellKnownSkillDigest 一样：按路径排序，路径\0内容\0
	h := sha256.New()
	for _, kv := range [][2]string{{"SKILL.md", lessonMD}, {"references/style.md", "风格"}} {
		h.Write([]byte(kv[0] + "\x00" + kv[1] + "\x00"))
	}
	if want := "sha256:" + hex.EncodeToString(h.Sum(nil)); lesson.Digest != want {
		t.Errorf("摘要 = %s, want %s", lesson.Digest, want)
	}
	if lesson.SourceURL != site.srv.URL+"/docs/.well-known/skills/lesson/SKILL.md" {
		t.Errorf("SourceURL = %s", lesson.SourceURL)
	}
	if len(lesson.Audit) != 0 {
		t.Error("网站来源没有审计")
	}
}

func TestAddingAWebsiteSkillToTheLibraryWritesTheSameRecordAsNpx(t *testing.T) {
	home, h := importHome(t)
	site := newFakeSite(t, docsV1(t))
	src, _ := ParseImportInput(site.srv.URL + "/docs")
	b, err := readSite(t, home, src)
	must(t, err)
	out := Scan(home).AddImport(b, []string{"lesson"}, ImportPlacement{Library: true}, nil)
	if !out[0].Added || out[0].Place != h(".agents/skills/lesson") {
		t.Fatalf("结果 = %+v", out)
	}
	raw := lockSkills(t, h)["lesson"]
	var rec map[string]string
	must(t, json.Unmarshal(raw, &rec))
	want := map[string]string{"source": "127.0.0.1", "sourceType": "well-known",
		"sourceUrl": site.srv.URL + "/docs/.well-known/skills/lesson/SKILL.md", "skillFolderHash": "",
		"sourceBaseUrl": site.srv.URL + "/docs", "wellKnownDigest": b.Skill("lesson").Digest}
	for k, v := range want {
		if got, ok := rec[k]; !ok || got != v {
			t.Errorf("%s = %q (有=%v), want %q", k, got, ok, v)
		}
	}
	// 字段顺序和 npx 的 globalWellKnownLockEntry 一致
	keys := []string{`"source"`, `"sourceType"`, `"sourceUrl"`, `"skillFolderHash"`, `"sourceBaseUrl"`, `"wellKnownDigest"`, `"installedAt"`, `"updatedAt"`}
	last := -1
	for _, k := range keys {
		i := strings.Index(string(raw), k)
		if i < last {
			t.Errorf("字段顺序不对：\n%s", raw)
			break
		}
		last = i
	}
	if _, ok := rec["skillPath"]; ok {
		t.Error("网站来源不写 skillPath")
	}
	// 「更新」页认得，但不自动更新
	if it := itemNamed(t, Scan(home).Updates(), "lesson"); it.State != UpdWebsite {
		t.Errorf("更新页 = %+v", it)
	}
}

func TestAddingAWebsiteSkillForOneAgentLeavesTheRecordAlone(t *testing.T) {
	home, h := importHome(t)
	site := newFakeSite(t, docsV1(t))
	src, _ := ParseImportInput(site.srv.URL + "/docs")
	b, err := readSite(t, home, src)
	must(t, err)
	before, _ := os.ReadFile(h(".agents/.skill-lock.json"))
	l := Scan(home)
	out := l.AddImport(b, []string{"quiz"}, ImportPlacement{Agents: []string{"claude-code"}}, nil)
	if !out[0].Added || out[0].Place != h(".claude/skills/quiz") {
		t.Fatalf("结果 = %+v", out)
	}
	if after, _ := os.ReadFile(h(".agents/.skill-lock.json")); string(after) != string(before) {
		t.Error("指定 Agent 模式不写安装记录")
	}
	if s, ok := Scan(home).ImportedFrom(h(".claude/skills/quiz")); !ok || s.Type != "well-known" || s.Repo != "127.0.0.1" {
		t.Errorf("应用记录的来源 = %+v %v", s, ok)
	}
}

func TestANamedWebsiteSkillIsTheOnlyOneRead(t *testing.T) {
	home, _ := importHome(t)
	site := newFakeSite(t, docsV1(t))
	src, _ := ParseImportInput("npx skills add " + site.srv.URL + "/docs --skill quiz -g")
	b, err := readSite(t, home, src)
	must(t, err)
	if got := batchNames(b); !slices.Equal(got, []string{"quiz"}) || b.Hidden != 1 {
		t.Fatalf("技能 = %v，隐藏 %d", got, b.Hidden)
	}
	if slices.ContainsFunc(site.hits, func(p string) bool { return strings.Contains(p, "/lesson/") }) {
		t.Errorf("没指名的技能不应下载：%v", site.hits)
	}
	src.All = true
	all, err := readSite(t, home, src)
	must(t, err)
	if !slices.Equal(batchNames(all), []string{"lesson", "quiz"}) || !slices.Equal(all.Preselected(), []string{"quiz"}) {
		t.Errorf("显示全部 = %v，预选 %v", batchNames(all), all.Preselected())
	}
}

func TestAPathWithoutAnIndexFallsBackToTheSiteRoot(t *testing.T) {
	home, _ := importHome(t)
	files := map[string]string{}
	for k, v := range docsV1(t) {
		files[strings.TrimPrefix(k, "/docs")] = v
	}
	site := newFakeSite(t, files)
	src, _ := ParseImportInput(site.srv.URL + "/guide/intro")
	b, err := readSite(t, home, src)
	must(t, err)
	if len(b.Skills) != 2 || b.BaseURL != site.srv.URL {
		t.Errorf("应在域名根目录找到索引：%v %s", batchNames(b), b.BaseURL)
	}
}

func TestTheNewIndexPathIsTriedFirst(t *testing.T) {
	home, _ := importHome(t)
	files := docsV1(t)
	files["/docs/.well-known/agent-skills/index.json"] = indexJSON(t, map[string]any{"skills": []any{
		map[string]any{"name": "quiz", "description": "出题", "files": []string{"SKILL.md"}},
	}})
	files["/docs/.well-known/agent-skills/quiz/SKILL.md"] = "---\nname: quiz\ndescription: 新路径\n---\n"
	site := newFakeSite(t, files)
	src, _ := ParseImportInput(site.srv.URL + "/docs")
	b, err := readSite(t, home, src)
	must(t, err)
	if len(b.Skills) != 1 || b.Skill("quiz").Description != "新路径" {
		t.Errorf("应先用 agent-skills：%+v", b.Skills)
	}
}

func TestAPageThatIsNotAnIndexIsNotASkillSource(t *testing.T) {
	home, _ := importHome(t)
	// 200 的网页、JSON 但不合 0.1.0 结构，都不算
	site := newFakeSite(t, map[string]string{
		"/.well-known/agent-skills/index.json": "<html>hello</html>",
		"/.well-known/skills/index.json":       `{"skills":[{"name":"Bad Name","description":"x","files":["SKILL.md"]}]}`,
	})
	src, _ := ParseImportInput(site.srv.URL)
	_, err := readSite(t, home, src)
	var ie *ImportError
	if !errors.As(err, &ie) || ie.Msg != "这个地址不是技能源。" || ie.Hint != "粘贴文档里给的完整安装命令" {
		t.Fatalf("err = %v", err)
	}

	src.Guessed = true
	_, err = readSite(t, home, src)
	if !errors.As(err, &ie) || !strings.HasSuffix(ie.Msg, " 的技能不在网站根目录。") || ie.Hint != "粘贴它文档里给的完整安装命令" {
		t.Fatalf("短名字 err = %v", err)
	}
}

func TestAShortNameAlsoTriesThePathAfterTheDomain(t *testing.T) {
	home, _ := importHome(t)
	site := newFakeSite(t, docsV1(t))
	src, err := ParseImportInput("mintlify.com/docs")
	must(t, err)
	if !src.Guessed || src.URL != "https://mintlify.com" || src.Subpath != "docs" {
		t.Fatalf("src = %+v", src)
	}
	src.URL = site.srv.URL // 测试服务器只有 http
	b, err := readSite(t, home, src)
	must(t, err)
	if len(b.Skills) != 2 || b.BaseURL != site.srv.URL+"/docs" {
		t.Errorf("根目录没有时应试 /docs：%v %s", batchNames(b), b.BaseURL)
	}
}

func TestAnUnreachableWebsiteSaysSo(t *testing.T) {
	home, _ := importHome(t)
	site := newFakeSite(t, nil)
	u := site.srv.URL
	site.srv.Close()
	src, _ := ParseImportInput(u)
	_, err := readSite(t, home, src)
	var ie *ImportError
	if !errors.As(err, &ie) || !ie.Network || !strings.HasPrefix(ie.Msg, "连不上 127.0.0.1") {
		t.Fatalf("err = %v", err)
	}
}

func TestAMissingFileMeansTheSkillCannotBeAdded(t *testing.T) {
	home, _ := importHome(t)
	files := docsV1(t)
	delete(files, "/docs/.well-known/skills/lesson/references/style.md")
	site := newFakeSite(t, files)
	src, _ := ParseImportInput(site.srv.URL + "/docs")
	b, err := readSite(t, home, src)
	must(t, err)
	if p := b.Skill("lesson").Problem; !strings.Contains(p, "references/style.md") || !strings.Contains(p, "404") {
		t.Errorf("缺文件 = %q", p)
	}
	if b.Skill("quiz").Problem != "" {
		t.Error("其他技能不受影响")
	}
}

func TestAWebsiteThatPublishesDuringTheReadAsksToRetry(t *testing.T) {
	home, _ := importHome(t)
	site := newFakeSite(t, docsV1(t))
	site.onFile = func(p string) {
		if strings.HasSuffix(p, "/style.md") {
			site.set("/docs/.well-known/skills/index.json", indexJSON(t, map[string]any{"skills": []any{
				map[string]any{"name": "lesson", "description": "写课件", "files": []string{"SKILL.md", "references/style.md", "references/new.md"}},
				map[string]any{"name": "quiz", "description": "出题", "files": []string{"SKILL.md"}},
			}}))
		}
	}
	src, _ := ParseImportInput(site.srv.URL + "/docs")
	_, err := readSite(t, home, src)
	var ie *ImportError
	if !errors.As(err, &ie) || ie.Msg != "网站刚更新过，这次读到的内容可能不完整。" || ie.Hint != "重新读取" {
		t.Fatalf("err = %v", err)
	}
}

func TestInternalWebsiteSkillsAreListedOnlyWhenNamed(t *testing.T) {
	home, _ := importHome(t)
	files := docsV1(t)
	files["/docs/.well-known/skills/quiz/SKILL.md"] = "---\nname: quiz\ndescription: 出题\nmetadata:\n  internal: true\n---\n"
	site := newFakeSite(t, files)
	src, _ := ParseImportInput(site.srv.URL + "/docs")
	b, err := readSite(t, home, src)
	must(t, err)
	if got := batchNames(b); !slices.Equal(got, []string{"lesson"}) || b.Hidden != 1 {
		t.Errorf("internal 不指名不列：%v，隐藏 %d", got, b.Hidden)
	}
	src.Skills = []string{"quiz"}
	b, err = readSite(t, home, src)
	must(t, err)
	if got := batchNames(b); !slices.Equal(got, []string{"quiz"}) {
		t.Errorf("指名了应列出：%v", got)
	}
}

// v2Site 是 0.2.0 索引：artifacts 是网址路径 → 内容，按 types 标类型。
func v2Site(t *testing.T, entries []map[string]any, artifacts map[string]string) *fakeSite {
	files := maps.Clone(artifacts)
	files["/.well-known/agent-skills/index.json"] = indexJSON(t, map[string]any{"$schema": discoverySchemaV2, "skills": entries})
	return newFakeSite(t, files)
}

func TestAV2SkillIsCheckedAgainstItsDigest(t *testing.T) {
	home, _ := importHome(t)
	md := "---\nname: mintlify\ndescription: 文档\n---\n"
	site := v2Site(t, []map[string]any{
		{"name": "mintlify", "type": "skill-md", "description": "文档", "url": "mintlify/skill.md", "digest": digestOf(md)},
		{"name": "broken", "type": "skill-md", "description": "被改过", "url": "/.well-known/agent-skills/broken/SKILL.md", "digest": digestOf("原来的")},
	}, map[string]string{
		"/.well-known/agent-skills/mintlify/skill.md": md,
		"/.well-known/agent-skills/broken/SKILL.md":   "---\nname: broken\ndescription: x\n---\n",
	})
	src, _ := ParseImportInput(site.srv.URL)
	b, err := readSite(t, home, src)
	must(t, err)
	m := b.Skill("mintlify")
	if b.Protocol != "0.2.0" || m.Problem != "" || m.Digest != digestOf(md) || m.SourceURL != site.srv.URL+"/.well-known/agent-skills/mintlify/skill.md" {
		t.Errorf("mintlify = %+v（%s）", m, b.Protocol)
	}
	if body, _ := os.ReadFile(filepath.Join(m.Dir, "SKILL.md")); string(body) != md {
		t.Errorf("SKILL.md = %q", body)
	}
	if p := b.Skill("broken").Problem; p != "下载的内容和网站声明的不一致，稍后重试" {
		t.Errorf("摘要不符应不能添加：%q", p)
	}
	if out := Scan(home).AddImport(b, []string{"broken"}, ImportPlacement{Library: true}, nil); out[0].Added {
		t.Error("摘要不符的不能添加")
	}
}

func tarGz(t *testing.T, entries map[string]string, link string) []byte {
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(zw)
	for name, body := range entries {
		must(t, tw.WriteHeader(&tar.Header{Name: name, Mode: 0o644, Size: int64(len(body)), Typeflag: tar.TypeReg}))
		tw.Write([]byte(body))
	}
	if link != "" {
		must(t, tw.WriteHeader(&tar.Header{Name: link, Linkname: "/etc/passwd", Typeflag: tar.TypeSymlink}))
	}
	must(t, tw.Close())
	must(t, zw.Close())
	return buf.Bytes()
}

func zipBytes(t *testing.T, entries map[string]string) []byte {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, body := range entries {
		w, err := zw.Create(name)
		must(t, err)
		w.Write([]byte(body))
	}
	must(t, zw.Close())
	return buf.Bytes()
}

func TestV2ArchivesAreUnpackedAndUnsafeOnesRejected(t *testing.T) {
	home, _ := importHome(t)
	skill := map[string]string{"SKILL.md": "---\nname: deck\ndescription: 幻灯片\n---\n", "scripts/run.sh": "echo"}
	tgz := string(tarGz(t, skill, ""))
	zp := string(zipBytes(t, skill))
	evil := string(tarGz(t, skill, "evil"))
	site := v2Site(t, []map[string]any{
		{"name": "deck", "type": "archive", "description": "幻灯片", "url": "deck.tar.gz", "digest": digestOf(tgz)},
		{"name": "deck-zip", "type": "archive", "description": "幻灯片", "url": "deck.zip", "digest": digestOf(zp)},
		{"name": "evil", "type": "archive", "description": "带链接", "url": "evil.tgz", "digest": digestOf(evil)},
	}, map[string]string{
		"/.well-known/agent-skills/deck.tar.gz": tgz,
		"/.well-known/agent-skills/deck.zip":    zp,
		"/.well-known/agent-skills/evil.tgz":    evil,
	})
	src, _ := ParseImportInput(site.srv.URL)
	b, err := readSite(t, home, src)
	must(t, err)
	for _, n := range []string{"deck", "deck-zip"} {
		sk := b.Skill(n)
		if sk.Problem != "" || sk.Count != 2 || !slices.Equal(sk.Exec, []string{"scripts/run.sh"}) {
			t.Errorf("%s = %+v", n, sk)
		}
	}
	if p := b.Skill("evil").Problem; !strings.Contains(p, "链接") {
		t.Errorf("带链接的压缩包应拒绝：%q", p)
	}
}

func TestWellKnownSkillLinksAreParsed(t *testing.T) {
	for in, want := range map[string][2]string{
		"https://mintlify.com/docs/.well-known/agent-skills/mintlify/SKILL.md": {"https://mintlify.com/docs", "mintlify"},
		"https://open.feishu.cn/.well-known/skills/index.json":                 {"https://open.feishu.cn", ""},
		"https://open.feishu.cn/":                                              {"https://open.feishu.cn", ""},
	} {
		src, err := ParseImportInput(in)
		must(t, err)
		skill := ""
		if len(src.Skills) > 0 {
			skill = src.Skills[0]
		}
		if src.Kind != ImportWebsite || src.URL != want[0] || skill != want[1] {
			t.Errorf("%s → %+v", in, src)
		}
	}
}

// ——— GitHub 压缩包退路 ———

func (f *fakeImport) Archive(ctx context.Context, repo, commit string) (io.ReadCloser, error) {
	if err := f.log("archive " + repo + " " + commit); err != nil {
		return nil, err
	}
	if f.noArchive {
		return nil, &RemoteError{Kind: RemoteNetwork}
	}
	c, err := f.commit(repo, commit)
	if err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(zw)
	top := strings.ReplaceAll(repo, "/", "-") + "-" + commit[:7] + "/"
	tw.WriteHeader(&tar.Header{Name: "pax_global_header", Typeflag: tar.TypeXGlobalHeader, PAXRecords: map[string]string{"comment": commit}})
	tw.WriteHeader(&tar.Header{Name: top, Typeflag: tar.TypeDir, Mode: 0o755})
	for _, e := range c.tree.Entries {
		if e.Type != "blob" || e.Mode == "120000" {
			continue
		}
		body, _ := os.ReadFile(filepath.Join(c.dir, e.Path))
		mode := int64(0o644)
		if e.Mode == "100755" {
			mode = 0o755
		}
		tw.WriteHeader(&tar.Header{Name: top + e.Path, Mode: mode, Size: int64(len(body)), Typeflag: tar.TypeReg})
		tw.Write(body)
	}
	tw.Close()
	zw.Close()
	return io.NopCloser(&buf), nil
}

func TestManyMissingFilesComeFromOneArchive(t *testing.T) {
	home, _ := importHome(t)
	f := newFakeImport()
	f.snapshots = false
	files := maps.Clone(antfuSkills)
	for i := range 12 {
		files["skills/vite/references/p"+string(rune('a'+i))+".md"] = "page"
	}
	commit := f.publish(t, "antfu/skills", files)
	b := readAntfu(t, home, f, "antfu/skills@vite")
	if b.Skill("vite").Tree != f.upstreamTree("antfu/skills", "skills/vite") {
		t.Fatal("内容应与 GitHub 一致")
	}
	if n := f.count("archive antfu/skills " + commit); n != 1 {
		t.Errorf("压缩包应只下载一次，实际 %d", n)
	}
	if n := f.count("file antfu/skills " + commit + " skills/vite/references"); n != 0 {
		t.Errorf("用了压缩包就不再逐个下载，实际 %d 次", n)
	}
}

func TestAFileThatFailsToDownloadIsTakenFromTheArchive(t *testing.T) {
	home, _ := importHome(t)
	f := newFakeImport()
	f.snapshots = false
	f.publish(t, "antfu/skills", antfuSkills)
	f.edit = func(p string, b []byte) []byte {
		if strings.HasSuffix(p, "build.md") {
			return []byte("被改过")
		}
		return b
	}
	b := readAntfu(t, home, f, "antfu/skills@vite")
	if body, _ := os.ReadFile(filepath.Join(b.Skill("vite").Dir, "references/build.md")); string(body) != "build" {
		t.Errorf("逐个下载对不上时应改用压缩包：%q", body)
	}

	// 压缩包也拿不到：照常报错
	f.noArchive = true
	src, _ := ParseImportInput("antfu/skills@vite")
	if _, err := ReadGitHubImport(context.Background(), home, f, src, nil); err == nil || !strings.Contains(err.Error(), "对不上") {
		t.Errorf("err = %v", err)
	}
}
