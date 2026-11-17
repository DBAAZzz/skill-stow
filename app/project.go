package main

import (
	"fmt"
	"slices"

	"github.com/egoist/mygo/ui"

	"skillmanager/internal/library"
	"skillmanager/locales"
)

// projectPage 是一个项目的技能（p1-b / p3-b）：卡片，或按技能目录
// 分列的看板。项目技能只读；有技能被全局链接时项目名旁边有感叹号。
func (a *app) projectPage(c *ui.Context, p *library.ProjectInfo) {
	skills := library.Filter(p.Skills, a.query)
	a.toolbar(c, p.Name, func() {
		ui.Text(c, locales.CommonSkillCount(len(p.Skills))).FontFeatures("tnum").SingleLine()
		readOnlyTag(c)
	}, func() {
		if n := len(p.Linked()); n > 0 && alertButton(c, locales.ProjectGlobalLinksHint(n)).Clicked() {
			a.showLinked(p)
		}
	}, func() {
		ui.Spacer(c)
		segmented(c.Key("view-mode"), &a.mode, locales.BrowseCards(), locales.BrowseBoard()).Label(locales.BrowseView())
		if iconButton(c, iconFolder, locales.ActionShowInFinder()).Clicked() {
			a.reveal(p.Dir)
		}
		searchBox(c, &a.query)
	})
	a.withPane(c, func(width float32) {
		a.modePane(c, func() {
			switch {
			case len(p.Skills) == 0:
				empty(c, locales.ProjectNoSkills())
			case len(skills) == 0:
				empty(c, locales.CommonNoSkills())
			case a.mode == viewBoard:
				a.projectBoard(c, p)
			default:
				a.cardArea(c, width, func() {
					sectionHeader(c, locales.ProjectSkills(), fmt.Sprint(len(skills)), 0, func() {
						if a.selected == nil {
							ui.Text(c, locales.ProjectReadersHint()).SingleLine()
						}
					}, nil)
					a.cardGrid(c, columnsFor(width), skills, func(s *library.Skill) {
						projectReaders(c, s, 16, 6)
					})
				}, nil)
			}
		})
	})
}

// projectBoard 是项目的看板：每个有技能的技能目录一列。同一份技能
// 经链接出现在几个目录里，就在几列里都有。
func (a *app) projectBoard(c *ui.Context, p *library.ProjectInfo) {
	a.fitBoard(c, func() {
		for _, d := range p.Dirs {
			skills := library.Filter(d.Skills, a.query)
			fitColumn(c.Key(d.Rel), func() {
				dirBadge(c, d.Rel, 16)
				columnTitle(c, library.ProjectDirName(d.Rel), len(skills))
				ui.Spacer(c)
				ui.Text(c, d.Rel).Font("monospace").FontSize(11).TextColor(colTextMuted).SingleLine()
			}, func() {
				for _, s := range skills {
					a.boardCard(c.Key(s.ID), s, "project:"+d.Rel, 2, false, func() { flags(c, s) })
				}
			})
		}
	})
}

// alertButton 是标题旁的橙色感叹号。
func alertButton(c *ui.Context, label string) ui.Element {
	b := ui.ButtonBase(c).Size(14, 14).Radius(7).Center().TextColor(white).
		Cursor(ui.CursorPointer).Label(label).Tooltip(label)
	tintSolid(b, colWarn)
	b.Children(func() { ui.Text(c, "!").FontSize(10).FontWeight(700).SingleLine() })
	return b
}

// showLinked 打开全局链接的弹窗（p3-a）。
func (a *app) showLinked(p *library.ProjectInfo) {
	a.linked, a.linkedOpen = p, true
}

// linkedDialog 列出项目里被全局链接的技能，每行右边是会读到它的 Agent。
func (a *app) linkedDialog(c *ui.Context) {
	p := a.linked
	if p == nil {
		return
	}
	linked := p.Linked()
	if len(linked) == 0 {
		a.linkedOpen = false // 都处理完了
		return
	}
	var agents []string
	links := 0
	for _, s := range linked {
		links += len(s.Enabled)
		for _, id := range s.Enabled {
			if name := library.AgentName(id); !slices.Contains(agents, name) {
				agents = append(agents, name)
			}
		}
	}
	title := locales.ProjectLinkedSkills(len(linked), p.Name)
	body := locales.ProjectGlobalLinksDescription(joinNames(agents), p.Name)
	dialog(c, &a.linkedOpen, title, body, func() {
		ui.Column(c).Margin(16, 0, 0, 0).Padding(4, 0).Radius(10).Background(colBgSubtle).Children(func() {
			for i, s := range linked {
				row := ui.Row(c.Key(s.ID)).Height(42).Padding(0, 16).Gap(12).AlignItems(ui.Center)
				if i > 0 {
					row.BorderWidth(1, 0, 0, 0).BorderColor(colBorderDefault)
				}
				row.Children(func() {
					ui.Text(c, s.Name).FontWeight(500).SingleLine().Shrink(1).MinWidth(0)
					ui.Spacer(c)
					agentChips(c, s.Enabled)
					a.globalActions(c, s, linkButton)
				})
			}
		})
		ui.Text(c, locales.ProjectLinkChoices(links, a.lib.Display(a.lib.Root))).FontSize(12).LineHeight(1.55).TextColor(colTextMuted).Margin(12, 0, 0, 0)
	}, func() {
		if dialogButton(c, locales.ActionShowInFinder(), false).Clicked() {
			a.reveal(p.Dir)
		}
		if dialogButton(c, locales.ActionNotNow(), true).Clicked() {
			a.linkedOpen = false
		}
	})
}

// joinNames 把名称连成「A、B 和 C」。
func joinNames(names []string) string {
	return library.JoinAnd(names)
}
