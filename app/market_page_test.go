package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"sync/atomic"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"skillmanager/internal/library"
	"skillmanager/locales"
)

func TestMarketInputClassification(t *testing.T) {
	cases := []struct {
		input string
		want  marketMode
	}{
		{"", marketModeBrowse},
		{"   ", marketModeBrowse},
		{"pdf", marketModeSearch},
		{"react ui components", marketModeSearch},
		{"agent workflow", marketModeSearch},
		{"https://github.com/anthropics/skills", marketModeLink},
		{"https://skills.sh/vercel-labs/skills/find-skills", marketModeLink},
		{"https://clawhub.ai/awspace/skills/pdf", marketModeLink},
		{"npx skills add vercel-labs/skills --skill find-skills", marketModeLink},
		{"antfu/skills", marketModeLink},
		{"skills.sh/vercel-labs/skills/find-skills", marketModeLink},
		{"clawhub.ai/awspace/pdf", marketModeLink},
		{"mintlify.com/mintlify", marketModeLink},
		{"/tmp/local-skill", marketModeLink},
		{"~/Downloads/my-skill.zip", marketModeLink},
	}
	for _, tc := range cases {
		if got := classifyMarketInput(tc.input); got != tc.want {
			t.Errorf("classifyMarketInput(%q) = %v, want %v", tc.input, got, tc.want)
		}
	}
}

func TestMarketChipSwitchingAndTagPrecedence(t *testing.T) {
	a, tt := newTester(t)
	a.openImport()
	tt.Frame()

	// frontend-design 在样例里同时是 Official 且已被 anthropics/skills 安装：只显示「已安装」，不显示「官方」。
	if !tt.HasText("find-skills") || !tt.HasText("官方") || !tt.HasText("已安装") {
		t.Fatalf("skills.sh 总榜缺预期标签或条目：%v", tt.Texts())
	}
	officialCount := 0
	for _, s := range tt.Texts() {
		if s == locales.MarketTagOfficial() {
			officialCount++
		}
	}
	// 顶部筛选 chip 本身含 ButtonBase.Label + Text 两处「官方」，列表行中只有 find-skills 与 agent-browser 显示「官方」（共 4 处）；frontend-design 优先显示「已安装」。
	if officialCount != 4 {
		t.Fatalf("预期总计 4 处「官方」（chip 2 处 + 列表 2 枚标签，frontend-design 不重复显示），实际 %d 处", officialCount)
	}

	// 切到趋势、热门、专题、再切 ClawHub，标签栏重置回「全部」
	must(t, tt.Click(locales.MarketViewTrending()))
	if !tt.HasText("ai-video-generation") {
		t.Fatalf("切到趋势后未显示 ai-video-generation：%v", tt.Texts())
	}
	must(t, tt.Click(locales.MarketTopicTesting()))
	if !tt.HasText("test-driven-development") || !tt.HasText(locales.MarketTopicIntro(2)) {
		t.Fatalf("切到测试专题后缺条目或介绍：%v", tt.Texts())
	}

	must(t, tt.Click("ClawHub"))
	tt.Advance(segmentedSpring.Spring.Duration)
	if !tt.HasText("skill-vetter") || !tt.HasText("下载") {
		t.Fatalf("切到 ClawHub 后未显示技能或下载单位：%v", tt.Texts())
	}
	must(t, tt.Click(locales.MarketCategorySecurity()))
	if curView, curKey := a.currentMarketView(); curView != library.MarketViewCategory || curKey != "security" {
		t.Fatalf("点击安全分类后视图=(%s, %q)", curView, curKey)
	}

	// 切回 skills.sh 后标签回到「总榜」
	must(t, tt.Click("skills.sh"))
	if curView, curKey := a.currentMarketView(); curView != library.MarketViewAllTime || curKey != "" {
		t.Fatalf("切换市场来源应回到总榜，实际=(%s, %q)", curView, curKey)
	}

	// 搜索结果绝不显示「官方」标签（即便该技能在总榜是官方）
	a.imp.input = "find"
	a.syncMarketSearchState()
	a.triggerMarketSearchNow("find")
	tt.Frame()
	if !tt.HasText("find-skills") {
		t.Fatalf("搜索 find 没找到 find-skills：%v", tt.Texts())
	}
	if tt.HasText(locales.MarketTagOfficial()) {
		t.Fatalf("搜索结果不应显示「官方」标签：%v", tt.Texts())
	}
}

func TestMarketDetailPaneActionsAndKeyboardNavigation(t *testing.T) {
	a, tt := newTester(t)
	var opened string
	a.openURL = func(u string) { opened = u }

	a.openImport()
	tt.Frame()

	// 点击 find-skills 打开详情栏
	must(t, tt.Click("find-skills"))
	tt.Advance(paneDuration)
	if a.mkt.selected == nil || a.mkt.selected.ID != "vercel-labs/skills/find-skills" {
		t.Fatalf("selected = %+v", a.mkt.selected)
	}
	for _, want := range []string{"每周安装", "112,607", "总安装量", "4,493,759", "总榜排名", "第 1", "查看并添加…", "在网页中打开"} {
		if !tt.HasText(want) {
			t.Errorf("详情栏缺 %q：%v", want, tt.Texts())
		}
	}

	// 打开原网页与 GitHub 外链（按按钮角色定位右下角外链，避开详情页头的静态副标题文本）
	must(t, tt.Click(locales.MarketOpenOnWeb()))
	if opened != "https://skills.sh/vercel-labs/skills/find-skills" {
		t.Fatalf("在网页中打开 = %q", opened)
	}
	ghBtn, ok := tt.AccessByRole(ui.RoleButton, "vercel-labs/skills")
	if !ok {
		t.Fatalf("找不到 GitHub 外链按钮：%v", tt.Texts())
	}
	tt.ClickAt(ghBtn.Bounds.X+ghBtn.Bounds.W/2, ghBtn.Bounds.Y+ghBtn.Bounds.H/2)
	if opened != "https://github.com/vercel-labs/skills" {
		t.Fatalf("GitHub 外链 = %q", opened)
	}

	// 上下键移动选中，Esc 取消选中
	tt.Key(0, ui.KeyDown)
	tt.Frame()
	if a.mkt.selected == nil || a.mkt.selected.ID != "heygen-com/hyperframes/hyperframes-cli" {
		t.Fatalf("按 Down 应切到第二项，实际 %+v", a.mkt.selected)
	}
	tt.Key(0, ui.KeyUp)
	tt.Frame()
	if a.mkt.selected == nil || a.mkt.selected.ID != "vercel-labs/skills/find-skills" {
		t.Fatalf("按 Up 应回到第一项，实际 %+v", a.mkt.selected)
	}
	tt.Key(0, ui.KeyEscape)
	tt.Advance(paneDuration)
	if a.mkt.selected != nil {
		t.Fatal("Esc 应取消详情选中")
	}

	// 点击「查看并添加…」应调用 startImport(item.ImportRef) 并切到读取（示例库报示例不支持联网，但能确认传参写入了链接态）
	must(t, tt.Click("find-skills"))
	tt.Advance(paneDuration)
	must(t, tt.Click(locales.MarketViewAndAdd()))
	if a.imp.input != "https://skills.sh/vercel-labs/skills/find-skills" {
		t.Fatalf("点查看并添加后 input = %q", a.imp.input)
	}
	if a.imp.err == nil || a.imp.err.Msg != locales.ImportSampleNoNetwork() {
		t.Fatalf("示例模式下调用 startImport 后 err = %+v", a.imp.err)
	}
}

func TestMarketSearchDebouncePartialFailureAndExpand(t *testing.T) {
	home := t.TempDir()
	var shCalls, chCalls atomic.Int32
	var shFail atomic.Bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/market-extras.json":
			_, _ = w.Write([]byte(`{"version":1,"descriptions":{},"topics":[]}`))
		case "/api/skills/all-time/0":
			_, _ = w.Write([]byte(`{"skills":[],"hasMore":false}`))
		case "/api/search":
			shCalls.Add(1)
			if shFail.Load() {
				http.Error(w, "down", http.StatusBadGateway)
				return
			}
			_, _ = w.Write([]byte(`{"skills":[
				{"source":"anthropics/skills","skillId":"pdf","name":"pdf","installs":235455},
				{"source":"openai/skills","skillId":"pdf","name":"pdf","installs":13995},
				{"source":"github/awesome-copilot","skillId":"pdftk-server","name":"pdftk-server","installs":10000},
				{"source":"tanis90/pdf-converter-mineru","skillId":"pdf-converter","name":"pdf-converter","installs":9739},
				{"source":"anthropics/knowledge-work-plugins","skillId":"view-pdf","name":"view-pdf","installs":6483}
			]}`))
		case "/api/v1/search":
			chCalls.Add(1)
			_, _ = w.Write([]byte(`{"results":[
				{"source":"clawhub","ownerHandle":"awspace","slug":"pdf","displayName":"Pdf","summary":"PDF kit","categories":["knowledge"],"canonicalUrl":"/awspace/skills/pdf","downloads":52554,"install":{"kind":"clawhub"}}
			]}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	a := newApp(library.Scan(home))
	mc := library.NewMarketClient(library.Settings{}, filepath.Join(home, "market"))
	mc.SkillsShBase, mc.ClawHubBase, mc.ExtrasURL = srv.URL, srv.URL, srv.URL+"/market-extras.json"
	mc.Client = srv.Client()
	a.mkt.client = mc

	var pendingDelay func()
	a.delay = func(d time.Duration, fn func()) {
		if d != 300*time.Millisecond {
			t.Errorf("搜索防抖延迟 = %v，应为 300ms", d)
		}
		pendingDelay = fn
	}

	tt := newUITester(a.view, 1280, 800)
	a.openImport()
	tt.Frame()

	// 输入后在 300ms 到期前不发请求；被新输入覆盖后只搜最后一次
	a.imp.input = "p"
	tt.Frame()
	if shCalls.Load() != 0 || chCalls.Load() != 0 || pendingDelay == nil {
		t.Fatalf("防抖到期前不应发搜索：sh=%d ch=%d pending=%v", shCalls.Load(), chCalls.Load(), pendingDelay != nil)
	}
	staleFn := pendingDelay
	a.imp.input = "pdf"
	tt.Frame()
	staleFn() // 已被 seq 作废
	if shCalls.Load() != 0 || chCalls.Load() != 0 {
		t.Fatalf("过期的防抖回调不应触发搜索：sh=%d ch=%d", shCalls.Load(), chCalls.Load())
	}
	pendingDelay()
	tt.Frame()
	if shCalls.Load() != 1 || chCalls.Load() != 1 {
		t.Fatalf("当前防抖到期应各搜一次：sh=%d ch=%d", shCalls.Load(), chCalls.Load())
	}
	if tt.HasText("view-pdf") {
		t.Fatal("折叠态每组最多只显示前 4 条")
	}

	// 点「显示全部」展开 skills.sh 并收起 ClawHub
	must(t, tt.Click(locales.MarketShowAll()))
	if !tt.HasText("view-pdf") || tt.HasText("awspace") {
		t.Fatalf("展开一组后应显示完整列表并收起另一组：%v", tt.Texts())
	}

	// 一边失败时显示不完整提示，另一边照常
	shFail.Store(true)
	a.imp.input = "broken"
	tt.Frame()
	pendingDelay()
	tt.Frame()
	if !tt.HasText(locales.MarketSourceUnavailable("skills.sh")) || !tt.HasText("Pdf") {
		t.Fatalf("一边失败时另一边仍应显示：%v", tt.Texts())
	}
}

func TestMarketOfflineRetryAndEmptyCacheError(t *testing.T) {
	home := t.TempDir()
	var fail atomic.Bool
	fail.Store(true)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if fail.Load() {
			http.Error(w, "offline", http.StatusServiceUnavailable)
			return
		}
		if r.URL.Path == "/api/skills/all-time/0" {
			_, _ = w.Write([]byte(`{"skills":[{"source":"vercel-labs/skills","skillId":"find-skills","name":"find-skills","installs":100}],"hasMore":false}`))
			return
		}
		_, _ = w.Write([]byte(`{"version":1,"descriptions":{},"topics":[]}`))
	}))
	defer srv.Close()

	a := newApp(library.Scan(home))
	mc := library.NewMarketClient(library.Settings{}, filepath.Join(home, "market"))
	mc.SkillsShBase, mc.ClawHubBase, mc.ExtrasURL = srv.URL, srv.URL, srv.URL+"/market-extras.json"
	mc.Client = srv.Client()
	a.mkt.client = mc

	tt := newUITester(a.view, 1280, 800)
	a.openImport()
	tt.Frame()
	if !tt.HasText(locales.MarketLoadFailed("skills.sh")) || !tt.HasText(locales.MarketRetry()) {
		t.Fatalf("无缓存失败应显示出错和重试：%v", tt.Texts())
	}

	fail.Store(false)
	must(t, tt.Click(locales.MarketRetry()))
	if !tt.HasText("find-skills") {
		t.Fatalf("点重试恢复后应列出技能：%v", tt.Texts())
	}
}

func TestMarketCompactCountAndMonotoneSparklineBounds(t *testing.T) {
	t.Cleanup(func() { testLocale.Store("") })
	testLocale.Store("zh-Hans")
	if got := compactCount(9739); got != "9,739" {
		t.Errorf("zh 9739 = %q", got)
	}
	if got := compactCount(14200); got != "1.4万" {
		t.Errorf("zh 14200 = %q", got)
	}
	if got := compactCount(4493759); got != "449.4万" {
		t.Errorf("zh 4493759 = %q", got)
	}

	testLocale.Store("en")
	if got := compactCount(9739); got != "9,739" {
		t.Errorf("en 9739 = %q", got)
	}
	if got := compactCount(13995); got != "14K" {
		t.Errorf("en 13995 = %q", got)
	}
	if got := compactCount(14200); got != "14.2K" {
		t.Errorf("en 14200 = %q", got)
	}
	if got := compactCount(4493759); got != "4.5M" {
		t.Errorf("en 4493759 = %q", got)
	}

	// 单调三次插值在任何阶跃、尖峰、V 形序列上都不越过首尾极值的纵坐标范围
	rect := ui.Rect{X: 0, Y: 0, W: 132, H: 36}
	samples := [][]int64{
		{10, 10, 10, 10, 10, 10, 10, 10},
		{0, 0, 1000, 1000, 0, 0, 500, 500},
		{107199, 110524, 121189, 121665, 115285, 110096, 109742, 112607},
		{10, 100, 5, 120, 2, 140, 1, 150},
	}
	for si, pts := range samples {
		xs, ys := sparklineCoords(pts, rect)
		segs := monotoneCubicSegments(xs, ys)
		minY, maxY := slices.Min(ys), slices.Max(ys)
		const eps = 1e-3
		for _, seg := range segs {
			for step := 0; step <= 40; step++ {
				tVal := float32(step) / 40
				u := 1 - tVal
				y := u*u*u*seg.y0 + 3*u*u*tVal*seg.c1y + 3*u*tVal*tVal*seg.c2y + tVal*tVal*tVal*seg.y1
				if y < minY-eps || y > maxY+eps {
					t.Fatalf("sample[%d] 插值点越界: y=%v 不在 [%v, %v]", si, y, minY, maxY)
				}
			}
		}
	}
}

func TestMarketSnapshots(t *testing.T) {
	dir := os.Getenv("SNAPSHOT_DIR")
	if dir == "" {
		t.Skip("设置 SNAPSHOT_DIR 以输出云市场截图")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	shot := func(tt *ui.Tester, name string) {
		tt.Advance(paneDuration + 80*time.Millisecond)
		tt.SetScale(2)
		writePNG(t, filepath.Join(dir, name), tt)
	}

	a := newApp(library.Sample())
	a.scope = scopeImport
	tt := newUITester(a.view, 1280, 800)
	tt.Frame()
	shot(tt, "m1-browse.png")

	must(t, tt.Click("find-skills"))
	shot(tt, "m2-selected.png")

	must(t, tt.Click("ClawHub"))
	tt.Advance(segmentedSpring.Spring.Duration)
	a.selectClawHubCategory("security")
	tt.Frame()
	must(t, tt.Click("skill-vetter"))
	shot(tt, "m3-clawhub.png")

	a.clearMarketInput()
	a.imp.input = "pdf"
	a.syncMarketSearchState()
	a.triggerMarketSearchNow("pdf")
	tt.Move(0, 0)
	tt.Frame()
	shot(tt, "m4-search.png")

	a.clearMarketInput()
	a.imp.input = "https://github.com/anthropics/skills"
	a.syncMarketSearchState()
	tt.Frame()
	shot(tt, "m5-link-ready.png")

	a.imp.cancel = func() {}
	a.imp.reading = "anthropics/skills"
	a.imp.prog = library.ImportProgress{
		Commit: "e4bb5d9000000000000000000000000000000000",
		Bytes:  3250585,
		Found: []string{
			"academy-guide", "algorithmic-art", "brand-guidelines", "canvas-design", "claude-api",
			"discernment-nudge", "doc-coauthoring", "docx", "frontend-design", "internal-comms", "mcp-builder",
		},
		Done:  11,
		Total: 19,
	}
	tt.Frame()
	shot(tt, "m6-link-reading.png")
	a.imp.cancel = nil
}

func TestMarketEnglishLocaleAndNarrowChipBar(t *testing.T) {
	a, tt := newTester(t)
	useLocale(t, tt, "en")
	a.openImport()
	tt.Frame()

	for _, want := range []string{"Skill Market", "All Time", "Trending", "Hot", "Official", "Installed", "installs", "4.5M"} {
		if !tt.HasText(want) {
			t.Errorf("英文总榜缺少 %q：%v", want, tt.Texts())
		}
	}

	must(t, tt.Click("find-skills"))
	tt.Advance(paneDuration)
	for _, want := range []string{"Weekly Installs", "Total Installs", "All-Time Rank", "#1", "View and Add…", "Open on the Web"} {
		if !tt.HasText(want) {
			t.Errorf("英文详情栏缺少 %q：%v", want, tt.Texts())
		}
	}

	must(t, tt.Click("ClawHub"))
	tt.Advance(segmentedSpring.Spring.Duration)
	for _, want := range []string{"Security", "downloads"} {
		if !tt.HasText(want) {
			t.Errorf("英文 ClawHub 缺少 %q：%v", want, tt.Texts())
		}
	}

	// 窄窗下标签栏保持单行可横向滚动并能选中靠后的专题
	tt.SetSize(720, 640)
	tt.Frame()
	must(t, tt.Click("skills.sh"))
	tt.Advance(segmentedSpring.Spring.Duration)
	if acc, ok := tt.AccessByRole(ui.RoleButton, "All Time"); ok {
		tt.Scroll(acc.Bounds.X+10, acc.Bounds.Y+10, 700, 0)
		tt.Frame()
	}
	must(t, tt.Click("Marketing"))
	if curView, curKey := a.currentMarketView(); curView != library.MarketViewTopic || curKey != "marketing" {
		t.Fatalf("窄窗滚动后选择 Marketing 失败: (%s, %q)", curView, curKey)
	}
}
