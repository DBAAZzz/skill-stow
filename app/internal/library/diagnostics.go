package library

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"syscall"
	"time"
)

// 诊断信息：用户报问题时复制给维护者的一段纯文本。只有排查用得上的状态，
// 不含技能正文、配置原文、token、代理地址；主目录一律写成 ~。字段名用
// 固定的英文，不跟界面语言变，方便维护者检索。

// diagVersion 是诊断文本的格式版本，加减字段时递增。
const diagVersion = 1

// DiagEnv 是只有界面层知道的运行环境。
type DiagEnv struct {
	Version  string // 应用版本
	Build    string // packaged 或 dev
	Language string // 界面实际用的语言
	Theme    string // 界面实际用的外观：light、dark
}

// osVersion 是 macOS 的版本号；测试里换掉。
var osVersion = func() string {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "sw_vers", "-productVersion").Output()
	if err != nil {
		return "?"
	}
	return strings.TrimSpace(string(out))
}

// recentInDiagnostics 是诊断里附几条最近的警告和错误。
const recentInDiagnostics = 20

// maxErrTextLen 是日志和诊断里单条错误文本的上限字节数。
const maxErrTextLen = 200

// DiagAgent 是单个 Agent 的只读诊断摘要，在界面线程提取。
type DiagAgent struct {
	Name        string
	SkillsRel   string
	MineCount   int
	ConfigState string
	Hidden      bool
}

// DiagSnapshot 是在界面线程提取的只读诊断数据，供后台安全格式化并读写磁盘日志。
type DiagSnapshot struct {
	Disk            bool
	Home            string
	RootDisplay     string
	SkillCount      int
	MineCount       int
	ProjectCount    int
	ManualCount     int
	ScanTookMS      int64
	UnreadableCount int
	BrokenCount     int
	Agents          []DiagAgent
	UpdatesErr      string
	RecordsErr      string
	NativeErr       string
	PendingUpdateTx int
	ProxyMode       string
	CustomProxy     bool
	UseGHAuth       bool
	HiddenAgents    int
	CustomDirs      int
	AutoDiscover    bool
	MergeWorktrees  bool
	OldVersion      string
	Tidy            diagTidyCounts
}

type diagTidyCounts struct {
	SameCopies    int
	DiffCopies    int
	ExtraLinks    int
	BrokenLinks   int
	Incomplete    int
	StaleRecords  int
	NoDescription int
	GlobalLinked  int
}

// DiagSnapshot 在界面线程复制诊断所需的只读状态，不引用 Library 里的可变 map 或切片。
func (l *Library) DiagSnapshot() DiagSnapshot {
	agents := make([]DiagAgent, 0, len(l.Agents))
	for _, a := range l.Agents {
		name := a.ID
		if IsCustom(a.ID) {
			name = "custom"
		}
		agents = append(agents, DiagAgent{
			Name:        name,
			SkillsRel:   a.Skills,
			MineCount:   l.SkillCount(a.ID),
			ConfigState: l.agentConfigState(a.ID),
			Hidden:      l.Hidden(a.ID),
		})
	}
	s := l.Settings
	p := l.Problems()
	return DiagSnapshot{
		Disk:            l.disk,
		Home:            l.Home,
		RootDisplay:     l.Display(l.Root),
		SkillCount:      len(l.Skills),
		MineCount:       l.mineCount(),
		ProjectCount:    len(l.Projects),
		ManualCount:     len(l.manual),
		ScanTookMS:      l.scanTook.Milliseconds(),
		UnreadableCount: len(l.unreadable),
		BrokenCount:     len(l.Broken),
		Agents:          agents,
		UpdatesErr:      errState(l.Home, l.updatesErr),
		RecordsErr:      errState(l.Home, l.recordsErr),
		NativeErr:       errState(l.Home, l.nativeErr),
		PendingUpdateTx: l.pendingTransactions(),
		ProxyMode:       cmpOr(s.UpdateProxyMode, "system"),
		CustomProxy:     strings.TrimSpace(s.UpdateProxy) != "",
		UseGHAuth:       s.UseGHAuth,
		HiddenAgents:    len(s.HiddenAgents),
		CustomDirs:      len(s.AgentDirs),
		AutoDiscover:    s.AutoDiscover,
		MergeWorktrees:  s.MergeWorktrees,
		OldVersion:      s.UpdateOldVersion,
		Tidy: diagTidyCounts{
			SameCopies:    p.Count(SameCopies),
			DiffCopies:    p.Count(DiffCopies),
			ExtraLinks:    p.Count(ExtraLinks),
			BrokenLinks:   p.Count(BrokenLinks),
			Incomplete:    p.Count(Incomplete),
			StaleRecords:  p.Count(StaleRecords),
			NoDescription: p.Count(NoDescription),
			GlobalLinked:  p.Count(GlobalLinked),
		},
	}
}

// Diagnostics 生成诊断文本。
func (l *Library) Diagnostics(env DiagEnv) string {
	return l.DiagSnapshot().Format(env)
}

// Format 在后台用只读快照和磁盘事件日志生成诊断文本。
func (s DiagSnapshot) Format(env DiagEnv) string {
	var b strings.Builder
	line := func(format string, args ...any) { fmt.Fprintf(&b, format+"\n", args...) }
	onOff := func(on bool) string {
		if on {
			return "on"
		}
		return "off"
	}

	line("Skill Stow diagnostics v%d (no skill contents, config contents, tokens or proxy addresses)", diagVersion)
	line("generated %s", time.Now().Format(time.RFC3339))
	line("app      %s (%s), %s/%s, macOS %s, lang=%s, theme=%s, sample=%t",
		env.Version, env.Build, runtime.GOOS, runtime.GOARCH, osVersion(), env.Language, env.Theme, !s.Disk)
	line("library  %s, %d skills (%d mine), %d projects (%d manual)",
		s.RootDisplay, s.SkillCount, s.MineCount, s.ProjectCount, s.ManualCount)
	line("scan     %dms, unreadable dirs=%d, broken links=%d", s.ScanTookMS, s.UnreadableCount, s.BrokenCount)

	line("agents")
	for _, a := range s.Agents {
		hidden := ""
		if a.Hidden {
			hidden = "  (hidden)"
		}
		line("  %-14s %s  mine=%d%s%s", a.Name, diagAgentDirState(s.Home, s.Disk, a.SkillsRel), a.MineCount, a.ConfigState, hidden)
	}

	line("store    updates=%s records=%s native=%s pendingUpdateTx=%d",
		s.UpdatesErr, s.RecordsErr, s.NativeErr, s.PendingUpdateTx)
	if s.Disk {
		dir := appDir(s.Home)
		if copies := corruptCopiesIn(dir); len(copies) > 0 {
			line("corrupt  %s", strings.Join(copies, ", "))
		}
		for _, e := range unconfirmedWritesIn(dir) {
			line("unconfirmed write  %s  %s  %s", e.Time.Format("01-02 15:04:05"), e.Target, e.Data["file"])
		}
	}

	line("network  proxyMode=%s customProxy=%t ghAuth=%s", s.ProxyMode, s.CustomProxy, onOff(s.UseGHAuth))
	line("settings hiddenAgents=%d customDirs=%d autoDiscover=%s mergeWorktrees=%s oldVersion=%s",
		s.HiddenAgents, s.CustomDirs, onOff(s.AutoDiscover), onOff(s.MergeWorktrees), s.OldVersion)

	line("tidy     same=%d diff=%d extraLinks=%d broken=%d incomplete=%d stale=%d noDescription=%d globalLinked=%d",
		s.Tidy.SameCopies, s.Tidy.DiffCopies, s.Tidy.ExtraLinks, s.Tidy.BrokenLinks,
		s.Tidy.Incomplete, s.Tidy.StaleRecords, s.Tidy.NoDescription, s.Tidy.GlobalLinked)

	var recent []Event
	if s.Disk {
		recent = recentEventsIn(appDir(s.Home), recentInDiagnostics, LevelWarn)
	}
	line("recent   %d warn/error", len(recent))
	for _, e := range recent {
		line("  %s", formatEvent(e))
	}
	return b.String()
}

func (l *Library) mineCount() int {
	n := 0
	for _, s := range l.Skills {
		if s.Source == Mine {
			n++
		}
	}
	return n
}

// diagAgentDirState 是 Agent 的技能目录在不在、是不是链接。
func diagAgentDirState(home string, disk bool, skillsRel string) string {
	if skillsRel == "" {
		return "-"
	}
	abs := filepath.Join(home, skillsRel)
	shown := displayPath(home, abs)
	if !disk {
		return shown
	}
	fi, err := os.Lstat(abs)
	switch {
	case err != nil:
		return shown + " missing"
	case fi.Mode()&os.ModeSymlink != 0:
		if real, err := realPath(abs); err == nil {
			return shown + " → " + displayPath(home, real)
		}
		return shown + " → (broken)"
	}
	return shown
}

func displayPath(home, path string) string {
	if home == "" {
		return path
	}
	if path == home {
		return "~"
	}
	if rel, err := filepath.Rel(home, path); err == nil && !strings.HasPrefix(rel, "..") {
		return "~/" + filepath.ToSlash(rel)
	}
	return path
}

// agentConfigState 是能按技能启停的 Agent 的原生配置读得怎么样。
func (l *Library) agentConfigState(id string) string {
	if !nativeSupported(id) {
		return ""
	}
	c := l.native[id]
	if c == nil {
		return "  config not read"
	}
	return "  config " + l.Display(c.file) + " " + errState(l.Home, c.err)
}

func (l *Library) pendingTransactions() int {
	if l.updatesErr != nil || l.updates == nil {
		return 0
	}
	n := 0
	for _, tx := range l.updates.Transactions {
		if tx.Status != "retained" {
			n++
		}
	}
	return n
}

func errState(home string, err error) string {
	if err == nil {
		return "ok"
	}
	return "error(" + safeErrMsg(home, err) + ")"
}

// safeErrMsg 返回适合放进日志和诊断快照的错误文本：原生配置解析错误只写种类，
// 其余错误统一脱敏主目录/网址凭据并限制长度。
func safeErrMsg(home string, err error) string {
	if err == nil {
		return ""
	}
	var pe *configParseError
	if errors.As(err, &pe) {
		return "parse error"
	}
	msg := strings.NewReplacer("\r", " ", "\n", " ").Replace(err.Error())
	return safeText(home, msg)
}

// safeText 脱敏主目录、网址凭据和查询串，并限制长度。
func safeText(home, s string) string {
	return truncateUTF8(redact(home, s), maxErrTextLen)
}

func cmpOr(s, def string) string {
	if s == "" {
		return def
	}
	return s
}

// formatEvent 是一条事件在诊断里的一行。
func formatEvent(e Event) string {
	parts := []string{e.Time.Format("01-02 15:04:05"), strings.ToUpper(string(e.Level)), e.Cat + "." + e.Op}
	if e.Target != "" {
		parts = append(parts, e.Target)
	}
	if e.Kind != "" {
		parts = append(parts, "kind="+e.Kind)
	}
	if e.Status != 0 {
		parts = append(parts, fmt.Sprintf("status=%d", e.Status))
	}
	if e.Msg != "" {
		parts = append(parts, e.Msg)
	}
	return strings.Join(parts, "  ")
}

var (
	urlUserinfo = regexp.MustCompile(`([a-zA-Z][a-zA-Z0-9+.-]*://)[^/\s@]+@`)
	urlQuery    = regexp.MustCompile(`(https?://[^\s?"']+)\?[^\s"']*`)
)

// redact 把主目录换成 ~，去掉网址里的账号密码和查询串。
func redact(home, s string) string {
	if s == "" {
		return s
	}
	if home != "" {
		s = strings.ReplaceAll(s, home, "~")
	}
	s = urlUserinfo.ReplaceAllString(s, "${1}***@")
	return urlQuery.ReplaceAllString(s, "${1}?…")
}

// netCause 把网络错误再细分：超时、DNS、TLS、代理、拒绝连接、连接重置、
// 半路断开。不是网络错误时为空。
func netCause(err error) string {
	if err == nil {
		return ""
	}
	var dns *net.DNSError
	var op *net.OpError
	var ne net.Error
	var unknownCA x509.UnknownAuthorityError
	var hostErr x509.HostnameError
	var certErr x509.CertificateInvalidError
	var recordErr tls.RecordHeaderError
	var verifyErr *tls.CertificateVerificationError
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		return "timeout"
	case errors.As(err, &dns):
		return "dns"
	case errors.As(err, &unknownCA), errors.As(err, &hostErr), errors.As(err, &certErr), errors.As(err, &recordErr), errors.As(err, &verifyErr):
		return "tls"
	case errors.As(err, &op) && op.Op == "proxyconnect":
		return "proxy"
	case errors.Is(err, syscall.ECONNREFUSED):
		return "refused"
	case errors.Is(err, syscall.ECONNRESET):
		return "reset"
	case errors.As(err, &ne) && ne.Timeout():
		return "timeout"
	case errors.Is(err, io.ErrUnexpectedEOF), errors.Is(err, io.EOF):
		return "eof"
	case errors.As(err, &ne), errors.As(err, &op):
		return "network"
	}
	return ""
}
