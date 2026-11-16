package library

import (
	"cmp"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

// 全文搜索（画板「搜索与筛选」sf3）：在技能的名称、简介和正文里找，
// 结果一行一个，按相关度排：名称命中在前，其次简介，再次正文。

// SearchField 是技能因为哪部分命中：决定排序和那一行的说明写什么。
type SearchField int

const (
	MatchName        SearchField = iota // 每个词都在名称里
	MatchDescription                    // 名称和简介合起来有每个词
	MatchBody                           // 有的词只在正文里
)

// SearchHit 是一个命中的技能。Snippet 是那一行的说明：名称命中时是简介，
// 否则是命中的那句；Marks 是 Snippet 里要加粗的字节区间。
type SearchHit struct {
	Skill   *Skill
	Field   SearchField
	File    string // 正文命中时是哪个文件，相对技能目录
	Snippet string
	Marks   [][2]int
	// Phonetic 是有词要按读音（如拼音）才对得上：排在原文对上的后面
	Phonetic bool
}

// SearchIndex 是各技能目录下 Markdown 和文本文件的内容，按行存。几百个
// 技能也只有几 MB，放在内存里逐行找就够快。
type SearchIndex struct {
	docs  map[string][]indexedFile // 技能的真实目录（示例数据用 ID）→ 文件
	files map[string]indexedFile   // 绝对路径 → 文件：下次建索引时没改过的直接用
}

type indexedFile struct {
	path  string // 相对技能目录
	size  int64
	mod   time.Time
	lines []string
	lower []string
	all   string // 整个文件的小写，先用它判断有没有，再逐行找
}

// SearchSource 是建索引要的技能信息：在界面线程上取好再交给后台，
// 后台不碰 Skill 本身。
type SearchSource struct {
	key   string
	real  string
	files []File // 示例数据的文件内容已经在内存里
}

// SearchSources 取出每个技能建索引要的信息。
func (l *Library) SearchSources() []SearchSource {
	out := make([]SearchSource, 0, len(l.Skills))
	for _, s := range l.Skills {
		src := SearchSource{key: searchKey(s), real: s.Real}
		if s.Files != nil && (s.Real == "" || !l.disk) {
			src.files = slices.Clone(s.Files)
		}
		out = append(out, src)
	}
	return out
}

func searchKey(s *Skill) string {
	if s.Real != "" {
		return s.Real
	}
	return s.ID
}

// textExts 是当正文搜索的文件：说明文档，不搜脚本和数据。
var textExts = map[string]bool{".md": true, ".mdx": true, ".markdown": true, ".txt": true}

// BuildSearchIndex 读出各技能的正文。prev 是上一次的索引，大小和修改时间
// 没变的文件不再读。可以在后台跑。
func BuildSearchIndex(sources []SearchSource, prev *SearchIndex) *SearchIndex {
	idx := &SearchIndex{docs: map[string][]indexedFile{}, files: map[string]indexedFile{}}
	for _, src := range sources {
		if _, done := idx.docs[src.key]; done {
			continue
		}
		if src.files != nil {
			var files []indexedFile
			for _, f := range src.files {
				if !f.Binary && textExts[strings.ToLower(filepath.Ext(f.Path))] {
					files = append(files, newIndexedFile(f.Path, f.Content))
				}
			}
			idx.docs[src.key] = files
			continue
		}
		idx.docs[src.key] = idx.readDir(src.real, prev)
	}
	return idx
}

func (idx *SearchIndex) readDir(dir string, prev *SearchIndex) []indexedFile {
	var files []indexedFile
	filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || p != dir && skipped(d.Name()) {
			if err == nil && d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if d.IsDir() || !textExts[strings.ToLower(filepath.Ext(p))] {
			return nil
		}
		if len(files) >= maxFiles {
			return filepath.SkipAll
		}
		fi, err := d.Info()
		if err != nil || fi.Size() > maxFileSize {
			return nil
		}
		if prev != nil {
			if f, ok := prev.files[p]; ok && f.size == fi.Size() && f.mod.Equal(fi.ModTime()) {
				idx.files[p] = f
				files = append(files, f)
				return nil
			}
		}
		b, err := os.ReadFile(p)
		if err != nil || !utf8.Valid(b) {
			return nil
		}
		rel, _ := filepath.Rel(dir, p)
		f := newIndexedFile(filepath.ToSlash(rel), string(b))
		f.size, f.mod = fi.Size(), fi.ModTime()
		idx.files[p] = f
		files = append(files, f)
		return nil
	})
	slices.SortStableFunc(files, func(a, b indexedFile) int {
		return strings.Compare(fileOrder(a.path), fileOrder(b.path))
	})
	return files
}

// newIndexedFile 按行存文件内容。SKILL.md 开头的 YAML 头不算正文：名称和
// 简介已经单独搜过。
func newIndexedFile(path, content string) indexedFile {
	lines := strings.Split(content, "\n")
	if path == "SKILL.md" {
		lines = withoutFrontmatter(lines)
	}
	f := indexedFile{path: path, lines: lines, lower: make([]string, len(lines))}
	for i, line := range lines {
		f.lower[i] = strings.ToLower(line)
	}
	f.all = strings.Join(f.lower, "\n")
	return f
}

func withoutFrontmatter(lines []string) []string {
	if len(lines) == 0 || strings.TrimSpace(lines[0]) != "---" {
		return lines
	}
	for i := 1; i < len(lines); i++ {
		if strings.TrimSpace(lines[i]) == "---" {
			return lines[i+1:]
		}
	}
	return lines
}

// SearchTerms 把输入拆成词：空格分开，不分大小写。
func SearchTerms(query string) []string { return strings.Fields(strings.ToLower(query)) }

// Search 找出名称、简介和正文合起来包含每个词的技能，按相关度排：名称
// 命中在前（和输入一样的、以它开头的更前），其次简介，再次正文；同一档
// 里按原文对上的在按读音对上的前面，再是用得多的在前。idx 为 nil 时只搜
// 名称和简介；ph 不为 nil 时名称和简介也按读音找（如拼音）。
func (l *Library) Search(idx *SearchIndex, query string, ph *Phonetic) []SearchHit {
	terms := SearchTerms(query)
	if len(terms) == 0 {
		return nil
	}
	type ranked struct {
		hit   SearchHit
		exact int // 名称就是输入为 0，以它开头为 1，其余为 2
		uses  int
	}
	var out []ranked
	for _, s := range l.Skills {
		hit, ok := l.matchSkill(s, idx, terms, ph)
		if !ok {
			continue
		}
		r := ranked{hit: hit, exact: 2, uses: l.Usage(s).Count}
		name, q := strings.ToLower(s.Name), strings.Join(terms, " ")
		switch {
		case name == q:
			r.exact = 0
		case strings.HasPrefix(name, q):
			r.exact = 1
		}
		out = append(out, r)
	}
	slices.SortStableFunc(out, func(a, b ranked) int {
		return cmp.Or(
			cmp.Compare(a.hit.Field, b.hit.Field),
			compareBool(a.hit.Phonetic, b.hit.Phonetic),
			cmp.Compare(a.exact, b.exact),
			cmp.Compare(b.uses, a.uses),
			cmp.Compare(a.hit.Skill.Source, b.hit.Skill.Source),
			strings.Compare(a.hit.Skill.Name, b.hit.Skill.Name),
		)
	})
	hits := make([]SearchHit, len(out))
	for i, r := range out {
		hits[i] = r.hit
	}
	return hits
}

// compareBool 把 false 排在 true 前面。
func compareBool(a, b bool) int {
	switch {
	case a == b:
		return 0
	case a:
		return 1
	}
	return -1
}

func (l *Library) matchSkill(s *Skill, idx *SearchIndex, terms []string, ph *Phonetic) (SearchHit, bool) {
	shown := oneLine(s.Description) // 说明里显示的简介：原文和读音都在它上面找，标出的位置才对得上
	name, desc := strings.ToLower(s.Name), strings.ToLower(shown)
	var sp spokenSkill
	if ph != nil {
		sp = l.spokenOf(s, ph)
	}
	inName, inHead := true, true
	phonName, phonHead := false, false // 有词要按读音才对得上
	var rest []string                  // 名称和简介里都没有的词，要到正文里找
	var heard [][2]int                 // 简介里按读音对上的地方
	for _, t := range terms {
		n, d := strings.Contains(name, t), strings.Contains(desc, t)
		pn, pd := false, false
		if !n {
			_, _, pn = sp.name.find(t)
		}
		if !d {
			var a, b int
			if a, b, pd = sp.desc.find(t); pd {
				heard = append(heard, [2]int{a, b})
			}
		}
		inName = inName && (n || pn)
		inHead = inHead && (n || d || pn || pd)
		phonName = phonName || !n && pn
		phonHead = phonHead || !n && !d && (pn || pd)
		if !n && !d && !pn && !pd {
			rest = append(rest, t)
		}
	}
	hit := SearchHit{Skill: s}
	switch {
	case inName:
		hit.Field, hit.Phonetic, hit.Snippet = MatchName, phonName, shown
		return hit, true
	case inHead:
		hit.Field, hit.Phonetic = MatchDescription, phonHead
		hit.Snippet, hit.Marks = snippet(shown, terms, heard)
		return hit, true
	case idx == nil:
		return hit, false
	}
	file, line, ok := idx.find(searchKey(s), rest, terms)
	if !ok {
		return hit, false
	}
	hit.Field, hit.File, hit.Phonetic = MatchBody, file, phonHead
	hit.Snippet, hit.Marks = snippet(cleanLine(line), terms, nil)
	return hit, true
}

// find 找正文里同时有 need 每个词的那个技能：每个词出现在任一文件里即可。
// 返回最能说明为什么命中的那行：含 need 的词最多的，其次含 terms 最多的。
func (idx *SearchIndex) find(key string, need, terms []string) (file, line string, ok bool) {
	files := idx.docs[key]
	for _, t := range need {
		found := false
		for _, f := range files {
			if strings.Contains(f.all, t) {
				found = true
				break
			}
		}
		if !found {
			return "", "", false
		}
	}
	best, bestScore := -1, 0
	full := 100*len(need) + len(terms) // 每个词都在的一行，不会有更好的
	var bestFile indexedFile
scan:
	for _, f := range files {
		if !slices.ContainsFunc(need, func(t string) bool { return strings.Contains(f.all, t) }) {
			continue
		}
		for i, l := range f.lower {
			score := 0
			for _, t := range need {
				if strings.Contains(l, t) {
					score += 100
				}
			}
			for _, t := range terms {
				if strings.Contains(l, t) {
					score++
				}
			}
			if score > bestScore && strings.TrimSpace(cleanLine(f.lines[i])) != "" {
				best, bestScore, bestFile = i, score, f
				if score == full {
					break scan
				}
			}
		}
	}
	if best < 0 {
		return "", "", false
	}
	return bestFile.path, bestFile.lines[best], true
}

// oneLine 把多行的简介并成一行：弹窗里只有一行，而且要在同一段文字上
// 匹配和标出命中的词。
func oneLine(s string) string { return strings.Join(strings.Fields(s), " ") }

// cleanLine 去掉 Markdown 行首的标记：标题、列表、引用、表格竖线。
func cleanLine(s string) string {
	s = strings.TrimSpace(s)
	s = strings.TrimLeft(s, "#>*-+| \t")
	if i := strings.IndexFunc(s, func(r rune) bool { return r < '0' || r > '9' }); i > 0 && strings.HasPrefix(s[i:], ". ") {
		s = s[i+2:]
	}
	return strings.TrimSpace(s)
}

// snippetRunes 是说明最多留多少字：一行放不下的由界面截断，这里只把
// 命中的词挪进前面看得见的地方。
const snippetRunes = 90

// snippet 标出 s 里要加粗的词：原文对上的 terms，加上按读音对上的 heard
// （s 里的字节区间）；s 太长时裁到第一个命中的词附近。
func snippet(s string, terms []string, heard [][2]int) (string, [][2]int) {
	marks := slices.Clone(heard)
	if lower, ok := lowerSameWidth(s); ok { // 有字小写后字节长度变了，标不准就不标
		for _, t := range terms {
			for from := 0; ; {
				i := strings.Index(lower[from:], t)
				if i < 0 {
					break
				}
				marks = append(marks, [2]int{from + i, from + i + len(t)})
				from += i + len(t)
			}
		}
	}
	marks = mergeMarks(marks)
	if len(marks) == 0 || utf8.RuneCountInString(s) <= snippetRunes {
		return s, marks
	}
	lead := utf8.RuneCountInString(s[:marks[0][0]])
	if lead <= 24 {
		return s, marks
	}
	cut := byteOffset(s, lead-16)
	const ellipsis = "…"
	shift := len(ellipsis) - cut
	for i := range marks {
		marks[i][0], marks[i][1] = marks[i][0]+shift, marks[i][1]+shift
	}
	return ellipsis + s[cut:], marks
}

// lowerSameWidth 是 s 的小写，每个字小写前后字节数都一样时 ok：这样小写
// 里找到的位置就是原文的位置。只比总长度不够，İ 变短、Ⱥ 变长会正好抵消。
func lowerSameWidth(s string) (string, bool) {
	for _, r := range s {
		if utf8.RuneLen(unicode.ToLower(r)) != utf8.RuneLen(r) {
			return "", false
		}
	}
	return strings.ToLower(s), true
}

func byteOffset(s string, runes int) int {
	for i := range s {
		if runes == 0 {
			return i
		}
		runes--
	}
	return len(s)
}

// mergeMarks 排好区间并合并重叠的。
func mergeMarks(marks [][2]int) [][2]int {
	slices.SortFunc(marks, func(a, b [2]int) int { return cmp.Compare(a[0], b[0]) })
	var out [][2]int
	for _, m := range marks {
		if n := len(out); n > 0 && m[0] <= out[n-1][1] {
			out[n-1][1] = max(out[n-1][1], m[1])
			continue
		}
		out = append(out, m)
	}
	return out
}
