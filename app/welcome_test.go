package main

import (
	"path/filepath"
	"testing"

	"github.com/egoist/mygo/ui"

	"skillmanager/internal/library"
	"skillmanager/locales"
)

// welcomeAt 打开主窗口，开场直接停在第 ms 毫秒：第一次摆的姿势立刻到位，
// 测试不必逐帧播过前面几秒（无窗口的光栅化逐帧画很慢）。再画一帧，主界面
// 卡片的位置（上一帧量出来的）也有了。
func welcomeAt(a *app, ms int) *ui.Tester {
	a.wel.open = true
	tt := newUITester(a.view, 1280, 800)
	a.wel.start = tt.Now().Add(-msd(ms))
	tt.Frame()
	tt.Frame()
	return tt
}

// skipStow 把收纳的时钟拨到第 ms 毫秒。
func skipStow(a *app, tt *ui.Tester, ms int) {
	a.wel.stowAt = tt.Now().Add(-msd(ms))
	tt.Frame()
}

// 第一次用本机数据打开时播开场；播到按钮、按回车收好，记进设置，下次启动不再播。
func TestWelcomePlaysOnceOnRealData(t *testing.T) {
	home, _ := filepath.EvalSymlinks(t.TempDir())
	a := newApp(library.Scan(home))
	if !a.needsWelcome(false) {
		t.Fatal("第一次打开没有开场")
	}
	tt := welcomeAt(a, welReady+600)
	tt.Key(0, ui.KeyEnter)
	tt.Frame()
	if a.wel.stowAt.IsZero() {
		t.Fatal("按回车没有收")
	}
	skipStow(a, tt, welDone)
	if a.wel.open {
		t.Fatal("收好以后开场还开着")
	}
	if newApp(library.Scan(home)).needsWelcome(false) {
		t.Fatal("看过开场，重新启动又播了")
	}
}

// 示例数据不写设置，平时不播；SKILLS_WELCOME 强制时才播。
func TestWelcomeSkippedForSampleUnlessForced(t *testing.T) {
	a := newApp(library.Sample())
	if a.needsWelcome(false) {
		t.Fatal("示例数据也播了开场")
	}
	if !a.needsWelcome(true) {
		t.Fatal("强制时没有开场")
	}
}

// 按 Esc 随时跳过，也算看过了。
func TestWelcomeEscapeSkips(t *testing.T) {
	home, _ := filepath.EvalSymlinks(t.TempDir())
	a := newApp(library.Scan(home))
	tt := welcomeAt(a, 1000)
	tt.Key(0, ui.KeyEscape)
	tt.Frame()
	if a.wel.open || a.lib.Settings.WelcomeVersion != welcomeVersion {
		t.Fatalf("按 Esc 没有跳过：开着=%v，记下的版本=%d", a.wel.open, a.lib.Settings.WelcomeVersion)
	}
}

// 按钮露出来之前点不动：卡片还在落，不能半路收。
func TestWelcomeButtonWaitsForTheCards(t *testing.T) {
	a := newApp(library.Sample())
	tt := welcomeAt(a, 3000)
	_ = tt.Click(locales.WelcomeStow())
	tt.Frame()
	if !a.wel.stowAt.IsZero() {
		t.Fatal("按钮还没露出来就收了")
	}
	a.wel.start = a.wel.start.Add(-msd(welReady))
	tt.Frame()
	must(t, tt.Click(locales.WelcomeStow()))
	tt.Frame()
	if a.wel.stowAt.IsZero() {
		t.Fatal("按钮露出来以后点了没收")
	}
}

// 开场跟着界面语言（默认是系统语言）：英文系统里整段都是英文，演示卡片的
// 简介也是。
func TestWelcomeSpeaksTheInterfaceLanguage(t *testing.T) {
	a := newApp(library.Sample())
	a.wel.open = true
	tt := newUITester(a.view, 1280, 800)
	useLocale(t, tt, "en")
	a.wel.start = tt.Now().Add(-msd(welReady + 100))
	tt.Frame()
	for _, s := range []string{"Welcome to Skill Stow", "Skills, scattered across every agent", "Stow Them",
		"Stow them in one place: duplicates merged, broken links found", "Skip",
		"4 copies → 1", "Broken link", "Write the tests first"} {
		if !tt.HasText(s) {
			t.Errorf("英文界面的开场里没有 %q", s)
		}
	}
}
