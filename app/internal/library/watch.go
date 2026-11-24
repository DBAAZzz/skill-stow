package library

import (
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// WatchMaxTargets 限制单个实例建立的文件监听总数，防止极端项目或大量软链占用过多资源。
const WatchMaxTargets = 256

// WatchPlan 描述需要监听的磁盘路径：Dirs 使用 HintsOnly 递归监听，
// Files 使用单文件监听。
//
// 为避免 Claude Code / Codex 会话每轮对话都频繁改写日志造成持续重扫，
// 刻意不监听 ~/.claude.json 和 ~/.codex/sessions；新增项目与首次生成的配置
// 由应用重新激活（OnDidBecomeActive）时的低频兜底触发发现。
type WatchPlan struct {
	Dirs  []string
	Files []string
	// Creates 是还不存在、出现时要重扫的配置文件：在最近一个存在的上级目录上
	// 不递归地看，只认 Names 里的直接子项。文件出现后重扫，计划随之改成单文件监听；
	// 单文件被删掉时监听会结束并重扫，又回到这里。
	Creates   []WatchCreate
	Truncated bool
}

// WatchCreate 是一个不递归监听的目录，以及其中要等它出现的子项名。
type WatchCreate struct {
	Dir   string
	Names []string
}

// WatchPlan 返回当前技能库状态下需要监听的目录与配置文件清单。
// 插件缓存由 installed_plugins.json 统一触发，外部软链只追踪 Mine 与 Project 技能。
func (l *Library) WatchPlan() WatchPlan {
	return l.watchPlanWithLimit(WatchMaxTargets)
}

func (l *Library) watchPlanWithLimit(limit int) WatchPlan {
	var plan WatchPlan
	if l == nil || l.Home == "" || limit <= 0 {
		return plan
	}

	seenDirReal := map[string]bool{}
	seenFile := map[string]bool{}
	var resolvedDirs []string

	atLimit := func() bool {
		return len(plan.Dirs)+len(plan.Files)+len(plan.Creates) >= limit
	}

	addDir := func(p string) bool {
		if p == "" {
			return false
		}
		p = filepath.Clean(p)
		fi, err := os.Stat(p)
		if err != nil || !fi.IsDir() {
			return false
		}
		realPath := p
		if r, err := filepath.EvalSymlinks(p); err == nil {
			realPath = filepath.Clean(r)
		}
		// 许多 Agent（如 ~/.claude/skills -> ~/.agents/skills）指向同一真实目录，按真实路径去重以免重复建流。
		if seenDirReal[realPath] {
			return true
		}
		if atLimit() {
			plan.Truncated = true
			return false
		}
		seenDirReal[realPath] = true
		plan.Dirs = append(plan.Dirs, realPath)
		resolvedDirs = append(resolvedDirs, realPath)
		return true
	}

	addFile := func(p string) {
		if p == "" {
			return
		}
		p = filepath.Clean(p)
		if seenFile[p] {
			return
		}
		fi, err := os.Lstat(p)
		if err != nil || fi.IsDir() {
			return
		}
		if atLimit() {
			plan.Truncated = true
			return
		}
		seenFile[p] = true
		plan.Files = append(plan.Files, p)
	}

	creates := map[string][]string{}
	// addMissing 监听 file：存在就单文件监听，不存在就等它（或它缺的上级目录）出现
	addMissing := func(file string) {
		if _, err := os.Lstat(file); err == nil {
			addFile(file)
			return
		}
		dir, name := filepath.Dir(file), filepath.Base(file)
		for {
			if fi, err := os.Stat(dir); err == nil && fi.IsDir() {
				break
			}
			parent := filepath.Dir(dir)
			if parent == dir {
				return
			}
			dir, name = parent, filepath.Base(dir)
		}
		if slices.Contains(creates[dir], name) {
			return
		}
		if _, ok := creates[dir]; !ok {
			if atLimit() {
				plan.Truncated = true
				return
			}
			plan.Creates = append(plan.Creates, WatchCreate{Dir: dir})
		}
		creates[dir] = append(creates[dir], name)
	}
	zcode := slices.ContainsFunc(l.Agents, func(a Agent) bool { return a.ID == "zcode" })

	homePath := func(relOrAbs string) string {
		if relOrAbs == "" {
			return ""
		}
		if filepath.IsAbs(relOrAbs) {
			return relOrAbs
		}
		return filepath.Join(l.Home, relOrAbs)
	}

	// 先纳入关键单文件（原生配置、插件清单、安装记录），确保不受目录数量截断影响。
	for _, a := range l.Agents {
		if a.Plugins != "" {
			pluginDir := homePath(a.Plugins)
			manifest := filepath.Join(pluginDir, "installed_plugins.json")
			if _, err := os.Lstat(manifest); err == nil {
				addFile(manifest)
			} else {
				// 首次装插件前清单尚未创建，先监听插件目录捕获首次生成；待清单出现并重扫后即收敛为单文件监听。
				addDir(pluginDir)
			}
		}
		if a.ID == codex {
			// Codex 从远程目录装插件只在缓存里新建 市场/名称 目录，不改 config.toml
			markets, _ := filepath.Glob(filepath.Join(homePath(a.Home), codexPluginCache, "*"))
			for _, m := range markets {
				addDir(m)
			}
		}
	}
	addFile(filepath.Join(l.Home, ".agents", ".skill-lock.json"))
	for _, rel := range []string{
		".claude/settings.json",
		".codex/config.toml",
		".gemini/settings.json",
		".config/opencode/opencode.json",
		".config/opencode/opencode.jsonc",
	} {
		addFile(filepath.Join(l.Home, rel))
	}
	// ZCode 在里面拨第一个开关时才建配置：没有也要等它出现
	if zcode {
		addMissing(filepath.Join(l.Home, zcodeUserConfig))
	}
	for _, p := range l.Projects {
		addFile(filepath.Join(p.Dir, ".gemini", "settings.json"))
	}

	// 再纳入技能库、各 Agent 目录与项目技能目录。
	addDir(l.Root)
	for _, a := range l.Agents {
		addDir(homePath(a.Skills))
		for _, r := range a.Reads {
			addDir(homePath(r))
		}
		for _, b := range a.Builtin {
			addDir(homePath(b))
		}
	}
	for _, d := range l.Settings.AgentDirs {
		addDir(d)
	}
	for _, p := range l.Projects {
		for _, d := range p.Dirs {
			if !addDir(filepath.Join(p.Dir, d.Rel)) && plan.Truncated {
				break
			}
		}
		if plan.Truncated {
			break
		}
		for _, rel := range projectSkillDirs {
			if !addDir(filepath.Join(p.Dir, rel)) && plan.Truncated {
				break
			}
		}
		if plan.Truncated {
			break
		}
	}

	// ZCode 的项目配置：项目到仓库根的每层，含还没有技能、还没有配置的项目
	if zcode && !plan.Truncated {
		for _, dir := range slices.Sorted(maps.Keys(l.zcodeChains)) {
			for _, d := range l.zcodeChains[dir] {
				for _, rel := range zcodeProjectFiles {
					addMissing(filepath.Join(d, rel))
				}
			}
		}
	}

	// 外部软链：只追踪用户直接维护的 Mine 与 Project 技能目标目录。
	if !plan.Truncated {
		for _, s := range l.Skills {
			if s.Source != Mine && s.Source != Project {
				continue
			}
			if s.Real == "" || pathUnderAny(s.Real, resolvedDirs) {
				continue
			}
			if !addDir(s.Real) && plan.Truncated {
				break
			}
		}
	}

	for i := range plan.Creates {
		plan.Creates[i].Names = creates[plan.Creates[i].Dir]
		slices.Sort(plan.Creates[i].Names)
	}
	slices.Sort(plan.Dirs)
	slices.Sort(plan.Files)
	slices.SortFunc(plan.Creates, func(a, b WatchCreate) int { return strings.Compare(a.Dir, b.Dir) })
	return plan
}

func pathUnderAny(target string, roots []string) bool {
	target = filepath.Clean(target)
	if realTarget, err := filepath.EvalSymlinks(target); err == nil {
		target = filepath.Clean(realTarget)
	}
	sep := string(filepath.Separator)
	for _, root := range roots {
		if target == root || strings.HasPrefix(target, root+sep) {
			return true
		}
	}
	return false
}
