package library

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// zcodeHome 复现本机的真实布局：kami 的真身在 ~/.claude/skills/kami，
// ~/.zcode/skills/kami 是指向它的软链接；ZCode 按解析后的 SKILL.md 路径记停用。
func zcodeHome(t *testing.T) (home string, h func(string) string, key string) {
	t.Helper()
	home, h = linkHome(t, "claude-code", "zcode", "codex")
	writeSkill(t, h(".claude/skills/kami"), "unchanged")
	must(t, os.MkdirAll(h(".zcode/skills"), 0o755))
	mustLink(t, h(".claude/skills/kami"), h(".zcode/skills/kami"))
	return home, h, h(".claude/skills/kami/SKILL.md")
}

func zcodeConfigFile(h func(string) string) string { return h(".zcode/cli/config.json") }

func writeZCodeConfig(t *testing.T, h func(string) string, src string) {
	t.Helper()
	must(t, os.MkdirAll(filepath.Dir(zcodeConfigFile(h)), 0o755))
	must(t, os.WriteFile(zcodeConfigFile(h), []byte(src), 0o600))
}

func zcodeEntries(t *testing.T, h func(string) string) map[string]any {
	t.Helper()
	b, err := os.ReadFile(zcodeConfigFile(h))
	must(t, err)
	var doc map[string]any
	must(t, json.Unmarshal(b, &doc))
	skills, _ := doc["skills"].(map[string]any)
	return skills
}

func TestZCodeSkillIsOnByDefaultAndTogglesByRealSkillPath(t *testing.T) {
	home, h, key := zcodeHome(t)
	t.Setenv("HOME", home)
	original := "{\n  \"plugins\": {\n    \"enabledPlugins\": {\n      \"tianyancha@zcode-plugins-official\": true\n    }\n  }\n}\n"
	writeZCodeConfig(t, h, original)
	before, _ := os.ReadFile(h(".claude/skills/kami/SKILL.md"))

	l := Scan(home)
	s := l.Get("kami")
	st := s.State("zcode")
	if !st.Discoverable || !st.Enabled || st.Kind != StateEnabled || !st.Controllable() {
		t.Fatalf("ZCode 读得到又没被停用的 Skill 应是可拨的已启用：%+v", st)
	}

	must(t, l.Enable(s, "zcode", false))
	if s.EnabledIn("zcode") || !s.DiscoveredIn("zcode") {
		t.Fatalf("停用后应仍可发现、但不再启用：%+v", s.State("zcode"))
	}
	entry, ok := zcodeEntries(t, h)[key].(map[string]any)
	if !ok || entry["enable"] != false {
		t.Fatalf("应按真实 SKILL.md 路径写 enable:false：%v", zcodeEntries(t, h))
	}
	if link, err := os.Readlink(h(".zcode/skills/kami")); err != nil || link != h(".claude/skills/kami") {
		t.Fatal("停用不该动软链接")
	}
	if s.State("zcode").Kind == StateRestart {
		t.Fatal("ZCode 每次列 Skill 都重读配置，不需要重启")
	}

	l = Scan(home)
	s = l.Get("kami")
	if s.EnabledIn("zcode") || s.State("zcode").Kind != StateDisabled {
		t.Fatalf("停用没有持久：%+v", s.State("zcode"))
	}
	if !s.EnabledIn("claude-code") {
		t.Fatal("不该影响别的 Agent")
	}

	must(t, l.Enable(s, "zcode", true))
	if !s.EnabledIn("zcode") {
		t.Fatalf("开启失败：%+v", s.State("zcode"))
	}
	b, _ := os.ReadFile(zcodeConfigFile(h))
	if string(b) != original {
		t.Fatalf("开启应删掉那一项、保留其余配置原样：\n%s", b)
	}
	after, _ := os.ReadFile(h(".claude/skills/kami/SKILL.md"))
	if string(before) != string(after) {
		t.Fatal("Skill 文件被改了")
	}
}

func TestZCodeCreatesConfigWhenMissingAndCleansUpAfterEnabling(t *testing.T) {
	home, h, key := zcodeHome(t)
	t.Setenv("HOME", home)
	l := Scan(home)
	s := l.Get("kami")
	if !s.EnabledIn("zcode") {
		t.Fatalf("没有配置文件时默认开启：%+v", s.State("zcode"))
	}
	must(t, l.Enable(s, "zcode", false))
	if e, _ := zcodeEntries(t, h)[key].(map[string]any); e["enable"] != false {
		t.Fatal(zcodeEntries(t, h))
	}
	must(t, l.Enable(s, "zcode", true))
	b, _ := os.ReadFile(zcodeConfigFile(h))
	if strings.Contains(string(b), "skills") {
		t.Fatalf("自己建出来的 skills 应一并收掉：%s", b)
	}
}

func TestZCodeHonoursDisablesWrittenByZCodeUnderAnyAliasPath(t *testing.T) {
	for name, cfg := range map[string]string{
		"real path":        `{"skills":{"%REAL%":{"enable":false}}}`,
		"symlink path":     `{"skills":{"%LINK%":{"enable":false}}}`,
		"legacy skill key": `{"skill":{"%REAL%":{"enable":false}}}`,
	} {
		t.Run(name, func(t *testing.T) {
			home, h, key := zcodeHome(t)
			t.Setenv("HOME", home)
			src := strings.NewReplacer("%REAL%", jsonStr(key), "%LINK%", jsonStr(h(".zcode/skills/kami/SKILL.md"))).Replace(cfg)
			writeZCodeConfig(t, h, src)
			l := Scan(home)
			s := l.Get("kami")
			if st := s.State("zcode"); st.Enabled || st.Kind != StateDisabled {
				t.Fatalf("ZCode 里关掉的 Skill 应显示已停用：%+v", st)
			}
			must(t, l.Enable(s, "zcode", true))
			l = Scan(home)
			if !l.Get("kami").EnabledIn("zcode") {
				t.Fatalf("开启应解除所有指向它的停用项：%s", must2(os.ReadFile(zcodeConfigFile(h))))
			}
		})
	}
}

func TestZCodeEntriesThatDoNotDisableKeepTheSkillOn(t *testing.T) {
	for _, cfg := range []string{
		`{"skills":{"%REAL%":{"enable":true}}}`,
		`{"skills":{"%REAL%":{}}}`,
		// 复数形式覆盖同一键的旧写法
		`{"skill":{"%REAL%":{"enable":false}},"skills":{"%REAL%":{"enable":true}}}`,
		`{"skills":{"/elsewhere/kami/SKILL.md":{"enable":false}}}`,
		// 相对路径不会被 ZCode 当成技能路径
		`{"skills":{"kami":{"enable":false}}}`,
	} {
		home, h, key := zcodeHome(t)
		t.Setenv("HOME", home)
		writeZCodeConfig(t, h, strings.ReplaceAll(cfg, "%REAL%", jsonStr(key)))
		if st := Scan(home).Get("kami").State("zcode"); !st.Enabled || st.Kind != StateEnabled {
			t.Errorf("%s 不该让它停用：%+v", cfg, st)
		}
	}
}

func TestZCodeUnreadableConfigIsUnknownAndNeverOverwritten(t *testing.T) {
	for _, cfg := range []string{
		"broken config",
		"[]",
		"{\n  // ZCode 用 JSON.parse，注释会让整份配置失效\n}\n",
		`{"skills":[]}`,
		`{"skills":{"%REAL%":true}}`,
		`{"skills":{"%REAL%":{"enable":"no"}}}`,
		`{"skills":{"enabled":"yes"}}`,
		`{"skill":{"%REAL%":{"enable":0}}}`,
		`{"features":{"skill":"off"}}`,
		`{"features":{"mcp":1}}`,
		`{"features":[]}`,
		`{"skills":{"roots":123}}`,
		`{"skills":{"roots":["a",1]}}`,
		`{"skills":{"includeInstructions":"yes"}}`,
		`{"skills":{"metadataBudget":0}}`,
		`{"skills":{"metadataBudget":"9"}}`,
		`{"skills":{"anything":1}}`,
		`{"skill":[]}`,
	} {
		home, h, key := zcodeHome(t)
		t.Setenv("HOME", home)
		src := strings.ReplaceAll(cfg, "%REAL%", jsonStr(key))
		writeZCodeConfig(t, h, src)
		l := Scan(home)
		s := l.Get("kami")
		if st := s.State("zcode"); st.Kind != StateUnknown || st.Message == "" {
			t.Errorf("%s 应显示未知并说明原因：%+v", cfg, st)
			continue
		}
		if l.Enable(s, "zcode", false) == nil {
			t.Errorf("%s 读不懂时不该写", cfg)
		}
		if b, _ := os.ReadFile(zcodeConfigFile(h)); string(b) != src {
			t.Errorf("%s 被改写成 %s", cfg, b)
		}
	}
}

func TestZCodeValidSkillRuntimeSettingsKeepTheToggle(t *testing.T) {
	home, h, _ := zcodeHome(t)
	t.Setenv("HOME", home)
	writeZCodeConfig(t, h, `{"features":{"mcp":true,"skill":true,"other":"ignored"},"skills":{"enabled":true,"includeInstructions":false,"metadataBudget":2.5,"roots":["~/more"]}}`)
	if st := Scan(home).Get("kami").State("zcode"); st.Kind != StateEnabled || !st.Controllable() {
		t.Fatalf("合法的运行设置不该让它变成未知：%+v", st)
	}
}

func TestZCodeMasterSwitchOffBlocksSingleToggle(t *testing.T) {
	for _, cfg := range []string{`{"features":{"skill":false}}`, `{"skills":{"enabled":false}}`} {
		home, h, _ := zcodeHome(t)
		t.Setenv("HOME", home)
		writeZCodeConfig(t, h, cfg)
		l := Scan(home)
		s := l.Get("kami")
		st := s.State("zcode")
		if st.Enabled || st.Kind != StateDisabled || st.Controllable() || st.Message == "" {
			t.Errorf("%s 关了全部 Skill：不能报启用，也不能单独拨：%+v", cfg, st)
		}
		if l.Enable(s, "zcode", true) == nil {
			t.Errorf("%s 总开关关着时单独开启应失败", cfg)
		}
		if b, _ := os.ReadFile(zcodeConfigFile(h)); string(b) != cfg {
			t.Errorf("%s 被改写成 %s", cfg, b)
		}
	}
}

func zcodeProject(t *testing.T, h func(string) string, l *Library) string {
	t.Helper()
	dir := h("code/app")
	must(t, os.MkdirAll(filepath.Join(dir, ".git"), 0o755))
	must(t, l.AddProjects([]string{dir}))
	return dir
}

func TestZCodeProjectConfigsLayerOverTheUserConfig(t *testing.T) {
	home, h, key := zcodeHome(t)
	t.Setenv("HOME", home)
	dir := zcodeProject(t, h, Scan(home))
	must(t, os.MkdirAll(filepath.Join(dir, ".zcode"), 0o755))
	project := `{"skills":{"` + jsonStr(key) + `":{"enable":false}}}`
	must(t, os.WriteFile(filepath.Join(dir, ".zcode/config.json"), []byte(project), 0o600))

	l := Scan(home)
	s := l.Get("kami")
	st := s.State("zcode")
	if !st.Enabled || !slices.Equal(st.Projects, []string{"app"}) || !strings.Contains(st.Message, "app") {
		t.Fatalf("全局开着、项目里关了：全局仍启用，并说明项目里仍停用：%+v", st)
	}
	must(t, l.Enable(s, "zcode", false))
	must(t, l.Enable(s, "zcode", true))
	if b, _ := os.ReadFile(filepath.Join(dir, ".zcode/config.json")); string(b) != project {
		t.Fatal("全局开关不该改项目配置")
	}

	// 项目里同一键写 enable:true 会盖过全局的停用
	must(t, os.WriteFile(filepath.Join(dir, ".zcode/config.json"), []byte(`{"skills":{"`+jsonStr(key)+`":{"enable":true}}}`), 0o600))
	must(t, l.Enable(Scan(home).Get("kami"), "zcode", false))
	st = Scan(home).Get("kami").State("zcode")
	if st.Enabled || st.Kind != StateDisabled || !strings.Contains(st.Message, "app") {
		t.Fatalf("全局停用、项目里开着：要如实说明：%+v", st)
	}

	// 总开关只认用户配置；项目里写了也不生效
	must(t, os.WriteFile(filepath.Join(dir, "zcode.json"), []byte(`{"skills":{"enabled":false},"features":{"skill":false}}`), 0o600))
	must(t, os.Remove(filepath.Join(dir, ".zcode/config.json")))
	l = Scan(home)
	must(t, l.Enable(l.Get("kami"), "zcode", true))
	if st := l.Get("kami").State("zcode"); !st.Enabled || len(st.Projects) > 0 || !st.Controllable() {
		t.Fatalf("项目配置不能关总开关：%+v", st)
	}

	must(t, os.WriteFile(filepath.Join(dir, "zcode.json"), []byte("{broken"), 0o600))
	if st := Scan(home).Get("kami").State("zcode"); !st.Enabled || !strings.Contains(st.Message, "app") {
		t.Fatalf("项目配置读不了要说明，但不影响全局状态：%+v", st)
	}
}

func TestZCodeProjectConfigsFromParentFoldersUpToTheRepositoryApply(t *testing.T) {
	home, h, key := zcodeHome(t)
	t.Setenv("HOME", home)
	must(t, os.MkdirAll(h("mono/.git"), 0o755))
	dir := h("mono/packages/web")
	must(t, os.MkdirAll(dir, 0o755))
	must(t, Scan(home).AddProjects([]string{dir}))
	// 仓库根关掉，子目录又打开：离工作目录近的后读，覆盖前面的
	must(t, os.WriteFile(h("mono/zcode.json"), []byte(`{"skills":{"`+jsonStr(key)+`":{"enable":false}}}`), 0o600))
	if st := Scan(home).Get("kami").State("zcode"); !slices.Equal(st.Projects, []string{"web"}) {
		t.Fatalf("仓库根的配置也作用于子目录项目：%+v", st)
	}
	must(t, os.MkdirAll(filepath.Join(dir, ".zcode"), 0o755))
	must(t, os.WriteFile(filepath.Join(dir, ".zcode/config.json"), []byte(`{"skills":{"`+jsonStr(key)+`":{"enable":true}}}`), 0o600))
	if st := Scan(home).Get("kami").State("zcode"); len(st.Projects) > 0 {
		t.Fatalf("离项目近的配置后读、覆盖仓库根：%+v", st)
	}
}

func TestZCodeReadsTheSharedLibraryOnce(t *testing.T) {
	home, h := linkHome(t, "zcode")
	t.Setenv("HOME", home)
	writeSkill(t, h(".agents/skills/shared"), "")
	writeSkill(t, h(".agents/skills/both"), "")
	must(t, os.MkdirAll(h(".zcode/skills"), 0o755))
	mustLink(t, h(".agents/skills/both"), h(".zcode/skills/both"))
	l := Scan(home)
	if s := l.Get("shared"); !s.DiscoveredIn("zcode") || !s.EnabledIn("zcode") {
		t.Fatalf("ZCode 也读 ~/.agents/skills：%+v", s.State("zcode"))
	}
	both := l.Get("both")
	if n := len(slices.DeleteFunc(slices.Clone(both.Available), func(id string) bool { return id != "zcode" })); n != 1 {
		t.Fatalf("两个目录都有时 ZCode 只算一次：%v", both.Available)
	}
	must(t, l.Enable(both, "zcode", false))
	if Scan(home).Get("both").EnabledIn("zcode") {
		t.Fatal("经两条路径读到的同一份 Skill 应一起停用")
	}
}

func TestZCodeProjectSkillStateIncludesUserAndProjectConfig(t *testing.T) {
	home, h := linkHome(t, "zcode")
	t.Setenv("HOME", home)
	dir := zcodeProject(t, h, Scan(home))
	writeSkill(t, filepath.Join(dir, ".zcode/skills/deploy"), "")
	l := Scan(home)
	s := projectSkill(t, l, "deploy")
	if st, ok := l.ProjectAgentState(s, "zcode"); !ok || st.Kind != StateEnabled || !st.Enabled {
		t.Fatalf("项目 Skill 默认在 ZCode 里启用：%+v %v", st, ok)
	}
	key := filepath.Join(dir, ".zcode/skills/deploy/SKILL.md")
	writeZCodeConfig(t, h, `{"skills":{"`+jsonStr(key)+`":{"enable":false}}}`)
	l = Scan(home)
	if st, _ := l.ProjectAgentState(projectSkill(t, l, "deploy"), "zcode"); st.Enabled || st.Kind != StateDisabled {
		t.Fatalf("在 ZCode 里关掉的项目 Skill 不能当成已启用：%+v", st)
	}
	must(t, os.WriteFile(filepath.Join(dir, "zcode.json"), []byte(`{"skills":{"`+jsonStr(key)+`":{"enable":true}}}`), 0o600))
	l = Scan(home)
	if st, _ := l.ProjectAgentState(projectSkill(t, l, "deploy"), "zcode"); !st.Enabled {
		t.Fatalf("项目配置覆盖用户配置：%+v", st)
	}
	if _, ok := l.ProjectAgentState(projectSkill(t, l, "deploy"), "claude-code"); ok {
		t.Fatal("其他 Agent 不在这里报项目状态")
	}
}

func TestZCodeProjectSkillIsUnknownWhenAProjectConfigIsUnreadable(t *testing.T) {
	home, h := linkHome(t, "zcode")
	t.Setenv("HOME", home)
	dir := zcodeProject(t, h, Scan(home))
	writeSkill(t, filepath.Join(dir, ".zcode/skills/deploy"), "")
	must(t, os.WriteFile(filepath.Join(dir, "zcode.json"), []byte("{broken"), 0o600))
	l := Scan(home)
	if st, _ := l.ProjectAgentState(projectSkill(t, l, "deploy"), "zcode"); st.Kind != StateUnknown || st.Enabled || !strings.Contains(st.Message, "app") {
		t.Fatalf("项目配置读不懂：不能报启用，要说明哪个项目：%+v", st)
	}

	// 读不了（权限）不能当成没有这份配置：文件本身读不了，或所在目录查不了
	must(t, os.Chmod(filepath.Join(dir, "zcode.json"), 0o000))
	t.Cleanup(func() { os.Chmod(filepath.Join(dir, "zcode.json"), 0o600) })
	if _, err := os.ReadFile(filepath.Join(dir, "zcode.json")); err == nil {
		t.Skip("以 root 运行，权限限制不生效")
	}
	l = Scan(home)
	if st, _ := l.ProjectAgentState(projectSkill(t, l, "deploy"), "zcode"); st.Kind != StateUnknown {
		t.Fatalf("读不了的项目配置不能当成不存在：%+v", st)
	}
}

func TestZCodeProjectConfigInAFolderThatCannotBeSearchedIsUnknown(t *testing.T) {
	home, h := linkHome(t, "zcode")
	t.Setenv("HOME", home)
	must(t, os.MkdirAll(h("mono/.git"), 0o755))
	dir := h("mono/web")
	writeSkill(t, filepath.Join(dir, ".zcode/skills/deploy"), "")
	must(t, Scan(home).AddProjects([]string{dir}))
	must(t, os.MkdirAll(h("mono/.zcode"), 0o755))
	must(t, os.WriteFile(h("mono/.zcode/config.json"), []byte("{}"), 0o600))
	// 仓库根的 .zcode 去掉查找权限：里面的 config.json 查不了，不等于不存在
	must(t, os.Chmod(h("mono/.zcode"), 0o600))
	t.Cleanup(func() { os.Chmod(h("mono/.zcode"), 0o755) })
	if _, err := os.Stat(h("mono/.zcode/config.json")); err == nil {
		t.Skip("以 root 运行，权限限制不生效")
	}
	l := Scan(home)
	if st, _ := l.ProjectAgentState(projectSkill(t, l, "deploy"), "zcode"); st.Kind != StateUnknown || !strings.Contains(st.Message, "web") {
		t.Fatalf("查不了的项目配置不能当成不存在：%+v", st)
	}
}

func TestZCodeConfigsAreWatched(t *testing.T) {
	home, h, _ := zcodeHome(t)
	t.Setenv("HOME", home)
	writeZCodeConfig(t, h, "{}")
	dir := zcodeProject(t, h, Scan(home))
	writeSkill(t, filepath.Join(dir, ".zcode/skills/deploy"), "")
	must(t, os.WriteFile(filepath.Join(dir, "zcode.json"), []byte("{}"), 0o600))
	must(t, os.WriteFile(filepath.Join(dir, ".zcode/config.json"), []byte("{}"), 0o600))
	plan := Scan(home).WatchPlan()
	for _, want := range []string{zcodeConfigFile(h), filepath.Join(dir, "zcode.json"), filepath.Join(dir, ".zcode/config.json")} {
		if !slices.Contains(plan.Files, want) {
			t.Errorf("应监听 %s：%v", want, plan.Files)
		}
	}
	if len(plan.Creates) != 0 {
		t.Errorf("配置都在时只做单文件监听：%+v", plan.Creates)
	}
}

func TestZCodeConfigsNotCreatedYetAreAwaitedInTheirParentFolder(t *testing.T) {
	home, h, _ := zcodeHome(t)
	t.Setenv("HOME", home)
	must(t, os.MkdirAll(h(".zcode/cli/log"), 0o755))
	dir := zcodeProject(t, h, Scan(home))
	plan := Scan(home).WatchPlan()
	want := []WatchCreate{
		{Dir: h(".zcode/cli"), Names: []string{"config.json"}},
		{Dir: dir, Names: []string{".zcode", "zcode.json"}},
	}
	for _, w := range want {
		if !slices.ContainsFunc(plan.Creates, func(c WatchCreate) bool { return c.Dir == w.Dir && slices.Equal(c.Names, w.Names) }) {
			t.Errorf("应在 %s 等 %v 出现：%+v", w.Dir, w.Names, plan.Creates)
		}
	}
	if slices.Contains(plan.Dirs, h(".zcode/cli")) || slices.Contains(plan.Dirs, dir) {
		t.Errorf("不能递归监听会话日志所在目录或整个项目：%v", plan.Dirs)
	}
}

func projectSkill(t *testing.T, l *Library, name string) *Skill {
	t.Helper()
	for _, s := range l.Skills {
		if s.Source == Project && s.Name == name {
			return s
		}
	}
	t.Fatalf("没有项目 Skill %s", name)
	return nil
}

func must2(b []byte, _ error) string { return string(b) }

func TestDeletingASkillClearsOwnZCodeDisableEvenWhenAllSkillsAreOff(t *testing.T) {
	home, h, key := zcodeHome(t)
	t.Setenv("HOME", home)
	l := Scan(home)
	must(t, l.Enable(l.Get("kami"), "zcode", false))
	b, _ := os.ReadFile(zcodeConfigFile(h))
	src := strings.Replace(string(b), "{\n", "{\n  \"features\": {\"skill\": false},\n", 1)
	writeZCodeConfig(t, h, src)
	l = Scan(home)
	s := l.Get("kami")
	if st := s.State("zcode"); st.Enabled || !st.Blocked {
		t.Fatalf("%+v", st)
	}
	if _, err := l.Delete(s); err != nil {
		t.Fatalf("删除时清理自己写的停用项不该被总开关挡住：%v", err)
	}
	if _, ok := zcodeEntries(t, h)[key]; ok {
		t.Fatalf("删掉的 Skill 留下了停用项：%s", must2(os.ReadFile(zcodeConfigFile(h))))
	}
}
