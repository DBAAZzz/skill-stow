package main

import (
	"fmt"
	"slices"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"skillmanager/internal/library"
	"skillmanager/locales"
)

// 整理页「全部收成一份」（清除安装记录也用这套进度，见 tidy.go）：后台一个个收，界面跟着走——按钮变形成环形进度，
// 正在收的行转圈，收好的行打勾、停一下再收起，数字往下滚，Dock 图标上
// 也有进度。系统要求减弱动态效果时，mygo 只淡入淡出。

// tidyProgress 是整理页一次批量处理的进度：收成一份、清除安装记录共用。
type tidyProgress struct {
	ids, names []string          // 要处理的行，按处理的顺序
	done       int               // 处理完了几行（没成功的也算）
	gone       map[string]bool   // 处理好、已经从表格收起的
	failed     map[string]string // 没成功的：原因
}

func newTidyProgress() *tidyProgress {
	return &tidyProgress{gone: map[string]bool{}, failed: map[string]string{}}
}

type rowState int

const (
	rowIdle    rowState = iota // 不在这次里
	rowWaiting                 // 等着
	rowRunning                 // 正在处理
	rowDone                    // 处理好了，打着勾
	rowFailed                  // 没成功
)

func (p *tidyProgress) state(id string) rowState {
	i := slices.Index(p.ids, id)
	switch {
	case i < 0:
		return rowIdle
	case p.failed[id] != "":
		return rowFailed
	case i < p.done:
		return rowDone
	case i == p.done:
		return rowRunning
	}
	return rowWaiting
}

// mergeRowHold 是收好的行打着勾停留多久再收起：看得见勾，又不拖沓。
const mergeRowHold = 650 * time.Millisecond

var (
	// morphSpring 是按钮变形成进度胶囊的弹簧，带一点回弹。
	morphSpring = ui.Animation{Spring: ui.SpringOptions{Duration: 450 * time.Millisecond, Bounce: 0.12}}
	// rowSpring 是行收起、下面的行补上来，不回弹。
	rowSpring = ui.Animation{Spring: ui.SpringOptions{Duration: 420 * time.Millisecond}}
	// popSpring 是对勾弹出来。
	popSpring = ui.Animation{Spring: ui.SpringOptions{Duration: 400 * time.Millisecond, Bounce: 0.35}}
	// swapSpring 是按钮、转圈、对勾之间互换。
	swapSpring = ui.Animation{Spring: ui.SpringOptions{Duration: 280 * time.Millisecond}}
)

// batchButtonMatch 让标题右边的批量按钮（「全部收成一份」「全部清除」）和
// 进度胶囊互为同一个东西：一个出现时从另一个的位置、大小、颜色变过来。
type batchButtonMatch struct{}

// 收成一份表格里的行：收起时淡出、高度收成零，其余的行跟着挪，底色渐变。
var mergeRowTransition = ui.ElementTransition{
	Animation: &rowSpring, Position: true, Colors: true,
	Exit: &ui.Motion{Opacity: 0, Collapse: true},
}

// 行末按钮、转圈、对勾互换：缩小、虚化、淡出，换上的反过来。
var swapTransition = ui.ElementTransition{
	Animation: &swapSpring,
	Enter:     &ui.Motion{Opacity: 0, Scale: 0.6, Blur: 2},
	Exit:      &ui.Motion{Opacity: 0, Scale: 0.6, Blur: 2},
}

// mergeAll 在后台把能收的都收成一份，收完重新检测一次。上百处副本要一处
// 一处移到废纸篓，要好几秒：放在界面线程上窗口会卡住，也看不到进度。
func (a *app) mergeAll(skills []*library.Skill) {
	a.mergeCopies(skills, 0, library.SameCopies)
}

func (a *app) mergeCopies(skills []*library.Skill, keep int, kind library.Issue) {
	lib := a.lib
	var tasks []tidyTask
	var total library.MergeResult
	for _, row := range skills {
		s := lib.ByID(row.ID)
		if s == nil || !lib.CanMerge(s) {
			continue
		}
		tasks = append(tasks, tidyTask{s.ID, s.Name, func() error {
			r, err := lib.Merge(s, keep)
			total.Add(r)
			return err
		}})
	}
	a.runTidy(tidyOperation{kind: kind, tasks: tasks, diskOnly: true,
		doing: locales.TidyMergingName,
		result: func(n int) string {
			if len(tasks) == 1 {
				return locales.MergeDoneOne(tasks[0].name, total.String())
			}
			return locales.MergeSkillResult(n, total.String())
		}})
}

// later 过 d 在界面线程上执行 fn 并重画。测试里没有窗口，马上执行（除非接管 delay）。
func (a *app) later(d time.Duration, fn func()) {
	if a.delay != nil {
		a.delay(d, func() {
			fn()
			a.changed()
		})
		return
	}
	if !a.live || d <= 0 {
		fn()
		return
	}
	time.AfterFunc(d, func() { a.post(fn) })
}

// dockProgress 在 Dock 图标上显示进度，切到别的应用也看得见；负数是收起。
func (a *app) dockProgress(v float64) {
	bar := mygo.ProgressBar{}
	if v >= 0 {
		bar = mygo.ProgressBar{
			State: mygo.ProgressNormal,
			Value: min(max(v, 0), 1),
		}
	}
	a.tidy.wantDock = bar
	a.applyDockProgress(a.progressWindow(), bar)
}

func (a *app) progressWindow() *mygo.Window {
	return a.dialogParent(false)
}

func (a *app) applyDockProgress(w *mygo.Window, bar mygo.ProgressBar) {
	if w != nil && !w.IsDestroyed() {
		w.SetProgressBar(bar)
		a.tidy.appliedDock = bar
		return
	}
	if !a.live && (a.main.open || a.set.open) {
		a.tidy.appliedDock = bar
	}
}

// syncDockProgress 在新建/重开窗口时将 Dock 进度对齐到当前业务期望状态。
func (a *app) syncDockProgress(w *mygo.Window) {
	if a.tidy.appliedDock != a.tidy.wantDock {
		a.applyDockProgress(w, a.tidy.wantDock)
	}
}

// clearDockProgressOnLastWindowClose 在最后一个打开的窗口即将销毁前（OnClose）
// 清除全局 Dock 进度条，避免所有窗口关闭后后台任务结束时因无窗口可调而残留进度。
func (a *app) clearDockProgressOnLastWindowClose(w *mygo.Window, otherOpen bool) {
	if otherOpen || a.tidy.appliedDock.State == mygo.ProgressNone {
		return
	}
	a.applyDockProgress(w, mygo.ProgressBar{})
}

// mergeAllButton 是「全部收成一份」；收的时候它变形成进度胶囊。
func (a *app) mergeAllButton(c *ui.Context, skills []*library.Skill) {
	if m := a.tidyProgressFor(library.SameCopies); m != nil {
		progressPill(c, m, locales.MergeCompleted())
		return
	}
	b := dialogButton(c, locales.MergeAll(), false).Shrink(0).
		Transition(ui.ElementTransition{Match: batchButtonMatch{}, Animation: &morphSpring})
	if a.tidy.busy() || a.tidy.scanning {
		b.Disabled(true)
	}
	if b.Clicked() {
		a.mergeAll(skills)
	}
}

// progressPill 是批量处理时的胶囊：环形进度和滚动的「3 / 13」，what 是读屏
// 念的「已收成一份 3 / 13」前半句。
func progressPill(c *ui.Context, m *tidyProgress, what string) {
	soft := colAccentBg
	label := fmt.Sprintf("%s %d / %d", what, m.done, len(m.ids))
	ui.Row(c).Height(28).Padding(0, 13, 0, 9).Gap(8).Radius(14).AlignItems(ui.Center).Shrink(0).
		Background(soft).TextColor(colAccent).Label(label).
		Transition(ui.ElementTransition{Match: batchButtonMatch{}, Animation: &morphSpring}).Children(func() {
		// 内容等胶囊变形到一半再淡进来
		late := ui.Animation{Duration: 220 * time.Millisecond, Delay: 120 * time.Millisecond}
		ui.Row(c).Gap(8).AlignItems(ui.Center).
			Transition(ui.ElementTransition{Animation: &late, Enter: &ui.Motion{Opacity: 0, Blur: 2}}).Children(func() {
			progressRing(c, float32(m.done)/float32(len(m.ids)), 15, colAccent, colAccent.Alpha(0.18), soft)
			ui.Row(c).AlignItems(ui.Baseline).Children(func() {
				ui.Text(c, fmt.Sprint(m.done)).FontSize(13).FontWeight(600).FontFeatures("tnum").SingleLine().
					ContentTransition(ui.ContentTransition{Numeric: true})
				ui.Text(c, fmt.Sprintf(" / %d", len(m.ids))).FontSize(13).FontWeight(500).FontFeatures("tnum").
					TextColor(colAccent.Alpha(0.6)).SingleLine()
			})
		})
	})
}

// progressRing 是环形进度，像 App Store 的下载按钮：v 从 0 到 1，弹簧追上去。
// hole 是中间挖空处的颜色，和底色一样。
func progressRing(c *ui.Context, v, size float32, color, track, hole ui.Color) {
	const width = 2.25
	ring := ui.Box(c).Size(size, size).Radius(size / 2).Shrink(0)
	v = ring.AnimateTo("progress", v, rowSpring)
	ring.ConicGradient(ui.ConicGradient{Stops: []ui.GradientStop{
		{Color: color, At: 0}, {Color: color, At: v}, {Color: track, At: v}, {Color: track, At: 1},
	}})
	ring.Children(func() {
		ui.Box(c).Absolute().Top(width).Left(width).Size(size-2*width, size-2*width).Radius(size/2 - width).Background(hole)
	})
}

// progressNote 是标题下面一行：正在处理哪个（doing 说出「正在收 brainstorming…」），
// 名字换的时候交叉淡入；都处理完了是 after。
func progressNote(c *ui.Context, m *tidyProgress, doing func(name string) string, after string) {
	text := after
	if m.done < len(m.ids) {
		text = doing(m.names[m.done])
	}
	ui.Text(c, text).FontSize(12).TextColor(colTextMuted).SingleLine().
		ContentTransition(ui.ContentTransition{}).
		Transition(ui.ElementTransition{Animation: &swapSpring, Enter: &ui.Motion{Opacity: 0, Y: -4}, Exit: &ui.Motion{Opacity: 0}})
}

// progressRowStyle 是批量处理表格的行：按 ID 认行，正在处理的那行淡蓝底。
func progressRowStyle(m *tidyProgress, id func(i int) string) *rowStyle {
	return &rowStyle{
		key: func(i int) any { return id(i) },
		apply: func(i int, row ui.Element) {
			row.Transition(mergeRowTransition)
			if m != nil && m.state(id(i)) == rowRunning {
				row.Background(colAccent.Alpha(0.07))
			}
		},
	}
}

func (a *app) sameRowStyle(skills []*library.Skill) *rowStyle {
	return progressRowStyle(a.tidyProgressFor(library.SameCopies), func(i int) string { return skills[i].ID })
}

// rowAction 是一行最右边那个按钮；批量处理时由 progressRowActions 换成转圈、对勾。
type rowAction struct {
	title         string // 按钮上的字，如「收成一份」
	running, done string // 读屏念的「正在收成一份」「已收成一份」
	disabled      string // 不为空时按钮点不了，悬停看原因
	run           func()
}

// progressRowActions 是一行最右边：平时是按钮；处理的时候是转圈、对勾，
// 没成功是红色的叹号，悬停看原因。m 是这一类的进度，nil 是没在处理。
func (a *app) progressRowActions(c *ui.Context, m *tidyProgress, id string, act rowAction) {
	if a.progressRowStatus(c, m, id, act.running, act.done) {
		return
	}
	state := rowIdle
	if m != nil {
		state = m.state(id)
	}
	ui.Row(c.Key("button")).AlignItems(ui.Center).Transition(swapTransition).Children(func() {
		b := pushButton(c, act.title)
		dim := float32(1)
		switch {
		case state == rowWaiting:
			b.Disabled(true)
			dim = 0.45
		case a.tidy.busy() || a.tidy.scanning:
			b.Disabled(true)
		case act.disabled != "":
			b.Disabled(true).Tooltip(act.disabled)
		}
		b.Opacity(b.AnimateTo("dim", dim, ui.Animation{Duration: 200 * time.Millisecond, NonMotion: true}))
		if b.Clicked() {
			act.run()
		}
	})
}

func (a *app) progressRowStatus(c *ui.Context, m *tidyProgress, id, running, done string) bool {
	state := rowIdle
	if m != nil {
		state = m.state(id)
	}
	slot := func(key string, fn func()) {
		ui.Row(c.Key(key)).AlignItems(ui.Center).Transition(swapTransition).Children(fn)
	}
	switch state {
	case rowRunning:
		slot("running", func() { ui.Spinner(c).Label(running) })
	case rowDone:
		slot("done", func() {
			ui.Box(c).Size(18, 18).Radius(9).Background(colOK).Center().TextColor(white).Label(done).
				Transition(ui.ElementTransition{Animation: &popSpring, Enter: &ui.Motion{Opacity: 0, Scale: 0.3}}).Children(func() {
				ui.Icon(c, iconCheck).Size(11, 11)
			})
		})
	case rowFailed:
		slot("failed", func() {
			ui.Icon(c, iconWarn).Size(15, 15).TextColor(colDanger).Label(m.failed[id]).Tooltip(m.failed[id])
		})
	default:
		return false
	}
	return true
}

// mergeRowActions 是收成一份表格一行最右边。
func (a *app) mergeRowActions(c *ui.Context, s *library.Skill) {
	act := rowAction{title: locales.TidyMergeRow(), running: locales.MergeInProgress(), done: locales.MergeCompleted(),
		run: func() { a.mergeAll([]*library.Skill{s}) }}
	if !a.lib.CanMerge(s) {
		act.disabled = locales.MergeLegacyLocationsUntouched()
	}
	a.progressRowActions(c, a.tidyProgressFor(library.SameCopies), s.ID, act)
}
