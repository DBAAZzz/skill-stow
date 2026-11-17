package main

import (
	"path"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"
	"github.com/egoist/mygo/ui/markdown"

	"skillmanager/internal/library"
	"skillmanager/locales"
)

// reader 是阅读视图：双击卡片或选中后按空格进入，‹ 或 Esc 返回。只读。
func (a *app) reader(c *ui.Context, s *library.Skill) {
	if a.file >= len(s.Files) {
		a.file = 0
	}
	var f library.File
	if len(s.Files) > 0 {
		f = s.Files[a.file]
	}

	fresh := a.readerIn
	a.readerIn = false
	bar := ui.Row(c).Height(toolbarHeight(c)).Shrink(0).Padding(0, toolbarRightInset(c), 0, a.toolbarLeftInset(c, 12)).Gap(10).AlignItems(ui.Center).
		BorderWidth(0, 0, 1, 0).BorderColor(colBorderDefault).DragWindow()
	fadeIn(bar, fresh, 60*time.Millisecond)
	bar.Children(func() {
		if iconButton(c, iconBack, locales.ActionBackShortcut()).Clicked() {
			a.closeReader()
		}
		ui.Column(c).Children(func() {
			ui.Text(c, s.Name).FontSize(13).FontWeight(600).FixedLineHeight(16).SingleLine()
			ui.Text(c, s.Origin()).FontSize(11).FixedLineHeight(14).TextColor(colTextMuted).SingleLine()
		})
		ui.Spacer(c)
		if !f.Binary && strings.EqualFold(path.Ext(f.Path), ".md") {
			importRawToggle(c, &a.readerRaw)
		}
		if iconButton(c, iconFolder, locales.ActionShowInFinder()).Clicked() {
			a.reveal(s.Dir)
		}
		if button(c, iconExternal, locales.ActionOpenInEditor()).Clicked() {
			a.openInEditor(filepath.Join(s.Dir, f.Path))
		}
	})

	ui.Row(c).Grow(1).MinHeight(0).AlignItems(ui.Stretch).Children(func() {
		a.fileList(c, s, fresh)
		ui.Box(c).Width(1).Background(colBorderDefault)
		ui.Column(c).Grow(1).MinWidth(0).MinHeight(0).Children(func() {
			fadeIn(ui.Column(c), fresh, 200*time.Millisecond).Children(func() { a.strip(c, s) })
			a.readerBody(c, s, f, fresh)
		})
	})
}

// ——— 卡片 ↔ 阅读视图（共享元素过渡）———
//
// 双击卡片时，卡片上的名称飞成正文的大标题、按比例放大（readerTitle），
// 上一页同时淡出，工具栏、文件列表、正文依次淡入；返回时名称从大标题飞回
// 卡片，卡片的简介和底栏随后淡入。只让名称飞：它是两页之间最显眼的同一
// 样东西，够把人带过去，其余靠淡入淡出，不抢眼。

// readerTitle 是名称和大标题共用的 ElementTransition.Match 身份。
type readerTitle struct{ id string }

// readerMorph 是名称飞过去、飞回来的弹簧：不回弹，稳稳落下。
var readerMorph = ui.Animation{Spring: ui.SpringOptions{Duration: 460 * time.Millisecond}}

// cardSettle 是回到浏览时，卡片的简介和底栏等多久再淡入：名称差不多落定。
const cardSettle = 260 * time.Millisecond

// pageFade 是进出阅读视图时上一页的淡出。阅读视图退得更快：回到浏览时
// 网格立刻在上面，它的正文透在下面的时间短一些。
var (
	pageFade   = ui.ElementTransition{Duration: 200 * time.Millisecond, Exit: &ui.Motion{Opacity: 0}}
	readerFade = ui.ElementTransition{Duration: 120 * time.Millisecond, Exit: &ui.Motion{Opacity: 0}}
)

// morphing 报告 s 的卡片名称这一帧要不要和阅读视图的大标题对上：选中的
// 那张，而且上一次 view 开始前就已经选中了（点击当帧及同帧重绘都不标，
// 到下一次重绘才为真）。对上要在打开的前一帧就标好，所以不能等双击；
// 刚选中的那一帧网格可能换列、卡片换行，不标，免得和它滑到新位置的过渡
// （cellTransition）打架。
func (a *app) morphing(s *library.Skill) bool {
	return a.selected == s && a.wasSelected == s
}

// titleMorph 是卡片名称和正文大标题之间的过渡：两边都飞，出现的一边从另
// 一边的大小按比例缩放过来，另一边在它下面淡出，看到的是同一个名字放大
// 或缩小。
func titleMorph(s *library.Skill) ui.ElementTransition {
	return ui.ElementTransition{Match: readerTitle{s.ID}, Fly: true, ScaleContent: true, Animation: &readerMorph}
}

// fadeIn 让阅读视图里的 e 在刚打开时（fresh）等 delay 后淡入、微微上移，
// 其余时候照常显示。整页是新建的，里面的元素没有 Enter，所以自己动画。
func fadeIn(e ui.Element, fresh bool, delay time.Duration) ui.Element {
	if fresh {
		e.Jump("in", 0)
	}
	t := e.AnimateTo("in", 1, ui.Animation{Duration: 240 * time.Millisecond, Delay: delay, NonMotion: true})
	return e.Opacity(t).Translate(0, 6*(1-t))
}

// fileList 是技能目录下的文件，SKILL.md 在最前，子目录里的文件缩进。
func (a *app) fileList(c *ui.Context, s *library.Skill, fresh bool) {
	ui.Scroll(c.Key("reader-files:"+s.ID)).Width(232).MinHeight(0).Shrink(0).
		Background(colBgPane).Scrollbars(false).Children(func() {
		list := ui.Column(c).Padding(12, 8).Gap(1)
		fadeIn(list, fresh, 90*time.Millisecond).Children(func() {
			sectionLabel(c, locales.FilesLabel()).Padding(2, 8, 6, 8)
			dir := ""
			for i, f := range s.Files {
				d, name := path.Split(f.Path)
				d = strings.TrimSuffix(d, "/")
				if d != dir && d != "" {
					ui.Row(c.Key("dir/"+d)).Height(26).Padding(0, 8).Gap(7).AlignItems(ui.Center).Children(func() {
						ui.Icon(c, iconFolder).Size(14, 14).TextColor(colAccent).Shrink(0)
						ui.Text(c, d).FontSize(13).SingleLine().MinWidth(0)
					})
				}
				dir = d
				row := ui.ButtonBase(c.Key(f.Path)).Height(26).Padding(0, 8, 0, 8+indent(d)).Gap(7).Radius(6).
					AlignItems(ui.Center).Label(f.Path)
				selectableRow(row, i == a.file, colInkSelected)
				if row.Clicked() {
					a.file = i
					a.readerRaw, a.readerEditor, a.readerEditorKey = false, nil, ""
				}
				row.Children(func() {
					ui.Icon(c, iconDoc).Size(14, 14).TextColor(colTextMuted).Shrink(0)
					ui.Text(c, name).FontSize(13).SingleLine().Grow(1).MinWidth(0)
					ui.Text(c, sizeLabel(f.Size)).FontSize(11).TextColor(colTextDisabled).FontFeatures("tnum").Shrink(0)
				})
			}
		})
	})
}

func indent(dir string) float32 {
	if dir == "" {
		return 0
	}
	return 18
}

// strip 是正文上方的提示条：启用在哪些 Agent、同名副本。
func (a *app) strip(c *ui.Context, s *library.Skill) {
	ui.Row(c).Height(38).Shrink(0).Padding(0, 28).Gap(8).AlignItems(ui.Center).
		BorderWidth(0, 0, 1, 0).BorderColor(colBorderSubtle).TextColor(colTextSecondary).Children(func() {
		switch {
		case s.ReadOnly():
			ui.Text(c, s.Origin()).FontSize(12)
		case s.AllDisabled():
			ui.Text(c, locales.CommonDisabled()).FontSize(12)
		case len(s.DiscoveredAgents()) == 0:
			ui.Text(c, locales.CommonNotEnabled()).FontSize(12)
		default:
			ui.Text(c, locales.ReaderEnabledFor()).FontSize(12)
			a.enabledBadges(c, s, "", 16, 10)
		}
		if s.Copies > 0 {
			ui.Box(c).Size(1, 14).Background(colBorderDefault).Margin(0, 4)
			ui.Row(c).Gap(4).AlignItems(ui.Center).TextColor(colWarnText).Children(func() {
				ui.Icon(c, iconWarn).Size(12, 12)
				ui.Text(c, locales.InspectorOtherCopies(s.Copies)).FontSize(12).FontWeight(500)
			})
		}
		ui.Spacer(c)
		// 回到浏览，详情栏显示这个技能
		if linkButton(c, locales.ReaderManage()).Clicked() {
			a.closeReader()
		}
	})
}

func (a *app) readerEditorState(s *library.Skill, f library.File, softWrap bool) *editor.State {
	key := s.ID + "\x00" + f.Path
	if a.readerEditor != nil && a.readerEditorKey == key && a.readerEditor.SoftWrap == softWrap {
		return a.readerEditor
	}
	a.readerEditor = newReadOnlyEditor(f.Content, f.Path, softWrap)
	a.readerEditorKey = key
	return a.readerEditor
}

func (a *app) readerImage(s *library.Skill, f library.File) ui.ImageSource {
	k := s.ID + "\x00" + f.Path
	if img, ok := a.readerImages[k]; ok {
		return img
	}
	dir := s.Real
	if dir == "" && filepath.IsAbs(s.Dir) {
		dir = s.Dir
	}
	var img ui.ImageSource
	switch {
	case dir != "":
		if loaded, err := markdown.FileImages(dir)(f.Path); err == nil {
			img = loaded
		}
	case strings.EqualFold(path.Ext(f.Path), ".svg") && f.Content != "":
		if parsed, err := ui.ParseSVG([]byte(f.Content)); err == nil {
			img = parsed
		}
	}
	if a.readerImages == nil {
		a.readerImages = map[string]ui.ImageSource{}
	}
	a.readerImages[k] = img
	return img
}

// readerBody 根据文件类型展示内容：非 Markdown 文本与打开原文的 Markdown 用
// 铺满的只读编辑器（与导入页文件详情一致），图片直接预览，Markdown 渲染排版。
func (a *app) readerBody(c *ui.Context, s *library.Skill, f library.File, fresh bool) {
	ext := strings.ToLower(path.Ext(f.Path))
	isMD := ext == ".md"
	scrollDoc := func(key string, body func()) {
		ui.Scroll(c.Key(key)).Grow(1).MinHeight(0).Scrollbars(false).Children(func() {
			ui.Column(c).Padding(28, 40).Gap(16).MaxWidth(760).Children(body)
		})
	}
	switch {
	case f.Path == "":
		scrollDoc(s.Name+"/empty", func() {
			ui.Text(c, locales.ReaderFilesUnreadable()).TextColor(colTextDisabled)
		})
	case slices.Contains(imageExts, ext):
		imageFileView(c, f, a.readerImage(s, f))
	case f.Binary:
		scrollDoc(s.Name+"/"+f.Path, func() {
			ui.Text(c, locales.ImportFileNotDisplayed(f.Path, sizeLabel(f.Size))).TextColor(colTextDisabled)
		})
	case !isMD || a.readerRaw:
		es := a.readerEditorState(s, f, isMD)
		editor.View(c.Key("reader-ed:"+s.ID+"/"+f.Path), es).Grow(1).MinHeight(0)
	default:
		scrollDoc(s.Name+"/"+f.Path, func() {
			a.document(c, s, f, fresh)
		})
	}
}

// document 渲染 Markdown 文件：SKILL.md 的 name / description 做成页头，其余原样渲染。
func (a *app) document(c *ui.Context, s *library.Skill, f library.File, fresh bool) {
	a.md.Theme = readerMarkdownTheme(c)
	if strings.EqualFold(f.Path, "SKILL.md") {
		md := library.ParseSkillMD(f.Content)
		name := md.Name
		if name == "" {
			name = s.Name
		}
		ui.Column(c).Gap(6).Children(func() {
			// 大标题是卡片上的名称飞过来的，不跟着淡入
			ui.Text(c, name).FontSize(22).Bold().AlignSelf(ui.Start).TransformOrigin(0, 0).Transition(titleMorph(s))
			if md.Description != "" {
				fadeIn(ui.Column(c), fresh, 220*time.Millisecond).Children(func() {
					ui.Text(c, md.Description).FontSize(14).TextColor(colTextSecondary).LineHeight(1.5)
				})
			}
		})
		fadeIn(ui.Column(c).Gap(16), fresh, 260*time.Millisecond).Children(func() {
			ui.Divider(c)
			markdown.View(c, md.Body, &a.md)
		})
		return
	}
	fadeIn(ui.Column(c).Gap(16), fresh, 220*time.Millisecond).Children(func() { markdown.View(c, f.Content, &a.md) })
}

// readerMarkdownTheme 是阅读页的 Markdown 主题：框架默认的配色与代码高亮，
// 字号换成 readingSizes。浅色、深色各建一份留着，理由同 skillMarkdownThemes。
func readerMarkdownTheme(c *ui.Context) *markdown.Theme {
	i := 0
	if darkUI {
		i = 1
	}
	if readerMarkdownThemes[i] == nil {
		th := markdown.DefaultTheme(c.Theme())
		readingSizes(th)
		readerMarkdownThemes[i] = th
	}
	return readerMarkdownThemes[i]
}

var readerMarkdownThemes [2]*markdown.Theme
