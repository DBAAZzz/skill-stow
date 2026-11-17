package main

import (
	"errors"
	"path/filepath"
	"runtime"
	"slices"
	"strings"

	"github.com/egoist/mygo/ui"

	"skillmanager/internal/library"
	"skillmanager/locales"
)

// 删除技能（del1–del6）：从详情栏的「…」、卡片悬停的操作行、右键菜单或
// ⌘⌫ 发起，确认框写出每个 Agent 会少了什么；删完出现提示条（toast.go），
// 「撤销」或 ⌘Z 放回去。提示条收起后 ⌘Z 仍能撤销，直到下一次删除。
// 使用情况页可以勾选几个没用过的一起删。

// deleteState 是删除的状态。
type deleteState struct {
	confirm     []*library.Skill // 待确认删除的技能
	confirmOpen bool
	copies      *library.Skill // 有同名副本、要先去对比的技能
	copiesOpen  bool

	last *library.Deletion // 最近一次删除，⌘Z 撤销它；撤销过或放下了为 nil

	picking bool            // 使用情况页「没用过的」在选择
	picked  map[string]bool // 勾上的技能 ID
	reveal  bool            // 下一帧把「没用过的」滚到顶上
}

// requestDelete 发起删除：有同名副本的单个技能先提示去对比，其余弹出确认框。
func (a *app) requestDelete(skills ...*library.Skill) {
	if len(skills) == 0 {
		return
	}
	if len(skills) == 1 && errors.Is(a.lib.CanDelete(skills[0]), library.ErrHasCopies) {
		a.del.copies, a.del.copiesOpen = skills[0], true
		return
	}
	a.del.confirm, a.del.confirmOpen = skills, true
}

// doDelete 删掉确认框里的技能，上一次删除就此放下，不能再撤销。
func (a *app) doDelete() {
	skills := a.del.confirm
	a.del.confirm, a.del.confirmOpen = nil, false
	next := a.nextSelection(skills)
	if a.del.last != nil {
		a.del.last.Discard()
		a.del.last = nil
	}
	d, err := a.lib.Delete(skills...)
	if d != nil && len(d.Names) > 0 {
		a.del.last = d
		if slices.Contains(skills, a.selected) {
			a.selected = next
		}
		if a.reading != nil && slices.Contains(skills, a.reading) {
			a.reading = nil
		}
		a.del.picking, a.del.picked = false, nil
		a.exitBatch()
	}
	defer a.rescan()
	switch {
	case err != nil:
		a.deleteNotice(toastError, err.Error(), d != nil && len(d.Names) > 0)
	case len(d.Names) == 1:
		a.deleteNotice(toastPlain, locales.DeleteMovedOne(d.Names[0]), true)
	default:
		a.deleteNotice(toastPlain, locales.DeleteMovedToTrash(len(d.Names)), true)
	}
}

// deleteNotice 是删除的结果；undoable 时带「撤销」，有删掉的就能撤销，
// 哪怕同时报了错。
func (a *app) deleteNotice(kind toastKind, text string, undoable bool) {
	t := &toast{source: fromDelete, kind: kind, text: text}
	if undoable {
		t.action = &toastAction{label: locales.ActionUndo(), shortcut: undoKey(), run: a.undoDelete}
	}
	a.showToast(t)
}

// undoDelete 撤销最近一次删除，放回的第一个技能选中。
func (a *app) undoDelete() {
	d := a.del.last
	if d == nil {
		return
	}
	defer a.rescan()
	if err := a.lib.Undo(d); err != nil {
		a.notify(fromDelete, toastError, err.Error())
		return
	}
	a.del.last = nil
	a.dismissToast(fromDelete)
	if s := a.lib.Get(d.Names[0]); s != nil {
		a.selected, a.coll = s, nil
	}
}

// discardDeletion 放下最近一次删除，清掉为撤销留的那份。退出时调用。
func (a *app) discardDeletion() {
	if a.del.last != nil {
		a.del.last.Discard()
		a.del.last = nil
	}
}

// nextSelection 是删掉 gone 之后选中谁：同一来源里排在后面的第一个，没有
// 就是前面的。
func (a *app) nextSelection(gone []*library.Skill) *library.Skill {
	if a.selected == nil || !slices.Contains(gone, a.selected) {
		return a.selected
	}
	same := bySource(a.lib.Skills, a.selected.Source)
	i := slices.Index(same, a.selected)
	for _, s := range slices.Concat(same[i+1:], reverse(same[:i])) {
		if !slices.Contains(gone, s) {
			return s
		}
	}
	return nil
}

func reverse(skills []*library.Skill) []*library.Skill {
	out := slices.Clone(skills)
	slices.Reverse(out)
	return out
}

// deleteShortcuts 是 ⌘⌫ 删除选中的技能、⌘Z 撤销最近一次删除。输入框
// 有焦点时它们先拿到按键，照常编辑文字。
func (a *app) deleteShortcuts(c *ui.Context) {
	if a.del.last != nil {
		c.OnWindowShortcut(ui.Cmd, ui.KeyZ, a.undoDelete)
	}
	if a.batch.on {
		a.batchDeleteShortcut(c)
		return
	}
	a.pruneFilterSelection()
	if s := a.selected; s != nil && !s.ReadOnly() && a.cmp == nil && a.scope != scopeTidy && a.scope != scopeUsage {
		c.OnWindowShortcut(ui.Cmd, ui.KeyBackspace, func() { a.requestDelete(s) })
	}
}

// ——— 入口 ———

// skillMenu 是技能卡片的右键菜单（del4-a）；只读的技能没有删除。
func (a *app) skillMenu(fm *frostedMenu, s *library.Skill) {
	if fm.action(locales.ActionOpen(), withIcon(iconDoc), withShortcut("Space")) {
		a.open(s)
	}
	a.fileItems(fm, s)
	if s.ReadOnly() {
		return
	}
	fm.separator()
	a.deleteItem(fm, s)
}

// paneMenu 是详情栏的「…」菜单（sf6-inspector）。
func (a *app) paneMenu(fm *frostedMenu, s *library.Skill) {
	a.fileItems(fm, s)
	fm.separator()
	a.deleteItem(fm, s)
}

func (a *app) fileItems(fm *frostedMenu, s *library.Skill) {
	if fm.action(locales.ActionShowInFinder(), withIcon(iconFolder)) {
		a.reveal(s.Dir)
	}
	if fm.action(locales.ActionOpenInEditor(), withIcon(iconExternal)) {
		a.openInEditor(filepath.Join(s.Dir, "SKILL.md"))
	}
}

func (a *app) deleteItem(fm *frostedMenu, s *library.Skill) {
	if fm.action(locales.ActionDeleteSkillEllipsis(), withIcon(iconTrash), withDanger(), withShortcut("⌘⌫")) {
		a.requestDelete(s)
	}
}

// cardActions 是指针停在我的技能卡片上时底部那一行（del6-c）：
// 左边预览、在 Finder 中显示，右边红色的删除。两个蓝色按钮之间只靠间距
// 分开，不加分隔符，和 macOS 并排的链接按钮一样。
func (a *app) cardActions(c *ui.Context, s *library.Skill) {
	ui.Row(c).Gap(16).AlignItems(ui.Center).Children(func() {
		if cardAction(c, locales.ActionPreview(), colAccent).Clicked() {
			a.open(s)
		}
		if cardAction(c, locales.ActionShowInFinder(), colAccent).Clicked() {
			a.reveal(s.Dir)
		}
	})
	ui.Spacer(c)
	if cardAction(c, locales.ActionDelete(), colDangerText).Clicked() {
		a.selected, a.coll, a.picked = s, nil, true
		a.requestDelete(s)
	}
}

func cardAction(c *ui.Context, label string, color ui.Color) ui.Element {
	b := ui.ButtonBase(c).Cursor(ui.CursorPointer).Label(label)
	b.TextColor(tint(b, "text", color, color.Alpha(0.72), color.Alpha(0.5)))
	b.Children(func() { ui.Text(c, label).FontSize(12).SingleLine() })
	return b
}

// ——— 确认框 ———

// deleteDialogs 画删除的确认框和「先去对比」的提示，没有要显示的就不画。
func (a *app) deleteDialogs(c *ui.Context) {
	if a.del.confirmOpen {
		a.confirmDeleteDialog(c)
	} else {
		a.del.confirm = nil
	}
	if a.del.copiesOpen {
		a.copiesFirstDialog(c)
	} else {
		a.del.copies = nil
	}
}

// confirmDeleteDialog 是确认框（del2-a、del5-b）：原件进废纸篓，按 Agent
// 写出各自少了什么。默认按钮是「取消」，回车不会删。
func (a *app) confirmDeleteDialog(c *ui.Context) {
	skills := a.del.confirm
	impacts := map[*library.Skill][]library.Impact{}
	links := 0
	for _, s := range skills {
		impacts[s] = a.lib.DeleteImpact(s)
		for _, im := range impacts[s] {
			if im.Kind == library.ImpactLink {
				links++
			}
		}
	}
	title := locales.DeleteTitleOne(skills[0].Name)
	body := locales.DeleteBodyTrash(len(skills))
	if len(skills) == 1 {
		if real, outside := a.lib.RealOutside(skills[0]); outside {
			body = locales.DeleteBodyOutside(a.lib.Display(real))
		}
	}
	switch {
	case len(skills) > 1:
		title = locales.DeleteConfirmCount(len(skills))
		if links > 0 {
			body += locales.DeleteBodyLinksMany(links)
		}
		var outside []string
		for _, s := range skills {
			if real, ok := a.lib.RealOutside(s); ok {
				outside = append(outside, locales.NameWithPath(s.Name, a.lib.Display(real)))
			}
		}
		if len(outside) > 0 {
			body += locales.DeleteOutsideOriginals(strings.Join(outside, locales.ListSeparator()))
		}
	case links > 0:
		body += locales.DeleteBodyLinksOne()
	}
	body = locales.SentenceEnd(body)
	dialog(c, &a.del.confirmOpen, title, body, func() {
		if len(skills) > 1 {
			a.batchImpacts(c, skills, impacts)
		} else {
			a.skillImpacts(c, impacts[skills[0]])
		}
	}, func() {
		cancel := dialogButton(c, locales.ActionCancel(), false).AutoFocus()
		if cancel.Clicked() {
			a.del.confirmOpen = false
		}
		if dangerButton(c, locales.ActionMoveToTrash()).Clicked() {
			a.doDelete()
		}
	})
}

// skillImpacts 是删一个技能时每个 Agent 一行；和上一行一样的写「同上」。
func (a *app) skillImpacts(c *ui.Context, impacts []library.Impact) {
	if len(impacts) == 0 {
		return
	}
	impactList(c, func() {
		prev := ""
		for i, im := range impacts {
			what := a.impactText(im)
			shown := what
			if what == prev {
				shown = locales.DeleteSameAsAbove()
			}
			prev = what
			impactRow(c.Key(i), i == 0, func() {
				agentBadge(c, im.Agent, 15)
				ui.Text(c, library.AgentName(im.Agent)).FontSize(13).FontWeight(500).Width(96).Shrink(0).SingleLine()
				ui.Text(c, shown).FontSize(12).TextColor(colTextSecondary).Grow(1).MinWidth(0).SingleLine()
			})
		}
	})
}

func (a *app) impactText(im library.Impact) string {
	switch im.Kind {
	case library.ImpactShared:
		where := locales.NavLibrary()
		if im.Dir != a.lib.Root {
			where = a.lib.Display(im.Dir)
		}
		return locales.DeleteImpactReads(where)
	case library.ImpactOriginal:
		return locales.DeleteOriginalInDirectory()
	}
	return locales.DeleteLinkInDirectory()
}

// batchImpacts 是删几个技能时的影响（del5-b）：先列名字，再按「少了哪几个」
// 把 Agent 归成一行。
func (a *app) batchImpacts(c *ui.Context, skills []*library.Skill, impacts map[*library.Skill][]library.Impact) {
	var names []string
	for _, s := range skills {
		names = append(names, s.Name)
	}
	ui.Text(c, nameList(names, batchNamesShown)).FontSize(13).FontWeight(500).LineHeight(1.5).Margin(8, 0, 0, 0)
	lost := map[string][]string{} // Agent → 少了的技能
	var agents []string
	for _, s := range skills {
		for _, im := range impacts[s] {
			if _, ok := lost[im.Agent]; !ok {
				agents = append(agents, im.Agent)
			}
			lost[im.Agent] = append(lost[im.Agent], s.Name)
		}
	}
	type group struct{ agents, lost []string }
	var groups []*group
	byLost := map[string]*group{}
	for _, id := range agents {
		key := strings.Join(lost[id], locales.ListSeparator())
		g := byLost[key]
		if g == nil {
			g = &group{lost: lost[id]}
			byLost[key] = g
			groups = append(groups, g)
		}
		g.agents = append(g.agents, id)
	}
	if len(groups) == 0 {
		return
	}
	impactList(c, func() {
		for i, g := range groups {
			var who []string
			for _, id := range g.agents {
				who = append(who, library.AgentName(id))
			}
			impactRow(c.Key(i), i == 0, func() {
				ui.Row(c).Gap(3).Shrink(0).Children(func() {
					for _, id := range g.agents {
						agentBadge(c.Key(id), id, 15)
					}
				})
				names := strings.Join(who, locales.ListSeparator())
				if len(who) > batchAgentsNamed {
					names = locales.DeleteAgentListSummary(strings.Join(who[:2], locales.ListSeparator()), len(who))
				}
				ui.Text(c, names).FontSize(13).FontWeight(500).Shrink(0).SingleLine()
				ui.Text(c, locales.DeleteLoses(nameList(g.lost, batchLostShown))).FontSize(12).TextColor(colTextSecondary).Grow(1).MinWidth(0).SingleLine()
			})
		}
	})
}

// batchAgentsNamed 是批量确认框里一组最多写出几个 Agent 的名字，多了写
// 「A、B 等 N 个」，免得把「少了…」挤出框。
const batchAgentsNamed = 3

// 批量确认框里最多写出几个技能名、每个 Agent「少了」后面最多写几个：一次
// 勾一个集合就是几十个，全写出来框会很长。
const (
	batchNamesShown = 10
	batchLostShown  = 3
)

// nameList 是「、」连起来的名字，多于 limit 个时写「A、B 等 N 个」。
func nameList(names []string, limit int) string {
	if len(names) <= limit {
		return strings.Join(names, locales.ListSeparator())
	}
	return locales.DeleteAgentListSummary(strings.Join(names[:limit], locales.ListSeparator()), len(names))
}

func impactList(c *ui.Context, fn func()) {
	ui.Column(c).Margin(12, 0, 0, 0).Radius(10).Border(1, colBorderDefault).Clip().Children(fn)
}

func impactRow(c *ui.Context, first bool, fn func()) {
	row := ui.Row(c).Padding(8, 12).Gap(10).AlignItems(ui.Center)
	if !first {
		row.BorderWidth(1, 0, 0, 0).BorderColor(colBorderSubtle)
	}
	row.Children(fn)
}

// copiesFirstDialog 是有同名副本时的提示（del4-b）：只删这一份不算删掉，
// 先去对比收成一份。
func (a *app) copiesFirstDialog(c *ui.Context) {
	s := a.del.copies
	title := locales.DeleteOtherCopies(s.Name, s.Copies)
	body := locales.DeleteMergeBeforeDeleting()
	dialog(c, &a.del.copiesOpen, title, body, func() { a.copiesList(c, s) }, func() {
		if dialogButton(c, locales.ActionCancel(), false).AutoFocus().Clicked() {
			a.del.copiesOpen = false
		}
		if s.Identical() && a.lib.CanMerge(s) {
			if dialogButton(c, locales.ActionMergeCopies(), true).Clicked() {
				a.del.copiesOpen = false
				a.merge(s, 0, false)
			}
			return
		}
		if dialogButton(c, locales.ActionCompareCopiesEllipsis(), true).Clicked() {
			a.del.copiesOpen = false
			a.openCompare(s, false)
		}
	})
}

// copiesShown 是副本提示里最多列几份，其余的收成一行。
const copiesShown = 3

// copiesList 列出这份之外的各份副本：谁的目录、在哪、内容和这份一不一样。
func (a *app) copiesList(c *ui.Context, s *library.Skill) {
	type copyRow struct {
		place library.Copy
		same  bool
	}
	var rows []copyRow
	seen := map[string]bool{s.Real: true, s.Dir: true} // 这份自己；示例数据没有 Real
	for i, v := range s.Versions() {
		for _, p := range v.Places {
			if !seen[p.Real] {
				seen[p.Real] = true
				rows = append(rows, copyRow{p, i == 0})
			}
		}
	}
	if len(rows) == 0 {
		return
	}
	impactList(c, func() {
		for i, r := range rows[:min(len(rows), copiesShown)] {
			impactRow(c.Key(i), i == 0, func() {
				if r.place.Owner != "" {
					agentBadge(c, r.place.Owner, 15)
				} else {
					ui.Icon(c, iconFolder).Size(15, 15).TextColor(colTextMuted)
				}
				ui.Text(c, a.lib.Display(r.place.Path)).Font("monospace").FontSize(11).TextColor(colTextSecondary).
					Grow(1).MinWidth(0).SingleLine()
				tag := locales.CompareSameContent()
				if !r.same {
					tag = locales.CompareDifferentContent()
				}
				ui.Text(c, tag).FontSize(11).TextColor(colTextMuted).Shrink(0).SingleLine()
			})
		}
		if more := len(rows) - copiesShown; more > 0 {
			impactRow(c.Key("more"), false, func() {
				ui.Text(c, locales.DeleteRemainingCopies(more)).FontSize(12).TextColor(colTextMuted)
			})
		}
	})
}

// dangerButton 是弹窗里红色的主按钮。
func dangerButton(c *ui.Context, label string) ui.Element {
	return filledButton(c, label, colDanger)
}

// undoKey 是撤销删除的快捷键，写在提示条的「撤销」后面。
func undoKey() string {
	if runtime.GOOS == "darwin" {
		return "⌘Z"
	}
	return "Ctrl+Z"
}

// ——— 使用情况页的选择 ———

// pickToggle 是使用情况页工具栏右边的「批量删除」/「退出批量」。它原先在
// 「没用过的」标题右边，那里在默认窗口高度下要滚过整个「用过的」列表才看得
// 到；放到工具栏一直看得到，点了再把「没用过的」滚到顶上。
func (a *app) pickToggle(c *ui.Context) {
	if batchToggle(c.Key("pick-toggle"), a.del.picking, locales.BatchDelete(), false).Clicked() {
		a.setPicking(!a.del.picking)
	}
}

// setPicking 进出使用情况页的选择；进入时切到「我的」那一页并滚过去。
func (a *app) setPicking(on bool) {
	a.del.picking, a.del.picked, a.del.reveal = on, nil, on
	if on {
		a.unusedTab = library.Mine
	}
}

// pickable 是选择时能不能勾：有同名副本的要先去对比。
func (a *app) pickable(s *library.Skill) bool { return a.lib.CanDelete(s) == nil }

// pickedOf 是 skills 里勾上了的。
func (a *app) pickedOf(skills []*library.Skill) []*library.Skill {
	var out []*library.Skill
	for _, s := range skills {
		if a.del.picked[s.ID] && a.pickable(s) {
			out = append(out, s)
		}
	}
	return out
}

func (a *app) setPicked(s *library.Skill, on bool) {
	if a.del.picked == nil {
		a.del.picked = map[string]bool{}
	}
	if on {
		a.del.picked[s.ID] = true
	} else {
		delete(a.del.picked, s.ID)
	}
}

// pickBox 是选择时行首的勾选框；part 是集合里勾了一部分。
func pickBox(c *ui.Context, on, part, disabled bool) {
	b := ui.Box(c).Size(15, 15).Shrink(0).Radius(4).Center()
	switch {
	case disabled:
		b.Background(colBgSubtle).Border(1, colBorderDefault)
	case on || part:
		b.Background(colAccent).TextColor(white)
	default:
		b.Background(colBgControl).Border(1, colBorderStrong)
	}
	b.Children(func() {
		switch {
		case on:
			ui.Icon(c, iconCheck).Size(11, 11)
		case part:
			ui.Box(c).Size(7, 1.8).Radius(1).Background(white)
		}
	})
}

// pickSetRow 是选择时的集合行：勾上整组里能删的。
func (a *app) pickSetRow(c *ui.Context, coll *library.Collection, unused []*library.Skill, sep bool) {
	var can []*library.Skill
	for _, s := range unused {
		if a.pickable(s) {
			can = append(can, s)
		}
	}
	n := len(a.pickedOf(can))
	all := n > 0 && n == len(can)
	row := usageRow(c)
	if len(can) > 0 {
		row.Cursor(ui.CursorPointer)
		if row.Clicked() {
			for _, s := range can {
				a.setPicked(s, !all)
			}
		}
	}
	if n > 0 {
		row.Background(colAccentBg)
	}
	rowBody(c, row, sep, func() {
		pickBox(c, all, n > 0 && !all, len(can) == 0)
		a.unusedSetCells(c, coll, len(unused))
	})
}

// pickSkillRow 是选择时的技能行：点一下勾上或取消；有副本的勾不上。
func (a *app) pickSkillRow(c *ui.Context, s *library.Skill, sep bool) {
	can := a.pickable(s)
	on := can && a.del.picked[s.ID]
	row := usageRow(c)
	if can {
		row.Cursor(ui.CursorPointer)
		if row.Clicked() {
			a.setPicked(s, !on)
		}
	} else {
		row.Tooltip(copiesRule())
	}
	if on {
		row.Background(colAccentBg)
	}
	rowBody(c, row, sep, func() {
		pickBox(c, on, false, !can)
		a.skillTile(c, s, false)
		sub := colTextMuted
		if !can {
			sub = colTextDisabled
		}
		twoLines(c, s.Name, unusedSkillTag(s), s.Description, sub)
		if !can {
			ui.Text(c, locales.CommonHasCopies()).FontSize(11).TextColor(colWarnText).SingleLine().Shrink(0)
		}
	})
}

// pickBar 是选择时卡片底部的一行：已选几个、为什么有的勾不上、「删除…」。
func (a *app) pickBar(c *ui.Context, skills []*library.Skill) {
	picked := a.pickedOf(skills)
	blocked := slices.ContainsFunc(skills, func(s *library.Skill) bool { return !a.pickable(s) })
	ui.Row(c).Shrink(0).Gap(8).Padding(12, 0, 0, 0).AlignItems(ui.Center).BorderWidth(1, 0, 0, 0).BorderColor(colBorderSubtle).Children(func() {
		ui.Text(c, locales.CommonSelectedCount(len(picked))).FontSize(13).FontWeight(500).TextColor(colTextBody).SingleLine()
		if blocked {
			ui.Text(c, copiesRule()).FontSize(12).TextColor(colTextMuted).SingleLine().MinWidth(0).Shrink(1)
		}
		ui.Spacer(c)
		b := ui.ButtonBase(c).Height(24).Padding(0, 12).Radius(6).Center().Background(colBgControl).TextColor(colDangerText).Label(locales.ActionDeleteEllipsis()).Disabled(len(picked) == 0)
		hairline(b, 0.2)
		lift(b, 1, 1, 0.06)
		if len(picked) == 0 {
			b.Opacity(0.5)
		}
		if b.Clicked() {
			a.requestDelete(picked...)
		}
		b.Children(func() { ui.Text(c, locales.ActionDeleteEllipsis()).FontSize(13).SingleLine() })
	})
}
