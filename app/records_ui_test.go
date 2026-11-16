package main

import (
	"testing"

	"skillmanager/internal/library"
)

func TestUserClearsRecordsOfDeletedSkillsFromTidy(t *testing.T) {
	// Given 示例数据里 dbskill 的技能删了，记录还在
	a, tt := newTester(t)
	must(t, tt.Click("整理"))

	// When 打开「已删除技能的安装记录」
	must(t, tt.Click("已删除技能的安装记录"))

	// Then 一行一个来源，写明技能和数量，能清除
	for _, want := range []string{"这些技能已从本地删除，但安装清单里还留着记录；不清理的话，下次更新可能会把它们重新装回来。",
		"dontbesilent2025/dbskill", "dbs、dbs-action、dbs-ai-check 等 32 个", "8 月 20 日", "清除记录", "全部清除"} {
		if !tt.HasText(want) {
			t.Errorf("缺少 %q：%q", want, tt.Texts())
		}
	}

	// When 清除第一行
	must(t, tt.Click("清除记录"))

	// Then 绿色结果行，没有撤销；只剩一行时不再有「全部清除」
	if !tt.HasText("已清理 32 条失效的安装记录。") {
		t.Errorf("结果行：%q", tt.Texts())
	}
	if tt.HasText("撤销") || tt.HasText("全部清除") {
		t.Errorf("不提供撤销；剩一行时隐藏全部清除：%q", tt.Texts())
	}
	if n := len(a.lib.Problems().Stale); n != 1 {
		t.Errorf("还剩 %d 行", n)
	}
	_ = library.StaleRecords
}

func TestClearingAllRecordsShowsProgressUntilItIsDone(t *testing.T) {
	// Given 示例数据里有两个来源的残留记录，后台停在半路
	a, tt := newTester(t)
	var work func(post func(func()))
	a.bg = func(w func(post func(func()))) { work = w }
	must(t, tt.Click("整理"))
	must(t, tt.Click("已删除技能的安装记录"))
	before := a.issueTotal(new(a.lib.Problems()))

	// When 点「全部清除」
	must(t, tt.Click("全部清除"))
	tt.Frame()

	// Then 按钮变成进度胶囊，标题下面说正在清哪个来源，别的批量操作和重新检测都点不了
	for _, want := range []string{"已清除 0 / 2", "正在清理 dontbesilent2025/dbskill…", "正在清除记录"} {
		if _, ok := tt.Access(want); !ok && !tt.HasText(want) {
			t.Errorf("清的时候应显示 %q：%q", want, tt.Texts())
		}
	}
	if tt.HasText("全部清除") {
		t.Error("清的时候不应还能点「全部清除」")
	}
	if info, ok := tt.Access("重新检测"); !ok || !info.Disabled {
		t.Error("清的时候「重新检测」应点不了")
	}

	// When 后台跑完
	work(func(fn func()) { fn() })
	tt.Frame()

	// Then 结果行写清了几条，这一类从左边消失，问题数少了两行
	if a.tidy.clearing != nil || !tt.HasText("已清理 34 条失效的安装记录。") {
		t.Errorf("清完应显示结果行：%q", tt.Texts())
	}
	if tt.HasText("已删除技能的安装记录") {
		t.Errorf("清完后这一类应消失：%q", tt.Texts())
	}
	if got := a.issueTotal(new(a.lib.Problems())); got != before-2 {
		t.Errorf("还剩 %d 个问题，应为 %d", got, before-2)
	}
}

func TestRescanFromTidyShowsItIsWorkingThenReportsTheResult(t *testing.T) {
	// Given 本机有一个没写说明的技能，后台扫描停在半路
	a, tt, h := importApp(t)
	writeTestSkill(t, h(".agents/skills/alpha"), "", "body")
	a.rescan()
	var work func(post func(func()))
	a.bg = func(w func(post func(func()))) { work = w }
	must(t, tt.Click("整理"))

	// When 点「重新检测」
	must(t, tt.Click("重新检测"))
	tt.Frame()

	// Then 按钮转圈、写着正在检测、点不了，「刚刚检测」先藏起来
	info, ok := tt.Access("正在检测…")
	if !ok || !info.Disabled {
		t.Fatalf("检测中按钮应写「正在检测…」且点不了：%q", tt.Texts())
	}
	if tt.HasText("刚刚检测") {
		t.Errorf("检测中不应还显示「刚刚检测」：%q", tt.Texts())
	}

	// When 检测途中又多了一个没写说明的技能，后台扫完
	writeTestSkill(t, h(".agents/skills/beta"), "", "body")
	work(func(fn func()) { fn() })
	tt.Frame()

	// Then 按钮复原，问题数跟上，提示条报出结果
	if a.tidy.scanning || !tt.HasText("重新检测") {
		t.Errorf("扫完按钮应复原：%q", tt.Texts())
	}
	if !tt.HasText("检测完成，共 2 个问题") {
		t.Errorf("扫完应有提示条报结果：%q", tt.Texts())
	}
}
