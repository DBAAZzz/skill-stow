package main

import (
	"cmp"
	"path/filepath"
	"slices"
	"strings"

	"github.com/egoist/mygo/ui"

	"skillmanager/internal/library"
	"skillmanager/locales"
)

type gitMenuRow struct {
	skill *library.Skill
	state library.GitState
	files []library.GitFile
}

func gitMenuRows(p *library.ProjectInfo) []gitMenuRow {
	if p.Git == nil || p.Git.Check != library.GitReady {
		return nil
	}
	var rows []gitMenuRow
	for _, skill := range p.Skills {
		row := gitMenuRow{skill: skill}
		for _, install := range p.Git.Installs[skill.Dir] {
			if install.State == library.GitAdded {
				row.state, row.files = library.GitAdded, nil
				break
			}
			if install.State == library.GitModified {
				row.state, row.files = library.GitModified, install.Files
			}
		}
		if row.state == 0 {
			continue
		}
		rows = append(rows, row)
	}
	slices.SortFunc(rows, func(a, b gitMenuRow) int {
		return cmp.Or(cmp.Compare(a.state, b.state), strings.Compare(a.skill.Name, b.skill.Name))
	})
	return rows
}

func gitBadge(state library.GitState) func(*ui.Context) {
	return func(c *ui.Context) {
		ic, color := iconGitModified, colWarn
		if state == library.GitAdded {
			ic, color = iconGitAdded, colOK
		}
		ui.Box(c).Size(menuIconSize, menuIconSize).Center().Shrink(0).TextColor(color).Children(func() { ui.Icon(c, ic).Size(15, 15) })
	}
}

func gitRowOptions(name string, state library.GitState) []menuOpt {
	label := locales.GitRowModified(name)
	if state == library.GitAdded {
		label = locales.GitRowAdded(name)
	}
	return []menuOpt{withBadge(gitBadge(state)), withLabel(label)}
}

func (a *app) gitUncommittedMenu(c *ui.Context, p *library.ProjectInfo) {
	key := "git-uncommitted:" + p.Dir
	rows := gitMenuRows(p)
	if len(rows) == 0 {
		if menu := a.menus[key]; menu != nil {
			menu.open, menu.hi = false, false
		}
		return
	}
	a.triggerMenu(c.Key(key), key, menuPanelOpts{}, func(btn ui.Element, open bool) {
		btn.Height(22).Padding(0, 5, 0, 6).Margin(0, 0, 0, -6).Radius(6).Gap(3).Shrink(0).AlignItems(ui.Center).TextColor(colTextSecondary).Label(locales.GitUncommittedButton(len(rows)))
		if open {
			btn.Background(colInkSelected)
		} else {
			tintInk(btn)
		}
		btn.Children(func() {
			ui.Text(c, locales.GitUncommittedCount(len(rows))).FontSize(12).SingleLine()
			ui.Icon(c, iconChevronDown).Size(10, 10)
		})
	}, func(fm *frostedMenu) {
		for _, row := range rows[:min(len(rows), 8)] {
			opts := gitRowOptions(row.skill.Name, row.state)
			if len(row.files) > 1 {
				fm.submenu(row.skill.Name, locales.GitFileCount(len(row.files)), opts, menuPanelOpts{}, func(sub *frostedMenu) {
					for _, file := range row.files {
						if sub.action(file.Name, gitRowOptions(file.Name, file.State)...) {
							a.openInEditor(filepath.Join(row.skill.Dir, file.Name))
						}
					}
				})
				continue
			}
			path := row.skill.SkillFile()
			if len(row.files) == 1 {
				path = filepath.Join(row.skill.Dir, row.files[0].Name)
				opts = append(opts, withTrailing(row.files[0].Name))
			}
			if fm.action(row.skill.Name, opts...) {
				a.openInEditor(path)
			}
		}
		if len(rows) > 8 {
			fm.note(locales.GitUncommittedMore(len(rows) - 8))
		}
	})
}
