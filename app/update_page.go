package main

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/transfer"
	"github.com/egoist/mygo/ui"
	"runtime"
	"skillmanager/internal/library"

	"skillmanager/locales"
)

const scopeUpdates = "updates"

type updateBatchStatus string

const (
	batchActive updateBatchStatus = "active"
	batchDone   updateBatchStatus = "done"
	batchFailed updateBatchStatus = "failed"
)

type updatePageState struct {
	// tabled 记下这次看更新页时以表格（带表头）显示过的组：更新完只剩一行
	// 也照旧是表格，不换成单行样式整块重画。重新检查、离开更新页时清空。
	tabled   map[library.UpdateGroup]bool
	report   library.UpdateReport
	group    library.UpdateGroup
	cancel   context.CancelFunc
	task     updateTask
	progress library.UpdateProgress
	err      string
	errors   map[string]string
	batch    map[string]updateBatchStatus
	copied   string // 刚复制的更新命令：那个按钮暂时写「已复制」
	prepared *library.Prepared
	diff     *compareState
}

func (a *app) updateSidebar(c *ui.Context) {
	if runtime.GOOS != "darwin" {
		return
	}
	row := a.sideRow(c, scopeUpdates, iconNavUpdates, locales.NavUpdates(), colTextSecondary).Height(sideRowHeight).Padding(0, 8)
	if row.Clicked() && a.scope == scopeUpdates {
		a.upd.group = ""
		a.closeUpdateCompare()
	}
	row.Children(func() {
		if n := a.upd.report.Count(); n > 0 {
			ui.Text(c, fmt.Sprint(n)).FontSize(12).FontWeight(600).TextColor(colAccent).FontFeatures("tnum").SingleLine()
		}
	})
}

func (a *app) updatePage(c *ui.Context) {
	st := &a.upd
	if st.prepared != nil {
		a.updateComparePage(c)
		return
	}
	title := locales.NavUpdates()
	if st.group != "" {
		title = string(st.group)
	}
	a.toolbarRow(c, func() {
		if st.group != "" {
			if iconButton(c, iconBack, locales.UpdateBack()).Clicked() {
				st.group = ""
			}
		}
		ui.Text(c, title).FontSize(13).FontWeight(600).SingleLine()
	}, func() {
		ui.Spacer(c)
		if st.cancel != nil {
			ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
				ui.Spinner(c).Size(13, 13).TextColor(colAccent)
				ui.Text(c, updateProgressText(st.task, st.progress)).FontSize(12).TextColor(colTextSecondary).FontFeatures("tnum").SingleLine()
			})
			if updButton(c, nil, locales.UpdateStopTask(string(st.task)), false).Clicked() {
				st.cancel()
			}
		} else {
			if !st.report.Checked.IsZero() {
				ui.Text(c, locales.UpdateLastChecked(formatCommitTime(st.report.Checked))).FontSize(12).TextColor(colTextMuted).FontFeatures("tnum").SingleLine()
			}
			if updButton(c, iconRefresh, locales.UpdateCheck(), false).Clicked() {
				a.checkUpdates(false)
			}
		}
		a.iconActionMenu(c, "update.toolbar", iconMore, locales.UpdateMoreActions(), actionIconOpts{
			alignEnd: true, padLead: true,
		}, func(fm *frostedMenu) {
			groups := []struct {
				g  library.UpdateGroup
				ic *ui.SVG
			}{
				{library.GroupIgnored, iconEyeOff},
				{library.GroupStopped, iconPauseCircle},
				{library.GroupOther, iconInbox},
			}
			for _, item := range groups {
				if fm.check(item.g.Label(), st.group == item.g, withIcon(item.ic), withTrailCheck(), withActiveBg()) {
					st.group = item.g
					a.selected = nil
				}
			}
			fm.separator()
			if fm.action(locales.UpdateSettingsEllipsis(), withIcon(iconSettings)) {
				a.openUpdateSettings()
			}
		})
	})
	a.withPane(c, func(width float32) {
		ui.Box(c).Grow(1).MinHeight(0).FillWidth().Children(func() {
			a.updateBody(c, width)
		})
	})
}

func updateProgressText(task updateTask, p library.UpdateProgress) string {
	switch task {
	case taskCheck:
		return locales.UpdateCheckingRepositories(p.Done, p.Total)
	case taskUpdate:
		return locales.UpdateUpdatingSkills(p.Done, p.Total)
	case taskDownload:
		if p.Total > 1 {
			return locales.UpdateDownloadingFiles(p.Done, p.Total)
		}
		return locales.UpdateDownloading()
	default:
		return locales.UpdateWorking(string(task))
	}
}

// updateTask 是更新页后台正在做的事。值不给人看：按钮和进度的说法在目录里
// 按它选（updateStopTask、updateWorking）。
type updateTask string

const (
	taskCheck    updateTask = "check"
	taskDownload updateTask = "download"
	taskUpdate   updateTask = "update"
	taskRestore  updateTask = "restore"
	taskSave     updateTask = "save"
	taskKeep     updateTask = "keep"
)

func (a *app) updateBody(c *ui.Context, width float32) {
	st := &a.upd
	if st.group == "" && st.report.Checked.IsZero() && st.cancel == nil && !hasUpdateAttention(st.report) {
		ui.Column(c).Fill().Background(colBgPage).Children(func() {
			if st.err != "" {
				errorLine(c, st.err)
			}
			if st.report.ReadOnly != "" {
				errorLine(c, st.report.ReadOnly)
			}
			ui.Column(c).Grow(1).Padding(24, 24, 72, 24).Center().Gap(10).Children(func() {
				ui.Column(c).Size(44, 44).Radius(22).Background(colAccentBg).Center().Children(func() {
					ui.Icon(c, iconRefresh).Size(20, 20).TextColor(colAccent)
				})
				ui.Text(c, locales.UpdateNotCheckedYet()).FontSize(14).FontWeight(600).TextColor(colTextPrimary).Margin(2, 0, 0, 0)
				ui.Text(c, locales.UpdateCheckDescription()).FontSize(12).TextColor(colTextMuted)
				if filledButton(c, locales.UpdateCheck(), colAccent).Margin(6, 0, 0, 0).Clicked() {
					a.checkUpdates(false)
				}
			})
		})
		return
	}

	padH := float32(32)
	if width < 640 {
		padH = 16
	}
	ui.Scroll(c).Fill().Background(colBgPage).Scrollbars(false).Children(func() {
		area := ui.Column(c).FillWidth().Padding(20, padH, 24, padH).AlignItems(ui.Center)
		area.Children(func() {
			ui.Column(c).FillWidth().MaxWidth(880).Gap(22).Children(func() {
				if st.err != "" {
					errorLine(c, st.err)
				}
				if st.report.ReadOnly != "" {
					errorLine(c, st.report.ReadOnly)
				}
				if st.group == "" {
					a.updateSummarySection(c)
				}
				if !st.report.Reset.IsZero() {
					quotaNotice(c, st.report.Reset)
				}
				groups := []library.UpdateGroup{
					library.GroupReady,
					library.GroupConfirm,
					library.GroupHandle,
					library.GroupFailed,
					library.GroupWebsite,
					library.GroupUnsupported,
				}
				if st.group != "" {
					groups = []library.UpdateGroup{st.group}
				}
				found := false
				for _, g := range groups {
					var items []library.UpdateItem
					for _, it := range st.report.Items {
						if it.State.Group() == g {
							items = append(items, it)
						}
					}
					if len(items) == 0 {
						continue
					}
					found = true
					a.updateGroupSection(c.Key(string(g)), g, items, width)
				}
				if st.group != "" && !found {
					updCard(c).Padding(28, 20).Center().Children(func() {
						ui.Text(c, locales.UpdateNoSkillsHere()).FontSize(13).TextColor(colTextSecondary)
					})
				}
			})
		})
		a.deselectOnBlank(area)
	})
}

func (a *app) updateSummarySection(c *ui.Context) {
	st := &a.upd
	// 有可更新的技能时这张卡一直在，更新进行中也不撤掉：撤掉会让下面整页
	// 往上跳，更新完又跳回来。单个更新时只是按钮不能点；一起更新时这张卡
	// 原地显示进度。
	if ready := st.report.Ready(); len(ready) > 0 {
		n := len(ready)
		batch := st.cancel != nil && st.task == taskUpdate && st.progress.Total > 1
		confirmCount := 0
		for _, it := range st.report.Items {
			if it.State.Group() == library.GroupConfirm {
				confirmCount++
			}
		}
		title, sub, action := locales.UpdateReadyCount(n), locales.UpdateReplaceDirectoriesHint(), locales.UpdateSelectedCount(n)
		if confirmCount > 0 {
			sub = locales.UpdateConfirmationExcluded()
		}
		if batch {
			title = locales.UpdateUpdatingSkills(st.progress.Done, st.progress.Total)
			sub, action = locales.UpdateCheckAfterCompletion(), locales.UpdateUpdating()
		}
		updCard(c.Key("sum-ready")).Transition(updateRowMotion).Padding(14, 18).Row().Gap(14).AlignItems(ui.Center).Children(func() {
			squircleIcon(c, iconDownload, 38, squircleBlue)
			ui.Column(c).Grow(1).MinWidth(0).Gap(2).Children(func() {
				ui.Text(c, title).FontSize(15).FontWeight(600).FixedLineHeight(20).FontFeatures("tnum").ContentTransition(labelTransition)
				ui.Text(c, sub).FontSize(12).TextColor(colTextSecondary).ContentTransition(labelTransition)
			})
			if filledButton(c, action, colAccent).Disabled(st.cancel != nil).Clicked() {
				a.runUpdates(ready, nil)
			}
		})
		return
	}
	switch {
	case st.report.Checked.IsZero():
		if st.cancel == nil {
			updCard(c.Key("sum-unchecked")).Transition(updateRowMotion).Padding(14, 18).Row().Gap(14).AlignItems(ui.Center).Children(func() {
				ui.Box(c).Size(38, 38).Radius(10).Background(colAccentBg).Center().TextColor(colAccent).Children(func() {
					ui.Icon(c, iconRefresh).Size(18, 18)
				})
				ui.Column(c).Grow(1).MinWidth(0).Gap(2).Children(func() {
					ui.Text(c, locales.UpdateNotCheckedYet()).FontSize(14).FontWeight(600).FixedLineHeight(20).TextColor(colTextPrimary)
					ui.Text(c, locales.UpdateCheckDescription()).FontSize(12).TextColor(colTextSecondary)
				})
				if filledButton(c, locales.UpdateCheck(), colAccent).Clicked() {
					a.checkUpdates(false)
				}
			})
		}
	case !st.report.Complete:
		updCard(c.Key("sum-incomplete")).Transition(updateRowMotion).Padding(14, 18).Row().Gap(12).AlignItems(ui.Center).Children(func() {
			ui.Box(c).Size(32, 32).Radius(16).Background(colWarnBg).Center().TextColor(colWarnText).Children(func() {
				ui.Icon(c, iconWarn).Size(16, 16)
			})
			ui.Column(c).Grow(1).MinWidth(0).Gap(2).Children(func() {
				ui.Text(c, locales.UpdateCheckIncomplete()).FontSize(14).FontWeight(600).TextColor(colTextPrimary)
				ui.Text(c, locales.UpdateRecheckIncomplete()).FontSize(12).TextColor(colTextSecondary)
			})
			if updButton(c, iconRefresh, locales.ActionCheckAgain(), false).Disabled(st.cancel != nil).Clicked() {
				a.checkUpdates(false)
			}
		})
	case st.report.Eligible == 0:
		updCard(c.Key("sum-none")).Transition(updateRowMotion).Padding(18, 20).Row().Gap(14).AlignItems(ui.Center).Children(func() {
			ui.Column(c).Grow(1).MinWidth(0).Gap(3).Children(func() {
				ui.Text(c, locales.UpdateNoSupportedSkills()).FontSize(14).FontWeight(600).TextColor(colTextPrimary)
				ui.Text(c, locales.UpdateSupportedSkillsHint()).FontSize(12).TextColor(colTextMuted)
			})
		})
	case !hasUpdateAttention(st.report):
		updCard(c.Key("sum-clear")).Transition(updateRowMotion).Padding(14, 18).Row().Gap(12).AlignItems(ui.Center).Children(func() {
			ui.Box(c).Size(32, 32).Radius(16).Background(colOKBg).Center().TextColor(colOKText).Children(func() {
				ui.Icon(c, iconCheck).Size(16, 16)
			})
			ui.Column(c).Grow(1).MinWidth(0).Gap(2).Children(func() {
				ui.Text(c, locales.UpdateNothingToUpdate()).FontSize(14).FontWeight(600).TextColor(colTextPrimary)
				ui.Text(c, locales.UpdateLastChecked(formatRecentDay(st.report.Checked, time.Now()))).FontSize(12).TextColor(colTextMuted).FontFeatures("tnum")
			})
		})
	}
}

func (a *app) updateGroupSection(c *ui.Context, g library.UpdateGroup, items []library.UpdateItem, width float32) {
	st := &a.upd
	// 一组整个没了（最后一个更新完）或新出现时同样收起、展开
	ui.Column(c).FillWidth().Gap(7).Transition(updateRowMotion).Children(func() {
		// 定高：右边换成「正在更新」胶囊时这一行不变高，下面的表格不动
		ui.Row(c).Height(24).Padding(0, 4).Gap(7).AlignItems(ui.Center).Children(func() {
			ui.Box(c).Size(7, 7).Radius(3.5).Shrink(0).Background(groupDotColor(g))
			ui.Text(c, g.Label()).FontSize(13).FontWeight(600).TextColor(colTextPrimary).SingleLine()
			ui.Text(c, fmt.Sprint(len(items))).FontSize(12).TextColor(colTextMuted).FontFeatures("tnum").SingleLine()
			ui.Spacer(c)
			if st.cancel != nil && st.task == taskUpdate && g == library.GroupReady {
				ui.Row(c).Height(24).Padding(0, 10, 0, 8).Radius(12).Background(colBgFill).Gap(6).AlignItems(ui.Center).Children(func() {
					ui.Spinner(c).Size(12, 12).TextColor(colAccent)
					ui.Text(c, locales.UpdateProgress(st.progress.Done, st.progress.Total)).FontSize(12).FontWeight(500).TextColor(colTextPrimary).FontFeatures("tnum").SingleLine()
				})
			} else if sub := groupSubtitle(g); sub != "" {
				ui.Text(c, sub).FontSize(12).TextColor(colTextMuted).SingleLine()
			}
		})

		updCard(c).Children(func() {
			if g == library.GroupWebsite || g == library.GroupUnsupported {
				for i, it := range items {
					updateRowCell(c, it, func() { a.updateSourceRow(c, it, i == 0) })
				}
				return
			}
			single := len(items) == 1 && st.group == "" && !st.tabled[g]
			if !single {
				if st.tabled == nil {
					st.tabled = map[library.UpdateGroup]bool{}
				}
				st.tabled[g] = true
			}
			if !single {
				a.updateTableHeader(c, g, width)
			}
			for i, it := range items {
				updateRowCell(c, it, func() { a.updateRow(c, it, i == 0, single, width) })
			}
		})
	})
}

// updateRowCell 包住表格里的一行。按技能认行，不按位置：更新完的那一行
// 离开这一组时原地收起、淡出，下面的行跟着滑上来；新来的行同样展开进来。
// 别的行不动。
func updateRowCell(c *ui.Context, it library.UpdateItem, row func()) {
	ui.Column(c.Key(it.Source.String() + "/" + it.Name + "@" + it.Record.Place)).Transition(updateRowMotion).Children(row)
}

var updateRowMotion = ui.ElementTransition{
	Duration: 260 * time.Millisecond,
	Ease:     paneEase,
	Enter:    &ui.Motion{Collapse: true, Opacity: 0},
	Exit:     &ui.Motion{Collapse: true, Opacity: 0},
}

func groupDotColor(g library.UpdateGroup) ui.Color {
	switch g {
	case library.GroupReady:
		return colAccent
	case library.GroupConfirm:
		return colWarn
	case library.GroupHandle:
		return colGroupHandle
	case library.GroupFailed:
		return colDanger
	case library.GroupWebsite:
		return colGroupWebsite
	case library.GroupOther:
		return colOKText
	default:
		return colTextMuted
	}
}

func groupSubtitle(g library.UpdateGroup) string {
	switch g {
	case library.GroupConfirm:
		return locales.UpdateCompareBeforeUpdating()
	case library.GroupWebsite:
		return locales.UpdateSourceNotAutomatic()
	case library.GroupIgnored:
		return locales.UpdateIgnoredReminderHint()
	default:
		return ""
	}
}

func updateCols(width float32) (skillW, srcW float32, compact bool) {
	switch {
	case width >= 820:
		return 180, 138, false
	case width >= 620:
		return 136, 106, false
	default:
		return 0, 0, true
	}
}

func (a *app) updateTableHeader(c *ui.Context, g library.UpdateGroup, width float32) {
	infoCol := locales.TidyCondition()
	switch g {
	case library.GroupReady:
		if a.upd.cancel != nil && a.upd.task == taskUpdate {
			infoCol = locales.CommonStatus()
		} else {
			infoCol = locales.UpdateUpstreamCommit()
		}
	case library.GroupHandle, library.GroupFailed:
		infoCol = locales.CommonReason()
	}
	skillW, srcW, compact := updateCols(width)
	ui.Row(c).Height(34).Padding(0, 16).Gap(14).AlignItems(ui.Center).Background(colBgSubtle).
		BorderWidth(0, 0, 1, 0).BorderColor(colBorderSubtle).FontSize(12).FontWeight(500).TextColor(colTextMuted).Children(func() {
		col1 := ui.Text(c, locales.CommonSkills()).SingleLine()
		col2 := ui.Text(c, locales.CommonSource()).SingleLine()
		col3 := ui.Text(c, infoCol).SingleLine()
		if compact {
			col1.Grow(1).Basis(0).MinWidth(0)
			col2.Grow(0.8).Basis(0).MinWidth(0)
			col3.Grow(1.2).Basis(0).MinWidth(0)
		} else {
			col1.Width(skillW).Shrink(0)
			col2.Width(srcW).Shrink(0)
			col3.Grow(1).MinWidth(0)
		}
		ui.Row(c).MinWidth(52).Shrink(0).Justify(ui.End).Children(func() {
			ui.Text(c, locales.CommonActions()).SingleLine()
		})
	})
}

func hasUpdateAttention(r library.UpdateReport) bool {
	for _, it := range r.Items {
		switch it.State.Group() {
		case library.GroupReady, library.GroupConfirm, library.GroupHandle, library.GroupFailed:
			return true
		}
	}
	return false
}

func (a *app) updateRow(c *ui.Context, it library.UpdateItem, first, single bool, width float32) {
	if !first {
		ui.Box(c).Height(1).Margin(0, 16).Background(colBorderSubtle)
	}
	skillW, srcW, compact := updateCols(width)
	selected := a.selected != nil && a.selected.Name == it.Name
	row := ui.Row(c).MinHeight(42).Padding(8, 16).Gap(14).AlignItems(ui.Center)
	if single {
		row.MinHeight(46).Padding(10, 16)
	}
	if selected {
		row.Background(colAccentBg)
	}
	row.Children(func() {
		cSkill := ui.Row(c).Gap(6).AlignItems(ui.Center).MinWidth(0)
		if compact {
			cSkill.Grow(1).Basis(0)
		} else {
			cSkill.Width(skillW).Shrink(0)
		}
		cSkill.Children(func() {
			skill := a.lib.Get(it.Name)
			if skill != nil {
				b := ui.ButtonBase(c).Label(it.Name).MinWidth(0).Shrink(1).Cursor(ui.CursorPointer)
				b.Children(func() {
					ui.Text(c, it.Name).FontSize(13).FontWeight(500).TextColor(colAccent).SingleLine()
				})
				if b.Clicked() {
					a.selected, a.coll, a.picked = skill, nil, true
				}
			} else {
				ui.Text(c, it.Name).FontSize(13).FontWeight(500).TextColor(colTextPrimary).SingleLine().MinWidth(0).Shrink(1)
			}
			if in := a.lib.InstalledIn(it); in != "" {
				pillTag(c, locales.UpdateInstalledIn(in))
			}
			if it.State == library.UpdIgnored {
				pillTag(c, locales.UpdateIgnored())
			}
		})

		cSrc := a.sourceTextLink(c, it.Source.String(), it.WebURL(), colTextSecondary, func(t ui.Element) {
			t.FontSize(12)
		})
		if compact {
			cSrc.Grow(0.8).Basis(0)
		} else {
			cSrc.Width(srcW).Shrink(0)
		}

		cInfo := ui.Column(c).MinWidth(0).Gap(3)
		if compact {
			cInfo.Grow(1.2).Basis(0)
		} else {
			cInfo.Grow(1)
		}
		cInfo.Children(func() {
			a.updateInfoCell(c, it)
		})

		ui.Row(c).Shrink(0).Justify(ui.End).AlignItems(ui.Center).Gap(6).Children(func() {
			a.updateActions(c, it)
		})
	})
}

func (a *app) updateInfoCell(c *ui.Context, it library.UpdateItem) {
	if status, ok := a.upd.batch[it.Name]; ok {
		switch status {
		case batchDone:
			msg := locales.UpdateUpdated()
			if it.Commit != "" {
				msg = locales.UpdateUpdatedTo(shortCommit(it.Commit))
			}
			ui.Text(c, msg).FontSize(12).TextColor(colOKText).SingleLine()
			return
		case batchActive:
			ui.Text(c, locales.UpdateDownloadingVersion()).FontSize(12).TextColor(colAccent).SingleLine()
			return
		}
	}

	if it.State == library.UpdReady && !it.CommitDate.IsZero() && !it.Stale {
		ui.Row(c).Gap(12).AlignItems(ui.Center).MinWidth(0).Children(func() {
			ui.Text(c, formatCommitTime(it.CommitDate)).Font("monospace").FontSize(12).TextColor(colTextSecondary).FontFeatures("tnum").SingleLine()
			if it.Commit != "" {
				commitMeta(c, it.Commit, colTextBody, colTextMuted)
			}
		})
		if e := a.upd.errors[it.Name]; e != "" {
			ui.Text(c, e).FontSize(12).TextColor(colDangerText)
		}
		return
	}

	text, color, sha := a.updateReasonInfo(it)
	if it.Stale {
		text = locales.UpdateNotCheckedThisTime(text)
	}
	ui.Row(c).Gap(10).AlignItems(ui.Center).MinWidth(0).Children(func() {
		ui.Text(c, text).FontSize(12).LineHeight(1.35).TextColor(color).FontFeatures("tnum").MinWidth(0).Shrink(1)
		if sha != "" {
			shaColor, iconColor := colTextBody, colTextMuted
			if it.State == library.UpdBoth || it.State == library.UpdUnknownBase {
				shaColor, iconColor = colWarnText, colWarnText
			}
			commitMeta(c, sha, shaColor, iconColor)
		}
	})
	if e := a.upd.errors[it.Name]; e != "" {
		ui.Text(c, e).FontSize(12).TextColor(colDangerText)
	}
}

func (a *app) updateReasonInfo(it library.UpdateItem) (string, ui.Color, string) {
	switch it.State {
	case library.UpdReady:
		if !it.CommitDate.IsZero() {
			return formatCommitTime(it.CommitDate), colTextSecondary, it.Commit
		}
		return it.Reason, colTextSecondary, it.Commit
	case library.UpdBoth:
		if !it.CommitDate.IsZero() {
			return locales.UpdateLocalAndUpstreamChanged(formatCommitTime(it.CommitDate)), colWarnText, it.Commit
		}
		return it.Reason, colWarnText, it.Commit
	case library.UpdUnknownBase:
		return it.Reason, colWarnText, it.Commit
	case library.UpdIgnored:
		if it.Commit != "" {
			return locales.UpdateKeepingLocal(), colTextSecondary, it.Commit
		}
		return it.Reason, colTextSecondary, ""
	case library.UpdCopies:
		if s := a.lib.Get(it.Name); s != nil && !s.Identical() {
			return locales.UpdateDifferentCopiesNeedMerge(), colWarnText, ""
		}
		return it.Reason, colWarnText, ""
	case library.UpdFailed:
		return it.Reason, colDangerText, ""
	default:
		return it.Reason, colTextSecondary, ""
	}
}

func (a *app) updateSourceRow(c *ui.Context, it library.UpdateItem, first bool) {
	if !first {
		ui.Box(c).Height(1).Margin(0, 16, 0, 56).Background(colBorderSubtle)
	}
	ui.Row(c).MinHeight(46).Padding(9, 16).Gap(12).AlignItems(ui.Center).Children(func() {
		a.sourceRowIcon(c, it)

		ui.Column(c).Grow(1).MinWidth(0).Gap(1).Children(func() {
			title := it.Source.String()
			sub := ""
			switch it.State {
			case library.UpdWebsite:
				if it.RecordIncomplete {
					title = it.Name
					sub = fmt.Sprintf("%s · %s", it.Source.Repo, it.Reason)
				} else {
					title = it.Source.Repo
					sub = locales.UpdateWellKnownSource()
				}
			default:
				sub = fmt.Sprintf("%s · %s", it.Source.Type, it.Reason)
			}
			ui.Row(c).Gap(7).AlignItems(ui.Center).MinWidth(0).Children(func() {
				titleURL := ""
				if title == it.Source.Repo || title == it.Source.String() {
					titleURL = it.WebURL()
				}
				a.sourceTextLink(c, title, titleURL, colTextPrimary, func(t ui.Element) {
					t.FontSize(13).FontWeight(600)
				}).Shrink(1)
				pillTag(c, locales.CommonItemTotal(max(1, len(it.Names))))
			})
			ui.Text(c, sub).FontSize(12).TextColor(colTextSecondary).SingleLine()
		})

		if it.State == library.UpdWebsite {
			a.updateActions(c, it)
		} else {
			ui.Text(c, locales.CommonNotSupportedYet()).FontSize(12).TextColor(colTextMuted).SingleLine()
		}
	})
}

func (a *app) sourceTextLink(c *ui.Context, label, webURL string, normal ui.Color, configure func(ui.Element)) ui.Element {
	if webURL == "" {
		t := ui.Text(c, label).TextColor(normal).SingleLine().MinWidth(0)
		if configure != nil {
			configure(t)
		}
		return t
	}
	b := ui.ButtonBase(c).Label(label).Justify(ui.Start).AlignItems(ui.Center).MinWidth(0).
		Cursor(ui.CursorPointer).Tooltip(locales.ActionOpenTarget(webURL))
	b.TextColor(tint(b, "src", normal, colAccent, darker(colAccent, 0.35)))
	b.Children(func() {
		t := ui.Text(c, label).SingleLine().MinWidth(0).Shrink(1)
		if configure != nil {
			configure(t)
		}
	})
	if b.Clicked() {
		a.openExternalURL(webURL)
	}
	return b
}

func shortCommit(s string) string { return s[:min(7, len(s))] }

func (a *app) updateActions(c *ui.Context, it library.UpdateItem) {
	if status, ok := a.upd.batch[it.Name]; ok {
		switch status {
		case batchDone:
			ui.Box(c).Size(20, 20).Radius(10).Background(colOKBg).Center().TextColor(colOKText).Children(func() {
				ui.Icon(c, iconCheck).Size(11, 11)
			})
			return
		case batchActive:
			ui.Spinner(c).Size(14, 14).TextColor(colAccent)
			return
		}
	}

	busy := a.upd.cancel != nil
	btn := func(label string, danger bool, fn func()) {
		if updButton(c, nil, label, danger).Disabled(busy).Clicked() {
			fn()
		}
	}
	if it.Stale {
		btn(locales.ActionCheckAgain(), false, func() { a.checkUpdates(false) })
		return
	}
	switch it.State {
	case library.UpdReady:
		if it.CanCompare() {
			if updGhostButton(c, locales.ActionViewDifferences()).Disabled(busy).Clicked() {
				a.prepareUpdate(it)
			}
		}
		if !it.ReadOnly {
			btn(locales.ActionUpdate(), false, func() { a.runUpdates([]library.UpdateItem{it}, nil) })
		}
	case library.UpdBoth, library.UpdUnknownBase:
		btn(locales.ActionViewDifferences(), false, func() { a.prepareUpdate(it) })
	case library.UpdFailed:
		btn(locales.ActionRetry(), false, func() { a.checkUpdates(false) })
	case library.UpdIgnored:
		btn(locales.UpdateStopIgnoring(), false, func() { a.updateMark(func(l *library.Library) error { return l.Unignore(it) }) })
	case library.UpdStopped:
		btn(locales.UpdateResumeChecking(), false, func() { a.updateMark(func(l *library.Library) error { return l.ResumeChecking(it) }) })
	case library.UpdMovedOut:
		ui.Text(c, locales.UpdateReinstallToUpdate()).FontSize(12).TextColor(colTextMuted).SingleLine()
	case library.UpdRemoved:
		btn(locales.UpdateKeepLocalStopChecking(), false, func() { a.updateMark(func(l *library.Library) error { return l.StopChecking(it) }) })
		btn(locales.ActionDeleteSkill(), true, func() {
			if s := a.lib.Get(it.Name); s != nil {
				a.requestDelete(s)
			}
		})
	case library.UpdCopies:
		btn(locales.ActionMergeCopies(), false, func() {
			if s := a.lib.Get(it.Name); s != nil {
				if s.Identical() {
					a.goTo(scopeTidy)
					a.tidy.kind = library.SameCopies
				} else {
					a.openCompare(s, false)
				}
			}
		})
	case library.UpdPending:
		btn(locales.ActionViewFiles(), false, func() { a.reveal(it.Retained) })
		a.iconActionMenu(c, "update.pending:"+it.Transaction+":"+it.Name, iconMore, locales.UpdateHandleIncomplete(), actionIconOpts{
			alignEnd: true,
		}, func(fm *frostedMenu) {
			if fm.action(locales.ActionCheckAgain(), withDisabled(busy)) {
				a.checkUpdates(true)
			}
			if fm.action(locales.UpdateRetryRecordWrite(), withDisabled(busy)) {
				a.recoverUpdates()
			}
		})
	case library.UpdWebsite:
		title := locales.UpdateCopyUpdateCommand()
		if it.RecordIncomplete {
			title = locales.UpdateCopyInstallCommand()
		}
		var ic *ui.SVG
		if a.upd.copied == it.Command {
			ic, title = iconCheck, locales.CommonCopied()
		}
		if updButton(c, ic, title, false).Disabled(busy).Clicked() {
			a.copyCommand(it.Command)
		}
	}
}

// copiedFor 是「已复制」在按钮上停多久。
const copiedFor = 1500 * time.Millisecond

// copyCommand 复制更新命令（画板 ts6-a）：按钮自己变成「已复制」，不弹
// 提示条；复制失败才弹错误提示条。
func (a *app) copyCommand(cmd string) {
	if a.live {
		if err := mygo.Clipboard.Write(transfer.TextData(cmd)); err != nil {
			a.notify(fromUpdate, toastError, locales.UpdateCopyCommandFailed(err.Error()))
			return
		}
	}
	a.upd.copied = cmd
	a.later(copiedFor, func() {
		if a.upd.copied == cmd {
			a.upd.copied = ""
		}
	})
}

// quotaNotice 是 GitHub 额度用完的页内提示（画板 ts5-b），在摘要卡下面：
// 它是这一页的状态，不是一件事，所以不放进提示条。
func quotaNotice(c *ui.Context, reset time.Time) {
	ui.Row(c).Height(34).Padding(0, 12).Gap(8).Radius(8).AlignItems(ui.Center).
		Background(colWarnBg).Border(1, colWarnEdge).Children(func() {
		ui.Image(c, toastSymbols[toastWarn]).Size(14, 14).Shrink(0)
		ui.Row(c).Grow(1).MinWidth(0).AlignItems(ui.Baseline).FontSize(13).Children(func() {
			ui.Text(c, locales.UpdateGitHubRateLimit(reset.Format("15:04"))).TextColor(colWarnText).SingleLine()
			ui.Text(c, locales.UpdateCheckedResultsRemain()).TextColor(colWarnText).SingleLine().MinWidth(0)
		})
	})
}

func (a *app) updateComparePage(c *ui.Context) {
	p, st := a.upd.prepared, a.upd.diff
	a.toolbarRow(c, func() {
		if iconButton(c, iconBack, locales.ActionBackShortcut()).Disabled(a.upd.cancel != nil).Clicked() {
			a.closeUpdateCompare()
		}
		ui.Row(c).Gap(8).AlignItems(ui.Baseline).Children(func() {
			ui.Text(c, p.Item.Name).FontSize(13).FontWeight(600).SingleLine()
			ui.Text(c, locales.UpdateDifferentFileCount(len(st.files))).FontSize(12).TextColor(colTextMuted).FontFeatures("tnum").SingleLine()
		})
	}, func() {
		diffModeSwitch(c, &st.fullFile)
	})
	if a.upd.err != "" {
		errorLine(c, a.upd.err)
	}
	ui.Box(c).Grow(1).MinHeight(0).Children(func() {
		ui.Row(c).Fill().AlignItems(ui.Stretch).Children(func() {
			diffFileTree(c, st)
			ui.Column(c).Grow(1).MinWidth(0).Children(func() {
				a.updateCompareHeader(c, p, st)
				a.updateCompareNotice(c, p, st)
				a.diffFileContent(c, st, &p.Item)
			})
		})
	})
}

func (a *app) updateCompareHeader(c *ui.Context, p *library.Prepared, st *compareState) {
	it := p.Item
	now := time.Now()
	diffVerBar(c, func() {
		localTag, tagBg, tagFg := locales.UpdateUnmodified(), colBgFill, colTextSecondary
		switch it.State {
		case library.UpdBoth:
			localTag, tagBg, tagFg = locales.UpdateModifiedLocally(), colWarnBg, colWarnText
		case library.UpdUnknownBase:
			localTag, tagBg, tagFg = locales.UpdateUnknownBaseline(), colWarnBg, colWarnText
		}
		localMeta := ""
		if s := a.lib.Get(it.Name); s != nil {
			if mod := a.lib.Modified(s); !mod.IsZero() {
				localMeta = locales.CompareModified(formatRecentDay(mod, now))
			}
		}
		localPlace := it.Place
		if localPlace == "" {
			if s := a.lib.Get(it.Name); s != nil {
				localPlace = s.Dir
			}
		}
		if localPlace != "" {
			localPlace = a.lib.Display(localPlace)
		} else {
			localPlace = st.leftSub
		}

		ui.Row(c).Padding(9, 14).Gap(10).AlignItems(ui.Center).Children(func() {
			squircleIcon(c, iconNavFolder, 22, squircleBlue)
			ui.Column(c).Grow(1).MinWidth(0).Gap(2).Children(func() {
				ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
					ui.Text(c, locales.CommonLocal()).FontSize(13).FontWeight(600).SingleLine()
					diffMiniTag(c, localTag, tagBg, tagFg, "")
					if localMeta != "" {
						ui.Text(c, localMeta).FontSize(11).TextColor(colTextMuted).FontFeatures("tnum").SingleLine().MinWidth(0)
					}
				})
				if localPlace != "" {
					ui.Text(c, localPlace).Font("monospace").FontSize(11).TextColor(colTextMuted).SingleLine().MinWidth(0).Tooltip(localPlace)
				}
			})
			tip := locales.UpdateRemindOnChange()
			if short := shortCommit(it.Commit); short != "" {
				tip = locales.UpdateIgnoreCommitHint(short)
			}
			btn := diffActionButton(c, locales.UpdateKeepLocal(), false).Disabled(a.upd.cancel != nil).Tooltip(tip)
			if btn.Clicked() {
				a.keepLocalUpdate(p)
			}
		})
	}, func() {
		upMeta := locales.UpdatePinnedCommit()
		if !it.CommitDate.IsZero() {
			upMeta = locales.UpdateCommittedAt(formatCommitTime(it.CommitDate))
		}
		ui.Row(c).Padding(9, 14).Gap(10).AlignItems(ui.Center).Children(func() {
			squircleIcon(c, iconGitCommit, 22, squircleGit)
			ui.Column(c).Grow(1).MinWidth(0).Gap(2).Children(func() {
				ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
					ui.Text(c, locales.UpdateUpstreamVersion()).FontSize(13).FontWeight(600).SingleLine()
					if short := shortCommit(it.Commit); short != "" {
						a.commitPillLink(c, short, it.CommitURL())
					}
					ui.Text(c, upMeta).FontSize(11).TextColor(colTextMuted).FontFeatures("tnum").SingleLine().MinWidth(0)
				})
				a.sourceTextLink(c, it.Source.String(), it.WebURL(), colTextMuted, func(t ui.Element) {
					t.Font("monospace").FontSize(11)
				})
			})
			btn := diffActionButton(c, locales.ActionUpdate(), true).Disabled(a.upd.cancel != nil || it.ReadOnly)
			if it.ReadOnly {
				btn.Tooltip(locales.UpdateReadOnlyCannotReplace())
			}
			if btn.Clicked() {
				a.runUpdates([]library.UpdateItem{it}, p)
			}
		})
	})
}

func (a *app) commitPillLink(c *ui.Context, short, commitURL string) {
	if commitURL == "" {
		ui.Row(c).Height(17).Padding(0, 6).Radius(4).Background(colAccentBg).AlignItems(ui.Center).Shrink(0).Children(func() {
			ui.Text(c, short).Font("monospace").FontSize(11).FontWeight(500).TextColor(colAccentText).SingleLine()
		})
		return
	}
	b := ui.ButtonBase(c).Height(17).Padding(0, 6).Radius(4).AlignItems(ui.Center).Shrink(0).
		Cursor(ui.CursorPointer).Label(short).Tooltip(locales.UpdateViewCommit(short))
	tintSolid(b, colAccentBg)
	b.Children(func() {
		ui.Text(c, short).Font("monospace").FontSize(11).FontWeight(500).TextColor(colAccentText).SingleLine()
	})
	if b.Clicked() {
		a.openExternalURL(commitURL)
	}
}

func (a *app) updateCompareNotice(c *ui.Context, p *library.Prepared, st *compareState) {
	var localOnly []string
	for _, f := range st.files {
		if f.OnlyLeft {
			localOnly = append(localOnly, f.Path)
		}
	}
	var notice string
	bg, border, fg := colBgPane, colBorderDefault, colTextSecondary
	switch {
	case len(localOnly) > 0:
		files := strings.Join(localOnly[:min(3, len(localOnly))], locales.ListSeparator())
		notice = locales.UpdateReplaceNotice(files)
		if len(localOnly) > 3 {
			notice = locales.UpdateReplaceNoticeMore(files, len(localOnly))
		}
		bg, border, fg = colWarnBg, colWarnEdge, colWarnText
	case p.Item.State == library.UpdUnknownBase:
		notice = locales.UpdateBaselineUnknownHint()
	default:
		return
	}
	ui.Row(c).Shrink(0).Padding(6, 14).Gap(6).AlignItems(ui.Center).Background(bg).
		BorderWidth(0, 0, 1, 0).BorderColor(border).Children(func() {
		ui.Icon(c, iconWarn).Size(12, 12).TextColor(fg)
		ui.Text(c, notice).FontSize(12).TextColor(fg).SingleLine().MinWidth(0)
	})
}

// ——— 更新页视觉组件 ———

func updCard(c *ui.Context) ui.Element {
	return ui.Column(c).FillWidth().Radius(10).Background(colBgCard).
		Border(1, ink(0.085)).
		Shadow(0, 1, 2, 0, shadow(0.03)).
		Clip()
}

func updButton(c *ui.Context, ic *ui.SVG, label string, danger bool) ui.Element {
	b := ui.ButtonBase(c).Height(26).Padding(0, 11).Gap(5).Radius(6).Center().Shrink(0).Label(label)
	tintSolid(b, colBgControl)
	fg, border := colTextPrimary, ink(0.14)
	if danger {
		fg, border = colDangerText, colDangerEdge
	}
	b.Border(1, border).Shadow(0, 1, 1, 0, shadow(0.04))
	b.Children(func() {
		if ic != nil {
			ui.Icon(c, ic).Size(13, 13).TextColor(fg)
		}
		ui.Text(c, label).FontSize(13).TextColor(fg).SingleLine().ContentTransition(labelTransition)
	})
	return b
}

// updIconButton 是只留图标的 updButton，字放进悬停提示。
func updIconButton(c *ui.Context, ic *ui.SVG, label string, danger bool) ui.Element {
	b := ui.ButtonBase(c).Size(30, 26).Radius(6).Center().Shrink(0).Label(label).Tooltip(label)
	tintSolid(b, colBgControl)
	fg, border := colTextPrimary, ink(0.14)
	if danger {
		fg, border = colDangerText, colDangerEdge
	}
	b.Border(1, border).Shadow(0, 1, 1, 0, shadow(0.04))
	b.Children(func() { ui.Icon(c, ic).Size(13, 13).TextColor(fg) })
	return b
}

func updGhostButton(c *ui.Context, label string) ui.Element {
	b := ui.ButtonBase(c).Height(26).Padding(0, 11).Radius(6).Center().Shrink(0).Label(label)
	tintSolid(b, colBgSubtle)
	b.Border(1, ink(0.1)).Shadow(0, 1, 1, 0, shadow(0.03))
	b.Children(func() {
		ui.Text(c, label).FontSize(13).TextColor(colTextSecondary).SingleLine().ContentTransition(labelTransition)
	})
	return b
}

var ()

func squircleIcon(c *ui.Context, ic *ui.SVG, size float32, tone [2]ui.Color) {
	radius := float32(6.5)
	iconSize := float32(15)
	if size >= 36 {
		radius = 8.5
		iconSize = 20
	}
	ui.Box(c).Size(size, size).Shrink(0).Radius(radius).Center().TextColor(white).
		LinearGradient(ui.LinearGradient{From: tone[0], To: tone[1], Angle: 180}).
		InsetShadow(0, 0.5, 0, 0, white.Alpha(0.38)).
		InsetShadow(0, 0, 0, 0.5, ink(0.08)).
		Shadow(0, 1, 2, 0, shadow(0.1)).Children(func() {
		ui.Icon(c, ic).Size(iconSize, iconSize)
	})
}

func commitMeta(c *ui.Context, sha string, shaColor, iconColor ui.Color) {
	ui.Row(c).Gap(4).AlignItems(ui.Center).Shrink(0).Children(func() {
		ui.Icon(c, iconGitCommit).Size(13, 13).TextColor(iconColor)
		ui.Text(c, shortCommit(sha)).Font("monospace").FontSize(12).FontWeight(500).TextColor(shaColor).SingleLine()
	})
}

func pillTag(c *ui.Context, text string) {
	ui.Row(c).Height(17).Padding(0, 6).Radius(4).Background(colBgFill).AlignItems(ui.Center).Shrink(0).Children(func() {
		ui.Text(c, text).FontSize(11).FontWeight(500).TextColor(colTextSecondary).FontFeatures("tnum").SingleLine()
	})
}

func formatCommitTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.Local().Format("2006-01-02 15:04")
}

func formatRecentDay(t, now time.Time) string {
	if t.IsZero() {
		return ""
	}
	y1, m1, d1 := t.Date()
	y2, m2, d2 := now.Date()
	if y1 == y2 && m1 == m2 && d1 == d2 {
		return locales.CommonTodayAt(t.Format("15:04"))
	}
	yest := now.AddDate(0, 0, -1)
	y3, m3, d3 := yest.Date()
	if y1 == y3 && m1 == m3 && d1 == d3 {
		return locales.CommonYesterdayAt(t.Format("15:04"))
	}
	return dayLabel(t, now) + " " + t.Format("15:04")
}

// ——— 更新设置 ———

func (a *app) updateSettings(c *ui.Context) {
	if runtime.GOOS != "darwin" {
		return
	}
	groupLabel(c, locales.UpdateNetworkAndOldVersions(), true)
	setBox(c, func() {
		setRow(c).Children(func() {
			rowText(c, locales.UpdateNetworkProxy(), locales.UpdateSystemProxyHint(), false)
			label := locales.CommonChoose()
			for _, o := range proxyChoices {
				if o.id == a.lib.Settings.UpdateProxyMode {
					label = o.label()
				}
			}
			a.selectButton(c, "settings.proxy", locales.UpdateNetworkProxy(), label, a.upd.cancel != nil, func(fm *frostedMenu) {
				for _, o := range proxyChoices {
					if fm.radio(o.label(), o.id == a.lib.Settings.UpdateProxyMode) {
						a.lib.Settings.UpdateProxyMode = o.id
						a.saveSettings()
					}
				}
			})
		})
		setRow(c).Children(func() {
			rowText(c, locales.UpdateManualProxy(), locales.UpdateProxyExample(), false)
			ui.TextInputBase(c, &a.lib.Settings.UpdateProxy).Width(210).Height(24).Padding(2, 6).Background(colBgCard).BorderWidth(1).BorderColor(colBorderDefault).Radius(5).Placeholder("http://127.0.0.1:7890").Label(locales.UpdateProxyAddress()).Disabled(a.upd.cancel != nil)
			if pushButton(c, locales.ActionSave()).Disabled(a.upd.cancel != nil).Clicked() {
				a.saveSettings()
			}
		})
		setRow(c).Children(func() {
			rowText(c, locales.UpdateOldVersions(), locales.UpdateNoUndo(), false)
			label := locales.CommonChoose()
			if a.lib.Settings.UpdateOldVersion == "trash" {
				label = locales.ActionMoveToTrash()
			}
			if a.lib.Settings.UpdateOldVersion == "delete" {
				label = locales.UpdateDeleteImmediately()
			}
			a.selectButton(c, "settings.oldVersion", locales.UpdateOldVersions(), label, a.upd.cancel != nil, func(fm *frostedMenu) {
				for _, o := range []struct{ id, label string }{{"trash", locales.ActionMoveToTrash()}, {"delete", locales.UpdateDeleteImmediately()}} {
					if fm.radio(o.label, o.id == a.lib.Settings.UpdateOldVersion) {
						a.lib.Settings.UpdateOldVersion = o.id
						a.saveSettings()
					}
				}
			})
		})
	})

	groupLabel(c, locales.UpdateGitHubAuthorization(), false)
	setBox(c, func() {
		setRow(c).Children(func() {
			rowText(c, locales.UpdateUseGhLogin(), locales.UpdateGhLoginHint(), false)
			if toggle(c, &a.lib.Settings.UseGHAuth, locales.UpdateToggleGhLogin()).Disabled(a.upd.cancel != nil).Changed() {
				a.saveSettings()
				if a.lib.Settings.UseGHAuth {
					a.probeGHAuth(true)
				} else {
					a.set.ghChecked, a.set.ghOK, a.set.ghNote = false, false, ""
				}
			}
		})
	})
	if a.lib.Settings.UseGHAuth {
		hint(c, func() {
			switch {
			case a.set.ghProbing:
				ui.Text(c, locales.UpdateCheckingGhLogin())
			case a.set.ghOK:
				ui.Text(c, locales.UpdateGhCredentialsReady()).TextColor(colOKText)
			case a.set.ghNote != "":
				ui.Text(c, locales.UpdateGhFallback(a.set.ghNote)).TextColor(colWarnText)
			}
		})
	}
}

var proxyChoices = []struct {
	id    string
	label func() string
}{{"system", locales.UpdateProxySystem}, {"env", locales.UpdateProxyEnv}, {"manual", locales.UpdateProxyManual}}

func (a *app) openUpdateSettings() { a.set.tab = tabUpdates; a.openSettings() }
