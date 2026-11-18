package security

import (
	"bytes"
	"path"
	"regexp"
	"strings"
	"unicode/utf16"
	"unicode/utf8"
)

// decode 把文件字节转成文本。支持 UTF-8（可带 BOM）和带 BOM 的 UTF-16；
// 含 NUL 或不是合法 UTF-8 的按二进制处理。返回的文本与原文按行一一对应。
func decode(b []byte) (string, Gap) {
	switch {
	case bytes.HasPrefix(b, []byte{0xEF, 0xBB, 0xBF}):
		b = b[3:]
	case bytes.HasPrefix(b, []byte{0xFF, 0xFE}), bytes.HasPrefix(b, []byte{0xFE, 0xFF}):
		if len(b)%2 != 0 {
			return "", GapEncoding
		}
		le := b[0] == 0xFF
		u := make([]uint16, 0, len(b)/2-1)
		for i := 2; i+1 < len(b); i += 2 {
			if le {
				u = append(u, uint16(b[i])|uint16(b[i+1])<<8)
			} else {
				u = append(u, uint16(b[i])<<8|uint16(b[i+1]))
			}
		}
		return string(utf16.Decode(u)), ""
	}
	if bytes.IndexByte(b, 0) >= 0 {
		return "", GapBinary
	}
	if !utf8.Valid(b) {
		return "", GapEncoding
	}
	return string(b), ""
}

// kind 是按路径和内容判断的文件种类。
type kind int

const (
	kindOther kind = iota
	kindMarkdown
	kindShell
	kindScript // Python、JavaScript 等，只做明确模式检查
	kindConfig
)

var shebang = regexp.MustCompile(`^#!\s*(?:/usr/bin/env\s+(?:-S\s+)?)?\S*?([a-z]+[0-9.]*)\b`)

func fileKind(p string, text string, mode uint32) kind {
	ext := strings.ToLower(path.Ext(p))
	switch ext {
	case ".md", ".markdown", ".mdx", ".txt", ".rst":
		return kindMarkdown
	case ".sh", ".bash", ".zsh", ".ksh", ".command":
		return kindShell
	case ".py", ".js", ".mjs", ".cjs", ".ts", ".rb", ".pl", ".php", ".ps1", ".lua", ".go", ".swift", ".applescript", ".scpt":
		return kindScript
	case ".json", ".yaml", ".yml", ".toml", ".ini", ".cfg", ".conf", ".env", ".plist", ".xml":
		return kindConfig
	}
	if m := shebang.FindStringSubmatch(firstLine(text)); m != nil {
		switch m[1] {
		case "sh", "bash", "zsh", "dash", "ksh":
			return kindShell
		}
		return kindScript
	}
	if base := path.Base(p); base == ".env" || strings.HasPrefix(base, ".env.") {
		return kindConfig
	}
	if mode&0o111 != 0 {
		return kindShell // 有执行位又没有扩展名，多半是 shell 脚本
	}
	return kindOther
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

// lines 把文本切成行，去掉行尾的 \r。
func lines(s string) []string {
	ls := strings.Split(s, "\n")
	for i, l := range ls {
		ls[i] = strings.TrimSuffix(l, "\r")
	}
	return ls
}

// block 是 Markdown 里的一段围栏代码。
type block struct {
	lang  string
	start int // 第一行代码的行号（1 起）
	body  string
	ctx   Context
}

// region 是 Markdown 正文里的一行，或一段 HTML 注释。
type region struct {
	line    int
	text    string
	comment bool // HTML 注释里的内容
	ctx     Context
}

var (
	// 这些词出现在代码块前面或同一段里，说明是安全说明或反例。
	teachingWords = regexp.MustCompile(`(?i)不要|切勿|禁止|避免|危险|恶意|攻击|注入|警惕|反例|钓鱼|\bnever\b|\bdon'?t\b|\bdo not\b|\bavoid\b|dangerous|malicious|attack|injection|beware|phishing|anti-?pattern|bad example`)
	// 这些词说明是给人看的示例。
	// 这些词说明是在教 Agent 防范：引号里的危险说法是在举要防的例子。
	defenseWords = regexp.MustCompile(`(?i)绝不|决不|不得|切勿|伪装|疑似|注入|防护|防范|一律忽略|当作数据|是数据|不是.{0,8}指令|prompt\s+injection|\binjection\b|treat\s+(?:it|them|this)?\s*as\s+data|do\s+not\s+follow|never\s+follow|don'?t\s+follow`)
	exampleWords = regexp.MustCompile(`(?i)示例|例如|比如|样例|举例|\bexample\b|\be\.g\.|for instance|\bsample\b`)
	fenceOpen    = regexp.MustCompile("^\\s{0,3}(`{3,}|~{3,})\\s*([A-Za-z0-9_+.-]*)")
	headingLine  = regexp.MustCompile(`^\s{0,3}#{1,6}\s`)
)

// markdown 把文档分成正文行、HTML 注释和围栏代码块，并给每段判断语境。
// 引用和代码块只提供语境，不会因此跳过检查。
func markdown(text string) (prose []region, blocks []block) {
	ls := lines(text)
	var heading string
	recent := make([]string, 0, 4) // 代码块前最近的几行正文
	push := func(s string) {
		if strings.TrimSpace(s) == "" {
			return
		}
		if len(recent) == cap(recent) {
			recent = recent[1:]
		}
		recent = append(recent, s)
	}
	for i := 0; i < len(ls); i++ {
		l := ls[i]
		if m := fenceOpen.FindStringSubmatch(l); m != nil {
			fence := m[1]
			lang := strings.ToLower(m[2])
			start := i + 2
			var body []string
			j := i + 1
			for ; j < len(ls); j++ {
				if strings.HasPrefix(strings.TrimSpace(ls[j]), fence[:3]) && strings.Trim(strings.TrimSpace(ls[j]), fence[:1]) == "" {
					break
				}
				body = append(body, ls[j])
			}
			ctxText := heading + "\n" + strings.Join(recent, "\n")
			blocks = append(blocks, block{lang: lang, start: start, body: strings.Join(body, "\n"), ctx: codeContext(ctxText)})
			i = j
			recent = recent[:0]
			continue
		}
		if k := strings.Index(l, "<!--"); k >= 0 {
			// HTML 注释：渲染后看不见，单独标出来
			end := strings.Index(l[k:], "-->")
			if end >= 0 {
				prose = append(prose, region{line: i + 1, text: l[k+4 : k+end], comment: true, ctx: CtxInstruction})
				rest := l[:k] + l[k+end+3:]
				prose = append(prose, region{line: i + 1, text: rest, ctx: proseContext(rest)})
				push(rest)
				continue
			}
			prose = append(prose, region{line: i + 1, text: l[k+4:], comment: true, ctx: CtxInstruction})
			j := i + 1
			for ; j < len(ls); j++ {
				if e := strings.Index(ls[j], "-->"); e >= 0 {
					prose = append(prose, region{line: j + 1, text: ls[j][:e], comment: true, ctx: CtxInstruction})
					break
				}
				prose = append(prose, region{line: j + 1, text: ls[j], comment: true, ctx: CtxInstruction})
			}
			i = j
			continue
		}
		if headingLine.MatchString(l) {
			heading = l
			recent = recent[:0]
		}
		prose = append(prose, region{line: i + 1, text: l, ctx: proseContext(l)})
		push(l)
	}
	return prose, blocks
}

func codeContext(around string) Context {
	switch {
	case teachingWords.MatchString(around):
		return CtxTeaching
	case exampleWords.MatchString(around):
		return CtxExample
	}
	return CtxInstruction
}

func proseContext(l string) Context {
	if (teachingWords.MatchString(l) || defenseWords.MatchString(l)) && (strings.ContainsAny(l, "\"'“”「」`") || exampleWords.MatchString(l)) {
		return CtxTeaching
	}
	return CtxInstruction
}

// shellLang 报告代码块的语言是否按 shell 解析。没写语言的也试着解析。
func shellLang(lang string) bool {
	switch lang {
	case "", "sh", "bash", "zsh", "shell", "console", "terminal", "shell-session", "shellsession":
		return true
	}
	return false
}

// stripPrompts 去掉终端会话里的提示符（“$ ”“% ”），保持行数不变。
func stripPrompts(body string) string {
	ls := lines(body)
	for i, l := range ls {
		t := strings.TrimLeft(l, " ")
		for _, p := range []string{"$ ", "% "} {
			if strings.HasPrefix(t, p) {
				ls[i] = t[len(p):]
			}
		}
	}
	return strings.Join(ls, "\n")
}
