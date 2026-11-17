package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/egoist/mygo"

	"skillmanager/internal/library"
)

func TestMacOSClosingLastWindowKeepsAppAliveAndDockRestoresMainWindow(t *testing.T) {
	keepAlive := true
	a := newApp(library.Sample())
	a.keepAliveOverride = &keepAlive

	// Given 启动后打开主窗口，选中一个技能并展开菜单与欢迎面板
	a.openMainWindow()
	if !a.main.open || !a.main.focused {
		t.Fatalf("启动后主窗口状态异常：%+v", a.main)
	}
	a.scope = scopeTidy
	a.selected = a.lib.Get("test-first")
	*a.menuOpen("browse.filter") = true
	a.wel.open = true
	if !a.wel.open || !a.windowMenuOpen(menuWinMain) {
		t.Fatal("前置状态应已打开欢迎面板和主窗口菜单")
	}

	// When 在 macOS 上关闭主窗口
	a.closeMainWindow()

	// Then 进程不退出，主窗口与附属弹层收起，应用状态保留
	if a.quitting {
		t.Fatal("macOS 关闭主窗口后进程不应退出")
	}
	if a.main.open || a.main.focused || a.wel.open || a.windowMenuOpen(menuWinMain) {
		t.Fatalf("主窗口关闭后应清理窗口与弹层状态：main=%+v wel=%v menu=%v",
			a.main, a.wel.open, a.windowMenuOpen(menuWinMain))
	}

	// When 点击 Dock 图标激活应用（当前无可见窗口）
	a.handleActivate(false)

	// Then 重新打开并聚焦主窗口，之前所在的页面与选中项完整恢复
	if !a.main.open || !a.main.focused {
		t.Fatalf("Dock 激活后应重建并聚焦主窗口：%+v", a.main)
	}
	if a.scope != scopeTidy || a.selected == nil || a.selected.Name != "test-first" {
		t.Fatalf("重建主窗口后应保留应用状态：scope=%q selected=%v", a.scope, a.selected)
	}

	// When 主窗口已可见时重复点击 Dock 或收到二次启动通知
	a.handleActivate(true)
	a.restoreMainWindow()
	if !a.main.open || !a.main.focused {
		t.Fatalf("重复激活应保持主窗口打开并聚焦：%+v", a.main)
	}
}

func TestMacOSLifecycleHandlesSettingsWindowAndMinimizedMainWindow(t *testing.T) {
	keepAlive := true
	a := newApp(library.Sample())
	a.keepAliveOverride = &keepAlive

	// Given 主窗口与设置窗口同时打开，随后关闭主窗口
	a.openMainWindow()
	a.openSettings()
	if !a.main.open || !a.set.open {
		t.Fatalf("主窗口与设置窗口应同时打开：main=%+v set=%+v", a.main, a.set)
	}
	a.closeMainWindow()

	// Then 设置窗口依然保持打开且可用，即使主窗口已关闭
	if a.main.open || !a.set.open || !a.set.focused || a.quitting {
		t.Fatalf("关闭主窗口后设置窗口应保持打开并获得焦点：main=%+v set=%+v", a.main, a.set)
	}

	// When 设置窗口仍可见（hasVisibleWindows=true）时点击 Dock 图标
	a.handleActivate(true)

	// Then 依然能恢复已关闭的主窗口，且设置窗口不被关闭
	if !a.main.open || !a.main.focused || !a.set.open {
		t.Fatalf("设置窗口开着时点 Dock 仍应恢复主窗口：main=%+v set=%+v", a.main, a.set)
	}

	// When 主窗口最小化，再点击 Dock 图标（无论是否有其它可见窗口）
	a.main.minimized = true
	a.main.focused = false
	a.handleActivate(true)

	// Then 最小化的主窗口被直接恢复并聚焦
	if !a.main.open || a.main.minimized || !a.main.focused {
		t.Fatalf("最小化主窗口经 Dock 激活应恢复并聚焦：%+v", a.main)
	}

	// When 关闭所有窗口后，通过菜单或 ⌘, 直接打开设置窗口
	a.closeSettings()
	a.closeMainWindow()
	a.openSettings()
	if !a.set.open || !a.set.focused || a.main.open {
		t.Fatalf("无主窗口时也应能独立打开设置窗口：main=%+v set=%+v", a.main, a.set)
	}
}

func TestDockProgressClearsOnLastWindowCloseAndSyncsOnReopen(t *testing.T) {
	keepAlive := true

	// 路径 1：无设置窗口时，整理运行中关闭主窗口 → 任务结束 → Dock 无残留 → 恢复主窗口
	t.Run("no_settings_window", func(t *testing.T) {
		a := newApp(library.Sample())
		a.keepAliveOverride = &keepAlive
		var work func(post func(func()))
		a.bg = func(w func(post func(func()))) { work = w }

		a.openMainWindow()
		a.mergeAll(a.lib.Problems().Same)
		if work == nil || a.tidy.appliedDock.State != mygo.ProgressNormal || a.tidy.appliedDock.Value != 0 {
			t.Fatalf("整理启动后 Dock 应显示初始进度：applied=%+v want=%+v", a.tidy.appliedDock, a.tidy.wantDock)
		}

		// 关闭唯一主窗口：销毁前（OnClose）立即清除 Dock 进度，但业务期望进度仍保留
		a.closeMainWindow()
		if a.tidy.appliedDock != (mygo.ProgressBar{}) {
			t.Fatalf("关闭最后一个窗口时应清除 Dock 进度条，实际 applied=%+v", a.tidy.appliedDock)
		}
		if a.tidy.wantDock.State != mygo.ProgressNormal {
			t.Fatalf("后台任务未结束前业务期望进度应保留，实际 want=%+v", a.tidy.wantDock)
		}

		// 若在任务进行中恢复主窗口，Dock 进度按业务状态恢复
		a.handleActivate(false)
		if a.tidy.appliedDock.State != mygo.ProgressNormal {
			t.Fatalf("任务进行中重开主窗口应恢复 Dock 进度，实际 applied=%+v", a.tidy.appliedDock)
		}

		// 再次关闭主窗口后后台任务完成：Dock 保持无残留，恢复主窗口后也无残留
		a.closeMainWindow()
		if a.tidy.appliedDock != (mygo.ProgressBar{}) {
			t.Fatalf("再次关闭主窗口时应清除 Dock 进度，实际 applied=%+v", a.tidy.appliedDock)
		}
		work(a.post)
		if a.tidy.work != nil || a.tidy.wantDock != (mygo.ProgressBar{}) || a.tidy.appliedDock != (mygo.ProgressBar{}) {
			t.Fatalf("无窗口期间任务完成后不应残留进度：merging=%v want=%+v applied=%+v",
				a.tidy.work, a.tidy.wantDock, a.tidy.appliedDock)
		}
		a.handleActivate(false)
		if a.tidy.appliedDock != (mygo.ProgressBar{}) {
			t.Fatalf("任务结束后恢复主窗口 Dock 不应有残留进度，实际 applied=%+v", a.tidy.appliedDock)
		}
	})

	// 路径 2：设置窗口开着时关闭主窗口 → 任务推进/结束通过设置窗口更新并清除 Dock（或设置窗口也关闭时清除）
	t.Run("with_settings_window_open", func(t *testing.T) {
		a := newApp(library.Sample())
		a.keepAliveOverride = &keepAlive
		var work func(post func(func()))
		a.bg = func(w func(post func(func()))) { work = w }

		a.openMainWindow()
		a.openSettings()
		a.mergeAll(a.lib.Problems().Same)

		// 关闭主窗口但设置窗口仍开着：Dock 进度不中断，继续由设置窗口承接
		a.closeMainWindow()
		if a.tidy.appliedDock.State != mygo.ProgressNormal {
			t.Fatalf("设置窗口仍开着时关闭主窗口不应提前清掉进度：applied=%+v", a.tidy.appliedDock)
		}

		// 任务在仅有设置窗口时结束：通过设置窗口正常清掉 Dock 进度
		work(a.post)
		if a.tidy.appliedDock != (mygo.ProgressBar{}) || a.tidy.wantDock != (mygo.ProgressBar{}) {
			t.Fatalf("仅设置窗口开着时任务完成也应清掉 Dock 进度：want=%+v applied=%+v",
				a.tidy.wantDock, a.tidy.appliedDock)
		}
		a.handleActivate(true)
		if a.tidy.appliedDock != (mygo.ProgressBar{}) {
			t.Fatalf("恢复主窗口后 Dock 应保持无残留：applied=%+v", a.tidy.appliedDock)
		}

		// 若在任务结束前连设置窗口（最后一个窗口）也关掉，则关设置窗口时立即清除 Dock 进度
		a.dockProgress(0.4)
		a.closeMainWindow()
		if a.tidy.appliedDock.Value != 0.4 {
			t.Fatalf("设置窗口仍在时应保留 0.4 进度：%+v", a.tidy.appliedDock)
		}
		a.closeSettings()
		if a.tidy.appliedDock != (mygo.ProgressBar{}) {
			t.Fatalf("关闭最后一个设置窗口时应清除 Dock 进度：%+v", a.tidy.appliedDock)
		}
	})
}

func TestClosedMainWindowSafelyHandlesAsyncCallbacksAndQuitCleanup(t *testing.T) {
	keepAlive := true
	home := makeTempSkillHome(t)
	a := newApp(library.Scan(home))
	a.keepAliveOverride = &keepAlive
	t.Cleanup(func() { testLocale.Store("") })

	var mu sync.Mutex
	a.watchCfg = &watchConfig{
		debounce:       30 * time.Millisecond,
		maxDelay:       100 * time.Millisecond,
		waitScanOnStop: true,
		post: func(fn func()) {
			mu.Lock()
			defer mu.Unlock()
			fn()
		},
	}
	a.startWatch()
	defer a.stopWatch()
	firstHub := a.watch.hub.Load()
	if firstHub == nil {
		t.Fatal("应已启动后台目录监听")
	}
	firstHub.waitSynced()

	// 捕获后台任务回调，模拟主窗口关闭期间异步任务完成
	var pendingBg func(post func(func()))
	a.bg = func(w func(post func(func()))) { pendingBg = w }

	a.openMainWindow()
	a.probeGHAuth(true)
	a.closeMainWindow()

	// 2. 主窗口关闭期间后台任务回调落地不应崩溃，且状态正常更新
	if pendingBg == nil {
		t.Fatal("应已捕获后台任务")
	}
	pendingBg(a.post)
	if a.set.ghProbing || !a.set.ghChecked {
		t.Fatalf("主窗口关闭期间后台任务回调应正常完成：%+v", a.set)
	}

	// 3. 主窗口关闭期间磁盘新增技能，监听正常合入；Dock 恢复主窗口后不重复建 watcher
	added := filepath.Join(home, ".agents/skills/closed-win-skill")
	must(t, os.MkdirAll(added, 0o755))
	must(t, os.WriteFile(filepath.Join(added, "SKILL.md"), []byte("---\nname: closed-win-skill\ndescription: added while closed\n---\n"), 0o644))

	waitUntil(t, 4*time.Second, "关窗期间新技能同步进 app.lib", func() bool {
		mu.Lock()
		defer mu.Unlock()
		return a.lib.ByID("mine:closed-win-skill") != nil
	})

	a.handleActivate(false)
	if !a.main.open || a.watch.hub.Load() != firstHub {
		t.Fatal("Dock 恢复主窗口后应复用原有目录监听，不重复启动 watcher")
	}

	// 4. 准备一条待撤销删除后执行 ⌘Q 真退出（shutdown）：应清理撤销副本并停止监听
	target := a.lib.ByID("mine:closed-win-skill")
	if target == nil {
		t.Fatal("未找到新技能")
	}
	undo, err := a.lib.Delete(target)
	must(t, err)
	a.del.last = undo
	a.shutdown()
	if !a.quitting || a.del.last != nil || a.watch.hub.Load() != nil {
		t.Fatalf("真退出后应清理待撤销删除并停止监听：quitting=%v last=%v hub=%v",
			a.quitting, a.del.last, a.watch.hub.Load())
	}
}

func TestNonMacOSClosingMainWindowClosesSettingsAndQuits(t *testing.T) {
	keepAlive := false
	a := newApp(library.Sample())
	a.keepAliveOverride = &keepAlive

	a.openMainWindow()
	a.openSettings()
	a.closeMainWindow()

	if a.main.open || a.set.open || !a.quitting {
		t.Fatalf("非 macOS 关闭主窗口应同步收起设置窗口并退出：main=%+v set=%+v quitting=%v",
			a.main, a.set, a.quitting)
	}
}

func TestMacOSSystemMenuIncludesAboutSettingsUpdatesAndFollowsLanguage(t *testing.T) {
	t.Cleanup(func() { testLocale.Store("") })
	initAppIdentity()

	meta := loadBundleMetadata()
	if meta.Name == "" || meta.Version == "" {
		t.Fatalf("mygo.json 元数据缺失：%+v", meta)
	}

	keepAlive := true
	a := newApp(library.Sample())
	a.keepAliveOverride = &keepAlive
	a.closeMainWindow()
	a.installAppMenu()

	m := a.sysMenu.menu
	if m == nil {
		t.Fatal("macOS 应构建系统应用菜单")
	}

	// 1. 保留默认顶层菜单（应用、文件、编辑、显示、窗口）及标准角色
	var topRoles []mygo.MenuRole
	for _, it := range m.Items() {
		topRoles = append(topRoles, it.Role)
	}
	wantTop := []mygo.MenuRole{
		mygo.RoleAppMenu,
		mygo.RoleFileMenu,
		mygo.RoleEditMenu,
		mygo.RoleViewMenu,
		mygo.RoleWindowMenu,
	}
	if !slices.Equal(topRoles, wantTop) {
		t.Fatalf("顶层系统菜单角色 = %v, want %v", topRoles, wantTop)
	}

	var appRoles []mygo.MenuRole
	for _, it := range m.Items()[0].Submenu {
		if it.Role != "" {
			appRoles = append(appRoles, it.Role)
		}
	}
	for _, want := range []mygo.MenuRole{mygo.RoleServices, mygo.RoleHide, mygo.RoleHideOthers, mygo.RoleUnhide, mygo.RoleQuit} {
		if !slices.Contains(appRoles, want) {
			t.Errorf("应用菜单缺少标准角色 %q：%v", want, appRoles)
		}
	}

	var editRoles []mygo.MenuRole
	for _, it := range m.Items()[2].Submenu {
		if it.Role != "" {
			editRoles = append(editRoles, it.Role)
		}
	}
	for _, want := range []mygo.MenuRole{mygo.RoleUndo, mygo.RoleRedo, mygo.RoleCut, mygo.RoleCopy, mygo.RolePaste, mygo.RoleSelectAll} {
		if !slices.Contains(editRoles, want) {
			t.Errorf("编辑菜单缺少标准角色 %q：%v", want, editRoles)
		}
	}

	// 2. 检查中文（默认源语言）下的自定义菜单项文案与快捷键
	aboutItem := m.ItemByID(menuIDAbout)
	updatesItem := m.ItemByID(menuIDCheckUpdates)
	settingsItem := m.ItemByID(menuIDSettings)
	if aboutItem == nil || updatesItem == nil || settingsItem == nil {
		t.Fatalf("缺少自定义菜单项：about=%v updates=%v settings=%v", aboutItem, updatesItem, settingsItem)
	}
	if aboutItem.Label != "关于 "+meta.Name {
		t.Errorf("关于菜单文案 = %q, want %q", aboutItem.Label, "关于 "+meta.Name)
	}
	if updatesItem.Label != "检查应用更新…" {
		t.Errorf("检查更新菜单文案 = %q, want %q", updatesItem.Label, "检查应用更新…")
	}
	if settingsItem.Label != "设置…" || settingsItem.Accelerator != "CmdOrCtrl+," {
		t.Errorf("设置菜单项 = (%q, %q), want (\"设置…\", \"CmdOrCtrl+,\")", settingsItem.Label, settingsItem.Accelerator)
	}

	// 3. 点击「关于」：显示正确应用名与 mygo.json 当前版本，不散落硬编码
	var gotAbout mygo.AboutPanelOptions
	a.onAbout = func(opts mygo.AboutPanelOptions) { gotAbout = opts }
	aboutItem.Click(aboutItem, nil)
	if gotAbout.ApplicationName != meta.Name || gotAbout.ApplicationVersion != meta.Version || gotAbout.Copyright != meta.Copyright {
		t.Fatalf("关于面板参数 = %+v, want Name=%q Version=%q Copyright=%q",
			gotAbout, meta.Name, meta.Version, meta.Copyright)
	}

	// 4. 点击「检查应用更新…」：打开仓库官方 Releases 页面
	var openedURL string
	a.openURL = func(u string) { openedURL = u }
	updatesItem.Click(updatesItem, nil)
	if openedURL != appReleasesURL {
		t.Fatalf("检查应用更新打开了 %q, want %q", openedURL, appReleasesURL)
	}

	// 5. 点击「设置…」：即使主窗口未打开也能打开设置窗口
	settingsItem.Click(settingsItem, nil)
	if !a.set.open {
		t.Fatal("点击系统菜单「设置…」应打开设置窗口")
	}

	// 6. 切换语言到 English 再切回简体中文，自定义菜单即时更新
	a.openMainWindow()
	a.setLanguage("en")
	if aboutItem.Label != "About "+meta.Name || updatesItem.Label != "Check for App Updates…" || settingsItem.Label != "Settings…" {
		t.Fatalf("切到英文后系统菜单未更新：about=%q updates=%q settings=%q",
			aboutItem.Label, updatesItem.Label, settingsItem.Label)
	}

	a.setLanguage("zh-Hans")
	if aboutItem.Label != "关于 "+meta.Name || updatesItem.Label != "检查应用更新…" || settingsItem.Label != "设置…" {
		t.Fatalf("切回中文后系统菜单未更新：about=%q updates=%q settings=%q",
			aboutItem.Label, updatesItem.Label, settingsItem.Label)
	}
}

func TestSingleInstanceHelperProcess(t *testing.T) {
	mode := os.Getenv("SKILLSTOW_TEST_SINGLE_INSTANCE")
	if mode == "" {
		return
	}
	initAppIdentity()
	switch mode {
	case "first":
		if !mygo.App.RequestSingleInstanceLock() {
			fmt.Println("FIRST_LOCK_FAILED")
			os.Exit(2)
		}
		fmt.Println("FIRST_LOCKED")
		// 等待父测试进程通知退出
		buf := make([]byte, 1)
		_, _ = os.Stdin.Read(buf)
		os.Exit(0)
	case "second":
		scanned := false
		_, ok := startSingleInstance(mygo.App.RequestSingleInstanceLock, func() *library.Library {
			scanned = true
			return library.Sample()
		})
		if ok || scanned {
			fmt.Printf("SECOND_UNEXPECTED ok=%v scanned=%v\n", ok, scanned)
			os.Exit(3)
		}
		fmt.Println("SECOND_EXITED_BEFORE_SCAN")
		os.Exit(0)
	}
}

func TestSingleInstanceLockExitsSecondProcessBeforeScan(t *testing.T) {
	// 使用短路径临时目录隔离单实例 Unix socket（避免超过 100 字节上限或干扰正在运行的应用）
	sockDir, err := os.MkdirTemp("/tmp", "ss-lock-")
	must(t, err)
	t.Cleanup(func() { _ = os.RemoveAll(sockDir) })

	// 1. 启动首个实例子进程持有单实例锁
	first := exec.Command(os.Args[0], "-test.run=^TestSingleInstanceHelperProcess$")
	first.Env = append(os.Environ(),
		"SKILLSTOW_TEST_SINGLE_INSTANCE=first",
		"XDG_RUNTIME_DIR="+sockDir,
	)
	stdin, err := first.StdinPipe()
	must(t, err)
	stdout, err := first.StdoutPipe()
	must(t, err)
	must(t, first.Start())
	t.Cleanup(func() {
		_ = stdin.Close()
		_ = first.Process.Kill()
		_ = first.Wait()
	})

	buf := make([]byte, 64)
	n, err := stdout.Read(buf)
	if err != nil || !strings.Contains(string(buf[:n]), "FIRST_LOCKED") {
		t.Fatalf("首实例未成功获取单实例锁：out=%q err=%v", string(buf[:n]), err)
	}

	// 2. 启动第二个实例子进程：必须在扫描/加载前退出
	second := exec.Command(os.Args[0], "-test.run=^TestSingleInstanceHelperProcess$")
	second.Env = append(os.Environ(),
		"SKILLSTOW_TEST_SINGLE_INSTANCE=second",
		"XDG_RUNTIME_DIR="+sockDir,
	)
	out, err := second.CombinedOutput()
	if err != nil {
		t.Fatalf("二次启动进程异常退出：%v, out=%s", err, out)
	}
	if !strings.Contains(string(out), "SECOND_EXITED_BEFORE_SCAN") {
		t.Fatalf("二次启动进程未在扫描前退出：%s", out)
	}

	// 3. 释放首实例后，新实例可正常获取锁并加载库
	_ = stdin.Close()
	_ = first.Wait()

	third := exec.Command(os.Args[0], "-test.run=^TestSingleInstanceHelperProcess$")
	third.Env = append(os.Environ(),
		"SKILLSTOW_TEST_SINGLE_INSTANCE=first",
		"XDG_RUNTIME_DIR="+sockDir,
	)
	thirdIn, err := third.StdinPipe()
	must(t, err)
	thirdOut, err := third.StdoutPipe()
	must(t, err)
	must(t, third.Start())
	n, err = thirdOut.Read(buf)
	_ = thirdIn.Close()
	_ = third.Wait()
	if err != nil || !strings.Contains(string(buf[:n]), "FIRST_LOCKED") {
		t.Fatalf("首实例退出后新实例未能获取锁：out=%q err=%v", string(buf[:n]), err)
	}
}
