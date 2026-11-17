package main

import (
	"testing"

	"github.com/egoist/mygo/ui"

	"skillmanager/internal/library"
)

func TestSettingsLayoutUsesLeftSidebarAndRightContent(t *testing.T) {
	a := newApp(library.Sample())
	a.set.open = true
	st := newUITester(a.settingsView, 760, 540)
	img := st.Image()

	// 4 个导航按钮竖排在左侧栏，同宽左对齐且都在左 192px 内；图标和文字靠左对齐
	navNames := []string{"通用", "Agent 管理", "项目", "技能更新"}
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

			btn, ok := st.Find(tc.button)
			if !ok {
				t.Fatalf("找不到 %q", tc.button)
			}
			midY := int(btn.Y + btn.H/2)
			// 汇总栏内部底色应区别于白色卡片底，且卡片左内边框把汇总栏与外部页面底隔开
			rowBg := img.RGBAAt(int(btn.X-24), midY)
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
