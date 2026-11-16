package library

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"skillmanager/internal/phonetic"
)

func hitNames(hits []SearchHit) []string {
	var out []string
	for _, h := range hits {
		out = append(out, h.Skill.Name)
	}
	return out
}

func TestSearchRanksNameMatchesBeforeDescriptionBeforeBody(t *testing.T) {
	l := &Library{Skills: []*Skill{
		{ID: "body", Name: "code-ablation", Description: "删掉多余的抽象"},
		{ID: "desc", Name: "durable-objects", Description: "Covers testing with Vitest"},
		{ID: "name", Name: "test-first", Description: "测试先行"},
	}}
	idx := BuildSearchIndex([]SearchSource{
		{key: "body", files: []File{{Path: "SKILL.md", Content: "先跑一遍 go test 记下基线"}}},
		{key: "desc", files: []File{}},
		{key: "name", files: []File{}},
	}, nil)
	hits := l.Search(idx, "test", nil)
	if got := hitNames(hits); len(got) != 3 || got[0] != "test-first" || got[1] != "durable-objects" || got[2] != "code-ablation" {
		t.Fatalf("顺序应是名称、简介、正文，得到 %v", got)
	}
	body := hits[2]
	if body.File != "SKILL.md" || body.Snippet != "先跑一遍 go test 记下基线" {
		t.Errorf("正文命中应给出文件和那一行：%+v", body)
	}
	if len(body.Marks) != 1 || body.Snippet[body.Marks[0][0]:body.Marks[0][1]] != "test" {
		t.Errorf("应标出命中的词：%v", body.Marks)
	}
}

func TestSearchNeedsEveryWordAndIgnoresTheFrontmatter(t *testing.T) {
	l := &Library{Skills: []*Skill{{ID: "a", Name: "spark", Description: "brainstorm an idea"}}}
	idx := BuildSearchIndex([]SearchSource{{key: "a", files: []File{{Path: "SKILL.md",
		Content: "---\nname: spark\nsecret: frontmatter-only\n---\n\nwrite a spec, then stop"}}}}, nil)
	if hits := l.Search(idx, "spark spec", nil); len(hits) != 1 || hits[0].Field != MatchBody {
		t.Errorf("名称和正文各有一个词时应算正文命中：%+v", hits)
	}
	if hits := l.Search(idx, "spark kubernetes", nil); len(hits) != 0 {
		t.Errorf("有一个词哪里都没有就不算命中：%v", hitNames(hits))
	}
	if hits := l.Search(idx, "frontmatter-only", nil); len(hits) != 0 {
		t.Errorf("SKILL.md 开头的 YAML 头不算正文：%v", hitNames(hits))
	}
}

func TestSearchIndexReadsTextFilesAndReusesUnchangedOnes(t *testing.T) {
	dir := t.TempDir()
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(os.MkdirAll(filepath.Join(dir, "references"), 0o755))
	must(os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte("# demo\n"), 0o644))
	must(os.WriteFile(filepath.Join(dir, "references", "guide.md"), []byte("use backtest data\n"), 0o644))
	must(os.WriteFile(filepath.Join(dir, "run.sh"), []byte("echo backtest\n"), 0o644))
	l := &Library{Skills: []*Skill{{ID: "demo", Name: "demo", Real: dir}}, disk: true}

	idx := BuildSearchIndex(l.SearchSources(), nil)
	hits := l.Search(idx, "backtest", nil)
	if len(hits) != 1 || hits[0].File != "references/guide.md" {
		t.Fatalf("应在 Markdown 里找到，不搜脚本：%+v", hits)
	}
	again := BuildSearchIndex(l.SearchSources(), idx)
	p := filepath.Join(dir, "references", "guide.md")
	if &again.files[p].lines[0] != &idx.files[p].lines[0] {
		t.Error("没改过的文件应沿用上次读的内容")
	}
}

var pinyinSearch = &Phonetic{Lang: "zh-Hans", Read: phonetic.Pinyin}

func TestPinyinFindsChineseNamesAndDescriptions(t *testing.T) {
	l := &Library{Skills: []*Skill{
		{ID: "a", Name: "测试先行", Description: "先写验收场景"},
		{ID: "b", Name: "debugger", Description: "调试 Go 程序"},
		{ID: "c", Name: "invoker", Description: "调用外部接口"},
	}}
	for _, c := range []struct{ query, want string }{
		{"ceshi", "测试先行"},       // 全拼
		{"cs", "测试先行"},          // 声母
		{"ces", "测试先行"},         // 最后一个字只打了一半
		{"csxx", "测试先行"},        // 整个名称的声母
		{"tiaoshi", "debugger"}, // 多音字「调」读 tiao
		{"diaoyong", "invoker"}, // 也读 diao
		{"yanshou", "测试先行"},     // 简介里的
	} {
		hits := l.Search(nil, c.query, pinyinSearch)
		if len(hits) == 0 || hits[0].Skill.Name != c.want {
			t.Errorf("%q 应找到 %s，得到 %v", c.query, c.want, hitNames(hits))
		}
	}
	if hits := l.Search(nil, "ceshi", nil); len(hits) != 0 {
		t.Errorf("没有读法（如英文界面）时不按拼音找：%v", hitNames(hits))
	}
	if hits := l.Search(nil, "c", pinyinSearch); len(hits) != 0 {
		t.Errorf("一个字母不按拼音找，免得对上一大片：%v", hitNames(hits))
	}
}

func TestPinyinMatchesMarkTheChineseAndRankAfterTextMatches(t *testing.T) {
	l := &Library{Skills: []*Skill{
		{ID: "a", Name: "builder", Description: "构建前端项目"},
		{ID: "b", Name: "go-review", Description: "审查 Go 代码"},
	}}
	hits := l.Search(nil, "go", pinyinSearch)
	if got := hitNames(hits); len(got) != 2 || got[0] != "go-review" {
		t.Fatalf("原文对上的应排在拼音对上的前面：%v", got)
	}
	hits = l.Search(nil, "goujian", pinyinSearch)
	if len(hits) != 1 || !hits[0].Phonetic {
		t.Fatalf("hits = %+v", hits)
	}
	h := hits[0]
	if len(h.Marks) != 1 || h.Snippet[h.Marks[0][0]:h.Marks[0][1]] != "构建" {
		t.Errorf("应加粗拼音对上的汉字：%q %v", h.Snippet, h.Marks)
	}
}

func TestPinyinStaysFastOnLongRunsOfPolyphones(t *testing.T) {
	// 「並」读 bing 和 ban，声母相同：不记下失败的位置时，n=24 就要好几秒
	sp := newSpoken(strings.Repeat("並", 40), phonetic.Pinyin)
	start := time.Now()
	if _, _, ok := sp.find(strings.Repeat("b", 40) + "x"); ok {
		t.Fatal("结尾的 x 对不上，不应命中")
	}
	if d := time.Since(start); d > 200*time.Millisecond {
		t.Errorf("一长串同声母的多音字用了 %v，回溯又成指数级了", d)
	}
}

func TestMarksStayOnCharacterBoundariesWhenLowercasingChangesWidths(t *testing.T) {
	// İ 小写后变短、Ⱥ 变长，总字节数正好相等
	s, marks := snippet("İfooȺ", []string{"foo"}, nil)
	for _, m := range marks {
		if !utf8.ValidString(s[m[0]:m[1]]) {
			t.Errorf("加粗的位置切坏了字：%q %v", s, marks)
		}
	}
}

func TestMixedPinyinAndBodyMatchesRankAsPinyinAndMultilineDescriptionsMatchWhatIsShown(t *testing.T) {
	l := &Library{Skills: []*Skill{{ID: "a", Name: "runner", Description: "first line\nsecond line has secret 测试"}}}
	idx := BuildSearchIndex([]SearchSource{{key: "a", files: []File{{Path: "SKILL.md", Content: "run vitest"}}}}, nil)
	if hits := l.Search(idx, "ceshi vitest", pinyinSearch); len(hits) != 1 || hits[0].Field != MatchBody || !hits[0].Phonetic {
		t.Errorf("一个词靠拼音、一个词在正文，也要算拼音命中往后排：%+v", hits)
	}
	hits := l.Search(nil, "secret", nil)
	if len(hits) != 1 || !strings.Contains(hits[0].Snippet, "secret") {
		t.Errorf("简介第二行命中时，说明里要看得见那个词：%+v", hits)
	}
	if hits := l.Search(nil, "ceshi", pinyinSearch); len(hits) != 1 {
		t.Errorf("简介第二行也要能按拼音找到：%v", hitNames(hits))
	}
}
