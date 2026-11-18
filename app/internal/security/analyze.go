package security

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"path"
	"sort"
	"strconv"
	"strings"
	"time"
)

// rawEv 是还没脱敏的证据：text 是原文一行，只在内存里、只用于计算指纹。
type rawEv struct {
	path string
	line int
	text string
	ref  bool
}

type analyzer struct {
	fp       Fingerprinter
	pol      Policy
	out      []Finding
	seen     map[string]bool
	caps     map[string]bool
	external map[string]bool
	full     bool // 发现数量到了上限
}

// emit 记下一条发现。同一规则、同一组证据只记一次。
func (a *analyzer) emit(r rule, ctx Context, conf Confidence, evs ...rawEv) {
	if r.cat == Capability {
		if a.caps[r.id] {
			return
		}
		a.caps[r.id] = true
	}
	parts := []string{r.id, strconv.Itoa(r.version)}
	for _, e := range evs {
		parts = append(parts, e.path, strings.TrimSpace(e.text))
	}
	if r.cat == FileBoundary || r.cat == Capability {
		// 结构问题和能力提示没有“片段”可言，按路径区分
		parts = parts[:2]
		for _, e := range evs {
			parts = append(parts, e.path)
		}
	}
	key := a.fp(parts...)
	if a.seen[key] {
		return
	}
	if len(a.out) >= a.pol.MaxFindings {
		a.full = true
		return
	}
	a.seen[key] = true
	notice, conf := classify(r, ctx, conf)
	f := Finding{Key: key, Rule: r.id, RuleVersion: r.version, Category: r.cat, Notice: notice, Confidence: conf, Context: ctx, Message: r.msg}
	for _, e := range evs {
		f.Evidence = append(f.Evidence, Evidence{Path: e.path, Line: e.line, Snippet: redact(e.text), Ref: e.ref})
	}
	a.out = append(a.out, f)
}

func (a *analyzer) remote(text string) {
	for _, u := range urlPattern.FindAllString(text, -1) {
		a.external[redactURL(u)] = true
	}
}

var (
	machO = [][]byte{{0xCF, 0xFA, 0xED, 0xFE}, {0xCE, 0xFA, 0xED, 0xFE}, {0xCA, 0xFE, 0xBA, 0xBE}, {0xFE, 0xED, 0xFA, 0xCF}}
	elf   = []byte{0x7F, 'E', 'L', 'F'}
)

func executableBinary(b []byte) bool {
	for _, m := range machO {
		if bytes.HasPrefix(b, m) {
			return true
		}
	}
	return bytes.HasPrefix(b, elf)
}

// Analyze 分析一份快照，总是返回报告：取消、超时和局部失败时保留已有的
// 发现，并在覆盖说明里写明没检查到的范围。
func Analyze(ctx context.Context, in Input, pol Policy, fp Fingerprinter) Report {
	if pol.MaxFindings <= 0 {
		pol = DefaultPolicy()
	}
	rep := Report{Schema: SchemaVersion, Engine: EngineVersion, Ruleset: RulesetVersion(), StartedAt: time.Now(), Execution: Completed}
	a := &analyzer{fp: fp, pol: pol, seen: map[string]bool{}, caps: map[string]bool{}, external: map[string]bool{}}
	holes := append([]Hole(nil), in.Gaps...)
	if in.Incomplete {
		holes = append(holes, Hole{Reason: GapSourcePartial})
	}
	files := append([]File(nil), in.Files...)
	sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })
	rep.FilesDiscovered = len(files)
	docs := map[string][]string{} // Markdown 文件的各行，最后找“谁引用了这个脚本”

	for i, f := range files {
		if err := ctx.Err(); err != nil {
			rep.Execution, holes = stopped(err, holes)
			for _, rest := range files[i:] {
				if rest.Kind == Regular {
					holes = append(holes, Hole{Path: rest.Path, Reason: gapFor(err)})
				}
			}
			break
		}
		switch f.Kind {
		case Symlink:
			if f.Escape {
				a.emit(rLinkOutside, CtxStructure, High, rawEv{path: f.Path, text: f.Path + " -> " + f.Link})
				holes = append(holes, Hole{Path: f.Path, Reason: GapLinkNotFollowed})
			}
			continue
		case Special:
			a.emit(rSpecialFile, CtxStructure, High, rawEv{path: f.Path, text: f.Path})
			continue
		}
		if f.Gap != "" {
			holes = append(holes, Hole{Path: f.Path, Reason: f.Gap})
		}
		if f.Data == nil {
			continue
		}
		rep.BytesRead += int64(len(f.Data))
		if executableBinary(f.Data) {
			a.emit(rHasBinary, CtxStructure, High, rawEv{path: f.Path, text: f.Path})
			holes = append(holes, Hole{Path: f.Path, Reason: GapBinary})
			continue
		}
		text, gap := decode(f.Data)
		if gap != "" {
			if f.Gap == "" {
				holes = append(holes, Hole{Path: f.Path, Reason: gap})
			}
			continue
		}
		if err := a.file(f, text, docs); err != nil {
			holes = append(holes, Hole{Path: f.Path, Reason: GapAnalysisError})
			continue
		}
		if f.Gap == "" {
			rep.FilesAnalyzed++
		}
	}
	a.prune()
	a.references(docs)
	if a.full {
		holes = append(holes, Hole{Reason: GapFindingLimit})
	}
	for u := range a.external {
		rep.External = append(rep.External, u)
	}
	sort.Strings(rep.External)
	sort.SliceStable(holes, func(i, j int) bool {
		if holes[i].Path != holes[j].Path {
			return holes[i].Path < holes[j].Path
		}
		return holes[i].Reason < holes[j].Reason
	})
	rep.Holes = holes
	rep.Complete = len(holes) == 0 && rep.Execution == Completed
	if d, ok := Digest(in); ok {
		rep.ContentDigest = d
	}
	rep.Findings = a.out
	sortFindings(rep.Findings)
	rep.FinishedAt = time.Now()
	return rep
}

func stopped(err error, holes []Hole) (Execution, []Hole) {
	if errors.Is(err, context.DeadlineExceeded) {
		return TimedOut, holes
	}
	return Cancelled, holes
}

func gapFor(err error) Gap {
	if errors.Is(err, context.DeadlineExceeded) {
		return GapDeadline
	}
	return GapCancelled
}

func sortFindings(fs []Finding) {
	sort.SliceStable(fs, func(i, j int) bool {
		a, b := fs[i], fs[j]
		if a.Notice.Rank() != b.Notice.Rank() {
			return a.Notice.Rank() > b.Notice.Rank()
		}
		if a.Confidence.rank() != b.Confidence.rank() {
			return a.Confidence.rank() > b.Confidence.rank()
		}
		if a.Rule != b.Rule {
			return a.Rule < b.Rule
		}
		pa, pb := firstEv(a), firstEv(b)
		if pa.Path != pb.Path {
			return pa.Path < pb.Path
		}
		return pa.Line < pb.Line
	})
}

func firstEv(f Finding) Evidence {
	if len(f.Evidence) == 0 {
		return Evidence{}
	}
	return f.Evidence[0]
}

// file 按文件种类分析一个文本文件。解析器出意外时不中断整个报告。
func (a *analyzer) file(f File, text string, docs map[string][]string) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("analyze %s: %v", f.Path, r)
		}
	}()
	ls := lines(text)
	k := fileKind(f.Path, text, uint32(f.Mode))
	if f.Mode&0o111 != 0 || k == kindShell || k == kindScript {
		a.emit(rHasScripts, CtxStructure, High, rawEv{path: f.Path, text: f.Path})
	}
	a.invisible(f.Path, text)
	switch k {
	case kindShell:
		a.secrets(f.Path, ls, 1, CtxScript)
		if !a.shell(f.Path, text, 1, ls, CtxScript) {
			a.lineRules(f.Path, ls, 1, CtxScript, true)
		}
	case kindScript:
		a.secrets(f.Path, ls, 1, CtxScript)
		a.lineRules(f.Path, ls, 1, CtxScript, true)
	case kindConfig:
		a.secrets(f.Path, ls, 1, CtxConfig)
	default:
		docs[f.Path] = ls
		prose, blocks := markdown(text)
		for _, r := range prose {
			a.prose(f.Path, r)
		}
		for _, b := range blocks {
			bl := lines(b.body)
			a.secrets(f.Path, bl, b.start, b.ctx)
			switch {
			case shellLang(b.lang):
				if !a.shell(f.Path, stripPrompts(b.body), b.start, ls, b.ctx) {
					a.lineRules(f.Path, bl, b.start, b.ctx, true)
				}
			default:
				a.lineRules(f.Path, bl, b.start, b.ctx, true)
			}
		}
	}
	return nil
}

// prose 检查文档正文的一行：给 Agent 的指令、隐藏注释，以及写在正文里的命令。
func (a *analyzer) prose(p string, r region) {
	ev := rawEv{path: p, line: r.line, text: r.text}
	if r.comment && commentCmd.MatchString(r.text) {
		a.emit(rHiddenComment, CtxInstruction, Medium, ev)
	}
	checks := []struct {
		re   interface{ MatchString(string) bool }
		r    rule
		conf Confidence
	}{
		{ignoreInstr, rIgnoreInstr, High},
		{conceal, rConceal, Medium},
		{bypass, rBypass, Medium},
		{leakContext, rLeakContext, Medium},
		{secretChat, rSecretInChat, Medium},
	}
	for _, c := range checks {
		if c.re.MatchString(r.text) {
			a.emit(c.r, r.ctx, c.conf, ev)
		}
	}
	a.secrets(p, []string{r.text}, r.line, r.ctx)
	if !r.comment {
		a.lineRules(p, []string{r.text}, r.line, r.ctx, false)
	}
}

// lineRules 逐行找明确的危险模式。code 为假时是文档正文：只认完整的
// 危险命令，不因提到某个路径就报。
func (a *analyzer) lineRules(p string, ls []string, start int, ctx Context, code bool) {
	for i, l := range ls {
		ev := rawEv{path: p, line: start + i, text: l}
		if linePipe.MatchString(l) || lineSubst.MatchString(l) || lineScriptRemote.MatchString(l) {
			a.emit(rRemotePipe, ctx, High, ev)
			a.remote(l)
		}
		if lineEncoded.MatchString(l) {
			a.emit(rEncodedExec, ctx, High, ev)
		}
		if lineReverse.MatchString(l) {
			a.emit(rReverseShell, ctx, High, ev)
		}
		if lineWipe.MatchString(l) {
			a.emit(rWipeHome, ctx, High, ev)
		}
		if linePersist.MatchString(l) {
			a.emit(rPersistence, ctx, Medium, ev)
		}
		upload := lineUpload.MatchString(l)
		if s := sensitiveIn(l); s != "" {
			switch {
			case upload:
				a.emit(rCredUpload, ctx, Medium, ev)
			case code:
				a.emit(rReadCreds, ctx, Medium, ev)
			}
		}
		if upload && secretEnv.MatchString(l) && !strings.Contains(strings.ToLower(l), "authorization") {
			a.emit(rSecretEnv, ctx, Medium, ev)
		}
		if code && ctx != CtxTeaching && ctx != CtxExample {
			if lineSudo.MatchString(l) {
				a.emit(rNeedsAdmin, ctx, High, ev)
			}
			if lineNetwork.MatchString(l) {
				a.emit(rUsesNetwork, ctx, High, ev)
			}
		}
	}
}

// secrets 找像是真的密钥、令牌、私钥。
func (a *analyzer) secrets(p string, ls []string, start int, ctx Context) {
	conf := High
	if ctx == CtxExample || ctx == CtxTeaching {
		conf = Medium
	}
	for i, l := range ls {
		for _, re := range secretPatterns {
			m := re.FindString(l)
			if m == "" {
				continue
			}
			next := ""
			if i+1 < len(ls) {
				next = ls[i+1]
			}
			if realSecret(m, next) {
				a.emit(rEmbeddedSecret, ctx, conf, rawEv{path: p, line: start + i, text: l})
			}
		}
	}
}

// invisible 找看不见的字符：双向控制符、零宽字符、Unicode 标签字符。
// 每个文件每种只报第一处。
func (a *analyzer) invisible(p, text string) {
	line := 1
	var bidi, hidden bool
	for i, r := range text {
		switch {
		case r == '\n':
			line++
		case !bidi && (r >= 0x202A && r <= 0x202E || r >= 0x2066 && r <= 0x2069):
			bidi = true
			a.emit(rBidi, CtxStructure, High, rawEv{path: p, line: line, text: lineAt(text, line)})
		case !hidden && (r >= 0xE0000 && r <= 0xE007F || r == 0x200B || r == 0x2060 || r == 0x180E || r == 0xFEFF && i > 0):
			hidden = true
			conf := Medium
			if r >= 0xE0000 {
				conf = High
			}
			a.emit(rInvisible, CtxStructure, conf, rawEv{path: p, line: line, text: lineAt(text, line)})
		}
	}
}

func lineAt(text string, n int) string {
	ls := lines(text)
	if n-1 < len(ls) {
		return visible(ls[n-1])
	}
	return ""
}

// visible 把看不见的字符换成可见的码点标记，证据里才看得出来。
func visible(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r >= 0x202A && r <= 0x202E || r >= 0x2066 && r <= 0x2069 || r >= 0xE0000 && r <= 0xE007F || r == 0x200B || r == 0x2060 || r == 0x180E || r == 0xFEFF {
			fmt.Fprintf(&b, "<U+%04X>", r)
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

// prune 去掉已经被外传发现覆盖的“读取凭据”：同一行既读又发，只报外传。
func (a *analyzer) prune() {
	sent := map[string]bool{}
	for _, f := range a.out {
		if f.Rule == rCredUpload.id {
			for _, e := range f.Evidence {
				sent[e.Path+":"+strconv.Itoa(e.Line)] = true
			}
		}
	}
	out := a.out[:0]
	for _, f := range a.out {
		if f.Rule == rReadCreds.id && len(f.Evidence) > 0 && sent[f.Evidence[0].Path+":"+strconv.Itoa(f.Evidence[0].Line)] {
			continue
		}
		out = append(out, f)
	}
	a.out = out
}

// references 给脚本里的风险补上“哪份文档提到了这个脚本”的位置。只认
// 文档里明写的相对路径，不推断动态拼出来的路径。
func (a *analyzer) references(docs map[string][]string) {
	if len(docs) == 0 {
		return
	}
	names := make([]string, 0, len(docs))
	for n := range docs {
		names = append(names, n)
	}
	sort.Strings(names)
	for i := range a.out {
		f := &a.out[i]
		if !f.Notice.Risky() || len(f.Evidence) == 0 {
			continue
		}
		target := f.Evidence[0].Path
		if _, isDoc := docs[target]; isDoc {
			continue
		}
		for _, doc := range names {
			if ev, ok := mention(doc, docs[doc], target); ok {
				f.Evidence = append(f.Evidence, ev)
				break
			}
		}
	}
}

func mention(doc string, ls []string, target string) (Evidence, bool) {
	rel := target
	if dir := path.Dir(doc); dir != "." && strings.HasPrefix(target, dir+"/") {
		rel = strings.TrimPrefix(target, dir+"/")
	}
	for i, l := range ls {
		if strings.Contains(l, rel) {
			return Evidence{Path: doc, Line: i + 1, Snippet: redact(l), Ref: true}, true
		}
	}
	return Evidence{}, false
}

// Digest 是快照内容的摘要：按路径排序，逐项以带长度的字段写入类型、路径、
// 权限位、链接目标和完整字节。任何一项没读全时返回 false，不能拿它当
// 完整内容的身份。
func Digest(in Input) (string, bool) {
	if in.Incomplete || len(in.Gaps) > 0 {
		return "", false
	}
	files := append([]File(nil), in.Files...)
	sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })
	h := sha256.New()
	field := func(w io.Writer, b []byte) {
		var n [8]byte
		binary.BigEndian.PutUint64(n[:], uint64(len(b)))
		w.Write(n[:])
		w.Write(b)
	}
	for _, f := range files {
		if f.Gap != "" || f.Kind == Regular && f.Data == nil {
			return "", false
		}
		field(h, []byte{byte(f.Kind)})
		field(h, []byte(f.Path))
		field(h, []byte(strconv.FormatUint(uint64(f.Mode.Perm()), 8)))
		field(h, []byte(f.Link))
		field(h, f.Data)
	}
	return hex.EncodeToString(h.Sum(nil)), true
}
