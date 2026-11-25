// sitegen 生成官网 skillstow.xyz 的静态页面。
//
// 页面模板和文案在仓库根的 site/，截图取自 docs/screenshots/，Agent 页面的数据
// 直接来自 library.Catalog：加了 Agent，重新生成就多一页。
//
//	cd app && go run ./tools/sitegen            # 输出到 ../site/dist
//	cd app && go run ./tools/sitegen -serve :8788  # 生成后在本机预览
package main

import (
	"flag"
	"fmt"
	"log"
	"net/http"
	"path/filepath"
)

func main() {
	site := flag.String("site", "../site", "模板、文案和静态文件所在目录")
	shots := flag.String("shots", "../docs/screenshots", "README 截图目录，按语言分子目录")
	out := flag.String("out", "../site/dist", "输出目录，生成前会清空")
	base := flag.String("base", "https://skillstow.xyz", "站点根地址，用于 canonical、sitemap 和结构化数据")
	app := flag.String("app", "mygo.json", "应用清单，取版本号")
	serve := flag.String("serve", "", "生成后在这个地址上提供预览，例如 :8788")
	flag.Parse()

	pages, err := build(config{Site: *site, Shots: *shots, Out: *out, Base: *base, App: *app})
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("sitegen: %d pages -> %s\n", pages, *out)
	if *serve != "" {
		fmt.Printf("sitegen: serving http://localhost%s\n", *serve)
		log.Fatal(http.ListenAndServe(*serve, http.FileServer(http.Dir(filepath.Clean(*out)))))
	}
}
