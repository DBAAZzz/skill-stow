package main

import (
	"slices"
	"time"

	"skillmanager/internal/library"
	"skillmanager/locales"
)

// One operation owns its rows until the completion checks have had time to show.
// Keeping a snapshot also prevents memory-only operations from removing rows early.
type tidyWork struct {
	kind     library.Issue
	rows     library.Problems
	progress *tidyProgress
	doing    func(string) string
}

type tidyTask struct {
	id, name string
	run      func() error
}

type tidyOperation struct {
	kind   library.Issue
	tasks  []tidyTask
	doing  func(string) string
	result func(int) string
	// Only operations that leave the live library untouched may run on a worker.
	// They scan a new library before publishing the result on the UI thread.
	diskOnly bool
	after    func()
}

func (a *app) tidyProgressFor(kind library.Issue) *tidyProgress {
	if w := a.tidy.work; w != nil && w.kind == kind {
		return w.progress
	}
	return nil
}

func (a *app) runTidy(op tidyOperation) {
	if a.tidy.busy() || a.tidy.scanning || len(op.tasks) == 0 {
		return
	}
	p := a.lib.Problems()
	p.Same, p.Diff, p.Global = copyTidySkills(p.Same), copyTidySkills(p.Diff), copyTidySkills(p.Global)
	m := newTidyProgress()
	for _, task := range op.tasks {
		m.ids, m.names = append(m.ids, task.id), append(m.names, task.name)
	}
	w := &tidyWork{kind: op.kind, rows: p, progress: m, doing: op.doing}
	a.tidy.work, a.tidy.msg, a.tidy.err = w, "", ""
	a.dismissToast(fromTidy)
	a.dockProgress(0)
	lib, live := a.lib, a.live
	a.background(func(post func(func())) {
		// Let even a quick unlink paint its running state before the completion check.
		if live {
			time.Sleep(150 * time.Millisecond)
		}
		for _, task := range op.tasks {
			finish := func(err error) {
				m.done++
				a.dockProgress(float64(m.done) / float64(len(m.ids)))
				if err != nil {
					m.failed[task.id] = locales.NameColonError(task.name, err.Error())
					return
				}
				a.later(mergeRowHold, func() { m.gone[task.id] = true })
			}
			if op.diskOnly && lib.Home != "" {
				err := task.run()
				post(func() { finish(err) })
			} else {
				post(func() { finish(task.run()) })
			}
		}
		var next *library.Library
		var report library.UpdateReport
		if op.diskOnly && lib.Home != "" {
			next = library.Scan(lib.Home)
			report = next.Updates()
		}
		post(func() {
			a.later(mergeRowHold, func() {
				if a.tidy.work != w {
					return
				}
				a.tidy.work = nil
				a.dockProgress(-1)
				if next != nil {
					a.useScan(next, report)
				}
				if op.after != nil {
					op.after()
				}
				var errs []string
				for _, id := range m.ids {
					if err := m.failed[id]; err != "" {
						errs = append(errs, err)
					}
				}
				n := len(m.ids) - len(errs)
				if len(errs) > 0 {
					a.tidy.err = locales.TidyOperationFailed(n, len(errs), errs[0])
					a.notify(fromTidy, cond(n > 0, toastWarn, toastError), a.tidy.err)
					return
				}
				if msg := op.result(n); msg != "" {
					a.notify(fromTidy, toastOK, msg)
					a.chimeDone()
				}
			})
		})
	})
}

func copyTidySkills(skills []*library.Skill) []*library.Skill {
	result := make([]*library.Skill, len(skills))
	for i, s := range skills {
		copy := *s
		result[i] = &copy
	}
	return result
}

// Only the active table comes from the snapshot; other categories stay current.
func (a *app) tidyProblems() library.Problems {
	p := a.lib.Problems()
	w := a.tidy.work
	if w == nil {
		return p
	}
	gone := w.progress.gone
	skills := func(rows []*library.Skill) []*library.Skill {
		return slices.DeleteFunc(slices.Clone(rows), func(s *library.Skill) bool { return gone[s.ID] })
	}
	switch w.kind {
	case library.SameCopies:
		p.Same = skills(w.rows.Same)
	case library.DiffCopies:
		p.Diff = skills(w.rows.Diff)
	case library.ExtraLinks:
		p.Extra = slices.DeleteFunc(slices.Clone(w.rows.Extra), func(e library.ExtraLink) bool { return gone[e.Path] })
	case library.BrokenLinks:
		p.Broken = slices.DeleteFunc(slices.Clone(w.rows.Broken), func(b library.BrokenLink) bool { return gone[b.Path] })
	case library.GlobalLinked:
		p.Global = skills(w.rows.Global)
	case library.StaleRecords:
		p.Stale = slices.DeleteFunc(slices.Clone(w.rows.Stale), func(r library.StaleRecord) bool { return gone[r.Source] })
	}
	return p
}

func (a *app) dropExtraLinks(rows []library.ExtraLink) {
	lib := a.lib
	var tasks []tidyTask
	for _, e := range rows {
		tasks = append(tasks, tidyTask{e.Path, e.Skill.Name, func() error {
			return lib.DropLink(lib.ByID(e.Skill.ID), e.Agent)
		}})
	}
	a.runTidy(tidyOperation{kind: library.ExtraLinks, tasks: tasks,
		doing:  locales.TidyProcessingName,
		result: locales.TidyLinksDeleted})
}

func (a *app) repairBrokenLink(b library.BrokenLink) {
	lib := a.lib
	result := func(int) string { return locales.TidyBrokenDeleted(b.Name) }
	run := func() error { return lib.RemoveBroken(b.Path) }
	if b.Relink {
		result = func(int) string { return locales.TidyLinkReconnected(b.Name) }
		run = func() error { return lib.Relink(b.Path) }
	}
	a.runTidy(tidyOperation{kind: library.BrokenLinks, tasks: []tidyTask{{b.Path, b.Name, run}},
		doing: locales.TidyProcessingName, result: result})
}

type projectTidyAction int

const (
	keepProjectLinks projectTidyAction = iota
	removeProjectLinks
	collectProjectSkill
)

func (a *app) tidyProject(s *library.Skill, action projectTidyAction) {
	lib := a.lib
	// Rendered rows may be snapshots; mutations always target the live library.
	s = lib.ByID(s.ID)
	if s == nil {
		return
	}
	run := func() error { return lib.KeepLinks(s) }
	result := func(int) string { return locales.TidyLinksKept(s.Name) }
	if action == removeProjectLinks {
		run = func() error { return lib.Unglobal(s) }
		result = func(int) string { return locales.TidyProjectOnly(s.Name) }
	} else if action == collectProjectSkill {
		run = func() error { return lib.Collect(s) }
		result = func(int) string { return locales.TidyCollected(s.Name) }
	}
	a.runTidy(tidyOperation{kind: library.GlobalLinked, tasks: []tidyTask{{s.ID, s.Name, run}},
		doing:  locales.TidyProcessingName,
		result: result, diskOnly: action == collectProjectSkill})
}
