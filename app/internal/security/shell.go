package security

import (
	"path"
	"regexp"
	"strings"

	"mvdan.cc/sh/v3/syntax"
)

// sh 分析一段 shell：只读语法树，从不执行。关联只跟踪同一脚本里明确
// 顺序上的直接参数、简单赋值和管道；变量被重新赋值时旧关系作废。
type sh struct {
	a     *analyzer
	path  string
	off   int      // 语法树行号加上它得到文件行号
	src   []string // 整个文件的各行，取证据用
	ctx   Context
	vars  map[string]rawEv // 变量 → 赋给它敏感内容的那一行
	files map[string]rawEv // 下载落到的文件 → 下载那一行
	exfil map[int]bool     // 已经报了外传的行，不再另报“读取凭据”
}

func (a *analyzer) shell(p, body string, start int, src []string, ctx Context) bool {
	f, err := syntax.NewParser(syntax.Variant(syntax.LangBash)).Parse(strings.NewReader(body), p)
	if err != nil {
		return false
	}
	s := &sh{a: a, path: p, off: start - 1, src: src, ctx: ctx, vars: map[string]rawEv{}, files: map[string]rawEv{}, exfil: map[int]bool{}}
	syntax.Walk(f, func(n syntax.Node) bool {
		switch n := n.(type) {
		case *syntax.Stmt:
			s.stmt(n)
		case *syntax.BinaryCmd:
			if n.Op == syntax.Pipe || n.Op == syntax.PipeAll {
				s.pipeline(flatten(n))
			}
		case *syntax.CallExpr:
			s.call(n)
		case *syntax.DeclClause:
			for _, as := range n.Args {
				s.assign(as)
			}
		}
		return true
	})
	return true
}

func (s *sh) ev(n syntax.Node) rawEv {
	line := int(n.Pos().Line()) + s.off
	text := ""
	if line-1 >= 0 && line-1 < len(s.src) {
		text = s.src[line-1]
	}
	return rawEv{path: s.path, line: line, text: text}
}

func flatten(b *syntax.BinaryCmd) []*syntax.Stmt {
	var out []*syntax.Stmt
	var walk func(st *syntax.Stmt)
	walk = func(st *syntax.Stmt) {
		if bc, ok := st.Cmd.(*syntax.BinaryCmd); ok && (bc.Op == syntax.Pipe || bc.Op == syntax.PipeAll) {
			walk(bc.X)
			walk(bc.Y)
			return
		}
		out = append(out, st)
	}
	walk(b.X)
	walk(b.Y)
	return out
}

// ---- 单词 ----

// text 尽量还原单词的字面：变量写成 $NAME，命令替换写成 $(…)。
func text(w *syntax.Word) string {
	if w == nil {
		return ""
	}
	var b strings.Builder
	var parts func(ps []syntax.WordPart)
	parts = func(ps []syntax.WordPart) {
		for _, p := range ps {
			switch p := p.(type) {
			case *syntax.Lit:
				b.WriteString(p.Value)
			case *syntax.SglQuoted:
				b.WriteString(p.Value)
			case *syntax.DblQuoted:
				parts(p.Parts)
			case *syntax.ParamExp:
				if p.Param != nil {
					b.WriteString("$" + p.Param.Value)
				}
			case *syntax.CmdSubst:
				b.WriteString("$(…)")
			case *syntax.ProcSubst:
				b.WriteString("<(…)")
			}
		}
	}
	parts(w.Parts)
	return b.String()
}

// vars 列出单词里引用的变量名。
func varsIn(w *syntax.Word) []string {
	var out []string
	syntax.Walk(w, func(n syntax.Node) bool {
		if p, ok := n.(*syntax.ParamExp); ok && p.Param != nil {
			out = append(out, p.Param.Value)
		}
		return true
	})
	return out
}

// substs 返回单词里命令替换、进程替换包着的语句。
func substs(w *syntax.Word) [][]*syntax.Stmt {
	var out [][]*syntax.Stmt
	syntax.Walk(w, func(n syntax.Node) bool {
		switch n := n.(type) {
		case *syntax.CmdSubst:
			out = append(out, n.Stmts)
		case *syntax.ProcSubst:
			out = append(out, n.Stmts)
		}
		return true
	})
	return out
}

// ---- 命令 ----

var wrappers = map[string]bool{"sudo": true, "env": true, "nohup": true, "time": true, "command": true, "exec": true, "nice": true, "doas": true}

// argv 去掉 sudo、env 这类前缀，返回真正的命令名与参数文本；sudo 另外报告。
func argv(c *syntax.CallExpr) (name string, args []string, words []*syntax.Word, sudo bool) {
	ws := c.Args
	for len(ws) > 0 {
		n := path.Base(text(ws[0]))
		if !wrappers[n] {
			break
		}
		if n == "sudo" || n == "doas" {
			sudo = true
		}
		ws = ws[1:]
		for len(ws) > 0 {
			t := text(ws[0])
			if strings.HasPrefix(t, "-") || n == "env" && strings.Contains(t, "=") {
				ws = ws[1:]
				continue
			}
			break
		}
	}
	if len(ws) == 0 {
		return "", nil, nil, sudo
	}
	for _, w := range ws[1:] {
		args = append(args, text(w))
	}
	return path.Base(text(ws[0])), args, ws[1:], sudo
}

var interpreters = map[string]bool{"sh": true, "bash": true, "zsh": true, "dash": true, "ksh": true, "fish": true, "python": true, "python3": true, "python2": true, "perl": true, "ruby": true, "node": true, "php": true, "osascript": true}

func isDownloader(name string) bool {
	return name == "curl" || name == "wget" || name == "fetch" || name == "aria2c"
}

// downloads 报告语句里有没有下载命令，返回那条命令。
func downloads(stmts []*syntax.Stmt) *syntax.CallExpr {
	var hit *syntax.CallExpr
	for _, st := range stmts {
		syntax.Walk(st, func(n syntax.Node) bool {
			if c, ok := n.(*syntax.CallExpr); ok && hit == nil {
				if name, _, _, _ := argv(c); isDownloader(name) {
					hit = c
				}
			}
			return hit == nil
		})
	}
	return hit
}

var decodeCmd = regexp.MustCompile(`^(?:base64|gbase64|openssl|xxd|uudecode)$`)

// decodes 报告这一级是不是在解码（base64 -d、xxd -r 等）。
func decodes(name string, args []string) bool {
	if !decodeCmd.MatchString(name) {
		return false
	}
	for _, a := range args {
		switch a {
		case "-d", "--decode", "-D", "-r", "-a":
			return true
		}
	}
	return name == "uudecode"
}

// runsStdin 报告解释器是不是在执行标准输入（没有给脚本文件，或给的是 -、-s）。
func runsStdin(name string, args []string) bool {
	if !interpreters[name] {
		return name == "source" || name == "." || name == "eval" || name == "iex"
	}
	for _, a := range args {
		if a == "-" || a == "-s" || a == "/dev/stdin" {
			return true
		}
		if !strings.HasPrefix(a, "-") {
			return false
		}
	}
	return true
}

func (s *sh) stmt(st *syntax.Stmt) {
	c, _ := st.Cmd.(*syntax.CallExpr)
	name := ""
	if c != nil {
		name, _, _, _ = argv(c)
	}
	for _, r := range st.Redirs {
		t := text(r.Word)
		switch r.Op {
		case syntax.RdrOut, syntax.AppOut, syntax.RdrAll, syntax.AppAll, syntax.RdrClob, syntax.DplOut:
			if strings.Contains(t, "/dev/tcp/") || strings.Contains(t, "/dev/udp/") {
				s.a.emit(rReverseShell, s.ctx, High, s.ev(r))
			}
			if linePersist.MatchString("> " + t) {
				s.a.emit(rPersistence, s.ctx, Medium, s.ev(r))
			}
		case syntax.RdrIn:
			if sensitiveIn(t) != "" && (name == "nc" || name == "ncat" || name == "netcat" || name == "curl" || name == "ssh") {
				ev := s.ev(r)
				s.exfil[ev.line] = true
				s.a.emit(rCredUpload, s.ctx, High, ev)
			}
		}
	}
}

func (s *sh) assign(as *syntax.Assign) {
	if as.Name == nil {
		return
	}
	name := as.Name.Value
	delete(s.vars, name)
	if as.Value == nil {
		return
	}
	if sensitiveIn(text(as.Value)) != "" {
		s.vars[name] = s.ev(as)
		return
	}
	for _, sub := range substs(as.Value) {
		if s.readsSensitive(sub) {
			s.vars[name] = s.ev(as)
			return
		}
	}
}

// readsSensitive 报告这些语句是否读取了敏感文件或钥匙串（cat ~/.ssh/id_rsa、
// security find-generic-password 等），或输出了已知的敏感变量。
func (s *sh) readsSensitive(stmts []*syntax.Stmt) bool {
	hit := false
	for _, st := range stmts {
		syntax.Walk(st, func(n syntax.Node) bool {
			if c, ok := n.(*syntax.CallExpr); ok && !hit {
				name, args, words, _ := argv(c)
				if name == "ssh" || name == "ssh-add" || name == "ssh-keygen" || name == "git" {
					return true
				}
				if sensitiveIn(name+" "+strings.Join(args, " ")) != "" {
					hit = true
				}
				for _, w := range words {
					for _, v := range varsIn(w) {
						if _, ok := s.vars[v]; ok {
							hit = true
						}
					}
				}
			}
			return !hit
		})
	}
	return hit
}

func (s *sh) call(c *syntax.CallExpr) {
	if len(c.Args) == 0 {
		for _, as := range c.Assigns {
			s.assign(as)
		}
		return
	}
	name, args, words, sudo := argv(c)
	ev := s.ev(c)
	line := name + " " + strings.Join(args, " ")
	if sudo {
		s.a.emit(rNeedsAdmin, s.ctx, High, ev)
	}
	if name == "" {
		return
	}
	switch {
	case isDownloader(name) || name == "scp" || name == "rsync" || name == "nc" || name == "ncat" || name == "ssh" || name == "http" || name == "https":
		if s.ctx != CtxTeaching && s.ctx != CtxExample {
			s.a.emit(rUsesNetwork, s.ctx, High, ev)
		}
	}

	// 下载后落盘的文件，之后被执行就构成“下载并执行”
	if isDownloader(name) {
		if out := outputFile(name, args); out != "" {
			s.files[clean(out)] = ev
		}
	}
	if target := executes(name, args); target != "" {
		if dl, ok := s.files[clean(target)]; ok {
			s.a.emit(rDownloadRun, s.ctx, High, dl, ev)
			s.a.remote(dl.text)
		}
	}

	// sh -c "$(curl …)"、bash <(curl …)、eval "$(curl …)"、source <(curl …)
	if interpreters[name] || name == "eval" || name == "source" || name == "." {
		for _, w := range words {
			for _, sub := range substs(w) {
				if dl := downloads(sub); dl != nil {
					s.a.emit(rRemotePipe, s.ctx, High, ev)
					s.a.remote(s.ev(dl).text)
				}
				if s.decodesIn(sub) {
					s.a.emit(rEncodedExec, s.ctx, High, ev)
				}
			}
		}
	}

	// 反向连接
	if strings.Contains(line, "/dev/tcp/") || strings.Contains(line, "/dev/udp/") ||
		(name == "nc" || name == "ncat" || name == "netcat") && hasFlag(args, "-e", "-c") ||
		name == "socat" && strings.Contains(strings.ToLower(line), "exec:") {
		s.a.emit(rReverseShell, s.ctx, High, ev)
	}

	// 大范围删除
	if name == "rm" && recursive(args) {
		for _, a := range args {
			if wipeTarget(a) {
				s.a.emit(rWipeHome, s.ctx, High, ev)
				break
			}
		}
	}

	// 持久化
	switch {
	case name == "launchctl" && hasFlag(args, "load", "bootstrap", "enable"),
		name == "crontab" && len(args) > 0 && !hasFlag(args, "-l", "-r"),
		(name == "cp" || name == "mv" || name == "ln" || name == "tee" || name == "install") && linePersist.MatchString("> "+lastArg(args)):
		s.a.emit(rPersistence, s.ctx, Medium, ev)
	}

	// 发送数据：参数里直接是敏感文件，或是装着敏感内容的变量
	if payloads := sends(name, args, words); len(payloads) > 0 {
		for _, p := range payloads {
			if sensitiveIn(text(p)) != "" {
				s.exfil[ev.line] = true
				s.a.emit(rCredUpload, s.ctx, High, ev)
			}
			for _, v := range varsIn(p) {
				if src, ok := s.vars[v]; ok {
					s.exfil[ev.line] = true
					s.a.emit(rCredUpload, s.ctx, High, src, ev)
				} else if secretEnv.MatchString("$" + v) {
					s.a.emit(rSecretEnv, s.ctx, Medium, ev)
				}
			}
		}
	}
	if (name == "scp" || name == "rsync") && sensitiveIn(line) != "" && remoteArg(args) {
		s.exfil[ev.line] = true
		s.a.emit(rCredUpload, s.ctx, High, ev)
	}

	// 单纯读取凭据
	if !s.exfil[ev.line] && name != "ssh" && name != "ssh-add" && name != "ssh-keygen" && name != "git" && name != "chmod" && name != "ls" && name != "test" && name != "[" {
		if sensitiveIn(line) != "" {
			s.a.emit(rReadCreds, s.ctx, Medium, ev)
		}
	}
}

func (s *sh) decodesIn(stmts []*syntax.Stmt) bool {
	hit := false
	for _, st := range stmts {
		syntax.Walk(st, func(n syntax.Node) bool {
			if c, ok := n.(*syntax.CallExpr); ok {
				if name, args, _, _ := argv(c); decodes(name, args) {
					hit = true
				}
			}
			return !hit
		})
	}
	return hit
}

// pipeline 看管道前后级的关系：下载 → 解释器、解码 → 解释器、敏感读取 → 发送。
func (s *sh) pipeline(stages []*syntax.Stmt) {
	type stage struct {
		name string
		args []string
		c    *syntax.CallExpr
	}
	var st []stage
	for _, x := range stages {
		c, ok := x.Cmd.(*syntax.CallExpr)
		if !ok {
			st = append(st, stage{})
			continue
		}
		n, a, _, _ := argv(c)
		st = append(st, stage{n, a, c})
	}
	for i, from := range st {
		if from.c == nil {
			continue
		}
		for _, to := range st[i+1:] {
			if to.c == nil {
				continue
			}
			ev := s.ev(from.c)
			switch {
			case isDownloader(from.name) && runsStdin(to.name, to.args):
				s.a.emit(rRemotePipe, s.ctx, High, ev)
				s.a.remote(ev.text)
			case decodes(from.name, from.args) && runsStdin(to.name, to.args):
				s.a.emit(rEncodedExec, s.ctx, High, ev)
			case s.readsSensitive([]*syntax.Stmt{stages[i]}) && sendsStdin(to.name, to.args):
				s.exfil[ev.line] = true
				s.exfil[s.ev(to.c).line] = true
				s.a.emit(rCredUpload, s.ctx, High, ev)
			}
		}
	}
}

// ---- 参数小工具 ----

func hasFlag(args []string, flags ...string) bool {
	for _, a := range args {
		for _, f := range flags {
			if a == f {
				return true
			}
		}
	}
	return false
}

func lastArg(args []string) string {
	if len(args) == 0 {
		return ""
	}
	return args[len(args)-1]
}

// clean 只取文件名比较：下载到 /tmp/x.sh、再执行 x.sh 也算同一个文件。
func clean(p string) string {
	return path.Base(strings.Trim(p, `"'`))
}

// outputFile 是 curl -o / wget -O 落盘的文件；curl -O 用网址最后一段。
func outputFile(name string, args []string) string {
	for i, a := range args {
		switch {
		case (a == "-o" || a == "--output") && name == "curl" && i+1 < len(args):
			return args[i+1]
		case strings.HasPrefix(a, "--output=") && name == "curl":
			return strings.TrimPrefix(a, "--output=")
		case (a == "-O" || a == "--output-document") && name == "wget" && i+1 < len(args):
			return args[i+1]
		case a == "-O" || a == "--remote-name":
			if name == "curl" {
				for _, u := range args {
					if urlPattern.MatchString(u) {
						return path.Base(redactURL(u))
					}
				}
			}
		}
	}
	if name == "wget" {
		for _, u := range args {
			if urlPattern.MatchString(u) {
				return path.Base(redactURL(u))
			}
		}
	}
	return ""
}

// executes 是这条命令执行的脚本文件：bash x.sh、./x.sh、source x.sh。
func executes(name string, args []string) string {
	if interpreters[name] || name == "source" || name == "." {
		for _, a := range args {
			if !strings.HasPrefix(a, "-") {
				return a
			}
		}
		return ""
	}
	return name
}

func recursive(args []string) bool {
	for _, a := range args {
		if a == "--recursive" || strings.HasPrefix(a, "-") && !strings.HasPrefix(a, "--") && strings.ContainsAny(a, "rR") {
			return true
		}
	}
	return false
}

func wipeTarget(a string) bool {
	switch strings.TrimRight(a, "/") {
	case "", "~", "$HOME", "/*", "~/*", "$HOME/*", "/Users", "/System", "/usr", "/etc", "/Library", "/Applications":
		return true
	}
	return false
}

// sends 返回把数据发出去的参数：curl -d/-F/-T/--data*、wget --post-*。
func sends(name string, args []string, words []*syntax.Word) []*syntax.Word {
	var out []*syntax.Word
	for i, a := range args {
		switch name {
		case "curl":
			switch {
			case a == "-d" || a == "--data" || a == "--data-binary" || a == "--data-raw" || a == "--data-urlencode" || a == "-F" || a == "--form" || a == "-T" || a == "--upload-file" || a == "--json":
				if i+1 < len(words) {
					out = append(out, words[i+1])
				}
			case strings.HasPrefix(a, "-d") && len(a) > 2, strings.HasPrefix(a, "-F") && len(a) > 2, strings.HasPrefix(a, "--data") && strings.Contains(a, "="), strings.HasPrefix(a, "--form="):
				out = append(out, words[i])
			}
		case "wget":
			if strings.HasPrefix(a, "--post-data") || strings.HasPrefix(a, "--post-file") || strings.HasPrefix(a, "--body-file") {
				if strings.Contains(a, "=") {
					out = append(out, words[i])
				} else if i+1 < len(words) {
					out = append(out, words[i+1])
				}
			}
		}
	}
	return out
}

// sendsStdin 报告这一级是不是把标准输入发出去。
func sendsStdin(name string, args []string) bool {
	switch name {
	case "nc", "ncat", "netcat", "socat":
		return true
	case "curl":
		for i, a := range args {
			if (a == "-d" || a == "--data" || a == "--data-binary" || a == "-F" || a == "-T" || a == "--upload-file" || a == "--json") && i+1 < len(args) && strings.Contains(args[i+1], "@-") ||
				(a == "-T" || a == "--upload-file") && i+1 < len(args) && (args[i+1] == "-" || args[i+1] == ".") ||
				strings.Contains(a, "@-") {
				return true
			}
		}
	case "wget":
		return strings.Contains(strings.Join(args, " "), "/dev/stdin")
	}
	return false
}

// remoteArg 报告 scp/rsync 的参数里有没有远端目标（host:path）。
func remoteArg(args []string) bool {
	for _, a := range args {
		if strings.HasPrefix(a, "-") || strings.HasPrefix(a, "/") || strings.HasPrefix(a, "~") || strings.HasPrefix(a, ".") {
			continue
		}
		if i := strings.Index(a, ":"); i > 0 && !strings.Contains(a[:i], "/") {
			return true
		}
	}
	return false
}
