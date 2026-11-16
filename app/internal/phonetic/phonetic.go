// Package phonetic 给出文字的拉丁字母读法，让搜索能按读音找：界面是
// 简体中文时，输入 ceshi 或 cs 能找到「测试」。以后支持别的语言（如日文
// 的罗马字）时，在 ForLanguage 里加上它的读法。
package phonetic

import (
	"slices"
	"strings"
	"sync"

	"github.com/mozillazg/go-pinyin"
)

// Reader 返回一个字的各种读法，小写拉丁字母、不带声调；不需要转写的字
// （字母、数字、标点）返回 nil。
type Reader func(r rune) []string

// ForLanguage 返回界面语言 tag（BCP 47，如 "zh-Hans"）该用的读法；
// 没有时为 nil，搜索只按原文匹配。
func ForLanguage(tag string) Reader {
	switch t := strings.ToLower(tag); {
	case t == "zh", t == "zh-hans", strings.HasPrefix(t, "zh-hans-"), t == "zh-cn", t == "zh-sg":
		return Pinyin
	}
	return nil
}

var pinyinArgs = func() pinyin.Args {
	a := pinyin.NewArgs()
	a.Style = pinyin.Normal // 不带声调：输入法里打的就是这样
	a.Heteronym = true      // 多音字要几种读法：「调用」diaoyong、「调试」tiaoshi（只留常用的，见 maxReadings）
	return a
}()

var pinyinCache sync.Map // rune → []string

// maxReadings 是一个字最多留几种读法。字典里常用的读法排在前面，后面的
// 很少用（「提」的 shi、「参」的 shen），留着只会让 tiaoshi 对上「条提」。
const maxReadings = 2

// Pinyin 是汉字的拼音，多音字给出最常用的两种读法；ü 写作 v，和输入法一样。
func Pinyin(r rune) []string {
	if r < 0x3400 { // 汉字从 CJK 扩展 A 开始，前面的都不用转写
		return nil
	}
	if v, ok := pinyinCache.Load(r); ok {
		return v.([]string)
	}
	var out []string
	for _, p := range pinyin.SinglePinyin(r, pinyinArgs) {
		p = strings.ReplaceAll(p, "ü", "v")
		if p != "" && !slices.Contains(out, p) && len(out) < maxReadings {
			out = append(out, p)
		}
	}
	pinyinCache.Store(r, out)
	return out
}
