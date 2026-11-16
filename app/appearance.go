package main

import "github.com/egoist/mygo"

// 设置里「主题」的三个选项，也是存进 Settings.Theme 的值。
const (
	themeAuto  = "" // 跟随系统
	themeLight = "light"
	themeDark  = "dark"
)

// applyTheme 让应用换成 id 的外观（themeAuto 跟随系统）。系统画的毛玻璃、
// 红绿灯、原生菜单和对话框跟着换；界面下一帧由 theme 按新外观换颜色。可以
// 在 App.Run 之前调用。测试里换成只记下选了什么。
var applyTheme = func(id string) {
	source := mygo.ThemeSystem
	switch id {
	case themeLight:
		source = mygo.ThemeLight
	case themeDark:
		source = mygo.ThemeDark
	}
	mygo.Theme.SetSource(source)
}

// setTheme 是在设置里选了一种主题：存进设置，窗口马上换过去。
func (a *app) setTheme(id string) {
	if a.lib.Settings.Theme == id {
		return
	}
	a.lib.Settings.Theme = id
	a.saveSettings()
	applyTheme(id)
}
