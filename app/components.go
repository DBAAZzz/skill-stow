package main

import (
	"fmt"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"

	"skillmanager/internal/library"
	"skillmanager/locales"
)

// 浏览页和项目页共用的组件：卡片、分节标题、看板的列、详情栏里的框、弹窗。

// ——— 卡片 ———

// sectionHeaderHeight 是分组标题行的高度。
const sectionHeaderHeight = 44

const cardPad, cardGap = 20, 12

// 卡片悬停时底部操作的过渡：淡入的时长、开始时在下面多少像素。
const (
	actionsFade = 300 * time.Millisecond
	actionsRise = 4
)

// dotRoom 是裁剪的行给图标外面的东西留的余量：状态圆点（伸出 2 像素）
// 和白底图标的细边。
const dotRoom = 3

// columnsFor 是内容区宽 width 时一行放几张卡片。
func columnsFor(width float32) int {
	return max(1, int((width-2*cardPad+cardGap)/(minCardWidth+cardGap)))
}

// cardArea 是放卡片的滚动区：点在卡片以外的空白处取消选中。卡片按
// width 排，不按滚动区此刻的宽度：详情栏进出时滚动区在变，网格已经是
// 最终的样子（withPane）。
//
// overlay 画在滚动区上面（顶部标题栏），nil 为没有。
func (a *app) cardArea(c *ui.Context, width float32, fn, overlay func()) {
	ui.Column(c).Grow(1).MinHeight(0).Children(func() {
		sc := a.cardScroll()
		scroll := ui.Scroll(c).Grow(1).Scrollbars(false).TrackScroll(&sc.ScrollState)
		a.cardTop = scroll.Bounds().Y
		a.scrolled, sc.lastY = sc.Y-sc.lastY, sc.Y
		scroll.Children(func() {
			area := ui.Column(c).Width(width).Padding(6, cardPad, 20, cardPad)
			area.Children(fn)
			a.deselectOnBlank(area)
		})
		if overlay != nil {
			overlay()
		}
	})
}

// cardGrid 是一组卡片，footer 画每张卡片底部左边的内容。
func (a *app) cardGrid(c *ui.Context, cols int, skills []*library.Skill, footer func(s *library.Skill)) {
	ui.Grid(c).Columns(cols).Gap(cardGap).Margin(0, 0, 10, 0).Transition(moveTransition).Children(func() {
		for i, s := range skills {
			// 外面包一层管换位置，卡片自己的 cardFeel 管悬停，两种过渡的
			// 时长不一样。
			cell := ui.Column(c.Key(cellKey{s.ID, i / cols}))
			cell.Transition(a.cellTransition(cell, s.ID)).Children(func() {
				a.skillCard(c, s, func() { footer(s) })
			})
		}
	})
}

// skillCard 是一张技能卡片（r8 .card）：名称、两行简介，底部左边由 footer
// 画，右边是问题提示。只读的技能是灰底。单击选中，双击阅读。
func (a *app) skillCard(c *ui.Context, s *library.Skill, footer func()) {
	card := ui.Column(c).Padding(12, 14).Radius(10).Cursor(ui.CursorPointer).Label(s.Name).Transition(cardFeel())
	a.noteSlot(card) // 开场的演示卡片往这里落
	switch {
	case a.selected == s:
		card.Background(colBgCard).Shadow(0, 0, 0, 2, colAccent)
	case s.ReadOnly():
		card.Background(colBgSubtle)
		hairline(card, 0.07)
	default:
		card.Background(colBgCard)
		hairline(card, 0.1)
		lift(card, 1, 3, 0.05)
	}
	batch := a.batch.on
	var batchOn, batchCan bool
	if batch {
		batchOn, batchCan = a.batchSkill(card, s)
	}
	hovered := card.Hovered()
	if a.selected != s && hovered && (!batch || batchCan) {
		hairline(card, 0.12)
		lift(card, 2, 6, 0.06)
	}
	if s.AllDisabled() {
		card.Opacity(0.55)
	}
	if batch {
		batchCardStyle(card, card, batchOn, false, batchCan, a.unpickableWhy(s))
	} else {
		a.pick(c, card, s, "skill.card:"+s.ID)
	}
	morph := a.morphing(s)
	card.Children(func() {
		if batch {
			batchCheck(c, batchOn, false, batchCan)
		}
		// 固定标题区高度，让有没有头像的卡片都能对齐简介与底栏。
		// 选择态右上角放勾选框，名称让出位置。
		title := ui.Row(c).Height(28).Gap(8).AlignItems(ui.Center)
		if batch {
			title.Padding(0, 24, 0, 0)
		}
		title.Children(func() {
			if av := a.sourceIconFor(s); av != nil {
				b := ui.Box(c).Size(26, 26).Radius(7).Clip().Background(colBgLogo).Shrink(0).
					Label(locales.SourceAvatar(s.Name))
				hairline(b, 0.16)
				b.Children(func() {
					ui.Image(c, av).Size(26, 26).Fit(ui.Cover)
				})
			}
			// 名称的框只包住字，不撑满这一行：飞到正文大标题时两边按同一个
			// 比例缩放，字不会被拉宽
			name := ui.Text(c, s.Name).FontSize(14).FontWeight(600).SingleLine().MinWidth(0)
			if morph {
				name.TransformOrigin(0, 0).Transition(titleMorph(s))
			}
		})
		// 从阅读视图回来时名称从大标题飞回这里，简介和底栏等它差不多
		// 落定再淡入
		back := a.readerBack == s
		fadeIn(ui.Column(c).Height(36).Margin(4, 0, 0, 0), back, cardSettle).Children(func() {
			descText(c, s).FontSize(12).FixedLineHeight(18).MaxLines(2)
		})
		// 指针停在我的技能上时，底部淡出，换成从下面浮上来的操作（del6-c）
		shown := float32(0)
		if hovered && !s.ReadOnly() && !batch {
			shown = 1
		}
		t := card.Animate("actions", shown, actionsFade)
		bottom := fadeIn(ui.Row(c).Height(18).Margin(10, 0, 0, 0), back, cardSettle)
		bottom.Children(func() {
			// 原来的先淡完（前一半时间），两层重叠得少，不显得乱
			ui.Row(c).Grow(1).MinWidth(0).Gap(4).AlignItems(ui.Center).Opacity(max(0, 1-2*t)).Children(func() {
				// 裁掉放不下的部分，但图标右上角的状态圆点会伸出图标 2 像素，
				// 白底图标的细边画在图标外面：四周留出余量，再用负外边距抵消，
				// 位置不变
				ui.Row(c).Gap(5).AlignItems(ui.Center).MinWidth(0).Shrink(1).Clip().TextColor(colTextMuted).
					Padding(dotRoom).Margin(-dotRoom).Children(footer)
				ui.Spacer(c)
				flags(c, s)
				a.dependNote(c, s)
			})
			if t > 0 || shown > 0 {
				ui.Row(c).Absolute().Top(0).Left(0).Right(0).Height(18).Gap(4).AlignItems(ui.Center).
					Opacity(t).Translate(0, (1-t)*actionsRise).Children(func() { a.cardActions(c, s) })
			}
		})
	})
}

// descText 是卡片上的简介，没写简介时显示浅灰的「没有简介」。
func descText(c *ui.Context, s *library.Skill) ui.Element {
	if s.MissingDescription() {
		return ui.Text(c, locales.CommonNoDescription()).TextColor(colTextDisabled)
	}
	return ui.Text(c, s.Description).TextColor(colTextBody)
}

// sectionHeader 是卡片分节的标题（r8 .sh）：标题、数量、灰色说明（note
// 画，可以带图标），right 放右边。
func sectionHeader(c *ui.Context, title, count string, note, right func()) {
	ui.Row(c).Height(sectionHeaderHeight).Gap(8).AlignItems(ui.Center).Children(func() {
		ui.Text(c, title).FontSize(14).FontWeight(600).SingleLine().Shrink(0)
		ui.Text(c, count).FontSize(12.5).TextColor(colTextMuted).FontFeatures("tnum").SingleLine().Shrink(0)
		if note != nil {
			ui.Row(c).Gap(4).Margin(0, 0, 0, 4).MinWidth(0).Shrink(1).Clip().AlignItems(ui.Center).
				FontSize(11.5).TextColor(colTextMuted).Children(note)
		}
		ui.Spacer(c)
		if right != nil {
			right()
		}
	})
}

func empty(c *ui.Context, text string) {
	ui.Column(c).Grow(1).Center().Children(func() {
		ui.Text(c, text).TextColor(colTextDisabled)
	})
}

// emptyState 是内容区没东西可列时的占位，照 macOS 的 ContentUnavailableView：
// 淡色的大符号、粗体标题、一两行说明，下面可选放按钮。按钮用窗口里普通的
// 白色按钮，不用蓝色实心的，空状态是提示，不是弹窗。整组比正中略靠上，
// 看起来才在中间。
func emptyState(c *ui.Context, glyph *ui.SVG, title, body string, actions func()) {
	ui.Column(c).Grow(1).Center().Padding(24, 32, 56, 32).Children(func() {
		// 成组、以标题为名：旁白把它当一块读，按钮固定在组的最下面
		ui.Column(c).MaxWidth(340).AlignItems(ui.Center).Role(ui.RoleGroup).Label(title).Children(func() {
			ui.Icon(c, glyph).Size(52, 52).TextColor(colTextDisabled)
			ui.Text(c, title).FontSize(17).FontWeight(600).TextColor(colTextSecondary).TextAlign(ui.Center).
				Margin(14, 0, 0, 0)
			if body != "" {
				ui.Text(c, body).FontSize(12.5).LineHeight(1.5).TextColor(colTextMuted).TextAlign(ui.Center).
					Margin(6, 0, 0, 0)
			}
			if actions != nil {
				ui.Row(c).Gap(8).Margin(20, 0, 0, 0).Children(actions)
			}
		})
	})
}

func emptyFiltered(c *ui.Context, onClear func()) {
	emptyState(c, iconEmptyFilter, locales.BrowseNoFilterMatches(), "", func() {
		if dialogButton(c, locales.ActionClearFilters(), false).Clicked() {
			onClear()
		}
	})
}

// emptyBrowse 是浏览页一个技能都列不出来时的占位，按原因分：搜索没结果、
// 筛选没结果、库里本来就没有。canAdd 时（我的技能、全部技能）库里没有就
// 给一个去添加技能页的按钮；只读的范围只说没有。
func (a *app) emptyBrowse(c *ui.Context, canAdd bool, fallback string) {
	filtered := a.currentFilter().active()
	switch query := strings.TrimSpace(a.query); {
	case query != "":
		var clear func()
		if filtered { // 搜索加筛选都没结果：先把筛选去掉可能就有了
			clear = func() {
				if dialogButton(c, locales.ActionClearFilters(), false).Clicked() {
					a.clearFilters()
				}
			}
		}
		emptyState(c, iconEmptySearch, locales.BrowseNoSearchResults(query), locales.BrowseNoSearchResultsHint(), clear)
	case filtered:
		emptyFiltered(c, a.clearFilters)
	case canAdd:
		emptyState(c, iconEmptyTray, locales.BrowseNoSkillsYetTitle(), locales.BrowseNoSkillsYetBody(), func() {
			if dialogButton(c, locales.ActionAddSkills(), false).Clicked() {
				a.openImport()
			}
		})
	default:
		empty(c, fallback)
	}
}

// emptyBoard 是看板一列都排不出来时的占位。nothing 为真时这一页本来就一个
// 技能都没有，原因交给 emptyBrowse 分；否则技能是有的，只是排不进看板（只有
// 插件、内置的，或者列都隐藏了），不能说成搜索没结果或还没有技能。
func (a *app) emptyBoard(c *ui.Context, scope library.Scope, nothing bool) {
	switch {
	case nothing:
		a.emptyBrowse(c, canAddSkills(scope), locales.BrowseBoardDescription())
	case a.currentFilter().active():
		emptyFiltered(c, a.clearFilters)
	default:
		empty(c, locales.BrowseBoardDescription())
	}
}

// canAddSkills 报告添加的技能会不会出现在 scope 里：我的技能、全部技能会，
// 内置技能不会。集合页自己传 false，不走这里。
func canAddSkills(scope library.Scope) bool {
	return scope == library.ScopeMine || scope == library.ScopeAll
}

// ——— 看板 ———

const minColumnWidth, boardGap = 228, 10

// board 是看板的一行列：放得下时各列平分宽度，放不下时每列 minColumnWidth，
// 横向滚动。width 是内容区的宽度。
func (a *app) boardRow(c *ui.Context, columns int, width float32, fn func(columnWidth float32)) {
	fits := float32(columns)*(minColumnWidth+boardGap)-boardGap+28 <= width
	var area ui.Element
	if fits {
		area = ui.Row(c).Grow(1).MinHeight(0).AlignItems(ui.Stretch).Padding(14, 14, 0, 14).Gap(boardGap)
		area.Children(func() { fn(0) })
	} else {
		area = ui.ScrollHorizontal(c).Grow(1).MinHeight(0).AlignItems(ui.Stretch).Scrollbars(false).
			Padding(14, 14, 0, 14).Gap(boardGap)
		area.Children(func() { fn(minColumnWidth) })
	}
	a.deselectOnBlank(area)
}

// boardColumn 是看板的一列（r8-a .col）：列头和可以滚动的卡片。width 为 0 时
// 和其他列平分宽度。
func boardColumn(c *ui.Context, width float32, header, body func()) ui.Element {
	col := ui.Column(c).MinWidth(0).Radius(10, 10, 0, 0).Background(colBgSubtle).Padding(6, 5, 0, 5).Gap(4)
	if width > 0 {
		col.Width(width).Shrink(0)
	} else {
		col.Grow(1).Basis(0)
	}
	col.Children(func() {
		columnHeader(c, header)
		// 内边距 2：选中卡片的蓝圈画在卡片外，不留空会被滚动区裁掉
		ui.Scroll(c).Grow(1).Scrollbars(false).Children(func() {
			ui.Column(c).Gap(6).Padding(2, 2, 10, 2).Children(body)
		})
	})
	return col
}

// 项目看板的列宽。项目通常只有一两个技能目录，列不铺满。
const fitColumnWidth = 280

// fitBoard 是列高随内容的看板（p3-b）：列宽固定，整块上下左右都能滚。
func (a *app) fitBoard(c *ui.Context, fn func()) {
	area := ui.ScrollBoth(c).Grow(1).MinHeight(0).Scrollbars(false)
	area.Children(func() {
		ui.Row(c).Padding(14).Gap(boardGap).AlignItems(ui.Start).Children(fn)
	})
	a.deselectOnBlank(area)
}

// fitColumn 是 fitBoard 里的一列：圆角的灰底，高度随卡片。
func fitColumn(c *ui.Context, header, body func()) {
	ui.Column(c).Width(fitColumnWidth).Shrink(0).Radius(10).Background(colBgSubtle).Padding(6, 5, 5, 5).Gap(4).Children(func() {
		columnHeader(c, header)
		ui.Column(c).Gap(6).Padding(2).Children(body)
	})
}

func columnHeader(c *ui.Context, header func()) {
	ui.Row(c).Height(30).Shrink(0).Padding(0, 7).Gap(7).AlignItems(ui.Center).Children(header)
}

// columnTitle 是列头的标题和数量。
func columnTitle(c *ui.Context, title string, count int) {
	ui.Text(c, title).FontSize(12.5).FontWeight(600).SingleLine().Shrink(1).MinWidth(0)
	ui.Text(c, fmt.Sprint(count)).FontSize(12).TextColor(colTextMuted).FontFeatures("tnum")
}

// boardCard 是看板里的小卡片：名称、lines 行简介，底部一行由 footer 画。
// dashed 是虚线框（未启用列）；colKey 区分不同列里的同一张技能，避免菜单状态共用。
func (a *app) boardCard(c *ui.Context, s *library.Skill, colKey string, lines int, dashed bool, footer func()) {
	card := ui.Column(c).Padding(8, 10, 9, 10).Radius(8).Cursor(ui.CursorPointer).Label(s.Name)
	switch {
	case a.selected == s:
		card.Background(colBgCard).Shadow(0, 0, 0, 2, colAccent)
	case dashed:
		card.Border(1, colBorderStrong).BorderStyle(ui.BorderDashed)
	default:
		card.Background(colBgCard)
		hairline(card, 0.09)
		lift(card, 1, 2, 0.05)
	}
	a.pick(c, card, s, "board.card:"+colKey+":"+s.ID)
	card.Children(func() {
		ui.Text(c, s.Name).FontSize(12.5).FontWeight(500).FixedLineHeight(17).SingleLine()
		desc := descText(c, s).FontSize(11.5).FixedLineHeight(16.5).Margin(1, 0, 0, 0)
		if lines > 1 {
			desc.MaxLines(lines)
		} else {
			desc.SingleLine()
		}
		ui.Row(c).Height(14).Margin(7, 0, 0, 0).Gap(3).AlignItems(ui.Center).Children(footer)
	})
}

// styleDashedButton 给列尾的虚线触发按钮（如「添加更多技能」）统一视觉。
func styleDashedButton(c *ui.Context, b ui.Element, open bool, label string) {
	b.Height(32).Radius(8).Border(1, colBorderStrong).BorderStyle(ui.BorderDashed).
		Gap(5).Center().TextColor(colTextMuted).Label(label)
	if open {
		b.Background(ink(0.045))
	} else {
		tintBg(b, ink(0), ink(0.025), ink(0.06))
	}
	b.Children(func() {
		ui.Icon(c, iconPlus).Size(12, 12)
		ui.Text(c, label).FontSize(12)
	})
}

// ——— 详情栏 ———

// paneHeader 是详情栏顶部：名称、简介、来源（带图标）。menu 非 nil 时名称
// 右边有「…」按钮，点开是这个菜单（del1-b）。「我的」技能带安装记录时，
// 名称左侧放来源头像/站标（src2-b），来源行也用它替换原来的通用图标。
func (a *app) paneHeader(c *ui.Context, s *library.Skill, originIcon *ui.SVG, menu func(fm *frostedMenu)) {
	av := a.sourceIconFor(s)
	ui.Row(c).Gap(11).AlignItems(ui.Center).Children(func() {
		if av != nil {
			ui.Box(c).Size(44, 44).Radius(10).Clip().Background(colBgLogo).Shrink(0).Children(func() {
				ui.Image(c, av).Size(44, 44).Fit(ui.Cover)
			})
		}
		name := ui.Row(c).Gap(8).AlignItems(ui.Center).Grow(1).MinWidth(0)
		if av == nil {
			name.AlignItems(ui.Start)
		}
		name.Children(func() {
			ui.Text(c, s.Name).FontSize(18).FontWeight(600).LetterSpacing(-0.2).Grow(1).MinWidth(0)
			if menu != nil {
				// 视觉对齐：「…」的点和下面内容的右边齐平，悬停的底色伸出去
				a.iconActionMenu(c, "inspector.more", iconMore, locales.ActionMore(), actionIconOpts{
					size: 24, height: 24, marginR: moreInset, alignEnd: true, padLead: true,
				}, menu)
			}
		})
	})
	if !s.MissingDescription() {
		ui.Text(c, s.Description).LineHeight(1.45).TextColor(colTextBody).Margin(4, 0, 0, 0)
	}
	ui.Row(c).Gap(5).Margin(6, 0, 0, 0).AlignItems(ui.Center).TextColor(colTextMuted).Children(func() {
		if av != nil {
			ui.Box(c).Size(16, 16).Radius(5).Clip().Background(colBgLogo).Shrink(0).Children(func() {
				ui.Image(c, av).Size(16, 16).Fit(ui.Cover)
			})
		} else if originIcon != nil {
			ui.Icon(c, originIcon).Size(12, 12)
		}
		ui.Text(c, a.originText(s)).FontSize(11.5)
	})
}

// originText 是来源行的一句话：带安装记录的「我的」技能写明来自哪里
// （antfu/skills、open.feishu.cn），其余沿用 Origin()。
func (a *app) originText(s *library.Skill) string {
	if s.Source == library.Mine {
		if rec, ok := a.lib.InstallRecord(s.Name); ok && rec.Source != "" {
			return locales.SourceFrom(rec.Source)
		}
	}
	return s.Origin()
}

// moreInset 是 24 像素的「…」按钮里，最右边的点离按钮右边的距离（16 像素
// 的图标居中，点画到 24 格里的 19.3）。
const moreInset = 7

// label 是详情栏的分组标题（.lbl）。
func label(c *ui.Context, text string) {
	sectionLabel(c, text).Margin(22, 0, 7, 2)
}

// box 是详情栏里的白色分组框（.box）。
func box(c *ui.Context, fn func()) {
	ui.Column(c).Radius(9).Background(colBgCard).Border(1, colBorderDefault).Clip().Children(fn)
}

// boxRow 是框里的一行，除第一行外上面有一条分隔线。
func boxRow(c *ui.Context, first bool) ui.Element {
	row := ui.Row(c).MinHeight(38).Padding(6, 12).Gap(9).AlignItems(ui.Center)
	if !first {
		row.BorderWidth(1, 0, 0, 0).BorderColor(colBorderSubtle)
	}
	return row
}

// titleGap 是框里一行的名称和下面灰色说明之间的距离。
const titleGap = 3

// titled 是框里一行的两行文字：名称，下面一行灰色说明。
func titled(c *ui.Context, title, sub string) {
	ui.Column(c).Grow(1).MinWidth(0).Gap(titleGap).Children(func() {
		ui.Text(c, title).FontWeight(500).SingleLine()
		if sub != "" {
			ui.Text(c, sub).FontSize(11.5).TextColor(colTextMuted).SingleLine()
		}
	})
}

// caption 是框下面的一行灰色说明（.cap）。
func caption(c *ui.Context, text string) {
	ui.Text(c, text).FontSize(11.5).LineHeight(1.5).TextColor(colTextMuted).Margin(7, 2, 0, 2)
}

// errorLine 是框下面的橙色错误说明。
func errorLine(c *ui.Context, text string) {
	ui.Row(c).Gap(6).AlignItems(ui.Start).Margin(8, 2, 0, 2).TextColor(colWarnText).Children(func() {
		ui.Icon(c, iconWarn).Size(12, 12).Margin(2, 0, 0, 0)
		ui.Text(c, text).FontSize(12).LineHeight(1.45).Grow(1)
	})
}

// callout 是橙色提示框（.callout），返回 action 被点了。
func callout(c *ui.Context, title, body, action string) bool {
	clicked := false
	ui.Column(c).Margin(18, 0, 0, 0).Padding(11, 12).Radius(9).Background(colWarnBg).Border(1, colWarnEdge).Children(func() {
		ui.Row(c).Gap(6).AlignItems(ui.Center).TextColor(colWarnText).Children(func() {
			ui.Icon(c, iconWarn).Size(14, 14)
			ui.Text(c, title).FontSize(12.5).FontWeight(600)
		})
		ui.Text(c, body).FontSize(12).LineHeight(1.5).TextColor(colTextBody).Margin(5, 0, 0, 0)
		if action != "" {
			clicked = linkButton(c, action).Margin(6, 0, 0, 0).AlignSelf(ui.Start).Clicked()
		}
	})
	return clicked
}

// doneBox 是绿色的完成提示（d2-b、f1-d2）：标题，body 写做了什么。
func doneBox(c *ui.Context, title, body string) {
	ui.Column(c).Margin(18, 0, 0, 0).Padding(11, 12).Radius(9).Background(colOKBg).Border(1, colOKEdge).Children(func() {
		ui.Row(c).Gap(6).AlignItems(ui.Center).TextColor(colOKText).Children(func() {
			ui.Icon(c, iconCheck).Size(14, 14)
			ui.Text(c, title).FontSize(12.5).FontWeight(600)
		})
		if body != "" {
			ui.Text(c, body).FontSize(12).LineHeight(1.5).TextColor(colTextBody).Margin(4, 0, 0, 20)
		}
	})
}

// warnLine 是一行橙色提示，右边一个蓝色链接（p2-a .warnline）。返回链接被点了。
func warnLine(c *ui.Context, icon *ui.SVG, text, action string) bool {
	clicked := false
	ui.Row(c).Margin(14, 0, 0, 0).Gap(6).AlignItems(ui.Center).TextColor(colWarnText).Children(func() {
		ui.Icon(c, icon).Size(13, 13)
		ui.Text(c, text).FontSize(12).FontWeight(500).Grow(1)
		clicked = linkButton(c, action).Clicked()
	})
	return clicked
}

// toggle 是 30×18 的开关（.sw）。
func toggle(c *ui.Context, on *bool, name string) ui.Element { return mixedToggle(c, on, false, name) }

// mixedToggle 是开关，mixed 时是「一部分开着」：滑块停在中间，浅蓝底。
// 滑块和底色走同一根弹簧：底色由滑块的位置算出来，两者始终同步；按住时
// 滑块像 macOS 的开关一样略微拉长。
func mixedToggle(c *ui.Context, on *bool, mixed bool, name string) ui.Element {
	const w, h, pad, knob = 30, 18, 2, 14
	sw := ui.SwitchBase(c, on).Size(w, h).Shrink(0).Radius(h / 2).Label(name).Cursor(ui.CursorPointer)
	target := float32(0)
	switch {
	case mixed:
		target = 0.5
	case *on:
		target = 1
	}
	pos := sw.AnimateTo("knob", target, toggleSpring)
	var stretch float32
	if sw.Pressed() && !sw.IsDisabled() {
		stretch = 3
	}
	kw := knob + sw.AnimateTo("press", stretch, toggleSpring)
	sw.Background(toggleTrack(pos))
	sw.Children(func() {
		ui.Box(c).Absolute().Top(pad).Left(pad+pos*(w-2*pad-kw)).Size(kw, knob).Radius(knob/2).Background(white).
			Shadow(0, 1, 2, 0, shadow(0.2))
	})
	return sw
}

// toggleSpring 是开关的弹簧：约 0.28 秒到位，不回弹。
var toggleSpring = ui.Animation{Spring: ui.SpringOptions{Duration: 280 * time.Millisecond}}

var ()

// toggleTrack 是滑块在 pos（0 关、0.5 一部分、1 开）时的底色：关到一部分
// 是灰到浅蓝，一部分到开是浅蓝到蓝。
func toggleTrack(pos float32) ui.Color {
	pos = min(max(pos, 0), 1)
	if pos <= 0.5 {
		return colControlTrack.Mix(colControlMixed, pos*2)
	}
	return colControlMixed.Mix(colAccent, pos*2-1)
}

// ——— 弹窗 ———

// dialog 是居中的全局弹窗（p3-a .modal）：标题、说明，content 放中间，
// 底部右边是按钮。点外面或 Esc 关掉。
func dialog(c *ui.Context, open *bool, title, body string, content, buttons func()) {
	ui.DialogBase(c, open, func(back, panel ui.Element) {
		back.Background(colBgScrim)
		panel.Width(520).Padding(24, 28, 22, 28).Radius(14).Background(colBgCard)
		hairline(panel, 0.18)
		panel.Shadow(0, 24, 60, 0, shadow(0.26))
		panel.Children(func() {
			ui.Text(c, title).FontSize(15).FontWeight(600).FixedLineHeight(21)
			ui.Text(c, body).FontSize(12.5).LineHeight(1.6).TextColor(colTextBody).Margin(6, 0, 0, 0)
			content()
			ui.Row(c).Gap(8).Justify(ui.End).Margin(18, 0, 0, 0).Children(buttons)
		})
	})
}

// dialogButton 是 28 高的按钮；primary 是蓝色的主按钮，只用在弹窗里按回车
// 就执行的那一个，窗口内容里的操作用白色的。
func dialogButton(c *ui.Context, label string, primary bool) ui.Element {
	if primary {
		return filledButton(c, label, colAccent)
	}
	b := ui.ButtonBase(c).Height(28).Padding(0, 14).Radius(7).Center().Label(label)
	tintSolid(b, colBgControl)
	hairline(b, 0.18)
	lift(b, 1, 1.5, 0.08)
	b.Children(func() { ui.Text(c, label).SingleLine().ContentTransition(labelTransition) })
	return b
}

// filledButton 是彩色实心的按钮（弹窗里的确认、删除）。照系统的默认按钮
// 做出能按下去的样子：上亮下暗一点点，顶边一道高光，一圈深色细边，下面
// 1 像素的投影；平的色块会被看成标签。按下时整块变深。
func filledButton(c *ui.Context, label string, color ui.Color) ui.Element {
	b := ui.ButtonBase(c).Height(28).Padding(0, 14).Radius(7).Center().TextColor(white).Label(label)
	base := tint(b, "bg", color, darker(color, 0.06), darker(color, 0.18))
	b.LinearGradient(ui.LinearGradient{From: base.Mix(white, 0.14), To: base, Angle: 180}).
		InsetShadow(0, 0.5, 0, 0, white.Alpha(0.3))
	hairline(b, 0.22)
	lift(b, 1, 1.5, 0.12)
	b.Children(func() { ui.Text(c, label).SingleLine().ContentTransition(labelTransition) })
	return b
}
