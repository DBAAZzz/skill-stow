package library

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"
)

var (
	viteV1 = map[string]string{"skills/vite/SKILL.md": skillMD("vite", "v1"), "skills/vite/run.sh*": "#!/bin/sh\n"}
	viteV2 = map[string]string{"skills/vite/SKILL.md": skillMD("vite", "v2"), "skills/vite/run.sh*": "#!/bin/sh\n"}
)

func local(files map[string]string, prefix string) map[string]string {
	out := map[string]string{}
	for p, b := range files {
		if rest, ok := strings.CutPrefix(p, prefix); ok {
			out[rest] = b
		}
	}
	return out
}

// viteHome：vite 装的是 v1（记录哈希 = 上游 v1 的目录），上游现在是 v2。
func viteHome(t *testing.T) (string, func(string) string, *fakeRemote, string) {
	t.Helper()
	home, h := updatesHome(t)
	r := newFakeRemote()
	r.publish(t, "antfu/skills", viteV1)
	installed := r.upstreamTree("antfu/skills", "skills/vite")
	install(t, h, "vite", local(viteV1, "skills/vite/"))
	writeRecords(t, h, gh("vite", "antfu/skills", "skills/vite/SKILL.md", installed))
	r.publish(t, "antfu/skills", viteV2)
	return home, h, r, installed
}

func TestUpstreamChangeOfAnUntouchedSkillIsReadyToUpdate(t *testing.T) {
	home, _, r, installed := viteHome(t)
	_, rep := checkNow(t, home, r)
	it := itemNamed(t, rep, "vite")
	if it.State != UpdReady || it.Base != installed || it.Upstream != r.upstreamTree("antfu/skills", "skills/vite") {
		t.Errorf("vite = %+v", it)
	}
	if it.Commit == "" || it.CommitDate.IsZero() || it.Source.Repo != "antfu/skills" {
		t.Errorf("应带上游提交：%+v", it)
	}
	if rep.Count() != 1 || !it.CanUpdate() {
		t.Errorf("可更新数 %d，CanUpdate %v", rep.Count(), it.CanUpdate())
	}
	if r.count("commit") != 1 || r.count("tree") != 1 {
		t.Errorf("每个仓库 2 个请求：%v", r.calls)
	}
}

func TestARecordBehindTheLocalCopyIsNotAnUpdate(t *testing.T) {
	// Given code-ablation 本机已经等于上游，记录还是旧的
	home, h := updatesHome(t)
	r := newFakeRemote()
	r.publish(t, "o/skills", map[string]string{"code-ablation/SKILL.md": skillMD("code-ablation", "old")})
	old := r.upstreamTree("o/skills", "code-ablation")
	r.publish(t, "o/skills", map[string]string{"code-ablation/SKILL.md": skillMD("code-ablation", "new")})
	install(t, h, "code-ablation", map[string]string{"SKILL.md": skillMD("code-ablation", "new")})
	writeRecords(t, h, gh("code-ablation", "o/skills", "code-ablation/SKILL.md", old))
	before, _ := os.ReadFile(h(".agents/.skill-lock.json"))

	l, rep := checkNow(t, home, r)

	// Then 已是最新，不计数；检查不改安装记录；登记了新的应用基线
	if it := itemNamed(t, rep, "code-ablation"); it.State != UpdLatest || rep.Count() != 0 {
		t.Errorf("code-ablation = %+v", it)
	}
	if after, _ := os.ReadFile(h(".agents/.skill-lock.json")); string(after) != string(before) {
		t.Error("检查改了安装记录")
	}
	if _, ok := l.updates.baseline(SourceID{"github", "o/skills", ""}, "code-ablation", h(".agents/skills/code-ablation"), old); !ok {
		t.Error("本机 = 上游时应登记应用基线")
	}
}

func TestARootInstalledSkillIsComparedWithItsCommitsTree(t *testing.T) {
	// Given kami 从仓库根目录装，记录存的是提交 ID；本机等于那个提交的根目录
	home, h := updatesHome(t)
	r := newFakeRemote()
	files := map[string]string{"SKILL.md": skillMD("kami", "v1"), "fonts/a.txt": "a"}
	commit := r.publish(t, "tw93/kami", files)
	install(t, h, "kami", files)
	writeRecords(t, h, gh("kami", "tw93/kami", "SKILL.md", commit))
	r.publish(t, "tw93/kami", map[string]string{"SKILL.md": skillMD("kami", "v2"), "fonts/a.txt": "a"})

	_, rep := checkNow(t, home, r)

	// Then 不说「本地改过」，是可更新
	if it := itemNamed(t, rep, "kami"); it.State != UpdReady {
		t.Errorf("kami = %v %s", it.State, it.Reason)
	}
}

func TestLocalEditsAndAnUpstreamChangeNeedAComparison(t *testing.T) {
	home, h, r, _ := viteHome(t)
	write(t, h(".agents/skills/vite"), "SKILL.md", skillMD("vite", "mine"), 0o644)
	_, rep := checkNow(t, home, r)
	it := itemNamed(t, rep, "vite")
	if it.State != UpdBoth || it.State.Group() != GroupConfirm || it.CanUpdate() {
		t.Errorf("vite = %+v", it)
	}
	if rep.Count() != 1 {
		t.Errorf("已确认有更新：%d", rep.Count())
	}
}

func TestLocalEditsWithoutAnUpstreamChangeAreNotAnUpdate(t *testing.T) {
	home, h := updatesHome(t)
	r := newFakeRemote()
	r.publish(t, "antfu/skills", viteV1)
	install(t, h, "vite", local(viteV1, "skills/vite/"))
	write(t, h(".agents/skills/vite"), "SKILL.md", skillMD("vite", "mine"), 0o644)
	writeRecords(t, h, gh("vite", "antfu/skills", "skills/vite/SKILL.md", r.upstreamTree("antfu/skills", "skills/vite")))
	_, rep := checkNow(t, home, r)
	if it := itemNamed(t, rep, "vite"); it.State != UpdLocalOnly || it.State.Group() != GroupOther || rep.Count() != 0 {
		t.Errorf("vite = %+v", it)
	}
}

func TestAnUnknownBaselineIsNeverCalledALocalEdit(t *testing.T) {
	// Given 记录里的哈希是 64 位内容哈希，推不出基线
	home, h, r, _ := viteHome(t)
	writeRecords(t, h, gh("vite", "antfu/skills", "skills/vite/SKILL.md", strings.Repeat("e", 64)))
	_, rep := checkNow(t, home, r)
	it := itemNamed(t, rep, "vite")
	if it.State != UpdUnknownBase || it.State.Group() != GroupConfirm || rep.Count() != 0 || it.CanUpdate() {
		t.Errorf("vite = %+v，count %d", it, rep.Count())
	}
}

func TestSeparateCopiesMustBeMergedFirst(t *testing.T) {
	home, h, r, _ := viteHome(t)
	must(t, os.MkdirAll(h(".claude"), 0o755))
	writeSkill(t, h(".claude/skills/vite"), "a different copy")
	_, rep := checkNow(t, home, Remote(r))
	if it := itemNamed(t, rep, "vite"); it.State != UpdCopies || it.State.Group() != GroupHandle {
		t.Errorf("vite = %+v", it)
	}
}

func TestLinksToTheSameFolderAreNotCopies(t *testing.T) {
	home, h, r, _ := viteHome(t)
	must(t, os.MkdirAll(h(".claude/skills"), 0o755))
	mustLink(t, h(".agents/skills/vite"), h(".claude/skills/vite"))
	_, rep := checkNow(t, home, r)
	if it := itemNamed(t, rep, "vite"); it.State != UpdReady {
		t.Errorf("链接不算副本：%v %s", it.State, it.Reason)
	}
}

func TestASkillMovedOutOfTheLibraryIsExplainedNotUpdated(t *testing.T) {
	home, h := updatesHome(t, "claude-code")
	r := newFakeRemote()
	writeSkill(t, h(".claude/skills/kami"), "")
	writeRecords(t, h, gh("kami", "tw93/kami", "SKILL.md", strings.Repeat("1", 40)), gh("dbs", "x/dbskill", "dbs/SKILL.md", strings.Repeat("2", 40)))
	_, rep := checkNow(t, home, r)
	it := itemNamed(t, rep, "kami")
	if it.State != UpdMovedOut || !strings.Contains(it.Reason, "技能库中找不到") {
		t.Errorf("kami = %+v", it)
	}
	for _, it := range rep.Items {
		if it.Name == "dbs" {
			t.Error("残留记录交给整理页，不出现在更新页")
		}
	}
	if len(r.calls) != 0 {
		t.Errorf("不该联网：%v", r.calls)
	}
}

func TestASkillRemovedUpstreamIsLeftAlone(t *testing.T) {
	home, h, r, _ := viteHome(t)
	r.publish(t, "antfu/skills", map[string]string{"skills/other/SKILL.md": skillMD("other", "")})
	_, rep := checkNow(t, home, r)
	if it := itemNamed(t, rep, "vite"); it.State != UpdRemoved || it.Reason != "上游仓库已移除该技能" {
		t.Errorf("vite = %+v", it)
	}
	if _, err := os.Stat(h(".agents/skills/vite/SKILL.md")); err != nil {
		t.Error("本机文件不动")
	}
}

func TestASkillThatMovedOrHasNamesakesUpstreamIsOnlyReported(t *testing.T) {
	home, _, r, _ := viteHome(t)
	r.publish(t, "antfu/skills", map[string]string{"new/vite/SKILL.md": skillMD("vite", "")})
	_, rep := checkNow(t, home, r)
	if it := itemNamed(t, rep, "vite"); it.State != UpdRelocated || it.CanUpdate() {
		t.Errorf("换路径：%+v", it)
	}
	r.publish(t, "antfu/skills", map[string]string{"a/vite/SKILL.md": "x", "b/vite/SKILL.md": "y"})
	_, rep = checkNow(t, home, r)
	if it := itemNamed(t, rep, "vite"); it.State != UpdAmbiguous {
		t.Errorf("多个同名：%+v", it)
	}
}

func TestOfflineChecksFailWithoutClaimingRemovals(t *testing.T) {
	home, _, r, _ := viteHome(t)
	r.fail["antfu/skills"] = &RemoteError{Kind: RemoteNetwork}
	_, rep := checkNow(t, home, r)
	it := itemNamed(t, rep, "vite")
	if it.State != UpdFailed || it.Reason != "连不上 GitHub" {
		t.Errorf("vite = %+v", it)
	}
	if rep.Complete {
		t.Error("失败的检查不算全部查完")
	}
}

func TestATruncatedTreeIsACheckFailure(t *testing.T) {
	home, _, r, _ := viteHome(t)
	c := r.repos["antfu/skills"].commits[r.repos["antfu/skills"].head]
	c.tree.Truncated = true
	c.tree.Entries = nil
	_, rep := checkNow(t, home, r)
	if it := itemNamed(t, rep, "vite"); it.State != UpdFailed || it.Reason != "仓库太大，目录树被截断" {
		t.Errorf("vite = %+v", it)
	}
}

func TestUnsupportedUpstreamFilesBlockTheUpdate(t *testing.T) {
	home, _, r, _ := viteHome(t)
	r.publish(t, "antfu/skills", map[string]string{"skills/vite/SKILL.md": skillMD("vite", "v3"), "skills/vite/l": "->SKILL.md"})
	_, rep := checkNow(t, home, r)
	if it := itemNamed(t, rep, "vite"); it.State != UpdFailed || it.Reason != "包含暂不支持的文件" {
		t.Errorf("vite = %+v", it)
	}
}

func TestQuotaStopsTheCheckButKeepsFinishedRepos(t *testing.T) {
	// Given 两个仓库，第二个额度用完
	home, h, r, _ := viteHome(t)
	r.publish(t, "z/other", map[string]string{"x/SKILL.md": skillMD("x", "1")})
	install(t, h, "x", map[string]string{"SKILL.md": skillMD("x", "1")})
	reset := time.Now().Add(30 * time.Minute)
	r.fail["z/other"] = &RemoteError{Kind: RemoteQuota, Reset: reset}
	writeRecords(t, h, gh("vite", "antfu/skills", "skills/vite/SKILL.md", itemBase(t, home, r)), gh("x", "z/other", "x/SKILL.md", ""))

	_, rep := checkNow(t, home, r)

	if it := itemNamed(t, rep, "vite"); it.State != UpdReady {
		t.Errorf("查完的照常：%+v", it)
	}
	it := itemNamed(t, rep, "x")
	if it.State != UpdFailed || !it.Reset.Equal(reset) || !strings.Contains(it.Reason, "额度") {
		t.Errorf("额度用完：%+v", it)
	}
	if rep.Complete || !rep.Reset.Equal(reset) {
		t.Errorf("报告应不完整并带恢复时间：%+v", rep.Reset)
	}
}

// itemBase 是 viteHome 里 vite 的记录哈希。
func itemBase(t *testing.T, home string, r *fakeRemote) string {
	recs, err := ReadInstallRecords(home)
	must(t, err)
	return recs.Get("vite").SkillFolderHash
}

func TestStoppingACheckLeavesTheRestUncheckedAndUpdatesUnavailable(t *testing.T) {
	// Given 上次完整检查过 vite（可更新）
	home, _, r, _ := viteHome(t)
	l, _ := checkNow(t, home, r)

	// When 这一次一开始就停止
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	must(t, l.CheckUpdates(ctx, r, nil))
	rep := l.Updates()

	// Then vite 保留上次的结果和时间，但不能直接更新
	it := itemNamed(t, rep, "vite")
	if !it.Stale || it.CanUpdate() || it.CheckedAt.IsZero() {
		t.Errorf("旧结果：%+v", it)
	}
	if rep.Complete {
		t.Error("停止的检查不算查完")
	}
}

func TestNeverCheckedShowsEveryGitHubSkillAsUnchecked(t *testing.T) {
	home, _, _, _ := viteHome(t)
	rep := Scan(home).Updates()
	if !rep.Checked.IsZero() || itemNamed(t, rep, "vite").State != UpdUnchecked || rep.Eligible != 1 {
		t.Errorf("从没检查过：%+v", rep)
	}
}

func TestAStoppedSkillIsNotCheckedOnline(t *testing.T) {
	home, h, r, _ := viteHome(t)
	l := Scan(home)
	l.updates.stop(SourceID{"github", "antfu/skills", ""}, h(".agents/skills/vite"), "vite")
	must(t, l.CheckUpdates(context.Background(), r, nil))
	if it := itemNamed(t, l.Updates(), "vite"); it.State != UpdStopped || len(r.calls) != 0 {
		t.Errorf("vite = %v，请求 %v", it.State, r.calls)
	}
}

func TestWebsiteSourcesAreGroupedWithACopyableCommand(t *testing.T) {
	home, h := updatesHome(t)
	for _, n := range []string{"lark-doc", "lark-im", "agently-mail", "my-gl"} {
		writeSkill(t, h(".agents/skills/"+n), "")
	}
	feishu := map[string]any{"sourceBaseUrl": "https://open.feishu.cn", "wellKnownDigest": "sha256:1"}
	writeRecords(t, h,
		rec{"lark-doc", "open.feishu.cn", "well-known", "", "", feishu},
		rec{"lark-im", "open.feishu.cn", "well-known", "", "", feishu},
		rec{"agently-mail", "agent.qq.com", "well-known", "", "", map[string]any{"sourceUrl": "https://agent.qq.com/.well-known/skills/agently-mail/SKILL.md"}},
		rec{"my-gl", "gitlab.com/me/x", "gitlab", "", "", nil},
	)
	rep := Scan(home).Updates()
	f := itemNamed(t, rep, "open.feishu.cn")
	if f.State != UpdWebsite || len(f.Names) != 2 || f.Command != "npx skills update lark-doc lark-im -g -y" {
		t.Errorf("飞书 = %+v", f)
	}
	q := itemNamed(t, rep, "agent.qq.com")
	if q.Reason != "安装记录不完整，建议重新安装" || q.Command != "npx skills add https://agent.qq.com -g -y" {
		t.Errorf("腾讯 = %+v", q)
	}
	if g := itemNamed(t, rep, "gitlab.com/me/x"); g.State != UpdUnsupported || g.Reason != "暂不支持这种来源" || g.Command != "" {
		t.Errorf("gitlab = %+v", g)
	}
	if rep.Eligible != 0 {
		t.Errorf("没有可由本应用检查的技能：%d", rep.Eligible)
	}
}

func TestAnUnknownRecordVersionIsCheckedReadOnly(t *testing.T) {
	for _, edited := range []bool{false, true} {
		name, want := "untouched", UpdReady
		if edited {
			name, want = "local edits", UpdBoth
		}
		t.Run(name, func(t *testing.T) {
			home, h, r, installed := viteHome(t)
			if edited {
				write(t, h(".agents/skills/vite"), "SKILL.md", skillMD("vite", "mine"), 0644)
			}
			writeRecordsVersion(t, h, 4, gh("vite", "antfu/skills", "skills/vite/SKILL.md", installed))
			_, rep := checkNow(t, home, r)
			it := itemNamed(t, rep, "vite")
			if it.State != want || it.CanUpdate() || rep.ReadOnly != "安装记录无法用于更新，只能查看" {
				t.Errorf("只读检查：%+v %q", it, rep.ReadOnly)
			}
			if rep.Count() != 0 {
				t.Errorf("read-only updates counted in badge: %d", rep.Count())
			}
		})
	}
}

func TestShellQuotingOfCopiedCommands(t *testing.T) {
	if got := shellArgs([]string{"plain", "has space", "it's"}); got != `plain 'has space' 'it'\''s'` {
		t.Errorf("%s", got)
	}
}

func TestChangedSourceNeverReusesCachedEligibility(t *testing.T) {
	home, h, r, _ := viteHome(t)
	_, rep := checkNow(t, home, r)
	if rep.Count() != 1 {
		t.Fatal("fixture should be ready")
	}
	writeRecords(t, h, gh("vite", "fork/skills", "skills/vite/SKILL.md", strings.Repeat("f", 40)))
	rep = Scan(home).Updates()
	if it := itemNamed(t, rep, "vite"); it.State != UpdUnchecked || it.CanUpdate() || rep.Count() != 0 {
		t.Fatalf("reused old source: %+v", it)
	}
}

func TestCheckUpdatesUsesPathCommitsOnlyWhenGHAuthEnabled(t *testing.T) {
	home, h := updatesHome(t)
	r := newFakeRemote()
	// 先发布一个初始版本并装到本机
	v1 := map[string]string{
		"skills/web-artifacts-builder/SKILL.md": skillMD("web-artifacts-builder", "old"),
		"skills/mcp-builder/SKILL.md":           skillMD("mcp-builder", "old"),
	}
	r.publish(t, "anthropics/skills", v1)
	install(t, h, "web-artifacts-builder", local(v1, "skills/web-artifacts-builder/"))
	install(t, h, "mcp-builder", local(v1, "skills/mcp-builder/"))
	writeRecords(t, h,
		gh("web-artifacts-builder", "anthropics/skills", "skills/web-artifacts-builder/SKILL.md", treeOf(t, h(".agents/skills/web-artifacts-builder"))),
		gh("mcp-builder", "anthropics/skills", "skills/mcp-builder/SKILL.md", treeOf(t, h(".agents/skills/mcp-builder"))),
	)

	// 2025-12-02 改动了 web-artifacts-builder
	cWeb := r.publish(t, "anthropics/skills", map[string]string{
		"skills/web-artifacts-builder/SKILL.md": skillMD("web-artifacts-builder", "new-2025"),
		"skills/mcp-builder/SKILL.md":           skillMD("mcp-builder", "old"),
	})
	dWeb := time.Date(2025, 12, 2, 10, 0, 0, 0, time.UTC)
	r.pathCommits["anthropics/skills\x00skills/web-artifacts-builder"] = Commit{SHA: cWeb, Date: dWeb}

	// 2026-11-07 仓库 HEAD 又改动了 mcp-builder
	cHead := r.publish(t, "anthropics/skills", map[string]string{
		"skills/web-artifacts-builder/SKILL.md": skillMD("web-artifacts-builder", "new-2025"),
		"skills/mcp-builder/SKILL.md":           skillMD("mcp-builder", "new-2026"),
	})
	dMcp := time.Date(2026, 11, 1, 9, 30, 0, 0, time.UTC)
	r.pathCommits["anthropics/skills\x00skills/mcp-builder"] = Commit{SHA: cHead, Date: dMcp}

	// 1. 不勾选 UseGHAuth 时沿用现有仓库 HEAD 提交
	l := Scan(home)
	l.Settings.UseGHAuth = false
	must(t, l.CheckUpdates(context.Background(), r, nil))
	if r.count("pathcommit ") != 0 {
		t.Fatalf("UseGHAuth=false should not call PathCommit, got %d", r.count("pathcommit "))
	}
	repAnon := l.Updates()
	webAnon := itemNamed(t, repAnon, "web-artifacts-builder")
	if webAnon.Commit != cHead {
		t.Fatalf("anon commit = %s, want repo HEAD %s", webAnon.Commit, cHead)
	}

	// 2. 勾选 UseGHAuth 后按技能目录显示各自最新提交与 SHA，且仍可通过 Prepare/Apply
	l.Settings.UseGHAuth = true
	must(t, l.CheckUpdates(context.Background(), r, nil))
	if r.count("pathcommit ") != 2 {
		t.Fatalf("expected 2 pathcommit calls, got %d", r.count("pathcommit "))
	}
	repAuth := l.Updates()
	webAuth := itemNamed(t, repAuth, "web-artifacts-builder")
	mcpAuth := itemNamed(t, repAuth, "mcp-builder")
	if webAuth.Commit != cWeb || !webAuth.CommitDate.Equal(dWeb) {
		t.Fatalf("web-artifacts-builder commit = %s (%v), want %s (%v)", webAuth.Commit, webAuth.CommitDate, cWeb, dWeb)
	}
	if mcpAuth.Commit != cHead || !mcpAuth.CommitDate.Equal(dMcp) {
		t.Fatalf("mcp-builder commit = %s (%v), want %s (%v)", mcpAuth.Commit, mcpAuth.CommitDate, cHead, dMcp)
	}

	// 使用按目录查到的提交仍可通过 validateSnapshot 完成对比与更新
	l.Settings.UpdateOldVersion = "trash"
	p, err := l.PrepareUpdate(context.Background(), r, webAuth)
	must(t, err)
	defer l.DiscardPrepared(p)
	out, err := l.ApplyUpdate(context.Background(), r, p.Item, p, nil)
	must(t, err)
	if !out.Updated {
		t.Fatal("per-path commit update failed")
	}
}

func TestUpdateItemWebURLCoversGitHubAndWebsites(t *testing.T) {
	cases := []struct {
		name string
		item UpdateItem
		want string
	}{
		{
			name: "github repo",
			item: UpdateItem{Source: SourceID{Type: "github", Repo: "antfu/skills"}},
			want: "https://github.com/antfu/skills",
		},
		{
			name: "github repo with ref",
			item: UpdateItem{Source: SourceID{Type: "github", Repo: "antfu/skills", Ref: "next/v2"}},
			want: "https://github.com/antfu/skills/tree/next/v2",
		},
		{
			name: "well-known with base url",
			item: UpdateItem{Source: SourceID{Type: "well-known", Repo: "open.feishu.cn"}, Record: LockEntry{SourceBaseURL: "https://open.feishu.cn/skills"}},
			want: "https://open.feishu.cn/skills",
		},
		{
			name: "well-known host fallback",
			item: UpdateItem{Source: SourceID{Type: "well-known", Repo: "open.feishu.cn"}},
			want: "https://open.feishu.cn",
		},
		{
			name: "gitlab url",
			item: UpdateItem{Source: SourceID{Type: "gitlab", Repo: "group/repo"}, Record: LockEntry{SourceURL: "https://gitlab.example.com/group/repo.git"}},
			want: "https://gitlab.example.com/group/repo",
		},
		{
			name: "local never opens file or local path",
			item: UpdateItem{Source: SourceID{Type: "local", Repo: "local"}, Record: LockEntry{SourceURL: "file:///etc/passwd"}},
			want: "",
		},
	}
	for _, tc := range cases {
		if got := tc.item.WebURL(); got != tc.want {
			t.Errorf("%s: WebURL() = %q, want %q", tc.name, got, tc.want)
		}
	}
}

func TestUpdateItemCommitAndFileWebURL(t *testing.T) {
	it := UpdateItem{
		Source: SourceID{Type: "github", Repo: "antfu/skills", Ref: "main"},
		Commit: "2c5958e1234567890",
		Record: LockEntry{SkillPath: "skills/vite/SKILL.md"},
	}
	if got, want := it.CommitURL(), "https://github.com/antfu/skills/commit/2c5958e1234567890"; got != want {
		t.Fatalf("CommitURL() = %q, want %q", got, want)
	}
	if got, want := it.FileWebURL("references/guide.md"), "https://github.com/antfu/skills/blob/2c5958e1234567890/skills/vite/references/guide.md"; got != want {
		t.Fatalf("FileWebURL() = %q, want %q", got, want)
	}
	// Ref fallback & unsafe commit/path rejection
	noCommit := UpdateItem{
		Source: SourceID{Type: "github", Repo: "antfu/skills", Ref: "next"},
		Commit: "not-hex!",
	}
	if got := noCommit.CommitURL(); got != "" {
		t.Fatalf("invalid commit returned URL %q", got)
	}
	if got, want := noCommit.FileWebURL("SKILL.md"), "https://github.com/antfu/skills/blob/next/SKILL.md"; got != want {
		t.Fatalf("FileWebURL ref fallback = %q, want %q", got, want)
	}
	if got := noCommit.FileWebURL("../etc/passwd"); got != "" {
		t.Fatalf("traversal path returned URL %q", got)
	}
	wk := UpdateItem{Source: SourceID{Type: "well-known", Repo: "open.feishu.cn"}, Commit: "abcdef1"}
	if wk.CommitURL() != "" || wk.FileWebURL("SKILL.md") != "" {
		t.Fatal("non-github item should not build github commit/file URLs")
	}
}
