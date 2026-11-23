package library

import (
	"slices"
	"testing"
)

func TestParseSkillMD(t *testing.T) {
	md := ParseSkillMD("---\nname: demo\ndescription: 做一件事\n---\n\n# demo\n\n## 用法\n")
	if md.Name != "demo" || md.Description != "做一件事" {
		t.Fatalf("frontmatter = %q / %q", md.Name, md.Description)
	}
	if md.Body != "## 用法\n" {
		t.Fatalf("正文应去掉 frontmatter 和同名 H1，得到 %q", md.Body)
	}
	if got := ParseSkillMD("# 没有 frontmatter\n").Body; got != "# 没有 frontmatter\n" {
		t.Fatalf("没有 frontmatter 时原样返回，得到 %q", got)
	}
}

func TestSampleReadsSkillDirectory(t *testing.T) {
	s := Sample().Get("test-first")
	if s.Files[0].Path != "SKILL.md" || len(s.Files) != 4 {
		t.Fatalf("files = %v", s.Files)
	}
	if s.Description == "" {
		t.Fatal("简介应取自 SKILL.md 的 frontmatter")
	}
}

func TestScopes(t *testing.T) {
	l := Sample()
	all := l.Count(ScopeAll)
	if sum := l.Count(ScopeMine) + l.Count(ScopeBuiltin) + l.Count(ScopeProject); sum != all {
		t.Errorf("三个来源之和 %d ≠ 全部 %d", sum, all)
	}
	if got := l.Find(ScopeAll, "VUE"); len(got) != 1 || got[0].Name != "vue-best-practices" {
		t.Errorf("搜索应不区分大小写，得到 %v", got)
	}
}

func TestSetEnabled(t *testing.T) {
	l := Sample()
	s := l.Get("code-review")
	s.SetEnabled("workbuddy", true)
	s.SetEnabled("codebuddy", true)
	if want := []string{"claude-code", "codex", "cursor", "codebuddy", "workbuddy"}; !slices.Equal(s.Enabled, want) {
		t.Errorf("启用后应按 Agent 顺序排列：%v", s.Enabled)
	}
	s.SetEnabled("claude-code", false)
	if s.EnabledIn("claude-code") {
		t.Error("停用后仍启用")
	}
	ro := l.Get("pdf")
	ro.SetEnabled("cursor", true)
	if ro.EnabledIn("cursor") {
		t.Error("只读 Skill 不能启用")
	}
}

func TestSampleShared(t *testing.T) {
	l := Sample()
	if !slices.Equal(l.SharedReaders(), []string{"codex", "cursor"}) {
		t.Fatalf("SharedReaders = %v", l.SharedReaders())
	}
	s := l.Get("test-first")
	// Claude Code 和 CodeBuddy 目录里是另一份拷贝，读到的不是这一份
	if !s.Shared || len(l.Separate(s)) != 2 || !s.CopyIn("claude-code") || !s.CopyIn("codebuddy") {
		t.Errorf("Separate = %v Copied = %v", l.Separate(s), s.Copied())
	}
	if err := l.Enable(s, "codex", false); err != nil || s.EnabledIn("codex") || !s.DiscoveredIn("codex") {
		t.Errorf("通用 Skill 可原生停用并保持可发现：%v", err)
	}
	if err := l.Tidy(s); err != nil || len(s.Redundant()) != 0 {
		t.Errorf("Tidy 后不应再有多余链接：%v %v", err, s.Redundant())
	}
	other := l.Get("tailwind-rules")
	if other.Shared || !slices.Equal(l.Separate(other), []string{"cursor"}) {
		t.Errorf("不在通用目录的 Skill，Separate 就是 Enabled：%v", l.Separate(other))
	}
}

func TestSampleCollections(t *testing.T) {
	l := Sample()
	var got []string
	for _, c := range l.Collections {
		got = append(got, c.Name)
	}
	if want := []string{"open.feishu.cn", "anthropics/skills", "DBAAZzz/skills", "document-skills"}; !slices.Equal(got, want) {
		t.Fatalf("集合 = %v，应为 %v（大的在前，同样大按名称）", got, want)
	}
	lark := l.Collections[0]
	if !lark.Shared() || lark.Kind != SetSource || l.Get("lark-doc").Collection != lark {
		t.Errorf("lark-* 应是通用目录里的一个来源集合")
	}
	if got := lark.Preview(3); got != "lark-calendar、lark-doc、lark-im 等" {
		t.Errorf("Preview = %q", got)
	}
	if n := lark.DependedBy(l.Get("lark-meeting")); n != 2 {
		t.Errorf("lark-meeting 被 %d 个 Skill 依赖，应为 2", n)
	}
	if n := lark.DependedBy(l.Get("lark-vc")); n != 0 {
		t.Errorf("lark-vc 只被自己的简介提到，应为 0，得到 %d", n)
	}
	if !l.Collections[3].ReadOnly() || l.Get("skill-creator").Collection != nil {
		t.Error("插件集合只读；不在插件里的内置 Skill 不算集合")
	}
	anthropic := l.Collections[1]
	if anthropic.EnabledIn("claude-code") != 2 || anthropic.EnabledIn("codex") != 3 {
		t.Errorf("anthropics/skills：Claude Code %d/3，Codex %d/3", anthropic.EnabledIn("claude-code"), anthropic.EnabledIn("codex"))
	}
	if err := l.EnableAll(anthropic, "claude-code", true); err == nil || anthropic.EnabledIn("claude-code") != 2 {
		t.Errorf("集合开关应拒绝写入并保持原数量：%v", err)
	}
	if err := l.EnableAll(anthropic, "codex", false); err == nil || anthropic.EnabledIn("codex") != 3 {
		t.Errorf("集合开关应拒绝写入并保持原数量：%v", err)
	}
}

func TestMentions(t *testing.T) {
	for _, c := range []struct {
		text, name string
		want       bool
	}{
		{"交由 lark-meeting 处理", "lark-meeting", true},
		{"指定 lark-vc-agent 时", "lark-vc", false},
		{"lark-vc，", "lark-vc", true},
		{"my-lark-vc", "lark-vc", false},
	} {
		if got := mentions(c.text, c.name); got != c.want {
			t.Errorf("mentions(%q, %q) = %v", c.text, c.name, got)
		}
	}
}
