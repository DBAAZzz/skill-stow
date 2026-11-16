package main

import (
	"path/filepath"
	"slices"
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

// 第三幕往主界面里真实卡片的位置飞；真实卡片不够时，接着最后一张往后排，
// 不叠在真实卡片上。
func TestWelcomeLandsOnRealCards(t *testing.T) {
	a := newApp(library.Sample())
	welcomeAt(a, welReady+100)
	if n := len(a.wel.slots); n < 4 {
		t.Fatalf("主界面里只记下 %d 张卡片的位置", n)
	}
	if got, want := a.welSlot(0, 1280, 800), a.wel.slots[0]; got != want {
		t.Fatalf("第一张落在 %v，主界面第一张卡片在 %v", got, want)
	}
	a.wel.slots = a.wel.slots[:2]
	last, next := a.wel.slots[1], a.welSlot(2, 1280, 800)
	if next.Y < last.Y || (next.Y == last.Y && next.X <= last.X) {
		t.Fatalf("真实卡片不够时第三张落在 %v，没有接在最后一张 %v 后面", next, last)
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

// 重复的几份合成一张后，其余几份叠在留下那张背后，淡一半、不消失；收纳时
// 跟着它飞进同一格，边飞边淡去。
func TestWelcomeMergedCopiesStayBehindTheKeeper(t *testing.T) {
	a := newApp(library.Sample())
	welcomeAt(a, welReady+100)
	g := newWelGeom(1280, 800)
	copies := welCopies(0)
	if len(copies) < 2 {
		t.Fatalf("第一组重复只有 %d 份", len(copies))
	}
	keep, _ := a.welCardPose(g, copies[0], welReady+100, -1, 1280, 800)
	for _, n := range copies[1:] {
		p, _ := a.welCardPose(g, n, welReady+100, -1, 1280, 800)
		if p.Fade <= 0 || p.Fade >= 1 {
			t.Errorf("合掉的一份淡去了 %v，应该半透明地叠在背后", p.Fade)
		}
		if dx, dy := p.X-keep.X, p.Y-keep.Y; dx*dx+dy*dy > 30*30 {
			t.Errorf("合掉的一份在 (%v,%v)，离留下那张 (%v,%v) 太远", p.X, p.Y, keep.X, keep.Y)
		}
	}
	landed, _ := a.welCardPose(g, copies[0], welReady+300, 200, 1280, 800)
	for _, n := range copies[1:] {
		p, _ := a.welCardPose(g, n, welReady+300, 200, 1280, 800)
		if p.X != landed.X || p.Y != landed.Y || p.Fade < 1 {
			t.Errorf("收纳时合掉的一份去了 (%v,%v) 淡去 %v，应该跟着留下那张进 (%v,%v) 并淡去",
				p.X, p.Y, p.Fade, landed.X, landed.Y)
		}
	}
}

// 堆最上面那张飞去合并后，下面那张补上来，摆成最上面那张的样子。
func TestWelcomePileClosesUpWhenTopCardLeaves(t *testing.T) {
	a := newApp(library.Sample())
	g := newWelGeom(1280, 800)
	under := slices.IndexFunc(welCast, func(c welCard) bool { return c.name == "vue-best-practices" })
	top := slices.IndexFunc(welCast, func(c welCard) bool { return c.pile == welCast[under].pile && c.i == 0 })
	at := welMerges[welCast[top].merge].at
	before, _ := a.welCardPose(g, under, at-1, -1, 1280, 800)
	after, _ := a.welCardPose(g, under, at, -1, 1280, 800)
	pile := welPiles[welCast[under].pile]
	want := g.card(welPose{x: pile.x, y: pile.y, r: pile.tilt, s: pile.z})
	if before == want {
		t.Fatal("上面那张还在时，下面那张就摆在最上面了")
	}
	if after != want {
		t.Fatalf("上面那张飞走后，下面那张在 %+v，应该补到 %+v", after, want)
	}
}
