package main

import (
	"context"
	"errors"
	"path/filepath"
	"slices"
	"strings"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/transfer"
	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"
	"github.com/egoist/mygo/ui/markdown"

	"skillmanager/internal/library"
	"skillmanager/locales"
)

// 「添加技能」的后台工作：读取（联网或复制）、补齐大仓库里勾选的技能、添加。
// 界面状态只在 post 里改。

// importPageState 是添加技能页的状态。
type importPageState struct {
	input   string
	err     *library.ImportError // 读取失败，显示在输入框下面
	cancel  context.CancelFunc   // 正在读取时非 nil
	reading string               // 正在读的来源（给人看的）
	prog    library.ImportProgress

	batch    *library.ImportBatch
	picked   map[string]bool
	replace  map[string]library.ImportCheck // 勾上的冲突项：用户确认替换时的那次检查，按 importKey
	focus    string                         // 右边预览的技能
	tab      int                            // 0 SKILL.md，1 文件
	file     int                            // 文件页选中的文件
	place    library.ImportPlacement
	placed   bool                           // place 是用户选过或沿用上次的
	checks   map[string]library.ImportCheck // 按当前 place 查的结果
	results  map[string]library.ImportOutcome
	fetching map[string]bool   // 大仓库里正在补齐的技能
	fetchErr map[string]string // 补齐失败的原因
	adding   bool
	cmp      *compareState
	// 后台还在用的批次（补齐、添加）。换来源时它们的暂存等最后一个任务结束
	// 再删，不在任务中途抽走；stop 让还在下载的补齐尽快停下。
	jobs    map[*library.ImportBatch]int
	dropped map[*library.ImportBatch]bool
	stop    context.CancelFunc
	ctx     context.Context
	// 预览用：每个技能的文件、打开过的只读编辑器、图片、Markdown 渲染选项与折叠态（按技能或路径），
	// SKILL.md 概览/文件标签页各自是否看原文，技能列表宽度拖动后待写进设置
	fileCache  map[string][]library.File
	editors    map[string]*editor.State
	images     map[string]ui.ImageSource
	mdOpts     map[string]*markdown.Options
	scanOpen   map[string]bool
	skillRaw   bool // SKILL.md 概览页右上角「查看原文 / 原文模式」
	raw        bool // 「文件」页右上角原文切换
	widthDirty bool // 冲突卡片的「对比差异」
	cmpName    string
	cmpKey     string // 对比的是哪个技能（importKey），对比页上的「用这份替换」用

	clip     string // 剪贴板里能识别的来源，输入框下面一行建议粘贴
	clipSeen string // 用户关掉或用过的，不再提示
}

// resetPreviewSelection 切换技能或进入新一批来源时重置右侧详情视图状态。
func (st *importPageState) resetPreviewSelection() {
	st.tab, st.file, st.skillRaw = 0, 0, false
}

// resetPreviewCaches 清空按当前批暂存文件衍生的编辑器、图片与 Markdown 渲染选项缓存。
func (st *importPageState) resetPreviewCaches() {
	st.fileCache, st.editors, st.images, st.mdOpts = nil, nil, nil, nil
}

// importPlacementDefault 是第一次添加时装到哪里：技能库，不另外建链接。
func importPlacementDefault() library.ImportPlacement { return library.ImportPlacement{Library: true} }

// openImport 打开添加技能页。
func (a *app) openImport() {
	a.navigate(func() {
		a.closeMenusIn(menuWinMain)
		a.scope, a.selected, a.reading, a.coll, a.inColl, a.cmp = scopeImport, nil, nil, nil, nil, nil
		a.exitBatch()
	})
	a.checkClipboard()
}

// openImportForProject 打开添加技能页，并把「装到」预设为指定项目。
func (a *app) openImportForProject(projectDir string) {
	a.openImport()
	a.setPlacement(library.ImportPlacement{
		Project: projectDir,
		Agents:  a.lib.ImportProjectDefaultAgents(),
	})
}

func (a *app) importProjectName(dir string) string {
	if p := a.lib.Project(dir); p != nil {
		return p.Name
	}
	return filepath.Base(dir)
}

func (a *app) chooseProjectForImport() {
	if !a.live {
		a.err = locales.ProjectsSampleCantAdd()
		return
	}
	parent := a.dialogParent(false)
	go func() {
		dirs, err := mygo.Dialog.Open(mygo.OpenDialogOptions{
			Parent: parent, Title: locales.NavAddProject(), ButtonLabel: locales.ProjectsDialogAdd(), Directory: true,
			Message: locales.ProjectsDialogMessage(),
		})
		if err != nil || len(dirs) == 0 {
			return
		}
		a.post(func() {
			dir, ok := a.lib.ValidProjectDir(dirs[0])
			if !ok {
				a.err = locales.ProjectInvalidFolder()
				a.changed()
				return
			}
			if err := a.lib.AddProjects([]string{dir}); err != nil {
				a.err = err.Error()
				a.changed()
				return
			}
			a.rescan()
			agents := a.imp.place.Agents
			if a.imp.place.Project == "" {
				agents = a.lib.ImportProjectDefaultAgents()
			}
			a.setPlacement(library.ImportPlacement{Project: dir, Agents: agents})
		})
	}()
}

// checkClipboard 读一次剪贴板：是能识别的链接或命令时，输入框下面提示粘贴。
func (a *app) checkClipboard() {
	if !a.live || !a.main.open || a.imp.batch != nil || a.imp.cancel != nil {
		return
	}
	b, err := mygo.Clipboard.ReadFormat(transfer.Text)
	if err != nil {
		return
	}
	a.offerClipboard(string(b))
}

// offerClipboard 判断剪贴板文字能不能读取，能就提示。
func (a *app) offerClipboard(text string) {
	text = strings.TrimSpace(text)
	if text == "" || len(text) > 2000 || text == a.imp.clipSeen || text == strings.TrimSpace(a.imp.input) {
		a.imp.clip = ""
		return
	}
	src, err := library.ParseImportInput(text)
	if err != nil || src.Kind != library.ImportGitHub && src.Kind != library.ImportWebsite {
		a.imp.clip = ""
		return
	}
	a.imp.clip = text
}

// clipLabel 是剪贴板提示里怎么称呼它：预选了技能写技能名，否则写来源。
func clipLabel(text string) string {
	src, err := library.ParseImportInput(text)
	if err != nil {
		return text
	}
	if len(src.Skills) == 1 {
		return src.Skills[0]
	}
	return src.Label()
}

// startImport 读取输入框里的来源。
func (a *app) startImport(text string) {
	st := &a.imp
	if st.cancel != nil {
		return
	}
	st.err = nil
	src, err := library.ParseImportInput(text)
	if err != nil {
		st.err = &library.ImportError{Msg: err.Error()}
		return
	}
	switch src.Kind {
	case library.ImportLocal:
		a.importPaths(src.Paths)
		return
	case library.ImportUnsupported:
		st.err = &library.ImportError{Msg: locales.ImportUnsupportedSource(), Hint: locales.ImportSupportedSourcesHint()}
		return
	}
	a.readRemote(src)
}

// readRemote 读一个 GitHub 或网站来源。
func (a *app) readRemote(src library.ImportSource) {
	if a.lib.Home == "" {
		a.imp.err = &library.ImportError{Msg: locales.ImportSampleNoNetwork()}
		return
	}
	a.runRead(src.Label(), func(ctx context.Context, l *library.Library, post func(func())) (*library.ImportBatch, error) {
		r := library.NewImportRemote(l.Settings)
		progress := func(p library.ImportProgress) { post(func() { a.imp.prog = p }) }
		if src.Kind == library.ImportWebsite {
			return library.ReadWebsiteImport(ctx, l.Home, r.Web, src, progress)
		}
		return library.ReadGitHubImport(ctx, l.Home, r, src, progress)
	})
}

// showAllImport 指定了技能、只读了那几个时，重新读整个仓库，仍勾上指定的。
func (a *app) showAllImport() {
	b := a.imp.batch
	if b == nil || b.Hidden == 0 || a.imp.cancel != nil || a.imp.adding {
		return
	}
	src := b.Source
	src.All = true
	a.readRemote(src)
}

// importPaths 读取拖入或选取的文件夹、zip。
func (a *app) importPaths(paths []string) {
	if len(paths) == 0 || a.imp.cancel != nil {
		return
	}
	if a.scope != scopeImport {
		a.openImport()
	}
	a.imp.err = nil
	if a.lib.Home == "" {
		a.imp.err = &library.ImportError{Msg: locales.ImportSampleCannotAdd()}
		return
	}
	label := library.ImportSource{Kind: library.ImportLocal, Paths: paths}.Label()
	if len(paths) > 1 {
		label = locales.ImportLocalSourceCount(len(paths))
	}
	a.imp.input = strings.Join(paths, " ")
	a.runRead(label, func(ctx context.Context, l *library.Library, _ func(func())) (*library.ImportBatch, error) {
		return library.ReadLocalImport(l.Home, paths)
	})
}

// runRead 在后台读取，读完进入选择页。
func (a *app) runRead(label string, read func(context.Context, *library.Library, func(func())) (*library.ImportBatch, error)) {
	st := &a.imp
	a.discardImport()
	ctx, cancel := context.WithCancel(context.Background())
	st.cancel, st.reading, st.prog, st.clip = cancel, label, library.ImportProgress{}, ""
	lib := a.lib
	a.background(func(post func(func())) {
		b, err := read(ctx, lib, post)
		canceled := ctx.Err() != nil
		cancel()
		post(func() {
			st.cancel, st.reading = nil, ""
			if canceled {
				b.Discard()
				return
			}
			if err != nil {
				var ie *library.ImportError
				if !errors.As(err, &ie) {
					ie = &library.ImportError{Msg: err.Error()}
				}
				st.err = ie
				return
			}
			a.showBatch(b)
		})
	})
}

// stopImport 停止读取。
func (a *app) stopImport() {
	if a.imp.cancel != nil {
		a.imp.cancel()
	}
}

// showBatch 进入选择页：按来源预选；只有一个技能时默认勾上。
func (a *app) showBatch(b *library.ImportBatch) {
	st := &a.imp
	st.batch, st.picked, st.replace, st.results, st.scanOpen = b, map[string]bool{}, map[string]library.ImportCheck{}, nil, nil
	st.resetPreviewSelection()
	st.fetching, st.fetchErr = map[string]bool{}, map[string]string{}
	st.ctx, st.stop = context.WithCancel(context.Background())
	if !st.placed {
		st.place = importPlacementDefault()
	}
	a.refreshImportChecks()
	pre := b.Preselected()
	if len(b.Skills) == 1 {
		pre = []string{b.Skills[0].Name}
	}
	for _, n := range pre {
		if st.checks[n].State == library.ImportNew {
			a.setImportPicked(n, true)
		}
	}
	st.focus = ""
	if len(pre) > 0 {
		st.focus = pre[0]
	} else if len(b.Skills) > 0 {
		st.focus = b.Skills[0].Name
	}
	switch {
	case a.iconFetch == nil:
	case b.Source.Kind == library.ImportGitHub:
		owner, _, _ := strings.Cut(b.Source.Repo, "/")
		a.scheduleSourceIcon("gh:"+owner, func(ctx context.Context) error {
			_, err := a.iconFetch.FetchAvatar(ctx, owner)
			return err
		})
	case b.Source.Kind == library.ImportWebsite:
		a.scheduleSourceIcon(b.Site, func(ctx context.Context) error {
			_, err := a.iconFetch.Fetch(ctx, b.BaseURL, b.Site)
			return err
		})
	}
}

// discardImport 放下这次读取，删掉暂存，回到输入页。
func (a *app) discardImport() {
	st := &a.imp
	if st.stop != nil {
		st.stop()
	}
	if b := st.batch; b != nil {
		if st.jobs[b] > 0 {
			if st.dropped == nil {
				st.dropped = map[*library.ImportBatch]bool{}
			}
			st.dropped[b] = true
		} else {
			b.Discard()
		}
	}
	st.batch, st.picked, st.replace, st.results, st.checks, st.cmp, st.scanOpen = nil, nil, nil, nil, nil, nil, nil
	st.fetching, st.fetchErr, st.stop, st.ctx = nil, nil, nil, nil
	st.resetPreviewSelection()
	st.resetPreviewCaches()
}

// holdBatch 记下后台任务开始用 b；任务结束（回到主线程）时调 releaseBatch。
func (a *app) holdBatch(b *library.ImportBatch) {
	st := &a.imp
	if st.jobs == nil {
		st.jobs = map[*library.ImportBatch]int{}
	}
	st.jobs[b]++
}

// releaseBatch 结束一个用 b 的后台任务；b 已被放下且没有别的任务时删掉它的暂存。
func (a *app) releaseBatch(b *library.ImportBatch) {
	st := &a.imp
	if st.jobs[b]--; st.jobs[b] > 0 {
		return
	}
	delete(st.jobs, b)
	if st.dropped[b] {
		delete(st.dropped, b)
		b.Discard()
	}
}

// importKey 是批次里一个技能在页面上的身份：勾选、检查结果、添加结果、
// 预览都按它记。一般就是名称；同一来源里重名的，第二个起（库层已标为不能
// 添加）另带暂存目录，免得和第一个共用状态、把能添加的那个也连带禁用。
func (a *app) importKey(s *library.ImportSkill) string {
	if b := a.imp.batch; b == nil || b.Skill(s.Name) == s {
		return s.Name
	}
	return s.Name + "\x00" + s.Dir
}

// importByKey 按 importKey 找技能。
func (a *app) importByKey(k string) *library.ImportSkill {
	if b := a.imp.batch; b != nil {
		for _, s := range b.Skills {
			if a.importKey(s) == k {
				return s
			}
		}
	}
	return nil
}

// refreshImportChecks 按当前装到哪里重新查每个技能的情况。
func (a *app) refreshImportChecks() {
	st := &a.imp
	st.checks = map[string]library.ImportCheck{}
	st.resetPreviewCaches() // 补齐下载后文件与本地图片映射都可能变化
	if st.batch == nil {
		return
	}
	for _, s := range st.batch.Skills {
		st.checks[a.importKey(s)] = a.lib.CheckImport(s, st.place)
	}
	// 勾上的冲突项只在已有的那一项还是确认时的样子时保留：换了装到哪里、
	// 或已有的变了，都要用户重新确认
	for n := range st.picked {
		chk := st.checks[n]
		if chk.State == library.ImportNew {
			delete(st.replace, n)
			continue
		}
		if r, ok := st.replace[n]; ok && chk.State == library.ImportConflict && chk.SameAs(r) {
			continue
		}
		delete(st.picked, n)
		delete(st.replace, n)
	}
}

// replaceable 报告这个检查结果是能替换的冲突。
func replaceable(chk library.ImportCheck) bool {
	return chk.State == library.ImportConflict && chk.Blocked == ""
}

// setImportReplace 确认或撤回用新版替换已有的同名项。确认时重新检查一次，
// 记下这次看到的已有项；添加时它变了就不替换。添加时冲突了的技能也从这里
// 重新决定。
func (a *app) setImportReplace(k string, on bool) {
	st := &a.imp
	if !on {
		delete(st.replace, k)
		a.setImportPicked(k, false)
		return
	}
	sk := a.importByKey(k)
	if sk == nil || st.adding {
		return
	}
	if o, done := st.results[k]; done && !o.Added {
		delete(st.results, k)
	}
	chk := a.lib.CheckImport(sk, st.place)
	st.checks[k] = chk
	switch {
	case chk.State == library.ImportNew: // 已有的不在了：照常添加
		delete(st.replace, k)
		a.setImportPicked(k, true)
	case replaceable(chk):
		st.replace[k] = chk
		a.setImportPicked(k, true)
	}
}

// importReplacing 报告这个技能勾上了、添加时会替换已有的同名项。
func (a *app) importReplacing(k string) bool {
	_, ok := a.imp.replace[k]
	return ok && a.imp.picked[k]
}

// setPlacement 换装到哪里：记住作为下次的默认，重新查冲突。
func (a *app) setPlacement(p library.ImportPlacement) {
	if old := a.imp.place; old.Library != p.Library || old.Project != p.Project || !slices.Equal(old.Agents, p.Agents) {
		a.imp.results = nil
	}
	a.imp.place, a.imp.placed = p, true
	a.refreshImportChecks()
}

// setImportPicked 勾上或取消；大仓库里只拿了 SKILL.md 的，勾上时在后台补齐。
func (a *app) setImportPicked(name string, on bool) {
	st := &a.imp
	if !on {
		delete(st.picked, name)
		return
	}
	st.picked[name] = true
	sk := a.importByKey(name)
	if sk == nil || !sk.Partial || st.fetching[name] {
		return
	}
	st.fetching[name] = true
	delete(st.fetchErr, name)
	b, ctx := st.batch, st.ctx
	a.holdBatch(b)
	a.background(func(post func(func())) {
		err := b.FetchImportSkill(ctx, sk.Name)
		post(func() {
			defer a.releaseBatch(b)
			if st.batch != b {
				return // 已经换了来源：结果和现在页面上的同名技能无关
			}
			delete(st.fetching, name)
			if err != nil {
				st.fetchErr[name] = err.Error()
				delete(st.picked, name)
			}
			a.refreshImportChecks()
		})
	})
}

// importAddable 是勾上、能添加的技能，按列表顺序。
func (a *app) importAddable() []string {
	st := &a.imp
	var out []string
	if st.batch == nil {
		return nil
	}
	for _, s := range st.batch.Skills {
		k := a.importKey(s)
		if !st.picked[k] || st.results[k].Name != "" {
			continue
		}
		if st.checks[k].State == library.ImportNew || a.importReplacing(k) {
			out = append(out, s.Name)
		}
	}
	return out
}

// importReplacements 是勾上的技能里要替换的，值是确认时的检查，交给 AddImport。
func (a *app) importReplacements() map[string]library.ImportCheck {
	out := map[string]library.ImportCheck{}
	for _, n := range a.importAddable() {
		if c, ok := a.imp.replace[n]; ok {
			out[n] = c
		}
	}
	return out
}

// importPending 报告勾上的技能里还有没补齐的。
func (a *app) importPending() bool {
	for _, n := range a.importAddable() {
		if sk := a.imp.batch.Skill(n); sk != nil && sk.Partial {
			return true
		}
	}
	return false
}

// addImports 把勾上的技能装进去。本机复制，按下即完成。
func (a *app) addImports() {
	st := &a.imp
	names := a.importAddable()
	if len(names) == 0 || st.adding || a.importPending() {
		return
	}
	st.adding = true
	b, p, home, replace := st.batch, st.place, a.lib.Home, a.importReplacements()
	a.holdBatch(b)
	a.background(func(post func(func())) {
		l := library.Scan(home)
		out := l.AddImport(b, names, p, replace)
		next := library.Scan(home)
		rep := next.Updates()
		post(func() {
			defer a.releaseBatch(b)
			st.adding = false
			a.useScan(next, rep)
			current := st.batch == b && st.place.Project == p.Project // 添加期间换了来源时，结果只发通知，不写进新来源的页面
			if current && st.results == nil {
				st.results = map[string]library.ImportOutcome{}
			}
			added, replaced, failed := 0, 0, 0
			var addedNames []string
			for _, o := range out {
				if current {
					st.results[o.Name] = o
					delete(st.picked, o.Name)
					delete(st.replace, o.Name)
				}
				if o.Replaced {
					replaced++
				}
				if o.Added {
					added++
					addedNames = append(addedNames, o.Name)
				} else {
					failed++
				}
			}
			if current {
				a.refreshImportChecks()
				if i := slices.IndexFunc(out, func(o library.ImportOutcome) bool { return !o.Added }); i >= 0 {
					st.focus = out[i].Name
				}
			}
			switch {
			case failed == 0:
				first := out[0]
				text := locales.ImportAddedNames(added, strings.Join(addedNames, locales.ListSeparator()))
				if replaced > 0 {
					text = locales.ImportAddedReplacedNames(added, replaced, strings.Join(addedNames, locales.ListSeparator()))
				}
				a.showToast(&toast{source: fromImport, kind: toastOK, text: text,
					action: &toastAction{label: locales.ActionView(), run: func() { a.showImported(first.Name, p.Project, first.Place) }}})
			case added == 0:
				a.notify(fromImport, toastWarn, locales.ImportFailedSkillCount(failed))
			default:
				a.notify(fromImport, toastWarn, locales.ImportAddedAndFailed(added, failed))
			}
		})
	})
}

// chooseImport 用系统面板选取文件夹或 zip。
func (a *app) chooseImport() {
	win := a.win
	if win == nil || win.IsDestroyed() {
		return
	}
	go func() {
		paths, err := openImportDialog(win, defaultImportDialogOptions())
		if err != nil || len(paths) == 0 {
			return
		}
		a.post(func() { a.importPaths(paths) })
	}()
}

// dropToImport 是拖到窗口任何地方的文件夹或 zip：切到添加技能页读取，不直接安装。
func (a *app) dropToImport(paths []string) {
	if len(paths) == 0 || !library.LocalSkillCandidates(paths) {
		return
	}
	if a.imp.cancel != nil {
		return
	}
	a.openImport()
	a.importPaths(paths)
}

// showImported 去「我的技能」或目标项目选中刚添加的技能。
func (a *app) showImported(name, project, place string) {
	a.navigate(func() {
		a.closeMenusIn(menuWinMain)
		a.inColl, a.coll, a.reading, a.cmp, a.selected = nil, nil, nil, nil, nil
		if project != "" {
			if p := a.lib.Project(project); p != nil {
				a.scope, a.projectsOpen = projectScope(p), true
				for _, s := range p.Skills {
					if library.SamePlace(s.Real, place) {
						a.selected = s
						break
					}
				}
				return
			}
		}
		a.scope = string(library.ScopeMine)
		if s := a.lib.Get(name); s != nil && s.Source == library.Mine && project == "" {
			a.selected = s
		}
	})
}
