package main

import (
	"slices"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"

	"skillmanager/internal/library"
	"skillmanager/internal/phonetic"
	"skillmanager/locales"
)

// ⌘K 搜索弹窗（画板「搜索与筛选」sf3）：任何页面都能呼出，搜所有来源技能
// 的名称、简介和正文，也能跳到整理、更新、设置这些地方。
//
// 一行一个结果：图标、名称、一句灰色说明，右边最多一个很淡的信息（「我的」
// 不标来源，其他才标）。不按命中的字段分组，按相关度排；只有技能和操作都
// 命中时才分两组。选中行右边是 ↵，按住 ⌘ 换成「在列表中显示」。

type paletteState struct {
	open  bool
	query string
	sel   int  // 选中第几行（所有分组连着数）
	moved bool // 这一帧用方向键换了选中：把它滚进视野

	idx      *library.SearchIndex // 正文索引，后台读好之前为 nil
	building bool                 // 正在后台读正文
	stale    bool                 // 读的时候库又换了，读完再读一次

	hits     []library.SearchHit // 上一次搜索的结果，输入、索引和语言没变就不重搜
	hitsFor  string
	hitsIdx  *library.SearchIndex
	hitsLib  *library.Library
	hitsLang string
}

// paletteAction 是能从弹窗里直接做的事。
type paletteAction struct {
	name     func() string
	words    string // 也能用这些词搜到
	icon     *ui.SVG
	shortcut string
	run      func(a *app)
}

var paletteActions = []paletteAction{
	{name: locales.NavTidy, words: "tidy 问题 副本 链接", //i18n:ignore 搜索别名，中英文都能搜到
		icon: iconNavTidy, run: func(a *app) { a.goTo(scopeTidy) }},
	{name: locales.NavUsage, words: "usage 统计 没用过", //i18n:ignore 搜索别名
		icon: iconNavUsage, run: func(a *app) { a.goTo(scopeUsage) }},
	{name: locales.PaletteCheckUpdates, words: "update skill updates 更新 技能更新 升级", //i18n:ignore 搜索别名
		icon: iconRefresh, run: func(a *app) {
			a.goTo(scopeUpdates)
			a.checkUpdates(false)
		}},
	{name: locales.NavAddProjectEllipsis, words: "project 文件夹 目录", //i18n:ignore 搜索别名
		icon: iconFolderAdd, run: func(a *app) { a.chooseProjects(true) }},
	{name: locales.NavSettings, words: "settings 偏好 代理 agent", //i18n:ignore 搜索别名
		icon: iconSettings, shortcut: "⌘ ,", run: func(a *app) { a.openSettings() }},
}

func (p paletteAction) matches(terms []string) bool {
	text := strings.ToLower(p.name() + " " + p.words)
	for _, t := range terms {
		if !strings.Contains(text, t) {
			return false
		}
	}
	return true
}

// paletteItem 是弹窗里的一行：一个技能或一个操作。
type paletteItem struct {
	hit  library.SearchHit // 技能：Skill 不为 nil
	act  *paletteAction
	meta string // 右边淡淡的信息：来源、打开的时间、快捷键
}

type paletteSection struct {
	title string // 空的不画标题
	items []paletteItem
}

// maxPaletteHits 是最多列出几个技能：再多就该多打几个字了。
const maxPaletteHits = 50

// openPalette 是 ⌘K：再按一次关掉。
func (a *app) openPalette() {
	if a.pal.open {
		a.closePalette()
		return
	}
	a.pal.open, a.pal.query, a.pal.sel = true, "", 0
	if a.pal.idx == nil {
		a.buildSearchIndex()
	}
}

func (a *app) closePalette() { a.pal.open = false }

// goTo 换到侧边栏的一页，像点了那一项。
func (a *app) goTo(scope string) {
	a.scope = scope
	a.selected, a.reading, a.coll, a.inColl, a.cmp = nil, nil, nil, nil, nil
	a.exitBatch()
}

// buildSearchIndex 在后台读各技能的正文。扫描换了库之后再调一次，没改过
// 的文件沿用上次读的。
func (a *app) buildSearchIndex() {
	if a.pal.building {
		a.pal.stale = true
		return
	}
	a.pal.building = true
	sources, prev := a.lib.SearchSources(), a.pal.idx
	a.background(func(post func(func())) {
		idx := library.BuildSearchIndex(sources, prev)
		post(func() {
			a.pal.idx, a.pal.building = idx, false
			if a.pal.stale {
				a.pal.stale = false
				a.buildSearchIndex()
			}
		})
	})
}

// paletteSections 是弹窗现在列出的行：没输入时是最近打开的技能和跳转，
// 输入后是命中的技能和操作。
func (a *app) paletteSections(now time.Time) []paletteSection {
	terms := library.SearchTerms(a.pal.query)
	if len(terms) == 0 {
		var recent []paletteItem
		for _, r := range a.lib.Settings.RecentSkills {
			if s := a.lib.ByID(r.ID); s != nil && len(recent) < 5 {
				recent = append(recent, paletteItem{hit: library.SearchHit{Skill: s}, meta: openedAgo(r.At, now)})
			}
		}
		var acts []paletteItem
		for i := range paletteActions {
			acts = append(acts, paletteItem{act: &paletteActions[i], meta: paletteActions[i].shortcut})
		}
		if len(recent) == 0 {
			return []paletteSection{{title: locales.PaletteNavigate(), items: acts}}
		}
		return []paletteSection{{title: locales.CommonRecent(), items: recent}, {title: locales.PaletteNavigate(), items: acts}}
	}

	lang := a.uiLanguage()
	if a.pal.hitsFor != a.pal.query || a.pal.hitsIdx != a.pal.idx || a.pal.hitsLib != a.lib || a.pal.hitsLang != lang {
		a.pal.hits = a.lib.Search(a.pal.idx, a.pal.query, searchPhonetic(lang))
		a.pal.hitsFor, a.pal.hitsIdx, a.pal.hitsLib, a.pal.hitsLang = a.pal.query, a.pal.idx, a.lib, lang
	}
	var skills, acts []paletteItem
	for _, h := range a.pal.hits[:min(len(a.pal.hits), maxPaletteHits)] {
		skills = append(skills, paletteItem{hit: h, meta: sourceNote(h.Skill)})
	}
	for i := range paletteActions {
		if paletteActions[i].matches(terms) {
			acts = append(acts, paletteItem{act: &paletteActions[i], meta: paletteActions[i].shortcut})
		}
	}
	switch {
	case len(acts) == 0:
		return []paletteSection{{items: skills}}
	case len(skills) == 0:
		return []paletteSection{{items: acts}}
	case a.pal.hits[0].Field == library.MatchName:
		// 两种都有时，最佳结果更准的那组在前：技能的名称命中了就技能在前
		return []paletteSection{{title: locales.CommonSkills(), items: skills}, {title: locales.CommonActions(), items: acts}}
	}
	return []paletteSection{{title: locales.CommonActions(), items: acts}, {title: locales.CommonSkills(), items: skills}}
}

// uiLanguage 是界面显示的语言：设置里选的；跟随系统时是简体中文，因为
// 界面现在只有这一种。做多语言时改成 mygo 按系统解析出的语言
// （mygo.App.Locale），搜索的读法会跟着换。
func (a *app) uiLanguage() string {
	if l := a.lib.Settings.Language; l != "" {
		return l
	}
	return "zh-Hans"
}

// searchPhonetic 是这种语言按读音搜索的读法：简体中文是拼音，没有的为 nil。
func searchPhonetic(lang string) *library.Phonetic {
	if read := phonetic.ForLanguage(lang); read != nil {
		return &library.Phonetic{Lang: lang, Read: read}
	}
	return nil
}

// sourceNote 是行右边的来源：「我的」不标，其他才标。
func sourceNote(s *library.Skill) string {
	switch s.Source {
	case library.Builtin:
		return locales.SourceBuiltin()
	case library.ViaPlugin:
		return locales.PalettePlugin(s.Plugin)
	case library.Project:
		return s.Project
	}
	return ""
}

// openedAgo 是「刚刚」「2 小时前」「昨天」「3 天前」。
func openedAgo(t, now time.Time) string {
	switch d := now.Sub(t); {
	case d < time.Minute:
		return locales.CommonJustNow()
	case d < time.Hour:
		return locales.CommonMinutesAgo(int(d.Minutes()))
	case d < 24*time.Hour && t.Day() == now.Day():
		return locales.CommonHoursAgo(int(d.Hours()))
	}
	return lastUsed(t, now)
}

// choosePalette 打开第 i 行：技能进阅读视图（正文命中的打开那个文件），
// reveal 时回到它所在的列表并选中；操作直接做。
func (a *app) choosePalette(it paletteItem, reveal bool) {
	a.closePalette()
	if it.act != nil {
		it.act.run(a)
		return
	}
	s := a.lib.ByID(it.hit.Skill.ID)
	if s == nil {
		return
	}
	a.goTo(a.scopeOf(s))
	// 列表要看得见它：工具栏的搜索词清掉，在集合里的就打开那个集合
	a.query = ""
	if s.Collection != nil {
		a.openSet(s.Collection)
	}
	a.selected, a.anchor = s, s.ID
	if reveal {
		return
	}
	a.open(s)
	if it.hit.File != "" {
		a.file = max(0, slices.IndexFunc(s.Files, func(f library.File) bool { return f.Path == it.hit.File }))
	}
}

// scopeOf 是技能在侧边栏的哪一页。
func (a *app) scopeOf(s *library.Skill) string {
	switch s.Source {
	case library.Builtin:
		return string(library.ScopeBuiltin)
	case library.Project:
		if p := a.lib.Project(s.ProjectDir); p != nil {
			return projectScope(p)
		}
	}
	return string(library.ScopeMine)
}

// ——— 画 ———

var paletteTransition = ui.ElementTransition{
	Duration:     160 * time.Millisecond,
	Ease:         ui.CubicBezier(0.2, 0.8, 0.2, 1),
	Enter:        &ui.Motion{Opacity: 0, Scale: 0.98},
	Exit:         &ui.Motion{Opacity: 0, Scale: 0.99},
	ExitDuration: 120 * time.Millisecond,
}

func (a *app) palette(c *ui.Context) {
	if !a.pal.open {
		return
	}
	reveal := c.Modifiers()&ui.Cmd != 0

	ui.Overlay(c, func() {
		back := ui.Column(c.Key("palette")).Absolute().Left(0).Top(0).Right(0).Bottom(0).
			AlignItems(ui.Center).Padding(paletteTop(c), 0, 0, 0).Modal()
		back.Children(func() {
			panel := ui.Column(c).Width(600).Shrink(1).Radius(14).Clip().
				Material(paletteGlass{}).
				Border(0.5, ink(0.2)).
				InsetShadow(0, 0.5, 0, 0, rim(0.8)).
				Shadow(0, 24, 64, 0, shadow(0.2)).
				Shadow(0, 3, 10, 0, shadow(0.07)).
				Transition(paletteTransition).Role(ui.RoleDialog).Label(locales.PaletteSearchSkills())
			panel.Children(func() {
				// 先处理输入框，再按改过的输入列结果：同一帧里打字又回车，
				// 打开的是新结果里的那一行
				a.paletteInput(c)
				if a.pal.open {
					a.paletteList(c, a.paletteSections(time.Now()), reveal)
				}
			})
			// 弹窗挡住了窗口的快捷键，⌘K 在这里再接一次：再按一下关掉
			if panel.PressedOutside() || back.OverlayShortcut(0, ui.KeyEscape) || back.OverlayShortcut(ui.Cmd, ui.KeyK) {
				a.closePalette()
			}
		})
	})
}

// paletteTop 是弹窗离窗口顶边多远：在工具栏下面，窗口矮时往上挪。
func paletteTop(c *ui.Context) float32 {
	return toolbarHeight(c) + 56
}

// paletteGlass 是弹窗的材质：后面的内容模糊透出来，再压一层浅灰。
type paletteGlass struct{}

func (paletteGlass) PaintMaterial(p *ui.Painter, box ui.Rect, radii [4]float32) {
	frostedGlass{}.PaintMaterial(p, box, radii)
	p.FillRadii(box, radii, colBgGlass)
}

// paletteItems 是现在列出的行，所有分组连着排；顺带把选中夹到范围里。
func (a *app) paletteItems() []paletteItem {
	var items []paletteItem
	for _, sec := range a.paletteSections(time.Now()) {
		items = append(items, sec.items...)
	}
	a.pal.sel = max(0, min(a.pal.sel, len(items)-1))
	return items
}

func (a *app) paletteInput(c *ui.Context) {
	ui.Row(c).Height(52).Shrink(0).Padding(0, 18).Gap(10).AlignItems(ui.Center).Children(func() {
		ui.Icon(c, iconSearch).Size(18, 18).TextColor(colTextMuted)
		in := ui.TextInputBase(c.Key("palette-input"), &a.pal.query).Placeholder(locales.PaletteSearchSkills()).
			FontSize(18).Grow(1).Label(locales.PaletteSearchSkills()).Search().AutoFocus()
		if in.Changed() {
			a.pal.sel, a.pal.moved = 0, true // 回到第一行，翻下去了也滚回来
		}
		items := a.paletteItems()
		n := len(items)
		switch {
		case n == 0:
		case in.Shortcut(0, ui.KeyDown):
			a.pal.sel, a.pal.moved = (a.pal.sel+1)%n, true
		case in.Shortcut(0, ui.KeyUp):
			a.pal.sel, a.pal.moved = (a.pal.sel-1+n)%n, true
		case in.Shortcut(ui.Cmd, ui.KeyEnter):
			a.choosePalette(items[a.pal.sel], true)
		case in.Submitted():
			a.choosePalette(items[a.pal.sel], false)
		}
	})
}

func (a *app) paletteList(c *ui.Context, sections []paletteSection, reveal bool) {
	if len(sections) == 0 || len(sections) == 1 && len(sections[0].items) == 0 {
		ui.Column(c).Padding(34, 20, 36, 20).AlignItems(ui.Center).
			BorderWidth(0.5, 0, 0, 0).BorderColor(ink(0.1)).Children(func() {
			ui.Text(c, locales.PaletteNoResults(strings.TrimSpace(a.pal.query))).FontSize(13).TextColor(colTextMuted).SingleLine()
		})
		return
	}
	moved := a.pal.moved
	a.pal.moved = false
	ui.Scroll(c.Key("palette-list")).MaxHeight(paletteListHeight).
		BorderWidth(0.5, 0, 0, 0).BorderColor(ink(0.1)).Children(func() {
		ui.Column(c).Padding(6).Children(func() {
			i := 0
			for si, sec := range sections {
				if sec.title != "" {
					top := float32(8)
					if si > 0 {
						top = 14
					}
					ui.Text(c, sec.title).FontSize(11).FontWeight(600).TextColor(colTextMuted).
						Padding(top, 10, 4, 10).SingleLine()
				}
				for _, it := range sec.items {
					row := a.paletteRow(c.Key(i), it, i == a.pal.sel, reveal)
					if i == a.pal.sel && moved {
						row.ScrollIntoView()
					}
					if row.Clicked() {
						a.choosePalette(it, reveal)
					}
					i++
				}
			}
		})
	})
}

// paletteListHeight 是结果最多占多高：再多就滚动。
const paletteListHeight = 420

func (a *app) paletteRow(c *ui.Context, it paletteItem, sel, reveal bool) ui.Element {
	name := ""
	if it.act != nil {
		name = it.act.name()
	} else {
		name = it.hit.Skill.Name
	}
	row := ui.ButtonBase(c).Height(34).Padding(0, 10).Gap(10).Radius(7).AlignItems(ui.Center).
		TabStop(false).FocusRing(false).Label(name)
	switch {
	case sel:
		row.Background(ink(0.07))
	case row.Hovered():
		row.Background(ink(0.035))
	}
	row.Children(func() {
		ui.Row(c).Size(20, 20).Shrink(0).Center().TextColor(colTextSecondary).Children(func() {
			if it.act != nil {
				ui.Icon(c, it.act.icon).Size(16, 16)
			} else {
				a.paletteSkillIcon(c, it.hit.Skill)
			}
		})
		ui.Text(c, name).FontSize(13.5).TextColor(colTextPrimary).SingleLine().Shrink(0).MaxWidth(260)
		sub := it.hit.Snippet
		if sel && reveal && it.act == nil {
			sub = a.lib.Display(it.hit.Skill.Dir)
		}
		paletteSub(c, sub, it.hit.Marks, sel && reveal)
		switch {
		case sel && reveal && it.act == nil:
			ui.Row(c).Shrink(0).Gap(6).AlignItems(ui.Center).Children(func() {
				ui.Text(c, locales.ActionShowInList()).FontSize(12).TextColor(colTextSecondary).SingleLine()
				keyCap(c, "⌘")
				keyCap(c, "↵")
			})
		case sel:
			keyCap(c, "↵")
		case it.meta != "":
			ui.Text(c, it.meta).FontSize(12).TextColor(colTextDisabled).SingleLine().Shrink(0)
		}
	})
	return row
}

// paletteSub 是名称后面的灰色说明，命中的词加粗变深；path 时是目录。
func paletteSub(c *ui.Context, s string, marks [][2]int, path bool) {
	gray := colTextMuted
	if path || len(marks) == 0 {
		t := ui.Text(c, s).FontSize(12.5).TextColor(gray).SingleLine().Grow(1).Shrink(1).MinWidth(0)
		if path {
			t.FontSize(11.5).Font("monospace")
		}
		return
	}
	var spans []ui.Span
	at := 0
	for _, m := range marks {
		if m[0] > at {
			spans = append(spans, ui.Span{Text: s[at:m[0]]})
		}
		spans = append(spans, ui.Span{Text: s[m[0]:m[1]], Weight: 600, Color: colTextBody})
		at = m[1]
	}
	if at < len(s) {
		spans = append(spans, ui.Span{Text: s[at:]})
	}
	ui.RichText(c, spans...).FontSize(12.5).TextColor(gray).SingleLine().Grow(1).Shrink(1).MinWidth(0)
}

// paletteSkillIcon 是技能的小图标：来源的头像，内置的是 Agent 的图标，
// 项目的是文件夹，其余是文档。
func (a *app) paletteSkillIcon(c *ui.Context, s *library.Skill) {
	switch {
	case a.sourceIconFor(s) != nil:
		ui.Box(c).Size(18, 18).Radius(4.5).Clip().Background(colBgLogo).
			InsetShadow(0, 0, 0, 0.5, ink(0.12)).Children(func() {
			ui.Image(c, a.sourceIconFor(s)).Size(18, 18).Fit(ui.Cover)
		})
	case s.Source == library.Builtin || s.Source == library.ViaPlugin:
		agentBadge(c, s.Provider, 16)
	case s.Source == library.Project:
		ui.Icon(c, iconFolder).Size(16, 16)
	default:
		ui.Icon(c, iconDoc).Size(16, 16)
	}
}

// keyCap 是淡灰底的按键，如 ↵、⌘。
func keyCap(c *ui.Context, key string) {
	ui.Text(c, key).FontSize(11.5).TextColor(colTextSecondary).SingleLine().Shrink(0).
		Height(19).MinWidth(20).Padding(0, 5).Radius(5).Background(ink(0.06)).Center()
}
