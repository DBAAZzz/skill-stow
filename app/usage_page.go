package main

import (
	"fmt"
	"math"
	"slices"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"

	"skillmanager/internal/library"
	"skillmanager/locales"
)

// 「使用情况」页（st6 定稿）：系统设置 / 屏幕使用时间的写法。浅灰底上居中
// 一列白色分组：概览（近 8 周按日柱状图、三项统计、库内技能分布条）、
// 用过的、没用过的。点一行选中，右侧详情栏预览技能（usagePreviewPane）。

// 侧边栏里「使用情况」的 ID。
const scopeUsage = "usage"

// 图上显示最近几周（按日列 56 根柱子）。
const usageWeeks = 8

// 列表先列几行，其余点「显示全部」展开。
const usageRowsShown = 7

func (a *app) usagePage(c *ui.Context) {
	now := time.Now()
	sub := ""
	if a.lib.HasUsage() {
		since, agents := a.usageSince(now), usageAgents()
		sub = locales.UsageRecordedSince(since, agents)
	}
	var pick func()
	if a.lib.HasUsage() && len(a.unusedBySource()[library.Mine]) > 0 {
		pick = func() { ui.Spacer(c); a.pickToggle(c) }
	}
	a.toolbar(c, locales.NavUsage(), sub, nil, pick)
	if !a.lib.HasUsage() {
		empty(c, locales.UsageReadingLogs())
		return
	}
	used := a.lib.Used()
	a.withPane(c, func(width float32) {
		ui.Scroll(c.Key("usage-scroll")).Grow(1).MinHeight(0).Background(colBgPage).Children(func() {
			ui.Column(c).AlignItems(ui.Center).Padding(24, 0, 40, 0).Children(func() {
				ui.Column(c).Width(min(820, width-48)).Children(func() {
					a.overview(c, now, used)
					a.usedSection(c, now, used)
					a.unusedSection(c)
				})
			})
		})
	})
}

// usageGroup 是页上的一块白色圆角分组。
func usageGroup(c *ui.Context) ui.Element {
	g := ui.Column(c).Radius(10).Background(colBgCard).Clip()
	hairline(g, 0.075)
	lift(g, 1, 1.5, 0.035)
	return g
}

// groupHeader 是分组上方的标题：「用过的 30」，right 放右侧控件。
func groupHeader(c *ui.Context, title string, count int, right func()) ui.Element {
	return ui.Row(c).MinHeight(26).Shrink(0).Margin(26, 0, 0, 0).Padding(0, 10, 7, 10).Gap(6).AlignItems(ui.End).Children(func() {
		ui.Text(c, title).FontSize(13).FontWeight(600).TextColor(colTextPrimary).SingleLine()
		ui.Text(c, fmt.Sprint(count)).FontSize(12).TextColor(colTextMuted).FontFeatures("tnum").SingleLine()
		if right != nil {
			ui.Spacer(c)
			ui.Row(c).Gap(8).AlignItems(ui.Center).Children(right)
		}
	})
}

// groupFoot 是分组下方的灰色说明。
func groupFoot(c *ui.Context, text string) {
	ui.Text(c, text).FontSize(12).LineHeight(1.4).TextColor(colTextMuted).Padding(7, 10, 0, 10)
}

// rowBody 画一行的内容；sep 时顶部加一条分隔线。分隔线要画在行里面，
// 画在外面会叠到整个分组的顶上。
func rowBody(c *ui.Context, row ui.Element, sep bool, fn func()) {
	row.Children(func() {
		if sep {
			rowSep(c, 46)
		}
		fn()
	})
}

// rowSep 是列表行之间的分隔线，从图标右边开始；只在行里面调用。
func rowSep(c *ui.Context, left float32) {
	ui.Box(c).Absolute().Top(0).Left(left).Right(0).Height(edgeSpread).Background(colBorderSubtle)
}

// ——— 概览 ———

func (a *app) overview(c *ui.Context, now time.Time, used []*library.Skill) {
	days := a.lib.Daily(now, usageWeeks)
	st := dailyStats(days)
	usageGroup(c).Children(func() {
		ui.Column(c).Padding(18, 20, 14, 20).Children(func() {
			ui.Row(c).AlignItems(ui.Start).Children(func() {
				ui.Column(c).Children(func() {
					ui.Text(c, locales.UsageCallsInWeeks(usageWeeks)).FontSize(12).FontWeight(500).TextColor(colTextSecondary).SingleLine()
					ui.Row(c).Gap(5).AlignItems(ui.Baseline).Margin(2, 0, 0, 0).Children(func() {
						ui.Text(c, fmt.Sprint(st.total)).FontSize(30).FontWeight(600).LetterSpacing(-0.6).
							TextColor(colTextPrimary).FontFeatures("tnum").SingleLine()
						ui.Text(c, locales.UsageCallsUnit()).FontSize(13).TextColor(colTextSecondary).SingleLine()
					})
				})
				ui.Spacer(c)
				ui.Row(c).Gap(22).Padding(2, 0, 0, 0).Children(func() {
					for _, id := range library.UsageAgents {
						n := st.by[id]
						ui.Column(c.Key(id)).MinWidth(92).Gap(3).Children(func() {
							ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
								ui.Box(c).Size(8, 8).Radius(2).Background(agentColor[id])
								ui.Text(c, library.AgentName(id)).FontSize(12).TextColor(colTextSecondary).SingleLine()
							})
							ui.Row(c).Gap(4).AlignItems(ui.Baseline).Children(func() {
								ui.Text(c, fmt.Sprint(n)).FontSize(17).FontWeight(600).LetterSpacing(-0.2).
									TextColor(colTextPrimary).FontFeatures("tnum").SingleLine()
								if st.total > 0 {
									ui.Text(c, fmt.Sprintf("%d%%", (n*100+st.total/2)/st.total)).FontSize(12).
										TextColor(colTextMuted).FontFeatures("tnum").SingleLine()
								}
							})
						})
					}
				})
			})
			dailyChart(c, days, st)
		})
		statsStrip(c, st)
		a.coverage(c, used)
	})
	if n := projectUsed(used); n > 0 {
		groupFoot(c, locales.UsageProjectSkillsUsed(n))
	}
}

// usageStats 是图上那几周的合计。
type usageStats struct {
	total, active, peak int
	peakDay             time.Time
	avg                 float64
	by                  map[string]int
}

func dailyStats(days []library.Day) usageStats {
	st := usageStats{by: map[string]int{}}
	traced := 0
	for _, d := range days {
		if d.Future || d.Untraced {
			continue
		}
		traced++
		n := d.Total()
		for id, v := range d.By {
			st.by[id] += v
		}
		st.total += n
		if n > 0 {
			st.active++
		}
		if n > st.peak {
			st.peak, st.peakDay = n, d.Date
		}
	}
	if traced > 0 {
		st.avg = float64(st.total) / float64(traced)
	}
	return st
}

const (
	chartPlotHeight = 132
	chartGutter     = 30 // 右边刻度那一栏
)

// chartTicks 是纵轴刻度：0 和最多两条整数线，最高的柱子留一点头顶空间。
func chartTicks(peak int) (ticks []int, top float32) {
	peak = max(peak, 4)
	step := 1
	for _, s := range []int{1, 2, 5, 10, 20, 50, 100, 200, 500, 1000} {
		step = s
		if float64(s) >= float64(peak)/2.5 {
			break
		}
	}
	for v := 0; v <= peak; v += step {
		ticks = append(ticks, v)
	}
	return ticks, float32(peak) * 1.08
}

// dailyChart 是按日堆叠柱状图：Claude Code 在下、Codex 在上；每周一条虚线
// 分隔，绿色虚线是日均；刻度在右边（屏幕使用时间的写法）。
func dailyChart(c *ui.Context, days []library.Day, st usageStats) {
	ticks, top := chartTicks(st.peak)
	y := func(v float64) float32 { return float32(v) / top * chartPlotHeight }
	ui.Row(c).Height(chartPlotHeight).Shrink(0).Margin(22, 0, 0, 0).AlignItems(ui.Stretch).Children(func() {
		for _, v := range ticks {
			line := ui.Box(c.Key(fmt.Sprint("line", v))).Absolute().Left(0).Right(0).Bottom(y(float64(v))).Height(0).BorderWidth(edgeSpread, 0, 0, 0)
			if v == 0 {
				line.BorderColor(colTextDisabled)
			} else {
				line.BorderColor(colBorderSubtle)
			}
			if st.avg > 0 && math.Abs(float64(y(float64(v))-y(st.avg))) < 12 {
				continue // 和「平均」挤在一起时只标平均
			}
			ui.Text(c.Key(fmt.Sprint("tick", v)), fmt.Sprint(v)).Absolute().Right(0).Bottom(y(float64(v)) - 6).FontSize(10).
				TextColor(colTextMuted).FontFeatures("tnum").SingleLine()
		}
		ui.Row(c).Grow(1).MinWidth(0).AlignItems(ui.Stretch).Margin(0, chartGutter, 0, 0).Children(func() {
			if st.avg > 0 {
				ui.Box(c).Absolute().Left(0).Right(-chartGutter+4).Bottom(y(st.avg)).Height(0).
					BorderWidth(1.2, 0, 0, 0).BorderStyle(ui.BorderDashed).BorderColor(colOK)
				ui.Text(c, locales.UsageAverage()).Absolute().Right(-chartGutter).Bottom(y(st.avg) + 2).FontSize(10).FontWeight(600).
					TextColor(colOK).SingleLine()
			}
			for w := 0; w*7 < len(days); w++ {
				week := ui.Row(c.Key(w)).Grow(1).Basis(0).MinWidth(0).Gap(3).Padding(0, 1.5).AlignItems(ui.End)
				if w > 0 {
					week.BorderWidth(0, 0, 0, edgeSpread).BorderStyle(ui.BorderDashed).BorderColor(colBorderSubtle)
				}
				week.Children(func() {
					for i := w * 7; i < min(len(days), w*7+7); i++ {
						dailyBar(c.Key(i), days[i], y)
					}
				})
			}
		})
	})
	ui.Row(c).Shrink(0).Margin(6, chartGutter, 0, 0).Children(func() {
		for w := 0; w*7 < len(days); w++ {
			text := fmt.Sprintf("%d/%d", days[w*7].Date.Month(), days[w*7].Date.Day())
			if w == usageWeeks-1 {
				text = locales.UsageThisWeek()
			}
			ui.Row(c.Key(w)).Grow(1).Basis(0).MinWidth(0).Children(func() {
				ui.Text(c, text).FontSize(11).TextColor(colTextMuted).FontFeatures("tnum").SingleLine()
			})
		}
	})
}

// dailyBar 画一天的堆叠柱子，悬停时提示当天各 Agent 的次数。
func dailyBar(c *ui.Context, d library.Day, y func(float64) float32) {
	col := ui.Column(c).Grow(1).Basis(0).MinWidth(0).FillHeight().Justify(ui.End).AlignItems(ui.Stretch).
		Gap(1).Tooltip(dailyTooltip(d))
	col.Children(func() {
		if d.Future || d.Untraced || d.Total() == 0 {
			return
		}
		var agents []string
		for i := len(library.UsageAgents) - 1; i >= 0; i-- {
			if id := library.UsageAgents[i]; d.By[id] > 0 {
				agents = append(agents, id)
			}
		}
		for i, id := range agents {
			seg := ui.Box(c.Key(id)).Height(max(2, y(float64(d.By[id])))).Background(agentColor[id])
			if i == 0 {
				seg.Radius(2.5, 2.5, 0, 0)
			}
		}
	})
}

func dailyTooltip(d library.Day) string {
	date := locales.UsageDayTitle(monthDay(d.Date, d.Date), weekdayName(d.Date))
	switch {
	case d.Future:
		return locales.UsageDayFuture(date)
	case d.Untraced:
		return locales.UsageDayUntraced(date)
	case d.Total() == 0:
		return locales.UsageDayNoCalls(date)
	}
	lines := []string{date}
	for _, id := range library.UsageAgents {
		lines = append(lines, fmt.Sprintf("%s  %d", library.AgentName(id), d.By[id]))
	}
	return strings.Join(lines, "\n")
}

// statsStrip 是图下面的三格：日均、有调用的天数、最多的一天。各占一格，
// 灰色标签在上、数字在下。
func statsStrip(c *ui.Context, st usageStats) {
	cell := func(key, label, value, unit, note string, first bool) {
		col := ui.Column(c.Key(key)).Grow(1).Basis(0).MinWidth(0).Padding(11, 20, 12, 20).Gap(2)
		if !first {
			col.BorderWidth(0, 0, 0, edgeSpread).BorderColor(colBorderSubtle)
		}
		col.Children(func() {
			ui.Text(c, label).FontSize(12).TextColor(colTextMuted).SingleLine()
			ui.Row(c).Gap(3).AlignItems(ui.Baseline).Children(func() {
				ui.Text(c, value).FontSize(17).FontWeight(600).LetterSpacing(-0.2).TextColor(colTextPrimary).
					FontFeatures("tnum").SingleLine()
				ui.Text(c, unit).FontSize(12).TextColor(colTextSecondary).SingleLine()
				if note != "" {
					ui.Text(c, note).FontSize(12).TextColor(colTextMuted).FontFeatures("tnum").
						Margin(0, 0, 0, 5).SingleLine()
				}
			})
		})
	}
	ui.Row(c).Shrink(0).BorderWidth(edgeSpread, 0, 0, 0).BorderColor(colBorderSubtle).Children(func() {
		cell("avg", locales.UsageDailyAverage(), fmt.Sprintf("%.1f", st.avg), locales.UsageCallsUnit(), "", true)
		cell("active", locales.UsageActiveDays(), fmt.Sprint(st.active), locales.UsageDaysUnit(), "", false)
		peak, note := "—", ""
		if st.peak > 0 {
			peak, note = fmt.Sprint(st.peak), monthDay(st.peakDay, st.peakDay)
		}
		cell("peak", locales.UsageBusiestDay(), peak, locales.UsageCallsUnit(), note, false)
	})
}

// coverage 是「库内技能」：储存空间式的分段条，按使用频率分 4 档。不算项目
// 技能：项目技能跟着项目走，没用过的不列，只算用过的那一半比例就偏了。
func (a *app) coverage(c *ui.Context, used []*library.Skill) {
	var freq, occ, rare, unused int
	for _, s := range used {
		if s.Source == library.Project {
			continue
		}
		switch n := a.lib.Usage(s).Count; {
		case n >= 10:
			freq++
		case n >= 3:
			occ++
		default:
			rare++
		}
	}
	for _, s := range a.lib.Skills {
		if a.lib.Unused(s) {
			unused++
		}
	}
	total := freq + occ + rare + unused
	buckets := []struct {
		label, note string
		n           int
		col         ui.Color
	}{
		{locales.UsageFrequent(), locales.UsageFrequentRange(), freq, colAccent},
		{locales.UsageOccasional(), locales.UsageOccasionalRange(), occ, colChartMid},
		{locales.UsageRare(), locales.UsageRareRange(), rare, colChartLow},
		{locales.UsageUnused(), "", unused, colChartNone},
	}
	ui.Column(c).Padding(14, 20, 16, 20).BorderWidth(edgeSpread, 0, 0, 0).BorderColor(colBorderSubtle).Children(func() {
		ui.Row(c).Gap(8).AlignItems(ui.Baseline).Children(func() {
			ui.Text(c, locales.UsageLibrarySkills()).FontSize(13).FontWeight(600).TextColor(colTextPrimary).SingleLine()
			ui.Spacer(c)
			// 「52 个里用过 30 个」「30 of 52 used」：用了几个加粗，在句中哪里看语言
			before, after := splitEmph(locales.UsageCoverage(total, emphMark))
			// 段末的空格画不出来，间距交给 Gap；空的那段不画
			ui.Row(c).Gap(4).AlignItems(ui.Baseline).Children(func() {
				part := func(s string) {
					if s = strings.TrimSpace(s); s != "" {
						ui.Text(c, s).FontSize(12).TextColor(colTextSecondary).FontFeatures("tnum").SingleLine()
					}
				}
				part(before)
				ui.Text(c, fmt.Sprint(total-unused)).FontSize(12).FontWeight(600).TextColor(colTextPrimary).FontFeatures("tnum").SingleLine()
				part(after)
			})
		})
		ui.Row(c).Height(18).Margin(10, 0, 0, 0).Radius(5).Clip().Gap(1.5).Children(func() {
			if total == 0 {
				ui.Box(c).Grow(1).FillHeight().Background(colChartNone)
				return
			}
			for i, b := range buckets {
				if b.n == 0 {
					continue
				}
				// 很少的几档也要看得见：按数量分宽，但至少留一截
				seg := ui.Box(c.Key(i)).Grow(float32(b.n)).Basis(0).FillHeight().Background(b.col)
				if b.n*20 < total {
					seg.MinWidth(10 + 2*float32(min(b.n, 12)))
				}
			}
		})
		ui.Row(c).Gap(20).Margin(9, 0, 0, 0).Children(func() {
			for i, b := range buckets {
				ui.Row(c.Key(i)).Gap(6).AlignItems(ui.Center).Children(func() {
					ui.Box(c).Size(8, 8).Radius(4).Background(b.col)
					ui.Text(c, b.label).FontSize(12).TextColor(colTextSecondary).SingleLine()
					if b.note != "" {
						ui.Text(c, b.note).FontSize(12).TextColor(colTextMuted).SingleLine()
					}
					ui.Text(c, fmt.Sprint(b.n)).FontSize(12).FontWeight(600).TextColor(colTextPrimary).FontFeatures("tnum").SingleLine()
				})
			}
		})
	})
}

// projectUsed 是用过的项目技能个数（不计入库内）。
func projectUsed(used []*library.Skill) int {
	n := 0
	for _, s := range used {
		if s.Source == library.Project {
			n++
		}
	}
	return n
}

// ——— 列表 ———

// listRow 是两个列表共用的一行：整行是按钮，点了选中；选中时蓝底铺满整行、
// 不缩进不圆角，首尾两行由分组的圆角裁出来——分组上下没有留白，缩进的圆角
// 块会左右有缝、上下贴边。右键弹出技能菜单（skill 非空时）。
func (a *app) listRow(c *ui.Context, label string, on bool, minHeight float32) ui.Element {
	row := ui.ButtonBase(c).MinHeight(minHeight).Padding(0, 12, 0, 14).Gap(10).AlignItems(ui.Center).Label(label)
	if on {
		row.Background(colChartFocus).TextColor(white)
	}
	return row
}

// showAllRow 是列表最后一行「显示全部 30 个」/「收起」。
func (a *app) showAllRow(c *ui.Context, key string, total int) {
	if total <= usageRowsShown {
		return
	}
	open := a.expanded[key]
	text := locales.ShowAllCount(total)
	if open {
		text = locales.ShowLess()
	}
	ui.Row(c).Height(36).Padding(0, 14).AlignItems(ui.Center).Children(func() {
		rowSep(c, 14)
		if linkButton(c, text).Clicked() {
			a.expanded[key] = !open
		}
	})
}

// usageShown 是列表这次列出几行。
func (a *app) usageShown(key string, total int) int {
	if a.expanded[key] {
		return total
	}
	return min(total, usageRowsShown)
}

// skillTile 是行首 22pt 的小图标：文档是我的、文件夹是项目、方块是内置，
// 有来源头像的用头像。
func (a *app) skillTile(c *ui.Context, s *library.Skill, on bool) {
	icon := iconDoc
	switch s.Source {
	case library.Project:
		icon = iconFolder
	case library.Builtin:
		icon = iconNavBuiltin
	case library.ViaPlugin:
		icon = iconStack
	}
	tile(c, a.sourceIconFor(s), icon, on)
}

func tile(c *ui.Context, img ui.ImageSource, icon *ui.SVG, on bool) {
	b := ui.Box(c).Size(22, 22).Radius(6).Clip().Shrink(0).Center()
	switch {
	case img != nil:
		b.Background(colBgLogo)
		hairline(b, 0.06)
		b.Children(func() { ui.Image(c, img).Size(22, 22).Fit(ui.Cover) })
	case on:
		b.Background(white.Alpha(0.2)).TextColor(white)
		b.Children(func() { ui.Icon(c, icon).Size(14, 14) })
	default:
		b.Background(colBgFill).TextColor(colTextMuted)
		hairline(b, 0.06)
		b.Children(func() { ui.Icon(c, icon).Size(14, 14) })
	}
}

func chevron(c *ui.Context, on bool) {
	col := colTextDisabled
	if on {
		col = white.Alpha(0.8)
	}
	ui.Icon(c, iconChevronRight).Size(12, 12).Shrink(0).TextColor(col)
}

// selectInUsage 选中 s，右侧详情栏预览它。
func (a *app) selectInUsage(s *library.Skill) {
	a.selected, a.coll = s, nil
}

// ——— 用过的 ———

func (a *app) usedSection(c *ui.Context, now time.Time, used []*library.Skill) {
	groupHeader(c, locales.UsageUsedSection(), len(used), nil)
	if len(used) == 0 {
		usageGroup(c).Children(func() {
			ui.Text(c, locales.UsageNoCallsYet()).FontSize(13).TextColor(colTextMuted).Padding(14)
		})
		return
	}
	most := max(1, a.lib.Usage(used[0]).Count)
	usageGroup(c).Children(func() {
		for i, s := range used[:a.usageShown("usage.used", len(used))] {
			// 选中的那行是蓝底，它和上下两行之间不画线
			sep := i > 0 && a.selected != s && a.selected != used[i-1]
			a.usedRow(c.Key(s.ID), now, s, most, sep)
		}
		a.showAllRow(c, "usage.used", len(used))
	})
}

func (a *app) usedRow(c *ui.Context, now time.Time, s *library.Skill, most int, sep bool) {
	u := a.lib.Usage(s)
	on := a.selected == s
	row := a.listRow(c, s.Name, on, 40)
	if row.Clicked() {
		a.selectInUsage(s)
	}
	a.contextMenu(c, row, "usage.used."+s.ID, true, func(fm *frostedMenu) { a.usageSkillMenu(fm, s) })
	tertiary := colTextMuted
	if on {
		tertiary = white.Alpha(0.8)
	}
	rowBody(c, row, sep, func() {
		a.skillTile(c, s, on)
		ui.Row(c).Width(236).Shrink(0).Gap(6).AlignItems(ui.Baseline).Children(func() {
			ui.Text(c, s.Name).FontSize(13).FontWeight(500).SingleLine().MinWidth(0).Shrink(1)
			if tag := usageSourceTag(s.Source); tag != "" {
				ui.Text(c, tag).FontSize(12).TextColor(tertiary).SingleLine().Shrink(0)
			}
		})
		ui.Row(c).Grow(1).MinWidth(0).Height(6).Radius(3).Gap(1.5).Clip().Children(func() {
			if on {
				ui.Box(c).Absolute().Fill().Background(white.Alpha(0.25))
			} else {
				ui.Box(c).Absolute().Fill().Background(colChartNone)
			}
			for _, id := range library.UsageAgents {
				if v := u.By[id]; v > 0 {
					col := agentColor[id]
					if on {
						col = col.Mix(white, 0.65)
					}
					ui.Box(c.Key(id)).WidthPercent(float32(v) / float32(most) * 100).FillHeight().Shrink(0).Background(col)
				}
			}
		})
		ui.Text(c, fmt.Sprint(u.Count)).Width(40).Shrink(0).TextAlign(ui.End).FontSize(13).FontWeight(600).
			FontFeatures("tnum").SingleLine()
		ui.Text(c, locales.DaysAgoShort(daysAgo(u.Last, now))).Width(58).Shrink(0).TextAlign(ui.End).FontSize(12).
			TextColor(tertiary).FontFeatures("tnum").SingleLine()
		chevron(c, on)
	})
}

func usageSourceTag(src library.Source) string {
	switch src {
	case library.Project:
		return locales.SourceProject()
	case library.Builtin:
		return locales.SourceBuiltin()
	case library.ViaPlugin:
		return locales.SourcePlugin()
	default:
		return ""
	}
}

// usageSkillMenu 是使用情况页一行的右键菜单：在详情栏预览、打开，其余同卡片。
func (a *app) usageSkillMenu(fm *frostedMenu, s *library.Skill) {
	if fm.action(locales.UsageShowDetails(), withIcon(iconDoc)) {
		a.selectInUsage(s)
	}
	if fm.action(locales.ActionOpen(), withIcon(iconExternal), withShortcut("Space")) {
		a.openFromUsage(s, 0)
	}
	fm.separator()
	a.fileItems(fm, s)
	if s.ReadOnly() {
		return
	}
	fm.separator()
	a.deleteItem(fm, s)
}

// openFromUsage 从使用情况页进入阅读视图，返回时回到使用情况页。file 是
// 先显示第几个文件。
func (a *app) openFromUsage(s *library.Skill, file int) {
	a.open(s)
	if file > 0 && file < len(s.Files) {
		a.file = file
	}
}

// ——— 没用过的 ———

// unusedSection 是「没用过的」：分我的和内置两段。一个集合里没用过的收成
// 一行，点了打开集合；单个技能点了在详情栏预览。
func (a *app) unusedSection(c *ui.Context) {
	bySource := a.unusedBySource()
	if a.unusedTab != library.Builtin {
		a.unusedTab = library.Mine
	}
	skills := bySource[a.unusedTab]
	picking := a.del.picking && a.unusedTab == library.Mine && len(skills) > 0
	head := groupHeader(c, locales.UsageUnusedSection(), len(bySource[library.Mine])+len(bySource[library.Builtin]), func() {
		a.unusedTabs(c, len(bySource[library.Mine]), len(bySource[library.Builtin]))
	})
	if a.del.reveal {
		a.del.reveal = false
		head.ScrollIntoView(ui.ScrollIntoViewOptions{Block: ui.ScrollStart, Animation: &ui.Smooth})
	}
	usageGroup(c).Children(func() {
		if len(skills) == 0 {
			ui.Text(c, locales.UsageAllUsed()).FontSize(13).TextColor(colTextMuted).Padding(14)
			return
		}
		sets, count, rest := unusedGroups(skills)
		total := len(sets) + len(rest)
		limit := total
		if !picking {
			limit = a.usageShown("usage.unused", total)
		}
		i, prevOn := 0, false
		for _, coll := range sets {
			if i == limit {
				break
			}
			if picking {
				a.pickSetRow(c.Key(coll.ID), coll, unusedIn(coll, skills), i > 0)
			} else {
				a.unusedSetRow(c.Key(coll.ID), coll, count[coll], i > 0)
			}
			i++
		}
		for _, s := range rest {
			if i == limit {
				break
			}
			if picking {
				a.pickSkillRow(c.Key(s.ID), s, i > 0)
			} else {
				a.unusedSkillRow(c.Key(s.ID), s, i > 0 && !prevOn)
			}
			prevOn = a.selected == s
			i++
		}
		if picking {
			// 选的时候列表全展开，选择条贴在窗口底部，不用滚到最后才能删
			ui.Column(c).Padding(0, 14, 12, 14).Background(colBgSurface).Sticky().Bottom(0).Children(func() { a.pickBar(c, skills) })
		} else {
			a.showAllRow(c, "usage.unused", total)
		}
	})
}

// unusedBySource 是没用过的技能，按「我的」「内置」分开；插件装的算我的。
func (a *app) unusedBySource() map[library.Source][]*library.Skill {
	bySource := map[library.Source][]*library.Skill{}
	for _, s := range a.lib.Skills {
		if a.lib.Unused(s) {
			src := s.Source
			if src == library.ViaPlugin {
				src = library.Mine // 插件装的也是你装的，和「我的」一页
			}
			bySource[src] = append(bySource[src], s)
		}
	}
	return bySource
}

// unusedTabs 是「我的 201 | 内置 2」分段控件：两段等宽，字重不变，白色
// 滑块滑到选中的那段，切换时文字不会跳动。
func (a *app) unusedTabs(c *ui.Context, mine, builtin int) {
	const pad, segW, segH = 2, 76, 20
	sel := 0
	if a.unusedTab == library.Builtin {
		sel = 1
	}
	prev := sel
	seg := ui.SegmentedBase(c, &sel, 2)
	track := seg.Track.Padding(pad).Radius(7).Background(colBgFill)
	x := track.AnimateTo("thumb", float32(sel)*segW, ui.Animation{Duration: 200 * time.Millisecond, Ease: ui.CubicBezier(0.2, 0.8, 0.2, 1)})
	track.Children(func() {
		thumb := ui.Box(c.Key("thumb")).Absolute().ZIndex(-1).Top(pad).Left(pad).Translate(x, 0).Size(segW, segH).Radius(5).Background(colBgThumb)
		lift(thumb, 1, 2, 0.1)
		hairline(thumb, 0.04)
		for i, it := range []struct {
			src library.Source
			n   int
		}{{library.Mine, mine}, {library.Builtin, builtin}} {
			text := fmt.Sprintf("%s %d", it.src.Label(), it.n)
			seg.Segment(i).Size(segW, segH).Radius(5).Center().Label(text).Children(func() {
				ui.Row(c).Gap(4).AlignItems(ui.Baseline).Children(func() {
					ui.Text(c, it.src.Label()).FontSize(12).FontWeight(500).TextColor(colTextPrimary).SingleLine()
					ui.Text(c, fmt.Sprint(it.n)).FontSize(12).TextColor(colTextMuted).FontFeatures("tnum").SingleLine()
				})
			})
		}
	})
	if sel != prev {
		a.unusedTab = library.Mine
		if sel == 1 {
			a.unusedTab = library.Builtin
		}
		a.del.picking, a.del.picked = false, nil
	}
}

// unusedGroups 把没用过的技能按集合收起来：返回集合（没用过的多的在前）、
// 每个集合没用过的个数，和不属于集合的技能（保持库内顺序）。
func unusedGroups(skills []*library.Skill) (sets []*library.Collection, count map[*library.Collection]int, rest []*library.Skill) {
	count = map[*library.Collection]int{}
	for _, s := range skills {
		switch coll := s.Collection; {
		case coll == nil:
			rest = append(rest, s)
		case count[coll] == 0:
			sets = append(sets, coll)
			fallthrough
		default:
			count[coll]++
		}
	}
	slices.SortStableFunc(sets, func(a, b *library.Collection) int { return count[b] - count[a] })
	return sets, count, rest
}

// usageRow 是「没用过的」一行的外框（选择时也用）：两行高；分隔线用 rowBody 画。
func usageRow(c *ui.Context) ui.Element {
	return ui.Row(c).MinHeight(50).Shrink(0).Padding(0, 12, 0, 14).Gap(10).AlignItems(ui.Center).MinWidth(0)
}

// unusedSetRow 是一个集合：整行点了打开集合。
func (a *app) unusedSetRow(c *ui.Context, coll *library.Collection, unused int, sep bool) {
	row := a.listRow(c, locales.UsageOpenCollection(coll.Name), false, 50)
	if row.Clicked() {
		a.showSet(coll)
	}
	rowBody(c, row, sep, func() {
		a.unusedSetCells(c, coll, unused)
		chevron(c, false)
	})
}

// unusedIn 是 skills 里属于 coll 的。
func unusedIn(coll *library.Collection, skills []*library.Skill) []*library.Skill {
	var out []*library.Skill
	for _, s := range skills {
		if s.Collection == coll {
			out = append(out, s)
		}
	}
	return out
}

// unusedSetCells 是集合行的内容：图标、名称、用过哪几个（插件集合标出）、
// 没用过的个数。
func (a *app) unusedSetCells(c *ui.Context, coll *library.Collection, unused int) {
	plugin := slices.ContainsFunc(coll.Skills, func(s *library.Skill) bool { return s.Source == library.ViaPlugin })
	var img ui.ImageSource
	if len(coll.Skills) > 0 {
		img = a.sourceIconFor(coll.Skills[0])
	}
	icon := iconStack
	if plugin {
		icon = iconAgentGrid
	}
	tile(c, img, icon, false)
	note := a.setUsageNote(coll)
	if plugin {
		note = locales.UsagePluginNote(note)
	}
	twoLines(c, coll.Name, note, colTextMuted)
	ui.Text(c, locales.UsageUnusedCount(unused)).FontSize(12).TextColor(colTextMuted).FontFeatures("tnum").
		Shrink(0).SingleLine()
}

// twoLines 是行里的名称和下面一行灰色说明。
func twoLines(c *ui.Context, title, sub string, subColor ui.Color) {
	ui.Column(c).Grow(1).MinWidth(0).Gap(1).Children(func() {
		ui.Text(c, title).FontSize(13).FontWeight(500).SingleLine()
		if sub != "" {
			ui.Text(c, sub).FontSize(12).TextColor(subColor).SingleLine()
		}
	})
}

// unusedSkillRow 是一个技能：整行点了在详情栏预览，右键是技能菜单。
func (a *app) unusedSkillRow(c *ui.Context, s *library.Skill, sep bool) {
	on := a.selected == s
	row := a.listRow(c, s.Name, on, 50)
	if row.Clicked() {
		a.selectInUsage(s)
	}
	a.contextMenu(c, row, "usage.unused."+s.ID, true, func(fm *frostedMenu) { a.usageSkillMenu(fm, s) })
	sub := colTextMuted
	if on {
		sub = white.Alpha(0.8)
	}
	rowBody(c, row, sep && !on, func() {
		a.skillTile(c, s, on)
		twoLines(c, s.Name, unusedSkillNote(s), sub)
		chevron(c, on)
	})
}

// unusedSkillNote 是单个技能下面那行：简介，插件装的标出来。
func unusedSkillNote(s *library.Skill) string {
	if s.Source == library.ViaPlugin {
		return locales.UsagePluginNote(s.Description)
	}
	return s.Description
}

// setUsageNote 是「27 个里只用过 lark-approval」：集合里用过的技能。
func (a *app) setUsageNote(coll *library.Collection) string {
	var used []string
	for _, s := range coll.Skills {
		if a.lib.Usage(s).Count > 0 {
			used = append(used, s.Name)
		}
	}
	n := len(coll.Skills)
	switch len(used) {
	case 0:
		return locales.UsageSetNoneUsed(n)
	case 1:
		return locales.UsageSetOnlyUsed(n, used[0])
	case 2:
		return locales.UsageSetUsed(n, strings.Join(used, locales.ListSeparator()))
	}
	return locales.UsageSetUsedSome(n, strings.Join(used[:2], locales.ListSeparator()), len(used))
}

// rowIcon 是行尾的灰色小图标按钮。
func rowIcon(c *ui.Context, icon *ui.SVG, label string) ui.Element {
	b := ui.ButtonBase(c).Size(22, 22).Shrink(0).Radius(6).Center().TextColor(colTextMuted).Label(label).Tooltip(label)
	tintBg(b, ink(0), colInkHover, colInkPress)
	b.Children(func() { ui.Icon(c, icon).Size(15, 15) })
	return b
}

// showSet 从使用情况页打开集合：换到它所在的来源，再打开。
func (a *app) showSet(coll *library.Collection) {
	a.navigate(func() {
		a.scope = string(library.ScopeMine)
		if len(coll.Skills) > 0 && coll.Skills[0].Source == library.Builtin {
			a.scope = string(library.ScopeBuiltin)
		}
		a.selected = nil
		a.openSet(coll)
	})
}

// ——— 详情栏预览 ———

// usagePreviewPane 是在使用情况页选中技能时的详情栏：SKILL.md 开头、全部
// 文件、使用情况和信息。点文件或「打开阅读视图」进入阅读视图。
func (a *app) usagePreviewPane(c *ui.Context, s *library.Skill) {
	a.lib.LoadFiles(s)
	var menu func(fm *frostedMenu)
	if s.ReadOnly() {
		menu = func(fm *frostedMenu) { a.fileItems(fm, s) }
	} else {
		menu = func(fm *frostedMenu) { a.paneMenu(fm, s) }
	}
	a.paneHeader(c, s, nil, menu)

	idx := slices.IndexFunc(s.Files, func(f library.File) bool { return f.Path == "SKILL.md" })
	if idx >= 0 && !s.Files[idx].Binary {
		label(c, "SKILL.md")
		a.skillMDPreview(c, s, s.Files[idx])
	}

	size := 0
	for _, f := range s.Files {
		size += f.Size
	}
	ui.Row(c).Margin(22, 0, 7, 2).AlignItems(ui.Baseline).Children(func() {
		sectionLabel(c, locales.FilesLabel())
		ui.Spacer(c)
		ui.Text(c, locales.FilesCountSize(len(s.Files), sizeLabel(size))).FontSize(11).TextColor(colTextMuted).
			FontFeatures("tnum").SingleLine()
	})
	a.previewFiles(c, s)
	a.usagePane(c, s)
	a.infoPane(c, s, "", colTextMuted)
}

// skillMDPreview 是 SKILL.md 正文的开头几行，底部渐隐，「打开阅读视图」看全文。
func (a *app) skillMDPreview(c *ui.Context, s *library.Skill, f library.File) {
	md := library.ParseSkillMD(f.Content)
	heading, lines := previewLines(md.Body, 5)
	ui.Column(c).Height(184).Radius(9).Background(colBgCard).Border(1, colBorderDefault).Clip().Children(func() {
		ui.Column(c).Grow(1).MinHeight(0).Padding(12, 14, 0, 14).Gap(6).Clip().Children(func() {
			if heading != "" {
				ui.Text(c, heading).FontSize(14).FontWeight(700).TextColor(colTextPrimary)
			}
			for i, l := range lines {
				t := ui.Text(c.Key(i), l.text).FontSize(13).LineHeight(1.5)
				if l.heading {
					t.FontSize(13).FontWeight(600).TextColor(colTextPrimary).Margin(4, 0, 0, 0)
				} else {
					t.TextColor(colTextSecondary)
				}
			}
			ui.Box(c).Absolute().Left(0).Right(0).Bottom(0).Height(36).Gradient(colBgCard.Alpha(0), colBgCard, 180)
		})
		ui.Row(c).Height(42).Shrink(0).Padding(0, 10).Justify(ui.End).AlignItems(ui.Center).Children(func() {
			if button(c, iconDoc, locales.OpenInReader()).Clicked() {
				a.openFromUsage(s, 0)
			}
		})
	})
}

type previewLine struct {
	text    string
	heading bool
}

// previewLines 从 Markdown 正文里取第一个标题和后面几段，去掉常见标记。
func previewLines(body string, n int) (heading string, out []previewLine) {
	inCode := false
	for _, raw := range strings.Split(body, "\n") {
		l := strings.TrimSpace(raw)
		if strings.HasPrefix(l, "```") {
			inCode = !inCode
			continue
		}
		if inCode || l == "" || l == "---" {
			continue
		}
		h := strings.HasPrefix(l, "#")
		l = strings.TrimSpace(strings.TrimLeft(l, "#>-*0123456789. "))
		l = strings.NewReplacer("**", "", "__", "", "`", "").Replace(l)
		if l == "" {
			continue
		}
		if h && heading == "" && len(out) == 0 {
			heading = l
			continue
		}
		out = append(out, previewLine{l, h})
		if len(out) == n {
			break
		}
	}
	return heading, out
}

// previewFiles 列出技能的文件：SKILL.md 在最前，子目录缩进；多了收起。
func (a *app) previewFiles(c *ui.Context, s *library.Skill) {
	const shown = 8
	box(c, func() {
		if len(s.Files) == 0 {
			boxRow(c, true).Children(func() {
				ui.Text(c, locales.FilesUnreadable()).FontSize(13).TextColor(colTextMuted)
			})
			return
		}
		dir, rows := "", 0
		for i, f := range s.Files {
			if rows == shown {
				break
			}
			d, name := splitDir(f.Path)
			if d != dir && d != "" {
				n := 0
				for _, g := range s.Files {
					if gd, _ := splitDir(g.Path); gd == d {
						n++
					}
				}
				boxRow(c.Key("dir/"+d), rows == 0).MinHeight(30).Padding(0, 12).Children(func() {
					ui.Icon(c, iconFolder).Size(14, 14).TextColor(colAccent)
					ui.Text(c, d).FontSize(13).SingleLine().Grow(1).MinWidth(0)
					ui.Text(c, locales.FilesInFolder(n)).FontSize(11).TextColor(colTextDisabled).FontFeatures("tnum")
				})
				rows++
				if rows == shown {
					break
				}
			}
			dir = d
			row := ui.ButtonBase(c.Key(f.Path)).MinHeight(30).Padding(0, 12, 0, 12+indent(d)).Gap(8).AlignItems(ui.Center).Label(f.Path)
			if rows > 0 {
				row.BorderWidth(1, 0, 0, 0).BorderColor(colBorderSubtle)
			}
			tintBg(row, colBgCard, colInkHover, colInkPress)
			if row.Clicked() {
				a.openFromUsage(s, i)
			}
			row.Children(func() {
				ui.Icon(c, iconDoc).Size(14, 14).TextColor(colTextMuted)
				ui.Text(c, name).FontSize(13).SingleLine().Grow(1).MinWidth(0)
				ui.Text(c, sizeLabel(f.Size)).FontSize(11).TextColor(colTextDisabled).FontFeatures("tnum")
			})
			rows++
		}
		if rest := len(s.Files) - countFileRows(s.Files, shown); rest > 0 {
			ui.Row(c).Height(30).Padding(0, 12, 0, 34).AlignItems(ui.Center).BorderWidth(1, 0, 0, 0).
				BorderColor(colBorderSubtle).Children(func() {
				if linkButton(c, locales.FilesMore(rest)).Clicked() {
					a.openFromUsage(s, 0)
				}
			})
		}
	})
}

// countFileRows 是前 limit 行里列出了几个文件（目录行不算）。
func countFileRows(files []library.File, limit int) int {
	dir, rows, n := "", 0, 0
	for _, f := range files {
		d, _ := splitDir(f.Path)
		if d != dir && d != "" {
			rows++
			if rows >= limit {
				break
			}
		}
		dir = d
		rows++
		n++
		if rows >= limit {
			break
		}
	}
	return n
}

func splitDir(p string) (dir, name string) {
	i := strings.LastIndex(p, "/")
	if i < 0 {
		return "", p
	}
	return p[:i], p[i+1:]
}
