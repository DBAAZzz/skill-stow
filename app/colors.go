package main

import (
	"fmt"
	"strings"

	"github.com/egoist/mygo/ui"
)

// 界面的全部颜色都在这个文件里（agents.go 的品牌色除外），别的文件只用这里
// 的名字，TestColorsLiveInColorsFile 拦着在别处写颜色值。
//
// 颜色按用途起名（设计规范 §13.3），浅色、深色两个值写在 tones 的同一行。
// 先在下面的 var 里声明名字，再在 tones 里写值；漏写值的
// TestEveryColorHasDarkValue 会拦。新颜色先看能不能用已有的名字：悬停、
// 按下这类在底色上叠出来的，用 tintSolid、tintInk 或
// colInkHover.Over(底色) 在用的地方合成，不另起名字。总数不超过 75 个
// （TestPaletteStaysSmall）。

// 颜色的名字，值在下面的 tones 里。
var (
	// 表面
	colBgSurface, colBgPage, colBgPane, colBgSubtle, colBgFill,
	colBgCard, colBgControl, colBgThumb, colBgSidebar, colBgWindow,
	colBgTitlebar, colBgSideVeil, colBgGlass, colBgToastGlass, colBgScrim,
	colBgLogo ui.Color

	// 文字
	colTextPrimary, colTextBody, colTextSecondary, colTextMuted, colTextDisabled ui.Color

	// 线
	colBorderSubtle, colBorderDefault, colBorderStrong, colBorderSidebar ui.Color

	// 交互
	colInkHover, colInkPress, colInkSelected, colControlTrack, colControlMixed,
	colControlDisabledOn ui.Color

	// 强调
	colAccent, colAccentText, colAccentBg, colAccentEdge ui.Color

	// 状态
	colOK, colOKText, colOKBg, colOKEdge, colWarn,
	colWarnText, colWarnBg, colWarnEdge, colDanger, colDangerText,
	colDangerBg, colDangerEdge ui.Color

	// 领域
	colDiffAddRow, colDiffAddWord, colDiffAddText, colDiffDelRow, colDiffDelWord,
	colDiffDelText, colChartNone, colChartLow, colChartMid, colChartFocus,
	colFolder, colGroupWebsite, colGroupHandle ui.Color

	// 组件例外
	colMenuHover, colMenuBorder ui.Color
)

// tone 是一个随深浅色变的颜色，值的写法同 CSS：#rrggbb 或 rgba(r,g,b,a)。
type tone struct {
	c           *ui.Color
	light, dark string
}

// tones 是每个颜色的浅色值和深色值。深色参照 macOS：内容区 #1e1e1e，卡片、
// 按钮这些浮起来的东西越往上越亮；状态色换成系统深色版，浅底标签换成深底
// 亮字。
var tones = []tone{
	// 表面
	{&colBgSurface, "#ffffff", "#1e1e1e"},                               // 内容区、面板的底；框架 Theme.Background
	{&colBgPage, "#f5f5f7", "#1e1e1e"},                                  // 放分组卡片的页面底（更新、使用情况、添加技能）；深色和内容区同色，卡片才浮得出来
	{&colBgPane, "#fbfbfc", "#232325"},                                  // 详情栏、文件列表、对比侧栏
	{&colBgSubtle, "#f5f5f7", "#262628"},                                // 看板列、嵌入分组、代码块、表头、只读卡片、叠层
	{&colBgFill, "#ececf0", "#3a3a3c"},                                  // 灰标签、胶囊、进度槽、分段轨道、说明块、图标垫
	{&colBgCard, "#ffffff", "#2a2a2c"},                                  // 卡片、分组框、对话框
	{&colBgControl, "#ffffff", "#38383a"},                               // 按钮、下拉、勾选框；框架 Theme.Surface
	{&colBgThumb, "#ffffff", "#5c5c60"},                                 // 分段控件滑块
	{&colBgSidebar, "#efeff1", "#262628"},                               // 侧边栏（没有毛玻璃时）、搜索框
	{&colBgWindow, "#ececee", "#202022"},                                // 设置窗口的底
	{&colBgTitlebar, "#e4e4e7", "#2a2a2c"},                              // 设置窗口顶栏
	{&colBgSideVeil, "rgba(255,255,255,0.45)", "rgba(30,30,30,0.40)"},   // 盖在毛玻璃侧边栏上：壁纸再深再亮，侧栏都停在同一档灰
	{&colBgGlass, "rgba(249,249,251,0.48)", "rgba(30,30,32,0.32)"},      // 菜单、⌘K 的毛玻璃叠色
	{&colBgToastGlass, "rgba(250,250,252,0.74)", "rgba(40,40,42,0.72)"}, // toast 的毛玻璃叠色
	{&colBgScrim, "rgba(0,0,0,0.18)", "rgba(0,0,0,0.45)"},               // 对话框遮罩
	{&colBgLogo, "#ffffff", "#ebebed"},                                  // 头像、网站图标垫的底；网站图标多是给白底画的，深色也垫浅底
	// 文字
	{&colTextPrimary, "#1d1d1f", "#ececee"},   // 标题、正文
	{&colTextBody, "#525257", "#c7c7cc"},      // 次级正文、路径、侧栏未选中、工具栏图标
	{&colTextSecondary, "#6e6e73", "#98989d"}, // 卡片简介、说明、计数、侧栏图标
	{&colTextMuted, "#8e8e93", "#8e8e93"},     // 数量、元信息、分组标题
	{&colTextDisabled, "#aeaeb2", "#636366"},  // 禁用、占位、极弱提示
	// 线
	{&colBorderSubtle, "rgba(0,0,0,0.06)", "rgba(255,255,255,0.08)"}, // 列表行间线、网格线、菜单分割线（叠加）
	{&colBorderDefault, "#e5e5ea", "#3a3a3c"},                        // 卡片边、分栏线、工具栏竖线；Theme.Border
	{&colBorderStrong, "#c7c7cc", "#545458"},                         // 勾选框边、虚线框
	{&colBorderSidebar, "#dcdce0", "#0e0e10"},                        // 侧栏、设置顶栏的结构分界线
	// 交互
	{&colInkHover, "rgba(0,0,0,0.045)", "rgba(255,255,255,0.07)"},   // 悬停（叠加）；实底控件用 .Over(底) 合成
	{&colInkPress, "rgba(0,0,0,0.09)", "rgba(255,255,255,0.13)"},    // 按下（叠加）；同上
	{&colInkSelected, "rgba(0,0,0,0.07)", "rgba(255,255,255,0.11)"}, // 中性的持续选中：侧栏、文件列表、设置标签
	{&colControlTrack, "#e3e3e8", "#48484a"},                        // 开关关着的轨道、停用的 Agent 图标底
	{&colControlMixed, "#8fc0ff", "#2f6fb8"},                        // 部分启用的开关
	{&colControlDisabledOn, "#b9d6fb", "#1f4a7a"},                   // 勾着但不能改的勾选框
	// 强调
	{&colAccent, "#007aff", "#0a84ff"},                                // 强调色、链接、焦点、实心按钮
	{&colAccentText, "#0062cc", "#6cb4ff"},                            // 浅蓝底上的字、激活的筛选
	{&colAccentBg, "#eef5ff", "#172a42"},                              // 选中卡片、勾选的行、淡蓝胶囊
	{&colAccentEdge, "rgba(0,122,255,0.24)", "rgba(10,132,255,0.40)"}, // 激活筛选、拖放区、标签描边
	// 状态
	{&colOK, "#34c759", "#30d158"},                                  // 成功图标、圆点、均值线
	{&colOKText, "#248a3d", "#30d158"},                              // 成功的字
	{&colOKBg, "#effaf2", "#16301f"},                                // 成功的浅底
	{&colOKEdge, "#c9ebd3", "#24553a"},                              // 成功的描边
	{&colWarn, "#ff9500", "#ff9f0a"},                                // 警告图标、圆点、橙色按钮
	{&colWarnText, "#c46a00", "#ff9f0a"},                            // 警告的字
	{&colWarnBg, "#fff7ea", "#3a2a12"},                              // 警告的浅底
	{&colWarnEdge, "#f4dcb4", "#5c4220"},                            // 警告的描边
	{&colDanger, "#ff3b30", "#ff453a"},                              // 危险实心按钮、图标
	{&colDangerText, "#d70015", "#ff6961"},                          // 危险的字：删除
	{&colDangerBg, "rgba(255,59,48,0.09)", "rgba(255,69,58,0.16)"},  // 危险的浅底、悬停
	{&colDangerEdge, "rgba(215,0,21,0.22)", "rgba(255,69,58,0.38)"}, // 危险的描边
	// 领域
	{&colDiffAddRow, "#e6ffec", "#1f3324"},                              // 对比页：新增行
	{&colDiffAddWord, "#abf2bc", "#285c33"},                             // 对比页：新增的词
	{&colDiffAddText, "#1a7f37", "#3fb950"},                             // 对比页：+ 号和数字
	{&colDiffDelRow, "#ffeef0", "#3a2223"},                              // 对比页：删除行
	{&colDiffDelWord, "#ffc1c6", "#7a3532"},                             // 对比页：删除的词
	{&colDiffDelText, "#cf222e", "#ff7b72"},                             // 对比页：- 号和数字
	{&colChartNone, "rgba(120,120,128,0.16)", "rgba(120,120,128,0.24)"}, // 使用情况：没用过、空槽
	{&colChartLow, "#a9cdff", "#234a75"},                                // 使用情况：很少
	{&colChartMid, "#4ca1ff", "#2f6db5"},                                // 使用情况：偶尔（常用 = colAccent）
	{&colChartFocus, "#0a68e0", "#0b5fd6"},                              // 使用情况：获得焦点的选中行
	{&colFolder, "#5ea1e8", "#64a8f0"},                                  // 文件夹图标
	{&colGroupWebsite, "#5856d6", "#5e5ce6"},                            // 更新页：由发布网站维护
	{&colGroupHandle, "#f56a00", "#ff7a1a"},                             // 更新页：需要处理
	// 组件例外
	{&colMenuHover, "rgba(0,0,0,0.065)", "rgba(255,255,255,0.1)"},  // 菜单行悬停：毛玻璃上要比普通悬停明显
	{&colMenuBorder, "rgba(0,0,0,0.20)", "rgba(255,255,255,0.14)"}, // 菜单面板描边
}

// 不随深浅色变的颜色：彩色上的白字、图标的渐变、设置里两个主题的示意图。
var (
	white = ui.RGB(255, 255, 255)
	black = ui.Hex("#000000")

	issueRed    = [2]ui.Color{ui.Hex("#ff7b70"), ui.Hex("#ff3b30")}
	issueOrange = [2]ui.Color{ui.Hex("#ffbd59"), ui.Hex("#ff9500")}
	issueBlue   = [2]ui.Color{ui.Hex("#52a8ff"), ui.Hex("#0a74ff")}
	issueGray   = [2]ui.Color{ui.Hex("#b4b4bc"), ui.Hex("#8a8a93")}

	squircleBlue   = [2]ui.Color{ui.Hex("#52a8ff"), ui.Hex("#0a74ff")}
	squircleTeal   = [2]ui.Color{ui.Hex("#2fd1a3"), ui.Hex("#00a67e")}
	squircleIndigo = [2]ui.Color{ui.Hex("#7c88ff"), ui.Hex("#5856d6")}
	squircleGray   = [2]ui.Color{ui.Hex("#b4b4bc"), ui.Hex("#8a8a93")}
	squircleGit    = [2]ui.Color{ui.Hex("#434954"), ui.Hex("#24292f")}

	// themePreviewColors 是设置里主题选项的小窗口示意（themePreview）：浅色、深色各一套。
	themePreviewColors = [2]struct{ bg, side, bar, line ui.Color }{
		{ui.Hex("#ffffff"), ui.Hex("#ececee"), ui.Hex("#c7c7cc"), ui.Hex("#e3e3e7")},
		{ui.Hex("#2a2a2d"), ui.Hex("#1e1e20"), ui.Hex("#6e6e73"), ui.Hex("#3e3e42")},
	}
)

var (
	darkUI     bool          // 现在用的是不是深色那套
	toneValues [2][]ui.Color // tones 解析好的值：[0] 浅色，[1] 深色
)

func init() {
	for _, t := range tones {
		toneValues[0] = append(toneValues[0], cssColor(t.light))
		toneValues[1] = append(toneValues[1], cssColor(t.dark))
	}
	setPalette(false)
}

// cssColor 解析 tones 里的颜色写法；写错了启动就 panic，测试一跑就知道。
func cssColor(s string) ui.Color {
	if strings.HasPrefix(s, "#") {
		return ui.Hex(s)
	}
	var r, g, b uint8
	var a float32
	if n, err := fmt.Sscanf(s, "rgba(%d,%d,%d,%g)", &r, &g, &b, &a); err != nil || n != 4 {
		panic("colors.go: bad color " + s)
	}
	return ui.RGBA(r, g, b, a)
}

// usePalette 把各颜色变量换成深色或浅色那一套。theme 在每个窗口每帧开头
// 调用；所有窗口跟着同一个系统外观，所以全局换一次就够。
func usePalette(dark bool) {
	if dark != darkUI {
		setPalette(dark)
	}
}

func setPalette(dark bool) {
	darkUI = dark
	vals := toneValues[0]
	if dark {
		vals = toneValues[1]
	}
	for i, t := range tones {
		*t.c = vals[i]
	}
}

// ink 是叠在底色上的一层：悬停、按下、细线。浅色界面叠黑，深色叠白。
func ink(alpha float32) ui.Color {
	if darkUI {
		return ui.RGBA(255, 255, 255, alpha)
	}
	return ui.RGBA(0, 0, 0, alpha)
}

// shadow 是投影的颜色。深色底上同样的投影几乎看不见，加深一倍。
func shadow(alpha float32) ui.Color {
	if darkUI {
		return ui.RGBA(0, 0, 0, min(alpha*2, 0.6))
	}
	return ui.RGBA(0, 0, 0, alpha)
}

// rim 是毛玻璃面板顶边那一道内高光。深色面板上只留一丝。
func rim(alpha float32) ui.Color {
	if darkUI {
		return ui.RGBA(255, 255, 255, alpha*0.12)
	}
	return ui.RGBA(255, 255, 255, alpha)
}
