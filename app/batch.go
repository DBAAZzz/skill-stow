package main

import (
	"slices"

	"github.com/egoist/mygo/ui"

	"skillmanager/internal/library"
	"skillmanager/locales"
)

// 我的技能的批量删除（画板「批量操作」batch-a0 / a1 / a）：顶栏「批量操作」
// 进入选择态，卡片右上角出现勾选框，单击卡片勾选；集合整组勾。顶栏左边
// 换成已选几项、全选、取消全选；「批量操作」原地变成「退出批量」，左边
// 多出「删除 N 个…」。
// 有同名副本的技能勾不上（先去整理合并，copiesRule），和使用情况页的规则一样。

// batchState 是批量选择的状态，和使用情况页的勾选分开存。
type batchState struct {
	on     bool
	picked map[string]bool // 勾上的技能 ID
}

// batchOffered 报告浏览页能不能进批量：只在我的技能的卡片视图。
func (a *app) batchOffered() bool {
	return a.scope == string(library.ScopeMine) && a.mode == viewCards && a.inColl == nil
}

// startBatch 进入选择态：详情栏收起，单击卡片改成勾选。
func (a *app) startBatch() {
	a.batch = batchState{on: true}
	a.selected, a.coll = nil, nil
}

func (a *app) exitBatch() { a.batch = batchState{} }

func (a *app) batchPicked(s *library.Skill) bool { return a.batch.picked[s.ID] && a.pickable(s) }

func (a *app) setBatchPicked(skills []*library.Skill, on bool) {
	if a.batch.picked == nil {
		a.batch.picked = map[string]bool{}
	}
	for _, s := range skills {
		if on && a.pickable(s) {
			a.batch.picked[s.ID] = true
		} else {
			delete(a.batch.picked, s.ID)
		}
	}
}

// pickState 是一组技能的勾选情况：勾了几个、能勾几个。
func (a *app) pickState(skills []*library.Skill) (picked, can int) {
	for _, s := range skills {
		if s.ReadOnly() || !a.pickable(s) {
			continue
		}
		can++
		if a.batch.picked[s.ID] {
			picked++
		}
	}
	return picked, can
}

// batchSelection 是卡片上看得到的这些里勾了什么。集合卡片代表整个集合，
// 勾它就是勾集合里所有能删的技能。
type batchSelection struct {
	skills  []*library.Skill // 要删的，去重
	items   int              // 勾了几项：一个集合算一项
	sets    int              // 其中几个集合
	skipped int              // 勾了的集合里有副本、跳过的技能
	all     bool             // 能勾的都勾了
	blocked bool             // 有勾不上的
}

func (a *app) batchSelection(shown []*library.Skill) batchSelection {
	var sel batchSelection
	sets, rest := a.group(shown)
	seen := map[string]bool{}
	add := func(s *library.Skill) {
		if !seen[s.ID] {
			seen[s.ID] = true
			sel.skills = append(sel.skills, s)
		}
	}
	sel.all = true
	for _, coll := range sets {
		picked, can := a.pickState(coll.Skills)
		sel.blocked = sel.blocked || can < len(coll.Skills)
		sel.all = sel.all && picked == can
		if picked == 0 {
			continue
		}
		sel.items++
		sel.sets++
		sel.skipped += len(coll.Skills) - can
		for _, s := range coll.Skills {
			if a.batchPicked(s) {
				add(s)
			}
		}
	}
	for _, s := range rest {
		can := !s.ReadOnly() && a.pickable(s)
		sel.blocked = sel.blocked || !can
		if !can {
			continue
		}
		if a.batchPicked(s) {
			sel.items++
			add(s)
		} else {
			sel.all = false
		}
	}
	return sel
}

// shownForBatch 是勾选范围：看得到的技能，加上看得到的集合里的全部技能。
func (a *app) shownForBatch(shown []*library.Skill) []*library.Skill {
	sets, rest := a.group(shown)
	out := slices.Clone(rest)
	for _, coll := range sets {
		out = append(out, coll.Skills...)
	}
	return out
}

// batchToolbar 是选择态的顶栏：左边照画板 batch-a1 / batch-a 写已选几项、
// 全选；右边和平常一样（browseControls），开关不挪位置。shown 是卡片上
// 看得到的技能。
func (a *app) batchToolbar(c *ui.Context, shown []*library.Skill) {
	sel := a.batchSelection(shown)
	a.toolbarRow(c, func() {
		ui.Row(c).Shrink(1).MinWidth(0).Gap(10).AlignItems(ui.Baseline).Children(func() {
			if len(sel.skills) == 0 {
				ui.Text(c, locales.BatchSelectSkillsToDelete()).FontSize(13).FontWeight(600).SingleLine().Shrink(0)
				if sel.blocked {
					ui.Text(c, copiesRule()).FontSize(12).TextColor(colTextMuted).SingleLine().MinWidth(0).Shrink(1)
				}
			} else {
				ui.Text(c, locales.BatchSelectedItems(sel.items)).FontSize(13).FontWeight(600).FontFeatures("tnum").SingleLine().Shrink(0)
				if note := sel.note(); note != "" {
					ui.Text(c, note).FontSize(12).TextColor(colTextMuted).FontFeatures("tnum").SingleLine().MinWidth(0).Shrink(1)
				}
			}
			if !sel.all {
				label := locales.BatchSelectAllItems()
				if len(sel.skills) == 0 {
					label = locales.ActionSelectAll()
				}
				if linkButton(c, label).Shrink(0).Clicked() {
					a.setBatchPicked(a.shownForBatch(shown), true)
				}
			}
			if len(sel.skills) > 0 && linkButton(c, locales.ActionDeselectAll()).Shrink(0).Clicked() {
				a.batch.picked = nil
			}
		})
	}, func() { a.browseControls(c, &sel) })
}

// batchToggle 是顶栏进入批量的开关（我的技能写「批量操作」，使用情况页写
// 「批量删除」），选择态下同一个位置写「退出批量」，底色和字换成蓝色表示
// 正开着。label 要和「退出批量」一样四个字，按钮才不变宽。
// iconOnly 只管没开时：开着时「退出批量」是唯一的出口，字一直留着。
func batchToggle(c *ui.Context, on bool, label string, iconOnly bool) ui.Element {
	if !on {
		return toolButton(c, iconBatch, label, iconOnly)
	}
	b := ui.ButtonBase(c).Height(24).Padding(0, 10).Gap(6).Radius(6).AlignItems(ui.Center).Shrink(0).
		Label(locales.BatchExit()).Tooltip(locales.BatchExitShortcut()).TextColor(colAccent)
	tintSolid(b, colAccentBg)
	b.Shadow(0, 0, 0, edgeSpread, colAccent.Alpha(0.35))
	b.Children(func() {
		ui.Icon(c, iconBatch).Size(13, 13).ContentTransition(labelTransition)
		ui.Text(c, locales.BatchExit()).FontSize(13).SingleLine().ContentTransition(labelTransition)
	})
	return b
}

// note 是「已选 N 项」后面的说明：有集合时写出一共几个技能，跳过的副本。
func (sel batchSelection) note() string {
	var note string
	if sel.sets > 0 {
		note = locales.BatchSelectionSummary(sel.sets, len(sel.skills))
	}
	if sel.skipped > 0 {
		if note != "" {
			note += locales.ClauseSeparator()
		}
		note += locales.BatchSkippedCopies(sel.skipped)
	}
	if note == "" {
		return ""
	}
	return locales.Parenthetical(note)
}

// batchDeleteButton 是选择条上红字白底的删除，没勾的时候淡着点不了。
func batchDeleteButton(c *ui.Context, label string, disabled bool) ui.Element {
	b := ui.ButtonBase(c).Height(24).Padding(0, 10).Gap(5).Radius(6).AlignItems(ui.Center).Shrink(0).Label(label).
		TextColor(colDangerText).Disabled(disabled)
	tintBg(b, colBgControl, colDangerBg.Over(colBgControl), colInkPress.Over(colDangerBg.Over(colBgControl)))
	b.Shadow(0, 0, 0, edgeSpread, colDanger.Alpha(0.35))
	if disabled {
		b.Opacity(0.45)
	}
	b.Children(func() {
		ui.Icon(c, iconTrash).Size(13, 13)
		ui.Text(c, label).FontSize(13).FontWeight(500).FontFeatures("tnum").SingleLine().ContentTransition(labelTransition)
	})
	return b
}

// batchSectionToggle 是选择态下每组标题右边的「全选这一组」。
func (a *app) batchSectionToggle(c *ui.Context, sec section) {
	var skills []*library.Skill
	skills = append(skills, sec.skills...)
	for _, coll := range sec.sets {
		skills = append(skills, coll.Skills...)
	}
	picked, can := a.pickState(skills)
	if can == 0 {
		return
	}
	all := picked == can
	label := locales.BatchSelectGroup()
	if all {
		label = locales.BatchDeselectGroup()
	}
	if linkButton(c, label).Clicked() {
		a.setBatchPicked(skills, !all)
	}
}

// copiesRule 是勾不上时的统一说法：写在选择条上，也是淡掉的卡片的悬停提示。
// 选择条上和别的字挤一行，要短到最小窗口也放得下。
func copiesRule() string { return locales.BatchCopiesRule() }

// batchCardStyle 是选择态下卡片的样子：勾上的蓝边浅蓝底，勾不上的连同外层
// 容器（如叠卡）一起淡掉，悬停时说为什么（why 为空就不说）。part 是集合里
// 勾了一部分。
func batchCardStyle(wrap, card ui.Element, on, part, can bool, why string) {
	switch {
	case !can:
		wrap.Opacity(0.5)
		card.Cursor(ui.CursorDefault)
		if why != "" {
			card.Tooltip(why)
		}
	case on || part:
		card.Background(colAccentBg).Shadow(0, 0, 0, 2, colAccent)
	}
}

// batchCheck 是卡片右上角的圆形勾选框（batch-a1）。
func batchCheck(c *ui.Context, on, part, can bool) {
	const size = 18
	b := ui.Box(c).Absolute().Top(10).Right(10).Size(size, size).Radius(size / 2).Center().PassThrough()
	switch {
	case !can:
		b.Background(colBgSubtle).Shadow(0, 0, 0, 1, colBorderDefault)
	case on || part:
		b.Background(colAccent).TextColor(white)
	default:
		b.Background(colBgControl).Shadow(0, 0, 0, 1.2, colBorderStrong)
	}
	b.Children(func() {
		switch {
		case on:
			ui.Icon(c, iconCheck).Size(12, 12)
		case part:
			ui.Box(c).Size(8, 2).Radius(1).Background(white)
		}
	})
}

// unpickableWhy 是选择态下 s 勾不上的原因，用作卡片的悬停提示。
func (a *app) unpickableWhy(s *library.Skill) string {
	switch {
	case s.ReadOnly():
		return locales.BatchReadOnlyCannotDelete()
	case !a.pickable(s):
		return copiesRule()
	}
	return ""
}

// batchSkill 是选择态下技能卡片的点击：勾上或取消。返回它勾没勾、能不能勾。
func (a *app) batchSkill(card ui.Element, s *library.Skill) (on, can bool) {
	can = !s.ReadOnly() && a.pickable(s)
	on = can && a.batch.picked[s.ID]
	if can && card.Clicked() {
		on = !on
		a.setBatchPicked([]*library.Skill{s}, on)
	}
	return on, can
}

// batchSet 是选择态下集合卡片的点击：整组勾上或取消。
func (a *app) batchSet(card ui.Element, coll *library.Collection) (on, part, can bool) {
	picked, n := a.pickState(coll.Skills)
	can, on, part = n > 0, n > 0 && picked == n, picked > 0 && picked < n
	if can && card.Clicked() {
		a.setBatchPicked(coll.Skills, !on)
		on, part = !on, false
	}
	return on, part, can
}

func (a *app) batchShown() []*library.Skill {
	return a.applyFilters(a.lib.Find(library.ScopeMine, a.query))
}

// pruneBatchPicked drops checked entries that are no longer reachable under the active search/filter/scan state.
func (a *app) pruneBatchPicked(shown []*library.Skill) {
	if len(a.batch.picked) == 0 {
		return
	}
	keep := map[string]bool{}
	for _, s := range a.shownForBatch(shown) {
		if !s.ReadOnly() && a.pickable(s) {
			keep[s.ID] = true
		}
	}
	for id := range a.batch.picked {
		if !keep[id] {
			delete(a.batch.picked, id)
		}
	}
}

// batchDeleteShortcut 是选择态下 ⌘⌫ 删除勾上的。
func (a *app) batchDeleteShortcut(c *ui.Context) {
	if !a.batch.on || !a.batchOffered() {
		return
	}
	shown := a.batchShown()
	a.pruneBatchPicked(shown)
	skills := a.batchSelection(shown).skills
	if len(skills) > 0 {
		c.OnWindowShortcut(ui.Cmd, ui.KeyBackspace, func() { a.requestDelete(skills...) })
	}
}
