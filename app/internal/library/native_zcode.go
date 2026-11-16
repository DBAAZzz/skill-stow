package library

import (
	"encoding/json"
	"errors"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"

	"skillmanager/locales"
)

// ZCode 按 SKILL.md 的路径记停用：skills["/abs/SKILL.md"] = {"enable": false}，
// 开启就删掉那一项（和 ZCode 桌面端的写法一致）。路径用解析软链后的真实路径；
// ZCode 比对时键和技能路径都会再解析一次，所以写成软链路径的键也算数。
//
// 配置分两层：用户的 ~/.zcode/cli/config.json；项目里从项目目录往上到 git
// 仓库根，每层的 zcode.json 与 .zcode/config.json，离项目近的后读。同一个键后读
// 的整项覆盖先读的；旧写法 skill（单数）同一文件里会被 skills 覆盖。总开关
// features.skill 与 skills.enabled 只认用户配置。
//
// ZCode 用 JSON.parse 读，再按 schema 校验整份配置，任何一处不合法整份都不生效。
// 这里只校验和技能有关的部分：严格 JSON、根是对象、features 下各开关、skills 下
// 的运行设置与每个路径项、skill 下的每一项；不合法就报「未知」并且不写。其他
// 部分（mcp、plugins、hooks 等）不校验：它们写错时 ZCode 会整份忽略，这里看不出来。
const zcodeUserConfig = ".zcode/cli/config.json"

// zcodeLayer 是一份 ZCode 配置里和技能开关有关的部分。
type zcodeLayer struct {
	file string
	// entries 是写了的键：值为 nil 是这一项没写 enable，不算停用
	entries map[string]*bool
	off     bool // 总开关关了全部技能（只在用户配置里生效）
	err     error
}

func zcodeLayerOf(c *nativeConfig, user bool) zcodeLayer {
	z := zcodeLayer{file: c.file, entries: map[string]*bool{}}
	// 读到了内容却不是严格 JSON（含注释、末尾逗号）：ZCode 整份不读
	if c.src != "" && !json.Valid([]byte(c.src)) {
		z.err = errors.New(locales.NativeZCodeInvalidJson())
		return z
	}
	if z.err = c.err; z.err != nil {
		return z
	}
	invalid := func(path string) error { return errors.New(locales.NativeZCodeInvalidEntry(path)) }
	boolean := func(n *jsonNode) (bool, bool) { return n.kind == 't', n.kind == 't' || n.kind == 'f' }
	// entry 读一项 {"enable": bool}；enable 可以不写
	entry := func(root, key string, n *jsonNode) error {
		if n.kind != '{' {
			return invalid(root + "." + key)
		}
		var enable *bool
		for _, m := range n.members {
			if m.key != "enable" {
				continue
			}
			v, ok := boolean(m.value)
			if !ok {
				return invalid(root + "." + key + ".enable")
			}
			enable = &v
		}
		if filepath.IsAbs(key) {
			z.entries[key] = enable
		}
		return nil
	}
	if user {
		features, _ := c.doc.at([]string{"features"})
		if features != nil && features.kind != '{' {
			z.err = invalid("features")
			return z
		}
		if features != nil {
			// features 下都是可选的布尔开关，不认识的键 ZCode 会丢掉
			for _, m := range features.members {
				if !slices.Contains([]string{"compact", "rewind", "subagent", "memory", "skill", "mcp"}, m.key) {
					continue
				}
				on, ok := boolean(m.value)
				if !ok {
					z.err = invalid("features." + m.key)
					return z
				}
				if m.key == "skill" {
					z.off = !on
				}
			}
		}
	}
	// 用户配置的 skills 里还放运行设置，类型照 ZCode 的 schema；项目配置只留路径键
	runtime := func(key string, n *jsonNode) error {
		switch key {
		case "enabled", "includeInstructions":
			on, ok := boolean(n)
			if !ok {
				return invalid("skills." + key)
			}
			if key == "enabled" {
				z.off = z.off || !on
			}
		case "metadataBudget":
			var v float64
			if json.Unmarshal([]byte(c.doc.clean[n.start:n.end]), &v) != nil || v <= 0 {
				return invalid("skills.metadataBudget")
			}
		case "roots":
			if n.kind != '[' || slices.ContainsFunc(n.items, func(item *jsonNode) bool { return item.kind != '"' }) {
				return invalid("skills.roots")
			}
		}
		return nil
	}
	// 单数在前、复数在后：同一个键复数的覆盖单数的
	for _, root := range []string{"skill", "skills"} {
		n, _ := c.doc.at([]string{root})
		if n == nil {
			continue
		}
		if n.kind != '{' {
			z.err = invalid(root)
			return z
		}
		for _, m := range n.members {
			if root == "skills" && !filepath.IsAbs(m.key) {
				if !user {
					continue
				}
				if slices.Contains([]string{"enabled", "includeInstructions", "metadataBudget", "roots"}, m.key) {
					if z.err = runtime(m.key, m.value); z.err != nil {
						return z
					}
					continue
				}
			}
			if z.err = entry(root, m.key, m.value); z.err != nil {
				return z
			}
		}
	}
	return z
}

// zcodeDisabled 报告叠起来的几层配置是否停用了 path（真实路径）。
func zcodeDisabled(path string, layers ...zcodeLayer) bool {
	final := map[string]*bool{}
	for _, z := range layers {
		maps.Copy(final, z.entries)
	}
	for key, enable := range final {
		if enable != nil && !*enable && zcodeKeyMatches(key, path) {
			return true
		}
	}
	return false
}

func zcodeKeyMatches(key, path string) bool {
	return filepath.Clean(key) == path || canonicalSkillPath(key) == path
}

// zcodeProjectChain 是项目要叠配置的目录：从项目目录往上找 git 仓库根，找不到
// 只算项目目录自己；仓库根在前、项目目录在后。
func zcodeProjectChain(dir string) []string {
	dirs := []string{dir}
	for d := dir; ; {
		if _, err := os.Stat(filepath.Join(d, ".git")); err == nil {
			break
		}
		parent := filepath.Dir(d)
		if parent == d {
			dirs = []string{dir}
			break
		}
		d = parent
		dirs = append(dirs, d)
	}
	slices.Reverse(dirs)
	return dirs
}

// zcodeProjectFiles 是每层要读的项目配置，先 zcode.json 后 .zcode/config.json。
var zcodeProjectFiles = []string{"zcode.json", ".zcode/config.json"}

// zcodeProjectLayers 读这些目录里的项目配置。只有确实不存在才跳过：读不了
// （权限、IO 错误）的作为带 err 的一层留下，不能当成没有。
func zcodeProjectLayers(chain []string) []zcodeLayer {
	var out []zcodeLayer
	for _, d := range chain {
		for _, rel := range zcodeProjectFiles {
			file := filepath.Join(d, rel)
			if _, err := os.Stat(file); errors.Is(err, fs.ErrNotExist) || errors.Is(err, syscall.ENOTDIR) {
				continue
			} else if err != nil {
				out = append(out, zcodeLayer{file: file, err: err})
				continue
			}
			out = append(out, zcodeLayerOf(readNativeConfig(file, false), false))
		}
	}
	return out
}

// zcodeState 给已算好全局开关的状态补上总开关和各项目里的差别。
func (l *Library) zcodeState(st *AgentState, c *nativeConfig, path string) {
	user := zcodeLayerOf(c, true)
	if user.off {
		st.Kind, st.Enabled, st.Blocked = StateDisabled, false, true
		st.Message = joinSentences(st.Message, locales.NativeZCodeSkillsOff())
		return
	}
	var on []string
	for _, dir := range slices.Sorted(maps.Keys(l.zcodeProjects)) {
		layers := l.zcodeProjects[dir]
		name := filepath.Base(dir)
		if i := slices.IndexFunc(layers, func(z zcodeLayer) bool { return z.err != nil }); i >= 0 {
			st.Message = joinSentences(st.Message, locales.AgentStateProjectUnreadable(name))
			continue
		}
		inProject := !zcodeDisabled(path, append([]zcodeLayer{user}, layers...)...)
		switch {
		case st.Enabled && !inProject:
			st.Projects = append(st.Projects, name)
		case !st.Enabled && inProject:
			on = append(on, name)
		}
	}
	if len(st.Projects) > 0 {
		st.Message = joinSentences(st.Message, locales.AgentStateStillDisabledIn(strings.Join(st.Projects, locales.ListSeparator())))
	}
	if len(on) > 0 {
		st.Message = joinSentences(st.Message, locales.AgentStateStillEnabledIn(strings.Join(on, locales.ListSeparator())))
	}
}

// ProjectAgentState 是项目技能在 Agent 里实际是否启用：读得到不等于启用。
// 只有能读出这个状态的 Agent（目前是 ZCode）返回 true；项目技能在这里不提供开关。
func (l *Library) ProjectAgentState(s *Skill, agent string) (AgentState, bool) {
	if agent != "zcode" || !l.disk || s.Source != Project ||
		!slices.ContainsFunc(s.Reads, func(r Read) bool { return r.Agent == agent }) {
		return AgentState{}, false
	}
	c := l.native[agent]
	if c == nil {
		c = l.readAgentConfig(agent)
	}
	user := zcodeLayerOf(c, true)
	if user.err != nil {
		return AgentState{Kind: StateUnknown, Discoverable: true, Message: user.err.Error()}, true
	}
	if user.off {
		return AgentState{Kind: StateDisabled, Discoverable: true, Blocked: true, Message: locales.NativeZCodeSkillsOff()}, true
	}
	layers := []zcodeLayer{user}
	for _, z := range l.zcodeProjects[s.ProjectDir] {
		// 项目配置读不懂就说不清在这个项目里是否启用：如实报未知
		if z.err != nil {
			msg := joinSentences(locales.AgentStateProjectUnreadable(s.Project), z.err.Error())
			return AgentState{Kind: StateUnknown, Discoverable: true, Message: msg}, true
		}
		layers = append(layers, z)
	}
	if zcodeDisabled(canonicalSkillPath(filepath.Join(s.Real, "SKILL.md")), layers...) {
		return AgentState{Kind: StateDisabled, Discoverable: true}, true
	}
	return AgentState{Kind: StateEnabled, Discoverable: true, Enabled: true}, true
}

// editZCode 改用户配置：停用写 skills[path].enable=false；开启删掉所有停用着
// 它的项，只剩 enable 的整项删，还有别的字段的只删 enable。
func (l *Library) editZCode(c *nativeConfig, path string, on bool) (string, error) {
	if !on {
		target := []string{"skills", path, "enable"}
		parents, err := missingJSONParents(c.doc, target)
		if err != nil {
			return c.src, err
		}
		l.nativeData.Created[c.file] = append(l.nativeData.Created[c.file], parents...)
		v := "false"
		return editJSON(c.src, target, &v)
	}
	out := c.src
	final := map[string]*bool{}
	maps.Copy(final, zcodeLayerOf(c, true).entries)
	for key, enable := range final {
		if enable == nil || *enable || !zcodeKeyMatches(key, path) {
			continue
		}
		for _, root := range []string{"skills", "skill"} {
			doc, err := parseJSON(out)
			if err != nil {
				return c.src, err
			}
			n, _ := doc.at([]string{root, key})
			v, _ := doc.at([]string{root, key, "enable"})
			if v == nil || v.kind != 'f' {
				continue
			}
			remove := []string{root, key, "enable"}
			if len(n.members) == 1 {
				remove = remove[:2]
			}
			if out, err = editJSON(out, remove, nil); err != nil {
				return c.src, err
			}
		}
	}
	out, err := pruneJSON(out, l.nativeData.Created[c.file])
	if err != nil {
		return c.src, err
	}
	doc, _ := parseJSON(out)
	l.nativeData.Created[c.file] = slices.DeleteFunc(l.nativeData.Created[c.file], func(p []string) bool { n, _ := doc.at(p); return n == nil })
	return out, nil
}
