package library

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func repoCmd(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-c", "user.name=Fixture", "-c", "user.email=fixture@example.invalid", "-c", "commit.gpgsign=false"}, args...)...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %s: %s %v", args, out, err)
	}
}

func TestGitSnapshot(t *testing.T) {
	if localGit() == "" {
		t.Skip("git unavailable")
	}
	home := fakeHome(t)
	root := filepath.Join(home, "code")
	dir := filepath.Join(root, "storefront")
	const rel = ".claude/skills"
	skill := filepath.Join(dir, rel, "notes-sync")
	write := func(path string) {
		t.Helper()
		must(t, os.MkdirAll(filepath.Dir(path), 0755))
		must(t, os.WriteFile(path, []byte(path), 0644))
	}
	snapshot := func() *ProjectGit {
		t.Helper()
		g := Scan(home).Project(dir).CheckGit()
		if g.Check != GitReady {
			t.Fatalf("git check %d", g.Check)
		}
		return g
	}
	repoCmd(t, root, "init", "-q")
	must(t, os.WriteFile(filepath.Join(root, ".gitignore"), []byte("ignored*/\n"), 0644))
	write(filepath.Join(dir, rel, "ignored-skill", "SKILL.md"))
	testName := "a b\tline\n.md"
	if runtime.GOOS == "windows" {
		testName = "a b.md"
	}
	old := filepath.Join(skill, testName)
	write(old)
	g := snapshot()
	if g.Installs[skill][0].State != GitAdded || g.Installs[filepath.Join(dir, rel, "ignored-skill")][0].State != gitIgnored {
		t.Fatalf("initial: %+v", g)
	}
	repoCmd(t, root, "add", ".")
	if got := snapshot().Installs[skill][0].State; got != GitAdded {
		t.Fatal(got)
	}
	repoCmd(t, root, "commit", "-qm", "initial")
	newName := "renamed " + testName
	repoCmd(t, root, "mv", old, filepath.Join(skill, newName))
	g = snapshot()
	if files := g.Installs[skill][0].Files; len(files) != 1 || files[0].Name != newName {
		t.Fatal(files)
	}
	for _, i := range g.Installs[skill] {
		if i.State != GitModified {
			t.Fatal(i)
		}
	}
	write(filepath.Join(skill, "new.md"))
	if got := snapshot().Installs[skill][0].Files; len(got) != 2 {
		t.Fatal(got)
	}
}

func TestGitFailures(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX command fixtures")
	}
	bin := localGit()
	if bin == "" {
		t.Skip("git unavailable")
	}
	dir, err := filepath.EvalSymlinks(t.TempDir())
	must(t, err)
	p := ProjectInfo{Dir: dir, gitScan: gitPlaces{dir: dir, dirs: []string{"skills"}}}
	if p.readGit(bin).Check != GitUnchecked {
		t.Fatal("not a repository")
	}
	repoCmd(t, dir, "init", "-q")
	must(t, os.MkdirAll(filepath.Join(dir, "skills"), 0755))
	for _, tc := range []struct {
		command, action string
	}{
		{"status", "printf corrupt"},
		{"status", "exec sleep 3"},
		{"rev-parse", "printf '%s\\n%s\\n' \"$PWD\" /super"},
	} {
		path := filepath.Join(t.TempDir(), "fake-git")
		script := "#!/bin/sh\nif [ \"$1\" = " + tc.command + " ]; then " + tc.action + "; else exec \"" + bin + "\" \"$@\"; fi\n"
		must(t, os.WriteFile(path, []byte(script), 0755))
		if g := p.readGit(path); g.Check != GitUnchecked {
			t.Fatalf("%s: %+v", tc.command, g)
		}
	}
	t.Setenv("PATH", t.TempDir())
	if probeGit() != "" {
		t.Fatal("expected missing git")
	}
	if runtime.GOOS == "darwin" {
		if checkGitTools("/usr/bin/git") != "" {
			t.Fatal("expected missing tools")
		}
	}
}

func TestGitStatusRejectsTruncatedRename(t *testing.T) {
	_, _, err := parseGitStatus("# branch.oid " + strings.Repeat("a", 40) + "\x002 R. N... 100644 100644 100644 old new R100 file\x00")
	if err == nil {
		t.Fatal("missing rename source")
	}
}

func TestGitChecksMergedWorktreeAndSkipsSubmodule(t *testing.T) {
	if localGit() == "" {
		t.Skip("git unavailable")
	}
	home := fakeHome(t)
	main := filepath.Join(home, "code/storefront")
	repoCmd(t, main, "init", "-q")
	repoCmd(t, main, "add", ".")
	repoCmd(t, main, "commit", "-qm", "initial")
	wt := filepath.Join(home, "code/other-worktree")
	repoCmd(t, main, "worktree", "add", "--detach", wt)
	must(t, os.WriteFile(filepath.Join(wt, ".claude/skills/notes-sync/worktree.md"), []byte("local"), 0644))
	must(t, os.MkdirAll(filepath.Join(home, ".codex/sessions"), 0755))
	must(t, os.WriteFile(filepath.Join(home, ".codex/sessions/cwd.jsonl"), []byte(`{"payload":{"cwd":"`+jsonStr(wt)+`"}}`+"\n"), 0644))
	l := Scan(home)
	if len(l.Projects) != 1 || l.Projects[0].CheckGit().Installs[filepath.Join(main, ".claude/skills/notes-sync")][0].State != gitClean {
		t.Fatal("worktree must use main checkout")
	}
	parent := filepath.Join(home, "super")
	must(t, os.MkdirAll(parent, 0755))
	repoCmd(t, parent, "init", "-q")
	repoCmd(t, parent, "-c", "protocol.file.allow=always", "submodule", "add", "-q", main, "sub")
	sub := filepath.Join(parent, "sub")
	p := ProjectInfo{Dir: sub, gitScan: gitPlaces{dir: sub}}
	if g := p.CheckGit(); g.Check != GitUnchecked {
		t.Fatal(g)
	}
}
