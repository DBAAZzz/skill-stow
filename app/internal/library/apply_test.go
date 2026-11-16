//go:build darwin

package library

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func preparedVite(t *testing.T) (*Library, func(string) string, *fakeRemote, *Prepared) {
	t.Helper()
	home, h, r, _ := viteHome(t)
	l, rep := checkNow(t, home, r)
	p, err := l.PrepareUpdate(context.Background(), r, itemNamed(t, rep, "vite"))
	must(t, err)
	t.Cleanup(func() { l.DiscardPrepared(p) })
	return l, h, r, p
}
func TestUpdatingReplacesOnlyTheSkillAndPreservesLinks(t *testing.T) {
	l, h, r, p := preparedVite(t)
	must(t, os.MkdirAll(h(".claude/skills"), 0755))
	mustLink(t, h(".agents/skills/vite"), h(".claude/skills/vite"))
	write(t, h(".claude"), "settings.json", `{"disabledSkills":["vite"]}`, 0600)
	before, _ := os.ReadFile(h(".claude/settings.json"))
	l.Settings.UpdateOldVersion = "trash"
	out, err := l.ApplyUpdate(context.Background(), r, p.Item, p, nil)
	must(t, err)
	if !out.Updated || treeOf(t, h(".agents/skills/vite")) != p.Item.Upstream {
		t.Fatal("新版没有生效")
	}
	target, err := os.Readlink(h(".claude/skills/vite"))
	must(t, err)
	if target != h(".agents/skills/vite") {
		t.Fatal("链接变了")
	}
	after, _ := os.ReadFile(h(".claude/settings.json"))
	if string(before) != string(after) {
		t.Fatal("配置被改了")
	}
	recs, err := ReadInstallRecords(l.Home)
	must(t, err)
	if recs.Get("vite").SkillFolderHash != p.Item.Upstream {
		t.Fatal("记录未更新")
	}
	if itemNamed(t, Scan(l.Home).Updates(), "vite").State != UpdLatest {
		t.Fatal("更新完成应是最新")
	}
}
func TestChangedAfterComparisonNeverOverwritesLocalFiles(t *testing.T) {
	l, h, r, p := preparedVite(t)
	l.Settings.UpdateOldVersion = "trash"
	write(t, h(".agents/skills/vite"), "mine.txt", "keep me", 0644)
	_, err := l.ApplyUpdate(context.Background(), r, p.Item, p, nil)
	if !errors.Is(err, ErrUpdateChanged) {
		t.Fatalf("want changed, got %v", err)
	}
	if b, _ := os.ReadFile(h(".agents/skills/vite/mine.txt")); string(b) != "keep me" {
		t.Fatal("丢失本地修改")
	}
}
func TestTamperedDownloadDoesNotTouchInstalledSkill(t *testing.T) {
	home, h, r, base := viteHome(t)
	l, rep := checkNow(t, home, r)
	r.edit = func(_ string, b []byte) []byte { return append(b, '!') }
	_, err := l.PrepareUpdate(context.Background(), r, itemNamed(t, rep, "vite"))
	if err == nil || treeOf(t, h(".agents/skills/vite")) != base {
		t.Fatal("篡改的下载不能替换本机")
	}
}
func TestChangedEffectiveNameIsReportedBeforeReplacing(t *testing.T) {
	home, h, r, base := viteHome(t)
	r.publish(t, "antfu/skills", map[string]string{"skills/vite/SKILL.md": skillMD("renamed", "new")})
	l, rep := checkNow(t, home, r)
	_, err := l.PrepareUpdate(context.Background(), r, itemNamed(t, rep, "vite"))
	if state := itemNamed(t, Scan(home).Updates(), "vite").State; state != UpdRenamed {
		t.Fatalf("renamed remains eligible: %s", state)
	}
	if !errors.Is(err, ErrSkillRenamed) || treeOf(t, h(".agents/skills/vite")) != base {
		t.Fatalf("rename: %v", err)
	}
}
func TestDownloadRejectsLFSAndTraversal(t *testing.T) {
	home, _, r, _ := viteHome(t)
	r.publish(t, "antfu/skills", map[string]string{"skills/vite/SKILL.md": skillMD("vite", "new"), "skills/vite/big": "version https://git-lfs.github.com/spec/v1\noid sha256:x\nsize 1\n"})
	l, rep := checkNow(t, home, r)
	_, err := l.PrepareUpdate(context.Background(), r, itemNamed(t, rep, "vite"))
	if err == nil {
		t.Fatal("LFS must fail")
	}
	for _, p := range []string{"../escape", "/absolute", "a/../../escape", "a\\b"} {
		if validTreeFiles([]TreeEntry{{Path: p, Type: "blob", Mode: "100644"}}, "") == nil {
			t.Fatalf("accepted %s", p)
		}
	}
}
func TestSnapshotRejectsRecordChangesAndNewCopies(t *testing.T) {
	for _, kind := range []string{"record", "version", "copy", "link", "deleted"} {
		t.Run(kind, func(t *testing.T) {
			l, h, r, p := preparedVite(t)
			l.Settings.UpdateOldVersion = "trash"
			switch kind {
			case "record":
				writeRecords(t, h, gh("vite", "other/repo", "SKILL.md", strings.Repeat("a", 40)))
			case "version":
				writeRecordsVersion(t, h, 4, gh("vite", "antfu/skills", "skills/vite/SKILL.md", p.Item.Record.SkillFolderHash))
			case "copy":
				must(t, os.MkdirAll(h(".claude"), 0755))
				writeSkill(t, h(".claude/skills/vite"), "copy")
			case "link":
				must(t, os.Rename(p.Item.Place, h("moved")))
				must(t, os.Symlink(h("moved"), p.Item.Place))
			case "deleted":
				must(t, os.RemoveAll(p.Item.Place))
			}
			_, err := l.ApplyUpdate(context.Background(), r, p.Item, p, nil)
			if err == nil {
				t.Fatal("changed target accepted")
			}
		})
	}
}
func TestPreparedDownloadContainsExecutableFilesAndLocalDeletions(t *testing.T) {
	home, h, r, _ := viteHome(t)
	write(t, h(".agents/skills/vite"), "my-template", "keep", 0644)
	l, rep := checkNow(t, home, r)
	p, err := l.PrepareUpdate(context.Background(), r, itemNamed(t, rep, "vite"))
	must(t, err)
	defer l.DiscardPrepared(p)
	fi, err := os.Stat(filepath.Join(p.Dir, "run.sh"))
	must(t, err)
	if fi.Mode()&0100 == 0 {
		t.Fatal("lost exec bit")
	}
	found := false
	for _, d := range l.CompareUpdate(p) {
		if d.Path == "my-template" && d.OnlyLeft {
			found = true
		}
	}
	if !found {
		t.Fatal("comparison omitted local-only file")
	}
}

func TestRecordWriteFailureKeepsOldFilesUntilRetry(t *testing.T) {
	l, h, r, p := preparedVite(t)
	l.Settings.UpdateOldVersion = "trash"
	must(t, os.Chmod(h(".agents"), 0555))
	t.Cleanup(func() { os.Chmod(h(".agents"), 0755) })
	out, err := l.ApplyUpdate(context.Background(), r, p.Item, p, nil)
	if err == nil || out.Updated {
		t.Fatal("record failure reported success")
	}
	if treeOf(t, p.Item.Place) != p.Item.Upstream || treeOf(t, p.Dir) != p.Item.Local {
		t.Fatal("both versions must remain")
	}
	must(t, os.Chmod(h(".agents"), 0755))
	must(t, l.RetryRecord(p.Item))
	if itemNamed(t, Scan(l.Home).Updates(), "vite").State != UpdLatest {
		t.Fatal("retry did not finish")
	}
}
func TestRootUpdateWritesCommitInsteadOfTree(t *testing.T) {
	home, h := updatesHome(t)
	r := newFakeRemote()
	files := map[string]string{"SKILL.md": skillMD("root", "old")}
	old := r.publish(t, "o/root", files)
	install(t, h, "root", files)
	writeRecords(t, h, gh("root", "o/root", "SKILL.md", old))
	newCommit := r.publish(t, "o/root", map[string]string{"SKILL.md": skillMD("root", "new")})
	l, rep := checkNow(t, home, r)
	l.Settings.UpdateOldVersion = "trash"
	out, err := l.ApplyUpdate(context.Background(), r, itemNamed(t, rep, "root"), nil, nil)
	must(t, err)
	if !out.Updated {
		t.Fatal("not updated")
	}
	recs, err := ReadInstallRecords(home)
	must(t, err)
	if recs.Get("root").SkillFolderHash != newCommit {
		t.Fatal("wrong record hash")
	}
}

func TestPrepareUpdateReusesUpstreamCacheAcrossCompareAndApply(t *testing.T) {
	home, h, r, _ := viteHome(t)
	l, rep := checkNow(t, home, r)
	it := itemNamed(t, rep, "vite")

	// 1. 首次查看差异：走远端下载并写入按 Upstream 哈希索引的本地缓存
	p1, err := l.PrepareUpdate(context.Background(), r, it)
	must(t, err)
	firstFiles := r.count("file ")
	if firstFiles == 0 {
		t.Fatal("expected initial download from remote")
	}
	cachePath := filepath.Join(upstreamCacheDir(home), it.Upstream)
	if h, e := TreeHash(cachePath); e != nil || h != it.Upstream {
		t.Fatalf("cache missing or hash mismatch: %v %q", e, h)
	}
	l.DiscardPrepared(p1)

	// 2. 即使此时断网（远端所有请求失败），再次查看差异仍命中缓存，不产生额外下载
	r.fail["antfu/skills"] = errors.New("offline")
	p2, err := l.PrepareUpdate(context.Background(), r, it)
	must(t, err)
	if r.count("file ") != firstFiles {
		t.Fatalf("second PrepareUpdate downloaded again: %d -> %d", firstFiles, r.count("file "))
	}
	l.DiscardPrepared(p2)

	// 3. 之后在列表页直接点更新（p == nil）同样命中缓存完成更新
	l.Settings.UpdateOldVersion = "trash"
	out, err := l.ApplyUpdate(context.Background(), r, it, nil, nil)
	must(t, err)
	if !out.Updated || r.count("file ") != firstFiles {
		t.Fatalf("ApplyUpdate did not reuse cache: updated=%v files=%d", out.Updated, r.count("file "))
	}
	if treeOf(t, h(".agents/skills/vite")) != it.Upstream {
		t.Fatal("updated directory content mismatch")
	}
}

func TestPrepareUpdateWithProgressConcurrentMultiFile(t *testing.T) {
	home, _, r, _ := viteHome(t)
	files := map[string]string{
		"skills/vite/SKILL.md": viteV2["skills/vite/SKILL.md"],
	}
	for i := 0; i < 12; i++ {
		files[fmt.Sprintf("skills/vite/references/doc-%02d.md", i)] = fmt.Sprintf("# doc %d\n", i)
	}
	r.publish(t, "antfu/skills", files)
	l, rep := checkNow(t, home, r)
	it := itemNamed(t, rep, "vite")

	var mu sync.Mutex
	var events []int
	var lastTotal int
	p, err := l.PrepareUpdateWithProgress(context.Background(), r, it, func(done, total int) {
		mu.Lock()
		events = append(events, done)
		lastTotal = total
		mu.Unlock()
	})
	must(t, err)
	defer l.DiscardPrepared(p)
	if lastTotal != len(files) || len(events) == 0 || events[len(events)-1] != len(files) {
		t.Fatalf("unexpected progress events total=%d events=%v", lastTotal, events)
	}
}

// 单条更新也走统一进度管线（方案 B）：开始一条 Active，结束一条完成。
func TestApplyUpdateReportsProgress(t *testing.T) {
	l, _, r, p := preparedVite(t)
	l.Settings.UpdateOldVersion = "trash"
	var events []UpdateProgress
	out, err := l.ApplyUpdate(context.Background(), r, p.Item, p, func(e UpdateProgress) {
		events = append(events, e)
	})
	must(t, err)
	if !out.Updated {
		t.Fatal("应已更新")
	}
	if len(events) != 2 {
		t.Fatalf("事件数 = %d, want 2: %+v", len(events), events)
	}
	if !events[0].Active || events[0].Done != 0 || events[0].Total != 1 || events[0].Name != "vite" || events[0].Failure != "" {
		t.Errorf("开始事件 = %+v", events[0])
	}
	if events[1].Active || events[1].Done != 1 || events[1].Total != 1 || events[1].Name != "vite" || events[1].Failure != "" {
		t.Errorf("完成事件 = %+v", events[1])
	}
}

func TestApplyUpdateRollsBackPendingTxOnBeforeSwapFailure(t *testing.T) {
	l, h, r, p := preparedVite(t)
	l.Settings.UpdateOldVersion = "trash"
	defer func() { afterTxSaveHook = nil }()
	afterTxSaveHook = func() {
		writeRecords(t, h, gh("vite", "other/repo", "SKILL.md", strings.Repeat("a", 40)))
	}
	_, err := l.ApplyUpdate(context.Background(), r, p.Item, p, nil)
	if !errors.Is(err, ErrRecordChanged) {
		t.Fatalf("want ErrRecordChanged, got %v", err)
	}
	st, serr := loadUpdates(l.Home)
	must(t, serr)
	if len(st.Transactions) != 0 {
		t.Fatalf("updates.json still has pending tx: %+v", st.Transactions)
	}
	if _, statErr := os.Stat(p.Dir); !os.IsNotExist(statErr) {
		t.Fatalf("staging dir not removed: %v", statErr)
	}
}

func TestApplyUpdateScanCount(t *testing.T) {
	var scans int
	defer func() { scanHook = nil }()

	l, _, r, p := preparedVite(t)
	l.Settings.UpdateOldVersion = "trash"
	scanHook = func() { scans++ }
	out, err := l.ApplyUpdate(context.Background(), r, p.Item, p, nil)
	must(t, err)
	if !out.Updated {
		t.Fatal("expected updated")
	}
	t.Logf("ApplyUpdate(p != nil) Scan count = %d", scans)
	if scans != 1 {
		t.Fatalf("ApplyUpdate(p != nil) Scan count = %d, want 1", scans)
	}

	home2, _, r2, _ := viteHome(t)
	l2, rep2 := checkNow(t, home2, r2)
	l2.Settings.UpdateOldVersion = "trash"
	scans = 0
	out2, err := l2.ApplyUpdate(context.Background(), r2, itemNamed(t, rep2, "vite"), nil, nil)
	must(t, err)
	if !out2.Updated {
		t.Fatal("expected updated")
	}
	t.Logf("ApplyUpdate(p == nil) Scan count = %d", scans)
	if scans != 2 {
		t.Fatalf("ApplyUpdate(p == nil) Scan count = %d, want 2", scans)
	}
}
