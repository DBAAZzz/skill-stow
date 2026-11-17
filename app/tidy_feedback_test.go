package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"skillmanager/internal/library"
)

// Control only scheduling: the actions, library and rendered UI stay real.
type tidyTestClock struct {
	work  func(func(func()))
	later []func()
}

func stageTidy(a *app) *tidyTestClock {
	clock := &tidyTestClock{}
	a.bg = func(work func(func(func()))) { clock.work = work }
	a.delay = func(_ time.Duration, fn func()) { clock.later = append(clock.later, fn) }
	return clock
}

func (clock *tidyTestClock) finish() {
	for len(clock.later) > 0 {
		callbacks := clock.later
		clock.later = nil
		for _, fn := range callbacks {
			fn()
		}
	}
}

func TestTidyActionsShowProgressCheckAndResult(t *testing.T) {
	for _, tc := range []struct {
		name                          string
		kind                          library.Issue
		button, result, running, done string
	}{
		{"extra-single", library.ExtraLinks, "删掉 Cursor 里的", "已删除", "正在删除链接", "已删除链接"},
		{"extra-all", library.ExtraLinks, "全部删掉", "已删除", "正在删除链接", "已删除链接"},
		{"reconnect", library.BrokenLinks, "重新连上", "已重新连上", "正在重新连接", "已重新连上"},
		{"remove-broken", library.BrokenLinks, "删掉链接", "已删除", "正在删除链接", "已删除链接"},
		{"keep-links", library.GlobalLinked, "保持链接", "已保留", "正在处理", "已处理"},
		{"project-only", library.GlobalLinked, "只在项目里用", "只在项目里使用", "正在处理", "已处理"},
		{"different-copies", library.DiffCopies, "对比", "已收成一份", "正在收成一份", "已收成一份"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a, tt := newTester(t)
			a.scope, a.tidy.kind = scopeTidy, tc.kind
			clock := stageTidy(a)
			chimes := 0
			a.sound = func() { chimes++ }
			tt.Frame()
			before := a.lib.Problems().Count(tc.kind)
			must(t, tt.Click(tc.button))
			if tc.kind == library.DiffCopies {
				tt.Frame()
				must(t, tt.Click("保留这个版本"))
			}
			tt.Frame()
			if !a.tidy.busy() || clock.work == nil {
				t.Fatal("action must show progress before doing work")
			}
			if _, ok := tt.Access(tc.running); !ok {
				t.Fatalf("missing running indicator: %s", tc.running)
			}
			captureTidyEvidence(t, tt, tc.name+"-running")
			if info, ok := tt.Access("重新检测"); !ok || !info.Disabled {
				t.Fatal("rescan must be disabled during work")
			}
			clock.work(func(fn func()) { fn() })
			tt.Frame()
			if _, ok := tt.Access(tc.done); !ok {
				t.Fatalf("missing completion check: %s", tc.done)
			}
			captureTidyEvidence(t, tt, tc.name+"-check")
			p := a.lib.Problems()
			if got := a.issueCount(&p, tc.kind); got != before {
				t.Fatalf("completed row must stay for its check: %d -> %d", before, got)
			}
			if chimes != 0 || shownText(a, fromTidy) != "" {
				t.Fatal("completion must wait until the check has been shown")
			}
			clock.finish()
			tt.Frame()
			captureTidyEvidence(t, tt, tc.name+"-done")
			if a.tidy.busy() || a.lib.Problems().Count(tc.kind) != before-1 {
				t.Fatal("work must finish and remove the resolved issue")
			}
			if got := shownText(a, fromTidy); !strings.Contains(got, tc.result) || shownKind(a, fromTidy) != toastOK {
				t.Fatalf("missing success toast: %q", got)
			}
			if chimes != 1 {
				t.Fatalf("want one completion sound, got %d", chimes)
			}
		})
	}
}

func TestCollectFromTidyShowsProgressAndResult(t *testing.T) {
	a, tt, h := importApp(t)
	src := h("code/demo/.claude/skills/probe")
	writeTestSkill(t, src, "probe", "body")
	must(t, os.MkdirAll(h(".claude/skills"), 0755))
	must(t, os.Symlink(src, h(".claude/skills/probe")))
	must(t, a.lib.AddProjects([]string{h("code/demo")}))
	a.rescan()
	a.scope, a.tidy.kind = scopeTidy, library.GlobalLinked
	clock := stageTidy(a)
	tt.Frame()
	must(t, tt.Click("收进我的技能"))
	dt := newUITester(a.collectDialog, 1280, 800)
	dt.Frame()
	must(t, dt.Click("收进我的技能"))
	tt.Frame()
	if !a.tidy.busy() || clock.work == nil {
		t.Fatal("collect must show progress after confirmation")
	}
	clock.work(func(fn func()) { fn() })
	tt.Frame()
	if !tt.HasText("probe") {
		t.Fatal("collected row must remain while its check is shown")
	}
	clock.finish()
	tt.Frame()
	if a.tidy.busy() || len(a.lib.Problems().Global) != 0 {
		t.Fatal("collect did not finish")
	}
	if got := shownText(a, fromTidy); !strings.Contains(got, "已收进我的技能") {
		t.Fatalf("missing collect result: %q", got)
	}
	if _, err := os.Stat(filepath.Join(h(".agents/skills/probe"), "SKILL.md")); err != nil {
		t.Fatal(err)
	}
}

func TestClearingRecordsUsesCompletionToast(t *testing.T) {
	a, tt := newTester(t)
	a.scope, a.tidy.kind = scopeTidy, library.StaleRecords
	tt.Frame()
	must(t, tt.Click("全部清除"))
	tt.Frame()
	if got := shownText(a, fromTidy); !strings.Contains(got, "已清理 34 条") {
		t.Fatalf("missing shared completion toast: %q", got)
	}
}

func TestTidyBatchContinuesAfterFailureAndKeepsFailedLink(t *testing.T) {
	a, tt, h := importApp(t)
	writeTestSkill(t, h(".agents/skills/probe"), "probe", "body")
	for _, dir := range []string{".codex/skills", ".cursor/skills"} {
		must(t, os.MkdirAll(h(dir), 0755))
		must(t, os.Symlink(h(".agents/skills/probe"), h(dir+"/probe")))
	}
	must(t, os.WriteFile(h(".cursor/config.json"), []byte("{}"), 0644))
	a.rescan()
	rows := a.lib.Problems().Extra
	if len(rows) != 2 {
		t.Fatalf("want two duplicate links for one skill, got %v", rows)
	}
	blocked := filepath.Dir(rows[0].Path)
	must(t, os.Chmod(blocked, 0555))
	t.Cleanup(func() { _ = os.Chmod(blocked, 0755) })
	a.scope, a.tidy.kind = scopeTidy, library.ExtraLinks
	clock := stageTidy(a)
	chimes := 0
	a.sound = func() { chimes++ }
	tt.Frame()
	must(t, tt.Click("全部删掉"))
	clock.work(func(fn func()) { fn() })
	tt.Frame()
	if _, ok := tt.Access("已删除链接"); !ok {
		t.Fatal("the successful link needs its completion check even when another link failed")
	}
	clock.finish()
	tt.Frame()
	captureTidyEvidence(t, tt, "partial-failure")
	left := a.lib.Problems().Extra
	if len(left) != 1 || left[0].Path != rows[0].Path {
		t.Fatalf("only the failed link should remain: %v", left)
	}
	if shownKind(a, fromTidy) != toastWarn || !strings.Contains(shownText(a, fromTidy), "已完成 1 项，1 项失败") {
		t.Fatalf("need partial result, got %q", shownText(a, fromTidy))
	}
	if chimes != 0 {
		t.Fatal("partial failure must not play the success sound")
	}
	if a.tidy.busy() {
		t.Fatal("failed batch must release the operation")
	}
	must(t, os.Chmod(blocked, 0755))
	a.bg, a.delay = nil, nil
	must(t, tt.Click("全部删掉"))
	tt.Frame()
	if len(a.lib.Problems().Extra) != 0 || shownKind(a, fromTidy) != toastOK {
		t.Fatal("failed link must be retryable")
	}
}

func TestFailedCollectShowsErrorAndKeepsRow(t *testing.T) {
	a, tt := newTester(t)
	a.scope, a.tidy.kind = scopeTidy, library.GlobalLinked
	clock := stageTidy(a)
	a.tidyProject(a.lib.Problems().Global[0], collectProjectSkill)
	clock.work(func(fn func()) { fn() })
	clock.finish()
	tt.Frame()
	if len(a.lib.Problems().Global) != 1 || a.tidy.busy() {
		t.Fatal("failed collection should leave its issue available")
	}
	if shownKind(a, fromTidy) != toastError || shownText(a, fromTidy) == "" {
		t.Fatal("failure must be visible in a persistent error toast")
	}
}

func TestTidyPreventsOverlappingOperationsAcrossCategories(t *testing.T) {
	a, tt := newTester(t)
	a.scope, a.tidy.kind = scopeTidy, library.ExtraLinks
	clock := stageTidy(a)
	rows := a.lib.Problems().Extra
	tt.Frame()
	must(t, tt.Click("全部删掉"))
	work := a.tidy.work
	must(t, tt.Click("项目技能被全局链接"))
	tt.Frame()
	for _, label := range []string{"保持链接", "只在项目里用", "收进我的技能"} {
		if info, ok := tt.Access(label); !ok || !info.Disabled {
			t.Fatalf("%s must be disabled during another operation", label)
		}
	}
	a.dropExtraLinks(rows)
	a.tidyProject(a.lib.Problems().Global[0], keepProjectLinks)
	if a.tidy.work != work {
		t.Fatal("a second action must not replace the in-flight operation")
	}
	clock.work(func(fn func()) { fn() })
	clock.finish()
	tt.Frame()
	if len(a.lib.Problems().Global) != 1 || shownText(a, fromTidy) == "" {
		t.Fatal("switching categories must preserve completion feedback without running the second action")
	}
}

// Optional evidence uses the actual MyGo renderer, never a mockup.
func TestTidyFeedbackSnapshots(t *testing.T) {
	dir := os.Getenv("TIDY_FEEDBACK_SHOTS")
	if dir == "" {
		t.Skip("set TIDY_FEEDBACK_SHOTS to capture evidence")
	}
	must(t, os.MkdirAll(dir, 0755))
	a, tt := newTester(t)
	a.scope, a.tidy.kind = scopeTidy, library.ExtraLinks
	tt.SetScale(2)
	tt.Frame()
	tt.Advance(time.Second)
	writePNG(t, filepath.Join(dir, "idle.png"), tt)
	clock := stageTidy(a)
	must(t, tt.Click("全部删掉"))
	tt.Frame()
	tt.Advance(300 * time.Millisecond)
	writePNG(t, filepath.Join(dir, "running.png"), tt)
	if clock.work != nil {
		clock.work(func(fn func()) { fn() })
	}
	tt.Frame()
	tt.Advance(300 * time.Millisecond)
	writePNG(t, filepath.Join(dir, "check.png"), tt)
	clock.finish()
	tt.Frame()
	tt.Advance(time.Second)
	writePNG(t, filepath.Join(dir, "done.png"), tt)
}

// Capture sequential frames from the real renderer with controlled work timing.
// This is visual evidence of transitions, not a performance measurement.
func TestTidyFeedbackRecording(t *testing.T) {
	dir := os.Getenv("TIDY_FEEDBACK_VIDEO")
	if dir == "" {
		t.Skip("set TIDY_FEEDBACK_VIDEO to render animation frames")
	}
	must(t, os.MkdirAll(dir, 0755))
	a, tt := newTester(t)
	a.scope, a.tidy.kind = scopeTidy, library.ExtraLinks
	var pending func(func(func()))
	type callback struct {
		at time.Duration
		fn func()
	}
	var callbacks []callback
	var elapsed time.Duration
	a.bg = func(fn func(func(func()))) { pending = fn }
	a.delay = func(d time.Duration, fn func()) { callbacks = append(callbacks, callback{elapsed + d, fn}) }
	tt.Frame()
	for frame := 0; frame < 105; frame++ {
		elapsed = time.Duration(frame) * time.Second / 30
		if frame == 30 {
			must(t, tt.Click("全部删掉"))
		}
		if frame == 35 && pending != nil {
			pending(func(fn func()) { fn() })
		}
		ready := callbacks
		callbacks = nil
		for _, cb := range ready {
			if cb.at <= elapsed {
				cb.fn()
			} else {
				callbacks = append(callbacks, cb)
			}
		}
		tt.Frame()
		tt.Advance(time.Second / 30)
		writePNG(t, filepath.Join(dir, fmt.Sprintf("frame%03d.png", frame)), tt)
	}
}

func captureTidyEvidence(t *testing.T, tt *ui.Tester, name string) {
	t.Helper()
	dir := os.Getenv("TIDY_FEEDBACK_SHOTS")
	if dir == "" {
		return
	}
	must(t, os.MkdirAll(dir, 0755))
	tt.Advance(400 * time.Millisecond)
	writePNG(t, filepath.Join(dir, name+".png"), tt)
}
