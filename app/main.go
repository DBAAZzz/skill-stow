// Skill 管理器：查看、启用和整理本机各 Agent 的技能。
//
//	go tool mygo dev    // 开发，改代码自动重载
//	go test ./...       // 行为测试；SNAPSHOT_DIR=… 时输出各页截图
//
// 默认读本机磁盘（library.Scan）；SKILLS_SAMPLE=1 时用示例数据。
package main

import (
	"log"
	"os"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"skillmanager/internal/library"
	"skillmanager/locales"
)

func main() {
	// 系统语言里既没有中文也没有英文时（如德语、日语）用英文：mygo 在都对不上
	// 时用第一个语言，默认是目录的源语言中文
	mygo.App.SetLanguages(appLanguages...)
	lib := library.Sample()
	if os.Getenv("SKILLS_SAMPLE") == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			log.Fatal(err)
		}
		lib = library.Scan(home)
	}
	a := newApp(lib)
	mygo.App.WhenReady(func() {
		applyLanguage(lib.Settings.Language) // 开窗前定下语言，第一帧就是它
		applyTheme(lib.Settings.Theme)       // 外观也是，免得开窗先闪一下另一种
		// 收成一份时移走的拷贝进系统的废纸篓，能「放回原处」
		library.Trash = func(_, path string) error { return mygo.Shell.TrashItem(path) }
		a.win = mygo.NewWindow(mygo.WindowOptions{
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
		})
		a.sound = playDoneSound
		a.win.OnBlur(func() { a.closeMenusIn(menuWinMain) })
		// 界面换语言时下一帧就重画，但窗口标题是开窗时给系统的，要跟着改
		mygo.App.OnLocaleChanged(func(string) { a.retitleWindows() })
		// 拖到侧边栏等没有接住的地方的文件夹或 zip，也切到添加技能页
		a.win.OnFileDrop(func(e *mygo.FileDropEvent) { a.win.Update(func() { a.dropToImport(e.Paths) }) })
		a.loadUsage()
		a.buildSearchIndex() // ⌘K 搜正文用，启动时在后台读一遍
		if lib.Home != "" {
			// well-known 来源行抓站点真图标，缓存里有了就直接用
			a.iconFetch = library.NewIconFetcher(lib.Settings, lib.Home)
			a.initSourceIcons()
		}
		if lib.Home != "" {
			library.CleanImportStaging(lib.Home) // 上次中途退出留下的读取暂存
			a.recoverUpdates()
			a.startWatch()
		}
	})
	// 退出后没法再撤销，清掉为撤销留的那份
	mygo.App.OnQuit(a.discardDeletion)
	mygo.App.OnQuit(a.stopWatch)
	mygo.App.OnDidBecomeActive(func() {
		a.triggerWatchIfStale(activateWatchCooldown)
		if a.win != nil && a.scope == scopeImport {
			a.win.Update(a.checkClipboard)
		}
	})
	if err := mygo.App.Run(); err != nil {
		log.Fatal(err)
	}
}
