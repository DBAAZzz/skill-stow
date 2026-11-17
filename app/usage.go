package main

import (
	"fmt"
	"time"

	"github.com/egoist/mygo/ui"

	"skillmanager/internal/library"
	"skillmanager/locales"
)

// 使用统计：从 Claude Code 和 Codex 的会话记录里数出每个技能被调用了几次。
// 「使用情况」页（v1-c）在 usage_page.go。

// loadUsage 在后台读会话记录（几百 MB，第一次要一会儿），读完重画。
func (a *app) loadUsage() {
	home := a.lib.Home
	if home == "" || !a.live {
		return
	}
	go func() {
		log := library.ReadUsage(home)
		a.post(func() {
			a.usage = log
			a.lib.SetUsage(log)
		})
	}()
}

// refreshUsage 在重新扫描后把已有的统计对到新的库上，再去读新的记录。
func (a *app) refreshUsage() {
	if a.usage != nil {
		a.lib.SetUsage(a.usage)
	}
	if a.readUsage != nil {
		a.readUsage()
		return
	}
	a.loadUsage()
}

// usageAgents 是「Claude Code 和 Codex」：留有调用记录的 Agent。
func usageAgents() string {
	var names []string
	for _, id := range library.UsageAgents {
		names = append(names, library.AgentName(id))
	}
	return library.JoinAnd(names)
}

// usageSince 是「9 月 27 日」：统计从哪天起，不是今年的带上年份。
func (a *app) usageSince(now time.Time) string {
	return monthDay(a.lib.UsageSince().Local(), now)
}

// lastUsed 是「今天」「昨天」「12 天前」。
func lastUsed(t, now time.Time) string {
	return locales.DaysAgo(daysAgo(t, now))
}

// usagePane 是详情栏的「使用情况」：调用了几次、上次什么时候、各 Agent
// 几次；没用过或不知道时说明原因。
func (a *app) usagePane(c *ui.Context, s *library.Skill) {
	label(c, locales.NavUsage())
	now := time.Now()
	u := a.lib.Usage(s)
	title, sub, note := "", "", ""
	since, agents := a.usageSince(now), usageAgents()
	switch {
	case !a.lib.HasUsage():
		title = locales.UsageCounting()
	case u.Count > 0:
		title, sub = locales.UsageCalledTimes(u.Count), locales.UsageLastUsed(daysAgo(u.Last, now))
		note = locales.UsageCountedFrom(agents, since)
	case a.lib.UsageTracked(s):
		title = locales.UsageNeverCalled()
		note = locales.UsageNotInLogs(since, agents)
	default:
		title = locales.UsageNoRecord()
		note = locales.UsageAgentsCantRead(agents)
	}
	box(c, func() {
		boxRow(c, true).MinHeight(44).Children(func() {
			titled(c, title, sub)
			for _, id := range library.UsageAgents {
				if n := u.By[id]; n > 0 {
					ui.Row(c).Gap(4).AlignItems(ui.Center).Shrink(0).Children(func() {
						agentBadge(c, id, 16)
						ui.Text(c, fmt.Sprint(n)).FontSize(12).TextColor(colTextSecondary).FontFeatures("tnum").SingleLine()
					})
				}
			}
		})
	})
	if note != "" {
		caption(c, note)
	}
}
