package main

import (
	"context"
	i18nerrors2 "errors"
	"fmt"

	"skillmanager/internal/library"
	"skillmanager/locales"
)

type updateWorkResult struct {
	err       error
	toastErr  bool      // true: err 用错误 Toast 显示；false: err 用页面顶部横幅显示
	toastKind toastKind // 非错误类的结果 Toast（成功 / 警告）
	toastText string
	apply     func() // 在主线程 post 中执行的界面收尾动作
}

type updateRunResult struct {
	single   bool
	itemName string
	updated  int
	failed   int
	warning  string
	err      error
}

func (a *app) canStartUpdateWork() bool {
	if a.upd.cancel != nil {
		return false
	}
	if a.lib.Home == "" {
		a.upd.err = locales.UpdateSampleNoRemote()
		return false
	}
	return true
}

// 后台只使用独立扫描的库；所有界面状态都通过 post 回主线程。
func (a *app) startUpdateWork(task updateTask, work func(context.Context, *library.Library, func(func())) updateWorkResult) {
	if !a.canStartUpdateWork() {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	a.dismissToast(fromUpdate)
	a.upd.cancel, a.upd.task, a.upd.err = cancel, task, ""
	a.upd.progress = library.UpdateProgress{Total: 1}
	home, settings := a.lib.Home, a.lib.Settings
	a.background(func(post func(func())) {
		l := library.Scan(home)
		l.Settings = settings
		res := work(ctx, l, post)
		// 在释放 ctx 前采样取消状态，避免 queued post 把收尾 cancel() 误判为用户取消。
		canceled := ctx.Err() != nil
		next := library.Scan(home)
		rep := next.Updates()
		post(func() {
			a.upd.cancel = nil
			a.upd.batch = nil
			a.useScan(next, rep)
			a.finishUpdateWork(res, canceled)
			a.ensureSourceIcons()
		})
		cancel()
	})
}

func (a *app) finishUpdateWork(res updateWorkResult, canceled bool) {
	if res.apply != nil {
		res.apply()
	}
	switch {
	case res.toastText != "":
		a.notify(fromUpdate, res.toastKind, res.toastText)
	case res.err != nil && !canceled:
		if res.toastErr {
			a.notify(fromUpdate, toastError, res.err.Error())
		} else {
			a.upd.err = res.err.Error()
		}
	}
}

func (a *app) recoverUpdates() {
	a.startUpdateWork(taskRestore, func(_ context.Context, l *library.Library, _ func(func())) updateWorkResult {
		return updateWorkResult{err: l.RecoverUpdates()}
	})
}

func (a *app) updateMark(fn func(*library.Library) error) {
	a.startUpdateWork(taskSave, func(_ context.Context, l *library.Library, _ func(func())) updateWorkResult {
		return updateWorkResult{err: fn(l), toastErr: true}
	})
}

func (a *app) checkUpdates(recheck bool) {
	if a.lib.Settings.UpdateProxyMode == "" {
		a.notify(fromUpdate, toastWarn, locales.UpdateChooseProxyFirst())
		a.openUpdateSettings()
		return
	}
	if !a.canStartUpdateWork() {
		return
	}
	a.upd.errors = nil
	a.upd.tabled = nil // 重新检查后结果会变，按新的行数重新决定表格还是单行
	a.startUpdateWork(taskCheck, func(ctx context.Context, l *library.Library, post func(func())) updateWorkResult {
		r := library.NewUpdateRemote(l.Settings)
		progress := func(p library.UpdateProgress) { post(func() { a.upd.progress = p }) }
		if recheck {
			return updateWorkResult{err: l.RecheckUpdates(ctx, r, progress)}
		}
		return updateWorkResult{err: l.CheckUpdates(ctx, r, progress)}
	})
}

func (a *app) prepareUpdate(it library.UpdateItem) {
	if !a.canStartUpdateWork() {
		return
	}
	delete(a.upd.errors, it.Name)
	a.startUpdateWork(taskDownload, func(ctx context.Context, l *library.Library, post func(func())) updateWorkResult {
		progress := func(done, total int) {
			post(func() { a.upd.progress = library.UpdateProgress{Done: done, Total: total} })
		}
		p, err := l.PrepareUpdateWithProgress(ctx, library.NewUpdateRemote(l.Settings), it, progress)
		if err != nil {
			return updateWorkResult{
				err:      fmt.Errorf("%s%w", locales.UpdateNamedErrorPrefix(it.Name), err),
				toastErr: true,
			}
		}
		diff := newUpdateCompareState(l, p)
		rep := a.scanPrepared(ctx, p)
		return updateWorkResult{apply: func() {
			a.upd.prepared = p
			a.upd.diff = diff
			a.sec.updFor, a.sec.updReport = p, rep
		}}
	})
}

func newUpdateCompareState(l *library.Library, p *library.Prepared) *compareState {
	return &compareState{
		files:    l.CompareUpdate(p),
		open:     map[int]bool{},
		updating: true,
		leftSub:  l.Display(p.Item.Place),
	}
}

func (a *app) runUpdates(items []library.UpdateItem, p *library.Prepared) {
	if a.lib.Settings.UpdateOldVersion == "" {
		a.notify(fromUpdate, toastWarn, locales.UpdateChooseOldVersionInSettings())
		a.openUpdateSettings()
		return
	}
	if len(items) == 0 || !a.canStartUpdateWork() {
		return
	}
	a.upd.errors = nil
	a.upd.batch = map[string]updateBatchStatus{}
	a.startUpdateWork(taskUpdate, func(ctx context.Context, l *library.Library, post func(func())) updateWorkResult {
		r := library.NewUpdateRemote(l.Settings)
		// 进度事件统一走库层：批量来自 ApplyUpdates，单条来自 ApplyUpdate，
		// 都汇到 applyUpdateProgress（行状态格和「正在更新 N/M」胶囊吃这些事件）。
		onProgress := func(pr library.UpdateProgress) {
			post(func() { a.applyUpdateProgress(pr) })
		}
		var res updateRunResult
		if p != nil || len(items) == 1 {
			res = runSingleUpdate(ctx, l, r, items[0], p, onProgress)
		} else {
			res = runBatchUpdates(ctx, l, r, items, onProgress)
		}
		return buildRunWorkResult(res, func() {
			a.upd.prepared = nil
			a.upd.diff = nil
		})
	})
}

func runSingleUpdate(ctx context.Context, l *library.Library, r library.Remote, it library.UpdateItem, p *library.Prepared, onProgress func(library.UpdateProgress)) updateRunResult {
	out, err := l.ApplyUpdate(ctx, r, it, p, onProgress)
	res := updateRunResult{
		single:   true,
		itemName: it.Name,
		warning:  out.Warning,
		err:      err,
	}
	if out.Updated {
		res.updated = 1
	} else if err != nil {
		res.failed = 1
	}
	return res
}

func runBatchUpdates(ctx context.Context, l *library.Library, r library.Remote, items []library.UpdateItem, onProgress func(library.UpdateProgress)) updateRunResult {
	onProgress(library.UpdateProgress{Done: 0, Total: len(items)})
	var failed int
	n, err := l.ApplyUpdates(ctx, r, items, func(pr library.UpdateProgress) {
		if !pr.Active && pr.Failure != "" {
			failed++
		}
		onProgress(pr)
	})
	return updateRunResult{
		updated: n,
		failed:  failed,
		err:     err,
	}
}

func (a *app) applyUpdateProgress(pr library.UpdateProgress) {
	a.upd.progress = pr
	if pr.Name == "" {
		return
	}
	if a.upd.batch == nil {
		a.upd.batch = map[string]updateBatchStatus{}
	}
	if a.upd.errors == nil {
		a.upd.errors = map[string]string{}
	}
	switch {
	case pr.Active:
		a.upd.batch[pr.Name] = batchActive
	case pr.Failure != "":
		a.upd.batch[pr.Name] = batchFailed
		a.upd.errors[pr.Name] = pr.Failure
	default:
		a.upd.batch[pr.Name] = batchDone
		delete(a.upd.errors, pr.Name)
	}
}

func buildRunWorkResult(res updateRunResult, closeCompare func()) updateWorkResult {
	out := updateWorkResult{apply: closeCompare}
	switch {
	case res.single && res.warning != "":
		out.toastKind, out.toastText = toastWarn, res.warning
	case res.single && res.updated == 1:
		out.toastKind, out.toastText = toastOK, locales.UpdateItemCompleted(res.itemName)
	case res.single && res.err != nil:
		out.err, out.toastErr = fmt.Errorf("%s%w", locales.UpdateItemErrorPrefix(res.itemName), res.err), true
	case res.updated > 0 && res.failed > 0:
		out.toastKind, out.toastText = toastWarn, locales.UpdateCompletedWithFailures(res.updated, res.failed)
	case res.updated == 0 && res.failed > 0:
		out.err, out.toastErr = i18nerrors2.New(locales.UpdateFailedCount(res.failed)), true
	case res.updated == 0 && res.err != nil:
		out.err, out.toastErr = res.err, true
	case res.updated > 0:
		out.toastKind, out.toastText = toastOK, locales.UpdateSkillsCompleted(res.updated)
	}
	return out
}

func (a *app) keepLocalUpdate(p *library.Prepared) {
	a.startUpdateWork(taskKeep, func(_ context.Context, l *library.Library, _ func(func())) updateWorkResult {
		if err := l.IgnoreUpdate(p.Item); err != nil {
			return updateWorkResult{err: err, toastErr: true}
		}
		l.DiscardPrepared(p)
		return updateWorkResult{apply: func() {
			a.upd.prepared = nil
			a.upd.diff = nil
		}}
	})
}

func (a *app) closeUpdateCompare() {
	if a.upd.prepared != nil && a.upd.cancel == nil {
		a.lib.DiscardPrepared(a.upd.prepared)
		a.upd.prepared = nil
		a.upd.diff = nil
	}
}
