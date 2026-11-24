package main

import (
	"fmt"
	"slices"
	"time"

	"github.com/egoist/mygo/ui"

	"skillmanager/internal/library"
	"skillmanager/locales"
)

// 技能集合（c1）：一起装上的一组技能在卡片里收成一项，选中后
// 详情栏的开关对整组生效，双击打开看里面的技能。

// grouping 报告浏览页把集合收起来：搜索时、打开集合后、需要处理里不收。
func (a *app) grouping() bool {
	switch library.Scope(a.scope) {
	case library.ScopeAll, library.ScopeMine, library.ScopeBuiltin:
		return a.query == "" && a.inColl == nil
	}
	return false
}

// group 把属于集合的技能收起来：返回集合（大的在前）和剩下的技能。
func (a *app) group(skills []*library.Skill) (sets []*library.Collection, rest []*library.Skill) {
	if !a.grouping() {
		return nil, skills
	}
	seen := map[*library.Collection]bool{}
	for _, s := range skills {
		if s.Collection != nil {
			seen[s.Collection] = true
		} else {
			rest = append(rest, s)
		}
	}
	for _, c := range a.lib.Collections {
		if seen[c] {
			sets = append(sets, c)
		}
	}
	return sets, rest
}

// pickSet 是集合卡片的点击：单击选中，双击打开。
func (a *app) pickSet(card ui.Element, coll *library.Collection) {
	if card.Clicked() {
		if a.coll != coll {
			a.err = ""
		}
		a.coll, a.selected, a.picked = coll, nil, true
	}
	if card.DoubleClicked() {
		a.openSet(coll)
	}
}

// openSet 打开集合：浏览页只列它的技能，Esc 或返回按钮回来。
func (a *app) openSet(coll *library.Collection) {
	a.navigate(func() { a.inColl, a.coll, a.selected, a.err = coll, nil, nil, "" })
}

// closeSet 从集合回到浏览页，集合仍然选中。
func (a *app) closeSet() {
	a.navigateUp(func() { a.coll, a.selected, a.inColl = a.inColl, nil, nil })
}

// ——— 卡片 ———

// setGrid 是一组集合的叠卡。
func (a *app) setGrid(c *ui.Context, cols int, sets []*library.Collection, setCounts map[*library.Collection]int) {
	ui.Grid(c).Columns(cols).Gap(cardGap).Margin(0, 0, 10, 0).Transition(moveTransition).Children(func() {
		for i, coll := range sets {
			matched := len(coll.Skills)
			if setCounts != nil {
				matched = setCounts[coll]
			}
			a.setCard(c.Key(cellKey{coll.ID, i / cols}), coll, matched)
		}
	})
}

// setCard 是集合的叠卡（c1-a .stack）：下面露出两层，名称旁是技能数，
// 简介的位置列出前几个技能，底部是整组启用于哪些 Agent。
func (a *app) setCard(c *ui.Context, coll *library.Collection, matched int) {
	stack := ui.Column(c).Padding(0, 0, 12, 0)
	stack.Transition(a.cellTransition(stack, coll.ID))
	batch := a.batch.on
	canPick := true
	if batch {
		_, n := a.pickState(coll.Skills)
		canPick = n > 0
	}
	// 鼠标移上去时轻轻展开：卡片抬起一点，下面两层往下错开（批量里选不上的保持静止）
	var target float32
	if stack.Hovered() && canPick {
		target = 1
	}
	spread := stack.Animate("spread", target, 180*time.Millisecond)
	stack.Children(func() {
		layer := func(inset, bottom float32, bg ui.Color, edge float32) {
			box := ui.Box(c).Absolute().ZIndex(-1).Left(inset).Right(inset).Bottom(bottom).Height(40).Radius(10).
				Background(bg)
			hairline(box, edge)
			lift(box, 1, 2, 0.06)
		}
		layer(14-2*spread, -4*spread, colBgSubtle, 0.17)
		layer(7-1*spread, 6-1.5*spread, colBgSubtle, 0.2)

		card := ui.Column(c).Padding(12, 14).Radius(10).Cursor(ui.CursorPointer).Label(coll.Name).Translate(0, -1.5*spread).
			Transition(cardFeel())
		a.noteSlot(card) // 开场的演示卡片往这里落
		switch {
		case a.coll == coll:
			card.Background(colBgCard).Shadow(0, 0, 0, 2, colAccent)
		case coll.ReadOnly():
			card.Background(colBgSubtle)
			hairline(card, 0.1)
		default:
			card.Background(colBgCard)
			hairline(card, 0.12)
			lift(card, 1, 3, 0.05)
		}
		if a.coll != coll && card.Hovered() && canPick {
			hairline(card, 0.14)
			lift(card, 2, 6, 0.06)
		}
		var batchOn, batchPart, batchCan bool
		if batch {
			batchOn, batchPart, batchCan = a.batchSet(card, coll)
			batchCardStyle(stack, card, batchOn, batchPart, batchCan, locales.CollectionCopiesNeedMerging())
		} else {
			a.pickSet(card, coll)
		}
		card.Children(func() {
			if batch {
				batchCheck(c, batchOn, batchPart, batchCan)
			}
			title := ui.Row(c).Height(28).Gap(8).AlignItems(ui.Center)
			if batch {
				title.Padding(0, 24, 0, 0)
			}
			title.Children(func() {
				// 来源集合显示来源头像（src2-b 实装迭代），插件和文件夹
				// 集合还是叠卡图标；头像没抓到时也先回到叠卡图标。
				av := a.srcIcons[a.collectionIconKey(coll)]
				if av != nil {
					b := ui.Box(c).Size(26, 26).Radius(7).Clip().Background(colBgLogo).Shrink(0).
						Label(locales.SourceAvatar(coll.Name))
					hairline(b, 0.16)
					b.Children(func() {
						ui.Image(c, av).Size(26, 26).Fit(ui.Cover)
					})
				} else {
					setIcon(c, 15, 13)
				}
				ui.Text(c, coll.Name).FontSize(13).FontWeight(600).SingleLine().Shrink(1).MinWidth(0)
				ui.Spacer(c)
				countPill(c, matched, len(coll.Skills))
			})
			ui.Column(c).Height(40).Margin(4, 0, 0, 0).Children(func() {
				ui.Text(c, coll.Preview(6)).FontSize(13).FixedLineHeight(20).MaxLines(2).TextColor(colTextSecondary)
			})
			// 指针停在能卸载的插件集合上时，底栏淡出，换成和技能卡片一样浮上来的操作
			shown := float32(0)
			if card.Hovered() && !batch && a.canUninstallSet(coll) {
				shown = 1
			}
			t := card.Animate("actions", shown, actionsFade)
			ui.Row(c).Height(18).Margin(10, 0, 0, 0).Children(func() {
				ui.Row(c).Grow(1).MinWidth(0).Gap(5).AlignItems(ui.Center).TextColor(colTextMuted).Opacity(max(0, 1-2*t)).Children(func() {
					a.setFooter(c, coll, 16, 3)
				})
				if t > 0 || shown > 0 {
					ui.Row(c).Absolute().Top(0).Left(0).Right(0).Height(18).Gap(4).AlignItems(ui.Center).
						Opacity(t).Translate(0, (1-t)*actionsRise).Children(func() { a.setCardActions(c, coll) })
				}
			})
		})
	})
}

// countPill 是集合名称旁的技能数，筛选态只显示一部分时展示 matched/total。
func countPill(c *ui.Context, matched, total int) {
	text := fmt.Sprint(total)
	if matched < total {
		text = fmt.Sprintf("%d/%d", matched, total)
	}
	ui.Box(c).Height(16).Padding(0, 6).Radius(8).Background(colBgFill).Center().Shrink(0).Children(func() {
		ui.Text(c, text).FontSize(11).FontWeight(500).TextColor(colTextSecondary).FontFeatures("tnum").SingleLine()
	})
}

// setFooter 是集合启用于哪些 Agent：都在通用目录里的收成「通用」，后面是
// 单独启用的 Agent，只启用了一部分的带上「2/3」。插件带的显示提供方。
func (a *app) setFooter(c *ui.Context, coll *library.Collection, size float32, limit int) {
	if coll.ReadOnly() {
		agentBadge(c, coll.Provider, 14)
		ui.Text(c, library.AgentName(coll.Provider)).FontSize(12).SingleLine()
		return
	}
	readers := a.readers()
	tag := coll.Shared() && len(readers) > 0
	if tag {
		sharedTag(c, readers, size)
	}
	if !a.setFractions(c, coll, size, limit) && !tag {
		ui.Text(c, locales.CommonNotEnabled()).FontSize(12).TextColor(colTextDisabled)
	}
}

// setFractions 是集合单独启用于哪些 Agent，只启用了一部分的带上「2/3」，
// 多于 limit 个收成「+N」。一个都没有时返回 false。
func (a *app) setFractions(c *ui.Context, coll *library.Collection, size float32, limit int) bool {
	var ids []string
	for _, ag := range a.setAgents(coll) {
		if coll.EnabledIn(ag.ID) > 0 {
			ids = append(ids, ag.ID)
		}
	}
	if coll.ReadOnly() || len(ids) == 0 {
		return false
	}
	shown := ids
	if len(ids) > limit {
		shown = ids[:limit-1]
	}
	ui.Row(c).Gap(3).AlignItems(ui.Center).Children(func() {
		for _, id := range shown {
			ui.Row(c.Key(id)).AlignItems(ui.Center).Children(func() {
				agentBadge(c, id, size)
				if k, n := coll.EnabledIn(id), len(coll.Skills); k < n {
					ui.Text(c, fmt.Sprintf("%d/%d", k, n)).FontSize(11).TextColor(colTextMuted).FontFeatures("tnum").Margin(0, 3, 0, 3).SingleLine()
				}
			})
		}
		if rest := len(ids) - len(shown); rest > 0 {
			ui.Text(c, fmt.Sprintf("+%d", rest)).FontSize(wholePt(size*0.72)).TextColor(colTextMuted).FontFeatures("tnum").Margin(0, 0, 0, 2)
		}
	})
	return true
}

// setAgents 是集合有开关的 Agent：都在通用目录里时去掉直接读通用目录的。
func (a *app) setAgents(coll *library.Collection) []library.Agent {
	agents := a.lib.Visible()
	if coll.Shared() && len(a.readers()) > 0 {
		agents = slices.DeleteFunc(agents, func(ag library.Agent) bool { return a.lib.ReadsShared(ag.ID) })
	}
	return agents
}

// ——— 打开的集合（c1-b）———

// setPage 是打开的集合：工具栏是返回和面包屑，下面是集合里的技能。
func (a *app) setPage(c *ui.Context, coll *library.Collection) {
	a.pruneFilterSelection()
	skills := a.applyFilters(library.Filter(coll.Skills, a.query))
	a.toolbarRow(c, func() {
		back := iconButton(c, iconBack, locales.ActionBackShortcut()).Size(26, 24).Margin(0, -4, 0, -6)
		if back.Clicked() {
			a.closeSet()
		}
		ui.Row(c).Shrink(1).MinWidth(0).Gap(8).AlignItems(ui.Baseline).Children(func() {
			ui.Row(c).Shrink(0).Gap(4).AlignItems(ui.Baseline).Children(func() {
				crumb := ui.ButtonBase(c).Label(library.Scope(a.scope).Title()).Cursor(ui.CursorPointer)
				if crumb.Clicked() {
					a.closeSet()
				}
				crumb.Children(func() {
					ui.Text(c, library.Scope(a.scope).Title()).FontSize(13).SingleLine().
						TextColor(tint(crumb, "text", colTextMuted, colTextPrimary, colTextPrimary))
				})
				ui.Text(c, "›").FontSize(13).TextColor(colTextDisabled)
				ui.Text(c, coll.Name).FontSize(13).FontWeight(600).SingleLine()
			})
			ui.Row(c).Gap(8).AlignItems(ui.Center).MinWidth(0).Shrink(1).FontSize(12).TextColor(colTextMuted).Children(func() {
				setSubtitle(c, coll, len(skills))
			})
		})
	}, func() { a.browseControls(c, nil) })
	a.withPane(c, func(width float32) {
		a.modePane(c, func() {
			switch {
			case len(skills) == 0:
				a.emptyBrowse(c, false, locales.CommonNoSkills())
			case a.mode == viewBoard:
				a.board(c, library.Scope(a.scope), skills, width)
			default:
				a.cardArea(c, width, func() {
					ui.Box(c).Height(10)
					a.cardGrid(c, columnsFor(width), skills, func(s *library.Skill) { a.cardFooter(c, s) })
				}, nil)
			}
		})
	})
}

// setSubtitle 是打开的集合标题旁的说明：几个技能，再加上从哪来、是否只读
// 或都在通用里，各自一段。
func setSubtitle(c *ui.Context, coll *library.Collection, shown int) {
	count := locales.CommonSkillCount(len(coll.Skills))
	if shown < len(coll.Skills) {
		count = locales.CollectionVisibleSkills(shown, len(coll.Skills))
	}
	ui.Text(c, count).FontFeatures("tnum").SingleLine().Shrink(0)
	switch {
	case viaPlugin(coll):
		ui.Text(c, locales.CollectionViaPlugin(library.AgentName(coll.Provider))).SingleLine().MinWidth(0).Shrink(1)
		readOnlyTag(c)
	case coll.ReadOnly():
		ui.Text(c, locales.OriginBuiltin(library.AgentName(coll.Provider))).SingleLine().MinWidth(0).Shrink(1)
		readOnlyTag(c)
	case coll.Shared():
		ui.Text(c, locales.CollectionAllShared()).SingleLine().MinWidth(0).Shrink(1)
	}
}

// dependNote 是打开的集合里，被好几个技能依赖的那个（简介里提到它）。
func (a *app) dependNote(c *ui.Context, s *library.Skill) {
	if a.inColl == nil {
		return
	}
	if n := a.inColl.DependedBy(s); n >= 2 {
		ui.Text(c, locales.CollectionDependents(n)).FontSize(11).TextColor(colTextMuted).SingleLine().Shrink(0)
	}
}

// ——— 详情栏（c1-c）———

// setIcon 是集合名称前的叠卡图标。行里居中对的是整行字高，而集合名多是
// 小写英文，字身落在行中线以下，图标看着偏上；按字号往下挪一点，对准大写
// 字母和小写字母字身的中间，名称是中文时也不显得偏低。
func setIcon(c *ui.Context, size, fontSize float32) {
	ui.Icon(c, iconStack).Size(size, size).TextColor(colAccent).Shrink(0).Translate(0, fontSize*opticalDrop)
}

// opticalDrop 是标题前图标按字号往下挪的比例，量截图得出。
const opticalDrop = 0.06

// setPane 是选中集合时的详情栏：整组的开关、包含哪些技能、从哪里来。
func (a *app) setPane(c *ui.Context, coll *library.Collection) {
	ui.Row(c).Gap(7).AlignItems(ui.Center).Children(func() {
		setIcon(c, 18, 18)
		ui.Text(c, coll.Name).FontSize(18).FontWeight(600).LetterSpacing(-0.2).MinWidth(0)
	})
	// 标题下不再写「集合、几个技能、哪天装的」：图标说明是集合，「包含」写了
	// 几个，安装日期在「来源」里

	if coll.ReadOnly() {
		label(c, locales.CommonEnabledFor())
		src := library.Builtin
		if viaPlugin(coll) {
			src = library.ViaPlugin
		}
		a.pluginReadOnlyNote(c, coll.Skills[0], src, coll.Provider, coll.Name)
	} else {
		a.setSwitches(c, coll)
	}

	label(c, locales.CollectionContains())
	box(c, func() {
		ui.Column(c).Padding(5, 0).Children(func() {
			for _, s := range coll.Skills[:min(4, len(coll.Skills))] {
				row := ui.ButtonBase(c.Key(s.ID)).Height(26).Padding(0, 12).Justify(ui.Start).AlignItems(ui.Center).Label(s.Name)
				tintInk(row)
				if row.Clicked() {
					a.selected, a.coll, a.err = s, nil, ""
				}
				row.Children(func() { ui.Text(c, s.Name).SingleLine() })
			}
		})
		ui.Row(c).Padding(9, 12).BorderWidth(1, 0, 0, 0).BorderColor(colBorderSubtle).Children(func() {
			if linkButton(c, locales.CollectionOpenAll(len(coll.Skills))).Clicked() {
				a.openSet(coll)
			}
		})
	})

	label(c, locales.CommonSource())
	box(c, func() {
		ui.Column(c).Padding(10, 12).Gap(3).Children(func() {
			name, how := coll.Name, ""
			switch coll.Kind {
			case library.SetSource:
				how = locales.CollectionInstalledWithNpx()
			case library.SetFolder:
				name, how = a.lib.Display(coll.Dir), locales.CollectionLinkedFromFolder()
			case library.SetPlugin:
				how = locales.CollectionAgentPlugin(library.AgentName(coll.Provider))
			}
			ui.Row(c).Gap(8).AlignItems(ui.Baseline).Children(func() {
				ui.Text(c, name).FontSize(13).FontWeight(500).SingleLine().MinWidth(0).Shrink(1).Selectable()
				if !coll.Installed.IsZero() {
					ui.Spacer(c)
					ui.Text(c, installedOn(coll.Installed, time.Now())).FontSize(12).TextColor(colTextMuted).FontFeatures("tnum").SingleLine().Shrink(0)
				}
			})
			ui.Text(c, how).Font("monospace").FontSize(11).TextColor(colTextMuted).SingleLine()
		})
	})
	if coll.Kind == library.SetFolder {
		ui.Row(c).Margin(8, 2, 0, 2).Children(func() {
			if linkButton(c, locales.ActionShowInFinder()).Clicked() {
				a.reveal(coll.Dir)
			}
		})
	}
}

// setSwitches shows per-Agent counts; collection-wide changes are not available in phase one.
func (a *app) setSwitches(c *ui.Context, coll *library.Collection) {
	label(c, locales.CommonEnablement())
	box(c, func() {
		for i, ag := range a.lib.Visible() {
			boxRow(c.Key(ag.ID), i == 0).MinHeight(42).Children(func() {
				agentBadge(c, ag.ID, 18)
				ui.Text(c, ag.DisplayName()).FontWeight(500).Grow(1)
				ui.Text(c, locales.CollectionEnabledCount(coll.EnabledIn(ag.ID), len(coll.Skills))).FontSize(12).TextColor(colTextMuted)
			})
		}
	})
	caption(c, locales.CollectionToggleEachSkill())
}

// installedOn 是「9 月 21 日安装」，不是今年的带上年份。
func installedOn(t, now time.Time) string {
	return locales.InstalledOnDate(monthDay(t.Local(), now))
}
