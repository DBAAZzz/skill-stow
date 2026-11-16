// Skill Stow：查看、启用和整理本机各 Agent 的技能。
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

	"skillmanager/internal/library"
)

func main() {
	initAppIdentity()
	// 系统语言里既没有中文也没有英文时（如德语、日语）用英文：mygo 在都对不上
	// 时用第一个语言，默认是目录的源语言中文
	mygo.App.SetLanguages(appLanguages...)
	a, ok := startSingleInstance(mygo.App.RequestSingleInstanceLock, func() *library.Library {
		if os.Getenv("SKILLS_SAMPLE") != "" {
			return library.Sample()
		}
		home, err := os.UserHomeDir()
		if err != nil {
			log.Fatal(err)
		}
		return library.Scan(home)
	})
	if !ok {
		return
	}
	a.bindLifecycle()
	mygo.App.WhenReady(func() {
		a.startLive()
	})
	if err := mygo.App.Run(); err != nil {
		log.Fatal(err)
	}
}
