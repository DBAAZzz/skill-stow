package main

import (
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"skillmanager/internal/library"
)

func waitUntil(t *testing.T, timeout time.Duration, desc string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(15 * time.Millisecond)
	}
	t.Fatalf("等待超时：%s", desc)
}

func makeTempSkillHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	home, _ = filepath.EvalSymlinks(home)
	baseSkill := filepath.Join(home, ".agents/skills/init-skill")
	must(t, os.MkdirAll(baseSkill, 0o755))
	must(t, os.WriteFile(filepath.Join(baseSkill, "SKILL.md"), []byte("---\nname: init-skill\ndescription: initial\n---\n"), 0o644))
	return home
}

func TestWatchIgnoreFiltersOnlyNoiseDirectories(t *testing.T) {
	// Given 开发噪音目录与普通目录/文件相对路径
	cases := []struct {
		rel   string
		isDir bool
		want  bool
	}{
		{".git", true, true},
		{"skill-a/.git/objects", true, true},
		{"skill-a/node_modules", true, true},
		{"skill-a/.venv", true, true},
		{"skill-a/__pycache__", true, true},
		{"skill-a/references", true, false},
		// HintsOnly 下 mygo 不按文件过滤，非目录保持 false，不留无效死分支
		{".git", false, false},
		{"SKILL.md", false, false},
	}

	// When/Then 调用 watchIgnore 只屏蔽噪音目录
	for _, tc := range cases {
		if got := watchIgnore(tc.rel, tc.isDir); got != tc.want {
			t.Errorf("watchIgnore(%q, %v) = %v, want %v", tc.rel, tc.isDir, got, tc.want)
		}
	}
}

func TestAppAutoRefreshesWhenSkillCreatedOnDisk(t *testing.T) {
	// Given 一个基于真实磁盘的 app 实例并开启目录监听
	home := makeTempSkillHome(t)
	a := newApp(library.Scan(home))

	var mu sync.Mutex
	a.watchCfg = &watchConfig{
		debounce:       40 * time.Millisecond,
		maxDelay:       120 * time.Millisecond,
		waitScanOnStop: true,
		post: func(fn func()) {
			mu.Lock()
			defer mu.Unlock()
			fn()
		},
	}
	a.startWatch()
	defer a.stopWatch()
	a.watch.hub.Load().waitSynced()

	// When 外部在 ~/.agents/skills 下创建新技能
	added := filepath.Join(home, ".agents/skills/ext-fresh")
	must(t, os.MkdirAll(added, 0o755))
	must(t, os.WriteFile(filepath.Join(added, "SKILL.md"), []byte("---\nname: ext-fresh\ndescription: fresh skill\n---\n"), 0o644))

	// Then 无需手动点刷新，app 的技能库自动出现该技能
	waitUntil(t, 4*time.Second, "新技能自动出现在 app.lib 中", func() bool {
		mu.Lock()
		defer mu.Unlock()
		return a.lib.ByID("mine:ext-fresh") != nil
	})
}

func TestAppIgnoresNoiseChangesUnderWatchedSkillDirs(t *testing.T) {
	// Given 一个开启了目录监听的 app，且技能目录里已有 .git 与 node_modules
	home := makeTempSkillHome(t)
	gitDir := filepath.Join(home, ".agents/skills/init-skill/.git")
	nmDir := filepath.Join(home, ".agents/skills/init-skill/node_modules/pkg")
	must(t, os.MkdirAll(gitDir, 0o755))
	must(t, os.MkdirAll(nmDir, 0o755))

	a := newApp(library.Scan(home))
	var scans atomic.Int32
	a.watchCfg = &watchConfig{
		debounce:       40 * time.Millisecond,
		maxDelay:       120 * time.Millisecond,
		waitScanOnStop: true,
		scan: func(h string) (*library.Library, library.UpdateReport) {
			scans.Add(1)
			l := library.Scan(h)
			return l, l.Updates()
		},
		post: func(fn func()) { fn() },
	}
	a.startWatch()
	defer a.stopWatch()
	a.watch.hub.Load().waitSynced()

	// 等待启动前 MkdirAll 遗留的内核 FSEvents 窗口静默，避免偶发误判
	quietUntil := time.Now().Add(3 * time.Second)
	last := scans.Load()
	stableSince := time.Now()
	for time.Now().Before(quietUntil) {
		cur := scans.Load()
		if cur != last {
			last = cur
			stableSince = time.Now()
		} else if time.Since(stableSince) >= 250*time.Millisecond {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	scans.Store(0)

	// When 在 .git 和 node_modules 中高频写文件
	must(t, os.WriteFile(filepath.Join(gitDir, "HEAD"), []byte("ref: refs/heads/main\n"), 0o644))
	must(t, os.WriteFile(filepath.Join(nmDir, "index.js"), []byte("export {};\n"), 0o644))

	time.Sleep(350 * time.Millisecond)

	// Then 不应触发全量重扫
	if got := scans.Load(); got != 0 {
		t.Fatalf("修改 .git / node_modules 不应触发重扫，实际扫描 %d 次", got)
	}
}

func TestAppAppliesCoalescedFollowUpScanWithAsyncPost(t *testing.T) {
	// Given 模拟 win.Update 的异步 FIFO 主线程队列，以及第 1 轮扫描期间到来的新变化
	home := makeTempSkillHome(t)
	a := newApp(library.Scan(home))

	uiQueue := make(chan func(), 16)
	uiDone := make(chan struct{})
	firstQueued := make(chan struct{}, 1)
	allowUIDrain := make(chan struct{})

	go func() {
		defer close(uiDone)
		<-allowUIDrain
		for fn := range uiQueue {
			fn()
		}
	}()

	var scanNum atomic.Int32
	lib1 := library.Sample()
	lib2 := library.Sample()
	scan2Started := make(chan struct{}, 1)
	applied2 := make(chan struct{}, 1)

	a.watchCfg = &watchConfig{
		noFSWatch:      true,
		waitScanOnStop: true,
		scan: func(string) (*library.Library, library.UpdateReport) {
			if scanNum.Add(1) == 1 {
				return lib1, library.UpdateReport{}
			}
			select {
			case scan2Started <- struct{}{}:
			default:
			}
			return lib2, library.UpdateReport{}
		},
		post: func(fn func()) {
			uiQueue <- func() {
				fn()
				if a.lib == lib2 {
					select {
					case applied2 <- struct{}{}:
					default:
					}
				}
			}
			select {
			case firstQueued <- struct{}{}:
			default:
			}
		},
	}
	a.startWatch()

	// When 第 1 次扫描入队后尚未被主线程消化，此时触发第 2 次变化并完成第 2 轮扫描，随后放开主线程消费队列
	a.triggerWatchScan()
	<-firstQueued
	a.triggerWatchScan()
	<-scan2Started
	time.Sleep(20 * time.Millisecond) // 确保 R2 在 R1 被 UI 执行前已读取 baseGen 或入队
	close(allowUIDrain)

	// Then 第 2 轮最新结果必须被应用，不能因第 1 轮先执行而被当作过期丢弃
	select {
	case <-applied2:
	case <-time.After(2 * time.Second):
		t.Fatalf("第 2 轮合并补扫被误丢弃：a.lib == lib1? %v, a.lib == lib2? %v", a.lib == lib1, a.lib == lib2)
	}

	a.stopWatch()
	close(uiQueue)
	<-uiDone
}

func TestAppDefersWatchScanDuringActiveInteractionAndFlushesLater(t *testing.T) {
	home := makeTempSkillHome(t)
	a := newApp(library.Scan(home))

	cases := []struct {
		name  string
		enter func()
		leave func()
	}{
		{"删除确认框", func() { a.del.confirmOpen = true }, func() { a.del.confirmOpen = false }},
		{"合并清理进行中", func() { a.tidy.work = &tidyWork{} }, func() { a.tidy.work = nil }},
		{"更新差异确认页", func() { a.upd.prepared = &library.Prepared{} }, func() { a.upd.prepared = nil }},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Given 用户正处于交互保护状态，收到一次后台监听扫描结果
			tc.enter()
			next := library.Sample()
			a.applyWatchScan(next, library.UpdateReport{}, a.watch.scanGen.Load())

			// Then 当前界面不被打断，新库暂存待刷新
			if a.lib == next {
				t.Fatalf("%s 期间不应立即替换 app.lib", tc.name)
			}
			if a.watch.pending == nil {
				t.Fatalf("%s 期间被延后的监听扫描结果应暂存", tc.name)
			}

			// When 交互状态结束，界面进入下一帧渲染
			tc.leave()
			_ = newUITester(a.view, 1280, 800)

			// Then 暂存的新库被自动套用
			if a.lib != next {
				t.Fatalf("%s 结束后下一帧应自动套用暂存结果", tc.name)
			}
			if a.watch.pending != nil {
				t.Fatalf("套用后 pending 应清空")
			}
		})
	}
}

func TestAppDiscardsStaleBackgroundWatchScan(t *testing.T) {
	// Given 监听扫描在 gen=0 时启动，在它落地前用户操作触发了 rescan()（gen 递增）
	home := makeTempSkillHome(t)
	a := newApp(library.Scan(home))
	staleGen := a.watch.scanGen.Load()

	// 模拟手动操作更新了技能库
	a.rescan()
	currentLib := a.lib

	// When 过期的后台扫描结果才回到主线程
	staleLib := library.Sample()
	a.applyWatchScan(staleLib, library.UpdateReport{}, staleGen)

	// Then 过期结果必须被丢弃，不能覆盖较新的 rescan 结果
	if a.lib != currentLib {
		t.Fatal("过期代际的后台监听结果不应覆盖较新的 rescan 结果")
	}
}

func TestAppCoalescesOverlappingWatchScans(t *testing.T) {
	// Given 不启动 FSEvents 流，纯粹验证 diskWatcher 调度在扫描重叠时的单飞与合并行为
	home := makeTempSkillHome(t)
	a := newApp(library.Scan(home))

	started := make(chan struct{}, 4)
	release := make(chan struct{})
	var scanCount atomic.Int32
	var postMu sync.Mutex

	a.watchCfg = &watchConfig{
		noFSWatch:      true,
		waitScanOnStop: true,
		scan: func(h string) (*library.Library, library.UpdateReport) {
			n := scanCount.Add(1)
			if n == 1 {
				started <- struct{}{}
				<-release
			}
			l := library.Scan(h)
			return l, l.Updates()
		},
		post: func(fn func()) {
			postMu.Lock()
			defer postMu.Unlock()
			fn()
		},
	}
	a.startWatch()
	defer a.stopWatch()

	// When 先触发第一轮扫描并卡在半路，期间再连续触发两次脏信号
	a.triggerWatchScan()
	<-started

	a.triggerWatchScan()
	a.triggerWatchScan()
	time.Sleep(40 * time.Millisecond)

	// 放行第一轮扫描，期间的多次脏信号应合并成恰好一次补扫
	close(release)

	waitUntil(t, 2*time.Second, "并发期间的脏信号合并为第 2 次扫描完成", func() bool {
		return scanCount.Load() >= 2
	})
	time.Sleep(60 * time.Millisecond)

	// Then 总共只扫了 2 次（首轮 + 合并后的补扫），不会并发重叠或多扫
	if got := scanCount.Load(); got != 2 {
		t.Fatalf("期望合并为 2 次扫描，实际扫描 %d 次", got)
	}
}

func TestAppSyncsNewAndRemovedTargetsAndStopsCleanly(t *testing.T) {
	// Given 已启动目录监听的 app，随后磁盘新增了一个外部软链技能目录
	home := makeTempSkillHome(t)
	a := newApp(library.Scan(home))
	var uiMu sync.Mutex
	a.watchCfg = &watchConfig{
		debounce:       40 * time.Millisecond,
		maxDelay:       120 * time.Millisecond,
		waitScanOnStop: true,
		post: func(fn func()) {
			uiMu.Lock()
			defer uiMu.Unlock()
			fn()
		},
	}
	a.startWatch()
	hub := a.watch.hub.Load()
	hub.waitSynced()

	extTarget := filepath.Join(home, "ext-dynamic-skill")
	must(t, os.MkdirAll(extTarget, 0o755))
	must(t, os.WriteFile(filepath.Join(extTarget, "SKILL.md"), []byte("---\nname: ext-dynamic-skill\ndescription: v1\n---\n"), 0o644))
	must(t, os.Symlink(extTarget, filepath.Join(home, ".agents/skills/ext-dynamic-skill")))

	// When 同步新的 WatchPlan 并随后移除该软链再次同步
	uiMu.Lock()
	a.useScan(library.Scan(home), library.UpdateReport{})
	uiMu.Unlock()
	hub.waitSynced()

	hub.mu.Lock()
	hasAdded := hub.dirs[extTarget] != nil
	hub.mu.Unlock()
	if !hasAdded {
		t.Fatalf("新增外部软链后应自动补充对应 watcher：%s", extTarget)
	}

	must(t, os.Remove(filepath.Join(home, ".agents/skills/ext-dynamic-skill")))
	uiMu.Lock()
	a.useScan(library.Scan(home), library.UpdateReport{})
	uiMu.Unlock()
	hub.waitSynced()

	hub.mu.Lock()
	stillHas := hub.dirs[extTarget] != nil
	hub.mu.Unlock()
	if stillHas {
		t.Fatalf("移除外部软链后应清理对应 watcher：%s", extTarget)
	}

	// And 调用 stopWatch 后清空全部活跃 watcher
	a.stopWatch()
	if a.watch.hub.Load() != nil {
		t.Fatal("stopWatch 后 hub 应置空")
	}
}

// zcodeWatchHome 复现本机布局：~/.zcode/skills/kami 软链到 ~/.claude/skills/kami，
// ~/.zcode/cli 里已有会话日志等目录。config 为空时不建配置文件。
func zcodeWatchHome(t *testing.T, config string) (home, configFile, skillMD string) {
	t.Helper()
	home = makeTempSkillHome(t)
	t.Setenv("HOME", home)
	real := filepath.Join(home, ".claude/skills/kami")
	must(t, os.MkdirAll(real, 0o755))
	must(t, os.WriteFile(filepath.Join(real, "SKILL.md"), []byte("---\nname: kami\ndescription: x\n---\n"), 0o644))
	must(t, os.MkdirAll(filepath.Join(home, ".zcode/skills"), 0o755))
	must(t, os.Symlink(real, filepath.Join(home, ".zcode/skills/kami")))
	configFile = filepath.Join(home, ".zcode/cli/config.json")
	must(t, os.MkdirAll(filepath.Join(home, ".zcode/cli/log"), 0o755))
	if config != "" {
		must(t, os.WriteFile(configFile, []byte(config), 0o600))
	}
	return home, configFile, filepath.Join(real, "SKILL.md")
}

// startZCodeWatch 开监听并返回加锁读 app 的办法和扫描次数。
func startZCodeWatch(t *testing.T, a *app) (with func(func(*app) bool) bool, scans *atomic.Int32) {
	t.Helper()
	var mu sync.Mutex
	scans = &atomic.Int32{}
	a.watchCfg = &watchConfig{
		debounce: 40 * time.Millisecond, maxDelay: 120 * time.Millisecond, waitScanOnStop: true,
		scan: func(h string) (*library.Library, library.UpdateReport) {
			scans.Add(1)
			l := library.Scan(h)
			return l, l.Updates()
		},
		post: func(fn func()) { mu.Lock(); defer mu.Unlock(); fn() },
	}
	a.startWatch()
	t.Cleanup(a.stopWatch)
	a.watch.hub.Load().waitSynced()
	return func(f func(*app) bool) bool { mu.Lock(); defer mu.Unlock(); return f(a) }, scans
}

// zcodeWrite 像 ZCode 一样写配置：先写临时文件再改名替换。
func zcodeWrite(t *testing.T, file, src string) {
	t.Helper()
	must(t, os.MkdirAll(filepath.Dir(file), 0o755))
	must(t, os.WriteFile(file+".tmp", []byte(src), 0o600))
	must(t, os.Rename(file+".tmp", file))
}

func zcodeOff(skillMD string) string {
	return `{"plugins":{},"skills":{"` + skillMD + `":{"enable":false}}}`
}

func kamiInZCode(on bool) func(*app) bool {
	return func(a *app) bool {
		s := a.lib.Get("kami")
		return s != nil && s.DiscoveredIn("zcode") && s.EnabledIn("zcode") == on
	}
}

func TestAppFollowsSkillTogglesMadeInZCode(t *testing.T) {
	// Given ZCode 读到 kami，配置里还没停用它
	home, config, skillMD := zcodeWatchHome(t, `{"plugins":{}}`)
	a := newApp(library.Scan(home))
	with, _ := startZCodeWatch(t, a)
	if !with(kamiInZCode(true)) {
		t.Fatal("开始时应启用")
	}
	// When 用户在 ZCode 里关掉它
	zcodeWrite(t, config, zcodeOff(skillMD))
	// Then 不用手动刷新，这里也显示已停用
	waitUntil(t, 4*time.Second, "ZCode 里的停用同步过来", func() bool { return with(kamiInZCode(false)) })
}

func TestAppFollowsZCodeWhenItCreatesItsFirstConfig(t *testing.T) {
	// Given ZCode 还从没拨过开关：~/.zcode/cli 里没有 config.json
	home, config, skillMD := zcodeWatchHome(t, "")
	a := newApp(library.Scan(home))
	with, _ := startZCodeWatch(t, a)
	if !with(kamiInZCode(true)) {
		t.Fatal("开始时应启用")
	}
	// When 用户在 ZCode 里第一次关掉一个技能，ZCode 建出配置
	zcodeWrite(t, config, zcodeOff(skillMD))
	// Then 这里也同步过来
	waitUntil(t, 4*time.Second, "首次建出的配置同步过来", func() bool { return with(kamiInZCode(false)) })
}

func TestAppFollowsZCodeConfigDeletedAndRecreated(t *testing.T) {
	// Given 配置里已停用 kami
	home, config, skillMD := zcodeWatchHome(t, "")
	must(t, os.WriteFile(config, []byte(zcodeOff(skillMD)), 0o600))
	a := newApp(library.Scan(home))
	with, _ := startZCodeWatch(t, a)
	if !with(kamiInZCode(false)) {
		t.Fatal("开始时应停用")
	}
	// When 配置被删掉：回到默认开启
	must(t, os.Remove(config))
	waitUntil(t, 4*time.Second, "删除配置后回到启用", func() bool { return with(kamiInZCode(true)) })
	// And 又重新建出停用它的配置
	zcodeWrite(t, config, zcodeOff(skillMD))
	// Then 仍能跟上
	waitUntil(t, 4*time.Second, "重新建出的配置同步过来", func() bool { return with(kamiInZCode(false)) })
}

func TestAppFollowsZCodeProjectConfigCreatedLater(t *testing.T) {
	// Given 项目 app 里有 ZCode 读的项目技能，项目里还没有任何 ZCode 配置
	home, _, _ := zcodeWatchHome(t, "{}")
	dir := filepath.Join(home, "code/app")
	skill := filepath.Join(dir, ".zcode/skills/deploy")
	must(t, os.MkdirAll(filepath.Join(dir, ".git"), 0o755))
	must(t, os.MkdirAll(skill, 0o755))
	must(t, os.WriteFile(filepath.Join(skill, "SKILL.md"), []byte("---\nname: deploy\ndescription: x\n---\n"), 0o644))
	must(t, library.Scan(home).AddProjects([]string{dir}))
	a := newApp(library.Scan(home))
	with, _ := startZCodeWatch(t, a)
	deploy := func(want library.StateKind) func(*app) bool {
		return func(a *app) bool {
			for _, s := range a.lib.Skills {
				if s.Source == library.Project && s.Name == "deploy" {
					st, ok := a.lib.ProjectAgentState(s, "zcode")
					return ok && st.Kind == want
				}
			}
			return false
		}
	}
	if !with(deploy(library.StateEnabled)) {
		t.Fatal("开始时应启用")
	}
	// When 项目根新建 zcode.json 停用它
	zcodeWrite(t, filepath.Join(dir, "zcode.json"), `{"skills":{"`+filepath.Join(skill, "SKILL.md")+`":{"enable":false}}}`)
	waitUntil(t, 4*time.Second, "项目根新建的 zcode.json 同步过来", func() bool { return with(deploy(library.StateDisabled)) })
	// And 再新建 .zcode/config.json 写坏
	zcodeWrite(t, filepath.Join(dir, ".zcode/config.json"), "{broken")
	waitUntil(t, 4*time.Second, "新建的 .zcode/config.json 同步过来", func() bool { return with(deploy(library.StateUnknown)) })
}

func TestAppIgnoresZCodeSessionNoiseWhileWaitingForItsConfig(t *testing.T) {
	// Given 还没有 config.json，正在等它出现
	home, _, _ := zcodeWatchHome(t, "")
	a := newApp(library.Scan(home))
	_, scans := startZCodeWatch(t, a)
	waitQuiet(scans)
	// When ZCode 会话不停写日志和别的文件
	for i := range 5 {
		must(t, os.WriteFile(filepath.Join(home, ".zcode/cli/log/session.log"), []byte(time.Now().String()), 0o644))
		must(t, os.WriteFile(filepath.Join(home, ".zcode/cli/state.json"), []byte{byte('0' + i)}, 0o644))
		time.Sleep(30 * time.Millisecond)
	}
	time.Sleep(350 * time.Millisecond)
	// Then 不触发全量重扫
	if got := scans.Load(); got != 0 {
		t.Fatalf("ZCode 的日志和其他文件不应触发重扫，实际扫描 %d 次", got)
	}
}

// waitQuiet 等启动前建目录留下的 FSEvents 静下来，再把计数清零。
func waitQuiet(scans *atomic.Int32) {
	deadline := time.Now().Add(3 * time.Second)
	last, stable := scans.Load(), time.Now()
	for time.Now().Before(deadline) {
		if cur := scans.Load(); cur != last {
			last, stable = cur, time.Now()
		} else if time.Since(stable) >= 250*time.Millisecond {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	scans.Store(0)
}
