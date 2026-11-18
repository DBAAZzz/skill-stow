package library

import (
	"os"
	"path/filepath"
	"testing"
)

func stockDocs(t *testing.T, l *Library, h func(string) string) *Skill {
	t.Helper()
	s := l.ByID("project:" + h("code/storefront/.claude/skills/notes-sync"))
	if s == nil || len(s.GlobalDirs()) == 0 {
		t.Fatalf("notes-sync 应被全局链接：%v", s)
	}
	return s
}

func TestCollectingProjectSkillCopiesIntoLibraryAndRepointsGlobalLinks(t *testing.T) {
	// Given 项目技能 notes-sync 被 ~/.claude/skills 全局链接
	home := fakeHome(t)
	h := func(p string) string { return filepath.Join(home, p) }
	l := Scan(home)
	s := stockDocs(t, l, h)
	readersBefore := s.Readers(true)

	// When 收进我的技能
	must(t, l.Collect(s))

	// Then 技能库里有一份真实目录，全局链接改指向它，项目原件不动
	if fi, err := os.Lstat(h(".agents/skills/notes-sync")); err != nil || !fi.IsDir() {
		t.Fatalf("技能库里应有真实目录：%v", err)
	}
	if real, _ := realPath(h(".claude/skills/notes-sync")); real != h(".agents/skills/notes-sync") {
		t.Errorf("全局链接应指向技能库，实际 %s", real)
	}
	if _, err := os.Stat(h("code/storefront/.claude/skills/notes-sync/SKILL.md")); err != nil {
		t.Error("项目里的原件不应动")
	}
	l = Scan(home)
	mine := l.Get("notes-sync")
	if mine == nil || mine.Source != Mine {
		t.Fatalf("应成为我的技能：%+v", mine)
	}
	for _, id := range readersBefore {
		if !mine.DiscoveredIn(id) {
			t.Errorf("原来读到它的 %s 收进后读不到了", id)
		}
	}
	if len(l.Problems().Global) != 0 {
		t.Error("收进后不应再列为全局链接")
	}
}

func TestCollectRefusesWhenLibraryHasSameName(t *testing.T) {
	home := fakeHome(t)
	h := func(p string) string { return filepath.Join(home, p) }
	writeSkill(t, h(".agents/skills/notes-sync"), "技能库里已有的")
	l := Scan(home)
	s := stockDocs(t, l, h)
	if err := l.Collect(s); err == nil {
		t.Fatal("技能库已有同名项时应拒绝")
	}
	if real, _ := realPath(h(".claude/skills/notes-sync")); real != h("code/storefront/.claude/skills/notes-sync") {
		t.Errorf("拒绝时链接不应动，实际 %s", real)
	}
}

func TestKeptLinksStopBeingReportedAndSurviveRescan(t *testing.T) {
	home := fakeHome(t)
	h := func(p string) string { return filepath.Join(home, p) }
	l := Scan(home)
	must(t, l.KeepLinks(stockDocs(t, l, h)))
	if len(l.Problems().Global) != 0 {
		t.Error("保持链接后不应再提醒")
	}
	l = Scan(home)
	if len(l.Problems().Global) != 0 {
		t.Error("重新扫描后仍不应提醒")
	}
	if real, _ := realPath(h(".claude/skills/notes-sync")); real != h("code/storefront/.claude/skills/notes-sync") {
		t.Error("保持链接不应改链接")
	}
}

func TestKeepLinksWriteFailureKeepsTheIssueRetryable(t *testing.T) {
	home := fakeHome(t)
	h := func(p string) string { return filepath.Join(home, p) }
	l := Scan(home)
	s := stockDocs(t, l, h)
	must(t, l.SaveSettings())
	must(t, os.Chmod(settingsFile(home), 0444))
	t.Cleanup(func() { _ = os.Chmod(settingsFile(home), 0644) })
	if err := l.KeepLinks(s); err == nil {
		t.Fatal("read-only settings should reject the operation")
	}
	if s.KeptLinks || len(l.Settings.KeptLinks) != 0 || len(l.Problems().Global) == 0 {
		t.Fatal("failed persistence must not hide the issue or keep an unsaved preference")
	}
}

func TestUnglobalFailureKeepsTheIssueRetryable(t *testing.T) {
	home := fakeHome(t)
	h := func(p string) string { return filepath.Join(home, p) }
	l := Scan(home)
	s := stockDocs(t, l, h)
	dir := filepath.Dir(s.globalLinks[0])
	must(t, os.Chmod(dir, 0555))
	t.Cleanup(func() { _ = os.Chmod(dir, 0755) })
	if err := l.Unglobal(s); err == nil {
		t.Fatal("read-only link directory should reject the operation")
	}
	if len(l.Problems().Global) == 0 || len(s.GlobalDirs()) == 0 {
		t.Fatal("failed unlink must stay visible for retry")
	}
	must(t, os.Chmod(dir, 0755))
	must(t, l.Unglobal(s))
	if len(l.Problems().Global) != 0 {
		t.Fatal("successful retry should resolve the issue")
	}
}
