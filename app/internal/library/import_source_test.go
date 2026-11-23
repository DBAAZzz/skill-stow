package library

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func TestImportInputRecognisesEveryDocumentedForm(t *testing.T) {
	home, _ := os.UserHomeDir()
	cases := []struct {
		in      string
		kind    ImportKind
		repo    string
		ref     string
		sub     string
		url     string
		skills  []string
		ignored bool
	}{
		{in: "antfu/skills", kind: ImportGitHub, repo: "antfu/skills"},
		{in: "  'antfu/skills'  ", kind: ImportGitHub, repo: "antfu/skills"},
		{in: "github.com/antfu/skills", kind: ImportGitHub, repo: "antfu/skills"},
		{in: "https://github.com/antfu/skills.git", kind: ImportGitHub, repo: "antfu/skills"},
		{in: "antfu/skills@vite", kind: ImportGitHub, repo: "antfu/skills", skills: []string{"vite"}},
		{in: "antfu/skills#dev", kind: ImportGitHub, repo: "antfu/skills", ref: "dev"},
		{in: "antfu/skills/skills/vite", kind: ImportGitHub, repo: "antfu/skills", sub: "skills/vite"},
		{in: "https://github.com/o/r/tree/main/skills/x", kind: ImportGitHub, repo: "o/r", ref: "main", sub: "skills/x"},
		{in: "https://github.com/o/r/blob/main/skills/x/SKILL.md", kind: ImportGitHub, repo: "o/r", ref: "main", sub: "skills/x"},
		{in: "skills.sh/vercel-labs/skills/find-skills", kind: ImportGitHub, repo: "vercel-labs/skills", skills: []string{"find-skills"}},
		{in: "https://skills.sh/vercel-labs/skills/find-skills", kind: ImportGitHub, repo: "vercel-labs/skills", skills: []string{"find-skills"}},
		{in: "npx skills add https://github.com/vercel-labs/skills --skill find-skills -g -y", kind: ImportGitHub,
			repo: "vercel-labs/skills", skills: []string{"find-skills"}, ignored: true},
		{in: "npx skills add vercel-labs/skills --skill find-skills", kind: ImportGitHub, repo: "vercel-labs/skills", skills: []string{"find-skills"}},
		{in: "npx skills add antfu/skills --skill a -s b", kind: ImportGitHub, repo: "antfu/skills", skills: []string{"a", "b"}},
		{in: "npx -y skills@latest add antfu/skills -a claude-code cursor -s vite", kind: ImportGitHub, repo: "antfu/skills", skills: []string{"vite"}, ignored: true},
		{in: "pnpm dlx skills add antfu/skills --skill=vite,vue", kind: ImportGitHub, repo: "antfu/skills", skills: []string{"vite", "vue"}},
		{in: "bunx skills add antfu/skills", kind: ImportGitHub, repo: "antfu/skills"},
		{in: "npx skills add antfu/skills --skill '*'", kind: ImportGitHub, repo: "antfu/skills"},
		{in: "npx skills add --skill vite antfu/skills", kind: ImportGitHub, repo: "antfu/skills", skills: []string{"vite"}},
		{in: "https://mintlify.com/docs", kind: ImportWebsite, url: "https://mintlify.com/docs"},
		{in: "npx skills add https://mintlify.com/docs", kind: ImportWebsite, url: "https://mintlify.com/docs"},
		{in: "mintlify.com/mintlify", kind: ImportWebsite, url: "https://mintlify.com", sub: "mintlify", skills: []string{"mintlify"}},
		{in: "skills.sh/mintlify.com/mintlify", kind: ImportWebsite, url: "https://mintlify.com", skills: []string{"mintlify"}},
		{in: "https://clawhub.ai/awspace/skills/pdf", kind: ImportClawHub, repo: "awspace/pdf", url: "https://clawhub.ai/awspace/skills/pdf"},
		{in: "https://www.clawhub.ai/spclaudehome/skills/skill-vetter/", kind: ImportClawHub, repo: "spclaudehome/skill-vetter", url: "https://clawhub.ai/spclaudehome/skills/skill-vetter"},
		{in: "https://clawhub.ai/awspace/pdf", kind: ImportClawHub, repo: "awspace/pdf", url: "https://clawhub.ai/awspace/skills/pdf"},
		{in: "clawhub.ai/awspace/pdf", kind: ImportClawHub, repo: "awspace/pdf", url: "https://clawhub.ai/awspace/skills/pdf"},
		{in: "clawhub.ai/awspace/skills/pdf", kind: ImportClawHub, repo: "awspace/pdf", url: "https://clawhub.ai/awspace/skills/pdf"},
		{in: "npx skills add https://clawhub.ai/awspace/skills/pdf -g -y", kind: ImportClawHub, repo: "awspace/pdf", url: "https://clawhub.ai/awspace/skills/pdf", ignored: true},
		{in: "npx skills add clawhub.ai/awspace/pdf", kind: ImportClawHub, repo: "awspace/pdf", url: "https://clawhub.ai/awspace/skills/pdf"},
		{in: "https://clawhub.ai/skills-sh/anthropics/skills/pdf", kind: ImportGitHub, repo: "anthropics/skills", skills: []string{"pdf"}},
		{in: "https://gitlab.com/a/b", kind: ImportUnsupported},
		{in: "git@github.com:a/b.git", kind: ImportUnsupported},
	}
	for _, c := range cases {
		got, err := ParseImportInput(c.in)
		if err != nil {
			t.Errorf("%q：%v", c.in, err)
			continue
		}
		if got.Kind != c.kind || got.Repo != c.repo || got.Ref != c.ref || got.Subpath != c.sub || got.URL != c.url ||
			!slices.Equal(got.Skills, c.skills) || got.IgnoredArgs != c.ignored {
			t.Errorf("%q → %+v", c.in, got)
		}
	}

	local, err := ParseImportInput("~/Downloads/release-notes")
	if err != nil || local.Kind != ImportLocal || local.Paths[0] != filepath.Join(home, "Downloads/release-notes") {
		t.Errorf("本机路径 → %+v %v", local, err)
	}
}

func TestImportInputRejectsWhatItCannotRead(t *testing.T) {
	if _, err := ParseImportInput("   "); !errors.Is(err, ErrImportEmpty) {
		t.Errorf("空输入：%v", err)
	}
	for _, in := range []string{
		"vite", "hello world", "npx skills add", "a b/c", "-bad/repo",
		"https://clawhub.ai", "https://clawhub.ai/awspace", "https://clawhub.ai/skills/pdf",
		"https://clawhub.ai/user/awspace", "https://clawhub.ai/a..b/skills/pdf",
	} {
		if got, err := ParseImportInput(in); !errors.Is(err, ErrImportUnknown) {
			t.Errorf("%q 应认不出，得到 %+v %v", in, got, err)
		}
	}
}

func TestSkillSlugMatchesNpx(t *testing.T) {
	for in, want := range map[string]string{"Find Skills": "find-skills", "my_skill": "my-skill", "--Vue 3!--": "vue-3", "ai-sdk": "ai-sdk"} {
		if got := SkillSlug(in); got != want {
			t.Errorf("SkillSlug(%q) = %q, want %q", in, got, want)
		}
	}
}
