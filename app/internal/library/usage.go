package library

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"
)

// UsageAgents 是留有调用记录的 Agent。其他 Agent 没有可读的记录：只被
// 它们读到的技能不知道用没用过。
var UsageAgents = []string{"claude-code", "codex"}

// UsageEvent 是会话记录里的一次技能调用。
type UsageEvent struct {
	Agent string    `json:"agent"`
	Name  string    `json:"name"`          // 技能名；Claude Code 的插件技能是「插件:技能」
	Dir   string    `json:"dir,omitempty"` // Codex：读的 SKILL.md 所在目录
	Cwd   string    `json:"cwd,omitempty"` // 当时的工作目录，用来认项目技能
	At    time.Time `json:"at"`
}

// UsageLog 是从会话记录里读出的全部调用。
type UsageLog struct {
	Events   []UsageEvent
	Since    time.Time // 最早的一条会话记录：统计从这时起
	Sessions int
}

// Usage 是一个技能被调用的情况。
type Usage struct {
	Count int
	Last  time.Time
	By    map[string]int // Agent → 次数
}

// usageFile 是一个会话文件里的调用，按大小和修改时间判断要不要重读。
type usageFile struct {
	Size   int64        `json:"size"`
	Mod    int64        `json:"mod"`
	First  time.Time    `json:"first"`
	Events []UsageEvent `json:"events,omitempty"`
}

// 解析方式变了就加一，旧的缓存作废。
const usageVersion = 1

type usageCache struct {
	Version int                   `json:"version"`
	Files   map[string]*usageFile `json:"files"`
}

// ReadUsage 读 Claude Code 和 Codex 的会话记录，找出技能调用。记录有几百
// MB，要放在后台跑；每个文件的结果记在 usage.json 里，没变的文件不重读。
// Agent 会清掉旧会话（Claude Code 默认留 30 天），已经消失的文件的结果
// 留在缓存里，所以统计的时间比 Agent 自己留的记录长。
func ReadUsage(home string) *UsageLog {
	file := filepath.Join(appDir(home), "usage.json")
	var cache usageCache
	if b, err := os.ReadFile(file); err == nil {
		json.Unmarshal(b, &cache)
	}
	if cache.Version != usageVersion || cache.Files == nil {
		cache = usageCache{Version: usageVersion, Files: map[string]*usageFile{}}
	}
	dirty := false
	walk := func(root string, parse func(r *bufio.Reader) (time.Time, []UsageEvent)) {
		filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() || !strings.HasSuffix(p, ".jsonl") {
				return nil
			}
			fi, err := d.Info()
			if err != nil {
				return nil
			}
			if old := cache.Files[p]; old != nil && old.Size == fi.Size() && old.Mod == fi.ModTime().UnixNano() {
				return nil
			}
			f, err := os.Open(p)
			if err != nil {
				return nil
			}
			defer f.Close()
			first, events := parse(bufio.NewReaderSize(f, 1<<20))
			cache.Files[p] = &usageFile{fi.Size(), fi.ModTime().UnixNano(), first, events}
			dirty = true
			return nil
		})
	}
	walk(filepath.Join(home, ".claude", "projects"), parseClaudeUsage)
	walk(filepath.Join(home, ".codex", "sessions"), func(r *bufio.Reader) (time.Time, []UsageEvent) {
		return parseCodexUsage(r, home)
	})
	if dirty {
		writeJSON(file, cache)
	}

	log := &UsageLog{Sessions: len(cache.Files)}
	for _, f := range cache.Files {
		log.Events = append(log.Events, f.Events...)
		if !f.First.IsZero() && (log.Since.IsZero() || f.First.Before(log.Since)) {
			log.Since = f.First
		}
	}
	slices.SortFunc(log.Events, func(a, b UsageEvent) int { return a.At.Compare(b.At) })
	return log
}

// eachLine 逐行读，行可能有几 MB。
func eachLine(r *bufio.Reader, fn func(line []byte)) {
	for {
		line, err := r.ReadBytes('\n')
		if len(line) > 0 {
			fn(line)
		}
		if err != nil {
			return
		}
	}
}

var commandName = regexp.MustCompile(`<command-name>/?([^<\s]+)</command-name>`)

// parseClaudeUsage 读 Claude Code 的一个会话：模型调用技能是 Skill 工具
// 调用，用户手动 /技能名 是一条 <command-name> 消息。手动调用后模型往往
// 再用 Skill 工具载入同一个技能，那一次不重复算。/model 这类内置命令也会
// 记下来，对不上技能，统计时自然丢掉。
func parseClaudeUsage(r *bufio.Reader) (first time.Time, events []UsageEvent) {
	pending := ""
	eachLine(r, func(line []byte) {
		if !first.IsZero() && !bytes.Contains(line, []byte(`"Skill"`)) && !bytes.Contains(line, []byte("<command-name>")) {
			return
		}
		var d struct {
			Type      string
			Timestamp time.Time
			Cwd       string
			Message   struct{ Content json.RawMessage }
		}
		if json.Unmarshal(line, &d) != nil {
			return
		}
		if first.IsZero() {
			first = d.Timestamp
		}
		switch d.Type {
		case "user":
			var text string
			if json.Unmarshal(d.Message.Content, &text) != nil {
				return // 工具结果
			}
			pending = ""
			if !strings.HasPrefix(strings.TrimSpace(text), "<command-") {
				return
			}
			if m := commandName.FindStringSubmatch(text); m != nil {
				pending = m[1]
				events = append(events, UsageEvent{Agent: "claude-code", Name: m[1], Cwd: d.Cwd, At: d.Timestamp})
			}
		case "assistant":
			var blocks []struct {
				Type, Name string
				Input      struct{ Skill string }
			}
			json.Unmarshal(d.Message.Content, &blocks)
			for _, b := range blocks {
				if b.Type != "tool_use" || b.Name != "Skill" || b.Input.Skill == "" {
					continue
				}
				if b.Input.Skill == pending {
					pending = ""
					continue
				}
				events = append(events, UsageEvent{Agent: "claude-code", Name: b.Input.Skill, Cwd: d.Cwd, At: d.Timestamp})
			}
		}
	})
	return first, events
}

var skillMDPath = regexp.MustCompile(`([^\s"'\\=;|&()<>]+)/SKILL\.md`)

// parseCodexUsage 读 Codex 的一个会话。Codex 没有专门的技能调用记录：
// 它用技能时执行命令读那份 SKILL.md，这里按命令里的路径推断，一个会话里
// 同一个技能只算一次。系统提示里列出的技能清单不是工具调用，不算。
func parseCodexUsage(r *bufio.Reader, home string) (first time.Time, events []UsageEvent) {
	cwd := ""
	seen := map[string]bool{}
	eachLine(r, func(line []byte) {
		if !first.IsZero() && !bytes.Contains(line, []byte("SKILL.md")) {
			return
		}
		var d struct {
			Timestamp time.Time
			Type      string
			Payload   json.RawMessage
		}
		if json.Unmarshal(line, &d) != nil {
			return
		}
		var p struct{ Type, Cwd string }
		json.Unmarshal(d.Payload, &p)
		if first.IsZero() {
			first = d.Timestamp
		}
		if d.Type == "session_meta" {
			cwd = p.Cwd
			return
		}
		if d.Type != "response_item" || !strings.HasSuffix(p.Type, "_call") {
			return
		}
		for _, m := range skillMDPath.FindAllSubmatch(d.Payload, -1) {
			dir := string(m[1])
			if strings.ContainsAny(dir, "*?$") {
				continue // 通配或变量，不是读某一个技能
			}
			if rest, ok := strings.CutPrefix(dir, "~/"); ok {
				dir = filepath.Join(home, rest)
			} else if !filepath.IsAbs(dir) {
				dir = filepath.Join(cwd, dir)
			}
			dir = filepath.Clean(dir)
			if seen[dir] {
				continue
			}
			seen[dir] = true
			events = append(events, UsageEvent{Agent: "codex", Name: filepath.Base(dir), Dir: dir, Cwd: cwd, At: d.Timestamp})
		}
	})
	return first, events
}

// SetUsage 把调用记录对到库里的技能上。重新扫描后要再调一次。
func (l *Library) SetUsage(log *UsageLog) {
	l.usageLog, l.usage, l.calls = log, map[*Skill]*Usage{}, nil
	if log == nil {
		return
	}
	byReal := map[string]*Skill{}
	for _, s := range l.Skills {
		if s.Real != "" {
			byReal[s.Real] = s
		}
		for _, p := range s.places {
			byReal[p.real] = s
		}
	}
	reals := map[string]string{}
	for _, ev := range log.Events {
		var s *Skill
		if ev.Dir != "" {
			real, ok := reals[ev.Dir]
			if !ok {
				real, _ = realPath(ev.Dir)
				reals[ev.Dir] = real
			}
			// 只认路径：别处的同名目录（Agent 自带的等）不是这个技能
			s = byReal[real]
		} else {
			s = l.usedSkill(ev.Name, ev.Cwd)
		}
		if s == nil {
			continue
		}
		u := l.usage[s]
		if u == nil {
			u = &Usage{By: map[string]int{}}
			l.usage[s] = u
		}
		u.Count++
		u.By[ev.Agent]++
		if ev.At.After(u.Last) {
			u.Last = ev.At
		}
		l.calls = append(l.calls, call{ev.Agent, ev.At})
	}
}

// usedSkill 按名称找被调用的技能：「插件:技能」是插件里的技能；否则先看
// 当时所在项目里的项目技能，再看「我的」，最后是不带插件名的内置技能和
// 插件装的技能。
func (l *Library) usedSkill(name, cwd string) *Skill {
	if plugin, n, ok := strings.Cut(name, ":"); ok {
		for _, s := range l.Skills {
			if s.Plugin != "" && s.Plugin == plugin && s.Name == n {
				return s
			}
		}
		return nil
	}
	var mine, builtin *Skill
	for _, s := range l.Skills {
		if s.Name != name {
			continue
		}
		switch s.Source {
		case Project:
			if cwd == s.ProjectDir || strings.HasPrefix(cwd, s.ProjectDir+string(filepath.Separator)) {
				return s
			}
		case Mine:
			if mine == nil {
				mine = s
			}
		case Builtin, ViaPlugin:
			if builtin == nil {
				builtin = s
			}
		}
	}
	if mine != nil {
		return mine
	}
	return builtin
}

// HasUsage 报告调用记录读出来了（后台读，刚启动时还没有）。
func (l *Library) HasUsage() bool { return l.usageLog != nil }

// UsageSince 是统计从什么时候起：最早的一条会话记录。
func (l *Library) UsageSince() time.Time {
	if l.usageLog == nil {
		return time.Time{}
	}
	return l.usageLog.Since
}

// Usage 是技能被调用的情况。
func (l *Library) Usage(s *Skill) Usage {
	if u := l.usage[s]; u != nil {
		return *u
	}
	return Usage{}
}

// UsageTracked 报告技能用没用过是知道的：留有记录的 Agent 里至少有一个
// 读得到它。
func (l *Library) UsageTracked(s *Skill) bool {
	for _, id := range UsageAgents {
		switch s.Source {
		case Builtin, ViaPlugin:
			if s.Provider == id {
				return true
			}
		case Project:
			if slices.ContainsFunc(s.Reads, func(r Read) bool { return r.Agent == id }) {
				return true
			}
		default:
			if s.DiscoveredIn(id) {
				return true
			}
		}
	}
	return false
}

// Used 是用过的技能，次数多的在前；一样多时最近用过的在前。
func (l *Library) Used() []*Skill {
	var out []*Skill
	for _, s := range l.Skills {
		if l.usage[s] != nil {
			out = append(out, s)
		}
	}
	slices.SortStableFunc(out, func(a, b *Skill) int {
		ua, ub := l.usage[a], l.usage[b]
		if ua.Count != ub.Count {
			return ub.Count - ua.Count
		}
		return ub.Last.Compare(ua.Last)
	})
	return out
}

// call 是对到技能上的一次调用：对不上技能的（/model 这类内置命令、
// 别处的同名目录）不算。
type call struct {
	agent string
	at    time.Time
}

// weekStart 是 t 所在那周的周一零点（本地时间）。
func weekStart(t time.Time) time.Time {
	return dayStart(t).AddDate(0, 0, -(int(t.Local().Weekday())+6)%7)
}

func dayStart(t time.Time) time.Time {
	y, m, d := t.Local().Date()
	return time.Date(y, m, d, 0, 0, 0, 0, time.Local)
}

// Day 是一天里各 Agent 调用技能的次数。Date 是当天本地零点。
type Day struct {
	Date     time.Time
	By       map[string]int
	Future   bool // 本周还没到的日子
	Untraced bool // 统计开始（最早的会话记录）之前，没有记录，不是没调用
}

// Total 是这一天的调用次数。
func (d Day) Total() int {
	n := 0
	for _, v := range d.By {
		n += v
	}
	return n
}

// Daily 返回最近 weeks 周按天统计的调用，从最早那周的周一起到本周的周日止。
func (l *Library) Daily(now time.Time, weeks int) []Day {
	if weeks <= 0 {
		return nil
	}
	today := dayStart(now)
	start := weekStart(now).AddDate(0, 0, -7*(weeks-1))
	var first time.Time
	if since := l.UsageSince(); !since.IsZero() {
		first = dayStart(since)
	}
	days := make([]Day, weeks*7)
	for i := range days {
		date := start.AddDate(0, 0, i)
		days[i] = Day{
			Date:     date,
			By:       map[string]int{},
			Future:   date.After(today),
			Untraced: date.Before(first),
		}
	}
	for _, c := range l.calls {
		if c.at.IsZero() {
			continue
		}
		d := dayStart(c.at)
		if d.Before(start) {
			continue
		}
		if idx := int(d.Sub(start).Hours()/24 + 0.5); 0 <= idx && idx < len(days) {
			days[idx].By[c.agent]++
		}
	}
	return days
}

// Unused 报告技能在有记录以来没有被调用过：「我的」和内置技能里，留有
// 记录的 Agent 读得到、却一次也没用的。项目技能跟着项目走，不算。
func (l *Library) Unused(s *Skill) bool {
	return l.HasUsage() && s.Source != Project && l.UsageTracked(s) && l.usage[s] == nil
}

// sampleUsage 给示例数据配上调用记录：列出的用过，其余的没用过。
func sampleUsage(l *Library) {
	now := time.Now()
	l.usageLog = &UsageLog{Since: now.AddDate(0, 0, -41), Sessions: 178}
	l.usage = map[*Skill]*Usage{}
	for name, u := range map[string]Usage{
		"test-first":          {18, now.Add(-3 * time.Hour), map[string]int{cl: 16, cx: 2}},
		"context-compression": {13, now.AddDate(0, 0, -1), map[string]int{cl: 11, cx: 2}},
		"code-review":         {9, now.AddDate(0, 0, -2), map[string]int{cl: 9}},
		"vue-best-practices":  {6, now.AddDate(0, 0, -6), map[string]int{cl: 4, cx: 2}},
		"brainstorming":       {5, now.AddDate(0, 0, -9), map[string]int{cl: 5}},
		"kami":                {4, now.AddDate(0, 0, -12), map[string]int{cl: 4}},
		"log-analysis":      {3, now.AddDate(0, 0, -4), map[string]int{cl: 3}},
		"lark-doc":            {2, now.AddDate(0, 0, -20), map[string]int{cx: 2}},
		"pdf":                 {7, now.AddDate(0, 0, -3), map[string]int{cl: 7}},
		"release-checklist":   {1, now.AddDate(0, 0, -15), map[string]int{cl: 1}},
	} {
		if s := l.Get(name); s != nil {
			l.usage[s] = &u
		}
	}
	// 每个技能的调用从统计开始到上次之间均匀排开，每周的图才有东西
	l.calls = nil
	for _, u := range l.usage {
		for agent, n := range u.By {
			span := u.Last.Sub(l.usageLog.Since)
			for i := range n {
				l.calls = append(l.calls, call{agent, u.Last.Add(-span * time.Duration(i) / time.Duration(n))})
			}
		}
	}
	slices.SortFunc(l.calls, func(a, b call) int { return a.at.Compare(b.at) })
}
