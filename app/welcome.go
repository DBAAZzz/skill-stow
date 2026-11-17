package main

import (
	_ "embed"
	"math"
	"slices"
	"strconv"
	"time"

	"github.com/egoist/mygo/ui"

	"skillmanager/internal/library"
	"skillmanager/locales"
)

// 首次启动的开场（画板 welcome-onboarding 第九轮）：盖在主窗口上的一层，
// 演一遍「从散乱到收好」。演员是一套固定的演示卡片，不是本机数据：谁打开
// 都一样好看，重复、断链这些情况也都演得到。
//
//	第一幕  图标浮起、高光扫过、一句欢迎
//	第二幕  图标缩小落到标题上方；卡片按 Agent 一堆堆落下，远处的模糊；
//	        重复的拖着虚线飞到一起，留下的那张放大成主角，其余几份叠在
//	        它背后；一张断了链接
//	第三幕  点「收好它们」：卡片按顺序飞向主界面里真实卡片的位置，背景
//	        淡去、真实页面淡入，演示卡片随后散去。真实卡片比演示的少时，
//	        多出来的接着往后排，落下后同样散去。

// welcomeVersion 是开场的版本。设置里记下的版本比它小，启动时就播；
// 以后要给老用户看新内容时加一。
const welcomeVersion = 1

//go:embed assets/welcome/icon.png
var welcomeIconPNG []byte

var welcomeIcon = func() ui.ImageSource {
	img, err := ui.DecodeImage(welcomeIconPNG)
	if err != nil {
		panic(err)
	}
	return img
}()

// welcomeState 是开场的状态。
type welcomeState struct {
	open   bool
	start  time.Time // 第一帧的时刻；零值是还没画过
	stowAt time.Time // 点「收好它们」的时刻；零值是还没点
	// slots 是这一帧主界面里卡片的位置（窗口坐标，按阅读顺序），skillCard
	// 和 setCard 画的时候记下，第三幕往这里飞
	slots []ui.Rect
	// brand 是标题上方小图标的位置（窗口坐标），第一幕的大图标缩小落到这里
	brand ui.Rect
}

// needsWelcome 报告启动时该不该播开场：读的是本机数据、还没看过这一版。
// 示例数据不写设置，每次都播会烦，SKILLS_WELCOME=1 时才播。
func (a *app) needsWelcome(force bool) bool {
	if force {
		return true
	}
	return a.lib.Home != "" && a.lib.Settings.WelcomeVersion < welcomeVersion
}

// finishWelcome 收起开场并记下看过了。
func (a *app) finishWelcome() {
	a.wel = welcomeState{}
	a.lib.Settings.WelcomeVersion = welcomeVersion
	a.lib.SaveSettings() // 写不下只是下次再看一遍，不打扰
}

// noteSlot 在开场开着时记下一张主界面卡片的位置。
func (a *app) noteSlot(card ui.Element) {
	if !a.wel.open {
		return
	}
	if r := card.Bounds(); r.W > 0 {
		a.wel.slots = append(a.wel.slots, r)
	}
}

// welcomePagePose 是开场开着时主界面右边这一页的样子：收纳前藏着，卡片
// 快落定时淡入。
func (a *app) welcomePagePose() (ui.Pose, ui.Animation) {
	if a.wel.stowAt.IsZero() {
		return ui.Pose{Fade: 1}, ui.Animation{Duration: time.Millisecond}
	}
	return ui.Pose{}, welEase(360, welPageIn)
}

// ——— 时间轴（毫秒） ———

// 从开场算起。
const (
	welHeroIn  = 80
	welHeroOut = 1750
	welSkipIn  = 700 // 右下角「esc 跳过」
	welDeal    = 1950
	welSettled = 4000 // 发牌的弹簧都停了；之后堆里的卡片挪位置不再带发牌的延迟
	welBroken  = 6350
	welShake   = 560 // 断链那张抖多久
	welReady   = 6950
	welCollide = 1100 // 重复的飞到一起后多久合成一张
	welTrail   = 760  // 虚线从堆里画到合成那张要多久
)

// 从点「收好它们」算起。
const (
	welStageOut = 120
	welPageIn   = 1000
	welCardsOut = 1360
	welDone     = 1800
)

// 设计稿是 1280×800 的窗口；窗口大小不同时整体等比缩放、居中。
const welW, welH = 1280, 800

// 演示卡片的大小（设计稿里，缩放前）。
const welCardW, welCardH = 176, 86

// 第一幕的大图标多大，落到标题上方时多大。
const welIconSize, welBrandSize float32 = 104, 26

// welPile 是一个 Agent 的一堆卡片：最上面那张的中心、歪多少、远近（z 越大
// 越近、越大）、模糊多少（远的模糊）、名牌上的技能数、堆里的几张（第 0
// 张在最上面）。
type welPile struct {
	agent   string
	x, y, z float32
	tilt    float32
	blur    float32
	count   int
	cards   []string
}

var welPiles = []welPile{
	{"cursor", 252, 244, 1, -2.4, 0, 21, []string{"test-first", "vue-best-practices", "interface-craft", "lark-calendar", "frontend-design"}},
	{"codex", 1032, 232, 0.92, 2.4, 0.4, 20, []string{"code-review", "context-compression", "test-first", "i18n-copy", "code-comments", "code-ablation"}},
	{"claude-code", 326, 452, 1, 1.8, 0, 17, []string{"brainstorming", "kami", "test-first", "systematic-debugging", "doc-coauthoring"}},
	{"codebuddy", 978, 458, 0.9, -1.8, 0.5, 10, []string{"test-first", "api-design", "brainstorming", "tailwind-rules", "sql-review"}},
	{"workbuddy", 632, 480, 0.96, -4, 0, 6, []string{"git-flow", "brainstorming", "design-lab", "miniprogram-guide", "wechat-pay"}},
}

// welEmpty 是没有技能的 Agent：只留一枚淡名牌（名牌的左上角）。
var welEmpty = []struct {
	agent string
	x, y  float32
}{{"gemini", 392, 92}, {"opencode", 1064, 566}}

// welDust 是最远的一层：几张很模糊、很淡的卡片铺在四角，只为景深。
var welDust = []struct {
	name    string
	x, y, r float32
}{
	{"frontend-design", 116, 402, -6}, {"sql-review", 1176, 336, 5}, {"i18n-copy", 770, 104, -3},
	{"wechat-pay", 140, 650, 4}, {"design-lab", 1146, 640, -5},
}

// welMerge 是一张重复的技能：各堆里的几份飞到哪、合成后多大、歪多少、
// 什么时候飞，合掉的几份叠在它背后的位置（相对它的中心和转角）。
type welMerge struct {
	name   string
	x, y   float32
	s, r   float32
	at     int
	ghosts [][3]float32
}

// 第 0 张是主角：放大、居中，带一圈淡蓝。
var welMerges = []welMerge{
	{"test-first", 560, 252, 1.2, -1, 4700, [][3]float32{{-16, 10, -7}, {18, 8, 6}, {4, -10, 3}}},
	{"brainstorming", 806, 334, 1, 1.6, 5250, [][3]float32{{-10, 8, -6}, {12, 6, 5}}},
}

// welBrokenName 是演示断掉链接的那张。
const welBrokenName = "git-flow"

// welDesc 是演示卡片的第二行：有简介的写简介，来自集合的写来源。
func welDesc(name string) string {
	switch name {
	case "test-first":
		return locales.WelcomeCastTestFirst()
	case "vue-best-practices":
		return locales.WelcomeCastVueBestPractices()
	case "code-review":
		return locales.WelcomeCastCodeReview()
	case "kami":
		return locales.WelcomeCastKami()
	case "context-compression":
		return locales.WelcomeCastContextCompression()
	case "brainstorming":
		return locales.WelcomeCastBrainstorming()
	case "api-design":
		return locales.WelcomeCastApiDesign()
	case "design-lab":
		return locales.WelcomeCastDesignLab()
	case "systematic-debugging":
		return locales.WelcomeCastSystematicDebugging()
	case "interface-craft":
		return locales.WelcomeCastInterfaceCraft()
	case "git-flow":
		return locales.WelcomeCastGitFlow()
	case "tailwind-rules":
		return locales.WelcomeCastTailwindRules()
	case "miniprogram-guide":
		return locales.WelcomeCastMiniprogramGuide()
	case "wechat-pay":
		return locales.WelcomeCastWechatPay()
	case "i18n-copy":
		return locales.WelcomeCastI18nCopy()
	case "sql-review":
		return locales.WelcomeCastSqlReview()
	case "lark-calendar":
		return "open.feishu.cn"
	case "frontend-design", "doc-coauthoring":
		return "anthropics/skills"
	}
	return "DBAAZzz/skills"
}

// welCard 是一张演示卡片。
type welCard struct {
	name  string
	pile  int
	i     int // 在堆里第几张，0 在最上面
	merge int // 属于哪一组重复（welMerges 的下标），-1 是不重复
	copy  int // 是这组重复里的第几份，第 0 份留下
	order int // 第三幕往第几个位置飞，-1 是合并后叠在留下那张背后的
}

// welPose 是设计稿里的一个样子：中心点、转角、缩放、淡去多少。
type welPose struct{ x, y, r, s, fade float32 }

// welCast 是全部演示卡片，按堆排。
var welCast = func() []welCard {
	var cards []welCard
	copies := map[string]int{}
	for p, pile := range welPiles {
		for i, name := range pile.cards {
			c := welCard{name: name, pile: p, i: i, merge: -1, order: -1}
			for k, m := range welMerges {
				if m.name == name {
					c.merge, c.copy = k, copies[name]
					copies[name]++
				}
			}
			cards = append(cards, c)
		}
	}
	// 第三幕的顺序：按第二幕结束时的位置从上到下、从左到右，飞的路线少交叉
	var keep []int
	for n, c := range cards {
		if c.merge < 0 || c.copy == 0 {
			keep = append(keep, n)
		}
	}
	at := func(c welCard) (float32, float32) {
		if c.merge >= 0 {
			return welMerges[c.merge].x, welMerges[c.merge].y
		}
		p := welPiles[c.pile]
		return p.x + float32(c.i)*5*p.z, p.y - float32(c.i)*6*p.z
	}
	slices.SortStableFunc(keep, func(i, j int) int {
		xi, yi := at(cards[i])
		xj, yj := at(cards[j])
		if bi, bj := int(yi/160), int(yj/160); bi != bj {
			return bi - bj
		}
		return int(xi - xj)
	})
	for k, n := range keep {
		cards[n].order = k
	}
	return cards
}()

// welCopies 是第 k 组重复的全部几份（welCast 的下标），留下的在前。
func welCopies(k int) []int {
	var out []int
	for n, c := range welCast {
		if c.merge == k {
			out = append(out, n)
		}
	}
	return out
}

// welInPile 报告第 n 张卡片此刻还在不在堆里：重复的那几份到点就飞走了。
func welInPile(n, t int) bool {
	wc := welCast[n]
	return wc.merge < 0 || t < welMerges[wc.merge].at
}

// welRank 是第 n 张卡片此刻在堆里排第几：上面的飞走了，它就往上挪。
func welRank(n, t int) int {
	wc := welCast[n]
	k := 0
	for o, oc := range welCast {
		if oc.pile == wc.pile && oc.i < wc.i && welInPile(o, t) {
			k++
		}
	}
	return k
}

// welRest 是第 n 张卡片此刻在堆里的样子：最上面那张端正一点，下面两张
// 往右上错开、一左一右地歪，只露出边；再往下的藏在第三张底下。
func welRest(n, t int) welPose {
	p := welPiles[welCast[n].pile]
	k := min(welRank(n, t), 2)
	r := p.tilt
	switch k {
	case 1:
		r += 3.2
	case 2:
		r -= 2.6
	}
	return welPose{x: p.x + float32(k)*5*p.z, y: p.y - float32(k)*6*p.z, r: r, s: p.z}
}

// ——— 动画 ———

var welOut = ui.CubicBezier(0.22, 1, 0.36, 1)

func msd(n int) time.Duration { return time.Duration(n) * time.Millisecond }

func welEase(d, delay int) ui.Animation {
	return ui.Animation{Duration: msd(d), Ease: welOut, Delay: msd(delay)}
}

func welSpring(d int, bounce float64, delay int) ui.Animation {
	return ui.Animation{Spring: ui.SpringOptions{Duration: msd(d), Bounce: bounce}, Delay: msd(delay)}
}

// welGeom 把设计稿坐标换成窗口坐标。
type welGeom struct{ s, ox, oy float32 }

func newWelGeom(w, h float32) welGeom {
	s := min(w/welW, h/welH)
	return welGeom{s, (w - welW*s) / 2, (h - welH*s) / 2}
}

func (g welGeom) x(v float32) float32 { return g.ox + v*g.s }
func (g welGeom) y(v float32) float32 { return g.oy + v*g.s }

// card 是演示卡片在设计稿里的样子换成的 Pose：卡片排在窗口左上角，
// 按中心点挪过去。
func (g welGeom) card(p welPose) ui.Pose {
	return ui.Pose{X: g.x(p.x) - welCardW/2, Y: g.y(p.y) - welCardH/2, Rotate: p.r, Scale: p.s * g.s, Fade: p.fade}
}

// welNext 是离下一件事还有多久（毫秒），没有了是 -1。t 从开场算起，
// st 从点按钮算起，还没点是 -1。
func welNext(t, st int) int {
	if st >= 0 {
		for _, at := range []int{welCardsOut, welDone} {
			if st < at {
				return at - st
			}
		}
		return -1
	}
	marks := []int{welHeroIn, welSkipIn, welHeroOut, welDeal, welBroken, welReady}
	for _, m := range welMerges {
		marks = append(marks, m.at, m.at+welCollide)
	}
	next := -1
	for _, at := range marks {
		if at > t && (next < 0 || at-t < next) {
			next = at - t
		}
	}
	return next
}

// ——— 画 ———

// welcomeLayer 画开场。主界面在它下面照常画（卡片的位置要从那里量）。
func (a *app) welcomeLayer(c *ui.Context) {
	if !a.wel.open {
		return
	}
	now := c.Now()
	if a.wel.start.IsZero() {
		a.wel.start = now
	}
	t, st := int(now.Sub(a.wel.start)/time.Millisecond), -1
	if !a.wel.stowAt.IsZero() {
		st = int(now.Sub(a.wel.stowAt) / time.Millisecond)
	}
	if st >= welDone {
		a.finishWelcome()
		return
	}
	if d := welNext(t, st); d > 0 {
		c.After(msd(d))
	}
	if st < 0 && t >= welBroken && t < welBroken+welShake {
		c.Invalidate() // 断链那张在抖：逐帧算
	}
	w, h := c.Size()
	g := newWelGeom(w, h)
	ready := st < 0 && t >= welReady

	ui.Overlay(c, func() {
		root := ui.Box(c.Key("welcome")).Absolute().Left(0).Top(0).Right(0).Bottom(0).Modal().
			Label(locales.WelcomeTitle(appDisplayName())).Transition(welcomeExit)
		root.Children(func() {
			welStage(c, st)
			welDustView(c, g, t, st)
			a.welTrails(c, g)
			for n := range welCast {
				a.welCardView(c, g, n, t, st, w, h)
			}
			welChips(c, g, t, st)
			welHeroTitle(c, g, t)
			a.welCopy(c, g, t, st, ready)
			a.welIcon(c, g, t, st, w)
			welSkipHint(c, t, st)
		})
		switch {
		case root.OverlayShortcut(0, ui.KeyEscape):
			a.finishWelcome()
		case root.OverlayShortcut(0, ui.KeyEnter) && ready:
			a.stowWelcome(c)
		}
	})
}

// welcomeExit 是按 Esc 跳过时整层淡去；播完时它已经透明了。
var welcomeExit = ui.ElementTransition{Exit: &ui.Motion{Opacity: 0}, ExitDuration: 320 * time.Millisecond}

func (a *app) stowWelcome(c *ui.Context) {
	a.wel.stowAt = c.Now()
	c.Invalidate()
}

// welShow 是一样东西该不该露出来：露出来时从下面 dy 处浮上来，delay、d
// 毫秒；收起时立刻淡去。
func welShow(on bool, dy float32, delay, d int) (ui.Pose, ui.Animation) {
	if on {
		return ui.Pose{}, welEase(d, delay)
	}
	return ui.Pose{Y: dy, Fade: 1}, welEase(260, 0)
}

// welPop 是一个小东西弹出来：从 0.5 倍大、透明弹到原样。
func welPop(on bool, delay int) (ui.Pose, ui.Animation) {
	if on {
		return ui.Pose{}, welSpring(460, 0.4, delay)
	}
	return ui.Pose{Scale: 0.5, Fade: 1}, welEase(200, 0)
}

// welStage 是浅色、有景深的底：上浅下深一点，三团蓝、紫、杏色光，中间一团
// 白光托住主角和标题，再铺一层中间浓、四周淡的点阵，四边压暗一点，像一张
// 桌面。收纳时淡去，露出主界面。
func welStage(c *ui.Context, st int) {
	pose, anim := ui.Pose{}, ui.Animation{Duration: time.Millisecond}
	if st >= 0 {
		pose, anim = ui.Pose{Fade: 1}, ui.Animation{Duration: 820 * time.Millisecond, Ease: ui.EaseInOut, Delay: welStageOut * time.Millisecond}
	}
	stage := ui.Box(c).Absolute().Left(0).Top(0).Right(0).Bottom(0).Clip().
		LinearGradient(ui.LinearGradient{From: colBgPage, To: colBgWindow, Angle: 180}).
		InsetShadow(0, 0, 180, 0, shadow(0.08)).AnimatePose(pose, anim)
	stage.Children(func() {
		glow := func(x, y, r float32, col ui.Color, alpha float32) {
			ui.Box(c).Absolute().LeftPercent(x).TopPercent(y).Size(2*r, 2*r).Radius(r).TranslatePercent(-50, -50).
				RadialGradient(ui.RadialGradient{Radius: r, Stops: []ui.GradientStop{
					{Color: col.Alpha(alpha), At: 0}, {Color: col.Alpha(alpha * 0.45), At: 0.45}, {Color: col.Alpha(0), At: 1},
				}})
		}
		glow(27, 10, 440, colAccent, 0.22)
		glow(85, 105, 500, colGroupWebsite, 0.18)
		glow(72, 6, 320, colWarn, 0.14)
		glow(50, 50, 520, colBgCard, 0.7)
		ui.Box(c).Absolute().Left(0).Top(0).Right(0).Bottom(0).Cache().Draw(welDots)
	})
}

// welDots 画点阵：22 一格，中间偏上最浓，往外淡到没有。
func welDots(p *ui.Painter, r ui.Rect) {
	const gap = 22
	cx, cy := r.X+r.W/2, r.Y+r.H*0.46
	rx, ry := r.W*0.62, r.H*0.58
	for y := r.Y + gap/2; y < r.Y+r.H; y += gap {
		for x := r.X + gap/2; x < r.X+r.W; x += gap {
			dx, dy := (x-cx)/rx, (y-cy)/ry
			k := 1 - (float32(math.Sqrt(float64(dx*dx+dy*dy)))-0.3)/0.7
			if k <= 0 {
				continue
			}
			p.Fill(ui.Rect{X: x - 1, Y: y - 1, W: 2, H: 2}, colTextSecondary.Alpha(0.2*min(k, 1)), 1)
		}
	}
}

// welHeroTitle 是第一幕图标下面的那句欢迎。
func welHeroTitle(c *ui.Context, g welGeom, t int) {
	pose, anim := ui.Pose{Y: 14, Fade: 1}, ui.Animation{Duration: time.Millisecond}
	switch {
	case t >= welHeroOut:
		pose, anim = ui.Pose{Y: -18, Scale: 0.96, Fade: 1}, welEase(520, 0)
	case t >= welHeroIn:
		pose, anim = ui.Pose{}, welEase(700, 0)
	}
	ui.Text(c, locales.WelcomeTitle(appDisplayName())).Absolute().Left(0).Right(0).Top(g.y(268)+welIconSize+22).
		FontSize(34).FontWeight(700).TextColor(colTextPrimary).TextAlign(ui.Center).AnimatePose(pose, anim)
}

// welIcon 是贯穿前两幕的图标：第一幕浮起、高光扫过；第二幕缩小，落到标题
// 上方的小图标位置，成了品牌的那一枚。
func (a *app) welIcon(c *ui.Context, g welGeom, t, st int, w float32) {
	cx, top := w/2, g.y(268)
	cy := top + welIconSize/2
	// 落到哪：标题上方留的那一格；还没量出来时按设计稿估
	to := ui.Point{X: cx, Y: g.y(572) + welBrandSize/2}
	if b := a.wel.brand; b.W > 0 {
		to = ui.Point{X: b.X + b.W/2, Y: b.Y + b.H/2}
	}
	landed := ui.Pose{X: to.X - cx, Y: to.Y - cy, Scale: welBrandSize / welIconSize}
	pose, anim := ui.Pose{Scale: 0.86, Fade: 1}, ui.Animation{Duration: time.Millisecond}
	sheenX, sheenAnim := float32(-70), ui.Animation{Duration: time.Millisecond}
	switch {
	case st >= 0:
		pose, anim = landed, welEase(300, 0)
		pose.Fade = 1
		sheenX = 120
	case t >= welHeroOut:
		pose, anim = landed, welSpring(1150, 0.12, 0)
		sheenX = 120
	case t >= welHeroIn:
		pose, anim = ui.Pose{}, welSpring(900, 0.3, 0)
		sheenX, sheenAnim = 120, ui.Animation{Duration: 1300 * time.Millisecond, Ease: ui.EaseInOut, Delay: 370 * time.Millisecond}
	}
	ui.Box(c.Key("icon")).Absolute().Left(cx-welIconSize/2).Top(top).Size(welIconSize, welIconSize).ZIndex(3600).
		AnimatePose(pose, anim).Children(func() {
		// 投影跟着图标的方块走，不跟着整张图（四周有透明的留白）
		// 图标是白底方块（约占 80%，居中），垫底比方块略小一圈，藏在图标后面只出投影，不从边上露出来
		ui.Box(c).Absolute().Left(12).Top(12).Size(80, 80).Radius(18).Background(colBgCard).
			Shadow(0, 14, 26, 0, shadow(0.22)).ZIndex(-1)
		ui.Image(c, welcomeIcon).Size(welIconSize, welIconSize)
		// 高光只落在图标的方块上，裁切框同样比方块略小，免得扫到方块外的透明留白
		ui.Box(c).Absolute().Left(11).Top(11).Size(82, 82).Radius(20).Clip().Children(func() {
			ui.Box(c).Absolute().Left(0).Top(-20).Size(40, 125).Rotate(14).
				LinearGradient(ui.LinearGradient{Angle: 90, Stops: []ui.GradientStop{
					{Color: white.Alpha(0), At: 0}, {Color: white.Alpha(0.7), At: 0.5}, {Color: white.Alpha(0), At: 1},
				}}).AnimatePose(ui.Pose{X: sheenX}, sheenAnim)
		})
	})
}

// welCopy 是第二幕底下的一组：小图标（第一幕的大图标落下来）和产品名、
// 标题、一句说明、按钮，紧挨卡片下沿。
func (a *app) welCopy(c *ui.Context, g welGeom, t, st int, ready bool) {
	on := st < 0 && t >= welDeal
	ui.Column(c).Absolute().Left(0).Right(0).Top(g.y(572)).AlignItems(ui.Center).ZIndex(3500).Children(func() {
		ui.Row(c).Height(welBrandSize).Gap(7).AlignItems(ui.Center).Children(func() {
			// 留给落下来的图标的一格；它本身不画，也不跟着别的东西动
			slot := ui.Box(c.Key("brand")).Size(welBrandSize, welBrandSize)
			a.wel.brand = slot.Bounds()
			ui.Text(c, appDisplayName()).FontSize(13).FontWeight(600).TextColor(colTextSecondary).SingleLine().
				AnimatePose(welShow(on, 0, 900, 600))
		})
		ui.Text(c, locales.WelcomeScattered()).FontSize(30).FontWeight(700).TextColor(colTextPrimary).SingleLine().
			Margin(12, 0, 0, 0).AnimatePose(welShow(on, 10, 450, 600))
		ui.Text(c, locales.WelcomeScatteredHint()).FontSize(14).TextColor(colTextSecondary).SingleLine().
			Margin(8, 0, 0, 0).AnimatePose(welShow(on, 8, 650, 600))
		ui.Row(c).Margin(22, 0, 0, 0).AnimatePose(welShow(ready, 8, 0, 500)).Children(func() {
			if welStowButton(c).Clicked() && ready {
				a.stowWelcome(c)
			}
		})
	})
}

// welStowButton 是「收好它们」：40 高的蓝色主按钮，右边一枚回车键帽。
// 照 filledButton 的做法做出能按下去的样子，投影带一点蓝。
func welStowButton(c *ui.Context) ui.Element {
	label := locales.WelcomeStow()
	b := ui.ButtonBase(c).Height(40).MinWidth(176).Padding(0, 18, 0, 24).Radius(10).Center().TextColor(white).Label(label)
	base := tint(b, "bg", colAccent, darker(colAccent, 0.06), darker(colAccent, 0.18))
	b.LinearGradient(ui.LinearGradient{From: base.Mix(white, 0.14), To: base, Angle: 180}).
		InsetShadow(0, 0.5, 0, 0, white.Alpha(0.35))
	hairline(b, 0.18)
	b.Shadow(0, 1, 2, 0, shadow(0.12)).Shadow(0, 10, 24, 0, colAccent.Alpha(0.28))
	b.Children(func() {
		ui.Row(c).Gap(10).AlignItems(ui.Center).Children(func() {
			ui.Text(c, label).FontSize(15).FontWeight(600).SingleLine()
			ui.Box(c).Size(20, 20).Radius(5).Center().Background(white.Alpha(0.2)).Children(func() {
				ui.Text(c, "↩").FontSize(12).TextColor(white.Alpha(0.9)).SingleLine()
			})
		})
	})
	return b
}

// welSkipHint 是右下角的「esc 跳过」。
func welSkipHint(c *ui.Context, t, st int) {
	ui.Row(c).Absolute().Right(24).Bottom(20).Gap(6).AlignItems(ui.Center).ZIndex(3500).
		AnimatePose(welShow(st < 0 && t >= welSkipIn, 0, 0, 600)).Children(func() {
		key := ui.Box(c).Height(18).Padding(0, 5).Radius(4).Center().Background(colBgToastGlass)
		hairline(key, 0.12)
		key.Children(func() {
			ui.Text(c, "esc").FontSize(11).TextColor(colTextSecondary).SingleLine()
		})
		ui.Text(c, locales.WelcomeSkip()).FontSize(12).TextColor(colTextMuted).SingleLine()
	})
}

// welCardFace 是演示卡片的底和投影，和它的名字、简介两行。
func welCardFace(c *ui.Context, key any) ui.Element {
	card := ui.Column(c.Key(key)).Absolute().Left(0).Top(0).Size(welCardW, welCardH).Padding(12, 13, 0, 13).
		Radius(11).Background(colBgCard)
	hairline(card, 0.08)
	return card.Shadow(0, 1, 2, 0, shadow(0.04)).Shadow(0, 8, 22, 0, shadow(0.09))
}

// welDustView 画最远的那几张：很模糊、半透明，发牌时慢慢浮现。
func welDustView(c *ui.Context, g welGeom, t, st int) {
	for k, d := range welDust {
		pose, anim := g.card(welPose{x: d.x, y: d.y + 14, r: d.r, s: 0.64, fade: 1}), welEase(260, 0)
		if st < 0 && t >= welDeal {
			pose, anim = g.card(welPose{x: d.x, y: d.y, r: d.r, s: 0.64, fade: 0.5}), welEase(1400, 200+k*120)
		}
		card := welCardFace(c, "dust:"+d.name).ZIndex(1).Blur(2.5*g.s).AnimatePose(pose, anim)
		card.Children(func() {
			ui.Text(c, d.name).FontSize(14).FontWeight(600).TextColor(colTextPrimary).SingleLine()
			ui.Text(c, welDesc(d.name)).FontSize(12).TextColor(colTextSecondary).SingleLine().Margin(4, 0, 0, 0)
		})
	}
}

// welTrails 画重复的那几份飞向合成处时拖出的虚线：从各自的堆画到合成的
// 那张，画完留着，告诉人它是从哪几堆来的；收纳时淡去。只在画的时候按
// 时钟算，不重建界面。
func (a *app) welTrails(c *ui.Context, g welGeom) {
	ui.Box(c.Key("trails")).Absolute().Left(0).Top(0).Right(0).Bottom(0).ZIndex(5).Draw(func(p *ui.Painter, r ui.Rect) {
		if a.wel.start.IsZero() {
			return
		}
		now := p.Now()
		t := int(now.Sub(a.wel.start) / time.Millisecond)
		fade := float32(1)
		if !a.wel.stowAt.IsZero() {
			fade = 1 - min(float32(now.Sub(a.wel.stowAt))/float32(240*time.Millisecond), 1)
		}
		moving := fade > 0 && fade < 1
		for k, m := range welMerges {
			if t < m.at {
				continue
			}
			alpha := float32(0.45)
			if k > 0 {
				alpha = 0.22
			}
			for _, n := range welCopies(k) {
				wc := welCast[n]
				src := welPiles[wc.pile]
				d := float32(t-m.at) / welTrail
				if d < 1 {
					moving = true
				}
				if d <= 0 || fade <= 0 {
					continue
				}
				d = min(d, 1)
				d = 1 - (1-d)*(1-d)*(1-d)
				mx, my := (src.x+m.x)/2, min(src.y, m.y)-70
				path := new(ui.Path).MoveTo(r.X+g.x(src.x), r.Y+g.y(src.y)).
					QuadTo(r.X+g.x(mx), r.Y+g.y(my), r.X+g.x(m.x), r.Y+g.y(m.y)).
					Dash(0, 2*g.s, 5*g.s).Trim(0, d)
				p.StrokePath(path, 1.4*g.s, colAccent.Alpha(alpha*fade))
			}
		}
		if moving {
			p.AnimationFrame()
		}
	})
}

// welCardView 画第 n 张演示卡片，样子按时间轴算。
func (a *app) welCardView(c *ui.Context, g welGeom, n, t, st int, w, h float32) {
	wc := welCast[n]
	pile := welPiles[wc.pile]
	pose, anim := a.welCardPose(g, n, t, st, w, h)
	z := int(pile.z*100)*10 + 20 - wc.i
	merged := !welInPile(n, t)
	settled := merged && t >= welMerges[wc.merge].at+welCollide
	if merged {
		z = 3000 + 10 - wc.copy
	}
	if st >= 0 && wc.order >= 0 {
		z = 4000 + wc.order
	}
	dead := wc.name == welBrokenName && t >= welBroken && st < 0
	card := welCardFace(c, n).ZIndex(z)
	// 主角：投影更深，外面一圈淡蓝
	if ring := card.Animate("ring", b2f(settled && wc.merge == 0 && wc.copy == 0 && st < 0), 500*time.Millisecond); ring > 0 {
		card.Shadow(0, 22, 48, 0, shadow(0.12*ring)).Shadow(0, 0, 0, 4, colAccent.Alpha(0.08*ring))
	}
	// 远处的堆模糊；飞出来合并的、收纳时飞走的变清楚
	blur := pile.blur * g.s
	if merged || st >= 0 {
		blur = 0
	}
	if b := card.Animate("blur", blur, 420*time.Millisecond); b > 0.01 {
		card.Blur(b)
	}
	if dead && t < welBroken+welShake {
		// 抖一下：越来越轻
		k := float64(t-welBroken) / welShake
		card.Translate(float32(7*math.Sin(k*5*math.Pi)*(1-k)), 0)
	}
	card.AnimatePose(pose, anim)
	var dim float32 = 1
	switch {
	case settled && wc.copy > 0:
		dim = 0 // 叠在主角背后的几份：只露边
	case st < 0 && !merged && welRank(n, t) > 0:
		dim = 0 // 压在堆里的：字藏起来，不从上面那张的边上露出来
	}
	dim = card.Animate("dim", dim, 300*time.Millisecond)
	title, desc := colTextPrimary, colTextSecondary
	if dead {
		title, desc = colTextMuted, colTextDisabled
	}
	fade := ui.Animation{Duration: 300 * time.Millisecond}
	card.Children(func() {
		ui.Column(c).Opacity(dim).Children(func() {
			name := ui.Text(c, wc.name).FontSize(14).FontWeight(600).SingleLine()
			name.TextColor(name.AnimateColor("c", title, fade))
			line := ui.Text(c, welDesc(wc.name)).FontSize(12).SingleLine().Margin(4, 0, 0, 0)
			line.TextColor(line.AnimateColor("c", desc, fade))
		})
		ui.Row(c).Absolute().Left(13).Right(13).Bottom(11).AlignItems(ui.Center).Opacity(dim).Children(func() {
			welCardFoot(c, n, t, st, settled, dead)
		})
	})
}

// welCardFoot 是卡片底部一行：Agent 图标；合成的那张依次弹出每一份的图标，
// 右边一枚「4 份合成 1 份」；断链的那张右边写「链接断了」。
func welCardFoot(c *ui.Context, n, t, st int, settled, dead bool) {
	wc := welCast[n]
	ids := []string{welPiles[wc.pile].agent}
	if wc.merge >= 0 && wc.copy == 0 {
		ids = ids[:0]
		for _, m := range welCopies(wc.merge) {
			ids = append(ids, welPiles[welCast[m].pile].agent)
		}
	}
	ui.Row(c).Gap(2).Children(func() {
		for j, id := range ids {
			ui.Box(c.Key(id)).AnimatePose(welPop(j == 0 || settled, 80+j*70)).Children(func() {
				agentBadge(c, id, 14)
			})
		}
	})
	ui.Box(c).Grow(1)
	switch {
	case wc.merge >= 0 && wc.copy == 0:
		pill := ui.Row(c).Height(18).Padding(0, 7).Radius(9).AlignItems(ui.Center).Background(colAccentBg).
			AnimatePose(welPop(settled && st < 0, 360))
		pill.Children(func() {
			ui.Text(c, locales.WelcomeMerged(len(ids))).FontSize(11).FontWeight(600).TextColor(colAccentText).
				FontFeatures("tnum").SingleLine()
		})
	case wc.name == welBrokenName:
		pose, anim := ui.Pose{X: -4, Fade: 1}, welEase(200, 0)
		if dead {
			pose, anim = ui.Pose{}, welEase(420, 180)
		}
		ui.Row(c).Gap(4).AlignItems(ui.Center).TextColor(colDangerText).AnimatePose(pose, anim).Children(func() {
			ui.Icon(c, issueIcons[library.BrokenLinks].glyph).Size(12, 12)
			ui.Text(c, locales.WelcomeBroken()).FontSize(11).FontWeight(600).SingleLine()
		})
	}
}

// welCardPose 是第 n 张演示卡片此刻该去的样子和怎么去。
func (a *app) welCardPose(g welGeom, n, t, st int, w, h float32) (ui.Pose, ui.Animation) {
	wc := welCast[n]
	pile := welPiles[wc.pile]
	if st >= 0 && (wc.order >= 0 || !welInPile(n, t)) {
		// 叠在主角背后的几份跟着它飞进同一格，边飞边淡去
		k := wc.order
		if k < 0 {
			k = welCast[welCopies(wc.merge)[0]].order
		}
		r := a.welSlot(k, w, h)
		p := ui.Pose{X: r.X + r.W/2 - welCardW/2, Y: r.Y + r.H/2 - welCardH/2, Scale: r.W / welCardW}
		if wc.order < 0 {
			p.Fade = 1
		}
		if st >= welCardsOut {
			p.Fade = 1
			return p, welEase(300, 0)
		}
		return p, welSpring(980, 0.18, 60+(k/4)*50+(k%4)*28)
	}
	if !welInPile(n, t) {
		m := welMerges[wc.merge]
		j := float32(wc.copy)
		switch {
		case t >= m.at+welCollide && wc.copy == 0:
			// 合成：微微弹大到主角的大小
			return g.card(welPose{x: m.x, y: m.y, r: m.r, s: m.s}), welSpring(720, 0.42, 0)
		case t >= m.at+welCollide:
			// 其余几份散开一点，叠在它背后
			gh := m.ghosts[wc.copy-1]
			return g.card(welPose{x: m.x + gh[0], y: m.y + gh[1], r: m.r + gh[2], s: m.s * 0.98, fade: 0.5}),
				welSpring(760, 0.25, (wc.copy-1)*60)
		default:
			// 各堆里的几份同时起飞（飞之前已经换到最上层、露出字，晚一刻起飞
			// 会先盖住它所在那堆的最上面一张），到了错开一点
			return g.card(welPose{x: m.x + j*3, y: m.y - j*3, r: (j - 1) * 2.2, s: m.s * 0.94}), welSpring(900, 0.2, 0)
		}
	}
	rest := welRest(n, t)
	if t >= welSettled {
		// 上面那张飞走了，往上补位
		return g.card(rest), welSpring(640, 0.22, 140)
	}
	if t >= welDeal {
		delay := wc.pile*170 + (len(pile.cards)-1-wc.i)*60
		return g.card(rest), welSpring(1000, 0.2, delay)
	}
	// 还没发牌：在窗口外，顺着堆离中心的方向
	dx, dy := pile.x-welW/2, pile.y-welH/2
	l := float32(math.Hypot(float64(dx), float64(dy)))
	rest.x += dx / l * 560
	rest.y += dy/l*560 + 60
	rest.r += 22 * float32(sign(dx))
	rest.fade = 1
	return g.card(rest), ui.Animation{Duration: time.Millisecond}
}

func sign(v float32) int {
	if v < 0 {
		return -1
	}
	return 1
}

// welSlot 是第三幕第 k 张卡片落到哪：主界面里第 k 张卡片的位置；真实卡片
// 不够时接着最后一张往后排（没有卡片时从内容区左上角排起）。
func (a *app) welSlot(k int, w, h float32) ui.Rect {
	if k < len(a.wel.slots) {
		return a.wel.slots[k]
	}
	left := a.sideShown + cardPad
	area := w - a.sideShown
	cols := columnsFor(area)
	cw := (area - 2*cardPad - float32(cols-1)*cardGap) / float32(cols)
	ch := cw * 120 / 246
	idx, top := k, float32(welEmptyTop)
	if n := len(a.wel.slots); n > 0 {
		last := a.wel.slots[n-1]
		cw, ch = last.W, last.H
		col := int(math.Round(float64((last.X - left) / (cw + cardGap))))
		idx, top = col+1+k-n, last.Y
	}
	return ui.Rect{X: left + float32(idx%cols)*(cw+cardGap), Y: top + float32(idx/cols)*(ch+cardGap), W: cw, H: ch}
}

// welEmptyTop 是没有真实卡片时第一排落在多高：工具栏和第一个分组标题下面。
const welEmptyTop = 108

// welChips 画每堆上方的 Agent 名牌，和没有技能的 Agent 的淡名牌。名牌不随
// 远近缩放，一律 12 号；远处那堆的名牌跟着一起模糊。
func welChips(c *ui.Context, g welGeom, t, st int) {
	on := st < 0 && t >= welDeal
	for p, pile := range welPiles {
		x := g.x(pile.x - welCardW/2*pile.z)
		y := g.y(pile.y - welCardH/2*pile.z - 12*pile.z - 34)
		welChip(c, g, pile.agent, pile.count, x, y, pile.blur, on, p*170+len(pile.cards)*60+520, false)
	}
	for k, e := range welEmpty {
		welChip(c, g, e.agent, 0, g.x(e.x), g.y(e.y), 0.6, on, 400+k*200, true)
	}
}

func welChip(c *ui.Context, g welGeom, agent string, count int, x, y, blur float32, on bool, delay int, empty bool) {
	pose, anim := ui.Pose{Y: 8, Scale: g.s, Fade: 1}, welEase(260, 0)
	if on {
		pose, anim = ui.Pose{Scale: g.s}, welEase(500, delay)
		if empty {
			pose.Fade = 0.3
		}
	}
	z := 2000
	if empty {
		z = 2
	}
	chip := ui.Row(c.Key("chip:"+agent)).Absolute().Left(x).Top(y).Height(24).Padding(0, 9, 0, 4).Gap(6).
		Radius(12).AlignItems(ui.Center).Background(colBgToastGlass).TransformOrigin(0, 0).ZIndex(z)
	hairline(chip, 0.08)
	chip.Shadow(0, 2, 8, 0, shadow(0.08))
	if blur > 0 {
		chip.Blur(blur * g.s)
	}
	chip.AnimatePose(pose, anim).Children(func() {
		agentBadge(c, agent, 16)
		name := ui.Text(c, library.AgentName(agent)).FontSize(12).FontWeight(600).SingleLine()
		if empty {
			name.TextColor(colTextMuted)
		} else {
			name.TextColor(colTextPrimary)
		}
		ui.Text(c, strconv.Itoa(count)).FontSize(12).FontWeight(500).TextColor(colTextMuted).FontFeatures("tnum").SingleLine()
	})
}
