package main

import (
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/plugins/updater"
	"github.com/egoist/mygo/plugins/updater/native"
	"github.com/egoist/mygo/ui"

	"skillmanager/locales"
)

// appUpdater 是应用自身的版本更新，设置页和「检查应用更新…」菜单通过它
// 读写 mygo 的 updater 插件。插件只能在 App.Run 之前注册一次，单元测试里
// 没有它，用 noAppUpdates 或测试替身。
type appUpdater interface {
	// Enabled 报告这份应用能不能自己更新：开发版、装在没有写权限的位置时不能。
	Enabled() bool
	// Check 打开更新窗口检查，立即返回。
	Check()
	AutomaticChecks() bool
	SetAutomaticChecks(on bool)
	// LastCheck 是上次检查成功的时间，从没检查过是零值。
	LastCheck() time.Time
	// Available 是找到的新版本，没有或用户跳过了是空串。
	Available() string
}

// noAppUpdates 是没有注册插件时的更新：不能更新，也从不检查。
type noAppUpdates struct{}

func (noAppUpdates) Enabled() bool           { return false }
func (noAppUpdates) Check()                  {}
func (noAppUpdates) AutomaticChecks() bool   { return false }
func (noAppUpdates) SetAutomaticChecks(bool) {}
func (noAppUpdates) LastCheck() time.Time    { return time.Time{} }
func (noAppUpdates) Available() string       { return "" }

// pluginUpdates 是 updater 插件提供的更新。
type pluginUpdates struct{ enabled bool }

// useAppUpdates 注册原生更新窗口并返回它的更新；必须在 App.Run 之前调用。
// 默认不在后台检查（README 承诺只在用户操作时联网），打开设置里的开关后
// 才每天检查一次。检查完成、用户在更新窗口里作答或改了开关后，在主线程
// 调 changed 重画设置页。
func useAppUpdates(changed func()) appUpdater {
	mygo.Use(native.New(appUpdateOptions()))
	updater.OnChange(changed)
	// Enabled 每次都要在安装目录旁试建临时目录，运行期间安装位置不会变，只问一次
	return pluginUpdates{enabled: mygo.Updater.Enabled()}
}

func appUpdateOptions() updater.Options {
	return updater.Options{DisableAutomaticChecks: true, Strings: appUpdateStrings()}
}

// appUpdateStrings 换掉插件「不能更新」的说明：默认说法让人去软件包管理器
// 里更新，对从磁盘映像拖出来用的 Mac 应用不成立，这里只说不能自行更新。
func appUpdateStrings() map[string]updater.Strings {
	strs := map[string]updater.Strings{}
	for _, lang := range appLanguages {
		// 插件用 fmt 把应用名填进 %[1]s，并在注册时检查占位符个数
		strs[lang] = updater.Strings{UnavailableMessage: locales.In(lang).AppUpdateUnavailableMessage("%[1]s")}
	}
	return strs
}

func (p pluginUpdates) Enabled() bool            { return p.enabled }
func (pluginUpdates) Check()                     { updater.CheckForUpdates() }
func (pluginUpdates) AutomaticChecks() bool      { return updater.AutomaticChecks() }
func (pluginUpdates) SetAutomaticChecks(on bool) { updater.SetAutomaticChecks(on) }
func (pluginUpdates) LastCheck() time.Time       { return updater.LastCheck() }
func (pluginUpdates) Available() string          { return updater.AvailableVersion() }

// appUpdateChecked 是设置页版本下面那行：上次什么时候检查的。
func appUpdateChecked(at, now time.Time) string {
	switch {
	case at.IsZero():
		return locales.SettingsAppUpdateNeverChecked()
	case sameDay(at, now):
		return checkedLabel(at)
	}
	return locales.SettingsAppUpdateCheckedOn(scanDay(at, now))
}

func sameDay(a, b time.Time) bool {
	a, b = a.Local(), b.Local()
	return a.Year() == b.Year() && a.YearDay() == b.YearDay()
}

// appUpdateGroup 是 设置 › 通用 里的「应用更新」分组（画板 app-self-update）。
func (a *app) appUpdateGroup(c *ui.Context) {
	u := a.appUpd
	groupLabel(c, locales.SettingsAppUpdates(), false)
	setBox(c, func() {
		setRow(c).Children(func() {
			sub := locales.SettingsAppUpdateUnavailable()
			if u.Enabled() {
				sub = appUpdateChecked(u.LastCheck(), time.Now())
			}
			rowText(c, locales.SettingsAppVersion(appVersion()), sub, false)
			label := locales.SettingsCheckForUpdates()
			if v := u.Available(); v != "" && u.Enabled() {
				tag(c, importTagSpec{text: locales.SettingsAppUpdateAvailable(v), tone: tagToneAccent}, false)
				label = locales.SettingsViewAppUpdate()
			}
			if b := pushButton(c, label).Disabled(!u.Enabled()); b.Clicked() {
				u.Check()
			}
		})
		setRow(c).Children(func() {
			rowText(c, locales.SettingsAutoCheckAppUpdates(), locales.SettingsAutoCheckAppUpdatesHint(), false)
			on := u.AutomaticChecks()
			if t := toggle(c, &on, locales.SettingsAutoCheckAppUpdates()).Disabled(!u.Enabled()); t.Changed() {
				u.SetAutomaticChecks(on)
			}
		})
	})
}
