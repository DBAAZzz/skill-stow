package main

import (
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
)

// 统一毛玻璃菜单与下拉选择器（对应设计系统 §13、画板 sf5-b / sf6）。
// 禁止在 menu.go 之外直接调用 Element.Menu / ContextMenu / MenuBase / ContextMenuBase / PopoverBase。

const (
	menuMinWidth float32 = 196
	menuMaxWidth float32 = 300
	menuPad      float32 = 5
	menuRadius   float32 = 11
	menuGap      float32 = 4
	menuRowH     float32 = 31
	menuRowPadX  float32 = 9
	menuRowRad   float32 = 6
	menuRowGap   float32 = 9
	menuIconSize float32 = 16
	menuFontSize float32 = 13
	menuSubSize  float32 = 12
)

type menuGlass struct{}

func (menuGlass) PaintMaterial(p *ui.Painter, box ui.Rect, radii [4]float32) {
	frostedGlass{}.PaintMaterial(p, box, radii)
	p.FillRadii(box, radii, colBgGlass)
}

// 下拉菜单、右键菜单与子菜单的弹出质感：从锚点边缘（AttachedOrigin）轻盈展开，
// 起手利落、收尾柔和（Bezier(0.16, 1, 0.3, 1)），空间不足翻转时 Flip 自动反向，收起干脆半拍。
var (
	menuEnterAnim = ui.Animation{Duration: 190 * time.Millisecond, Ease: ui.CubicBezier(0.16, 1, 0.3, 1)}

	dropdownMenuTransition = ui.ElementTransition{
		Animation:    &menuEnterAnim,
		Enter:        &ui.Motion{Opacity: 0, Scale: 0.93, Y: -6, Flip: true},
		Exit:         &ui.Motion{Opacity: 0, Scale: 0.96, Y: -3, Flip: true},
		ExitDuration: 110 * time.Millisecond,
	}
	contextMenuTransition = ui.ElementTransition{
		Animation:    &menuEnterAnim,
		Enter:        &ui.Motion{Opacity: 0, Scale: 0.92},
		Exit:         &ui.Motion{Opacity: 0, Scale: 0.96},
		ExitDuration: 100 * time.Millisecond,
	}
	submenuTransition = ui.ElementTransition{
		Animation:    &menuEnterAnim,
		Enter:        &ui.Motion{Opacity: 0, Scale: 0.95, X: -6, Flip: true},
		Exit:         &ui.Motion{Opacity: 0, Scale: 0.97, X: -4, Flip: true},
		ExitDuration: 100 * time.Millisecond,
	}
)

type menuWin uint8

const (
	menuWinMain menuWin = iota
	menuWinSettings
)

type menuEntry struct {
	open bool
	win  menuWin
	hi   bool
}

func (a *app) menuWindowForKey(key string) menuWin {
	if a.menuBuildWin == menuWinSettings || strings.HasPrefix(key, "settings.") {
		return menuWinSettings
	}
	return menuWinMain
}

func (a *app) menuEntry(key string) *menuEntry {
	if a.menus == nil {
		a.menus = map[string]*menuEntry{}
	}
	if e := a.menus[key]; e != nil {
		e.win = a.menuWindowForKey(key)
		return e
	}
	e := &menuEntry{win: a.menuWindowForKey(key)}
	a.menus[key] = e
	return e
}

func (a *app) menuOpen(key string) *bool {
	return &a.menuEntry(key).open
}

func (a *app) windowMenuOpen(win menuWin) bool {
	for _, e := range a.menus {
		if e != nil && e.open && e.win == win {
			return true
		}
	}
	return false
}

func (a *app) anyMenuOpen() bool {
	for _, e := range a.menus {
		if e != nil && e.open {
			return true
		}
	}
	return false
}

func (a *app) closeMenus() {
	for _, e := range a.menus {
		if e != nil {
			e.open = false
			e.hi = false
		}
	}
}

func (a *app) closeMenusIn(win menuWin) {
	for _, e := range a.menus {
		if e != nil && e.win == win {
			e.open = false
			e.hi = false
		}
	}
}

type menuPanelOpts struct {
	width    float32
	minWidth float32
	maxWidth float32
	alignEnd bool
	padLead  bool
}

func styleMenuPanel(c *ui.Context, panel ui.Element, o menuPanelOpts) {
	minW := o.minWidth
	if minW <= 0 {
		minW = menuMinWidth
	}
	maxW := o.maxWidth
	if maxW <= 0 {
		maxW = menuMaxWidth
	}
	panel.Material(menuGlass{}).Padding(menuPad).Radius(menuRadius).Gap(0).
		InsetShadow(0, 0.5, 0, 0, rim(0.75)).
		Shadow(0, 18, 48, 0, shadow(0.17)).
		Shadow(0, 2, 8, 0, shadow(0.06))
	hairline(panel, 0.20)
	if o.width > 0 {
		panel.Width(o.width)
	} else {
		panel.MinWidth(minW).MaxWidth(maxW)
	}
}

// frostedMenu 只在弹层回调的那一次构建里有效。Context、MenuParts、面板这些
// 构建期的值经闭包取用而不存成字段：mygo vet 不允许结构体保存它们。
type frostedMenu struct {
	ctx     func() *ui.Context
	parts   func() ui.MenuParts
	panel   func() ui.Element
	entry   *menuEntry
	padLead bool
	prevHi  bool
	hasHi   bool
}

func newFrostedMenu(c *ui.Context, parts ui.MenuParts, panel ui.Element, entry *menuEntry, padLead bool) *frostedMenu {
	return &frostedMenu{
		ctx:     func() *ui.Context { return c },
		parts:   func() ui.MenuParts { return parts },
		panel:   func() ui.Element { return panel },
		entry:   entry,
		padLead: padLead,
	}
}

type menuHotkey struct {
	mods            ui.Modifiers
	key             ui.Key
	needNoHighlight bool
	valid           bool
}

func resolveMenuHotkey(o menuItemOpts) menuHotkey {
	switch o.shortcut {
	case "⌘⌫":
		return menuHotkey{mods: ui.Cmd, key: ui.KeyBackspace, valid: true}
	case "Space":
		return menuHotkey{mods: 0, key: ui.KeySpace, needNoHighlight: true, valid: true}
	}
	return menuHotkey{}
}

func buildFrostedPopup(c *ui.Context, m ui.MenuParts, entry *menuEntry, o menuPanelOpts, anchorToTrigger bool, build func(fm *frostedMenu)) {
	m.Popup(func(panel ui.Element) {
		styleMenuPanel(c, panel, o)
		if anchorToTrigger {
			if o.alignEnd {
				panel.AttachTo(m.Trigger, ui.AnchorBottomEnd, ui.AnchorTopEnd)
			} else {
				panel.AttachTo(m.Trigger, ui.AnchorBottomStart, ui.AnchorTopStart)
			}
			panel.Margin(menuGap, 0, 0, 0)
			panel.AttachedOrigin().Transition(dropdownMenuTransition)
		} else {
			panel.AttachedOrigin().Transition(contextMenuTransition)
		}
		fm := newFrostedMenu(c, m, panel, entry, o.padLead)
		fm.prevHi = entry != nil && entry.hi
		build(fm)
		if entry != nil {
			entry.hi = fm.hasHi
		}
	})
}

type actionIconOpts struct {
	size     float32
	height   float32
	active   bool
	disabled bool
	alignEnd bool
	padLead  bool
	width    float32
	marginR  float32
	tooltip  string
}

func (a *app) iconActionMenu(c *ui.Context, key string, ic *ui.SVG, label string, o actionIconOpts, build func(fm *frostedMenu)) ui.MenuParts {
	entry := a.menuEntry(key)
	m := ui.MenuBase(c, &entry.open)
	w, h := o.size, o.height
	if w <= 0 {
		w = 30
	}
	if h <= 0 {
		h = 28
	}
	btn := m.Trigger.Size(w, h).Radius(6).Center().Label(label)
	if o.marginR != 0 {
		btn.Margin(0, -o.marginR, 0, 0)
	}
	if o.tooltip != "" {
		btn.Tooltip(o.tooltip)
	}
	if o.disabled {
		btn.Disabled(true).Opacity(0.45)
	}
	styleMenuIconTrigger(btn, entry.open, o.active)
	btn.Children(func() { ui.Icon(c, ic).Size(16, 16) })
	buildFrostedPopup(c, m, entry, menuPanelOpts{
		width:    o.width,
		alignEnd: o.alignEnd,
		padLead:  o.padLead,
	}, true, build)
	return m
}

func styleMenuIconTrigger(btn ui.Element, open, active bool) {
	switch {
	case active && open:
		btn.Background(colInkHover.Over(colAccentBg)).TextColor(colAccentText)
	case active:
		tintBg(btn, colAccentBg, colInkHover.Over(colAccentBg), colInkHover.Over(colAccentBg))
		btn.TextColor(colAccentText)
	case open:
		btn.Background(colInkSelected).TextColor(colTextPrimary)
	default:
		tintInk(btn)
		btn.TextColor(colTextBody)
	}
}

func (a *app) triggerMenu(c *ui.Context, key string, o menuPanelOpts, styleTrigger func(btn ui.Element, open bool), build func(fm *frostedMenu)) ui.MenuParts {
	entry := a.menuEntry(key)
	m := ui.MenuBase(c, &entry.open)
	styleTrigger(m.Trigger, entry.open)
	buildFrostedPopup(c, m, entry, o, true, build)
	return m
}

func (a *app) selectButton(c *ui.Context, key, accessibleName, current string, disabled bool, build func(fm *frostedMenu)) ui.MenuParts {
	entry := a.menuEntry(key)
	m := ui.MenuBase(c, &entry.open)
	btn := m.Trigger.Height(24).Padding(0, 7, 0, 10).Gap(10).Radius(6).AlignItems(ui.Center).Label(accessibleName)
	hairline(btn, 0.18)
	lift(btn, 1, 1.5, 0.08)
	if disabled {
		btn.Disabled(true).Opacity(0.55)
	}
	if entry.open {
		btn.Background(colInkPress.Over(colBgControl))
	} else {
		tintSolid(btn, colBgControl)
	}
	btn.Children(func() {
		ui.Text(c, current).FontSize(menuFontSize).TextColor(colTextPrimary).SingleLine()
		ui.Icon(c, iconUpDown).Size(10, 12).TextColor(colTextSecondary).Shrink(0)
	})
	buildFrostedPopup(c, m, entry, menuPanelOpts{
		minWidth: menuMinWidth,
		alignEnd: true,
		padLead:  true,
	}, true, build)
	return m
}

func (a *app) contextMenu(c *ui.Context, target ui.Element, key string, padLead bool, build func(fm *frostedMenu)) ui.MenuParts {
	entry := a.menuEntry(key)
	m := ui.ContextMenuBase(c, target, &entry.open)
	buildFrostedPopup(c, m, entry, menuPanelOpts{padLead: padLead}, false, build)
	return m
}

// ——— 菜单项 ———

type menuItemOpts struct {
	key        string
	a11yLabel  string
	icon       *ui.SVG
	badge      func(c *ui.Context)
	shortcut   string
	trailing   string
	subtitle   string
	detail     string // 名称下面一行灰色说明，这一行随之变高
	disabled   bool
	danger     bool
	muted      bool
	keepOpen   bool
	checkMark  bool
	trailCheck bool
	checked    bool
	activeBg   bool
	roleCheck  bool
	roleRadio  bool
	isSub      bool
}

type menuOpt func(*menuItemOpts)

func withKey(k string) menuOpt             { return func(o *menuItemOpts) { o.key = k } }
func withLabel(s string) menuOpt           { return func(o *menuItemOpts) { o.a11yLabel = s } }
func withIcon(ic *ui.SVG) menuOpt          { return func(o *menuItemOpts) { o.icon = ic } }
func withBadge(fn func(*ui.Context)) menuOpt { return func(o *menuItemOpts) { o.badge = fn } }
func withShortcut(s string) menuOpt        { return func(o *menuItemOpts) { o.shortcut = s } }
func withTrailing(s string) menuOpt        { return func(o *menuItemOpts) { o.trailing = s } }
func withSubtitle(s string) menuOpt        { return func(o *menuItemOpts) { o.subtitle = s } }
func withDetail(s string) menuOpt          { return func(o *menuItemOpts) { o.detail = s } }
func withDisabled(v bool) menuOpt          { return func(o *menuItemOpts) { o.disabled = v } }
func withDanger() menuOpt                  { return func(o *menuItemOpts) { o.danger = true } }
func withMuted() menuOpt                   { return func(o *menuItemOpts) { o.muted = true } }
func withKeepOpen() menuOpt                { return func(o *menuItemOpts) { o.keepOpen = true } }
func withTrailCheck() menuOpt              { return func(o *menuItemOpts) { o.trailCheck = true } }
func withActiveBg() menuOpt                { return func(o *menuItemOpts) { o.activeBg = true } }

func (fm *frostedMenu) triggered(el ui.Element, o menuItemOpts) bool {
	if el.Highlighted() {
		fm.hasHi = true
	}
	if el.Clicked() {
		return true
	}
	if o.disabled {
		return false
	}
	hk := resolveMenuHotkey(o)
	if !hk.valid || (hk.needNoHighlight && fm.prevHi) {
		return false
	}
	if fm.panel().OverlayShortcut(hk.mods, hk.key) {
		if !o.keepOpen && fm.entry != nil {
			fm.entry.open = false
			fm.entry.hi = false
		}
		return true
	}
	return false
}

func (fm *frostedMenu) action(label string, opts ...menuOpt) bool {
	var o menuItemOpts
	for _, fn := range opts {
		fn(&o)
	}
	return fm.triggered(fm.row(label, o), o)
}

func (fm *frostedMenu) check(label string, checked bool, opts ...menuOpt) bool {
	o := menuItemOpts{checkMark: true, checked: checked, roleCheck: true}
	for _, fn := range opts {
		fn(&o)
	}
	return fm.triggered(fm.row(label, o), o)
}

func (fm *frostedMenu) radio(label string, selected bool, opts ...menuOpt) bool {
	o := menuItemOpts{checkMark: true, checked: selected, activeBg: true, roleRadio: true}
	for _, fn := range opts {
		fn(&o)
	}
	return fm.triggered(fm.row(label, o), o)
}

func (fm *frostedMenu) submenu(label, value string, opts []menuOpt, subOpts menuPanelOpts, build func(sub *frostedMenu)) {
	o := menuItemOpts{isSub: true, trailing: value}
	for _, fn := range opts {
		fn(&o)
	}
	row := fm.row(label, o)
	fm.parts().Submenu(row, func(panel ui.Element) {
		styleMenuPanel(fm.ctx(), panel, subOpts)
		panel.Margin(-menuPad, 0, 0, menuGap)
		panel.AttachedOrigin().Transition(submenuTransition)
		build(newFrostedMenu(fm.ctx(), fm.parts(), panel, fm.entry, subOpts.padLead))
	})
}

// header 是菜单里一组的标题：半粗的小字，note 是右边的灰色说明。
func (fm *frostedMenu) header(title, note string) {
	ui.Row(fm.ctx()).Height(26).Padding(0, menuRowPadX).Gap(8).AlignItems(ui.Center).Shrink(0).Children(func() {
		ui.Text(fm.ctx(), title).FontSize(12).FontWeight(600).TextColor(colTextSecondary).SingleLine()
		if note != "" {
			ui.Text(fm.ctx(), note).FontSize(12).TextColor(colTextMuted).SingleLine()
		}
	})
}

// note 是菜单里一段灰色说明（不能点）。
func (fm *frostedMenu) note(text string) {
	ui.Text(fm.ctx(), text).FontSize(12).LineHeight(1.5).TextColor(colTextMuted).Padding(4, menuRowPadX, 6, menuRowPadX)
}

func (fm *frostedMenu) separator() {
	ui.Box(fm.ctx()).Height(edgeSpread).Margin(4, 5).Background(colBorderSubtle).Shrink(0)
}

func (fm *frostedMenu) row(label string, o menuItemOpts) ui.Element {
	c := fm.ctx()
	if o.key != "" {
		c.Key(o.key)
	}
	var el ui.Element
	switch {
	case o.isSub:
		el = fm.parts().SubmenuItem()
	case o.roleRadio:
		el = fm.parts().RadioItem(o.checked)
	case o.roleCheck:
		el = fm.parts().CheckItem(o.checked)
	default:
		el = fm.parts().Item()
	}
	accessible := o.a11yLabel
	if accessible == "" {
		accessible = label
	}
	el.Gap(menuRowGap).Radius(menuRowRad).Shrink(0).Label(accessible)
	if o.detail == "" {
		el.Height(menuRowH).Padding(0, menuRowPadX).AlignItems(ui.Center)
	} else {
		// 带说明的行按内容长高：说明可能折成几行。不能先设固定高再改——
		// Height(0) 是「高 0」不是「自动」，会被 MinHeight 撑成一行高，说明溢出到下一项。
		el.MinHeight(menuRowH).Padding(7, menuRowPadX).AlignItems(ui.Start)
	}
	if o.disabled {
		el.Disabled(true)
	}
	if o.keepOpen {
		el.KeepOpen()
	}

	fg := colTextPrimary
	switch {
	case o.disabled:
		fg = colTextDisabled
	case o.danger:
		fg = colDangerText
	case o.muted && !el.Highlighted():
		fg = colTextSecondary
	}
	el.TextColor(fg)

	switch {
	case o.disabled:
	case el.Highlighted() && o.danger:
		el.Background(colDangerBg)
	case el.Highlighted():
		el.Background(colMenuHover)
	case o.activeBg && o.checked:
		el.Background(colInkHover)
	}

	hasLead := o.badge != nil || o.icon != nil || o.checkMark || fm.padLead
	iconFg := colTextBody
	if o.danger {
		iconFg = colDangerText
	} else if o.disabled {
		iconFg = colTextDisabled
	}

	el.Children(func() {
		if hasLead {
			switch {
			case o.badge != nil:
				o.badge(c)
			case o.icon != nil:
				ui.Box(c).Size(menuIconSize, menuIconSize).Center().Shrink(0).TextColor(iconFg).Children(func() {
					ui.Icon(c, o.icon).Size(15, 15)
				})
			case o.checkMark && !o.trailCheck && o.checked:
				ui.Box(c).Size(menuIconSize, menuIconSize).Center().Shrink(0).TextColor(colTextPrimary).Children(func() {
					ui.Icon(c, iconCheck).Size(13, 13)
				})
			default:
				ui.Box(c).Size(menuIconSize, menuIconSize).Shrink(0)
			}
		}

		if o.detail != "" {
			ui.Column(c).Grow(1).MinWidth(0).Gap(2).Children(func() {
				ui.Text(c, label).FontSize(menuFontSize).SingleLine()
				ui.Text(c, o.detail).FontSize(12).LineHeight(1.4).TextColor(colTextMuted)
			})
		} else {
			ui.Text(c, label).FontSize(menuFontSize).SingleLine().Grow(1).MinWidth(0)
		}

		if o.subtitle != "" {
			ui.Text(c, o.subtitle).FontSize(12).TextColor(colTextMuted).SingleLine().Shrink(0)
		}
		if o.trailing != "" {
			ui.Text(c, o.trailing).FontSize(menuSubSize).TextColor(colTextMuted).SingleLine().Shrink(0)
		}
		if o.shortcut != "" {
			ui.Text(c, o.shortcut).FontSize(menuSubSize).TextColor(colTextMuted).SingleLine().Shrink(0)
		}
		if o.trailCheck && o.checked {
			ui.Icon(c, iconCheck).Size(13, 13).TextColor(colTextPrimary).Shrink(0)
		}
		if o.isSub {
			ui.Icon(c, iconChevronRight).Size(12, 12).TextColor(colTextMuted).Shrink(0)
		}
	})
	return el
}
