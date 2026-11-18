package main

import (
	"runtime"
	"strings"

	"github.com/egoist/mygo/ui"

	"skillmanager/locales"
)

// 页面栈：像 Finder、浏览器那样后退、前进走过的页面。改变页面的操作都经
// navigate 记下来；不经它的变化（重新扫描后技能没了、删掉了正在读的技能）
// 在下一帧开头记到当前这一页上，不算新的一页。

// historyLimit 是最多记多少页，再多丢掉最早的。
const historyLimit = 50

// place 是一页：侧边栏选中的范围，加上打开的集合、阅读的技能或对比的副本。
// 只记 ID，重新扫描后按 ID 找回。
type place struct {
	scope   string
	set     string // 打开的集合
	reading string // 阅读视图的技能
	compare string // 副本对比页的技能
	tidy    bool   // 对比页是从整理页打开的
}

// visit 是历史里的一条：那一页，和离开时选中的、阅读视图看的文件，回来时还原。
type visit struct {
	place
	selected, coll string
	file           string
}

type history struct {
	visits []visit
	at     int
	depth  int // navigate 的嵌套层数：只有最外层记
}

// here 是现在显示的这一页。
func (a *app) here() place {
	p := place{scope: a.scope}
	if a.inColl != nil {
		p.set = a.inColl.ID
	}
	if a.reading != nil {
		p.reading = a.reading.ID
	}
	if a.cmp != nil {
		p.compare, p.tidy = a.cmp.skill.ID, a.cmp.tidy
	}
	return p
}

// navigate 做一次跳转：change 改界面状态，换了页就记成新的一页，截掉前进
// 的那些。里面再调 navigate 的（⌘K 先换范围、再开集合、再打开技能）合成
// 一页。
func (a *app) navigate(change func()) { a.travel(change, false) }

// navigateUp 是回上一级（‹、Esc、集合的面包屑）：上一级正是前一页时就是
// 后退，前进还能回来；否则上一级记成新的一页。
func (a *app) navigateUp(change func()) { a.travel(change, true) }

func (a *app) travel(change func(), up bool) {
	h := &a.hist
	if h.depth == 0 {
		a.syncHistory()
		a.keepVisit()
	}
	h.depth++
	change()
	h.depth--
	if h.depth > 0 {
		return
	}
	p := a.here()
	switch {
	case p == h.visits[h.at].place:
	case up && h.at > 0 && h.visits[h.at-1].place == p:
		h.at--
	default:
		h.visits = append(h.visits[:h.at+1], visit{place: p})
		if n := len(h.visits) - historyLimit; n > 0 {
			h.visits = h.visits[n:]
		}
		h.at = len(h.visits) - 1
	}
}

// syncHistory 让当前这一页跟上不经 navigate 的变化；第一次调用时记下第一页。
func (a *app) syncHistory() {
	h := &a.hist
	if len(h.visits) == 0 {
		h.visits, h.at = []visit{{place: a.here()}}, 0
		return
	}
	h.visits[h.at].place = a.here()
}

// keepVisit 记下这一页现在选中的和阅读视图看的文件，回来时还原。
func (a *app) keepVisit() {
	v := &a.hist.visits[a.hist.at]
	v.selected, v.coll, v.file = "", "", ""
	if a.selected != nil {
		v.selected = a.selected.ID
	}
	if a.coll != nil {
		v.coll = a.coll.ID
	}
	switch {
	case a.reading != nil && a.file == secFile:
		v.file = secHistoryFile
	case a.reading != nil && a.file >= 0 && a.file < len(a.reading.Files):
		v.file = a.reading.Files[a.file].Path
	}
}

// historyTarget 是往 d 的方向（-1 后退，1 前进）最近的还在的一页，没有为 -1。
// 和现在同一页的跳过：当前这一页失效后跟上来，可能和前一页一样。
func (a *app) historyTarget(d int) int {
	h := &a.hist
	if len(h.visits) == 0 {
		return -1
	}
	here := a.here()
	for i := h.at + d; i >= 0 && i < len(h.visits); i += d {
		if p := h.visits[i].place; p != here && a.exists(p) {
			return i
		}
	}
	return -1
}

// exists 报告这一页还在不在：项目没移除，集合、技能没删。
func (a *app) exists(p place) bool {
	if dir, ok := strings.CutPrefix(p.scope, projectPrefix); ok && a.lib.KnownProject(dir) == nil {
		return false
	}
	if p.set != "" && a.lib.CollectionByID(p.set) == nil {
		return false
	}
	if p.reading != "" && a.lib.ByID(p.reading) == nil {
		return false
	}
	if p.compare != "" {
		if s := a.lib.ByID(p.compare); s == nil || len(s.Versions()) < 2 {
			return false
		}
	}
	return true
}

// goHistory 后退（d 为 -1）或前进（1）一页。
func (a *app) goHistory(d int) {
	i := a.historyTarget(d)
	if i < 0 {
		return
	}
	a.syncHistory()
	a.keepVisit()
	a.hist.at = i
	a.restore(a.hist.visits[i])
}

// restore 回到历史里的一页。只还原界面，不重做打开时顺带做的事（记最近
// 打开的技能）。
func (a *app) restore(v visit) {
	leaving := a.reading
	a.closeMenusIn(menuWinMain)
	a.exitBatch()
	a.setPicking(false)
	a.scope = v.scope
	a.inColl, a.reading, a.cmp = nil, nil, nil
	a.err, a.errAgent, a.notice, a.tidy.msg, a.tidy.err = "", "", nil, "", ""
	if v.set != "" {
		a.inColl = a.lib.CollectionByID(v.set)
	}
	a.selected, a.coll = nil, nil
	if v.selected != "" {
		a.selected = a.lib.ByID(v.selected)
	}
	if v.coll != "" {
		a.coll = a.lib.CollectionByID(v.coll)
	}
	if s := a.lib.ByID(v.reading); v.reading != "" && s != nil {
		a.showReader(s)
		for i, f := range s.Files {
			if f.Path == v.file {
				a.file = i
			}
		}
		if v.file == secHistoryFile {
			a.file = secFile
		}
	}
	if s := a.lib.ByID(v.compare); v.compare != "" && s != nil {
		a.showCompare(s, v.tidy)
	}
	if leaving != nil && a.reading == nil {
		a.readerBack = leaving // 卡片从正文收回去
	}
}

// historyKey 是后退、前进的快捷键：macOS 是 ⌘[ ⌘]，其它平台 Alt+← Alt+→。
// 鼠标的侧键哪里都行（historyMouseKey）。
var historyKey = func() [2]shortcutKey {
	if runtime.GOOS == "darwin" {
		return [2]shortcutKey{{ui.Cmd, ui.KeyBracketLeft, "⌘["}, {ui.Cmd, ui.KeyBracketRight, "⌘]"}}
	}
	return [2]shortcutKey{{ui.Alt, ui.KeyLeft, "Alt+←"}, {ui.Alt, ui.KeyRight, "Alt+→"}}
}()

var historyMouseKey = [2]ui.Key{ui.KeyBack, ui.KeyForward}

// shortcutKey 是一个快捷键和提示里写的样子。
type shortcutKey struct {
	mods ui.Modifiers
	key  ui.Key
	hint string
}

// historyDir 把 0、1 换成后退、前进的方向。
func historyDir(i int) int { return 2*i - 1 }

// historyLabel 是后退（i 为 0）、前进（1）按钮的名称和提示。
func historyLabel(i int) string {
	label := locales.NavBack()
	if i == 1 {
		label = locales.NavForward()
	}
	return locales.WithShortcut(label, historyKey[i].hint)
}

func (a *app) historyShortcuts(c *ui.Context) {
	for i := range 2 {
		d := historyDir(i)
		c.OnWindowShortcut(historyKey[i].mods, historyKey[i].key, func() { a.goHistory(d) })
		c.OnWindowShortcut(0, historyMouseKey[i], func() { a.goHistory(d) })
	}
}

// historyButtons 画收起按钮右边的后退、前进，和它一样盖在侧边栏和内容区上面。
func (a *app) historyButtons(c *ui.Context) {
	icons := [2]*ui.SVG{iconArrowBack, iconArrowForward}
	for i := range 2 {
		d := historyDir(i)
		can := a.historyTarget(d) >= 0
		label := historyLabel(i)
		b := ui.ButtonBase(c.Key(i)).Size(sidebarToggleW, sidebarToggleH).Radius(6).Center().Label(label).Tooltip(label).Disabled(!can).Absolute().
			Left(historyButtonLeft(c, i)).Top((toolbarHeight(c) - sidebarToggleH) / 2)
		if can {
			tintBg(b, ink(0), colInkHover, colInkPress).TextColor(colTextBody)
		} else {
			b.TextColor(colTextDisabled)
		}
		b.Children(func() { ui.Icon(c, icons[i]).Size(16, 16) })
		if b.Clicked() {
			a.goHistory(d)
		}
	}
}

// historyButtonLeft 是后退（i 为 0）、前进（1）按钮的左边，紧挨着收起按钮。
func historyButtonLeft(c *ui.Context, i int) float32 {
	return sidebarToggleLeft(c) + float32(i+1)*(sidebarToggleW+historyButtonGap)
}

const historyButtonGap = 2

// chromeRight 是左上角红绿灯、收起、后退、前进这一排的右边。
func chromeRight(c *ui.Context) float32 {
	return historyButtonLeft(c, 1) + sidebarToggleW
}
