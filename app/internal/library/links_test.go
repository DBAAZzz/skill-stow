package library

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

// linkHome 是一个空主目录，装了 agents 里的 Agent（目录里放一个 skills 以外的文件）。
func linkHome(t *testing.T, agents ...string) (home string, h func(string) string) {
	t.Helper()
	home, _ = filepath.EvalSymlinks(t.TempDir())
	h = func(p string) string { return filepath.Join(home, p) }
	for _, id := range agents {
		a, _ := agentByID(nil, id)
		must(t, os.MkdirAll(h(a.Home), 0o755))
		must(t, os.WriteFile(filepath.Join(h(a.Home), "config.json"), []byte("{}"), 0o644))
	}
	return home, h
}

func writeSkill(t *testing.T, dir, body string) {
	t.Helper()
	must(t, os.MkdirAll(dir, 0o755))
	must(t, os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte("---\nname: "+filepath.Base(dir)+"\ndescription: x\n---\n"+body), 0o644))
}

func TestStoppingOneAgentKeepsTheLinkAnotherAgentGotFromIt(t *testing.T) {
	// Given 技能只以 ~/.claude/skills/foo → ~/dev/foo 存在
	home, h := linkHome(t, "claude-code", "windsurf")
	writeSkill(t, h("dev/foo"), "")
	must(t, os.MkdirAll(h(".claude/skills"), 0o755))
	mustLink(t, h("dev/foo"), h(".claude/skills/foo"))
	l := Scan(home)
	foo := l.ByID("mine:foo")

	// When 对 Windsurf 启用，再停用 Claude Code
	must(t, l.AddToAgent(foo, "windsurf"))
	must(t, l.Enable(foo, "claude-code", false))

	// Then Windsurf 的链接仍然通到技能本身
	if _, err := os.Stat(h(".codeium/windsurf/skills/foo/SKILL.md")); err != nil {
		t.Fatalf("停用 Claude Code 后 Windsurf 的链接断了：%v", err)
	}
	again := Scan(home).ByID("mine:foo")
	if again == nil || !slices.Equal(again.Enabled, []string{"windsurf"}) {
		t.Errorf("重新扫描后应只有 Windsurf 启用：%+v", again)
	}
}

func TestLinkStaysValidWhenTheAgentDirIsItselfALink(t *testing.T) {
	// Given dotfiles 管理：~/.codeium/windsurf/skills 是指向 ~/dotfiles/windsurf-skills 的链接
	home, h := linkHome(t, "windsurf")
	writeSkill(t, h(".agents/skills/foo"), "")
	must(t, os.MkdirAll(h("dotfiles/windsurf-skills"), 0o755))
	mustLink(t, h("dotfiles/windsurf-skills"), h(".codeium/windsurf/skills"))
	l := Scan(home)

	// When 对 Windsurf 启用
	must(t, l.AddToAgent(l.ByID("mine:foo"), "windsurf"))

	// Then 链接能读到技能
	if _, err := os.Stat(h(".codeium/windsurf/skills/foo/SKILL.md")); err != nil {
		t.Fatalf("Agent 目录是链接时，建的相对链接解析错了：%v", err)
	}
}

func TestTidyNeverRemovesTheSkillLibraryEntryForAgentsWhoseOwnDirIsTheLibrary(t *testing.T) {
	// Given 装了 Cline（自己的目录就是技能库），技能库里那一项是 ~/dev/foo 的链接
	home, h := linkHome(t, "cline", "cursor")
	writeSkill(t, h("dev/foo"), "")
	must(t, os.MkdirAll(h(".agents/skills"), 0o755))
	mustLink(t, h("dev/foo"), h(".agents/skills/foo"))
	l := Scan(home)
	foo := l.ByID("mine:foo")

	// Then 它不是多余的链接，整理里不列
	if len(foo.Redundant()) != 0 || len(l.Problems().Extra) != 0 {
		t.Fatalf("技能库里那一项被当成了多余的链接：%v", foo.Redundant())
	}
	// When 清理，或者单独停用 Cline
	must(t, l.Tidy(foo))
	if err := l.Enable(foo, "cline", false); err == nil {
		t.Error("Cline 尚未适配原生停用")
	}
	// Then 技能库里那一项还在，读技能库的都还读到
	if _, err := os.Lstat(h(".agents/skills/foo")); err != nil {
		t.Fatalf("技能库里的技能被删了：%v", err)
	}
	if !slices.Equal(foo.Enabled, []string{"cursor", "cline"}) || foo.Via["cline"] == "" {
		t.Errorf("Enabled %v Via %v", foo.Enabled, foo.Via)
	}
}

func TestNativeDisableCanControlAnIndependentCopy(t *testing.T) {
	// Given ~/.agents/skills/foo 和 ~/.claude/skills/foo 是两份独立的拷贝；
	// OpenCode 先读 ~/.claude/skills，Cursor 先读技能库
	home, h := linkHome(t, "claude-code", "cursor", "opencode")
	writeSkill(t, h(".agents/skills/foo"), "新版")
	writeSkill(t, h(".claude/skills/foo"), "旧版")
	l := Scan(home)
	foo := l.ByID("mine:foo")

	// Each Agent reports its own native state, including independent copies.
	if !slices.Equal(foo.Enabled, []string{"claude-code", "cursor", "opencode"}) {
		t.Errorf("启用于 %v，应包含读到独立副本的 Agent", foo.Enabled)
	}
	if !foo.CopyIn("claude-code") || !foo.CopyIn("opencode") || foo.CopyIn("cursor") {
		t.Errorf("读到另一份的：%v", foo.Copied())
	}

	// When 停用 Claude Code
	err := l.Enable(foo, "claude-code", false)

	// Then 原生配置停用，但那份拷贝原样不动
	if err != nil || foo.EnabledIn("claude-code") {
		t.Errorf("应按名称停用另一份而不修改文件：%v", err)
	}
	if b, _ := os.ReadFile(h(".claude/skills/foo/SKILL.md")); !slices.Contains([]string{"旧版"}, string(b[len(b)-len("旧版"):])) {
		t.Errorf("Claude Code 那份被改了：%s", b)
	}
}

func TestScanListsLoopingLinksAsBrokenInsteadOfCrashing(t *testing.T) {
	// Given ~/.claude/skills 里有两条互相指向的链接
	home, h := linkHome(t, "claude-code")
	dir := h(".claude/skills")
	must(t, os.MkdirAll(dir, 0o755))
	if err := os.Symlink("b", filepath.Join(dir, "a")); err != nil {
		t.Skipf("这个平台建不了悬空的符号链接：%v", err)
	}
	must(t, os.Symlink("a", filepath.Join(dir, "b")))

	// When 扫描
	l := Scan(home)

	// Then 两条都列为坏链接
	want := []string{filepath.Join(dir, "a"), filepath.Join(dir, "b")}
	got := slices.Sorted(slices.Values(l.Broken))
	if !slices.Equal(got, want) {
		t.Errorf("坏链接应为 %v，得到 %v", want, got)
	}
}
