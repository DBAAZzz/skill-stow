package library

import (
	"os"
	"testing"
)

func TestUpdateStateSurvivesARestart(t *testing.T) {
	home, _ := linkHome(t)
	src := SourceID{Type: "github", Repo: "antfu/skills"}
	st := &updateStore{}
	st.setBaseline(src, "skills/vite/SKILL.md", "/h/.agents/skills/vite", "rec1", "tree1")
	st.ignore(src, "skills/vue", "/h/.agents/skills/vue", "/h/.agents/skills", "up2")
	st.stop(src, "/h/.agents/skills/turborepo", "turborepo")
	must(t, st.save(home))

	got, err := loadUpdates(home)
	must(t, err)
	if tree, ok := got.baseline(src, "skills/vite", "/h/.agents/skills/vite", "rec1"); !ok || tree != "tree1" {
		t.Errorf("基线：%q %v", tree, ok)
	}
	if !got.ignored(src, "skills/vue/SKILL.md", "/h/.agents/skills/vue", "/h/.agents/skills", "up2") || !got.stopped(src, "/h/.agents/skills/turborepo") {
		t.Error("忽略和停止检查应保留")
	}
}

func TestBaselinesIgnoresAndStopsDoNotCarryOverToAnotherSource(t *testing.T) {
	src := SourceID{Type: "github", Repo: "antfu/skills"}
	other := SourceID{Type: "github", Repo: "fork/skills"}
	branch := SourceID{Type: "github", Repo: "antfu/skills", Ref: "dev"}
	st := &updateStore{}
	st.setBaseline(src, "skills/vite", "/p/vite", "rec1", "tree1")
	st.ignore(src, "skills/vite", "/p/vite", "/p", "up")
	st.stop(src, "/p/vite", "vite")
	for _, s := range []SourceID{other, branch} {
		if _, ok := st.baseline(s, "skills/vite", "/p/vite", "rec1"); ok {
			t.Errorf("%v 不应用 %v 的基线", s, src)
		}
		if st.ignored(s, "skills/vite", "/p/vite", "/p", "up") || st.stopped(s, "/p/vite") {
			t.Errorf("%v 不应继承忽略、停止检查", s)
		}
	}
	// 安装记录被外部工具换了版本：旧基线失效
	if _, ok := st.baseline(src, "skills/vite", "/p/vite", "rec2"); ok {
		t.Error("记录里的安装版本变了，基线应失效")
	}
	// 只忽略这一个上游目录版本
	if st.ignored(src, "skills/vite", "/p/vite", "/p", "up-next") {
		t.Error("上游目录再变应重新提示")
	}
	st.unignore(src, "skills/vite", "/p/vite", "/p", "up")
	st.resume(src, "/p/vite")
	if st.ignored(src, "skills/vite", "/p/vite", "/p", "up") || st.stopped(src, "/p/vite") {
		t.Error("取消忽略、恢复检查后应清掉标记")
	}
}

func TestABrokenUpdateStateIsReportedNotReplaced(t *testing.T) {
	home, _ := linkHome(t)
	must(t, os.MkdirAll(appDir(home), 0o755))
	must(t, os.WriteFile(updatesFile(home), []byte("{oops"), 0o644))
	if _, err := loadUpdates(home); err == nil {
		t.Error("读不了应报错，不能当成空的覆盖掉（里面可能有未完成的更新）")
	}
}
