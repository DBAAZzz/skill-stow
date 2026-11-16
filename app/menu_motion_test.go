package main

import (
	"image"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"skillmanager/internal/library"
)

func clampedMenuBox(img *image.RGBA, anchor ui.Rect, padLeft, padTop, width, height int) image.Rectangle {
	b := img.Bounds()
	x0 := max(b.Min.X, int(anchor.X)-padLeft)
	y0 := max(b.Min.Y, int(anchor.Y)-padTop)
	x1 := min(b.Max.X, x0+width)
	y1 := min(b.Max.Y, y0+height)
	return image.Rect(x0, y0, x1, y1)
}

func TestDropdownSelectAndContextMenusAnimateOnOpenAndClose(t *testing.T) {
	// 1. 浏览页「筛选」下拉菜单（含「按 Agent」子菜单）开合均应有过渡动效
	a, tt := newTester(t)
	tt.Frame()
	baselineImg := tt.Image()

	filterBtn, ok := tt.Find("筛选")
	if !ok {
		t.Fatal("未找到「筛选」按钮")
	}

	clickMenuButton(t, tt, "筛选")
	tt.Advance(50 * time.Millisecond)
	midOpenImg := tt.Image()
	tt.Advance(300 * time.Millisecond)
	settledOpenImg := tt.Image()

	menuBox := clampedMenuBox(settledOpenImg, filterBtn, 210, 0, 260, 240)
	if regionPixelsEqual(midOpenImg, settledOpenImg, menuBox) {
		t.Fatal("「筛选」下拉框打开 50ms 时像素已与终态完全一致，缺少弹出动画效果")
	}
	if regionPixelsEqual(baselineImg, settledOpenImg, menuBox) {
		t.Fatal("「筛选」下拉框打开终态应绘制出菜单内容")
	}

	// 展开「按 Agent」子菜单也应有展开动画
	clickMenuItem(t, tt, "按 Agent")
	tt.Advance(45 * time.Millisecond)
	midSubImg := tt.Image()
	tt.Advance(300 * time.Millisecond)
	settledSubImg := tt.Image()

	subItem, ok := tt.AccessByRole(ui.RoleMenuItemCheckBox, "Claude Code")
	if !ok {
		t.Fatal("展开「按 Agent」子菜单后未找到「Claude Code」")
	}
	subBox := clampedMenuBox(settledSubImg, subItem.Bounds, 20, 40, 230, 180)
	if regionPixelsEqual(midSubImg, settledSubImg, subBox) {
		t.Fatal("「按 Agent」子菜单打开 45ms 时像素已与终态完全一致，缺少展开动画效果")
	}

	// 关闭主菜单：语义树立即可用（无残影阻塞），且视觉上带有短暂退场淡出而非生硬闪灭
	tt.Key(0, ui.KeyEscape)
	tt.Key(0, ui.KeyEscape)
	if a.windowMenuOpen(menuWinMain) || tt.HasText("按使用状态") {
		t.Fatal("按 Esc 收起菜单后状态应立即关闭且不残留可达标签")
	}
	tt.Advance(35 * time.Millisecond)
	midCloseImg := tt.Image()
	tt.Advance(250 * time.Millisecond)
	settledCloseImg := tt.Image()
	if regionPixelsEqual(midCloseImg, settledCloseImg, menuBox) {
		t.Fatal("「筛选」下拉框关闭 35ms 时像素已与完全消失时一致，缺少收起退场动效")
	}

	// 2. 设置页原生选择器下拉框（selectButton，例如「网络代理」）也应有打开动画
	aSet := newApp(library.Sample())
	aSet.set.tab = tabUpdates
	aSet.lib.Settings.UpdateProxyMode = "system"
	ttSet := newUITester(aSet.settingsView, settingsWidth, settingsMaxHeight)
	ttSet.Frame()
	if _, ok := ttSet.AccessByRole(ui.RoleMenuButton, "网络代理"); !ok {
		t.Fatal("设置页未找到「网络代理」下拉框")
	}
	clickMenuButton(t, ttSet, "网络代理")
	ttSet.Advance(50 * time.Millisecond)
	midSetImg := ttSet.Image()
	ttSet.Advance(300 * time.Millisecond)
	settledSetImg := ttSet.Image()
	proxyItem, ok := ttSet.AccessByRole(ui.RoleMenuItemRadio, "只读环境变量")
	if !ok {
		t.Fatal("展开「网络代理」下拉框后未找到「只读环境变量」")
	}
	proxyBox := clampedMenuBox(settledSetImg, proxyItem.Bounds, 20, 40, 240, 140)
	if regionPixelsEqual(midSetImg, settledSetImg, proxyBox) {
		t.Fatal("设置页下拉框打开 50ms 时像素已与终态完全一致，缺少弹出动画效果")
	}

	// 3. 卡片右键菜单（contextMenu）同样通过统一毛玻璃面板获得打开动画
	_, ttCtx := newTester(t)
	ttCtx.Frame()
	if _, ok := ttCtx.Find("systematic-debugging"); !ok {
		t.Fatal("未找到技能卡片")
	}
	must(t, ttCtx.RightClick("systematic-debugging"))
	ttCtx.Advance(50 * time.Millisecond)
	midCtxImg := ttCtx.Image()
	ttCtx.Advance(300 * time.Millisecond)
	settledCtxImg := ttCtx.Image()
	ctxItem, ok := ttCtx.AccessByRole(ui.RoleMenuItem, "在 Finder 中显示")
	if !ok {
		t.Fatal("打开右键菜单后未找到「在 Finder 中显示」")
	}
	ctxBox := clampedMenuBox(settledCtxImg, ctxItem.Bounds, 20, 20, 240, 180)
	if regionPixelsEqual(midCtxImg, settledCtxImg, ctxBox) {
		t.Fatal("右键菜单打开 50ms 时像素已与终态完全一致，缺少弹出动画效果")
	}
}

func TestMotionVisualSnapshots(t *testing.T) {
	dir := os.Getenv("SNAPSHOT_DIR")
	if dir == "" {
		t.Skip("设置 SNAPSHOT_DIR 以输出动效关键帧截图")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}

	_, tt := newTester(t)
	tt.SetScale(2)
	tt.Frame()
	must(t, tt.Click("看板"))
	tt.Advance(70 * time.Millisecond)
	writePNG(t, filepath.Join(dir, "motion-segmented-mid.png"), tt)
	tt.Advance(350 * time.Millisecond)
	writePNG(t, filepath.Join(dir, "motion-segmented-board.png"), tt)

	clickMenuButton(t, tt, "筛选")
	tt.Advance(55 * time.Millisecond)
	writePNG(t, filepath.Join(dir, "motion-menu-mid.png"), tt)
	tt.Advance(220 * time.Millisecond)
	writePNG(t, filepath.Join(dir, "motion-menu-settled.png"), tt)
}
