package main

import (
	"strings"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/i18n"

	"skillmanager/locales"
)

// 界面语言。文案在 locales/ 的目录里（zh-Hans.json 是源语言，en.json 是
// 翻译），`go tool mygo generate` 为每条生成一个函数，按当前语言说。
// 设置里的 Language 是唯一的来源：启动时和改的时候交给 mygo.App.SetLocale，
// 下一帧整个界面就换成新语言，不重建、不读盘。

// languageOption 是「语言」选择器里的一项；name 为空时用 label 现取，
// 语言自己的名字（简体中文、English）不随界面语言变。
type languageOption struct {
	id    string // BCP 47；空为跟随系统
	name  string
	label func() string
}

var languageOptions = []languageOption{
	{id: "", label: locales.SettingsLanguageSystem},
	{id: "zh-Hans", name: "简体中文"}, //i18n:ignore 语言用它自己的文字写
	{id: "en", name: "English"},
}

func (o languageOption) title() string {
	if o.label != nil {
		return o.label()
	}
	return o.name
}

// appLanguages 是界面写成的语言，对不上系统语言时退回第一个。
var appLanguages = []string{"en", sourceLocale}

// applyLanguage 让界面换成 id 的语言（空为跟随系统）。测试里换成只改测试
// 用的语言，不去动系统里记住的应用语言。
var applyLanguage = func(id string) { mygo.App.SetLocale(id) }

// setLanguage 是在设置里选了一种语言：存进设置，界面马上换过去。
func (a *app) setLanguage(id string) {
	if a.lib.Settings.Language == id {
		return
	}
	a.lib.Settings.Language = id
	a.saveSettings()
	applyLanguage(id)
	a.retitleWindows()
	a.refreshAppMenu()
}

// retitleWindows 按现在的语言重写窗口标题：标题是开窗时交给系统的，界面
// 重画时不会跟着换。
func (a *app) retitleWindows() {
	if a.win != nil && !a.win.IsDestroyed() {
		a.win.SetTitle(locales.AppName())
	}
	if a.setWin != nil && !a.setWin.IsDestroyed() {
		a.setWin.SetTitle(settingsTabs[a.set.tab].label())
	}
}

// sourceLocale 是目录的源语言：测试里和启动前没定语言时按它说。
const sourceLocale = "zh-Hans"

// uiLocale 是界面现在用的语言，给 CLDR 的日期、星期名用。
func uiLocale() string {
	if l := i18n.Locale(); l != "" {
		return l
	}
	return sourceLocale
}

// monthDay 是「10 月 6 日」「Oct 6」，不是 now 那年的带上年份。中文照产品
// 文档在数字和汉字之间空一格（CLDR 的中文格式不空），所以日期写在目录里，
// 不用 i18n.FormatDate。
func monthDay(t, now time.Time) string {
	if t.Year() != now.Year() {
		return locales.DateYearMonthDay(t.Year(), int(t.Month()), t.Day())
	}
	return locales.DateMonthDay(int(t.Month()), t.Day())
}

// weekdayName 是「周一」「Mon」，按 CLDR。
func weekdayName(t time.Time) string {
	return i18n.Weekdays(uiLocale(), i18n.Abbreviated, true)[t.Weekday()]
}

// daysAgo 是 t 离 now 隔了几个日历日（本地时间），今天是 0。
func daysAgo(t, now time.Time) int {
	day := func(t time.Time) time.Time {
		y, m, d := t.Local().Date()
		return time.Date(y, m, d, 0, 0, 0, 0, time.Local)
	}
	return max(0, int(day(now).Sub(day(t)).Hours()/24+0.5))
}

// emphMark 占住一句话里要强调的那段。各语言语序不同，强调的部分不一定在
// 句首句尾，所以先用它说出整句，再从它两边切开（splitEmph），分开画。
const emphMark = "\uE000"

// splitEmph 把用 emphMark 说出的整句切成强调部分前后两段。
func splitEmph(s string) (before, after string) {
	before, after, _ = strings.Cut(s, emphMark)
	return before, after
}
