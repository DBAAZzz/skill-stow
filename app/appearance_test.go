package main

import "testing"

func TestUserPicksDarkThenLightThemeInSettings(t *testing.T) {
	// Given 在设置里，主题是默认的跟随系统
	a, _ := newTester(t)
	var applied []string
	applyTheme = func(id string) { applied = append(applied, id) }
	t.Cleanup(func() { applyTheme = func(string) {} })
	st := newUITester(a.settingsView, settingsWidth, settingsMaxHeight)
	if a.lib.Settings.Theme != themeAuto {
		t.Fatalf("默认应跟随系统，得到 %q", a.lib.Settings.Theme)
	}

	// When 点「深色」
	must(t, st.Click("深色"))
	// Then 存下深色，并让应用换成深色外观
	if a.lib.Settings.Theme != themeDark || len(applied) != 1 || applied[0] != themeDark {
		t.Fatalf("应存下并换成深色：设置 %q，换过 %q", a.lib.Settings.Theme, applied)
	}

	// When 再点「浅色」
	must(t, st.Click("浅色"))
	// Then 换回浅色
	if a.lib.Settings.Theme != themeLight || applied[len(applied)-1] != themeLight {
		t.Fatalf("应存下并换成浅色：设置 %q，换过 %q", a.lib.Settings.Theme, applied)
	}
}
