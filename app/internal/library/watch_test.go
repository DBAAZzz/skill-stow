package library

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func TestLibraryWatchPlanIsEmptyForSampleLibrary(t *testing.T) {
	// Given 一个无磁盘主目录的示例技能库
	lib := Sample()

	// When 计算监听目标计划
	plan := lib.WatchPlan()

	// Then 不需要监听任何目录或文件
	if len(plan.Dirs) != 0 || len(plan.Files) != 0 || plan.Truncated {
		t.Fatalf("示例库应返回空监听计划，实际 = %+v", plan)
	}
}

func TestLibraryWatchPlanCoversSkillsAgentsConfigsAndProjects(t *testing.T) {
	// Given 一个包含技能库、已安装 Agent、自定义目录、原生配置与项目的磁盘环境
	home := fakeHome(t)
	customDir := filepath.Join(home, "custom-agent-skills")
	must(t, os.MkdirAll(customDir, 0o755))

	// 让一个已安装 Agent 的技能目录直接软链到技能库，验证会按真实路径去重
	mustLink(t, filepath.Join(home, ".agents/skills"), filepath.Join(home, ".gemini/skills"))

	lib := Scan(home)
	must(t, lib.AddAgentDir(customDir))

	// 补一个原生配置和 lock 文件，以及项目级 .gemini/settings.json
	must(t, os.MkdirAll(filepath.Join(home, ".claude"), 0o755))
	must(t, os.WriteFile(filepath.Join(home, ".claude/settings.json"), []byte("{}"), 0o644))
	must(t, os.WriteFile(filepath.Join(home, ".agents/.skill-lock.json"), []byte(`{"version":3,"skills":{}}`), 0o644))
	projGemCfg := filepath.Join(home, "code/storefront/.gemini/settings.json")
	must(t, os.MkdirAll(filepath.Dir(projGemCfg), 0o755))
	must(t, os.WriteFile(projGemCfg, []byte("{}"), 0o644))

	lib = Scan(home)

	// When 计算监听目标计划
	plan := lib.WatchPlan()

	// Then 技能库、Agent 技能目录、自定义目录与项目技能目录在 Dirs 中，且去重有序
	wantDirs := []string{
		filepath.Join(home, ".agents/skills"),
		filepath.Join(home, ".claude/skills"),
		filepath.Join(home, ".cursor/skills"),
		customDir,
		filepath.Join(home, "code/storefront/.claude/skills"),
	}
	for _, want := range wantDirs {
		if !slices.Contains(plan.Dirs, want) {
			t.Errorf("Dirs 缺少 %s\n实际 Dirs = %v", want, plan.Dirs)
		}
	}
	if slices.Contains(plan.Dirs, filepath.Join(home, ".gemini/skills")) {
		t.Errorf("指向 Skill 库的软链目录不应重复占一个 watcher：%v", plan.Dirs)
	}
	if !slices.IsSorted(plan.Dirs) {
		t.Errorf("Dirs 应有序：%v", plan.Dirs)
	}

	// And 插件清单、安装锁、用户与项目原生配置在 Files 中
	wantFiles := []string{
		filepath.Join(home, ".agents/.skill-lock.json"),
		filepath.Join(home, ".claude/plugins/installed_plugins.json"),
		filepath.Join(home, ".claude/settings.json"),
		filepath.Join(home, ".gemini/settings.json"),
		projGemCfg,
	}
	for _, want := range wantFiles {
		if !slices.Contains(plan.Files, want) {
			t.Errorf("Files 缺少 %s\n实际 Files = %v", want, plan.Files)
		}
	}
	if !slices.IsSorted(plan.Files) {
		t.Errorf("Files 应有序：%v", plan.Files)
	}
}

func TestLibraryWatchPlanFollowsExternalSymlinksOnlyForMineAndProject(t *testing.T) {
	// Given 保留未 EvalSymlinks 的临时路径（覆盖 /var 与 /private/var 别名对齐）
	home := t.TempDir()
	realHome, _ := filepath.EvalSymlinks(home)

	extMine := filepath.Join(home, "ext-mine-skill")
	proj := filepath.Join(realHome, "code/demo")
	extProj := filepath.Join(proj, "shared/ext-proj-skill")
	extPlugin := filepath.Join(home, "ext-plugin-store/pua")
	for _, dir := range []string{extMine, extProj, extPlugin} {
		must(t, os.MkdirAll(dir, 0o755))
		must(t, os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte("---\nname: "+filepath.Base(dir)+"\ndescription: d\n---\n"), 0o644))
	}

	// 技能库里既有指向外部目录的软链，也有通过未解析别名指向技能库内部技能的软链（不应重复监听）
	insideSkill := filepath.Join(home, ".agents/skills/inside-real")
	must(t, os.MkdirAll(insideSkill, 0o755))
	must(t, os.WriteFile(filepath.Join(insideSkill, "SKILL.md"), []byte("---\nname: inside-real\ndescription: d\n---\n"), 0o644))
	must(t, os.MkdirAll(filepath.Join(home, ".claude/skills"), 0o755))
	mustLink(t, insideSkill, filepath.Join(home, ".claude/skills/inside-real"))
	mustLink(t, extMine, filepath.Join(home, ".agents/skills/ext-mine-skill"))

	// 已安装 Claude Code，且插件本身位于主目录下的外链缓存目录，由 installed_plugins.json 管理
	must(t, os.MkdirAll(filepath.Join(home, ".claude/plugins/cache/m/pua/1.0/skills"), 0o755))
	mustLink(t, extPlugin, filepath.Join(home, ".claude/plugins/cache/m/pua/1.0/skills/pua"))
	must(t, os.WriteFile(filepath.Join(home, ".claude/plugins/installed_plugins.json"),
		[]byte(`{"plugins":{"pua@m":[{"installPath":"`+jsonStr(filepath.Join(home, ".claude/plugins/cache/m/pua/1.0"))+`"}]}}`), 0o644))

	// 项目技能目录内的软链指向项目内但位于技能目录之外的子目录
	must(t, os.MkdirAll(filepath.Join(proj, ".claude/skills"), 0o755))
	mustLink(t, extProj, filepath.Join(proj, ".claude/skills/ext-proj-skill"))
	must(t, os.WriteFile(filepath.Join(home, ".claude.json"), []byte(`{"projects":{"`+jsonStr(proj)+`":{}}}`), 0o644))

	lib := Scan(home)

	// When 计算监听目标计划
	plan := lib.WatchPlan()

	// Then Mine 与 Project 的外部软链真实目录应加入 Dirs，而插件外链和已在监听树内的技能不额外占 watcher
	wantMineReal := filepath.Join(realHome, "ext-mine-skill")
	wantProjReal := filepath.Join(realHome, "code/demo/shared/ext-proj-skill")
	wantInsideReal := filepath.Join(realHome, ".agents/skills/inside-real")
	wantPluginReal := filepath.Join(realHome, "ext-plugin-store/pua")

	if !slices.Contains(plan.Dirs, wantMineReal) {
		t.Errorf("Mine 外部软链真实目录 %s 应被监听，Dirs=%v", wantMineReal, plan.Dirs)
	}
	if !slices.Contains(plan.Dirs, wantProjReal) {
		t.Errorf("Project 外部软链真实目录 %s 应被监听，Dirs=%v", wantProjReal, plan.Dirs)
	}
	if slices.Contains(plan.Dirs, wantInsideReal) {
		t.Errorf("位于 Skill 库树内的 Skill %s 不应作为外部软链重复添加，Dirs=%v", wantInsideReal, plan.Dirs)
	}
	if slices.Contains(plan.Dirs, wantPluginReal) {
		t.Errorf("插件内部 Skill 真实目录 %s 不应单独建 watcher，Dirs=%v", wantPluginReal, plan.Dirs)
	}
}

func TestLibraryWatchPlanTruncatesAtLimitWhileKeepingConfigFiles(t *testing.T) {
	// Given 存在多个目录和关键配置文件，且限制总数上限为 3
	home := fakeHome(t)
	lib := Scan(home)

	// When 按较紧的上限生成监听计划
	plan := lib.watchPlanWithLimit(3)

	// Then 总数不超过上限、标记 Truncated，并优先保留配置文件
	if total := len(plan.Dirs) + len(plan.Files); total > 3 {
		t.Fatalf("监听总数 %d 超过上限 3", total)
	}
	if !plan.Truncated {
		t.Fatalf("预期 Truncated=true，实际 = %+v", plan)
	}
	if len(plan.Files) == 0 {
		t.Fatalf("配置文件应优先纳入监听计划，实际 Files=%v Dirs=%v", plan.Files, plan.Dirs)
	}
}
