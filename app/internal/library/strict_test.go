package library

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// treeVectors 是调研 Q1 的测试向量（与 git write-tree 核对过）。
func TestTreeHashMatchesGitForTheResearchVectors(t *testing.T) {
	cases := []struct {
		name  string
		build func(dir string)
		want  string
	}{
		{"一个文件", func(d string) { write(t, d, "a.txt", "hello\n", 0o644) }, "2e81171448eb9f2ee3821e3d447aa6b2fe3ddba1"},
		{"可执行文件", func(d string) { write(t, d, "run.sh", "#!/bin/sh\necho ok\n", 0o755) }, "a80548db97f17b12b82cfc7880580bdc4f41ab05"},
		{"子目录", func(d string) { write(t, d, "a.txt", "A\n", 0o644); write(t, d, "d/b.txt", "nested\n", 0o644) }, "2ac96d4c06570c4ea186735d7425a5022800f34c"},
		{"隐藏文件、链接、空目录、排序", func(d string) {
			write(t, d, ".hidden", "x\r\n", 0o644)
			must(t, os.Symlink(".hidden", filepath.Join(d, "link")))
			must(t, os.Mkdir(filepath.Join(d, "empty"), 0o755))
			write(t, d, "a.c", "c", 0o644)
			write(t, d, "a/z", "z", 0o644)
		}, "e45587d68f1c7e836f92b6a9a73d64de56c91bbe"},
	}
	for _, c := range cases {
		dir := t.TempDir()
		c.build(dir)
		got, err := TreeHash(dir)
		if err != nil || got != c.want {
			t.Errorf("%s：%s %v，want %s", c.name, got, err, c.want)
		}
	}
}

func TestTreeHashReportsUnreadableFilesInsteadOfSkippingThem(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "SKILL.md", "x", 0o644)
	write(t, dir, "secret", "y", 0o000)
	if _, err := TreeHash(dir); err == nil {
		t.Error("读不了的文件应报错")
	}
}

func write(t *testing.T, dir, rel, body string, mode os.FileMode) {
	t.Helper()
	p := filepath.Join(dir, rel)
	must(t, os.MkdirAll(filepath.Dir(p), 0o755))
	must(t, os.WriteFile(p, []byte(body), mode))
	must(t, os.Chmod(p, mode))
}

// copiesHome：技能库里一份 foo，CodeBuddy 目录里一份内容一样的拷贝；
// change 再改动 CodeBuddy 那份。
func copiesHome(t *testing.T, change func(dir string)) *Library {
	t.Helper()
	home, h := linkHome(t, "codebuddy")
	writeSkill(t, h(".agents/skills/foo"), "body")
	writeSkill(t, h(".codebuddy/skills/foo"), "body")
	change(h(".codebuddy/skills/foo"))
	return Scan(home)
}

func TestCopiesDifferingOnlyInHiddenFilesModesOrLinksAreNotIdentical(t *testing.T) {
	for name, change := range map[string]func(string){
		".DS_Store":   func(d string) { write(t, d, ".DS_Store", "finder", 0o644) },
		"__pycache__": func(d string) { write(t, d, "__pycache__/x.pyc", "pyc", 0o644) },
		"执行权限":        func(d string) { must(t, os.Chmod(filepath.Join(d, "SKILL.md"), 0o755)) },
	} {
		foo := copiesHome(t, change).Get("foo")
		if foo.Identical() || len(foo.Versions()) != 2 {
			t.Errorf("%s 不同：应算内容不同的副本，得到 %d 种", name, len(foo.Versions()))
		}
	}
	// 链接目标不同
	foo := copiesHome(t, func(d string) {
		write(t, d, "a", "", 0o644)
		write(t, d, "b", "", 0o644)
		must(t, os.Symlink("a", filepath.Join(d, "l")))
	})
	_ = foo
	l2 := copiesHome(t, func(d string) {
		write(t, d, "a", "", 0o644)
		write(t, d, "b", "", 0o644)
		must(t, os.Symlink("b", filepath.Join(d, "l")))
	})
	if l2.Get("foo").Identical() {
		t.Error("多出链接的拷贝不应算一样")
	}
}

func TestACopyWithAnUnreadableFileIsNeverCalledIdentical(t *testing.T) {
	foo := copiesHome(t, func(d string) { must(t, os.Chmod(filepath.Join(d, "SKILL.md"), 0o000)) }).Get("foo")
	if foo.Identical() {
		t.Error("读不了的那份不能当成一样，要人来处理")
	}
}

func TestComparingCopiesShowsHiddenFilesAndModeChanges(t *testing.T) {
	l := copiesHome(t, func(d string) {
		write(t, d, ".env.example", "A=1\n", 0o644)
		must(t, os.Chmod(filepath.Join(d, "SKILL.md"), 0o755))
	})
	vs := l.Get("foo").Versions()
	files := l.Compare(&vs[0], &vs[1])
	var hidden, mode bool
	for _, f := range files {
		hidden = hidden || f.Path == ".env.example" && f.OnlyRight
		mode = mode || f.Path == "SKILL.md" && f.ModeOnly
	}
	if !hidden || !mode {
		t.Errorf("对比应列出隐藏文件和执行权限的不同：%+v", files)
	}
}

func TestOperationsOnABusySkillWait(t *testing.T) {
	home, _ := deleteHome(t)
	l := Scan(home)
	release, err := lockSkill("design-lab")
	must(t, err)
	defer release()

	if _, err := l.Delete(l.Get("design-lab")); !errors.Is(err, ErrBusy) {
		t.Errorf("删除：%v，want ErrBusy", err)
	}
	if !exists(filepath.Join(home, ".agents/skills/design-lab")) {
		t.Error("忙的时候不应删掉")
	}
	if _, err := lockSkill("design-lab"); !errors.Is(err, ErrBusy) {
		t.Errorf("同一技能不能同时做两件事：%v", err)
	}
	release()
	if _, err := l.Delete(l.Get("design-lab")); err != nil {
		t.Errorf("放开后应能删：%v", err)
	}
}
