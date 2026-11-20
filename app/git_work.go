package main

import "skillmanager/internal/library"

// 新扫描换整份库；同一次扫描中重新进页面也要读到终端里刚做的提交。
func (a *app) syncPageGit() {
	page := a.here()
	if page == a.gitPage {
		return
	}
	a.gitPage = page
	if a.lib.Home == "" || a.reading != nil || a.cmp != nil {
		return
	}
	if p := a.project(); p != nil && len(p.Skills) > 0 {
		a.startProjectGit(p)
	}
}

func (a *app) startProjectGit(p *library.ProjectInfo) {
	lib := a.lib
	pending := &library.ProjectGit{Check: library.GitPending}
	p.Git = pending
	a.background(func(post func(func())) {
		result := p.CheckGit()
		post(func() {
			// 项目重扫会换库，重新进入页面会换 pending；两种晚到的结果都丢弃。
			if a.lib == lib && p.Git == pending {
				p.Git = result
			}
		})
	})
}
