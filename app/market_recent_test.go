package main

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"sync/atomic"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"skillmanager/internal/library"
	"skillmanager/locales"
)

// newRecentMarketTester 是一个用临时目录和假市场服务的 Skill 市场页：
// 搜索不碰公网，最近搜索写进临时目录的 settings.json。
func newRecentMarketTester(t *testing.T, recent ...string) (*app, *ui.Tester, string, *atomic.Int32) {
	t.Helper()
	home := t.TempDir()
	var searches atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/market-extras.json":
			_, _ = w.Write([]byte(`{"version":1,"descriptions":{},"topics":[]}`))
		case "/api/skills/all-time/0":
			_, _ = w.Write([]byte(`{"skills":[],"hasMore":false}`))
		case "/api/search":
			searches.Add(1)
			_, _ = w.Write([]byte(`{"skills":[{"source":"anthropics/skills","skillId":"pdf","name":"pdf","installs":235455}]}`))
		case "/api/v1/search":
			_, _ = w.Write([]byte(`{"results":[]}`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)

	lib := library.Scan(home)
	// 后记的在前
	for _, q := range slices.Backward(recent) {
		lib.NoteMarketSearch(q)
	}
	a := newApp(lib)
	mc := library.NewMarketClient(library.Settings{}, filepath.Join(home, "market"))
	mc.SkillsShBase, mc.ClawHubBase, mc.ExtrasURL = srv.URL, srv.URL, srv.URL+"/market-extras.json"
	mc.Client = srv.Client()
	a.mkt.client = mc
	a.delay = func(time.Duration, func()) {} // 防抖的自动搜索不跑，只看明确触发的

	tt := newUITester(a.view, 1280, 800)
	a.openImport()
	tt.Frame()
	return a, tt, home, &searches
}

func clickMarketSearchBox(t *testing.T, tt *ui.Tester) {
	t.Helper()
	acc, ok := tt.AccessByRole(ui.RoleComboBox, locales.MarketTitle())
	if !ok {
		t.Fatalf("找不到 Skill 市场搜索框：%q", tt.Texts())
	}
	tt.ClickAt(acc.Bounds.X+acc.Bounds.W/2, acc.Bounds.Y+acc.Bounds.H/2)
	tt.Advance(menuEnterAnim.Duration)
}

func recentDropdownShown(tt *ui.Tester) bool {
	return tt.HasText(locales.MarketClearRecentSearches())
}

func TestMarketRecentSearchesShowWhenTheEmptySearchBoxIsFocused(t *testing.T) {
	// Given 搜过 pdf 和 anthropics/skills
	_, tt, _, _ := newRecentMarketTester(t, "pdf", "anthropics/skills")
	if recentDropdownShown(tt) {
		t.Fatal("搜索框没有焦点时不应出现最近搜索")
	}

	// When 点进空搜索框
	clickMarketSearchBox(t, tt)

	// Then 下拉列出标题、两条记录和清除
	for _, want := range []string{locales.MarketRecentSearches(), "pdf", "anthropics/skills", locales.MarketClearRecentSearches()} {
		if !tt.HasText(want) {
			t.Errorf("最近搜索下拉缺 %q：%q", want, tt.Texts())
		}
	}

	// When 开始打字
	tt.Type("ag")
	tt.Frame()

	// Then 下拉收起，页面照常进入搜索
	if recentDropdownShown(tt) {
		t.Fatalf("打字后最近搜索应收起：%q", tt.Texts())
	}

	// When 按 Esc 清空搜索框
	tt.Key(0, ui.KeyEscape)
	tt.Advance(menuEnterAnim.Duration)

	// Then 框空了，焦点还在，最近搜索又出现
	if !recentDropdownShown(tt) {
		t.Fatalf("清空搜索框后最近搜索应再出现：%q", tt.Texts())
	}

	// When 再按 Esc
	tt.Key(0, ui.KeyEscape)
	tt.Advance(menuEnterAnim.Duration)

	// Then 下拉收起
	if recentDropdownShown(tt) {
		t.Fatalf("Esc 应收起最近搜索：%q", tt.Texts())
	}
}

func TestMarketRecentSearchesHideWithoutRecords(t *testing.T) {
	// Given 还没搜过任何东西
	_, tt, _, _ := newRecentMarketTester(t)

	// When 点进空搜索框
	clickMarketSearchBox(t, tt)

	// Then 不出现下拉
	if tt.HasText(locales.MarketRecentSearches()) {
		t.Fatalf("没有记录时不应出现最近搜索：%q", tt.Texts())
	}
}

func TestMarketRecentSearchChosenWithKeysSearchesAgainAndMovesToTop(t *testing.T) {
	// Given 搜过 pdf、react testing（react testing 较早）
	a, tt, home, searches := newRecentMarketTester(t, "pdf", "react testing")
	clickMarketSearchBox(t, tt)

	// When 按两次 ↓ 选到 react testing，回车
	tt.Key(0, ui.KeyDown)
	tt.Key(0, ui.KeyDown)
	tt.Key(0, ui.KeyEnter)
	tt.Frame()

	// Then 填进搜索框并立刻搜索，不等防抖
	if a.imp.input != "react testing" {
		t.Fatalf("搜索框 = %q，应为 react testing", a.imp.input)
	}
	if searches.Load() != 1 {
		t.Fatalf("选中记录应立刻搜索一次，实际 %d 次", searches.Load())
	}
	if recentDropdownShown(tt) {
		t.Fatalf("选中后下拉应收起：%q", tt.Texts())
	}

	// And 这一条挪到最前，并写进设置
	want := []string{"react testing", "pdf"}
	if got := library.Scan(home).Settings.MarketSearches; !slices.Equal(got, want) {
		t.Fatalf("最近搜索 = %q，应为 %q", got, want)
	}
}

func TestMarketSearchSubmittedWithEnterIsRemembered(t *testing.T) {
	// Given 搜过 pdf
	a, tt, home, _ := newRecentMarketTester(t, "pdf")
	clickMarketSearchBox(t, tt)

	// When 输入 agent 后回车
	tt.Type("agent")
	tt.Frame()
	tt.Key(0, ui.KeyEnter)
	tt.Frame()

	// Then 记下 agent，排在最前；打字过程中的半截词不记
	want := []string{"agent", "pdf"}
	if got := library.Scan(home).Settings.MarketSearches; !slices.Equal(got, want) {
		t.Fatalf("最近搜索 = %q，应为 %q（输入框 %q）", got, want, a.imp.input)
	}
}

func TestMarketRecentSearchRemovedOrClearedFromTheDropdown(t *testing.T) {
	// Given 搜过 pdf、anthropics/skills
	a, tt, home, searches := newRecentMarketTester(t, "pdf", "anthropics/skills")
	clickMarketSearchBox(t, tt)

	// When 鼠标停在 anthropics/skills 那一行，点右边的 ×
	r, ok := tt.Find("anthropics/skills")
	if !ok {
		t.Fatalf("找不到 anthropics/skills：%q", tt.Texts())
	}
	tt.Move(r.X+r.W/2, r.Y+r.H/2)
	tt.Frame()
	x, ok := tt.AccessByRole(ui.RoleButton, locales.MarketRemoveRecentSearch())
	if !ok {
		t.Fatalf("悬停的行应出现 ×：%q", tt.Texts())
	}
	tt.ClickAt(x.Bounds.X+x.Bounds.W/2, x.Bounds.Y+x.Bounds.H/2)
	tt.Frame()

	// Then 只去掉这一条，不填进搜索框、不读取，下拉还开着
	if got := library.Scan(home).Settings.MarketSearches; !slices.Equal(got, []string{"pdf"}) {
		t.Fatalf("去掉后 = %q", got)
	}
	if a.imp.input != "" || a.imp.cancel != nil || searches.Load() != 0 {
		t.Fatalf("点 × 不应选中这一条：input=%q reading=%v searches=%d", a.imp.input, a.imp.cancel != nil, searches.Load())
	}
	if !recentDropdownShown(tt) || tt.HasText("anthropics/skills") {
		t.Fatalf("删一条后下拉应留着、少这一条：%q", tt.Texts())
	}

	// When 点「清除最近搜索」
	must(t, tt.Click(locales.MarketClearRecentSearches()))
	tt.Advance(menuEnterAnim.Duration)

	// Then 全部清空，下拉收起
	if got := library.Scan(home).Settings.MarketSearches; len(got) != 0 {
		t.Fatalf("清除后 = %q", got)
	}
	if tt.HasText(locales.MarketRecentSearches()) {
		t.Fatalf("清空后下拉应收起：%q", tt.Texts())
	}
}

func TestMarketRecentSearchesShowWhenFocusedWithCommandF(t *testing.T) {
	// Given 搜过 pdf
	_, tt, _, _ := newRecentMarketTester(t, "pdf")

	// When 按 ⌘F 聚焦搜索框
	tt.Key(ui.Cmd, ui.KeyF)
	tt.Advance(menuEnterAnim.Duration)

	// Then 出现最近搜索
	if !recentDropdownShown(tt) {
		t.Fatalf("⌘F 聚焦空搜索框后应出现最近搜索：%q", tt.Texts())
	}
}

func TestMarketRecentSearchesCloseAndLetGoOfTheFocusWhenClickingElsewhere(t *testing.T) {
	// Given 点进空搜索框，最近搜索下拉开着
	_, tt, _, _ := newRecentMarketTester(t, "pdf", "anthropics/skills")
	clickMarketSearchBox(t, tt)
	if !recentDropdownShown(tt) {
		t.Fatalf("点进空搜索框后应出现最近搜索：%q", tt.Texts())
	}

	// When 点页面下方的空白处，等下拉收起的动画放完
	tt.ClickAt(640, 700)
	tt.Advance(menuEnterAnim.Duration)
	tt.Advance(menuEnterAnim.Duration)

	// Then 下拉收起，搜索框也不再有焦点（不会被收起的下拉还回去再弹出来）
	acc, ok := tt.AccessByRole(ui.RoleComboBox, locales.MarketTitle())
	if !ok {
		t.Fatalf("找不到 Skill 市场搜索框：%q", tt.Texts())
	}
	if acc.Focused || recentDropdownShown(tt) {
		t.Fatalf("点别处后应收起并失去焦点：focused=%v %q", acc.Focused, tt.Texts())
	}

	// When 再点回搜索框
	clickMarketSearchBox(t, tt)

	// Then 下拉又出现
	if !recentDropdownShown(tt) {
		t.Fatalf("再点回搜索框应出现最近搜索：%q", tt.Texts())
	}
}

func TestMarketEscapeClosesTheSkillPaneBeforeClearingTheSearch(t *testing.T) {
	// Given 搜了 pdf，点开搜索结果里的一个 Skill，右边出了详情
	a, tt, _, _ := newRecentMarketTester(t)
	clickMarketSearchBox(t, tt)
	tt.Type("pdf")
	tt.Frame()
	tt.Key(0, ui.KeyEnter)
	tt.Advance(paneDuration)
	r, ok := tt.Find("23.5万")
	if !ok {
		t.Fatalf("找不到搜索结果：%q", tt.Texts())
	}
	tt.ClickAt(r.X, r.Y+r.H/2)
	tt.Advance(paneDuration)
	if a.mkt.selected == nil {
		t.Fatalf("点搜索结果应打开详情：%q", tt.Texts())
	}

	// When 按 Esc
	tt.Key(0, ui.KeyEscape)
	tt.Advance(paneDuration)

	// Then 只关掉详情，搜索词和结果都还在
	if a.mkt.selected != nil {
		t.Fatal("Esc 应关掉右边的详情")
	}
	if a.imp.input != "pdf" {
		t.Fatalf("Esc 先关详情，不应清空搜索：输入框 = %q", a.imp.input)
	}

	// When 再按 Esc
	tt.Key(0, ui.KeyEscape)
	tt.Frame()

	// Then 才清空搜索
	if a.imp.input != "" {
		t.Fatalf("第二次 Esc 应清空搜索：输入框 = %q", a.imp.input)
	}
}

func TestMarketSearchBoxKeepsTheFocusAfterSwitchingToAnotherAppAndBack(t *testing.T) {
	// Given 点进空搜索框，最近搜索开着
	_, tt, _, _ := newRecentMarketTester(t, "pdf")
	clickMarketSearchBox(t, tt)

	// When 切到别的应用（不是点别处），再切回来
	tt.SetFocused(false)
	tt.Advance(menuEnterAnim.Duration)
	tt.Advance(menuEnterAnim.Duration)
	tt.SetFocused(true)
	tt.Advance(menuEnterAnim.Duration)

	// Then 焦点还在搜索框：只有点了别处才放掉焦点
	acc, _ := tt.AccessByRole(ui.RoleComboBox, locales.MarketTitle())
	if !acc.Focused {
		t.Fatalf("切回来后搜索框应还有焦点：%q", tt.Texts())
	}
}

func TestMarketRecentSearchChosenPutsTheCaretAtTheEnd(t *testing.T) {
	// Given 搜过 pdf，点进空搜索框
	a, tt, _, _ := newRecentMarketTester(t, "pdf")
	clickMarketSearchBox(t, tt)

	// When ↓ 回车选中 pdf，接着打字
	tt.Key(0, ui.KeyDown)
	tt.Key(0, ui.KeyEnter)
	tt.Frame()
	tt.Type(" form")
	tt.Frame()

	// Then 字接在后面
	if a.imp.input != "pdf form" {
		t.Fatalf("输入框 = %q，应为 pdf form", a.imp.input)
	}
}

func TestMarketEscapeInTheSearchBoxClosesTheSkillPaneFirst(t *testing.T) {
	// Given 搜了 pdf，点开一个结果，又点回搜索框
	a, tt, _, _ := newRecentMarketTester(t)
	clickMarketSearchBox(t, tt)
	tt.Type("pdf")
	tt.Frame()
	tt.Key(0, ui.KeyEnter)
	tt.Advance(paneDuration)
	r, ok := tt.Find("23.5万")
	if !ok {
		t.Fatalf("找不到搜索结果：%q", tt.Texts())
	}
	tt.ClickAt(r.X, r.Y+r.H/2)
	tt.Advance(paneDuration)
	clickMarketSearchBox(t, tt)

	// When 在搜索框里按 Esc
	tt.Key(0, ui.KeyEscape)
	tt.Advance(paneDuration)

	// Then 先关详情，搜索留着
	if a.mkt.selected != nil || a.imp.input != "pdf" {
		t.Fatalf("应只关详情：selected=%v input=%q", a.mkt.selected != nil, a.imp.input)
	}

	// When 再按 Esc
	tt.Key(0, ui.KeyEscape)
	tt.Frame()

	// Then 清空搜索
	if a.imp.input != "" {
		t.Fatalf("第二次 Esc 应清空搜索：%q", a.imp.input)
	}
}
