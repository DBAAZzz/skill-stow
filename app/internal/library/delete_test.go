package library

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

const deleteLock = `{
  "version": 3,
  "skills": {
    "design-lab": {
      "source": "0xdesign/design-plugin",
      "sourceType": "github"
    },
    "grill-me": {
      "source": "mattpocock/skills",
      "sourceType": "github"
    }
  }
}
`

// deleteHome 是删除用例的主目录：design-lab 和 grill-me 是 npx skills 装进
// 技能库的，Claude Code 目录里各有一个链接指向它们；Codex 直接读技能库。
func deleteHome(t *testing.T, agents ...string) (string, func(string) string) {
	t.Helper()
	home, h := linkHome(t, append([]string{"claude-code", "codex"}, agents...)...)
	t.Setenv("HOME", home)
	// 撤销用的那份放进系统临时目录，测试里改到用例自己的临时目录
	tmp := t.TempDir()
	for _, k := range []string{"TMPDIR", "TMP", "TEMP"} {
		t.Setenv(k, tmp)
	}
	must(t, os.MkdirAll(h(".claude/skills"), 0o755))
	for _, name := range []string{"design-lab", "grill-me"} {
		writeSkill(t, h(".agents/skills/"+name), name+" body")
		mustLink(t, filepath.Join("..", "..", ".agents", "skills", name), h(".claude/skills/"+name))
	}
	must(t, os.WriteFile(h(".agents/.skill-lock.json"), []byte(deleteLock), 0o644))
	return home, h
}

func lockSkills(t *testing.T, h func(string) string) map[string]json.RawMessage {
	t.Helper()
	var lock struct {
		Skills map[string]json.RawMessage `json:"skills"`
	}
	b, err := os.ReadFile(h(".agents/.skill-lock.json"))
	must(t, err)
	must(t, json.Unmarshal(b, &lock))
	return lock.Skills
}

func TestDeletedSkillGoesToTrashAndNoAgentSeesItAnymore(t *testing.T) {
	// Given design-lab 原件在技能库，Claude Code 目录里有指向它的链接
	home, h := deleteHome(t)
	l := Scan(home)

	// When 删除它
	_, err := l.Delete(l.Get("design-lab"))
	must(t, err)

	// Then 原件进了废纸篓，链接没了，重新扫描后「我的」里没有它
	if exists(h(".agents/skills/design-lab")) {
		t.Error("原件还在 Skill 库里")
	}
	if !exists(h(".Trash/design-lab/SKILL.md")) {
		t.Error("废纸篓里没有它")
	}
	if exists(h(".claude/skills/design-lab")) {
		t.Error("Claude Code 目录里的链接还在")
	}
	if s := Scan(home).Get("design-lab"); s != nil {
		t.Errorf("重新扫描后仍有它：%+v", s)
	}
}

func TestDeletingASkillLeavesLinksToOtherSkillsAlone(t *testing.T) {
	// Given Claude Code 目录里还有指向 grill-me 的链接
	home, h := deleteHome(t)
	l := Scan(home)

	// When 删除 design-lab
	_, err := l.Delete(l.Get("design-lab"))
	must(t, err)

	// Then grill-me 的链接和原件都在，Claude Code 照样读得到
	if real, err := realPath(h(".claude/skills/grill-me")); err != nil || real != h(".agents/skills/grill-me") {
		t.Fatalf("grill-me 的链接坏了：%q %v", real, err)
	}
	if s := Scan(home).Get("grill-me"); s == nil || !s.EnabledIn("claude-code") {
		t.Errorf("grill-me 应仍对 Claude Code 启用：%+v", s)
	}
}

func TestBuiltinSkillsCannotBeDeleted(t *testing.T) {
	// Given 一个 Antigravity 自带的技能
	home, h := deleteHome(t, "antigravity")
	writeSkill(t, h(".gemini/antigravity/builtin/skills/browser"), "")
	l := Scan(home)

	s := l.ByID("builtin:" + h(".gemini/antigravity/builtin/skills/browser"))
	if s == nil {
		t.Fatal("示例 Skill 没扫到")
	}
	// When 删除它
	_, err := l.Delete(s)

	// Then 报错，什么都不动
	if err == nil {
		t.Errorf("%s 不应能删除", s.ID)
	}
	if !exists(s.Real) {
		t.Errorf("%s 被移走了", s.Real)
	}
}

func TestSkillWithSameNameCopiesCannotBeDeleted(t *testing.T) {
	// Given Claude Code 目录里另有一份内容不同的 design-lab
	home, h := deleteHome(t)
	must(t, os.Remove(h(".claude/skills/design-lab")))
	writeSkill(t, h(".claude/skills/design-lab"), "edited copy")
	l := Scan(home)

	// When 删除 design-lab
	_, err := l.Delete(l.Get("design-lab"))

	// Then 报「有同名副本」，两份都在原处
	if !errors.Is(err, ErrHasCopies) {
		t.Errorf("应报有同名副本，得到 %v", err)
	}
	for _, p := range []string{".agents/skills/design-lab/SKILL.md", ".claude/skills/design-lab/SKILL.md"} {
		if !exists(h(p)) {
			t.Errorf("%s 不见了", p)
		}
	}
}

func TestDeletingAnNpxInstalledSkillDropsOnlyItsInstallRecord(t *testing.T) {
	// Given design-lab 和 grill-me 都记在 npx skills 的安装记录里
	home, h := deleteHome(t)
	l := Scan(home)

	// When 删除 design-lab
	_, err := l.Delete(l.Get("design-lab"))
	must(t, err)

	// Then 只去掉 design-lab 这一条，其余内容和格式不变
	want := `{
  "version": 3,
  "skills": {
    "grill-me": {
      "source": "mattpocock/skills",
      "sourceType": "github"
    }
  }
}
`
	b, _ := os.ReadFile(h(".agents/.skill-lock.json"))
	if string(b) != want {
		t.Errorf("安装记录应为\n%s\n得到\n%s", want, b)
	}
}

func TestDeletingClearsOnlyTheDisableRecordsThisAppWrote(t *testing.T) {
	// Given 本应用在 Claude Code 里停用过 design-lab；用户自己在 Gemini 里停用了它
	home, h := deleteHome(t, "gemini")
	must(t, os.WriteFile(h(".claude/settings.json"), []byte("{\n  \"theme\": \"dark\"\n}\n"), 0o600))
	gemini := "{\n  \"skills\": {\n    \"disabled\": [\"design-lab\"]\n  }\n}\n"
	must(t, os.WriteFile(h(".gemini/settings.json"), []byte(gemini), 0o600))
	l := Scan(home)
	must(t, l.Enable(l.Get("design-lab"), "claude-code", false))

	// When 删除 design-lab
	_, err := l.Delete(l.Get("design-lab"))
	must(t, err)

	// Then Claude Code 的停用记录去掉了，其他设置和 Gemini 的配置都不动
	claude, _ := os.ReadFile(h(".claude/settings.json"))
	if strings.Contains(string(claude), "design-lab") || !strings.Contains(string(claude), `"theme": "dark"`) {
		t.Errorf("Claude Code 设置应只去掉停用记录：\n%s", claude)
	}
	if b, _ := os.ReadFile(h(".gemini/settings.json")); string(b) != gemini {
		t.Errorf("用户自己写的 Gemini 停用不应被动：\n%s", b)
	}
}

func TestNothingChangesWhenTheSkillCannotBeMovedToTrash(t *testing.T) {
	// Given 废纸篓拒收，本应用在 Claude Code 里停用过 design-lab
	home, h := deleteHome(t)
	l := Scan(home)
	must(t, l.Enable(l.Get("design-lab"), "claude-code", false))
	settings, _ := os.ReadFile(h(".claude/settings.json"))
	defer func(old func(string, string) error) { Trash = old }(Trash)
	Trash = func(string, string) error { return errors.New("废纸篓不可用") }

	// When 删除 design-lab
	_, err := l.Delete(l.Get("design-lab"))

	// Then 报错，原件、链接、安装记录、停用记录都保持原样
	if err == nil {
		t.Fatal("应报错")
	}
	if !exists(h(".agents/skills/design-lab/SKILL.md")) || !exists(h(".claude/skills/design-lab")) {
		t.Error("原件或链接被动了")
	}
	if _, ok := lockSkills(t, h)["design-lab"]; !ok {
		t.Error("安装记录被去掉了")
	}
	if b, _ := os.ReadFile(h(".claude/settings.json")); string(b) != string(settings) {
		t.Errorf("Claude Code 设置被改了：\n%s", b)
	}
}

func TestUndoPutsEverythingBackAsBeforeTheDeletion(t *testing.T) {
	// Given 删除了一个本应用在 Claude Code 里停用过的 design-lab
	home, h := deleteHome(t)
	l := Scan(home)
	must(t, l.Enable(l.Get("design-lab"), "claude-code", false))
	d, err := l.Delete(l.Get("design-lab"))
	must(t, err)

	// When 撤销
	must(t, l.Undo(d))

	// Then 原件、链接、安装记录回来了，Claude Code 里仍是停用
	if b, _ := os.ReadFile(h(".agents/skills/design-lab/SKILL.md")); !strings.Contains(string(b), "design-lab body") {
		t.Errorf("原件没回到原处：%q", b)
	}
	if real, err := realPath(h(".claude/skills/design-lab")); err != nil || real != h(".agents/skills/design-lab") {
		t.Errorf("Claude Code 的链接没回来：%q %v", real, err)
	}
	if _, ok := lockSkills(t, h)["design-lab"]; !ok {
		t.Error("安装记录没写回")
	}
	s := Scan(home).Get("design-lab")
	if s == nil || s.EnabledIn("claude-code") || !s.EnabledIn("codex") {
		t.Errorf("应恢复成 Claude Code 停用、Codex 启用：%+v", s)
	}
}

func TestUndoIsNoLongerPossibleOnceTheDeletionIsLetGo(t *testing.T) {
	// Given 删除了 design-lab，之后这次删除被放下了（又删了别的，或退出了应用）
	home, h := deleteHome(t)
	l := Scan(home)
	d, err := l.Delete(l.Get("design-lab"))
	must(t, err)
	d.Discard()

	// When 撤销
	err = l.Undo(d)

	// Then 报「没法撤销了」，什么都不建，废纸篓里那份还在
	if !errors.Is(err, ErrUndoExpired) {
		t.Errorf("应报没法撤销，得到 %v", err)
	}
	if exists(h(".claude/skills/design-lab")) || exists(h(".agents/skills/design-lab")) {
		t.Error("不应建出链接或目录")
	}
	if !exists(h(".Trash/design-lab/SKILL.md")) {
		t.Error("废纸篓里那份不见了")
	}
}

func TestUndoNeverOverwritesASkillThatCameBack(t *testing.T) {
	// Given 删除 design-lab 后，npx skills 又把它装回了原处
	home, h := deleteHome(t)
	l := Scan(home)
	d, err := l.Delete(l.Get("design-lab"))
	must(t, err)
	writeSkill(t, h(".agents/skills/design-lab"), "reinstalled")

	// When 撤销
	err = l.Undo(d)

	// Then 报冲突，装回来的那份不动，删掉的那份仍在废纸篓
	if err == nil {
		t.Fatal("应报冲突")
	}
	if b, _ := os.ReadFile(h(".agents/skills/design-lab/SKILL.md")); !strings.Contains(string(b), "reinstalled") {
		t.Errorf("装回来的那份被覆盖了：%q", b)
	}
	if !exists(h(".Trash/design-lab/SKILL.md")) {
		t.Error("废纸篓里那份不见了")
	}
}

func TestOneUndoRestoresAWholeBatch(t *testing.T) {
	// Given 一次删除了 design-lab 和 grill-me
	home, h := deleteHome(t)
	l := Scan(home)
	d, err := l.Delete(l.Get("design-lab"), l.Get("grill-me"))
	must(t, err)
	if s := Scan(home); s.Get("design-lab") != nil || s.Get("grill-me") != nil {
		t.Fatal("应两个都删掉")
	}

	// When 撤销一次
	must(t, l.Undo(d))

	// Then 两个都回来了
	s := Scan(home)
	for _, name := range []string{"design-lab", "grill-me"} {
		if k := s.Get(name); k == nil || !k.EnabledIn("claude-code") {
			t.Errorf("%s 没恢复：%+v", name, k)
		}
		if _, ok := lockSkills(t, h)[name]; !ok {
			t.Errorf("%s 的安装记录没写回", name)
		}
	}
}

func TestABatchWithAnUndeletableSkillDeletesNothing(t *testing.T) {
	// Given grill-me 另有一份内容不同的副本
	home, h := deleteHome(t)
	must(t, os.Remove(h(".claude/skills/grill-me")))
	writeSkill(t, h(".claude/skills/grill-me"), "edited copy")
	l := Scan(home)

	// When 一次删除 design-lab 和 grill-me
	_, err := l.Delete(l.Get("design-lab"), l.Get("grill-me"))

	// Then 报错，design-lab 也没删
	if err == nil {
		t.Fatal("应报错")
	}
	if !exists(h(".agents/skills/design-lab/SKILL.md")) {
		t.Error("design-lab 被删了")
	}
}

func TestDeleteImpactNamesWhatEachAgentLoses(t *testing.T) {
	// Given Claude Code 经链接读 design-lab，Codex 直接读技能库
	home, _ := deleteHome(t)
	l := Scan(home)

	// When 看删除 design-lab 的影响
	impact := l.DeleteImpact(l.Get("design-lab"))

	// Then Claude Code 是删掉它的链接，Codex 是读的目录里没了原件
	got := map[string]ImpactKind{}
	for _, im := range impact {
		got[im.Agent] = im.Kind
	}
	if got["claude-code"] != ImpactLink || got["codex"] != ImpactShared {
		t.Errorf("影响不对：%+v", impact)
	}
}

func TestDeletingKeepsAnUnknownVersionOfTheRecordsUntouched(t *testing.T) {
	// Given 安装记录是不认识的 version 4
	home, h := deleteHome(t)
	src := strings.Replace(deleteLock, `"version": 3`, `"version": 4`, 1)
	must(t, os.WriteFile(h(".agents/.skill-lock.json"), []byte(src), 0o644))
	l := Scan(home)

	// When 删除 design-lab
	_, err := l.Delete(l.Get("design-lab"))

	// Then 技能删了，记录一字不改，并说明没能改记录
	if !errors.Is(err, ErrRecordUnusable) {
		t.Errorf("err = %v，want ErrRecordUnusable", err)
	}
	if after, _ := os.ReadFile(h(".agents/.skill-lock.json")); string(after) != src {
		t.Error("version 4 的记录被改了")
	}
}

func TestUndoRetriesOnlyFailedItemsAfterPartialFailure(t *testing.T) {
	// Given 一次删除 design-lab 和 grill-me，之后在 design-lab 原位置预先放了一个同名目录
	home, h := deleteHome(t)
	l := Scan(home)
	d, err := l.Delete(l.Get("design-lab"), l.Get("grill-me"))
	must(t, err)
	writeSkill(t, h(".agents/skills/design-lab"), "blocking")

	// When 第一次撤销（grill-me 成功，design-lab 因同名冲突失败）
	err = l.Undo(d)
	if err == nil {
		t.Fatal("第一次撤销应返回冲突错误")
	}
	if !exists(h(".agents/skills/grill-me/SKILL.md")) {
		t.Fatal("grill-me 应该已恢复")
	}

	// And 移走冲突后第二次撤销，只重试 design-lab，不对已成功的 grill-me 再报错
	must(t, os.RemoveAll(h(".agents/skills/design-lab")))
	if err := l.Undo(d); err != nil {
		t.Fatalf("第二次撤销不应再对已恢复项报错，得到 %v", err)
	}
	if !exists(h(".agents/skills/design-lab/SKILL.md")) {
		t.Fatal("design-lab 没恢复")
	}
	if err := l.Undo(d); !errors.Is(err, ErrUndoExpired) {
		t.Fatalf("全部撤销后应置 spent，再撤销得到 %v", err)
	}
}

func TestRealOutsideReportsPathsOutsideSkillDirs(t *testing.T) {
	home, h := deleteHome(t)
	// 在已知技能目录外创建一个真实技能，并从通用目录链接过去
	writeSkill(t, h("dev/ext-skill"), "outside")
	mustLink(t, h("dev/ext-skill"), h(".agents/skills/ext-skill"))
	// 再在 Agent 目录和早年目录里各放一个真实技能（均属于已知技能目录）
	writeSkill(t, h(".claude/skills/in-agent"), "inside agent")
	must(t, os.MkdirAll(h(".agent/skills"), 0o755))
	writeSkill(t, h(".agent/skills/in-legacy"), "inside legacy")
	mustLink(t, h(".agent/skills/in-legacy"), h(".claude/skills/in-legacy"))
	// 自定义技能目录
	customDir := h(".foo/skills")
	writeSkill(t, filepath.Join(customDir, "in-custom"), "inside custom")
	must(t, Scan(home).AddAgentDir(customDir))

	l := Scan(home)
	if got, ok := l.RealOutside(l.Get("ext-skill")); !ok || got != h("dev/ext-skill") {
		t.Fatalf("ext-skill should be outside: got %q %v", got, ok)
	}
	for _, name := range []string{"design-lab", "in-agent", "in-legacy", "in-custom"} {
		if got, ok := l.RealOutside(l.Get(name)); ok {
			t.Errorf("%s should be inside known skill dirs, got outside=%q", name, got)
		}
	}
	// Sample 数据没有 Real，不应误报为在外面
	if _, ok := Sample().RealOutside(Sample().Get("test-first")); ok {
		t.Error("sample skill without Real should not report outside")
	}
}

func TestDeleteTargetReportsOriginalAndLinksOnDisk(t *testing.T) {
	// Given 技能库里的 design-lab（Claude Code 有链接）、技能目录外的 ext-skill，
	// 以及一个带全局链接的项目技能
	home, h := deleteHome(t)
	writeSkill(t, h("dev/ext-skill"), "outside")
	mustLink(t, h("dev/ext-skill"), h(".agents/skills/ext-skill"))

	proj := h("work/storefront")
	writeSkill(t, filepath.Join(proj, ".claude/skills/release-check"), "project")
	mustLink(t, filepath.Join(proj, ".claude/skills/release-check"), h(".claude/skills/release-check"))
	must(t, Scan(home).AddProjects([]string{proj}))

	l := Scan(home)

	// When 取 design-lab 的删除目标
	dt := l.DeleteTarget(l.Get("design-lab"))
	// Then 原件在技能库，不在目录外，链接指向 .claude/skills/design-lab
	if dt.Original != h(".agents/skills/design-lab") || dt.Outside {
		t.Fatalf("design-lab original = %q outside=%v", dt.Original, dt.Outside)
	}
	if len(dt.Links) != 1 || dt.Links[0].Path != h(".claude/skills/design-lab") || dt.Links[0].Global {
		t.Fatalf("design-lab links = %+v", dt.Links)
	}

	// When 取外部原件 ext-skill 的删除目标
	ext := l.DeleteTarget(l.Get("ext-skill"))
	// Then 标为目录外，并列出技能库里的链接
	if ext.Original != h("dev/ext-skill") || !ext.Outside {
		t.Fatalf("ext-skill original = %q outside=%v", ext.Original, ext.Outside)
	}
	if len(ext.Links) != 1 || ext.Links[0].Path != h(".agents/skills/ext-skill") {
		t.Fatalf("ext-skill links = %+v", ext.Links)
	}

	// When 取带全局链接的项目技能的删除目标
	var projSkill *Skill
	for _, s := range l.Skills {
		if s.Source == Project && s.Name == "release-check" {
			projSkill = s
			break
		}
	}
	if projSkill == nil {
		t.Fatal("未扫描到项目技能 release-check")
	}
	pt := l.DeleteTarget(projSkill)
	if pt.Original != filepath.Join(proj, ".claude/skills/release-check") {
		t.Fatalf("project original = %q", pt.Original)
	}
	if len(pt.Links) != 1 || pt.Links[0].Path != h(".claude/skills/release-check") || !pt.Links[0].Global {
		t.Fatalf("project links = %+v", pt.Links)
	}
}

func TestDeleteTargetReportsPathsInSampleLibrary(t *testing.T) {
	l := Sample()

	// 通用目录技能：原件在技能库，三个链接分别在 Claude Code、CodeBuddy、WorkBuddy
	vue := l.DeleteTarget(l.Get("vue-best-practices"))
	if vue.Original != "~/.agents/skills/vue-best-practices" || vue.Outside {
		t.Fatalf("vue original = %q outside=%v", vue.Original, vue.Outside)
	}
	if len(vue.Links) != 3 {
		t.Fatalf("vue links = %+v, want 3", vue.Links)
	}

	// 原件就在 Agent 目录里且无其他 Agent：0 个链接
	gf := l.DeleteTarget(l.Get("git-flow"))
	if gf.Original != "~/.claude/skills/git-flow" || len(gf.Links) != 0 {
		t.Fatalf("git-flow target = %+v", gf)
	}

	// 项目技能带全局链接：标出 Global=true
	var rc *Skill
	for _, s := range l.Skills {
		if s.Source == Project && s.Name == "release-checklist" {
			rc = s
			break
		}
	}
	pt := l.DeleteTarget(rc)
	if pt.Original != "~/code/storefront/.agents/skills/release-checklist" {
		t.Fatalf("release-checklist original = %q", pt.Original)
	}
	if len(pt.Links) != 1 || pt.Links[0].Path != "~/.claude/skills/release-checklist" || !pt.Links[0].Global {
		t.Fatalf("release-checklist links = %+v", pt.Links)
	}
}

func TestProjectDeleteTargetWarnsWhenLinkPointsOutsideSkillFolders(t *testing.T) {
	home, h := deleteHome(t)
	project := h("work/storefront")
	source := filepath.Join(project, "docs/team-handbook")
	writeSkill(t, source, "shared handbook")
	known := filepath.Join(project, ".claude/skills")
	writeSkill(t, filepath.Join(known, "local"), "local skill")
	mustLink(t, source, filepath.Join(known, "team-handbook"))
	must(t, Scan(home).AddProjects([]string{project}))
	l := Scan(home)
	for _, tc := range []struct {
		name, original string
		outside        bool
	}{
		{"team-handbook", source, true},
		{"local", filepath.Join(known, "local"), false},
	} {
		s := l.Get(tc.name)
		if s == nil || s.Source != Project {
			t.Fatalf("missing project skill %s", tc.name)
		}
		target := l.DeleteTarget(s)
		if target.Outside != tc.outside || target.Original != tc.original {
			t.Errorf("%s: original=%q outside=%v", tc.name, target.Original, target.Outside)
		}
	}
}

func TestDeleteConfirmedKeepsLinksTheDialogDidNotList(t *testing.T) {
	// Given 确认框打开时 design-lab 还没有链接（列出的链接为空），
	// 之后 Claude Code 目录里出现了指向它的链接
	home, h := deleteHome(t)
	l := Scan(home)
	s := l.Get("design-lab")
	target := l.DeleteTarget(s)
	if len(target.Links) != 1 {
		t.Fatalf("design-lab links = %+v, want the Claude Code link", target.Links)
	}
	target.Links = nil

	// When 按确认框里的内容删除
	d, err := l.DeleteConfirmed(target)
	must(t, err)

	// Then 原件移走，没列出的链接留着不动
	if exists(h(".agents/skills/design-lab")) {
		t.Error("the original should be moved to the Trash")
	}
	if !isLink(h(".claude/skills/design-lab")) {
		t.Error("a link the dialog did not list should be left alone")
	}

	// And 撤销照样能放回原件
	must(t, l.Undo(d))
	if !exists(h(".agents/skills/design-lab")) {
		t.Error("undo should put the original back")
	}
}

func TestDeleteConfirmedRemovesTheListedLinks(t *testing.T) {
	home, h := deleteHome(t)
	l := Scan(home)
	_, err := l.DeleteConfirmed(l.DeleteTarget(l.Get("design-lab")))
	must(t, err)
	if isLink(h(".claude/skills/design-lab")) {
		t.Error("the listed link should be removed")
	}
}

func TestSampleDeleteTargetKeepsAbsoluteAgentDirs(t *testing.T) {
	l := Sample()
	s := l.Get("git-flow")
	id := customPrefix + "/opt/agent/skills"
	l.custom = append(l.custom, Agent{ID: id, Name: "agent", Skills: "/opt/agent/skills"})
	s.Enabled = append(slices.Clone(s.Enabled), id)
	if len(s.Available) > 0 {
		s.Available = append(slices.Clone(s.Available), id)
	}
	dt := l.DeleteTarget(s)
	want := filepath.Join("/opt/agent/skills", "git-flow")
	if !slices.ContainsFunc(dt.Links, func(ln DeleteLink) bool { return ln.Path == want }) {
		t.Fatalf("links = %+v, want %s", dt.Links, want)
	}
}
