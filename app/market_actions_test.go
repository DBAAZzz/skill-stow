package main

import (
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"skillmanager/locales"
)

// newMarketActionsTester 是示例数据的 Skill 市场页：记下复制了什么、打开了
// 什么网址；「已复制」不会自己变回去，好断言。
func newMarketActionsTester(t *testing.T) (*app, *ui.Tester, *[]string, *string) {
	t.Helper()
	a, tt := newTester(t)
	var copied []string
	var opened string
	a.copyText = func(s string) error { copied = append(copied, s); return nil }
	a.openURL = func(u string) { opened = u }
	a.delay = func(time.Duration, func()) {}
	a.openImport()
	tt.Frame()
	return a, tt, &copied, &opened
}

func lastCopied(copied []string) string {
	if len(copied) == 0 {
		return ""
	}
	return copied[len(copied)-1]
}

func toastSays(a *app, text string) bool {
	for _, t := range a.toasts.items {
		if t.text == text {
			return true
		}
	}
	return false
}

func TestMarketDetailPaneCopiesTheRepositoryLink(t *testing.T) {
	// Given 点开 find-skills（vercel-labs/skills 仓库里的）
	_, tt, copied, _ := newMarketActionsTester(t)
	must(t, tt.Click("find-skills"))
	tt.Advance(paneDuration)

	// When 点详情里的「复制链接」
	must(t, tt.Click(locales.MarketCopyLink()))
	tt.Frame()

	// Then 复制的是 GitHub 仓库地址，按钮变成「已复制」
	if got := lastCopied(*copied); got != "https://github.com/vercel-labs/skills" {
		t.Fatalf("复制了 %q", got)
	}
	if !tt.HasText(locales.CommonCopied()) || tt.HasText(locales.MarketCopyLink()) {
		t.Fatalf("按钮应变成「已复制」：%q", tt.Texts())
	}
}

func TestMarketSelectedSkillLinkIsCopiedWithCommandC(t *testing.T) {
	// Given 点一下选中 find-skills
	a, tt, copied, _ := newMarketActionsTester(t)
	must(t, tt.Click("find-skills"))
	tt.Advance(paneDuration)

	// When 按 ⌘C
	tt.Key(ui.Cmd, ui.KeyC)
	tt.Frame()

	// Then 复制仓库地址，并用提示条说复制了什么
	link := "https://github.com/vercel-labs/skills"
	if got := lastCopied(*copied); got != link {
		t.Fatalf("⌘C 复制了 %q", got)
	}
	if !toastSays(a, locales.MarketLinkCopied(link)) {
		t.Fatalf("⌘C 后应提示已复制：%+v", a.toasts.items)
	}
}

func TestMarketCommandCInTheSearchBoxCopiesTheTypedText(t *testing.T) {
	// Given 选中了 find-skills，又点回搜索框打了字
	_, tt, copied, _ := newMarketActionsTester(t)
	must(t, tt.Click("find-skills"))
	tt.Advance(paneDuration)
	clickMarketSearchBox(t, tt)
	tt.Type("find")
	tt.Frame()

	// When 在搜索框里按 ⌘C
	tt.Key(ui.Cmd, ui.KeyC)
	tt.Frame()

	// Then 不抢搜索框的复制
	for _, s := range *copied {
		if s == "https://github.com/vercel-labs/skills" {
			t.Fatalf("在搜索框里按 ⌘C 不应复制选中 Skill 的地址：%q", *copied)
		}
	}
}

func TestMarketRowContextMenuOpensAndCopies(t *testing.T) {
	// Given 选中了 hyperframes-cli，又右键 find-skills
	a, tt, copied, opened := newMarketActionsTester(t)
	must(t, tt.Click("hyperframes-cli"))
	tt.Advance(paneDuration)
	must(t, tt.RightClick("find-skills"))
	tt.Advance(menuEnterAnim.Duration)

	// Then 右键的那一行被选中，菜单上的 ⌘C 说的就是它
	if a.mkt.selected == nil || a.mkt.selected.Name != "find-skills" {
		t.Fatalf("右键应选中 find-skills，实际 %+v", a.mkt.selected)
	}

	// Then 菜单里有添加、打开和复制
	for _, want := range []string{locales.MarketViewAndAdd(), locales.MarketOpenOnGitHub(), locales.MarketOpenOnWeb(), locales.MarketCopyLink()} {
		if !tt.HasText(want) {
			t.Errorf("右键菜单缺 %q：%q", want, tt.Texts())
		}
	}

	// When 点「复制链接」
	must(t, tt.Click(locales.MarketCopyLink()))
	tt.Advance(menuEnterAnim.Duration)

	// Then 复制仓库地址
	if got := lastCopied(*copied); got != "https://github.com/vercel-labs/skills" {
		t.Fatalf("右键复制了 %q", got)
	}

	// When 再右键，点「在 GitHub 中打开」
	must(t, tt.RightClick("find-skills"))
	tt.Advance(menuEnterAnim.Duration)
	must(t, tt.Click(locales.MarketOpenOnGitHub()))
	tt.Frame()

	// Then 打开仓库
	if *opened != "https://github.com/vercel-labs/skills" {
		t.Fatalf("在 GitHub 中打开 = %q", *opened)
	}
}

func TestMarketClawHubSkillCopiesItsPageLink(t *testing.T) {
	// Given 切到 ClawHub 的安全分类，右键 skill-vetter（没有 GitHub 仓库）
	a, tt, copied, _ := newMarketActionsTester(t)
	must(t, tt.Click("ClawHub"))
	tt.Advance(segmentedSpring.Spring.Duration)
	a.selectClawHubCategory("security")
	tt.Frame()
	must(t, tt.RightClick("skill-vetter"))
	tt.Advance(menuEnterAnim.Duration)

	// Then 没有「在 GitHub 中打开」
	if tt.HasText(locales.MarketOpenOnGitHub()) {
		t.Fatalf("ClawHub 的 Skill 不应有「在 GitHub 中打开」：%q", tt.Texts())
	}

	// When 点「复制链接」
	must(t, tt.Click(locales.MarketCopyLink()))
	tt.Frame()

	// Then 复制它在 ClawHub 的网页
	var want string
	for _, it := range a.marketVisibleList(marketModeBrowse) {
		if it.Name == "skill-vetter" {
			want = it.WebURL
		}
	}
	if want == "" || lastCopied(*copied) != want {
		t.Fatalf("复制了 %q，应为 %q", lastCopied(*copied), want)
	}
}

func TestMarketRowContextMenuCopiesWithCommandC(t *testing.T) {
	// Given 右键 find-skills，菜单开着
	_, tt, copied, _ := newMarketActionsTester(t)
	must(t, tt.RightClick("find-skills"))
	tt.Advance(menuEnterAnim.Duration)

	// When 按菜单上写着的 ⌘C
	tt.Key(ui.Cmd, ui.KeyC)
	tt.Advance(menuEnterAnim.Duration)

	// Then 复制仓库地址
	if got := lastCopied(*copied); got != "https://github.com/vercel-labs/skills" {
		t.Fatalf("菜单开着按 ⌘C 复制了 %q", got)
	}
}

func TestMarketCopiedLinkIsNotOfferedBackFromTheClipboard(t *testing.T) {
	// Given 复制了 find-skills 的地址
	a, tt, _, _ := newMarketActionsTester(t)
	must(t, tt.Click("find-skills"))
	tt.Advance(paneDuration)
	must(t, tt.Click(locales.MarketCopyLink()))
	tt.Frame()

	// When 切回应用时检查剪贴板，剪贴板里正是这个地址
	a.offerClipboard("https://github.com/vercel-labs/skills")

	// Then 不提示把它读回来
	if a.imp.clip != "" {
		t.Fatalf("自己复制出去的地址不应再提示读取：%q", a.imp.clip)
	}
}
