package main

import (
	"testing"

	"skillmanager/internal/library"
)

func TestGitDropsLateChecks(t *testing.T) {
	dir := t.TempDir()
	p := &library.ProjectInfo{Dir: dir, Skills: []*library.Skill{{Name: "fixture"}}}
	lib := &library.Library{Home: dir, Projects: []*library.ProjectInfo{p}}
	a := newApp(lib)
	var posted []func()
	a.bg = func(work func(func(func()))) { work(func(fn func()) { posted = append(posted, fn) }) }
	a.scope = projectScope(p)
	a.syncPageGit()
	a.syncPageGit()
	if len(posted) != 1 {
		t.Fatalf("queued %d callbacks in one visit", len(posted))
	}
	a.scope = scopeUsage
	a.syncPageGit()
	a.scope = projectScope(p)
	a.syncPageGit()
	posted[0]()
	if p.Git.Check != library.GitPending {
		t.Fatal("previous visit replaced pending result")
	}
	posted[1]()
	if p.Git.Check == library.GitPending {
		t.Fatal("current result discarded")
	}
	a.startProjectGit(p)
	a.lib = &library.Library{Home: dir}
	posted[2]()
	if p.Git.Check != library.GitPending {
		t.Fatal("previous scan accepted")
	}
}
