package main

import (
	"image"
	"math"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
)

// thumbCenterX 沿分段控件顶部避开文字的扫描线统计白色滑块像素的重心 X（DIP）。
func thumbCenterX(t *testing.T, tt *ui.Tester, cardRect, kanbanRect ui.Rect) float32 {
	t.Helper()
	img := tt.Image()
	y := int(cardRect.Y + 3)
	xMin := int(cardRect.X) + 1
	xMax := int(kanbanRect.X+kanbanRect.W) - 1
	var sum, count float64
	for x := xMin; x < xMax; x++ {
		c := img.RGBAAt(x, y)
		if c.R >= 248 && c.G >= 248 && c.B >= 248 {
			sum += float64(x)
			count++
		}
	}
	if count == 0 {
		t.Fatalf("未在分段轨道 (y=%d, x=[%d,%d)) 检测到白色滑块像素", y, xMin, xMax)
	}
	return float32(sum / count)
}

// regionPixelsEqual 比较两次截图在指定矩形内的 RGBA 像素是否完全一致。
func regionPixelsEqual(a, b *image.RGBA, r image.Rectangle) bool {
	if a == nil || b == nil {
		return false
	}
	for y := r.Min.Y; y < r.Max.Y; y++ {
		for x := r.Min.X; x < r.Max.X; x++ {
			if a.RGBAAt(x, y) != b.RGBAAt(x, y) {
				return false
			}
		}
	}
	return true
}

func TestSegmentedViewToggleAnimatesThumbAndViewContentInMine(t *testing.T) {
	// Given 用户处于默认的「我的」技能卡片视图
	a, tt := newTester(t)
	cardRect, ok := tt.Find("卡片")
	if !ok {
		t.Fatal("未找到「卡片」分段按钮")
	}
	kanbanRect, ok := tt.Find("看板")
	if !ok {
		t.Fatal("未找到「看板」分段按钮")
	}
	cardCenter := cardRect.X + cardRect.W/2
	kanbanCenter := kanbanRect.X + kanbanRect.W/2

	restCard := thumbCenterX(t, tt, cardRect, kanbanRect)
	if math.Abs(float64(restCard-cardCenter)) > 2.5 {
		t.Fatalf("初始滑块应在「卡片」居中 (%v)，实际 = %v", cardCenter, restCard)
	}

	// When 点击「看板」并推进约 70ms 到动画中段（此时「批量操作」按钮会隐藏）
	must(t, tt.Click("看板"))
	if a.mode != viewBoard {
		t.Fatalf("点击「看板」应切换为看板模式，实际 mode=%d", a.mode)
	}
	tt.Advance(70 * time.Millisecond)

	// Then 分段滑块应处于「卡片」与「看板」之间的平滑滑动中段，而非一步跳到终点
	midToBoard := thumbCenterX(t, tt, cardRect, kanbanRect)
	if !(midToBoard > cardCenter+4 && midToBoard < kanbanCenter-4) {
		t.Fatalf("切换「卡片 → 看板」70ms 后滑块应处于滑动中段 (%v < x < %v)，实际 = %v（缺少过渡动效）",
			cardCenter+4, kanbanCenter-4, midToBoard)
	}

	// And 主内容区在 70ms 时也应处于过渡动画中（尚未完全静止到最终帧）
	midImg := tt.Image()
	tt.Advance(450 * time.Millisecond)
	settledBoardImg := tt.Image()
	contentBox := image.Rect(int(sidebarWidth)+20, 70, 1240, 500)
	if regionPixelsEqual(midImg, settledBoardImg, contentBox) {
		t.Fatal("切换「卡片 → 看板」70ms 时内容区像素已与终点完全一致，缺少视图切换动效")
	}

	// And 动画结束后滑块应停在「看板」居中位置
	restBoard := thumbCenterX(t, tt, cardRect, kanbanRect)
	if math.Abs(float64(restBoard-kanbanCenter)) > 2.5 {
		t.Fatalf("动画结束后滑块应停在「看板」(%v)，实际 = %v", kanbanCenter, restBoard)
	}

	// When 再点击「卡片」并推进约 70ms（「批量操作」按钮重新出现）
	must(t, tt.Click("卡片"))
	if a.mode != viewCards {
		t.Fatalf("点击「卡片」应切回卡片模式，实际 mode=%d", a.mode)
	}
	tt.Advance(70 * time.Millisecond)

	// Then 滑块也应处于从「看板」滑回「卡片」的中段，并在完成后停回「卡片」
	midToCards := thumbCenterX(t, tt, cardRect, kanbanRect)
	if !(midToCards > cardCenter+4 && midToCards < kanbanCenter-4) {
		t.Fatalf("切换「看板 → 卡片」70ms 后滑块应处于回滑中段 (%v < x < %v)，实际 = %v（缺少过渡动效）",
			cardCenter+4, kanbanCenter-4, midToCards)
	}

	tt.Advance(450 * time.Millisecond)
	restBack := thumbCenterX(t, tt, cardRect, kanbanRect)
	if math.Abs(float64(restBack-cardCenter)) > 2.5 {
		t.Fatalf("回切完成后滑块应停回「卡片」(%v)，实际 = %v", cardCenter, restBack)
	}
}
