package main

import (
	"github.com/egoist/mygo/ui"

	"skillmanager/internal/library"
	"skillmanager/locales"
)

// globalActions 是被全局链接的项目技能的处理按钮（整理页表格、全局链接弹窗共用）。
func (a *app) globalActions(c *ui.Context, s *library.Skill, button func(*ui.Context, string) ui.Element) {
	if button(c, locales.CollectKeepLinks()).Disabled(a.tidy.busy() || a.tidy.scanning).Tooltip(locales.CollectKeepLinksHint()).Clicked() {
		a.tidyProject(s, keepProjectLinks)
	}
	if button(c, locales.CollectIntoMySkills()).Disabled(a.tidy.busy() || a.tidy.scanning).Clicked() {
		a.collect, a.linkedOpen, a.collectOpen = s, false, true
	}
}

// collectDialog 确认把项目技能收进技能库：写清复制到哪、哪些链接改指向、
// 会多出哪些 Agent 读到。
func (a *app) collectDialog(c *ui.Context) {
	s := a.collect
	if s == nil {
		return
	}
	body := locales.CollectCopyDescription(a.lib.Display(a.lib.CollectTarget(s)), len(s.GlobalDirs()), s.Project)
	dialog(c, &a.collectOpen, locales.CollectTitle(s.Name), body, func() {
		ui.Column(c).Margin(12, 0, 0, 0).Gap(8).Children(func() {
			ui.Text(c, locales.CollectCopiesIndependent()).
				FontSize(13).LineHeight(1.55).TextColor(colWarnText)
			if breaks := a.lib.CollectBreaks(s); len(breaks) > 0 {
				ui.Text(c, locales.CollectBreaksWarning()).FontSize(13).LineHeight(1.55).TextColor(colWarnText)
				for _, ref := range breaks {
					pathText(c, ref)
				}
			}
			if gains := a.lib.CollectGains(s); len(gains) > 0 {
				ui.Text(c, locales.CollectAdditionalReaders()).FontSize(12).TextColor(colTextSecondary)
				agentChips(c, gains)
				ui.Text(c, locales.CollectDisableAfterward()).FontSize(12).TextColor(colTextMuted)
			}
		})
	}, func() {
		if dialogButton(c, locales.ActionCancel(), false).Clicked() {
			a.collectOpen = false
		}
		if dialogButton(c, locales.CollectIntoMySkills(), true).Disabled(a.tidy.busy() || a.tidy.scanning).Clicked() {
			a.collectOpen = false
			a.tidyProject(s, collectProjectSkill)
		}
	})
}
