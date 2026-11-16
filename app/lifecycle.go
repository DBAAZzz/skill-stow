package main

import (
	_ "embed"
	"encoding/json"
	"runtime"
	"sync"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"skillmanager/internal/library"
	"skillmanager/locales"
)

//go:embed mygo.json
var bundleConfigJSON []byte

type bundleMetadata struct {
	Name       string `json:"name"`
	Identifier string `json:"identifier"`
	Version    string `json:"version"`
	Copyright  string `json:"copyright"`
}

var loadBundleMetadata = sync.OnceValue(func() bundleMetadata {
	var meta bundleMetadata
	_ = json.Unmarshal(bundleConfigJSON, &meta)
	return meta
})

// initAppIdentity 在启动最初把 mygo.json 的应用名和版本交给 mygo.App：
// 打包版（mygo build）和开发版（mygo dev）的锁键仍由各自 bundle identifier 区分，
// 但菜单栏、关于面板与未打包二进制统一拿到真实应用名和版本号。
func initAppIdentity() {
	meta := loadBundleMetadata()
	if meta.Name != "" {
		mygo.App.SetName(meta.Name)
	}
	if mygo.App.Version() == "" && meta.Version != "" {
		mygo.App.SetVersion(meta.Version)
	}
}

// startSingleInstance 先抢单实例锁，拿到锁才执行 load（扫描磁盘或加载示例数据）；
// 二次启动进程直接返回 false 退出，不触碰本机目录与配置。
func startSingleInstance(lock func() bool, load func() *library.Library) (*app, bool) {
	if !lock() {
		return nil, false
	}
	return newApp(load()), true
}

type mainWindowState struct {
	open      bool
	minimized bool
	focused   bool
}

func (a *app) keepAliveOnWindowClose() bool {
	if a.keepAliveOverride != nil {
		return *a.keepAliveOverride
	}
	return runtime.GOOS == "darwin"
}

// post 在主线程执行 fn 并刷新当前打开的窗口；无事件循环的单元测试里就地执行。
func (a *app) post(fn func()) {
	if !a.live {
		fn()
		a.changed()
		return
	}
	mygo.App.WhenReady(func() {
		fn()
		a.changed()
	})
}

func (a *app) mainWindowOptions() mygo.WindowOptions {
	return mygo.WindowOptions{
		Title:     locales.AppName(),
		Width:     1280,
		Height:    800,
		MinWidth:  960,
		MinHeight: 600,
		StateKey:  "main",
		// 隐藏标题栏：macOS 红绿灯嵌在侧边栏顶部；其它平台降级为
		// hidden，窗口按钮在右上角，由工具栏读 c.TitleBar() 让位。
		TitleBarStyle:  mygo.TitleBarHiddenInset,
		TitleBarHeight: titleBarHeight,
		// 系统给这种窗口的是 38 高的紧凑标题栏，红绿灯中心在 19，比 52
		// 高的顶栏（标题、收起侧边栏的按钮）中心高 7。挪到和顶栏同一条
		// 中线上，像备忘录。
		TrafficLightPosition: &mygo.Point{X: 20, Y: (titleBarHeight - trafficLightSize) / 2},
		Vibrancy:             mygo.VibrancySidebar,
		Content:              ui.View(a.view),
	}
}

// openMainWindow 创建并显示主窗口；主窗口已开着时只聚焦，不重复建窗。
func (a *app) openMainWindow() *mygo.Window {
	if a.main.open && (!a.live || (a.win != nil && !a.win.IsDestroyed())) {
		a.focusMainWindow()
		return a.win
	}
	a.closeMenusIn(menuWinMain)
	a.main.open = true
	a.main.minimized = false
	a.main.focused = true
	if a.set.open {
		a.set.focused = false
	}
	if !a.live {
		a.syncDockProgress(nil)
		return nil
	}
	win := mygo.NewWindow(a.mainWindowOptions())
	a.win = win
	a.syncDockProgress(win)
	win.OnFocus(func() {
		a.main.focused = true
		a.set.focused = false
	})
	win.OnBlur(func() {
		a.main.focused = false
		a.closeMenusIn(menuWinMain)
	})
	win.OnMinimize(func() {
		a.main.minimized = true
		a.main.focused = false
	})
	win.OnRestore(func() {
		a.main.minimized = false
	})
	// 拖到侧边栏等没有接住的地方的文件夹或 zip，也切到添加技能页
	win.OnFileDrop(func(e *mygo.FileDropEvent) {
		a.post(func() { a.dropToImport(e.Paths) })
	})
	win.OnClose(func(*mygo.CloseEvent) {
		a.clearDockProgressOnLastWindowClose(win, a.setWin != nil && !a.setWin.IsDestroyed())
	})
	win.OnClosed(func() {
		if a.win == win {
			a.win = nil
		}
		a.onMainWindowClosed()
	})
	return win
}

func (a *app) focusMainWindow() {
	if !a.main.open {
		return
	}
	a.main.focused = true
	if a.set.open {
		a.set.focused = false
	}
	if a.win != nil && !a.win.IsDestroyed() {
		a.win.Focus()
	}
}

func (a *app) closeMainWindow() {
	if w := a.win; w != nil {
		w.Close()
		return
	}
	if a.main.open {
		a.clearDockProgressOnLastWindowClose(nil, a.set.open)
		a.onMainWindowClosed()
	}
}

func (a *app) onMainWindowClosed() {
	a.closeMenusIn(menuWinMain)
	a.closePalette()
	a.main.open = false
	a.main.minimized = false
	a.main.focused = false
	if !a.keepAliveOnWindowClose() {
		if a.set.open || a.setWin != nil {
			a.closeSettings()
		}
		if !a.live {
			a.shutdown()
		}
		return
	}
	if a.set.open && !a.set.minimized {
		a.set.focused = true
	}
}

// restoreMainWindow 在二次实例启动或需要恢复主窗口时，确保主窗口存在、取消最小化并聚焦。
func (a *app) restoreMainWindow() {
	if a.live && !mygo.App.IsReady() {
		return
	}
	if a.live && mygo.App.IsHidden() {
		mygo.App.Show()
	}
	if !a.main.open || (a.live && (a.win == nil || a.win.IsDestroyed())) {
		a.openMainWindow()
		return
	}
	if a.main.minimized || (a.win != nil && a.win.IsMinimized()) {
		a.main.minimized = false
		if a.win != nil {
			a.win.Restore()
		}
	}
	if a.win != nil && !a.win.IsVisible() {
		a.win.Show()
	}
	a.focusMainWindow()
}

// handleActivate 处理 macOS 点击 Dock 图标等重激活事件：
// 主窗口已关闭（哪怕设置窗口仍开着）、主窗口已最小化或无可见窗口时恢复主窗口；
// 主窗口已可见时不重复建窗。
func (a *app) handleActivate(hasVisibleWindows bool) {
	if !a.main.open || a.main.minimized || !hasVisibleWindows {
		a.restoreMainWindow()
		return
	}
	if a.win != nil && (a.win.IsDestroyed() || a.win.IsMinimized() || !a.win.IsVisible()) {
		a.restoreMainWindow()
		return
	}
	if !a.set.open || !a.set.focused {
		a.focusMainWindow()
	}
}

func (a *app) handleDidBecomeActive() {
	a.triggerWatchIfStale(activateWatchCooldown)
	if a.main.open && a.scope == scopeImport {
		a.post(a.checkClipboard)
	}
}

// shutdown 在真正退出应用（⌘Q）时清理待撤销删除副本并停止后台目录监听。
func (a *app) shutdown() {
	if a.quitting {
		return
	}
	a.quitting = true
	a.discardDeletion()
	a.stopWatch()
}

func (a *app) bindLifecycle() {
	if a.keepAliveOnWindowClose() {
		mygo.App.OnWindowAllClosed(func() {})
		mygo.App.OnActivate(a.handleActivate)
	}
	mygo.App.OnSecondInstance(func([]string, string) {
		a.restoreMainWindow()
	})
	// 退出后没法再撤销，清掉为撤销留的那份，并停止监听
	mygo.App.OnQuit(a.shutdown)
	mygo.App.OnDidBecomeActive(a.handleDidBecomeActive)
}

func (a *app) startLive() {
	a.live = true
	applyLanguage(a.lib.Settings.Language) // 开窗前定下语言，第一帧就是它
	applyTheme(a.lib.Settings.Theme)       // 外观也是，免得开窗先闪一下另一种
	// 收成一份时移走的拷贝进系统的废纸篓，能「放回原处」
	library.Trash = func(_, path string) error { return mygo.Shell.TrashItem(path) }
	a.sound = playDoneSound
	a.openMainWindow()
	// 界面换语言时下一帧就重画，但窗口标题是给系统的，要跟着改
	mygo.App.OnLocaleChanged(func(string) {
		a.retitleWindows()
	})
	a.loadUsage()
	a.buildSearchIndex() // ⌘K 搜正文用，启动时在后台读一遍
	if a.lib.Home != "" {
		// well-known 来源行抓站点真图标，缓存里有了就直接用
		a.iconFetch = library.NewIconFetcher(a.lib.Settings, a.lib.Home)
		a.initSourceIcons()
		library.CleanImportStaging(a.lib.Home) // 上次中途退出留下的读取暂存
		a.recoverUpdates()
		a.startWatch()
	}
}
