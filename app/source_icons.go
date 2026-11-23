package main

import (
	"context"
	"strings"

	"github.com/egoist/mygo/ui"

	"skillmanager/internal/library"
)

// 来源身份的图标（src2-b）：well-known 来源行显示站点自己的图标，卡片
// footer 和详情栏显示 GitHub 作者头像或站点 logo。iconFetch 在 main 里建好
// （测试里为 nil，一切不启动），启动和每次更新报告刷新后把没图标的排进
// 后台抓取，抓到的缓存进 srcIcons：键是 host，或 gh:<owner>。磁盘缓存归
// IconFetcher 管（icons/<host>.png|svg、icons/github/<owner>.png），下次
// 启动直接读，不再请求。

// initSourceIcons 在启动时给初始报告里的网站来源抓图标。
func (a *app) initSourceIcons() {
	a.ensureSourceIcons()
}

// ensureSourceIcons 给更新报告里的网站来源和「我的」技能安装记录里的来源
// （GitHub owner、well-known 站点）排抓取任务。在界面线程上调用（post 块
// 里），幂等：抓过的和抓着的键不重复排。
func (a *app) ensureSourceIcons() {
	if a.iconFetch == nil {
		return
	}
	if a.srcIcons == nil {
		a.srcIcons = map[string]ui.ImageSource{}
	}
	for _, it := range a.upd.report.Items {
		if it.State != library.UpdWebsite {
			continue
		}
		a.scheduleSourceIcon(it.Source.Repo, func(ctx context.Context) error {
			_, err := a.iconFetch.Fetch(ctx, it.Record.SourceBaseURL, it.Source.Repo)
			return err
		})
	}
	if a.lib != nil {
		for _, s := range a.lib.Skills {
			if s.Source != library.Mine {
				continue
			}
			rec, ok := a.lib.InstallRecord(s.Name)
			if !ok {
				continue
			}
			switch rec.SourceType {
			case "github":
				owner, _, _ := strings.Cut(rec.Source, "/")
				a.scheduleSourceIcon("gh:"+owner, func(ctx context.Context) error {
					_, err := a.iconFetch.FetchAvatar(ctx, owner)
					return err
				})
			case "well-known":
				a.scheduleSourceIcon(rec.Source, func(ctx context.Context) error {
					_, err := a.iconFetch.Fetch(ctx, rec.SourceBaseURL, rec.Source)
					return err
				})
			case "clawhub":
				a.scheduleSourceIcon("clawhub.ai", func(ctx context.Context) error {
					_, err := a.iconFetch.Fetch(ctx, "https://clawhub.ai", "clawhub.ai")
					return err
				})
			}
		}
	}
}

// scheduleSourceIcon 排一个键的抓取任务，抓着的键不重复排。
func (a *app) scheduleSourceIcon(key string, fetch func(context.Context) error) {
	if key == "" || a.srcIcons[key] != nil || a.srcIconBusy[key] {
		return
	}
	if a.srcIconBusy == nil {
		a.srcIconBusy = map[string]bool{}
	}
	a.srcIconBusy[key] = true
	f := a.iconFetch
	a.background(func(post func(func())) {
		var data []byte
		var ok bool
		if strings.HasPrefix(key, "gh:") {
			data, ok = f.LoadAvatar(strings.TrimPrefix(key, "gh:"))
		} else {
			data, ok = f.Load(key)
		}
		if !ok {
			if err := fetch(context.Background()); err != nil {
				post(func() { a.srcIconBusy[key] = false })
				return
			}
			if strings.HasPrefix(key, "gh:") {
				data, ok = f.LoadAvatar(strings.TrimPrefix(key, "gh:"))
			} else {
				data, ok = f.Load(key)
			}
			if !ok {
				post(func() { a.srcIconBusy[key] = false })
				return
			}
		}
		// 位图要在画它之前建好（在界面线程外），仿 loadUsage 的做法。
		var src ui.ImageSource
		if decoded, err := ui.DecodeImage(data); err == nil {
			src = decoded
		}
		post(func() {
			a.srcIconBusy[key] = false
			if src != nil {
				a.srcIcons[key] = src
			}
			a.changed()
		})
	})
}

// sourceKey 返回技能的来源图标在 srcIcons 里的键；没有记录来源时为空。
func (a *app) sourceKey(s *library.Skill) string {
	if s.Source != library.Mine {
		return ""
	}
	rec, ok := a.lib.InstallRecord(s.Name)
	if !ok {
		return ""
	}
	return recordIconKey(rec)
}

// recordIconKey 是安装记录来源对应的图标键：GitHub 来源按作者（gh:<owner>），
// well-known 来源按站点。
func recordIconKey(rec library.LockEntry) string {
	switch rec.SourceType {
	case "github":
		owner, _, _ := strings.Cut(rec.Source, "/")
		return "gh:" + owner
	case "well-known":
		return rec.Source
	case "clawhub":
		return "clawhub.ai"
	}
	return ""
}

// collectionIconKey 是来源集合（SetSource）的图标键，取集合里第一个有
// 安装记录的技能定性；插件、文件夹集合为空，沿用原来的叠卡图标。
func (a *app) collectionIconKey(coll *library.Collection) string {
	if coll.Kind != library.SetSource {
		return ""
	}
	for _, s := range coll.Skills {
		rec, ok := a.lib.InstallRecord(s.Name)
		if !ok {
			continue
		}
		return recordIconKey(rec)
	}
	return ""
}

// sourceIconFor 取技能已加载的来源图标；还没有时为 nil。
func (a *app) sourceIconFor(s *library.Skill) ui.ImageSource {
	return a.srcIcons[a.sourceKey(s)]
}

// sourceRowIcon 画更新页来源行 28pt 的图标：抓到真 logo 就白底圆角位图；
// 还没加载到（示例数据、抓取失败或进行中）沿用自定义 squircle。
func (a *app) sourceRowIcon(c *ui.Context, it library.UpdateItem) {
	if src := a.srcIcons[it.Source.Repo]; src != nil {
		ui.Box(c).Size(28, 28).Shrink(0).Radius(6.5).Clip().Background(colBgLogo).
			InsetShadow(0, 0, 0, 0.5, ink(0.08)).
			Shadow(0, 1, 2, 0, shadow(0.1)).
			Children(func() {
				ui.Image(c, src).Size(28, 28).Fit(ui.Cover)
			})
		return
	}
	switch {
	case it.State == library.UpdWebsite && it.RecordIncomplete:
		squircleIcon(c, iconLinkBold, 28, squircleIndigo)
	case it.State == library.UpdWebsite:
		squircleIcon(c, iconGlobe, 28, squircleTeal)
	default:
		squircleIcon(c, iconLinkBold, 28, squircleGray)
	}
}
