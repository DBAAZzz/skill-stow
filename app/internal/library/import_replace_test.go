package library

import (
	"encoding/json"
	"errors"
	"maps"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"syscall"
	"testing"
)

// 替换：已有同名、内容不同的技能，用户确认后换成要添加的这份，旧的进废纸篓。

// localReplaceHome：技能库里有旧版 vue，Downloads 里有新版，读好了等着替换。
func localReplaceHome(t *testing.T) (string, func(string) string, *ImportBatch, *Library) {
	t.Helper()
	home, h := importHome(t)
	writeSkill(t, h(".agents/skills/vue"), "旧版")
	writeSkill(t, h("Downloads/vue"), "新版")
	b, err := ReadLocalImport(home, []string{h("Downloads/vue")})
	must(t, err)
	t.Cleanup(b.Discard)
	return home, h, b, Scan(home)
}

// failTrash 让废纸篓用不了；hook 在失败前调用，模拟同时发生的事。
func failTrash(t *testing.T, hook func(path string)) {
	t.Helper()
	trash := Trash
	t.Cleanup(func() { Trash = trash })
	Trash = func(_, path string) error {
		if hook != nil {
			hook(path)
		}
		return errors.New("废纸篓满了")
	}
}

func TestTheOldVersionIsKeptWhenARollbackCannotPutItBack(t *testing.T) {
	// Given 确认替换；换进去之后，别的工具把新版挪走了，废纸篓又用不了
	home, h, b, l := localReplaceHome(t)
	p := ImportPlacement{Library: true}
	chk := l.CheckImport(b.Skill("vue"), p)
	var old string
	failTrash(t, func(path string) {
		if old == "" {
			old = path
			must(t, os.Rename(h(".agents/skills/vue"), h(".agents/skills/vue-moved")))
		}
	})

	out := l.AddImport(b, []string{"vue"}, p, map[string]ImportCheck{"vue": chk})[0]

	// Then 没法换回去、也进不了废纸篓：旧版回到空出来的原位置
	if out.Added || out.Err == nil {
		t.Fatalf("结果 = %+v", out)
	}
	if !strings.Contains(readSkillBody(t, h(".agents/skills/vue")), "旧版") {
		t.Error("旧版应放回空出来的原位置")
	}
	if left := stagingLeft(t, home); len(left) > 0 {
		t.Errorf("放回去了，暂存就不该留：%v", left)
	}
}

func TestTheOldVersionStaysInStagingAndIsNamedWhenNothingElseWorks(t *testing.T) {
	// Given 新版被挪走后原位置又被别的东西占了，废纸篓也用不了
	home, h, b, l := localReplaceHome(t)
	p := ImportPlacement{Library: true}
	chk := l.CheckImport(b.Skill("vue"), p)
	realTrash := Trash
	var old string
	failTrash(t, func(path string) {
		if old == "" {
			old = path
			must(t, os.Rename(h(".agents/skills/vue"), h(".agents/skills/vue-moved")))
			writeSkill(t, h(".agents/skills/vue"), "别的工具刚装的")
		}
	})

	out := l.AddImport(b, []string{"vue"}, p, map[string]ImportCheck{"vue": chk})[0]

	// Then 旧版留在暂存里，错误里写明在哪
	if out.Added || out.Err == nil || !strings.Contains(out.Err.Error(), l.Display(old)) {
		t.Fatalf("要告诉用户旧版在哪：%+v", out)
	}
	if !strings.Contains(readSkillBody(t, old), "旧版") {
		t.Fatal("旧版不能被删")
	}
	// 下次启动清暂存：只移到废纸篓，不删
	Trash = realTrash
	CleanImportStaging(home)
	if !strings.Contains(readSkillBody(t, h(".Trash/vue")), "旧版") {
		t.Error("启动时应把留下的旧版移到废纸篓")
	}
}

func TestAnInterruptedReplacementIsMovedToTheTrashOnStartNotDeleted(t *testing.T) {
	// Given 换进去之后、移到废纸篓之前应用退出了
	home, h, b, l := localReplaceHome(t)
	_, old, _, err := l.placeImport(b.Skill("vue"), h(".agents/skills/vue"), true)
	must(t, err)

	// When 下次启动
	CleanImportStaging(home)

	// Then 旧版在废纸篓里，暂存清空
	if exists(old) || !strings.Contains(readSkillBody(t, h(".Trash/vue")), "旧版") {
		t.Errorf("旧版应移到废纸篓：old 还在=%v", exists(old))
	}
	if left := stagingLeft(t, home); len(left) > 0 {
		t.Errorf("暂存里不该留东西：%v", left)
	}
}

func TestAnOldLinkThatCannotBeTrashedUndoesTheWholeReplacement(t *testing.T) {
	// Given Claude Code 的目录里有个指向 ~/dev/vue 的旧链接，替换时废纸篓用不了
	home, h := importHome(t)
	writeSkill(t, h("dev/vue"), "开发中的 vue")
	must(t, os.MkdirAll(h(".claude/skills"), 0o755))
	must(t, os.Symlink(h("dev/vue"), h(".claude/skills/vue")))
	writeSkill(t, h("Downloads/vue"), "新版")
	b, err := ReadLocalImport(home, []string{h("Downloads/vue")})
	must(t, err)
	defer b.Discard()
	l := Scan(home)
	p := ImportPlacement{Library: true, Agents: []string{"claude-code"}}
	chk := l.CheckImport(b.Skill("vue"), p)
	failTrash(t, nil)

	out := l.AddImport(b, []string{"vue"}, p, map[string]ImportCheck{"vue": chk})[0]

	// Then 报错，旧链接原样回到原处，新版没留下
	if out.Added || out.Err == nil {
		t.Fatalf("结果 = %+v", out)
	}
	if to, err := os.Readlink(h(".claude/skills/vue")); err != nil || to != h("dev/vue") {
		t.Errorf("旧链接应原样回来：%q %v", to, err)
	}
	if exists(h(".agents/skills/vue")) {
		t.Error("新版应撤掉")
	}
	if left := stagingLeft(t, home); len(left) > 0 {
		t.Errorf("暂存里不该留东西：%v", left)
	}
}

func TestAnOldLinkAlreadyTrashedIsRebuiltWhenTheOldVersionCannotBe(t *testing.T) {
	// Given 技能库里有旧版 vue，Kiro 目录里有个指向别处的旧链接；旧链接进了
	// 废纸篓，旧版却进不去
	home, h, b, l := localReplaceHome(t)
	writeSkill(t, h("dev/vue"), "开发中的 vue")
	must(t, os.MkdirAll(h(".kiro/skills"), 0o755))
	must(t, os.Symlink(h("dev/vue"), h(".kiro/skills/vue")))
	l = Scan(home)
	p := ImportPlacement{Library: true, Agents: []string{"kiro"}}
	chk := l.CheckImport(b.Skill("vue"), p)
	if len(chk.Replaces) != 2 || chk.Blocked != "" {
		t.Fatalf("检查 = %+v", chk)
	}
	trash := Trash
	t.Cleanup(func() { Trash = trash })
	Trash = func(home, path string) error {
		if isLink(path) {
			return trash(home, path)
		}
		return errors.New("废纸篓满了")
	}

	out := l.AddImport(b, []string{"vue"}, p, map[string]ImportCheck{"vue": chk})[0]

	if out.Added || out.Err == nil {
		t.Fatalf("结果 = %+v", out)
	}
	if !strings.Contains(readSkillBody(t, h(".agents/skills/vue")), "旧版") {
		t.Error("旧版应换回来")
	}
	if to, err := os.Readlink(h(".kiro/skills/vue")); err != nil || to != h("dev/vue") {
		t.Errorf("进了废纸篓的旧链接应照原样重建：%q %v", to, err)
	}
}

func TestASkillThatCannotBeReadInFullIsNotReplaceable(t *testing.T) {
	// Given 旧版里有读不了的东西（命名管道）：没法确认确认之后它有没有被改
	_, h, b, l := localReplaceHome(t)
	must(t, syscall.Mkfifo(h(".agents/skills/vue/pipe"), 0o600))
	p := ImportPlacement{Library: true}
	chk := l.CheckImport(b.Skill("vue"), p)
	if chk.State != ImportConflict || chk.Blocked == "" {
		t.Fatalf("不能替换，要说原因：%+v", chk)
	}
	out := l.AddImport(b, []string{"vue"}, p, map[string]ImportCheck{"vue": chk})[0]
	if out.Added || out.Err == nil || !strings.Contains(readSkillBody(t, h(".agents/skills/vue")), "旧版") {
		t.Errorf("什么都不能动：%+v", out)
	}
}

func TestAFailedReplacementRestoresTheUpdateBaseline(t *testing.T) {
	// Given 从 antfu/skills 装过 vue（有基线），上游出了新版，要替换成新版
	home, h := importHome(t)
	f := newFakeImport()
	f.publish(t, "antfu/skills", antfuSkills)
	b := readAntfu(t, home, f, "antfu/skills")
	p := ImportPlacement{Library: true}
	if out := Scan(home).AddImport(b, []string{"vue"}, p, nil)[0]; !out.Added {
		t.Fatal(out)
	}
	before, err := loadUpdates(home)
	must(t, err)
	next := maps.Clone(antfuSkills)
	next["skills/vue/SKILL.md"] += "\nv2"
	f.publish(t, "antfu/skills", next)
	b2 := readAntfu(t, home, f, "antfu/skills")
	l := Scan(home)
	chk := l.CheckImport(b2.Skill("vue"), p)
	failTrash(t, nil)

	out := l.AddImport(b2, []string{"vue"}, p, map[string]ImportCheck{"vue": chk})[0]

	// Then 文件、应用记录、基线都回到替换前
	if out.Err == nil {
		t.Fatalf("结果 = %+v", out)
	}
	after, err := loadUpdates(home)
	must(t, err)
	if !reflect.DeepEqual(before.Imports, after.Imports) || !reflect.DeepEqual(before.Baselines, after.Baselines) {
		t.Errorf("应用状态应回到替换前：\nbefore=%+v %+v\nafter=%+v %+v", before.Imports, before.Baselines, after.Imports, after.Baselines)
	}
	if strings.Contains(readSkillBody(t, h(".agents/skills/vue")), "v2") {
		t.Error("旧版应换回来")
	}
}

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
		if strings.HasPrefix(e.Name(), "add-") || strings.HasPrefix(e.Name(), replacePrefix) {
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
