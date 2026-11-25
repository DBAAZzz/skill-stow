package main

import (
	"testing"

	"skillmanager/internal/library"
)

func TestAppOpensOnMySkillsTheFirstTime(t *testing.T) {
	// Given 从没打开过（临时目录里没有设置）
	a := newApp(library.Scan(t.TempDir()))

	// Then 停在「我的」
	if a.scope != string(library.ScopeMine) {
		t.Fatalf("第一次打开停在 %q，应为我的", a.scope)
	}
}

func TestAppReopensOnThePageItWasLeftOn(t *testing.T) {
	// Given 打开应用，换到「使用情况」
	home := t.TempDir()
	a := newApp(library.Scan(home))
	tt := newUITester(a.view, 1280, 800)
	tt.Frame()
	a.goTo(scopeUsage)
	tt.Frame()

	// When 关掉再打开
	again := newApp(library.Scan(home))

	// Then 回到「使用情况」
	if again.scope != scopeUsage {
		t.Fatalf("重新打开停在 %q，应为 %q", again.scope, scopeUsage)
	}
}

func TestAppReopensTidyReadyToCheck(t *testing.T) {
	// Given 上次停在整理页
	home := t.TempDir()
	lib := library.Scan(home)
	lib.NoteLastScope(scopeTidy)

	// When 打开应用
	a := newApp(library.Scan(home))

	// Then 回到整理页，并像从侧边栏点进来一样准备检查
	if a.scope != scopeTidy || !a.tidy.auto {
		t.Fatalf("scope=%q auto=%v，应回到整理页并准备检查", a.scope, a.tidy.auto)
	}
}

func TestAppFallsBackToMySkillsWhenTheLastPageIsGone(t *testing.T) {
	for _, last := range []string{projectPrefix + "/no/such/project", "no-such-page"} {
		// Given 上次停在一个已经移除的项目，或者一个认不得的页面
		home := t.TempDir()
		library.Scan(home).NoteLastScope(last)

		// When 打开应用
		a := newApp(library.Scan(home))

		// Then 停在「我的」
		if a.scope != string(library.ScopeMine) {
			t.Errorf("上次停在 %q，打开后停在 %q，应为我的", last, a.scope)
		}
	}
}
