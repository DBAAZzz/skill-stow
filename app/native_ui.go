package main

import (
	"github.com/egoist/mygo/ui"
	"skillmanager/internal/library"
	"skillmanager/locales"
	"strings"
)

type enableConfirmation struct {
	skill           *library.Skill
	agents, impacts []string
	on              bool
}

func (a *app) controllableAgents(s *library.Skill) []string {
	var out []string
	for _, ag := range a.lib.Visible() {
		if s.State(ag.ID).Controllable() {
			out = append(out, ag.ID)
		}
	}
	return out
}
func (a *app) requestEnable(s *library.Skill, ids []string, on bool) {
	var impacts []string
	if !on {
		for _, id := range ids {
			if !s.State(id).Controllable() {
				continue
			}
			for _, p := range a.lib.NameImpacts(s, id) {
				impacts = append(impacts, library.AgentName(id)+" · "+a.lib.Display(p))
			}
		}
	}
	if len(impacts) > 0 {
		a.enableConfirm = &enableConfirmation{s, ids, impacts, on}
		a.confirmOpen = true
		a.changed()
		return
	}
	for _, id := range ids {
		a.enable(s, id, on)
	}
}
func (a *app) enableDialog(c *ui.Context) {
	p := a.enableConfirm
	if p == nil {
		return
	}
	dialog(c, &a.confirmOpen, locales.NativeDisableTitle(p.skill.Name), locales.InspectorDisableCopiesMessage(), func() {
		ui.Scroll(c).MaxHeight(220).Margin(12, 0, 0, 0).Children(func() {
			ui.Column(c).Gap(6).Children(func() {
				for _, path := range p.impacts {
					ui.Text(c, path).FontSize(12).TextColor(colTextSecondary)
				}
			})
		})
	}, func() {
		if dialogButton(c, locales.ActionCancel(), false).Clicked() {
			a.confirmOpen = false
		}
		if dialogButton(c, locales.InspectorDisableTogether(), true).Clicked() {
			a.confirmOpen = false
			for _, id := range p.agents {
				a.enable(p.skill, id, p.on)
			}
		}
	})
}
func (a *app) addToAgent(s *library.Skill, id string) {
	a.err, a.errAgent = "", ""
	if err := a.lib.AddToAgent(s, id); err != nil {
		a.err = err.Error()
	}
	a.changed()
}
func (a *app) nativeTips(c *ui.Context, s *library.Skill) {
	var tips []library.Agent
	for _, ag := range a.lib.Visible() {
		if st := s.State(ag.ID); st.Discoverable && st.Message != "" {
			tips = append(tips, ag)
		}
	}
	if len(tips) == 0 {
		return
	}
	// 高度跟着提示条数走，不留空白
	ui.Column(c.Key("native-tips")).Shrink(0).Margin(8, 0, 0, 0).Radius(10).Background(colBgSubtle).Children(func() {
		ui.Column(c).Padding(4, 0).Children(func() {
			for _, ag := range tips {
				st := s.State(ag.ID)
				ui.Row(c.Key(ag.ID)).Padding(8, 12).Gap(10).AlignItems(ui.Start).Children(func() {
					agentBadge(c, ag.ID, 18)
					ui.Text(c, st.Message).FontSize(12).LineHeight(1.5).TextColor(colTextSecondary).Grow(1).MinWidth(0)
					// 操作单独一列在右边，竖直居中，不和原因挤在一起（en4-a 评论）
					switch st.Kind {
					case library.StateFailed:
						if tipAction(c, locales.ActionRetry()).Clicked() {
							a.requestEnable(s, []string{ag.ID}, st.RetryOn)
						}
					case library.StateUnknown:
						if tipAction(c, locales.ActionRetry()).Clicked() {
							a.rescan()
						}
					}
				})
			}
		})
	})
}

// tipAction 是提示右边的操作：单独一列，竖直居中，不换行。
func tipAction(c *ui.Context, label string) ui.Element {
	return linkButton(c, label).AlignSelf(ui.Center).Shrink(0).Margin(0, 0, 0, 6)
}
func stateBadge(c *ui.Context, s *library.Skill, id string, size float32) {
	st := s.State(id)
	wrap := ui.Box(c).Size(size, size).Shrink(0).Tooltip(library.AgentName(id) + " · " + st.Kind.Label())
	if st.Kind == library.StateUnknown {
		wrap.Border(1, colTextMuted).BorderStyle(ui.BorderDashed).Radius(4).Padding(2).Size(size+4, size+4)
	}
	wrap.Children(func() {
		if !st.Enabled && st.Kind != library.StateUnknown {
			br := brands[id]
			br.color = false
			br.bg = colControlTrack
			br.fg = white
			br.gradient = [2]ui.Color{}
			badge(c, br, library.AgentName(id), size)
			ui.Icon(c, iconSlash).Absolute().Top(0).Left(0).Size(size, size).TextColor(colTextMuted)
		} else {
			agentBadge(c, id, size)
		}
		if st.Kind == library.StateRestart {
			ui.Box(c).Absolute().Top(-2).Right(-2).Size(5, 5).Radius(3).Background(colWarnText)
		}
	})
}
func (a *app) nativeBoardCard(c *ui.Context, s *library.Skill, id string) {
	st := s.State(id)
	card := ui.Column(c).Padding(8, 10, 9, 10).Radius(8).Background(colBgCard).Cursor(ui.CursorPointer).Label(s.Name)
	hairline(card, 0.09)
	if !st.Enabled && st.Kind != library.StateUnknown {
		card.Background(colBgSubtle).Opacity(0.65)
	}
	if a.selected == s {
		card.Shadow(0, 0, 0, 2, colAccent)
	}
	a.pick(c, card, s, "board.card:"+id+":"+s.ID)
	card.Children(func() {
		ui.Row(c).Gap(4).AlignItems(ui.Center).Children(func() {
			ui.Text(c, s.Name).FontSize(13).FontWeight(500).Grow(1).MinWidth(0).SingleLine()
			a.triggerMenu(c, "board.card.more:"+id+":"+s.ID, menuPanelOpts{alignEnd: true}, func(more ui.Element, open bool) {
				more.Size(20, 18).Center().Radius(4).Label(locales.NativeCardActions(library.AgentName(id), s.Name))
				styleMenuIconTrigger(more, open, false)
				if !card.Hovered() && !more.Hovered() && !open {
					more.Opacity(0)
				}
				more.Children(func() { ui.Icon(c, iconMore).Size(15, 15) })
			}, func(fm *frostedMenu) {
				label := locales.NativeDisableIn(library.AgentName(id))
				if !st.Enabled {
					label = locales.NativeEnableIn(library.AgentName(id))
				}
				if fm.action(label, withDisabled(!st.Controllable())) {
					a.requestEnable(s, []string{id}, !st.Enabled)
				}
				for _, other := range s.DiscoveredAgents() {
					if other == id {
						continue
					}
					os := s.State(other)
					fm.action(library.AgentName(other)+" · "+os.Kind.Label(), withDisabled(true))
				}
			})
		})
		descText(c, s).FontSize(12).SingleLine()
		ui.Row(c).Height(16).Margin(7, 0, 0, 0).Gap(4).AlignItems(ui.Center).Children(func() { a.enabledBadges(c, s, id, 13, 6); ui.Spacer(c); flags(c, s) })
		if len(st.Projects) > 0 {
			ui.Text(c, locales.NativeStillDisabledIn(strings.Join(st.Projects, locales.ListSeparator()))).FontSize(11).TextColor(colTextMuted)
		} else if st.Kind == library.StateRestart {
			ui.Text(c, locales.NativeRestartNeeded(library.AgentName(id))).FontSize(11).TextColor(colWarnText)
		}
	})
}
