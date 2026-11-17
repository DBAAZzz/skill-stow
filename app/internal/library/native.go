package library

import (
	"encoding/json"
	"errors"

	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"skillmanager/locales"
)

// StateKind 是 Agent 对一个技能的启用状态；给人看的状态词是 Label。
type StateKind string

const (
	StateEnabled     StateKind = "enabled"
	StateDisabled    StateKind = "disabled"
	StateRestart     StateKind = "restart"
	StateFailed      StateKind = "failed"
	StateUnknown     StateKind = "unknown"
	StateUnsupported StateKind = "unsupported" // 读得到，但这里改不了它的启用设置（没有按技能停用的配置，或还没适配）
)

// Label 是状态词，按界面当前的语言说：已启用、需要重启、始终启用……
func (k StateKind) Label() string {
	switch k {
	case StateEnabled:
		return locales.AgentStateEnabled()
	case StateDisabled:
		return locales.AgentStateDisabled()
	case StateRestart:
		return locales.AgentStateRestart()
	case StateFailed:
		return locales.AgentStateFailed()
	case StateUnsupported:
		return locales.AgentStateAlwaysOn()
	}
	return locales.AgentStateUnknown()
}

type AgentState struct {
	Kind                  StateKind
	Discoverable, Enabled bool
	Message               string   // 状态词（Kind）之外要补充的话，没有就是空：异常的原因、还能怎么做
	Projects              []string // 全局开着、在这些项目里仍停用（Gemini、ZCode 的项目配置）；全局状态另算
	RetryOn               bool
	// Blocked 是 Agent 的总开关把技能全关了：单独拨这一个不会生效
	Blocked bool
}

func (s AgentState) Controllable() bool {
	return s.Discoverable && !s.Blocked && s.Kind != StateUnknown && s.Kind != StateUnsupported
}
func (s *Skill) State(agent string) AgentState {
	if st, ok := s.States[agent]; ok {
		return st
	}
	st := AgentState{Discoverable: slices.Contains(s.DiscoveredAgents(), agent), Enabled: slices.Contains(s.Enabled, agent), Kind: StateEnabled}
	if !st.Enabled {
		st.Kind = StateDisabled
	}
	if !nativeSupported(agent) {
		st.Kind = StateUnsupported
	}
	return st
}
func (s *Skill) DiscoveredIn(agent string) bool { return s.State(agent).Discoverable }
func (s *Skill) DiscoveredAgents() []string {
	if s.Available != nil {
		return s.Available
	}
	return s.Enabled
}
func (s *Skill) AllDisabled() bool {
	ids := s.DiscoveredAgents()
	if len(ids) == 0 {
		return false
	}
	for _, id := range ids {
		st := s.State(id)
		if st.Enabled || st.Kind == StateUnknown {
			return false
		}
	}
	return true
}

// nativeAgents 是能在这里按技能启停的 Agent：都改它们自己的配置文件。
var nativeAgents = []string{"claude-code", "codex", "gemini", "opencode", "zcode"}

func nativeSupported(id string) bool { return slices.Contains(nativeAgents, id) }

// pathKeyed 报告 Agent 按 SKILL.md 的路径而不是名称记停用：不会连带同名技能。
func pathKeyed(id string) bool { return id == "codex" || id == "zcode" }

type nativeConfig struct {
	file, src string
	doc       *jsonDocument
	blocks    []tomlSkill
	err       error
}
type nativeRestore struct {
	Original *string `json:"original"`
}
type nativeData struct {
	Restore map[string]nativeRestore `json:"restore"`
	Created map[string][][]string    `json:"created"`
	Restart map[string]time.Time     `json:"restart"`
	Links   map[string]string        `json:"links"`
}

func emptyNativeData() nativeData {
	return nativeData{map[string]nativeRestore{}, map[string][][]string{}, map[string]time.Time{}, map[string]string{}}
}
func (l *Library) nativeFile() string { return filepath.Join(appDir(l.Home), "native-skills.json") }
func (l *Library) loadNativeData() {
	l.nativeData = emptyNativeData()
	b, err := os.ReadFile(l.nativeFile())
	if errors.Is(err, os.ErrNotExist) {
		return
	}
	if err == nil {
		err = json.Unmarshal(b, &l.nativeData)
	}
	l.nativeErr = err
	if err == nil {
		l.clearRestarts()
	}
}
func (l *Library) saveNativeData() error {
	b, err := json.MarshalIndent(l.nativeData, "", "  ")
	if err != nil {
		return err
	}
	return atomicConfigWrite(l.nativeFile(), string(b))
}
func (l *Library) configPath(id string) string {
	switch id {
	case "claude-code":
		return filepath.Join(l.Home, ".claude/settings.json")
	case "codex":
		return filepath.Join(l.Home, ".codex/config.toml")
	case "gemini":
		return filepath.Join(l.Home, ".gemini/settings.json")
	case "opencode":
		p := filepath.Join(l.Home, ".config/opencode/opencode.jsonc")
		if _, err := os.Lstat(p); err == nil {
			return p
		}
		return filepath.Join(l.Home, ".config/opencode/opencode.json")
	case "zcode":
		return filepath.Join(l.Home, zcodeUserConfig)
	}
	return ""
}
func (l *Library) readAgentConfig(id string) *nativeConfig {
	c := readNativeConfig(l.configPath(id), id == "codex")
	if id == "opencode" && strings.HasSuffix(c.file, ".jsonc") {
		if _, err := os.Stat(filepath.Join(l.Home, ".config/opencode/opencode.json")); err == nil {
			c.err = errors.New(locales.NativeOpenCodeConfigConflict())
		}
	}
	return c
}
func readNativeConfig(file string, toml bool) *nativeConfig {
	c := &nativeConfig{file: file}
	b, err := os.ReadFile(file)
	if errors.Is(err, os.ErrNotExist) { // A broken symlink is not an absent config.
		if _, linkErr := os.Lstat(file); linkErr == nil {
			c.err = err
			return c
		}
		if toml {
			b = []byte("")
		} else {
			b = []byte("{}\n")
		}
		err = nil
	}
	c.src = string(b)
	c.err = err
	if err != nil {
		return c
	}
	if toml {
		c.blocks, c.err = parseTOMLSkills(c.src)
	} else {
		c.doc, c.err = parseJSON(c.src)
	}
	return c
}
func (l *Library) refreshNative() {
	if !l.disk {
		return
	}
	l.loadNativeData()
	l.native = map[string]*nativeConfig{}
	for _, id := range nativeAgents {
		l.native[id] = l.readAgentConfig(id)
	}

	for _, s := range l.Skills {
		if s.Source == Mine {
			l.applyNative(s)
		}
	}
}
func (l *Library) clearRestarts() {
	if len(l.nativeData.Restart) > 0 {
		cmd := exec.Command("ps", "-axo", "lstart=,command=")
		cmd.Env = append(os.Environ(), "LC_ALL=C")
		b, err := cmd.Output()
		if err == nil {
			for key, t := range l.nativeData.Restart {
				id, _, _ := strings.Cut(key, "\x00")
				if !needsRestart(id, t, string(b)) {
					delete(l.nativeData.Restart, key)
				}
			}
		}
	}
}
func needsRestart(id string, written time.Time, processes string) bool {
	for _, line := range strings.Split(processes, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 6 {
			continue
		}
		started, err := time.ParseInLocation("Mon Jan 2 15:04:05 2006", strings.Join(fields[:5], " "), time.Local)
		if err != nil || started.After(written) {
			continue
		}
		args := fields[5:]
		name := filepath.Base(args[0])
		match := name == id
		if id == "claude-code" {
			match = name == "claude"
		}
		if name == "node" && len(args) > 1 {
			match = slices.ContainsFunc(args[1:], func(arg string) bool {
				return filepath.Base(arg) == id ||
					id == "gemini" && strings.Contains(arg, "gemini-cli") ||
					id == "codex" && strings.Contains(arg, "/codex/") ||
					id == "opencode" && strings.Contains(arg, "opencode")
			})
		}
		if match {
			return true
		}
	}
	return false
}
func (s *Skill) nativeTarget(id string) (name, path string) {
	path = s.targets[id]
	if path == "" {
		path = filepath.Join(s.Real, "SKILL.md")
	}
	name = s.nativeNames[id]
	if name == "" && id != "opencode" {
		name = s.Name
	}
	return name, canonicalSkillPath(path)
}
func nativeKey(id, name, path string) string {
	if pathKeyed(id) {
		name = path
	}
	if id == "gemini" {
		name = strings.ToLower(name)
	}
	return id + "\x00" + name
}
func nativeJSONPath(id, name string) []string {
	switch id {
	case "claude-code":
		return []string{"skillOverrides", name}
	case "gemini":
		return []string{"skills", "disabled"}
	default:
		return []string{"permission", "skill", name}
	}
}
func (c *nativeConfig) stringValue(path []string) (string, error) {
	raw, err := c.doc.raw(path)
	if err != nil || raw == nil {
		return "", err
	}
	var s string
	err = json.Unmarshal([]byte(*raw), &s)
	return s, err
}
func (c *nativeConfig) disabledNames() ([]string, error) {
	n, err := c.doc.at([]string{"skills", "disabled"})
	if err != nil || n == nil {
		return nil, err
	}
	if n.kind != '[' {
		return nil, errors.New(locales.NativeDisabledMustBeNameArray())
	}
	var names []string
	for _, v := range n.items {
		var name string
		if json.Unmarshal([]byte(c.doc.clean[v.start:v.end]), &name) != nil {
			return nil, errors.New(locales.NativeDisabledMustBeNameArray())
		}
		names = append(names, name)
	}
	return names, nil
}
func wildcard(pattern, name string) bool {
	expr := "^" + strings.ReplaceAll(strings.ReplaceAll(regexp.QuoteMeta(pattern), `\*`, ".*"), `\?`, ".") + "$"
	ok, _ := regexp.MatchString(expr, name)
	return ok
}
func (c *nativeConfig) openCodeAllowed(name string) (bool, error) {
	if name == "" {
		return false, errors.New(locales.NativeMissingFrontmatterName())
	}
	for _, path := range [][]string{{"permissions"}, {"agents"}} {
		n, err := c.doc.at(path)
		if err != nil {
			return false, err
		}
		if n != nil {
			return false, errors.New(locales.NativeOpenCodeV2Unsupported())
		}
	}
	n, err := c.doc.at([]string{"permission"})
	if err != nil {
		return false, err
	}
	if n == nil {
		return true, nil
	}
	if n.kind != '{' {
		return false, errors.New(locales.NativeUnknownPermissionStructure())
	}
	allowed := true
	for _, m := range n.members {
		if !wildcard(m.key, "skill") {
			continue
		}
		if m.value.kind == '"' {
			v, _ := c.stringValue([]string{"permission", m.key})
			if v != "allow" && v != "ask" && v != "deny" {
				return false, errors.New(locales.NativeUnknownPermissionValue())
			}
			allowed = v != "deny"
			continue
		}
		if m.value.kind != '{' {
			return false, errors.New(locales.NativeUnknownSkillPermissionStructure())
		}
		for _, rule := range m.value.members {
			v, e := c.stringValue([]string{"permission", m.key, rule.key})
			if e != nil || v != "allow" && v != "ask" && v != "deny" {
				return false, errors.New(locales.NativeUnknownPermissionValue())
			}
			if wildcard(rule.key, name) {
				allowed = v != "deny"
			}
		}
	}
	return allowed, nil
}
func (c *nativeConfig) allowed(id, name, path string) (bool, error) {
	if c.err != nil {
		return false, c.err
	}
	switch id {
	case "claude-code":
		v, err := c.stringValue(nativeJSONPath(id, name))
		if err != nil {
			return false, err
		}
		return v != "off", nil
	case "codex":
		allowed := true
		for _, b := range c.blocks {
			if b.path != "" && canonicalSkillPath(b.path) == path || b.name != "" && b.name == name {
				allowed = allowed && b.enabled
			}
		}
		return allowed, nil
	case "gemini":
		names, err := c.disabledNames()
		return !slices.ContainsFunc(names, func(n string) bool { return strings.EqualFold(n, name) }), err
	case "opencode":
		return c.openCodeAllowed(name)
	case "zcode":
		user := zcodeLayerOf(c, true)
		if user.err != nil {
			return false, user.err
		}
		// 总开关另算（见 zcodeState）：这里只看这一个技能的配置，删除时清理自己写的停用项不受它挡
		return !zcodeDisabled(path, user), nil
	}
	return true, nil
}
func (l *Library) applyNative(s *Skill) {
	previous := s.States
	s.States = map[string]AgentState{}
	s.Enabled = nil
	for _, id := range s.Available {
		st := AgentState{Kind: StateEnabled, Discoverable: true, Enabled: true}
		name, path := s.nativeTarget(id)
		key := nativeKey(id, name, path)
		if !nativeSupported(id) {
			st.Kind = StateUnsupported
		} else {
			c := l.native[id]
			enabled, err := c.allowed(id, name, path)
			if l.nativeErr != nil {
				err = l.nativeErr
			}
			st.Enabled = enabled
			if err != nil {
				st.Kind = StateUnknown
				st.Message = err.Error()
			} else {
				if !enabled {
					st.Kind = StateDisabled
				}
				if _, ok := l.nativeData.Restart[key]; ok {
					st.Kind = StateRestart
					if id == "gemini" {
						st.Message = joinSentences(st.Message, locales.NativeReloadSkillsHint())
					}
				}
				if id == "opencode" && !enabled {
					// OpenCode 的停用只对模型生效，这一点从「已停用」看不出来
					st.Message = joinSentences(st.Message, locales.NativeCliStillAvailable())
				}
				if id == "gemini" {
					for dir, project := range l.geminiProjects {
						ok, err := project.allowed(id, name, path)
						if err != nil {
							st.Message = joinSentences(st.Message, locales.AgentStateProjectUnreadable(filepath.Base(dir)))
						} else if !ok {
							st.Projects = append(st.Projects, filepath.Base(dir))
						}
					}
					slices.Sort(st.Projects)
					if len(st.Projects) > 0 {
						st.Message = joinSentences(st.Message, locales.AgentStateStillDisabledIn(strings.Join(st.Projects, locales.ListSeparator())))
					}
				}
				if id == "zcode" {
					l.zcodeState(&st, c, path)
				}
			}
		}
		// 刻意保留上一次操作失败的 StateFailed：setNative 结尾的 defer 会对全部
		// 「我的」技能重跑 applyNative，保留它能避免操作其他技能或其他 Agent 时
		// 抹掉刚发生的失败提示；用户再次拨动或重试该开关（setNative 开头 delete）
		// 或重新 Scan/刷新后才会清掉。
		if failed, ok := previous[id]; ok && failed.Kind == StateFailed {
			st = failed
		}
		s.States[id] = st
		if st.Enabled {
			s.Enabled = append(s.Enabled, id)
		}
	}
}

// atomicConfigWrite follows config symlinks and preserves the target's permissions.
func atomicConfigWrite(file, src string) error {
	mode := os.FileMode(0600)
	if _, err := os.Lstat(file); err == nil {
		if isLink(file) {
			file, err = realPath(file)
			if err != nil {
				return err
			}
		}
		fi, err := os.Stat(file)
		if err != nil {
			return err
		}
		mode = fi.Mode().Perm()
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(file), 0755); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(file), ".skill-manager-*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	if err = f.Chmod(mode); err == nil {
		_, err = f.WriteString(src)
	}
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	return os.Rename(tmp, file)
}

// writeAgentConfig 写 Agent 的原生配置，前后各记一条事件（只记指纹，不记
// 原文）：只有开始没有结束，就是写到一半应用退出了。
func (l *Library) writeAgentConfig(agent, file, before, after string) error {
	id := newEventID()
	l.Log(Event{Level: LevelInfo, Cat: "native", Op: "write.start", ID: id, Target: agent,
		Data: map[string]string{"file": file, "before": digest(before)}})
	err := atomicConfigWrite(file, after)
	end := Event{Level: LevelInfo, Cat: "native", Op: "write.end", ID: id, Target: agent,
		Data: map[string]string{"file": file, "after": digest(after)}}
	if err != nil {
		end.Level, end.Msg = LevelError, err.Error()
		end.Kind, _ = errorKind(err)
	}
	l.Log(end)
	return err
}

func (l *Library) setNative(s *Skill, id string, on bool) error {
	name, path := s.nativeTarget(id)
	key := nativeKey(id, name, path)
	delete(s.States, id)
	defer func() {
		for _, skill := range l.Skills {
			if skill.Source == Mine {
				l.applyNative(skill)
			}
		}
	}()
	c := l.readAgentConfig(id)
	current, err := c.allowed(id, name, path)
	l.native[id] = c
	if err != nil {
		return err
	}
	l.loadNativeData()
	if l.nativeErr != nil {
		return l.nativeErr
	}
	if current == on {
		if on {
			if _, exists := l.nativeData.Restore[key]; exists {
				delete(l.nativeData.Restore, key)
				if err := l.saveNativeData(); err != nil {
					return err
				}
			}
		}
		return nil
	}
	record, restoring := l.nativeData.Restore[key]
	out := c.src
	if id == "codex" {
		if on {
			out = strings.Replace(out, ownedTOMLBlock(path), "", 1)
			if out == c.src {
				return errors.New(locales.NativeDisabledByExistingConfig())
			}
		} else {
			out += ownedTOMLBlock(path)
		}
	} else if id == "zcode" {
		out, err = l.editZCode(c, path, on)
	} else {
		target := nativeJSONPath(id, name)
		original, e := c.doc.raw(target)
		if e != nil {
			return e
		}
		var value *string
		if on {
			if restoring {
				value = record.Original
			}
		} else {
			v := `"off"`
			if id == "opencode" {
				v = `"deny"`
			}
			value = &v
		}
		if !on {
			parents, e := missingJSONParents(c.doc, target)
			if e != nil {
				return e
			}
			l.nativeData.Created[c.file] = append(l.nativeData.Created[c.file], parents...)
			if original == nil {
				l.nativeData.Created[c.file] = append(l.nativeData.Created[c.file], target)
			}
			if !restoring {
				record.Original = original
			}
		}
		if id == "gemini" {
			out, err = editJSONArray(out, target, name, on)
		} else {
			out, err = editJSON(out, target, value)
		}
		if err == nil && on {
			out, err = pruneJSON(out, l.nativeData.Created[c.file])
			if err == nil {
				doc, _ := parseJSON(out)
				l.nativeData.Created[c.file] = slices.DeleteFunc(l.nativeData.Created[c.file], func(p []string) bool { n, _ := doc.at(p); return n == nil })
			}
		}
	}
	if err != nil {
		return err
	}
	// Parse and verify the planned value before committing it (e.g. a later wildcard can override it).
	planned := &nativeConfig{src: out}
	if id == "codex" {
		planned.blocks, planned.err = parseTOMLSkills(out)
	} else {
		planned.doc, planned.err = parseJSON(out)
	}
	result, err := planned.allowed(id, name, path)
	if err != nil {
		return err
	}
	if result != on {
		return errors.New(locales.NativeToggleOverridden())
	}
	if err = l.writeAgentConfig(id, c.file, c.src, out); err != nil {
		return err
	}
	readback := readNativeConfig(c.file, id == "codex")
	l.native[id] = readback
	if result, err = readback.allowed(id, name, path); err != nil {
		return err
	}
	if result != on {
		return errors.New(locales.NativeReadbackMismatch())
	}
	if on {
		delete(l.nativeData.Restore, key)
	} else {
		l.nativeData.Restore[key] = record
	}
	// ZCode 每次列技能都重读配置，和 Claude Code 一样不用重启
	if id != "claude-code" && id != "zcode" {
		l.nativeData.Restart[key] = time.Now()
	}
	return l.saveNativeData()
}
