package main

import (
	"fmt"
	"log"
	"runtime"
	"strings"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"skillmanager/internal/library"
	"skillmanager/locales"
)

// 侧边栏「项目」下先列最近用过的几个，其余收进「显示全部 N 个」。
const recentProjects = 5

// 侧边栏里项目的 ID：前缀加项目的完整路径。
const projectPrefix = "project:"

func projectScope(p *library.ProjectInfo) string { return projectPrefix + p.Dir }

// 菜单图标不带颜色（画板「侧边栏」nav3）：平时灰色，项目的文件夹再浅一档，
// 选中的那一行跟文字一起变深。
var ()

// sidebar 是左边的侧边栏（画板「侧边栏」方案 A，像邮件的分组）：没有标题的
// 一组放整理、使用情况；「技能库」放我的、内置；「项目」是能折叠的分组，
// 每个项目各自一页。底栏是添加项目和设置。
func (a *app) sidebar(c *ui.Context, vibrant bool) {
	ui.Box(c).Height(toolbarHeight(c)).Shrink(0).DragWindow()
	bar := ui.Sidebar(c, &a.scope, func() {
		a.tidyItem(c)
		a.sideRow(c, scopeUsage, iconNavUsage, locales.NavUsage(), colTextSecondary).Height(sideRowHeight).Padding(0, 8)
		a.updateSidebar(c)
		a.importSidebar(c)
		sectionTitle(c, ui.Row(c), locales.NavLibrary())
		a.sideItem(c, string(library.ScopeMine), iconNavMine, locales.NavMine(), a.lib.Count(library.ScopeMine))
		a.sideItem(c, string(library.ScopeBuiltin), iconNavBuiltin, locales.NavBuiltin(), a.lib.Count(library.ScopeBuiltin))
		if len(a.lib.Projects) > 0 {
			a.projectsHeader(c)
			if a.projectsOpen {
				a.projectItems(c)
			}
		}
	}).Padding(4, 10, 0, 10).Gap(0).Grow(1).Label(locales.NavScopes()).Scrollbars(false).Bind(&a.sideBar)
	if vibrant {
		bar.Background(ui.Transparent)
	}
	if bar.Changed() {
		a.closeMenusIn(menuWinMain)
		a.selected, a.reading, a.coll, a.inColl, a.err, a.errAgent = nil, nil, nil, nil, "", ""
		a.cmp, a.notice, a.tidy.msg, a.tidy.err = nil, nil, "", ""
		a.exitBatch()
	}

	// 底栏：左边添加项目，右边设置（⌘,）。技能库的位置在设置里。
	ui.Row(c).Height(40).Shrink(0).Padding(0, 8).Gap(6).AlignItems(ui.Center).
		BorderWidth(1, 0, 0, 0).BorderColor(colBorderSidebar).TextColor(colTextSecondary).Children(func() {
		add := ui.ButtonBase(c).Height(26).Padding(0, 7, 0, 6).Gap(6).Radius(6).AlignItems(ui.Center).Label(locales.NavAddProjectEllipsis())
		tintBg(add, ink(0), ink(0.06), ink(0.11))
		add.Children(func() {
			ui.Icon(c, iconFolderAdd).Size(16, 16)
			ui.Text(c, locales.NavAddProject()).FontSize(13).SingleLine()
		})
		if add.Clicked() {
			a.chooseProjects(true)
		}
		ui.Spacer(c)
		if iconButton(c, iconSettings, locales.WithShortcut(locales.NavSettings(), "⌘,")).Clicked() {
			a.openSettings()
		}
	})
}

// sideItem 是侧边栏的一项，右边是数量。
func (a *app) sideItem(c *ui.Context, id string, icon *ui.SVG, label string, count int) ui.Element {
	it := a.sideRow(c, id, icon, label, colTextSecondary).Height(sideRowHeight).Padding(0, 8)
	if it.Clicked() && a.scope == id && a.inColl != nil {
		// 再点一次已选中的范围，从打开的集合回来
		a.closeSet()
	}
	it.Children(func() { a.sideCount(c, count) })
	return it
}

// tidyItem 是「整理」：有问题时右边是橙色的问题数，处理完消失。
func (a *app) tidyItem(c *ui.Context) {
	it := a.sideRow(c, scopeTidy, iconNavTidy, locales.NavTidy(), colTextSecondary).Height(sideRowHeight).Padding(0, 8)
	if it.Clicked() && a.scope == scopeTidy && a.cmp != nil {
		a.cmp = nil // 再点一次，从对比页回来
	}
	it.Children(func() {
		p := a.lib.Problems()
		n := a.issueTotal(&p)
		if n == 0 {
			return
		}
		ui.Text(c, fmt.Sprint(n)).FontSize(12).FontWeight(600).TextColor(colWarnText).FontFeatures("tnum").SingleLine().
			ContentTransition(ui.ContentTransition{Numeric: true, Down: true})
	})
}

// sideRow 是侧边栏的一行，图标是 iconColor 的灰色。选中的底色一直是灰的，
// 焦点在不在侧边栏都一样，图标跟文字一起变深。mygo 的 SidebarItem 拿主题的
// 强调色画图标、焦点在时选中是蓝底白字，所以每一行换一个主题：强调色就是
// 图标的颜色，选中的再把底色和文字改回来。
func (a *app) sideRow(c *ui.Context, id string, icon *ui.SVG, label string, iconColor ui.Color) ui.Element {
	selected := a.scope == id
	if selected {
		iconColor = colTextPrimary
	}
	t := *c.Theme()
	t.Accent, t.InactiveAccent, t.AccentText = iconColor, iconColor, colTextPrimary
	var it ui.Element
	ui.Themed(c, &t).Background(ui.Transparent).Shrink(0).Children(func() {
		it = ui.SidebarItem(c, id, icon, label).Radius(6)
		if selected {
			it.Background(colInkSelected).TextColor(colTextPrimary)
		} else {
			it.TextColor(colTextBody)
		}
	})
	return it
}

// sideCount 是一行右边的数量。
func (a *app) sideCount(c *ui.Context, n int) {
	if n <= 0 {
		return
	}
	ui.Text(c, fmt.Sprint(n)).FontSize(12).TextColor(colTextMuted).FontFeatures("tnum")
}

// 侧边栏的分组标题：11 号半粗的浅灰字，比菜单项和数量都安静。两个分组的
// 标题同高、上面留同样的空，菜单项一律 28 高、行间不留缝，整列一个节奏。
// mygo 的 SidebarSection 组内每行之间多 2px，所以不用它。
const (
	sideRowHeight     = 28
	sideSectionHeight = 26
	sideSectionGap    = 12
)

// sectionTitle 把 row 排成分组标题，标题字在左；right 给标题右边的东西。
func sectionTitle(c *ui.Context, row ui.Element, title string, right ...func()) ui.Element {
	row.Height(sideSectionHeight).Margin(sideSectionGap, 0, 0, 0).Padding(0, 0, 0, 8).AlignItems(ui.Center).Shrink(0).
		Children(func() {
			ui.Text(c, title).FontSize(11).FontWeight(500).TextColor(colTextMuted).Grow(1).SingleLine()
			for _, fn := range right {
				fn()
			}
		})
	return row
}

// projectsHeader 是「项目」分组的标题，点它收起或展开。右边的箭头一直在：
// 16px，鼠标移到标题上时箭头垫一块浅灰底，展开朝下、收起朝右。
func (a *app) projectsHeader(c *ui.Context) {
	head := ui.ButtonBase(c).Label(locales.NavProjects())
	sectionTitle(c, head, locales.NavProjects(), func() {
		box := ui.Box(c).Size(24, 24).Radius(6).Center().TextColor(colTextSecondary)
		box.Background(tint(head, "chev", ink(0), ink(0.07), ink(0.12)))
		box.Children(func() {
			arrow := ui.Icon(c, iconChevronDown).Size(16, 16)
			arrow.Rotate(arrow.Animate("open", -90*b2f(!a.projectsOpen), 150*time.Millisecond))
		})
	})
	if head.Clicked() {
		a.projectsOpen = !a.projectsOpen
	}
}

func b2f(b bool) float32 {
	if b {
		return 1
	}
	return 0
}

// projectItems 是「项目」下的各个项目，最近用过的在前；多出来的收进居中的
// 「显示全部 N 个」。
func (a *app) projectItems(c *ui.Context) {
	projects := a.lib.Projects
	total := len(projects)
	more := total > recentProjects
	if more && !a.allProjects {
		projects = projects[:recentProjects]
		// 选中的项目不在最近几个里时也要列出来
		if p := a.project(); p != nil && !containsProject(projects, p) {
			projects = append(projects, p)
		}
	}
	for _, p := range projects {
		id := projectScope(p)
		it := a.sideRow(c, id, iconNavFolder, p.Name, colTextDisabled).Height(sideRowHeight).Padding(0, 8)
		it.Tooltip(a.lib.Display(p.Dir))
		it.Children(func() { a.sideCount(c, len(p.Skills)) })
	}
	if more {
		text := locales.ShowAllCount(total)
		if a.allProjects {
			text = locales.ShowLess()
		}
		b := ui.ButtonBase(c).Height(sideRowHeight).Radius(6).Center().Shrink(0).Label(text)
		b.TextColor(tint(b, "fg", colTextMuted, colTextSecondary, colTextPrimary))
		b.Children(func() { ui.Text(c, text).FontSize(13).SingleLine() })
		if b.Clicked() {
			a.allProjects = !a.allProjects
		}
	}
}

func containsProject(ps []*library.ProjectInfo, p *library.ProjectInfo) bool {
	for _, q := range ps {
		if q == p {
			return true
		}
	}
	return false
}

// project 是侧边栏选中的项目，没选项目时为 nil。
func (a *app) project() *library.ProjectInfo {
	dir, ok := strings.CutPrefix(a.scope, projectPrefix)
	if !ok {
		return nil
	}
	return a.lib.Project(dir)
}

// chooseProjects 打开系统的选择文件夹面板（可多选），选中的目录记成项目，
// 重新扫描后列在侧边栏里；show 时打开第一个。
func (a *app) chooseProjects(show bool) {
	if !a.live {
		a.set.err = locales.ProjectsSampleCantAdd()
		return
	}
	parent := a.dialogParent(!show)
	go func() {
		dirs, err := mygo.Dialog.Open(mygo.OpenDialogOptions{
			Parent: parent, Title: locales.NavAddProject(), ButtonLabel: locales.ProjectsDialogAdd(), Directory: true, Multiple: true,
			Message: locales.ProjectsDialogMessage(),
		})
		if err != nil || len(dirs) == 0 {
			return
		}
		a.post(func() {
			if err := a.lib.AddProjects(dirs); err != nil {
				a.err, a.set.err = err.Error(), err.Error()
				a.changed()
				return
			}
			a.rescan()
			if p := a.lib.Project(dirs[0]); show && p != nil {
				a.scope, a.selected, a.coll, a.inColl, a.projectsOpen = projectScope(p), nil, nil, nil, true
			}
		})
	}()
}

// 收起侧边栏的按钮（像备忘录）：固定在红绿灯右边，侧边栏开着时落在它的
// 顶栏里，收起后压在内容区工具栏的左端。
const (
	sidebarToggleGap  = 14 // 按钮和红绿灯的间距，图标本身离绿灯约 19
	sidebarToggleEdge = 12 // 左上角没有红绿灯（全屏、Windows/Linux）时离窗口左边
	sidebarToggleW    = 28 // iconButton 的宽
	sidebarToggleH    = 26
)

// sidebarShortcut 是收起侧边栏的快捷键 ⌃⌘S，macOS「显示」菜单的惯例。
// 其它平台 ui.Cmd 就是 Ctrl，⌃⌘S 会变成 Ctrl+S，所以不设。
var sidebarShortcut = func() ui.Modifiers {
	if runtime.GOOS == "darwin" {
		return ui.Ctrl | ui.Cmd
	}
	return 0
}()

func (a *app) toggleSidebar() {
	a.lib.Settings.SidebarHidden = !a.lib.Settings.SidebarHidden
	if err := a.lib.SaveSettings(); err != nil {
		log.Println("save sidebar state:", err)
	}
}

// sidebarRoom 是侧边栏连同分隔线占的宽度（动画的终点）。
func (a *app) sidebarRoom() float32 {
	if a.lib.Settings.SidebarHidden {
		return 0
	}
	return sidebarWidth + 1
}

func sidebarToggleLeft(c *ui.Context) float32 {
	if left := c.TitleBar().Left; left > 0 {
		return left + sidebarToggleGap
	}
	return sidebarToggleEdge
}

// toolbarLeftInset 是内容区工具栏的左边距：侧边栏开着时用设计稿的 base，
// 收起时让出红绿灯和收起按钮。跟着侧边栏露出的宽度逐帧算，标题一路
// 平滑地滑，不会跳到红绿灯底下。
func (a *app) toolbarLeftInset(c *ui.Context, base float32) float32 {
	return max(base, sidebarToggleLeft(c)+sidebarToggleW+toolbarControlGap-a.sideShown)
}

// sidebarToggleLabel 是收起按钮的名称和提示。
func sidebarToggleLabel(hidden bool) string {
	label := locales.SidebarHide()
	if hidden {
		label = locales.SidebarShow()
	}
	if sidebarShortcut != 0 {
		label = locales.WithShortcut(label, "⌃⌘S")
	}
	return label
}

// sidebarToggle 画收起按钮，盖在侧边栏和内容区上面。
func (a *app) sidebarToggle(c *ui.Context) {
	// 图标比工具栏的 16 大一档，和红绿灯的分量相当
	label := sidebarToggleLabel(a.lib.Settings.SidebarHidden)
	b := ui.ButtonBase(c).Size(sidebarToggleW, sidebarToggleH).Radius(6).Center().TextColor(colTextBody).Label(label).Tooltip(label).Absolute().
		Left(sidebarToggleLeft(c)).Top((toolbarHeight(c) - sidebarToggleH) / 2)
	tintBg(b, ink(0), colInkHover, colInkPress)
	b.Children(func() { ui.Icon(c, iconSidebar).Size(18, 18) })
	if b.Clicked() {
		a.toggleSidebar()
	}
}
