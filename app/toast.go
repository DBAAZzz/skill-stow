package main

import (
	"slices"
	"time"

	"github.com/egoist/mygo/plugins/glass"
	"github.com/egoist/mygo/ui"

	"skillmanager/locales"
)

// 提示条（画板「提示条」第二轮 ts4–ts8）：内容区顶部、工具栏下方的浅色
// 毛玻璃横幅，详情栏开着时在它左边居中。全应用只有这一种，删除、整理、
// 更新、打开链接的结果都放在这里：
//
//   - 同一来源同时只有一条，新的原地替换旧的，旧的「撤销」不会留在下面；
//   - 最多 3 条，再来一条时最早的先收起；
//   - 常规、成功停 5 秒，带操作的停 8 秒，警告和错误等用户关。
//
// 页面里的结果行（整理页的绿行红行、更新页的横幅和额度提示）不是提示条：
// 它们说的是这一页的状态，留在页面里。

// toastKind 是提示条的语义，只体现在左边符号的颜色上，卡片本身不染色。
type toastKind int

const (
	toastPlain toastKind = iota // 常规（删除）：灰色垃圾桶
	toastOK                     // 成功：绿色勾
	toastWarn                   // 警告：黄色三角
	toastError                  // 错误：红色叹号
)

// toastSource 是提示条从哪来：同一来源同时只有一条。
type toastSource int

const (
	fromDelete  toastSource = iota // 删除技能、撤销删除
	fromTidy                       // 整理页在后台做完的事
	fromUpdate                     // 检查、下载、更新
	fromLink                       // 打开外部链接
	fromImport                     // 添加技能
	fromAppData                    // 应用自己的数据文件坏了、已备份
	fromPlugin                     // 卸载插件
)

// toastAction 是提示条右边的文字按钮，如「撤销 ⌘Z」。
type toastAction struct {
	label    string
	shortcut string // 按钮后面淡淡写出的快捷键，可以为空
	run      func()
}

type toast struct {
	source toastSource
	kind   toastKind
	text   string
	action *toastAction // nil 是没有按钮
}

// maxToasts 是同时显示的提示条上限。
const maxToasts = 3

// lifetime 是提示条停多久自己收起；0 是等用户关。
func (t *toast) lifetime() time.Duration {
	switch {
	case t.kind == toastWarn || t.kind == toastError:
		return 0
	case t.action != nil:
		return 8 * time.Second // 留时间去点；收起后快捷键照样能用
	}
	return 5 * time.Second
}

// closable 报告提示条要用户手动关：不会自己收起的才有关闭按钮。
func (t *toast) closable() bool { return t.lifetime() == 0 }

// toastStack 是正在显示的提示条，先来的在上。
type toastStack struct {
	items []*toast
}

// find 返回这个来源正在显示的提示条，没有为 nil。
func (s *toastStack) find(src toastSource) *toast {
	if i := slices.IndexFunc(s.items, func(t *toast) bool { return t.source == src }); i >= 0 {
		return s.items[i]
	}
	return nil
}

// notify 显示一条没有按钮的提示条。
func (a *app) notify(src toastSource, kind toastKind, text string) {
	a.showToast(&toast{source: src, kind: kind, text: text})
}

// showToast 显示 t：同一来源的原地替换，满了先收起最早的；会自己收起的，
// 到时候收起（被替换掉的不会把替换上来的一起收掉）。
func (a *app) showToast(t *toast) {
	a.logToast(t.kind, t.text)
	s := &a.toasts
	if i := slices.IndexFunc(s.items, func(o *toast) bool { return o.source == t.source }); i >= 0 {
		s.items[i] = t
	} else {
		s.items = append(s.items, t)
		if len(s.items) > maxToasts {
			s.items = s.items[1:]
		}
	}
	a.changed()
	// 测试里没有窗口，提示条一直留着，好断言它的内容
	if d := t.lifetime(); d > 0 && a.live {
		time.AfterFunc(d, func() { a.post(func() { a.closeToast(t) }) })
	}
}

// closeToast 收起 t；已经收起或被替换掉时什么也不做。
func (a *app) closeToast(t *toast) {
	s := &a.toasts
	if i := slices.Index(s.items, t); i >= 0 {
		s.items = slices.Delete(s.items, i, i+1)
		a.changed()
	}
}

// dismissToast 收起这个来源的提示条，如撤销成功后收起「已移到废纸篓」。
func (a *app) dismissToast(src toastSource) {
	if t := a.toasts.find(src); t != nil {
		a.closeToast(t)
	}
}

// ——— 画 ———

// 提示条进场要让人察觉到，又不能抢戏：卡片从上方 16px、略小、带一点
// 模糊落下来，柔和的弹簧轻轻停稳；落定前左边的符号再弹出来，把视线引到
// 它身上。收起时往上淡出，下面的补上来。系统要求减弱动态效果时，mygo
// 只淡入淡出。
var (
	toastSpring       = ui.Animation{Spring: ui.SpringOptions{Duration: 520 * time.Millisecond, Bounce: 0.14}}
	toastSymbolSpring = ui.Animation{Spring: ui.SpringOptions{Duration: 450 * time.Millisecond, Bounce: 0.4}, Delay: 120 * time.Millisecond}

	toastTransition = ui.ElementTransition{
		Animation:    &toastSpring,
		Position:     true,
		Enter:        &ui.Motion{Opacity: 0, Y: -16, Scale: 0.92, Blur: 4},
		Exit:         &ui.Motion{Opacity: 0, Y: -8, Scale: 0.97},
		ExitDuration: 200 * time.Millisecond,
		ExitEase:     ui.CubicBezier(0.4, 0, 1, 1),
	}
	toastSymbolTransition = ui.ElementTransition{
		Animation: &toastSymbolSpring,
		Enter:     &ui.Motion{Opacity: 0, Scale: 0.3},
	}
)

// toastLayer 叠在内容区上面，每帧都要建：栈本身不在了，收起的提示条就
// 没有地方播放离场动画。右边让出详情栏，跟着详情栏一起滑。
func (a *app) toastLayer(c *ui.Context) {
	right := c.Root().AnimateWith("toast-pane", a.paneRoom, paneDuration, paneEase)
	ui.Column(c.Key("toasts")).Absolute().Top(toolbarHeight(c) + 12).Left(0).Right(right).
		AlignItems(ui.Center).Gap(8).PassThrough().Children(func() {
		for _, t := range a.toasts.items {
			a.toastCard(c.Key(t.source), t)
		}
	})
}

// frostedGlass 是提示条的材质：后面的内容模糊透出来，再压一层白。
type frostedGlass struct{}

func (frostedGlass) PaintMaterial(p *ui.Painter, box ui.Rect, radii [4]float32) {
	glass.Blur{Radius: 28}.PaintMaterial(p, box, radii)
	p.FillRadii(box, radii, colBgToastGlass)
}

// toastCard 是一条提示条：符号、文字，右边可选的文字按钮和关闭。文字长了
// 换行，符号和按钮对齐第一行。
func (a *app) toastCard(c *ui.Context, t *toast) {
	card := ui.Row(c).MaxWidth(560).Padding(11, 14, 11, 12).Gap(9).Radius(12).AlignItems(ui.Start).
		Material(frostedGlass{}).
		Border(0.5, ink(0.14)).
		InsetShadow(0, 0.5, 0, 0, rim(0.85)).
		Shadow(0, 10, 30, -4, shadow(0.14)).
		Shadow(0, 2, 6, 0, shadow(0.06)).
		Transition(toastTransition)
	if t.action != nil || t.closable() {
		card.Padding(11, 8, 11, 12)
	}
	card.Children(func() {
		// 按这一条提示认符号：同一来源换上新的一条时，符号再弹一次，看得出
		// 内容变了
		ui.Image(c.Key(t), toastSymbols[t.kind]).Size(16, 16).Shrink(0).Margin(1, 0, 0, 0).
			Transition(toastSymbolTransition)
		ui.Text(c, t.text).FontSize(13).FixedLineHeight(18).TextColor(colTextPrimary).Grow(1).MinWidth(0).
			ContentTransition(ui.ContentTransition{}) // 同一来源替换时文字交叉淡入
		if act := t.action; act != nil {
			ui.Row(c).Height(24).Margin(-3, 0, -3, 4).Gap(2).AlignItems(ui.Center).Shrink(0).Children(func() {
				b := ui.ButtonBase(c).Height(24).Padding(0, 6).Radius(6).Center().Label(act.label)
				tintBg(b, ui.Transparent, colAccent.Alpha(0.09), colAccent.Alpha(0.15))
				b.Children(func() { ui.Text(c, act.label).FontSize(13).FontWeight(500).TextColor(colAccent).SingleLine() })
				if b.Clicked() {
					act.run()
				}
				if act.shortcut != "" {
					ui.Text(c, act.shortcut).FontSize(12).TextColor(colTextDisabled).SingleLine().Margin(0, 6, 0, 0)
				}
			})
		}
		if t.closable() {
			b := ui.ButtonBase(c).Size(18, 18).Radius(9).Center().Shrink(0).Margin(0, 2, 0, 6).
				TextColor(colTextMuted).Label(locales.ToastDismiss())
			tintBg(b, ink(0.055), ink(0.11), ink(0.15))
			b.Children(func() { ui.Icon(c, iconToastClose).Size(8, 8) })
			if b.Clicked() {
				a.closeToast(t)
			}
		}
	})
}

// toastSymbols 是四种语义的实心符号（16 视口，自带颜色）。
var toastSymbols = map[toastKind]*ui.SVG{
	toastPlain: toastSymbol(`<path fill="#8e8e93" d="M6.3 1.5h3.4c.5 0 .9.4.9.9V3h3a.65.65 0 0 1 0 1.3H2.4a.65.65 0 0 1 0-1.3h3V2.4c0-.5.4-.9.9-.9zM3.4 5.2h9.2l-.6 8.1c-.1.9-.8 1.5-1.6 1.5H5.6c-.8 0-1.5-.6-1.6-1.5z"/><path d="M6.4 7.3v5M9.6 7.3v5" stroke="#fff" stroke-width="1.1" stroke-linecap="round"/>`),
	toastOK:    toastSymbol(`<circle cx="8" cy="8" r="7.2" fill="#34c759"/><path d="m4.9 8.2 2.1 2.1 4.1-4.4" fill="none" stroke="#fff" stroke-width="1.7" stroke-linecap="round" stroke-linejoin="round"/>`),
	toastWarn:  toastSymbol(`<path fill="#ffc400" d="M6.8 1.9c.5-.9 1.9-.9 2.4 0l5.9 10.6c.5.9-.2 2-1.2 2H2.1c-1 0-1.7-1.1-1.2-2z"/><path d="M8 5.6v4" stroke="#1d1d1f" stroke-opacity=".86" stroke-width="1.6" stroke-linecap="round"/><circle cx="8" cy="11.9" r=".95" fill="#1d1d1f" fill-opacity=".86"/>`),
	toastError: toastSymbol(`<circle cx="8" cy="8" r="7.2" fill="#ff3b30"/><path d="M8 4.4v4.4" stroke="#fff" stroke-width="1.7" stroke-linecap="round"/><circle cx="8" cy="11.3" r="1" fill="#fff"/>`),
}

func toastSymbol(shapes string) *ui.SVG {
	return ui.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 16 16">` + shapes + `</svg>`))
}

var iconToastClose = ui.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 8 8" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round"><path d="M1 1l6 6M7 1 1 7"/></svg>`))
