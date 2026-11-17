package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/egoist/mygo"

	"skillmanager/internal/library"
)

const (
	defaultWatchDebounce  = 300 * time.Millisecond
	defaultWatchMaxDelay  = 2 * time.Second
	activateWatchCooldown = 5 * time.Second
)

type pendingWatchScan struct {
	lib         *library.Library
	report      library.UpdateReport
	gen         uint64
	reloadUsage bool
}

type watchState struct {
	hub       atomic.Pointer[diskWatcher]
	scanGen   atomic.Uint64
	scanEpoch uint64 // 每次 applyScan 或 watch 暂存 pending 递增，防较早发起的设置扫描回盖较新结果
	pending   *pendingWatchScan
}

type watchConfig struct {
	debounce       time.Duration
	maxDelay       time.Duration
	noFSWatch      bool // 测试仅测扫描调度时跳过建 FSEvents 流
	waitScanOnStop bool // 测试退出时等待后台扫描结束，避免 t.TempDir 清理竞争
	scan           func(home string) (*library.Library, library.UpdateReport)
	post           func(func())
}

type watchedEntry struct {
	watcher *mygo.FileWatcher
	ctx     context.Context
	cancel  context.CancelFunc
	names   []string // 等待创建的监听只认这些直接子项，其余变化不重扫
}

// watchKind 是一项监听的种类：目录树只给提示，单文件看它自己，等待创建的
// 目录不递归、只看点名的子项。
type watchKind int

const (
	watchDir watchKind = iota
	watchFile
	watchCreate
)

// createKey 是等待创建的监听在表里的键：目录相同、点名的子项变了也要重开。
func createKey(c library.WatchCreate) string { return c.Dir + "\x00" + strings.Join(c.Names, "\x00") }

type diskWatcher struct {
	home           string
	debounce       time.Duration
	maxDelay       time.Duration
	noFSWatch      bool
	waitScanOnStop bool
	scan           func(home string) (*library.Library, library.UpdateReport)
	post           func(func())
	onReady        func(*library.Library, library.UpdateReport, uint64)
	curGen         func() uint64

	ctx     context.Context
	cancel  context.CancelFunc
	watchWG sync.WaitGroup
	scanWG  sync.WaitGroup

	mu          sync.Mutex
	stopped     bool
	dirs        map[string]*watchedEntry
	files       map[string]*watchedEntry
	creates     map[string]*watchedEntry
	failed      map[string]string
	truncLogged bool
	wantPlan    library.WatchPlan
	wantPending bool
	syncing     bool
	syncDone    chan struct{}
	dirty       bool
	scanning    bool
}

func (a *app) startWatch() {
	if a.lib == nil || a.lib.Home == "" {
		return
	}
	a.stopWatch()

	var cfg watchConfig
	if a.watchCfg != nil {
		cfg = *a.watchCfg
	}
	if cfg.debounce <= 0 {
		cfg.debounce = defaultWatchDebounce
	}
	if cfg.maxDelay <= 0 {
		cfg.maxDelay = defaultWatchMaxDelay
	}
	if cfg.maxDelay < cfg.debounce {
		cfg.maxDelay = cfg.debounce
	}
	if cfg.scan == nil {
		cfg.scan = func(home string) (*library.Library, library.UpdateReport) {
			l := library.Scan(home)
			return l, l.Updates()
		}
	}
	if cfg.post == nil && a.live {
		cfg.post = a.post
	}
	if cfg.post == nil {
		return
	}

	ctx, cancel := context.WithCancel(context.Background())
	hub := &diskWatcher{
		home:           a.lib.Home,
		debounce:       cfg.debounce,
		maxDelay:       cfg.maxDelay,
		noFSWatch:      cfg.noFSWatch,
		waitScanOnStop: cfg.waitScanOnStop,
		scan:           cfg.scan,
		post:           cfg.post,
		onReady:        a.applyWatchScan,
		curGen:         a.watch.scanGen.Load,
		ctx:            ctx,
		cancel:         cancel,
		dirs:           map[string]*watchedEntry{},
		files:          map[string]*watchedEntry{},
		creates:        map[string]*watchedEntry{},
		failed:         map[string]string{},
	}
	a.watch.hub.Store(hub)
	if !cfg.noFSWatch {
		hub.sync(a.lib.WatchPlan())
	}
}

func (a *app) stopWatch() {
	hub := a.watch.hub.Swap(nil)
	if hub == nil {
		return
	}
	hub.stop()
}

func (a *app) triggerWatchScan() {
	if hub := a.watch.hub.Load(); hub != nil {
		hub.markDirty()
	}
}

func (a *app) triggerWatchIfStale(cooldown time.Duration) {
	if a.watch.hub.Load() == nil {
		return
	}
	if time.Since(a.set.scannedAt) >= cooldown {
		a.triggerWatchScan()
	}
}

func (a *app) isWatchApplyDeferred() bool {
	return a.upd.cancel != nil ||
		a.upd.prepared != nil ||
		a.upd.diff != nil ||
		a.tidy.busy() ||
		a.del.confirmOpen ||
		a.del.copiesOpen ||
		a.confirmOpen ||
		a.collectOpen ||
		a.linkedOpen ||
		a.cmp != nil ||
		a.anyMenuOpen() ||
		a.batch.on ||
		a.del.picking
}

// applyWatchScan 在界面线程接收后台监听扫描结果：若被非监听扫描超越则丢弃，
// 若用户正处于关键交互则延后到空闲再应用。监听结果之间保持顺序递增，不递增 scanGen，
// 避免并发补扫因前一轮先落地而被误判过期。
func (a *app) applyWatchScan(lib *library.Library, rep library.UpdateReport, baseGen uint64) {
	if baseGen != a.watch.scanGen.Load() {
		// 若在途监听扫描被后台流程的 useScan 超越，顺手补标记一次确保最新外部变化收敛。
		a.triggerWatchScan()
		return
	}
	reload := a.watch.pending != nil && a.watch.pending.reloadUsage
	if a.isWatchApplyDeferred() {
		a.watch.scanEpoch++
		a.watch.pending = &pendingWatchScan{lib: lib, report: rep, gen: baseGen, reloadUsage: reload}
		return
	}
	a.watch.pending = nil
	a.applyScan(lib, rep, reload)
	a.ensureSourceIcons()
}

func (a *app) flushPendingWatch() {
	p := a.watch.pending
	if p == nil || a.isWatchApplyDeferred() {
		return
	}
	a.watch.pending = nil
	if p.gen != a.watch.scanGen.Load() {
		return
	}
	a.applyScan(p.lib, p.report, p.reloadUsage)
	a.ensureSourceIcons()
}

// watchIgnore 过滤开发与构建噪音目录。注意在 HintsOnly 模式下 mygo 只对目录询问
// Ignore，忽略目录下的子项不会触发提示；非目录项由各自路径语义处理，无需文件后缀规则。
func watchIgnore(rel string, isDir bool) bool {
	if !isDir {
		return false
	}
	for _, part := range strings.Split(filepath.ToSlash(rel), "/") {
		switch part {
		case ".git", "node_modules", ".venv", "__pycache__", "marketplaces":
			return true
		}
	}
	return false
}

// sync 将期望监听清单提交到后台协程异步对齐，避免在主线程持锁或调用 WatchFiles
// 时触发 mygo 嵌套事件循环导致重入死锁。
func (h *diskWatcher) sync(plan library.WatchPlan) {
	if h.noFSWatch {
		return
	}
	h.mu.Lock()
	if h.stopped || h.ctx.Err() != nil {
		h.mu.Unlock()
		return
	}
	h.wantPlan = plan
	h.wantPending = true
	if !h.syncing {
		h.syncing = true
		h.syncDone = make(chan struct{})
		h.watchWG.Add(1)
		h.mu.Unlock()
		go h.syncLoop()
		return
	}
	h.mu.Unlock()
}

func (h *diskWatcher) waitSynced() {
	for {
		h.mu.Lock()
		if !h.syncing && !h.wantPending {
			h.mu.Unlock()
			return
		}
		ch := h.syncDone
		h.mu.Unlock()
		if ch == nil {
			return
		}
		<-ch
	}
}

func (h *diskWatcher) syncLoop() {
	defer h.watchWG.Done()
	for {
		h.mu.Lock()
		if h.stopped || h.ctx.Err() != nil || !h.wantPending {
			h.syncing = false
			if h.syncDone != nil {
				close(h.syncDone)
				h.syncDone = nil
			}
			h.mu.Unlock()
			return
		}
		plan := h.wantPlan
		h.wantPending = false
		if plan.Truncated && !h.truncLogged {
			h.truncLogged = true
			library.Warn(h.home, "watch.limit", library.Event{Cat: "watch", Op: "limit", Msg: fmt.Sprintf("target limit (%d) reached, some targets omitted", library.WatchMaxTargets)})
		}

		var toClose []*watchedEntry
		var opens []watchTarget
		wanted := wantedTargets(plan)
		for kind, active := range h.tables() {
			for key, entry := range active {
				if _, ok := wanted[kind][key]; !ok {
					toClose = append(toClose, entry)
					delete(active, key)
				}
			}
			for key, target := range wanted[kind] {
				if _, ok := active[key]; !ok {
					opens = append(opens, target)
				}
			}
		}
		h.mu.Unlock()

		for _, entry := range toClose {
			entry.cancel()
			_ = entry.watcher.Close()
		}

		type opened struct {
			target watchTarget
			entry  *watchedEntry
			err    error
		}
		var results []opened
		for _, target := range opens {
			if h.ctx.Err() != nil {
				break
			}
			entryCtx, entryCancel := context.WithCancel(h.ctx)
			opts := mygo.FileWatchOptions{Debounce: h.debounce, MaxDelay: h.maxDelay}
			if target.kind == watchDir {
				opts.Recursive, opts.HintsOnly, opts.Ignore = true, true, watchIgnore
			}
			w, err := mygo.WatchFiles(entryCtx, target.path, opts)
			if err != nil {
				entryCancel()
				results = append(results, opened{target: target, err: err})
				continue
			}
			results = append(results, opened{target: target,
				entry: &watchedEntry{watcher: w, ctx: entryCtx, cancel: entryCancel, names: target.names}})
		}

		var extraClose []*watchedEntry
		appeared := false
		h.mu.Lock()
		tables, wantedNow := h.tables(), wantedTargets(h.wantPlan)
		for _, r := range results {
			if r.err != nil {
				if msg := r.err.Error(); h.failed[r.target.path] != msg {
					h.failed[r.target.path] = msg
					library.Warn(h.home, "watch.failed:"+r.target.path, library.Event{Cat: "watch", Op: "failed", Target: r.target.path, Msg: msg})
				}
				continue
			}
			delete(h.failed, r.target.path)
			active := tables[r.target.kind]
			_, stillWanted := wantedNow[r.target.kind][r.target.key]
			if h.stopped || h.ctx.Err() != nil || !stillWanted || active[r.target.key] != nil {
				extraClose = append(extraClose, r.entry)
				continue
			}
			active[r.target.key] = r.entry
			h.watchWG.Add(1)
			go h.watchEntry(r.entry, r.target.key, r.target.kind)
			if r.target.kind == watchCreate && slices.ContainsFunc(r.target.names, func(name string) bool {
				_, err := os.Lstat(filepath.Join(r.target.path, name))
				return err == nil
			}) {
				// 等的东西在计划算好到监听开好之间已经出现了：这次变化不会再来，补扫一次
				appeared = true
			}
		}
		h.mu.Unlock()
		if appeared {
			h.markDirty()
		}

		for _, entry := range extraClose {
			entry.cancel()
			_ = entry.watcher.Close()
		}
	}
}

// watchTarget 是计划里的一项监听。
type watchTarget struct {
	kind      watchKind
	key, path string
	names     []string
}

// wantedTargets 把计划按种类展开成以键索引的监听项。
func wantedTargets(p library.WatchPlan) map[watchKind]map[string]watchTarget {
	out := map[watchKind]map[string]watchTarget{watchDir: {}, watchFile: {}, watchCreate: {}}
	for _, d := range p.Dirs {
		out[watchDir][d] = watchTarget{kind: watchDir, key: d, path: d}
	}
	for _, f := range p.Files {
		out[watchFile][f] = watchTarget{kind: watchFile, key: f, path: f}
	}
	for _, c := range p.Creates {
		out[watchCreate][createKey(c)] = watchTarget{kind: watchCreate, key: createKey(c), path: c.Dir, names: c.Names}
	}
	return out
}

// tables 是各种类正在运行的监听；调用方持有 h.mu。
func (h *diskWatcher) tables() map[watchKind]map[string]*watchedEntry {
	return map[watchKind]map[string]*watchedEntry{watchDir: h.dirs, watchFile: h.files, watchCreate: h.creates}
}

func (h *diskWatcher) watchEntry(entry *watchedEntry, key string, kind watchKind) {
	defer h.watchWG.Done()
	for {
		events, err := entry.watcher.Next(entry.ctx)
		if err != nil {
			h.removeEntry(key, kind, entry)
			// 被监听目录或配置文件自身被外部删除/替换时触发一次补扫。
			if entry.ctx.Err() == nil && h.ctx.Err() == nil {
				h.markDirty()
			}
			return
		}
		if slices.ContainsFunc(events, entry.wants) {
			h.markDirty()
		}
	}
}

// wants 报告这次变化要不要重扫：等待创建的监听只认点名的直接子项，
// 系统丢了变化（"."）时也重扫；其余监听有变化就重扫。
func (e *watchedEntry) wants(ev mygo.FileEvent) bool {
	if e.names == nil {
		return true
	}
	first, _, _ := strings.Cut(ev.Path, "/")
	old, _, _ := strings.Cut(ev.OldPath, "/")
	return first == "." || slices.Contains(e.names, first) || ev.OldPath != "" && slices.Contains(e.names, old)
}

func (h *diskWatcher) removeEntry(key string, kind watchKind, entry *watchedEntry) {
	h.mu.Lock()
	defer h.mu.Unlock()
	active := h.tables()[kind]
	if active[key] == entry {
		delete(active, key)
	}
}

func (h *diskWatcher) markDirty() {
	h.mu.Lock()
	if h.stopped || h.ctx.Err() != nil {
		h.mu.Unlock()
		return
	}
	h.dirty = true
	if h.scanning {
		h.mu.Unlock()
		return
	}
	h.scanning = true
	h.scanWG.Add(1)
	h.mu.Unlock()

	go h.scanLoop()
}

func (h *diskWatcher) scanLoop() {
	defer h.scanWG.Done()
	for {
		h.mu.Lock()
		if h.stopped || h.ctx.Err() != nil || !h.dirty {
			h.scanning = false
			h.mu.Unlock()
			return
		}
		h.dirty = false
		h.mu.Unlock()

		baseGen := h.curGen()
		lib, rep := h.scan(h.home)
		if h.ctx.Err() != nil {
			h.mu.Lock()
			h.scanning = false
			h.mu.Unlock()
			return
		}
		h.post(func() {
			if h.ctx.Err() == nil {
				h.onReady(lib, rep, baseGen)
			}
		})
	}
}

func (h *diskWatcher) stop() {
	h.mu.Lock()
	if h.stopped {
		h.mu.Unlock()
		return
	}
	h.stopped = true
	var entries []*watchedEntry
	for k, e := range h.dirs {
		entries = append(entries, e)
		delete(h.dirs, k)
	}
	for k, e := range h.files {
		entries = append(entries, e)
		delete(h.files, k)
	}
	for k, e := range h.creates {
		entries = append(entries, e)
		delete(h.creates, k)
	}
	h.mu.Unlock()

	h.cancel()
	for _, e := range entries {
		e.cancel()
		_ = e.watcher.Close()
	}
	h.watchWG.Wait()
	if h.waitScanOnStop {
		h.scanWG.Wait()
	}
}
