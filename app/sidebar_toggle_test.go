package main

import (
	"testing"

	"github.com/egoist/mygo/ui"
)

func TestSidebarToggleHidesTheSidebarAndMakesRoomInTheToolbar(t *testing.T) {
	a, tt := newTester(t)
	if err := tt.Click(sidebarToggleLabel(false)); err != nil {
		t.Fatal(err)
	}
	tt.Advance(paneDuration)
	tt.Frame()
	if !a.lib.Settings.SidebarHidden {
		t.Fatal("点收起按钮应收起侧边栏")
	}
	if tt.HasText("添加项目") {
		t.Error("收起后不应再显示侧边栏")
	}
	title, ok := tt.Find("我的 Skill")
	if !ok {
		t.Fatalf("工具栏标题不见了：%q", tt.Texts())
	}
	if title.X < sidebarToggleEdge+sidebarToggleW {
		t.Errorf("工具栏标题在 x=%v，被收起按钮压住了", title.X)
	}

	if err := tt.Click(sidebarToggleLabel(true)); err != nil {
		t.Fatal(err)
	}
	tt.Advance(paneDuration)
	tt.Frame()
	if a.lib.Settings.SidebarHidden || !tt.HasText("添加项目") {
		t.Error("再点一次应展开侧边栏")
	}
}

func TestControlCommandSTogglesTheSidebarOnMac(t *testing.T) {
	if sidebarShortcut == 0 {
		t.Skip("只有 macOS 有 ⌃⌘S")
	}
	a, tt := newTester(t)
	tt.Key(sidebarShortcut, ui.KeyS)
	if !a.lib.Settings.SidebarHidden {
		t.Error("⌃⌘S 应收起侧边栏")
	}
}
