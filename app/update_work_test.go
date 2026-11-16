package main

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"skillmanager/internal/library"
)

func TestCheckingUpdatesClearsPreviousRowFailures(t *testing.T) {
	a := newApp(library.Scan(t.TempDir()))
	a.lib.Settings.UpdateProxyMode = "env"
	a.upd.errors = map[string]string{"vite": "download failed"}
	a.checkUpdates(false)
	if len(a.upd.errors) != 0 || a.upd.err != "" {
		t.Fatalf("successful check kept old failures: %v %q", a.upd.errors, a.upd.err)
	}
}

func TestUpdateMarkRunsInBackgroundAndDeliversErrorsOnUIThread(t *testing.T) {
	a := newApp(library.Scan(t.TempDir()))
	var worker func(func(func()))
	a.bg = func(work func(func(func()))) { worker = work }
	called := false
	a.updateMark(func(l *library.Library) error {
		called = true
		if l == a.lib {
			t.Error("background mark reused UI library")
		}
		return errors.New("save failed")
	})
	if called || worker == nil || a.upd.cancel == nil {
		t.Fatal("mark ran on the click thread instead of starting a background task")
	}
	var queue []func()
	worker(func(fn func()) { queue = append(queue, fn) })
	if !called || shownText(a, fromUpdate) != "" {
		t.Fatal("worker must execute the mark and defer UI changes")
	}
	for _, fn := range queue {
		fn()
	}
	if shownText(a, fromUpdate) != "save failed" || a.upd.cancel != nil {
		t.Fatalf("completion lost error or left task busy: %+v", a.upd)
	}
}

func TestKeepLocalRunsInBackgroundAndKeepsComparisonOnFailure(t *testing.T) {
	a := newApp(library.Scan(t.TempDir()))
	p := &library.Prepared{Item: library.UpdateItem{Name: "vite", State: library.UpdReady}, Dir: t.TempDir()}
	st := &compareState{updating: true}
	a.upd.prepared, a.upd.diff = p, st
	var worker func(func(func()))
	a.bg = func(work func(func(func()))) { worker = work }
	tt := newUITester(func(c *ui.Context) { theme(c); a.updateCompareHeader(c, p, st) }, 1280, 180)
	must(t, tt.Click("保留本地"))
	if worker == nil || a.upd.cancel == nil || shownText(a, fromUpdate) != "" {
		t.Fatal("keep local performed disk work on the click thread")
	}
	var queue []func()
	worker(func(fn func()) { queue = append(queue, fn) })
	for _, fn := range queue {
		fn()
	}
	if a.upd.prepared != p || a.upd.diff != st || shownKind(a, fromUpdate) != toastError || shownText(a, fromUpdate) == "" {
		t.Fatal("failed ignore must keep the comparison and show its error")
	}
}

func TestStartUpdateWorkQueuedDeliveryPreservesErrorAndCancellationSemantics(t *testing.T) {
	t.Run("normal failure is not treated as canceled after worker releases ctx", func(t *testing.T) {
		a := newApp(library.Scan(t.TempDir()))
		var queue []func()
		a.bg = func(work func(func(func()))) {
			work(func(fn func()) { queue = append(queue, fn) })
		}
		a.startUpdateWork(taskDownload, func(context.Context, *library.Library, func(func())) updateWorkResult {
			return updateWorkResult{err: errors.New("network timeout"), toastErr: true}
		})
		for _, fn := range queue {
			fn()
		}
		if a.upd.err != "" || shownKind(a, fromUpdate) != toastError || shownText(a, fromUpdate) != "network timeout" {
			t.Fatalf("unexpected result: err=%q kind=%v toast=%q", a.upd.err, shownKind(a, fromUpdate), shownText(a, fromUpdate))
		}
	})

	t.Run("user cancel suppresses banner and toast errors", func(t *testing.T) {
		for _, toastErr := range []bool{false, true} {
			a := newApp(library.Scan(t.TempDir()))
			var queue []func()
			a.bg = func(work func(func(func()))) {
				work(func(fn func()) { queue = append(queue, fn) })
			}
			a.startUpdateWork(taskCheck, func(ctx context.Context, _ *library.Library, _ func(func())) updateWorkResult {
				a.upd.cancel()
				return updateWorkResult{err: ctx.Err(), toastErr: toastErr}
			})
			for _, fn := range queue {
				fn()
			}
			if a.upd.err != "" || shownText(a, fromUpdate) != "" {
				t.Fatalf("toastErr=%v leaked error after cancel: err=%q toast=%q", toastErr, a.upd.err, shownText(a, fromUpdate))
			}
		}
	})
}

func TestRunUpdatesSingleItemOutcomesUnderQueuedPost(t *testing.T) {
	t.Run("single failure shows error toast even after worker releases ctx", func(t *testing.T) {
		a := newApp(library.Scan(t.TempDir()))
		p := &library.Prepared{Item: library.UpdateItem{Name: "vite"}, Dir: t.TempDir()}
		a.upd.prepared, a.upd.diff = p, &compareState{updating: true}
		var queue []func()
		a.bg = func(work func(func(func()))) {
			work(func(fn func()) { queue = append(queue, fn) })
		}
		a.startUpdateWork(taskUpdate, func(ctx context.Context, l *library.Library, _ func(func())) updateWorkResult {
			res := runSingleUpdate(ctx, l, stubUpdateRemote{}, p.Item, p, nil)
			return buildRunWorkResult(res, func() {
				a.upd.prepared = nil
				a.upd.diff = nil
			})
		})
		for _, fn := range queue {
			fn()
		}
		if shownKind(a, fromUpdate) != toastError || shownText(a, fromUpdate) == "" || a.upd.err != "" {
			t.Fatalf("expected error toast without banner, got err=%q kind=%v toast=%q", a.upd.err, shownKind(a, fromUpdate), shownText(a, fromUpdate))
		}
		if a.upd.prepared != nil || a.upd.diff != nil {
			t.Fatal("single update completion must close comparison view")
		}
	})

	t.Run("single warning is not overwritten by ok toast", func(t *testing.T) {
		a := newApp(library.Scan(t.TempDir()))
		a.upd.prepared, a.upd.diff = &library.Prepared{}, &compareState{updating: true}
		a.finishUpdateWork(buildRunWorkResult(updateRunResult{
			single:   true,
			itemName: "vite",
			updated:  1,
			warning:  "更新已完成，旧版本已保留：trash failed",
		}, func() {
			a.upd.prepared = nil
			a.upd.diff = nil
		}), false)
		if shownKind(a, fromUpdate) != toastWarn || shownText(a, fromUpdate) != "更新已完成，旧版本已保留：trash failed" {
			t.Fatalf("got kind=%v toast=%q", shownKind(a, fromUpdate), shownText(a, fromUpdate))
		}
		if a.upd.prepared != nil || a.upd.diff != nil {
			t.Fatal("expected comparison view cleared")
		}
	})

	t.Run("single cancel closes compare without error toast", func(t *testing.T) {
		a := newApp(library.Scan(t.TempDir()))
		a.upd.prepared, a.upd.diff = &library.Prepared{}, &compareState{updating: true}
		a.finishUpdateWork(buildRunWorkResult(updateRunResult{
			single:   true,
			itemName: "vite",
			failed:   1,
			err:      context.Canceled,
		}, func() {
			a.upd.prepared = nil
			a.upd.diff = nil
		}), true)
		if shownText(a, fromUpdate) != "" || a.upd.prepared != nil || a.upd.diff != nil {
			t.Fatalf("unexpected state after cancel: toast=%q prepared=%v", shownText(a, fromUpdate), a.upd.prepared)
		}
	})
}

func TestRunUpdatesBatchProgressAndOutcomesUnderQueuedPost(t *testing.T) {
	home := t.TempDir()
	remote := newBatchFakeRemote()
	remote.addSkill(t, home, "vite", "antfu/skills", "skills/vite", "v1", "v2")
	remote.addSkill(t, home, "vitest", "antfu/skills", "skills/vitest", "v1", "v2")
	remote.failFile["skills/vitest/SKILL.md"] = errors.New("download failed")

	l := library.Scan(home)
	l.Settings.UpdateOldVersion = "delete"
	must(t, l.CheckUpdates(context.Background(), remote, nil))
	items := l.Updates().Ready()
	if len(items) != 2 {
		t.Fatalf("expected 2 ready items, got %d", len(items))
	}

	a := newApp(library.Scan(home))
	a.lib.Settings.UpdateOldVersion = "delete"
	a.upd.prepared, a.upd.diff = &library.Prepared{Dir: t.TempDir()}, &compareState{updating: true}

	var queue []func()
	a.bg = func(work func(func(func()))) {
		work(func(fn func()) { queue = append(queue, fn) })
	}

	a.startUpdateWork(taskUpdate, func(ctx context.Context, lib *library.Library, post func(func())) updateWorkResult {
		res := runBatchUpdates(ctx, lib, remote, items, func(pr library.UpdateProgress) {
			post(func() { a.applyUpdateProgress(pr) })
		})
		return buildRunWorkResult(res, func() {
			a.upd.prepared = nil
			a.upd.diff = nil
		})
	})

	if shownText(a, fromUpdate) != "" || len(a.upd.errors) != 0 {
		t.Fatal("queued posts must not mutate UI before draining")
	}
	for _, fn := range queue {
		fn()
	}

	if a.upd.cancel != nil || a.upd.batch != nil {
		t.Fatalf("completion left busy or batch state: cancel=%v batch=%v", a.upd.cancel != nil, a.upd.batch)
	}
	if shownKind(a, fromUpdate) != toastWarn || shownText(a, fromUpdate) != "已更新 1 个，1 个失败" {
		t.Fatalf("unexpected partial toast: kind=%v text=%q", shownKind(a, fromUpdate), shownText(a, fromUpdate))
	}
	if a.upd.errors["vitest"] == "" || a.upd.errors["vite"] != "" {
		t.Fatalf("unexpected row errors: %v", a.upd.errors)
	}
	if a.upd.prepared != nil || a.upd.diff != nil {
		t.Fatal("batch completion must close comparison view")
	}
	b, err := os.ReadFile(filepath.Join(home, ".agents/skills/vite/SKILL.md"))
	must(t, err)
	if string(b) != "---\nname: vite\ndescription: v2\n---\n" {
		t.Fatalf("vite was not updated: %q", string(b))
	}
}

func TestRunUpdatesBatchPreItemFailureShowsToastUnderQueuedPost(t *testing.T) {
	home := t.TempDir()
	remote := newBatchFakeRemote()
	remote.addSkill(t, home, "vite", "antfu/skills", "skills/vite", "v1", "v2")
	remote.addSkill(t, home, "vitest", "antfu/skills", "skills/vitest", "v1", "v2")

	l := library.Scan(home)
	l.Settings.UpdateOldVersion = "delete"
	must(t, l.CheckUpdates(context.Background(), remote, nil))
	items := l.Updates().Ready()

	entered := make(chan struct{})
	release := make(chan struct{})
	remote.onFile = func(path string) {
		if path == "skills/vite/SKILL.md" {
			close(entered)
			<-release
		}
	}

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		_, _ = l.PrepareUpdate(context.Background(), remote, items[0])
	}()
	<-entered
	remote.onFile = nil

	a := newApp(library.Scan(home))
	a.lib.Settings.UpdateOldVersion = "delete"
	a.upd.prepared, a.upd.diff = &library.Prepared{Dir: t.TempDir()}, &compareState{updating: true}
	var queue []func()
	a.bg = func(work func(func(func()))) {
		work(func(fn func()) { queue = append(queue, fn) })
	}

	a.startUpdateWork(taskUpdate, func(ctx context.Context, lib *library.Library, post func(func())) updateWorkResult {
		res := runBatchUpdates(ctx, lib, remote, items, func(pr library.UpdateProgress) {
			post(func() { a.applyUpdateProgress(pr) })
		})
		return buildRunWorkResult(res, func() {
			a.upd.prepared = nil
			a.upd.diff = nil
		})
	})

	close(release)
	wg.Wait()

	for _, fn := range queue {
		fn()
	}
	if shownKind(a, fromUpdate) != toastError || shownText(a, fromUpdate) != library.ErrBusy.Error() {
		t.Fatalf("pre-item error lost: kind=%v toast=%q", shownKind(a, fromUpdate), shownText(a, fromUpdate))
	}
	if a.upd.prepared != nil || a.upd.diff != nil {
		t.Fatal("pre-item batch failure must still close comparison view")
	}
}

func TestRunUpdatesBatchTotalFailureAndCancelUnderQueuedPost(t *testing.T) {
	t.Run("all failed without cancel shows error toast after ctx release", func(t *testing.T) {
		home := t.TempDir()
		remote := newBatchFakeRemote()
		remote.addSkill(t, home, "vite", "antfu/skills", "skills/vite", "v1", "v2")
		remote.addSkill(t, home, "vitest", "antfu/skills", "skills/vitest", "v1", "v2")
		remote.failFile["skills/vite/SKILL.md"] = errors.New("boom 1")
		remote.failFile["skills/vitest/SKILL.md"] = errors.New("boom 2")

		l := library.Scan(home)
		l.Settings.UpdateOldVersion = "delete"
		must(t, l.CheckUpdates(context.Background(), remote, nil))
		items := l.Updates().Ready()

		a := newApp(library.Scan(home))
		a.lib.Settings.UpdateOldVersion = "delete"
		var queue []func()
		a.bg = func(work func(func(func()))) {
			work(func(fn func()) { queue = append(queue, fn) })
		}
		a.startUpdateWork(taskUpdate, func(ctx context.Context, lib *library.Library, post func(func())) updateWorkResult {
			res := runBatchUpdates(ctx, lib, remote, items, func(pr library.UpdateProgress) {
				post(func() { a.applyUpdateProgress(pr) })
			})
			return buildRunWorkResult(res, func() {
				a.upd.prepared = nil
				a.upd.diff = nil
			})
		})
		for _, fn := range queue {
			fn()
		}
		if shownKind(a, fromUpdate) != toastError || shownText(a, fromUpdate) != "更新失败（2 个）" {
			t.Fatalf("got kind=%v toast=%q", shownKind(a, fromUpdate), shownText(a, fromUpdate))
		}
		if len(a.upd.errors) != 2 {
			t.Fatalf("expected 2 row errors, got %v", a.upd.errors)
		}
	})

	t.Run("batch canceled after partial progress reports updated count", func(t *testing.T) {
		a := newApp(library.Scan(t.TempDir()))
		a.finishUpdateWork(buildRunWorkResult(updateRunResult{updated: 2, failed: 0}, nil), true)
		if shownKind(a, fromUpdate) != toastOK || shownText(a, fromUpdate) != "已更新 2 个技能。" {
			t.Fatalf("got kind=%v toast=%q", shownKind(a, fromUpdate), shownText(a, fromUpdate))
		}
	})

	t.Run("batch canceled with zero updated suppresses error toast", func(t *testing.T) {
		a := newApp(library.Scan(t.TempDir()))
		a.finishUpdateWork(buildRunWorkResult(updateRunResult{updated: 0, failed: 1, err: context.Canceled}, nil), true)
		if shownText(a, fromUpdate) != "" {
			t.Fatalf("unexpected toast after cancel: %q", shownText(a, fromUpdate))
		}
	})
}

func TestApplyUpdateProgressIgnoresEmptyNameInitialProgress(t *testing.T) {
	a := newApp(library.Scan(t.TempDir()))
	a.applyUpdateProgress(library.UpdateProgress{Done: 0, Total: 3})
	if len(a.upd.batch) != 0 {
		t.Fatalf("initial progress polluted batch map: %v", a.upd.batch)
	}
	if a.upd.progress.Total != 3 {
		t.Fatalf("progress total = %d, want 3", a.upd.progress.Total)
	}
}

func TestConcurrentBackgroundWorkerAndUIPostDeliveryHasNoRaces(t *testing.T) {
	home := t.TempDir()
	remote := newBatchFakeRemote()
	remote.addSkill(t, home, "vite", "antfu/skills", "skills/vite", "v1", "v2")
	remote.addSkill(t, home, "vitest", "antfu/skills", "skills/vitest", "v1", "v2")

	l := library.Scan(home)
	l.Settings.UpdateOldVersion = "delete"
	must(t, l.CheckUpdates(context.Background(), remote, nil))
	items := l.Updates().Ready()

	item2InFlight := make(chan struct{})
	uiSawItem1Done := make(chan struct{})
	remote.onFile = func(path string) {
		if path == "skills/vitest/SKILL.md" {
			close(item2InFlight)
			<-uiSawItem1Done
		}
	}

	a := newApp(library.Scan(home))
	a.lib.Settings.UpdateOldVersion = "delete"
	posts := make(chan func(), 16)
	a.bg = func(work func(func(func()))) {
		go func() {
			work(func(fn func()) { posts <- fn })
			close(posts)
		}()
	}

	a.startUpdateWork(taskUpdate, func(ctx context.Context, lib *library.Library, post func(func())) updateWorkResult {
		res := runBatchUpdates(ctx, lib, remote, items, func(pr library.UpdateProgress) {
			post(func() { a.applyUpdateProgress(pr) })
		})
		return buildRunWorkResult(res, func() {
			a.upd.prepared = nil
			a.upd.diff = nil
		})
	})

	// UI 线程在 worker 运行期间持续消费 post，并在第 2 个技能阻塞下载时已看到第 1 个完成。
	released := false
	for fn := range posts {
		fn()
		if !released && a.upd.batch["vite"] == batchDone {
			<-item2InFlight
			if a.upd.cancel == nil {
				t.Fatal("worker exited before UI acknowledged overlap")
			}
			close(uiSawItem1Done)
			released = true
		}
	}
	if !released {
		t.Fatal("UI did not observe item 1 completion while worker was running")
	}
	if a.upd.cancel != nil || shownKind(a, fromUpdate) != toastOK || shownText(a, fromUpdate) != "已更新 2 个技能。" {
		t.Fatalf("unexpected concurrent background result: cancel=%v kind=%v toast=%q", a.upd.cancel != nil, shownKind(a, fromUpdate), shownText(a, fromUpdate))
	}
}

type stubUpdateRemote struct{}

func (stubUpdateRemote) Commit(context.Context, string, string) (library.Commit, error) {
	return library.Commit{}, errors.New("unreachable")
}
func (stubUpdateRemote) Tree(context.Context, string, string) (library.Tree, error) {
	return library.Tree{}, errors.New("unreachable")
}
func (stubUpdateRemote) File(context.Context, string, string, string) ([]byte, error) {
	return nil, errors.New("unreachable")
}

type batchFakeRemote struct {
	commit   library.Commit
	entries  []library.TreeEntry
	files    map[string][]byte
	failFile map[string]error
	onFile   func(path string)
	records  map[string]map[string]any
}

func newBatchFakeRemote() *batchFakeRemote {
	return &batchFakeRemote{
		commit: library.Commit{
			SHA:  "3f8a21c000000000000000000000000000000000",
			Tree: "4b825dc642cb6eb9a060e54bf8d69288fbee4904",
			Date: time.Now().Add(-time.Hour),
		},
		files:    map[string][]byte{},
		failFile: map[string]error{},
		records:  map[string]map[string]any{},
	}
}

func (r *batchFakeRemote) addSkill(t *testing.T, home, name, repo, skillDir, oldDesc, newDesc string) {
	t.Helper()
	oldBody := fmt.Sprintf("---\nname: %s\ndescription: %s\n---\n", name, oldDesc)
	dir := filepath.Join(home, ".agents/skills", name)
	must(t, os.MkdirAll(dir, 0755))
	must(t, os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(oldBody), 0644))
	oldHash, err := library.TreeHash(dir)
	must(t, err)

	newBody := []byte(fmt.Sprintf("---\nname: %s\ndescription: %s\n---\n", name, newDesc))
	newDir := t.TempDir()
	must(t, os.WriteFile(filepath.Join(newDir, "SKILL.md"), newBody, 0644))
	newTreeHash, err := library.TreeHash(newDir)
	must(t, err)

	path := skillDir + "/SKILL.md"
	r.files[path] = newBody
	r.entries = append(r.entries,
		library.TreeEntry{
			Path: skillDir,
			Mode: "040000",
			Type: "tree",
			SHA:  newTreeHash,
		},
		library.TreeEntry{
			Path: path,
			Mode: "100644",
			Type: "blob",
			SHA:  gitBlobSHA(newBody),
			Size: int64(len(newBody)),
		},
	)
	r.records[name] = map[string]any{
		"source":          repo,
		"sourceType":      "github",
		"sourceUrl":       "https://github.com/" + repo + ".git",
		"skillPath":       path,
		"skillFolderHash": oldHash,
		"installedAt":     "2026-08-20T01:02:03.000Z",
		"updatedAt":       "2026-08-20T01:02:03.000Z",
	}
	lockData, err := json.MarshalIndent(map[string]any{"version": 3, "skills": r.records}, "", "  ")
	must(t, err)
	must(t, os.WriteFile(filepath.Join(home, ".agents/.skill-lock.json"), lockData, 0644))
}

func gitBlobSHA(b []byte) string {
	h := sha1.New()
	fmt.Fprintf(h, "blob %d\x00", len(b))
	h.Write(b)
	return hex.EncodeToString(h.Sum(nil))
}

func (r *batchFakeRemote) Commit(context.Context, string, string) (library.Commit, error) {
	return r.commit, nil
}

func (r *batchFakeRemote) Tree(context.Context, string, string) (library.Tree, error) {
	return library.Tree{Entries: r.entries}, nil
}

func (r *batchFakeRemote) File(_ context.Context, _, _, path string) ([]byte, error) {
	if r.onFile != nil {
		r.onFile(path)
	}
	if err := r.failFile[path]; err != nil {
		return nil, err
	}
	b, ok := r.files[path]
	if !ok {
		return nil, errors.New("not found")
	}
	return b, nil
}
