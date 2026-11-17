package main

import (
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"skillmanager/internal/library"
)

// chimeTester 是记着响了几声的应用：测试里不真的放声音。时钟停在一处，
// 测试自己往前拨（*now）。
func chimeTester(t *testing.T) (a *app, chimes *int, now *time.Time) {
	t.Helper()
	a, _ = newTester(t)
	n, clock := 0, time.Date(2026, 11, 16, 9, 0, 0, 0, time.Local)
	a.sound = func() { n++ }
	a.clock = func() time.Time { return clock }
	return a, &n, &clock
}

// tickingClock 让每次看时间都过去两秒：一秒一声的限制不会挡住任何一声。
func tickingClock(a *app, now *time.Time) {
	a.clock = func() time.Time { *now = now.Add(2 * time.Second); return *now }
}

func TestMergingCopiesChimesOnce(t *testing.T) {
	// Given 整理页有内容相同的副本
	a, chimes, _ := chimeTester(t)
	p := a.lib.Problems()
	if len(p.Same) == 0 {
		t.Fatal("示例数据应有内容相同的副本")
	}

	// When 全部收成一份
	a.mergeAll(p.Same)

	// Then 响一声
	if *chimes != 1 {
		t.Errorf("应响 1 声，实际 %d", *chimes)
	}
}

func TestClearingSeveralRecordsChimesOnceForTheBatch(t *testing.T) {
	// Given 不止一个来源有已删除技能的安装记录；清的过程跨过好几秒
	a, chimes, now := chimeTester(t)
	tickingClock(a, now)
	stale := a.lib.Problems().Stale
	if len(stale) < 2 {
		t.Fatalf("示例数据应有至少两个来源，实际 %d", len(stale))
	}
	early := 0
	a.sound = func() {
		*chimes++
		if a.tidy.clearing != nil {
			early++
		}
	}

	// When 全部清除
	a.clearRecords(stale, stale...)

	// Then 整批清完才响，只响一声
	if *chimes != 1 || early != 0 {
		t.Errorf("应在清完后响 1 声，实际 %d 声，其中 %d 声在清完前", *chimes, early)
	}
}

func TestSingleTidyActionChimesWhenItSucceeds(t *testing.T) {
	// Given 有重复的链接
	a, chimes, _ := chimeTester(t)
	e := a.lib.Problems().Extra[0]

	// When 删掉它
	a.tidyRun(func() error { return a.lib.DropLink(e.Skill, e.Agent) })

	// Then 响一声
	if *chimes != 1 {
		t.Errorf("应响 1 声，实际 %d", *chimes)
	}
}

func TestFailedTidyActionStaysSilent(t *testing.T) {
	// Given 技能库里没有这个名字
	a, chimes, _ := chimeTester(t)

	// When 把断掉的链接重新连上
	a.tidyRun(func() error { return a.lib.Relink("~/.claude/skills/no-such-skill") })

	// Then 显示原因，不响
	if a.tidy.err == "" {
		t.Fatal("应显示失败原因")
	}
	if *chimes != 0 {
		t.Errorf("失败不应响，实际 %d 声", *chimes)
	}
}

func TestBackToBackActionsChimeOnlyOnce(t *testing.T) {
	// Given 有重复的链接和项目技能被全局链接
	a, chimes, _ := chimeTester(t)
	p := a.lib.Problems()
	e, g := p.Extra[0], p.Global[0]

	// When 一秒内接连处理两件
	a.tidyRun(func() error { return a.lib.DropLink(e.Skill, e.Agent) })
	a.tidyRun(func() error { return a.lib.Unglobal(g) })

	// Then 只响一声，不叠在一起
	if *chimes != 1 {
		t.Errorf("应响 1 声，实际 %d", *chimes)
	}
}

func TestChimeCanBeTurnedOff(t *testing.T) {
	// Given 设置里关掉了提示音
	a, chimes, _ := chimeTester(t)
	a.lib.Settings.QuietTidy = true
	e := a.lib.Problems().Extra[0]

	// When 处理一项
	a.tidyRun(func() error { return a.lib.DropLink(e.Skill, e.Agent) })

	// Then 不响
	if *chimes != 0 {
		t.Errorf("关掉后不应响，实际 %d 声", *chimes)
	}
}

func TestMergingFromCompareChimes(t *testing.T) {
	// Given 内容不同的副本，在对比页里选了一份
	a, chimes, _ := chimeTester(t)
	s := a.lib.Problems().Diff[0]

	// When 保留这个版本
	a.merge(s, 0, false)

	// Then 响一声
	if *chimes != 1 {
		t.Errorf("应响 1 声，实际 %d", *chimes)
	}
}

func TestUserTurnsOffTheChimeInSettings(t *testing.T) {
	// Given 在设置里，提示音默认开着
	a, chimes, _ := chimeTester(t)
	st := newUITester(a.settingsView, settingsWidth, settingsHeight)
	if a.lib.Settings.QuietTidy {
		t.Fatal("提示音默认应开着")
	}

	// When 关掉「完成整理时播放提示音」，再处理一项
	st.Frame()
	sw, ok := st.AccessByRole(ui.RoleSwitch, "完成整理时播放提示音")
	if !ok || !sw.Checked {
		t.Fatalf("应有开着的提示音开关：%+v", sw)
	}
	st.ClickAt(sw.Bounds.X+sw.Bounds.W/2, sw.Bounds.Y+sw.Bounds.H/2)
	e := a.lib.Problems().Extra[0]
	a.tidyRun(func() error { return a.lib.DropLink(e.Skill, e.Agent) })

	// Then 记下关掉了，处理完不响
	if !a.lib.Settings.QuietTidy || *chimes != 0 {
		t.Errorf("应关掉提示音：quiet=%v，响了 %d 声", a.lib.Settings.QuietTidy, *chimes)
	}
}

func TestBatchWithAFailureStaysSilent(t *testing.T) {
	// Given 两个技能要收成一份，其中一个已经收不了（变成只读）
	a, chimes, now := chimeTester(t)
	tickingClock(a, now)
	p := a.lib.Problems()
	ok, stuck := p.Same[0], p.Diff[0]
	stuck.Source = library.Builtin

	// When 一起收成一份
	a.mergeAll([]*library.Skill{ok, stuck})

	// Then 写出失败原因，不响
	if a.tidy.err == "" {
		t.Fatal("应显示失败原因")
	}
	if *chimes != 0 {
		t.Errorf("有失败时不应响，实际 %d 声", *chimes)
	}
}

func TestChimesAgainOnceASecondHasPassed(t *testing.T) {
	// Given 刚处理完一项，响过一声
	a, chimes, now := chimeTester(t)
	p := a.lib.Problems()
	e, g := p.Extra[0], p.Global[0]
	a.tidyRun(func() error { return a.lib.DropLink(e.Skill, e.Agent) })

	// When 过了一秒多再处理一项
	*now = now.Add(1100 * time.Millisecond)
	a.tidyRun(func() error { return a.lib.Unglobal(g) })

	// Then 又响一声
	if *chimes != 2 {
		t.Errorf("应响 2 声，实际 %d", *chimes)
	}
}
