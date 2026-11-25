package library

import (
	"fmt"
	"slices"
	"testing"
)

func TestMarketSearchesKeepTheLatestTenWithoutDuplicates(t *testing.T) {
	// Given 一个写在临时目录的技能库，已经搜过 pdf
	home := t.TempDir()
	l := Scan(home)
	l.NoteMarketSearch("pdf")

	// When 又搜了 11 段文字，其中一段是换了大小写、带空白的 PDF
	for i := range 10 {
		l.NoteMarketSearch(fmt.Sprintf("q%d", i))
	}
	l.NoteMarketSearch("  PDF ")
	l.NoteMarketSearch("   ")

	// Then 只留最近 10 条，PDF 只有一条、排在最前、用这次的写法，空白不记
	want := []string{"PDF", "q9", "q8", "q7", "q6", "q5", "q4", "q3", "q2", "q1"}
	if got := l.Settings.MarketSearches; !slices.Equal(got, want) {
		t.Fatalf("最近搜索 = %q，应为 %q", got, want)
	}

	// And 重新打开技能库，记录还在
	if got := Scan(home).Settings.MarketSearches; !slices.Equal(got, want) {
		t.Fatalf("重新扫描后最近搜索 = %q，应为 %q", got, want)
	}
}

func TestMarketSearchesCanBeRemovedOneByOneOrCleared(t *testing.T) {
	// Given 搜过 a、b、c
	home := t.TempDir()
	l := Scan(home)
	for _, q := range []string{"a", "b", "c"} {
		l.NoteMarketSearch(q)
	}

	// When 去掉 b
	l.ForgetMarketSearch("b")

	// Then 剩下 c、a，并写进了设置
	if got := Scan(home).Settings.MarketSearches; !slices.Equal(got, []string{"c", "a"}) {
		t.Fatalf("去掉 b 后 = %q", got)
	}

	// When 清除全部
	l.ClearMarketSearches()

	// Then 一条不剩
	if got := Scan(home).Settings.MarketSearches; len(got) != 0 {
		t.Fatalf("清除后 = %q", got)
	}
}
