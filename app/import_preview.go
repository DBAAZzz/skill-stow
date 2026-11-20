package main

import (
	"path"
	"slices"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"
	"github.com/egoist/mygo/ui/markdown"

	"skillmanager/internal/library"
	"skillmanager/locales"
)

// 添加技能页右侧的预览（画板 im6、au-d）：页头，「SKILL.md | 文件」两页。
// 「文件」页左边是文件栏，右边是选中的文件：Markdown 渲染（可看原文），
// 脚本等文本是铺满的只读编辑器，图片直接显示。只有 SKILL.md 一个文件时
// 没有「文件」页。

// 技能列表的宽度：默认 300，拖动分隔线在 240–480 之间调，记在设置里。
const (
	importListDefault = 300
	importListMin     = 240
	importListMax     = 480
)

// importSplit 是左侧技能列表和右侧预览，中间的分隔线可拖动。
func (a *app) importSplit(c *ui.Context) {
	st := &a.imp
	w := &a.lib.Settings.ImportListWidth
	if *w == 0 {
		*w = importListDefault
	}
	sp := ui.Split(c, w, func() { a.importList(c) }, func() { a.importPreview(c) }).
		SplitReset(importListDefault).Grow(1).MinHeight(0)
	*w = min(max(*w, importListMin), importListMax)
	// 拖动时每帧都在变：停下来再写设置
	switch {
	case sp.Changed():
		st.widthDirty = true
	case st.widthDirty:
		st.widthDirty = false
		if a.lib.Home != "" {
			a.lib.SaveSettings()
		}
	}
}

// importFilesOf 是技能暂存里的文件，按技能缓存：每帧重读目录太费。
// 换批次、补齐下载后清掉（refreshImportChecks）。
func (a *app) importFilesOf(s *library.ImportSkill) []library.File {
	st := &a.imp
	k := a.importKey(s)
	if f, ok := st.fileCache[k]; ok {
		return f
	}
	if st.fileCache == nil {
		st.fileCache = map[string][]library.File{}
	}
	f := s.Files()
	st.fileCache[k] = f
	return f
}

func (a *app) importPreview(c *ui.Context) {
	st := &a.imp
	s := a.importByKey(st.focus)
	if s == nil {
		ui.Column(c).Grow(1).Background(colBgSurface).Children(func() { empty(c, locales.ImportSelectSkillToPreview()) })
		return
	}
	files := a.importFilesOf(s)
	multi := !s.Partial && len(files) > 1
	if !multi {
		st.tab = 0
	}
	skillFile, hasSkillMD := findSkillMD(files)
	ui.Column(c).Grow(1).MinWidth(0).MinHeight(0).Background(colBgSurface).Children(func() {
		if st.tab == 1 || (st.skillRaw && hasSkillMD) {
			ui.Column(c).Padding(24, 32, 16, 32).Shrink(0).BorderWidth(0, 0, 1, 0).BorderColor(colBorderDefault).Children(func() {
				a.importHead(c, s, files, multi, st.tab == 0 && hasSkillMD)
			})
			if st.tab == 1 {
				a.importFiles(c, s, files)
			} else {
				a.importSkillRawView(c, s, skillFile)
			}
			return
		}
		ui.Scroll(c.Key("pv-" + a.importKey(s))).Grow(1).MinHeight(0).Scrollbars(false).Children(func() {
			ui.Column(c).Padding(24, 32, 28, 32).Gap(0).Children(func() {
				a.importHead(c, s, files, multi, hasSkillMD)
				a.importStatusNotes(c, s)
				a.importSecuritySection(c, s)
				a.importScan(c, s)
				a.importInspectorGroup(c, s, files, len(s.Audit) > 0)
				ui.Box(c).Height(1).AlignSelf(ui.Stretch).Background(colBorderDefault).Margin(20, 0, 18, 0)
				ui.Column(c).MaxWidth(780).Children(func() {
					if s.Partial && st.fetching[a.importKey(s)] {
						ui.Row(c).Gap(6).AlignItems(ui.Center).Margin(0, 0, 10, 0).Children(func() {
							ui.Spinner(c).Size(12, 12).TextColor(colAccent)
							ui.Text(c, locales.ImportDownloadingOtherFiles()).FontSize(12).TextColor(colTextSecondary)
						})
					}
					a.importSkillMD(c, s, files, false)
				})
			})
		})
	})
}

// importHead 是预览的页头：20px 主标题和状态胶囊、右上角「查看原文」轻量入口，
// 下面接简介、位置和统计；多文件时右下角切换「SKILL.md | 文件 N」。
func (a *app) importHead(c *ui.Context, s *library.ImportSkill, files []library.File, multi, showRaw bool) {
	st := &a.imp
	ui.Column(c).Gap(6).Children(func() {
		ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
			ui.Text(c, s.Name).FontSize(20).FontWeight(600).LetterSpacing(-0.2).SingleLine().Shrink(1).MinWidth(0)
			tag(c, a.importTag(s), false)
			if showRaw {
				ui.Spacer(c)
				importRawToggle(c, &st.skillRaw)
			}
		})
		if !s.MissingDescription() {
			ui.Text(c, s.Description).FontSize(14).LineHeight(1.5).TextColor(colTextSecondary).MaxLines(3)
		}
		ui.Row(c).Gap(10).AlignItems(ui.Center).Margin(2, 0, 0, 0).Children(func() {
			where := s.RepoPath
			switch {
			case st.batch.Source.Kind == library.ImportLocal:
				where = a.lib.Display(where)
			case st.batch.Source.Kind == library.ImportWebsite:
				where = webPath(s.SourceURL)
			case where == "":
				where = locales.ImportRepositoryRoot()
			}
			ui.Text(c, where).FontSize(12).Font("monospace").TextColor(colTextMuted).SingleLine().Shrink(1).MinWidth(0)
			// 「文件」页签上的数和实际总数一样时不重复写；没有页签，或页签
			// 少列了（.DS_Store、node_modules 之类不预览但照样装上）时写出总数
			if s.Count > 1 && (!multi || s.Count != len(files)) {
				ui.Text(c, locales.CommonFileCount(s.Count)).FontSize(12).TextColor(colTextMuted).FontFeatures("tnum").SingleLine()
			}
			if s.Size >= 0 { // 大仓库只拿了 SKILL.md 时大小要补齐后才知道
				ui.Text(c, library.HumanSize(s.Size)).FontSize(12).TextColor(colTextMuted).FontFeatures("tnum").SingleLine()
			}
			ui.Spacer(c)
			if multi {
				previewTabs(c, &st.tab, "SKILL.md", locales.ImportFilesHeadingCount(len(files)))
			}
		})
	})
}

// importSkillMD 渲染 SKILL.md 正文（过滤掉本地打不开的外链徽章占位框）。
// all 为真时（「文件」页里选中 SKILL.md）顶部先列出全部 frontmatter 字段。
func (a *app) importSkillMD(c *ui.Context, s *library.ImportSkill, files []library.File, all bool) {
	if f, ok := findSkillMD(files); ok {
		md := library.ParseSkillMD(f.Content)
		if all {
			skillFrontmatterCard(c, md.Fields)
		}
		markdown.View(c, cleanSkillMarkdownBody(md.Body, s.Dir), a.skillMarkdownOptions(s))
		return
	}
	ui.Text(c, locales.ImportCannotReadSkillFile()).TextColor(colTextDisabled)
}

// importFiles 是「文件」页：左边文件栏（沿用阅读视图），右边是选中的文件。
func (a *app) importFiles(c *ui.Context, s *library.ImportSkill, files []library.File) {
	st := &a.imp
	if st.file >= len(files) {
		st.file = 0
	}
	ui.Row(c).Grow(1).MinHeight(0).AlignItems(ui.Stretch).Children(func() {
		ui.Scroll(c.Key("ftree-"+a.importKey(s))).Width(212).Shrink(0).Background(colBgPane).
			BorderWidth(0, 1, 0, 0).BorderColor(colBorderDefault).Scrollbars(false).Children(func() {
			ui.Column(c).Padding(10, 8).Gap(1).Children(func() {
				ui.Row(c).Padding(2, 8, 6, 8).AlignItems(ui.Center).Children(func() {
					sectionLabel(c, locales.FilesLabel())
					ui.Spacer(c)
					if s.Size >= 0 {
						ui.Text(c, library.HumanSize(s.Size)).FontSize(11).TextColor(colTextDisabled).FontFeatures("tnum")
					}
				})
				dir := ""
				for i, f := range files {
					d, name := path.Split(f.Path)
					d = strings.TrimSuffix(d, "/")
					if d != dir && d != "" {
						ui.Row(c.Key("dir/"+d)).Height(26).Padding(0, 8).Gap(7).AlignItems(ui.Center).Children(func() {
							ui.Icon(c, iconFolder).Size(14, 14).TextColor(colAccent)
							ui.Text(c, d).FontSize(13).SingleLine().MinWidth(0)
						})
					}
					dir = d
					row := ui.ButtonBase(c.Key("f-"+f.Path)).Height(26).Padding(0, 8, 0, 8+indent(d)).Gap(7).Radius(6).
						AlignItems(ui.Center).Label(f.Path)
					selectableRow(row, i == st.file, colInkSelected)
					if row.Clicked() {
						st.file = i
					}
					row.Children(func() {
						ui.Icon(c, iconDoc).Size(14, 14).TextColor(colTextMuted)
						ui.Text(c, name).FontSize(13).SingleLine().Grow(1).MinWidth(0)
						if f.Exec || slices.Contains(s.Exec, f.Path) {
							ui.Icon(c, iconTerminal).Size(12, 12).TextColor(colWarnText).Tooltip(locales.ImportExecutableHint())
						}
						ui.Text(c, sizeLabel(f.Size)).FontSize(11).TextColor(colTextDisabled).FontFeatures("tnum").Shrink(0)
					})
				}
			})
		})
		ui.Column(c).Grow(1).MinWidth(0).MinHeight(0).Children(func() {
			if len(files) > 0 {
				a.importFileView(c, s, files, files[st.file])
			}
		})
	})
}

// imageExts 是「文件」页里直接显示的图片。
var imageExts = []string{".png", ".jpg", ".jpeg", ".gif", ".webp", ".bmp", ".svg"}

// importFileView 显示「文件」页选中的文件。
func (a *app) importFileView(c *ui.Context, s *library.ImportSkill, files []library.File, f library.File) {
	st := &a.imp
	ext := strings.ToLower(path.Ext(f.Path))
	switch {
	case slices.Contains(imageExts, ext):
		imageFileView(c, f, a.importImage(s, f))
	case f.Binary:
		ui.Column(c).Padding(18, 22).Children(func() {
			ui.Text(c, locales.ImportFileNotDisplayed(f.Path, sizeLabel(f.Size))).FontSize(12).TextColor(colTextDisabled)
		})
	case ext == ".md" && !st.raw:
		a.importMarkdownFile(c, s, files, f)
	default:
		a.importCode(c, s, f)
	}
}

// imageFileView 展示选中的图片文件及其大小，供导入「文件」页和阅读视图共用。
func imageFileView(c *ui.Context, f library.File, img ui.ImageSource) {
	ui.Scroll(c.Key("img-" + f.Path)).Grow(1).MinHeight(0).Scrollbars(false).Children(func() {
		ui.Column(c).Padding(18, 22).Gap(8).Children(func() {
			if img == nil {
				ui.Text(c, locales.ImportPreviewCantOpen(f.Path)).FontSize(12).TextColor(colTextDisabled)
				return
			}
			ui.Column(c).Padding(16).Radius(8).Background(colBgSubtle).Center().Children(func() {
				ui.Image(c, img).MaxWidth(560).MaxHeight(420).Fit(ui.Contain)
			})
			ui.Text(c, sizeLabel(f.Size)).FontSize(12).TextColor(colTextMuted)
		})
	})
}

// importMarkdownFile 渲染 Markdown；右上角「渲染 / 原文」切换，不另占一条栏。
func (a *app) importMarkdownFile(c *ui.Context, s *library.ImportSkill, files []library.File, f library.File) {
	a.importModeBar(c)
	ui.Scroll(c.Key("md-" + a.importKey(s) + "/" + f.Path)).Grow(1).MinHeight(0).Scrollbars(false).Children(func() {
		ui.Column(c).Padding(4, 22, 24, 22).Gap(14).MaxWidth(760).Children(func() {
			if strings.EqualFold(f.Path, "SKILL.md") {
				a.importSkillMD(c, s, files, true)
				return
			}
			markdown.View(c, cleanSkillMarkdownBody(f.Content, s.Dir), a.skillMarkdownOptions(s))
		})
	})
}

// importModeBar 是 Markdown 文件的「渲染 / 原文」，靠右放在内容上方。
func (a *app) importModeBar(c *ui.Context) {
	st := &a.imp
	ui.Row(c).Padding(10, 18, 4, 18).Justify(ui.End).Shrink(0).Children(func() {
		tab := 0
		if st.raw {
			tab = 1
		}
		previewTabs(c, &tab, locales.ImportRendered(), locales.CommonSourceText())
		st.raw = tab == 1
	})
}

// importCode 是铺满的只读编辑器：行号、语法高亮，能选中复制，不能改。
func (a *app) importCode(c *ui.Context, s *library.ImportSkill, f library.File) {
	if strings.EqualFold(path.Ext(f.Path), ".md") {
		a.importModeBar(c)
	}
	k := a.importKey(s) + "/" + f.Path
	es := a.ensureReadOnlyEditor(k, f.Content, f.Path, false)
	editor.View(c.Key("ed-"+k), es).Grow(1).MinHeight(0)
}

// importImage 读暂存里的图片，按技能和路径缓存。
func (a *app) importImage(s *library.ImportSkill, f library.File) ui.ImageSource {
	st := &a.imp
	k := a.importKey(s) + "/" + f.Path
	if img, ok := st.images[k]; ok {
		return img
	}
	img, err := markdown.FileImages(s.Dir)(f.Path)
	if err != nil {
		img = nil
	}
	if st.images == nil {
		st.images = map[string]ui.ImageSource{}
	}
	st.images[k] = img
	return img
}

// —— skills.sh 第三方扫描（画板 au-d）——

// 状态图标自带颜色（ui.Image 按 SVG 自己的颜色画；ui.Icon 会整个染成一色，
// 白色的叉号、对勾就看不见了）。颜色只落在图标上，背景保持中性。
func colorSymbol(shapes string) *ui.SVG {
	return ui.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24">` + shapes + `</svg>`))
}

var (
	symCritical = colorSymbol(`<path d="M8.2 2h7.6L22 8.2v7.6L15.8 22H8.2L2 15.8V8.2z" fill="#ff3b30"/><path d="M9 9l6 6M15 9l-6 6" fill="none" stroke="#fff" stroke-width="2.2" stroke-linecap="round"/>`)
	symHigh     = colorSymbol(`<path d="M10.3 3.6a2 2 0 0 1 3.4 0l8.4 14.6A2 2 0 0 1 20.4 21H3.6a2 2 0 0 1-1.7-2.8z" fill="#ff9f0a"/><path d="M12 9v5" stroke="#fff" stroke-width="2.2" stroke-linecap="round"/><circle cx="12" cy="17.3" r="1.25" fill="#fff"/>`)
	symAlert    = colorSymbol(`<path d="M10.3 3.6a2 2 0 0 1 3.4 0l8.4 14.6A2 2 0 0 1 20.4 21H3.6a2 2 0 0 1-1.7-2.8z" fill="#ffcc00"/><path d="M12 9v5" stroke="#1d1d1f" stroke-width="2.2" stroke-linecap="round"/><circle cx="12" cy="17.3" r="1.25" fill="#1d1d1f"/>`)
	symInfo     = colorSymbol(`<circle cx="12" cy="12" r="10" fill="#8e8e93"/><path d="M12 10.5v6" stroke="#fff" stroke-width="2.2" stroke-linecap="round"/><circle cx="12" cy="7.3" r="1.3" fill="#fff"/>`)
	symOK       = colorSymbol(`<circle cx="12" cy="12" r="10" fill="#34c759"/><path d="M7.5 12.3l3 3 6-6.3" fill="none" stroke="#fff" stroke-width="2.2" stroke-linecap="round" stroke-linejoin="round"/>`)
	symTagWarn  = ui.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 16 16"><path d="M7.15 2.2a1 1 0 0 1 1.7 0l5.45 9.6A1 1 0 0 1 13.45 13.3H2.55a1 1 0 0 1-.85-1.5L7.15 2.2z" fill="#f59e0b"/><path d="M8 6.1v3.2" fill="none" stroke="#1d1d1f" stroke-width="1.5" stroke-linecap="round"/><circle cx="8" cy="11.2" r=".85" fill="#1d1d1f"/></svg>`))
	symTagMinus = ui.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 16 16"><circle cx="8" cy="8" r="6.5" fill="#8e8e93"/><path d="M5.2 8h5.6" fill="none" stroke="#fff" stroke-width="1.6" stroke-linecap="round"/></svg>`))
	symTagMuted = ui.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 16 16"><circle cx="8" cy="8" r="6.5" fill="#8e8e93"/><path d="M5.2 8.2l1.9 1.9 3.7-4" fill="none" stroke="#fff" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round"/></svg>`))
	iconOutward = icon(`<path d="M8 16 16 8M9 8h7v7"/>`)
)

// riskRank 让最严重的排在最上面。
func riskRank(r string) int {
	return slices.Index([]string{"safe", "low", "medium", "high", "critical"}, r)
}

// riskSymbol 是一家扫描结果的状态图标。
func riskSymbol(r string) *ui.SVG {
	switch r {
	case "critical":
		return symCritical
	case "high":
		return symHigh
	case "medium":
		return symAlert
	case "safe":
		return symOK
	}
	return symInfo
}

// scanDay 是扫描日期：今年的写「9月2日」，往年的带上年份。
func scanDay(t, now time.Time) string {
	if t.IsZero() {
		return ""
	}
	t = t.Local()
	if t.Year() == now.Year() {
		return locales.DateMonthDayCompact(int(t.Month()), t.Day())
	}
	return locales.DateYearMonthDayCompact(t.Year(), int(t.Month()), t.Day())
}

func latestAuditAt(rows []library.AuditResult) time.Time {
	var best time.Time
	for _, r := range rows {
		if r.At.After(best) {
			best = r.At
		}
	}
	return best
}

// importScan 是 skills.sh 收录时的第三方安全审计（画板 au-d / au-e / r7）：
// 无风险时收成「没发现问题，N 家」可点开查看明细；有风险时直接列出每家结果（最严重在前）。
func (a *app) importScan(c *ui.Context, s *library.ImportSkill) {
	if len(s.Audit) == 0 {
		return
	}
	st := &a.imp
	b := st.batch
	k := a.importKey(s)
	rows := slices.Clone(s.Audit)
	slices.SortStableFunc(rows, func(x, y library.AuditResult) int { return riskRank(y.Risk) - riskRank(x.Risk) })
	now := time.Now()
	stale := false
	for _, r := range rows {
		if !r.At.IsZero() && b.CommitDate.After(r.At) {
			stale = true
		}
	}
	ui.Column(c).Margin(18, 0, 0, 0).Gap(6).Children(func() {
		ui.Row(c).Padding(0, 2).Gap(6).AlignItems(ui.Center).Children(func() {
			sectionLabel(c, locales.ImportSecurityAudit())
			ui.Spacer(c)
			link := ui.ButtonBase(c).Gap(2).AlignItems(ui.Center).Cursor(ui.CursorPointer).Label(locales.ImportViewAuditWebsite())
			link.Children(func() {
				col := colAccent
				if link.Hovered() {
					col = darker(colAccent, 0.12)
				}
				ui.Text(c, locales.ImportViewAuditWebsite()).FontSize(12).TextColor(col)
				ui.Icon(c, iconOutward).Size(10, 10).TextColor(col)
			})
			if link.Clicked() {
				a.openExternalURL("https://skills.sh/" + b.Source.Repo + "/" + s.Name)
			}
		})
		insetGroup(c, func() {
			if !s.Risky() && !st.scanOpen[k] {
				row := insetGroupClickableRow(c, "scan-summary-"+k, locales.ImportAuditNoIssues(), true, 40, func() {
					ui.Image(c, symOK).Size(16, 16).Shrink(0)
					ui.Text(c, locales.ImportAuditNoIssues()).FontSize(13).TextColor(colTextPrimary).SingleLine().Shrink(0)
					ui.Spacer(c)
					ui.Text(c, locales.ImportAuditProviderCount(len(rows))).FontSize(12).TextColor(colTextSecondary).FontFeatures("tnum").SingleLine().Shrink(0)
					ui.Row(c).Width(56).Shrink(0).Justify(ui.End).Children(func() {
						ui.Text(c, scanDay(latestAuditAt(rows), now)).FontSize(12).TextColor(colTextDisabled).FontFeatures("tnum").SingleLine()
					})
					ui.Icon(c, iconChevronRight).Size(12, 12).TextColor(colTextDisabled).Shrink(0).Margin(0, 0, 0, -2)
				})
				if row.Clicked() {
					if st.scanOpen == nil {
						st.scanOpen = map[string]bool{}
					}
					st.scanOpen[k] = true
				}
				return
			}
			for i, r := range rows {
				insetGroupRow(c, "scan-"+r.Provider, i == 0, 40, func() {
					ui.Image(c, riskSymbol(r.Risk)).Size(16, 16).Shrink(0)
					ui.Text(c, r.ProviderName()).FontSize(13).SingleLine().Shrink(1).MinWidth(0)
					ui.Spacer(c)
					ui.Row(c).Gap(6).AlignItems(ui.Center).Shrink(0).Children(func() {
						level := ui.Text(c, riskLabel(r.Risk)).FontSize(12).TextColor(colTextSecondary).SingleLine()
						if r.Risk == "critical" {
							level.TextColor(colDangerText).FontWeight(500)
						}
						if r.Alerts > 0 {
							ui.Text(c, locales.ImportAuditAlertCount(r.Alerts)).FontSize(12).TextColor(colTextSecondary).SingleLine()
						}
					})
					ui.Row(c).Width(56).Shrink(0).Justify(ui.End).Children(func() {
						ui.Text(c, scanDay(r.At, now)).FontSize(12).TextColor(colTextDisabled).FontFeatures("tnum").SingleLine()
					})
				})
			}
		})
		if stale {
			ui.Text(c, locales.ImportAuditMayBeOutdated(scanDay(b.CommitDate, now))).
				FontSize(11).LineHeight(1.4).TextColor(colTextMuted).Padding(2, 2, 0, 2)
		}
	})
}
