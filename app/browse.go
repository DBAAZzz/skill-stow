package main

import (
	"fmt"
	"slices"
	"time"

	"github.com/egoist/mygo/ui"

	"skillmanager/internal/library"
	"skillmanager/locales"
)

// browse 是浏览页：工具栏、卡片 / 看板，选中时右侧出现详情栏。
func (a *app) browse(c *ui.Context) {
	scope := library.Scope(a.scope)
	all := a.lib.Find(scope, a.query)
	skills := a.applyFilters(all)
	a.pruneFilterSelection()

	sets, _ := a.group(skills)
	subtitle := countLabel(len(skills), len(sets), true)
	if a.mode == viewBoard {
		subtitle = locales.BrowseSkillsAndAgents(len(mineOf(skills)), len(a.boardAgents(mineOf(skills))))
	}
	switch {
	case a.batch.on && a.batchOffered():
		a.pruneBatchPicked(skills)
		a.batchToolbar(c, skills)
	default:
		a.exitBatch() // 换了视图、进了集合，选择态就不在了
		a.toolbar(c, scope.Title(), subtitle, nil, func() { a.browseControls(c, nil) })
	}

	a.withPane(c, func(width float32) {
		a.modePane(c, func() {
			switch a.mode {
			case viewBoard:
				a.board(c, scope, skills, width)
			default:
				a.cards(c, scope, skills, width)
			}
		})
	})
}

var modeSpring = ui.Animation{Spring: ui.SpringOptions{Duration: 260 * time.Millisecond}}

// modePane 为「卡片 / 看板」内容区提供方向一致的切换进入过渡；仅配置 Enter、
// 不保留 Exit 副本，避免退场残影影响点击或检索命中。
func (a *app) modePane(c *ui.Context, content func()) {
	shift := float32(-14)
	if a.mode == viewBoard {
		shift = 14
	}
	ui.Column(c.Key(fmt.Sprintf("mode-%d", a.mode))).Grow(1).MinWidth(0).MinHeight(0).
		Transition(ui.ElementTransition{
			Animation: &modeSpring,
			Enter:     &ui.Motion{Opacity: 0, X: shift},
		}).
		Children(content)
}

// browseControls 是浏览页工具栏右边：批量操作、视图、筛选、搜索。sel 不为
// nil 时在选择态：「批量操作」原地换成「退出批量」，鼠标不动再点一下就
// 退出；左边多出删除，右边的控件留在原位、变灰，顶栏不跳。
func (a *app) browseControls(c *ui.Context, sel *batchSelection) {
	compact := a.toolbarCompact(c)
	ui.Spacer(c)
	if sel != nil {
		label := locales.ActionDelete()
		if n := len(sel.skills); n > 0 {
			label = locales.BatchDeleteCount(n)
		}
		if batchDeleteButton(c.Key("batch-delete"), label, len(sel.skills) == 0).Clicked() {
			a.requestDelete(sel.skills...)
		}
	}
	if a.batchOffered() && len(a.lib.Find(library.ScopeMine, a.query)) > 0 {
		if batchToggle(c.Key("batch-toggle"), sel != nil, locales.BatchActions(), compact).Clicked() {
			if sel != nil {
				a.exitBatch()
			} else {
				a.startBatch()
			}
		}
	}
	if sel != nil && compact {
		return // 窄的时候放不下，选择态用不到的控件先收起来
	}
	controls := ui.Row(c.Key("controls")).Shrink(0).Gap(10).AlignItems(ui.Center).Disabled(sel != nil)
	if compact {
		// 只在窄的时候让搜索框跟着缩；宽的时候放得下，左边的说明先省略，
		// 右边的控件一动不动（批量开关要留在指针下）
		controls.Shrink(1).MinWidth(0)
	}
	controls.Children(func() {
		if a.scope == string(library.ScopeMine) && a.inColl == nil {
			add := updButton
			if compact {
				add = updIconButton
			}
			if add(c.Key("add-skill"), iconPlus, locales.ActionAddSkills(), false).Clicked() {
				a.openImport()
			}
		}
		segmented(c.Key("view-mode"), &a.mode, locales.BrowseCards(), locales.BrowseBoard()).Label(locales.BrowseView()).Shrink(0)
		a.filterMenu(c)
		searchBox(c, &a.query)
	})
}

// withPane 放内容区，选中技能时右侧滑出详情栏。content 拿到的是详情栏
// 进出之后内容区的宽度，网格一开始就按它排（cardArea）：只换一次列数，
// 卡片沿 moveTransition 从原来的位置滑到新位置，不会先挤窄再跳。内容区
// 本身跟着详情栏一起变宽变窄，超出的部分裁掉。
func (a *app) withPane(c *ui.Context, content func(width float32)) {
	w, _ := c.Size()
	contentWidth := w - a.sidebarRoom()
	// 这一帧里点击可能改掉选中的，详情栏画开始时的那个
	sel, coll := a.selected, a.coll
	pane := sel != nil || coll != nil
	if pane {
		contentWidth -= inspectorWidth + 1
		a.paneRoom = inspectorWidth + 1
	}
	switch {
	case sel != nil:
		a.anchor = sel.ID
	case coll != nil:
		a.anchor = coll.ID
	}
	ui.Row(c).Grow(1).MinHeight(0).AlignItems(ui.Stretch).Children(func() {
		area := ui.Column(c.Key("content")).Grow(1).MinWidth(0).Clip().Transition(contentTransition)
		area.Children(func() { content(contentWidth) })
		if pane {
			// 从右边推进来、推出去；换选中的技能时不动
			ui.Row(c.Key("pane")).Shrink(0).AlignItems(ui.Stretch).Transition(paneTransition).Children(func() {
				ui.Box(c).Width(1).Background(colBorderDefault)
				a.inspector(c, sel, coll)
			})
		}
	})
}

// 详情栏进出、内容区变宽变窄、卡片换位置是同一个动作：同样的时长和曲线，
// 起步不急，收尾放缓，三者始终贴在一起。
const paneDuration = 300 * time.Millisecond

var (
	paneEase       = ui.CubicBezier(0.3, 0, 0.2, 1)
	paneTransition = ui.ElementTransition{
		Duration: paneDuration,
		Ease:     paneEase,
		Enter:    &ui.Motion{XPercent: 100, Opacity: 1},
		Exit:     &ui.Motion{XPercent: 100, Opacity: 1},
	}
	contentTransition = ui.ElementTransition{Duration: paneDuration, Ease: paneEase, Width: true}
	// moveTransition 让网格和分组在换列时滑到新位置。拖窗口改变的布局
	// 不动画，系统减弱动态效果时也不动。
	moveTransition = ui.ElementTransition{Duration: paneDuration, Ease: paneEase, Position: true}
	// cellMove 是网格里的一张卡片：还在同一行的横着滑过去；换了行的
	// 是另一个元素（cellKey），在原处淡出、新位置淡入，不斜着穿过别的卡片。
	cellMove = ui.ElementTransition{
		Duration:     paneDuration,
		Ease:         paneEase,
		Position:     true,
		Enter:        &ui.Motion{Opacity: 0, Scale: 0.97},
		Exit:         &ui.Motion{Opacity: 0, Scale: 0.97},
		ExitDuration: paneDuration / 2,
	}
)

// cellKey 是网格里一张卡片的身份：同一个技能或集合换到另一行就算新的。
type cellKey struct {
	id  string
	row int
}

// cellMatch 把换了行的卡片和它原来的样子对上（ElementTransition.Match）。
type cellMatch struct{ id string }

// cellTransition 是网格里 id 这张卡片的过渡。用户正看着的那张（指针停在
// 上面，或刚选中、刚取消选中）换了行也从原位置滑过去，不消失再出现。
func (a *app) cellTransition(cell ui.Element, id string) ui.ElementTransition {
	t := cellMove
	if id == a.anchor || cell.Hovered() {
		t.Match, t.Fly = cellMatch{id}, true // 飞在别的卡片上面
	}
	return t
}

// pick 是卡片的点击：单击选中，双击进入阅读视图。
func (a *app) pick(c *ui.Context, card ui.Element, s *library.Skill, menuKey string) {
	a.contextMenu(c, card, menuKey, true, func(fm *frostedMenu) {
		a.skillMenu(fm, s)
	})
	if card.Clicked() {
		if a.selected != s {
			a.err = ""
		}
		a.selected, a.coll, a.picked = s, nil, true
	}
	if card.DoubleClicked() {
		a.open(s)
	}
}

// deselectOnBlank 让点在卡片之外的空白处取消选中。
func (a *app) deselectOnBlank(area ui.Element) {
	if area.Clicked() && !a.picked {
		a.selected, a.coll = nil, nil
	}
	a.picked = false
}

func mineOf(skills []*library.Skill) []*library.Skill { return bySource(skills, library.Mine) }

func bySource(skills []*library.Skill, src library.Source) []*library.Skill {
	var out []*library.Skill
	for _, s := range skills {
		if s.Source == src {
			out = append(out, s)
		}
	}
	return out
}

// section 是卡片里的一组技能。
type section struct {
	key, title string
	sets       []*library.Collection // 收起来的技能集合，排在前面
	setCounts  map[*library.Collection]int
	skills     []*library.Skill // 不在集合里的技能
	rows       int              // 全部技能里折叠时显示几行
	note       func(c *ui.Context)
}

func (sec *section) setCount(coll *library.Collection) int {
	if sec.setCounts != nil {
		return sec.setCounts[coll]
	}
	return len(coll.Skills)
}

// count 是组里的技能数（含集合里的匹配数），有集合时带上集合数。
func (sec *section) count() string {
	n := len(sec.skills)
	for _, coll := range sec.sets {
		n += sec.setCount(coll)
	}
	return countLabel(n, len(sec.sets), false)
}

// sections 把技能分组：我的分成通用和通用之外（本机没有 Agent 读通用目录时
// 不分），然后是插件装的、内置和项目。集合收成一项，放在它所在的组的前面。
// 空的组不在里面。
func (a *app) sections(skills []*library.Skill) []section {
	setCounts := map[*library.Collection]int{}
	for _, s := range skills {
		if s.Collection != nil {
			setCounts[s.Collection]++
		}
	}
	var out []section
	mineSets, mine := a.group(mineOf(skills))
	if readers := a.readers(); len(readers) > 0 {
		var in, outside []*library.Skill
		var inSets, outSets []*library.Collection
		for _, s := range mine {
			if s.Shared {
				in = append(in, s)
			} else {
				outside = append(outside, s)
			}
		}
		for _, coll := range mineSets {
			if coll.Shared() {
				inSets = append(inSets, coll)
			} else {
				outSets = append(outSets, coll)
			}
		}
		out = append(out, section{key: "shared", title: locales.CommonShared(), sets: inSets, setCounts: setCounts, skills: in, rows: 2, note: func(c *ui.Context) {
			ui.Text(c, a.lib.Display(a.lib.Root)).Font("monospace").FontSize(11).Margin(0, 4, 0, 0).SingleLine().Shrink(0)
			agentBadges(c, readers, 14, 8)
			ui.Text(c, locales.BrowseSharedReaders()).Margin(0, 0, 0, 3).SingleLine().Shrink(0)
		}}, section{key: "outside", title: locales.BrowseOutsideShared(), sets: outSets, setCounts: setCounts, skills: outside, rows: 1, note: func(c *ui.Context) {
			text := locales.BrowseAgentDirectoryNote()
			ui.Text(c, text).SingleLine().MinWidth(0)
		}})
	} else {
		out = append(out, section{key: "mine", title: library.Mine.Label(), sets: mineSets, setCounts: setCounts, skills: mine, rows: 2})
	}
	readOnly := func(c *ui.Context) {
		ui.Text(c, locales.CommonReadOnly()).SingleLine()
	}
	for _, src := range []library.Source{library.ViaPlugin, library.Builtin, library.Project} {
		sets, rest := a.group(bySource(skills, src))
		out = append(out, section{key: fmt.Sprint("source", int(src)), title: src.Label(), sets: sets, setCounts: setCounts, skills: rest, rows: 1, note: readOnly})
	}
	return slices.DeleteFunc(out, func(sec section) bool { return len(sec.sets)+len(sec.skills) == 0 })
}

// ——— 卡片 ———

func (a *app) cards(c *ui.Context, scope library.Scope, skills []*library.Skill, width float32) {
	if len(skills) == 0 {
		a.emptyBrowse(c, canAddSkills(scope), locales.CommonNoSkills())
		return
	}
	cols := columnsFor(width)
	sections := a.sections(skills)
	// 不止一组时分节；全部技能里每节先显示一两行
	headed := len(sections) > 1
	var heads []pinHead
	a.cardArea(c, width, func() {
		for _, sec := range sections {
			if h, ok := a.cardSection(c.Key("sec"+sec.key), sec, scope, cols, headed); ok {
				heads = append(heads, h)
			}
		}
	}, func() {
		if headed {
			a.pinnedHeader(c, heads)
		}
	})
}

// cardSection 是卡片里的一组：标题和网格。换列时前一组变高，整组跟着往下滑。
// 有标题时返回顶部标题栏要的这一组（ok），位置是上一帧的。
func (a *app) cardSection(c *ui.Context, sec section, scope library.Scope, cols int, headed bool) (head pinHead, ok bool) {
	group := ui.Column(c).Margin(6, 0, 0, 0).Transition(moveTransition)
	b := group.Bounds()
	group.Children(func() {
		sets, shown := sec.sets, sec.skills
		limit := sec.rows * cols
		folds := headed && scope == library.ScopeAll && len(sets)+len(shown) > limit
		if folds && !a.expanded[sec.key] {
			// 集合先占满整行，剩下的行放技能
			sets = sets[:min(len(sets), limit)]
			setRows := (len(sets) + cols - 1) / cols
			shown = shown[:min(len(shown), max(0, limit-setRows*cols))]
		}
		if headed {
			a.cardSectionHeader(c, sec, folds)
			head, ok = pinHead{sec, folds, b.Y - a.scrolled}, b.H > 0
		}
		if len(sets) > 0 {
			a.setGrid(c.Key("sets"+sec.key), cols, sets, sec.setCounts)
		}
		if len(shown) > 0 {
			a.cardGrid(c.Key("grid"+sec.key), cols, shown, func(s *library.Skill) { a.cardFooter(c, s) })
		}
	})
	return head, ok
}

// cardSectionHeader 是一组卡片的标题：名称、数量、说明，能折叠时右边是
// 「显示全部」。
func (a *app) cardSectionHeader(c *ui.Context, sec section, folds bool) {
	sectionHeader(c.Key(sec.key), sec.title, sec.count(), func() {
		if sec.note != nil {
			sec.note(c)
		}
	}, func() {
		if a.batch.on {
			a.batchSectionToggle(c, sec)
			return
		}
		if !folds {
			return
		}
		label := locales.ActionShowAllChevron()
		if a.expanded[sec.key] {
			label = locales.ShowLess()
		}
		if linkButton(c, label).Clicked() {
			a.expanded[sec.key] = !a.expanded[sec.key]
		}
	})
}

// pinHead 是顶部标题栏能显示的一组：它的标题、能不能折叠，和这一组此刻
// 的顶边（窗口坐标，也是它标题的顶边）：上一帧的位置减去这一帧又滚过的
// 距离，滚得快时也不慢半拍。
type pinHead struct {
	sec   section
	folds bool
	top   float32
}

// pinnedHeader 是卡片区顶上的标题栏：只有一条，滚动时换的是里面的内容，
// 不是两条标题互相顶。第一组的标题滚到顶时它出现，和下面那行标题重合，
// 看不出接缝；滚到下一组时换成那一组：往下滚，旧的往上
// 滑走、新的从下面上来，往上滚反过来，都带一点淡入和虚化。下面的分隔线
// 随着滚过的距离显出来。
func (a *app) pinnedHeader(c *ui.Context, heads []pinHead) {
	// 往下滚，下一组的标题碰到标题栏底边就换，像是它推了进来；往上滚，
	// 那行标题一离开标题栏顶边就换回上一组，它从标题栏下面露出来时上面
	// 已经不是它，不会出现两行一样的标题。
	down, up := -1, -1
	for i, h := range heads {
		if h.top <= a.cardTop+0.5 {
			up = i
		}
		if h.top <= a.cardTop+0.5 || i > 0 && h.top <= a.cardTop+sectionHeaderHeight {
			down = i
		}
	}
	cur := max(up, min(a.pinIdx, down)) // 没在滚（如换列）：夹在两者之间不换
	switch {
	case a.scrolled > 0:
		cur = down
	case a.scrolled < 0:
		cur = up
	}
	if cur < 0 {
		a.pinIdx = -1
		return
	}
	if cur != a.pinIdx {
		a.pinDir = 1
		if cur < a.pinIdx {
			a.pinDir = -1
		}
		a.pinIdx = cur
	}
	line := min(1, max(0, (a.cardTop-heads[0].top)/12))
	dir := a.pinDir
	bar := ui.Row(c).Absolute().Top(0).Left(0).Right(0).Height(sectionHeaderHeight+1).Padding(0, cardPad).
		Background(colBgSurface).BorderWidth(0, 0, 1, 0).BorderColor(colBorderDefault.Alpha(line)).
		Shadow(0, 2, 8, 0, shadow(0.04*line)).Clip()
	bar.ChildExit(ui.Motion{Y: -pinShift * dir, Opacity: 0, Blur: pinBlur})
	bar.Children(func() {
		h := heads[cur]
		ui.Column(c.Key(h.sec.key)).Grow(1).MinWidth(0).Transition(ui.ElementTransition{
			Animation:    &pinSpring,
			Enter:        &ui.Motion{Y: pinShift * dir, Opacity: 0, Blur: pinBlur},
			Exit:         &ui.Motion{Y: -pinShift * dir, Opacity: 0, Blur: pinBlur},
			ExitDuration: pinExit,
		}).Children(func() { a.cardSectionHeader(c, h.sec, h.folds) })
	})
}

// 顶部标题栏换组：内容滑过的距离、途中的虚化。新的跟一根不回弹的弹簧
// 进来，快速来回滚时接着当前的速度走，不会跳；旧的退得快一些，两行字
// 叠在一起的时间短，不糊。
const (
	pinShift = 22
	pinBlur  = 3
	pinExit  = 180 * time.Millisecond
)

var pinSpring = ui.Animation{Spring: ui.SpringOptions{Duration: 380 * time.Millisecond}}

// cardFooter 是卡片底部左边：我的显示启用于哪些 Agent（在通用目录里的
// 收成「通用」），只读的显示来源。
func (a *app) cardFooter(c *ui.Context, s *library.Skill) {
	switch s.Source {
	case library.Builtin, library.ViaPlugin:
		agentBadge(c, s.Provider, 14)
		label := library.AgentName(s.Provider)
		if s.Plugin != "" {
			// 不同插件可能带同名技能，标出插件才分得清
			label += " · " + s.Plugin
		}
		ui.Text(c, label).FontSize(11.5).SingleLine()
	case library.Project:
		ui.Icon(c, iconFolder).Size(12, 12)
		ui.Text(c, s.Project).Font("monospace").FontSize(11).SingleLine()
	default:
		// 每个问题提示占两个图标的宽度，至少留两格
		flagged := 0
		for _, on := range []bool{s.Copies > 0, s.MissingDescription(), len(s.Defects) > 0} {
			if on {
				flagged++
			}
		}
		limit := max(6-2*flagged, 2)
		if s.AllDisabled() {
			ui.Text(c, locales.CommonDisabled()).FontSize(11.5).TextColor(colTextDisabled)
		} else if !a.enabledBadges(c, s, "", 16, limit) {
			ui.Text(c, locales.CommonNotEnabled()).FontSize(11.5).TextColor(colTextDisabled)
		}
	}
}

// ——— 看板 ———

// board 把通用技能单列，其余按 Agent 排列；已停用的技能留在对应 Agent 列底。
func (a *app) board(c *ui.Context, scope library.Scope, skills []*library.Skill, width float32) {
	f := a.currentFilter()
	mine := mineOf(skills)
	if len(mine) == 0 {
		a.emptyBoard(c, scope, len(skills) == 0)
		return
	}
	agents := a.boardAgents(mine)
	var shared, outside, unassigned []*library.Skill
	for _, s := range mine {
		if s.Shared {
			shared = append(shared, s)
			continue
		}
		outside = append(outside, s)
		if len(s.DiscoveredAgents()) == 0 {
			unassigned = append(unassigned, s)
		}
	}
	if f.agent != "" && f.agent != filterUnbound {
		unassigned = nil
	}
	columns := len(agents)
	if len(shared) > 0 {
		columns++
	}
	if len(unassigned) > 0 {
		columns++
	}
	if columns == 0 {
		a.emptyBoard(c, scope, false)
		return
	}
	a.boardRow(c, columns, width, func(colW float32) {
		if len(shared) > 0 {
			boardColumn(c.Key("shared"), colW, func() {
				badge(c, sharedBrand, locales.CommonShared(), 16)
				columnTitle(c, locales.CommonShared(), len(shared))
			}, func() {
				for _, s := range shared {
					a.boardCard(c.Key(s.ID), s, "shared", 1, false, func() { a.cardFooter(c, s) })
				}
			})
		}
		for _, ag := range agents {
			var found []*library.Skill
			for _, s := range outside {
				if s.DiscoveredIn(ag.ID) {
					found = append(found, s)
				}
			}
			a.agentColumn(c.Key(ag.ID), colW, ag, found, outside)
		}
		if len(unassigned) > 0 {
			boardColumn(c.Key("unassigned"), colW, func() { columnTitle(c, locales.CommonNotEnabled(), len(unassigned)) }, func() {
				for _, s := range unassigned {
					a.boardCard(c.Key(s.ID), s, "unassigned", 1, true, func() { flags(c, s) })
				}
			})
		}
	})
}

// Column addition only creates a discovery link; toggles live in each card's menu.
func (a *app) agentColumn(c *ui.Context, width float32, ag library.Agent, skills, candidates []*library.Skill) {
	var add, disabled, active []*library.Skill
	enabledCount := 0
	for _, s := range candidates {
		if a.lib.CanAddToAgent(s, ag.ID) {
			add = append(add, s)
		}
	}
	for _, s := range skills {
		st := s.State(ag.ID)
		if st.Enabled {
			enabledCount++
		}
		if !st.Enabled && st.Kind != library.StateUnknown {
			disabled = append(disabled, s)
		} else {
			active = append(active, s)
		}
	}
	buildAddMenu := func(fm *frostedMenu) {
		if len(add) == 0 {
			fm.action(locales.BrowseAllSkillsAdded(), withDisabled(true))
		}
		for _, s := range add {
			skill := s
			if fm.action(skill.Name, withKey(skill.ID)) {
				a.addToAgent(skill, ag.ID)
			}
		}
	}
	boardColumn(c, width, func() {
		agentBadge(c, ag.ID, 16)
		columnTitle(c, ag.DisplayName(), enabledCount)
		ui.Spacer(c)
		a.triggerMenu(c, "board.col.add:"+ag.ID, menuPanelOpts{alignEnd: true}, func(btn ui.Element, open bool) {
			btn.Size(20, 20).Center().Radius(5).Label(locales.ActionAddTo(ag.DisplayName())).Tooltip(locales.ActionAddSkills())
			styleMenuIconTrigger(btn, open, false)
			btn.Children(func() { ui.Icon(c, iconPlus).Size(15, 15) })
		}, buildAddMenu)
	}, func() {
		for _, s := range active {
			a.nativeBoardCard(c.Key(s.ID), s, ag.ID)
		}
		if len(disabled) > 0 {
			ui.Row(c).Padding(8, 5, 2, 5).Gap(6).Children(func() {
				ui.Text(c, locales.CommonDisabled()).FontSize(11.5).TextColor(colTextMuted)
				ui.Text(c, fmt.Sprint(len(disabled))).FontSize(11.5).TextColor(colTextMuted)
			})
			for _, s := range disabled {
				a.nativeBoardCard(c.Key(s.ID), s, ag.ID)
			}
		}
		if len(skills) == 0 {
			ui.Text(c, locales.BrowseNoOutsideShared()).FontSize(11.5).TextColor(colTextDisabled).Padding(6, 5)
		}
		a.triggerMenu(c, "board.col.bottom:"+ag.ID, menuPanelOpts{}, func(btn ui.Element, open bool) {
			styleDashedButton(c, btn, open, locales.BrowseAddMoreSkills())
		}, buildAddMenu)
	})
}

// ——— 工具栏筛选（画板 sf5-b）———

const filterUnbound = "__unbound__"

type skillFilter struct {
	agent     string
	updatable bool
	unused    bool
	disabled  bool
}

func (f skillFilter) active() bool {
	return f.agent != "" || f.updatable || f.unused || f.disabled
}

func (a *app) currentFilter() *skillFilter {
	if a.filters == nil {
		a.filters = map[string]*skillFilter{}
	}
	f := a.filters[a.scope]
	if f == nil {
		f = &skillFilter{}
		a.filters[a.scope] = f
	}
	return f
}

func (a *app) clearFilters() {
	*a.currentFilter() = skillFilter{}
}

func (a *app) pruneFilterSelection() {
	if !a.currentFilter().active() {
		return
	}
	upd := a.updatableNames()
	if a.selected != nil && !a.filterMatches(a.selected, upd) {
		a.selected = nil
	}
	if a.coll != nil && !slices.ContainsFunc(a.coll.Skills, func(s *library.Skill) bool {
		return a.filterMatches(s, upd)
	}) {
		a.coll = nil
	}
}

func (a *app) updatableNames() map[string]bool {
	if !a.currentFilter().updatable {
		return nil
	}
	out := map[string]bool{}
	for _, it := range a.upd.report.Items {
		if !it.Stale && !it.ReadOnly && (it.State == library.UpdReady || it.State == library.UpdBoth) {
			out[it.Name] = true
		}
	}
	return out
}

func (a *app) applyFilters(skills []*library.Skill) []*library.Skill {
	if !a.currentFilter().active() {
		return skills
	}
	upd := a.updatableNames()
	var out []*library.Skill
	for _, s := range skills {
		if a.filterMatches(s, upd) {
			out = append(out, s)
		}
	}
	return out
}

func (a *app) filterMatches(s *library.Skill, updatable map[string]bool) bool {
	f := a.currentFilter()
	if f.agent == filterUnbound {
		if skillAssignedToAnyAgent(s) {
			return false
		}
	} else if f.agent != "" && !skillMatchesAgent(s, f.agent) {
		return false
	}
	if f.updatable && (s.Source != library.Mine || !updatable[s.Name]) {
		return false
	}
	if f.unused && !a.lib.Unused(s) {
		return false
	}
	if f.disabled && !s.AllDisabled() {
		return false
	}
	return true
}

func skillAssignedToAnyAgent(s *library.Skill) bool {
	return s.AllDisabled() || len(s.Enabled) > 0 || len(s.DiscoveredAgents()) > 0 || len(s.Reads) > 0 || s.Provider != ""
}

func skillMatchesAgent(s *library.Skill, id string) bool {
	if s.DiscoveredIn(id) || s.EnabledIn(id) || s.Provider == id {
		return true
	}
	return slices.ContainsFunc(s.Reads, func(r library.Read) bool { return r.Agent == id })
}

func (a *app) findAgent(id string) (library.Agent, bool) {
	if i := slices.IndexFunc(a.lib.Agents, func(ag library.Agent) bool { return ag.ID == id }); i >= 0 {
		return a.lib.Agents[i], true
	}
	return library.Agent{}, false
}

func (a *app) filterAgents() []library.Agent {
	agents := a.lib.Visible()
	if id := a.currentFilter().agent; id != "" && id != filterUnbound && !slices.ContainsFunc(agents, func(ag library.Agent) bool { return ag.ID == id }) {
		if ag, ok := a.findAgent(id); ok {
			agents = append(agents, ag)
		}
	}
	return agents
}

func (a *app) boardAgents(mine []*library.Skill) []library.Agent {
	f := a.currentFilter()
	if f.agent == filterUnbound {
		return nil
	}
	if f.agent != "" {
		if ag, ok := a.findAgent(f.agent); ok {
			return []library.Agent{ag}
		}
		return nil
	}
	agents := a.lib.Visible()
	if !f.active() {
		return agents
	}
	return slices.DeleteFunc(slices.Clone(agents), func(ag library.Agent) bool {
		return !slices.ContainsFunc(mine, func(s *library.Skill) bool {
			return !s.Shared && s.DiscoveredIn(ag.ID)
		})
	})
}

func (a *app) agentFilterSummary() string {
	switch id := a.currentFilter().agent; id {
	case "":
		return ""
	case filterUnbound:
		return locales.CommonNotEnabled()
	default:
		return library.AgentName(id)
	}
}

func (a *app) filterMenu(c *ui.Context) {
	f := a.currentFilter()
	a.iconActionMenu(c, "browse.filter:"+a.scope, iconFilter, locales.BrowseFilter(), actionIconOpts{
		size: 30, height: 28, active: f.active(), padLead: true, width: 224,
	}, func(fm *frostedMenu) {
		agents := a.filterAgents()
		fm.submenu(locales.BrowseFilterByAgent(), a.agentFilterSummary(), []menuOpt{withIcon(iconAgentGrid)}, menuPanelOpts{width: 192}, func(sub *frostedMenu) {
			for _, ag := range agents {
				id := ag.ID
				selected := f.agent == id
				if sub.check(ag.DisplayName(), selected, withKey(id), withBadge(func(c *ui.Context) { agentBadge(c, id, 16) }), withTrailCheck(), withActiveBg()) {
					if selected {
						f.agent = ""
					} else {
						f.agent = id
					}
					a.pruneFilterSelection()
				}
			}
			sub.separator()
			unboundSelected := f.agent == filterUnbound
			if sub.check(locales.CommonNotEnabled(), unboundSelected, withIcon(iconUnbound), withTrailCheck(), withActiveBg()) {
				if unboundSelected {
					f.agent = ""
				} else {
					f.agent = filterUnbound
				}
				a.pruneFilterSelection()
			}
		})
		if fm.check(locales.BrowseHasUpdate(), f.updatable, withIcon(iconRefresh), withTrailCheck()) {
			f.updatable = !f.updatable
			a.pruneFilterSelection()
		}
		if fm.check(locales.BrowseNeverUsed(), f.unused, withIcon(iconClock), withTrailCheck(), withDisabled(!a.lib.HasUsage())) {
			f.unused = !f.unused
			a.pruneFilterSelection()
		}
		if fm.check(locales.CommonDisabled(), f.disabled, withIcon(iconOff), withTrailCheck()) {
			f.disabled = !f.disabled
			a.pruneFilterSelection()
		}
		fm.separator()
		if fm.action(locales.ActionClearFilters(), withIcon(iconClose), withMuted(), withDisabled(!f.active())) {
			a.clearFilters()
		}
	})
}
