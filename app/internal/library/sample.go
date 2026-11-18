package library

import (
	"embed"
	"fmt"
	"io/fs"
	"maps"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/egoist/mygo/i18n"
)

// sample/ 下是有完整目录的示例技能；其余示例只有一份生成的 SKILL.md。
//
//go:embed sample
var sampleFS embed.FS

const (
	cl = "claude-code"
	cx = "codex"
	cu = "cursor"
	cb = "codebuddy"
	wb = "workbuddy"
	sh = "通用" // 示例里标记「在通用目录里」，Codex 和 Cursor 直接读到
)

// Sample 返回界面开发用的示例技能库，内容与画板 r8 / r9 一致。
// 接入真实扫描后只在开发和测试里用。
func Sample() *Library {
	mine := func(name, desc string, enabled ...string) *Skill {
		s := &Skill{Name: name, Description: desc, Source: Mine}
		for _, id := range enabled {
			if id == sh {
				s.Shared = true
			} else {
				s.Enabled = append(s.Enabled, id)
			}
		}
		return s
	}
	builtin := func(name, desc, provider string) *Skill {
		return &Skill{Name: name, Description: desc, Source: Builtin, Provider: provider}
	}
	// 插件是用户用 /plugin install 装的
	plugin := func(s *Skill, name string) *Skill {
		s.Plugin, s.Source = name, ViaPlugin
		return s
	}
	project := func(name, desc, proj string, global ...string) *Skill {
		s := &Skill{Name: name, Description: desc, Source: Project, Project: proj, Enabled: global,
			Reads: []Read{{cl, ".claude/skills", false}}}
		for _, ag := range global {
			s.Reads = append(s.Reads, Read{ag, "~/.claude/skills", true})
		}
		return s
	}
	testFirst := mine("test-first", "", sh, cl, cb)

	l := &Library{Root: "~/.agents/skills", Skills: []*Skill{
		testFirst,
		mine("vue-best-practices", "Vue 组件拆分、状态管理与组合式 API 规范", sh, cl, cb, wb),
		mine("code-review", "按清单审查代码变更，先找正确性问题", sh, cl),
		mine("doc-coauthoring", "", sh),
		mine("code-comments", "注释先想清楚写给谁看：调用者还是维护者", sh, cl),
		mine("systematic-debugging", "先复现、再定位、最后修复，不靠猜", cl),
		mine("interface-craft", "界面设计的判断方法与审查清单", cl, cb),
		mine("kami", "把文档排成可打印的 PDF 与幻灯片", sh, cl, wb),
		mine("git-flow", "分支与发布流程约定", cl),
		mine("context-compression", "把分析结论讲给没读过代码的人", sh, cl),
		mine("brainstorming", "动手前先把需求问清楚", sh, cl, cb),
		mine("api-design", "REST 接口命名与错误码约定", sh, cb),
		mine("tailwind-rules", "Tailwind 类名顺序与拆分规则", cu),
		mine("miniprogram-guide", "小程序目录结构与分包约定", cb),
		mine("wechat-pay", "微信支付接入与回调校验", cb),
		mine("i18n-copy", "中英文案对照与术语表", cb),
		mine("sql-review", "慢查询与索引检查清单", cb),
		mine("log-analysis", "从日志里找出报错和慢请求", cl, wb),
		mine("notes-sync", "把笔记同步备份到远端", cl, wb),
		mine("incident-review", "按时间线复盘线上故障", cl, wb),
		mine("dataviz", "图表配色与版式规范", cl, wb),
		mine("spark", "把想法聊成一份需求文档"),
		mine("design-lab", "一次生成五种界面方案对比", sh),
		mine("web-artifacts-builder", "做多组件的 HTML 页面产物", sh, cl, cb),
		mine("frontend-design", "做有辨识度的前端界面，不落俗套", sh, cl),
		mine("code-ablation", "逐段删减代码，找出真正起作用的部分", sh, cl),
		mine("pre-mortem", "动手前假设项目已经失败，倒推原因", sh, cl),
		mine("lark-calendar", "飞书日历：查看、搜索和创建日程", sh),
		mine("lark-doc", "飞书云文档：读取、创建和编辑文档", sh),
		mine("lark-im", "飞书即时通讯：收发消息和管理群聊", sh),
		mine("lark-meeting", "飞书视频会议：查询会议记录与纪要、妙记", sh),
		mine("lark-minutes", "仅当显式指定 lark-minutes 时使用，相关请求统一交由 lark-meeting 处理", sh),
		mine("lark-vc", "仅当显式指定 lark-vc 时使用，相关请求统一交由 lark-meeting 处理", sh),

		builtin("skill-creator", "新建、修改技能并评估触发效果", cl),
		plugin(builtin("pdf", "读取、拆分与合并 PDF 文件", cl), "document-skills"),
		plugin(builtin("docx", "创建和编辑 Word 文档", cl), "document-skills"),
		plugin(builtin("xlsx", "读写表格、公式与图表", cl), "document-skills"),
		builtin("memory", "按日期记录工作笔记与上下文", wb),
		builtin("cloud-deploy", "一键部署到云开发环境", cb),

		project("release-checklist", "发版前的检查项与回滚步骤", "storefront", cl),
		project("api-conventions", "本项目的接口命名与分页约定", "storefront"),
		project("db-migration", "数据库迁移脚本的写法与校验", "admin-console"),
		project("e2e-testing", "端到端测试的启动与数据准备", "admin-console"),
	}}

	for _, s := range l.Skills {
		switch {
		case s.Source == Mine && s.Shared:
			// 通用目录里的：Codex、Cursor 直接读到
			s.Dir = filepath.Join(l.Root, s.Name)
			s.Via = map[string]string{cx: l.Root, cu: l.Root}
			s.Enabled = append([]string{cx, cu}, s.Enabled...)
			slices.SortFunc(s.Enabled, func(a, b string) int { return l.catalogIndex(a) - l.catalogIndex(b) })
		case s.Source == Mine && len(s.Enabled) > 0:
			a, _ := l.agentByID(s.Enabled[0])
			s.Dir = filepath.Join("~", a.Skills, s.Name)
		case s.Source == Mine:
			s.Dir = filepath.Join("~/Skills", s.Name)
		case s.Source == Builtin:
			s.Dir = filepath.Join("~/."+s.Provider, "skills", s.Name)
		case s.Source == ViaPlugin:
			s.Dir = filepath.Join("~/."+s.Provider, "plugins/cache", s.Plugin, "skills", s.Name)
		case s.Source == Project:
			s.Dir = filepath.Join("~/code", s.Project, ".claude/skills", s.Name)
		}
		s.ID = s.Source.Label() + ":" + s.Name
		s.Files = sampleFiles(s)
		if s.Source == Project {
			s.ProjectDir = filepath.Join("~/code", s.Project)
			p := l.Project(s.ProjectDir)
			if p == nil {
				p = &ProjectInfo{Name: s.Project, Dir: s.ProjectDir}
				l.Projects = append(l.Projects, p)
			}
			p.Skills = append(p.Skills, s)
			if len(p.Dirs) == 0 {
				p.Dirs = []ProjectDir{{Rel: ".claude/skills"}}
			}
			p.Dirs[0].Skills = append(p.Dirs[0].Skills, s)
		}
		if s.Description == "" {
			// 与真实扫描一致：简介取自 SKILL.md 的 frontmatter
			s.Description = ParseSkillMD(s.Files[0].Content).Description
		}
	}
	for _, s := range l.Skills {
		s.Defects = sampleDefects(l, s)
	}
	if strings.HasPrefix(i18n.Locale(), "en") {
		// 英文界面（截图、README）里卡片和详情栏显示英文简介；正文仍是中文
		for _, s := range l.Skills {
			if d, ok := sampleEnglish[s.Name]; ok {
				s.Description = d
			}
		}
	}
	for _, id := range []string{cl, cx, cu, "gemini", "opencode", cb, wb} {
		a, _ := l.agentByID(id)
		l.Agents = append(l.Agents, a)
	}
	l.readers = map[string][]string{l.Root: {cx, cu}}
	l.Settings = DefaultSettings()
	l.Discovery = Discovery{Sources: []DiscoverySource{
		{"Claude Code", cl, "~/.claude.json", 77, ""},
		{"VS Code 系编辑器", "vscode", "…/User/workspaceStorage", 189, "Code、Cursor"},
		{"Codex", cx, "~/.codex/sessions", 6, ""},
	}, Total: 212, WithSkills: 1, Took: 100 * time.Millisecond}
	if p := l.Project("~/code/admin-console"); p != nil {
		p.Manual = true
		l.manual = append(l.manual, p)
	}
	l.manual = append(l.manual, &ProjectInfo{Name: "v3-waterfall", Dir: "~/code/v3-waterfall", Manual: true})
	// 技能集合：npx skills 从同一个来源装的
	lock := map[string]installed{}
	for src, names := range map[string][]string{
		"open.feishu.cn":    {"lark-calendar", "lark-doc", "lark-im", "lark-meeting", "lark-minutes", "lark-vc"},
		"anthropics/skills": {"doc-coauthoring", "web-artifacts-builder", "frontend-design"},
		"DBAAZzz/skills":    {"code-comments", "code-ablation", "pre-mortem"},
	} {
		at := map[string]time.Time{
			"open.feishu.cn":    time.Date(2026, 9, 21, 10, 55, 0, 0, time.Local),
			"anthropics/skills": time.Date(2026, 1, 25, 17, 14, 0, 0, time.Local),
			"DBAAZzz/skills":    time.Date(2026, 6, 14, 22, 28, 0, 0, time.Local),
		}[src]
		for _, n := range names {
			lock[n] = installed{src, at}
		}
	}
	l.collect(lock)
	l.records = sampleRecords(lock)
	l.markSameNames()
	sampleProblems(l)
	sampleNativeStates(l)
	sampleUsage(l)
	return l
}

// sampleEnglish 是示例技能的英文简介，界面是英文时替换中文简介。
// doc-coauthoring 故意没有简介（整理页「没有简介」的示例），不在这里。
var sampleEnglish = map[string]string{
	"test-first":            "Write behavior tests first, watch them fail, then make them pass",
	"vue-best-practices":    "Vue component structure, state management and Composition API conventions",
	"code-review":           "Review changes against a checklist, correctness first",
	"code-comments":         "Decide who a comment is for before writing it: callers or maintainers",
	"systematic-debugging":  "Reproduce, locate, then fix. No guessing",
	"interface-craft":       "How to judge interface design, with a review checklist",
	"kami":                  "Typeset documents into printable PDFs and slides",
	"git-flow":              "Branching and release conventions",
	"context-compression":   "Explain findings to someone who hasn't read the code",
	"brainstorming":         "Pin down the requirements before you start",
	"api-design":            "REST endpoint naming and error code conventions",
	"tailwind-rules":        "Tailwind class ordering and extraction rules",
	"miniprogram-guide":     "Mini program folder layout and subpackage rules",
	"wechat-pay":            "WeChat Pay integration and callback verification",
	"i18n-copy":             "Bilingual UI copy and a terminology glossary",
	"sql-review":            "Slow query and index review checklist",
	"log-analysis":          "Find errors and slow requests in logs",
	"notes-sync":            "Back up notes to a remote server",
	"incident-review":       "Walk through an outage timeline and write the postmortem",
	"dataviz":               "Chart color and layout guidelines",
	"spark":                 "Turn an idea into a requirements doc through conversation",
	"design-lab":            "Generate five interface variations to compare",
	"web-artifacts-builder": "Build multi-component HTML artifacts",
	"frontend-design":       "Distinctive frontend interfaces, not generic ones",
	"code-ablation":         "Remove code piece by piece to find what actually matters",
	"pre-mortem":            "Assume the project already failed and work out why",
	"lark-calendar":         "Lark Calendar: view, search and create events",
	"lark-doc":              "Lark Docs: read, create and edit documents",
	"lark-im":               "Lark Messenger: send messages and manage group chats",
	"lark-meeting":          "Lark Meetings: find recordings, notes and minutes",
	"lark-minutes":          "Only when lark-minutes is named explicitly; otherwise lark-meeting handles it",
	"lark-vc":               "Only when lark-vc is named explicitly; otherwise lark-meeting handles it",
	"skill-creator":         "Create and edit skills, and evaluate how they trigger",
	"pdf":                   "Read, split and merge PDF files",
	"docx":                  "Create and edit Word documents",
	"xlsx":                  "Read and write spreadsheets, formulas and charts",
	"memory":                "Keep dated work notes and context",
	"cloud-deploy":          "Deploy to a cloud dev environment in one step",
	"release-checklist":     "Pre-release checks and rollback steps",
	"api-conventions":       "This project's API naming and pagination rules",
	"db-migration":          "How to write and verify database migrations",
	"e2e-testing":           "Starting end-to-end tests and preparing test data",
}

// sampleProblems 是整理页的示例：同名副本（test-first 有两种内容，
// brainstorming 内容都一样）、重复的链接、断掉的链接、被全局链接的项目技能。
func sampleProblems(l *Library) {
	at := func(dir, name, owner string, readers ...string) Copy {
		p := filepath.Join(dir, name)
		return Copy{Dir: dir, Path: p, Real: p, Owner: owner, Readers: readers}
	}
	day := func(m, d int) time.Time { return time.Date(2026, time.Month(m), d, 10, 0, 0, 0, time.Local) }

	tf := l.Get("test-first")
	older := slices.Clone(tf.Files)
	md := older[0].Content
	md = strings.Replace(md, "，先见红再见绿。", "，先见红再见绿，规则能 lint 的都 lint。", 1)
	md = strings.Replace(md, "- 按改动范围运行对应级别的测试，不每次都跑全量，见 `references/test-levels.md`。\n", "- 每次都跑全量测试。\n- 能 lint 的规则都交给 lint。\n", 1)
	older[0].Content, older[0].Size = md, len(md)
	older = slices.DeleteFunc(older, func(f File) bool { return f.Path == "references/test-levels.md" })
	tf.versions = []Version{
		{Places: []Copy{at(l.Root, tf.Name, "", cx, cu), at("~/.claude/skills", tf.Name, cl, cl, cu), at("~/.agent/skills", tf.Name, "")},
			Real: filepath.Join(l.Root, tf.Name), Shared: true, Modified: day(9, 17), files: tf.Files, hash: "a"},
		{Places: []Copy{at("~/.codebuddy/skills", tf.Name, cb, cb)},
			Real: filepath.Join("~/.codebuddy/skills", tf.Name), Modified: day(8, 2), files: older, hash: "b"},
	}
	tf.Copies, tf.copied = 3, []string{cl, cb}
	tf.redundant, tf.Links = []string{cu}, map[string]string{cu: filepath.Join("~/.cursor/skills", tf.Name)}

	bs := l.Get("brainstorming")
	bs.versions = []Version{{
		Places: []Copy{at(l.Root, bs.Name, "", cx, cu), at("~/.claude/skills", bs.Name, cl, cl, cu), at("~/.codebuddy/skills", bs.Name, cb, cb)},
		Real:   filepath.Join(l.Root, bs.Name), Shared: true, Modified: day(10, 8), files: bs.Files, hash: "c",
	}}
	bs.Copies, bs.copied = 2, []string{cl, cb}
	for _, s := range []*Skill{tf, bs} {
		s.Enabled = slices.DeleteFunc(s.Enabled, func(id string) bool { return s.CopyIn(id) })
	}

	l.Broken = []string{"~/.claude/skills/lark-doc", "~/.codebuddy/skills/old-notes"}
	if rc := l.Get("release-checklist"); rc != nil {
		rc.Dir = filepath.Join(rc.ProjectDir, sharedDir, rc.Name)
		rc.Real = rc.Dir
		rc.globalLinks = []string{"~/.claude/skills/release-checklist"}
		rc.Reads = []Read{
			{cl, ".claude/skills", false},
			{cx, sharedDir, false},
			{"gemini", sharedDir, false},
			{cu, sharedDir, false},
			{cl, "~/.claude/skills", true},
		}
		if sp := l.Project(rc.ProjectDir); sp != nil {
			api := l.Get("api-conventions")
			if api != nil {
				api.Dir = filepath.Join(api.ProjectDir, sharedDir, api.Name)
				api.Real = api.Dir
			}
			sp.Dirs = []ProjectDir{
				{Rel: sharedDir, Skills: []*Skill{rc, api}},
				{Rel: ".claude/skills", Skills: []*Skill{rc}},
			}
		}
	}
}

// sampleFiles 读出 sample/<name> 下的文件，SKILL.md 排第一；没有目录时
// 生成一份只有 frontmatter 和简介的 SKILL.md。
func sampleFiles(s *Skill) []File {
	var files []File
	root := "sample/" + s.Name
	fs.WalkDir(sampleFS, root, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		b, _ := sampleFS.ReadFile(p)
		f := File{Path: strings.TrimPrefix(p, root+"/"), Size: len(b), Content: string(b)}
		if f.Path == "SKILL.md" {
			files = append([]File{f}, files...)
		} else {
			files = append(files, f)
		}
		return nil
	})
	if len(files) > 0 {
		return files
	}
	var md string
	if s.Description == "" {
		md = fmt.Sprintf("---\nname: %s\n---\n\n# %s\n\n（还没有写简介和正文。）\n", s.Name, s.Name)
	} else {
		md = fmt.Sprintf("---\nname: %s\ndescription: %s\n---\n\n# %s\n\n%s\n\n## 何时使用\n\n示例技能，正文待补充。\n", s.Name, s.Description, s.Name, s.Description)
	}
	return []File{{Path: "SKILL.md", Size: len(md), Content: md}}
}

// sampleNativeStates previews the complete state vocabulary without touching disk.
func sampleNativeStates(l *Library) {
	for _, s := range l.Skills {
		if s.Source != Mine {
			continue
		}
		s.Available = slices.Clone(s.Enabled)
		for _, id := range s.copied {
			if !slices.Contains(s.Available, id) {
				s.Available = append(s.Available, id)
			}
		}
		slices.SortFunc(s.Available, func(a, b string) int { return l.catalogIndex(a) - l.catalogIndex(b) })
		s.Enabled = slices.Clone(s.Available)
		s.States = map[string]AgentState{}
		for _, id := range s.Available {
			st := AgentState{Discoverable: true, Enabled: true, Kind: StateEnabled}
			if !nativeSupported(id) {
				st.Kind = StateUnsupported
			}
			s.States[id] = st
		}
	}
	set := func(name, id string, st AgentState) {
		s := l.Get(name)
		if !slices.Contains(s.Available, id) {
			s.Available = append(s.Available, id)
		}
		st.Discoverable = true
		s.States[id] = st
		if !st.Enabled {
			s.Enabled = slices.DeleteFunc(s.Enabled, func(v string) bool { return v == id })
		}
	}
	set("test-first", "gemini", AgentState{Kind: StateRestart, Message: "可在会话中执行 /skills reload 立即生效。storefront 里仍停用。", Projects: []string{"storefront"}})
	set("test-first", "opencode", AgentState{Kind: StateUnknown, Message: "无法读取 OpenCode 配置，请检查格式后重试。"})
	set("git-flow", cl, AgentState{Kind: StateDisabled})
	set("code-comments", cx, AgentState{Kind: StateRestart, Enabled: true})
	set("notes-sync", cl, AgentState{Kind: StateFailed, Enabled: true, Message: "配置文件没有写入权限，未改动。", RetryOn: false})
}

// sampleRecords 是示例的安装记录：集合里的技能，加上已经删掉、记录还在的
// dbskill 32 个和飞书 2 个（整理页「已删除技能的安装记录」）。
func sampleRecords(lock map[string]installed) *InstallRecords {
	r := &InstallRecords{Version: lockVersion}
	add := func(name, source string, at time.Time) {
		typ := "github"
		if !strings.Contains(source, "/") {
			typ = "well-known"
		}
		r.Entries = append(r.Entries, LockEntry{Name: name, Source: source, SourceType: typ, InstalledAt: at})
	}
	names := slices.Sorted(maps.Keys(lock))
	for _, n := range names {
		add(n, lock[n].Source, lock[n].At)
	}
	db := time.Date(2026, 8, 20, 9, 0, 0, 0, time.Local)
	for _, n := range []string{"dbs", "dbs-action", "dbs-ai-check"} {
		add(n, "dontbesilent2025/dbskill", db)
	}
	for i := 4; i <= 32; i++ {
		add(fmt.Sprintf("dbs-%02d", i), "dontbesilent2025/dbskill", db)
	}
	feishu := time.Date(2026, 9, 12, 9, 0, 0, 0, time.Local)
	add("lark-mail", "open.feishu.cn", feishu)
	add("lark-okr", "open.feishu.cn", feishu)
	return r
}

// sampleDefects 和真实扫描一样查 SKILL.md，文件按示例里的列表找。
func sampleDefects(l *Library, s *Skill) []Defect {
	exists := func(rel string) bool {
		return slices.ContainsFunc(s.Files, func(f File) bool { return f.Path == rel || strings.HasPrefix(f.Path, rel+"/") })
	}
	sibling := func(name string) bool { return l.Get(name) != nil }
	return textDefects(s.Files[0].Content, exists, sibling)
}
