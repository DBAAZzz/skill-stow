//go:build darwin

package main

import (
	"slices"
	"testing"

	"skillmanager/internal/library"
	"skillmanager/internal/security"
	"skillmanager/locales"
)

// 每条检查结果都有自己的摘要和说明，不落到「未识别」。
func TestEverySecurityMessageHasItsOwnText(t *testing.T) {
	for _, m := range security.Messages() {
		if secSummary(m) == locales.SecFindingUnknown() || secDetail(m) == locales.SecFindingUnknownDetail() {
			t.Errorf("%s 没有对应的文案", m)
		}
	}
}

// 示例数据里 stock-docs 会把私钥发出去、wechat-pay 会下载脚本直接执行：
// 卡片上标出来，详情栏写明，点进去是报告；标为已看过后卡片和整理页都不再算它。
func TestSecurityFindingsShowOnCardInspectorAndReport(t *testing.T) {
	a, tt := newTester(t)
	if !tt.HasText("高风险") || !tt.HasText("安全警告") {
		t.Fatalf("卡片上应标出高风险和安全警告：%q", tt.Texts())
	}
	s := a.lib.Get("stock-docs")
	a.selected = s
	tt.Frame()
	tt.Advance(paneDuration) // 详情栏滑进来
	tt.Frame()
	for _, want := range []string{"本地检查", "高风险 1", "会读取凭据并发往外部地址"} {
		if !tt.HasText(want) {
			t.Errorf("详情栏缺少 %q：%q", want, tt.Texts())
		}
	}
	if err := tt.Click("安全检查"); err != nil {
		t.Fatal(err)
	}
	if a.reading != s || a.file != secFile {
		t.Fatalf("点本地检查应打开阅读视图的安全检查：reading=%v file=%d", a.reading, a.file)
	}
	for _, want := range []string{"安全检查", "会读取凭据并发往外部地址", "scripts/sync.sh", "已看过", "高风险 1 项"} {
		if !tt.HasText(want) {
			t.Errorf("报告缺少 %q：%q", want, tt.Texts())
		}
	}

	// 证据跳到文件那一行，以原文打开、标底色
	if err := tt.Click("在文件里查看"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if a.file < 0 || s.Files[a.file].Path != "scripts/sync.sh" || !a.readerRaw {
		t.Fatalf("应打开 scripts/sync.sh 原文：file=%d raw=%v", a.file, a.readerRaw)
	}
	if a.readerEditor == nil || len(a.readerEditor.LineBackgrounds) != 1 {
		t.Fatalf("证据所在行应标底色：%+v", a.readerEditor)
	}

	// 后退回到报告
	a.file = secFile
	tt.Frame()
	if err := tt.Click("已看过"); err != nil {
		t.Fatal(err)
	}
	if v := a.secView(s); v.Pending() != 0 {
		t.Fatalf("标为已看过后不应再有待查看的：%d", v.Pending())
	}
	if tt.HasText("高风险 1 项") {
		t.Error("已看过后顶部状态栏不再提示")
	}
	if n := a.secIssueCount(library.SecurityHighRisk); n != 0 {
		t.Errorf("整理页高风险应为 0，实际 %d", n)
	}
	if rows := a.secRows(library.SecurityHighRisk, true); len(rows) != 1 {
		t.Errorf("已看过的应折叠在整理页里：%d", len(rows))
	}
}

// 整理页把高风险、安全警告列在最前，点「查看」进报告。
func TestTidyListsSecurityFirst(t *testing.T) {
	a, tt := newTester(t)
	a.scope = scopeTidy
	tt.Frame()
	if kinds := a.tidyKinds(); kinds[0] != library.SecurityHighRisk || kinds[1] != library.SecurityWarning {
		t.Fatalf("安全检查的类型应排在最前：%v", kinds)
	}
	if a.tidy.kind = library.SecurityHighRisk; true {
		tt.Frame()
	}
	for _, want := range []string{"stock-docs", "会读取凭据并发往外部地址", "scripts/sync.sh:3"} {
		if !tt.HasText(want) {
			t.Errorf("整理页高风险缺少 %q：%q", want, tt.Texts())
		}
	}
	if err := tt.Click("查看"); err != nil {
		t.Fatal(err)
	}
	if a.reading == nil || a.reading.Name != "stock-docs" || a.file != secFile {
		t.Fatalf("查看应打开 stock-docs 的安全检查：%v %d", a.reading, a.file)
	}
}

// 页面历史记得看的是安全检查报告：后退再前进回到报告，不落到第一个文件。
func TestHistoryRemembersSecurityReport(t *testing.T) {
	a, tt := newTester(t)
	s := a.lib.Get("wechat-pay")
	a.openSecurityReport(s)
	tt.Frame()
	a.back()
	tt.Frame()
	if a.reading != nil {
		t.Fatalf("后退应离开阅读视图")
	}
	a.goHistory(1)
	tt.Frame()
	if a.reading != s || a.file != secFile {
		t.Fatalf("前进应回到安全检查：file=%d", a.file)
	}
}

// 文件列表里有风险的文件标出来，安全检查在最上面。
func TestReaderFileListMarksRiskyFiles(t *testing.T) {
	a, tt := newTester(t)
	s := a.lib.Get("wechat-pay")
	a.open(s)
	tt.Frame()
	if _, ok := a.secFileMark(s, "scripts/setup.sh"); !ok {
		t.Error("scripts/setup.sh 应标出风险")
	}
	if _, ok := a.secFileMark(s, "SKILL.md"); ok && !slices.ContainsFunc(a.secView(s).Report.Findings, func(f security.Finding) bool {
		return f.Notice.Risky() && len(f.Evidence) > 0 && f.Evidence[0].Path == "SKILL.md"
	}) {
		t.Error("没有风险的文件不标")
	}
	if !tt.HasText("安全检查") {
		t.Errorf("文件列表应有安全检查：%q", tt.Texts())
	}
}
