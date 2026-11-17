package main

import (
	"fmt"
	"image"
	"testing"

	"skillmanager/internal/library"
)

// cardEdges 找内容区第一张白卡：沿内容区中线往下，先过工具栏到灰色页面底，
// 再遇到的第一个白点是卡片顶边；在卡片顶边往下几行横扫，第一个和最后一个
// 白点是左右边缘。
func cardEdges(t *testing.T, img *image.RGBA, left int) (x0, x1, y0 int) {
	t.Helper()
	b := img.Bounds()
	page, card := colBgPage, colBgCard
	is := func(x, y int, want [3]uint8) bool {
		p := img.RGBAAt(x, y)
		d := func(a, b uint8) int { return max(int(a)-int(b), int(b)-int(a)) }
		return d(p.R, want[0]) <= 1 && d(p.G, want[1]) <= 1 && d(p.B, want[2]) <= 1
	}
	pageRGB := [3]uint8{page.R, page.G, page.B}
	cardRGB := [3]uint8{card.R, card.G, card.B}
	mid := (left + b.Max.X) / 2
	y, seenPage := 0, false
	for ; y < b.Max.Y; y++ {
		if !seenPage {
			seenPage = is(mid, y, pageRGB)
			continue
		}
		if is(mid, y, cardRGB) {
			break
		}
	}
	if y == b.Max.Y {
		t.Fatal("内容区里找不到白卡")
	}
	row := y + 6
	x0, x1 = -1, -1
	for x := left; x < b.Max.X; x++ {
		if is(x, row, cardRGB) {
			if x0 < 0 {
				x0 = x
			}
			x1 = x
		}
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
