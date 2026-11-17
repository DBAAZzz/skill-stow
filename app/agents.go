package main

import (
	"embed"
	"fmt"
	"slices"
	"strings"

	"github.com/egoist/mygo/ui"

	"skillmanager/internal/library"
	"skillmanager/locales"
)

// brand 是 Agent 图标：lobe-icons 的图形，按其 Avatar 的参数画成方块。
// 来源和参数见 assets/agents/SOURCE.md。lobe-icons 没有的画成字母方块。
type brand struct {
	icon     *ui.SVG
	color    bool        // icon 是彩色的，按原色画（Image），不用 fg 着色
	bg       ui.Color    // 纯色底
	gradient [2]ui.Color // 径向渐变底（中心色、外圈色），零值时用 bg
	fg       ui.Color
	scale    float32 // 图标占方块的比例
	letter   string  // 没有图标时画的字母
}

//go:embed assets/agents
var agentAssets embed.FS

func agentSVG(name string) *ui.SVG {
	b, err := agentAssets.ReadFile("assets/agents/" + name + ".svg")
	if err != nil {
		panic(err)
	}
	return ui.MustParseSVG(b)
}

var (
	mono = func(name, bg, fg string, scale float32) brand {
		return brand{icon: agentSVG(name), bg: ui.Hex(bg), fg: ui.Hex(fg), scale: scale}
	}
	colored = func(name string, scale float32) brand {
		return brand{icon: agentSVG(name), color: true, bg: white, scale: scale}
	}
	letter = func(l, bg string) brand { return brand{letter: l, bg: ui.Hex(bg), fg: white} }
)

// agentColor 是图表里各 Agent 的颜色，取自各自图标。
var agentColor = map[string]ui.Color{
	"claude-code": ui.Hex("#d97757"),
	"codex":       ui.Hex("#7b8cff"),
}

var brands = map[string]brand{
	"claude-code":    mono("claude", "#D97757", "#fff", 0.7),
	"codex":          colored("codex-color", 1),
	"cursor":         {icon: agentSVG("cursor"), bg: black, fg: white, scale: 0.6},
	"gemini":         colored("gemini-color", 0.8),
	"github-copilot": mono("githubcopilot", "#000", "#fff", 0.75),
	"opencode":       mono("opencode", "#000", "#fff", 0.75),
	"windsurf":       mono("windsurf", "#fff", "#000", 0.75),
	"antigravity":    colored("antigravity-color", 0.7),
	"cline":          mono("cline", "#323B43", "#fff", 0.6),
	"amp":            mono("amp", "#000", "#F34E3F", 0.6),
	"kiro":           mono("kiro", "#9046ff", "#fff", 1),
	"trae":           mono("trae", "#000", "#32F08C", 0.65),
	"trae-cn":        mono("trae", "#000", "#32F08C", 0.65),
	"qwen":           mono("qwen", "#082dff", "#fff", 0.6),
	"qoder":          mono("qoder", "#000", "#fff", 0.7),
	"qoder-cn":       mono("qoder", "#000", "#fff", 0.7),
	"codebuddy":      {icon: agentSVG("codebuddy"), gradient: [2]ui.Color{ui.Hex("#2EA99D"), ui.Hex("#6C4DFF")}, fg: white, scale: 1},
	"workbuddy":      {icon: agentSVG("workbuddy"), gradient: [2]ui.Color{ui.Hex("#94d378"), ui.Hex("#01C886")}, fg: white, scale: 1},
	"zcode":          mono("zcode", "#0b0c0f", "#fff", 1),
	"vscode":         colored("vscode-color", 0.7), // 设置 › 项目里的 VS Code 系编辑器
	"droid":          letter("D", "#ee6018"),
	"roo":            mono("roocode", "#000", "#fff", 0.75),
	"kilo":           mono("kilocode", "#F8F676", "#1A1A18", 0.7),
	"goose":          mono("goose", "#fff", "#000", 0.65),
	"pi":             mono("pi", "#000", "#fff", 0.65),
	"kimi":           mono("kimi", "#000", "#fff", 0.6),
	"iflow":          letter("i", "#5b5bd6"),
	"crush":          letter("C", "#6b50ff"),
	"augment":        letter("A", "#1d1d1f"),
	"continue":       letter("C", "#1d1d1f"),
	"openhands":      mono("openhands", "#fff", "#000", 0.75),
	"junie":          mono("junie", "#000", "#47E054", 0.6),
	"lingma":         letter("灵", "#615ced"), //i18n:ignore 通义灵码图标上的字，和标志一样不翻
}

// sharedBrand 是项目里的通用目录 .agents/skills：好几个 Agent 都读，画成灰色的 A。
var sharedBrand = letter("A", "#aeaeb2")

// agentBadge 是 Agent 的方块图标，鼠标停上去显示名称。
func agentBadge(c *ui.Context, id string, size float32) {
	br, ok := brands[id]
	if !ok {
		// 手动添加的技能目录等：名称的第一个字
		name := []rune(library.AgentName(id))
		br = letter(strings.ToUpper(string(name[:1])), "#8e8e93")
	}
	badge(c, br, library.AgentName(id), size)
}

// dirBadge 是项目里一个技能目录的图标：它的主人，通用目录是灰色的 A。
func dirBadge(c *ui.Context, rel string, size float32) {
	if id := library.ProjectDirAgent(rel); id != "" {
		agentBadge(c, id, size)
		return
	}
	badge(c, sharedBrand, library.ProjectDirName(rel), size)
}

// blendsIn 报告图标的底色是不是和卡片几乎一样：浅色界面里的白底，深色界面
// 里的黑底、深灰底（Cursor、OpenCode 这些）。
func blendsIn(br brand) bool {
	if br.gradient[0] != (ui.Color{}) {
		return false
	}
	if !darkUI {
		return br.bg == white
	}
	luma := 0.299*float32(br.bg.R) + 0.587*float32(br.bg.G) + 0.114*float32(br.bg.B)
	return luma < 60
}

func badge(c *ui.Context, br brand, name string, size float32) {
	b := ui.Box(c).Size(size, size).Shrink(0).Radius(size * 0.25).Clip().Center().
		TextColor(br.fg).Tooltip(name).Label(name)
	switch {
	case br.gradient[0] != (ui.Color{}):
		// CSS radial-gradient(66.67% 66.67% at 87.5% 102.08%, …)
		b.RadialGradient(ui.RadialGradient{
			Center: ui.Point{X: 0.875, Y: 1.0208},
			Radius: size * 0.6667,
			Stops:  []ui.GradientStop{{Color: br.gradient[0], At: 0}, {Color: br.gradient[1], At: 1}},
		})
	default:
		b.Background(br.bg)
	}
	if blendsIn(br) {
		// 和卡片一样明暗的图标要一圈细边才看得出方块
		hairline(b, 0.16)
	}
	b.Children(func() {
		switch {
		case br.icon != nil && br.color:
			ui.Image(c, br.icon).Size(size*br.scale, size*br.scale)
		case br.icon != nil:
			ui.Icon(c, br.icon).Size(size*br.scale, size*br.scale)
		default:
			ui.Text(c, br.letter).FontSize(size * 0.58).FontWeight(700).SingleLine()
		}
	})
}

// sharedTag 是灰色的「通用」标（u3-a .pill）：技能在通用目录里，readers
// 都直接读到，鼠标停上去列出它们。size 是旁边 Agent 图标的大小。
func sharedTag(c *ui.Context, readers []string, size float32) ui.Element {
	h, icon := float32(15), float32(11)
	if size >= 16 {
		h, icon = 17, 12
	}
	var names []string
	for _, id := range readers {
		names = append(names, library.AgentName(id))
	}
	tip := locales.AgentsReadCommonDir(library.JoinAnd(names))
	tag := ui.Row(c).Height(h).Padding(0, 5, 0, 2).Gap(3).Radius(4).AlignItems(ui.Center).Shrink(0).
		Background(colBgFill).TextColor(colTextBody).Tooltip(tip)
	tag.Children(func() {
		badge(c, sharedBrand, locales.CommonShared(), icon)
		ui.Text(c, locales.CommonShared()).FontSize(11).FontWeight(500).SingleLine()
	})
	return tag
}

// readers 是界面里显示的、直接读通用目录的 Agent（设置里关掉的不算）。
func (a *app) readers() []string { return a.shown(a.lib.SharedReaders()) }

// shown 去掉设置里关掉的 Agent。
func (a *app) shown(ids []string) []string {
	return slices.DeleteFunc(slices.Clone(ids), a.lib.Hidden)
}

// enabledBadges 是「我的」技能启用于哪些 Agent：在通用目录里的，直接读它的
// Agent 收成一个「通用」标，后面只列单独启用的。except 是不用再列的
// Agent（看板里本列的）。什么都没画时返回 false。
func (a *app) enabledBadges(c *ui.Context, s *library.Skill, except string, size float32, limit int) bool {
	ids := a.shown(s.DiscoveredAgents())
	ids = slices.DeleteFunc(slices.Clone(ids), func(id string) bool { return id == except })
	n := len(ids)
	if n > limit {
		n = limit - 1 // 放不下时最后一格留给「+N」
	}
	for _, id := range ids[:n] {
		stateBadge(c.Key(id), s, id, size)
	}
	if n < len(ids) {
		ui.Text(c, fmt.Sprintf("+%d", len(ids)-n)).FontSize(10).TextColor(colTextMuted).Shrink(0).Tooltip(agentNames(ids[n:]))
	}
	return len(ids) > 0
}

// projectReaders 是在项目里会读到项目技能的 Agent：经项目里的通用目录
// 读到的收成「通用」标，其余逐个列出。
func projectReaders(c *ui.Context, s *library.Skill, size float32, limit int) {
	var shared, others []string
	for _, r := range s.Reads {
		if !r.Global && r.Dir == library.SharedDir && !slices.Contains(shared, r.Agent) {
			shared = append(shared, r.Agent)
		}
	}
	for _, id := range s.Readers(false) {
		if !slices.Contains(shared, id) {
			others = append(others, id)
		}
	}
	if len(shared) > 1 {
		sharedTag(c, shared, size)
		limit--
	} else {
		others = s.Readers(false)
	}
	if len(others) > 0 {
		agentBadges(c, others, size, max(limit, 2))
	}
}

// agentBadges 是一排 Agent 图标。放不下 limit 个时，多出来的收成「+N」。
func agentBadges(c *ui.Context, ids []string, size float32, limit ...int) {
	n := len(ids)
	if len(limit) > 0 && n > limit[0] {
		n = limit[0] - 1
	}
	ui.Row(c).Gap(3).AlignItems(ui.Center).Children(func() {
		for _, id := range ids[:n] {
			agentBadge(c.Key(id), id, size)
		}
		if n < len(ids) {
			ui.Text(c, fmt.Sprintf("+%d", len(ids)-n)).FontSize(wholePt(size*0.72)).TextColor(colTextMuted).FontFeatures("tnum").
				Margin(0, 0, 0, 2).Tooltip(agentNames(ids[n:]))
		}
	})
}

// agentChip 是白色的药丸：图标 + 名称（p3-a 弹窗里的 .pill）。named 为
// false 时只有图标，名称在鼠标停上去时显示。
func agentChip(c *ui.Context, id string, named bool) {
	chip := ui.Row(c).Height(24).Gap(6).Radius(12).AlignItems(ui.Center).Background(colBgCard)
	hairline(chip, 0.1)
	if named {
		chip.Padding(0, 11, 0, 6)
	} else {
		chip.Width(24).Justify(ui.Center)
	}
	chip.Children(func() {
		agentBadge(c, id, 14)
		if named {
			ui.Text(c, library.AgentName(id)).FontSize(12).TextColor(colTextBody).SingleLine()
		}
	})
}

// agentChips 是一排 agentChip：两个以内带名称，多了只画图标，再多收成「+N」。
func agentChips(c *ui.Context, ids []string) {
	const most = 6
	named := len(ids) <= 2
	gap := float32(10)
	if !named {
		gap = 4
	}
	ui.Row(c).Gap(gap).Shrink(0).AlignItems(ui.Center).Children(func() {
		for _, id := range ids[:min(len(ids), most)] {
			agentChip(c.Key(id), id, named)
		}
		if n := len(ids) - most; n > 0 {
			ui.Text(c, fmt.Sprintf("+%d", n)).FontSize(12).TextColor(colTextMuted).Margin(0, 0, 0, 2)
		}
	})
}

// agentNames 是「+N」悬停时列出的 Agent 名称。
func agentNames(ids []string) string {
	var names []string
	for _, id := range ids {
		names = append(names, library.AgentName(id))
	}
	return library.JoinAnd(names)
}
