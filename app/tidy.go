package main

import (
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"skillmanager/internal/library"
	"skillmanager/locales"
)

// 侧边栏里「整理」的 ID。
const scopeTidy = "tidy"

// tidyState 是整理页的状态。
type tidyState struct {
	kind  library.Issue
	msg   string            // 上一次处理的结果（绿色），换一类或离开时清掉
	err   string            // 上一次处理失败的原因（红色）
	stats map[string]string // 内容不同的副本的差异统计，按技能 ID；重新扫描时清掉

	work *tidyWork // One shared operation owns progress and retained rows.

	scanning bool // 点了「重新检测」、后台还在扫

	// Dock 进度条状态：wantDock 是业务当前期望显示的进度，appliedDock 是
	// 最近一次通过有效窗口写入系统 Dock tile 的进度（用于关窗前清理与重开后恢复）。
	wantDock    mygo.ProgressBar
	appliedDock mygo.ProgressBar
}

// busy 报告整理页正在后台批量处理：这时不能再开始另一件，也不能重新检测。
func (t *tidyState) busy() bool { return t.work != nil }

// 左边一类问题收起、下面的补上来；右边换一类时交叉淡入。
var (
	issueRowTransition  = ui.ElementTransition{Animation: &rowSpring, Position: true, Exit: &ui.Motion{Opacity: 0, Collapse: true}}
	issuePaneTransition = ui.ElementTransition{Duration: 220 * time.Millisecond,
		Enter: &ui.Motion{Opacity: 0, Y: 6}, Exit: &ui.Motion{Opacity: 0}}
)

// issueCount 是这一类还剩几个：批量处理的时候，处理好、收起的先减掉，
// 数字跟着往下滚。
func (a *app) issueCount(p *library.Problems, k library.Issue) int {
	if w := a.tidy.work; w != nil && w.kind == k {
		return w.rows.Count(k) - len(w.progress.gone)
	}
	return p.Count(k)
}

// issueTotal 是全部问题还剩几个：顶栏和侧边栏「整理」的数字。
func (a *app) issueTotal(p *library.Problems) int {
	n := 0
	for _, k := range library.Issues {
		n += a.issueCount(p, k)
	}
	return n
}

// 每类问题标题下的说明：只有看名字不知道为什么算问题的才写。
var issueNotes = map[library.Issue]func() string{
	library.ExtraLinks:    locales.TidyNoteExtraLinks,
	library.NoDescription: locales.TidyNoteNoDescription,
	library.GlobalLinked:  locales.TidyNoteGlobalLinked,
	library.StaleRecords:  locales.TidyNoteStaleRecords,
	library.Incomplete:    locales.TidyNoteIncomplete,
}

// tidyPage 是整理页（z1–z6）：左边问题类型，右边这一类的表格。没有问题时
// 是 g2-b。
func (a *app) tidyPage(c *ui.Context) {
	p := a.tidyProblems()
	total := a.issueTotal(&p)
	a.toolbar(c, locales.NavTidy(), nil, func() {
		if total > 0 {
			ui.Row(c).AlignItems(ui.Baseline).FontSize(12).TextColor(colTextMuted).Label(locales.TidyIssueCount(total)).Children(func() {
				// 数字单独画，变的时候滚动；它在句子里的位置随语言变
				before, after := splitEmph(locales.TidyIssueCountShown(total, emphMark))
				if before != "" {
					ui.Text(c, before).SingleLine()
				}
				ui.Text(c, fmt.Sprint(total)).FontFeatures("tnum").SingleLine().
					ContentTransition(ui.ContentTransition{Numeric: true, Down: true})
				if after != "" {
					ui.Text(c, after).SingleLine()
				}
			})
		}
	}, func() {
		ui.Spacer(c)
		if !a.tidy.scanning {
			ui.Text(c, scannedLabel(a.set.scannedAt, time.Now())).FontSize(12).TextColor(colTextMuted).SingleLine()
		}
		label := cond(a.tidy.scanning, locales.TidyRescanning(), locales.ActionDetectAgain())
		refresh := busyButton(c, iconRefresh, label, a.tidy.scanning)
		if a.tidy.busy() || a.tidy.scanning {
			refresh.Disabled(true) // 处理完会重新检测
		}
		if refresh.Clicked() {
			a.tidyRescan()
		}
	})
	if total == 0 {
		ui.Column(c).Grow(1).Center().Gap(12).Background(colBgPane).Children(func() {
			if a.tidy.msg != "" {
				ui.Text(c, a.tidy.msg).FontSize(12).TextColor(colOKText)
			}
			ui.Box(c).Size(44, 44).Radius(22).Background(colOKBg).Center().TextColor(colOKText).Children(func() {
				ui.Icon(c, iconCheck).Size(20, 20)
			})
			ui.Text(c, locales.TidyNoIssues()).FontSize(14).FontWeight(600)
		})
		return
	}
	if a.issueCount(&p, a.tidy.kind) == 0 {
		// 这一类处理完了，左边那一项消失，换到下一类
		for _, k := range library.Issues {
			if a.issueCount(&p, k) > 0 {
				a.tidy.kind = k
				break
			}
		}
	}
	ui.Row(c).Grow(1).MinHeight(0).AlignItems(ui.Stretch).Children(func() {
		ui.Column(c).Width(250).Shrink(0).Padding(10, 8).Gap(2).Background(colBgPane).
			BorderWidth(0, 1, 0, 0).BorderColor(colBorderDefault).Children(func() {
			for _, k := range library.Issues {
				if n := a.issueCount(&p, k); n > 0 {
					a.issueRow(c.Key(int(k)), k, n)
				}
			}
		})
		ui.Box(c).Grow(1).MinWidth(0).Clip().Children(func() {
			ui.Column(c.Key(int(a.tidy.kind))).Absolute().Fill().Transition(issuePaneTransition).
				Children(func() { a.issueDetail(c, &p, a.tidy.kind) })
		})
	})
}

// minRescanSpin 是「重新检测」至少转多久：扫描常常一瞬间就完，太快了看不出
// 点没点上。
const minRescanSpin = 600 * time.Millisecond

// tidyRescan 在后台重新扫描，按钮转圈；扫完换上新结果，用提示条报一句
// 还剩几个问题。扫的途中别处先扫过一遍（删除、监听）就不拿这份旧的盖回去。
func (a *app) tidyRescan() {
	if a.tidy.scanning || a.tidy.busy() || a.lib.Home == "" {
		return
	}
	a.tidy.msg, a.tidy.err = "", ""
	a.tidy.scanning = true
	home, gen, live := a.lib.Home, a.watch.scanGen.Load(), a.live
	a.background(func(post func(func())) {
		start := time.Now()
		lib := library.Scan(home)
		rep := lib.Updates()
		if wait := minRescanSpin - time.Since(start); live && wait > 0 {
			time.Sleep(wait)
		}
		post(func() {
			a.tidy.scanning = false
			if gen == a.watch.scanGen.Load() && !a.tidy.busy() {
				a.useScan(lib, rep)
			}
			p := a.lib.Problems()
			text := locales.TidyRescanClean()
			if n := a.issueTotal(&p); n > 0 {
				text = locales.TidyRescanDone(n)
			}
			a.notify(fromTidy, toastOK, text)
		})
	})
}

// issueRow 是左边的一类问题：图标、名称、数量。
func (a *app) issueRow(c *ui.Context, k library.Issue, n int) {
	row := ui.ButtonBase(c).Height(40).Padding(0, 10).Gap(10).Radius(7).AlignItems(ui.Center).Label(k.Title()).
		Transition(issueRowTransition)
	selectableRow(row, a.tidy.kind == k, colInkSelected)
	if row.Clicked() && a.tidy.kind != k {
		a.tidy.kind, a.tidy.msg, a.tidy.err = k, "", ""
	}
	row.Children(func() {
		issueIcon(c, k, 24)
		ui.Text(c, k.Title()).SingleLine().Grow(1).MinWidth(0)
		ui.Text(c, fmt.Sprint(n)).FontSize(12).TextColor(colTextMuted).FontFeatures("tnum").
			ContentTransition(ui.ContentTransition{Numeric: true, Down: true})
	})
}

// issueDetail 是右边：图标、名称、需要时一句说明、批量按钮，下面是表格。
func (a *app) issueDetail(c *ui.Context, p *library.Problems, k library.Issue) {
	ui.Row(c).Padding(20, 24, 16, 24).Gap(14).AlignItems(ui.Center).Shrink(0).
		BorderWidth(0, 0, 1, 0).BorderColor(colBorderDefault).Children(func() {
		issueIcon(c, k, 40)
		ui.Column(c).Grow(1).MinWidth(0).Gap(2).Children(func() {
			// 标题下面多出一行进度时，标题顺着往上挪
			ui.Text(c, k.Title()).FontSize(15).FontWeight(600).Transition(ui.ElementTransition{Animation: &swapSpring, Position: true})
			if w := a.tidy.work; w != nil && w.kind == k {
				progressNote(c.Key("working"), w.progress, w.doing, locales.TidyProcessingFinished())
			} else if note := issueNotes[k]; note != nil {
				ui.Text(c, note()).FontSize(12).TextColor(colTextMuted)
			}
		})
		switch k {
		case library.SameCopies:
			a.mergeAllButton(c, p.Same)
		case library.StaleRecords:
			a.clearAllButton(c, p.Stale)
		case library.ExtraLinks:
			if m := a.tidyProgressFor(k); m != nil {
				progressPill(c, m, locales.TidyLinkDeleted())
			} else if dialogButton(c, locales.ActionDeleteAll(), false).Shrink(0).
				Disabled(a.tidy.busy() || a.tidy.scanning).
				Transition(ui.ElementTransition{Match: batchButtonMatch{}, Animation: &morphSpring}).Clicked() {
				a.dropExtraLinks(p.Extra)
			}
		}
	})
	ui.Scroll(c).Grow(1).Scrollbars(false).Children(func() {
		switch k {
		case library.SameCopies:
			a.sameTable(c, p.Same)
		case library.DiffCopies:
			a.diffTable(c, p.Diff)
		case library.ExtraLinks:
			a.extraTable(c, p.Extra)
		case library.BrokenLinks:
			a.brokenTable(c, p.Broken)
		case library.NoDescription:
			a.noDescTable(c, p.NoDesc)
		case library.Incomplete:
			a.incompleteTable(c, p.Incomplete)
		case library.GlobalLinked:
			a.globalTable(c, p.Global)
		case library.StaleRecords:
			a.staleTable(c, p.Stale)
		}
	})
}

// ——— 表格 ———

// column 是表格的一列：width 为 0 时按 grow 分剩下的宽度。
type column struct {
	title string
	width float32
	grow  float32
}

// rowStyle 是表格行的额外设置，nil 是没有。
type rowStyle struct {
	key   func(i int) any             // 行的 Key：行会收起时按 ID 认行，动画才跟着对的行
	apply func(i int, row ui.Element) // 过渡、底色等
}

// table 是原生列表样式的表格：列标题、隔行底色，每行最右边是按钮
// （actions 宽，放得下这一类最宽的按钮）。
func table(c *ui.Context, cols []column, actions float32, n int, cell func(i, col int), buttons func(i int), style *rowStyle) {
	fill := func(row ui.Element, cellFn func(col int), acts func()) {
		row.Children(func() {
			for j, col := range cols {
				box := ui.Row(c.Key(j)).MinWidth(0).AlignItems(ui.Center).Gap(6)
				if col.width > 0 {
					box.Width(col.width).Shrink(0)
				} else {
					box.Grow(col.grow).Basis(0)
				}
				box.Children(func() { cellFn(j) })
			}
			ui.Row(c.Key("actions")).MinWidth(actions).Shrink(0).Gap(6).Justify(ui.End).AlignItems(ui.Center).Children(acts)
		})
	}
	header := ui.Row(c).Height(28).Padding(0, 24).Gap(12).AlignItems(ui.Center).Background(colBgPane).
		BorderWidth(0, 0, 1, 0).BorderColor(colBorderDefault).FontSize(12).TextColor(colTextSecondary)
	fill(header, func(j int) { ui.Text(c, cols[j].title).FontWeight(500).SingleLine() }, func() {})
	for i := 0; i < n; i++ {
		var key any = i
		if style != nil && style.key != nil {
			key = style.key(i)
		}
		row := ui.Row(c.Key(key)).MinHeight(36).Padding(4, 24).Gap(12).AlignItems(ui.Center).FontSize(13)
		if i%2 == 1 {
			row.Background(colBgSubtle)
		}
		if style != nil && style.apply != nil {
			style.apply(i, row)
		}
		fill(row, func(j int) { cell(i, j) }, func() { buttons(i) })
	}
}

func cellText(c *ui.Context, text string) ui.Element {
	return ui.Text(c, text).SingleLine().MinWidth(0)
}

func pathText(c *ui.Context, text string) ui.Element {
	return ui.Text(c, text).Font("monospace").FontSize(12).TextColor(colTextSecondary).SingleLine().MinWidth(0)
}

// agentNamed 是 Agent 图标加名称。
func agentNamed(c *ui.Context, id string) {
	agentBadge(c, id, 18)
	cellText(c, library.AgentName(id))
}

// ——— 六类问题的表格 ———

func (a *app) sameTable(c *ui.Context, skills []*library.Skill) {
	cols := []column{{locales.CommonSkills(), 0, 1.3}, {locales.TidyCopyCountColumn(), 70, 0}, {locales.TidyReadersColumn(), 0, 2}, {locales.CommonLastModified(), 110, 0}}
	table(c, cols, 90, len(skills), func(i, j int) {
		s := skills[i]
		switch j {
		case 0:
			cellText(c, s.Name)
		case 1:
			cellText(c, locales.CommonCopyCount(s.Copies+1)).FontFeatures("tnum")
		case 2:
			// 读到任意一份的 Agent 都算：只看启用的，读到副本的会漏掉
			if vs := s.Versions(); len(vs) > 0 {
				a.versionAgents(c, &vs[0], 18, 10)
			} else {
				a.enabledBadges(c, s, "", 18, 10)
			}
		case 3:
			cellText(c, dayLabel(a.lib.Modified(s), time.Now())).TextColor(colTextMuted)
		}
	}, func(i int) { a.mergeRowActions(c, skills[i]) }, a.sameRowStyle(skills))
}

func (a *app) diffTable(c *ui.Context, skills []*library.Skill) {
	cols := []column{{locales.CommonSkills(), 0, 1}, {locales.CommonVersion(), 0, 2}, {locales.CommonDifferences(), 0, 1.5}, {locales.TidyRecentChanges(), 110, 0}}
	table(c, cols, 60, len(skills), func(i, j int) {
		s := skills[i]
		vs := s.Versions()
		switch j {
		case 0:
			cellText(c, s.Name)
		case 1:
			// 三种以上内容时一行放不下，折到下一行，不压到差异那一列
			ui.Row(c).Grow(1).MinWidth(0).Wrap().Gap(6).Padding(3, 0).Children(func() {
				for k := range vs {
					a.versionChip(c.Key(k), s, k)
				}
			})
		case 2:
			ui.Text(c, a.diffStat(s)).FontFeatures("tnum").SingleLine().MinWidth(0)
		case 3:
			cellText(c, dayLabel(a.lib.Modified(s), time.Now())).TextColor(colTextMuted)
		}
	}, func(i int) {
		a.progressRowActions(c, a.tidyProgressFor(library.DiffCopies), skills[i].ID,
			rowAction{title: locales.ActionCompare(), running: locales.MergeInProgress(), done: locales.MergeCompleted(),
				run: func() { a.openCompare(a.lib.ByID(skills[i].ID), true) }})
	}, progressRowStyle(a.tidyProgressFor(library.DiffCopies), func(i int) string { return skills[i].ID }))
}

// versionChip 是一种内容的小药丸：字母、基准、几处、读到它的 Agent。
func (a *app) versionChip(c *ui.Context, s *library.Skill, k int) {
	v := &s.Versions()[k]
	ui.Row(c).Height(22).Padding(0, 8).Gap(5).Radius(6).AlignItems(ui.Center).Shrink(0).Background(colBgCard).
		Border(1, colBorderDefault).FontSize(12).Children(func() {
		ui.Text(c, versionLetter(k)).FontWeight(600)
		if k == 0 {
			baseTag(c)
		}
		ui.Text(c, locales.CommonLocationCount(len(v.Places))).TextColor(colTextSecondary).SingleLine()
		a.versionAgents(c, v, 14, 3)
	})
}

// versionAgents 是读到这种内容的 Agent：在技能库里的收成「通用」标。
func (a *app) versionAgents(c *ui.Context, v *library.Version, size float32, limit int) {
	ids := a.shown(v.Agents())
	if readers := a.readers(); v.Shared && len(readers) > 0 {
		sharedTag(c, readers, size)
		ids = slices.DeleteFunc(ids, func(id string) bool { return slices.Contains(readers, id) })
		limit--
	}
	if len(ids) > 0 {
		agentBadges(c, ids, size, max(limit, 1))
	}
}

func baseTag(c *ui.Context) {
	ui.Row(c).Height(16).Padding(0, 5).Radius(4).Background(colAccentBg).AlignItems(ui.Center).Children(func() {
		ui.Text(c, locales.CompareBaseline()).FontSize(11).TextColor(colAccentText).SingleLine()
	})
}

func versionLetter(k int) string { return string(rune('A' + k)) }

// diffStat 是基准和第二种内容差几行，如「SKILL.md −2 +4，多 1 个文件」。
func (a *app) diffStat(s *library.Skill) string {
	if t, ok := a.tidy.stats[s.ID]; ok {
		return t
	}
	vs := s.Versions()
	if len(vs) < 2 {
		return ""
	}
	var added, deleted, more int
	for _, f := range a.lib.Compare(&vs[0], &vs[1]) {
		switch {
		case f.OnlyLeft || f.OnlyRight:
			more++
		default:
			added += f.Added
			deleted += f.Deleted
		}
	}
	text := ""
	if added+deleted > 0 {
		text = fmt.Sprintf("−%d +%d", deleted, added)
	}
	if more > 0 {
		if text != "" {
			text += locales.ClauseSeparator()
		}
		text += locales.TidyOneSidedFiles(more)
	}
	if a.tidy.stats == nil {
		a.tidy.stats = map[string]string{}
	}
	a.tidy.stats[s.ID] = text
	return text
}

func (a *app) extraTable(c *ui.Context, extra []library.ExtraLink) {
	cols := []column{{locales.CommonSkills(), 0, 1}, {locales.TidyDuplicateReaders(), 0, 1}, {locales.TidyExtraCopy(), 0, 2}}
	table(c, cols, 140, len(extra), func(i, j int) {
		e := extra[i]
		switch j {
		case 0:
			cellText(c, e.Skill.Name)
		case 1:
			agentNamed(c, e.Agent)
		case 2:
			pathText(c, a.lib.Display(e.Path))
		}
	}, func(i int) {
		e := extra[i]
		a.progressRowActions(c, a.tidyProgressFor(library.ExtraLinks), e.Path,
			rowAction{title: locales.TidyDropFrom(library.AgentName(e.Agent)), running: locales.TidyDeletingLink(), done: locales.TidyLinkDeleted(),
				run: func() { a.dropExtraLinks([]library.ExtraLink{e}) }})
	}, progressRowStyle(a.tidyProgressFor(library.ExtraLinks), func(i int) string { return extra[i].Path }))
}

func (a *app) brokenTable(c *ui.Context, broken []library.BrokenLink) {
	cols := []column{{locales.CommonLink(), 0, 1}, {locales.TidyWhere(), 0, 2}, {locales.TidyCondition(), 0, 1}}
	table(c, cols, 90, len(broken), func(i, j int) {
		b := broken[i]
		switch j {
		case 0:
			cellText(c, b.Name)
		case 1:
			pathText(c, a.lib.Display(b.Dir))
		case 2:
			if b.Relink {
				cellText(c, locales.TidyLibraryNameExists())
			} else {
				cellText(c, locales.TidySkillGone()).TextColor(colTextMuted)
			}
		}
	}, func(i int) {
		b := broken[i]
		act := rowAction{title: locales.TidyDeleteLink(), running: locales.TidyDeletingLink(), done: locales.TidyLinkDeleted(),
			run: func() { a.repairBrokenLink(b) }}
		if b.Relink {
			act.title, act.running, act.done = locales.TidyReconnect(), locales.TidyReconnecting(), locales.TidyReconnected()
		}
		a.progressRowActions(c, a.tidyProgressFor(library.BrokenLinks), b.Path, act)
	}, progressRowStyle(a.tidyProgressFor(library.BrokenLinks), func(i int) string { return broken[i].Path }))
}

func (a *app) noDescTable(c *ui.Context, skills []*library.Skill) {
	cols := []column{{locales.CommonSkills(), 0, 1}, {locales.CommonSource(), 0, 1}, {locales.CommonLocation(), 0, 2.5}}
	table(c, cols, 110, len(skills), func(i, j int) {
		s := skills[i]
		switch j {
		case 0:
			cellText(c, s.Name)
		case 1:
			if s.Source == library.Project {
				cellText(c, s.Project)
			} else {
				cellText(c, s.Source.Label())
			}
		case 2:
			pathText(c, a.lib.Display(s.Dir))
		}
	}, func(i int) {
		if pushButton(c, locales.ActionOpenInEditor()).Clicked() {
			a.openInEditor(skills[i].SkillFile())
		}
	}, nil)
}

// incompleteTable 列出文件不完整的技能：第一处问题，后面还有几处；
// 悬停看全部。处理办法和缺简介一样，在编辑器里改。
func (a *app) incompleteTable(c *ui.Context, skills []*library.Skill) {
	cols := []column{{locales.CommonSkills(), 0, 1}, {locales.CommonSource(), 0, 0.8}, {locales.TidyCondition(), 0, 2.4}}
	table(c, cols, 110, len(skills), func(i, j int) {
		s := skills[i]
		switch j {
		case 0:
			cellText(c, s.Name)
		case 1:
			if s.Source == library.Project {
				cellText(c, s.Project)
			} else {
				cellText(c, s.Source.Label())
			}
		case 2:
			cellText(c, s.Defects[0].String()).Shrink(1).Tooltip(defectLines(s.Defects))
			if n := len(s.Defects) - 1; n > 0 {
				ui.Text(c, locales.TidyMoreDefects(n)).FontSize(12).TextColor(colTextMuted).SingleLine().Shrink(0)
			}
		}
	}, func(i int) {
		if pushButton(c, locales.ActionOpenInEditor()).Clicked() {
			a.openInEditor(skills[i].SkillFile())
		}
	}, nil)
}

// defectLines 是每处问题一行。
func defectLines(ds []library.Defect) string {
	lines := make([]string, len(ds))
	for i, d := range ds {
		lines[i] = d.String()
	}
	return strings.Join(lines, "\n")
}

func (a *app) globalTable(c *ui.Context, skills []*library.Skill) {
	cols := []column{{locales.CommonSkills(), 0, 1}, {locales.TidyProjectColumn(), 0, 1}, {locales.TidyGloballyLinkedTo(), 0, 1.4}}
	table(c, cols, 300, len(skills), func(i, j int) {
		s := skills[i]
		switch j {
		case 0:
			cellText(c, s.Name)
		case 1:
			cellText(c, s.Project)
		case 2:
			if dirs := s.GlobalDirs(); len(dirs) == 1 {
				if id := a.lib.DirOwner(dirs[0]); id != "" {
					agentNamed(c, id)
					return
				}
			}
			agentBadges(c, a.shown(s.Readers(true)), 18, 6)
		}
	}, func(i int) {
		s := skills[i]
		if a.progressRowStatus(c, a.tidyProgressFor(library.GlobalLinked), s.ID, locales.TidyProcessing(), locales.TidyProcessed()) {
			return
		}
		a.globalActions(c, s, pushButton)
		if pushButton(c, locales.TidyUseOnlyInProject()).Disabled(a.tidy.busy() || a.tidy.scanning).Clicked() {
			a.tidyProject(s, removeProjectLinks)
		}
	}, progressRowStyle(a.tidyProgressFor(library.GlobalLinked), func(i int) string { return skills[i].ID }))
}

func (a *app) staleTable(c *ui.Context, rows []library.StaleRecord) {
	cols := []column{{locales.CommonSource(), 0, 1.2}, {locales.CommonSkills(), 0, 2}, {locales.TidyInstalledIn(), 110, 0}}
	table(c, cols, 90, len(rows), func(i, j int) {
		r := rows[i]
		switch j {
		case 0:
			cellText(c, r.Source)
		case 1:
			cellText(c, namesPreview(r.Names, 3))
		case 2:
			cellText(c, dayLabel(r.Installed, time.Now())).TextColor(colTextMuted)
		}
	}, func(i int) {
		r := rows[i]
		a.progressRowActions(c, a.tidyProgressFor(library.StaleRecords), r.Source, rowAction{title: locales.TidyClearRecord(), running: locales.TidyClearingRecord(), done: locales.TidyRecordCleared(),
			run: func() { a.clearRecords(r) }})
	}, progressRowStyle(a.tidyProgressFor(library.StaleRecords), func(i int) string { return rows[i].Source }))
}

// clearAllButton 是「全部清除」，两行及以上才有；清的时候它变形成进度胶囊。
func (a *app) clearAllButton(c *ui.Context, rows []library.StaleRecord) {
	if m := a.tidyProgressFor(library.StaleRecords); m != nil {
		if a.tidy.work.rows.Count(library.StaleRecords) >= 2 {
			progressPill(c, m, locales.TidyRecordCleared())
		}
		return
	}
	if len(rows) < 2 {
		return
	}
	b := dialogButton(c, locales.ActionClearAll(), false).Shrink(0).
		Transition(ui.ElementTransition{Match: batchButtonMatch{}, Animation: &morphSpring})
	if a.tidy.busy() || a.tidy.scanning {
		b.Disabled(true)
	}
	if b.Clicked() {
		a.clearRecords(rows...)
	}
}

// namesPreview 是前 n 个名字，多的写「等 N 个」：「dbs、dbs-action、dbs-ai-check 等 32 个」。
func namesPreview(names []string, n int) string {
	text := strings.Join(names[:min(n, len(names))], locales.ListSeparator())
	if len(names) > n {
		text = locales.ListEtcTotal(text, len(names))
	}
	return text
}

// clearRecords uses the shared progress flow. Record edits run on the UI thread
// because they also mutate the in-memory manifest.
func (a *app) clearRecords(todo ...library.StaleRecord) {
	lib := a.lib
	var tasks []tidyTask
	n, skipped := 0, false
	for _, r := range todo {
		tasks = append(tasks, tidyTask{r.Source, r.Source, func() error {
			k, err := lib.ClearRecords(r)
			n += k
			if k < len(r.Names) {
				skipped = true
			}
			return err
		}})
	}
	a.runTidy(tidyOperation{kind: library.StaleRecords, tasks: tasks,
		doing:  locales.TidyClearingName,
		result: func(int) string { return locales.TidyStaleRecordsCleared(n) },
		after: func() {
			if skipped {
				a.rescan()
			}
		}})
}

// mergeNotice 是刚收成一份的技能：详情栏顶部的绿色提示，和开关变亮的
// 动画（f1-d）。选中别的技能或离开这一页时清掉。
type mergeNotice struct {
	skill    string   // 技能 ID
	result   string   // 做了什么
	unlocked []string // 原来是副本、开关拨不动的 Agent
	started  bool     // 动画已经开始
}

// merge 收成一份，留下第 keep 种内容。fromTidy 时留在整理页，否则回到
// 详情栏，顶部是绿色提示。
func (a *app) merge(s *library.Skill, keep int, fromTidy bool) {
	if fromTidy {
		a.mergeCopies([]*library.Skill{s}, keep, library.DiffCopies)
		return
	}
	a.tidy.msg, a.tidy.err, a.err, a.errAgent = "", "", "", ""
	defer a.changed()
	before := slices.Clone(s.Copied())
	r, err := a.lib.Merge(s, keep)
	id := s.ID
	if a.lib.Home != "" {
		a.rescan()
	}
	if err == nil {
		a.chimeDone() // 收成功了就响，重新扫描后找不找得到这个技能是另一回事
	}
	if s = a.lib.ByID(id); s == nil {
		return
	}
	if err != nil {
		a.tidy.err, a.err = err.Error(), err.Error()
	}
	a.selected = s
	if err == nil {
		unlocked := slices.DeleteFunc(before, func(id string) bool { return s.CopyIn(id) })
		a.notice = &mergeNotice{skill: id, result: r.String(), unlocked: unlocked}
	}
}

// ——— 时间 ———

// scannedLabel 是「刚刚检测」「5 分钟前检测」。
func scannedLabel(at, now time.Time) string {
	switch d := now.Sub(at); {
	case d < time.Minute:
		return locales.TidyCheckedJustNow()
	case d < time.Hour:
		return locales.TidyCheckedMinutesAgo(int(d.Minutes()))
	}
	return locales.TidyCheckedAt(at.Format("15:04"))
}

// dayLabel 是「9 月 17 日」，不是今年的带上年份。
func dayLabel(t, now time.Time) string {
	if t.IsZero() {
		return ""
	}
	return monthDay(t, now)
}
