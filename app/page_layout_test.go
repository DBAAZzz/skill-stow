package main

import (
	"fmt"
	"image"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"skillmanager/internal/library"
	"skillmanager/locales"
)

// cardEdges 找内容区第一张卡片：从工具栏下面沿内容区中线往下，第一个卡片底色
// 的点是卡片顶边；在顶边往下几行横扫，第一个和最后一个卡片底色的点是左右边缘。
// 不靠页面底色找起点：深色下页面底和工具栏同色。
func cardEdges(t *testing.T, img *image.RGBA, left int) (x0, x1, y0 int) {
	t.Helper()
	b := img.Bounds()
	card := colBgCard
	isCard := func(x, y int) bool {
		p := img.RGBAAt(x, y)
		d := func(a, b uint8) int { return max(int(a)-int(b), int(b)-int(a)) }
		return d(p.R, card.R) <= 1 && d(p.G, card.G) <= 1 && d(p.B, card.B) <= 1
	}
	mid := (left + b.Max.X) / 2
	y := titleBarHeight + 1
	for y < b.Max.Y && !isCard(mid, y) {
		y++
	}
	if y == b.Max.Y {
		t.Fatal("内容区里找不到卡片")
	}
	row := y + 6
	x0, x1 = -1, -1
	for x := left; x < b.Max.X; x++ {
		if isCard(x, row) {
			if x0 < 0 {
				x0 = x
			}
			x1 = x
		}
	}
	if x0 < 0 || x1-x0 < 100 {
		t.Fatalf("y=%d 处找到的不像卡片：x=%d…%d", row, x0, x1)
	}
	return x0, x1, y
}

func TestGroupedPagesShareOneContentColumn(t *testing.T) {
	for _, w := range []int{1280, 1000, 760} {
		t.Run(fmt.Sprint(w), func(t *testing.T) {
			usage := newApp(library.Sample())
			usage.scope = scopeUsage
			upd := newApp(library.Sample())
			updateFixture(upd)
			imp := newApp(library.Sample())
			imp.scope = scopeImport

			left := int(usage.sidebarRoom())
			shot := func(a *app) *image.RGBA {
				tt := newUITester(a.view, w, 800)
				tt.Frame()
				return tt.Image()
			}
			ux0, ux1, uy := cardEdges(t, shot(usage), left)
			px0, px1, py := cardEdges(t, shot(upd), left)
			// 技能更新的卡片描边画在框内、使用情况的细线画在框外，白色起点差 1 像素以内算重合
			near := func(a, b int) bool { return a-b <= 1 && b-a <= 1 }
			if !near(ux0, px0) || !near(ux1, px1) || !near(uy, py) {
				t.Errorf("使用情况与技能更新的内容栏应完全重合：使用情况 x=%d…%d y=%d，技能更新 x=%d…%d y=%d",
					ux0, ux1, uy, px0, px1, py)
			}

			ix0, ix1, _ := cardEdges(t, shot(imp), left)
			if ix1-ix0 > ux1-ux0 {
				t.Errorf("添加技能是表单页，内容栏不应比使用情况宽：%d > %d", ix1-ix0, ux1-ux0)
			}
			if d := (ix0 + ix1) - (ux0 + ux1); d < -1 || d > 1 {
				t.Errorf("添加技能应与其它页居中在同一条轴上：添加 %d…%d，使用情况 %d…%d", ix0, ix1, ux0, ux1)
			}
		})
	}
}

// 详情栏收起的动画途中，内容区还没放宽：内容栏要跟着当时的宽度，不能先按
// 终点宽度撑开、在还窄的区域里居中，把左边裁掉。
func TestGroupedPagesKeepCardsInsideWhileInspectorCloses(t *testing.T) {
	for name, setup := range map[string]func(a *app){
		"usage":   func(a *app) { a.scope = scopeUsage },
		"updates": func(a *app) { updateFixture(a) },
	} {
		t.Run(name, func(t *testing.T) {
			a := newApp(library.Sample())
			setup(a)
			a.selected = a.lib.Get("vue-best-practices")
			tt := newUITester(a.view, 960, 800)
			tt.Advance(paneDuration)

			a.selected = nil
			tt.Frame()
			tt.Advance(100 * time.Millisecond)

			left := int(a.sidebarRoom())
			x0, _, _ := cardEdges(t, tt.Image(), left)
			if x0 < left+pagePadNarrow-1 {
				t.Errorf("收起详情栏途中卡片左边被裁：卡片从 x=%d 起，内容区从 x=%d 起", x0, left)
			}
		})
	}
}

// 主窗口最窄（960）又开着详情栏时，内容栏只剩三百来宽：每行该有的都要在
// 卡片里，不能被挤出右边、也不能叠在按钮上。
func TestGroupedPagesFitNarrowestWindowWithInspector(t *testing.T) {
	const w = 960
	paneLeft := float32(w - inspectorWidth - 1)
	inside := func(t *testing.T, tt *ui.Tester, text string) ui.Rect {
		t.Helper()
		r, ok := tt.Find(text)
		if !ok {
			t.Fatalf("找不到 %q", text)
		}
		if r.X+r.W > paneLeft-pagePadNarrow {
			t.Errorf("%q 被挤出内容栏：右边到 %.0f，内容栏右边是 %.0f", text, r.X+r.W, paneLeft-pagePadNarrow)
		}
		return r
	}

	t.Run("usage", func(t *testing.T) {
		render := func(width int, pane bool) (*app, *ui.Tester) {
			a := newApp(library.Sample())
			a.scope = scopeUsage
			if pane {
				a.selected = a.lib.Get("vue-best-practices")
			}
			tt := newUITester(a.view, width, 1400)
			tt.Advance(paneDuration)
			return a, tt
		}
		_, wide := render(1280, false)
		a, tt := render(w, true)
		// 样例的调用记录按今天往前排，日期类的字从数据算，不写死
		now := time.Now()
		st := dailyStats(a.lib.Daily(now, usageWeeks))
		peakDate := monthDay(st.peakDay, st.peakDay)
		lastUsed := locales.DaysAgoShort(daysAgo(a.lib.Usage(a.lib.Used()[0]).Last, now))
		// 图例、概览、行尾的字不能被截成省略号：和宽窗口下一样宽
		for _, text := range []string{"10 次以上", "没用过", peakDate, lastUsed} {
			full, ok := wide.Find(text)
			if !ok {
				t.Fatalf("宽窗口里找不到 %q", text)
			}
			if r := inside(t, tt, text); r.W < full.W-0.5 {
				t.Errorf("%q 被截断：宽 %.1f，完整应为 %.1f", text, r.W, full.W)
			}
		}
		// 「最多的一天」日期换了行，三格的标签仍在同一高度
		avg, _ := tt.Find("日均")
		if peak, _ := tt.Find("最多的一天"); peak.Y != avg.Y {
			t.Errorf("概览三格的标签没对齐：日均 y=%.1f，最多的一天 y=%.1f", avg.Y, peak.Y)
		}
		// 用过的那几行，次数左边的用量条还在（末端是没填满的底色）
		count := inside(t, tt, "13")
		img := tt.Image()
		p := img.RGBAAt(int(count.X)-14, int(count.Y+count.H/2))
		none := colChartNone.Over(colBgCard)
		if d := max(int(p.R)-int(none.R), int(none.R)-int(p.R)); d > 2 {
			t.Errorf("次数左边看不到用量条：x=%d 处是 %v", int(count.X)-14, p)
		}
	})

	t.Run("updates", func(t *testing.T) {
		a := newApp(library.Sample())
		updateFixture(a)
		a.selected = a.lib.Get("vue-best-practices")
		tt := newUITester(a.view, w, 900)
		tt.Advance(paneDuration)
		sha := inside(t, tt, "3f8a21c")
		btn := inside(t, tt, "查看差异")
		if sha.Y < btn.Y+btn.H && btn.Y < sha.Y+sha.H && sha.X+sha.W > btn.X {
			t.Errorf("commit 号叠在「查看差异」上：commit %v，按钮 %v", sha, btn)
		}
		inside(t, tt, "vite")
		inside(t, tt, "antfu/skills")
	})
}
