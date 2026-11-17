package main

import (
	"fmt"
	"image"
	"testing"

	"skillmanager/internal/library"
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
