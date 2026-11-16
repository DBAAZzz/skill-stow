package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/egoist/mygo/ui"

	"skillmanager/internal/library"
	"skillmanager/locales"
)

func TestEmptyLibraryLeadsToAddSkillsPage(t *testing.T) {
	for _, scope := range []library.Scope{library.ScopeMine, library.ScopeAll} {
		for _, mode := range []int{viewCards, viewBoard} {
			a := newApp(library.Scan(t.TempDir()))
			a.scope, a.mode = string(scope), mode
			tt := newUITester(a.view, 1280, 800)
			tt.Frame()
			if !tt.HasText(locales.BrowseNoSkillsYetBody()) {
				t.Fatalf("%s 视图 %d：库为空时没有引导", scope, mode)
			}
			// 侧栏、工具栏也有「添加技能」，点空状态这一组最下面的按钮
			g, ok := tt.AccessByRole(ui.RoleGroup, locales.BrowseNoSkillsYetTitle())
			if !ok {
				t.Fatalf("%s 视图 %d：找不到空状态", scope, mode)
			}
			tt.ClickAt(g.Bounds.X+g.Bounds.W/2, g.Bounds.Y+g.Bounds.H-4)
			tt.Frame()
			if a.scope != scopeImport {
				t.Fatalf("%s 视图 %d：点添加技能后到了 %q，不是添加技能页", scope, mode, a.scope)
			}
		}
	}
}

func TestSearchWithNoResultsSaysSoInsteadOfEmptyLibrary(t *testing.T) {
	a, tt := newTester(t)
	a.query = "  kubernetes-nothing  "
	tt.Frame()
	if !tt.HasText(locales.BrowseNoSearchResults("kubernetes-nothing")) {
		t.Fatal("搜索没结果时没有说明是搜索没结果")
	}
	if tt.HasText(locales.BrowseNoSkillsYetTitle()) || tt.HasText(locales.ActionClearFilters()) {
		t.Fatal("搜索没结果时不该引导添加技能，也没有筛选可清除")
	}

	a.currentFilter().unused = true
	tt.Frame()
	must(t, tt.Click(locales.ActionClearFilters()))
	tt.Frame()
	if a.currentFilter().active() {
		t.Fatal("搜索加筛选都没结果时，清除筛选没生效")
	}
}

func TestBoardDoesNotCallSearchHitsOutsideTheBoardNoResults(t *testing.T) {
	a, tt := newTester(t)
	// 挑一个搜它的名字只搜得到插件带的技能的
	var plugin *library.Skill
	for _, s := range a.lib.Skills {
		if s.Source == library.ViaPlugin && len(mineOf(a.lib.Find(library.ScopeMine, s.Name))) == 0 {
			plugin = s
			break
		}
	}
	if plugin == nil {
		t.Fatal("示例数据里没有可用的插件带的技能")
	}
	// 插件带的技能在「我的技能」里，但不进看板
	a.mode, a.query = viewBoard, plugin.Name
	tt.Frame()
	if tt.HasText(locales.BrowseNoSearchResults(plugin.Name)) || tt.HasText(locales.BrowseNoSkillsYetTitle()) {
		t.Fatal("搜到了插件带的技能，看板却说没有结果")
	}
	if !tt.HasText(locales.BrowseBoardDescription()) {
		t.Fatal("看板排不出列时应说明看板只排你自己的技能")
	}
}

// 加了技能也不会出现在内置技能、集合里，那里空着只说没有，不引导添加。
func TestPlacesNewSkillsDoNotLandInDoNotOfferAddingSkills(t *testing.T) {
	places := []struct {
		name  string
		setup func(a *app)
		want  map[int]string // 视图 → 应显示的说明
	}{
		{"builtin", func(a *app) { a.scope = string(library.ScopeBuiltin) },
			map[int]string{viewCards: locales.CommonNoSkills(), viewBoard: locales.BrowseBoardDescription()}},
		{"set", func(a *app) { a.scope, a.inColl = string(library.ScopeMine), &library.Collection{Name: "empty-set"} },
			map[int]string{viewCards: locales.CommonNoSkills(), viewBoard: locales.CommonNoSkills()}},
	}
	for _, p := range places {
		for mode, want := range p.want {
			a := newApp(library.Scan(t.TempDir()))
			p.setup(a)
			a.mode = mode
			tt := newUITester(a.view, 1280, 800)
			tt.Frame()
			if tt.HasText(locales.BrowseNoSkillsYetTitle()) {
				t.Fatalf("%s 视图 %d：不该引导添加技能", p.name, mode)
			}
			if !tt.HasText(want) {
				t.Fatalf("%s 视图 %d：没有显示 %q：%q", p.name, mode, want, tt.Texts())
			}
		}
	}
}

// TestEmptyStateSnapshots 输出各种空状态的截图，用法同 TestSnapshots。
func TestEmptyStateSnapshots(t *testing.T) {
	dir := os.Getenv("SNAPSHOT_DIR")
	if dir == "" {
		t.Skip("设置 SNAPSHOT_DIR 以输出截图")
	}
	shots := []struct {
		name  string
		lib   func() *library.Library
		setup func(a *app)
	}{
		{"e1-empty-mine", func() *library.Library { return library.Scan(t.TempDir()) }, func(a *app) {}},
		{"e2-empty-board", func() *library.Library { return library.Scan(t.TempDir()) }, func(a *app) { a.mode = viewBoard }},
		{"e3-empty-search", library.Sample, func(a *app) { a.query = "kubernetes" }},
		{"e4-empty-filter", library.Sample, func(a *app) {
			a.currentFilter().unused, a.currentFilter().disabled, a.currentFilter().updatable = true, true, true
		}},
	}
	for _, s := range shots {
		a := newApp(s.lib())
		a.scope = string(library.ScopeMine)
		s.setup(a)
		tt := newUITester(a.view, 1280, 800)
		tt.SetScale(2)
		tt.Frame()
		writePNG(t, filepath.Join(dir, s.name+".png"), tt)
	}
}
