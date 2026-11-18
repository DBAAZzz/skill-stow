package library

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// usageHome 在 fakeHome 上加会话记录：Claude Code 两个会话，Codex 一个。
func usageHome(t *testing.T) string {
	t.Helper()
	home := fakeHome(t)
	h := func(p string) string { return filepath.Join(home, p) }
	write := func(file string, lines ...string) {
		t.Helper()
		must(t, os.MkdirAll(filepath.Dir(file), 0o755))
		must(t, os.WriteFile(file, []byte(strings.Join(lines, "\n")+"\n"), 0o644))
	}
	tool := func(at, cwd, skill string) string {
		return `{"type":"assistant","timestamp":"` + at + `","cwd":"` + jsonStr(cwd) + `","message":{"content":[{"type":"tool_use","name":"Skill","input":{"skill":"` + skill + `"}}]}}`
	}
	slash := func(at, name string) string {
		return `{"type":"user","timestamp":"` + at + `","cwd":"` + jsonStr(home) + `","message":{"content":"<command-message>` + name + `</command-message>\n<command-name>/` + name + `</command-name>"}}`
	}
	proj := h("code/storefront")
	write(h(".claude/projects/a/1.jsonl"),
		`{"type":"summary"}`,
		`{"type":"user","timestamp":"2026-10-01T08:00:00Z","message":{"content":"你好"}}`,
		tool("2026-10-01T08:01:00Z", home, "test-first"),
		tool("2026-10-02T08:00:00Z", home, "pua:pua"),
		// 工具结果里引用了别的调用：不算
		`{"type":"user","timestamp":"2026-10-02T08:01:00Z","message":{"content":[{"type":"tool_result","content":"\"name\":\"Skill\",\"input\":{\"skill\":\"spark\"} <command-name>/spark</command-name>"}]}}`,
		slash("2026-10-03T08:00:00Z", "model"),
	)
	write(h(".claude/projects/b/2.jsonl"),
		// 手动 /test-first 之后模型再载入它：只算一次
		slash("2026-10-05T08:00:00Z", "test-first"),
		tool("2026-10-05T08:00:01Z", home, "test-first"),
		tool("2026-10-06T08:00:00Z", filepath.Join(proj, "src"), "notes-sync"),
	)
	write(h(".codex/sessions/2026/10/07/s.jsonl"),
		`{"timestamp":"2026-10-07T08:00:00Z","type":"session_meta","payload":{"cwd":"`+jsonStr(proj)+`","instructions":"技能清单 ~/.agents/skills/spark/SKILL.md"}}`,
		`{"timestamp":"2026-10-07T08:00:01Z","type":"response_item","payload":{"type":"message","role":"developer","content":"`+jsonStr(h(".agents/skills/spark/SKILL.md"))+`"}}`,
		`{"timestamp":"2026-10-07T08:01:00Z","type":"response_item","payload":{"type":"custom_tool_call","name":"exec","input":"tools.exec_command({cmd:\"cat `+jsonStr(h(".agents/skills/test-first/SKILL.md"))+`\"})"}}`,
		`{"timestamp":"2026-10-07T08:02:00Z","type":"response_item","payload":{"type":"custom_tool_call","name":"exec","input":"cat ~/.agents/skills/test-first/SKILL.md && ls ~/.agents/skills/*/SKILL.md"}}`,
		`{"timestamp":"2026-10-07T08:03:00Z","type":"response_item","payload":{"type":"function_call","arguments":"cat /opt/codex/skills/spark/SKILL.md"}}`,
	)
	return home
}

func TestUsageCountsSkillCallsFromSessionLogs(t *testing.T) {
	home := usageHome(t)
	l := Scan(home)
	if l.HasUsage() || l.Unused(l.Get("spark")) {
		t.Fatal("记录还没读时不知道用没用过")
	}
	l.SetUsage(ReadUsage(home))

	u := l.Usage(l.Get("test-first"))
	if u.Count != 3 || u.By["claude-code"] != 2 || u.By["codex"] != 1 {
		t.Errorf("test-first：Claude Code 2 次（手动后再载入只算一次）、Codex 1 次（一个会话一次），得到 %+v", u)
	}
	if got := u.Last.UTC().Format("01-02"); got != "10-07" {
		t.Errorf("上次调用 = %s，应为 10-07", got)
	}
	if n := l.Usage(l.ByID("builtin:" + filepath.Join(home, ".claude/plugins/cache/m/pua/1.0/skills/pua"))).Count; n != 1 {
		t.Errorf("「插件:技能」应记在内置技能上，得到 %d", n)
	}
	var docs *Skill
	for _, s := range l.Skills {
		if s.Source == Project && s.Name == "notes-sync" {
			docs = s
		}
	}
	if n := l.Usage(docs).Count; n != 1 {
		t.Errorf("在项目里调用的应记在项目技能上，得到 %d", n)
	}
	if got := l.UsageSince().UTC().Format("01-02"); got != "10-01" {
		t.Errorf("统计从 %s 起，应为 10-01", got)
	}
}

func TestUnusedListsOnlySkillsWhoseUsageIsKnown(t *testing.T) {
	home := usageHome(t)
	l := Scan(home)
	l.SetUsage(ReadUsage(home))

	// spark 只在工具结果、系统提示和别处的同名目录里出现过：没用过
	if n := l.Usage(l.Get("spark")).Count; n != 0 {
		t.Errorf("spark 没有被调用过，得到 %d", n)
	}
	var names []string
	for _, s := range l.Skills {
		if l.Unused(s) {
			names = append(names, s.Name)
		}
	}
	// test-first-old 只有 Cursor 读得到，Cursor 没有记录：不知道，不列
	if got := strings.Join(names, " "); got != "spark" {
		t.Errorf("没用过 = %q，应只有 spark", got)
	}
	if l.UsageTracked(l.Get("test-first-old")) {
		t.Error("只被没有记录的 Agent 读到的技能，用没用过不知道")
	}
}

func TestUsageKeepsCountsAfterTheAgentCleansOldSessions(t *testing.T) {
	home := usageHome(t)
	ReadUsage(home)
	must(t, os.Remove(filepath.Join(home, ".claude/projects/a/1.jsonl")))
	// 还在写的会话接着读
	f, err := os.OpenFile(filepath.Join(home, ".claude/projects/b/2.jsonl"), os.O_APPEND|os.O_WRONLY, 0)
	must(t, err)
	f.WriteString(`{"type":"assistant","timestamp":"2026-10-09T08:00:00Z","message":{"content":[{"type":"tool_use","name":"Skill","input":{"skill":"spark"}}]}}` + "\n")
	must(t, f.Close())

	l := Scan(home)
	l.SetUsage(ReadUsage(home))
	if n := l.Usage(l.Get("test-first")).Count; n != 3 {
		t.Errorf("会话文件被清掉后次数应还在，得到 %d", n)
	}
	if n := l.Usage(l.Get("spark")).Count; n != 1 {
		t.Errorf("变了的会话文件应重读，spark 得到 %d", n)
	}
}

func TestUsedRanksSkillsByCallsThenRecency(t *testing.T) {
	home := usageHome(t)
	l := Scan(home)
	l.SetUsage(ReadUsage(home))

	var names []string
	for _, s := range l.Used() {
		names = append(names, s.Name)
	}
	// test-first 3 次；notes-sync（10-06）和 pua（10-02）各 1 次，近的在前
	if got := strings.Join(names, " "); got != "test-first notes-sync pua" {
		t.Errorf("用过的 = %q", got)
	}
}

func TestDailyCountsCallsPerDayAcrossWeeks(t *testing.T) {
	home := usageHome(t)
	l := Scan(home)
	l.SetUsage(ReadUsage(home))

	// now 是 2026-10-08（周四），取 2 周 = 14 天：09-28（周一）到 10-11（周日）
	now := time.Date(2026, 10, 8, 12, 0, 0, 0, time.Local)
	days := l.Daily(now, 2)
	if len(days) != 14 {
		t.Fatalf("应有 14 天，得到 %d", len(days))
	}
	if got := days[0].Date.Format("01-02"); got != "09-28" {
		t.Fatalf("第一天应为周一 09-28，得到 %s", got)
	}
	// 记录从 10-01 起：09-28..09-30 没有记录，不是没调用
	for i := 0; i < 3; i++ {
		if !days[i].Untraced {
			t.Errorf("%s 在统计开始之前，应标为 Untraced", days[i].Date.Format("01-02"))
		}
	}
	if days[3].Untraced {
		t.Error("10-01 有记录，不应标为 Untraced")
	}
	// 10-01 是第 3 天（idx 3）：test-first (claude-code)
	if got := days[3].By["claude-code"]; got != 1 {
		t.Errorf("10-01 claude-code = %d，应为 1", got)
	}
	// 10-07 是第 9 天（idx 9）：Codex test-first
	if got := days[9].By["codex"]; got != 1 {
		t.Errorf("10-07 codex = %d，应为 1", got)
	}
	// 10-08 是今天（idx 10），不标 Future；10-09..10-11（idx 11..13）标 Future
	if days[10].Future {
		t.Error("今天不应标为 Future")
	}
	for i := 11; i < 14; i++ {
		if !days[i].Future {
			t.Errorf("第 %d 天 (%s) 应标为 Future", i, days[i].Date.Format("01-02"))
		}
	}
}
