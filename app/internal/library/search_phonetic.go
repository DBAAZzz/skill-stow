package library

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

// 按读音搜索：界面是简体中文时，输入 ceshi、cs 或 ces 都能找到「测试」。
// 只用在名称和简介上：正文有几十万行，逐字转写太占内存，而且要找正文的
// 人多半会直接打中文。

// Phonetic 是一种读法：Read 给出一个字的各种读法（小写拉丁字母，如拼音
// 的 "ce"），Lang 是它的语言，换了语言就重新转写。
type Phonetic struct {
	Lang string
	Read func(r rune) []string
}

// spoken 是一段文字逐字的读法，用来把输入的字母对回原文。
type spoken struct {
	at    []int      // 每个字在原文里的字节位置，最后多一个原文的长度
	lower []rune     // 每个字的小写
	reads [][]string // 每个字的读法，不用转写的为 nil
}

type spokenSkill struct{ name, desc *spoken }

// spokenOf 是技能名称和简介的读法，第一次搜到它时转写，之后沿用。
func (l *Library) spokenOf(s *Skill, ph *Phonetic) spokenSkill {
	if l.spokenLang != ph.Lang || l.spokenCache == nil {
		l.spokenLang, l.spokenCache = ph.Lang, map[*Skill]spokenSkill{}
	}
	sp, ok := l.spokenCache[s]
	if !ok {
		sp = spokenSkill{newSpoken(s.Name, ph.Read), newSpoken(oneLine(s.Description), ph.Read)}
		l.spokenCache[s] = sp
	}
	return sp
}

// newSpoken 转写 s；没有要转写的字时为 nil，按原文找就够了。
func newSpoken(s string, read func(rune) []string) *spoken {
	sp := &spoken{}
	heard := false
	for i, r := range s {
		reads := read(r)
		heard = heard || reads != nil
		sp.at = append(sp.at, i)
		sp.lower = append(sp.lower, unicode.ToLower(r))
		sp.reads = append(sp.reads, reads)
	}
	if !heard {
		return nil
	}
	sp.at = append(sp.at, len(s))
	return sp
}

// spellable 报告 t 能按读音找：至少两个字母、只有字母和数字，免得打一个
// 字母就对上一大片。
func spellable(t string) bool {
	if len(t) < 2 {
		return false
	}
	letters := false
	for _, r := range t {
		switch {
		case r >= 'a' && r <= 'z':
			letters = true
		case r >= '0' && r <= '9':
		default:
			return false
		}
	}
	return letters
}

// find 找输入的词 t 按读音对上的地方，返回原文的字节区间。从一个要转写
// 的字开始，每个字可以打全拼或只打声母，最后一个字可以只打前几个字母；
// 中间夹着的字母、数字照原样对。
func (sp *spoken) find(t string) (start, end int, ok bool) {
	if sp == nil || !spellable(t) {
		return 0, 0, false
	}
	m := matcher{sp: sp, t: t, failed: make([]bool, (len(sp.reads)+1)*(len(t)+1))}
	for i := range sp.reads {
		if sp.reads[i] == nil {
			continue
		}
		if j, ok := m.from(i, 0); ok {
			return sp.at[i], sp.at[j], true
		}
	}
	return 0, 0, false
}

// matcher 对一个词。多音字的几种读法、全拼和声母都要试，同一个位置会被
// 走到很多次；failed 记下从第 i 个字、词的第 k 个字节起对不上的，不再
// 重试，免得一长串同声母的多音字把搜索拖成指数级。
type matcher struct {
	sp     *spoken
	t      string
	failed []bool // [i*(len(t)+1)+k]
}

// from 从第 i 个字、词的第 k 个字节起对，返回对完时到了第几个字。
func (m *matcher) from(i, k int) (int, bool) {
	t, sp := m.t[k:], m.sp
	if t == "" {
		return i, true
	}
	if i >= len(sp.reads) {
		return 0, false
	}
	key := i*(len(m.t)+1) + k
	if m.failed[key] {
		return 0, false
	}
	if reads := sp.reads[i]; reads == nil {
		if r, size := utf8.DecodeRuneInString(t); sp.lower[i] == r {
			if j, ok := m.from(i+1, k+size); ok {
				return j, true
			}
		}
	} else {
		for _, p := range reads {
			if strings.HasPrefix(p, t) { // 最后一个字只打了前几个字母
				return i + 1, true
			}
			if strings.HasPrefix(t, p) {
				if j, ok := m.from(i+1, k+len(p)); ok {
					return j, true
				}
			}
			for _, ini := range initials(p) {
				if strings.HasPrefix(t, ini) {
					if j, ok := m.from(i+1, k+len(ini)); ok {
						return j, true
					}
				}
			}
		}
	}
	m.failed[key] = true
	return 0, false
}

// initials 是一种读法能简写成的声母：首字母，zh、ch、sh 开头的也可以打
// 两个字母。
func initials(p string) []string {
	if len(p) < 2 {
		return nil
	}
	if len(p) > 2 && p[1] == 'h' && strings.ContainsRune("zcs", rune(p[0])) {
		return []string{p[:1], p[:2]}
	}
	return []string{p[:1]}
}
