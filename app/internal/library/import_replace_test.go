package library

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// 替换：已有同名、内容不同的技能，用户确认后换成要添加的这份，旧的进废纸篓。

func readSkillBody(t *testing.T, dir string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, "SKILL.md"))
	must(t, err)
	return string(b)
}

// stagingLeft 是暂存目录里还留着的东西（替换完不该留下旧版或临时目录）。
func stagingLeft(t *testing.T, home string) []string {
	t.Helper()
	entries, _ := os.ReadDir(importStaging(home))
	var out []string
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "add-") || strings.HasPrefix(e.Name(), "replaced-") {
			out = append(out, e.Name())
		}
	}
	return out
}

func TestReplacingSwapsInTheNewVersionAndTrashesTheOld(t *testing.T) {
	// Given 技能库里有 npx 从别的仓库装的 vue，Claude Code 通过用户自己建的链接读它
	home, h := importHome(t)
	writeSkill(t, h(".agents/skills/vue"), "我的 vue")
	writeRecords(t, h, gh("vue", "old/vue", "vue/SKILL.md", strings.Repeat("b", 40)))
	must(t, os.MkdirAll(h(".claude/skills"), 0o755))
	must(t, os.Symlink(h(".agents/skills/vue"), h(".claude/skills/vue")))
	f := newFakeImport()
	f.publish(t, "antfu/skills", antfuSkills)
	b := readAntfu(t, home, f, "antfu/skills")
	l := Scan(home)
	p := ImportPlacement{Library: true, Agents: []string{"claude-code"}}

	chk := l.CheckImport(b.Skill("vue"), p)
	if chk.State != ImportConflict || chk.Blocked != "" ||
		len(chk.Replaces) != 1 || chk.Replaces[0] != (ImportExisting{Path: h(".agents/skills/vue")}) {
		t.Fatalf("检查 = %+v", chk)
	}

	// When 确认替换
	out := l.AddImport(b, []string{"vue"}, p, map[string]ImportCheck{"vue": chk})

	// Then 换成了新版，旧版在废纸篓，指向它的链接照样能用
	if !out[0].Added || !out[0].Replaced || out[0].Place != h(".agents/skills/vue") {
		t.Fatalf("结果 = %+v", out)
	}
	if hash, _ := TreeHash(h(".agents/skills/vue")); hash != f.upstreamTree("antfu/skills", "skills/vue") {
		t.Error("技能库里应是要添加的这份")
	}
	if !strings.Contains(readSkillBody(t, h(".Trash/vue")), "我的 vue") {
		t.Error("旧版应在废纸篓里")
	}
	if real, err := realPath(h(".claude/skills/vue")); err != nil || real != h(".agents/skills/vue") || !isLink(h(".claude/skills/vue")) {
		t.Errorf("Claude Code 的链接应原样留着、指向新版：%v %v", real, err)
	}
	var rec map[string]string
	must(t, json.Unmarshal(lockSkills(t, h)["vue"], &rec))
	if rec["source"] != "antfu/skills" {
		t.Errorf("安装记录应换成新来源：%v", rec)
	}
	if left := stagingLeft(t, home); len(left) > 0 {
		t.Errorf("暂存里不该留东西：%v", left)
	}
	if src, ok := Scan(home).ImportedFrom(h(".agents/skills/vue")); !ok || src.Repo != "antfu/skills" {
		t.Errorf("应用记录 = %+v %v", src, ok)
	}
}

func TestReplacingWithALocalFolderDropsTheOldInstallRecord(t *testing.T) {
	// Given 技能库里的 vue 是 npx 从 GitHub 装的
	home, h := importHome(t)
	writeSkill(t, h(".agents/skills/vue"), "GitHub 上的 vue")
	writeRecords(t, h, gh("vue", "old/vue", "vue/SKILL.md", strings.Repeat("b", 40)), gh("vite", "o/r", "vite/SKILL.md", ""))
	src := h("Downloads/vue")
	writeSkill(t, src, "我自己改的 vue")
	b, err := ReadLocalImport(home, []string{src})
	must(t, err)
	defer b.Discard()
	l := Scan(home)
	p := ImportPlacement{Library: true}
	chk := l.CheckImport(b.Skill("vue"), p)

	// When 用本机文件夹替换
	out := l.AddImport(b, []string{"vue"}, p, map[string]ImportCheck{"vue": chk})

	// Then 旧的安装记录去掉：不然「更新」会拿 old/vue 覆盖这份
	if !out[0].Added || !out[0].Replaced {
		t.Fatalf("结果 = %+v", out)
	}
	skills := lockSkills(t, h)
	if _, ok := skills["vue"]; ok {
		t.Error("旧来源的安装记录应去掉")
	}
	if _, ok := skills["vite"]; !ok {
		t.Error("其他记录不能动")
	}
	if !strings.Contains(readSkillBody(t, h(".agents/skills/vue")), "我自己改的 vue") {
		t.Error("技能库里应是本机这份")
	}
}

func TestAReplacementIsSkippedWhenTheExistingSkillChangedAfterConfirming(t *testing.T) {
	home, h := importHome(t)
	writeSkill(t, h(".agents/skills/vue"), "我的 vue")
	f := newFakeImport()
	f.publish(t, "antfu/skills", antfuSkills)
	b := readAntfu(t, home, f, "antfu/skills")
	l := Scan(home)
	p := ImportPlacement{Library: true}
	chk := l.CheckImport(b.Skill("vue"), p)

	// 确认之后，用户在编辑器里又改了自己的 vue
	writeSkill(t, h(".agents/skills/vue"), "我的 vue，刚改过")
	out := l.AddImport(b, []string{"vue", "slidev"}, p, map[string]ImportCheck{"vue": chk})

	if out[0].Added || !out[0].Stale || out[0].Conflict != h(".agents/skills/vue") || !out[1].Added {
		t.Fatalf("结果 = %+v", out)
	}
	if !strings.Contains(readSkillBody(t, h(".agents/skills/vue")), "刚改过") {
		t.Error("改过的 vue 不能被换掉")
	}
	if exists(h(".Trash/vue")) {
		t.Error("没替换就不该往废纸篓里放东西")
	}
	if now := Scan(home).CheckImport(b.Skill("vue"), p); now.SameAs(chk) {
		t.Error("重新检查应看得出变了，要用户重新确认")
	}
}

func TestReplacingAnExistingSkillThatIsGoneJustAddsIt(t *testing.T) {
	home, h := importHome(t)
	writeSkill(t, h(".agents/skills/vue"), "我的 vue")
	f := newFakeImport()
	f.publish(t, "antfu/skills", antfuSkills)
	b := readAntfu(t, home, f, "antfu/skills")
	l := Scan(home)
	p := ImportPlacement{Library: true}
	chk := l.CheckImport(b.Skill("vue"), p)
	must(t, os.RemoveAll(h(".agents/skills/vue")))

	out := l.AddImport(b, []string{"vue"}, p, map[string]ImportCheck{"vue": chk})
	if !out[0].Added || out[0].Replaced {
		t.Fatalf("已有的不在了就照常添加：%+v", out)
	}
}

func TestReplacingAnOldLinkMovesOnlyTheLink(t *testing.T) {
	// Given 技能库里没有 vue，Claude Code 的目录里有个指向 ~/dev/vue 的链接
	home, h := importHome(t)
	writeSkill(t, h("dev/vue"), "开发中的 vue")
	must(t, os.MkdirAll(h(".claude/skills"), 0o755))
	must(t, os.Symlink(h("dev/vue"), h(".claude/skills/vue")))
	f := newFakeImport()
	f.publish(t, "antfu/skills", antfuSkills)
	b := readAntfu(t, home, f, "antfu/skills")
	l := Scan(home)
	p := ImportPlacement{Library: true, Agents: []string{"claude-code"}}
	chk := l.CheckImport(b.Skill("vue"), p)
	if chk.State != ImportConflict || chk.Blocked != "" ||
		len(chk.Replaces) != 1 || chk.Replaces[0] != (ImportExisting{Path: h(".claude/skills/vue"), Link: true}) {
		t.Fatalf("检查 = %+v", chk)
	}

	out := l.AddImport(b, []string{"vue"}, p, map[string]ImportCheck{"vue": chk})

	if !out[0].Added || !out[0].Replaced {
		t.Fatalf("结果 = %+v", out)
	}
	if real, _ := realPath(h(".claude/skills/vue")); real != h(".agents/skills/vue") {
		t.Errorf("Claude Code 应改读技能库里的新版：%s", real)
	}
	if !isLink(h(".Trash/vue")) {
		t.Error("旧链接应在废纸篓里")
	}
	if !strings.Contains(readSkillBody(t, h("dev/vue")), "开发中的 vue") {
		t.Error("旧链接指向的内容不能动")
	}
	if !Scan(home).CanRemoveFromAgent(Scan(home).Get("vue"), "claude-code") {
		t.Error("新链接是本应用建的，应能移除")
	}
	if left := stagingLeft(t, home); len(left) > 0 {
		t.Errorf("暂存里不该留东西：%v", left)
	}
}

func TestAnAgentsOwnCopyIsNeverReplacedThroughALink(t *testing.T) {
	// Given Claude Code 自己的目录里有一份真实的 vue（不是链接）
	home, h := importHome(t)
	writeSkill(t, h(".claude/skills/vue"), "Claude 自己的 vue")
	f := newFakeImport()
	f.publish(t, "antfu/skills", antfuSkills)
	b := readAntfu(t, home, f, "antfu/skills")
	l := Scan(home)
	p := ImportPlacement{Library: true, Agents: []string{"claude-code"}}
	chk := l.CheckImport(b.Skill("vue"), p)
	if chk.State != ImportConflict || chk.Blocked == "" {
		t.Fatalf("不能替换，要说原因：%+v", chk)
	}

	out := l.AddImport(b, []string{"vue"}, p, map[string]ImportCheck{"vue": chk})
	if out[0].Added || out[0].Err == nil {
		t.Fatalf("结果 = %+v", out)
	}
	if exists(h(".agents/skills/vue")) || !strings.Contains(readSkillBody(t, h(".claude/skills/vue")), "Claude 自己的") {
		t.Error("什么都不能动")
	}
}

func TestAReplacementThatCannotTrashTheOldVersionPutsEverythingBack(t *testing.T) {
	// Given 技能库里的 vue 有 npx 的安装记录、Claude Code 通过链接读它
	home, h := importHome(t)
	writeSkill(t, h(".agents/skills/vue"), "我的 vue")
	writeRecords(t, h, gh("vue", "old/vue", "vue/SKILL.md", strings.Repeat("b", 40)))
	recBefore := string(lockSkills(t, h)["vue"])
	f := newFakeImport()
	f.publish(t, "antfu/skills", antfuSkills)
	b := readAntfu(t, home, f, "antfu/skills")
	l := Scan(home)
	p := ImportPlacement{Library: true, Agents: []string{"kiro"}}
	chk := l.CheckImport(b.Skill("vue"), p)

	// When 废纸篓用不了
	trash := Trash
	Trash = func(string, string) error { return errors.New("废纸篓满了") }
	defer func() { Trash = trash }()
	out := l.AddImport(b, []string{"vue"}, p, map[string]ImportCheck{"vue": chk})

	// Then 什么都没换：旧版、记录原样，新链接撤掉，暂存清干净
	if out[0].Added || out[0].Err == nil || !strings.Contains(out[0].Err.Error(), "废纸篓满了") {
		t.Fatalf("结果 = %+v", out)
	}
	if !strings.Contains(readSkillBody(t, h(".agents/skills/vue")), "我的 vue") {
		t.Error("旧版应换回来")
	}
	if got := string(lockSkills(t, h)["vue"]); got != recBefore {
		t.Errorf("安装记录应换回原样：%s", got)
	}
	if exists(h(".kiro/skills/vue")) {
		t.Error("新建的链接应撤掉")
	}
	if _, ok := Scan(home).ImportedFrom(h(".agents/skills/vue")); ok {
		t.Error("应用记录应撤掉")
	}
	if left := stagingLeft(t, home); len(left) > 0 {
		t.Errorf("暂存里不该留东西：%v", left)
	}
}
