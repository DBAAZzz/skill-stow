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

// settingsState 是设置窗口的状态。
type settingsState struct {
	open       bool
	minimized  bool
	focused    bool
	tab        int
	allAgents  bool      // 「其余 N 个」展开
	scannedAt  time.Time // 上一次检测 / 检查的时间
	linkResult string    // 「检查并修复」的结果
	allDirs    bool      // 「在项目里查看的目录」展开
	err        string
	height     int // 窗口内容按当前标签该有的高度，上一帧量出来的
	ghProbing  bool
	ghChecked  bool
	ghOK       bool
	ghNote     string
}

// 设置窗口的宽、顶栏高和最高（再高就滚动）。
const (
	settingsWidth     = 660
	settingsBarHeight = 86
	settingsMaxHeight = 760
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
	// 先在屏幕外按当前标签排一遍，量出窗口该有的高度（fitSettings 记在
	// a.set.height）；窗口藏着建好，调到这个高度（盖过 StateKey 记住的上次
	// 高度）再显示。藏着的窗口不出帧，没法等它自己量，所以在外面量。
	// 这样打开时不会先按默认或上次的高度出现、再缩一下。
	ui.NewTester(a.settingsView, settingsWidth, settingsMaxHeight).Frame()
	win := mygo.NewWindow(mygo.WindowOptions{
		Hidden:               true,
		Title:                settingsTabs[a.set.tab].label(),
		Width:                settingsWidth,
		Height:               cmp.Or(a.set.height, 520),
		UseContentSize:       true,
		StateKey:             "settings",
		TitleBarStyle:        mygo.TitleBarHidden,
		TrafficLightPosition: &mygo.Point{X: 16, Y: 14},
		DisableResize:        true,
		DisableMaximize:      true,
		DisableFullScreen:    true,
		BackgroundColor:      "#ececee",
		Content:              ui.View(a.settingsView),
	})
	a.setWin = win
	a.closeMenusIn(menuWinSettings)
	win.SetContentSize(settingsWidth, cmp.Or(a.set.height, 520))
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

// settingsView 是设置窗口的内容：顶栏（标题、标签）和当前标签。
// 窗口高度跟着内容走，换标签时顶边不动，像 macOS 的设置窗口。
func (a *app) settingsView(c *ui.Context) {
	prevWin := a.menuBuildWin
	a.menuBuildWin = menuWinSettings
	defer func() { a.menuBuildWin = prevWin }()
	theme(c)
	c.Root().Background(colBgWindow)
	c.OnWindowShortcut(ui.Cmd, ui.KeyW, a.closeSettings)
	ui.Column(c).Fill().Children(func() {
		a.settingsHeader(c)
		ui.Scroll(c).Grow(1).MinHeight(0).Scrollbars(false).Children(func() {
			body := ui.Column(c.Key(a.set.tab)).Padding(18, 24, 22, 24).Children(func() {
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
			a.fitSettings(c, body.Bounds().H)
		})
	})
}

// fitSettings 按上一帧量出的内容高度调整窗口。
func (a *app) fitSettings(c *ui.Context, bodyH float32) {
	if bodyH <= 0 {
		c.Invalidate() // 第一帧还没量出来：再要一帧（openSettings 在屏幕外量时也靠它）
		return
	}
	h := min(settingsBarHeight+int(bodyH+0.99), settingsMaxHeight)
	if h == a.set.height {
		return
	}
	a.set.height = h
	if w := a.setWin; w != nil {
		go w.SetContentSize(settingsWidth, h) // 不在构建这一帧时改窗口
	}
}

// settingsHeader 是窗口顶栏（.tb）：标题、标签，可以拖动窗口。
// 红绿灯是系统画的，在左上角。
func (a *app) settingsHeader(c *ui.Context) {
	ui.Column(c).Height(settingsBarHeight).Shrink(0).Background(colBgTitlebar).BorderWidth(0, 0, 1, 0).BorderColor(colBorderSidebar).
		AlignItems(ui.Center).DragWindow().Children(func() {
		ui.Text(c, settingsTabs[a.set.tab].label()).FontWeight(600).TextColor(colTextBody).Margin(10, 0, 0, 0)
		ui.Spacer(c)
		ui.Row(c).Gap(4).Margin(0, 0, 6, 0).Children(func() {
			for i, tab := range settingsTabs {
				on := a.set.tab == i
				b := ui.ButtonBase(c.Key(i)).Size(88, 46).Radius(7).Gap(3).Column().AlignItems(ui.Center).Justify(ui.Center).Label(tab.label())
				selectableRow(b, on, colInkSelected)
				b.Children(func() {
					icon := ui.Icon(c, tab.icon).Size(20, 20).TextColor(colTextSecondary)
					text := ui.Text(c, tab.label()).FontSize(11).TextColor(colTextSecondary).SingleLine()
					if on {
						icon.TextColor(colAccent)
						text.TextColor(colTextPrimary)
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
	})
}

// ——— 通用 ———

func (a *app) generalTab(c *ui.Context) {
	groupLabel(c, locales.NavLibrary(), true)
	setBox(c, func() {
		setRow(c, true).Children(func() {
			n := 0
			for _, s := range a.lib.Skills {
				if s.Shared {
					n++
				}
			}
			rowText(c, locales.CommonLocation(), locales.SettingsLibraryPathAndCount(a.lib.Display(a.lib.Root), n), true)
			if pushButton(c, locales.ActionShowInFinder()).Clicked() {
				a.reveal(a.lib.Root)
			}
		})
		setRow(c, false).Children(func() {
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
		setRow(c, true).Padding(10, 12).Children(func() {
			rowText(c, locales.SettingsTheme(), "", false)
			ui.Row(c).Gap(14).Role(ui.RoleRadioGroup).Label(locales.SettingsTheme()).Children(func() {
				a.themeTile(c, themeLight, locales.SettingsThemeLight())
				a.themeTile(c, themeDark, locales.SettingsThemeDark())
				a.themeTile(c, themeAuto, locales.SettingsLanguageSystem())
			})
		})
		setRow(c, false).Children(func() {
			rowText(c, locales.SettingsLanguage(), "", false)
			a.languagePicker(c)
		})
		setRow(c, false).Children(func() {
			rowText(c, locales.SettingsTidySound(), locales.SettingsTidySoundHint(), false)
			on := !a.lib.Settings.QuietTidy
			if toggle(c, &on, locales.SettingsTidySound()).Changed() {
				a.lib.Settings.QuietTidy = !on
				a.saveSettings()
			}
		})
	})
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
		t := ui.Text(c, name).FontSize(11.5).TextColor(colTextBody)
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
		setRow(c, true).Children(func() {
			rowText(c, locales.SettingsShowInInterface(), locales.SettingsHiddenAgentNote(), false)
		})
		for _, ag := range shown {
			hidden := a.lib.Hidden(ag.ID)
			row := setRow(c.Key(ag.ID), false)
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
					FontFeatures("tnum").SingleLine().Shrink(0)
				on := !hidden
				if toggle(c, &on, locales.SettingsShowAgent(ag.DisplayName())).Margin(0, 0, 0, 4).Changed() {
					a.settingsDo(a.lib.SetHidden(ag.ID, !on), false)
				}
			})
		}
		if rest := len(agents) - shownAgents; rest > 0 {
			text := locales.ShowLess()
			if !a.set.allAgents {
				text = locales.SettingsOtherAgents(rest, counts[agents[shownAgents].ID])
			}
			more := ui.ButtonBase(c).Padding(8, 12).Justify(ui.Start).BorderWidth(1, 0, 0, 0).BorderColor(colBorderSubtle).
				TextColor(colAccent).Label(text)
			more.Children(func() { ui.Text(c, text).FontSize(12).SingleLine() })
			if more.Clicked() {
				a.set.allAgents = !a.set.allAgents
			}
		}
		sumRow(c, func() {
			ui.Text(c, scannedLabel(a.set.scannedAt, time.Now())).SingleLine()
			ui.Spacer(c)
			if pushButton(c, locales.ActionDetectAgain()).Clicked() {
				a.rescan()
			}
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
		setRow(c, true).Children(func() {
			rowText(c, locales.SettingsFindProjectsFromLogs(), locales.SettingsProjectDiscoveryNote(), false)
			if toggle(c, &set.AutoDiscover, locales.SettingsFindProjectsFromLogs()).Changed() {
				a.saveSettings()
				a.rescan()
			}
		})
		for _, src := range d.Sources {
			dim := src.Count < 0 || !set.AutoDiscover
			row := setRow(c.Key(src.Agent), false)
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
				ui.Text(c, count).FontSize(12).TextColor(cond(dim, colTextDisabled, colTextSecondary)).FontFeatures("tnum").SingleLine()
			})
		}
		sumRow(c, func() {
			if set.AutoDiscover {
				before, after := splitEmph(locales.SettingsDiscoverSummary(d.Total, emphMark))
				ui.Text(c, before).SingleLine()
				ui.Text(c, locales.SettingsWithSkills(d.WithSkills)).FontWeight(600).SingleLine()
				if after != "" {
					ui.Text(c, after).SingleLine()
				}
				ui.Text(c, locales.SettingsDiscoverMeta(checkedLabel(a.set.scannedAt), took(d.Took))).TextColor(colTextMuted).SingleLine()
			} else {
				ui.Text(c, locales.SettingsAutoDiscoverOff()).TextColor(colTextMuted).SingleLine()
			}
			ui.Spacer(c)
			if pushButton(c, locales.ActionCheckAgain()).Clicked() {
				a.rescan()
			}
		})
	})

	groupLabel(c, locales.SettingsManualFolders(), false)
	manual := a.lib.ManualProjects()
	setBox(c, func() {
		if len(manual) == 0 {
			setRow(c, true).Children(func() {
				ui.Text(c, locales.SettingsNoManualFolders()).FontSize(12).TextColor(colTextMuted)
			})
		}
		for i, p := range manual {
			setRow(c.Key(p.Dir), i == 0).Children(func() {
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
		add := ui.ButtonBase(c).Padding(8, 12).Gap(6).AlignItems(ui.Center).Justify(ui.Start).
			BorderWidth(1, 0, 0, 0).BorderColor(colBorderSubtle).TextColor(colAccent).Label(locales.ActionAddFolderEllipsis())
		add.Children(func() {
			ui.Icon(c, iconPlus).Size(13, 13)
			ui.Text(c, locales.ActionAddFolderEllipsis()).FontSize(12).SingleLine()
			ui.Spacer(c)
			ui.Text(c, locales.SettingsFolderSelectionHint()).FontSize(11.5).TextColor(colTextMuted).SingleLine()
		})
		if add.Clicked() {
			a.chooseProjects(false)
		}
	})

	groupLabel(c, locales.SettingsDisplay(), false)
	setBox(c, func() {
		setRow(c, true).Children(func() {
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
		setRow(c, true).Children(func() {
			ui.Text(c, locales.SettingsProjectDirectoriesHint()).
				FontSize(11.5).LineHeight(1.45).TextColor(colTextMuted).Grow(1).MinWidth(0)
		})
		ui.Row(c).Wrap().Gap(6).Padding(0, 12, 12, 12).Children(func() {
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
	l := sectionLabel(c, text).Margin(0, 0, 6, 4)
	if !first {
		l.Margin(18, 0, 6, 4)
	}
}

// setBox 是设置里的白色分组框（.box）。
func setBox(c *ui.Context, fn func()) {
	box := ui.Column(c).Radius(9).Background(colBgCard)
	hairline(box, 0.1)
	box.Clip().Children(fn)
}

// setRow 是框里的一行（.row），除第一行外上面有分隔线。
func setRow(c *ui.Context, first bool) ui.Element {
	row := ui.Row(c).MinHeight(36).Padding(5, 12).Gap(10).AlignItems(ui.Center)
	if !first {
		row.BorderWidth(1, 0, 0, 0).BorderColor(colBorderSubtle)
	}
	return row
}

// rowText 是一行左边的名称和下面一行说明；mono 时说明是路径。
func rowText(c *ui.Context, title, sub string, mono bool) {
	ui.Column(c).Grow(1).MinWidth(0).Gap(titleGap).Children(func() {
		ui.Text(c, title).FontWeight(500).SingleLine()
		if sub == "" {
			return
		}
		t := ui.Text(c, sub).FontSize(11.5).LineHeight(1.45).TextColor(colTextMuted)
		if mono {
			t.Font("monospace").FontSize(11).SingleLine()
		}
	})
}

// sumRow 是框底部的灰色汇总行（.sum）。
func sumRow(c *ui.Context, fn func()) {
	ui.Row(c).Padding(9, 12).Gap(6).AlignItems(ui.Center).Background(colBgSubtle).
		BorderWidth(1, 0, 0, 0).BorderColor(colBorderSubtle).FontSize(12).TextColor(colTextSecondary).Children(fn)
}

// hint 是框下面的一行灰色说明（.hint2）。
func hint(c *ui.Context, fn func()) {
	ui.Row(c).Margin(6, 4, 0, 4).Gap(2).AlignItems(ui.Center).FontSize(11.5).TextColor(colTextMuted).Children(fn)
}

// pushButton 是白色的小按钮（.btn）。
func pushButton(c *ui.Context, label string) ui.Element {
	b := ui.ButtonBase(c).Height(22).Padding(0, 10).Radius(6).Center().Shrink(0).Label(label)
	tintSolid(b, colBgControl)
	hairline(b, 0.18)
	lift(b, 1, 1.5, 0.08)
	b.Children(func() {
		ui.Text(c, label).FontSize(12).TextColor(colTextPrimary).SingleLine().ContentTransition(labelTransition)
	})
	return b
}

// saveSettings 写下设置，失败时显示在设置窗口里。
func (a *app) saveSettings() {
	defer a.changed()
	a.set.err = ""
	if err := a.lib.SaveSettings(); err != nil {
		a.set.err = err.Error()
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
