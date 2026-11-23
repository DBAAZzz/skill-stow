package main

import (
	"context"
	"fmt"
	"math"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/egoist/mygo/i18n"
	"github.com/egoist/mygo/ui"

	"skillmanager/internal/library"
	"skillmanager/locales"
)

// 云市场页面（doc.xml 第 2 节）：
// 工具栏内根据输入自动切到浏览、搜索或粘贴链接三步；读完进入现有 importPickPage。

type marketMode int

const (
	marketModeBrowse marketMode = iota
	marketModeSearch
	marketModeLink
)

type marketFeedKey struct {
	source library.MarketSource
	view   library.MarketView
	key    string
}

type marketFeedState struct {
	items   []library.MarketItem
	hasMore bool
	page    int
	loading bool
	loaded  bool
	err     error
}

type marketPageState struct {
	client    *library.MarketClient // 测试注入假客户端用；nil 时用 library.MarketClient()
	sourceIdx int                   // 0 skills.sh, 1 ClawHub
	shView    library.MarketView
	shTopic   string
	chView    library.MarketView
	chCat     string

	selected *library.MarketItem
	picked   bool
	keyMoved bool
	focusReq bool

	searchBox ui.Handle
	scroll    ui.ScrollState

	feeds         map[marketFeedKey]*marketFeedState
	extras        *library.MarketExtras
	extrasLoading bool

	searchSeq       int
	searchTimer     *time.Timer
	searchLastInput string
	searchQuery     string
	searchLoading   bool
	searchDone      bool
	searchRes       library.MarketSearchResult
	searchExpand    library.MarketSource
}

func (a *app) marketClient() (*library.MarketClient, bool) {
	if a.mkt.client != nil {
		return a.mkt.client, true
	}
	mc := a.lib.MarketClient()
	// 无窗口测试里若使用 Scan(home) 且未显式注入假客户端，不访问真实公网。
	allowNet := a.live || a.lib.Home == ""
	return mc, allowNet
}

func (a *app) currentMarketSource() library.MarketSource {
	if a.mkt.sourceIdx == 1 {
		return library.MarketClawHub
	}
	return library.MarketSkillsSh
}

func (a *app) currentMarketView() (library.MarketView, string) {
	if a.currentMarketSource() == library.MarketClawHub {
		if a.mkt.chView == "" {
			return library.MarketViewAllTime, ""
		}
		return a.mkt.chView, a.mkt.chCat
	}
	if a.mkt.shView == "" {
		return library.MarketViewAllTime, ""
	}
	return a.mkt.shView, a.mkt.shTopic
}

func (a *app) focusMarketSearch() {
	a.mkt.focusReq = true
}

func (a *app) clearMarketInput() {
	a.stopMarketSearchTimer()
	a.imp.input = ""
	a.imp.err = nil
	a.mkt.searchLastInput = ""
	a.mkt.searchQuery = ""
	a.mkt.searchLoading = false
	a.mkt.searchDone = false
	a.mkt.searchRes = library.MarketSearchResult{}
	a.mkt.searchExpand = ""
	a.mkt.selected = nil
}

func (a *app) stopMarketSearchTimer() {
	if a.mkt.searchTimer != nil {
		a.mkt.searchTimer.Stop()
		a.mkt.searchTimer = nil
	}
}

// classifyMarketInput 按 doc.xml 2.1 判断搜索框处于浏览、链接还是搜索状态。
func classifyMarketInput(raw string) marketMode {
	q := strings.TrimSpace(raw)
	if q == "" {
		return marketModeBrowse
	}
	if marketInputIsSource(q) {
		return marketModeLink
	}
	return marketModeSearch
}

func marketInputIsSource(q string) bool {
	if q == "" {
		return false
	}
	if strings.Contains(q, "://") || strings.HasPrefix(q, "npx ") {
		return true
	}
	if strings.HasPrefix(q, "/") || strings.HasPrefix(q, "~/") {
		return true
	}
	if strings.ContainsAny(q, " \t\r\n") {
		return false
	}
	if strings.Count(q, "/") == 1 {
		return true
	}
	host := q
	if before, _, ok := strings.Cut(q, "/"); ok {
		host = before
	}
	return strings.IndexByte(host, '.') > 0
}

// compactCount 按 2.10 约定缩写列表与组标题数字：小于 10000 写千分位，其余交给 CLDR compact。
func compactCount(n int64) string {
	if n < 10000 {
		return i18n.FormatNumber(uiLocale(), n, i18n.NumberOptions{})
	}
	return i18n.FormatNumber(uiLocale(), n, i18n.NumberOptions{
		Notation:          i18n.CompactShort,
		MaxFractionDigits: 1,
	})
}

func fullCount(n int64) string {
	return i18n.FormatNumber(uiLocale(), n, i18n.NumberOptions{})
}

func marketSourceName(s library.MarketSource) string {
	if s == library.MarketClawHub {
		return "ClawHub" //i18n:ignore 产品名
	}
	return "skills.sh" //i18n:ignore 产品名
}

// marketPage 画云市场页面（浏览、搜索、粘贴链接三步）。
func (a *app) marketPage(c *ui.Context) {
	a.syncMarketSearchState()
	mode := classifyMarketInput(a.imp.input)
	if a.imp.cancel != nil {
		mode = marketModeLink
	}

	if mode == marketModeBrowse {
		a.ensureMarketExtras()
		src := a.currentMarketSource()
		view, key := a.currentMarketView()
		a.ensureMarketFeed(src, view, key)
	}

	a.marketToolbar(c, mode)

	if mode == marketModeLink {
		w, _ := c.Size()
		ui.Scroll(c).Grow(1).MinHeight(0).Background(colBgPage).Scrollbars(false).Children(func() {
			pageColumn(c, w-a.sidebarRoom(), formMaxWidth, func(float32) { a.marketLinkBody(c) })
		})
		return
	}

	if mode == marketModeBrowse {
		if a.imp.clip != "" && strings.TrimSpace(a.imp.input) == "" {
			ui.Column(c).Background(colBgCard).BorderWidth(0, 0, 1, 0).BorderColor(colBorderSubtle).Children(func() {
				a.importClipRow(c)
			})
		}
		a.marketChipBar(c)
	}

	a.marketMainWithPane(c, mode)
}

func (a *app) syncMarketSearchState() {
	m := &a.mkt
	if a.imp.input == m.searchLastInput {
		return
	}
	m.searchLastInput = a.imp.input
	m.stopSearchTimerIfNeeded()
	mode := classifyMarketInput(a.imp.input)
	if mode != marketModeSearch {
		m.searchSeq++
		m.searchQuery = ""
		m.searchLoading = false
		m.searchDone = false
		m.searchRes = library.MarketSearchResult{}
		m.searchExpand = ""
		if mode == marketModeLink {
			m.selected = nil
		}
		return
	}
	q := strings.TrimSpace(a.imp.input)
	if q == m.searchQuery && m.searchDone {
		return
	}
	m.searchSeq++
	seq := m.searchSeq
	m.searchQuery = q
	m.searchExpand = ""
	m.selected = nil
	m.searchDone = false
	m.searchLoading = true

	fire := func() {
		a.executeMarketSearch(seq, q)
	}
	if a.delay != nil {
		a.delay(300*time.Millisecond, fire)
		return
	}
	m.searchTimer = time.AfterFunc(300*time.Millisecond, func() {
		a.post(fire)
	})
}

func (m *marketPageState) stopSearchTimerIfNeeded() {
	if m.searchTimer != nil {
		m.searchTimer.Stop()
		m.searchTimer = nil
	}
}

func (a *app) triggerMarketSearchNow(q string) {
	q = strings.TrimSpace(q)
	if q == "" {
		return
	}
	m := &a.mkt
	m.stopSearchTimerIfNeeded()
	m.searchSeq++
	seq := m.searchSeq
	m.searchQuery = q
	m.searchLoading = true
	m.searchDone = false
	a.executeMarketSearch(seq, q)
}

func (a *app) executeMarketSearch(seq int, q string) {
	m := &a.mkt
	if seq != m.searchSeq || classifyMarketInput(a.imp.input) != marketModeSearch || strings.TrimSpace(a.imp.input) != q {
		return
	}
	mc, allowNet := a.marketClient()
	if !allowNet {
		m.searchLoading = false
		m.searchDone = true
		return
	}
	lib := a.lib
	a.background(func(post func(func())) {
		res := mc.SearchMarket(context.Background(), q)
		post(func() {
			if seq != a.mkt.searchSeq {
				return
			}
			lib.MarkInstalled(res.SkillsSh)
			lib.MarkInstalled(res.ClawHub)
			a.mkt.searchRes = res
			a.mkt.searchLoading = false
			a.mkt.searchDone = true
			a.scheduleMarketIcons(res.SkillsSh)
			a.scheduleMarketIcons(res.ClawHub)
		})
	})
}

func (a *app) marketToolbar(c *ui.Context, mode marketMode) {
	st := &a.imp
	reading := st.cancel != nil
	a.toolbarRow(c, func() {
		ui.Text(c, locales.MarketTitle()).FontSize(13).FontWeight(600).SingleLine().Shrink(0)
	}, func() {
		a.marketSearchGroup(c, mode, reading)
		ui.Spacer(c)
		if mode == marketModeBrowse {
			prev := a.mkt.sourceIdx
			seg := segmentedOptions(c.Key("market-source"), &a.mkt.sourceIdx, 84,
				segmentOption{label: "skills.sh", icon: iconSkillsSh}, //i18n:ignore 产品名
				segmentOption{label: "ClawHub", icon: iconClawHub},    //i18n:ignore 产品名
			)
			seg.Shrink(0).Label(locales.CommonSource())
			if a.mkt.sourceIdx != prev {
				a.switchMarketSource(a.mkt.sourceIdx)
			}
		}
		recent := a.lib.RecentImports(6)
		a.iconActionMenu(c, "market.recent", iconClock, locales.ImportRecentlyAdded(), actionIconOpts{
			size:     28,
			height:   28,
			alignEnd: true,
			width:    250,
			disabled: len(recent) == 0,
			tooltip:  locales.ImportRecentlyAdded(),
		}, func(fm *frostedMenu) {
			for _, r := range recent {
				r := r
				ic := iconGitHub
				switch r.Source.Type {
				case "local":
					ic = iconFolder
				case "well-known":
					ic = iconGlobe
				case "clawhub":
					ic = iconClawHub
				}
				if fm.action(r.Name, withKey(r.Place), withDetail(r.Label(a.lib.Home)), withIcon(ic)) {
					a.showImported(r.Name, r.Project, r.Place)
				}
			}
			fm.separator()
			if fm.action(locales.ImportViewInUpdates()) {
				a.closeMenusIn(menuWinMain)
				a.goTo(scopeUpdates)
			}
		})
		if iconButton(c, iconFolderAdd, locales.ImportFromComputer()).Size(28, 28).Clicked() {
			a.chooseImport()
		}
	})
}

func (a *app) marketSearchGroup(c *ui.Context, mode marketMode, reading bool) {
	st := &a.imp
	m := &a.mkt

	wrap := ui.Row(c).Grow(1).MinWidth(280).MaxWidth(696).Gap(8).AlignItems(ui.Center)
	if mode != marketModeLink {
		wrap.MaxWidth(620)
	}
	wrap.Children(func() {
		field := ui.Row(c).Grow(1).MinWidth(280).MaxWidth(620).Height(32).Radius(8).Padding(0, 10).Gap(7).
			AlignItems(ui.Center).Transition(ui.ElementTransition{Shadows: true, Colors: true, Duration: hoverIn})
		switch {
		case reading:
			field.Background(colBgSubtle)
			hairline(field, 0.16)
		case field.FocusWithin():
			field.Background(colBgCard).Shadow(0, 0, 0, edgeSpread, colAccent.Alpha(0.7)).Shadow(0, 0, 0, 3.5, colAccent.Alpha(0.22))
		default:
			field.Background(colBgSidebar)
		}
		field.Children(func() {
			ic := iconSearch
			if mode == marketModeLink {
				ic = iconLink
				if src, err := library.ParseImportInput(st.input); err == nil {
					switch src.Kind {
					case library.ImportGitHub:
						ic = iconGitHub
					case library.ImportWebsite:
						ic = iconGlobe
					case library.ImportClawHub:
						ic = iconClawHub
					}
				} else if strings.Contains(st.input, "github") || strings.Count(st.input, "/") == 1 {
					ic = iconGitHub
				}
			}
			ui.Icon(c, ic).Size(14, 14).TextColor(colTextMuted).ContentTransition(labelTransition)
			in := ui.TextInputBase(c.Key("market-input"), &st.input).Bind(&m.searchBox).
				Placeholder(locales.MarketSearchPlaceholder()).FontSize(13).Grow(1).
				Label(locales.MarketTitle()).Disabled(reading)
			if reading {
				in.TextColor(colTextSecondary)
			}
			if m.focusReq {
				m.focusReq = false
				m.searchBox.Focus(c)
				in.SetTextSelection(0, utf8.RuneCountInString(st.input))
			}
			if in.Changed() {
				st.err = nil
			}
			if in.Shortcut(0, ui.KeyEscape) && !reading && st.input != "" {
				a.clearMarketInput()
			}
			if in.Submitted() {
				switch mode {
				case marketModeLink:
					a.startImport(st.input)
				case marketModeSearch:
					a.triggerMarketSearchNow(st.input)
				}
			}
			if st.input == "" {
				badge := ui.Text(c, "⌘F").FontSize(11).TextColor(colTextMuted).Padding(1, 5).Radius(4).Background(colBgCard).Shrink(0)
				hairline(badge, 0.18)
			} else if !reading {
				clr := ui.ButtonBase(c).Size(16, 16).Radius(8).Center().Background(colTextDisabled).
					Label(locales.ActionClearSearch()).Tooltip(locales.ActionClearSearch())
				clr.Children(func() {
					ui.Icon(c, iconClose).Size(10, 10).TextColor(white)
				})
				if clr.Clicked() {
					a.clearMarketInput()
				}
			}
		})

		if mode == marketModeLink {
			switch {
			case reading:
				if dialogButton(c, locales.ActionCancel(), false).Shrink(0).Clicked() {
					a.stopImport()
				}
			case canReadMarketInput(st.input):
				if filledButton(c, locales.ImportRead(), colAccent).Shrink(0).Clicked() {
					a.startImport(st.input)
				}
			default:
				dialogButton(c, locales.ImportRead(), false).Shrink(0).Disabled(true).TextColor(colTextMuted)
			}
		}
	})
}

func canReadMarketInput(raw string) bool {
	src, err := library.ParseImportInput(raw)
	return err == nil && src.Kind != library.ImportUnsupported
}

func (a *app) switchMarketSource(idx int) {
	a.mkt.sourceIdx = idx
	a.mkt.selected = nil
	a.mkt.scroll.Y = 0
	if idx == 1 {
		a.mkt.chView, a.mkt.chCat = library.MarketViewAllTime, ""
		a.ensureMarketFeed(library.MarketClawHub, library.MarketViewAllTime, "")
		return
	}
	a.mkt.shView, a.mkt.shTopic = library.MarketViewAllTime, ""
	a.ensureMarketFeed(library.MarketSkillsSh, library.MarketViewAllTime, "")
}

func (a *app) selectSkillsShTab(view library.MarketView, topic string) {
	a.mkt.sourceIdx = 0
	a.mkt.shView = view
	a.mkt.shTopic = topic
	a.mkt.selected = nil
	a.mkt.scroll.Y = 0
	a.ensureMarketFeed(library.MarketSkillsSh, view, topic)
}

func (a *app) selectClawHubCategory(cat string) {
	a.mkt.sourceIdx = 1
	a.mkt.selected = nil
	a.mkt.scroll.Y = 0
	if cat == "" || cat == "all" {
		a.mkt.chView, a.mkt.chCat = library.MarketViewAllTime, ""
		a.ensureMarketFeed(library.MarketClawHub, library.MarketViewAllTime, "")
		return
	}
	a.mkt.chView, a.mkt.chCat = library.MarketViewCategory, cat
	a.ensureMarketFeed(library.MarketClawHub, library.MarketViewCategory, cat)
}

func (a *app) marketChipBar(c *ui.Context) {
	bar := ui.ScrollHorizontal(c.Key("market-chips")).Height(54).Shrink(0).Scrollbars(false).
		BorderWidth(0, 0, 1, 0).BorderColor(colBorderSubtle)
	bar.Children(func() {
		ui.Row(c).Height(54).Padding(0, 20).Gap(8).AlignItems(ui.Center).Children(func() {
			if a.currentMarketSource() == library.MarketClawHub {
				curView, curCat := a.currentMarketView()
				if marketChip(c, locales.MarketAll(), iconMarketAll, curView == library.MarketViewAllTime || curCat == "").Clicked() {
					a.selectClawHubCategory("")
				}
				ui.Box(c).Size(1, 18).Shrink(0).Margin(0, 4).Background(colBorderDefault)
				for _, cat := range library.ClawHubCategories {
					cat := cat
					if marketChip(c, marketCategoryLabel(cat), nil, curView == library.MarketViewCategory && curCat == cat).Clicked() {
						a.selectClawHubCategory(cat)
					}
				}
				return
			}

			curView, curTopic := a.currentMarketView()
			views := []struct {
				view  library.MarketView
				label string
				icon  *ui.SVG
			}{
				{library.MarketViewAllTime, locales.MarketViewAllTime(), iconMarketAll},
				{library.MarketViewTrending, locales.MarketViewTrending(), iconMarketTrending},
				{library.MarketViewHot, locales.MarketViewHot(), iconMarketHot},
				{library.MarketViewOfficial, locales.MarketViewOfficial(), iconMarketOfficial},
			}
			for _, v := range views {
				v := v
				if marketChip(c, v.label, v.icon, curView == v.view).Clicked() {
					a.selectSkillsShTab(v.view, "")
				}
			}
			if a.mkt.extras != nil && len(a.mkt.extras.Topics) > 0 {
				ui.Box(c).Size(1, 18).Shrink(0).Margin(0, 4).Background(colBorderDefault)
				for _, t := range a.mkt.extras.Topics {
					t := t
					if marketChip(c, marketTopicLabel(t.ID, t.Title), nil, curView == library.MarketViewTopic && curTopic == t.ID).Clicked() {
						a.selectSkillsShTab(library.MarketViewTopic, t.ID)
					}
				}
			}
		})
	})
}

func marketChip(c *ui.Context, label string, ic *ui.SVG, selected bool) ui.Element {
	b := ui.ButtonBase(c).Height(30).Shrink(0).Padding(0, 11).Radius(8).Gap(6).
		AlignItems(ui.Center).Cursor(ui.CursorPointer).Label(label)
	if selected {
		tintSolid(b, colBgFill)
		b.Border(1, colBorderStrong).TextColor(colTextPrimary)
	} else {
		tintSolid(b, colBgCard)
		b.Border(1, colBorderDefault).TextColor(colTextBody)
	}
	b.Children(func() {
		if ic != nil {
			iconCol := colTextSecondary
			if selected {
				iconCol = colTextPrimary
			}
			ui.Icon(c, ic).Size(14, 14).TextColor(iconCol)
		}
		ui.Text(c, label).FontSize(13).FontWeight(500).SingleLine()
	})
	return b
}

func marketTopicLabel(id, title string) string {
	switch id {
	case "react":
		return "React" //i18n:ignore 技术专有名词
	case "nextjs":
		return "Next.js" //i18n:ignore 技术专有名词
	case "design":
		return locales.MarketTopicDesign()
	case "mobile":
		return locales.MarketTopicMobile()
	case "agent-workflows":
		return locales.MarketTopicAgentWorkflows()
	case "databases":
		return locales.MarketTopicDatabases()
	case "testing":
		return locales.MarketTopicTesting()
	case "marketing":
		return locales.MarketTopicMarketing()
	}
	if title != "" {
		return title
	}
	return id
}

func marketCategoryLabel(cat string) string {
	switch cat {
	case "integrations":
		return locales.MarketCategoryIntegrations()
	case "research":
		return locales.MarketCategoryResearch()
	case "automation":
		return locales.MarketCategoryAutomation()
	case "creative":
		return locales.MarketCategoryCreative()
	case "agents":
		return locales.MarketCategoryAgents()
	case "development":
		return locales.MarketCategoryDevelopment()
	case "communication":
		return locales.MarketCategoryCommunication()
	case "knowledge":
		return locales.MarketCategoryKnowledge()
	case "finance":
		return locales.MarketCategoryFinance()
	case "productivity":
		return locales.MarketCategoryProductivity()
	case "security":
		return locales.MarketCategorySecurity()
	case "operations":
		return locales.MarketCategoryOperations()
	case "lifestyle":
		return locales.MarketCategoryLifestyle()
	case "other":
		return locales.MarketCategoryOther()
	}
	return cat
}

func (a *app) ensureMarketExtras() {
	m := &a.mkt
	if m.extras != nil || m.extrasLoading {
		return
	}
	mc, allowNet := a.marketClient()
	if cached := mc.CachedMarketExtras(); cached != nil {
		m.extras = cached
	}
	if !allowNet {
		return
	}
	m.extrasLoading = true
	a.background(func(post func(func())) {
		ex, err := mc.FetchMarketExtras(context.Background())
		post(func() {
			a.mkt.extrasLoading = false
			if err == nil && ex != nil {
				a.mkt.extras = ex
				// 补齐已加载榜单的简介与所属专题。
				for k, f := range a.mkt.feeds {
					if k.source == library.MarketSkillsSh && len(f.items) > 0 {
						mc.MergeMarketExtras(f.items, ex)
					}
				}
				if a.mkt.selected != nil && a.mkt.selected.Source == library.MarketSkillsSh {
					one := []library.MarketItem{*a.mkt.selected}
					mc.MergeMarketExtras(one, ex)
					a.mkt.selected = &one[0]
				}
			}
		})
	})
}

func (a *app) marketFeedState(k marketFeedKey) *marketFeedState {
	if a.mkt.feeds == nil {
		a.mkt.feeds = make(map[marketFeedKey]*marketFeedState)
	}
	f := a.mkt.feeds[k]
	if f == nil {
		f = &marketFeedState{}
		a.mkt.feeds[k] = f
	}
	return f
}

func (a *app) ensureMarketFeed(source library.MarketSource, view library.MarketView, key string) {
	fk := marketFeedKey{source: source, view: view, key: key}
	f := a.marketFeedState(fk)
	if f.loading || f.loaded {
		return
	}
	mc, allowNet := a.marketClient()
	if len(f.items) == 0 {
		if cached := mc.CachedMarketList(source, view, key, 0); cached != nil {
			f.items = cached.Items
			f.hasMore = cached.HasMore
			f.page = 0
			a.lib.MarkInstalled(f.items)
			a.scheduleMarketIcons(f.items)
		}
	}
	if !allowNet {
		f.loaded = true
		return
	}
	a.refreshMarketFeed(fk, 0, false)
}

func (a *app) refreshMarketFeed(fk marketFeedKey, page int, appendPage bool) {
	f := a.marketFeedState(fk)
	if f.loading {
		return
	}
	mc, allowNet := a.marketClient()
	if !allowNet {
		return
	}
	f.loading = true
	if !appendPage {
		f.err = nil
	}
	lib := a.lib
	a.background(func(post func(func())) {
		res, err := mc.FetchMarketList(context.Background(), fk.source, fk.view, fk.key, page)
		post(func() {
			cur := a.marketFeedState(fk)
			cur.loading = false
			cur.loaded = true
			if err != nil {
				if !appendPage && len(cur.items) == 0 {
					cur.err = err
				}
				return
			}
			cur.err = nil
			cur.hasMore = res.HasMore
			cur.page = page
			if a.mkt.extras != nil && fk.source == library.MarketSkillsSh {
				mc.MergeMarketExtras(res.Items, a.mkt.extras)
			}
			lib.MarkInstalled(res.Items)
			if appendPage {
				seen := make(map[string]bool, len(cur.items))
				for _, it := range cur.items {
					seen[string(it.Source)+":"+it.ID] = true
				}
				for _, it := range res.Items {
					id := string(it.Source) + ":" + it.ID
					if !seen[id] {
						cur.items = append(cur.items, it)
						seen[id] = true
					}
				}
			} else {
				cur.items = res.Items
			}
			if a.mkt.selected != nil {
				for i := range cur.items {
					if sameMarketItem(cur.items[i], *a.mkt.selected) {
						cp := cur.items[i]
						a.mkt.selected = &cp
						break
					}
				}
			}
			a.scheduleMarketIcons(res.Items)
		})
	})
}

func (a *app) marketVisibleList(mode marketMode) []library.MarketItem {
	if mode == marketModeBrowse {
		src := a.currentMarketSource()
		view, key := a.currentMarketView()
		return a.marketFeedState(marketFeedKey{src, view, key}).items
	}
	res := a.mkt.searchRes
	switch a.mkt.searchExpand {
	case library.MarketSkillsSh:
		return res.SkillsSh
	case library.MarketClawHub:
		return res.ClawHub
	}
	var out []library.MarketItem
	out = append(out, res.SkillsSh[:min(4, len(res.SkillsSh))]...)
	out = append(out, res.ClawHub[:min(4, len(res.ClawHub))]...)
	return out
}

func (a *app) marketHandleNavigation(c *ui.Context, mode marketMode) {
	if a.windowMenuOpen(menuWinMain) || a.pal.open {
		return
	}
	items := a.marketVisibleList(mode)
	if len(items) == 0 {
		return
	}
	move := func(delta int) {
		idx := -1
		if a.mkt.selected != nil {
			for i := range items {
				if sameMarketItem(items[i], *a.mkt.selected) {
					idx = i
					break
				}
			}
		}
		if idx < 0 {
			if delta > 0 {
				idx = 0
			} else {
				idx = len(items) - 1
			}
		} else {
			idx = (idx + delta + len(items)) % len(items)
		}
		cp := items[idx]
		a.mkt.selected = &cp
		a.mkt.keyMoved = true
	}
	if !a.mkt.searchBox.Focused(c) {
		c.OnWindowShortcut(0, ui.KeyDown, func() { move(1) })
		c.OnWindowShortcut(0, ui.KeyUp, func() { move(-1) })
		if a.mkt.selected != nil {
			ref := a.mkt.selected.ImportRef
			c.OnWindowShortcut(0, ui.KeyEnter, func() { a.startImport(ref) })
		}
	}
}

func (a *app) marketMainWithPane(c *ui.Context, mode marketMode) {
	a.marketHandleNavigation(c, mode)
	// 先拍下本帧开头选中的条目，避免同一帧点击取消选中或点「查看并添加」后空指针解引用。
	var sel library.MarketItem
	pane := a.mkt.selected != nil
	if pane {
		sel = *a.mkt.selected
		a.paneRoom = inspectorWidth + 1
	}
	ui.Row(c).Grow(1).MinHeight(0).AlignItems(ui.Stretch).Children(func() {
		area := ui.Column(c.Key("market-content")).Grow(1).MinWidth(0).Clip().Transition(contentTransition)
		area.Children(func() {
			if mode == marketModeSearch {
				a.marketSearchBody(c, pane)
				return
			}
			a.marketBrowseBody(c, pane)
		})
		if pane {
			ui.Row(c.Key("market-pane")).Shrink(0).AlignItems(ui.Stretch).Transition(paneTransition).Children(func() {
				ui.Box(c).Width(1).Background(colBorderDefault)
				a.marketPane(c, sel, mode == marketModeSearch)
			})
		}
	})
}

func (a *app) marketBrowseBody(c *ui.Context, hasPane bool) {
	src := a.currentMarketSource()
	view, key := a.currentMarketView()
	fk := marketFeedKey{source: src, view: view, key: key}
	f := a.marketFeedState(fk)
	a.lib.MarkInstalled(f.items)

	switch {
	case len(f.items) == 0 && f.loading:
		a.marketCenteredState(c, func() {
			ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
				ui.Spinner(c).Size(13, 13).TextColor(colAccent)
				ui.Text(c, locales.MarketLoading(marketSourceName(src))).FontSize(13).TextColor(colTextSecondary)
			})
		})
		return
	case len(f.items) == 0 && f.err != nil:
		a.marketCenteredState(c, func() {
			ui.Column(c).Gap(8).AlignItems(ui.Center).Children(func() {
				ui.Text(c, locales.MarketLoadFailed(marketSourceName(src))).FontSize(13).TextColor(colTextSecondary)
				if linkButton(c, locales.MarketRetry()).Clicked() {
					f.loaded = false
					a.refreshMarketFeed(fk, 0, false)
				}
			})
		})
		return
	case len(f.items) == 0:
		a.marketCenteredState(c, func() {
			ui.Text(c, locales.MarketEmptyFilter()).FontSize(13).TextColor(colTextSecondary)
		})
		return
	}

	cols := 2
	if hasPane {
		cols = 1
	}
	inTopic := src == library.MarketSkillsSh && view == library.MarketViewTopic
	scroll := ui.Scroll(c.Key("market-scroll")).Grow(1).MinHeight(0).Background(colBgSurface).Scrollbars(false).TrackScroll(&a.mkt.scroll)
	if a.mkt.scroll.MaxY > 0 && a.mkt.scroll.Y >= a.mkt.scroll.MaxY-120 && f.hasMore && !f.loading && view != library.MarketViewTopic {
		a.refreshMarketFeed(fk, f.page+1, true)
	}
	scroll.Children(func() {
		col := ui.Column(c).Padding(8, 20)
		col.Children(func() {
			if inTopic {
				if topic := a.findMarketTopic(key); topic != nil {
					a.marketTopicIntroBanner(c, topic)
				}
			}
			a.marketItemsGrid(c, cols, f.items, inTopic, false)
		})
		a.marketDeselectOnBlank(col)
	})
}

func (a *app) findMarketTopic(id string) *library.MarketTopic {
	if a.mkt.extras == nil {
		return nil
	}
	for i := range a.mkt.extras.Topics {
		if a.mkt.extras.Topics[i].ID == id {
			return &a.mkt.extras.Topics[i]
		}
	}
	return nil
}

func (a *app) marketTopicIntroBanner(c *ui.Context, topic *library.MarketTopic) {
	ui.Column(c).Padding(6, 10, 10, 10).Gap(3).Children(func() {
		ui.Row(c).Gap(8).AlignItems(ui.Baseline).Children(func() {
			ui.Text(c, marketTopicLabel(topic.ID, topic.Title)).FontSize(15).FontWeight(600).SingleLine()
			ui.Text(c, locales.MarketTopicIntro(len(topic.Skills))).FontSize(12).TextColor(colTextMuted).FontFeatures("tnum").SingleLine()
		})
		if topic.Summary != "" {
			ui.Text(c, topic.Summary).FontSize(12.5).LineHeight(1.45).TextColor(colTextSecondary)
		}
	})
}

func (a *app) marketSearchBody(c *ui.Context, hasPane bool) {
	m := &a.mkt
	a.lib.MarkInstalled(m.searchRes.SkillsSh)
	a.lib.MarkInstalled(m.searchRes.ClawHub)

	if m.searchLoading && !m.searchDone {
		a.marketCenteredState(c, func() {
			ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
				ui.Spinner(c).Size(13, 13).TextColor(colAccent)
				ui.Text(c, locales.MarketLoading("skills.sh")).FontSize(13).TextColor(colTextSecondary) //i18n:ignore 产品名
			})
		})
		return
	}

	sh, ch := m.searchRes.SkillsSh, m.searchRes.ClawHub
	shErr, chErr := m.searchRes.SkillsShErr, m.searchRes.ClawHubErr
	if m.searchDone && shErr == nil && chErr == nil && len(sh) == 0 && len(ch) == 0 {
		a.marketCenteredState(c, func() {
			ui.Text(c, locales.MarketNoResults(m.searchQuery)).FontSize(13).TextColor(colTextSecondary)
		})
		return
	}

	cols := 2
	if hasPane {
		cols = 1
	}
	scroll := ui.Scroll(c.Key("market-search-scroll")).Grow(1).MinHeight(0).Background(colBgSurface).Scrollbars(false)
	scroll.Children(func() {
		col := ui.Column(c).Padding(8, 20)
		col.Children(func() {
			if m.searchExpand == "" || m.searchExpand == library.MarketSkillsSh {
				a.marketSearchGroupSection(c, library.MarketSkillsSh, locales.MarketGroupByInstalls(), sh, shErr, cols)
			}
			if m.searchExpand == "" || m.searchExpand == library.MarketClawHub {
				a.marketSearchGroupSection(c, library.MarketClawHub, locales.MarketGroupByDownloads(), ch, chErr, cols)
			}
		})
		a.marketDeselectOnBlank(col)
	})
}

func (a *app) marketSearchGroupSection(c *ui.Context, src library.MarketSource, unitHint string, items []library.MarketItem, err error, cols int) {
	if err == nil && len(items) == 0 {
		return
	}
	expanded := a.mkt.searchExpand == src
	shown := items
	if !expanded && len(items) > 4 {
		shown = items[:4]
	}
	ui.Column(c.Key(string(src))).Children(func() {
		ui.Row(c).Height(34).Padding(0, 10, 6, 10).Gap(8).AlignItems(ui.End).Children(func() {
			ui.Text(c, marketSourceName(src)).FontSize(12).FontWeight(600).TextColor(colTextBody).SingleLine()
			ui.Text(c, unitHint).FontSize(12).TextColor(colTextMuted).SingleLine()
			ui.Spacer(c)
			if err == nil && len(items) > 4 {
				btnLabel := locales.MarketShowAll()
				if expanded {
					btnLabel = locales.ShowLess()
				}
				if linkButton(c, btnLabel).Clicked() {
					if expanded {
						a.mkt.searchExpand = ""
					} else {
						a.mkt.searchExpand = src
					}
				}
			}
		})
		if err != nil {
			ui.Text(c, locales.MarketSourceUnavailable(marketSourceName(src))).FontSize(12.5).TextColor(colTextSecondary).Padding(8, 10)
			return
		}
		a.marketItemsGrid(c, cols, shown, false, true)
	})
}

func (a *app) marketItemsGrid(c *ui.Context, cols int, items []library.MarketItem, inTopic, isSearch bool) {
	keyMoved := a.mkt.keyMoved
	a.mkt.keyMoved = false
	for i := 0; i < len(items); i += cols {
		ui.Row(c.Key(fmt.Sprintf("mrow-%d", i))).Gap(28).AlignItems(ui.Stretch).Children(func() {
			for colIdx := range cols {
				idx := i + colIdx
				if idx >= len(items) {
					ui.Box(c).Grow(1).MinWidth(0)
					continue
				}
				it := items[idx]
				selected := a.mkt.selected != nil && sameMarketItem(*a.mkt.selected, it)
				row := a.marketRow(c.Key(string(it.Source)+":"+it.ID), it, selected, inTopic, isSearch)
				row.Grow(1).MinWidth(0)
				if selected && keyMoved {
					row.ScrollIntoView()
				}
				if row.Clicked() {
					a.mkt.picked = true
					if selected {
						a.mkt.selected = nil
					} else {
						cp := it
						a.mkt.selected = &cp
					}
				}
				if row.DoubleClicked() {
					a.startImport(it.ImportRef)
				}
			}
		})
	}
}

func (a *app) marketRow(c *ui.Context, it library.MarketItem, selected, inTopic, isSearch bool) ui.Element {
	row := ui.ButtonBase(c).Height(62).Padding(0, 10).Radius(9).Gap(12).AlignItems(ui.Center).
		Cursor(ui.CursorPointer).Label(it.Name)
	switch {
	case selected:
		row.Background(colAccentBg).Border(1, colAccentEdge)
	case row.Hovered():
		row.Background(colInkHover.Over(colBgSurface))
	}
	row.Children(func() {
		a.marketItemIcon(c, it, 36, 9, 17)
		ui.Column(c).Grow(1).MinWidth(0).Children(func() {
			ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
				ui.Text(c, it.Name).FontSize(14).FontWeight(600).SingleLine().MaxLines(1).MinWidth(0).Shrink(1)
				if tagSpec, ok := marketTagSpec(it, isSearch); ok {
					tag(c, tagSpec, true)
				}
			})
			if sub := marketRowSubtitle(it, inTopic); sub != "" {
				ui.Text(c, sub).FontSize(12.5).TextColor(colTextSecondary).SingleLine().MaxLines(1).MinWidth(0).Margin(1, 0, 0, 0)
			}
		})
		if len(it.Weekly) >= 2 {
			marketSparkline(c, it.Weekly, 64, 24, 1.5, 2, false)
		} else {
			ui.Box(c).Size(64, 24).Shrink(0)
		}
		a.marketRowCount(c, it, inTopic)
	})
	return row
}

func (a *app) marketRowCount(c *ui.Context, it library.MarketItem, inTopic bool) {
	if inTopic && it.Count == 0 {
		return
	}
	unit := locales.MarketUnitInstalls()
	if it.Metric == library.MarketMetricDownloads {
		unit = locales.MarketUnitDownloads()
	}
	ui.Row(c).MinWidth(72).Shrink(0).Justify(ui.End).AlignItems(ui.Baseline).Gap(3).Children(func() {
		ui.Text(c, compactCount(it.Count)).FontSize(12).TextColor(colTextSecondary).FontFeatures("tnum").SingleLine()
		ui.Text(c, unit).FontSize(11).TextColor(colTextMuted).SingleLine()
	})
}

func marketTagSpec(it library.MarketItem, isSearch bool) (importTagSpec, bool) {
	if it.Installed {
		return importTagSpec{text: locales.MarketTagInstalled(), tone: tagToneOK, icon: symOK}, true
	}
	if it.Official && !isSearch {
		return importTagSpec{text: locales.MarketTagOfficial(), tone: tagToneAccent, icon: symOfficial}, true
	}
	return importTagSpec{}, false
}

func marketRowSubtitle(it library.MarketItem, inTopic bool) string {
	if it.Source == library.MarketClawHub {
		return firstSentence(it.Description)
	}
	if inTopic && strings.TrimSpace(it.What) != "" {
		return strings.TrimSpace(it.What)
	}
	if strings.TrimSpace(it.Description) != "" {
		return strings.TrimSpace(it.Description)
	}
	if it.Repo != "" {
		return it.Repo
	}
	return it.Owner
}

func firstSentence(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	if idx := strings.IndexByte(s, '\n'); idx >= 0 {
		s = strings.TrimSpace(s[:idx])
	}
	best := len(s)
	const zhPeriod = "\u3002" //i18n:ignore 中文句号用于摘要首句切分
	for _, sep := range []string{". ", zhPeriod} {
		if i := strings.Index(s, sep); i >= 0 {
			end := i
			if sep == zhPeriod {
				end += len(sep)
			}
			if end < best {
				best = end
			}
		}
	}
	return strings.TrimSpace(s[:best])
}

func (a *app) marketItemIcon(c *ui.Context, it library.MarketItem, size, radius, glyph float32) {
	var src ui.ImageSource
	fallback := iconSkillsSh
	if it.Source == library.MarketClawHub {
		src = a.srcIcons["clawhub.ai"]
		fallback = iconClawHub
	} else if it.Owner != "" {
		src = a.srcIcons["gh:"+it.Owner]
	}
	bg := colBgControl
	if src != nil {
		bg = colBgLogo
	}
	box := ui.Box(c).Size(size, size).Shrink(0).Radius(radius).Clip().Background(bg).Center()
	hairline(box, 0.16)
	box.Children(func() {
		if src != nil {
			ui.Image(c, src).Size(size, size).Fit(ui.Cover)
			return
		}
		ui.Icon(c, fallback).Size(glyph, glyph).TextColor(colTextPrimary)
	})
}

func (a *app) scheduleMarketIcons(items []library.MarketItem) {
	if a.iconFetch == nil || len(items) == 0 {
		return
	}
	for _, it := range items {
		if it.Source == library.MarketClawHub {
			a.scheduleSourceIcon("clawhub.ai", func(ctx context.Context) error {
				_, err := a.iconFetch.Fetch(ctx, "https://clawhub.ai", "clawhub.ai")
				return err
			})
			continue
		}
		owner := it.Owner
		if owner != "" {
			a.scheduleSourceIcon("gh:"+owner, func(ctx context.Context) error {
				_, err := a.iconFetch.FetchAvatar(ctx, owner)
				return err
			})
		}
	}
}

func (a *app) marketDeselectOnBlank(area ui.Element) {
	if area.Clicked() && !a.mkt.picked {
		a.mkt.selected = nil
	}
	a.mkt.picked = false
}

func (a *app) marketCenteredState(c *ui.Context, fn func()) {
	area := ui.Column(c).Grow(1).MinHeight(0).Background(colBgSurface).Center()
	area.Children(fn)
	a.marketDeselectOnBlank(area)
}

func (a *app) marketPane(c *ui.Context, it library.MarketItem, isSearch bool) {
	ui.Scroll(c.Key("market-pane-scroll")).Width(inspectorWidth).Shrink(0).Background(colBgPane).Scrollbars(false).Children(func() {
		ui.Column(c).Padding(22, 20).Gap(14).Children(func() {
			// 头部：40x40 图标 + 名称 + owner/repo
			ui.Row(c).Gap(12).AlignItems(ui.Center).Children(func() {
				a.marketItemIcon(c, it, 40, 10, 20)
				ui.Column(c).Grow(1).MinWidth(0).Gap(2).Children(func() {
					ui.Text(c, it.Name).FontSize(18).FontWeight(600).SingleLine()
					sub := it.Owner
					if it.Source == library.MarketSkillsSh && it.Repo != "" {
						sub = it.Repo
					}
					if sub != "" {
						ui.Text(c, sub).FontSize(12).TextColor(colTextMuted).SingleLine()
					}
				})
			})
			if tagSpec, ok := marketTagSpec(it, isSearch); ok {
				ui.Row(c).Children(func() { tag(c, tagSpec, true) })
			}
			if desc := strings.TrimSpace(it.Description); desc != "" {
				ui.Text(c, desc).FontSize(13).LineHeight(1.55).TextColor(colTextBody).MaxLines(6)
			}
			ui.Row(c).Gap(8).Children(func() {
				if dialogButton(c, locales.MarketViewAndAdd(), true).Clicked() {
					a.startImport(it.ImportRef)
				}
				if dialogButton(c, locales.MarketOpenOnWeb(), false).Clicked() {
					a.openExternalURL(it.WebURL)
				}
			})

			a.marketPaneDataGroup(c, it)
			a.marketPaneTaxonomyGroup(c, it)
			a.marketPaneLinksGroup(c, it)
		})
	})
}

func (a *app) marketPaneDataGroup(c *ui.Context, it library.MarketItem) {
	curView, _ := a.currentMarketView()
	inTopic := it.Source == library.MarketSkillsSh && curView == library.MarketViewTopic
	var hasRows bool
	if it.Source == library.MarketClawHub {
		hasRows = true
	} else {
		hasRows = len(it.Weekly) >= 2 || (!inTopic || it.Count > 0) || (it.Rank > 0 && curView == library.MarketViewAllTime)
	}
	if !hasRows {
		return
	}
	marketSectionHeading(c, locales.MarketSectionData())
	marketDetailGroup(c, func() {
		first := true
		if it.Source == library.MarketClawHub {
			marketDetailRow(c, locales.MarketDownloads(), fullCount(it.Count), &first)
			marketDetailRow(c, locales.MarketInstalls(), fullCount(it.Installs), &first)
			marketDetailRow(c, locales.MarketStars(), fullCount(it.Stars), &first)
			marketDetailRow(c, locales.MarketVersions(), fullCount(int64(it.Versions)), &first)
			return
		}
		if len(it.Weekly) >= 2 {
			first = false
			ui.Column(c).Padding(10, 12).Gap(2).Children(func() {
				ui.Text(c, locales.MarketWeeklyInstalls()).FontSize(12).TextColor(colTextSecondary).SingleLine()
				ui.Row(c).Gap(12).AlignItems(ui.End).Justify(ui.SpaceBetween).Children(func() {
					curWeek := it.Weekly[len(it.Weekly)-1]
					ui.Text(c, fullCount(curWeek)).FontSize(20).FontWeight(600).FontFeatures("tnum").SingleLine()
					marketSparkline(c, it.Weekly, 132, 36, 1.75, 2.6, true)
				})
			})
		}
		if !inTopic || it.Count > 0 {
			marketDetailRow(c, locales.MarketTotalInstalls(), fullCount(it.Count), &first)
		}
		if it.Rank > 0 && curView == library.MarketViewAllTime {
			marketDetailRow(c, locales.MarketRank(), locales.MarketRankValue(it.Rank), &first)
		}
	})
}

func (a *app) marketPaneTaxonomyGroup(c *ui.Context, it library.MarketItem) {
	if it.Source == library.MarketClawHub {
		if len(it.Categories) == 0 {
			return
		}
		marketSectionHeading(c, locales.MarketSectionCategory())
		marketDetailGroup(c, func() {
			ui.Row(c).MinHeight(36).Padding(8, 12).Gap(8).AlignItems(ui.Center).Children(func() {
				ui.Text(c, locales.MarketClawHubCategory()).FontSize(12.5).TextColor(colTextSecondary).SingleLine()
				ui.Spacer(c)
				ui.Row(c).Gap(0).AlignItems(ui.Center).Shrink(1).Children(func() {
					for i, cat := range it.Categories {
						cat := cat
						if i > 0 {
							ui.Text(c, locales.ListSeparator()).FontSize(12.5).TextColor(colTextSecondary)
						}
						if linkButton(c, marketCategoryLabel(cat)).Clicked() {
							a.clearMarketSearchKeepingBrowse()
							a.selectClawHubCategory(cat)
						}
					}
				})
			})
		})
		return
	}
	if len(it.Topics) == 0 {
		return
	}
	marketSectionHeading(c, locales.MarketSectionTopic())
	marketDetailGroup(c, func() {
		ui.Row(c).MinHeight(36).Padding(8, 12).Gap(8).AlignItems(ui.Center).Children(func() {
			ui.Text(c, locales.MarketSkillsShTopic()).FontSize(12.5).TextColor(colTextSecondary).SingleLine()
			ui.Spacer(c)
			ui.Row(c).Gap(0).AlignItems(ui.Center).Shrink(1).Children(func() {
				for i, tid := range it.Topics {
					tid := tid
					title := tid
					if t := a.findMarketTopic(tid); t != nil {
						title = t.Title
					}
					if i > 0 {
						ui.Text(c, locales.ListSeparator()).FontSize(12.5).TextColor(colTextSecondary)
					}
					if linkButton(c, marketTopicLabel(tid, title)).Clicked() {
						a.clearMarketSearchKeepingBrowse()
						a.selectSkillsShTab(library.MarketViewTopic, tid)
					}
				}
			})
		})
	})
}

func (a *app) clearMarketSearchKeepingBrowse() {
	if strings.TrimSpace(a.imp.input) != "" {
		a.clearMarketInput()
	}
}

func (a *app) marketPaneLinksGroup(c *ui.Context, it library.MarketItem) {
	if it.Source != library.MarketSkillsSh || strings.Count(it.Repo, "/") != 1 {
		return
	}
	marketSectionHeading(c, locales.MarketSectionLinks())
	marketDetailGroup(c, func() {
		ui.Row(c).MinHeight(36).Padding(8, 12).Gap(8).AlignItems(ui.Center).Children(func() {
			ui.Text(c, "GitHub").FontSize(12.5).TextColor(colTextSecondary).SingleLine() //i18n:ignore 品牌名
			ui.Spacer(c)
			btn := ui.ButtonBase(c).Cursor(ui.CursorPointer).Gap(4).AlignItems(ui.Center).Label(it.Repo)
			btn.TextColor(tint(btn, "text", colAccent, colAccent, darker(colAccent, 0.35)))
			btn.Children(func() {
				ui.Text(c, it.Repo).FontSize(12.5).SingleLine()
				ui.Icon(c, iconOutward).Size(12, 12)
			})
			if btn.Clicked() {
				a.openExternalURL("https://github.com/" + it.Repo)
			}
		})
	})
}

func marketSectionHeading(c *ui.Context, text string) {
	sectionLabel(c, text).Margin(2, 0, -8, 2)
}

func marketDetailGroup(c *ui.Context, fn func()) {
	col := ui.Column(c).Radius(8).Background(colBgCard).Clip()
	hairline(col, 0.14)
	col.Children(fn)
}

func marketDetailRow(c *ui.Context, k, v string, first *bool) {
	row := ui.Row(c).MinHeight(36).Padding(8, 12).AlignItems(ui.Center)
	if !*first {
		row.BorderWidth(1, 0, 0, 0).BorderColor(colBorderSubtle)
	}
	*first = false
	row.Children(func() {
		ui.Text(c, k).FontSize(12.5).TextColor(colTextSecondary).SingleLine()
		ui.Spacer(c)
		ui.Text(c, v).FontSize(12.5).FontWeight(500).FontFeatures("tnum").SingleLine()
	})
}

// marketLinkBody 画粘贴链接状态的居中白卡分组：来源识别摘要 + 进度/报错。
func (a *app) marketLinkBody(c *ui.Context) {
	st := &a.imp
	reading := st.cancel != nil
	src, parseErr := library.ParseImportInput(st.input)
	invalid := parseErr != nil || src.Kind == library.ImportUnsupported

	importSection(c, locales.ImportFromLink(), false, nil)
	usageGroup(c).Transition(ui.ElementTransition{Height: true, Animation: &importGrow}).Children(func() {
		if invalid {
			errObj := &library.ImportError{Msg: locales.ImportUnsupportedSource(), Hint: locales.ImportSupportedSourcesHint()}
			if parseErr != nil {
				errObj = &library.ImportError{Msg: parseErr.Error()}
			}
			if st.err != nil {
				errObj = st.err
			}
			a.importErrorRow(c, errObj)
			return
		}
		a.marketLinkSourceRow(c, src)
		switch {
		case reading:
			a.importProgress(c)
		case st.err != nil:
			a.importErrorRow(c, st.err)
		}
	})
	note := locales.ImportReviewBeforeAdding()
	if reading {
		note = locales.ImportDownloadOnce()
	}
	ui.Text(c, note).FontSize(12).LineHeight(1.45).TextColor(colTextSecondary).Padding(7, 4, 0, 4).ContentTransition(labelTransition)
}

func (a *app) marketLinkSourceRow(c *ui.Context, src library.ImportSource) {
	ui.Row(c).MinHeight(54).Padding(10, 12, 10, 14).Gap(10).AlignItems(ui.Center).Children(func() {
		switch src.Kind {
		case library.ImportGitHub:
			squircleIcon(c, iconGitHub, 28, squircleGit)
		case library.ImportWebsite:
			squircleIcon(c, iconGlobe, 28, squircleTeal)
		case library.ImportClawHub:
			a.marketItemIcon(c, library.MarketItem{Source: library.MarketClawHub}, 28, 7, 15)
		default:
			squircleIcon(c, iconNavFolder, 28, squircleBlue)
		}
		ui.Column(c).Grow(1).MinWidth(0).Gap(2).Children(func() {
			ui.Text(c, src.Label()).FontSize(13).FontWeight(500).SingleLine()
			ui.Text(c, marketLinkKindLabel(src)).FontSize(12).TextColor(colTextSecondary).SingleLine()
		})
	})
}

func marketLinkKindLabel(src library.ImportSource) string {
	switch src.Kind {
	case library.ImportClawHub:
		return locales.MarketLinkKindClawHub()
	case library.ImportWebsite:
		return locales.MarketLinkKindWebsite()
	case library.ImportLocal:
		return locales.CommonLocal()
	default:
		if strings.Contains(strings.ToLower(src.Input), "skills.sh") {
			return locales.MarketLinkKindSkillsSh()
		}
		return locales.MarketLinkKindGitHub()
	}
}

func sameMarketItem(a, b library.MarketItem) bool {
	return a.Source == b.Source && a.ID == b.ID
}

type cubicSegment struct {
	x0, y0, c1x, c1y, c2x, c2y, x1, y1 float32
}

// monotoneCubicSegments 按 Fritsch–Carlson 单调三次插值生成平滑且永不冲过数据范围的曲线段。
func monotoneCubicSegments(xs, ys []float32) []cubicSegment {
	n := len(xs)
	if n < 2 {
		return nil
	}
	dx := make([]float32, n-1)
	d := make([]float32, n-1)
	for i := range n - 1 {
		dx[i] = xs[i+1] - xs[i]
		if dx[i] > 0 {
			d[i] = (ys[i+1] - ys[i]) / dx[i]
		}
	}
	m := make([]float32, n)
	m[0] = d[0]
	m[n-1] = d[n-2]
	for i := 1; i < n-1; i++ {
		if d[i-1]*d[i] <= 0 {
			m[i] = 0
			continue
		}
		w1 := 2*dx[i] + dx[i-1]
		w2 := dx[i] + 2*dx[i-1]
		m[i] = (w1 + w2) / (w1/d[i-1] + w2/d[i])
	}
	for i := range n - 1 {
		if d[i] == 0 {
			m[i], m[i+1] = 0, 0
			continue
		}
		alpha := m[i] / d[i]
		beta := m[i+1] / d[i]
		if hyp := float32(math.Hypot(float64(alpha), float64(beta))); hyp > 3 {
			tau := 3 / hyp
			m[i] = tau * alpha * d[i]
			m[i+1] = tau * beta * d[i]
		}
	}
	segs := make([]cubicSegment, n-1)
	for i := range n - 1 {
		step := dx[i] / 3
		segs[i] = cubicSegment{
			x0:  xs[i],
			y0:  ys[i],
			c1x: xs[i] + step,
			c1y: ys[i] + m[i]*step,
			c2x: xs[i+1] - step,
			c2y: ys[i+1] - m[i+1]*step,
			x1:  xs[i+1],
			y1:  ys[i+1],
		}
	}
	return segs
}

func sparklineCoords(pts []int64, r ui.Rect) ([]float32, []float32) {
	n := len(pts)
	pw := max(r.W-6, 1)
	ph := max(r.H-6, 1)
	minV, maxV := slices.Min(pts), slices.Max(pts)
	xs := make([]float32, n)
	ys := make([]float32, n)
	for i, v := range pts {
		xs[i] = r.X + 3 + (float32(i)/float32(n-1))*pw
		if maxV == minV {
			ys[i] = r.Y + 4 + ph/2
		} else {
			ys[i] = r.Y + 4 + ph - float32(v-minV)/float32(maxV-minV)*ph
		}
	}
	return xs, ys
}

func marketSparkline(c *ui.Context, pts []int64, w, h, strokeW, dotR float32, detail bool) {
	if len(pts) < 2 {
		ui.Box(c).Size(w, h).Shrink(0)
		return
	}
	ui.Box(c).Size(w, h).Shrink(0).Draw(func(p *ui.Painter, r ui.Rect) {
		accent := colAccent
		xs, ys := sparklineCoords(pts, r)
		segs := monotoneCubicSegments(xs, ys)
		if len(segs) == 0 {
			return
		}
		line := new(ui.Path).MoveTo(segs[0].x0, segs[0].y0)
		fill := new(ui.Path).MoveTo(segs[0].x0, segs[0].y0)
		for _, s := range segs {
			line.CubeTo(s.c1x, s.c1y, s.c2x, s.c2y, s.x1, s.y1)
			fill.CubeTo(s.c1x, s.c1y, s.c2x, s.c2y, s.x1, s.y1)
		}
		bottom := r.Y + r.H
		fill.LineTo(xs[len(xs)-1], bottom).LineTo(xs[0], bottom).Close()

		p.FillPathGradient(fill, ui.LinearGradient{
			Angle: 180,
			Stops: []ui.GradientStop{
				{Color: accent.Alpha(0.22), At: 0},
				{Color: accent.Alpha(0), At: 1},
			},
		})
		p.StrokePath(line, strokeW, accent)

		lx, ly := xs[len(xs)-1], ys[len(ys)-1]
		if detail {
			p.FillPath(new(ui.Path).Circle(lx, ly, 4.5), accent.Alpha(0.18))
			dot := new(ui.Path).Circle(lx, ly, dotR)
			p.FillPath(dot, accent)
			p.StrokePath(dot, 1.2, white)
			return
		}
		p.FillPath(new(ui.Path).Circle(lx, ly, dotR), accent)
	})
}
