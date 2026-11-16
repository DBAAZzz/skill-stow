package library

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func nativeHome(t *testing.T) (string, func(string) string) {
	home, h := linkHome(t, "claude-code", "codex", "gemini", "opencode", "kiro")
	must(t, os.WriteFile(h(".gemini/settings.json"), []byte("{}\n"), 0600))
	writeSkill(t, h(".agents/skills/demo"), "unchanged")
	must(t, os.MkdirAll(h(".claude/skills"), 0755))
	mustLink(t, h(".agents/skills/demo"), h(".claude/skills/demo"))
	return home, h
}
func TestNativeAdaptersRoundTrip(t *testing.T) {
	for _, id := range []string{"claude-code", "codex", "gemini", "opencode"} {
		t.Run(id, func(t *testing.T) {
			home, h := nativeHome(t)
			t.Setenv("HOME", home)
			l := Scan(home)
			s := l.Get("demo")
			before, _ := os.ReadFile(h(".agents/skills/demo/SKILL.md"))
			if !s.EnabledIn(id) {
				t.Fatal(s.State(id))
			}
			must(t, l.Enable(s, id, false))
			if s.EnabledIn(id) || !s.DiscoveredIn(id) {
				t.Fatalf("disable lost discovery: %+v", s.State(id))
			}
			link, err := os.Readlink(h(".claude/skills/demo"))
			if err != nil || link != h(".agents/skills/demo") {
				t.Fatal("link changed")
			}
			l = Scan(home)
			s = l.Get("demo")
			if s.EnabledIn(id) {
				t.Fatal("disable not persistent")
			}
			must(t, l.Enable(s, id, true))
			if !s.EnabledIn(id) {
				t.Fatal(s.State(id))
			}
			after, _ := os.ReadFile(h(".agents/skills/demo/SKILL.md"))
			if string(before) != string(after) {
				t.Fatal("skill modified")
			}
			for _, other := range []string{"claude-code", "codex", "gemini", "opencode"} {
				if !s.EnabledIn(other) {
					t.Fatal("changed other Agent", other)
				}
			}
		})
	}
}
func TestNativeRestoreAndSymlink(t *testing.T) {
	home, h := nativeHome(t)
	t.Setenv("HOME", home)
	src := "{\n  \"theme\": \"dark\",\n  \"skillOverrides\": {\"demo\": \"user-invocable-only\"}\n}\n"
	must(t, os.WriteFile(h("settings-real.json"), []byte(src), 0640))
	requireFileSymlink(t, h("settings-real.json"), h(".claude/settings.json"))
	l := Scan(home)
	must(t, l.Enable(l.Get("demo"), "claude-code", false))
	l = Scan(home)
	must(t, l.Enable(l.Get("demo"), "claude-code", true))
	b, _ := os.ReadFile(h("settings-real.json"))
	if string(b) != src {
		t.Fatalf("original not restored: %s", b)
	}
	if !isLink(h(".claude/settings.json")) {
		t.Fatal("replaced symlink")
	}
	fi, _ := os.Stat(h("settings-real.json"))
	if fi.Mode().Perm() != 0640 {
		t.Fatal("mode changed")
	}
}
func TestNativeMalformedNeverOverwrites(t *testing.T) {
	for id, file := range map[string]string{"claude-code": ".claude/settings.json", "codex": ".codex/config.toml", "gemini": ".gemini/settings.json", "opencode": ".config/opencode/opencode.json"} {
		t.Run(id, func(t *testing.T) {
			home, h := nativeHome(t)
			t.Setenv("HOME", home)
			must(t, os.WriteFile(h(file), []byte("broken config"), 0600))
			l := Scan(home)
			s := l.Get("demo")
			if s.State(id).Kind != StateUnknown {
				t.Fatal(s.State(id))
			}
			if l.Enable(s, id, false) == nil {
				t.Fatal("should fail")
			}
			b, _ := os.ReadFile(h(file))
			if string(b) != "broken config" {
				t.Fatal("overwritten")
			}
		})
	}
}
func TestGeminiMergesUserAndProjectAndPreservesOtherNames(t *testing.T) {
	home, h := nativeHome(t)
	t.Setenv("HOME", home)
	must(t, os.WriteFile(h(".gemini/settings.json"), []byte(`{"skills":{"disabled":["other"]}}`), 0600))
	must(t, os.MkdirAll(h("code/project/.gemini"), 0755))
	project := `{"skills":{"disabled":["DEMO"]}}`
	must(t, os.WriteFile(h("code/project/.gemini/settings.json"), []byte(project), 0600))
	l := Scan(home)
	must(t, l.AddProjects([]string{h("code/project")}))
	l = Scan(home)
	s := l.Get("demo")
	must(t, l.Enable(s, "gemini", false))
	must(t, l.Enable(s, "gemini", true))
	if !s.EnabledIn("gemini") || len(s.State("gemini").Projects) != 1 {
		t.Fatal(s.State("gemini"))
	}
	b, _ := os.ReadFile(h("code/project/.gemini/settings.json"))
	if string(b) != project {
		t.Fatal("project changed")
	}
	b, _ = os.ReadFile(h(".gemini/settings.json"))
	if strings.Contains(strings.ToLower(string(b)), "demo") || !strings.Contains(string(b), "other") {
		t.Fatal(string(b))
	}
}
func TestOpenCodeUsesFrontmatterAndRejectsUnknownShape(t *testing.T) {
	home, h := nativeHome(t)
	t.Setenv("HOME", home)
	must(t, os.WriteFile(h(".agents/skills/demo/SKILL.md"), []byte("---\nname: actual-id\ndescription: x\n---\n"), 0644))
	must(t, os.WriteFile(h(".config/opencode/opencode.jsonc"), []byte("{\n // keep\n \"permission\": {\"skill\": {\"*\": \"allow\"}}\n}"), 0600))
	l := Scan(home)
	s := l.Get("demo")
	must(t, l.Enable(s, "opencode", false))
	b, _ := os.ReadFile(h(".config/opencode/opencode.jsonc"))
	if !strings.Contains(string(b), `"actual-id": "deny"`) || !strings.Contains(string(b), "// keep") {
		t.Fatal(string(b))
	}
	must(t, l.Enable(s, "opencode", true))
	must(t, os.WriteFile(h(".config/opencode/opencode.jsonc"), []byte(`{"permission":[{"skill":"allow"}]}`), 0600))
	l = Scan(home)
	if l.Get("demo").State("opencode").Kind != StateUnknown {
		t.Fatal("unknown format accepted")
	}
}
func TestNativeExistingDisabledAndUserTOMLRemain(t *testing.T) {
	home, h := nativeHome(t)
	t.Setenv("HOME", home)
	src := "[[skills.config]]\nname = \"other\"\nenabled = false\n"
	must(t, os.WriteFile(h(".codex/config.toml"), []byte(src), 0600))
	l := Scan(home)
	s := l.Get("demo")
	must(t, l.Enable(s, "codex", false))
	must(t, l.Enable(s, "codex", true))
	b, _ := os.ReadFile(h(".codex/config.toml"))
	if string(b) != src {
		t.Fatal(string(b))
	}
	must(t, os.WriteFile(h(".claude/settings.json"), []byte(`{"skillOverrides":{"demo":"off"}}`), 0600))
	l = Scan(home)
	s = l.Get("demo")
	must(t, l.Enable(s, "claude-code", false))
	if len(l.nativeData.Restore) != 0 {
		t.Fatal("invented restore record")
	}
	must(t, l.Enable(s, "claude-code", true))
	if !s.EnabledIn("claude-code") {
		t.Fatal("could not enable existing disabled skill")
	}
}
func TestAddRemoveOnlyOwnedLinks(t *testing.T) {
	home, h := nativeHome(t)
	t.Setenv("HOME", home)
	l := Scan(home)
	s := l.Get("demo")
	if l.Enable(s, "kiro", true) == nil {
		t.Fatal("toggle built a link")
	}
	must(t, l.AddToAgent(s, "kiro"))
	if !l.CanRemoveFromAgent(s, "kiro") {
		t.Fatal("missing owned link")
	}
	must(t, l.RemoveFromAgent(s, "kiro"))
	if _, err := os.Lstat(h(".kiro/skills/demo")); !os.IsNotExist(err) {
		t.Fatal(err)
	}
	if l.RemoveFromAgent(s, "claude-code") == nil {
		t.Fatal("removed unowned link")
	}
}
func TestRestartOnlyOldAgentProcesses(t *testing.T) {
	now := time.Date(2026, 11, 11, 12, 0, 0, 0, time.Local)
	text := "Wed Nov 11 11:00:00 2026 /usr/local/bin/codex exec\nWed Nov 11 13:00:00 2026 /usr/local/bin/opencode\nWed Nov 11 10:00:00 2026 /usr/bin/vim /tmp/codex.txt\n"
	if !needsRestart("codex", now, text) || needsRestart("opencode", now, text) || needsRestart("gemini", now, text) {
		t.Fatal("process start detection")
	}
	_ = filepath.Separator
}

func TestNativeSharedParentsCleanupAcrossSkills(t *testing.T) {
	for _, id := range []string{"claude-code", "gemini", "opencode"} {
		t.Run(id, func(t *testing.T) {
			home, h := nativeHome(t)
			t.Setenv("HOME", home)
			writeSkill(t, h(".agents/skills/second"), "")
			mustLink(t, h(".agents/skills/second"), h(".claude/skills/second"))
			l := Scan(home)
			file := l.configPath(id)
			src := "{\n  \"keep\": true\n}\n"
			must(t, os.WriteFile(file, []byte(src), 0600))
			l = Scan(home)
			must(t, l.Enable(l.Get("demo"), id, false))
			must(t, l.Enable(l.Get("second"), id, false))
			must(t, l.Enable(l.Get("demo"), id, true))
			must(t, l.Enable(l.Get("second"), id, true))
			b, _ := os.ReadFile(file)
			if string(b) != src {
				t.Fatalf("owned empty parents remain: %s", b)
			}
		})
	}
}
func TestCodexCanonicalPathAndExistingSelectorProtection(t *testing.T) {
	home, h := nativeHome(t)
	t.Setenv("HOME", home)
	must(t, os.MkdirAll(h(".codex/skills"), 0755))
	mustLink(t, h(".agents/skills/demo"), h(".codex/skills/demo"))
	l := Scan(home)
	s := l.Get("demo")
	must(t, l.Enable(s, "codex", false))
	b, _ := os.ReadFile(h(".codex/config.toml"))
	if strings.Count(string(b), "[[skills.config]]") != 1 || !strings.Contains(string(b), jsonString(h(".agents/skills/demo/SKILL.md"))) {
		t.Fatal(string(b))
	}
	must(t, l.Enable(s, "codex", true))
	src := "[[skills.config]]\npath = " + jsonString(h(".codex/skills/demo/SKILL.md")) + "\nenabled = false\n"
	must(t, os.WriteFile(h(".codex/config.toml"), []byte(src), 0600))
	l = Scan(home)
	s = l.Get("demo")
	if s.EnabledIn("codex") {
		t.Fatal("symlink selector ignored")
	}
	if l.Enable(s, "codex", true) == nil {
		t.Fatal("must not delete user block")
	}
	b, _ = os.ReadFile(h(".codex/config.toml"))
	if string(b) != src {
		t.Fatal("user block changed")
	}
}
func TestNativeReReadsBeforeWriteAndRefreshesAfterRepair(t *testing.T) {
	home, h := nativeHome(t)
	t.Setenv("HOME", home)
	l := Scan(home)
	s := l.Get("demo")
	must(t, os.WriteFile(h(".claude/settings.json"), []byte(`{"newPreference":42}`), 0600))
	must(t, l.Enable(s, "claude-code", false))
	b, _ := os.ReadFile(h(".claude/settings.json"))
	if !strings.Contains(string(b), `"newPreference":42`) {
		t.Fatal("stale snapshot overwritten")
	}
	must(t, os.WriteFile(h(".claude/settings.json"), []byte(`broken`), 0600))
	if l.Enable(s, "claude-code", true) == nil {
		t.Fatal("accepted broken config")
	}
	must(t, os.WriteFile(h(".claude/settings.json"), []byte(`{}`), 0600))
	must(t, l.Enable(s, "claude-code", true))
	if !s.EnabledIn("claude-code") || s.State("claude-code").Kind == StateFailed {
		t.Fatal("repair retry left stale state", s.State("claude-code"))
	}
}
func TestSameNameImpactIncludesProjectAndUsesNativeName(t *testing.T) {
	home, h := nativeHome(t)
	t.Setenv("HOME", home)
	writeSkill(t, h("code/project/.claude/skills/alias"), "")
	must(t, os.WriteFile(h("code/project/.claude/skills/alias/SKILL.md"), []byte("---\nname: demo\ndescription: same name\n---\n"), 0644))
	l := Scan(home)
	must(t, l.AddProjects([]string{h("code/project")}))
	l = Scan(home)
	impacts := l.NameImpacts(l.Get("demo"), "claude-code")
	if len(impacts) != 1 || impacts[0] != h("code/project/.claude/skills/alias") {
		t.Fatal(impacts)
	}
	if len(l.NameImpacts(l.Get("demo"), "codex")) != 0 {
		t.Fatal("Codex name impact")
	}
}
func TestOpenCodeLaterWildcardAndV2AreNotOverwritten(t *testing.T) {
	for _, src := range []string{`{"permission":{"skill":{"demo":"allow","*":"allow"}}}`, `{"permissions":[{"action":"skill","resource":"*","effect":"allow"}]}`} {
		home, h := nativeHome(t)
		t.Setenv("HOME", home)
		file := h(".config/opencode/opencode.json")
		must(t, os.WriteFile(file, []byte(src), 0600))
		l := Scan(home)
		if l.Enable(l.Get("demo"), "opencode", false) == nil {
			t.Fatal("should not write ineffective/unknown rule")
		}
		b, _ := os.ReadFile(file)
		if string(b) != src {
			t.Fatal("config changed")
		}
	}
}

func TestCodexExplicitEnabledBlockIsRestoredUntouched(t *testing.T) {
	home, h := nativeHome(t)
	t.Setenv("HOME", home)
	src := "# user choice\n[[skills.config]]\npath = " + jsonString(h(".agents/skills/demo/SKILL.md")) + "\nenabled = true\n"
	must(t, os.WriteFile(h(".codex/config.toml"), []byte(src), 0600))
	l := Scan(home)
	s := l.Get("demo")
	must(t, l.Enable(s, "codex", false))
	if s.EnabledIn("codex") {
		t.Fatal("explicit enabled selector not disabled")
	}
	must(t, l.Enable(s, "codex", true))
	b, _ := os.ReadFile(h(".codex/config.toml"))
	if string(b) != src {
		t.Fatal("user selector changed", string(b))
	}
}

func TestNativeWriteFailureKeepsConfigAndOtherAgents(t *testing.T) {
	home, h := nativeHome(t)
	t.Setenv("HOME", home)
	file := h(".claude/settings.json")
	src := "{\"keep\":1}\n"
	must(t, os.WriteFile(file, []byte(src), 0600))
	l := Scan(home)
	s := l.Get("demo")
	chmodReadOnly(t, h(".claude"))
	defer os.Chmod(h(".claude"), 0700)
	if l.Enable(s, "claude-code", false) == nil {
		t.Fatal("expected write permission error")
	}
	b, _ := os.ReadFile(file)
	if string(b) != src || !s.EnabledIn("claude-code") || !s.EnabledIn("codex") || s.State("claude-code").Kind != StateFailed {
		t.Fatal("failure changed state or content", s.State("claude-code"))
	}
}

func TestBatchResultsKeepEarlierAgentFailure(t *testing.T) {
	home, h := nativeHome(t)
	t.Setenv("HOME", home)
	l := Scan(home)
	s := l.Get("demo")
	chmodReadOnly(t, h(".claude"))
	defer os.Chmod(h(".claude"), 0700)
	if l.Enable(s, "claude-code", false) == nil {
		t.Fatal("expected failure")
	}
	must(t, l.Enable(s, "codex", false))
	if s.State("claude-code").Kind != StateFailed || !s.State("claude-code").Enabled || s.EnabledIn("codex") {
		t.Fatal("lost per-Agent result", s.States)
	}
}
func TestNativeDoesNotClaimRecreatedUserParent(t *testing.T) {
	home, h := nativeHome(t)
	t.Setenv("HOME", home)
	l := Scan(home)
	s := l.Get("demo")
	must(t, l.Enable(s, "claude-code", false))
	must(t, l.Enable(s, "claude-code", true))
	src := "{\n  \"skillOverrides\": {}\n}\n"
	must(t, os.WriteFile(h(".claude/settings.json"), []byte(src), 0600))
	must(t, l.Enable(s, "claude-code", false))
	must(t, l.Enable(s, "claude-code", true))
	b, _ := os.ReadFile(h(".claude/settings.json"))
	if !strings.Contains(string(b), `"skillOverrides"`) {
		t.Fatal("removed user's parent")
	}
}

func TestOpenCodeAmbiguousFilesStayUnknown(t *testing.T) {
	home, h := nativeHome(t)
	t.Setenv("HOME", home)
	must(t, os.WriteFile(h(".config/opencode/opencode.json"), []byte(`{"permission":{"skill":{"demo":"deny"}}}`), 0600))
	must(t, os.WriteFile(h(".config/opencode/opencode.jsonc"), []byte(`{}`), 0600))
	l := Scan(home)
	if l.Get("demo").State("opencode").Kind != StateUnknown {
		t.Fatal("ignored another config file")
	}
	if l.Enable(l.Get("demo"), "opencode", false) == nil {
		t.Fatal("wrote ambiguous config")
	}
}

func TestFailedEnableKeepsDisabledAgentVisible(t *testing.T) {
	home, h := nativeHome(t)
	t.Setenv("HOME", home)
	l := Scan(home)
	s := l.Get("demo")
	must(t, l.Enable(s, "claude-code", false))
	chmodReadOnly(t, h(".claude"))
	defer os.Chmod(h(".claude"), 0700)
	if l.Enable(s, "claude-code", true) == nil {
		t.Fatal("expected failure")
	}
	if !s.DiscoveredIn("claude-code") || s.EnabledIn("claude-code") || s.State("claude-code").Kind != StateFailed {
		t.Fatal("failed enable lost original state", s.State("claude-code"))
	}
}

func TestGeminiRestrictionInKnownProjectWithoutLocalSkills(t *testing.T) {
	home, h := nativeHome(t)
	t.Setenv("HOME", home)
	project := h("code/empty-project")
	must(t, os.MkdirAll(filepath.Join(project, ".gemini"), 0755))
	must(t, os.WriteFile(filepath.Join(project, ".gemini/settings.json"), []byte(`{"skills":{"disabled":["demo"]}}`), 0600))
	must(t, os.WriteFile(h(".claude.json"), []byte(`{"projects":{`+jsonString(project)+`:{}}}`), 0600))
	l := Scan(home)
	if len(l.Get("demo").State("gemini").Projects) != 1 {
		t.Fatal("missed known project without local skills")
	}
}
