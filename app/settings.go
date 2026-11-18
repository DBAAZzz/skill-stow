package main

import (
	"cmp"
	"path/filepath"
	"skillmanager/locales"
	"slices"
	"strings"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"skillmanager/internal/library"
)

// 设置窗口（s1-a / s1-b / p2-b）：侧边栏左下角的齿轮或 ⌘, 打开，三个标签。
const (
	tabGeneral = iota
	tabAgents
	tabProjects
	tabUpdates
)

var settingsTabs = []struct {
	label func() string
	icon  *ui.SVG
}{
	{locales.SettingsTabGeneral, iconSettings},
	{locales.SettingsTabAgents, iconRobot},
	{locales.SettingsTabProjects, iconFolder},
	{locales.SettingsTabUpdates, iconRefresh},
}

// Agent 管理里直接列出技能最多的几个，其余收起。
const shownAgents = 9

// 「在项目里查看的目录」先列几个。
const shownDirs = 7

type settingsRescanPhase int

const (
	rescanIdle settingsRescanPhase = iota
	rescanRunning
	rescanDone
)

// settingsRescanHold 是重新检查 / 重新检测完成后，按钮保持绿色确认态再淡回原样的时长。
const settingsRescanHold = 1200 * time.Millisecond

// settingsState 是设置窗口的状态。
type settingsState struct {
	open        bool
	minimized   bool
	focused     bool
	tab         int
	allAgents   bool                // 「其余 N 个」展开
	scannedAt   time.Time           // 上一次检测 / 检查的时间
	rescanSeq   int                 // 防旧定时回调误清新一轮检查状态
	rescanTab   int                 // 这轮检查是 tabAgents 还是 tabProjects 触发的
	rescanPhase settingsRescanPhase // 重新检测 / 重新检查三态（空闲 / 扫描中 / 已完成）
	linkResult  string              // 「检查并修复」的结果
	allDirs     bool                // 「在项目里查看的目录」展开
	err         string
	ghProbing   bool
	ghChecked   bool
	ghOK        bool
	ghNote      string
}

// 设置窗口的固定宽高、左侧导航栏宽度和顶部拖动区高度。
const (
	settingsWidth      = 760
	settingsHeight     = 540
	settingsSideWidth  = 192
	settingsDragHeight = 48
)

// openSettings 打开设置窗口：一个单独的原生窗口（左上角是系统的红绿灯），
// 已经开着就拿到前面。测试里没有窗口，只记下 open。
func (a *app) openSettings() {
	a.set.err, a.set.linkResult = "", ""
	a.set.open = true
	a.set.minimized = false
	a.set.focused = true
	if a.main.open {
		a.main.focused = false
	}
	if a.set.tab == tabUpdates && a.lib.Settings.UseGHAuth {
		a.probeGHAuth(false)
	}
	if !a.live {
		a.syncDockProgress(nil)
		return
	}
	if a.setWin != nil && !a.setWin.IsDestroyed() {
		a.setWin.SetTitle(settingsTabs[a.set.tab].label())
		if a.setWin.IsMinimized() {
			a.setWin.Restore()
		}
		a.setWin.Focus()
		a.changed()
		return
	}
	win := mygo.NewWindow(mygo.WindowOptions{
		Hidden:               true,
		Title:                settingsTabs[a.set.tab].label(),
		Width:                settingsWidth,
		Height:               settingsHeight,
		UseContentSize:       true,
		StateKey:             "settings",
		TitleBarStyle:        mygo.TitleBarHiddenInset,
		TitleBarHeight:       settingsDragHeight,
		TrafficLightPosition: &mygo.Point{X: 18, Y: (settingsDragHeight - trafficLightSize) / 2},
		DisableResize:        true,
		DisableMaximize:      true,
		DisableFullScreen:    true,
		BackgroundColor:      windowBackground(&colBgPage),
		Content:              ui.View(a.settingsView),
	})
	a.setWin = win
	a.closeMenusIn(menuWinSettings)
	// 覆盖 StateKey 可能记住的旧版本尺寸，确保始终以固定 760×540 打开。
	win.SetContentSize(settingsWidth, settingsHeight)
	win.Show()
	a.syncDockProgress(win)
	win.OnFocus(func() {
		a.set.focused = true
		a.main.focused = false
	})
	win.OnBlur(func() {
		a.set.focused = false
		a.closeMenusIn(menuWinSettings)
	})
	win.OnMinimize(func() {
		a.set.minimized = true
		a.set.focused = false
	})
	win.OnRestore(func() {
		a.set.minimized = false
	})
	win.OnClose(func(*mygo.CloseEvent) {
		a.clearDockProgressOnLastWindowClose(win, a.win != nil && !a.win.IsDestroyed())
	})
	win.OnClosed(func() {
		a.closeMenusIn(menuWinSettings)
		if a.setWin == win {
			a.setWin = nil
		}
		a.set.open, a.set.minimized, a.set.focused = false, false, false
		if a.main.open && !a.main.minimized {
			a.main.focused = true
		}
	})
}

// closeSettings 是 ⌘W。
func (a *app) closeSettings() {
	if a.setWin == nil && a.set.open {
		a.clearDockProgressOnLastWindowClose(nil, a.main.open)
	}
	a.closeMenusIn(menuWinSettings)
	a.set.open, a.set.minimized, a.set.focused = false, false, false
	if a.main.open && !a.main.minimized {
		a.main.focused = true
	}
	if w := a.setWin; w != nil {
		a.setWin = nil
		w.Close()
	}
}

// settingsView 是设置窗口的内容：统一底色下左右分栏，无竖向与顶栏分割线。
func (a *app) settingsView(c *ui.Context) {
	prevWin := a.menuBuildWin
	a.menuBuildWin = menuWinSettings
	defer func() { a.menuBuildWin = prevWin }()
	a.flushPendingWatch()
	theme(c)
	c.Root().Background(colBgPage)
	c.OnWindowShortcut(ui.Cmd, ui.KeyW, a.closeSettings)
	ui.Row(c).Fill().AlignItems(ui.Stretch).Children(func() {
		a.settingsSidebar(c)
		ui.Column(c).Grow(1).MinWidth(0).Children(func() {
			ui.Box(c).Height(settingsContentTop(c)).Shrink(0).DragWindow()
			ui.Scroll(c).Grow(1).MinHeight(0).Scrollbars(false).Children(func() {
				ui.Column(c.Key(a.set.tab)).Padding(0, 24, 22, 14).Children(func() {
					switch a.set.tab {
					case tabAgents:
						a.agentsTab(c)
					case tabUpdates:
						a.updateSettings(c)
					case tabProjects:
						a.projectsTab(c)
					default:
						a.generalTab(c)
					}
					if a.set.err != "" {
						errorLine(c, a.set.err)
					}
				})
			})
		})
	})
	a.diagnosticsDialog(c)
}

func settingsContentTop(c *ui.Context) float32 {
	if bar := c.TitleBar(); bar.Right > 0 {
		// Windows/Linux 的窗口按钮在右上角：右侧内容顶部让出控件带高度
		return max(bar.Height, 20)
	}
	return 20
}

// settingsSidebar 是左侧导航栏：红绿灯下方直接排开 4 个设置分类，与右侧共用底色。
func (a *app) settingsSidebar(c *ui.Context) {
	ui.Column(c).Width(settingsSideWidth).Shrink(0).Padding(0, 10, 14, 10).Children(func() {
		ui.Box(c).Height(max(c.TitleBar().Height, settingsDragHeight)).Shrink(0).DragWindow()
		ui.Column(c).Gap(2).Children(func() {
			for i, tab := range settingsTabs {
				on := a.set.tab == i
				b := ui.ButtonBase(c.Key(i)).Height(32).Padding(0, 10).Radius(8).Gap(8).Justify(ui.Start).AlignItems(ui.Center).Label(tab.label())
				selectableRow(b, on, colInkSelected)
				b.Children(func() {
					icon := ui.Icon(c, tab.icon).Size(16, 16).TextColor(colTextSecondary)
					text := ui.Text(c, tab.label()).FontSize(13).TextColor(colTextBody).SingleLine()
					if on {
						icon.TextColor(colTextPrimary)
						text.TextColor(colTextPrimary).FontWeight(500)
					}
				})
				if b.Clicked() {
					a.closeMenusIn(menuWinSettings)
					a.set.tab, a.set.err = i, ""
					if i == tabUpdates && a.lib.Settings.UseGHAuth {
						a.probeGHAuth(false)
					}
					if a.setWin != nil && !a.setWin.IsDestroyed() {
						a.setWin.SetTitle(tab.label())
					}
				}
			}
		})
		ui.Box(c).Grow(1).DragWindow()
	})
}

// ——— 通用 ———

func (a *app) generalTab(c *ui.Context) {
	groupLabel(c, locales.NavLibrary(), true)
	setBox(c, func() {
		setRow(c).Children(func() {
			n := 0
			for _, s := range a.lib.Skills {
				if s.Shared {
					n++
				}
			}
			// 路径用等宽，技能数用正文字体，各自一段
			ui.Column(c).Grow(1).MinWidth(0).Gap(titleGap).Children(func() {
				ui.Text(c, locales.CommonLocation()).FontWeight(500).SingleLine()
				ui.Row(c).Gap(8).AlignItems(ui.Baseline).MinWidth(0).TextColor(colTextMuted).Children(func() {
					ui.Text(c, a.lib.Display(a.lib.Root)).Font("monospace").FontSize(11).SingleLine().MinWidth(0).Shrink(1)
					ui.Text(c, locales.CommonSkillCount(n)).FontSize(12).FontFeatures("tnum").SingleLine().Shrink(0)
				})
			})
			if pushButton(c, locales.ActionShowInFinder()).Clicked() {
				a.reveal(a.lib.Root)
			}
		})
		setRow(c).Children(func() {
			rowText(c, locales.SettingsCheckLinks(), locales.SettingsCheckLinksHint(), false)
			if pushButton(c, locales.SettingsCheckAndRepair()).Clicked() {
				a.checkLinks()
			}
		})
	})
	if a.set.linkResult != "" {
		hint(c, func() { ui.Text(c, a.set.linkResult) })
	}

	groupLabel(c, locales.SettingsAppearance(), false)
	setBox(c, func() {
		setRow(c).Padding(12, 18).Children(func() {
			rowText(c, locales.SettingsTheme(), "", false)
			ui.Row(c).Gap(14).Role(ui.RoleRadioGroup).Label(locales.SettingsTheme()).Children(func() {
				a.themeTile(c, themeLight, locales.SettingsThemeLight())
				a.themeTile(c, themeDark, locales.SettingsThemeDark())
				a.themeTile(c, themeAuto, locales.SettingsLanguageSystem())
			})
		})
		setRow(c).Children(func() {
			rowText(c, locales.SettingsLanguage(), "", false)
			a.languagePicker(c)
		})
		setRow(c).Children(func() {
			rowText(c, locales.SettingsTidySound(), locales.SettingsTidySoundHint(), false)
			on := !a.lib.Settings.QuietTidy
			if toggle(c, &on, locales.SettingsTidySound()).Changed() {
				a.lib.Settings.QuietTidy = !on
				a.saveSettings()
			}
		})
	})

	a.appUpdateGroup(c)
	a.supportGroup(c)
}

// checkLinks 是「检查并修复」：修好了就重新扫描。
func (a *app) checkLinks() {
	a.set.err = ""
	r, err := a.lib.CheckLinks()
	if err != nil {
		a.set.err = err.Error()
	}
	msg := locales.SettingsLinksChecked(r.Checked)
	switch {
	case len(r.Fixed) == 0 && len(r.Broken) == 0:
		msg += locales.SettingsLinksAllGood()
	case len(r.Fixed) > 0:
		msg += locales.SettingsLinksFixed(len(r.Fixed))
		fallthrough
	default:
		if len(r.Broken) > 0 {
			var names []string
			for _, p := range r.Broken {
				names = append(names, a.lib.Display(p))
			}
			msg += locales.SettingsLinksBroken(len(r.Broken), strings.Join(names, locales.ListSeparator()))
		}
		msg = locales.SentenceEnd(msg)
	}
	a.set.linkResult = msg
	if len(r.Fixed) > 0 {
		a.rescan()
	}
}

// themeTile 是主题的一个选项（.th）：小窗口的示意图和名称，点了换成这种
// 主题（setTheme）。
func (a *app) themeTile(c *ui.Context, kind, name string) {
	on := a.lib.Settings.Theme == kind
	col := ui.ButtonBase(c.Key("theme-" + kind)).Column().Gap(5).AlignItems(ui.Center).Cursor(ui.CursorPointer).
		Role(ui.RoleRadio).Checked(on).Label(name)
	if col.Clicked() {
		a.setTheme(kind)
	}
	col.Children(func() {
		pv := ui.Box(c).Size(68, 44).Radius(7).Clip()
		if on {
			pv.Shadow(0, 0, 0, 2.5, colAccent)
		} else {
			hairline(pv, 0.2)
		}
		pv.Children(func() {
			themePreview(c, kind == themeDark)
			if kind == themeAuto {
				// 右半边是深色
				ui.Box(c).Absolute().Top(0).Left(34).Size(34, 44).Clip().Children(func() {
					ui.Box(c).Absolute().Top(0).Left(-34).Size(68, 44).Children(func() { themePreview(c, true) })
				})
			}
		})
		t := ui.Text(c, name).FontSize(12).TextColor(colTextBody)
		if on {
			t.TextColor(colTextPrimary).FontWeight(500)
		}
	})
}

// themePreview 是示意图：左边一条侧边栏，右边三道横线。
func themePreview(c *ui.Context, dark bool) {
	p := themePreviewColors[0]
	if dark {
		p = themePreviewColors[1]
	}
	bg, side, bar, line := p.bg, p.side, p.bar, p.line
	ui.Box(c).Absolute().Top(0).Left(0).Size(68, 44).Background(bg).Children(func() {
		ui.Box(c).Absolute().Top(0).Left(0).Size(20, 44).Background(side)
		for i, color := range []ui.Color{bar, line, line} {
			ui.Box(c.Key(i)).Absolute().Top(float32(9+10*i)).Left(26).Size(34, 5).Radius(3).Background(color)
		}
	})
}

// languagePicker 是「语言」的统一弹出选择器（sf6-settings）：跟随系统、
// 简体中文、English，选了马上换（setLanguage）。
func (a *app) languagePicker(c *ui.Context) {
	current := languageOptions[0].title()
	for _, l := range languageOptions {
		if l.id == a.lib.Settings.Language {
			current = l.title()
		}
	}
	a.selectButton(c, "settings.lang", locales.SettingsLanguage(), current, false, func(fm *frostedMenu) {
		for _, l := range languageOptions {
			if fm.radio(l.title(), l.id == a.lib.Settings.Language) {
				a.setLanguage(l.id)
			}
		}
	})
}

// ——— Agent 管理 ———

func (a *app) agentsTab(c *ui.Context) {
	agents := slices.Clone(a.lib.Agents)
	counts := map[string]int{}
	for _, ag := range agents {
		counts[ag.ID] = a.lib.SkillCount(ag.ID)
	}
	slices.SortStableFunc(agents, func(x, y library.Agent) int { return cmp.Compare(counts[y.ID], counts[x.ID]) })
	shown := agents
	if len(agents) > shownAgents && !a.set.allAgents {
		shown = agents[:shownAgents]
	}

	groupLabel(c, locales.SettingsInstalledAgents(len(agents)), true)
	setBox(c, func() {
		setListRow(c).Padding(9, 16).Children(func() {
			rowText(c, locales.SettingsShowInInterface(), locales.SettingsHiddenAgentNote(), false)
		})
		for _, ag := range shown {
			setListDivider(c.Key("div:" + ag.ID))
			hidden := a.lib.Hidden(ag.ID)
			row := setListRow(c.Key(ag.ID))
			if hidden {
				row.TextColor(colTextDisabled)
			}
			row.Children(func() {
				ui.Box(c).Shrink(0).Opacity(cond[float32](hidden, 0.4, 1)).Children(func() { agentBadge(c, ag.ID, 18) })
				rowText(c, ag.DisplayName(), a.agentDir(ag), true)
				if library.IsCustom(ag.ID) && linkButton(c, locales.ActionRemove()).Tooltip(locales.SettingsRemoveDirectoryHint()).Clicked() {
					a.settingsDo(a.lib.RemoveAgentDir(ag.ID), true)
				}
				ui.Text(c, locales.CommonSkillTotal(counts[ag.ID])).FontSize(12).TextColor(cond(hidden, colTextDisabled, colTextSecondary)).
					FontFeatures("tnum").SingleLine().Shrink(0).ContentTransition(ui.ContentTransition{Numeric: true})
				on := !hidden
				if toggle(c, &on, locales.SettingsShowAgent(ag.DisplayName())).Margin(0, 0, 0, 4).Changed() {
					a.settingsDo(a.lib.SetHidden(ag.ID, !on), false)
				}
			})
		}
		if rest := len(agents) - shownAgents; rest > 0 {
			text := locales.ShowLess()
			if !a.set.allAgents {
				text = locales.SettingsOtherAgents(rest)
			}
			more := ui.ButtonBase(c).Padding(9, 16).Justify(ui.Start).BorderWidth(1, 0, 0, 0).BorderColor(colBorderSubtle).
				TextColor(colAccent).Label(text)
			more.Children(func() { ui.Text(c, text).FontSize(13).SingleLine() })
			if more.Clicked() {
				a.set.allAgents = !a.set.allAgents
			}
		}
		sumRow(c, func() {
			ui.Text(c, scannedLabel(a.set.scannedAt, time.Now())).SingleLine().ContentTransition(labelTransition)
			ui.Spacer(c)
			a.rescanButton(c, tabAgents, locales.ActionDetectAgain(), locales.SettingsDetecting(), locales.SettingsDetected())
		})
	})
	hint(c, func() {
		ui.Text(c, locales.SettingsAgentNotFound())
		if linkButton(c, locales.SettingsAddSkillDirectoryEllipsis()).Clicked() {
			a.chooseAgentDir()
		}
	})
}

// agentDir 是 Agent 自己的技能目录（显示用）。
func (a *app) agentDir(ag library.Agent) string {
	if a.lib.Home == "" {
		return "~/" + ag.Skills // 示例数据
	}
	return a.lib.Display(filepath.Join(a.lib.Home, ag.Skills))
}

// chooseAgentDir 选一个技能目录，记下来算一个 Agent。
func (a *app) chooseAgentDir() {
	if !a.live {
		a.set.err = locales.SettingsSampleCannotAddDirectory()
		return
	}
	parent := a.dialogParent(true)
	go func() {
		dirs, err := mygo.Dialog.Open(mygo.OpenDialogOptions{
			Parent: parent, Title: locales.SettingsAddSkillDirectory(), ButtonLabel: locales.ProjectsDialogAdd(), Directory: true,
			Message: locales.SettingsChooseSkillDirectory(),
		})
		if err != nil || len(dirs) == 0 {
			return
		}
		a.post(func() { a.settingsDo(a.lib.AddAgentDir(dirs[0]), true) })
	}()
}

// ——— 项目 ———

func (a *app) projectsTab(c *ui.Context) {
	set := &a.lib.Settings
	d := a.lib.Discovery
	groupLabel(c, locales.SettingsAutoDiscover(), true)
	setBox(c, func() {
		setListRow(c).Padding(9, 16).Children(func() {
			rowText(c, locales.SettingsFindProjectsFromLogs(), locales.SettingsProjectDiscoveryNote(), false)
			if toggle(c, &set.AutoDiscover, locales.SettingsFindProjectsFromLogs()).Changed() {
				a.saveSettings()
				a.rescan()
			}
		})
		for _, src := range d.Sources {
			setListDivider(c.Key("div:" + src.Agent))
			dim := src.Count < 0 || !set.AutoDiscover
			row := setListRow(c.Key(src.Agent))
			if dim {
				row.TextColor(colTextDisabled)
			}
			if src.Note != "" {
				row.Tooltip(locales.SettingsFoundNote(src.Note))
			}
			row.Children(func() {
				ui.Box(c).Shrink(0).Opacity(cond[float32](dim, 0.4, 1)).Children(func() { agentBadge(c, src.Agent, 18) })
				sub := src.Path
				if src.Count < 0 {
					sub = locales.SettingsNoUsageLogs()
				}
				rowText(c, src.Name, sub, src.Count >= 0)
				count := "—"
				if src.Count >= 0 {
					count = locales.CommonProjectCount(src.Count)
				}
				ui.Text(c, count).FontSize(12).TextColor(cond(dim, colTextDisabled, colTextSecondary)).FontFeatures("tnum").SingleLine().
					ContentTransition(ui.ContentTransition{Numeric: true})
			})
		}
		sumRow(c, func() {
			if set.AutoDiscover {
				before, after := splitEmph(locales.SettingsDiscoverSummary(d.Total, emphMark))
				ui.Text(c, before).SingleLine().ContentTransition(ui.ContentTransition{Numeric: true})
				ui.Text(c, locales.SettingsWithSkills(d.WithSkills)).FontWeight(600).TextColor(colTextPrimary).FontFeatures("tnum").
					SingleLine().ContentTransition(ui.ContentTransition{Numeric: true})
				if after != "" {
					ui.Text(c, after).SingleLine().ContentTransition(ui.ContentTransition{Numeric: true})
				}
				ui.Text(c, locales.SettingsDiscoverMeta(checkedLabel(a.set.scannedAt), took(d.Took))).TextColor(colTextMuted).
					SingleLine().MinWidth(0).Shrink(1).ContentTransition(labelTransition)
			} else {
				ui.Text(c, locales.SettingsAutoDiscoverOff()).TextColor(colTextMuted).SingleLine().MinWidth(0).Shrink(1)
			}
			ui.Spacer(c)
			a.rescanButton(c, tabProjects, locales.ActionCheckAgain(), locales.SettingsChecking(), locales.SettingsChecked())
		})
	})

	groupLabel(c, locales.SettingsManualFolders(), false)
	manual := a.lib.ManualProjects()
	setBox(c, func() {
		if len(manual) == 0 {
			setListRow(c).Children(func() {
				ui.Text(c, locales.SettingsNoManualFolders()).FontSize(12).TextColor(colTextMuted)
			})
		}
		for i, p := range manual {
			if i > 0 {
				setListDivider(c.Key("div:" + p.Dir))
			}
			setListRow(c.Key(p.Dir)).Children(func() {
				ui.Icon(c, iconFolderFill).Size(18, 18).TextColor(colFolder)
				rowText(c, p.Name, a.lib.Display(p.Dir), true)
				switch {
				case p.Missing:
					ui.Text(c, locales.SettingsFolderMissing()).FontSize(12).TextColor(colWarnText).SingleLine()
				case len(p.Skills) == 0:
					ui.Text(c, locales.SettingsFolderNoSkills()).FontSize(12).TextColor(colTextDisabled).SingleLine()
				default:
					ui.Text(c, locales.CommonSkillCount(len(p.Skills))).FontSize(12).TextColor(colTextSecondary).FontFeatures("tnum").SingleLine()
				}
				if removeButton(c, locales.ActionRemoveTarget(p.Name)).Margin(0, 0, 0, 4).Clicked() {
					a.settingsDo(a.lib.RemoveProjects([]string{p.Dir}), true)
				}
			})
		}
		// 底部只有「添加」：移除在每一行上
		add := ui.ButtonBase(c).Padding(10, 16).Gap(6).AlignItems(ui.Center).Justify(ui.Start).
			BorderWidth(1, 0, 0, 0).BorderColor(colBorderSubtle).TextColor(colAccent).Label(locales.ActionAddFolderEllipsis())
		add.Children(func() {
			ui.Icon(c, iconPlus).Size(13, 13)
			ui.Text(c, locales.ActionAddFolderEllipsis()).FontSize(13).SingleLine()
			ui.Spacer(c)
			ui.Text(c, locales.SettingsFolderSelectionHint()).FontSize(12).TextColor(colTextMuted).SingleLine()
		})
		if add.Clicked() {
			a.chooseProjects(false)
		}
	})

	groupLabel(c, locales.SettingsDisplay(), false)
	setBox(c, func() {
		setRow(c).Children(func() {
			rowText(c, locales.SettingsGroupWorktrees(), locales.SettingsGroupWorktreesHint(), false)
			if toggle(c, &set.MergeWorktrees, locales.SettingsGroupWorktrees()).Changed() {
				a.saveSettings()
				a.rescan()
			}
		})
	})

	groupLabel(c, locales.SettingsProjectDirectories(), false)
	dirs := library.ProjectSkillDirs()
	setBox(c, func() {
		setRow(c).Children(func() {
			ui.Text(c, locales.SettingsProjectDirectoriesHint()).
				FontSize(12).LineHeight(1.45).TextColor(colTextMuted).Grow(1).MinWidth(0)
		})
		ui.Row(c).Wrap().Gap(6).Padding(0, 18, 14, 18).Children(func() {
			shown := dirs
			if !a.set.allDirs && len(dirs) > shownDirs {
				shown = dirs[:shownDirs]
			}
			for _, d := range shown {
				dirTag(c.Key(d), d+"/")
			}
			if len(dirs) > shownDirs {
				text := locales.ShowLess()
				if !a.set.allDirs {
					text = locales.CommonMoreItems(len(dirs) - shownDirs)
				}
				if linkButton(c, text).Height(22).Clicked() {
					a.set.allDirs = !a.set.allDirs
				}
			}
		})
	})
}

// dirTag 是一个目录的标签：文件夹图标加路径，一眼看出是目录。
func dirTag(c *ui.Context, path string) {
	ui.Row(c).Height(22).Padding(0, 7, 0, 6).Gap(4).Radius(5).AlignItems(ui.Center).Background(colBorderSubtle).Children(func() {
		ui.Icon(c, iconFolder).Size(13, 13).TextColor(colFolder)
		ui.Text(c, path).Font("monospace").FontSize(11).TextColor(colTextBody).SingleLine()
	})
}

// removeButton 是行尾的 ⊖：悬停变红，点了移除这一行。
func removeButton(c *ui.Context, label string) ui.Element {
	b := ui.ButtonBase(c).Size(22, 22).Radius(6).Center().Shrink(0).Label(label).
		Tooltip(locales.SettingsRemoveFolderHint())
	tintBg(b, colDangerBg.Alpha(0), colDangerBg, colInkPress.Over(colDangerBg.Over(colBgCard)))
	b.TextColor(tint(b, "text", colTextMuted, colDanger, colDanger))
	b.Children(func() { ui.Icon(c, iconMinusCircle).Size(15, 15) })
	return b
}

// ——— 设置里的组件 ———

// groupLabel 是一组的标题（.gl）。
func groupLabel(c *ui.Context, text string, first bool) {
	l := ui.Text(c, text).FontSize(12).FontWeight(500).TextColor(colTextMuted).Margin(0, 0, 8, 4)
	if !first {
		l.Margin(20, 0, 8, 4)
	}
}

// setBox 是设置里的圆角白色分组框（10px 圆角 + 细边与浮起投影）。
func setBox(c *ui.Context, fn func()) {
	box := ui.Column(c).Radius(10).Background(colBgCard)
	hairline(box, 0.06)
	lift(box, 1, 3, 0.03)
	box.Clip().Children(fn)
}

// setRow 是通用/更新等短卡片里的一行，内部不画横线，靠留白呼吸。
func setRow(c *ui.Context) ui.Element {
	return ui.Row(c).MinHeight(36).Padding(11, 18).Gap(10).AlignItems(ui.Center)
}

// setListRow 是 Agent/项目列表卡片里的一行，配合 setListDivider 留出内缩分隔线。
func setListRow(c *ui.Context) ui.Element {
	return ui.Row(c).MinHeight(43).Padding(6, 16).Gap(10).AlignItems(ui.Center)
}

// setListDivider 是列表卡片行间向内缩进的极淡分隔线，不顶到圆角边缘。
func setListDivider(c *ui.Context) {
	ui.Box(c).Height(1).Margin(0, 16).Background(colBorderSubtle)
}

// rowText 是一行左边的名称和下面一行说明；mono 时说明是路径。
func rowText(c *ui.Context, title, sub string, mono bool) {
	ui.Column(c).Grow(1).MinWidth(0).Gap(titleGap).Children(func() {
		ui.Text(c, title).FontWeight(500).SingleLine()
		if sub == "" {
			return
		}
		t := ui.Text(c, sub).FontSize(12).LineHeight(1.45).TextColor(colTextMuted)
		if mono {
			t.Font("monospace").FontSize(11).SingleLine()
		}
	})
}

// sumRow 是框底部内嵌圆角汇总条，四面保留卡片白边托住，避免底色融进页面。
func sumRow(c *ui.Context, fn func()) {
	bar := ui.Row(c).Margin(4, 10, 10, 10).Padding(9, 12).Gap(6).Radius(6).AlignItems(ui.Center).
		Background(colBgSubtle).FontSize(12).TextColor(colTextBody)
	hairline(bar, 0.06)
	bar.Children(fn)
}

// hint 是框下面的一行灰色说明（.hint2）。
func hint(c *ui.Context, fn func()) {
	ui.Row(c).Margin(6, 4, 0, 4).Gap(2).AlignItems(ui.Center).FontSize(12).TextColor(colTextMuted).Children(fn)
}

// pushButton 是白色的小按钮（.btn）。
func pushButton(c *ui.Context, label string) ui.Element {
	b := ui.ButtonBase(c).Height(22).Padding(0, 10).Radius(6).Center().Shrink(0).Label(label)
	tintSolid(b, colBgControl)
	hairline(b, 0.18)
	lift(b, 1, 1.5, 0.08)
	b.Children(func() {
		ui.Text(c, label).FontSize(13).TextColor(colTextPrimary).SingleLine().ContentTransition(labelTransition)
	})
	return b
}

// rescanButton 是设置页汇总栏右边的「重新检测 / 重新检查」按钮，按三态平滑过渡：
// 空闲（刷新图标 + 白底）→ 进行中（蓝色 Spinner + 禁点）→ 刚完成（绿色对勾确认态）。
func (a *app) rescanButton(c *ui.Context, tab int, idleLabel, runningLabel, doneLabel string) {
	phase := rescanIdle
	switch {
	case a.set.rescanPhase == rescanRunning:
		phase = rescanRunning
	case a.set.rescanPhase == rescanDone && a.set.rescanTab == tab:
		phase = rescanDone
	}

	label := idleLabel
	fg := colTextPrimary
	bgRest, bgHover, bgPress := colBgControl, colInkHover.Over(colBgControl), colInkPress.Over(colBgControl)
	switch phase {
	case rescanRunning:
		label = runningLabel
		fg = colTextSecondary
	case rescanDone:
		label = doneLabel
		fg = colOKText
		bgRest, bgHover, bgPress = colOKBg, colOKBg, colInkPress.Over(colOKBg)
	}

	b := ui.ButtonBase(c).Height(22).Padding(0, 10).Gap(5).Radius(6).Center().Shrink(0).Label(label).
		Disabled(phase == rescanRunning).
		Transition(ui.ElementTransition{Animation: &swapSpring, Position: true})
	tintBg(b, bgRest, bgHover, bgPress)
	if phase == rescanDone {
		b.Shadow(0, 0, 0, edgeSpread, colOKEdge)
	} else {
		hairline(b, 0.18)
		lift(b, 1, 1.5, 0.08)
	}
	fgAnim := b.AnimateColor("fg", fg, ui.Animation{Duration: hoverOut})
	b.TextColor(fgAnim)
	b.Children(func() {
		ui.Box(c).Size(12, 12).Center().Children(func() {
			switch phase {
			case rescanRunning:
				ui.Box(c.Key("run")).Center().Transition(swapTransition).Children(func() {
					ui.Spinner(c).Size(12, 12).TextColor(colAccent)
				})
			case rescanDone:
				ui.Box(c.Key("done")).Center().Transition(ui.ElementTransition{
					Animation: &popSpring,
					Enter:     &ui.Motion{Opacity: 0, Scale: 0.4},
					Exit:      &ui.Motion{Opacity: 0, Scale: 0.6},
				}).Children(func() {
					ui.Icon(c, iconCheck).Size(12, 12).TextColor(colOKText)
				})
			default:
				ui.Box(c.Key("idle")).Center().Transition(swapTransition).Children(func() {
					ui.Icon(c, iconRefresh).Size(12, 12).TextColor(colTextSecondary)
				})
			}
		})
		ui.Text(c, label).FontSize(12).SingleLine().ContentTransition(labelTransition)
	})
	if b.Clicked() {
		a.settingsRescan(tab)
	}
}

// settingsRescan 在后台重新扫描技能库并驱动按钮三段反馈；示例模式下保留内存开关，
// 同样展示完整扫描节奏并刷新最近检查时间。
func (a *app) settingsRescan(tab int) {
	if a.set.rescanPhase == rescanRunning {
		return
	}
	a.set.rescanSeq++
	seq := a.set.rescanSeq
	a.set.rescanTab = tab
	a.set.rescanPhase = rescanRunning
	a.changed()

	home, gen, epoch, live := a.lib.Home, a.watch.scanGen.Load(), a.watch.scanEpoch, a.live
	a.background(func(post func(func())) {
		start := time.Now()
		var lib *library.Library
		var rep library.UpdateReport
		if home != "" {
			lib = library.Scan(home)
			rep = lib.Updates()
		}
		if wait := minRescanSpin - time.Since(start); live && wait > 0 {
			time.Sleep(wait)
		}
		post(func() {
			if a.set.rescanSeq != seq {
				return
			}
			if lib != nil {
				switch {
				case gen != a.watch.scanGen.Load() || epoch != a.watch.scanEpoch:
					// 期间已有更新的扫描落地或待冲刷：保留较新结果，保留手动重检的 usage 刷新并触发 watch 补扫。
					if a.watch.pending != nil {
						a.watch.pending.reloadUsage = true
					} else {
						a.refreshUsage()
					}
					a.triggerWatchScan()
				case a.isWatchApplyDeferred():
					// 推进 scanGen，拒绝更早出发的 watch 扫描覆写这轮暂存结果。
					newGen := a.watch.scanGen.Add(1)
					a.watch.pending = &pendingWatchScan{lib: lib, report: rep, gen: newGen, reloadUsage: true}
				default:
					a.useScan(lib, rep)
				}
			} else {
				a.set.scannedAt = time.Now()
				a.changed()
			}
			a.set.rescanPhase = rescanDone
			a.changed()
			a.later(settingsRescanHold, func() {
				if a.set.rescanSeq == seq && a.set.rescanPhase == rescanDone {
					a.set.rescanPhase = rescanIdle
				}
			})
		})
	})
}

// saveSettings 写下设置，失败时显示在设置窗口里。
func (a *app) saveSettings() {
	defer a.changed()
	a.set.err = ""
	if err := a.lib.SaveSettings(); err != nil {
		a.set.err = err.Error()
		a.logSettingsError("save-settings", a.set.err)
	}
}

func (a *app) probeGHAuth(force bool) {
	if a.set.ghProbing || (!force && a.set.ghChecked) {
		return
	}
	a.set.ghProbing = true
	a.background(func(post func(func())) {
		ok, note := library.ProbeGHAuth()
		post(func() {
			a.set.ghProbing = false
			a.set.ghChecked = true
			a.set.ghOK = ok
			a.set.ghNote = note
			a.changed()
		})
	})
}

// settingsDo 显示 err；没出错且 rescan 时重新扫描。
func (a *app) settingsDo(err error, rescan bool) {
	defer a.changed()
	a.set.err = ""
	if err != nil {
		a.set.err = err.Error()
		a.logSettingsError("settings", a.set.err)
		return
	}
	if rescan {
		a.rescan()
	}
}

// checkedLabel 是「刚刚检查」「5 分钟前检查」「14:20 检查」。
func checkedLabel(t time.Time) string {
	switch d := time.Since(t); {
	case d < time.Minute:
		return locales.SettingsCheckedJustNow()
	case d < time.Hour:
		return locales.SettingsCheckedMinutesAgo(int(d.Minutes()))
	}
	return locales.SettingsCheckedAt(t.Format("15:04"))
}

func cond[T any](ok bool, yes, no T) T {
	if ok {
		return yes
	}
	return no
}

// took 是检查用时，不到 0.1 秒不写成 0.0 秒。
func took(d time.Duration) string {
	if d < 100*time.Millisecond {
		return locales.SettingsElapsedUnderTenth()
	}
	return locales.SettingsElapsed(d.Seconds())
}
