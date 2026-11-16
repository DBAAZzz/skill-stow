package main

import (
	"embed"

	"github.com/egoist/mygo/ui"

	"skillmanager/internal/library"
)

// icon 是 16px、1.6 描边的线性图标（24 视口）。
func icon(shapes string) *ui.SVG {
	return ui.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round">` + shapes + `</svg>`))
}

// solidIcon 是实心图标（24 视口，currentColor 填充），次要的部分淡一些。
func solidIcon(shapes string) *ui.SVG {
	return ui.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="currentColor">` + shapes + `</svg>`))
}

// 侧边栏菜单的图标（画板「侧边栏」nav3）：单色实心，颜色跟着这一行的文字走。
var (
	iconNavTidy    = solidIcon(`<g fill="none" stroke="currentColor" stroke-width="2.3" stroke-linecap="round" stroke-linejoin="round"><path d="M3.8 6.6l1.9 1.9 3.4-3.6M12.6 6.8h7.6"/><path d="M3.8 12.2l1.9 1.9 3.4-3.6M12.6 12.4h7.6"/><g opacity=".5"><circle cx="6.2" cy="18" r="1.9"/><path d="M12.6 18h7.6"/></g></g>`)
	iconNavUsage   = solidIcon(`<rect x="3.4" y="12.6" width="4.8" height="8" rx="1.7" opacity=".5"/><rect x="9.6" y="8" width="4.8" height="12.6" rx="1.7" opacity=".75"/><rect x="15.8" y="3.4" width="4.8" height="17.2" rx="1.7"/>`)
	iconNavMine    = solidIcon(`<circle cx="12" cy="7.6" r="4.1"/><path d="M3.9 19.9c.7-4.3 4-7 8.1-7s7.4 2.7 8.1 7a1.1 1.1 0 0 1-1.1 1.3H5a1.1 1.1 0 0 1-1.1-1.3Z"/>`)
	iconNavUpdates = solidIcon(`<g fill="none" stroke="currentColor" stroke-width="2.1" stroke-linecap="round" stroke-linejoin="round"><path d="M12 18.5a6.5 6.5 0 1 1 5.6-3.2"/><path d="M19.5 11.5v4h-4"/></g><circle cx="12" cy="12" r="2.1" fill="currentColor" stroke="none"/>`)
	iconNavBuiltin = solidIcon(`<path d="M12 2.6 20.4 7 12 11.4 3.6 7Z" opacity=".55"/><path d="M3 8.3 11.2 12.6v9L3 17.2Z" opacity=".8"/><path d="M12.8 12.6 21 8.3v8.9l-8.2 4.4Z"/><path d="M7.6 4.6 16 9.1v3.3" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" opacity=".9"/>`)
	iconNavFolder  = solidIcon(`<path d="M3 7.25A2.25 2.25 0 0 1 5.25 5H9.1a1.5 1.5 0 0 1 1.06.44L11.5 6.75h7.25A2.25 2.25 0 0 1 21 9v8.75A2.25 2.25 0 0 1 18.75 20H5.25A2.25 2.25 0 0 1 3 17.75Z"/>`)
)

var (
	iconAll          = icon(`<rect x="7" y="7" width="13" height="13" rx="2.5"/><path d="M4 15V6.5A2.5 2.5 0 0 1 6.5 4H15"/>`)
	iconWarn         = icon(`<path d="M12 4 21 19.5H3z"/><path d="M12 10v4.5M12 17.2v.01"/>`)
	iconFolder       = icon(`<path d="M3.5 7.5a2 2 0 0 1 2-2h4l2 2h7a2 2 0 0 1 2 2v8a2 2 0 0 1-2 2h-13a2 2 0 0 1-2-2z"/>`)
	iconFolderAdd    = icon(`<path d="M3.5 7.5a2 2 0 0 1 2-2h4l2 2h7a2 2 0 0 1 2 2v8a2 2 0 0 1-2 2h-13a2 2 0 0 1-2-2z"/><path d="M12 10.5v6M9 13.5h6"/>`)
	iconLink         = icon(`<path d="M10 14a4 4 0 0 0 5.7 0l3-3a4 4 0 0 0-5.7-5.7l-1 1M14 10a4 4 0 0 0-5.7 0l-3 3a4 4 0 0 0 5.7 5.7l1-1"/>`)
	iconGear         = icon(`<circle cx="12" cy="12" r="3"/><path d="M12 3v2.5M12 18.5V21M3 12h2.5M18.5 12H21M5.6 5.6l1.8 1.8M16.6 16.6l1.8 1.8M5.6 18.4l1.8-1.8M16.6 7.4l1.8-1.8"/>`)
	iconBack         = icon(`<path d="m15 5-7 7 7 7"/>`)
	iconDoc          = icon(`<path d="M7 3.5h7l4.5 4.5v12.5H7z"/><path d="M14 3.5V8h4.5"/>`)
	iconLock         = icon(`<rect x="5.5" y="10.5" width="13" height="9.5" rx="2"/><path d="M8.5 10.5V8a3.5 3.5 0 0 1 7 0v2.5"/>`)
	iconExternal     = icon(`<path d="M14 4h6v6M20 4l-9 9M18 14v5a1 1 0 0 1-1 1H5a1 1 0 0 1-1-1V7a1 1 0 0 1 1-1h5"/>`)
	iconSearch       = icon(`<circle cx="10.5" cy="10.5" r="6"/><path d="m15 15 5 5"/>`)
	iconFilter       = icon(`<path d="M4 7h16M7 12h10M10 17h4"/>`)
	iconClock        = icon(`<circle cx="12" cy="12" r="8.5"/><path d="M12 7.5V12l3 2"/>`)
	iconClose        = icon(`<path d="M7 7l10 10M17 7 7 17"/>`)
	iconPlus         = icon(`<path d="M12 5v14M5 12h14"/>`)
	iconOff          = icon(`<circle cx="12" cy="12" r="8"/><path d="M6.5 6.5l11 11"/>`)
	iconMinusCircle  = icon(`<circle cx="12" cy="12" r="8.5"/><path d="M8.5 12h7"/>`)
	iconUpDown       = icon(`<path d="m8 9.5 4-4 4 4M8 14.5l4 4 4-4"/>`)
	iconRobot        = icon(`<rect x="4.5" y="8" width="15" height="11" rx="3"/><path d="M12 8V5M12 4.2v.01"/><circle cx="9.3" cy="13.3" r="1.1" fill="currentColor" stroke="none"/><circle cx="14.7" cy="13.3" r="1.1" fill="currentColor" stroke="none"/><path d="M2.5 12.5v2M21.5 12.5v2"/>`)
	iconSettings     = icon(`<path d="M12.22 2h-.44a2 2 0 0 0-2 2v.18a2 2 0 0 1-1 1.73l-.43.25a2 2 0 0 1-2 0l-.15-.08a2 2 0 0 0-2.73.73l-.22.38a2 2 0 0 0 .73 2.73l.15.1a2 2 0 0 1 1 1.72v.51a2 2 0 0 1-1 1.74l-.15.09a2 2 0 0 0-.73 2.73l.22.38a2 2 0 0 0 2.73.73l.15-.08a2 2 0 0 1 2 0l.43.25a2 2 0 0 1 1 1.73V20a2 2 0 0 0 2 2h.44a2 2 0 0 0 2-2v-.18a2 2 0 0 1 1-1.73l.43-.25a2 2 0 0 1 2 0l.15.08a2 2 0 0 0 2.73-.73l.22-.39a2 2 0 0 0-.73-2.73l-.15-.08a2 2 0 0 1-1-1.74v-.5a2 2 0 0 1 1-1.74l.15-.09a2 2 0 0 0 .73-2.73l-.22-.38a2 2 0 0 0-2.73-.73l-.15.08a2 2 0 0 1-2 0l-.43-.25a2 2 0 0 1-1-1.73V4a2 2 0 0 0-2-2z"/><circle cx="12" cy="12" r="3"/>`)
	iconFolderFill   = ui.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="currentColor"><path d="M3 7.2A2.2 2.2 0 0 1 5.2 5h3.9a2 2 0 0 1 1.4.6L12 7h6.8A2.2 2.2 0 0 1 21 9.2v8.6a2.2 2.2 0 0 1-2.2 2.2H5.2A2.2 2.2 0 0 1 3 17.8z"/></svg>`))
	iconChevronRight = icon(`<path d="m9 5.5 6.5 6.5L9 18.5"/>`)
	iconChevronDown  = icon(`<path d="m5.5 9 6.5 6.5L18.5 9"/>`)
	iconStack        = icon(`<rect x="3.5" y="8" width="17" height="12" rx="2.2"/><path d="M5.5 5h13M7.5 2.5h9"/>`)
	iconCheck        = icon(`<path d="m5 12.5 4.5 4.5L19 7.5"/>`)
	iconBatch        = icon(`<rect x="3.5" y="4.5" width="6" height="6" rx="1.5"/><path d="m4.9 7.6 1.2 1.2 2.2-2.4M13 7.5h7.5"/><rect x="3.5" y="13.5" width="6" height="6" rx="1.5"/><path d="M13 16.5h7.5"/>`)
	iconSidebar      = icon(`<rect x="3" y="4.5" width="18" height="15" rx="3"/><path d="M9.5 4.5v15M5.8 8.5h1.2M5.8 11h1.2M5.8 13.5h1.2"/>`)
	// iconSlash 是停用的 Agent 图标上的斜线：收在图标里，外面一圈白边，
	// 叠在灰色图标上也看得清
	iconSlash    = ui.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" stroke-linecap="round"><path d="M19.5 4.5 4.5 19.5" stroke="#fff" stroke-width="5"/><path d="M19.5 4.5 4.5 19.5" stroke="currentColor" stroke-width="2.2"/></svg>`))
	iconMore     = icon(`<circle cx="6" cy="12" r="1.3" fill="currentColor" stroke="none"/><circle cx="12" cy="12" r="1.3" fill="currentColor" stroke="none"/><circle cx="18" cy="12" r="1.3" fill="currentColor" stroke="none"/>`)
	iconTrash    = icon(`<path d="M4.5 6.5h15M9.5 6.5V4.5h5v2M6.5 6.5l1 13h9l1-13M10 10.5v5.5M14 10.5v5.5"/>`)
	iconRefresh  = icon(`<path d="M19.5 12a7.5 7.5 0 1 1-2.2-5.3"/><path d="M19.5 4.5v4h-4"/>`)
	iconDownload = ui.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.1" stroke-linecap="round" stroke-linejoin="round"><path d="M12 4v11M7.5 11l4.5 4.5 4.5-4.5M5 19.5h14"/></svg>`))
	iconGlobe    = ui.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><circle cx="12" cy="12" r="8"/><path d="M4.5 12h15M12 4a11 11 0 0 1 0 16M12 4a11 11 0 0 0 0 16"/></svg>`))
	iconLinkBold = ui.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M10 13a5 5 0 0 0 7.54.54l3-3a5 5 0 0 0-7.07-7.07l-1.72 1.71"/><path d="M14 11a5 5 0 0 0-7.54-.54l-3 3a5 5 0 0 0 7.07 7.07l1.71-1.71"/></svg>`))
	// iconGitHub 是 GitHub 的 logo（Octicons mark-github，16 视口）。
	iconGitHub      = ui.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 16 16" fill="currentColor"><path d="M8 0c4.42 0 8 3.58 8 8a8.013 8.013 0 0 1-5.45 7.59c-.4.08-.55-.17-.55-.38 0-.27.01-1.13.01-2.2 0-.75-.25-1.23-.54-1.48 1.78-.2 3.65-.88 3.65-3.95 0-.88-.31-1.59-.82-2.15.08-.2.36-1.02-.08-2.12 0 0-.67-.22-2.2.82-.64-.18-1.32-.27-2-.27-.68 0-1.36.09-2 .27-1.53-1.03-2.2-.82-2.2-.82-.44 1.1-.16 1.92-.08 2.12-.51.56-.82 1.28-.82 2.15 0 3.06 1.86 3.75 3.64 3.95-.23.2-.44.55-.51 1.07-.46.21-1.61.55-2.33-.66-.15-.24-.6-.83-1.23-.82-.67.01-.27.38.01.53.34.19.73.9.82 1.13.16.45.68 1.31 2.69.94 0 .67.01 1.3.01 1.49 0 .21-.15.45-.55.38A7.995 7.995 0 0 1 0 8c0-4.42 3.58-8 8-8Z"/></svg>`))
	iconGitCommit   = ui.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><circle cx="12" cy="12" r="3.5"/><path d="M3 12h5.5M15.5 12H21"/></svg>`))
	iconAgentGrid   = icon(`<rect x="3.5" y="3.5" width="7" height="7" rx="1.6"/><rect x="13.5" y="3.5" width="7" height="7" rx="1.6"/><rect x="3.5" y="13.5" width="7" height="7" rx="1.6"/><rect x="13.5" y="13.5" width="7" height="7" rx="1.6"/>`)
	iconUnbound     = ui.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"><rect x="4" y="4" width="16" height="16" rx="4" stroke-dasharray="3.2 2.5"/><circle cx="12" cy="12" r="2.2" fill="currentColor" stroke="none" opacity=".55"/></svg>`))
	iconEyeOff      = icon(`<path d="M17.9 17.9A10.1 10.1 0 0 1 12 20c-6 0-10-8-10-8a18.5 18.5 0 0 1 5.1-5.9M9.9 4.2A9.1 9.1 0 0 1 12 4c6 0 10 8 10 8a18.5 18.5 0 0 1-2.2 3.2"/><path d="M14.1 14.1a3 3 0 1 1-4.2-4.2"/><path d="M3 3l18 18"/>`)
	iconPauseCircle = icon(`<circle cx="12" cy="12" r="8.5"/><path d="M10 9v6M14 9v6"/>`)
	iconInbox       = icon(`<polyline points="22 12 16 12 14 15 10 15 8 12 2 12"/><path d="M5.45 5.11 2 12v6a2 2 0 0 0 2 2h16a2 2 0 0 0 2-2v-6l-3.45-6.89A2 2 0 0 0 16.76 4H7.24a2 2 0 0 0-1.79 1.11z"/>`)
	// iconEmptyTray 是「还没有技能」空状态的大符号（托盘和落进去的箭头）：
	// 按 52px 显示画的，描边比 16px 的图标细，放大后和系统符号一样轻。
	iconEmptyTray = ui.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.15" stroke-linecap="round" stroke-linejoin="round"><path d="M12 3v8.6M8.7 8.4l3.3 3.3 3.3-3.3"/><path d="M7.3 9.3H6.2a1.8 1.8 0 0 0-1.68 1.16L3.5 13.4v4.6a2.2 2.2 0 0 0 2.2 2.2h12.6a2.2 2.2 0 0 0 2.2-2.2v-4.6l-1.02-2.94A1.8 1.8 0 0 0 17.8 9.3h-1.1"/><path d="M3.5 13.4h4.3l1.3 2.4h5.8l1.3-2.4h4.3"/></svg>`))
	// iconEmptySearch、iconEmptyFilter 是搜索、筛选没有结果时空状态的大符号。
	iconEmptySearch = ui.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.15" stroke-linecap="round" stroke-linejoin="round"><circle cx="10.5" cy="10.5" r="6.3"/><path d="m15.2 15.2 5.3 5.3"/></svg>`))
	iconEmptyFilter = ui.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.15" stroke-linecap="round" stroke-linejoin="round"><circle cx="12" cy="12" r="8.6"/><path d="M7.6 9.4h8.8M9.2 12.4h5.6M10.8 15.4h2.4"/></svg>`))
	iconCode        = ui.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M8.5 7 3.5 12l5 5M15.5 7 20.5 12l-5 5M13.5 4.5l-3 15"/></svg>`))
)

// 整理页每类问题的图标（画板「问题图标」第三轮 1a–6a）：上亮下暗的彩色方块，
// 白色实心图形，次要的部分淡一些。颜色按要不要你管：红=会让 Agent 读不到，
// 橙=要你决定，蓝=能一键整理，灰=建议。图形以 assets/issues 里的 SVG 为准
// （24 视口，currentColor 画白色，遮罩挖空），最初由画板导出。
var issueIcons = map[library.Issue]issueStyle{
	library.SameCopies:    {issueBlue, issueSVG("same-copies")},
	library.DiffCopies:    {issueOrange, issueSVG("diff-copies")},
	library.ExtraLinks:    {issueBlue, issueSVG("extra-links")},
	library.BrokenLinks:   {issueRed, issueSVG("broken-links")},
	library.NoDescription: {issueGray, issueSVG("no-description")},
	library.GlobalLinked:  {issueOrange, issueSVG("global-linked")},
	library.StaleRecords:  {issueBlue, issueSVG("stale-records")},
	library.Incomplete:    {issueRed, issueSVG("incomplete")},
}

type issueStyle struct {
	tone  [2]ui.Color // 方块顶部、底部的颜色
	glyph *ui.SVG
}

var ()

//go:embed assets/issues
var issueAssets embed.FS

func issueSVG(name string) *ui.SVG {
	b, err := issueAssets.ReadFile("assets/issues/" + name + ".svg")
	if err != nil {
		panic(err)
	}
	return ui.MustParseSVG(b)
}

// issueIcon 是一类问题的图标，size 是方块的边长。顶边一道 0.5 的高光、
// 一圈很淡的深色细边，方块下面一点点投影。
func issueIcon(c *ui.Context, k library.Issue, size float32) {
	ic := issueIcons[k]
	ui.Box(c).Size(size, size).Shrink(0).Radius(size*0.235).Center().TextColor(white).
		LinearGradient(ui.LinearGradient{From: ic.tone[0], To: ic.tone[1], Angle: 180}).
		InsetShadow(0, 0.5, 0, 0, white.Alpha(0.38)).
		InsetShadow(0, 0, 0, 0.5, ink(0.1)).
		Shadow(0, 0.5, 1.5, 0, shadow(0.14)).Children(func() {
		ui.Image(c, ic.glyph).Size(size*0.64, size*0.64)
	})
}
