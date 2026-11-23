package main

import (
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"skillmanager/internal/library"
)

func TestSettingsLayoutUsesLeftSidebarAndRightContent(t *testing.T) {
	a := newApp(library.Sample())
	a.set.open = true
	st := newUITester(a.settingsView, 760, 540)
	img := st.Image()

	// 4 个导航按钮竖排在左侧栏，同宽左对齐且都在左 192px 内；图标和文字靠左对齐
	navNames := []string{"通用", "Agent 管理", "项目", "更新 Skill"}
	var prevY float32 = -1
	var maxNavRight float32
	for _, name := range navNames {
		btn, ok := st.AccessByRole(ui.RoleButton, name)
		if !ok {
			t.Fatalf("找不到设置导航按钮 %q", name)
		}
		if btn.Bounds.Y <= prevY {
			t.Fatalf("导航项 %q (%v) 应排在上一项下方 (prevY=%.1f)", name, btn.Bounds, prevY)
		}
		if btn.Bounds.X+btn.Bounds.W > 192 {
			t.Fatalf("导航项 %q 超出 192px 左侧栏：%v", name, btn.Bounds)
		}
		// 按钮左内边距后（x+10..x+26）应直接画出 16px 图标，而不是被居中推到中间
		bg := img.RGBAAt(int(btn.Bounds.X+4), int(btn.Bounds.Y+btn.Bounds.H/2))
		hasIconPixel := false
		for y := int(btn.Bounds.Y + 8); y < int(btn.Bounds.Y+btn.Bounds.H-8); y++ {
			for x := int(btn.Bounds.X + 10); x < int(btn.Bounds.X+26); x++ {
				if img.RGBAAt(x, y) != bg {
					hasIconPixel = true
					break
				}
			}
			if hasIconPixel {
				break
			}
		}
		if !hasIconPixel {
			t.Fatalf("导航项 %q 图标应左对齐落在按钮左侧内边距旁，而不是居中 (%v)", name, btn.Bounds)
		}
		prevY = btn.Bounds.Y
		maxNavRight = max(maxNavRight, btn.Bounds.X+btn.Bounds.W)
	}

	// 右侧内容位于侧栏右边，且顶部不再被 86px 顶栏往下推
	libLabel, ok := st.Find("技能库")
	if !ok {
		t.Fatal("找不到「技能库」分组标题")
	}
	if libLabel.X < maxNavRight {
		t.Fatalf("内容区应位于左侧栏右侧：label.X=%.1f, maxNavRight=%.1f", libLabel.X, maxNavRight)
	}
	if libLabel.Y >= 60 {
		t.Fatalf("无顶栏融底布局下「技能库」应贴近顶部起笔，当前 Y=%.1f", libLabel.Y)
	}
}

func TestSettingsLayoutHasSeamlessUnifiedCanvasInLightAndDark(t *testing.T) {
	defer usePalette(false)
	a := newApp(library.Sample())
	a.set.open = true

	checkCanvas := func(dark bool) {
		t.Helper()
		st := ui.NewTesterWith(a.settingsView, 760, 540, ui.TesterOptions{Dark: dark})
		img := st.Image()
		pageTone := toneOf(t, &colBgPage)
		want := cssColor(pageTone.light)
		if dark {
			want = cssColor(pageTone.dark)
		}
		// 左右分界处（x=192, y=24）与内容区上方空白（x=240, y=10）都应是统一的 colBgPage，没有竖向分割线或顶栏底色
		for _, pt := range [][2]int{{191, 24}, {192, 24}, {240, 10}} {
			px := img.RGBAAt(pt[0], pt[1])
			got := ui.Color{R: px.R, G: px.G, B: px.B, A: px.A}
			if got != want {
				t.Fatalf("dark=%v 坐标 (%d,%d) 应为统一底色 %v，实际 %v", dark, pt[0], pt[1], want, got)
			}
		}
	}

	checkCanvas(false)
	checkCanvas(true)
}

func TestSettingsLayoutScrollsProjectsAndClearsNonMacTitleBar(t *testing.T) {
	a := newApp(library.Sample())
	a.set.open = true
	a.set.tab = tabProjects

	// Windows/Linux 隐藏标题栏时，右上角窗口按钮占 Height=36、Right=138，内容应让出顶部高度
	st := newUITester(a.settingsView, 760, 540)
	st.SetTitleBar(ui.TitleBar{Height: 36, Right: 138})
	st.Frame()
	autoLabel, ok := st.Find("自动发现")
	if !ok {
		t.Fatal("找不到「自动发现」")
	}
	if autoLabel.Y < 36 {
		t.Fatalf("非 macOS 下顶部内容未让出标题栏控件高度 36px：Y=%.1f", autoLabel.Y)
	}

	// 重置为 macOS（无右侧标题栏占位）并在 540px 定高窗口里滚动到底部展开项目目录
	st.SetTitleBar(ui.TitleBar{})
	st.Frame()
	st.Scroll(480, 300, 0, 280)
	if err := st.Click("还有 20 个…"); err != nil {
		t.Fatalf("滚动后应能点击底部的「还有 20 个…」：%v", err)
	}
	if !st.HasText(".agent/skills/") {
		t.Fatal("展开后应显示全部目录")
	}
}

func TestSettingsLayoutSumRowStandsOutFromPageAndCard(t *testing.T) {
	defer usePalette(false)
	for _, tc := range []struct {
		name   string
		tab    int
		button string
	}{
		{name: "agents", tab: tabAgents, button: "重新检测"},
		{name: "projects", tab: tabProjects, button: "重新检查"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := newApp(library.Sample())
			a.set.open = true
			a.set.tab = tc.tab
			st := ui.NewTesterWith(a.settingsView, 760, 540, ui.TesterOptions{Dark: false})
			img := st.Image()

			btn, ok := st.AccessByRole(ui.RoleButton, tc.button)
			if !ok {
				t.Fatalf("找不到 %q", tc.button)
			}
			midY := int(btn.Bounds.Y + btn.Bounds.H/2)
			// 汇总栏内部底色应区别于白色卡片底，且卡片左内边框把汇总栏与外部页面底隔开
			rowBg := img.RGBAAt(int(btn.Bounds.X-12), midY)
			cardEdge := img.RGBAAt(210, midY) // 左侧栏 192 + 右侧内边距 14 = 卡片左缘 206，210 落在卡片内缘留白上
			cardWant := cssColor(toneOf(t, &colBgCard).light)
			gotEdge := ui.Color{R: cardEdge.R, G: cardEdge.G, B: cardEdge.B, A: cardEdge.A}
			if gotEdge != cardWant {
				t.Fatalf("汇总栏外缘 (%d,%d) 应被卡片白底 %v 托住，避免融进页面底色；实际 %v", 210, midY, cardWant, gotEdge)
			}
			gotRow := ui.Color{R: rowBg.R, G: rowBg.G, B: rowBg.B, A: rowBg.A}
			if gotRow == cardWant {
				t.Fatalf("汇总栏内部底色不应与卡片白底 %v 完全相同：%v", cardWant, gotRow)
			}
		})
	}
}

func TestSettingsRescanShowsSpinnerDoneFeedbackAndRecovers(t *testing.T) {
	defer usePalette(false)

	t.Run("sample_projects_progress_done_and_guard", func(t *testing.T) {
		// Given 示例模式下上一次检查显示为 3 分钟前，后台与延时回调由测试接管
		a := newApp(library.Sample())
		a.set.open = true
		a.set.tab = tabProjects
		a.set.scannedAt = time.Now().Add(-3 * time.Minute)
		var works []func(post func(func()))
		a.bg = func(w func(post func(func()))) { works = append(works, w) }
		var resets []func()
		a.delay = func(_ time.Duration, fn func()) { resets = append(resets, fn) }

		st := newUITester(a.settingsView, settingsWidth, settingsHeight)
		if !st.HasText("3 分钟前检查") {
			t.Fatalf("初始应显示「3 分钟前检查」：%q", st.Texts())
		}

		// When 点「重新检查」
		must(t, st.Click("重新检查"))
		st.Frame()

		// Then 按钮进入进行中状态（禁用、写着正在检查…），再点一次不重复起后台扫描
		if len(works) != 1 {
			t.Fatalf("应发起 1 次后台检查，实际 %d 次", len(works))
		}
		info, ok := st.Access("正在检查…")
		if !ok || !info.Disabled {
			t.Fatalf("检查中按钮应显示「正在检查…」且不可点击：%q", st.Texts())
		}
		_ = st.Click("正在检查…")
		if len(works) != 1 {
			t.Fatalf("检查中连续点击不应发起第 2 次扫描：%d", len(works))
		}

		// When 后台检查完成落地
		works[0](func(fn func()) { fn() })
		st.Frame()

		// Then 时间刷新为「刚刚检查」，按钮短暂变成绿色「已检查」确认态
		if !st.HasText("刚刚检查") {
			t.Fatalf("示例模式重新检查完成后也应刷新时间：%q", st.Texts())
		}
		doneBtn, ok := st.Access("已检查")
		if !ok {
			t.Fatalf("检查完成后应短暂显示「已检查」确认态：%q", st.Texts())
		}
		st.Advance(300 * time.Millisecond)
		st.Frame()
		doneBtn, _ = st.Access("已检查")
		img := st.Image()
		px := img.RGBAAt(int(doneBtn.Bounds.X+4), int(doneBtn.Bounds.Y+doneBtn.Bounds.H/2))
		wantOK := cssColor(toneOf(t, &colOKBg).light)
		if (ui.Color{R: px.R, G: px.G, B: px.B, A: px.A}) != wantOK {
			t.Fatalf("「已检查」按钮底色应为 %v，实际 %+v", wantOK, px)
		}
		if len(resets) != 1 {
			t.Fatalf("应注册 1 次完成态回收定时，实际 %d", len(resets))
		}

		// 验证陈旧定时器不会误清后续新一轮扫描；再完成回收到初始按钮
		must(t, st.Click("已检查"))
		st.Frame()
		if !st.HasText("正在检查…") {
			t.Fatalf("确认态再次点击应进入下一轮检查：%q", st.Texts())
		}
		resets[0]() // 旧周期的回收回调不应干扰新一轮
		st.Frame()
		if !st.HasText("正在检查…") {
			t.Fatalf("旧定时回调不应打断新一轮扫描中状态：%q", st.Texts())
		}
		works[1](func(fn func()) { fn() })
		st.Frame()
		if len(resets) != 2 || !st.HasText("已检查") {
			t.Fatalf("第二轮结束后应显示「已检查」：%q", st.Texts())
		}
		resets[1]()
		st.Frame()
		if !st.HasText("重新检查") {
			t.Fatalf("停留结束后按钮应恢复「重新检查」：%q", st.Texts())
		}
	})

	t.Run("real_agents_detects_new_skill_and_cross_tab_isolation", func(t *testing.T) {
		// Given 真实临时目录里初始 0 个技能
		a, _, h := importApp(t)
		a.set.open = true
		a.set.tab = tabAgents
		var work func(post func(func()))
		a.bg = func(w func(post func(func()))) { work = w }
		var reset func()
		a.delay = func(_ time.Duration, fn func()) { reset = fn }

		st := newUITester(a.settingsView, settingsWidth, settingsHeight)
		must(t, st.Click("重新检测"))
		st.Frame()
		if info, ok := st.Access("正在检测…"); !ok || !info.Disabled {
			t.Fatalf("Agent 管理页检测中应显示禁用的「正在检测…」：%q", st.Texts())
		}

		// When 检测过程中磁盘新增了技能
		writeTestSkill(t, h(".claude/skills/alpha"), "alpha", "body")
		work(func(fn func()) { fn() })
		st.Frame()

		// Then 扫出新技能，当前页显示「已检测」，切到项目页不会误显「已检查」
		if !st.HasText("1 个技能") || !st.HasText("已检测") {
			t.Fatalf("完成后应刷新技能计数并显示「已检测」：%q", st.Texts())
		}
		must(t, st.Click("项目"))
		st.Frame()
		if !st.HasText("重新检查") || st.HasText("已检查") {
			t.Fatalf("在 Agent 页完成检测，切到项目页应保持「重新检查」：%q", st.Texts())
		}
		reset()
		st.Frame()
		must(t, st.Click("Agent 管理"))
		st.Frame()
		if !st.HasText("重新检测") {
			t.Fatalf("定时器到期后应恢复「重新检测」：%q", st.Texts())
		}
	})
}

func TestSettingsRescanRespectsNewerWatchAndDefersDuringInteractions(t *testing.T) {
	t.Run("does_not_overwrite_newer_watch_scan", func(t *testing.T) {
		// Given 设置扫描在途期间，文件监听已经应用了包含新技能 beta 的结果
		a, _, h := importApp(t)
		writeTestSkill(t, h(".claude/skills/alpha"), "alpha", "body")
		a.rescan()
		var finish func()
		a.bg = func(w func(post func(func()))) {
			w(func(fn func()) { finish = fn })
		}
		a.delay = func(_ time.Duration, fn func()) {}

		st := newUITester(a.settingsView, settingsWidth, settingsHeight)
		a.set.tab = tabAgents
		st.Frame()
		must(t, st.Click("重新检测"))

		writeTestSkill(t, h(".claude/skills/beta"), "beta", "body")
		fresh := library.Scan(a.lib.Home)
		a.applyWatchScan(fresh, fresh.Updates(), a.watch.scanGen.Load())
		if a.lib.Get("beta") == nil {
			t.Fatal("前置条件：watch 扫描应已发现 beta")
		}

		// When 旧的设置扫描回调后落地
		finish()

		// Then 不得回退覆盖掉 watch 新发现的 beta，且按钮完成确认态照常展示
		if a.lib.Get("beta") == nil {
			t.Fatal("较早发起的设置扫描不应覆盖后落地的 watch 扫描结果")
		}
		st.Frame()
		if !st.HasText("已检测") {
			t.Fatalf("被 watch 超越后设置按钮仍应进入「已检测」：%q", st.Texts())
		}
	})

	t.Run("defers_apply_when_protected_interaction_begins_mid_scan", func(t *testing.T) {
		// Given 点击重新检测后，用户在扫描结束前打开了副本对比
		a, tt, h := importApp(t)
		writeTestSkill(t, h(".claude/skills/alpha"), "alpha", "body")
		a.rescan()
		var work func(post func(func()))
		a.bg = func(w func(post func(func()))) { work = w }
		a.delay = func(_ time.Duration, fn func()) {}

		st := newUITester(a.settingsView, settingsWidth, settingsHeight)
		a.set.tab = tabAgents
		st.Frame()
		must(t, st.Click("重新检测"))

		writeTestSkill(t, h(".claude/skills/gamma"), "gamma", "body")
		a.cmp = &compareState{}

		// When 后台扫描完成回到主线程
		work(func(fn func()) { fn() })

		// Then 正在进行的对比交互不得被打断，结果挂起直到交互结束再应用
		if a.cmp == nil {
			t.Fatal("异步设置扫描完成时不应关闭扫描期间新打开的对比视图")
		}
		if a.watch.pending == nil {
			t.Fatal("受保护交互期间完成的扫描应暂存到 pending")
		}

		// When 用户关掉对比并刷新界面
		a.cmp = nil
		tt.Frame()

		// Then 挂起的扫描结果自动应用，新技能 gamma 生效
		if a.watch.pending != nil {
			t.Fatal("交互结束后挂起扫描应被冲刷并清空")
		}
		if a.lib.Get("gamma") == nil {
			t.Fatal("延后应用的设置扫描结果应包含扫描期间写入的 gamma")
		}
	})

	t.Run("rejects_older_watch_scan_after_deferring_settings_rescan", func(t *testing.T) {
		// Given 一个老的 watch 扫描早在 baseGen 时就已经出发；期间设置扫描扫到了新技能 gamma 并因打开对比而暂存 pending
		a, tt, h := importApp(t)
		writeTestSkill(t, h(".claude/skills/alpha"), "alpha", "body")
		a.rescan()
		olderLib, olderRep := library.Scan(a.lib.Home), library.UpdateReport{}
		olderGen := a.watch.scanGen.Load()

		writeTestSkill(t, h(".claude/skills/gamma"), "gamma", "body")
		var work func(post func(func()))
		a.bg = func(w func(post func(func()))) { work = w }
		a.delay = func(_ time.Duration, fn func()) {}

		st := newUITester(a.settingsView, settingsWidth, settingsHeight)
		a.set.tab = tabAgents
		st.Frame()
		must(t, st.Click("重新检测"))
		a.cmp = &compareState{}
		work(func(fn func()) { fn() })

		// When 早先那轮不含 gamma 的 watch 扫描此时才回到主线程
		a.applyWatchScan(olderLib, olderRep, olderGen)

		// Then 较老的 watch 扫描不得覆写挂起队列里较新的设置扫描结果
		if a.watch.pending == nil || a.watch.pending.lib.Get("gamma") == nil {
			t.Fatal("较早出发的 watch 扫描不应覆盖挂起队列里较新的设置扫描结果")
		}
		a.cmp = nil
		tt.Frame()
		if a.lib.Get("gamma") == nil {
			t.Fatal("交互结束后应保留并应用包含 gamma 的设置扫描结果")
		}
	})

	t.Run("preserves_usage_refresh_when_watch_scan_supersedes_deferred_settings_rescan", func(t *testing.T) {
		// Given 对比打开期间，设置扫描在途，一轮仅扫技能的 watch 扫描先一步进入 pending（reloadUsage=false）
		a, tt, h := importApp(t)
		writeTestSkill(t, h(".claude/skills/alpha"), "alpha", "body")
		a.rescan()
		var finish func()
		a.bg = func(w func(post func(func()))) {
			w(func(fn func()) { finish = fn })
		}
		a.delay = func(_ time.Duration, fn func()) {}

		st := newUITester(a.settingsView, settingsWidth, settingsHeight)
		a.set.tab = tabAgents
		st.Frame()
		must(t, st.Click("重新检测"))

		a.cmp = &compareState{}
		writeTestSkill(t, h(".claude/skills/beta"), "beta", "body")
		fresh := library.Scan(a.lib.Home)
		a.applyWatchScan(fresh, fresh.Updates(), a.watch.scanGen.Load())

		// When 设置扫描后落地但因为已被 watch 超越而让位
		finish()

		// Then 挂起的替换扫描必须继承手动重检的 reloadUsage 诉求，待交互结束一同刷新调用统计与库
		if a.watch.pending == nil || !a.watch.pending.reloadUsage {
			t.Fatalf("被 watch 超越让位后，挂起队列中的替换扫描应保留 reloadUsage=true：%#v", a.watch.pending)
		}
		a.cmp = nil
		tt.Frame()
		if a.lib.Get("beta") == nil {
			t.Fatal("冲刷后应应用较新的 beta 扫描结果")
		}
	})

	t.Run("preserves_pending_usage_refresh_when_immediate_watch_applies_first", func(t *testing.T) {
		// Given 设置扫描因打开对比而挂起（reloadUsage=true），随后对比关闭，但在下一帧冲刷前新的一轮 watch 立即落地
		a, _, h := importApp(t)
		refreshes := 0
		a.readUsage = func() { refreshes++ }
		writeTestSkill(t, h(".claude/skills/alpha"), "alpha", "body")
		a.rescan()
		refreshes = 0

		var work func(post func(func()))
		a.bg = func(w func(post func(func()))) { work = w }
		a.delay = func(_ time.Duration, fn func()) {}

		st := newUITester(a.settingsView, settingsWidth, settingsHeight)
		a.set.tab = tabAgents
		st.Frame()
		must(t, st.Click("重新检测"))

		a.cmp = &compareState{}
		work(func(fn func()) { fn() })
		if a.watch.pending == nil || !a.watch.pending.reloadUsage {
			t.Fatalf("前置条件：设置扫描应已挂起且带 reloadUsage=true：%#v", a.watch.pending)
		}

		// 交互结束但未先切回绘图冲刷前，新一轮 watch 扫描带着新技能 delta 直接落地
		a.cmp = nil
		writeTestSkill(t, h(".claude/skills/delta"), "delta", "body")
		fresh := library.Scan(a.lib.Home)
		a.applyWatchScan(fresh, fresh.Updates(), a.watch.scanGen.Load())

		if a.lib.Get("delta") == nil {
			t.Fatal("watch 结果 delta 应已立即应用")
		}
		if a.watch.pending != nil {
			t.Fatal("立即应用后不应残留旧 pending")
		}
		if refreshes == 0 {
			t.Fatal("watch 直接取代挂起设置扫描时不应丢失 reloadUsage 调用统计刷新")
		}
	})

	t.Run("next_serial_watch_scan_still_applies_after_one_that_reloads_usage", func(t *testing.T) {
		// Given 设置扫描因打开对比而挂起（reloadUsage=true），对比随后关闭
		a, _, h := importApp(t)
		writeTestSkill(t, h(".claude/skills/alpha"), "alpha", "body")
		a.rescan()
		var work func(post func(func()))
		a.bg = func(w func(post func(func()))) { work = w }
		a.delay = func(_ time.Duration, fn func()) {}

		st := newUITester(a.settingsView, settingsWidth, settingsHeight)
		a.set.tab = tabAgents
		st.Frame()
		must(t, st.Click("重新检测"))
		a.cmp = &compareState{}
		work(func(fn func()) { fn() })
		a.cmp = nil

		// 监听是一轮接一轮扫的：下一轮在上一轮结果落地前就记下了 baseGen
		baseGen := a.watch.scanGen.Load()
		writeTestSkill(t, h(".claude/skills/delta"), "delta", "body")
		first := library.Scan(a.lib.Home)
		writeTestSkill(t, h(".claude/skills/epsilon"), "epsilon", "body")
		second := library.Scan(a.lib.Home)

		// When 两轮监听结果依次落地，第一轮接手了挂起的 reloadUsage
		a.applyWatchScan(first, first.Updates(), baseGen)
		a.applyWatchScan(second, second.Updates(), baseGen)

		// Then 第二轮不应被当成过期丢掉
		if a.lib.Get("epsilon") == nil {
			t.Fatal("带 reloadUsage 的监听结果落地后，同批次的下一轮监听结果应照常应用")
		}
	})
}
