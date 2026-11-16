package phonetic

import (
	"slices"
	"testing"
)

func TestPinyinGivesEveryReadingWithoutTones(t *testing.T) {
	for _, c := range []struct {
		r    rune
		want string
	}{{'测', "ce"}, {'调', "diao"}, {'调', "tiao"}, {'行', "xing"}, {'行', "hang"}, {'绿', "lv"}} {
		if got := Pinyin(c.r); !slices.Contains(got, c.want) {
			t.Errorf("Pinyin(%q) = %v，应有 %q", c.r, got, c.want)
		}
	}
	if got := Pinyin('提'); slices.Contains(got, "shi") {
		t.Errorf("「提」的 shi 很少用，不该留：%v", got)
	}
	if got := Pinyin('a'); got != nil {
		t.Errorf("字母不用转写，得到 %v", got)
	}
}

func TestOnlySimplifiedChineseReadsPinyin(t *testing.T) {
	for _, tag := range []string{"zh-Hans", "zh-CN", "zh"} {
		if ForLanguage(tag) == nil {
			t.Errorf("%s 应按拼音搜索", tag)
		}
	}
	for _, tag := range []string{"en", "ja", "zh-Hant", ""} {
		if ForLanguage(tag) != nil {
			t.Errorf("%s 不应按拼音搜索", tag)
		}
	}
}
