package main

import (
	"time"

	"github.com/egoist/mygo/ui"
)

// 可点的东西（按钮、列表行、图标按钮）的手感，照 macOS 的分寸：悬停的
// 底色进快出慢，指针划过一排按钮留下一串渐渐淡去的痕迹；按下立刻变深，
// 晚一帧都像没点上；松开慢慢回来。颜色的渐变在系统要求减弱动态效果时
// 照旧。
//
// 菜单项不用它：系统菜单的高亮是即时的，渐变反倒显得迟钝。
const (
	hoverIn  = 80 * time.Millisecond
	hoverOut = 200 * time.Millisecond
	release  = 150 * time.Millisecond
)

// tint 返回 b 这一帧的颜色：平时 rest、悬停 hover、按下 press。按下立刻
// 变过去，其余按上面的时长渐变。key 区分同一个元素上的几种颜色（底色、
// 文字）。b 要是会响应点击的元素：它查 Clicked 认出刚松开的那一帧。
func tint(b ui.Element, key string, rest, hover, press ui.Color) ui.Color {
	target, d := rest, hoverOut
	switch {
	case b.Pressed():
		target, d = press, time.Nanosecond // 0 是弹簧，要立刻到就给最短的时长
	case b.Clicked(): // 刚松开
		target, d = hover, release
	case b.Hovered():
		target, d = hover, hoverIn
	}
	return b.AnimateColor(key, target, ui.Animation{Duration: d})
}

// tintBg 是只变底色的常见情形。
func tintBg(b ui.Element, rest, hover, press ui.Color) ui.Element {
	return b.Background(tint(b, "bg", rest, hover, press))
}

// tintSolid 是实底的按钮、行：悬停、按下在底色上叠一层 colInkHover、
// colInkPress，合成实色再渐变（透明叠层和实底之间渐变会先变淡）。
func tintSolid(b ui.Element, base ui.Color) ui.Element {
	return tintBg(b, base, colInkHover.Over(base), colInkPress.Over(base))
}

// tintInk 是透明的行、按钮：平时透明，悬停、按下叠在后面的底上。
func tintInk(b ui.Element) ui.Element {
	return tintBg(b, colInkHover.Alpha(0), colInkHover, colInkPress)
}

// darker 是按下时深一档的颜色，给彩色的实心按钮、链接用。
func darker(c ui.Color, amount float32) ui.Color { return c.Mix(black, amount) }

// selectableRow 给一行上悬停、按下、选中的底色：没选中时同 tintInk，选中时
// 是 selected。
func selectableRow(row ui.Element, on bool, selected ui.Color) {
	if on {
		row.Background(tint(row, "bg", selected, selected, selected))
		return
	}
	tintInk(row)
}

// cardFeel 是卡片悬停抬起、选中描边时阴影和底色的渐变。卡片没有按下的
// 颜色，进出用同一个时长：取悬停淡入淡出的中间。
func cardFeel() ui.ElementTransition {
	return ui.ElementTransition{Shadows: true, Colors: true, Duration: (hoverIn + hoverOut) / 2}
}

// labelTransition 是按钮上的字、图标换的时候：交叉淡入，不跳。
var labelTransition = ui.ContentTransition{}
