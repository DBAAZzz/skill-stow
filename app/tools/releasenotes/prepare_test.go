package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func makeGitLogRecord(sha, header, body string) string {
	return sha + "\x1f" + header + "\x1f" + body + "\x1e\n"
}

func TestComputePlan(t *testing.T) {
	t.Run("creates tag when bumped version has no tag yet", func(t *testing.T) {
		res, err := computePlan("0.3.0", "v0.2.0", false, "")
		if err != nil {
			t.Fatalf("computePlan() error = %v", err)
		}
		if res.Action != "tag" || res.Tag != "v0.3.0" {
			t.Errorf("got %+v, want action=tag tag=v0.3.0", res)
		}
	})

	t.Run("rejects untagged version not newer than latest tag", func(t *testing.T) {
		_, err := computePlan("0.2.0", "v0.2.0", false, "")
		if err == nil || !strings.Contains(err.Error(), "not newer") {
			t.Fatalf("expected not newer error, got %v", err)
		}
	})

	t.Run("noop when only non-release commits landed", func(t *testing.T) {
		raw := makeGitLogRecord("11111111", "ci(release): automate notes", "") +
			makeGitLogRecord("22222222", "docs(agents): update guide", "") +
			makeGitLogRecord("33333333", "chore: bump husky", "")
		res, err := computePlan("0.2.0", "v0.2.0", true, raw)
		if err != nil {
			t.Fatalf("computePlan() error = %v", err)
		}
		if res.Action != "noop" {
			t.Errorf("got %+v, want action=noop", res)
		}
	})

	t.Run("bumps minor for feat commits and orders feat before fix", func(t *testing.T) {
		raw := makeGitLogRecord("aaaa1111", "fix(ui): align icon", "") +
			makeGitLogRecord("bbbb2222", "feat(browse): list uncommitted skills", "") +
			makeGitLogRecord("cccc3333", "docs(readme): add screenshot", "")
		res, err := computePlan("0.2.0", "v0.2.0", true, raw)
		if err != nil {
			t.Fatalf("computePlan() error = %v", err)
		}
		if res.Action != "pr" || res.NextVersion != "0.3.0" || res.Tag != "v0.3.0" {
			t.Fatalf("got %+v, want pr 0.3.0", res)
		}
		if len(res.Commits) != 2 {
			t.Fatalf("got %d commits, want 2", len(res.Commits))
		}
		if res.Commits[0].Type != "feat" || res.Commits[1].Type != "fix" {
			t.Errorf("unexpected commit order: %+v", res.Commits)
		}
	})

	t.Run("bumps patch for fix only", func(t *testing.T) {
		raw := makeGitLogRecord("aaaa1111", "fix(update): retry timeout", "")
		res, err := computePlan("0.2.0", "v0.2.0", true, raw)
		if err != nil {
			t.Fatalf("computePlan() error = %v", err)
		}
		if res.Action != "pr" || res.NextVersion != "0.2.1" {
			t.Errorf("got %+v, want pr 0.2.1", res)
		}
	})

	t.Run("breaking bumps minor on 0.x and major on 1.x", func(t *testing.T) {
		raw := makeGitLogRecord("aaaa1111", "refactor(api)!: rename flag", "")
		res0, err := computePlan("0.2.0", "v0.2.0", true, raw)
		if err != nil || res0.NextVersion != "0.3.0" {
			t.Fatalf("0.x breaking got %+v, err=%v; want 0.3.0", res0, err)
		}
		rawFooter := makeGitLogRecord("bbbb2222", "fix(api): drop legacy", "details\n\nBREAKING CHANGE: removed old flag")
		res1, err := computePlan("1.2.3", "v1.2.3", true, rawFooter)
		if err != nil || res1.NextVersion != "2.0.0" {
			t.Fatalf("1.x breaking got %+v, err=%v; want 2.0.0", res1, err)
		}
	})
}

func TestUpdateMygoVersionAndPrependChangelogDraft(t *testing.T) {
	dir := t.TempDir()
	mygoPath := writeTemp(t, dir, "mygo.json", `{
  "name": "Skill Stow",
  "slug": "skill-stow",
  "version": "0.2.0",
  "updates": {
    "github": "DBAAZzz/skill-stow"
  }
}
`)
	changelogPath := writeTemp(t, dir, "CHANGELOG.md", `# Changelog

每个版本一节。

## 0.2.0

中文

1. 旧版本。

English

1. Older release.
`)
	tmplPath := writeTemp(t, dir, "tmpl.md", sampleTemplate)
	commits := parseGitLog(
		makeGitLogRecord("ae1c74f0", "feat(browse): list uncommitted project skills in the toolbar", "") +
			makeGitLogRecord("e3221240", "feat(library): read git state of project skills", ""),
	)

	if err := updateMygoVersion(mygoPath, "0.3.0"); err != nil {
		t.Fatalf("updateMygoVersion() error = %v", err)
	}
	gotMygo, err := os.ReadFile(mygoPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(gotMygo), `"version": "0.3.0",`) || !strings.Contains(string(gotMygo), `"slug": "skill-stow",`) {
		t.Errorf("unexpected mygo.json:\n%s", gotMygo)
	}

	if err := prependChangelogDraft(changelogPath, "0.3.0", commits); err != nil {
		t.Fatalf("prependChangelogDraft() error = %v", err)
	}
	gotChangelog, err := os.ReadFile(changelogPath)
	if err != nil {
		t.Fatal(err)
	}
	text := string(gotChangelog)
	idxNew := strings.Index(text, "## 0.3.0")
	idxOld := strings.Index(text, "## 0.2.0")
	if idxNew < 0 || idxOld < 0 || idxNew > idxOld {
		t.Fatalf("new section should appear before 0.2.0:\n%s", text)
	}

	// 新生成的草稿应能直接通过 renderNotes 校验。
	if _, _, err := renderNotes(renderOptions{
		version:       "0.3.0",
		changelogPath: changelogPath,
		templatePath:  tmplPath,
		dmgName:       "skill-stow-0.3.0.dmg",
		dmgSHA256:     strings.Repeat("a", 64),
	}); err != nil {
		t.Fatalf("generated changelog draft failed renderNotes(): %v\n%s", err, text)
	}

	// 再次插入同版本应报错而不是重复写入。
	if err := prependChangelogDraft(changelogPath, "0.3.0", commits); err == nil {
		t.Fatal("expected duplicate version error")
	}

	body := renderPRBody("0.3.0", "v0.2.0", commits)
	for _, want := range []string{"0.3.0", "v0.2.0", "ae1c74f", "docs/releases/v0.3.0.md"} {
		if !strings.Contains(body, want) {
			t.Errorf("PR body missing %q:\n%s", want, body)
		}
	}
	if strings.ContainsAny(body, "·•‧⋅・") {
		t.Errorf("PR body should not contain dot separators:\n%s", body)
	}
	_ = filepath.Base(mygoPath)
}
