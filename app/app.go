package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"
	"github.com/egoist/mygo/ui/markdown"

	"skillmanager/internal/library"
	"skillmanager/locales"
)

// 浏览时的两种视图，顺序即工具栏分段控件的顺序。
const (
	viewCards = iota
	viewBoard
)

// app 是窗口的全部状态。
type app struct {
	upd    updatePageState
	imp    importPageState // 添加技能页（import_page.go）
	lib    *library.Library
	win    *mygo.Window // 测试里为 nil
	setWin *mygo.Window // 设置窗口，开着时非 nil

	sound     func()           // 放完成提示音，nil 是不放（测试里没有窗口）
	clock     func() time.Time // 提示音限频用的时钟，nil 是 time.Now（测试里拨动）
	lastChime time.Time        // 上一次放提示音的时间（sound.go）

	toasts   toastStack // 内容区顶部的提示条（toast.go）
	paneRoom float32    // 这一帧详情栏占的宽度，没开为 0：提示条在它左边居中

	scope       string // 侧边栏选中的范围（library.Scope）或项目（projectPrefix + 路径）
	mode        int    // viewCards / viewBoard
	query       string
	selected    *library.Skill              // 选中的技能，详情栏显示它
	coll        *library.Collection         // 选中的技能集合，详情栏显示它；和 selected 至多一个非 nil
	inColl      *library.Collection         // 打开的技能集合，非 nil 时浏览页只列它的技能
	reading     *library.Skill              // 正在阅读的技能，非 nil 时显示阅读视图
	file        int                         // 阅读视图打开的文件
	err         string                      // 上一次启用 / 停用失败的原因，显示在详情栏
	errAgent    string                      // err 是拨这个 Agent 的开关时出的，写在那一行下面
	notice      *mergeNotice                // 刚收成一份的技能，详情栏顶部的绿色提示
	cmp         *compareState               // 打开的对比副本页，nil 是没打开
	expanded    map[string]bool             // 全部技能里展开了的节（section.key）
	picked      bool                        // 这一帧点中了卡片，空白处的点击不再取消选中
	anchor      string                      // 最近选中的技能或集合的 ID：网格换列时它滑到新位置，不跟着淡出淡入
	cardTop     float32                     // 卡片滚动区上一帧的顶边（窗口坐标）：顶部标题栏据此判断滚到了哪一组
	cardScrolls map[string]*cardScrollState // 各页各自的卡片滚动位置与上一帧 Y（避免跨页共用 ScrollState）
	scrolled    float32                     // 这一帧比上一帧又往下滚了多少：上一帧的位置减去它才是现在的
	readerIn    bool                        // 刚打开阅读视图：这一帧起正文从透明开始淡入
	readerBack  *library.Skill              // 刚从它的阅读视图回来：这一帧它的卡片从正文收回去
	wasSelected *library.Skill              // 上一次 view 开始前选中的技能（defer 在本轮结束时写入本轮开始前的值）
	pinIdx      int                         // 顶部标题栏正显示第几组，-1 为没显示
	pinDir      float32                     // 顶部标题栏上次换组的方向：1 往下滚，-1 往上滚

	readerRaw       bool
	readerEditor    *editor.State
	readerEditorKey string
	readerImages    map[string]ui.ImageSource

	sideBar      ui.Handle            // 侧边栏，查它有没有焦点
	projectsOpen bool                 // 侧边栏「项目」展开
	sideShown    float32              // 侧边栏这一帧露出的宽度，收起、展开时在动画里
	batch        batchState           // 我的技能的批量删除
	allProjects  bool                 // 「显示全部 N 个」展开
	linked       *library.ProjectInfo // 全局链接弹窗显示的项目
	linkedOpen   bool
	sharedOpen   map[string]bool   // 看板里展开了「通用里单独启用的」的 Agent
	set          settingsState     // 设置弹窗
	tidy         tidyState         // 整理页
	usage        *library.UsageLog // 会话记录里的技能调用，后台读出来之前为 nil
	unusedTab    library.Source    // 使用情况页「没用过的」列哪个来源：我的或内置

	pal           paletteState // ⌘K 搜索弹窗（palette.go）
	del           deleteState
	enableConfirm *enableConfirmation
	collect       *library.Skill          // 待确认收进技能库的项目技能
	filters       map[string]*skillFilter // 浏览页工具栏按作用域保存的筛选条件
	menus         map[string]*menuEntry
	menuBuildWin  menuWin
	collectOpen   bool
	confirmOpen   bool
	md            markdown.Options

	// well-known 来源的站点图标：iconFetch 只在 main 里建，测试里为 nil
	// 时抓取整体不启动；srcIcons 是抓到的图，srcIconBusy 防重复排任务。
	iconFetch   *library.IconFetcher
	srcIcons    map[string]ui.ImageSource
	srcIconBusy map[string]bool

	// bg 替换 background 的做法：测试里停在半路，看进行中的界面
	bg       func(work func(post func(func())))
	openURL  func(string)
	watchCfg *watchConfig
	watch    watchState
}

// background 在后台跑 work；work 改界面状态都通过 post，post 在界面线程
// 上执行并重画。测试里没有窗口，就地跑完。
func (a *app) background(work func(post func(func()))) {
	switch {
	case a.bg != nil:
		a.bg(work)
	case a.win == nil:
		work(func(fn func()) { fn() })
		a.changed()
	default:
		go work(func(fn func()) { a.win.Update(fn) })
	}
}

type cardScrollState struct {
	ui.ScrollState
	lastY float32
}

func (a *app) cardScrollKey() string {
	switch p := a.project(); {
	case p != nil:
		return "project:" + p.Dir
	case a.inColl != nil:
		return "set:" + a.inColl.ID
	default:
		return "browse:" + a.scope
	}
}

func (a *app) cardScroll() *cardScrollState {
	if a.cardScrolls == nil {
		a.cardScrolls = map[string]*cardScrollState{}
	}
	key := a.cardScrollKey()
	s := a.cardScrolls[key]
	if s == nil {
		s = &cardScrollState{}
		a.cardScrolls[key] = s
	}
	return s
}

func newApp(lib *library.Library) *app {
	a := &app{
		lib:          lib,
		scope:        string(library.ScopeMine),
		expanded:     map[string]bool{},
		sharedOpen:   map[string]bool{},
		cardScrolls:  map[string]*cardScrollState{},
		projectsOpen: true,
	}
	a.set.scannedAt = time.Now()
	if lib.Home != "" {
		a.upd.report = lib.Updates()
	}
	return a
}

func (a *app) view(c *ui.Context) {
	prevWin := a.menuBuildWin
	a.menuBuildWin = menuWinMain
	defer func() { a.menuBuildWin = prevWin }()
	a.flushPendingWatch()
	defer func(s *library.Skill) { a.wasSelected = s }(a.selected) // 下一轮 view 用：本轮开始前选中的，点击后同帧重绘时仍为旧值
	theme(c)
	vibrant := c.Vibrancy()
	if vibrant {
		c.Root().Background(ui.Transparent)
	}
	c.OnWindowShortcut(0, ui.KeyEscape, a.back)
	c.OnWindowShortcut(ui.Cmd, ui.KeyComma, a.openSettings)
	c.OnWindowShortcut(ui.Cmd, ui.KeyK, a.openPalette)
	if !a.windowMenuOpen(menuWinMain) {
		a.deleteShortcuts(c)
		c.OnWindowShortcut(0, ui.KeySpace, func() {
			switch {
			case a.reading != nil:
			case a.selected != nil:
				a.open(a.selected)
			case a.coll != nil:
				a.openSet(a.coll)
			}
		})
	}

	if sidebarShortcut != 0 {
		c.OnWindowShortcut(sidebarShortcut, ui.KeyS, a.toggleSidebar)
	}

	a.sideShown = c.Root().AnimateWith("sidebar", a.sidebarRoom(), paneDuration, paneEase)

	ui.Row(c).Fill().AlignItems(ui.Stretch).Children(func() {
		if a.sideShown > 0 {
			// 收起时整栏往左滑出去，内容区跟着变宽（像备忘录）
			ui.Row(c.Key("sidebar")).Width(a.sideShown).Shrink(0).ClipX().Justify(ui.End).AlignItems(ui.Stretch).Children(func() {
				side := ui.Column(c).Width(sidebarWidth).Shrink(0)
				if vibrant {
					side.Background(colBgSideVeil)
				} else {
					side.Background(colBgSidebar)
				}
				side.Children(func() { a.sidebar(c, vibrant) })
				ui.Box(c).Width(1).Shrink(0).Background(colBorderSidebar)
			})
		}
		mainCol := ui.Column(c.Key("main")).Grow(1).MinWidth(0).Background(colBgSurface)
		mainCol.Children(func() {
			key, page := a.page(c)
			// 每页一个键：换页时上一页整个不在了，里面带离场动画的（整理页的
			// 问题行和右边）不会被当成新页里的元素，留在新页上淡出
			host := ui.Column(c).Grow(1).MinHeight(0)
			if key != "reader" && a.readerBack == nil {
				// 只有进出阅读视图时上一页才淡出；选中技能后去别的页面不淡出
				host.ChildExit(ui.Motion{Opacity: 1})
			}
			host.Children(func() {
				a.paneRoom = 0 // 用了 withPane 的页面会改成详情栏的宽度
				pg := ui.Column(c.Key(key)).Grow(1).MinHeight(0)
				// 进出阅读视图时上一页淡出，卡片在它上面展开或收回（reader.go）。
				// 有选中的技能时才可能进入阅读视图，那时先把淡出备好；若实际切去
				// 其他页面，外层的 ChildExit 会取消淡出。
				switch {
				case key == "reader":
					pg.Transition(readerFade)
				case a.selected != nil && (key == "browse" || key == "set" || key == "project"):
					pg.Transition(pageFade)
				}
				pg.Children(page)
			})
			if key != "reader" {
				a.readerBack = nil
			}
			if key != "updates" {
				a.upd.tabled = nil
			}
			a.toastLayer(c)
			a.dropLayer(c, mainCol)
		})
	})
	a.sidebarToggle(c)
	if a.confirmOpen {
		a.enableDialog(c)
	}
	if a.collectOpen {
		a.collectDialog(c)
	}
	if a.linkedOpen {
		a.linkedDialog(c)
	}
	a.deleteDialogs(c)
	a.palette(c)
}

// page 是右边这一页：它的键和画它的函数。
func (a *app) page(c *ui.Context) (string, func()) {
	switch p := a.project(); {
	case a.cmp != nil:
		return "compare", func() { a.comparePage(c, a.cmp) }
	case a.scope == scopeTidy:
		return "tidy", func() { a.tidyPage(c) }
	case a.scope == scopeUpdates:
		return "updates", func() { a.updatePage(c) }
	case a.scope == scopeImport:
		return "import", func() { a.importPage(c) }
	case a.scope == scopeUsage:
		return "usage", func() { a.usagePage(c) }
	case a.reading != nil:
		return "reader", func() { a.reader(c, a.reading) }
	case p != nil:
		return "project", func() { a.projectPage(c, p) }
	case a.inColl != nil:
		return "set", func() { a.setPage(c, a.inColl) }
	}
	return "browse", func() { a.browse(c) }
}

// open 进入阅读视图。
func (a *app) open(s *library.Skill) {
	if s == nil {
		return
	}
	a.lib.LoadFiles(s)
	a.selected, a.reading, a.file = s, s, 0
	a.readerRaw, a.readerEditor, a.readerEditorKey, a.readerImages = false, nil, "", nil
	a.readerIn = true
	a.lib.NoteOpened(s, time.Now())
}

// closeReader 从阅读视图回到浏览，详情栏显示这个技能，它的卡片从正文收回去。
func (a *app) closeReader() {
	a.readerBack, a.selected, a.reading = a.reading, a.reading, nil
}

// enable 启用或停用，失败时记下原因给详情栏显示。
func (a *app) enable(s *library.Skill, agent string, on bool) {
	a.err, a.errAgent = "", ""
	defer a.changed()
	if err := a.lib.Enable(s, agent, on); err != nil {
		a.err, a.errAgent = err.Error(), agent
	}
}

// rescan 重新扫描磁盘，选中和阅读的技能按 ID 找回来。
func (a *app) rescan() {
	if a.lib.Home == "" {
		return
	}
	lib := library.Scan(a.lib.Home)
	a.useScan(lib, lib.Updates())
}

// useScan 把已扫描的库和更新报告交给界面，不重新计算更新状态。
func (a *app) useScan(lib *library.Library, report library.UpdateReport) {
	hadPending := a.watch.pending != nil
	a.watch.scanGen.Add(1)
	a.watch.pending = nil
	a.applyScan(lib, report, true)
	if hadPending {
		a.triggerWatchScan()
	}
}

func (a *app) applyScan(lib *library.Library, report library.UpdateReport, reloadUsage bool) {
	defer a.changed()
	a.set.scannedAt = time.Now()
	if a.lib != nil && lib != nil {
		// 设置只在界面线程上改、改完就写盘，内存里这份最新；后台扫描是在
		// 开始时读的盘，期间改的设置（语言、最近打开的技能）不能被它退回去
		lib.Settings = a.lib.Settings
	}
	a.lib = lib
	a.upd.report = report
	a.tidy.stats, a.cmp = nil, nil
	if reloadUsage {
		a.refreshUsage()
	} else if a.usage != nil {
		a.lib.SetUsage(a.usage)
	}
	if a.selected != nil {
		a.selected = a.lib.ByID(a.selected.ID)
	}
	if a.coll != nil {
		a.coll = a.lib.CollectionByID(a.coll.ID)
	}
	if a.inColl != nil {
		a.inColl = a.lib.CollectionByID(a.inColl.ID)
	}
	if a.reading != nil {
		a.reading = a.lib.ByID(a.reading.ID)
		if a.reading != nil {
			a.lib.LoadFiles(a.reading)
		}
		a.readerEditor, a.readerEditorKey, a.readerImages = nil, "", nil
	}
	a.pruneFilterSelection()
	if a.batch.on && a.batchOffered() {
		a.pruneBatchPicked(a.batchShown())
	}
	a.linked, a.linkedOpen = nil, false
	if a.pal.idx != nil || a.pal.building {
		a.buildSearchIndex() // 正文跟着新的库重读，没改过的文件沿用
	}
	if hub := a.watch.hub.Load(); hub != nil && lib != nil {
		hub.sync(lib.WatchPlan())
	}
}

// changed 让两个窗口都按新的状态重画：设置里关掉的 Agent 要从看板消失，
// 看板里启用的技能也要算进设置里的技能数。
func (a *app) changed() {
	for _, w := range []*mygo.Window{a.win, a.setWin} {
		if w != nil {
			w.Invalidate()
		}
	}
}

// dialogParent 是系统面板挂在哪个窗口上：设置窗口开着时从设置里打开的挂在它上面。
func (a *app) dialogParent(fromSettings bool) *mygo.Window {
	if fromSettings && a.setWin != nil {
		return a.setWin
	}
	return a.win
}

// back 是 Esc：阅读视图回到浏览，浏览时取消选中，打开的集合里没有
// 选中时回到集合外。
func (a *app) back() {
	switch {
	case a.windowMenuOpen(menuWinMain):
		a.closeMenusIn(menuWinMain)
	case a.scope == scopeUpdates && a.upd.prepared != nil:
		a.closeUpdateCompare()
	case a.scope == scopeImport && a.imp.cmp != nil:
		a.imp.cmp = nil
	case a.cmp != nil:
		a.cmp = nil
	case a.reading != nil:
		a.closeReader()
	case a.batch.on:
		a.exitBatch()
	case a.scope == scopeUsage && a.del.picking:
		a.setPicking(false)
	case a.selected != nil || a.coll != nil:
		a.selected, a.coll = nil, nil
	case a.inColl != nil:
		a.closeSet()
	}
}

// toolbar 是内容区顶部的工具栏：标题、数量，after 放在标题后面（如问题
// 提示），right 放右侧的控件。
func (a *app) toolbar(c *ui.Context, title, subtitle string, after, right func()) {
	a.toolbarRow(c, func() {
		// 标题和数量同一行（r8 评论：不需要单独一行）
		// 窄的时候副标题先省略，标题和右侧控件不让
		ui.Row(c).Shrink(1).MinWidth(0).Gap(8).AlignItems(ui.Baseline).Children(func() {
			ui.Text(c, title).FontSize(13).FontWeight(600).SingleLine().Shrink(0)
			if after != nil {
				ui.Row(c).AlignSelf(ui.Center).Shrink(0).Children(after)
			}
			if subtitle != "" {
				ui.Text(c, subtitle).FontSize(12).TextColor(colTextMuted).FontFeatures("tnum").SingleLine().MinWidth(0).Shrink(1)
			}
		})
	}, right)
}

// toolbarRow 是工具栏本身：left 画标题部分，right 放右侧的控件。高度和右
// 内边距跟着 c.TitleBar() 走，免得 Windows/Linux 的窗口按钮压住右侧控件；
// 左边距见 toolbarLeftInset。
func (a *app) toolbarRow(c *ui.Context, left, right func()) {
	ui.Row(c).Height(toolbarHeight(c)).Shrink(0).Padding(0, toolbarRightInset(c), 0, a.toolbarLeftInset(c, 20)).Gap(10).AlignItems(ui.Center).
		BorderWidth(0, 0, 1, 0).BorderColor(colBorderDefault).DragWindow().Children(func() {
		left()
		if right != nil {
			right()
		}
	})
}

// reveal 在 Finder 中显示技能目录（示例数据的目录不存在）。
func (a *app) reveal(p string) {
	if a.win != nil {
		mygo.Shell.ShowItemInFolder(expandHome(p))
	}
}

func (a *app) openInEditor(p string) {
	if a.win != nil {
		if err := mygo.Shell.OpenPath(expandHome(p)); err != nil {
			fmt.Fprintln(os.Stderr, locales.CommonOpenFailedPrefix(), err)
		}
	}
}

func (a *app) openExternalURL(raw string) {
	if raw == "" {
		return
	}
	if a.openURL != nil {
		a.openURL(raw)
		return
	}
	if a.win != nil {
		if err := mygo.Shell.OpenExternal(raw); err != nil {
			a.notify(fromLink, toastError, locales.AppCantOpenLink(err.Error()))
		}
	}
}

// expandHome 把开头的 ~ 换成主目录。示例数据的路径用 filepath.Join 拼，
// Windows 上是 ~\…，所以 ~ 后面认本平台的任一分隔符。
func expandHome(p string) string {
	rest, ok := strings.CutPrefix(p, "~")
	if !ok || rest == "" || !os.IsPathSeparator(rest[0]) {
		return p
	}
	if home, err := os.UserHomeDir(); err == nil {
		return filepath.Join(home, rest)
	}
	return p
}
