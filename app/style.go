package main

import (
	"fmt"
	"os"
	"runtime"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"

	"skillmanager/internal/library"
	"skillmanager/locales"
)

const (
	titleBarHeight   = 52
	trafficLightSize = 16 // 红绿灯按钮的边长（含外圈）
	sidebarWidth     = 220
	inspectorWidth   = 340
	minCardWidth     = 210
)

// 设计稿用 0.5px 发丝线（Retina 2x 下正好 1 物理像素）和 5% 的淡投影。
// MyGo 按设备像素取整（ui/paint.go 的 snap），Windows 的 100%/125%/150%
// 缩放会把 0.5 DIP round 成 0 或只在某一侧留 1px：卡片于是要么没边、要么
// 像描了一圈硬线，淡投影也几乎看不见。macOS 保留设计稿原值，其它平台把
// 细线加宽到 1 DIP（任何缩放都至少 1 物理像素）、投影加深一档。
var edgeSpread, liftBoost = func() (float32, float32) {
	if runtime.GOOS == "darwin" {
		return 0.5, 1
	}
	return 1, 2
}()

// hairline 是控件四周的一圈细线，alpha 用设计稿的取值。
func hairline(e ui.Element, alpha float32) {
	e.Shadow(0, 0, 0, edgeSpread, ink(alpha))
}

// lift 是控件浮起的一层投影，alpha 用设计稿的取值。非 macOS 上只把 5~8%
// 的淡投影加深一档（低 DPI 下几乎看不见），本来就重的投影保持原值。
func lift(e ui.Element, y, blur, alpha float32) {
	if alpha < 0.1 {
		alpha *= liftBoost
	}
	e.Shadow(0, y, blur, 0, shadow(alpha))
}

// 隐藏标题栏后系统窗口控件压在视图上，位置随平台变：macOS 的红绿灯在
// 左上角，侧边栏收起时压在内容区工具栏上；Windows/Linux 的三个按钮在
// 右上角，压在内容区工具栏上。c.TitleBar() 给出它们占的 DIP，全屏时全为 0（控件隐藏）。
// 参见 mygo docs/ui/windows.md「Title bars」。
const (
	toolbarEdge       = 14 // 设计稿的顶栏右内边距
	toolbarControlGap = 12 // 让位后控件与顶栏内容之间的间隙
)

// toolbarHeight 是内容区顶栏的高度，取系统控件带与设计稿里较大的那个。
func toolbarHeight(c *ui.Context) float32 {
	return max(c.TitleBar().Height, titleBarHeight)
}

// toolbarRightInset 是顶栏的右内边距。Windows/Linux 上要把右上角三个按钮
// 占的 138 DIP 让出来；macOS 的 bar.Right 为 0，仍是设计稿的 14。
func toolbarRightInset(c *ui.Context) float32 {
	return max(toolbarEdge, c.TitleBar().Right+toolbarControlGap)
}

// theme 按系统的深浅色换好颜色（usePalette），再把框架的主题调成设计规范
// 的取值。每帧开头调用：系统外观一变，下一帧就跟着变。
func theme(c *ui.Context) *ui.Theme {
	dark := c.Theme().Dark // 这时还是框架按系统外观给的主题
	usePalette(dark)
	t := *ui.LightTheme()
	if dark {
		t = *ui.DarkTheme()
		t.Surface, t.SurfaceHover, t.SurfacePressed = colBgControl, colInkHover.Over(colBgControl), colInkPress.Over(colBgControl)
	}
	t.Background = colBgSurface
	t.Accent = colAccent
	t.Text = colTextPrimary
	t.TextMuted = colTextSecondary
	t.Border = colBorderDefault
	t.FontSize = 13
	c.SetTheme(&t)
	return c.Theme()
}

// flag 是橙色的提示：图标 + 文字。
func flag(c *ui.Context, icon *ui.SVG, text string) ui.Element {
	return ui.Row(c).Gap(3).AlignItems(ui.Center).Shrink(0).TextColor(colWarnText).Children(func() {
		ui.Icon(c, icon).Size(11, 11)
		ui.Text(c, text).FontSize(11).FontWeight(500).SingleLine()
	})
}

// copyTip 是悬停时列出的副本路径，主目录写成 ~。
func copyTip(s *library.Skill) string {
	home, _ := os.UserHomeDir()
	var lines []string
	for _, d := range s.CopyDirs() {
		lines = append(lines, library.ShortPath(home, d))
	}
	return strings.Join(lines, "\n")
}

// flags 是技能的问题提示；被全局链接的项目技能标出来。
func flags(c *ui.Context, s *library.Skill) {
	if s.MissingDescription() {
		flag(c, iconWarn, locales.ImportMissingDescription())
	}
	if len(s.Defects) > 0 {
		flag(c, iconWarn, library.Incomplete.Title()).Tooltip(defectLines(s.Defects))
	}
	if s.Copies > 0 {
		f := flag(c, iconWarn, locales.CommonOtherCopies(s.Copies))
		if tip := copyTip(s); tip != "" {
			f.Tooltip(tip)
		}
	}
	if s.SameName {
		flag(c, iconWarn, locales.CommonSameNameAsMine())
	}
	if s.Source == library.Project && len(s.Enabled) > 0 {
		flag(c, iconLink, locales.CommonGlobalLink())
	}
}

// linkButton 是蓝色文字按钮。
func linkButton(c *ui.Context, label string) ui.Element {
	b := ui.ButtonBase(c).Cursor(ui.CursorPointer).Label(label)
	b.TextColor(tint(b, "text", colAccent, colAccent, darker(colAccent, 0.35)))
	b.Children(func() { ui.Text(c, label).FontSize(12).SingleLine().ContentTransition(labelTransition) })
	return b
}

// iconButton 是只有图标的工具栏按钮。
func iconButton(c *ui.Context, icon *ui.SVG, label string) ui.Element {
	b := ui.ButtonBase(c).Size(28, 26).Radius(6).Center().TextColor(colTextBody).Label(label).Tooltip(label)
	tintBg(b, ink(0), colInkHover, colInkPress)
	b.Children(func() { ui.Icon(c, icon).Size(16, 16).ContentTransition(labelTransition) })
	return b
}

// button 是白色浮起的工具栏按钮（r9 .btn）。
func button(c *ui.Context, icon *ui.SVG, label string) ui.Element {
	return toolButton(c, icon, label, false)
}

// toolButton 是 button；iconOnly 时只留图标，字放进悬停提示（工具栏窄时用，
// 见 toolbarCompact）。
func toolButton(c *ui.Context, icon *ui.SVG, label string, iconOnly bool) ui.Element {
	b := ui.ButtonBase(c).Height(24).Padding(0, 10).Gap(6).Radius(6).AlignItems(ui.Center).Shrink(0).Label(label)
	if iconOnly {
		b.Padding(0, 7).Tooltip(label)
	}
	tintSolid(b, colBgControl)
	hairline(b, 0.18)
	lift(b, 1, 1, 0.06)
	b.Children(func() {
		ui.Icon(c, icon).Size(13, 13).ContentTransition(labelTransition)
		if !iconOnly {
			ui.Text(c, label).FontSize(12).SingleLine().ContentTransition(labelTransition)
		}
	})
	return b
}

// busyButton 是 button；busy 时图标换成转圈、按钮点不了，字由调用方换成
// 「正在……」。
func busyButton(c *ui.Context, icon *ui.SVG, label string, busy bool) ui.Element {
	b := ui.ButtonBase(c).Height(24).Padding(0, 10).Gap(6).Radius(6).AlignItems(ui.Center).Shrink(0).Label(label)
	tintSolid(b, colBgControl)
	hairline(b, 0.18)
	lift(b, 1, 1, 0.06)
	b.Disabled(busy)
	b.Children(func() {
		if busy {
			ui.Spinner(c).Size(13, 13).TextColor(colAccent)
		} else {
			ui.Icon(c, icon).Size(13, 13)
		}
		ui.Text(c, label).FontSize(12).SingleLine().ContentTransition(labelTransition)
	})
	return b
}

// toolbarCompactBelow 是浏览页工具栏放下全部控件的宽度：比它窄时（最小窗口
// 进批量、或系统显示缩放到 125%）按钮只留图标、搜索框变窄，批量时右边灰掉
// 的控件收起来，免得互相压住或把搜索框挤出窗口。
const toolbarCompactBelow = 830

// toolbarCompact 报告内容区上方的工具栏是不是窄到要收紧。
func (a *app) toolbarCompact(c *ui.Context) bool {
	w, _ := c.Size()
	return w-a.sidebarRoom() < toolbarCompactBelow
}

func sectionLabel(c *ui.Context, text string) ui.Element {
	return ui.Text(c, text).FontSize(11).FontWeight(600).TextColor(colTextMuted)
}

var segmentedSpring = ui.Animation{Spring: ui.SpringOptions{Duration: 280 * time.Millisecond, Bounce: 0.14}}

// segmented 是工具栏的分段控件（r8 .seg）：灰底，白色滑块浮起，
// 切换时滑块带弹性动画滑到选中的段。各段等宽，滑块的位置才算得出来。
func segmented(c *ui.Context, selected *int, labels ...string) ui.Element {
	const pad, segW, segH = 2, 52, 24
	seg := ui.SegmentedBase(c, selected, len(labels))
	track := seg.Track.Padding(pad).Radius(6).Background(colBgFill)
	x := track.AnimateTo("thumb", float32(*selected)*segW, segmentedSpring)
	track.Children(func() {
		thumb := ui.Box(c.Key("thumb")).Absolute().ZIndex(-1).Top(pad).Left(pad).Translate(x, 0).Size(segW, segH).Radius(4).Background(colBgThumb)
		lift(thumb, 1, 1.5, 0.14)
		hairline(thumb, 0.05)
		for i, label := range labels {
			s := seg.Segment(i).Size(segW, segH).Radius(4).Center().Label(label)
			s.Children(func() {
				t := ui.Text(c, label).FontSize(12).SingleLine().TextColor(colTextBody)
				if i == *selected {
					t.TextColor(colTextPrimary).FontWeight(500)
				} else if s.Pressed() {
					t.Opacity(0.6)
				}
			})
		}
	})
	return track
}

// searchBox 是工具栏的搜索框（r8 .search）。
func searchBox(c *ui.Context, query *string) {
	ui.Row(c).Width(190).MinWidth(120).Height(28).Shrink(1).Radius(6).Background(colBgSidebar).Padding(0, 9).Gap(6).
		AlignItems(ui.Center).Children(func() {
		ui.Icon(c, iconSearch).Size(14, 14).TextColor(colTextMuted)
		ui.TextInputBase(c, query).Placeholder(locales.ActionSearch()).FontSize(13).Grow(1).Label(locales.CommonSearchPlaceholder()).Search()
	})
}

func sizeLabel(n int) string {
	switch {
	case n < 1024:
		return fmt.Sprintf("%d B", n)
	case n < 1<<20:
		return fmt.Sprintf("%.1f KB", float64(n)/1024)
	}
	return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
}
