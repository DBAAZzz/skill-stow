//go:build darwin

package library

import (
	"context"
	"maps"
	"os"
	"testing"
)

// agentVite：vite 用「指定的 Agent」模式装进 Claude Code，Kiro 通过链接读；
// 之后上游改了 vite。
func agentVite(t *testing.T) (string, func(string) string, *fakeImport) {
	t.Helper()
	home, h := importHome(t)
	f := newFakeImport()
	f.publish(t, "antfu/skills", antfuSkills)
	b := readAntfu(t, home, f, "antfu/skills")
	out := Scan(home).AddImport(b, []string{"vite"}, ImportPlacement{Agents: []string{"claude-code", "kiro"}}, nil)
	if !out[0].Added {
		t.Fatalf("%+v", out)
	}
	v2 := maps.Clone(antfuSkills)
	v2["skills/vite/SKILL.md"] += "v2\n"
	f.publish(t, "antfu/skills", v2)
	return home, h, f
}

func TestAnAgentModeSkillIsCheckedForUpdatesFromItsAppRecord(t *testing.T) {
	home, h, f := agentVite(t)
	_, rep := checkNow(t, home, f)
	it := itemNamed(t, rep, "vite")
	if it.State != UpdReady || it.Place != h(".claude/skills/vite") || it.Record.Place == "" {
		t.Fatalf("vite = %+v", it)
	}
	if exists(h(".agents/.skill-lock.json")) {
		t.Error("检查不应写安装记录")
	}
	if in := Scan(home).InstalledIn(it); in != "Claude Code" {
		t.Errorf("更新页应标出装在哪：%q", in)
	}
}

func TestUpdatingAnAgentModeSkillReplacesItInPlaceAndKeepsLinks(t *testing.T) {
	home, h, f := agentVite(t)
	l, rep := checkNow(t, home, f)
	p, err := l.PrepareUpdate(context.Background(), f, itemNamed(t, rep, "vite"))
	must(t, err)
	t.Cleanup(func() { l.DiscardPrepared(p) })
	l.Settings.UpdateOldVersion = "trash"
	out, err := l.ApplyUpdate(context.Background(), f, p.Item, p, nil)
	must(t, err)
	if !out.Updated || treeOf(t, h(".claude/skills/vite")) != p.Item.Upstream {
		t.Fatal("新版没有装到 Claude Code 的目录")
	}
	if real, err := realPath(h(".kiro/skills/vite")); err != nil || real != h(".claude/skills/vite") {
		t.Errorf("Kiro 的链接变了：%v %v", real, err)
	}
	if exists(h(".agents/skills/vite")) || exists(h(".agents/.skill-lock.json")) {
		t.Error("不应装进 Skill 库或写安装记录")
	}
	l = Scan(home)
	if it := itemNamed(t, l.Updates(), "vite"); it.State != UpdLatest {
		t.Errorf("更新后 = %+v", it)
	}
	if _, rep = checkNow(t, home, f); itemNamed(t, rep, "vite").State != UpdLatest || rep.Count() != 0 {
		t.Errorf("再检查 = %+v", itemNamed(t, rep, "vite"))
	}
}

func TestTheSameNameInTheLibraryAndAnAgentAreTwoUpdates(t *testing.T) {
	home, h, f := agentVite(t)
	// 之后又用 npx skills 把 vite 装进了技能库（旧版）
	old := f.upstreamTree("antfu/skills", "skills/vite")
	install(t, h, "vite", map[string]string{"SKILL.md": antfuSkills["skills/vite/SKILL.md"], "references/build.md": "build"})
	writeRecords(t, h, gh("vite", "antfu/skills", "skills/vite/SKILL.md", old))
	_, rep := checkNow(t, home, f)
	places := map[string]bool{}
	for _, it := range rep.Items {
		if it.Name == "vite" {
			places[it.Place] = true
		}
	}
	if len(places) != 2 || !places[h(".claude/skills/vite")] || !places[h(".agents/skills/vite")] {
		t.Errorf("应是两条：%v", places)
	}
}

func TestAMovedAwayAgentModeSkillIsNotListedOrReinstalled(t *testing.T) {
	home, h, f := agentVite(t)
	must(t, os.Remove(h(".kiro/skills/vite")))
	must(t, os.RemoveAll(h(".claude/skills/vite")))
	_, rep := checkNow(t, home, f)
	for _, it := range rep.Items {
		if it.Name == "vite" {
			t.Errorf("不应列出：%+v", it)
		}
	}
	if exists(h(".claude/skills/vite")) {
		t.Error("不应重装")
	}
}

func TestDeletingAnAgentModeSkillDropsItFromUpdatesUntilUndone(t *testing.T) {
	home, h, f := agentVite(t)
	l := Scan(home)
	d, err := l.Delete(l.Get("vite"))
	must(t, err)
	if _, ok := Scan(home).ImportedFrom(h(".claude/skills/vite")); ok {
		t.Error("删除后应去掉应用记录")
	}
	if rep := Scan(home).Updates(); len(rep.Items) != 0 {
		t.Errorf("删除后还列着：%+v", rep.Items)
	}
	must(t, l.Undo(d))
	if _, ok := Scan(home).ImportedFrom(h(".claude/skills/vite")); !ok {
		t.Error("撤销后应写回应用记录")
	}
	if _, rep := checkNow(t, home, f); itemNamed(t, rep, "vite").State != UpdReady {
		t.Errorf("撤销后 = %+v", itemNamed(t, rep, "vite"))
	}
}

// slowCommit 让检查停在请求上游提交那一步，直到 proceed 关闭。
type slowCommit struct {
	*fakeImport
	entered, proceed chan struct{}
}

func (r *slowCommit) Commit(ctx context.Context, repo, ref string) (Commit, error) {
	close(r.entered)
	<-r.proceed
	return r.fakeImport.Commit(ctx, repo, ref)
}

func TestUndoingADeleteDuringAnUpdateCheckKeepsWhereTheSkillCameFrom(t *testing.T) {
	// Given 指定 Agent 模式装的 vite 被删了；另一个技能 vue 正在检查更新
	home, h, f := agentVite(t)
	l := Scan(home)
	d, err := l.Delete(l.Get("vite"))
	must(t, err)
	install(t, h, "vue", map[string]string{"SKILL.md": antfuSkills["skills/vue/SKILL.md"]})
	writeRecords(t, h, gh("vue", "antfu/skills", "skills/vue/SKILL.md", f.upstreamTree("antfu/skills", "skills/vue")))
	r := &slowCommit{fakeImport: f, entered: make(chan struct{}), proceed: make(chan struct{})}
	done := make(chan error, 1)
	go func() { done <- Scan(home).CheckUpdates(context.Background(), r, nil) }()
	<-r.entered

	// When 检查途中撤销删除，然后检查结束
	must(t, l.Undo(d))
	close(r.proceed)
	must(t, <-done)

	// Then 检查写下结果时没有把撤销写回的来源冲掉
	if _, ok := Scan(home).ImportedFrom(h(".claude/skills/vite")); !ok {
		t.Fatal("检查结束后来源记录丢了")
	}
	if _, rep := checkNow(t, home, f); itemNamed(t, rep, "vite").State != UpdReady {
		t.Error("撤销后应能继续更新")
	}
}

func TestTheLibraryCopyAndTheAgentCopyAreUpdatedIndependently(t *testing.T) {
	home, h, f := agentVite(t)
	st, err := loadUpdates(home)
	must(t, err)
	install(t, h, "vite", map[string]string{"SKILL.md": antfuSkills["skills/vite/SKILL.md"], "references/build.md": "build"})
	writeRecords(t, h, gh("vite", "antfu/skills", "skills/vite/SKILL.md", st.Imports[0].Tree))
	_, rep := checkNow(t, home, f)
	n := 0
	for _, it := range rep.Items {
		if it.Name == "vite" {
			n++
			if it.State != UpdReady {
				t.Errorf("%s 应可更新：%s %s", it.Place, it.State, it.Reason)
			}
		}
	}
	if n != 2 {
		t.Errorf("应是两行，实际 %d", n)
	}
}

func TestAnUntrackedCopyStillHasToBeMergedFirst(t *testing.T) {
	// 技能库里的 vite 之外，Cursor 目录里还有一份没人追踪的 vite
	home, h, r, _ := viteHome(t)
	install(t, h, "vite", local(viteV1, "skills/vite/"))
	must(t, os.MkdirAll(h(".cursor/skills/vite"), 0o755))
	must(t, os.WriteFile(h(".cursor/skills/vite/SKILL.md"), []byte(skillMD("vite", "mine")), 0o644))
	if _, rep := checkNow(t, home, r); itemNamed(t, rep, "vite").State != UpdCopies {
		t.Errorf("vite = %+v", itemNamed(t, rep, "vite"))
	}
}

func TestKeepingTheLocalVersionInOnePlaceLeavesTheOtherPlaceAlone(t *testing.T) {
	home, h := importHome(t)
	f := newFakeImport()
	f.publish(t, "antfu/skills", antfuSkills)
	b := readAntfu(t, home, f, "antfu/skills")
	for _, id := range []string{"claude-code", "kiro"} {
		if out := Scan(home).AddImport(b, []string{"vite"}, ImportPlacement{Agents: []string{id}}, nil); !out[0].Added {
			t.Fatalf("%+v", out)
		}
	}
	for _, p := range []string{".kiro/skills/vite/custom.md", ".claude/skills/vite/custom.md"} {
		must(t, os.WriteFile(h(p), []byte("local"), 0o644))
	}
	v2 := maps.Clone(antfuSkills)
	v2["skills/vite/SKILL.md"] += "v2"
	f.publish(t, "antfu/skills", v2)
	l, rep := checkNow(t, home, f)
	if len(rep.Items) != 2 {
		t.Fatalf("%+v", rep.Items)
	}
	must(t, l.IgnoreUpdate(rep.Items[0]))
	for _, it := range Scan(home).Updates().Items {
		want := UpdBoth
		if it.Place == rep.Items[0].Place {
			want = UpdIgnored
		}
		if it.State != want {
			t.Errorf("%s = %s，应为 %s", it.Place, it.State, want)
		}
	}
}

func TestABrokenInstallRecordDoesNotStopCheckingAgentModeSkills(t *testing.T) {
	home, h, f := agentVite(t)
	must(t, os.MkdirAll(h(".agents"), 0o755))
	must(t, os.WriteFile(h(".agents/.skill-lock.json"), []byte("{"), 0o644))
	l := Scan(home)
	must(t, l.CheckUpdates(context.Background(), f, nil))
	if it := itemNamed(t, l.Updates(), "vite"); it.State != UpdReady || it.ReadOnly {
		t.Errorf("vite = %+v", it)
	}
}

func TestAnInterruptedAgentModeUpdateIsRecoveredFromTheAppRecord(t *testing.T) {
	for _, swapped := range []bool{false, true} {
		home, h, f := agentVite(t)
		l, rep := checkNow(t, home, f)
		it := itemNamed(t, rep, "vite")
		p, err := l.PrepareUpdate(context.Background(), f, it)
		must(t, err)
		sid, err := identify(p.Dir)
		must(t, err)
		tx := &transaction{ID: "crash", Item: it, Stage: p.Dir, TargetID: p.Target, StageID: sid, RecordFile: appRecordFile, RecordHash: it.Upstream, Policy: "delete", Status: "pending"}
		l.updates.Transactions = append(l.updates.Transactions, tx)
		must(t, l.updates.save(home))
		if swapped {
			must(t, swapDirectories(it.Place, p.Dir))
		}
		fresh := Scan(home)
		must(t, fresh.RecoverUpdates())
		st, err := loadUpdates(home)
		must(t, err)
		want := it.Local
		if swapped {
			want = it.Upstream
		}
		if len(st.Transactions) != 0 || st.Imports[0].Tree != want || treeOf(t, it.Place) != want {
			t.Errorf("换入=%v：记录 %s，目录 %s，应为 %s", swapped, st.Imports[0].Tree, treeOf(t, it.Place), want)
		}
		if exists(h(".agents/.skill-lock.json")) {
			t.Error("不应写安装记录")
		}
	}
}
