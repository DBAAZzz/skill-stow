package library

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// bigLock 是 n 条 github 记录的安装记录，vite 在中间；顶层带 npx skills
// 认识而本应用不认识的字段。
func bigLock(n int) string {
	var b strings.Builder
	b.WriteString("{\n  \"version\": 3,\n  \"skills\": {\n")
	for i := 0; i < n; i++ {
		name := fmt.Sprintf("skill-%02d", i)
		if i == n/2 {
			name = "vite"
		}
		fmt.Fprintf(&b, "    %q: {\n      \"source\": \"antfu/skills\",\n      \"sourceType\": \"github\",\n      \"sourceUrl\": \"https://github.com/antfu/skills.git\",\n      \"skillPath\": \"skills/%s/SKILL.md\",\n      \"skillFolderHash\": \"%040d\",\n      \"installedAt\": \"2026-08-20T01:02:03.000Z\",\n      \"updatedAt\": \"2026-08-20T01:02:03.000Z\",\n      \"pluginName\": \"x\"\n    }", name, name, i)
		if i < n-1 {
			b.WriteString(",")
		}
		b.WriteString("\n")
	}
	b.WriteString("  },\n  \"dismissed\": {\"findSkillsPrompt\": true},\n  \"lastSelectedAgents\": [\"claude-code\"]\n}\n")
	return b.String()
}

func TestInstallRecordsAreReadFromXDGStateWhenNpxSkillsKeepsThemThere(t *testing.T) {
	// Given XDG_STATE_HOME 下有安装记录，~/.agents 下另有一份旧的
	home, h := linkHome(t)
	state := h("state")
	t.Setenv("XDG_STATE_HOME", state)
	writeSkill(t, h(".agents/skills/a"), "")
	writeSkill(t, h(".agents/skills/b"), "")
	must(t, os.MkdirAll(filepath.Join(state, "skills"), 0o755))
	must(t, os.WriteFile(filepath.Join(state, "skills", ".skill-lock.json"), []byte(`{"version":3,"skills":{"a":{"source":"o/new","sourceType":"github"},"b":{"source":"o/new","sourceType":"github"}}}`), 0o644))
	must(t, os.WriteFile(h(".agents/.skill-lock.json"), []byte(`{"version":3,"skills":{"a":{"source":"o/old","sourceType":"github"},"b":{"source":"o/old","sourceType":"github"}}}`), 0o644))

	// When 扫描
	l := Scan(home)

	// Then 技能集合按 XDG 那份的来源认
	if c := l.Get("a").Collection; c == nil || c.Name != "o/new" {
		t.Fatalf("集合 = %+v，应来自 XDG_STATE_HOME 的记录", c)
	}
}

func TestInstallRecordsFallBackToAgentsDirWhenXDGHasNone(t *testing.T) {
	home, h := linkHome(t)
	t.Setenv("XDG_STATE_HOME", h("state")) // 设了，但那里没有记录
	if got, want := lockFile(home), h(".agents/.skill-lock.json"); got != want {
		t.Errorf("lockFile = %s，want %s", got, want)
	}
}

func TestDeletingASkillDropsItsRecordFromTheXDGRecordFile(t *testing.T) {
	// Given 记录在 XDG_STATE_HOME
	home, h := deleteHome(t)
	state := h("state")
	t.Setenv("XDG_STATE_HOME", state)
	must(t, os.MkdirAll(filepath.Join(state, "skills"), 0o755))
	must(t, os.Rename(h(".agents/.skill-lock.json"), filepath.Join(state, "skills", ".skill-lock.json")))

	// When 删除 design-lab
	l := Scan(home)
	_, err := l.Delete(l.Get("design-lab"))
	must(t, err)

	// Then XDG 那份里没有它了
	recs, err := ReadInstallRecords(home)
	must(t, err)
	if recs.Get("design-lab") != nil || recs.Get("grill-me") == nil {
		t.Errorf("记录 = %v", recs.Names())
	}
}

func TestReadingInstallRecordsKeepsEachEntrysFields(t *testing.T) {
	home, h := linkHome(t)
	must(t, os.MkdirAll(h(".agents"), 0o755))
	must(t, os.WriteFile(h(".agents/.skill-lock.json"), []byte(bigLock(5)), 0o644))

	recs, err := ReadInstallRecords(home)
	must(t, err)
	if recs.Version != 3 || len(recs.Entries) != 5 {
		t.Fatalf("version %d，%d 条", recs.Version, len(recs.Entries))
	}
	v := recs.Get("vite")
	if v == nil || v.Source != "antfu/skills" || v.SourceType != "github" || v.SkillPath != "skills/vite/SKILL.md" ||
		v.SkillFolderHash != fmt.Sprintf("%040d", 2) || v.InstalledAt.IsZero() {
		t.Errorf("vite = %+v", v)
	}
	if !strings.Contains(v.Raw, `"pluginName": "x"`) {
		t.Errorf("Raw 应是原文：%s", v.Raw)
	}
}

func TestMissingInstallRecordsAreNotAnError(t *testing.T) {
	home, _ := linkHome(t)
	recs, err := ReadInstallRecords(home)
	if err != nil || recs != nil {
		t.Errorf("没有记录文件：recs=%v err=%v", recs, err)
	}
}

func TestUpdatingARecordChangesOnlyItsHashAndTime(t *testing.T) {
	// Given 90 条记录
	home, h := linkHome(t)
	must(t, os.MkdirAll(h(".agents"), 0o755))
	before := bigLock(90)
	must(t, os.WriteFile(h(".agents/.skill-lock.json"), []byte(before), 0o644))
	recs, err := ReadInstallRecords(home)
	must(t, err)
	orig := recs.Get("vite").Raw

	// When 写 vite 的新版本
	newHash := strings.Repeat("a", 40)
	_, err = setRecordVersion(home, "vite", orig, newHash, "2026-11-13T07:00:00.000Z")
	must(t, err)

	// Then 只有这两个值变了，其他逐字节不变
	after, err := os.ReadFile(h(".agents/.skill-lock.json"))
	must(t, err)
	want := strings.Replace(before, `"skillFolderHash": "`+fmt.Sprintf("%040d", 45)+`"`, `"skillFolderHash": "`+newHash+`"`, 1)
	i := strings.Index(want, `"vite"`)
	j := i + strings.Index(want[i:], `"updatedAt": "2026-08-20T01:02:03.000Z"`)
	want = want[:j] + `"updatedAt": "2026-11-13T07:00:00.000Z"` + want[j+len(`"updatedAt": "2026-08-20T01:02:03.000Z"`):]
	if string(after) != want {
		t.Errorf("改动超出 vite 的两个字段：\n%s", firstDiff(want, string(after)))
	}
}

func TestARecordChangedByAnotherProgramIsNotOverwritten(t *testing.T) {
	home, h := linkHome(t)
	must(t, os.MkdirAll(h(".agents"), 0o755))
	must(t, os.WriteFile(h(".agents/.skill-lock.json"), []byte(bigLock(3)), 0o644))
	recs, _ := ReadInstallRecords(home)
	orig := recs.Get("vite").Raw

	// Given 读过之后，npx skills 改了 vite 这一条
	src, _ := os.ReadFile(h(".agents/.skill-lock.json"))
	changed := strings.Replace(string(src), "skills/vite/SKILL.md", "skills/vite2/SKILL.md", 1)
	must(t, os.WriteFile(h(".agents/.skill-lock.json"), []byte(changed), 0o644))

	// When 按读到的原条目写
	_, err := setRecordVersion(home, "vite", orig, strings.Repeat("b", 40), "2026-11-13T07:00:00.000Z")

	// Then 不写，报被改过
	if !errors.Is(err, ErrRecordChanged) {
		t.Fatalf("err = %v，want ErrRecordChanged", err)
	}
	if after, _ := os.ReadFile(h(".agents/.skill-lock.json")); string(after) != changed {
		t.Error("记录被改了")
	}
}

func TestARecordAlreadyAtTheExpectedVersionCountsAsWritten(t *testing.T) {
	home, h := linkHome(t)
	must(t, os.MkdirAll(h(".agents"), 0o755))
	must(t, os.WriteFile(h(".agents/.skill-lock.json"), []byte(bigLock(3)), 0o644))
	recs, _ := ReadInstallRecords(home)
	orig := recs.Get("vite").Raw
	newHash := strings.Repeat("c", 40)
	_, err := setRecordVersion(home, "vite", orig, newHash, "2026-11-13T07:00:00.000Z")
	must(t, err)

	// When 同一次改动再写一遍（崩溃后补写）
	_, err = setRecordVersion(home, "vite", orig, newHash, "2026-11-13T08:00:00.000Z")

	// Then 当作已写入
	must(t, err)
}

func TestUnknownRecordVersionsAreReadButNeverWritten(t *testing.T) {
	home, h := linkHome(t)
	must(t, os.MkdirAll(h(".agents"), 0o755))
	src := strings.Replace(bigLock(3), `"version": 3`, `"version": 4`, 1)
	must(t, os.WriteFile(h(".agents/.skill-lock.json"), []byte(src), 0o644))

	recs, err := ReadInstallRecords(home)
	must(t, err)
	if recs.Version != 4 || recs.Get("vite") == nil {
		t.Fatalf("应能只读：%+v", recs)
	}
	if recs.Writable() == nil {
		t.Error("version 4 不应可写")
	}
	_, err = setRecordVersion(home, "vite", recs.Get("vite").Raw, strings.Repeat("d", 40), "2026-11-13T07:00:00.000Z")
	if !errors.Is(err, ErrRecordUnusable) {
		t.Fatalf("err = %v，want ErrRecordUnusable", err)
	}
	if after, _ := os.ReadFile(h(".agents/.skill-lock.json")); string(after) != src {
		t.Error("version 4 的记录被改了")
	}
}

func firstDiff(a, b string) string {
	for i := 0; i < len(a) && i < len(b); i++ {
		if a[i] != b[i] {
			lo := max(0, i-80)
			return fmt.Sprintf("want …%s…\n got …%s…", a[lo:min(len(a), i+80)], b[lo:min(len(b), i+80)])
		}
	}
	return fmt.Sprintf("长度不同：%d vs %d", len(a), len(b))
}
