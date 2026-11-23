package library

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"
)

// fakeHome 搭一个最小的主目录：技能库、Agent 目录（链接和拷贝）、
// 插件、用过的项目。
func fakeHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	home, _ = filepath.EvalSymlinks(home) // macOS 的 /var → /private/var
	skill := func(dir, desc string) {
		t.Helper()
		must(t, os.MkdirAll(dir, 0o755))
		md := "---\nname: " + filepath.Base(dir) + "\ndescription: " + desc + "\n---\n\n# x\n"
		must(t, os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(md), 0o644))
	}
	link := func(target, at string) {
		t.Helper()
		must(t, os.MkdirAll(filepath.Dir(at), 0o755))
		mustLink(t, target, at)
	}
	h := func(p string) string { return filepath.Join(home, p) }

	skill(h(".agents/skills/test-first"), "先写测试")
	skill(h(".agents/skills/spark"), ">\n  想法和需求\n  先聊清楚")
	must(t, os.WriteFile(h(".agents/skills/test-first/notes.md"), []byte("# 笔记\n"), 0o644))
	link("../../.agents/skills/test-first", h(".claude/skills/test-first"))
	link(h(".agents/skills/spark"), h(".cursor/skills/spark"))
	skill(h(".cursor/skills/test-first-old"), "旧版")
	skill(h(".codebuddy/skills/test-first"), "一份拷贝") // 同名拷贝
	skill(h(".codex/skills/test-first"), "另一份拷贝")    // 没装的 Agent，只算副本
	link(h("gone"), h(".claude/skills/broken"))      // 坏链接
	// 装了的 Agent：目录里除了 skills 还有别的。Codex 只有 skills 目录，算没装
	for _, f := range []string{".cursor/argv.json", ".codebuddy/settings.json", ".workbuddy/config.json", ".gemini/settings.json"} {
		must(t, os.MkdirAll(filepath.Dir(h(f)), 0o755))
		must(t, os.WriteFile(h(f), []byte("{}"), 0o644))
	}

	// Claude Code 的插件
	skill(h(".claude/plugins/cache/m/pua/1.0/skills/pua"), "插件 Skill")
	must(t, os.WriteFile(h(".claude/plugins/installed_plugins.json"),
		[]byte(`{"plugins":{"pua@m":[{"installPath":"`+jsonStr(h(".claude/plugins/cache/m/pua/1.0"))+`"}]}}`), 0o644))

	// 项目：Claude Code 用过；项目里的技能被全局链接到 Claude Code
	proj := h("code/storefront")
	skill(filepath.Join(proj, ".claude/skills/notes-sync"), "股票文档")
	link(filepath.Join(proj, ".claude/skills/notes-sync"), h(".claude/skills/notes-sync"))
	link("../../.claude/skills/notes-sync", filepath.Join(proj, ".agents/skills/notes-sync")) // 同一份
	must(t, os.WriteFile(h(".claude.json"), []byte(`{"projects":{"`+jsonStr(proj)+`":{},"`+jsonStr(home)+`":{}}}`), 0o644))
	return home
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

// jsonStr 把路径转成能安全放进 JSON 字符串里的形式：Windows 的路径带反斜杠，
// 直接拼进去会让 JSON 非法。
func jsonStr(s string) string {
	b, _ := json.Marshal(s)
	return string(b[1 : len(b)-1])
}

// mustLink 在 at 建一条指向 target 的链接。macOS 上目标不存在也能建出悬空
// 链接；Windows 的目录联接只能指向已存在的目录，所以先建出来再删掉。
func mustLink(t *testing.T, target, at string) {
	t.Helper()
	abs := target
	if !filepath.IsAbs(abs) {
		abs = filepath.Join(filepath.Dir(at), target)
	}
	created := false
	if _, err := os.Stat(abs); err != nil {
		must(t, os.MkdirAll(abs, 0o755))
		created = true
	}
	must(t, makeLink(target, at))
	if created {
		must(t, os.RemoveAll(abs))
	}
}

// requireFileSymlink 建一条文件符号链接。Windows 上没有开发者模式时普通用户
// 建不了文件链接（目录联接只能指向目录），这类用例跳过。
func requireFileSymlink(t *testing.T, target, at string) {
	t.Helper()
	if err := os.Symlink(target, at); err != nil {
		t.Skipf("这个平台建不了文件符号链接：%v", err)
	}
}

// chmodReadOnly 把目录设成只读，用来模拟写入失败。Windows 上目录权限挡不住
// 写入，这类用例只能跳过。
func chmodReadOnly(t *testing.T, dir string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("Windows 上 chmod 不阻止写入")
	}
	must(t, os.Chmod(dir, 0o500))
}

// Antigravity 的 IDE 和 CLI 各有一个主目录，任一存在就算装了；它自带的内置
// 技能放在 builtin/skills 下，没有插件那样的登记文件。
func TestAntigravityIsFoundByEitherHomeAndReadsBuiltinSkills(t *testing.T) {
	for _, shape := range []string{".gemini/antigravity", ".gemini/antigravity-cli"} {
		t.Run(shape, func(t *testing.T) {
			root := t.TempDir()
			root, _ = filepath.EvalSymlinks(root)
			h := func(p string) string { return filepath.Join(root, p) }
			must(t, os.MkdirAll(h(shape+"/cache"), 0o755))
			writeSkill(t, h(shape+"/builtin/skills/automation"), "")

			l := Scan(root)
			if !slices.ContainsFunc(l.Agents, func(a Agent) bool { return a.ID == "antigravity" }) {
				t.Fatalf("只有 %s 时没认出 Antigravity：%v", shape, agentIDs(l.Agents))
			}
			s := l.Get("automation")
			if s == nil || s.Source != Builtin || s.Provider != "antigravity" {
				t.Fatalf("自带 Skill 应算 Antigravity 的内置：%+v", s)
			}
		})
	}
}

// 用户用插件命令装的技能算在「我的」里，只读；Agent 预装的插件（市场名以
// -builtin 结尾）和 Codex 放在 skills/.system 里的自带技能才是内置。
func TestUserPluginsCountAsMineAndPreinstalledOnesAsBuiltin(t *testing.T) {
	root := t.TempDir()
	root, _ = filepath.EvalSymlinks(root)
	h := func(p string) string { return filepath.Join(root, p) }
	registry := func(file, name, dir string) {
		t.Helper()
		must(t, os.MkdirAll(filepath.Dir(h(file)), 0o755))
		must(t, os.WriteFile(h(file), []byte(`{"plugins":{"`+name+`":[{"installPath":"`+jsonStr(h(dir))+`"}]}}`), 0o644))
	}
	writeSkill(t, h(".claude/plugins/cache/pua-skills/pua/3.0.0/skills/p10"), "")
	registry(".claude/plugins/installed_plugins.json", "pua@pua-skills", ".claude/plugins/cache/pua-skills/pua/3.0.0")
	writeSkill(t, h(".workbuddy/plugins/cache/docx/1.0/skills/docx"), "")
	registry(".workbuddy/plugins/installed_plugins.json", "docx@workbuddy-builtin", ".workbuddy/plugins/cache/docx/1.0")
	must(t, os.WriteFile(h(".workbuddy/config.json"), []byte("{}"), 0o644))
	writeSkill(t, h(".codex/skills/.system/imagegen"), "")
	must(t, os.WriteFile(h(".codex/config.toml"), nil, 0o644))

	l := Scan(root)
	for _, c := range []struct {
		name     string
		src      Source
		provider string
		scope    Scope
	}{
		{"p10", ViaPlugin, "claude-code", ScopeMine},
		{"docx", Builtin, "workbuddy", ScopeBuiltin},
		{"imagegen", Builtin, "codex", ScopeBuiltin},
	} {
		i := slices.IndexFunc(l.Skills, func(s *Skill) bool { return s.Name == c.name })
		if i < 0 {
			t.Errorf("没扫到 %s：%v", c.name, names(l))
			continue
		}
		s := l.Skills[i]
		if s.Source != c.src || s.Provider != c.provider || !c.scope.match(s) || !s.ReadOnly() {
			t.Errorf("%s 应是 %s 的%s、列在 %s、只读：%+v", c.name, c.provider, c.src.Label(), c.scope, s)
		}
	}
}

func TestScan(t *testing.T) {
	home := fakeHome(t)
	l := Scan(home)

	tf := l.ByID("mine:test-first")
	if tf == nil {
		t.Fatalf("没扫到 test-first：%v", names(l))
	}
	if got := agentIDs(l.Agents); !slices.Equal(got, []string{"claude-code", "cursor", "gemini", "codebuddy", "workbuddy"}) {
		t.Errorf("装了的 Agent：%v", got)
	}
	// Cursor 和 Gemini 读通用目录 ~/.agents/skills，库里的技能它们都读到；
	// CodeBuddy 目录里是另一份拷贝，读到的不是这一份
	if want := []string{"claude-code", "cursor", "gemini", "codebuddy"}; !slices.Equal(tf.Enabled, want) || !tf.CopyIn("codebuddy") {
		t.Errorf("test-first 启用于 %v，应为 %v（链接、通用目录都算，拷贝不算）", tf.Enabled, want)
	}
	if tf.Via["cursor"] == "" || tf.Via["gemini"] != l.Root || tf.Links["claude-code"] == "" {
		t.Errorf("Links %v Via %v", tf.Links, tf.Via)
	}
	if tf.Copies != 2 {
		t.Errorf("test-first 副本 %d，应为 2（CodeBuddy 和 Codex 各一份）", tf.Copies)
	}
	if tf.Dir != filepath.Join(home, ".agents/skills/test-first") || tf.Description != "先写测试" {
		t.Errorf("test-first 应以 Skill 库那份为准：%s %q", tf.Dir, tf.Description)
	}
	if s := l.ByID("mine:spark"); s == nil || s.Description != "想法和需求 先聊清楚" || s.Links["cursor"] == "" {
		t.Errorf("spark：%+v", s)
	}
	if s := l.ByID("mine:test-first-old"); s == nil || !slices.Equal(s.Enabled, []string{"cursor"}) {
		t.Errorf("只在 Cursor 目录里的 Skill 也是我的，启用于 Cursor：%+v", s)
	}
	if len(l.Broken) != 1 || !strings.HasSuffix(l.Broken[0], "broken") {
		t.Errorf("坏链接：%v", l.Broken)
	}

	var plugged, project []*Skill
	for _, s := range l.Skills {
		switch s.Source {
		case ViaPlugin:
			plugged = append(plugged, s)
		case Project:
			project = append(project, s)
		}
	}
	if len(plugged) != 1 || plugged[0].Name != "pua" || plugged[0].Provider != "claude-code" {
		t.Errorf("插件装的：%v", plugged)
	}
	if len(project) != 1 || project[0].Name != "notes-sync" || project[0].Project != "storefront" {
		t.Fatalf("项目里同一份 Skill 只算一个：%v", project)
	}
	// 链到 ~/.claude/skills：Claude Code 读，Cursor 也读
	if !slices.Equal(project[0].Enabled, []string{"claude-code", "cursor"}) {
		t.Errorf("notes-sync 被全局链接：%v", project[0].Enabled)
	}
	if got := project[0].Readers(false); !slices.Equal(got, []string{"claude-code", "cursor", "gemini"}) {
		t.Errorf("在项目里读到 notes-sync 的：%v", got)
	}
	if len(l.Projects) != 1 || l.Projects[0].Name != "storefront" || len(l.Projects[0].Linked()) != 1 {
		t.Fatalf("项目：%+v", l.Projects)
	}
	var rels []string
	for _, d := range l.Projects[0].Dirs {
		rels = append(rels, d.Rel)
	}
	if !slices.Equal(rels, []string{".claude/skills", ".agents/skills"}) {
		t.Errorf("项目里的 Skill 目录：%v", rels)
	}
	if l.Get("notes-sync").Source != Project {
		t.Error("全局链接的项目 Skill 不应算成我的")
	}
}

func TestAddBuildsLinksAndNativeDisableKeepsThem(t *testing.T) {
	home := fakeHome(t)
	l := Scan(home)
	tf := l.ByID("mine:test-first")

	must(t, l.AddToAgent(tf, "workbuddy"))
	at := filepath.Join(home, ".workbuddy/skills/test-first")
	target, err := os.Readlink(at)
	if err != nil {
		t.Fatalf("应建链接：%v", err)
	}
	// macOS 建的是相对链接；Windows 没符号链接权限时降级成目录联接，
	// 只能存绝对路径。
	if runtime.GOOS != "windows" && target != "../../.agents/skills/test-first" {
		t.Fatalf("应建相对链接，得到 %q", target)
	}
	if real, err := realPath(at); err != nil || real != filepath.Join(home, ".agents/skills/test-first") {
		t.Fatalf("链接应指向 Skill 库，得到 %q %v", real, err)
	}
	if !tf.EnabledIn("workbuddy") {
		t.Error("启用后状态没更新")
	}

	must(t, l.Enable(tf, "claude-code", false))
	if _, err := os.Readlink(filepath.Join(home, ".claude/skills/test-first")); err != nil {
		t.Error("原生停用必须保留链接")
	}
	if _, err := os.Stat(filepath.Join(home, ".agents/skills/test-first/SKILL.md")); err != nil {
		t.Error("停用不能动 Skill 本身")
	}

	// Cursor 的原生停用尚未适配。
	if err := l.Enable(tf, "cursor", false); err == nil || !strings.Contains(err.Error(), "不支持") {
		t.Errorf("未适配的 Agent 不应修改配置：%v", err)
	}

	// CodeBuddy 里是一份拷贝：拒绝，文件还在
	if err := l.Enable(tf, "codebuddy", false); err == nil {
		t.Error("拷贝不能停用")
	}
	if _, err := os.Stat(filepath.Join(home, ".codebuddy/skills/test-first/SKILL.md")); err != nil || !tf.CopyIn("codebuddy") {
		t.Error("拒绝后拷贝和状态都应保持")
	}

	// 重新扫描与磁盘一致
	if got := Scan(home).ByID("mine:test-first").Enabled; !slices.Equal(got, []string{"cursor", "gemini", "codebuddy", "workbuddy"}) {
		t.Errorf("重新扫描得到 %v", got)
	}
}

func TestManualProjects(t *testing.T) {
	home := fakeHome(t)
	empty := filepath.Join(home, "code", "empty")
	must(t, os.MkdirAll(empty, 0o755))
	stock := filepath.Join(home, "code", "storefront")
	must(t, Scan(home).AddProjects([]string{empty, stock}))
	l := Scan(home)
	if l.Project(empty) != nil {
		t.Error("没有 Skill 的项目不在侧边栏列出")
	}
	var dirs []string
	for _, p := range l.ManualProjects() {
		dirs = append(dirs, p.Dir)
	}
	if !slices.Equal(dirs, []string{empty, stock}) || len(l.ManualProjects()[1].Skills) != 1 {
		t.Fatalf("设置里列出全部手动加的文件夹：%v", dirs)
	}
	must(t, l.RemoveProjects([]string{empty}))
	if n := len(Scan(home).ManualProjects()); n != 1 {
		t.Errorf("移除后还剩 %d 个", n)
	}
}

func TestSettingsPersist(t *testing.T) {
	home := fakeHome(t)
	l := Scan(home)
	if !l.Settings.AutoDiscover || !l.Settings.MergeWorktrees {
		t.Fatalf("默认设置：%+v", l.Settings)
	}
	must(t, l.SetHidden("cursor", true))
	l = Scan(home)
	if !l.Hidden("cursor") || slices.ContainsFunc(l.Visible(), func(a Agent) bool { return a.ID == "cursor" }) {
		t.Error("关掉的 Agent 重新扫描后仍然隐藏")
	}
	if !l.ByID("mine:spark").EnabledIn("cursor") {
		t.Error("隐藏只影响界面，不影响读到")
	}
	l.Settings.AutoDiscover = false
	must(t, l.SaveSettings())
	if l := Scan(home); len(l.Projects) != 0 || l.Discovery.Sources[0].Count != 1 {
		t.Errorf("关掉自动发现后只剩手动加的项目，来源仍计数：%d %+v", len(l.Projects), l.Discovery.Sources)
	}
}

func TestDiscoveryAndWorktrees(t *testing.T) {
	home := fakeHome(t)
	h := func(p string) string { return filepath.Join(home, p) }
	// storefront 的一个 worktree，Codex 在里面用过
	wt := h("code/storefront-wt")
	must(t, os.MkdirAll(h("code/storefront/.git/worktrees/wt"), 0o755))
	must(t, os.MkdirAll(wt, 0o755))
	must(t, os.WriteFile(filepath.Join(wt, ".git"), []byte("gitdir: "+h("code/storefront/.git/worktrees/wt")+"\n"), 0o644))
	must(t, os.MkdirAll(h(".codex/sessions/2026"), 0o755))
	must(t, os.WriteFile(h(".codex/sessions/2026/a.jsonl"), []byte(`{"payload":{"cwd":"`+jsonStr(wt)+`"}}`+"\n"), 0o644))

	l := Scan(home)
	d := l.Discovery
	if d.Total != 1 || d.WithSkills != 1 {
		t.Errorf("worktree 合并到主仓库后只有 1 个项目：%+v", d)
	}
	counts := map[string]int{}
	for _, s := range d.Sources {
		counts[s.Agent] = s.Count
	}
	if counts["claude-code"] != 1 || counts["codex"] != 1 || counts["vscode"] != -1 {
		t.Errorf("各处使用记录：%v", counts)
	}
	l.Settings.MergeWorktrees = false
	must(t, l.SaveSettings())
	if d := Scan(home).Discovery; d.Total != 2 {
		t.Errorf("不合并时 worktree 单独算：%+v", d)
	}
}

func TestCheckLinks(t *testing.T) {
	home := fakeHome(t)
	h := func(p string) string { return filepath.Join(home, p) }
	mustLink(t, h("old/spark"), h(".claude/skills/spark")) // 断了，技能库里有 spark
	r, err := Scan(home).CheckLinks()
	must(t, err)
	if len(r.Fixed) != 1 || r.Fixed[0] != h(".claude/skills/spark") || len(r.Broken) != 1 || r.Checked < 3 {
		t.Fatalf("检查结果：%+v", r)
	}
	if real, _ := realPath(h(".claude/skills/spark")); real != h(".agents/skills/spark") {
		t.Errorf("修复后指向 %s", real)
	}
}

func TestCustomAgentDir(t *testing.T) {
	home := fakeHome(t)
	dir := filepath.Join(home, ".foo", "skills")
	must(t, os.MkdirAll(filepath.Join(dir, "x"), 0o755))
	must(t, os.WriteFile(filepath.Join(dir, "x", "SKILL.md"), []byte("---\nname: x\n---\n"), 0o644))
	must(t, Scan(home).AddAgentDir(dir))
	l := Scan(home)
	id := "dir:" + dir
	if AgentName(id) != "foo" || !l.ByID("mine:x").EnabledIn(id) {
		t.Fatalf("手动添加的目录算一个 Agent：%v", l.ByID("mine:x"))
	}
	must(t, l.AddToAgent(l.ByID("mine:spark"), id))
	if _, err := os.Lstat(filepath.Join(dir, "spark")); err != nil {
		t.Error("可以在手动添加的目录里启用 Skill")
	}
	must(t, l.RemoveAgentDir(id))
	if slices.ContainsFunc(Scan(home).Agents, func(a Agent) bool { return a.ID == id }) {
		t.Error("移除后不再算 Agent")
	}
}

func TestLoadFiles(t *testing.T) {
	l := Scan(fakeHome(t))
	s := l.ByID("mine:test-first")
	l.LoadFiles(s)
	var paths []string
	for _, f := range s.Files {
		paths = append(paths, f.Path)
	}
	if !slices.Equal(paths, []string{"SKILL.md", "notes.md"}) || s.Files[1].Content != "# 笔记\n" {
		t.Errorf("files = %v", paths)
	}
}

func TestLoadFilesIncludesHiddenFilesAndSkipsNoise(t *testing.T) {
	home := fakeHome(t)
	dir := filepath.Join(home, ".agents/skills/test-first")
	write := func(rel, body string) {
		p := filepath.Join(dir, rel)
		must(t, os.MkdirAll(filepath.Dir(p), 0o755))
		must(t, os.WriteFile(p, []byte(body), 0o644))
	}
	write(".env.example", "TOKEN=\n")
	write(".hidden/run.sh", "#!/bin/sh\necho hi\n")
	write(".DS_Store", "finder")
	write("._SKILL.md", "appledouble")
	write(".git/HEAD", "ref: refs/heads/main\n")
	write("node_modules/pkg/index.js", "module.exports = {}\n")
	write("__pycache__/a.pyc", "pyc")

	l := Scan(home)
	s := l.ByID("mine:test-first")
	l.LoadFiles(s)
	var paths []string
	for _, f := range s.Files {
		paths = append(paths, f.Path)
	}
	want := []string{"SKILL.md", ".env.example", "notes.md", ".hidden/run.sh"}
	if !slices.Equal(paths, want) {
		t.Fatalf("files = %v，期望 %v", paths, want)
	}
}

func TestParseBlockDescription(t *testing.T) {
	md := ParseSkillMD("---\nname: a\ndescription: |\n  第一行\n  第二行\nmetadata:\n  k: v\n---\n")
	if md.Description != "第一行\n第二行" {
		t.Errorf("| 块：%q", md.Description)
	}
	if got := ParseSkillMD("---\nname: a\ndescription: \"带引号\"\n---\n").Description; got != "带引号" {
		t.Errorf("引号：%q", got)
	}
}

func agentIDs(as []Agent) []string {
	var out []string
	for _, a := range as {
		out = append(out, a.ID)
	}
	return out
}

func names(l *Library) []string {
	var out []string
	for _, s := range l.Skills {
		out = append(out, s.ID)
	}
	return out
}

func TestSharedAndTidy(t *testing.T) {
	home := fakeHome(t)
	l := Scan(home)
	if !slices.Equal(l.SharedReaders(), []string{"cursor", "gemini"}) {
		t.Fatalf("读通用目录的：%v", l.SharedReaders())
	}
	tf := l.ByID("mine:test-first")
	if !tf.Shared || !slices.Equal(l.Separate(tf), []string{"claude-code", "codebuddy"}) {
		t.Errorf("test-first 单独启用于 %v", l.Separate(tf))
	}
	if old := l.ByID("mine:test-first-old"); old.Shared || !slices.Equal(l.Separate(old), []string{"cursor"}) {
		t.Errorf("不在通用目录：%v", l.Separate(old))
	}
	// spark 在通用目录，Cursor 自己目录里又链过去一次：多余
	spark := l.ByID("mine:spark")
	if !slices.Equal(spark.Redundant(), []string{"cursor"}) || len(l.Separate(spark)) != 0 {
		t.Fatalf("Redundant %v Separate %v", spark.Redundant(), l.Separate(spark))
	}
	if err := l.Tidy(spark); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(filepath.Join(home, ".cursor/skills/spark")); !os.IsNotExist(err) {
		t.Error("多余的链接应删掉")
	}
	if _, err := os.Stat(filepath.Join(home, ".agents/skills/spark/SKILL.md")); err != nil {
		t.Error("Skill 本身不能动")
	}
	if len(spark.Redundant()) != 0 || !spark.EnabledIn("cursor") || spark.Via["cursor"] != l.Root {
		t.Errorf("清理后 Cursor 仍经通用目录读到：Redundant %v Via %v", spark.Redundant(), spark.Via)
	}
}

func TestScanCollections(t *testing.T) {
	home := fakeHome(t)
	h := func(p string) string { return filepath.Join(home, p) }
	skill := func(dir string) {
		t.Helper()
		must(t, os.MkdirAll(dir, 0o755))
		must(t, os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte("---\nname: x\n---\n"), 0o644))
	}
	// npx skills 从同一个来源装的两个；只装了一个的来源不算集合
	skill(h(".agents/skills/lark-doc"))
	skill(h(".agents/skills/lark-im"))
	must(t, os.WriteFile(h(".agents/.skill-lock.json"), []byte(`{"version":3,"skills":{
		"lark-doc":{"source":"open.feishu.cn","installedAt":"2026-09-21T02:55:56Z"},
		"lark-im":{"source":"open.feishu.cn","installedAt":"2026-09-21T02:56:10Z"},
		"spark":{"source":"someone/seedex"},
		"gone":{"source":"open.feishu.cn"}}}`), 0o644))
	// 整个文件夹链进 Claude Code；Cursor 里两个单独链到另一个文件夹
	skill(h("code/superpowers/skills/brainstorm"))
	skill(h("code/superpowers/skills/tdd"))
	mustLink(t, h("code/superpowers/skills"), h(".claude/skills/superpowers"))
	skill(h("Skills/a"))
	skill(h("Skills/b"))
	mustLink(t, h("Skills/a"), h(".cursor/skills/a"))
	mustLink(t, h("Skills/b"), h(".cursor/skills/b"))

	l := Scan(home)
	sets := map[string]*Collection{}
	for _, c := range l.Collections {
		sets[c.Name] = c
	}
	if len(sets) != 3 {
		t.Fatalf("集合：%v", sets)
	}
	lark := sets["open.feishu.cn"]
	if lark == nil || lark.Kind != SetSource || len(lark.Skills) != 2 || lark.Installed.Day() != 21 {
		t.Errorf("open.feishu.cn = %+v", lark)
	}
	if sp := sets["superpowers"]; sp == nil || sp.Kind != SetFolder || len(sp.Skills) != 2 {
		t.Errorf("整组链接进来的应是一个文件夹集合：%+v", sp)
	}
	if s := sets["Skills"]; s == nil || len(s.Skills) != 2 {
		t.Errorf("链到同一个文件夹的应是一个集合：%+v", s)
	}
	if tf := l.ByID("mine:test-first"); tf.Collection != nil {
		t.Error("Agent 目录之间的链接不算集合")
	}
}

func TestConcurrentScanAndCatalogIndex(t *testing.T) {
	home := fakeHome(t)
	dir := filepath.Join(home, ".foo", "skills")
	must(t, os.MkdirAll(filepath.Join(dir, "x"), 0o755))
	must(t, os.WriteFile(filepath.Join(dir, "x", "SKILL.md"), []byte("---\nname: x\n---\n"), 0o644))
	must(t, Scan(home).AddAgentDir(dir))
	id := "dir:" + dir

	var wg sync.WaitGroup
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 10 {
				l := Scan(home)
				if idx := l.catalogIndex(id); idx < len(Catalog) {
					t.Errorf("custom agent index %d < len(Catalog) %d", idx, len(Catalog))
				}
			}
		}()
	}
	wg.Wait()
}
