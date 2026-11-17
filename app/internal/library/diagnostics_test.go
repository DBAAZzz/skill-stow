package library

import (
	"context"
	"crypto/x509"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

func eventsOf(t *testing.T, home string) []Event {
	t.Helper()
	return readEvents(appDir(home))
}

func TestDisablingASkillLogsAPairedConfigWriteWithoutTheConfigContents(t *testing.T) {
	// Given Claude Code 读得到的一个技能，配置里有用户自己的内容
	home, h := nativeHome(t)
	t.Setenv("HOME", home)
	must(t, os.MkdirAll(h(".claude"), 0o755))
	must(t, os.WriteFile(h(".claude/settings.json"), []byte(`{"secretNote": "keep-me-private"}`+"\n"), 0o600))
	l := Scan(home)

	// When 在 Claude Code 里停用它
	must(t, l.Enable(l.Get("demo"), "claude-code", false))

	// Then 日志里有同一个编号的开始和结束，只有指纹，没有配置原文
	var start, end *Event
	for _, e := range eventsOf(t, home) {
		switch e.Op {
		case "write.start":
			start = &e
		case "write.end":
			end = &e
		}
	}
	if start == nil || end == nil || start.ID != end.ID || end.Level != LevelInfo {
		t.Fatalf("应有配对的写入事件：start=%+v end=%+v", start, end)
	}
	if start.Data["file"] != "~/.claude/settings.json" || start.Data["before"] == end.Data["after"] {
		t.Errorf("写入事件应记缩写的文件和前后指纹：%+v %+v", start.Data, end.Data)
	}
	raw, _ := os.ReadFile(eventsFile(appDir(home)))
	if strings.Contains(string(raw), "keep-me-private") || strings.Contains(string(raw), home) {
		t.Errorf("日志不应含配置原文或主目录：%s", raw)
	}
	if len(l.UnconfirmedWrites()) != 0 {
		t.Errorf("写完的不应算未确认：%+v", l.UnconfirmedWrites())
	}
}

func TestAWriteThatNeverFinishedIsReportedAsUnconfirmed(t *testing.T) {
	// Given 日志里只有一次写入的开始（写到一半应用退出了）
	home := t.TempDir()
	appendEvent(appDir(home), Event{Level: LevelInfo, Cat: "native", Op: "write.start", ID: "abc", Target: "codex", Data: map[string]string{"file": "~/.codex/config.toml"}})
	l := Scan(home)

	// When 生成诊断信息
	got := l.Diagnostics(DiagEnv{Version: "0.1.0", Build: "dev"})

	// Then 列出这次没确认的写入
	if len(l.UnconfirmedWrites()) != 1 || !strings.Contains(got, "unconfirmed write") || !strings.Contains(got, "~/.codex/config.toml") {
		t.Errorf("应报告未确认的写入：\n%s", got)
	}
}

func TestEventLogRotatesAndKeepsTheNewestEvents(t *testing.T) {
	// Given 已经写满一份的日志
	dir := t.TempDir()
	pad := strings.Repeat("x", 1000)
	for i := range eventsMax/1000 + 50 {
		appendEvent(dir, Event{Level: LevelError, Cat: "test", Op: fmt.Sprint(i), Msg: pad})
	}

	// Then 轮转成两份，加起来不超过两份的上限，最新的一条还在
	cur, err1 := os.Stat(eventsFile(dir))
	old, err2 := os.Stat(rotatedEventsFile(dir))
	if err1 != nil || err2 != nil || cur.Size() > eventsMax || old.Size() > eventsMax {
		t.Fatalf("应轮转成两份且各不超过上限：%v %v", err1, err2)
	}
	all := readEvents(dir)
	if last := all[len(all)-1]; last.Op != fmt.Sprint(eventsMax/1000+49) {
		t.Errorf("最新的事件应在最后，got %s", last.Op)
	}
}

func TestDiagnosticsLeaveOutHomeProxyAddressAndSkillContents(t *testing.T) {
	// Given 设了带账号密码的代理、开了 gh 登录的一台机器
	home, h := nativeHome(t)
	t.Setenv("HOME", home)
	write(t, appDir(home), "settings.json", `{"updateProxyMode": "manual", "updateProxy": "http://alice:s3cret@proxy.corp:8080", "useGhAuth": true, "autoDiscover": true}`, 0o644)
	writeSkill(t, h(".agents/skills/private"), "TOP-SECRET-BODY")
	l := Scan(home)
	l.Log(Event{Level: LevelError, Cat: "update", Op: "fetch", Msg: "Get https://user:pw@api.github.com/repos?token=abc: failed in " + h("x")})

	// When 生成诊断信息
	got := l.Diagnostics(DiagEnv{Version: "0.1.0", Build: "dev", Language: "en", Theme: "dark"})

	// Then 有排查要用的状态，没有主目录、代理地址、账号密码和技能正文
	for _, want := range []string{"Skill Stow diagnostics v1", "0.1.0 (dev)", "proxyMode=manual customProxy=true ghAuth=on", "claude-code", "~/.claude/settings.json ok", "update.fetch"} {
		if !strings.Contains(got, want) {
			t.Errorf("诊断应含 %q：\n%s", want, got)
		}
	}
	for _, leak := range []string{home, "alice", "s3cret", "proxy.corp", "TOP-SECRET-BODY", "pw@", "token=abc"} {
		if strings.Contains(got, leak) {
			t.Errorf("诊断不应含 %q：\n%s", leak, got)
		}
	}
}

func TestDiagnosticsShowWhyAnAgentConfigIsUnknown(t *testing.T) {
	// Given 写坏了的 Codex 配置
	home, h := nativeHome(t)
	t.Setenv("HOME", home)
	must(t, os.MkdirAll(h(".codex"), 0o755))
	must(t, os.WriteFile(h(".codex/config.toml"), []byte("[[skills.config]\npath = \n"), 0o600))

	// When 生成诊断信息
	got := Scan(home).Diagnostics(DiagEnv{})

	// Then Codex 那行写出解析错误，而不只是「未知」
	for _, line := range strings.Split(got, "\n") {
		if strings.Contains(line, "~/.codex/config.toml") {
			if !strings.Contains(line, "error(") {
				t.Errorf("Codex 配置应报错误原因：%s", line)
			}
			return
		}
	}
	t.Errorf("诊断里没有 Codex 配置：\n%s", got)
}

func TestUnreadableSkillFolderIsCountedAndWarnedOnce(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root 读得了没有权限的目录")
	}
	// Given Claude Code 的技能目录没有读权限
	home, h := nativeHome(t)
	must(t, os.Chmod(h(".claude/skills"), 0o000))
	t.Cleanup(func() { os.Chmod(h(".claude/skills"), 0o755) })

	// When 扫描两次
	l := Scan(home)
	Scan(home)

	// Then 记下读不了的目录，警告只记一次
	if len(l.Unreadable()) != 1 || l.Unreadable()[0] != h(".claude/skills") {
		t.Errorf("应记下读不了的目录：%v", l.Unreadable())
	}
	n := 0
	for _, e := range eventsOf(t, home) {
		if e.Cat == "scan" && e.Op == "unreadable" {
			n++
			if e.Msg != "~/.claude/skills" {
				t.Errorf("警告应写缩写的目录：%q", e.Msg)
			}
		}
	}
	if n != 1 {
		t.Errorf("同样的警告应只记一次，记了 %d 次", n)
	}
}

func TestFailedOperationsAreLoggedWithAStableKind(t *testing.T) {
	home := t.TempDir()
	l := Scan(home)
	cases := []struct {
		err  error
		kind string
	}{
		{&RemoteError{Kind: RemoteQuota, Status: 403}, "quota"},
		{&RemoteError{Kind: RemoteNetwork, Err: &net.DNSError{Err: "no such host", Name: "api.github.com"}}, "dns"},
		{&ImportError{Network: true, Err: &url.Error{Op: "Get", URL: "https://x", Err: &net.OpError{Op: "proxyconnect", Err: syscall.ECONNREFUSED}}}, "proxy"},
		{&url.Error{Op: "Get", URL: "https://x", Err: &net.OpError{Op: "dial", Err: syscall.ECONNREFUSED}}, "refused"},
		{&url.Error{Op: "Get", URL: "https://x", Err: x509.UnknownAuthorityError{}}, "tls"},
		{fmt.Errorf("wrapped: %w", context.DeadlineExceeded), "timeout"},
		{&os.PathError{Op: "open", Path: "/x", Err: os.ErrPermission}, "permission"},
		{errors.New("something else"), ""},
	}
	for _, c := range cases {
		func() {
			err := c.err
			defer l.track("test", "op", "")(&err)
		}()
		all := eventsOf(t, home)
		if got := all[len(all)-1]; got.Kind != c.kind || got.Level != LevelError {
			t.Errorf("%v：kind=%q level=%s，want %q error", c.err, got.Kind, got.Level, c.kind)
		}
	}
}

func TestCorruptCopiesAreLimitedAndReportedOnce(t *testing.T) {
	// Given 已经留着 3 份旧的损坏设置
	home := t.TempDir()
	dir := appDir(home)
	for i := range keepCorrupt {
		write(t, dir, fmt.Sprintf("settings.json.corrupt-2026010%d-000000", i+1), "old", 0o644)
	}
	write(t, dir, "settings.json", "{broken", 0o644)

	// When 扫描时又发现设置坏了
	l := Scan(home)

	// Then 只留最近 3 份，最早的那份删了；这次挪开的列给界面，并记一条事件
	copies := l.CorruptCopies()
	if len(copies) != keepCorrupt || copies[0] != "settings.json.corrupt-20260102-000000" {
		t.Errorf("应只留最近 %d 份：%v", keepCorrupt, copies)
	}
	if len(l.Recovered()) != 1 || !strings.HasPrefix(filepath.Base(l.Recovered()[0]), "settings.json.corrupt-") {
		t.Errorf("这次挪开的应列给界面：%v", l.Recovered())
	}
	if len(Scan(home).Recovered()) != 0 {
		t.Error("下次扫描不应再提示")
	}
	var logged bool
	for _, e := range eventsOf(t, home) {
		logged = logged || (e.Cat == "appdata" && e.Op == "corrupt" && e.Target == "settings.json")
	}
	if !logged {
		t.Error("应记一条数据文件损坏的事件")
	}
}

func TestSampleLibraryWritesNoLog(t *testing.T) {
	l := Sample()
	l.Log(Event{Level: LevelError, Cat: "test", Op: "x"})
	if l.AppDir() != "" || l.RecentEvents(10, LevelInfo) != nil {
		t.Error("示例数据不应有数据目录和日志")
	}
	if got := l.Diagnostics(DiagEnv{}); !strings.Contains(got, "sample=true") {
		t.Errorf("示例数据的诊断应标出 sample=true：\n%s", got)
	}
}

func TestRecentEventsFilterByLevel(t *testing.T) {
	home := t.TempDir()
	l := Scan(home)
	for _, lv := range []Level{LevelInfo, LevelWarn, LevelError, LevelInfo} {
		l.Log(Event{Level: lv, Cat: "test", Op: string(lv), Time: time.Now()})
	}
	got := l.RecentEvents(10, LevelWarn)
	if len(got) != 2 || got[0].Level != LevelWarn || got[1].Level != LevelError {
		t.Errorf("应只列 warn 和 error：%+v", got)
	}
}

func TestDiagnosticsAndLogsRedactBadCodexConfigAndReadErrors(t *testing.T) {
	// Given Codex 配置里有一行带密钥的语法错误，以及读失败的 native-skills.json（路径含 home）
	home, h := nativeHome(t)
	t.Setenv("HOME", home)
	must(t, os.MkdirAll(h(".codex"), 0o755))
	must(t, os.WriteFile(h(".codex/config.toml"), []byte("api_key \"sk-secret\"\n"), 0o600))
	must(t, os.MkdirAll(filepath.Join(appDir(home), "native-skills.json"), 0o755))

	l := Scan(home)

	// When 启停 Codex 技能失败（会写日志），并生成诊断信息
	err := l.Enable(l.Get("demo"), "codex", false)
	if err == nil {
		t.Fatal("坏 Codex 配置应返回错误")
	}
	if !strings.Contains(err.Error(), "sk-secret") {
		t.Errorf("返回给界面的原始错误文案应保持不变：%v", err)
	}

	got := l.Diagnostics(DiagEnv{})
	rawLog, _ := os.ReadFile(eventsFile(appDir(home)))

	// Then 快照和日志里都不应出现密钥原文或主目录绝对路径
	for _, leak := range []string{"sk-secret", "api_key", home} {
		if strings.Contains(got, leak) {
			t.Errorf("诊断快照不应含 %q：\n%s", leak, got)
		}
		if strings.Contains(string(rawLog), leak) {
			t.Errorf("事件日志不应含 %q：\n%s", leak, string(rawLog))
		}
	}
	if !strings.Contains(got, "~/.codex/config.toml error(parse error)") {
		t.Errorf("Codex 配置解析错误应只记种类：%s", got)
	}
	if !strings.Contains(got, "native=error(") || !strings.Contains(got, "~/") {
		t.Errorf("native-skills.json 读失败应脱敏主目录：%s", got)
	}
}

func TestDiagSnapshotAndLogAppStartAreRaceFreeWhileLibraryMutates(t *testing.T) {
	home, h := nativeHome(t)
	t.Setenv("HOME", home)
	must(t, os.MkdirAll(h(".claude"), 0o755))
	must(t, os.WriteFile(h(".claude/settings.json"), []byte("{}\n"), 0o600))
	l := Scan(home)

	// 在界面线程提取只读快照
	snap := l.DiagSnapshot()
	startSnap := l.AppStartSnapshot()

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for range 5 {
			got := snap.Format(DiagEnv{Version: "0.1.0", Build: "dev"})
			if !strings.Contains(got, "Skill Stow diagnostics v1") {
				t.Errorf("后台格式化诊断失败：\n%s", got)
			}
			startSnap.Log(DiagEnv{Version: "0.1.0", Build: "dev"})
		}
	}()

	// 主线程同时改动 Library 的 native map、Enabled 切片和 Skills 列表
	s := l.Get("demo")
	for i := range 5 {
		must(t, l.Enable(s, "claude-code", i%2 == 0))
		l.native["codex"] = &nativeConfig{file: h(".codex/config.toml")}
		s.Enabled = append(s.Enabled, "extra")
		l.Agents = append(l.Agents, Agent{ID: "tmp"})
	}
	wg.Wait()
}

func TestOversizedEventIsTruncatedAndReadEventsSkipsCorruptLongLine(t *testing.T) {
	dir := t.TempDir()

	// Given 一条超过单条上限和 1MB Scanner 上限的超长事件
	huge := strings.Repeat("x", 2<<20)
	appendEvent(dir, Event{
		Level: LevelError,
		Cat:   "test",
		Op:    "giant",
		Msg:   huge,
		Data:  map[string]string{"payload": huge},
	})

	fi, err := os.Stat(eventsFile(dir))
	if err != nil {
		t.Fatalf("应写入截断后的事件：%v", err)
	}
	if fi.Size() > maxEventBytes+1 {
		t.Fatalf("单条事件超过上限 %d：got %d", maxEventBytes, fi.Size())
	}

	// 再手工在日志文件里塞一条超过 1MB 的坏行，后面跟一条正常事件
	f, err := os.OpenFile(eventsFile(dir), os.O_APPEND|os.O_WRONLY, 0o644)
	must(t, err)
	_, err = f.WriteString(`{"t":"2026-01-01T00:00:00Z","lvl":"error","cat":"bad","op":"long","msg":"` + strings.Repeat("y", 1500<<10) + `"}` + "\n")
	must(t, err)
	must(t, f.Close())

	appendEvent(dir, Event{Level: LevelInfo, Cat: "test", Op: "after-long", Msg: "ok"})

	// Then 超长事件被截断保留，且手工超长坏行之后的正常事件仍能读出
	got := readEvents(dir)
	var sawGiant, sawAfter bool
	for _, e := range got {
		if e.Op == "giant" {
			sawGiant = true
			if !strings.HasSuffix(e.Msg, "…") {
				t.Errorf("超长 Msg 应被截断：%d", len(e.Msg))
			}
		}
		if e.Op == "after-long" && e.Msg == "ok" {
			sawAfter = true
		}
	}
	if !sawGiant || !sawAfter {
		t.Errorf("应同时读出截断事件和超长坏行之后的事件：sawGiant=%v sawAfter=%v (total=%d)", sawGiant, sawAfter, len(got))
	}
}
