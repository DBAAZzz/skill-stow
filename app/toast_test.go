package main

import (
	"testing"
	"time"

	"skillmanager/internal/library"
)

// shownText 是这个来源正在显示的提示条文字，没有为空。
func shownText(a *app, src toastSource) string {
	if t := a.toasts.find(src); t != nil {
		return t.text
	}
	return ""
}

// shownKind 是这个来源正在显示的提示条语义，没有时是 toastPlain。
func shownKind(a *app, src toastSource) toastKind {
	if t := a.toasts.find(src); t != nil {
		return t.kind
	}
	return toastPlain
}

func TestANewToastFromTheSameSourceReplacesTheOldOne(t *testing.T) {
	// Given 删掉了一个技能，提示条带着「撤销」
	a, tt := newTester(t)
	a.deleteNotice(toastPlain, "已把 grill-me 移到废纸篓", true)

	// When 又删了一个
	a.deleteNotice(toastPlain, "已把 kami 移到废纸篓", true)
	tt.Frame()

	// Then 只剩新的一条：旧的「撤销」不会留在下面
	if tt.HasText("已把 grill-me 移到废纸篓") || !tt.HasText("已把 kami 移到废纸篓") {
		t.Errorf("应只显示最新的删除提示：%q", tt.Texts())
	}
	if n := len(a.toasts.items); n != 1 {
		t.Errorf("同一来源应只有 1 条，实际 %d 条", n)
	}
}

func TestAtMostThreeToastsShowAndTheOldestLeavesFirst(t *testing.T) {
	// Given 已经有三条不同来源的提示条
	a, tt := newTester(t)
	a.deleteNotice(toastPlain, "已把 grill-me 移到废纸篓", true)
	a.notify(fromTidy, toastOK, "13 个 Skill 已收成一份")
	a.notify(fromUpdate, toastWarn, "已更新 1 个，1 个失败")

	// When 又来一条
	a.notify(fromLink, toastError, "无法打开链接：没有可以打开这个链接的应用")
	tt.Frame()

	// Then 最早的删除提示收起，其余三条按先后留着
	if tt.HasText("已把 grill-me 移到废纸篓") {
		t.Errorf("最早的一条应先收起：%q", tt.Texts())
	}
	for _, want := range []string{"13 个 Skill 已收成一份", "已更新 1 个，1 个失败", "无法打开链接：没有可以打开这个链接的应用"} {
		if !tt.HasText(want) {
			t.Errorf("缺少 %q：%q", want, tt.Texts())
		}
	}
}

func TestOnlyWarningsAndErrorsCanBeClosedByHand(t *testing.T) {
	// Given 一条成功提示和一条错误提示
	a, tt := newTester(t)
	a.notify(fromTidy, toastOK, "13 个 Skill 已收成一份")
	tt.Frame()

	// Then 成功的会自己收起，没有关闭按钮
	if _, ok := tt.Access("关闭提示"); ok {
		t.Error("成功提示不应有关闭按钮")
	}

	// When 出了错，点错误提示的关闭
	a.notify(fromLink, toastError, "无法打开链接：没有可以打开这个链接的应用")
	tt.Frame()
	must(t, tt.Click("关闭提示"))

	// Then 错误提示收起，成功的还在
	if tt.HasText("无法打开链接：没有可以打开这个链接的应用") || !tt.HasText("13 个 Skill 已收成一份") {
		t.Errorf("应只收起错误提示：%q", tt.Texts())
	}
}

func TestAnErrorRaisedOutsideTheUpdatesPageIsStillShown(t *testing.T) {
	// Given 在对比副本页
	a, tt := newTester(t)
	must(t, tt.Click("整理"))
	must(t, tt.Click("内容不同的副本"))
	must(t, tt.Click("对比"))

	// When 打开链接失败
	a.notify(fromLink, toastError, "无法打开链接：没有可以打开这个链接的应用")
	tt.Frame()

	// Then 这一页就看得见
	if !tt.HasText("无法打开链接：没有可以打开这个链接的应用") {
		t.Errorf("错误提示应在当前页显示：%q", tt.Texts())
	}
}

func TestTheGitHubQuotaIsANoticeOnTheUpdatesPageNotAToast(t *testing.T) {
	// Given GitHub 额度用完了
	a := newApp(library.Sample())
	updateFullFixture(a)
	a.upd.report.Reset = time.Date(2026, 11, 14, 17, 18, 0, 0, time.Local)

	// When 打开更新页
	tt := newUITester(a.view, 1280, 900)

	// Then 页内写着什么时候能再查，没有放进提示条
	if !tt.HasText("GitHub 请求额度已用完，17:18 后可以再查。") {
		t.Errorf("应有额度的页内提示：%q", tt.Texts())
	}
	if len(a.toasts.items) != 0 {
		t.Errorf("额度不应是提示条：%d 条", len(a.toasts.items))
	}

	// When 去了别的页面
	must(t, tt.Click("我的"))

	// Then 额度提示跟着更新页消失
	if tt.HasText("GitHub 请求额度已用完，17:18 后可以再查。") {
		t.Error("离开更新页后不应再显示额度提示")
	}
}
