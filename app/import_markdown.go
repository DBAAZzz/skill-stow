package main

import (
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"
	"github.com/egoist/mygo/ui/markdown"
	"github.com/egoist/mygo/ui/markdown/parser"

	"skillmanager/internal/library"
	"skillmanager/locales"
)

// 技能详情页（r7 定稿 sk-a / sk-b / sk-c）的辅助组件：
// - 右上角「查看原文 / 原文模式」轻量切换与完整只读 SKILL.md 视图
// - 对齐 16px 图标轨道的 #f5f5f7 无边框分组（安全审计、工具权限、可执行文件、元信息）
// - 克制字号阶梯 + 外链徽章占位过滤的 Markdown 排版

var ()

// insetDivider 画分组列表行间发丝细线；inset 让分隔线从文字起点开始，不贯穿左侧图标轨道。
func insetDivider(c *ui.Context, inset float32) {
	ui.Row(c).Padding(0, 0, 0, inset).Children(func() {
		ui.Box(c).Height(edgeSpread).Grow(1).Background(colBorderDefault)
	})
}

// insetGroup 画圆角 8、#f5f5f7 背景的无边框分组容器。
func insetGroup(c *ui.Context, rows func()) {
	ui.Column(c).Radius(8).Background(colBgSubtle).Clip().AlignSelf(ui.Stretch).Children(rows)
}

// insetGroupRow 是分组里的一行：最小 40px 高、14px 左右内边距、10px 间距（左起 40px 接正文）。
func insetGroupRow(c *ui.Context, key string, first bool, inset float32, children func()) ui.Element {
	if !first {
		insetDivider(c, inset)
	}
	return ui.Row(c.Key(key)).MinHeight(40).Padding(8, 14).Gap(10).AlignItems(ui.Center).Children(children)
}

// insetGroupClickableRow 是可整行点击展开或跳转的分组行。
func insetGroupClickableRow(c *ui.Context, key, label string, first bool, inset float32, children func()) ui.Element {
	if !first {
		insetDivider(c, inset)
	}
	b := ui.ButtonBase(c.Key(key)).MinHeight(40).Padding(8, 14).Gap(10).
		AlignItems(ui.Center).AlignSelf(ui.Stretch).Cursor(ui.CursorPointer).Label(label)
	if b.Hovered() {
		b.Background(ink(0.03))
	}
	return b.Children(children)
}

// styleHairlineButton 为详情页右上角及状态卡提供一致的 24px 极细描边白底按钮样式。
func styleHairlineButton(btn ui.Element) {
	tintSolid(btn, colBgControl)
	btn.Border(edgeSpread, ink(0.14)).Shadow(0, 0.5, 1, 0, shadow(0.06))
}

// detailHairlineButton 是详情页状态提示右侧的轻量操作按钮（「对比差异」「在 Finder 中显示」）。
func detailHairlineButton(c *ui.Context, key, label string) ui.Element {
	btn := ui.ButtonBase(c.Key(key)).Height(24).Padding(0, 10).Radius(6).
		Center().Shrink(0).Cursor(ui.CursorPointer).Label(label)
	styleHairlineButton(btn)
	return btn.Children(func() {
		ui.Text(c, label).FontSize(12).FontWeight(500).TextColor(colTextPrimary).SingleLine()
	})
}

// importRawToggle 放在详情页右上角，在渲染排版和原始 SKILL.md（含 YAML frontmatter）间切换。
func importRawToggle(c *ui.Context, raw *bool) {
	label := locales.ActionViewSource()
	if *raw {
		label = locales.ImportSourceMode()
	}
	btn := ui.ButtonBase(c.Key("toggle-raw-skill")).Height(24).Padding(0, 9).Radius(6).Gap(5).
		AlignItems(ui.Center).Shrink(0).Cursor(ui.CursorPointer).Label(label)
	fg := colTextBody
	if *raw {
		fg = colAccentText
		bg := colAccentBg
		if btn.Hovered() {
			bg = colInkHover.Over(colAccentBg)
		}
		btn.Background(bg).Border(edgeSpread, colAccentEdge)
	} else {
		styleHairlineButton(btn)
	}
	btn.Children(func() {
		ui.Icon(c, iconCode).Size(12, 12).TextColor(fg)
		ui.Text(c, label).FontSize(12).FontWeight(500).TextColor(fg).SingleLine()
	})
	if btn.Clicked() {
		*raw = !*raw
	}
}

func findSkillMD(files []library.File) (library.File, bool) {
	for _, f := range files {
		if strings.EqualFold(f.Path, "SKILL.md") {
			return f, true
		}
	}
	return library.File{}, false
}

// splitFrontmatterFields 分离权限声明（allowed-tools 等）和其余 frontmatter 字段。
func splitFrontmatterFields(fields []library.SkillField, includeCore bool) (tools, extra []library.SkillField) {
	for _, f := range fields {
		if !includeCore && (f.Key == "name" || f.Key == "description") {
			continue
		}
		if f.GrantsTools() {
			tools = append(tools, f)
		} else {
			extra = append(extra, f)
		}
	}
	return tools, extra
}

func formatExecSummary(paths []string, limit int) string {
	if len(paths) == 0 {
		return ""
	}
	if len(paths) <= limit {
		return strings.Join(paths, locales.ListSeparator())
	}
	return locales.ImportPathsEtc(strings.Join(paths[:limit], locales.ListSeparator()), len(paths))
}

func inspectorGroupTitle(hasTools, hasExec bool) string {
	switch {
	case hasTools && hasExec:
		return locales.ImportFilesAndPermissions()
	case hasTools:
		return locales.ImportToolPermissions()
	case hasExec:
		return locales.ImportExecutables()
	default:
		return locales.ImportMetadata()
	}
}

// importInspectorGroup 在分割线上方统一展示 allowed-tools、可执行文件与额外 frontmatter 字段，
// 复用 #f5f5f7 分组语言，与上方安全审计共用 16px 左对齐图标轨道。
func (a *app) importInspectorGroup(c *ui.Context, s *library.ImportSkill, files []library.File, hasAudit bool) {
	st := &a.imp
	var fields []library.SkillField
	if f, ok := findSkillMD(files); ok {
		fields = library.ParseSkillMD(f.Content).Fields
	}
	tools, extra := splitFrontmatterFields(fields, false)
	if len(tools) == 0 && len(s.Exec) == 0 && len(extra) == 0 {
		return
	}
	topMargin := float32(18)
	if hasAudit {
		topMargin = 14
	}
	title := inspectorGroupTitle(len(tools) > 0, len(s.Exec) > 0)
	ui.Column(c).Margin(topMargin, 0, 0, 0).Gap(6).Children(func() {
		ui.Row(c).Padding(0, 2).Children(func() { sectionLabel(c, title) })
		insetGroup(c, func() {
			rowIdx := 0
			for _, f := range tools {
				insetGroupRow(c, "tool-"+f.Key, rowIdx == 0, 40, func() {
					ui.Icon(c, iconTerminal).Size(16, 16).TextColor(colWarnText).Shrink(0)
					ui.Text(c, f.Value).FontSize(12).FontWeight(500).Font("monospace").TextColor(colTextPrimary).
						SingleLine().Shrink(1).MinWidth(0).Tooltip(f.Value)
					ui.Spacer(c)
					ui.Text(c, locales.ImportCommandsAllowed()).FontSize(12).TextColor(colWarnText).SingleLine().Shrink(0)
				})
				rowIdx++
			}
			if len(s.Exec) > 0 {
				execTip := locales.ImportCommandsNotRunHere()
				summary := formatExecSummary(s.Exec, 3)
				row := insetGroupRow(c, "exec-files", rowIdx == 0, 40, func() {
					ui.Icon(c, iconDoc).Size(16, 16).TextColor(colTextSecondary).Shrink(0)
					ui.Text(c, locales.ImportContainsExecutables(len(s.Exec))).
						FontSize(13).TextColor(colTextPrimary).SingleLine().Shrink(0)
					ui.Text(c, summary).FontSize(12).TextColor(colTextSecondary).
						SingleLine().Shrink(1).MinWidth(0).Tooltip(strings.Join(s.Exec, "\n"))
					ui.Spacer(c)
					if !s.Partial {
						btn := ui.ButtonBase(c.Key("view-exec")).Gap(3).Padding(2, 0).
							AlignItems(ui.Center).Shrink(0).Cursor(ui.CursorPointer).Label(locales.ActionView())
						col := colAccent
						if btn.Hovered() {
							col = darker(colAccent, 0.12)
						}
						btn.Children(func() {
							ui.Text(c, locales.ActionView()).FontSize(12).TextColor(col).SingleLine()
							ui.Icon(c, iconChevronRight).Size(11, 11).TextColor(col)
						})
						if btn.Clicked() {
							st.tab = 1
							for i, f := range files {
								if f.Path == s.Exec[0] {
									st.file = i
									break
								}
							}
						}
					}
				})
				row.Tooltip(execTip)
				rowIdx++
			}
			for _, f := range extra {
				insetGroupRow(c, "fm-"+f.Key, rowIdx == 0, 40, func() {
					ui.Icon(c, iconDoc).Size(16, 16).TextColor(colTextDisabled).Shrink(0)
					ui.Text(c, f.Key).FontSize(12).Font("monospace").TextColor(colTextMuted).Width(96).Shrink(0).SingleLine()
					ui.Text(c, f.Value).FontSize(13).LineHeight(1.45).TextColor(colTextPrimary).Grow(1).MinWidth(0)
				})
				rowIdx++
			}
		})
	})
}

// skillFrontmatterCard 在「文件」页选中 SKILL.md 时显示完整 frontmatter（含 name / description）。
func skillFrontmatterCard(c *ui.Context, fields []library.SkillField) {
	tools, extra := splitFrontmatterFields(fields, true)
	if len(tools) == 0 && len(extra) == 0 {
		return
	}
	insetGroup(c, func() {
		rowIdx := 0
		for _, f := range tools {
			insetGroupRow(c, "fm-tool-"+f.Key, rowIdx == 0, 40, func() {
				ui.Icon(c, iconTerminal).Size(16, 16).TextColor(colWarnText).Shrink(0)
				ui.Text(c, f.Key).FontSize(12).Font("monospace").TextColor(colWarnText).Width(96).Shrink(0).SingleLine()
				ui.Column(c).Grow(1).MinWidth(0).Gap(2).Children(func() {
					ui.Text(c, f.Value).FontSize(12).FontWeight(500).Font("monospace").TextColor(colWarnText)
					ui.Text(c, locales.ImportCommandsAllowed()).FontSize(12).TextColor(colWarnText)
				})
			})
			rowIdx++
		}
		for _, f := range extra {
			insetGroupRow(c, "fm-"+f.Key, rowIdx == 0, 40, func() {
				ui.Icon(c, iconDoc).Size(16, 16).TextColor(colTextDisabled).Shrink(0)
				ui.Text(c, f.Key).FontSize(12).Font("monospace").TextColor(colTextMuted).Width(96).Shrink(0).SingleLine()
				v := ui.Text(c, f.Value).FontSize(13).LineHeight(1.45).TextColor(colTextPrimary).Grow(1).MinWidth(0)
				if f.Key == "description" {
					v.TextColor(colTextSecondary).MaxLines(2)
				}
			})
			rowIdx++
		}
	})
}

// ensureReadOnlyEditor 复用缓存的纯白只读代码编辑器；SKILL.md 原文模式开启软换行，方便查看长 frontmatter 与链接。
func (a *app) ensureReadOnlyEditor(cacheKey, content, filePath string, softWrap bool) *editor.State {
	st := &a.imp
	if es := st.editors[cacheKey]; es != nil {
		return es
	}
	es := newReadOnlyEditor(content, filePath, softWrap)
	if st.editors == nil {
		st.editors = map[string]*editor.State{}
	}
	st.editors[cacheKey] = es
	return es
}

func newReadOnlyEditor(content, filePath string, softWrap bool) *editor.State {
	es := &editor.State{
		Buffer:          editor.NewBuffer(content),
		ReadOnly:        true,
		SoftWrap:        softWrap,
		NoIndentGuides:  true,
		NoFolding:       true,
		NoActiveLine:    true,
		NoScrollPastEnd: true,
		Label:           filePath,
	}
	es.Buffer.SetLanguage(editor.LanguageFor(filePath))
	return es
}

// importSkillRawView 铺满固定页头与分隔线下方的全部剩余宽高，以纯白原生只读编辑器展示完整 SKILL.md。
func (a *app) importSkillRawView(c *ui.Context, s *library.ImportSkill, f library.File) {
	k := a.importKey(s)
	es := a.ensureReadOnlyEditor("raw:"+k, f.Content, f.Path, true)
	editor.View(c.Key("raw-ed-"+k), es).Grow(1).MinHeight(0)
}

// skillMarkdownTheme 是 SKILL.md 详情页专用排版主题：所有标题严格低于 20px 主标题，
// 引用块收敛为 2px 细灰线，代码块融入无边框 #f5f5f7。
func skillMarkdownTheme() *markdown.Theme {
	i := 0
	if darkUI {
		i = 1
	}
	if skillMarkdownThemes[i] == nil {
		skillMarkdownThemes[i] = newSkillMarkdownTheme()
	}
	return skillMarkdownThemes[i]
}

// skillMarkdownThemes 是浅色、深色各一份，用到时按当前颜色建好留着：
// Markdown 视图按主题指针缓存排版，每帧新建会白白重排。
var skillMarkdownThemes [2]*markdown.Theme

func newSkillMarkdownTheme() *markdown.Theme {
	th := &markdown.Theme{
		CodeLineHeight:      1.5,
		Text:                colTextPrimary,
		Muted:               colTextDisabled,
		Link:                colAccent,
		Strong:              colTextPrimary,
		StrongWeight:        600,
		Code:                colTextPrimary,
		CodeBackground:      colBgSubtle,
		BlockSpacing:        0.62,
		ItemSpacing:         0.25,
		QuoteBarWidth:       2,
		QuoteBar:            colBorderDefault,
		QuoteText:           colTextSecondary,
		Rule:                colBorderDefault,
		CodeBlockBackground: colBgSubtle,
		CodeBlockBorder:     colBgSubtle,
		CodeBlockRadius:     8,
		CodeBlockPadding:    1.0,
		TableBorder:         colBorderDefault,
		TableHeader:         colBgSubtle,
		TableCellPadding:    [2]float32{0.85, 0.54},
		Headings: [6]markdown.HeadingStyle{
			{Weight: 600, Color: colTextPrimary, Top: 1.38, Bottom: 0.46},
			{Weight: 600, Color: colTextPrimary, Top: 1.38, Bottom: 0.46},
			{Weight: 600, Color: colTextPrimary, Top: 1.08, Bottom: 0.31},
			{Weight: 600, Color: colTextPrimary, Top: 0.92, Bottom: 0.25},
			{Weight: 600, Color: colTextPrimary, Top: 0.85, Bottom: 0.25},
			{Weight: 600, Color: colTextSecondary, Top: 0.85, Bottom: 0.25},
		},
	}
	readingSizes(th)
	return th
}

// readingSizes 是 SKILL.md 这类长文的整数字号：正文 14，比界面正文的 13 大一档，
// 和 VS Code 的 Markdown 预览一样；代码 13；标题 18/16/15/14/13/13，都低于页面
// 上 20 以上的技能名。主题里的字号是相对正文的倍数，这里按 pt 换算。
func readingSizes(th *markdown.Theme) {
	const body = 14
	th.FontSize = body
	th.LineHeight = 22.0 / body
	th.CodeSize = 13.0 / body
	for i, pt := range [6]float32{18, 16, 15, 14, 13, 13} {
		th.Headings[i].Size = pt / body
	}
}

// skillCodeBlock 用 #f5f5f7 无边框圆角块绘制代码，只保留右上角淡灰语言角标，去掉 bash/Copy 顶栏。
func skillCodeBlock(c *ui.Context, cb *markdown.CodeBlockView) {
	box := ui.Column(c).Background(colBgSubtle).Radius(8).Padding(8, 0, 1, 0).AlignSelf(ui.Stretch).Clip()
	label := "Code"
	if cb.Language != "" {
		label = "Code, " + cb.Language
	}
	box.Role(ui.RoleGroup).Label(label)
	box.Children(func() {
		cb.Body(c)
		if cb.Language != "" {
			ui.Text(c, cb.Language).
				Font("monospace").FontSize(11).TextColor(colTextDisabled).Unselectable().
				Background(colBgSubtle.Alpha(0.9)).Padding(1, 4).Radius(3).
				Absolute().Top(8).Right(12)
		}
	})
}

// skillMarkdownOptions 按技能缓存 Markdown 选项；LoadImage 使用各自技能目录，防止同名相对路径串图。
func (a *app) skillMarkdownOptions(s *library.ImportSkill) *markdown.Options {
	st := &a.imp
	k := a.importKey(s)
	if o := st.mdOpts[k]; o != nil {
		o.Theme = skillMarkdownTheme() // 深浅色换过之后跟着换
		return o
	}
	o := &markdown.Options{
		Theme:     skillMarkdownTheme(),
		CodeBlock: skillCodeBlock,
	}
	if s.Dir != "" {
		o.LoadImage = markdown.FileImages(s.Dir)
	}
	if st.mdOpts == nil {
		st.mdOpts = map[string]*markdown.Options{}
	}
	st.mdOpts[k] = o
	return o
}

// cleanSkillMarkdownBody 过滤本地拿不到的外链图片/徽章段落与注释/纯外链图片 HTML 块，
// 避免渲染出灰色图片占位框和多余空行；技能目录内真实存在的本地图片（如 assets/*.png）完整保留。
func cleanSkillMarkdownBody(body, skillDir string) string {
	doc := parser.Parse(body)
	type byteSpan struct{ start, end int }
	var drop []byteSpan
	for i := 1; i < doc.Len(); {
		b := doc.Block(i)
		switch b.Kind {
		case parser.HTMLBlock:
			if isSuppressibleHTMLBlock(body[b.Start:b.End], skillDir) {
				drop = append(drop, byteSpan{b.Start, extendPastBlankLines(body, b.End)})
			}
		case parser.Paragraph:
			if isUnresolvableImageOnlyParagraph(doc.Inlines(i), skillDir) {
				drop = append(drop, byteSpan{b.Start, extendPastBlankLines(body, b.End)})
			}
		}
		if b.Next <= i {
			i++
		} else {
			i = b.Next
		}
	}
	if len(drop) == 0 {
		return body
	}
	var out strings.Builder
	out.Grow(len(body))
	pos := 0
	for _, s := range drop {
		if s.start > pos {
			out.WriteString(body[pos:s.start])
		}
		if s.end > pos {
			pos = s.end
		}
	}
	if pos < len(body) {
		out.WriteString(body[pos:])
	}
	return strings.TrimLeft(out.String(), "\r\n")
}

func extendPastBlankLines(src string, end int) int {
	i := end
	if i < len(src) && src[i] == '\r' {
		i++
	}
	if i < len(src) && src[i] == '\n' {
		i++
	}
	for i < len(src) {
		j := i
		for j < len(src) && (src[j] == ' ' || src[j] == '\t') {
			j++
		}
		if j < len(src) && src[j] == '\r' {
			j++
		}
		if j < len(src) && src[j] == '\n' {
			i = j + 1
			continue
		}
		break
	}
	return i
}

func isUnresolvableImageOnlyParagraph(inls []parser.Inline, skillDir string) bool {
	if len(inls) == 0 {
		return false
	}
	hasUnresolvable, hasResolvable := false, false
	for k := 0; k < len(inls); {
		in := inls[k]
		next := in.Next
		if next <= k {
			next = k + 1
		}
		switch in.Kind {
		case parser.SoftBreak, parser.LineBreak:
			k++
		case parser.Text:
			if strings.Trim(in.Text, " \t\r\n|·•/") != "" { //separator:ignore 认出 README 里只有徽章和分隔符的行
				return false
			}
			k++
		case parser.Image:
			if canResolveSkillImage(in.Dest, skillDir) {
				hasResolvable = true
			} else {
				hasUnresolvable = true
			}
			k = next
		case parser.Link:
			ok, u, r := linkHoldsOnlyImages(inls, k+1, next, skillDir)
			if !ok {
				return false
			}
			hasUnresolvable = hasUnresolvable || u
			hasResolvable = hasResolvable || r
			k = next
		case parser.HTML:
			if src, isImg := extractHTMLImgSrc(in.Text); isImg {
				if canResolveSkillImage(src, skillDir) {
					hasResolvable = true
				} else {
					hasUnresolvable = true
				}
				k++
				continue
			}
			if !isHarmlessHTMLTagOrComment(in.Text) {
				return false
			}
			k++
		default:
			return false
		}
	}
	return hasUnresolvable && !hasResolvable
}

func linkHoldsOnlyImages(inls []parser.Inline, start, end int, skillDir string) (onlyImages, hasUnresolvable, hasResolvable bool) {
	sawImage := false
	for k := start; k < end; {
		in := inls[k]
		next := in.Next
		if next <= k || next > end {
			next = k + 1
		}
		switch in.Kind {
		case parser.Image:
			sawImage = true
			if canResolveSkillImage(in.Dest, skillDir) {
				hasResolvable = true
			} else {
				hasUnresolvable = true
			}
			// 跳过图片内部的 alt text 子节点，避免把 [![alt](url)](href) 误判成普通文字链接。
			k = next
		case parser.SoftBreak, parser.LineBreak:
			k++
		case parser.Text:
			if strings.TrimSpace(in.Text) != "" {
				return false, false, false
			}
			k++
		default:
			return false, false, false
		}
	}
	return sawImage, hasUnresolvable, hasResolvable
}

func isSuppressibleHTMLBlock(raw, skillDir string) bool {
	stripped := stripHTMLComments(raw)
	if strings.TrimSpace(stripped) == "" {
		return true
	}
	// 无任何正文文字、只剩图片/居中包装标签的 HTML 块：若其中所有图片都无法在本地解析，则隐藏
	if hasVisibleTextOutsideTags(stripped) {
		return false
	}
	hasUnresolvable, hasResolvable := false, false
	rest := stripped
	for {
		i := strings.Index(strings.ToLower(rest), "<img")
		if i < 0 {
			break
		}
		gt := strings.IndexByte(rest[i:], '>')
		if gt < 0 {
			break
		}
		tag := rest[i : i+gt+1]
		if src, ok := extractHTMLImgSrc(tag); ok {
			if canResolveSkillImage(src, skillDir) {
				hasResolvable = true
			} else {
				hasUnresolvable = true
			}
		} else {
			hasUnresolvable = true
		}
		rest = rest[i+gt+1:]
	}
	return hasUnresolvable && !hasResolvable
}

func stripHTMLComments(s string) string {
	for {
		a := strings.Index(s, "<!--")
		if a < 0 {
			return s
		}
		b := strings.Index(s[a+4:], "-->")
		if b < 0 {
			return s[:a]
		}
		s = s[:a] + s[a+4+b+3:]
	}
}

func hasVisibleTextOutsideTags(s string) bool {
	inTag := false
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '<':
			inTag = true
		case '>':
			inTag = false
		default:
			if !inTag && s[i] != ' ' && s[i] != '\t' && s[i] != '\r' && s[i] != '\n' {
				return true
			}
		}
	}
	return false
}

func isHarmlessHTMLTagOrComment(s string) bool {
	t := strings.TrimSpace(stripHTMLComments(s))
	if t == "" {
		return true
	}
	low := strings.ToLower(t)
	for _, prefix := range []string{"<a ", "<a>", "</a>", "<p ", "<p>", "</p>", "<div ", "<div>", "</div>", "<center>", "</center>", "<br", "<picture", "</picture>", "<source "} {
		if strings.HasPrefix(low, prefix) && strings.HasSuffix(low, ">") {
			return true
		}
	}
	return false
}

func extractHTMLImgSrc(tag string) (string, bool) {
	low := strings.ToLower(tag)
	if !strings.Contains(low, "<img") {
		return "", false
	}
	for _, attr := range []string{`src="`, `src='`} {
		if i := strings.Index(low, attr); i >= 0 {
			quote := attr[len(attr)-1]
			valStart := i + len(attr)
			if end := strings.IndexByte(tag[valStart:], quote); end >= 0 {
				return tag[valStart : valStart+end], true
			}
		}
	}
	return "", false
}

func canResolveSkillImage(dest, skillDir string) bool {
	if skillDir == "" || dest == "" {
		return false
	}
	if strings.Contains(dest, "://") || strings.HasPrefix(dest, "//") || strings.HasPrefix(strings.ToLower(dest), "data:") {
		return false
	}
	if i := strings.IndexAny(dest, "?#"); i >= 0 {
		dest = dest[:i]
	}
	if u, err := url.PathUnescape(dest); err == nil {
		dest = u
	}
	clean := filepath.Clean(filepath.FromSlash(dest))
	if clean == "" || clean == "." || filepath.IsAbs(clean) || strings.HasPrefix(clean, "..") {
		return false
	}
	info, err := os.Stat(filepath.Join(skillDir, clean))
	return err == nil && !info.IsDir()
}
