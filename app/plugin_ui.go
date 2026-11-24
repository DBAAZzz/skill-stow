package main

import (
	"slices"

	"github.com/egoist/mygo/ui"

	"skillmanager/internal/library"
	"skillmanager/locales"
)

// 卸载插件：Claude Code 插件装的技能不能单独删，只能连插件一起卸载。
// 从技能卡片的右键菜单、详情栏只读说明里的链接发起，确认框写出要执行的
// 命令和一起没了的技能；命令在后台跑，跑完重新扫描。卸载不能撤销。

// pluginState 是卸载插件的确认框和正在执行的命令。
type pluginState struct {
	confirm *library.Skill // 确认框要卸载的插件里的一个技能
	open    bool
	running bool
}

// requestUninstallPlugin 打开卸载 s 所在插件的确认框。
func (a *app) requestUninstallPlugin(s *library.Skill) {
	if !a.lib.CanUninstallPlugin(s) || a.plug.running {
		return
	}
	a.plug.confirm, a.plug.open = s, true
}

// uninstallPluginItem 是右键菜单里的「卸载插件…」，不能卸载的不加。
func (a *app) uninstallPluginItem(fm *frostedMenu, s *library.Skill) {
	if !a.lib.CanUninstallPlugin(s) {
		return
	}
	fm.separator()
	if fm.action(locales.ActionUninstallPluginEllipsis(), withIcon(iconTrash), withDanger()) {
		a.requestUninstallPlugin(s)
	}
}

// pluginReadOnlyNote 是插件和内置技能（或集合）详情栏里的只读说明；s 是其中
// 一个技能，所在插件能卸载时说明后面带「卸载插件…」。
func (a *app) pluginReadOnlyNote(c *ui.Context, s *library.Skill, src library.Source, provider, plugin string) {
	if !a.lib.CanUninstallPlugin(s) {
		readOnlyNote(c, pluginNote(src, provider, plugin), nil)
		return
	}
	readOnlyNote(c, locales.InspectorPluginUninstallHere(plugin, library.AgentName(provider)), func() {
		if linkButton(c, locales.ActionUninstallPluginEllipsis()).Disabled(a.plug.running).Clicked() {
			a.requestUninstallPlugin(s)
		}
	})
}

// uninstallPluginDialog 是卸载插件的确认框：默认按钮是「取消」，回车不会卸载。
// 命令执行期间确认框留着，按钮显示「正在卸载…」。
func (a *app) uninstallPluginDialog(c *ui.Context) {
	s := a.plug.confirm
	if !a.plug.open || s == nil {
		if !a.plug.running {
			a.plug.confirm = nil
		}
		return
	}
	skills := a.lib.PluginSkills(s)
	agent := library.AgentName(s.Provider)
	dialog(c, &a.plug.open, locales.PluginUninstallTitle(s.Plugin), locales.PluginUninstallBody(agent, len(skills)), func() {
		sectionLabel(c, locales.PluginUninstallCommandLabel()).Margin(14, 2, 6, 2)
		deleteInsetBox(c, false, 0, func() {
			deleteFileRow(c, true, func() {
				cmd := a.lib.UninstallCommand(s)
				ui.Text(c, cmd).Font("monospace").FontSize(12).TextColor(colTextPrimary).Grow(1).MinWidth(0).Selectable()
			})
		})
		sectionLabel(c, locales.PluginUninstallSkillsLabel()).Margin(14, 2, 6, 2)
		deleteInsetBox(c, len(skills) > 5, 196, func() {
			for i, k := range skills {
				deleteFileRow(c.Key(k.ID), i == 0, func() {
					ui.Icon(c, iconDoc).Size(14, 14).Shrink(0).TextColor(colTextMuted)
					ui.Text(c, k.Name).FontSize(13).TextColor(colTextPrimary).Grow(1).MinWidth(0).SingleLine()
				})
			}
		})
	}, func() {
		cancel := dialogButton(c, locales.ActionCancel(), false).AutoFocus().Disabled(a.plug.running)
		if cancel.Clicked() {
			a.plug.open = false
		}
		label := locales.ActionUninstall()
		if a.plug.running {
			label = locales.ActionUninstalling()
		}
		if dangerButton(c, label).Disabled(a.plug.running).Clicked() {
			a.uninstallPlugin(s)
		}
	})
	if a.plug.running {
		a.plug.open = true // 命令跑完之前不让 Esc 或点外面关掉
	}
}

// uninstallPlugin 在后台执行卸载命令，结果用提示条告诉用户。
func (a *app) uninstallPlugin(s *library.Skill) {
	lib := a.lib
	a.plug.running = true
	a.background(func(post func(func())) {
		err := lib.UninstallPlugin(s)
		post(func() {
			a.plug.running, a.plug.open, a.plug.confirm = false, false, nil
			if err != nil {
				a.notify(fromPlugin, toastError, locales.PluginUninstallFailed(s.Plugin, err.Error()))
				return
			}
			a.forgetPlugin(lib, s)
			a.notify(fromPlugin, toastPlain, locales.PluginUninstalled(s.Plugin))
			a.rescan()
		})
	})
}

// forgetPlugin 让界面不再停在卸掉的插件上：示例数据不重新扫描，直接从库里拿掉。
func (a *app) forgetPlugin(lib *library.Library, s *library.Skill) {
	if a.lib != lib {
		return
	}
	gone := lib.PluginSkills(s)
	if slices.Contains(gone, a.selected) {
		a.selected = nil
	}
	if a.reading != nil && slices.Contains(gone, a.reading) {
		a.reading = nil
	}
	if a.coll != nil && len(a.coll.Skills) > 0 && slices.Contains(gone, a.coll.Skills[0]) {
		a.coll = nil
	}
	if a.inColl != nil && len(a.inColl.Skills) > 0 && slices.Contains(gone, a.inColl.Skills[0]) {
		a.inColl = nil
	}
	lib.ForgetPlugin(s)
}

// canUninstallSet 报告集合是能在这里卸载的插件。
func (a *app) canUninstallSet(coll *library.Collection) bool {
	return viaPlugin(coll) && a.lib.CanUninstallPlugin(coll.Skills[0])
}

// setCardActions 是指针停在插件集合卡片上时底部那一行：左边打开集合，
// 右边红色的卸载，和技能卡片的预览、删除同一个样子。
func (a *app) setCardActions(c *ui.Context, coll *library.Collection) {
	if cardAction(c, locales.ActionOpen(), colAccent).Clicked() {
		a.openSet(coll)
	}
	ui.Spacer(c)
	if cardAction(c, locales.ActionUninstall(), colDangerText).Clicked() {
		a.requestUninstallPlugin(coll.Skills[0])
	}
}

// soloPlugin 报告 s 是能卸载的插件里唯一的技能：卡片就代表整个插件，悬停时
// 可以直接卸载。插件带好几个技能时，从集合卡片卸载，免得以为只删这一个。
func (a *app) soloPlugin(s *library.Skill) bool {
	return a.lib.CanUninstallPlugin(s) && len(a.lib.PluginSkills(s)) == 1
}
