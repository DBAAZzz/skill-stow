package main

import (
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"skillmanager/locales"
)

// fakeAppUpdates 是测试里的应用更新，记下检查和开关的调用。
type fakeAppUpdates struct {
	enabled, auto bool
	last          time.Time
	available     string
	checks        int
}

func (f *fakeAppUpdates) Enabled() bool              { return f.enabled }
func (f *fakeAppUpdates) Check()                     { f.checks++ }
func (f *fakeAppUpdates) AutomaticChecks() bool      { return f.auto }
func (f *fakeAppUpdates) SetAutomaticChecks(on bool) { f.auto = on }
func (f *fakeAppUpdates) LastCheck() time.Time       { return f.last }
func (f *fakeAppUpdates) Available() string          { return f.available }

func TestAppUpdatesInGeneralSettings(t *testing.T) {
	a, _ := newTester(t)
	upd := &fakeAppUpdates{enabled: true}
	a.appUpd = upd
	st := newUITester(a.settingsView, settingsWidth, 1200)

	for _, want := range []string{"应用更新", "版本 " + appVersion(), "还没有检查过", "检查更新…", "自动检查更新"} {
		if !st.HasText(want) {
			t.Errorf("通用缺少 %q：%q", want, st.Texts())
		}
	}
	if err := st.Click("检查更新…"); err != nil {
		t.Fatal(err)
	}
	if upd.checks != 1 {
		t.Fatalf("「检查更新…」应打开更新窗口检查一次，检查了 %d 次", upd.checks)
	}
	clickSwitch(t, st, "自动检查更新")
	if !upd.auto {
		t.Fatal("开关应打开后台自动检查")
	}

	t.Run("found a newer version", func(t *testing.T) {
		upd.available, upd.last = "9.9.0", time.Now().Add(-30*time.Second)
		st.Frame()
		for _, want := range []string{"9.9.0 可用", "查看更新…", "刚刚检查"} {
			if !st.HasText(want) {
				t.Errorf("有新版本时缺少 %q：%q", want, st.Texts())
			}
		}
		if err := st.Click("查看更新…"); err != nil {
			t.Fatal(err)
		}
		if upd.checks != 2 {
			t.Fatalf("「查看更新…」应打开更新窗口，检查了 %d 次", upd.checks)
		}
	})

	t.Run("cannot update itself", func(t *testing.T) {
		upd.enabled, upd.auto = false, false
		st.Frame()
		if !st.HasText("不能自行更新") || st.HasText("9.9.0 可用") {
			t.Errorf("不能更新时只说不能自行更新，不显示新版本：%q", st.Texts())
		}
		st.Click("检查更新…")
		clickSwitch(t, st, "自动检查更新")
		if upd.checks != 2 || upd.auto {
			t.Fatalf("不能更新时按钮和开关应禁用：检查 %d 次，自动检查 %v", upd.checks, upd.auto)
		}
	})
}

func TestAppUpdateCheckedLabel(t *testing.T) {
	now := time.Date(2026, 11, 18, 15, 0, 0, 0, time.Local)
	cases := []struct {
		at   time.Time
		want string
	}{
		{time.Time{}, "还没有检查过"},
		{time.Date(2026, 11, 17, 23, 0, 0, 0, time.Local), "11月17日 检查"},
	}
	for _, c := range cases {
		if got := appUpdateChecked(c.at, now); got != c.want {
			t.Errorf("appUpdateChecked(%v) = %q, want %q", c.at, got, c.want)
		}
	}
}

func TestAppUpdateStringsKeepThePluginPlaceholder(t *testing.T) {
	// 插件注册时检查占位符个数，不对会在启动时 panic
	for lang, s := range appUpdateStrings() {
		if n := strings.Count(s.UnavailableMessage, "%"); n != 1 || !strings.Contains(s.UnavailableMessage, "%[1]s") {
			t.Errorf("%s: UnavailableMessage = %q，要恰好一个 %%[1]s", lang, s.UnavailableMessage)
		}
	}
}

func clickSwitch(t *testing.T, st *ui.Tester, name string) {
	t.Helper()
	st.Frame()
	sw, ok := st.AccessByRole(ui.RoleSwitch, name)
	if !ok {
		t.Fatalf("没有开关 %q", name)
	}
	st.ClickAt(sw.Bounds.X+sw.Bounds.W/2, sw.Bounds.Y+sw.Bounds.H/2)
}

func TestAppUpdatesStayOfflineUntilTurnedOn(t *testing.T) {
	if !appUpdateOptions().DisableAutomaticChecks {
		t.Error("默认不应在后台检查更新：README 承诺只在用户操作时联网")
	}
}

func TestAppUpdatePluginRegisters(t *testing.T) {
	// 插件注册时校验 Strings，出错会在启动时 panic。插件表是进程全局的、
	// 注册了撤不掉，所以在子进程里注册，不影响本进程的其他测试和 -count=N
	if os.Getenv("APP_UPDATE_REGISTER") == "1" {
		if upd := useAppUpdates(func() {}); upd.Enabled() {
			t.Error("测试里的构建没有签名公钥，不应能自行更新")
		}
		return
	}
	cmd := exec.Command(os.Args[0], "-test.run=^TestAppUpdatePluginRegisters$", "-test.count=1", "-test.v")
	cmd.Env = append(os.Environ(), "APP_UPDATE_REGISTER=1")
	out, err := cmd.CombinedOutput()
	if err != nil || !strings.Contains(string(out), "--- PASS: TestAppUpdatePluginRegisters") {
		t.Fatalf("注册更新插件失败：%v\n%s", err, out)
	}
}

func TestSettingsGearShowsAFoundAppUpdateAndOpensGeneral(t *testing.T) {
	a, tt := newTester(t)
	upd := &fakeAppUpdates{enabled: true}
	a.appUpd = upd
	plain := locales.WithShortcut(locales.NavSettings(), "⌘,")
	found := locales.WithShortcut(locales.NavSettingsUpdateAvailable("9.9.0"), "⌘,")

	tt.Frame()
	if _, ok := tt.Find(plain); !ok {
		t.Fatalf("没有新版本时齿轮应叫 %q", plain)
	}

	upd.available = "9.9.0"
	a.set.tab = tabUpdates
	tt.Frame()
	if _, ok := tt.Find(found); !ok {
		t.Fatalf("找到新版本时齿轮的提示应带版本号 %q", found)
	}
	if err := tt.Click(found); err != nil {
		t.Fatal(err)
	}
	if !a.set.open || a.set.tab != tabGeneral {
		t.Fatalf("有新版本时点齿轮应打开 设置 › 通用：open=%v tab=%d", a.set.open, a.set.tab)
	}

	upd.enabled = false
	tt.Frame()
	if _, ok := tt.Find(found); ok {
		t.Error("不能自行更新时不提示新版本")
	}
}
