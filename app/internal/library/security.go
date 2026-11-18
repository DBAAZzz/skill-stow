package library

import (
	"context"
	"errors"
	"io/fs"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"skillmanager/internal/security"
)

// 本地安全检查服务：由应用持有，不随页面关闭，也不随 Library 重建。
//
//   - 首次（本机没有报告）或规则、引擎升级后，发现结果里的每个实际目录
//     都排进队列，后台最多同时扫 2 个。
//   - 重启时先显示上次结果，再只比对元信息（条目、大小、修改时间、权限、
//     链接目标，不读正文）；变了的标为已过期并重扫，读不了的标为无法核实。
//   - 运行中由目录监听提示、应用激活和定时比对发现变化。
//   - 扫描只读、不联网、不执行；报告和“已看过”标记存在本机。

// Freshness 是上次结果和当前目录的对应关系。
type Freshness int

const (
	FreshUnknown  Freshness = iota // 无法核实：目录读不了、读的时候在变等
	FreshChecking                  // 刚启动，还没比对元信息
	FreshCurrent                   // 元信息和扫描时一致
	FreshStale                     // 目录变了，或规则、引擎升级了
)

// SkillSecurity 是一个 Skill 目录安全检查状态的只读副本，卡片、整理页和
// 详情都从它派生，不另算一套。
type SkillSecurity struct {
	Supported bool
	Report    *security.Report // 最近一次结束的报告；nil 是还没扫过
	Previous  *security.Report // 上一份完整报告
	Fresh     Freshness
	Scanning  bool
	Queued    bool
	Acked     map[string]bool // 仍然有效的“已看过”
}

// Pending 是还没看过的警告和高风险的条数。
func (v SkillSecurity) Pending() int {
	n := 0
	if v.Report != nil {
		for _, f := range v.Report.Findings {
			if f.Notice.Risky() && !v.Acked[f.Key] {
				n++
			}
		}
	}
	return n
}

// Risky 是警告和高风险的条数，看过的也算。
func (v SkillSecurity) Risky() int {
	n := 0
	if v.Report != nil {
		for _, f := range v.Report.Findings {
			if f.Notice.Risky() {
				n++
			}
		}
	}
	return n
}

// TopPending 是没看过的风险里最严重的类型；都看过了或没有风险为空。
func (v SkillSecurity) TopPending() security.Notice {
	var top security.Notice
	if v.Report != nil {
		for _, f := range v.Report.Findings {
			if f.Notice.Risky() && !v.Acked[f.Key] && f.Notice.Rank() > top.Rank() {
				top = f.Notice
			}
		}
	}
	return top
}

// Unfinished 报告这次检查有没有完整完成：失败、取消、超时或覆盖不完整。
func (v SkillSecurity) Unfinished() bool {
	return v.Report != nil && (v.Report.Execution != security.Completed || !v.Report.Complete)
}

// Clean 报告能不能说“未发现已知风险”：完整完成、结果当前有效、没有风险。
func (v SkillSecurity) Clean() bool {
	return v.Report != nil && !v.Unfinished() && v.Fresh == FreshCurrent && v.Risky() == 0
}

type secRoot struct {
	rec      *securityRecord
	prev     *securityRecord
	acks     []SecurityAck
	fresh    Freshness
	loaded   bool
	queued   bool
	running  context.CancelFunc
	again    bool // 扫描中目录又变了：扫完再排一次
	verified bool // 这次启动后元信息比对过
}

// SecurityService 是本地安全检查服务。
type SecurityService struct {
	mu       sync.Mutex
	dir      string // 落盘目录；空表示只在内存里（示例数据、不支持的平台）
	sample   bool
	fp       security.Fingerprinter
	lim      readLimits
	roots    map[string]*secRoot
	queue    []string
	workers  int
	busy     int
	done     int // 这一批完成了几个
	total    int // 这一批一共几个
	ctx      context.Context
	stop     context.CancelFunc
	wake     chan struct{}
	onChange func()
	metaBusy bool
	lastMeta time.Time
	timers   map[string]*time.Timer
}

// NewSecurity 建一个落盘的服务。onChange 可能在任意 goroutine 调用，
// 调用方自己切回界面线程。
func NewSecurity(home string, onChange func()) *SecurityService {
	dir := ""
	if home != "" && SecuritySupported() {
		dir = securityDir(home)
	}
	return newSecurity(dir, loadKey(dir), onChange, 2)
}

func newSecurity(dir string, key []byte, onChange func(), workers int) *SecurityService {
	ctx, stop := context.WithCancel(context.Background())
	if onChange == nil {
		onChange = func() {}
	}
	s := &SecurityService{dir: dir, fp: fingerprinter(key), lim: defaultLimits, roots: map[string]*secRoot{},
		workers: workers, ctx: ctx, stop: stop, wake: make(chan struct{}, 1), onChange: onChange, timers: map[string]*time.Timer{}}
	for range workers {
		go s.work()
	}
	return s
}

// Close 停下所有扫描。应用退出时调用；下次启动把没扫完的重新排队。
func (s *SecurityService) Close() {
	s.stop()
	s.mu.Lock()
	for _, t := range s.timers {
		t.Stop()
	}
	s.mu.Unlock()
}

// SecurityKey 是 Skill 在安全检查里的目录：解析过链接的实际目录，示例
// 数据没有时用它的目录。多个 Agent 引用同一份实际内容时共用一个结果。
func SecurityKey(s *Skill) string {
	if s.Real != "" {
		return s.Real
	}
	return s.Dir
}

func (s *SecurityService) root(r string) *secRoot {
	st := s.roots[r]
	if st == nil {
		st = &secRoot{fresh: FreshChecking}
		s.roots[r] = st
	}
	return st
}

// load 读出一个目录落盘的报告和标记，调用方持锁。
func (s *SecurityService) load(r string, st *secRoot) {
	if st.loaded {
		return
	}
	st.loaded = true
	if s.dir == "" {
		return
	}
	d := reportDir(s.dir, r)
	var bad bool
	st.rec, bad = loadRecord(filepath.Join(d, "current.json"), r)
	st.prev, _ = loadRecord(filepath.Join(d, "previous.json"), r)
	st.acks = loadAcks(filepath.Join(d, "acks.json"))
	if bad {
		st.fresh = FreshUnknown
	}
}

func outdated(r *security.Report) bool {
	return r.Schema != security.SchemaVersion || r.Engine != security.EngineVersion || r.Ruleset != security.RulesetVersion()
}

// Sync 和发现结果对齐：没扫过或规则升级过的目录排队扫描（首次打开即全量），
// 其余的先显示上次结果，再在后台比对元信息。发现里没有了的目录移出内存。
func (s *SecurityService) Sync(roots []string) {
	if s.sample {
		return
	}
	s.mu.Lock()
	live := map[string]bool{}
	var scan, check []string
	for _, r := range roots {
		if r == "" || live[r] {
			continue
		}
		live[r] = true
		st := s.root(r)
		if st.loaded {
			continue
		}
		s.load(r, st)
		switch {
		case st.rec == nil || outdated(&st.rec.Report):
			if st.rec != nil {
				st.fresh = FreshStale
			}
			scan = append(scan, r)
		default:
			check = append(check, r)
		}
	}
	for r, st := range s.roots {
		if !live[r] && st.running == nil && !st.queued {
			delete(s.roots, r)
		}
	}
	s.enqueue(scan, false)
	s.mu.Unlock()
	if len(check) > 0 {
		go s.checkMeta(check)
	}
	s.onChange()
}

// Scan 把目录排进队列。manual 为真表示用户点了“扫描 / 重新扫描”：
// 排到最前面，总是重新分析。
func (s *SecurityService) Scan(roots []string, manual bool) {
	if s.sample || !SecuritySupported() {
		return
	}
	s.mu.Lock()
	for _, r := range roots {
		s.load(r, s.root(r))
	}
	s.enqueue(roots, manual)
	s.mu.Unlock()
	s.onChange()
}

// enqueue 调用方持锁。正在扫的目录记一笔，扫完再来一次。
func (s *SecurityService) enqueue(roots []string, front bool) {
	if len(roots) == 0 {
		return
	}
	if len(s.queue) == 0 && s.busy == 0 {
		s.done, s.total = 0, 0
	}
	var add []string
	for _, r := range roots {
		st := s.root(r)
		switch {
		case st.running != nil:
			st.again = true
		case st.queued:
			if front {
				s.queue = slices.DeleteFunc(s.queue, func(q string) bool { return q == r })
				add = append(add, r)
			}
		default:
			st.queued = true
			add = append(add, r)
			s.total++
		}
	}
	if front {
		s.queue = append(add, s.queue...)
	} else {
		s.queue = append(s.queue, add...)
	}
	select {
	case s.wake <- struct{}{}:
	default:
	}
}

// Progress 是这一批扫描的进度；没有在扫时 total 为 0。
func (s *SecurityService) Progress() (done, total int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.queue) == 0 && s.busy == 0 {
		return 0, 0
	}
	return s.done, s.total
}

// CancelAll 停下这一批：清空队列，正在扫的取消。被取消的目录保留上次
// 结果，不拿取消的报告覆盖它。
func (s *SecurityService) CancelAll() {
	s.mu.Lock()
	for _, r := range s.queue {
		s.roots[r].queued = false
	}
	s.queue = nil
	for _, st := range s.roots {
		if st.running != nil {
			st.running()
			st.again = false
		}
	}
	s.mu.Unlock()
	s.onChange()
}

func (s *SecurityService) work() {
	for {
		s.mu.Lock()
		for len(s.queue) == 0 {
			s.mu.Unlock()
			select {
			case <-s.ctx.Done():
				return
			case <-s.wake:
			}
			s.mu.Lock()
		}
		r := s.queue[0]
		s.queue = s.queue[1:]
		st := s.root(r)
		st.queued = false
		ctx, cancel := context.WithTimeout(s.ctx, 30*time.Second)
		st.running = cancel
		s.busy++
		s.mu.Unlock()
		// 还有活的话叫醒另一个 worker
		select {
		case s.wake <- struct{}{}:
		default:
		}
		s.onChange()

		rec := s.analyze(ctx, r, false)
		cancel()

		s.mu.Lock()
		s.busy--
		s.done++
		st.running = nil
		userCancelled := rec.Report.Execution == security.Cancelled && s.ctx.Err() == nil
		if !userCancelled || st.rec == nil {
			s.store(r, st, rec)
		}
		if st.again {
			st.again = false
			st.fresh = FreshStale
			s.enqueue([]string{r}, false)
		}
		s.mu.Unlock()
		s.onChange()
	}
}

// analyze 读取并分析一个目录。读不了时返回失败报告，不当作没有风险。
func (s *SecurityService) analyze(ctx context.Context, r string, incomplete bool) *securityRecord {
	in, meta, err := readSkill(ctx, r, s.lim)
	if err != nil {
		rep := security.Report{Schema: security.SchemaVersion, Engine: security.EngineVersion, Ruleset: security.RulesetVersion(),
			StartedAt: time.Now(), FinishedAt: time.Now(), Execution: security.Failed, Error: readErrKind(err)}
		return &securityRecord{Root: r, Report: rep}
	}
	in.Incomplete = incomplete
	rep := security.Analyze(ctx, in, security.DefaultPolicy(), s.fp)
	if rep.Execution != security.Completed {
		meta = ""
	}
	return &securityRecord{Root: r, Meta: meta, Report: rep}
}

func readErrKind(err error) string {
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return "not_found"
	case errors.Is(err, fs.ErrPermission):
		return "permission"
	case errors.Is(err, context.Canceled):
		return "cancelled"
	}
	return "unreadable"
}

// store 换上新报告，落盘，并清掉不再成立的标记。调用方持锁。
func (s *SecurityService) store(r string, st *secRoot, rec *securityRecord) {
	if st.rec != nil && st.rec.Report.Complete {
		st.prev = st.rec
	}
	st.rec = rec
	st.fresh = FreshCurrent
	if rec.Meta == "" {
		st.fresh = FreshUnknown
	}
	st.verified = true
	valid := s.validAcks(st)
	st.acks = slices.DeleteFunc(st.acks, func(a SecurityAck) bool { return !valid[a.Key] })
	if s.dir == "" {
		return
	}
	d := reportDir(s.dir, r)
	if err := writePrivate(filepath.Join(d, "current.json"), rec); err != nil {
		appendEvent(appDirOf(s.dir), Event{Level: LevelError, Cat: "security", Op: "store", Kind: "write", Msg: safeErrMsg(s.dir, err)})
		return
	}
	if st.prev != nil {
		writePrivate(filepath.Join(d, "previous.json"), st.prev)
	}
	writePrivate(filepath.Join(d, "acks.json"), st.acks)
}

func appDirOf(securityDir string) string { return filepath.Dir(securityDir) }

// validAcks 算出还有效的标记：警告要同一条发现（指纹含规则版本、路径和
// 证据原文）仍在；高风险还要整个 Skill 的内容摘要没变。
func (s *SecurityService) validAcks(st *secRoot) map[string]bool {
	out := map[string]bool{}
	if st.rec == nil {
		return out
	}
	rep := &st.rec.Report
	for _, a := range st.acks {
		for _, f := range rep.Findings {
			if f.Key != a.Key || f.Notice != a.Notice || !f.Notice.Risky() {
				continue
			}
			if f.Notice == security.HighRisk && (a.Content == "" || a.Content != rep.ContentDigest) {
				continue
			}
			out[a.Key] = true
		}
	}
	return out
}

// View 是一个目录的只读状态。
func (s *SecurityService) View(r string) SkillSecurity {
	if s == nil {
		return SkillSecurity{}
	}
	v := SkillSecurity{Supported: SecuritySupported() || s.sample}
	s.mu.Lock()
	defer s.mu.Unlock()
	st := s.roots[r]
	if st == nil {
		return v
	}
	v.Fresh, v.Queued, v.Scanning = st.fresh, st.queued, st.running != nil
	if st.rec != nil {
		rep := st.rec.Report
		v.Report = &rep
		if outdated(&rep) {
			v.Fresh = FreshStale
		}
	}
	if st.prev != nil {
		p := st.prev.Report
		v.Previous = &p
	}
	v.Acked = s.validAcks(st)
	return v
}

var errNoFinding = errors.New("security: finding not in current report")

// Ack 把当前报告里的一条警告或高风险标为已看过。
func (s *SecurityService) Ack(r, key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	st := s.roots[r]
	if st == nil || st.rec == nil {
		return errNoFinding
	}
	for _, f := range st.rec.Report.Findings {
		if f.Key != key || !f.Notice.Risky() {
			continue
		}
		a := SecurityAck{Key: key, Notice: f.Notice, Rule: f.Rule, RuleVersion: f.RuleVersion}
		if f.Notice == security.HighRisk {
			a.Content = st.rec.Report.ContentDigest
		}
		st.acks = append(slices.DeleteFunc(st.acks, func(x SecurityAck) bool { return x.Key == key }), a)
		return s.saveAcks(r, st)
	}
	return errNoFinding
}

// Unack 撤销一条“已看过”。
func (s *SecurityService) Unack(r, key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	st := s.roots[r]
	if st == nil {
		return nil
	}
	st.acks = slices.DeleteFunc(st.acks, func(x SecurityAck) bool { return x.Key == key })
	return s.saveAcks(r, st)
}

func (s *SecurityService) saveAcks(r string, st *secRoot) error {
	if s.dir == "" {
		return nil
	}
	return writePrivate(filepath.Join(reportDir(s.dir, r), "acks.json"), st.acks)
}

// ---- 失效 ----

const metaCooldown = 60 * time.Second

// hintDebounce 是监听提示到重扫之间等多久；测试里调短。
var hintDebounce = 2 * time.Second

// CheckMeta 对所有已知目录比对元信息（只看元信息，不读正文）。force 为假时
// 带冷却，给应用激活用；定时比对传真。
func (s *SecurityService) CheckMeta(force bool) {
	if s.sample {
		return
	}
	s.mu.Lock()
	if !force && time.Since(s.lastMeta) < metaCooldown {
		s.mu.Unlock()
		return
	}
	var roots []string
	for r, st := range s.roots {
		if st.rec != nil && st.running == nil && !st.queued {
			roots = append(roots, r)
		}
	}
	s.mu.Unlock()
	slices.Sort(roots)
	go s.checkMeta(roots)
}

func (s *SecurityService) checkMeta(roots []string) {
	s.mu.Lock()
	if s.metaBusy {
		s.mu.Unlock()
		return
	}
	s.metaBusy = true
	s.lastMeta = time.Now()
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		s.metaBusy = false
		s.mu.Unlock()
		s.onChange()
	}()
	for _, r := range roots {
		if s.ctx.Err() != nil {
			return
		}
		ctx, cancel := context.WithTimeout(s.ctx, 5*time.Second)
		d, err := metaDigest(ctx, r, s.lim)
		cancel()
		s.mu.Lock()
		st := s.roots[r]
		if st == nil || st.rec == nil || st.running != nil || st.queued {
			s.mu.Unlock()
			continue
		}
		st.verified = true
		switch {
		case err != nil && errors.Is(err, fs.ErrNotExist):
			// 目录没了：等发现结果把它移出
		case err != nil:
			st.fresh = FreshUnknown
		case st.rec.Meta == "":
			st.fresh = FreshUnknown
		case d == st.rec.Meta:
			if st.fresh != FreshStale {
				st.fresh = FreshCurrent
			}
		default:
			st.fresh = FreshStale
			s.enqueue([]string{r}, false)
		}
		s.mu.Unlock()
	}
}

// Hint 接收目录监听的变化提示。落在某个 Skill 目录里的变化立即标过期、
// 稍等后重扫；说不清落在哪（系统丢了事件，或只给了上级目录）时，对这些
// 目录下的 Skill 做一次元信息比对，而不是全部重扫。
func (s *SecurityService) Hint(paths []string) {
	if s.sample || len(paths) == 0 {
		return
	}
	s.mu.Lock()
	var check []string
	for r, st := range s.roots {
		for _, p := range paths {
			switch {
			case p == r || strings.HasPrefix(p, r+string(filepath.Separator)):
				if st.rec != nil {
					st.fresh = FreshStale
				}
				s.debounce(r)
			case strings.HasPrefix(r, p+string(filepath.Separator)):
				check = append(check, r)
			default:
				continue
			}
			break
		}
	}
	s.mu.Unlock()
	if len(check) > 0 {
		slices.Sort(check)
		go s.checkMeta(slices.Compact(check))
	}
	s.onChange()
}

// debounce 调用方持锁：同一目录短时间内的多次变化只扫一次。
func (s *SecurityService) debounce(r string) {
	if t := s.timers[r]; t != nil {
		t.Stop()
	}
	s.timers[r] = time.AfterFunc(hintDebounce, func() {
		s.mu.Lock()
		delete(s.timers, r)
		if _, ok := s.roots[r]; ok {
			s.enqueue([]string{r}, false)
		}
		s.mu.Unlock()
		s.onChange()
	})
}

// ---- 导入、更新 ----

// StagedSecurity 是对暂存内容的一次检查，不落盘、不进队列。
type StagedSecurity struct {
	Report security.Report
}

// ScanStaged 同步检查一个暂存目录（导入、更新用）。incomplete 为真表示
// 来源还没补齐（只下载了 SKILL.md），报告会标为不完整。
func (s *SecurityService) ScanStaged(ctx context.Context, dir string, incomplete bool) StagedSecurity {
	if s == nil || !SecuritySupported() {
		return StagedSecurity{}
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	rec := s.analyze(ctx, dir, incomplete)
	return StagedSecurity{Report: rec.Report}
}
