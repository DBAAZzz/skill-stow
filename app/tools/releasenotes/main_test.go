package main

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

var itoa = strconv.Itoa

const sampleTemplate = `## 中文

{{.ZH}}

### 安装

下载 ` + "`{{.DMGName}}`" + `。

---

## English

{{.EN}}

### Install

Download ` + "`{{.DMGName}}`" + `.

---

SHA-256 (` + "`{{.DMGName}}`" + `): ` + "`{{.DMGSHA256}}`" + `
`

const sampleChangelog = `# Changelog

## [0.3.0] - 2026-11-20

中文

1. 新增功能甲。
2. 修复问题乙。

English

1. Added feature A.
2. Fixed bug B.

## 0.2.0

中文

1. 旧版本内容。

English

1. Older release notes.
`

func TestRenderNotesFromChangelog(t *testing.T) {
	dir := t.TempDir()
	changelog := writeTemp(t, dir, "CHANGELOG.md", sampleChangelog)
	tmpl := writeTemp(t, dir, "release-notes.tmpl.md", sampleTemplate)
	dmgContent := []byte("fake-dmg-payload")
	dmg := writeTemp(t, dir, "skill-stow-0.3.0.dmg", string(dmgContent))
	wantSum := sha256.Sum256(dmgContent)

	got, source, err := renderNotes(renderOptions{
		version:       "0.3.0",
		changelogPath: changelog,
		overridePath:  filepath.Join(dir, "docs/releases/v0.3.0.md"), // 不存在，应回退到 CHANGELOG
		templatePath:  tmpl,
		dmgPath:       dmg,
	})
	if err != nil {
		t.Fatalf("renderNotes() error = %v", err)
	}
	if source != changelog {
		t.Errorf("source = %q, want %q", source, changelog)
	}
	for _, want := range []string{
		"## 中文\n\n1. 新增功能甲。\n2. 修复问题乙。",
		"下载 `skill-stow-0.3.0.dmg`。",
		"## English\n\n1. Added feature A.\n2. Fixed bug B.",
		"Download `skill-stow-0.3.0.dmg`.",
		"SHA-256 (`skill-stow-0.3.0.dmg`): `" + hex.EncodeToString(wantSum[:]) + "`",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("rendered output missing %q\n--- got ---\n%s", want, got)
		}
	}
	if strings.Contains(got, "旧版本内容") {
		t.Errorf("rendered output leaked older version notes:\n%s", got)
	}
}

func TestRenderNotesUsesOverrideWhenPresent(t *testing.T) {
	dir := t.TempDir()
	changelog := writeTemp(t, dir, "CHANGELOG.md", sampleChangelog)
	tmpl := writeTemp(t, dir, "release-notes.tmpl.md", sampleTemplate)
	override := writeTemp(t, dir, "v0.3.0.md", `## 中文

### 新功能

- 详细说明甲。

`+"```"+`
English
`+"```"+`

---

## English

### New

- Detailed note A.
`)

	got, source, err := renderNotes(renderOptions{
		version:       "v0.3.0",
		changelogPath: changelog,
		overridePath:  override,
		templatePath:  tmpl,
		dmgName:       "skill-stow-0.3.0.dmg",
		dmgSHA256:     "abc123",
	})
	if err != nil {
		t.Fatalf("renderNotes() error = %v", err)
	}
	if source != override {
		t.Errorf("source = %q, want %q", source, override)
	}
	if !strings.Contains(got, "### 新功能\n\n- 详细说明甲。") {
		t.Errorf("override Chinese body not used:\n%s", got)
	}
	if !strings.Contains(got, "### New\n\n- Detailed note A.") {
		t.Errorf("override English body not used:\n%s", got)
	}
	if strings.Contains(got, "新增功能甲") {
		t.Errorf("changelog body should be replaced by override:\n%s", got)
	}
	if strings.Contains(got, "---\n\n### 安装") {
		t.Errorf("trailing --- from override Chinese section should be stripped:\n%s", got)
	}
}

func TestRenderNotesStillRequiresChangelogSectionWhenOverrideExists(t *testing.T) {
	dir := t.TempDir()
	changelog := writeTemp(t, dir, "CHANGELOG.md", sampleChangelog)
	tmpl := writeTemp(t, dir, "release-notes.tmpl.md", sampleTemplate)
	override := writeTemp(t, dir, "v0.9.0.md", "## 中文\n\n内容\n\n## English\n\nBody\n")

	_, _, err := renderNotes(renderOptions{
		version:       "0.9.0",
		changelogPath: changelog,
		overridePath:  override,
		templatePath:  tmpl,
		dmgName:       "skill-stow-0.9.0.dmg",
		dmgSHA256:     "abc123",
	})
	if err == nil || !strings.Contains(err.Error(), "## 0.9.0") {
		t.Fatalf("expected missing changelog section error, got %v", err)
	}
}

func TestSplitBilingualErrors(t *testing.T) {
	cases := []struct {
		name string
		md   string
		want string
	}{
		{"missing zh", "## English\n\nHello", `missing "中文"`},
		{"missing en", "## 中文\n\n你好", `missing "English"`},
		{"empty zh", "中文\n\n---\n\nEnglish\n\nHello", `"中文" section is empty`},
		{"empty en", "中文\n\n你好\n\nEnglish\n", `"English" section is empty`},
		{"duplicate zh", "## 中文\nA\n## 中文\nB\n## English\nC", "duplicate Chinese"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, _, err := splitBilingual(tc.md)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("splitBilingual() error = %v, want substring %q", err, tc.want)
			}
		})
	}
}

func TestSplitBilingualRespectsFenceTypeAndLength(t *testing.T) {
	md := "## 中文\n\n````markdown\n~~~\nEnglish\n~~~\n```\nEnglish\n```\n````\n\n## English\n\nReal English body.\n"
	zh, en, err := splitBilingual(md)
	if err != nil {
		t.Fatalf("splitBilingual() error = %v", err)
	}
	if !strings.Contains(zh, "~~~\nEnglish\n~~~") || !strings.Contains(zh, "```\nEnglish\n```") {
		t.Errorf("code block containing English was prematurely closed:\n%s", zh)
	}
	if en != "Real English body." {
		t.Errorf("en = %q, want %q", en, "Real English body.")
	}
}

func TestResolveDMGRejectsConflictingFlags(t *testing.T) {
	_, _, err := resolveDMG(renderOptions{
		dmgPath: "/tmp/a.dmg",
		dmgName: "a.dmg",
	})
	if err == nil || !strings.Contains(err.Error(), "cannot be combined") {
		t.Fatalf("expected conflicting flags error, got %v", err)
	}
}

func TestVerifyAssets(t *testing.T) {
	dir := t.TempDir()
	manifestContent := `{
  "version": "0.3.0",
  "url": "https://github.com/DBAAZzz/skill-stow/releases/download/v0.3.0/skill-stow-0.3.0-darwin-universal.tar.gz",
  "size": 2000,
  "signature": "sig-archive",
  "deltas": [
    {
      "from": "0.2.0",
      "url": "https://github.com/DBAAZzz/skill-stow/releases/download/v0.3.0/skill-stow-0.2.0-to-0.3.0-darwin-universal.delta",
      "size": 500,
      "signature": "sig-delta"
    }
  ],
  "previous": [
    {
      "version": "0.2.0",
      "url": "https://github.com/DBAAZzz/skill-stow/releases/download/v0.2.0/skill-stow-0.2.0-darwin-universal.tar.gz",
      "size": 1900,
      "signature": "sig-prev"
    }
  ]
}`
	manifest := writeTemp(t, dir, "update-darwin-universal.json", manifestContent)
	manifestSize := len(manifestContent)

	makeAssets := func(extra string) []byte {
		return []byte(`{"assets":[` +
			`{"name":"update-darwin-universal.json","size":` + itoa(manifestSize) + `,"state":"uploaded"},` +
			`{"name":"skill-stow-0.3.0.dmg","size":1500,"state":"uploaded"},` +
			`{"name":"SHA256SUMS","size":120,"state":"uploaded"},` +
			extra +
			`]}`)
	}

	goodAssets := makeAssets(`{"name":"skill-stow-0.3.0-darwin-universal.tar.gz","size":2000,"state":"uploaded"},{"name":"skill-stow-0.2.0-to-0.3.0-darwin-universal.delta","size":500,"state":"uploaded"}`)
	if err := verifyAssets("0.3.0", "skill-stow-0.3.0.dmg", manifest, goodAssets); err != nil {
		t.Fatalf("verifyAssets() unexpected error: %v", err)
	}

	t.Run("missing delta asset", func(t *testing.T) {
		missingDelta := makeAssets(`{"name":"skill-stow-0.3.0-darwin-universal.tar.gz","size":2000,"state":"uploaded"}`)
		err := verifyAssets("0.3.0", "skill-stow-0.3.0.dmg", manifest, missingDelta)
		if err == nil || !strings.Contains(err.Error(), "skill-stow-0.2.0-to-0.3.0-darwin-universal.delta") {
			t.Fatalf("expected missing delta error, got %v", err)
		}
	})

	t.Run("archive size mismatch", func(t *testing.T) {
		wrongSize := makeAssets(`{"name":"skill-stow-0.3.0-darwin-universal.tar.gz","size":1999,"state":"uploaded"},{"name":"skill-stow-0.2.0-to-0.3.0-darwin-universal.delta","size":500,"state":"uploaded"}`)
		err := verifyAssets("0.3.0", "skill-stow-0.3.0.dmg", manifest, wrongSize)
		if err == nil || !strings.Contains(err.Error(), "size is 1999") {
			t.Fatalf("expected size mismatch error, got %v", err)
		}
	})

	t.Run("stale manifest size", func(t *testing.T) {
		staleManifest := []byte(`{
  "assets": [
    {"name": "update-darwin-universal.json", "size": 12, "state": "uploaded"},
    {"name": "skill-stow-0.3.0.dmg", "size": 1500, "state": "uploaded"},
    {"name": "SHA256SUMS", "size": 120, "state": "uploaded"},
    {"name": "skill-stow-0.3.0-darwin-universal.tar.gz", "size": 2000, "state": "uploaded"},
    {"name": "skill-stow-0.2.0-to-0.3.0-darwin-universal.delta", "size": 500, "state": "uploaded"}
  ]
}`)
		err := verifyAssets("0.3.0", "skill-stow-0.3.0.dmg", manifest, staleManifest)
		if err == nil || !strings.Contains(err.Error(), "update-darwin-universal.json size is 12") {
			t.Fatalf("expected stale manifest size error, got %v", err)
		}
	})

	t.Run("asset not uploaded", func(t *testing.T) {
		starter := []byte(`{
  "assets": [
    {"name": "update-darwin-universal.json", "size": ` + itoa(manifestSize) + `, "state": "starter"},
    {"name": "skill-stow-0.3.0.dmg", "size": 1500, "state": "uploaded"},
    {"name": "SHA256SUMS", "size": 120, "state": "uploaded"},
    {"name": "skill-stow-0.3.0-darwin-universal.tar.gz", "size": 2000, "state": "uploaded"},
    {"name": "skill-stow-0.2.0-to-0.3.0-darwin-universal.delta", "size": 500, "state": "uploaded"}
  ]
}`)
		err := verifyAssets("0.3.0", "skill-stow-0.3.0.dmg", manifest, starter)
		if err == nil || !strings.Contains(err.Error(), `state is "starter"`) {
			t.Fatalf("expected state error, got %v", err)
		}
	})

	t.Run("missing signature", func(t *testing.T) {
		unsigned := writeTemp(t, dir, "unsigned.json", `{
  "version": "0.3.0",
  "url": "https://github.com/DBAAZzz/skill-stow/releases/download/v0.3.0/skill-stow-0.3.0-darwin-universal.tar.gz",
  "size": 2000
}`)
		err := verifyAssets("0.3.0", "skill-stow-0.3.0.dmg", unsigned, goodAssets)
		if err == nil || !strings.Contains(err.Error(), "missing size or signature") {
			t.Fatalf("expected missing signature error, got %v", err)
		}
	})

	t.Run("wrong release tag in url", func(t *testing.T) {
		wrongTag := writeTemp(t, dir, "wrongtag.json", `{
  "version": "0.3.0",
  "url": "https://github.com/DBAAZzz/skill-stow/releases/download/v0.2.0/skill-stow-0.3.0-darwin-universal.tar.gz",
  "size": 2000,
  "signature": "sig-archive"
}`)
		err := verifyAssets("0.3.0", "skill-stow-0.3.0.dmg", wrongTag, goodAssets)
		if err == nil || !strings.Contains(err.Error(), "does not target tag v0.3.0") {
			t.Fatalf("expected wrong tag error, got %v", err)
		}
	})

	t.Run("version mismatch", func(t *testing.T) {
		err := verifyAssets("0.4.0", "skill-stow-0.4.0.dmg", manifest, goodAssets)
		if err == nil || !strings.Contains(err.Error(), `manifest version "0.3.0"`) {
			t.Fatalf("expected version mismatch error, got %v", err)
		}
	})
}

// TestRepoReleaseNotesIntegrity 守护仓库里真实的模板、CHANGELOG.md 和可选的 docs/releases/v*.md，
// 保证日常 PR 的 go test ./... 就能提前拦住发版说明格式错误。
func TestRepoReleaseNotesIntegrity(t *testing.T) {
	changelogPath := filepath.Join("..", "..", "CHANGELOG.md")
	templatePath := filepath.Join("..", "..", "..", ".github", "release-notes.tmpl.md")

	changelogBytes, err := os.ReadFile(changelogPath)
	if err != nil {
		t.Fatalf("read %s: %v", changelogPath, err)
	}
	versions := listChangelogVersions(string(changelogBytes))
	if len(versions) == 0 {
		t.Fatalf("%s has no ## <version> sections", changelogPath)
	}
	for _, v := range versions {
		if _, _, err := renderNotes(renderOptions{
			version:       v,
			changelogPath: changelogPath,
			templatePath:  templatePath,
			dmgName:       "skill-stow-" + v + ".dmg",
			dmgSHA256:     strings.Repeat("0", 64),
		}); err != nil {
			t.Errorf("CHANGELOG.md section %s failed to render: %v", v, err)
		}
	}

	overrides, err := filepath.Glob(filepath.Join("..", "..", "..", "docs", "releases", "v*.md"))
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range overrides {
		b, err := os.ReadFile(path)
		if err != nil {
			t.Errorf("read %s: %v", path, err)
			continue
		}
		if _, _, err := splitBilingual(string(b)); err != nil {
			t.Errorf("%s failed bilingual check: %v", path, err)
		}
	}
}

func writeTemp(t *testing.T, dir, name, content string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}
