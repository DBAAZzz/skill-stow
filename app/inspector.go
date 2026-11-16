package main

import (
	"slices"
	"strings"

	"github.com/egoist/mygo/ui"

	"skillmanager/internal/library"
	"skillmanager/locales"
)

// inspector 是右侧详情栏（r8-c / p2-a / c1-c）：选中技能或集合才出现，Esc
// 或点空白处收起。
func (a *app) inspector(c *ui.Context, s *library.Skill, coll *library.Collection) {
	ui.Scroll(c.Key("inspector")).Width(inspectorWidth).Shrink(0).Background(colBgPane).Scrollbars(false).Children(func() {
		ui.Column(c).Padding(22, 20).Children(func() {
			if coll != nil {
				a.setPane(c, coll)
				return
			}
			if a.scope == scopeUsage {
				a.usagePreviewPane(c, s)
				return
			}
			switch s.Source {
			case library.Project:
				a.projectSkillPane(c, s)
			case library.Builtin, library.ViaPlugin:
				a.paneHeader(c, s, iconLock, nil)
				defectsCallout(c, s)
				label(c, locales.CommonEnabledFor())
				readOnlyNote(c, pluginNote(s.Source, s.Provider, s.Plugin))
				a.usagePane(c, s)
				a.infoPane(c, s, "", colTextMuted)
			default:
				a.mineSkillPane(c, s)
			}
		})
	})
}

// defectsCallout 列出技能里说有、磁盘上没有的东西，没有时不显示。
func defectsCallout(c *ui.Context, s *library.Skill) {
	if len(s.Defects) > 0 {
		callout(c, library.Incomplete.Title(), defectLines(s.Defects), "")
	}
}

// mineSkillPane keeps discovery, native controls, and adding an Agent separate.
func (a *app) mineSkillPane(c *ui.Context, s *library.Skill) {
	a.paneHeader(c, s, nil, func(fm *frostedMenu) { a.paneMenu(fm, s) })
	if a.notice != nil && a.notice.skill == s.ID {
		doneBox(c, locales.MergeCompleted(), a.notice.result)
		a.notice.started = true
	}
	if s.MissingDescription() {
		callout(c, locales.CommonNoDescription(), locales.InspectorMissingDescription(), "")
	}
	defectsCallout(c, s)
	a.copiesCallout(c, s)
	label(c, "Agent")
	ids := a.controllableAgents(s)
	enabled := 0
	for _, id := range ids {
		if s.State(id).Enabled {
			enabled++
		}
	}
	box(c, func() {
		boxRow(c, true).MinHeight(44).Children(func() {
			ui.Text(c, locales.CommonAllAgents()).FontWeight(600).Grow(1)
			on := len(ids) > 0 && enabled == len(ids)
			sw := mixedToggle(c, &on, enabled > 0 && enabled < len(ids), locales.InspectorToggleAllAgents()).Disabled(len(ids) == 0)
			if sw.Changed() {
				a.requestEnable(s, ids, on)
			}
		})
	})
	ui.Box(c).Height(8)
	var add []library.Agent
	for _, ag := range a.lib.Visible() {
		if a.lib.CanAddToAgent(s, ag.ID) {
			add = append(add, ag)
		}
	}
	box(c, func() {
		first := true
		for _, ag := range a.lib.Visible() {
			if !s.DiscoveredIn(ag.ID) {
				continue
			}
			a.agentToggle(c.Key(ag.ID), s, ag, first)
			first = false
		}
		if len(add) > 0 {
			a.addAgentRow(c, s, add, first)
		}
	})
	var note string
	noteColor := colTextMuted
	switch {
	case s.Copies > 0:
		note, noteColor = locales.InspectorIndependentCopies(s.Copies+1), colWarnText
	case s.LinkedFrom() > 0:
		note = locales.InspectorLinkedLocations(s.LinkedFrom())
	}
	a.infoPane(c, s, note, noteColor)
	a.nativeTips(c, s)
	if redundant := s.Redundant(); len(redundant) > 0 {
		a.redundantNote(c, s, redundant)
	}
	if a.err != "" && a.errAgent == "" {
		errorLine(c, a.err)
	}
	a.usagePane(c, s)
}

// infoPane 是详情栏的「信息」框：「我的」技能带安装记录时先列来源、网站和
// 安装时间（src2-b），最后一行是位置；note 是位置下面一行说明（几份副本、
// 几处链接），空为没有。
func (a *app) infoPane(c *ui.Context, s *library.Skill, note string, noteColor ui.Color) {
	label(c, locales.CommonInfo())
	box(c, func() {
		n := 0
		row := func(key string, value func()) {
			boxRow(c.Key(key), n == 0).MinHeight(38).Children(func() {
				ui.Text(c, key).FontSize(12.5).TextColor(colTextMuted).Width(52).Shrink(0)
				value()
			})
			n++
		}
		if s.Source == library.Mine {
			if rec, ok := a.lib.InstallRecord(s.Name); ok && rec.Source != "" {
				row(locales.CommonSource(), func() {
					ui.Row(c).Gap(6).AlignItems(ui.Center).Grow(1).MinWidth(0).Children(func() {
						if rec.SourceType == "github" {
							ui.Icon(c, iconGitHub).Size(14, 14).Shrink(0).TextColor(colTextPrimary).Label("GitHub")
							ui.Text(c, rec.Source).FontSize(12.5).MinWidth(0).SingleLine()
							return
						}
						ui.Text(c, rec.Source+" · "+rec.SourceType).FontSize(12.5).MinWidth(0).SingleLine()
					})
				})
				if url := recordWebURL(rec); url != "" {
					row(locales.CommonWebsite(), func() { a.sourceTextLink(c, url, url, colAccent, nil).Grow(1).MinWidth(0) })
				}
				if !rec.InstalledAt.IsZero() {
					row(locales.InspectorInstalledAt(), func() {
						ui.Text(c, rec.InstalledAt.Format("2006-01-02")).FontSize(12.5).
							FontFeatures("tnum").Grow(1).MinWidth(0).SingleLine()
					})
				}
			}
		}
		row(locales.CommonLocation(), func() {
			ui.Column(c).Grow(1).MinWidth(0).Gap(2).Children(func() {
				ui.Text(c, a.lib.Display(s.Dir)).Font("monospace").FontSize(11.5).TextColor(colTextBody).
					SingleLine().Selectable()
				if note != "" {
					ui.Text(c, note).FontSize(11.5).TextColor(noteColor)
				}
			})
			// 向外的箭头表示点了会跳出去，悬停提示说清是在 Finder 中显示
			if rowIcon(c, iconExternal, locales.ActionShowInFinder()).TextColor(colAccent).Clicked() {
				a.reveal(s.Dir)
			}
		})
	})
}

// recordWebURL 是安装记录对应的网页：GitHub 来源拼仓库地址，其余来源用
// 登记过的入口/来源地址；没有可信地址时为空。
func recordWebURL(rec library.LockEntry) string {
	switch rec.SourceType {
	case "github":
		repo := strings.TrimSuffix(strings.TrimSpace(rec.Source), ".git")
		if repo != "" && strings.Count(repo, "/") == 1 && !strings.Contains(repo, "..") &&
			!strings.HasPrefix(repo, "http") {
			return "https://github.com/" + repo
		}
	case "well-known", "gitlab", "git", "download":
		for _, u := range []string{rec.SourceBaseURL, rec.SourceURL} {
			if strings.HasPrefix(u, "https://") || strings.HasPrefix(u, "http://") {
				return u
			}
		}
	}
	return ""
}

func (a *app) agentToggle(c *ui.Context, s *library.Skill, ag library.Agent, first bool) {
	st := s.State(ag.ID)
	boxRow(c, first).MinHeight(44).Children(func() {
		agentBadge(c, ag.ID, 18)
		ui.Text(c, ag.DisplayName()).FontWeight(500).Grow(1).MinWidth(0).SingleLine()
		// 在这里加到这个 Agent 的可以「移除」：改不了启用设置的只留它（放在开关的
		// 位置）；改得了的放在开关左边，停用只是关掉，移除才删链接
		removable := a.lib.CanRemoveFromAgent(s, ag.ID)
		added := st.Kind == library.StateUnsupported && removable
		label := ""
		color := colTextMuted
		switch st.Kind {
		case library.StateRestart:
			label = st.Kind.Label()
			color = colWarnText
		case library.StateUnknown, library.StateFailed:
			label = st.Kind.Label()
			color = colDanger
		case library.StateUnsupported:
			if !added {
				label = st.Kind.Label()
			}
		}
		// 状态词靠右，和开关的距离每行一样
		word := ui.Text(c, label).Width(52).TextAlign(ui.End).FontSize(11.5).TextColor(color).SingleLine()
		if st.Kind == library.StateUnsupported && !added {
			word.Tooltip(locales.InspectorAlwaysOnTip(ag.DisplayName()))
		}
		if removable {
			if linkButton(c, locales.ActionRemove()).Width(30).Justify(ui.End).Shrink(0).Tooltip(locales.InspectorRemoveLinkTip(ag.DisplayName())).Clicked() {
				if err := a.lib.RemoveFromAgent(s, ag.ID); err != nil {
					a.err, a.errAgent = err.Error(), ag.ID
				}
				a.changed()
			}
		}
		if added {
			return
		}
		on := st.Enabled
		sw := toggle(c, &on, locales.InspectorToggle(ag.DisplayName())).Disabled(!st.Controllable())
		if !st.Controllable() {
			sw.Opacity(0.4)
		}
		if sw.Changed() {
			a.requestEnable(s, []string{ag.ID}, on)
		}
	})
}

// copiesCallout 是顶部的同名副本提示：内容不一样的「对比副本 ›」，一样的
// 「收成一份」（d0-a、d0-b）。剩下的都在动不了的地方时只说在哪。
func (a *app) copiesCallout(c *ui.Context, s *library.Skill) {
	if s.Copies <= 0 {
		return
	}
	vs := s.Versions()
	title := locales.InspectorOtherCopies(s.Copies)
	wrap := ui.Column(c)
	if tip := copyTip(s); tip != "" {
		wrap.Tooltip(tip)
	}
	wrap.Children(func() { a.copiesCalloutBody(c, s, vs, title) })
}

func (a *app) copiesCalloutBody(c *ui.Context, s *library.Skill, vs []library.Version, title string) {
	switch {
	case len(vs) > 1:
		reals := map[string]bool{}
		for _, v := range vs[1:] {
			for _, p := range v.Places {
				reals[p.Real] = true
			}
		}
		if callout(c, title, locales.InspectorDifferentCopies(len(reals)), locales.ActionCompareCopiesChevron()) {
			a.openCompare(s, false)
		}
	case s.Identical() && a.lib.CanMerge(s):
		if callout(c, title, locales.InspectorCopiesMatch(), locales.ActionMergeCopies()) {
			a.merge(s, 0, false)
		}
	case s.Identical():
		var dirs []string
		for _, p := range vs[0].Places {
			if d := a.lib.Display(p.Dir); p.Dir != a.lib.Root && !slices.Contains(dirs, d) {
				dirs = append(dirs, d)
			}
		}
		callout(c, title, locales.InspectorCopiesSame(strings.Join(dirs, locales.ListSeparator())), "")
	default:
		callout(c, title, locales.InspectorOtherCopiesMayDiffer(), "")
	}
}

// redundantNote 是灰色的提示：读通用目录的 Agent 自己目录里还有指向这份技能
// 的链接，和通用目录重复，可以清理掉。
func (a *app) redundantNote(c *ui.Context, s *library.Skill, agents []string) {
	var names []string
	for _, id := range agents {
		names = append(names, library.AgentName(id))
	}
	text := locales.InspectorDupLinkOne(names[0])
	if len(names) > 1 {
		text = locales.InspectorDupLinkMany(library.JoinAnd(names))
	}
	// 操作单独一列在右边，竖直居中，和提示区的「重试」一样
	ui.Row(c).Margin(10, 0, 0, 0).Padding(9, 12).Gap(10).Radius(9).Background(colBgFill).AlignItems(ui.Start).Children(func() {
		ui.Text(c, text).FontSize(12).LineHeight(1.5).TextColor(colTextSecondary).Grow(1).MinWidth(0)
		if tipAction(c, locales.ActionCleanUp()).Tooltip(locales.InspectorRemoveExtraLinksHint()).Clicked() {
			a.err = ""
			defer a.changed()
			if err := a.lib.Tidy(s); err != nil {
				a.err = err.Error()
			}
		}
	})
}

// labelNote 是带灰色说明的分组标题。
func labelNote(c *ui.Context, text, note string) {
	ui.Row(c).Gap(6).Margin(22, 0, 7, 2).AlignItems(ui.Baseline).Children(func() {
		sectionLabel(c, text)
		ui.Text(c, note).FontSize(11).TextColor(colTextDisabled).SingleLine()
	})
}

// projectSkillPane 是项目技能（p2-a）：谁会读到，没有开关。
func (a *app) projectSkillPane(c *ui.Context, s *library.Skill) {
	a.paneHeader(c, s, iconFolder, nil)
	defectsCallout(c, s)
	if n := len(s.Enabled); n > 0 && warnLine(c, iconLink, locales.InspectorGlobalLinkAgents(n), locales.ActionViewEllipsis()) {
		if p := a.lib.Project(s.ProjectDir); p != nil {
			a.showLinked(p)
		}
	}
	label(c, locales.ImportWhoCanRead())
	if len(s.Reads) == 0 {
		readOnlyNote(c, locales.InspectorNoAgentReadsDirectory())
	} else {
		box(c, func() {
			for i, r := range s.Reads {
				boxRow(c.Key(i), i == 0).MinHeight(44).Children(func() {
					agentBadge(c, r.Agent, 18)
					where, dir := locales.InspectorWhenIn(s.Project), r.Dir
					if r.Global {
						where, dir = locales.InspectorGlobalScope(), a.lib.Display(r.Dir)
					}
					titled(c, library.AgentName(r.Agent), where)
					ui.Text(c, dir).Font("monospace").FontSize(10.5).TextColor(colTextDisabled).SingleLine().Shrink(0)
					// 读得到不等于启用：Agent 配置里关掉的要写出来
					if st, ok := a.lib.ProjectAgentState(s, r.Agent); ok && st.Kind != library.StateEnabled {
						color := colTextMuted
						if st.Kind == library.StateUnknown {
							color = colDanger
						}
						ui.Text(c, st.Kind.Label()).FontSize(11.5).TextColor(color).SingleLine().Shrink(0)
					}
				})
			}
		})
	}
	a.projectTips(c, s)
	caption(c, locales.InspectorProjectNoToggle())
	a.usagePane(c, s)
	a.infoPane(c, s, "", colTextMuted)
}

// projectTips 是项目技能在 Agent 里的状态说明（为什么停用、哪份配置读不了），
// 和「我的」技能的提示区一个样子；读不了的可以重试。
func (a *app) projectTips(c *ui.Context, s *library.Skill) {
	var ids []string
	for _, r := range s.Reads {
		if st, ok := a.lib.ProjectAgentState(s, r.Agent); ok && st.Message != "" && !slices.Contains(ids, r.Agent) {
			ids = append(ids, r.Agent)
		}
	}
	if len(ids) == 0 {
		return
	}
	ui.Column(c.Key("project-tips")).Shrink(0).Margin(8, 0, 0, 0).Radius(10).Background(colBgSubtle).Padding(4, 0).Children(func() {
		for _, id := range ids {
			st, _ := a.lib.ProjectAgentState(s, id)
			ui.Row(c.Key(id)).Padding(8, 12).Gap(10).AlignItems(ui.Start).Children(func() {
				agentBadge(c, id, 18)
				ui.Text(c, st.Message).FontSize(12).LineHeight(1.5).TextColor(colTextSecondary).Grow(1).MinWidth(0)
				if st.Kind == library.StateUnknown && tipAction(c, locales.ActionRetry()).Clicked() {
					a.rescan()
				}
			})
		}
	})
}

// pluginNote 说明只读的技能为什么不能在这里停用：内置的随 Agent 生效；
// 插件装的随插件生效，要不用了得在 Agent 里卸载整个插件。
func pluginNote(src library.Source, provider, plugin string) string {
	agent := library.AgentName(provider)
	switch {
	case src != library.ViaPlugin && plugin != "":
		return locales.InspectorPluginWithAgent(agent)
	case src != library.ViaPlugin:
		return locales.InspectorBuiltinWithAgent(agent)
	case provider == "claude-code":
		return locales.InspectorPluginUninstallCommand(plugin, agent)
	}
	return locales.InspectorPluginUninstallManager(plugin, agent)
}

// viaPlugin 报告集合是用户用插件命令装的插件。
func viaPlugin(coll *library.Collection) bool {
	return len(coll.Skills) > 0 && coll.Skills[0].Source == library.ViaPlugin
}

func readOnlyNote(c *ui.Context, text string) {
	box(c, func() {
		ui.Row(c).Padding(10, 12).Gap(8).AlignItems(ui.Start).TextColor(colTextSecondary).Children(func() {
			ui.Icon(c, iconLock).Size(13, 13).Margin(2, 0, 0, 0)
			ui.Text(c, text).FontSize(12).LineHeight(1.5).Grow(1)
		})
	})
}

// addAgentRow 是 Agent 列表的最后一行「＋ 添加到其他 Agent…」（en6-a）：
// 图标和上面的 Agent 图标同一列，点开复用统一毛玻璃菜单（sf6）。
func (a *app) addAgentRow(c *ui.Context, s *library.Skill, agents []library.Agent, first bool) {
	text := locales.InspectorAddToOther()
	a.triggerMenu(c, "inspector.addAgent", menuPanelOpts{width: menuMinWidth}, func(row ui.Element, open bool) {
		row.Height(40).Padding(0, 12).Gap(9).AlignItems(ui.Center).Justify(ui.Start).
			TextColor(colAccent).Label(text)
		if !first {
			row.BorderWidth(1, 0, 0, 0).BorderColor(colBorderSubtle)
		}
		if open {
			row.Background(colInkHover)
		} else {
			tintInk(row)
		}
		row.Children(func() {
			ui.Box(c).Size(18, 18).Shrink(0).Radius(5).Background(colAccentBg).Center().Children(func() {
				ui.Icon(c, iconPlus).Size(11, 11)
			})
			ui.Text(c, text).SingleLine()
		})
	}, func(fm *frostedMenu) {
		for _, ag := range agents {
			id := ag.ID
			if fm.action(ag.DisplayName(), withKey(id), withLabel(locales.ActionAddTo(ag.DisplayName())), withBadge(func(c *ui.Context) { agentBadge(c, id, 16) })) {
				a.addToAgent(s, id)
			}
		}
	})
}
