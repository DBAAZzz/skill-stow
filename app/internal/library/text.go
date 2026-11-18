package library

import (
	"strings"
	"unicode/utf8"

	"skillmanager/locales"
)

// textError 是给人看的错误，Error() 每次按界面当前的语言现说。包级的哨兵
// 错误（ErrHasCopies 这类）在程序启动时就建好，那时还不知道界面用什么语言，
// 存死一句话就永远是源语言了。用指针，errors.Is 才比得了。
type textError struct{ say func() string }

func (e *textError) Error() string { return e.say() }

// textErr 建一个按当前语言说 say 的错误；say 传 locales 里生成的函数。
func textErr(say func() string) error { return &textError{say} }

// joinSentences 把 b 接在 a 后面：中文句号、全角标点后直接接，其他语言的
// 句子之间空一格。
func joinSentences(a, b string) string {
	if a == "" || b == "" {
		return a + b
	}
	if r, _ := utf8.DecodeLastRuneInString(a); r >= 0x3000 && r <= 0x303f || r >= 0xff00 && r <= 0xffef {
		return a + b
	}
	return a + " " + b
}

// Sentence 给一句话补上句末标点：已经有了（。.!?！？）就原样返回。中文的
// 提示不带句号、英文的带，接在界面上都要成句。
func Sentence(s string) string {
	switch r, _ := utf8.DecodeLastRuneInString(s); r {
	case utf8.RuneError, '.', '!', '?', '\u3002', '\uff01', '\uff1f': // 。！？
		return s
	}
	return locales.SentenceEnd(s)
}

// JoinAnd 列举几项：「A 和 B」「A、B 和 C」/「A and B」「A, B and C」。
func JoinAnd(items []string) string {
	switch len(items) {
	case 0:
		return ""
	case 1:
		return items[0]
	}
	last := len(items) - 1
	return locales.ListAnd(strings.Join(items[:last], locales.ListSeparator()), items[last])
}

// JoinOr 列举几项：「A 或 B」「A、B 或 C」/「A or B」「A, B or C」。
func JoinOr(items []string) string {
	switch len(items) {
	case 0:
		return ""
	case 1:
		return items[0]
	}
	last := len(items) - 1
	return locales.ListOr(strings.Join(items[:last], locales.ListSeparator()), items[last])
}
