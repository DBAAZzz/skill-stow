package main

import (
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"skillmanager/internal/library"
	"skillmanager/locales"
)

func TestProjectGitMenuKeyboardAndDismissal(t *testing.T) {
	a, tt := newTester(t)
	p := projectNamed(a, "storefront")
	a.scope = projectScope(p)
	tt.Frame()
	open := func() {
		t.Helper()
		must(t, tt.Click(locales.GitUncommittedButton(2)))
		tt.Advance(250 * time.Millisecond)
	}
	menu := a.menuEntry("git-uncommitted:" + p.Dir)
	open()
	tt.Key(0, ui.KeyDown)
	if !tt.Focused(locales.GitRowAdded("release-checklist")) {
		t.Fatal("first item must be the added skill")
	}
	tt.Key(0, ui.KeyDown)
	tt.Key(0, ui.KeyRight)
	if !tt.HasText("examples/pagination.md") {
		t.Fatal("missing added file")
	}
	tt.Key(0, ui.KeyEnter)
	if menu.open || a.selected != nil {
		t.Fatal("file action left the menu or inspector open")
	}
	open()
	tt.Key(0, ui.KeyEscape)
	if menu.open {
		t.Fatal("escape did not close")
	}
	open()
	tt.ClickAt(1000, 700)
	if menu.open {
		t.Fatal("outside click did not close")
	}
	open()
	p.Git = &library.ProjectGit{Check: library.GitReady}
	tt.Frame()
	if menu.open || tt.HasText(locales.GitUncommittedCount(2)) {
		t.Fatal("empty result must close the menu")
	}
}

func TestProjectGitMenuLimitsVisibleRows(t *testing.T) {
	a, tt := newTester(t)
	p := projectNamed(a, "storefront")
	for _, s := range a.lib.Skills[:8] {
		p.Skills = append(p.Skills, s)
		p.Git.Installs[s.Dir] = []library.GitInstall{{State: library.GitModified}}
	}
	rows := gitMenuRows(p)
	if len(rows) != 10 {
		t.Fatal(len(rows))
	}
	a.scope = projectScope(p)
	tt.Frame()
	must(t, tt.Click(locales.GitUncommittedButton(10)))
	if !tt.HasText(locales.GitUncommittedMore(2)) {
		t.Fatal("overflow count")
	}
}
