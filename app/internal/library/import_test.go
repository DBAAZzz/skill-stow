package library

import (
	"archive/zip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"skillmanager/locales"
)

// fakeImport 是读取用的上游：GitHub 部分沿用 fakeRemote，skills.sh 的快照
// 按技能目录给出（可篡改），审计按技能名给出。
type fakeImport struct {
	*fakeRemote
	snapshots bool                          // 提供快照
	tamper    map[string]string             // 快照里改掉的文件（相对技能目录）
	audit     map[string][]AuditResult      // 审计结果；nil 是取不到
	snapCalls []string                      // 请求过快照的 slug
	auditHook func(repo string, s []string) // 看审计请求
	noArchive bool                          // 压缩包下载失败
}

func newFakeImport() *fakeImport { return &fakeImport{fakeRemote: newFakeRemote(), snapshots: true} }

func (f *fakeImport) Snapshot(ctx context.Context, repo, slug string) ([]SnapshotFile, error) {
	f.mu.Lock()
	f.snapCalls = append(f.snapCalls, slug)
	f.mu.Unlock()
	if !f.snapshots {
		return nil, errors.New("skills.sh 不可用")
	}
	c, err := f.commit(repo, "")
	if err != nil {
		return nil, err
	}
	// 找名称 slug 对应的技能目录
	for _, e := range c.tree.Entries {
		if filepath.Base(e.Path) != "SKILL.md" {
			continue
		}
		b, _ := os.ReadFile(filepath.Join(c.dir, e.Path))
		if SkillSlug(ParseSkillMD(string(b)).Name) != slug {
			continue
		}
		dir := filepath.Dir(e.Path)
		var out []SnapshotFile
		for _, x := range c.tree.Entries {
			if x.Type != "blob" || dir != "." && !strings.HasPrefix(x.Path, dir+"/") {
				continue
			}
			rel := strings.TrimPrefix(x.Path, dir+"/")
			body, _ := os.ReadFile(filepath.Join(c.dir, x.Path))
			if t, ok := f.tamper[rel]; ok {
				body = []byte(t)
			}
			out = append(out, SnapshotFile{rel, string(body)})
		}
		return out, nil
	}
	return nil, errors.New("404")
}

func (f *fakeImport) Audit(ctx context.Context, repo string, skills []string) (map[string][]AuditResult, error) {
	if f.auditHook != nil {
		f.auditHook(repo, skills)
	}
	if f.audit == nil {
		return nil, errors.New("超时")
	}
	return f.audit, nil
}

var antfuSkills = map[string]string{
	"README.md":                        "# antfu skills",
	"skills/vite/SKILL.md":             "---\nname: vite\ndescription: Vite 配置、插件和构建优化\n---\n# vite\n",
	"skills/vite/references/build.md":  "build",
	"skills/slidev/SKILL.md":           "---\nname: slidev\ndescription: 用 Markdown 写演示文稿\n---\n# Slidev\n",
	"skills/slidev/scripts/export.sh*": "#!/bin/sh\necho export",
	"skills/vue/SKILL.md":              "---\nname: vue\ndescription: Vue 3\n---\nvue body",
	"skills/notes/SKILL.md":            "---\nname: web-design-guidelines\n---\n没有简介",
}

// importHome 是添加用例的主目录：装了 claude-code、codex、cursor、kiro。
func importHome(t *testing.T) (string, func(string) string) {
	t.Helper()
	home, h := updatesHome(t, "claude-code", "codex", "cursor", "kiro")
	return home, h
}

func readAntfu(t *testing.T, home string, f *fakeImport, input string) *ImportBatch {
	t.Helper()
	src, err := ParseImportInput(input)
	must(t, err)
	b, err := ReadGitHubImport(context.Background(), home, f, src, nil)
	must(t, err)
	t.Cleanup(b.Discard)
	return b
}

func batchNames(b *ImportBatch) []string {
	var out []string
	for _, s := range b.Skills {
		out = append(out, s.Name)
	}
	return out
}

func TestReadingAGitHubRepoDownloadsEverySkillWhole(t *testing.T) {
	home, _ := importHome(t)
	f := newFakeImport()
	commit := f.publish(t, "antfu/skills", antfuSkills)
	b := readAntfu(t, home, f, "antfu/skills")

	if got := batchNames(b); !slices.Equal(got, []string{"web-design-guidelines", "slidev", "vite", "vue"}) {
		t.Fatalf("Skill = %v", got)
	}
	if b.Commit != commit {
		t.Errorf("提交 = %s", b.Commit)
	}
	vite := b.Skill("vite")
	if vite.Tree != f.upstreamTree("antfu/skills", "skills/vite") || vite.Problem != "" || vite.Count != 2 {
		t.Errorf("vite = %+v", vite)
	}
	if body, err := os.ReadFile(filepath.Join(vite.Dir, "references/build.md")); err != nil || string(body) != "build" {
		t.Errorf("vite 应整份下载到本机：%q %v", body, err)
	}
	slidev := b.Skill("slidev")
	if !slices.Equal(slidev.Exec, []string{"scripts/export.sh"}) {
		t.Errorf("可执行文件 = %v", slidev.Exec)
	}
	if fi, _ := os.Stat(filepath.Join(slidev.Dir, "scripts/export.sh")); fi == nil || fi.Mode()&0o100 == 0 {
		t.Error("执行位应保留")
	}
	notes := b.Skill("web-design-guidelines")
	if !notes.MissingDescription() || notes.Problem != "" || notes.Folder != "notes" {
		t.Errorf("缺简介的 Skill = %+v", notes)
	}
}

func TestSnapshotContentIsUsedOnlyWhereItMatchesGitHub(t *testing.T) {
	home, _ := importHome(t)
	f := newFakeImport()
	f.publish(t, "antfu/skills", antfuSkills)
	f.tamper = map[string]string{"references/build.md": "被改过的内容"}
	b := readAntfu(t, home, f, "antfu/skills@vite")

	vite := b.Skill("vite")
	if body, _ := os.ReadFile(filepath.Join(vite.Dir, "references/build.md")); string(body) != "build" {
		t.Errorf("快照对不上的文件应改从 GitHub 取：%q", body)
	}
	if vite.Tree != f.upstreamTree("antfu/skills", "skills/vite") {
		t.Error("预览内容应与 GitHub 一致")
	}
	if n := f.count("file antfu/skills " + b.Commit + " skills/vite/references"); n != 1 {
		t.Errorf("只有对不上的文件从 GitHub 下载，实际 %d 次：%v", n, f.calls)
	}
}

func TestReadingStillWorksWithoutSkillsSh(t *testing.T) {
	home, _ := importHome(t)
	f := newFakeImport()
	f.snapshots = false
	f.publish(t, "antfu/skills", antfuSkills)
	b := readAntfu(t, home, f, "antfu/skills")
	for _, s := range b.Skills {
		if len(s.Audit) != 0 || s.Problem != "" {
			t.Errorf("%s = %+v", s.Name, s)
		}
	}
	if b.Skill("vite").Tree != f.upstreamTree("antfu/skills", "skills/vite") {
		t.Error("没有快照也应从 GitHub 拿到完整内容")
	}
}

func TestAuditIsAttachedPerSkill(t *testing.T) {
	home, _ := importHome(t)
	f := newFakeImport()
	f.publish(t, "antfu/skills", antfuSkills)
	f.audit = map[string][]AuditResult{"slidev": {{Provider: "ath", Risk: "safe"}, {Provider: "snyk", Risk: "high"}}}
	var asked []string
	f.auditHook = func(repo string, s []string) { asked = s }
	b := readAntfu(t, home, f, "antfu/skills")
	if !b.Skill("slidev").Risky() || b.Skill("vite").Risky() || len(b.Skill("vite").Audit) != 0 {
		t.Errorf("审计 = %+v / %+v", b.Skill("slidev").Audit, b.Skill("vite").Audit)
	}
	if !slices.Contains(asked, "vite") {
		t.Errorf("审计应按 Skill 名请求：%v", asked)
	}
}

func TestANamedSkillIsTheOnlyOneRead(t *testing.T) {
	home, _ := importHome(t)
	f := newFakeImport()
	f.publish(t, "antfu/skills", antfuSkills)
	b := readAntfu(t, home, f, "npx skills add https://github.com/antfu/skills --skill slidev -g -y")
	if got := batchNames(b); !slices.Equal(got, []string{"slidev"}) || b.Hidden != 3 {
		t.Errorf("只应读 slidev：%v，没读的 %d 个", got, b.Hidden)
	}
	if got := b.Preselected(); !slices.Equal(got, []string{"slidev"}) {
		t.Errorf("预选 = %v", got)
	}
	if !slices.Equal(f.snapCalls, []string{"slidev"}) {
		t.Errorf("其他 Skill 不应下载：%v", f.snapCalls)
	}
	if !b.Source.IgnoredArgs {
		t.Error("应提示命令里的其他参数不生效")
	}
}

func TestShowingAllStillPreselectsTheNamedSkill(t *testing.T) {
	home, _ := importHome(t)
	f := newFakeImport()
	f.publish(t, "antfu/skills", antfuSkills)
	src, err := ParseImportInput("antfu/skills@slidev")
	must(t, err)
	src.All = true
	b, err := ReadGitHubImport(context.Background(), home, f, src, nil)
	must(t, err)
	defer b.Discard()
	if len(b.Skills) != 4 || b.Hidden != 0 || !slices.Equal(b.Preselected(), []string{"slidev"}) {
		t.Errorf("Skill %v，预选 %v", batchNames(b), b.Preselected())
	}
}

func TestANameThatIsNotAFolderNameFallsBackToReadingAll(t *testing.T) {
	// notes 目录里的技能叫 web-design-guidelines：只看目录名找不到，读全部再按名称预选
	home, _ := importHome(t)
	f := newFakeImport()
	f.publish(t, "antfu/skills", antfuSkills)
	b := readAntfu(t, home, f, "antfu/skills@web-design-guidelines")
	if len(b.Skills) != 4 || !slices.Equal(b.Preselected(), []string{"web-design-guidelines"}) {
		t.Errorf("Skill %v，预选 %v", batchNames(b), b.Preselected())
	}
	if b2 := readAntfu(t, home, f, "skills.sh/antfu/skills/notes"); !slices.Equal(b2.Preselected(), []string{"web-design-guidelines"}) {
		t.Errorf("按目录名也能预选：%v", b2.Preselected())
	}
}

func TestReadErrorsSayWhatHappenedAndWhatToDo(t *testing.T) {
	home, _ := importHome(t)
	f := newFakeImport()
	f.publish(t, "vercel/next.js", map[string]string{"README.md": "x", "packages/next/index.js": "x"})
	cases := map[string]string{
		"antfu/skils":    "GitHub 上没有 antfu/skils。",
		"vercel/next.js": "vercel/next.js 里没有找到 SKILL.md。",
	}
	for in, want := range cases {
		src, _ := ParseImportInput(in)
		_, err := ReadGitHubImport(context.Background(), home, f, src, nil)
		var ie *ImportError
		if !errors.As(err, &ie) || ie.Msg != want || ie.Hint == "" {
			t.Errorf("%s：%v", in, err)
		}
	}
	f.fail["antfu/skills"] = &RemoteError{Kind: RemoteQuota, Reset: time.Now().Add(23 * time.Minute)}
	src, _ := ParseImportInput("antfu/skills")
	_, err := ReadGitHubImport(context.Background(), home, f, src, nil)
	if err == nil || !strings.Contains(err.Error(), "大约 23 分钟后恢复") {
		t.Errorf("限流：%v", err)
	}
	f.fail["antfu/skills"] = &RemoteError{Kind: RemoteNetwork}
	_, err = ReadGitHubImport(context.Background(), home, f, src, nil)
	var ie *ImportError
	if !errors.As(err, &ie) || ie.Msg != "连不上 GitHub。" || !ie.Network {
		t.Errorf("网络不通：%v", err)
	}
	if items, _ := os.ReadDir(stagingDir(home)); len(items) != 0 {
		t.Errorf("失败的读取不应留下暂存：%v", items)
	}
}

func TestAnInvalidNameCannotBeAdded(t *testing.T) {
	home, _ := importHome(t)
	f := newFakeImport()
	f.publish(t, "me/notes", map[string]string{"SKILL.md": "---\nname: My Notes\ndescription: x\n---\n"})
	b := readAntfu(t, home, f, "me/notes")
	sk := b.Skills[0]
	if !strings.Contains(sk.Problem, "名称里有空格和大写字母") {
		t.Errorf("问题 = %q", sk.Problem)
	}
	l := Scan(home)
	if c := l.CheckImport(sk, ImportPlacement{Library: true}); c.State != ImportInvalid {
		t.Errorf("状态 = %v", c.State)
	}
	if out := l.AddImport(b, []string{sk.Name}, ImportPlacement{Library: true}, nil); out[0].Added || out[0].Err == nil {
		t.Errorf("不能添加：%+v", out)
	}
}

func TestAddingToTheLibraryWritesTheSameRecordAsNpx(t *testing.T) {
	home, h := importHome(t)
	writeRecords(t, h, gh("design-lab", "o/r", "skills/design-lab/SKILL.md", strings.Repeat("a", 40)))
	// 顶层字段 npx 也写，不能动
	lock, _ := os.ReadFile(h(".agents/.skill-lock.json"))
	lock = []byte(strings.Replace(string(lock), "\"version\": 3,", "\"version\": 3,\n  \"lastSelectedAgents\": [\"codex\"],\n  \"dismissed\": {\"findSkillsPrompt\": true},", 1))
	must(t, os.WriteFile(h(".agents/.skill-lock.json"), lock, 0o644))
	before := lockSkills(t, h)["design-lab"]

	f := newFakeImport()
	commit := f.publish(t, "antfu/skills", antfuSkills)
	b := readAntfu(t, home, f, "antfu/skills")
	l := Scan(home)
	out := l.AddImport(b, []string{"vite"}, ImportPlacement{Library: true}, nil)
	if !out[0].Added || out[0].Place != h(".agents/skills/vite") {
		t.Fatalf("结果 = %+v", out)
	}
	if hash, _ := TreeHash(h(".agents/skills/vite")); hash != f.upstreamTree("antfu/skills", "skills/vite") {
		t.Error("Skill 库里的内容应与 GitHub 一致")
	}

	var rec map[string]string
	skills := lockSkills(t, h)
	must(t, json.Unmarshal(skills["vite"], &rec))
	want := map[string]string{"source": "antfu/skills", "sourceType": "github", "sourceUrl": "https://github.com/antfu/skills.git",
		"skillPath": "skills/vite/SKILL.md", "skillFolderHash": f.upstreamTree("antfu/skills", "skills/vite")}
	for k, v := range want {
		if rec[k] != v {
			t.Errorf("%s = %q, want %q", k, rec[k], v)
		}
	}
	if _, err := time.Parse("2006-01-02T15:04:05.000Z", rec["installedAt"]); err != nil || rec["installedAt"] != rec["updatedAt"] {
		t.Errorf("时间写法应与 npx 一致：%v", rec)
	}
	if string(skills["design-lab"]) != string(before) {
		t.Error("其他条目不能动")
	}
	after, _ := os.ReadFile(h(".agents/.skill-lock.json"))
	if !strings.Contains(string(after), `"lastSelectedAgents": ["codex"]`) || !strings.Contains(string(after), `"findSkillsPrompt": true`) {
		t.Errorf("顶层字段不能动：\n%s", after)
	}

	// 衔接更新：添加后立刻出现在「更新」页，上游有新提交时可直接更新
	l = Scan(home)
	if it := itemNamed(t, l.Updates(), "vite"); it.State != UpdUnchecked || it.Record.SkillFolderHash != rec["skillFolderHash"] {
		t.Errorf("更新页 = %+v", it)
	}
	next := maps.Clone(antfuSkills)
	next["skills/vite/SKILL.md"] += "v2\n"
	f.publish(t, "antfu/skills", next)
	_, rep := checkNow(t, home, f)
	if it := itemNamed(t, rep, "vite"); it.State != UpdReady {
		t.Errorf("上游有新提交时应可更新：%+v", it)
	}
	_ = commit
}

func TestARootSkillIsRecordedWithItsCommitLikeNpx(t *testing.T) {
	home, h := importHome(t)
	f := newFakeImport()
	commit := f.publish(t, "tw93/kami", map[string]string{"SKILL.md": "---\nname: kami\ndescription: 排版\n---\n", "assets/a.css": "a"})
	b := readAntfu(t, home, f, "tw93/kami")
	out := Scan(home).AddImport(b, []string{"kami"}, ImportPlacement{Library: true}, nil)
	if !out[0].Added {
		t.Fatalf("%+v", out)
	}
	var rec map[string]string
	must(t, json.Unmarshal(lockSkills(t, h)["kami"], &rec))
	if rec["skillPath"] != "SKILL.md" || rec["skillFolderHash"] != commit {
		t.Errorf("根目录 Skill 的记录 = %v", rec)
	}
}

func TestALeftoverRecordIsReplacedKeepingItsInstallTime(t *testing.T) {
	home, h := importHome(t)
	writeRecords(t, h, gh("vue", "old/vue", "vue/SKILL.md", strings.Repeat("b", 40)))
	f := newFakeImport()
	f.publish(t, "antfu/skills", antfuSkills)
	b := readAntfu(t, home, f, "antfu/skills")
	if out := Scan(home).AddImport(b, []string{"vue"}, ImportPlacement{Library: true}, nil); !out[0].Added {
		t.Fatalf("%+v", out)
	}
	var rec map[string]string
	must(t, json.Unmarshal(lockSkills(t, h)["vue"], &rec))
	if rec["source"] != "antfu/skills" || rec["installedAt"] != "2026-08-20T01:02:03.000Z" || rec["updatedAt"] == rec["installedAt"] {
		t.Errorf("残留记录应换成新条目、installedAt 沿用：%v", rec)
	}
}

func TestAnExistingSkillIsNeverOverwritten(t *testing.T) {
	home, h := importHome(t)
	writeSkill(t, h(".agents/skills/vue"), "我的 vue")
	f := newFakeImport()
	f.publish(t, "antfu/skills", antfuSkills)
	b := readAntfu(t, home, f, "antfu/skills")
	install(t, h, "vite", local(antfuSkills, "skills/vite/"))
	l := Scan(home)
	lib := ImportPlacement{Library: true}
	if c := l.CheckImport(b.Skill("vue"), lib); c.State != ImportConflict || c.Existing != h(".agents/skills/vue") {
		t.Errorf("vue = %+v", c)
	}
	if c := l.CheckImport(b.Skill("vite"), lib); c.State != ImportSame {
		t.Errorf("内容相同的 vite 应是「已添加 · 最新」：%+v", c)
	}
	out := l.AddImport(b, []string{"vue", "slidev"}, lib, nil)
	if out[0].Added || out[0].Conflict != h(".agents/skills/vue") || !out[1].Added {
		t.Errorf("结果 = %+v", out)
	}
	if body, _ := os.ReadFile(h(".agents/skills/vue/SKILL.md")); !strings.Contains(string(body), "我的 vue") {
		t.Error("已有的 vue 不能被改")
	}
}

func TestASkillThatAppearsDuringPreviewIsSkippedAndTheRestAreAdded(t *testing.T) {
	home, h := importHome(t)
	f := newFakeImport()
	f.publish(t, "antfu/skills", antfuSkills)
	b := readAntfu(t, home, f, "antfu/skills")
	l := Scan(home)
	if c := l.CheckImport(b.Skill("vue"), ImportPlacement{Library: true}); c.State != ImportNew {
		t.Fatalf("预览时 vue 是新的：%+v", c)
	}
	// 预览时另开终端装了 vue
	writeSkill(t, h(".agents/skills/vue"), "npx 装的")
	out := l.AddImport(b, []string{"vue", "slidev"}, ImportPlacement{Library: true}, nil)
	if out[0].Conflict == "" || !out[1].Added {
		t.Errorf("结果 = %+v", out)
	}
	if _, ok := lockSkills(t, h)["vue"]; ok {
		t.Error("没添加的 Skill 不能写记录")
	}
}

func TestAgentModePutsFilesInTheFirstAgentAndLinksTheRest(t *testing.T) {
	home, h := importHome(t)
	f := newFakeImport()
	f.publish(t, "antfu/skills", antfuSkills)
	b := readAntfu(t, home, f, "antfu/skills")
	l := Scan(home)
	p := ImportPlacement{Agents: []string{"claude-code", "kiro"}}
	chosen, also := l.ImportReaders(p)
	if !slices.Equal(chosen, []string{"claude-code", "kiro"}) || !slices.Equal(also, []string{"cursor"}) {
		t.Errorf("谁会读到 = %v，顺带 = %v", chosen, also)
	}
	out := l.AddImport(b, []string{"slidev"}, p, nil)
	if !out[0].Added || out[0].Place != h(".claude/skills/slidev") {
		t.Fatalf("%+v", out)
	}
	if isLink(h(".claude/skills/slidev")) {
		t.Error("第一个 Agent 的目录里应是真实文件")
	}
	if real, err := realPath(h(".kiro/skills/slidev")); !isLink(h(".kiro/skills/slidev")) || err != nil || real != h(".claude/skills/slidev") {
		t.Errorf("Kiro 应通过链接读同一份：%v %v", real, err)
	}
	if exists(h(".agents/skills/slidev")) {
		t.Error("Skill 库里不应有")
	}
	if exists(h(".agents/.skill-lock.json")) {
		t.Error("指定 Agent 模式不写安装记录")
	}
	// 链接是本应用建的：可以在详情栏移除
	l = Scan(home)
	if s := l.Get("slidev"); s == nil || !l.CanRemoveFromAgent(s, "kiro") {
		t.Error("本应用建的链接应能移除")
	}
	if src, ok := l.ImportedFrom(h(".claude/skills/slidev")); !ok || src.Repo != "antfu/skills" {
		t.Errorf("应用记录 = %+v %v", src, ok)
	}
}

func TestLibraryModeLinksTheAgentsTheUserAdded(t *testing.T) {
	home, h := importHome(t)
	f := newFakeImport()
	f.publish(t, "antfu/skills", antfuSkills)
	b := readAntfu(t, home, f, "antfu/skills")
	l := Scan(home)
	if got := l.ImportLinkable(); !slices.Equal(got, []string{"claude-code", "kiro"}) {
		t.Errorf("可另外添加的 = %v", got)
	}
	chosen, _ := l.ImportReaders(ImportPlacement{Library: true, Agents: []string{"claude-code"}})
	if !slices.Contains(chosen, "codex") || !slices.Contains(chosen, "claude-code") || slices.Contains(chosen, "kiro") {
		t.Errorf("谁会读到 = %v", chosen)
	}
	out := l.AddImport(b, []string{"vite"}, ImportPlacement{Library: true, Agents: []string{"claude-code"}}, nil)
	if !out[0].Added {
		t.Fatalf("%+v", out)
	}
	if real, _ := realPath(h(".claude/skills/vite")); real != h(".agents/skills/vite") {
		t.Errorf("Claude Code 的链接 → %s", real)
	}
}

func TestAnUnreadableRecordFileMeansNothingIsAdded(t *testing.T) {
	home, h := importHome(t)
	writeRecordsVersion(t, h, 2, gh("x", "o/r", "x/SKILL.md", ""))
	before, _ := os.ReadFile(h(".agents/.skill-lock.json"))
	f := newFakeImport()
	f.publish(t, "antfu/skills", antfuSkills)
	b := readAntfu(t, home, f, "antfu/skills")
	out := Scan(home).AddImport(b, []string{"vite"}, ImportPlacement{Library: true, Agents: []string{"claude-code"}}, nil)
	if out[0].Added || out[0].Err == nil || !strings.Contains(out[0].Err.Error(), "无法识别") {
		t.Errorf("%+v", out)
	}
	if exists(h(".agents/skills/vite")) || exists(h(".claude/skills/vite")) {
		t.Error("失败时目录和链接都不能留")
	}
	if after, _ := os.ReadFile(h(".agents/.skill-lock.json")); string(after) != string(before) {
		t.Error("记录要原样不动")
	}
}

func TestALargeRepoDownloadsSkillsOnlyWhenPicked(t *testing.T) {
	home, h := importHome(t)
	files := map[string]string{}
	for i := range largeRepo + 1 {
		files[fmt.Sprintf("skills/s%02d/SKILL.md", i)] = fmt.Sprintf("---\nname: s%02d\ndescription: x\n---\n", i)
		files[fmt.Sprintf("skills/s%02d/ref.md", i)] = "ref"
	}
	f := newFakeImport()
	f.publish(t, "big/skills", files)
	b := readAntfu(t, home, f, "big/skills")
	sk := b.Skill("s07")
	if !sk.Partial || exists(filepath.Join(sk.Dir, "ref.md")) {
		t.Fatalf("大仓库读取时只拿 SKILL.md：%+v", sk)
	}
	l := Scan(home)
	if out := l.AddImport(b, []string{"s07"}, ImportPlacement{Library: true}, nil); !errors.Is(out[0].Err, ErrImportPending) {
		t.Errorf("补齐前不能添加：%+v", out)
	}
	must(t, b.FetchImportSkill(context.Background(), "s07"))
	if sk.Partial || !exists(filepath.Join(sk.Dir, "ref.md")) {
		t.Fatalf("补齐后 = %+v", sk)
	}
	if out := l.AddImport(b, []string{"s07"}, ImportPlacement{Library: true}, nil); !out[0].Added || !exists(h(".agents/skills/s07/ref.md")) {
		t.Errorf("%+v", out)
	}
}

func TestABranchNameWithSlashesIsSplitOffTheFolder(t *testing.T) {
	// Given 仓库有个带斜杠的分支 feature/x
	home, _ := importHome(t)
	f := newFakeImport()
	sha := f.publish(t, "antfu/skills", antfuSkills)
	f.branch("antfu/skills", "feature/x", sha)
	f.branch("antfu/skills", "feature-y", sha)

	// When 粘贴这个分支下子目录的链接
	b := readAntfu(t, home, f, "https://github.com/antfu/skills/tree/feature/x/skills/vite")

	// Then 分支名整段认出，剩下的是子目录
	if b.Source.Ref != "feature/x" || b.Source.Subpath != "skills/vite" || !slices.Equal(batchNames(b), []string{"vite"}) {
		t.Errorf("ref=%q sub=%q Skill %v", b.Source.Ref, b.Source.Subpath, batchNames(b))
	}
}

func TestAnAmbiguousBranchNameIsReportedNotGuessed(t *testing.T) {
	// Given 分支 feature 和标签 feature/x 同时存在
	home, _ := importHome(t)
	f := newFakeImport()
	sha := f.publish(t, "antfu/skills", antfuSkills)
	f.branch("antfu/skills", "feature", sha)
	f.branch("antfu/skills", "feature/x", sha)
	src, err := ParseImportInput("https://github.com/antfu/skills/tree/feature/x/skills/vite")
	must(t, err)

	// When 读取
	_, err = ReadGitHubImport(context.Background(), home, f, src, nil)

	// Then 报出两个都对得上，不去猜
	var ie *ImportError
	if !errors.As(err, &ie) || ie.Msg != locales.ImportAmbiguousRef("feature", "feature/x") {
		t.Errorf("err = %v", err)
	}
}

func TestWithoutTreeSizesTheSizeIsCountedWhileDownloading(t *testing.T) {
	// Given 目录树不带文件大小（git 协议）
	home, _ := importHome(t)
	f := newFakeImport()
	f.unsized = true
	f.publish(t, "antfu/skills", antfuSkills)

	b := readAntfu(t, home, f, "antfu/skills")

	vite := b.Skill("vite")
	want := int64(len(antfuSkills["skills/vite/SKILL.md"]) + len(antfuSkills["skills/vite/references/build.md"]))
	if vite.Size != want || vite.Problem != "" || vite.Count != 2 {
		t.Errorf("vite = %+v，大小 want %d", vite, want)
	}
}

func TestWithoutTreeSizesAPickedSkillGetsItsSizeOnceFetched(t *testing.T) {
	// Given 大仓库，目录树不带文件大小
	home, _ := importHome(t)
	files := map[string]string{}
	for i := range largeRepo + 1 {
		files[fmt.Sprintf("skills/s%02d/SKILL.md", i)] = fmt.Sprintf("---\nname: s%02d\ndescription: x\n---\n", i)
		files[fmt.Sprintf("skills/s%02d/ref.md", i)] = "ref"
	}
	f := newFakeImport()
	f.unsized = true
	f.publish(t, "big/skills", files)
	b := readAntfu(t, home, f, "big/skills")

	// Then 只拿了 SKILL.md 时大小未知，补齐后才有
	sk := b.Skill("s07")
	if !sk.Partial || sk.Size != -1 {
		t.Fatalf("补齐前 = %+v", sk)
	}
	must(t, b.FetchImportSkill(context.Background(), "s07"))
	if want := int64(len(files["skills/s07/SKILL.md"]) + 3); sk.Partial || sk.Size != want {
		t.Errorf("补齐后大小 %d，want %d", sk.Size, want)
	}
}

func TestWithoutTreeSizesAnOversizedSkillStopsAndKeepsOnlySkillMD(t *testing.T) {
	// Given 目录树不带文件大小，一个技能的文件超过单个技能的上限
	home, _ := importHome(t)
	f := newFakeImport()
	f.unsized = true
	f.publish(t, "big/one", map[string]string{
		"skills/big/SKILL.md": "---\nname: big\ndescription: x\n---\n",
		"skills/big/data.bin": strings.Repeat("x", maxImportBytes+1),
	})

	b := readAntfu(t, home, f, "big/one")

	// Then 下载时发现超限：标出原因，暂存里只留 SKILL.md
	big := b.Skill("big")
	if big.Problem == "" || big.Problem != sizeProblem(big.Count, big.Size) {
		t.Errorf("原因 = %q（大小 %d）", big.Problem, big.Size)
	}
	if exists(filepath.Join(big.Dir, "data.bin")) || !exists(filepath.Join(big.Dir, "SKILL.md")) {
		t.Error("超限后应只留 SKILL.md")
	}
}

func TestWithoutTreeSizesAnLFSFileDoesNotLiftTheSizeLimit(t *testing.T) {
	// Given 目录树不带文件大小，先遇到 LFS 指针，后面的文件加起来超限
	home, _ := importHome(t)
	f := newFakeImport()
	f.unsized, f.snapshots, f.noArchive = true, false, true
	f.publish(t, "big/one", map[string]string{
		"skills/big/SKILL.md":   "---\nname: big\ndescription: x\n---\n",
		"skills/big/a-lfs":      "version https://git-lfs.github.com/spec/v1\noid sha256:123\nsize 42\n",
		"skills/big/b.bin":      strings.Repeat("x", 11<<20),
		"skills/big/c.bin":      strings.Repeat("y", 11<<20),
		"skills/big/d-tail.txt": "超限后不该再下载",
	})

	b := readAntfu(t, home, f, "big/one")

	// Then 照样在超限处停下，只留 SKILL.md
	big := b.Skill("big")
	if big.Problem != sizeProblem(big.Count, big.Size) {
		t.Errorf("原因 = %q（大小 %d）", big.Problem, big.Size)
	}
	for _, name := range []string{"b.bin", "c.bin", "d-tail.txt"} {
		if exists(filepath.Join(big.Dir, name)) {
			t.Errorf("超限后不该留 %s", name)
		}
	}
}

func TestLocalFoldersAreCopiedAndNeverRecordedForNpx(t *testing.T) {
	home, h := importHome(t)
	src := h("Downloads/pack")
	writeSkill(t, filepath.Join(src, "release-notes"), "notes")
	writeSkill(t, filepath.Join(src, "group/inner"), "inner")
	must(t, os.MkdirAll(filepath.Join(src, "docs"), 0o755))
	b, err := ReadLocalImport(home, []string{src})
	must(t, err)
	defer b.Discard()
	if got := batchNames(b); !slices.Equal(got, []string{"inner", "release-notes"}) {
		t.Errorf("Skill = %v", got)
	}
	if !slices.Equal(b.NotSkills, []string{"docs"}) {
		t.Errorf("不是 Skill 的 = %v", b.NotSkills)
	}
	out := Scan(home).AddImport(b, []string{"release-notes"}, ImportPlacement{Library: true}, nil)
	if !out[0].Added || !exists(filepath.Join(src, "release-notes/SKILL.md")) {
		t.Fatalf("应复制、不移动原文件：%+v", out)
	}
	if exists(h(".agents/.skill-lock.json")) {
		t.Error("本地来源不写安装记录")
	}
	if src2, ok := Scan(home).ImportedFrom(h(".agents/skills/release-notes")); !ok || src2.Type != "local" || src2.Repo != filepath.Join(src, "release-notes") {
		t.Errorf("来源记为本机路径：%+v", src2)
	}
}

func TestLocalSkillsWithLinksOutsideThemCannotBeAdded(t *testing.T) {
	home, h := importHome(t)
	writeSkill(t, h("dev/sneaky"), "x")
	mustLink(t, h(".ssh"), h("dev/sneaky/keys"))
	must(t, os.MkdirAll(h(".ssh"), 0o700))
	b, err := ReadLocalImport(home, []string{h("dev/sneaky")})
	must(t, err)
	defer b.Discard()
	if p := b.Skills[0].Problem; !strings.Contains(p, "链接") {
		t.Errorf("问题 = %q", p)
	}
}

func TestALinkChainThatEndsOutsideTheSkillCannotBeAdded(t *testing.T) {
	// a -> . 和 b -> a/.. 单看都在技能里，顺着解析 b 落在上一层
	home, h := importHome(t)
	writeSkill(t, h("dev/chain"), "x")
	mustLink(t, ".", h("dev/chain/a"))
	mustLink(t, "a/..", h("dev/chain/b"))
	b, err := ReadLocalImport(home, []string{h("dev/chain")})
	must(t, err)
	defer b.Discard()
	if p := b.Skills[0].Problem; !strings.Contains(p, "链接") {
		t.Errorf("问题 = %q", p)
	}
	if out := Scan(home).AddImport(b, []string{b.Skills[0].Name}, ImportPlacement{Library: true}, nil); out[0].Added {
		t.Error("不应能添加")
	}
}

func TestLinksThatStayInsideTheSkillAreKept(t *testing.T) {
	home, h := importHome(t)
	writeSkill(t, h("dev/inner"), "inner")
	write(t, h("dev/inner/docs"), "a.md", "a", 0o644)
	mustLink(t, "docs/a.md", h("dev/inner/latest.md"))
	b, err := ReadLocalImport(home, []string{h("dev/inner")})
	must(t, err)
	defer b.Discard()
	if p := b.Skills[0].Problem; p != "" {
		t.Errorf("Skill 里的链接应能添加：%q", p)
	}
}

func TestAZipCannotWriteThroughItsOwnLinks(t *testing.T) {
	home, h := importHome(t)
	outside := h("elsewhere")
	must(t, os.MkdirAll(outside, 0o755))
	evil := h("Downloads/linked.zip")
	must(t, os.MkdirAll(filepath.Dir(evil), 0o755))
	out, err := os.Create(evil)
	must(t, err)
	zw := zip.NewWriter(out)
	hd := &zip.FileHeader{Name: "s/alias"}
	hd.SetMode(os.ModeSymlink | 0o777)
	w, err := zw.CreateHeader(hd)
	must(t, err)
	w.Write([]byte(outside))
	for _, name := range []string{"s/SKILL.md", "s/alias/planted.txt"} {
		w, err = zw.Create(name)
		must(t, err)
		w.Write([]byte("---\nname: s\n---\n"))
	}
	must(t, zw.Close())
	must(t, out.Close())

	if _, err := ReadLocalImport(home, []string{evil}); err == nil || !strings.Contains(err.Error(), "不安全的链接") {
		t.Errorf("应拒绝整个 zip：%v", err)
	}
	if exists(filepath.Join(outside, "planted.txt")) {
		t.Fatal("zip 顺着链接写到了外面")
	}
}

func writeZip(t *testing.T, file string, entries map[string]string) {
	t.Helper()
	out, err := os.Create(file)
	must(t, err)
	zw := zip.NewWriter(out)
	for name, body := range entries {
		w, err := zw.Create(name)
		must(t, err)
		w.Write([]byte(body))
	}
	must(t, zw.Close())
	must(t, out.Close())
}

func TestZipsAreUnpackedAndUnsafeOnesRejectedWhole(t *testing.T) {
	home, h := importHome(t)
	good := h("Downloads/slides.zip")
	must(t, os.MkdirAll(filepath.Dir(good), 0o755))
	writeZip(t, good, map[string]string{"slides/SKILL.md": "---\nname: slides\ndescription: x\n---\n", "slides/a.md": "a", "__MACOSX/._x": "x"})
	b, err := ReadLocalImport(home, []string{good})
	must(t, err)
	defer b.Discard()
	if got := batchNames(b); !slices.Equal(got, []string{"slides"}) || b.Skills[0].RepoPath != filepath.Join(good, "slides") {
		t.Errorf("zip 里的 Skill = %v %s", got, b.Skills[0].RepoPath)
	}

	evil := h("Downloads/evil.zip")
	writeZip(t, evil, map[string]string{"ok/SKILL.md": "---\nname: ok\n---\n", "../evil": "x"})
	if _, err := ReadLocalImport(home, []string{evil}); err == nil || !strings.Contains(err.Error(), "不安全的路径") {
		t.Errorf("路径穿越应拒绝整个 zip：%v", err)
	}
	if exists(h("Downloads/evil")) || exists(filepath.Join(stagingDir(home), "evil")) {
		t.Error("不能写到暂存以外")
	}

	empty := h("Downloads/skills-backup.zip")
	writeZip(t, empty, map[string]string{"docs/a.md": "a", "images/b.png": "b"})
	_, err = ReadLocalImport(home, []string{empty})
	var ie *ImportError
	if !errors.As(err, &ie) || ie.Msg != "skills-backup.zip 里没有 SKILL.md。" || !strings.Contains(ie.Hint, "2 个文件夹（docs、images）") {
		t.Errorf("不是 Skill：%v", err)
	}
}

func TestStagingLeftByAQuitIsCleanedOnStart(t *testing.T) {
	home, _ := importHome(t)
	must(t, os.MkdirAll(filepath.Join(stagingDir(home), "import-123/0"), 0o755))
	must(t, os.MkdirAll(filepath.Join(stagingDir(home), "update-9"), 0o755))
	CleanImportStaging(home)
	if exists(filepath.Join(stagingDir(home), "import-123")) || !exists(filepath.Join(stagingDir(home), "update-9")) {
		t.Error("只清读取留下的暂存，更新的不动")
	}
}

func TestGitHubStyleZipsAndNestedFoldersAreFound(t *testing.T) {
	home, h := importHome(t)
	ghZip := h("Downloads/skills-main.zip")
	must(t, os.MkdirAll(filepath.Dir(ghZip), 0o755))
	writeZip(t, ghZip, map[string]string{
		"skills-main/skills/antfu/SKILL.md":            "---\nname: antfu\ndescription: a\n---\n",
		"skills-main/skills/vue/SKILL.md":              "---\nname: vue\ndescription: v\n---\n",
		"skills-main/skills/vue/examples/sub/SKILL.md": "---\nname: nested-sub\ndescription: no\n---\n",
		"skills-main/.claude/skills/helper/SKILL.md":   "---\nname: helper\ndescription: h\n---\n",
		"skills-main/instructions/vue.md":              "doc",
		"skills-main/.github/FUNDING.yml":              "github",
		"skills-main/node_modules/pkg/SKILL.md":        "---\nname: dep\ndescription: no\n---\n",
		"skills-main/.git/hooks/SKILL.md":              "---\nname: hook\ndescription: no\n---\n",
	})
	b, err := ReadLocalImport(home, []string{ghZip})
	must(t, err)
	defer b.Discard()
	if got := batchNames(b); !slices.Equal(got, []string{"helper", "antfu", "vue"}) {
		t.Fatalf("GitHub zip Skill = %v", got)
	}
	if got := b.Skill("vue").RepoPath; got != filepath.Join(ghZip, "skills-main/skills/vue") {
		t.Errorf("RepoPath = %q", got)
	}

	nestedDir := h("Downloads/nested-pack")
	writeSkill(t, filepath.Join(nestedDir, "a/b/c/d/e/deep-ok"), "ok")
	writeSkill(t, filepath.Join(nestedDir, "a/b/c/d/e/f/too-deep"), "skip")
	b2, err := ReadLocalImport(home, []string{nestedDir})
	must(t, err)
	defer b2.Discard()
	if got := batchNames(b2); !slices.Equal(got, []string{"deep-ok"}) {
		t.Errorf("嵌套本地目录 = %v", got)
	}

	emptyRepo := h("Downloads/empty-repo-main.zip")
	writeZip(t, emptyRepo, map[string]string{
		"empty-repo-main/docs/a.md":      "a",
		"empty-repo-main/.github/ci.yml": "ci",
	})
	_, err = ReadLocalImport(home, []string{emptyRepo})
	var ie *ImportError
	if !errors.As(err, &ie) || ie.Msg != "empty-repo-main.zip 里没有 SKILL.md。" || !strings.Contains(ie.Hint, "1 个文件夹（empty-repo-main）") {
		t.Errorf("空仓库 zip 错误 = %v (hint=%q)", err, ie.Hint)
	}
}
