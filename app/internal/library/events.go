package library

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"math/rand/v2"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

// 事件日志：应用改了什么、哪里失败了，写在数据目录的 logs/events.jsonl，
// 用户报问题时附在诊断信息里。只记边界上的事（改 Agent 配置、链接、更新、
// 添加、读不了的文件），不记扫描细节，也不记技能正文、配置原文、token。

// Level 是事件的级别。
type Level string

const (
	LevelInfo  Level = "info"
	LevelWarn  Level = "warn"
	LevelError Level = "error"
)

// Event 是日志里的一行。
type Event struct {
	Time   time.Time         `json:"t"`
	Level  Level             `json:"lvl"`
	Cat    string            `json:"cat"`              // native、link、update、import、delete、appdata、scan、watch、app、ui
	Op     string            `json:"op"`               // 这一类里的哪个动作
	ID     string            `json:"id,omitempty"`     // 同一次写入的开始和结束共用
	Target string            `json:"target,omitempty"` // 技能、Agent，或缩写过的路径
	Kind   string            `json:"kind,omitempty"`   // 失败的种类，见 errorKind
	Status int               `json:"status,omitempty"` // HTTP 状态
	MS     int64             `json:"ms,omitempty"`
	Msg    string            `json:"msg,omitempty"`
	Data   map[string]string `json:"data,omitempty"`
}

// eventsMax 是一份日志的上限，到了就轮转成 events.1.jsonl，只留这两份。
const (
	eventsMax     = 512 << 10
	maxEventBytes = 16 << 10
)

// eventsMu 让后台的更新、扫描和界面线程的写入一行一行地进文件。
var eventsMu sync.Mutex

func eventsFile(dir string) string { return filepath.Join(dir, "logs", "events.jsonl") }

func rotatedEventsFile(dir string) string { return filepath.Join(dir, "logs", "events.1.jsonl") }

// appendEvent 把 e 追加到应用数据目录 dir 下的事件日志。写不进去就算了：
// 记日志不能让用户的操作失败。
func appendEvent(dir string, e Event) {
	if e.Time.IsZero() {
		e.Time = time.Now()
	}
	b, ok := fitEventSize(e)
	if !ok {
		return
	}
	eventsMu.Lock()
	defer eventsMu.Unlock()
	file := eventsFile(dir)
	if fi, err := os.Stat(file); err == nil && fi.Size()+int64(len(b))+1 > eventsMax {
		os.Rename(file, rotatedEventsFile(dir))
	}
	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		return
	}
	f, err := os.OpenFile(file, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	f.Write(append(b, '\n'))
}

func fitEventSize(e Event) ([]byte, bool) {
	b, err := json.Marshal(e)
	if err != nil {
		return nil, false
	}
	if len(b) <= maxEventBytes {
		return b, true
	}
	if len(e.Data) > 0 {
		e.Data = maps.Clone(e.Data)
	}
	for limit := maxEventBytes / 2; ; limit /= 2 {
		e.Msg = truncateUTF8(e.Msg, limit)
		for k, v := range e.Data {
			e.Data[k] = truncateUTF8(v, limit)
		}
		b, err = json.Marshal(e)
		if err != nil {
			return nil, false
		}
		if len(b) <= maxEventBytes {
			return b, true
		}
		if limit == 0 {
			break
		}
	}
	e.Data = nil
	e.Target = truncateUTF8(e.Target, 256)
	b, err = json.Marshal(e)
	if err != nil || len(b) > maxEventBytes {
		return nil, false
	}
	return b, true
}

func truncateUTF8(s string, maxBytes int) string {
	if len(s) <= maxBytes {
		return s
	}
	if maxBytes <= 0 {
		return ""
	}
	const ellipsis = "…"
	if maxBytes <= len(ellipsis) {
		return ""
	}
	cut := maxBytes - len(ellipsis)
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut] + ellipsis
}

// readEvents 是日志里的全部事件，旧的在前。解析不了或超长的行跳过，继续往后读。
func readEvents(dir string) []Event {
	eventsMu.Lock()
	defer eventsMu.Unlock()
	var out []Event
	for _, file := range []string{rotatedEventsFile(dir), eventsFile(dir)} {
		f, err := os.Open(file)
		if err != nil {
			continue
		}
		r := bufio.NewReaderSize(f, 1<<20)
		for {
			line, err := r.ReadSlice('\n')
			if errors.Is(err, bufio.ErrBufferFull) {
				for errors.Is(err, bufio.ErrBufferFull) {
					_, err = r.ReadSlice('\n')
				}
				if err != nil && !errors.Is(err, io.EOF) {
					break
				}
				continue
			}
			if trimmed := bytes.TrimSpace(line); len(trimmed) > 0 {
				var e Event
				if json.Unmarshal(trimmed, &e) == nil {
					out = append(out, e)
				}
			}
			if err != nil {
				break
			}
		}
		f.Close()
	}
	return out
}

// Log 记一条事件。路径和说明里的主目录换成 ~；示例数据不记。
func (l *Library) Log(e Event) {
	if l.disk {
		logTo(l.Home, e)
	}
}

// logTo 把 e 脱敏后写进 home 的事件日志；home 为空（示例数据）时不记。
func logTo(home string, e Event) {
	if home == "" {
		return
	}
	e.Target = safeText(home, e.Target)
	e.Msg = safeText(home, e.Msg)
	for k, v := range e.Data {
		e.Data[k] = safeText(home, v)
	}
	appendEvent(appDir(home), e)
}

// track 记一次操作：成功是 info，失败是 error，带上耗时和失败的种类。
// 在有名字返回值的方法开头写 defer l.track(cat, op, target)(&err)。
func (l *Library) track(cat, op, target string) func(*error) {
	if !l.disk {
		return func(*error) {}
	}
	return trackIn(l.Home, cat, op, target)
}

// trackIn 是不在 Library 上的操作（读取要添加的技能）用的 track。
func trackIn(home, cat, op, target string) func(*error) {
	start := time.Now()
	return func(errp *error) {
		logTo(home, operationEvent(cat, op, target, time.Since(start), *errp))
	}
}

// operationEvent 是一次操作的结果：成功或取消是 info，失败是 error。
func operationEvent(cat, op, target string, took time.Duration, err error) Event {
	e := Event{Level: LevelInfo, Cat: cat, Op: op, Target: target, MS: took.Milliseconds()}
	if err != nil {
		e.Kind, e.Status = errorKind(err)
		if e.Kind != "canceled" {
			e.Level = LevelError
		}
		e.Msg = safeErrMsg("", err)
	}
	return e
}

// newEventID 是一次写入的编号，开始和结束两行共用，对不上的就是写到一半。
func newEventID() string { return fmt.Sprintf("%08x", rand.Uint32()) }

// digest 是一份配置内容的指纹：sha256 前 8 位和字节数，不记原文。
func digest(src string) string {
	sum := sha256.Sum256([]byte(src))
	return fmt.Sprintf("%x/%dB", sum[:4], len(src))
}

// RecentEvents 是最近 n 条不低于 min 级别的事件，新的在后。
func (l *Library) RecentEvents(n int, min Level) []Event {
	if !l.disk {
		return nil
	}
	return recentEventsIn(appDir(l.Home), n, min)
}

func recentEventsIn(dir string, n int, min Level) []Event {
	rank := map[Level]int{LevelInfo: 0, LevelWarn: 1, LevelError: 2}
	all := slices.DeleteFunc(readEvents(dir), func(e Event) bool { return rank[e.Level] < rank[min] })
	return all[max(0, len(all)-n):]
}

// UnconfirmedWrites 是只记了开始、没记结束的配置写入：写到一半应用退出了，
// 那个文件现在是什么样不确定。
func (l *Library) UnconfirmedWrites() []Event {
	if !l.disk {
		return nil
	}
	return unconfirmedWritesIn(appDir(l.Home))
}

func unconfirmedWritesIn(dir string) []Event {
	var open []Event
	for _, e := range readEvents(dir) {
		switch e.Op {
		case "write.start":
			open = append(open, e)
		case "write.end":
			open = slices.DeleteFunc(open, func(o Event) bool { return o.ID == e.ID })
		}
	}
	return open
}

// errorKind 把错误归成稳定的种类，给日志检索用；不认识的是空。
func errorKind(err error) (string, int) {
	var re *RemoteError
	var ie *ImportError
	var pe *configParseError
	switch {
	case errors.Is(err, context.Canceled):
		return "canceled", 0
	case errors.As(err, &pe):
		return "parse", 0
	case errors.As(err, &re):
		return re.kindName(), re.Status
	case errors.As(err, &ie) && ie.Network:
		return netCause(err), 0
	case errors.Is(err, ErrBusy):
		return "busy", 0
	case errors.Is(err, os.ErrPermission):
		return "permission", 0
	case errors.Is(err, os.ErrNotExist):
		return "notfound", 0
	}
	if cause := netCause(err); cause != "" {
		return cause, 0
	}
	return "", 0
}

func (e *RemoteError) kindName() string {
	switch e.Kind {
	case RemoteQuota:
		return "quota"
	case RemoteThrottled:
		return "throttled"
	case RemoteNotFound:
		return "notfound"
	case RemoteNetwork:
		if cause := netCause(e.Err); cause != "" {
			return cause
		}
		return "network"
	case RemoteAuth:
		return "auth"
	}
	return "other"
}

// lastWarned 是每种重复出现的警告上次记下的内容。扫描在每次文件变动后
// 都会跑，同样的问题只在第一次和变了的时候记。
var (
	lastWarnedMu sync.Mutex
	lastWarned   = map[string]string{}
)

// warnChanged 在 key 这类警告的内容 msg 跟上次不同时记一条 warn；
// msg 为空表示问题没了，下次再出现时重新记。
func (l *Library) warnChanged(key string, e Event) {
	if l.disk {
		warnIn(l.Home, key, e)
	}
}

func warnIn(home, key string, e Event) {
	lastWarnedMu.Lock()
	seen := lastWarned[home+"\x00"+key]
	lastWarned[home+"\x00"+key] = e.Msg
	lastWarnedMu.Unlock()
	if e.Msg == "" || e.Msg == seen {
		return
	}
	e.Level = LevelWarn
	logTo(home, e)
}

// Warn 在 home 的事件日志里记一条同样内容只记一次的警告，给拿不到
// Library 的后台任务用（文件监听）。key 区分不同的警告。
func Warn(home, key string, e Event) { warnIn(home, key, e) }

// logScanProblems 记下扫描时读不了的目录和安装记录。
func (l *Library) logScanProblems() {
	var dirs []string
	for _, d := range l.unreadable {
		dirs = append(dirs, l.Display(d))
	}
	l.warnChanged("scan.unreadable", Event{Cat: "scan", Op: "unreadable", Msg: strings.Join(dirs, ", "), Data: countData(len(dirs))})
	msg := safeErrMsg(l.Home, l.recordsErr)
	l.warnChanged("scan.records", Event{Cat: "scan", Op: "records", Msg: msg})
}

func countData(n int) map[string]string {
	if n == 0 {
		return nil
	}
	return map[string]string{"count": fmt.Sprint(n)}
}

// ScanTook 是这次扫描用了多久。
func (l *Library) ScanTook() time.Duration { return l.scanTook }

// Unreadable 是这次扫描时存在却读不了的目录。
func (l *Library) Unreadable() []string { return l.unreadable }

func toggleOp(on bool) string {
	if on {
		return "enable"
	}
	return "disable"
}

func skillNames(skills []*Skill) string {
	names := make([]string, 0, len(skills))
	for _, s := range skills {
		names = append(names, s.Name)
	}
	return strings.Join(names, ", ")
}

// urlHost 是网址的域名：网站来源只记到域名。
func urlHost(raw string) string {
	if u, err := url.Parse(raw); err == nil && u.Host != "" {
		return u.Host
	}
	return ""
}

// AppStartSnapshot 是在界面线程提取的只读启动统计，供后台安全写启动日志。
type AppStartSnapshot struct {
	Disk       bool
	Home       string
	ScanTookMS int64
	Agents     int
	Skills     int
	Unreadable int
}

// AppStartSnapshot 在界面线程复制启动日志需要的只读计数。
func (l *Library) AppStartSnapshot() AppStartSnapshot {
	return AppStartSnapshot{
		Disk:       l.disk,
		Home:       l.Home,
		ScanTookMS: l.scanTook.Milliseconds(),
		Agents:     len(l.Agents),
		Skills:     len(l.Skills),
		Unreadable: len(l.unreadable),
	}
}

// Log 记一次启动，带上版本和这次扫描的规模。上一次只记了启动、
// 没记退出，说明应用是被强行结束或崩溃的，先补记一条。
func (s AppStartSnapshot) Log(env DiagEnv) {
	if !s.Disk {
		return
	}
	var last *Event
	for _, e := range readEvents(appDir(s.Home)) {
		if e.Cat == "app" {
			last = &e
		}
	}
	if last != nil && last.Op == "start" {
		logTo(s.Home, Event{Level: LevelWarn, Cat: "app", Op: "unclean-exit", Msg: "previous run started " + last.Time.Format(time.RFC3339) + " without a quit"})
	}
	logTo(s.Home, Event{Level: LevelInfo, Cat: "app", Op: "start", MS: s.ScanTookMS, Data: map[string]string{
		"version": env.Version, "build": env.Build, "os": osVersion(),
		"agents": fmt.Sprint(s.Agents), "skills": fmt.Sprint(s.Skills), "unreadable": fmt.Sprint(s.Unreadable),
	}})
}

// LogAppStart 记一次启动；从后台调用时应先用 AppStartSnapshot() 提取只读副本。
func (l *Library) LogAppStart(env DiagEnv) {
	l.AppStartSnapshot().Log(env)
}

// LogAppQuit 记一次正常退出。
func (l *Library) LogAppQuit() {
	l.Log(Event{Level: LevelInfo, Cat: "app", Op: "quit"})
}
