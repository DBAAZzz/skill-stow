package library

import (
	"os"
	"testing"
	"time"
)

func TestOpenCodeEnableRejectsWildcardDenyWithoutWriting(t *testing.T) {
	home, h := nativeHome(t)
	t.Setenv("HOME", home)
	src := `{"permission":{"skill":{"*":"deny"}}}`
	file := h(".config/opencode/opencode.json")
	must(t, os.WriteFile(file, []byte(src), 0600))
	before, err := os.Stat(file)
	must(t, err)
	l := Scan(home)
	s := l.Get("demo")
	if l.Enable(s, "opencode", true) == nil {
		t.Error("enable reported success despite wildcard deny")
	}
	after, err := os.Stat(file)
	must(t, err)
	b, err := os.ReadFile(file)
	must(t, err)
	if string(b) != src || !os.SameFile(before, after) {
		t.Error("ineffective enable wrote the config")
	}
	if s.EnabledIn("opencode") || s.State("opencode").Kind != StateFailed || len(l.nativeData.Restart) != 0 {
		t.Fatalf("incorrect failed-enable state: %+v", s.State("opencode"))
	}
	if _, err := os.Stat(l.nativeFile()); !os.IsNotExist(err) {
		t.Fatalf("failed enable persisted native data: %v", err)
	}
}

func TestFailedEnablePreservesOtherSkillRestart(t *testing.T) {
	home, h := nativeHome(t)
	t.Setenv("HOME", home)
	// Keep an old Codex session present without depending on the host's processes.
	must(t, os.MkdirAll(h("bin"), 0755))
	must(t, os.WriteFile(h("bin/ps"), []byte("#!/bin/sh\nprintf '%s\\n' 'Wed Nov 11 11:00:00 2020 /usr/local/bin/codex'\n"), 0755))
	t.Setenv("PATH", h("bin")+string(os.PathListSeparator)+os.Getenv("PATH"))
	writeSkill(t, h(".agents/skills/second"), "")
	src := "[[skills.config]]\nname = 'second'\nenabled = false\n"
	must(t, os.WriteFile(h(".codex/config.toml"), []byte(src), 0600))
	l := Scan(home)
	first, second := l.Get("demo"), l.Get("second")
	must(t, l.Enable(first, "codex", false))
	before := first.State("codex")
	if before.Kind != StateRestart {
		t.Fatal(before)
	}
	if l.Enable(second, "codex", true) == nil {
		t.Fatal("expected existing user selector to block enabling")
	}
	after := first.State("codex")
	if after.Kind != StateRestart || after.Message != before.Message || after.Enabled {
		t.Fatalf("another skill's failure erased restart: %+v", after)
	}
	if second.State("codex").Kind != StateFailed {
		t.Fatal("failure was not attached to the requested skill")
	}
}

func TestRestartDetectsNodeAgentScriptsAfterOptions(t *testing.T) {
	written := time.Date(2026, 11, 11, 12, 0, 0, 0, time.Local)
	for _, tt := range []struct {
		id, command string
		want        bool
	}{
		{"gemini", "node /Users/me/.nvm/versions/node/v22.22.0/bin/gemini", true},
		{"gemini", "node --no-warnings /Users/me/.nvm/versions/node/v22.22.0/bin/gemini", true},
		{"gemini", "node --inspect /opt/@google/gemini-cli/dist/index.js", true},
		{"codex", "node --no-warnings /opt/bin/codex", true},
		{"codex", "node --inspect /opt/@openai/codex/bin/codex.js", true},
		{"opencode", "node --no-warnings /opt/bin/opencode", true},
		{"opencode", "node --inspect /opt/opencode-ai/bin/opencode.js", true},
		{"gemini", "node /tmp/gemini-notes.js", false},
		{"gemini", "vim /opt/bin/gemini", false},
		{"codex", "node /tmp/codex-notes.js", false},
	} {
		t.Run(tt.command, func(t *testing.T) {
			if got := needsRestart(tt.id, written, "Wed Nov 11 11:00:00 2026 "+tt.command); got != tt.want {
				t.Fatalf("needsRestart = %v, want %v", got, tt.want)
			}
			if needsRestart(tt.id, written, "Wed Nov 11 13:00:00 2026 "+tt.command) {
				t.Fatal("new session should not need restart")
			}
		})
	}
}

func TestGeminiArrayEditsPreserveFormattingAndComments(t *testing.T) {
	for _, src := range []string{
		"{\n  \"skills\": {\n    \"disabled\": [\n      // user choice\n      \"other\", /* keep trailing comma */\n    ]\n  }\n}\n",
		"{\n  \"skills\": {\n    \"disabled\": [\n      \"other\" // keep this note\n    ]\n  }\n}\n",
		"{\r\n  \"skills\": {\r\n    \"disabled\": [\r\n      \"oth\\u0065r\",\r\n    ]\r\n  }\r\n}\r\n",
		`{"skills":{"disabled":["other" /* keep */]}}`,
		"{\n  \"skills\": {\"disabled\": [\n    // keep empty list note\n  ]}\n}\n",
	} {
		t.Run(src, func(t *testing.T) {
			home, h := nativeHome(t)
			t.Setenv("HOME", home)
			file := h(".gemini/settings.json")
			must(t, os.WriteFile(file, []byte(src), 0600))
			l := Scan(home)
			s := l.Get("demo")
			must(t, l.Enable(s, "gemini", false))
			if s.EnabledIn("gemini") {
				t.Fatal("disable did not take effect")
			}
			must(t, l.Enable(s, "gemini", true))
			b, err := os.ReadFile(file)
			must(t, err)
			if string(b) != src {
				t.Fatalf("array round trip changed user text:\n%s", b)
			}
		})
	}
}

func TestGeminiEnableRemovesMatchingElementsOnly(t *testing.T) {
	home, h := nativeHome(t)
	t.Setenv("HOME", home)
	src := "{\n  \"skills\": {\n    \"disabled\": [\n      \"DEMO\",\n      // keep other spelling\n      \"oth\\u0065r\", /* keep note */\n      \"demo\"\n    ]\n  }\n}\n"
	want := "{\n  \"skills\": {\n    \"disabled\": [\n      // keep other spelling\n      \"oth\\u0065r\" /* keep note */\n    ]\n  }\n}\n"
	file := h(".gemini/settings.json")
	must(t, os.WriteFile(file, []byte(src), 0600))
	l := Scan(home)
	must(t, l.Enable(l.Get("demo"), "gemini", true))
	b, err := os.ReadFile(file)
	must(t, err)
	if string(b) != want || !l.Get("demo").EnabledIn("gemini") {
		t.Fatalf("changed unrelated array elements:\n%s", b)
	}
}

func TestCodexUnrelatedLegalTOMLDoesNotBlockToggle(t *testing.T) {
	home, h := nativeHome(t)
	t.Setenv("HOME", home)
	src := "model = \"\"\"line one \\\n  line two\"\"\"\nfuture_value = '\"not parsed\"'\n[\"future.section\"]\nx = true\n[future.section]\nx = false\n[[skills.config]]\nname = 'second'\nenabled = false\n"
	file := h(".codex/config.toml")
	must(t, os.WriteFile(file, []byte(src), 0600))
	l := Scan(home)
	s := l.Get("demo")
	if s.State("codex").Kind == StateUnknown {
		t.Fatal(s.State("codex"))
	}
	must(t, l.Enable(s, "codex", false))
	must(t, l.Enable(s, "codex", true))
	b, err := os.ReadFile(file)
	must(t, err)
	if string(b) != src {
		t.Fatal("unrelated TOML changed", string(b))
	}
}
