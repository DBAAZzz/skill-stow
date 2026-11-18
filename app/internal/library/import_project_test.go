package library

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestProjectTargetDirsTable(t *testing.T) {
	cases := []struct {
		name   string
		agents []string
		want   []string
	}{
		{"claude only", []string{"claude-code"}, []string{".claude/skills"}},
		{"codex and gemini", []string{"codex", "gemini"}, []string{".agents/skills"}},
		{"claude and codex", []string{"claude-code", "codex"}, []string{".agents/skills", ".claude/skills"}},
		{"claude and cursor", []string{"claude-code", "cursor"}, []string{".claude/skills"}},
		{"codex and kiro", []string{"codex", "kiro"}, []string{".agents/skills", ".kiro/skills"}},
		{"cursor only prefers shared", []string{"cursor"}, []string{".agents/skills"}},
		{"windsurf and kiro follow catalog order", []string{"kiro", "windsurf"}, []string{".windsurf/skills", ".kiro/skills"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ProjectTargetDirs(tc.agents)
			if err != nil {
				t.Fatalf("ProjectTargetDirs(%v): %v", tc.agents, err)
			}
			if !slices.Equal(got, tc.want) {
				t.Errorf("ProjectTargetDirs(%v) = %v, want %v", tc.agents, got, tc.want)
			}
		})
	}
}

func TestImportProjectSingleAgentClaudeOnly(t *testing.T) {
	home, h := linkHome(t, "claude-code")
	proj := h("code/proj-a")
	must(t, os.MkdirAll(proj, 0o755))
	src := t.TempDir()
	writeSkill(t, filepath.Join(src, "x"), "")

	l := Scan(home)
	b, err := ReadLocalImport(home, []string{filepath.Join(src, "x")})
	if err != nil {
		t.Fatal(err)
	}
	defer b.Discard()

	p := ImportPlacement{Project: proj, Agents: []string{"claude-code"}}
	out := l.AddImport(b, []string{"x"}, p, nil)
	if len(out) != 1 || !out[0].Added || out[0].Err != nil {
		t.Fatalf("AddImport: %+v", out)
	}
	wantReal := filepath.Join(proj, ".claude/skills/x")
	if fi, err := os.Lstat(wantReal); err != nil || !fi.IsDir() || isLink(wantReal) {
		t.Fatalf("expected real dir at %s, got err=%v link=%v", wantReal, err, isLink(wantReal))
	}
	if exists(filepath.Join(proj, ".agents/skills/x")) {
		t.Fatal("should not create .agents/skills when only claude-code is selected")
	}

	l2 := Scan(home)
	pi := l2.Project(proj)
	if pi == nil || len(pi.Skills) != 1 || pi.Skills[0].Name != "x" {
		t.Fatalf("expected project skill x in Scan, got %+v", pi)
	}
	if pi.Skills[0].ReadOnly() {
		t.Fatal("project skill should not be ReadOnly()")
	}
}

func TestImportProjectMultiAgentRelativeSymlinkSurvivesMove(t *testing.T) {
	home, h := linkHome(t, "claude-code", "codex")
	proj := h("code/proj-b")
	must(t, os.MkdirAll(proj, 0o755))
	src := t.TempDir()
	writeSkill(t, filepath.Join(src, "x"), "")

	l := Scan(home)
	b, err := ReadLocalImport(home, []string{filepath.Join(src, "x")})
	if err != nil {
		t.Fatal(err)
	}
	defer b.Discard()

	p := ImportPlacement{Project: proj, Agents: []string{"claude-code", "codex"}}
	out := l.AddImport(b, []string{"x"}, p, nil)
	if len(out) != 1 || !out[0].Added || out[0].Err != nil {
		t.Fatalf("AddImport: %+v", out)
	}
	realDir := filepath.Join(proj, ".agents/skills/x")
	linkPath := filepath.Join(proj, ".claude/skills/x")
	if isLink(realDir) || !exists(realDir) {
		t.Fatalf("expected real dir at %s", realDir)
	}
	to, err := os.Readlink(linkPath)
	if err != nil {
		t.Fatalf("expected symlink at %s: %v", linkPath, err)
	}
	if filepath.IsAbs(to) || filepath.ToSlash(to) != "../../.agents/skills/x" {
		t.Fatalf("expected relative symlink ../../.agents/skills/x, got %q", to)
	}

	// Copy/move project to another path and verify symlink still resolves inside the new copy.
	moved := h("code/proj-b-copy")
	must(t, os.Rename(proj, moved))
	resolved, err := realPath(filepath.Join(moved, ".claude/skills/x"))
	if err != nil {
		t.Fatalf("symlink broken after move: %v", err)
	}
	wantResolved, _ := realPath(filepath.Join(moved, ".agents/skills/x"))
	if resolved != wantResolved {
		t.Fatalf("resolved %q, want %q", resolved, wantResolved)
	}
}

func TestImportProjectBlockedWhenLinkSlotIsRealDir(t *testing.T) {
	home, h := linkHome(t, "claude-code", "codex")
	proj := h("code/proj-c")
	writeSkill(t, filepath.Join(proj, ".claude/skills/x"), "existing")
	src := t.TempDir()
	writeSkill(t, filepath.Join(src, "x"), "incoming")

	l := Scan(home)
	b, err := ReadLocalImport(home, []string{filepath.Join(src, "x")})
	if err != nil {
		t.Fatal(err)
	}
	defer b.Discard()

	p := ImportPlacement{Project: proj, Agents: []string{"claude-code", "codex"}}
	chk := l.CheckImport(b.Skill("x"), p)
	if chk.State != ImportConflict || chk.Blocked == "" {
		t.Fatalf("expected blocked conflict, got %+v", chk)
	}
	if !strings.Contains(chk.Blocked, ".claude/skills") {
		t.Errorf("expected .claude/skills in blocked reason, got %q", chk.Blocked)
	}

	out := l.AddImport(b, []string{"x"}, p, map[string]ImportCheck{"x": chk})
	if out[0].Added || out[0].Err == nil {
		t.Fatalf("expected AddImport to refuse blocked replace, got %+v", out[0])
	}
	if exists(filepath.Join(proj, ".agents/skills/x")) {
		t.Fatal("should not touch .agents/skills/x when blocked")
	}
}

func TestImportProjectRollbackOnLinkFailure(t *testing.T) {
	home, h := linkHome(t, "claude-code", "codex")
	proj := h("code/proj-d")
	must(t, os.MkdirAll(proj, 0o755))
	// Make .claude a regular file so MkdirAll(.claude/skills) fails when creating symlink.
	must(t, os.WriteFile(filepath.Join(proj, ".claude"), []byte("blocker"), 0o644))
	src := t.TempDir()
	writeSkill(t, filepath.Join(src, "x"), "")

	l := Scan(home)
	b, err := ReadLocalImport(home, []string{filepath.Join(src, "x")})
	if err != nil {
		t.Fatal(err)
	}
	defer b.Discard()

	p := ImportPlacement{Project: proj, Agents: []string{"claude-code", "codex"}}
	out := l.AddImport(b, []string{"x"}, p, nil)
	if out[0].Added || out[0].Err == nil {
		t.Fatalf("expected failure, got %+v", out[0])
	}
	if exists(filepath.Join(proj, ".agents/skills/x")) {
		t.Fatal("rollback failed: .agents/skills/x still exists")
	}
	if _, ok := l.ImportedFrom(filepath.Join(proj, ".agents/skills/x")); ok {
		t.Fatal("rollback failed: importMark was left behind")
	}
}

func TestDeleteProjectSkillTrashesOriginalAndProjectLinkAndDropsGlobalLinks(t *testing.T) {
	home, h := linkHome(t, "claude-code", "codex")
	proj := h("code/proj-del")
	must(t, os.MkdirAll(proj, 0o755))
	src := t.TempDir()
	writeSkill(t, filepath.Join(src, "x"), "")

	l := Scan(home)
	b, err := ReadLocalImport(home, []string{filepath.Join(src, "x")})
	if err != nil {
		t.Fatal(err)
	}
	defer b.Discard()

	p := ImportPlacement{Project: proj, Agents: []string{"claude-code", "codex"}}
	out := l.AddImport(b, []string{"x"}, p, nil)
	if !out[0].Added {
		t.Fatalf("AddImport: %+v", out[0])
	}
	// Also add a global link pointing to the project skill.
	globalLink := h(".claude/skills/x")
	must(t, os.MkdirAll(filepath.Dir(globalLink), 0o755))
	mustLink(t, filepath.Join(proj, ".agents/skills/x"), globalLink)

	l = Scan(home)
	pi := l.Project(proj)
	if pi == nil || len(pi.Skills) != 1 {
		t.Fatalf("expected 1 project skill, got %+v", pi)
	}
	s := pi.Skills[0]
	if len(s.GlobalLinks()) != 1 {
		t.Fatalf("expected 1 global link, got %v", s.GlobalLinks())
	}

	var trashed []string
	oldTrash := Trash
	Trash = func(_ string, path string) error {
		trashed = append(trashed, path)
		return os.RemoveAll(path)
	}
	t.Cleanup(func() { Trash = oldTrash })

	del, err := l.Delete(s)
	if err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if len(pi.Skills) != 0 {
		t.Fatalf("expected project skill unlisted immediately, got %d", len(pi.Skills))
	}
	if exists(filepath.Join(proj, ".agents/skills/x")) || exists(filepath.Join(proj, ".claude/skills/x")) || exists(globalLink) {
		t.Fatal("expected original, project link, and global link all removed")
	}
	if !slices.Contains(trashed, s.Real) || !slices.Contains(trashed, filepath.Join(proj, ".claude/skills/x")) {
		t.Fatalf("expected both original and project-internal link in Trash, got %v", trashed)
	}
	if slices.Contains(trashed, globalLink) {
		t.Fatalf("global link should be removed without trashing, got %v", trashed)
	}

	// Undo restores everything.
	if err := l.Undo(del); err != nil {
		t.Fatalf("Undo: %v", err)
	}
	if len(pi.Skills) != 1 || !exists(filepath.Join(proj, ".agents/skills/x")) || !isLink(filepath.Join(proj, ".claude/skills/x")) {
		t.Fatal("expected Undo to restore project skill and link")
	}
}

func TestImportProjectGitHubExcludedFromUpdatesAndLockFile(t *testing.T) {
	home, h := linkHome(t, "claude-code")
	proj := h("code/proj-gh")
	must(t, os.MkdirAll(proj, 0o755))

	// Use fake GitHub batch directly as import_add tests do.
	srcDir := t.TempDir()
	writeSkill(t, filepath.Join(srcDir, "gh-skill"), "")
	sk := &ImportSkill{
		Name:     "gh-skill",
		Dir:      filepath.Join(srcDir, "gh-skill"),
		RepoPath: "skills/gh-skill",
		Tree:     mustTreeHash(t, filepath.Join(srcDir, "gh-skill")),
	}
	b := &ImportBatch{
		Source: ImportSource{Kind: ImportGitHub, Repo: "owner/repo", Ref: "main"},
		Commit: "abcdef123456",
		Skills: []*ImportSkill{sk},
	}

	l := Scan(home)
	p := ImportPlacement{Project: proj, Agents: []string{"claude-code"}}
	out := l.AddImport(b, []string{"gh-skill"}, p, nil)
	if len(out) != 1 || !out[0].Added || out[0].Err != nil {
		t.Fatalf("AddImport: %+v", out)
	}
	if exists(h(".agents/.skill-lock.json")) {
		t.Fatal("project import should not write ~/.agents/.skill-lock.json")
	}
	if entries := l.updateState().appEntries(l.Root); len(entries) != 0 {
		t.Fatalf("project import should be excluded from update appEntries, got %+v", entries)
	}
}

func mustTreeHash(t *testing.T, dir string) string {
	t.Helper()
	h, err := TreeHash(dir)
	if err != nil {
		t.Fatal(err)
	}
	return h
}

func TestProjectImportRequiresUsableDestination(t *testing.T) {
	home, h := linkHome(t, "codex")
	proj := h("work")
	must(t, os.MkdirAll(proj, 0755))
	shared := h(".agents")
	must(t, os.MkdirAll(shared, 0755))
	mustLink(t, shared, filepath.Join(proj, ".agents"))
	l := Scan(home)
	for _, dir := range []string{home, h("removed-project"), proj} {
		_, _, err := l.importTargets("demo", ImportPlacement{Project: dir, Agents: []string{"codex"}})
		if err == nil {
			t.Errorf("accepted project destination %q", dir)
		}
	}
}

func TestScanKeepsExternalSkillDirectoryOutOfProject(t *testing.T) {
	home, h := linkHome(t, "codex")
	writeSkill(t, h(".agents/skills/personal"), "")
	must(t, os.MkdirAll(h("work"), 0755))
	mustLink(t, h(".agents"), h("work/.agents"))
	must(t, writeManual(home, []manualProject{{Dir: h("work")}}))
	l := Scan(home)
	if s := l.Get("personal"); s == nil || s.Source != Mine {
		t.Fatal("a global skill was classified as owned by the project")
	}
}

func TestDeleteProjectCleansLinksWithDifferentNames(t *testing.T) {
	home, h := linkHome(t, "codex", "claude-code")
	writeSkill(t, h("work/.agents/skills/demo"), "")
	must(t, os.MkdirAll(h("work/.claude/skills"), 0755))
	mustLink(t, h("work/.agents/skills/demo"), h("work/.claude/skills/alias"))
	mustLink(t, h("work/.agents/skills/demo"), h("work/.claude/skills/second"))
	must(t, writeManual(home, []manualProject{{Dir: h("work")}}))
	l := Scan(home)
	s := l.Project(h("work")).Skills[0]
	links := l.linksTo(s)
	if len(links) != 2 || links[0].path != h("work/.claude/skills/alias") || links[1].path != h("work/.claude/skills/second") {
		t.Fatalf("links to remove and restore = %+v", links)
	}
}

func TestImportRollsBackWhenProjectCannotBeRegistered(t *testing.T) {
	home, h := linkHome(t, "codex")
	must(t, os.MkdirAll(manualFile(home), 0755))
	proj := h("work")
	must(t, os.MkdirAll(proj, 0755))
	writeSkill(t, h("download/demo"), "")
	b, err := ReadLocalImport(home, []string{h("download/demo")})
	must(t, err)
	defer b.Discard()
	l := Scan(home)
	p := ImportPlacement{Project: proj, Agents: []string{"codex"}}
	out := l.AddImport(b, []string{"demo"}, p, nil)[0]
	place := h("work/.agents/skills/demo")
	if _, marked := l.ImportedFrom(place); out.Err == nil || out.Added || marked || exists(place) {
		t.Fatalf("registration failed but installation survived: %+v", out)
	}
}

func TestProjectImportChecksAllSelectedDirectories(t *testing.T) {
	home, h := linkHome(t, "codex", "claude-code")
	writeSkill(t, h("work/.agents/skills/demo"), "")
	writeSkill(t, h("work/.claude/skills/demo"), "different")
	writeSkill(t, h("download/demo"), "")
	b, err := ReadLocalImport(home, []string{h("download/demo")})
	must(t, err)
	defer b.Discard()
	chk := Scan(home).CheckImport(b.Skill("demo"), ImportPlacement{Project: h("work"), Agents: []string{"codex", "claude-code"}})
	if chk.State != ImportConflict || chk.Blocked == "" {
		t.Fatalf("independent copy in link slot should block installation: %+v", chk)
	}
}

func TestImportProjectAddsMissingReaderToMatchingCopy(t *testing.T) {
	home, h := linkHome(t, "codex", "claude-code")
	target := h("work/.agents/skills/demo")
	writeSkill(t, target, "")
	before, err := os.Stat(target)
	must(t, err)
	writeSkill(t, h("download/demo"), "")
	b, err := ReadLocalImport(home, []string{h("download/demo")})
	must(t, err)
	defer b.Discard()
	l := Scan(home)
	p := ImportPlacement{Project: h("work"), Agents: []string{"codex", "claude-code"}}
	if chk := l.CheckImport(b.Skill("demo"), p); chk.State != ImportNew {
		t.Fatalf("missing reader cannot be added: %+v", chk)
	}
	out := l.AddImport(b, []string{"demo"}, p, nil)[0]
	after, err := os.Stat(target)
	must(t, err)
	if !out.Added || !os.SameFile(before, after) || !pointsAt(h("work/.claude/skills/demo"), target) {
		t.Fatalf("expected only the missing link to be installed: %+v", out)
	}
}

func TestUndoProjectDeletionRetriesOnlyMissingLinks(t *testing.T) {
	home, h := linkHome(t, "codex", "claude-code")
	target := h("work/.agents/skills/demo")
	link := h("work/.claude/skills/demo")
	writeSkill(t, target, "")
	must(t, os.MkdirAll(filepath.Dir(link), 0755))
	mustLink(t, target, link)
	must(t, writeManual(home, []manualProject{{Dir: h("work")}}))
	l := Scan(home)
	oldTrash := Trash
	Trash = func(_ string, path string) error { return os.RemoveAll(path) }
	t.Cleanup(func() { Trash = oldTrash })
	d, err := l.Delete(l.Get("demo"))
	must(t, err)
	defer d.Discard()
	must(t, os.WriteFile(link, []byte("occupied"), 0644))
	if err := l.Undo(d); err == nil || !isSkill(target) {
		t.Fatalf("expected only the link to await restoration: %v", err)
	}
	must(t, os.Remove(link))
	must(t, l.Undo(d))
	if !pointsAt(link, target) {
		t.Fatal("missing restored project link")
	}
}

func TestProjectDeleteChecksCurrentLocation(t *testing.T) {
	home, h := linkHome(t, "codex")
	dir := h("work/.agents/skills")
	writeSkill(t, filepath.Join(dir, "demo"), "")
	must(t, writeManual(home, []manualProject{{Dir: h("work")}}))
	l := Scan(home)
	s := l.Get("demo")
	must(t, os.Rename(dir, h("archived-skills")))
	mustLink(t, h("archived-skills"), dir)
	if err := l.CanDelete(s); err == nil {
		t.Fatal("stale project selection still allows deletion after its directory moved")
	}
}
