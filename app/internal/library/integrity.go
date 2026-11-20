package library

import (
	"io"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"

	"github.com/egoist/mygo/ui/markdown/parser"

	"skillmanager/locales"
)

// 完整性：技能里说有、磁盘上却没有的东西。Agent 照样加载这种技能，
// 只在用到那一步时找不到文件，然后自由发挥——没有任何地方报错。
// 只查这类客观损坏，不评文字好坏（产品文档 §11）。

// DefectKind 是一种损坏。
type DefectKind int

const (
	MissingFile     DefectKind = iota // SKILL.md 链到的文件不存在
	MissingSkill                      // 链到旁边另一个技能（../名字/…），那个技能没装
	DanglingLink                      // 技能目录里的链接指向不存在的地方
	OpenFrontmatter                   // 开头的 --- 没有闭合，name 和简介都读不到
	NoName                            // frontmatter 里没写 name（OpenCode、Codex 会拒绝加载）
)

// Defect 是一处损坏。Ref 是 SKILL.md 里写的路径（去掉锚点）、缺的技能名，
// 或损坏链接相对技能目录的路径；frontmatter 的问题没有 Ref。
type Defect struct {
	Kind DefectKind
	Ref  string
}

// String 是给人看的一句话。
func (d Defect) String() string {
	switch d.Kind {
	case MissingSkill:
		return locales.DefectMissingSkill(d.Ref)
	case DanglingLink:
		return locales.DefectDanglingLink(d.Ref)
	case OpenFrontmatter:
		return locales.DefectOpenFrontmatter()
	case NoName:
		return locales.DefectNoName()
	}
	return locales.DefectMissingFile(d.Ref)
}

// maxWalk 限制查链接时看多少项，防止技能目录里塞了大目录时拖慢扫描。
const maxWalk = 2000

// checkSkill 查磁盘上的技能目录 dir（已解析链接的真实目录）。
func checkSkill(dir string) []Defect {
	src, ok := readSkillSource(dir)
	if !ok {
		return nil
	}
	exists := func(rel string) bool { _, err := os.Stat(filepath.Join(dir, rel)); return err == nil }
	sibling := func(name string) bool { _, err := os.Stat(filepath.Join(filepath.Dir(dir), name)); return err == nil }
	out := textDefects(src, exists, sibling)
	walkLinks(dir, func(rel, p string) {
		if _, err := os.Stat(p); err != nil {
			out = append(out, Defect{DanglingLink, rel})
		}
	})
	return out
}

// textDefects 查 SKILL.md 的内容：frontmatter，和正文里链到本地文件的链接。
// exists 报告相对技能目录的路径存在；sibling 报告技能旁边有这个名字的目录。
func textDefects(src string, exists func(rel string) bool, sibling func(name string) bool) []Defect {
	var out []Defect
	add := func(d Defect) {
		if !slices.Contains(out, d) {
			out = append(out, d)
		}
	}
	front, body, state := splitFrontmatter(src)
	switch state {
	case frontOpen:
		add(Defect{OpenFrontmatter, ""})
	case frontClosed:
		if !slices.ContainsFunc(frontmatter(front), func(f SkillField) bool { return f.Key == "name" && f.Value != "" }) {
			add(Defect{NoName, ""})
		}
	}
	for _, ref := range localRefs(body) {
		if exists(ref) {
			continue
		}
		// ../名字/SKILL.md、../名字 明说链到旁边的技能：不在时说缺的是哪个技能。
		// ../名字/别的文件 可能只是搬来的文档路径，按缺文件报
		if name, ok := siblingSkill(ref); ok && !sibling(name) {
			add(Defect{MissingSkill, name})
			continue
		}
		add(Defect{MissingFile, ref})
	}
	return out
}

// siblingSkill 报告 ref 是 ../名字 或 ../名字/SKILL.md，返回名字。
func siblingSkill(ref string) (string, bool) {
	rest, ok := strings.CutPrefix(ref, "../")
	if !ok {
		return "", false
	}
	rest = strings.TrimSuffix(rest, "/SKILL.md")
	return rest, rest != "" && rest != ".." && !strings.Contains(rest, "/")
}

// checkAll 并行查技能：几百个技能挨个读要几十毫秒，扫描会跟着变慢。
func checkAll(skills []*Skill) {
	next := make(chan *Skill)
	var wg sync.WaitGroup
	for range min(8, runtime.NumCPU()) {
		wg.Go(func() {
			for s := range next {
				s.Defects = checkSkill(s.Real)
			}
		})
	}
	for _, s := range skills {
		next <- s
	}
	close(next)
	wg.Wait()
}

// localRefs 是正文里 Markdown 链接和图片指向的本地相对路径（斜杠分隔、
// 去掉锚点和查询、解码过），按出现顺序。代码块和行内代码里的不算：多是示例。
func localRefs(body string) []string {
	doc := parser.Parse(body)
	var out []string
	for i := 1; i < doc.Len(); i++ {
		for _, in := range doc.Inlines(i) {
			if (in.Kind != parser.Link && in.Kind != parser.Image) || in.Auto {
				continue
			}
			if ref, ok := localRef(in.Dest); ok {
				out = append(out, ref)
			}
		}
	}
	return out
}

// localRef 把链接目标变成相对技能目录的路径。网址、锚点、绝对路径、~、
// 带占位符（{baseDir}、${VAR}、<…>、通配符、省略号）的都不是技能里的文件。
func localRef(dest string) (string, bool) {
	dest = strings.TrimSpace(dest)
	if i := strings.IndexAny(dest, "#?"); i >= 0 {
		dest = dest[:i] // 先去掉锚点和查询：解码出来的 # ? 是文件名的一部分
	}
	if u, err := url.PathUnescape(dest); err == nil {
		dest = u // 解码后再分辨：%7BbaseDir%7D、%2Fetc 也是占位符、绝对路径
	}
	if dest == "" || hasScheme(dest) || strings.ContainsAny(dest[:1], `/~\`) ||
		strings.ContainsAny(dest, "{}<>$*…") || strings.Contains(dest, "...") {
		return "", false
	}
	ref := filepath.ToSlash(filepath.Clean(dest))
	return ref, ref != "."
}

// hasScheme 报告 s 以 https:、mailto:、C: 这样的前缀开头。
func hasScheme(s string) bool {
	i := strings.IndexByte(s, ':')
	if i <= 0 {
		return false
	}
	for k, r := range s[:i] {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || k > 0 && (r >= '0' && r <= '9' || r == '+' || r == '-' || r == '.')) {
			return false
		}
	}
	return true
}

// walkLinks 对 dir 里的每个链接调 fn（相对路径、完整路径），跳过系统垃圾、
// .git 和依赖缓存，不进链接指向的目录。
func walkLinks(dir string, fn func(rel, p string)) {
	n := 0
	filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || p == dir {
			return nil
		}
		if n++; n > maxWalk {
			return filepath.SkipAll
		}
		if skipped(d.Name()) {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if d.Type()&fs.ModeSymlink != 0 {
			rel, _ := filepath.Rel(dir, p)
			fn(filepath.ToSlash(rel), p)
		}
		return nil
	})
}

// readSkillSource 读整份 SKILL.md（最多 maxFileSize）。
func readSkillSource(dir string) (string, bool) {
	f, err := os.Open(filepath.Join(dir, "SKILL.md"))
	if err != nil {
		return "", false
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, maxFileSize))
	return string(b), err == nil
}

// CollectBreaks 是收进技能库后会断的引用：现在指向技能目录外、还在，
// 复制到技能库后按原样解析就找不到的 SKILL.md 链接和目录里的链接。
// 收进前在确认框里列出来。
func (l *Library) CollectBreaks(s *Skill) []string {
	if !l.disk || s.Real == "" {
		return nil
	}
	src, ok := readSkillSource(s.Real)
	if !ok {
		return nil
	}
	dst := l.CollectTarget(s)
	breaks := func(from, rel string) bool {
		// 按技能根换算：../自己的名字/… 绕回来的也在技能里面，跟着一起复制
		if in, err := filepath.Rel(s.Real, filepath.Join(s.Real, from, rel)); err == nil && filepath.IsLocal(in) {
			return false
		}
		_, now := os.Stat(filepath.Join(s.Real, from, rel))
		_, after := os.Stat(filepath.Join(dst, from, rel))
		return now == nil && after != nil
	}
	var out []string
	_, body, _ := splitFrontmatter(src)
	for _, ref := range localRefs(body) {
		if breaks(".", filepath.FromSlash(ref)) && !slices.Contains(out, ref) {
			out = append(out, ref)
		}
	}
	walkLinks(s.Real, func(rel, p string) {
		target, err := os.Readlink(p)
		if err != nil || filepath.IsAbs(target) {
			return // 绝对路径的链接复制后照样指向原处
		}
		if breaks(filepath.Dir(filepath.FromSlash(rel)), target) {
			out = append(out, rel)
		}
	})
	return out
}
