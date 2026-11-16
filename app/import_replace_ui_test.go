package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"

	"skillmanager/internal/library"
)

// 已有同名、内容不同的技能：卡片、对比页、勾选框都能确认替换，按「替换」才真的换。

func readBody(t *testing.T, dir string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, "SKILL.md"))
	must(t, err)
	return string(b)
}

func wantTexts(t *testing.T, tt *ui.Tester, texts ...string) {
	t.Helper()
	for _, s := range texts {
		if !tt.HasText(s) {
			t.Errorf("缺 %q：%v", s, tt.Texts())
		}
	}
}

func TestAConflictCanBeReplacedAndTheOldVersionGoesToTheTrash(t *testing.T) {
	// Given 技能库里有我的 vue，要添加的是别人的 vue
	a, tt, h := importApp(t)
	writeTestSkill(t, h(".agents/skills/vue"), "我的 vue", "mine")
	writeTestSkill(t, h("Downloads/vue"), "别人的 vue", "theirs")
	a.rescan()
	a.importPaths([]string{h("Downloads/vue")})
	tt.Frame()
	wantTexts(t, tt, "已有同名", "对比差异", "替换")

	// When 在冲突卡片上点「替换」
	must(t, tt.Click("替换"))

	// Then 标成将替换，列出会移到废纸篓的，按钮写明是替换；这时还没动磁盘
	wantTexts(t, tt, "将替换", "不替换", "添加时换掉下面这些，旧的移到废纸篓：", "~/.agents/skills/vue", "替换 1 个技能")
	if !strings.Contains(readBody(t, h(".agents/skills/vue")), "mine") {
		t.Fatal("确认替换不等于替换：按下按钮前不能动")
	}

	// When 按「替换 1 个技能」
	must(t, tt.Click("替换 1 个技能"))

	// Then 技能库里是新版，旧版在废纸篓
	if !strings.Contains(readBody(t, h(".agents/skills/vue")), "theirs") {
		t.Error("技能库里应是要添加的这份")
	}
	if !strings.Contains(readBody(t, h(".Trash/vue")), "mine") {
		t.Error("旧版应在废纸篓里")
	}
	wantTexts(t, tt, "已添加", "已替换 ~/.agents/skills/vue 里的旧版，旧版在废纸篓。")
}

func TestReplacingCanBeChosenFromTheCompareAndTakenBack(t *testing.T) {
	a, tt, h := importApp(t)
	writeTestSkill(t, h(".agents/skills/vue"), "我的 vue", "mine")
	writeTestSkill(t, h("Downloads/vue"), "别人的 vue", "theirs")
	a.rescan()
	a.importPaths([]string{h("Downloads/vue")})
	tt.Frame()

	// 看完差异直接在对比页决定：回到选择页、标成将替换
	must(t, tt.Click("对比差异"))
	must(t, tt.Click("用这份替换"))
	if a.imp.cmp != nil || !a.importReplacing("vue") {
		t.Fatalf("应回到选择页并标成将替换：cmp=%v", a.imp.cmp != nil)
	}
	wantTexts(t, tt, "将替换", "替换 1 个技能")

	// 改主意：撤回
	must(t, tt.Click("不替换"))
	if a.importReplacing("vue") || len(a.importAddable()) != 0 {
		t.Error("撤回后不应再替换")
	}
	wantTexts(t, tt, "已有同名", "添加技能")

	// 只是多给一个 Agent 建链接：要换掉的还是那一份，确认留着
	a.setImportReplace("vue", true)
	a.setPlacement(library.ImportPlacement{Library: true, Agents: []string{"claude-code"}})
	if !a.importReplacing("vue") {
		t.Error("要换掉的没变，确认不该作废")
	}
	// 换到 Agent 自己的目录：那里没有 vue，不再是替换
	a.setPlacement(library.ImportPlacement{Agents: []string{"claude-code"}})
	if a.importReplacing("vue") {
		t.Error("装到别处时之前的替换确认要作废")
	}
}

func TestSelectAllNeverPicksAReplacementButTheCheckboxCan(t *testing.T) {
	a, tt, h := importApp(t)
	writeTestSkill(t, h(".agents/skills/vue"), "我的 vue", "mine")
	src := h("Downloads/pack")
	writeTestSkill(t, filepath.Join(src, "vue"), "别人的 vue", "theirs")
	writeTestSkill(t, filepath.Join(src, "deploy"), "部署", "x")
	a.rescan()
	a.importPaths([]string{src})
	tt.Frame()

	must(t, tt.Click("全选可添加的 1 个"))
	if got := a.importAddable(); len(got) != 1 || got[0] != "deploy" {
		t.Fatalf("全选只勾能直接添加的：%v", got)
	}
	must(t, tt.Click("勾选 vue"))
	wantTexts(t, tt, "添加 1 个 · 替换 1 个", "将替换")
	must(t, tt.Click("添加 1 个 · 替换 1 个"))
	if !strings.Contains(readBody(t, h(".agents/skills/vue")), "theirs") || !exists(h(".agents/skills/deploy")) {
		t.Error("两个都应装好")
	}
}

func TestAReplacementIsRedecidedWhenTheExistingSkillChangedAfterConfirming(t *testing.T) {
	a, tt, h := importApp(t)
	writeTestSkill(t, h(".agents/skills/vue"), "我的 vue", "mine")
	writeTestSkill(t, h("Downloads/vue"), "别人的 vue", "theirs")
	a.rescan()
	a.importPaths([]string{h("Downloads/vue")})
	tt.Frame()
	must(t, tt.Click("替换"))

	// 确认之后、按下之前，用户又改了自己的 vue
	writeTestSkill(t, h(".agents/skills/vue"), "我的 vue", "mine, edited")
	must(t, tt.Click("替换 1 个技能"))
	if !strings.Contains(readBody(t, h(".agents/skills/vue")), "edited") {
		t.Fatal("改过的不能被换掉")
	}
	wantTexts(t, tt, "确认替换之后，技能库里的 vue 又有了改动，这次没有替换。", "再对比一次，还要替换就再点「替换」。")

	// 看过之后仍要替换：卡片上重新确认，再按
	must(t, tt.Click("替换"))
	must(t, tt.Click("替换 1 个技能"))
	if !strings.Contains(readBody(t, h(".agents/skills/vue")), "theirs") {
		t.Error("重新确认后应替换")
	}
	if !strings.Contains(readBody(t, h(".Trash/vue")), "edited") {
		t.Error("废纸篓里应是改过的那份")
	}
}
