package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"skillmanager/internal/library"
)

// supportApp 是用临时主目录扫出来的应用：有数据目录和事件日志。
func supportApp(t *testing.T) (*app, string) {
	t.Helper()
	home := t.TempDir()
	a := newApp(library.Scan(home))
	a.set.open = true
	return a, home
}

func TestDiagnosticsArePreviewedBeforeTheyAreCopied(t *testing.T) {
	// Given 设置 › 通用
	a, _ := supportApp(t)
	var copied []string
	a.copyText = func(s string) error { copied = append(copied, s); return nil }
	st := newUITester(a.settingsView, settingsWidth, 900)
	for _, want := range []string{"支持与诊断", "应用数据", "诊断信息", "反馈问题"} {
		if !st.HasText(want) {
			t.Errorf("通用缺少 %q：%q", want, st.Texts())
		}
	}

	// When 点「预览并复制…」
	must(t, st.Click("预览并复制…"))
	st.Frame()

	// Then 先出预览弹窗，剪贴板还没动
	if !a.sup.open || len(copied) != 0 || !st.HasText("复制") {
		t.Fatalf("应先预览、还没复制：open=%v copied=%d", a.sup.open, len(copied))
	}
	if !strings.HasPrefix(a.sup.text, "Skill Stow diagnostics v1") {
		t.Fatalf("预览里应是诊断信息：%q", a.sup.text)
	}

	// When 确认复制
	must(t, st.Click("复制"))
	st.Frame()

	// Then 复制的就是预览里的全文，弹窗关上并提示已复制
	if len(copied) != 1 || copied[0] != a.sup.text || a.sup.open {
		t.Fatalf("应复制预览里的全文并关上弹窗：copied=%d open=%v", len(copied), a.sup.open)
	}
	if !st.HasText("诊断信息已复制") {
		t.Errorf("应提示已复制：%q", st.Texts())
	}
}

func TestReportingAnIssueCopiesDiagnosticsThenOpensTheIssueForm(t *testing.T) {
	a, _ := supportApp(t)
	var copied, opened string
	a.copyText = func(s string) error { copied = s; return nil }
	a.openURL = func(u string) { opened = u }
	st := newUITester(a.settingsView, settingsWidth, 900)

	// When 点「前往 GitHub…」，在预览里确认
	must(t, st.Click("前往 GitHub…"))
	st.Frame()
	if opened != "" {
		t.Fatal("确认之前不应打开网页")
	}
	must(t, st.Click("复制并前往 GitHub"))

	// Then 先复制，再打开带问题模板和版本的新问题页
	if copied == "" || !strings.HasPrefix(opened, appIssueURL+"?") || !strings.Contains(opened, "template=bug_report.yml") || !strings.Contains(opened, "version=") {
		t.Fatalf("应复制后打开问题模板：copied=%d opened=%q", len(copied), opened)
	}
}

func TestHelpMenuOffersSupportAndFollowsLanguage(t *testing.T) {
	t.Cleanup(func() { testLocale.Store("") })
	keepAlive := true
	a := newApp(library.Sample())
	a.keepAliveOverride = &keepAlive
	a.installAppMenu()
	m := a.sysMenu.menu

	report, diag, data := m.ItemByID(menuIDReportIssue), m.ItemByID(menuIDDiagnostics), m.ItemByID(menuIDAppData)
	if report == nil || diag == nil || data == nil {
		t.Fatalf("「帮助」菜单缺少支持项：report=%v diag=%v data=%v", report, diag, data)
	}
	if report.Label != "反馈问题…" || diag.Label != "复制诊断信息…" || data.Label != "打开应用数据文件夹" {
		t.Errorf("帮助菜单文案 = %q %q %q", report.Label, diag.Label, data.Label)
	}

	// 点「复制诊断信息…」：打开设置的通用页和预览
	a.set.tab = tabProjects
	diag.Click(diag, nil)
	if !a.set.open || a.set.tab != tabGeneral || !a.sup.open || a.sup.report {
		t.Fatalf("应打开设置的通用页和预览：set=%v tab=%d sup=%+v", a.set.open, a.set.tab, a.sup.open)
	}

	a.setLanguage("en")
	if report.Label != "Report an Issue…" || diag.Label != "Copy Diagnostic Info…" || data.Label != "Open App Data Folder" {
		t.Errorf("切到英文后帮助菜单未更新：%q %q %q", report.Label, diag.Label, data.Label)
	}
	a.setLanguage("zh-Hans")
}

func TestDamagedSettingsAreAnnouncedOnceWithTheBackup(t *testing.T) {
	// Given 写坏了的设置文件
	home := t.TempDir()
	dir := filepath.Join(home, "Library", "Application Support", "Skill Stow")
	must(t, os.MkdirAll(dir, 0o755))
	must(t, os.WriteFile(filepath.Join(dir, "settings.json"), []byte("{oops"), 0o644))

	// When 启动
	a := newApp(library.Scan(home))

	// Then 提示一次，说出备份的文件名，能在 Finder 里找到它
	ts := a.toasts.find(fromAppData)
	if ts == nil || ts.kind != toastWarn || !strings.Contains(ts.text, "settings.json") || !strings.Contains(ts.text, ".corrupt-") || ts.action == nil {
		t.Fatalf("应提示设置坏了并给出备份：%+v", ts)
	}

	// When 再扫描一次
	a.closeToast(ts)
	a.rescan()

	// Then 不再提示
	if a.toasts.find(fromAppData) != nil {
		t.Error("同一次损坏不应重复提示")
	}
}

func TestErrorsShownToTheUserAreLogged(t *testing.T) {
	a, _ := supportApp(t)

	a.notify(fromLink, toastError, "无法打开链接：boom")
	a.settingsDo(os.ErrPermission, false)

	got := a.lib.RecentEvents(10, library.LevelError)
	if len(got) != 2 || got[0].Cat != "ui" || got[0].Msg != "无法打开链接：boom" || got[1].Op != "settings" {
		t.Fatalf("界面上的错误应记进事件日志：%+v", got)
	}
}

func TestStaleDiagnosticsCallbackIsIgnoredAfterCancelAndReopen(t *testing.T) {
	a, _ := supportApp(t)
	var posts []func()
	a.bg = func(work func(post func(func()))) {
		work(func(fn func()) {
			posts = append(posts, fn)
		})
	}

	// Given 第一次打开诊断，还没回调就取消，再重新打开
	a.openDiagnostics(false)
	a.sup.open = false
	a.openDiagnostics(true)
	if len(posts) != 2 {
		t.Fatalf("应有两次后台回调排队，got %d", len(posts))
	}

	// When 旧请求的回调先回来
	posts[0]()

	// Then 旧回调被忽略：仍在 loading，report 仍为 true，text 没被旧结果覆盖
	if !a.sup.loading || a.sup.text != "" || !a.sup.report {
		t.Fatalf("过期回调不应改写新弹窗状态：%+v", a.sup)
	}

	// When 新请求的回调回来
	posts[1]()

	// Then 新弹窗完成加载并填入诊断信息
	if a.sup.loading || !strings.HasPrefix(a.sup.text, "Skill Stow diagnostics v1") {
		t.Fatalf("当前请求应正常回填诊断信息：loading=%v text=%q", a.sup.loading, a.sup.text)
	}
}
