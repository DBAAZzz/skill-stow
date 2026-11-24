package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const goodNotes = `中文

1. 可以在应用里直接卸载 Codex 插件
2. 修复插件卸载菜单的图标没对齐

English

1. Uninstall Codex plugins right from the app
2. Fix misaligned icons in the plugin uninstall menu`

const changelogWithDraft = `# Changelog

说明文字。

## 0.6.0

中文

1. feat(browse): uninstall Codex plugins from the app

English

1. feat(browse): uninstall Codex plugins from the app

## 0.5.0

中文

1. 旧版本

English

1. Old release
`

func TestApplyReplacesDraftWithModelNotes(t *testing.T) {
	dir := t.TempDir()
	changelog := filepath.Join(dir, "CHANGELOG.md")
	notes := filepath.Join(dir, "notes.txt")
	writeFile(t, changelog, changelogWithDraft)
	writeFile(t, notes, "Let me check the commits first.\n<release-notes>\n"+goodNotes+"\n</release-notes>\nDone.")

	if err := runApply([]string{"-version", "0.6.0", "-changelog", changelog, "-notes", notes}); err != nil {
		t.Fatalf("runApply() error = %v", err)
	}
	got := readFile(t, changelog)
	section, err := extractChangelogSection(got, "0.6.0")
	if err != nil {
		t.Fatal(err)
	}
	if section != goodNotes {
		t.Errorf("0.6.0 section =\n%s\nwant\n%s", section, goodNotes)
	}
	if old, _ := extractChangelogSection(got, "0.5.0"); old != "中文\n\n1. 旧版本\n\nEnglish\n\n1. Old release" {
		t.Errorf("0.5.0 section changed: %q", old)
	}
	if !strings.HasPrefix(got, "# Changelog\n\n说明文字。\n\n## 0.6.0\n\n中文") || !strings.HasSuffix(got, "1. Old release\n") {
		t.Errorf("surrounding text changed:\n%s", got)
	}
}

func TestApplyLeavesChangelogAloneWhenNotesAreRejected(t *testing.T) {
	cases := map[string]string{
		"no tags":               goodNotes,
		"raw commit headers":    "<release-notes>\n中文\n\n1. feat(browse): 卸载插件\n\nEnglish\n\n1. feat(browse): uninstall plugins\n</release-notes>",
		"english under 中文":      "<release-notes>\n中文\n\n1. Uninstall plugins\n\nEnglish\n\n1. Uninstall plugins\n</release-notes>",
		"chinese in English":    "<release-notes>\n中文\n\n1. 卸载插件\n\nEnglish\n\n1. 卸载插件\n</release-notes>",
		"bullet list":           "<release-notes>\n中文\n\n- 卸载插件\n\nEnglish\n\n- Uninstall plugins\n</release-notes>",
		"item count differs":    "<release-notes>\n中文\n\n1. 卸载插件\n2. 修复图标\n\nEnglish\n\n1. Uninstall plugins\n</release-notes>",
		"numbering skips":       "<release-notes>\n中文\n\n1. 卸载插件\n3. 修复图标\n\nEnglish\n\n1. Uninstall plugins\n3. Fix icons\n</release-notes>",
		"dot separator":         "<release-notes>\n中文\n\n1. 插件 · 卸载\n\nEnglish\n\n1. Plugins · uninstall\n</release-notes>",
		"missing English":       "<release-notes>\n中文\n\n1. 卸载插件\n</release-notes>",
		"chinese item too long": "<release-notes>\n中文\n\n1. " + strings.Repeat("卸", 51) + "\n\nEnglish\n\n1. Uninstall plugins\n</release-notes>",
		"english item too long": "<release-notes>\n中文\n\n1. 卸载插件\n\nEnglish\n\n1. " + strings.Repeat("a", 141) + "\n</release-notes>",
		"two chinese sentences": "<release-notes>\n中文\n\n1. 可以卸载插件。列表会刷新。\n\nEnglish\n\n1. Uninstall plugins\n</release-notes>",
	}
	for name, output := range cases {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			changelog := filepath.Join(dir, "CHANGELOG.md")
			notes := filepath.Join(dir, "notes.txt")
			writeFile(t, changelog, changelogWithDraft)
			writeFile(t, notes, output)

			if err := runApply([]string{"-version", "0.6.0", "-changelog", changelog, "-notes", notes}); err == nil {
				t.Fatal("runApply() accepted bad notes")
			}
			if got := readFile(t, changelog); got != changelogWithDraft {
				t.Errorf("CHANGELOG.md was modified:\n%s", got)
			}
		})
	}
}

func TestApplyFailsWhenVersionSectionIsMissing(t *testing.T) {
	dir := t.TempDir()
	changelog := filepath.Join(dir, "CHANGELOG.md")
	notes := filepath.Join(dir, "notes.txt")
	writeFile(t, changelog, changelogWithDraft)
	writeFile(t, notes, "<release-notes>\n"+goodNotes+"\n</release-notes>")

	err := runApply([]string{"-version", "0.7.0", "-changelog", changelog, "-notes", notes})
	if err == nil || !strings.Contains(err.Error(), "## 0.7.0") {
		t.Fatalf("expected missing section error, got %v", err)
	}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
