package main

import (
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"

	"skillmanager/internal/library"
	"skillmanager/locales"
)

// 添加技能页（画板「添加技能」）：输入框在最上，下面是拖放区和最近添加；
// 读取时整份下载到本机；读完进入选择页，顶部来源栏右边是「装到」「谁会
// 读到」和「添加 N 个技能」，左边勾选，右边预览。

const scopeImport = "import"

var (
	// 四宫格补一格（画板「添加技能图标」C）：三块由浅到深，第四格是加号
	iconNavImport = solidIcon(`<rect x="3.2" y="3.2" width="7.6" height="7.6" rx="2.6" opacity=".5"/><rect x="13.2" y="3.2" width="7.6" height="7.6" rx="2.6" opacity=".75"/><rect x="3.2" y="13.2" width="7.6" height="7.6" rx="2.6" opacity=".75"/><path d="M17 13.6v6.8M13.6 17h6.8" fill="none" stroke="currentColor" stroke-width="2.6" stroke-linecap="round"/>`)
	iconTray      = icon(`<path d="M4 13.5 6.5 6h11L20 13.5V18a1.5 1.5 0 0 1-1.5 1.5h-13A1.5 1.5 0 0 1 4 18z"/><path d="M4 13.5h4.5l1.2 2h4.6l1.2-2H20"/><path d="M12 3v7.5M9.5 8 12 10.5 14.5 8"/>`)
	iconTerminal  = icon(`<rect x="3.5" y="5" width="17" height="14" rx="2.5"/><path d="m7.5 10 2.5 2-2.5 2M12 14.5h4"/>`)
	iconBranch    = icon(`<circle cx="7" cy="6" r="2"/><circle cx="7" cy="18" r="2"/><circle cx="17" cy="8" r="2"/><path d="M7 8v8M17 10c0 3.5-4 3.5-8.5 6"/>`)
	iconShield    = icon(`<path d="M12 3.5 19 6v5.5c0 4.2-2.9 7.6-7 9-4.1-1.4-7-4.8-7-9V6z"/><path d="m9 12 2 2 4-4"/>`)
)

// importSidebar 是侧边栏「更新」下面的「添加技能」。
func (a *app) importSidebar(c *ui.Context) {
	row := a.sideRow(c, scopeImport, iconNavImport, locales.ActionAddSkills(), colTextSecondary).Height(sideRowHeight).Padding(0, 8)
	if row.Clicked() {
		a.checkClipboard()
	}
}

// importPage 画添加技能页。
func (a *app) importPage(c *ui.Context) {
	st := &a.imp
	if st.cmp != nil {
		a.importComparePage(c)
		return
	}
	if st.batch != nil {
		a.importPickPage(c)
		return
	}
	a.toolbarRow(c, func() {
		ui.Text(c, locales.ActionAddSkills()).FontSize(13).FontWeight(600).SingleLine()
	}, func() {
		ui.Spacer(c)
		if updButton(c, iconExternal, locales.ImportBrowseSkillsWebsite(), false).Clicked() {
			a.openExternalURL("https://skills.sh")
		}
	})
	w, _ := c.Size()
	ui.Scroll(c).Grow(1).MinHeight(0).Background(colBgPage).Scrollbars(false).Children(func() {
		pageColumn(c, w-a.sidebarRoom(), formMaxWidth, func(float32) { a.importHome(c) })
	})
}

// 入口页（画板「添加技能」第九轮）：系统设置那样的灰底白色分组。读取中、
// 出错、剪贴板建议都是第一个分组里输入行下面的一行，分组的高度跟着弹开
// 收起（里面的行都不压缩，动画中途只裁掉底下）；新出现的行淡入，不跳。
var (
	importGrow   = ui.Animation{Spring: ui.SpringOptions{Duration: 320 * time.Millisecond}}
	importRowIn  = ui.ElementTransition{Animation: &importGrow, Enter: &ui.Motion{Opacity: 0, Y: -4}}
	progressEase = ui.Animation{Spring: ui.SpringOptions{Duration: 650 * time.Millisecond}}
	// 读取中新找到的技能一个个弹进来，已有的换行时滑到新位置
	chipSpring = ui.Animation{Spring: ui.SpringOptions{Duration: 380 * time.Millisecond, Bounce: 0.12}}
	chipIn     = ui.ElementTransition{Animation: &chipSpring, Enter: &ui.Motion{Opacity: 0, Scale: 0.82, Y: 4}}
	// 数字往上翻：读取中只会变大
	countUp = ui.ContentTransition{Numeric: true}

	symErrorDot = colorSymbol(`<circle cx="12" cy="12" r="10" fill="#ff3b30"/><path d="M12 7v6.2" stroke="#fff" stroke-width="2.4" stroke-linecap="round"/><circle cx="12" cy="16.9" r="1.45" fill="#fff"/>`)
)

// importHome 是输入页：从链接添加、从电脑添加、最近添加三个分组。
func (a *app) importHome(c *ui.Context) {
	st := &a.imp
	reading := st.cancel != nil
	importSection(c, locales.ImportFromLink(), false, nil)
	usageGroup(c).Transition(ui.ElementTransition{Height: true, Animation: &importGrow}).Children(func() {
		a.importInputRow(c, reading)
		switch {
		case reading:
			a.importProgress(c)
		case st.err != nil:
			a.importErrorRow(c, st.err)
		case st.clip != "" && strings.TrimSpace(st.input) == "":
			a.importClipRow(c)
		}
	})
	note := locales.ImportReviewBeforeAdding()
	if reading {
		note = locales.ImportDownloadOnce()
	}
	ui.Text(c, note).FontSize(12).LineHeight(1.45).TextColor(colTextSecondary).Padding(7, 4, 0, 4).ContentTransition(labelTransition)

	rest := ui.Column(c).FillWidth()
	if reading {
		rest.Opacity(0.45).Disabled(true)
	}
	rest.Children(func() {
		importSection(c, locales.ImportFromComputer(), true, nil)
		usageGroup(c).Children(func() {
			ui.Row(c).MinHeight(54).Padding(10, 12, 10, 14).Gap(10).AlignItems(ui.Center).Children(func() {
				squircleIcon(c, iconNavFolder, 28, squircleBlue)
				ui.Column(c).Grow(1).MinWidth(0).Gap(2).Children(func() {
					ui.Text(c, locales.ImportChooseFolderOrZip()).FontSize(13).FontWeight(500).SingleLine()
					ui.Text(c, locales.ImportDropSkillFolder()).FontSize(12).TextColor(colTextSecondary).SingleLine()
				})
				if dialogButton(c, locales.ActionChooseEllipsis(), false).Shrink(0).Clicked() {
					a.chooseImport()
				}
			})
		})
		a.recentImports(c)
	})
}

// importSection 是分组上方的小标题，right 放右侧的链接。
func importSection(c *ui.Context, title string, gapAbove bool, right func()) {
	row := ui.Row(c).Padding(0, 4, 7, 4).Gap(6).AlignItems(ui.Center)
	if gapAbove {
		row.Margin(24, 0, 0, 0)
	}
	row.Children(func() {
		ui.Text(c, title).FontSize(11).FontWeight(600).TextColor(colTextSecondary).Grow(1).SingleLine()
		if right != nil {
			right()
		}
	})
}

// importInputRow 是第一个分组的首行：输入框和「读取」，读取中换成「取消」。
// 输入框聚焦时一圈系统蓝的光晕。
func (a *app) importInputRow(c *ui.Context, reading bool) {
	st := &a.imp
	ui.Row(c).Shrink(0).Padding(10, 12).Gap(8).AlignItems(ui.Center).Children(func() {
		field := ui.Row(c).Grow(1).MinWidth(0).Height(28).Radius(7).Padding(0, 9).Gap(7).AlignItems(ui.Center).
			Transition(ui.ElementTransition{Shadows: true, Colors: true, Duration: hoverIn})
		switch {
		case reading:
			field.Background(colBgSubtle)
			hairline(field, 0.16)
		case field.FocusWithin():
			field.Background(colBgCard).Shadow(0, 0, 0, edgeSpread, colAccent.Alpha(0.7)).Shadow(0, 0, 0, 3.5, colAccent.Alpha(0.22))
		default:
			field.Background(colBgCard)
			hairline(field, 0.22)
		}
		field.Children(func() {
			ic := iconLink
			if src, err := library.ParseImportInput(st.input); err == nil && src.Kind == library.ImportWebsite {
				ic = iconGlobe
			} else if strings.Contains(st.input, "github") || strings.Count(st.input, "/") == 1 {
				ic = iconGitHub
			}
			ui.Icon(c, ic).Size(14, 14).TextColor(colTextMuted).ContentTransition(labelTransition)
			in := ui.TextInputBase(c.Key("import-input"), &st.input).Placeholder(locales.ImportSourcePlaceholder()).
				FontSize(13).Grow(1).Label(locales.CommonSource()).Disabled(reading)
			if reading {
				in.TextColor(colTextSecondary)
			}
			if in.Submitted() {
				a.startImport(st.input)
			}
			if in.Changed() {
				st.err = nil
			}
		})
		switch {
		case reading:
			if dialogButton(c, locales.ActionCancel(), false).Shrink(0).Clicked() {
				a.stopImport()
			}
		case strings.TrimSpace(st.input) == "":
			dialogButton(c, locales.ImportRead(), false).Shrink(0).Disabled(true).TextColor(colTextMuted)
		default:
			if filledButton(c, locales.ImportRead(), colAccent).Shrink(0).Clicked() {
				a.startImport(st.input)
			}
		}
	})
}

// importBoxRow 是输入行下面的一行（剪贴板、出错），上面一道细线。
func importBoxRow(c *ui.Context, key string, fn func()) {
	ui.Row(c.Key(key)).Shrink(0).MinHeight(38).Padding(7, 12, 7, 14).Gap(10).AlignItems(ui.Center).Transition(importRowIn).Children(func() {
		rowSep(c, 14)
		fn()
	})
}

// importClipRow 是剪贴板里有能读的来源时的建议；输入框里有字就收起。
func (a *app) importClipRow(c *ui.Context) {
	st := &a.imp
	importBoxRow(c, "import-clip", func() {
		ic, what := iconGitHub, locales.ImportClipboardRepository()
		src, err := library.ParseImportInput(st.clip)
		if err == nil && src.Kind == library.ImportWebsite {
			ic, what = iconGlobe, locales.ImportClipboardWebsite()
		}
		if err == nil && len(src.Skills) == 1 {
			what = locales.ImportClipboardSkill()
		}
		ui.Icon(c, ic).Size(15, 15).TextColor(colAccent)
		ui.Row(c).Grow(1).MinWidth(0).Gap(8).AlignItems(ui.Baseline).Children(func() {
			ui.Text(c, what).FontSize(13).SingleLine().Shrink(0)
			ui.Text(c, clipLabel(st.clip)).FontSize(12).TextColor(colTextSecondary).SingleLine().MinWidth(0).Shrink(1)
		})
		if linkButton(c, locales.ImportPasteAndRead()).Shrink(0).Clicked() {
			st.input, st.clipSeen = st.clip, st.clip
			st.clip = ""
			a.startImport(st.input)
		}
		if iconButton(c, iconClose, locales.ActionNoThanks()).Size(20, 20).Clicked() {
			st.clipSeen, st.clip = st.clip, ""
		}
	})
}

// importErrorRow 是读取失败：一句话说明，下一步跟在后面。改好输入直接点
// 「读取」，所以不另放「重试」；连不上网时多一个「打开设置…」。
func (a *app) importErrorRow(c *ui.Context, e *library.ImportError) {
	importBoxRow(c, "import-error", func() {
		ui.Image(c, symErrorDot).Size(16, 16).Shrink(0).AlignSelf(ui.Start).Margin(1, 0, 0, 0)
		ui.Column(c).Grow(1).MinWidth(0).Gap(2).Children(func() {
			ui.Text(c, e.Msg).FontSize(13).FontWeight(500).LineHeight(1.45)
			if e.Hint != "" {
				ui.Text(c, library.Sentence(e.Hint)).FontSize(12).LineHeight(1.45).TextColor(colTextSecondary)
			}
		})
		if e.Network && linkButton(c, locales.ActionOpenSettings()).Shrink(0).Clicked() {
			a.openUpdateSettings()
		}
	})
}

// importProgress 是读取中：正在下载哪个提交、多大、已找到几个，一条进度条，
// 下面是找到的技能。数字往上翻，进度条弹簧追到新位置，新技能一个个弹进来。
func (a *app) importProgress(c *ui.Context) {
	st := &a.imp
	col := ui.Column(c.Key("import-progress")).Shrink(0).Padding(10, 12, 12, 14).Gap(9).Transition(importRowIn)
	col.Children(func() {
		rowSep(c, 14)
		ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
			ui.Spinner(c).Size(13, 13).TextColor(colAccent)
			what := locales.ImportReading(st.reading)
			if short := shortCommit(st.prog.Commit); short != "" {
				what = locales.ImportDownloading(st.reading, short)
			}
			ui.Text(c, what).FontSize(13).Grow(1).MinWidth(0).SingleLine().ContentTransition(labelTransition)
			ui.Row(c).Gap(12).Shrink(0).AlignItems(ui.Center).Children(func() {
				if st.prog.Bytes > 0 {
					ui.Text(c.Key("bytes"), library.HumanSize(st.prog.Bytes)).FontSize(12).TextColor(colTextMuted).
						FontFeatures("tnum").SingleLine().ContentTransition(countUp)
				}
				if n := len(st.prog.Found); n > 0 {
					ui.Text(c.Key("found"), locales.ImportSkillsFound(n)).FontSize(12).TextColor(colTextMuted).
						FontFeatures("tnum").SingleLine().ContentTransition(countUp)
				}
			})
		})
		// 总数要等仓库的目录读完才知道：之前是空的轨道，之后从 0 平滑地长
		var frac float32
		if st.prog.Total > 0 {
			frac = float32(st.prog.Done) / float32(st.prog.Total)
		}
		p := col.AnimateTo("progress", frac, progressEase)
		ui.Box(c).Height(4).Shrink(0).Radius(2).Background(colBgFill).Clip().Children(func() {
			ui.Box(c).Height(4).Radius(2).Background(colAccent).WidthPercent(min(1, max(0, p)) * 100)
		})
		if len(st.prog.Found) > 0 {
			ui.Row(c).Wrap().Gap(6).Children(func() {
				for _, n := range st.prog.Found {
					ui.Text(c.Key(n), n).FontSize(12).Padding(2, 7).Radius(5).Background(colBgSubtle).TextColor(colTextSecondary).
						SingleLine().Transition(chipIn)
				}
			})
		}
	})
}

// recentImports 是「最近添加」，没有记录时整组不出现。
func (a *app) recentImports(c *ui.Context) {
	recent := a.lib.RecentImports(6)
	if len(recent) == 0 {
		return
	}
	importSection(c, locales.ImportRecentlyAdded(), true, func() {
		if linkButton(c, locales.ImportViewInUpdates()).Clicked() {
			a.closeMenusIn(menuWinMain)
			a.goTo(scopeUpdates)
		}
	})
	usageGroup(c).Children(func() {
		for i, r := range recent {
			row := ui.ButtonBase(c.Key(r.Place)).MinHeight(40).Padding(0, 12).Gap(10).AlignItems(ui.Center).Label(r.Name)
			tintSolid(row, colBgCard)
			row.Children(func() {
				if i > 0 {
					rowSep(c, 44)
				}
				ic := iconGitHub
				switch r.Source.Type {
				case "local":
					ic = iconFolder
				case "well-known":
					ic = iconGlobe
				}
				ui.Box(c).Size(22, 22).Radius(6).Shrink(0).Background(colBgFill).Center().Children(func() {
					ui.Icon(c, ic).Size(13, 13).TextColor(colTextSecondary)
				})
				ui.Row(c).Grow(1).MinWidth(0).Gap(8).AlignItems(ui.Baseline).Children(func() {
					ui.Text(c, r.Name).FontSize(13).FontWeight(500).SingleLine().Shrink(0)
					ui.Text(c, r.Label(a.lib.Home)).FontSize(12).TextColor(colTextMuted).SingleLine().MinWidth(0).Shrink(1)
				})
				ui.Text(c, formatRecentDay(r.At, time.Now())).FontSize(12).TextColor(colTextMuted).FontFeatures("tnum").Shrink(0)
				ui.Icon(c, iconChevronRight).Size(11, 11).TextColor(colTextDisabled).Shrink(0)
			})
			if row.Clicked() {
				a.showImported(r.Name)
			}
		}
	})
}

// ——— 选择与预览 ———

func (a *app) importPickPage(c *ui.Context) {
	st := &a.imp
	b := st.batch
	a.toolbarRow(c, func() {
		if iconButton(c, iconBack, locales.ImportChooseAnotherSource()).Clicked() {
			a.discardImport()
			return
		}
		// 来源名只在下面的来源栏写一次（im6）
		ui.Text(c, locales.ActionAddSkills()).FontSize(13).FontWeight(600).SingleLine()
	}, func() {
		ui.Spacer(c)
		if b.Source.Kind == library.ImportWebsite {
			if updButton(c, iconExternal, locales.ActionOpenWebsite(), false).Clicked() {
				a.openExternalURL(b.BaseURL)
			}
		}
		if b.Source.Kind == library.ImportGitHub {
			if updButton(c, iconExternal, locales.ActionViewOnGitHub(), false).Clicked() {
				u := "https://github.com/" + b.Source.Repo
				if b.Source.Subpath != "" || b.Source.Ref != "" {
					ref := b.Source.Ref
					if ref == "" {
						ref = b.Commit
					}
					u += "/tree/" + ref + "/" + b.Source.Subpath
				}
				a.openExternalURL(u)
			}
		}
	})
	// 上面的按钮可能在这一帧里放下了这次读取（换一个来源）：后面不能再画它
	if st.batch == nil {
		return
	}
	a.importSourceBar(c)
	if st.batch == nil { // 「继续添加」
		return
	}
	if b.Source.IgnoredArgs {
		ui.Row(c).Padding(7, 16).Gap(6).AlignItems(ui.Center).Background(colAccentBg).BorderWidth(0, 0, 1, 0).BorderColor(colBorderDefault).Children(func() {
			ui.Icon(c, iconTerminal).Size(13, 13).TextColor(colAccent)
			ui.Text(c, locales.ImportCommandOptionsIgnored()).FontSize(13).TextColor(colTextPrimary)
		})
	}
	a.importSplit(c)
}

// importSourceBar 是顶部来源栏：左边来源、版本、已下载到本机、技能数；右边
// 「装到」「谁会读到」和主按钮。添加完成后右边换成「继续添加」「在『我的技能』中查看」。
func (a *app) importSourceBar(c *ui.Context) {
	st := &a.imp
	b := st.batch
	ui.Row(c).MinHeight(58).Padding(10, 16).Gap(10).AlignItems(ui.Center).Background(colBgPane).
		BorderWidth(0, 0, 1, 0).BorderColor(colBorderDefault).Children(func() {
		a.importSourceIcon(c, b)
		ui.Column(c).Gap(3).MinWidth(0).Shrink(1).Children(func() {
			ui.Text(c, b.Source.Label()).FontSize(14).FontWeight(600).SingleLine()
			// 技能数不在这里写：下面列表头已经有了
			ui.Row(c).Gap(10).AlignItems(ui.Center).TextColor(colTextMuted).Children(func() {
				iconLabel := func(ic *ui.SVG, text string) {
					ui.Row(c).Gap(4).AlignItems(ui.Center).Shrink(0).Children(func() {
						ui.Icon(c, ic).Size(11, 11)
						ui.Text(c, text).FontSize(12).SingleLine()
					})
				}
				switch b.Source.Kind {
				case library.ImportGitHub:
					ref := b.Source.Ref
					if ref == "" {
						ref = locales.ImportDefaultBranch()
					}
					iconLabel(iconBranch, ref)
					if b.Commit != "" {
						commitMeta(c, b.Commit, colTextSecondary, colTextMuted)
					}
				case library.ImportWebsite:
					iconLabel(iconGlobe, "well-known "+b.Protocol)
					ui.Text(c, locales.ImportWebsiteMutable()).FontSize(12).SingleLine().Shrink(1).MinWidth(0)
				default:
					iconLabel(iconFolder, locales.CommonLocal())
				}
			})
		})
		ui.Spacer(c)
		added, notAdded := a.importResultCounts()
		if added+notAdded > 0 && len(a.importAddable()) == 0 {
			text := locales.ImportSkillsAdded(added)
			if notAdded > 0 {
				text = locales.ImportAddedAndNotAdded(added, notAdded)
			}
			ui.Text(c, text).FontSize(12).TextColor(colTextSecondary).SingleLine()
			if updButton(c, nil, locales.ImportContinueAdding(), false).Clicked() {
				a.discardImport()
			}
			if filledButton(c, locales.ImportViewInMySkills(), colAccent).Clicked() {
				first := ""
				for _, s := range b.Skills {
					if st.results[s.Name].Added {
						first = s.Name
						break
					}
				}
				a.showImported(first)
			}
			return
		}
		ui.Text(c, locales.ImportInstallTo()).FontSize(12).TextColor(colTextSecondary).SingleLine()
		a.placementMenu(c)
		a.readersMenu(c)
		ui.Box(c).Width(1).Height(20).Background(colBorderDefault).Margin(0, 2)
		n := len(a.importAddable())
		label := locales.ActionAddSkills()
		switch r := len(a.importReplacements()); {
		case r > 0 && r == n:
			label = locales.ImportReplaceSkillCount(r)
		case r > 0:
			label = locales.ImportAddAndReplaceCount(n-r, r)
		case n > 0:
			label = locales.ImportAddSkillCount(n)
		}
		if st.adding {
			label = locales.ImportAdding()
		}
		btn := filledButton(c.Key("import-add"), label, colAccent).Padding(0, 16)
		noAgent := !st.place.Library && len(st.place.Agents) == 0
		if n == 0 || st.adding || a.importPending() || noAgent {
			btn.Disabled(true).Opacity(0.5)
		}
		switch {
		case noAgent:
			btn.Tooltip(locales.ImportChooseAtLeastOneAgent())
		case a.importPending():
			btn.Tooltip(locales.ImportSelectedStillDownloading())
		}
		if btn.Clicked() {
			a.requestAddImports()
		}
		a.importConfirmDialog(c)
	})
}

func (a *app) importResultCounts() (added, notAdded int) {
	for _, o := range a.imp.results {
		if o.Added {
			added++
		} else {
			notAdded++
		}
	}
	return added, notAdded
}

// importSourceIcon 是来源的头像或图标。
func (a *app) importSourceIcon(c *ui.Context, b *library.ImportBatch) {
	if b.Source.Kind == library.ImportGitHub {
		owner, _, _ := strings.Cut(b.Source.Repo, "/")
		if src := a.srcIcons["gh:"+owner]; src != nil {
			box := ui.Box(c).Size(34, 34).Radius(8).Clip().Background(colBgLogo).Shrink(0)
			hairline(box, 0.12)
			box.Children(func() { ui.Image(c, src).Size(34, 34).Fit(ui.Cover) })
			return
		}
		ui.Column(c).Size(34, 34).Radius(8).Background(colTextPrimary).Center().Shrink(0).Children(func() {
			ui.Icon(c, iconGitHub).Size(18, 18).TextColor(white)
		})
		return
	}
	if b.Source.Kind == library.ImportWebsite {
		if src := a.srcIcons[b.Site]; src != nil {
			box := ui.Box(c).Size(34, 34).Radius(8).Clip().Background(colBgLogo).Shrink(0)
			hairline(box, 0.12)
			box.Children(func() { ui.Image(c, src).Size(34, 34).Fit(ui.Cover) })
			return
		}
		squircleIcon(c, iconGlobe, 34, squircleTeal)
		return
	}
	squircleIcon(c, iconNavFolder, 34, squircleBlue)
}

// placementMenu 是「装到」：技能库（通用）或指定的 Agent。
func (a *app) placementMenu(c *ui.Context) {
	st := &a.imp
	current := locales.ImportSharedLibraryTarget()
	if !st.place.Library {
		current = locales.ImportSelectedAgents()
	}
	a.selectButton(c, "import.place", locales.ImportInstallTo(), current, st.adding, func(fm *frostedMenu) {
		readers := agentNames(a.lib.SharedReaders())
		if fm.radio(locales.ImportSharedLibraryTarget(), st.place.Library, withDetail(locales.ImportPlaceLibraryDetail(readers)), withKey("place-lib")) && !st.place.Library {
			a.setPlacement(importPlacementDefault())
		}
		if fm.radio(locales.ImportSelectedAgentsEllipsis(), !st.place.Library, withDetail(locales.ImportAgentDirectoryHint()), withKey("place-agents")) && st.place.Library {
			p := library.ImportPlacement{}
			if ids := a.lib.ImportPlaceable(); len(ids) > 0 {
				p.Agents = []string{ids[0]}
			}
			a.setPlacement(p)
			a.menuEntry("import.readers").open = true
		}
	})
}

// readersMenu 是「谁会读到」：技能库模式下列直接读技能库的（锁定）和另外
// 添加到的（勾选，建链接）；指定 Agent 模式下勾选装给谁，列出顺带也会读到的。
func (a *app) readersMenu(c *ui.Context) {
	st := &a.imp
	chosen, also := a.lib.ImportReaders(st.place)
	all := append(slices.Clone(chosen), also...)
	a.triggerMenu(c, "import.readers", menuPanelOpts{width: 340, alignEnd: true, padLead: true}, func(btn ui.Element, open bool) {
		btn.Height(28).Padding(0, 8, 0, 6).Gap(6).Radius(7).AlignItems(ui.Center).Label(locales.ImportWhoCanRead())
		if open {
			btn.Background(colInkSelected)
		} else {
			tintInk(btn)
		}
		btn.Children(func() {
			agentBadges(c, all, 18, 6)
			text := locales.CommonAgentCount(len(all))
			if !st.place.Library && len(chosen) > 0 && len(chosen) <= 2 {
				text = agentNames(chosen)
			}
			if len(all) == 0 {
				text = locales.ImportChooseAgents()
			}
			ui.Text(c, text).FontSize(13).TextColor(colTextPrimary).SingleLine()
			ui.Icon(c, iconChevronDown).Size(11, 11).TextColor(colTextSecondary)
		})
	}, func(fm *frostedMenu) {
		if st.place.Library {
			fm.header(locales.ImportDirectLibraryReaders(), locales.ImportDirectLibraryReadersHint())
			for _, id := range a.lib.SharedReaders() {
				fm.check(library.AgentName(id), true, withDisabled(true), withTrailing(locales.CommonShared()), withBadge(func(c *ui.Context) { agentBadge(c, id, 18) }), withKey("shared-"+id))
			}
			if linkable := a.lib.ImportLinkable(); len(linkable) > 0 {
				fm.separator()
				fm.header(locales.ImportAlsoAddTo(), locales.ImportCreateLinks())
				for _, id := range linkable {
					on := slices.Contains(st.place.Agents, id)
					if fm.check(library.AgentName(id), on, withKeepOpen(), withTrailing(a.lib.Display(a.lib.AgentSkillsDir(id))),
						withBadge(func(c *ui.Context) { agentBadge(c, id, 18) }), withKey("link-"+id)) {
						p := st.place
						p.Agents = toggleID(p.Agents, id)
						a.setPlacement(p)
					}
				}
			}
			fm.note(locales.ImportAgentChoiceScope())
			return
		}
		fm.header(locales.ImportInstallForAgents(), locales.ImportOwnDirectory())
		for _, id := range a.lib.ImportPlaceable() {
			on := slices.Contains(st.place.Agents, id)
			if fm.check(library.AgentName(id), on, withKeepOpen(), withTrailing(a.lib.Display(a.lib.AgentSkillsDir(id))),
				withBadge(func(c *ui.Context) { agentBadge(c, id, 18) }), withKey("agent-"+id)) {
				p := st.place
				p.Agents = toggleID(p.Agents, id)
				a.setPlacement(p)
			}
		}
		if len(also) > 0 {
			fm.separator()
			fm.header(locales.ImportIndirectReaders(), "")
			for _, id := range st.place.Agents {
				dir := a.lib.AgentSkillsDir(id)
				var readers []string
				for _, r := range a.lib.DirReaders(dir) {
					if slices.Contains(also, r) {
						readers = append(readers, r)
					}
				}
				if len(readers) > 0 {
					fm.note(locales.ImportAlsoReads(agentNames(readers), a.lib.Display(dir)))
				}
			}
		}
		// 按这一帧勾选之后的选择写：上面刚取消了最后一个时这里已经没有了
		if picked := st.place.Agents; len(picked) > 0 {
			first := library.AgentName(picked[0])
			dir := a.lib.Display(a.lib.AgentSkillsDir(picked[0]))
			text := locales.ImportPlaceAgentOnly(dir, first)
			if len(picked) > 1 {
				text = locales.ImportPlaceAgentsShared(dir, agentNames(picked[1:]))
			}
			fm.note(text)
		}
	})
}

func toggleID(ids []string, id string) []string {
	if slices.Contains(ids, id) {
		return slices.DeleteFunc(slices.Clone(ids), func(x string) bool { return x == id })
	}
	return append(slices.Clone(ids), id)
}

type tagTone int

const (
	tagToneWarn tagTone = iota
	tagToneCrit
	tagToneMuted
	tagToneOK
	tagToneAccent
)

type tagStyle struct {
	bg, border, fg ui.Color
}

// tagStyleOf 是一种标签的颜色，每次取当前深浅色的那一套。
func tagStyleOf(t tagTone) tagStyle {
	switch t {
	case tagToneWarn:
		return tagStyle{colWarnBg, colWarnEdge, colWarnText}
	case tagToneCrit:
		return tagStyle{colDangerBg, colDangerEdge, colDangerText}
	case tagToneMuted:
		return tagStyle{colBgFill, colBorderDefault, colTextSecondary}
	case tagToneOK:
		return tagStyle{colOKBg, colOKEdge, colOKText}
	case tagToneAccent:
		return tagStyle{colAccentBg, colAccentEdge, colAccentText}
	}
	return tagStyle{}
}

type importTagSpec struct {
	text    string
	tone    tagTone
	icon    *ui.SVG
	loading bool
}

// importTag 返回添加页技能状态的标签规格（半透色阶 + 内环细线 + 语义图标）。
func (a *app) importTag(s *library.ImportSkill) importTagSpec {
	st := &a.imp
	k := a.importKey(s)
	if o, ok := st.results[k]; ok {
		if o.Added {
			return importTagSpec{text: locales.CommonAdded(), tone: tagToneOK, icon: symOK}
		}
		return importTagSpec{text: locales.ImportNotAdded(), tone: tagToneCrit, icon: symCritical}
	}
	if st.fetching[k] {
		return importTagSpec{text: locales.CommonDownloading(), tone: tagToneMuted, loading: true}
	}
	switch st.checks[k].State {
	case library.ImportSame:
		return importTagSpec{text: locales.ImportAddedUpToDate(), tone: tagToneMuted, icon: symTagMuted}
	case library.ImportConflict:
		if a.importReplacing(k) {
			return importTagSpec{text: locales.ImportWillReplace(), tone: tagToneWarn, icon: symTagWarn}
		}
		return importTagSpec{text: locales.ImportNameExists(), tone: tagToneWarn, icon: symTagWarn}
	case library.ImportInvalid:
		return importTagSpec{text: locales.ImportCannotAdd(), tone: tagToneCrit, icon: symCritical}
	}
	// 本地检查排在缺描述、审计前面：高风险、没检查完的要先看到
	if spec, ok := a.importSecTag(s); ok {
		return spec
	}
	if s.MissingDescription() {
		return importTagSpec{text: locales.ImportMissingDescription(), tone: tagToneMuted, icon: symTagMinus}
	}
	if s.Risky() {
		return importTagSpec{text: locales.ImportScanRiskFound(), tone: tagToneWarn, icon: symTagWarn}
	}
	return importTagSpec{text: locales.CommonNew(), tone: tagToneAccent}
}

func tag(c *ui.Context, spec importTagSpec, compact bool) {
	st := tagStyleOf(spec.tone)
	h, padR, padL, gap, font, ico := float32(21), float32(8), float32(5.5), float32(5), float32(12), float32(12)
	if compact {
		h, padR, padL, gap, font, ico = 19, 7, 5, 4, 11, 11
	}
	if spec.icon == nil && !spec.loading {
		padL = padR
	}
	ui.Row(c).Height(h).Radius(h/2).Padding(0, padR, 0, padL).Gap(gap).
		AlignItems(ui.Center).Shrink(0).
		Background(st.bg).Border(edgeSpread, st.border).
		Children(func() {
			switch {
			case spec.loading:
				ui.Spinner(c).Size(ico-1, ico-1).TextColor(st.fg)
			case spec.icon != nil:
				ui.Image(c, spec.icon).Size(ico, ico).Shrink(0)
			}
			ui.Text(c, spec.text).FontSize(font).FontWeight(550).TextColor(st.fg).SingleLine()
		})
}

// importList 是左侧的技能列表：勾选框、名称、简介、状态标签。
func (a *app) importList(c *ui.Context) {
	st := &a.imp
	b := st.batch
	var selectable []string
	for _, s := range b.Skills {
		if k := a.importKey(s); st.checks[k].State == library.ImportNew && st.results[k].Name == "" {
			selectable = append(selectable, k)
		}
	}
	picked := 0 // 能直接添加的里勾上了几个；全选不碰要替换的
	for _, k := range selectable {
		if st.picked[k] {
			picked++
		}
	}
	ui.Column(c).Grow(1).MinWidth(0).Background(colBgSurface).Children(func() {
		ui.Row(c).Height(36).Shrink(0).Padding(0, 14).Gap(9).AlignItems(ui.Center).BorderWidth(0, 0, 1, 0).BorderColor(colBorderDefault).Children(func() {
			all := picked > 0 && picked == len(selectable)
			box := checkBox(c.Key("import-all"), all, picked > 0 && !all, len(selectable) > 0, locales.ActionSelectAll())
			if box.Clicked() {
				for _, n := range selectable {
					a.setImportPicked(n, !all)
				}
			}
			// 选了几个看复选框和主按钮上的数字，这里只写一共几个
			ui.Text(c, locales.CommonSkillCount(len(b.Skills))).FontSize(12).TextColor(colTextSecondary).FontFeatures("tnum").Grow(1).SingleLine()
			if b.Hidden > 0 {
				// 指定了技能时只读了那几个；要看仓库里其他的再整个读一遍
				if linkButton(c, locales.CommonShowAllTotal(len(b.Skills)+b.Hidden)).Tooltip(locales.ImportRepositoryHasMore(b.Hidden)).Clicked() {
					a.showAllImport()
				}
			}
			if len(selectable) > 0 && picked < len(selectable) {
				if linkButton(c, locales.ImportSelectAllAddable(len(selectable))).Clicked() {
					for _, n := range selectable {
						a.setImportPicked(n, true)
					}
				}
			}
		})
		ui.Scroll(c).Grow(1).Scrollbars(false).Children(func() {
			ui.Column(c).Padding(4, 6).Children(func() {
				for _, s := range b.Skills {
					a.importRow(c, s)
				}
			})
		})
	})
}

func (a *app) importRow(c *ui.Context, s *library.ImportSkill) {
	st := &a.imp
	k := a.importKey(s)
	chk := st.checks[k]
	o, done := st.results[k]
	conflict := replaceable(chk)
	can := (chk.State == library.ImportNew || conflict) && !done
	on := st.picked[k] || chk.State == library.ImportSame || done && o.Added
	row := ui.ButtonBase(c.Key("row-"+k)).Padding(8, 8).Gap(9).Radius(7).AlignItems(ui.Start).Label(s.Name)
	selectableRow(row, st.focus == k, colAccentBg)
	row.Children(func() {
		box := checkBox(c.Key("chk-"+k), on, false, can, locales.ImportSelectSkill(s.Name))
		if box.Clicked() && can {
			if conflict {
				a.setImportReplace(k, !st.picked[k])
			} else {
				a.setImportPicked(k, !st.picked[k])
			}
		}
		ui.Column(c).Grow(1).MinWidth(0).Gap(3).Children(func() {
			ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
				ui.Text(c, s.Name).FontSize(13).FontWeight(500).SingleLine().Shrink(1).MinWidth(0)
				tag(c, a.importTag(s), true)
			})
			desc := s.Description
			col := colTextSecondary
			switch {
			case done && o.Added:
				desc, col = a.lib.Display(o.Place), colTextMuted
			case done && o.Conflict != "":
				desc, col = locales.ImportJustAppeared(s.Name), colWarnText
			case done && o.Err != nil:
				desc, col = o.Err.Error(), colWarnText
			case st.fetchErr[k] != "":
				desc, col = st.fetchErr[k], colWarnText
			case chk.State == library.ImportConflict:
				desc = a.conflictLine(chk)
			case chk.State == library.ImportInvalid:
				desc, col = s.Problem, colWarnText
			case s.MissingDescription():
				desc, col = locales.ImportMissingDescriptionHint(), colTextMuted
			}
			ui.Text(c, desc).FontSize(12).TextColor(col).SingleLine()
		})
		if len(s.Exec) > 0 {
			ui.Icon(c, iconTerminal).Size(13, 13).TextColor(colTextDisabled).Margin(2, 0, 0, 0).Tooltip(locales.ImportHasExecutables(len(s.Exec)))
		}
	})
	if row.Clicked() {
		if st.focus != k {
			st.focus = k
			st.resetPreviewSelection()
		}
	}
}

// conflictLine 是已有同名时列表里的说明。
func (a *app) conflictLine(chk library.ImportCheck) string {
	dir := filepath.Dir(chk.Existing)
	if dir == a.lib.Root {
		return locales.ImportLibraryNameConflict()
	}
	return locales.ImportConflictIn(a.lib.Display(dir))
}

// checkBox 是 16 的方形勾选框；can 为 false 时淡显。
func checkBox(c *ui.Context, on, mixed, can bool, label string) ui.Element {
	b := ui.ButtonBase(c).Size(16, 16).Radius(4).Center().Shrink(0).Label(label).Margin(1, 0, 0, 0)
	switch {
	case (on || mixed) && can:
		b.Background(colAccent).TextColor(white)
	case on:
		b.Background(colControlDisabledOn).TextColor(white)
	case !can:
		b.Background(colBgSubtle).Shadow(0, 0, 0, 1, colBorderDefault)
	default:
		b.Background(colBgControl).Shadow(0, 0, 0, 1, colBorderStrong)
	}
	if !can {
		b.Disabled(true)
	}
	b.Children(func() {
		switch {
		case on:
			ui.Icon(c, iconCheck).Size(11, 11)
		case mixed:
			ui.Box(c).Size(8, 2).Radius(1).Background(white)
		}
	})
	return b
}

// importStatusNotes 展示预览区最上方的状态提示（添加完成、同名冲突、重复/无效）。
func (a *app) importStatusNotes(c *ui.Context, s *library.ImportSkill) {
	st := &a.imp
	k := a.importKey(s)
	chk := st.checks[k]
	o, done := st.results[k]
	var render func()
	switch {
	case done && o.Added:
		render = func() {
			insetGroup(c, func() {
				ui.Row(c).Padding(10, 14).Gap(10).AlignItems(ui.Center).Children(func() {
					ui.Image(c, symOK).Size(16, 16).Shrink(0).AlignSelf(ui.Start).Margin(1, 0, 0, 0)
					ui.Column(c).Grow(1).MinWidth(0).Gap(2).Children(func() {
						title := locales.ImportAddedTo(a.lib.Display(o.Place))
						if o.Replaced {
							title = locales.ImportReplacedAt(a.lib.Display(o.Place))
						}
						ui.Text(c, title).FontSize(13).FontWeight(600).TextColor(colTextPrimary)
						ui.Text(c, a.importedNote(s)).FontSize(12).LineHeight(1.45).TextColor(colTextSecondary)
					})
					if detailHairlineButton(c, "reveal-imported", locales.ActionShowInFinder()).Clicked() {
						a.reveal(o.Place)
					}
				})
			})
		}
	case done && o.Conflict != "" && o.Stale:
		render = func() {
			a.conflictCard(c, s, o.Conflict, locales.ImportChangedSinceConfirm(a.placeName(o.Conflict), s.Name),
				locales.ImportChangedSinceConfirmNote())
		}
	case done && o.Conflict != "":
		render = func() {
			a.conflictCard(c, s, o.Conflict, locales.ImportAppearedWhilePreviewing(a.placeName(o.Conflict), s.Name),
				locales.ImportConcurrentInstallNote())
		}
	case done && o.Err != nil:
		render = func() { errorLine(c, o.Err.Error()) }
	case chk.State == library.ImportConflict:
		render = func() {
			a.conflictCard(c, s, chk.Existing, locales.ImportConflictExisting(a.placeName(chk.Existing), s.Name), "")
		}
	case chk.State == library.ImportSame:
		render = func() {
			ui.Text(c, locales.ImportSameExisting(a.placeName(chk.Existing))).FontSize(12).TextColor(colTextSecondary)
		}
	case chk.State == library.ImportInvalid:
		render = func() { errorLine(c, s.Problem) }
	}
	if render != nil {
		ui.Column(c).Margin(14, 0, 0, 0).Gap(10).Children(render)
	}
}

// importedNote 说明添加后来源记成什么、会不会出现在「更新」里。
func (a *app) importedNote(s *library.ImportSkill) string {
	b := a.imp.batch
	switch b.Source.Kind {
	case library.ImportLocal:
		return locales.ImportSourceLocal(a.lib.Display(s.RepoPath))
	case library.ImportWebsite:
		return locales.ImportSourceWebsite(b.Site)
	}
	return locales.ImportSourceGitHub(b.Source.Repo, shortCommit(b.Commit))
}

// placeName 是已有那一项所在的位置怎么说：技能库，或 Agent 的目录。
func (a *app) placeName(existing string) string {
	dir := filepath.Dir(existing)
	if dir == a.lib.Root {
		return locales.NavLibrary()
	}
	if owner := a.lib.DirOwner(dir); owner != "" {
		return locales.ImportAgentSkillsDir(library.AgentName(owner))
	}
	return a.lib.Display(dir)
}

func riskLabel(r string) string {
	switch r {
	case "safe":
		return locales.ImportRiskSafe()
	case "low":
		return locales.ImportRiskLow()
	case "medium":
		return locales.ImportRiskMedium()
	case "high":
		return locales.ImportRiskHigh()
	case "critical":
		return locales.ImportRiskCritical()
	}
	return r
}

// conflictCard 是已有同名的提示（r11 方案 A）：采用与安全审计一致的 #f5f5f7 无边框分组，
// 16px 图标与正文严格对齐 x=40px 轨道，无内部分割线，右上角是「对比差异」和
// 「替换」。确认替换后列出添加时会移到废纸篓的项；不能替换时写原因。
func (a *app) conflictCard(c *ui.Context, s *library.ImportSkill, existing, title, body string) {
	b := a.imp.batch
	k := a.importKey(s)
	chk := a.imp.checks[k]
	replacing := a.importReplacing(k)
	headAlign := ui.Center
	if body != "" {
		headAlign = ui.Start
	}
	insetGroup(c, func() {
		ui.Row(c).Padding(10, 14, 6, 14).Gap(10).AlignItems(headAlign).Children(func() {
			img := ui.Image(c, symTagWarn).Size(16, 16).Shrink(0)
			if body != "" {
				img.Margin(1, 0, 0, 0)
			}
			ui.Column(c).Grow(1).MinWidth(0).Gap(2).Children(func() {
				ui.Text(c, title).FontSize(13).FontWeight(600).TextColor(colTextPrimary)
				if body != "" {
					ui.Text(c, body).FontSize(12).LineHeight(1.45).TextColor(colTextSecondary)
				}
			})
			if detailHairlineButton(c, "conflict-compare", locales.ActionCompareDifferences()).Clicked() {
				a.openImportCompare(s, existing)
			}
			if a.canDecideReplace(k) {
				label := locales.ActionReplace()
				if replacing {
					label = locales.ActionDontReplace()
				}
				if detailHairlineButton(c, "conflict-replace", label).Clicked() {
					a.setImportReplace(k, !replacing)
				}
			}
		})
		theirs, meta := a.lib.Display(s.RepoPath), locales.CommonLocal()
		switch b.Source.Kind {
		case library.ImportGitHub:
			theirs, meta = strings.TrimSuffix(b.Source.Repo+"/"+s.RepoPath, "/"), shortCommit(b.Commit)
		case library.ImportWebsite:
			theirs, meta = webPath(s.SourceURL), locales.CommonWebsite()
		}
		mine := ""
		if src, ok := a.lib.ImportedFrom(existing); ok {
			mine = src.String()
		} else if rec, ok := a.lib.InstallRecord(s.Name); ok && rec.Source != "" {
			mine = rec.Source
		}
		ui.Column(c).Padding(1, 14, 11, 40).Gap(3).Children(func() {
			for _, r := range [][3]string{{locales.ImportYourCopy(), a.lib.Display(existing), mine}, {locales.ImportIncomingCopy(), theirs, meta}} {
				ui.Row(c.Key(r[0])).Gap(10).AlignItems(ui.Center).Children(func() {
					ui.Text(c, r[0]).FontSize(12).TextColor(colTextSecondary).Width(56).Shrink(0).SingleLine()
					ui.Text(c, r[1]).FontSize(12).Font("monospace").TextColor(colTextBody).Grow(1).MinWidth(0).SingleLine()
					ui.Text(c, r[2]).FontSize(11).Font("monospace").TextColor(colTextMuted).Shrink(0).SingleLine()
				})
			}
		})
		switch {
		case chk.State == library.ImportConflict && chk.Blocked != "":
			ui.Text(c, chk.Blocked).FontSize(12).LineHeight(1.45).TextColor(colWarnText).Padding(0, 14, 11, 40)
		case replacing:
			ui.Column(c).Padding(0, 14, 11, 40).Gap(3).Children(func() {
				ui.Text(c, locales.ImportReplaceTrashNote()).FontSize(12).TextColor(colWarnText)
				for _, e := range chk.Replaces {
					ui.Row(c.Key("trash-" + e.Path)).Gap(10).AlignItems(ui.Center).Children(func() {
						ui.Text(c, a.lib.Display(e.Path)).FontSize(12).Font("monospace").TextColor(colTextBody).Grow(1).MinWidth(0).SingleLine()
						if e.Link {
							// 有了 Label 标签才进无障碍树，读屏才念得到悬停说明
							pillTag(c, locales.ImportReplaceLinkTag()).Label(locales.ImportReplaceLinkTag()).Tooltip(locales.ImportReplaceLinkOnly())
						}
					})
				}
			})
		}
	})
}

// canDecideReplace 报告这个技能现在能不能确认或撤回替换：已有同名且能替换，
// 或添加时撞上了同名（重新检查后再决定）。添加中、已添加的不行。
func (a *app) canDecideReplace(k string) bool {
	st := &a.imp
	if st.adding {
		return false
	}
	if o, done := st.results[k]; done {
		// 撞上的那一项后来又不在了也给按钮：重新决定时照常添加
		return !o.Added && o.Conflict != "" && (replaceable(st.checks[k]) || st.checks[k].State == library.ImportNew)
	}
	return replaceable(st.checks[k])
}

// openImportCompare 打开「你的」和「要添加的」的对比。
func (a *app) openImportCompare(s *library.ImportSkill, existing string) {
	real := existing
	if r, err := filepath.EvalSymlinks(existing); err == nil {
		real = r
	}
	a.imp.cmpName, a.imp.cmpKey = s.Name, a.importKey(s)
	a.imp.cmp = &compareState{
		files:    a.lib.Compare(&library.Version{Real: real}, &library.Version{Real: s.Dir}),
		open:     map[int]bool{},
		leftSub:  a.lib.Display(existing),
		rightSub: a.imp.batch.Source.Label(),
	}
}

func (a *app) importComparePage(c *ui.Context) {
	st := a.imp.cmp
	a.toolbarRow(c, func() {
		if iconButton(c, iconBack, locales.ActionBackShortcut()).Clicked() {
			a.imp.cmp = nil
		}
		ui.Row(c).Gap(8).AlignItems(ui.Baseline).Children(func() {
			ui.Text(c, a.imp.cmpName).FontSize(13).FontWeight(600).SingleLine()
			ui.Text(c, locales.ImportDifferentFileCount(len(st.files))).FontSize(12).TextColor(colTextMuted).FontFeatures("tnum").SingleLine()
		})
	}, func() {
		diffModeSwitch(c, &st.fullFile)
		if k := a.imp.cmpKey; a.canDecideReplace(k) {
			replacing := a.importReplacing(k)
			label := locales.ImportReplaceWithIncoming()
			if replacing {
				label = locales.ActionDontReplace()
			}
			if updButton(c.Key("compare-replace"), nil, label, false).Clicked() {
				a.setImportReplace(k, !replacing)
				if !replacing {
					a.imp.cmp = nil // 回到选择页，按「添加」时才真的替换
				}
			}
		}
	})
	ui.Row(c).Grow(1).MinHeight(0).AlignItems(ui.Stretch).Children(func() {
		diffFileTree(c, st)
		ui.Column(c).Grow(1).MinWidth(0).Children(func() {
			diffVerBar(c, func() {
				compareSide(c, locales.ImportYourCopy(), st.leftSub)
			}, func() {
				compareSide(c, locales.ImportIncomingCopy(), st.rightSub)
			})
			a.diffFileContent(c, st, nil)
		})
	})
}

func compareSide(c *ui.Context, title, sub string) {
	ui.Column(c).Padding(9, 14).Gap(2).Children(func() {
		ui.Text(c, title).FontSize(13).FontWeight(600).SingleLine()
		ui.Text(c, sub).FontSize(12).TextColor(colTextMuted).SingleLine()
	})
}

// ——— 拖入 ———

// dropLayer 让整个窗口都能接住拖进来的文件夹或 zip：拖到上面时出现蓝色内框，
// 松开后切到添加技能页读取，不直接安装。
func (a *app) dropLayer(c *ui.Context, host ui.Element) {
	if paths := host.DroppedFiles(); len(paths) > 0 {
		a.dropToImport(paths)
	}
	if host.FileDragOver() {
		ui.Box(c).Absolute().Top(6).Left(6).Right(6).Bottom(6).Radius(12).PassThrough().
			Border(2, colAccent).Background(colAccentBg).Children(func() {
			ui.Column(c).Fill().Center().Gap(8).Children(func() {
				ui.Column(c).Padding(14, 20).Radius(12).Background(colBgCard).Gap(4).AlignItems(ui.Center).
					Shadow(0, 8, 24, 0, shadow(0.12)).Children(func() {
					squircleIcon(c, iconNavFolder, 28, squircleBlue)
					ui.Text(c, locales.ImportDropToRead()).FontSize(13).FontWeight(600)
					ui.Text(c, locales.ImportDropReviewFirst()).FontSize(12).TextColor(colTextMuted)
				})
			})
		})
	}
}

// previewTabs 是预览右上角的小分段：SKILL.md / 文件 N。
func previewTabs(c *ui.Context, tab *int, labels ...string) {
	ui.Row(c).Height(24).Padding(2).Gap(1).Radius(7).Background(colBgFill).AlignItems(ui.Center).Shrink(0).Children(func() {
		for i, l := range labels {
			btn := ui.ButtonBase(c.Key("tab-"+l)).Height(20).Padding(0, 9).Radius(5).AlignItems(ui.Center).Shrink(0).Label(l)
			if *tab == i {
				btn.Background(colBgThumb).Shadow(0, 1, 2, 0, shadow(0.12))
			}
			btn.Children(func() { ui.Text(c, l).FontSize(12).SingleLine() })
			if btn.Clicked() {
				*tab = i
			}
		}
	})
}

// webPath 是网址去掉协议，给人看。
func webPath(u string) string {
	return strings.TrimPrefix(strings.TrimPrefix(u, "https://"), "http://")
}
