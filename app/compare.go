package main

import (
	"fmt"
	"path"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"

	"skillmanager/internal/library"
	"skillmanager/locales"
)

// compareState 是对比副本 / 对比更新页的状态。
type compareState struct {
	updating bool
	fullFile bool // true 时展开所有未改动行，方便通读全文
	leftSub  string
	rightSub string
	skill    *library.Skill
	other    int                // 右边对比的那种内容（Versions 的下标，基准是 0）
	file     int                // 看的是第几个有差异的文件
	open     map[int]bool       // 展开了的折叠段，按段开头那一行
	tidy     bool               // 从整理页打开的：保留后回到整理页
	files    []library.FileDiff // 基准和 other 有差异的文件
}

// 改动的上下各留几行没改动的，再多的折叠起来。
const diffContext = 3

// openCompare 打开对比副本页：基准对第二种内容。
func (a *app) openCompare(s *library.Skill, fromTidy bool) {
	if len(s.Versions()) < 2 {
		return
	}
	a.navigate(func() { a.showCompare(s, fromTidy) })
}

// showCompare 显示对比副本页，右边先对比第二种内容。
func (a *app) showCompare(s *library.Skill, fromTidy bool) {
	a.cmp = &compareState{skill: s, tidy: fromTidy}
	a.compareWith(1)
}

// closeCompare 从对比副本页回到打开它的那一页。
func (a *app) closeCompare() {
	a.navigateUp(func() { a.cmp = nil })
}

// compareWith 换右边对比的那种内容。
func (a *app) compareWith(k int) {
	st := a.cmp
	vs := st.skill.Versions()
	st.other, st.file, st.open = k, 0, map[int]bool{}
	st.files = a.lib.Compare(&vs[0], &vs[k])
}

// comparePage 是对比副本页（td3-b）：顶栏右侧切要对比的副本，左边是差异文件树，
// 右边顶部对齐展示双方信息与「保留这个版本」按钮，下面是文件名与并排逐行对比。
func (a *app) comparePage(c *ui.Context, st *compareState) {
	s := st.skill
	vs := s.Versions()
	a.toolbarRow(c, func() {
		if iconButton(c, iconBack, locales.ActionBackShortcut()).Clicked() {
			a.closeCompare()
		}
		ui.Row(c).Gap(8).AlignItems(ui.Baseline).Children(func() {
			ui.Text(c, locales.CompareCopiesOf(s.Name)).FontSize(13).FontWeight(600).SingleLine()
			ui.Text(c, locales.CompareCopiesAndVersions(s.Copies+1, len(vs))).FontSize(12).TextColor(colTextMuted).FontFeatures("tnum").SingleLine()
		})
	}, func() {
		if len(vs) > 2 {
			a.compareVersionSwitcher(c, st, vs)
		} else {
			diffModeSwitch(c, &st.fullFile)
		}
	})
	if st.other >= len(vs) {
		return
	}
	ui.Row(c).Grow(1).MinHeight(0).AlignItems(ui.Stretch).Children(func() {
		diffFileTree(c, st)
		ui.Column(c).Grow(1).MinWidth(0).Children(func() {
			diffVerBar(c, func() {
				a.localVerSide(c, st, 0)
			}, func() {
				a.localVerSide(c, st, st.other)
			})
			a.diffFileContent(c, st, nil)
		})
	})
}

func (a *app) compareVersionSwitcher(c *ui.Context, st *compareState, vs []library.Version) {
	ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
		ui.Text(c, locales.ActionCompareCopies()).FontSize(12).TextColor(colTextMuted).SingleLine()
		seg := ui.Row(c).Height(26).Padding(2).Gap(1).Radius(8).Background(colBgFill).AlignItems(ui.Center)
		seg.Children(func() {
			for k := 1; k < len(vs); k++ {
				v := &vs[k]
				active := k == st.other
				diffCount := len(st.files)
				if !active {
					diffCount = len(a.lib.Compare(&vs[0], v))
				}
				name := versionDisplayName(v, k)
				btn := ui.ButtonBase(c.Key(k)).Height(22).Padding(0, 8).Gap(5).Radius(6).
					AlignItems(ui.Center).Shrink(0).Label(name)
				if active {
					btn.Background(colBgThumb).Shadow(0, 1, 2, 0, shadow(0.12))
				} else {
					tintBg(btn, ui.Color{}, colBgThumb.Alpha(0.45), colBgThumb.Alpha(0.7))
				}
				if btn.Clicked() {
					a.compareWith(k)
				}
				btn.Children(func() {
					versionBadge(c, v, k, 14)
					t := ui.Text(c, name).FontSize(13).SingleLine()
					if active {
						t.FontWeight(500).TextColor(colTextPrimary)
					} else {
						t.TextColor(colTextSecondary)
					}
					ui.Text(c, fmt.Sprintf("(%d)", diffCount)).FontSize(11).TextColor(colTextMuted).FontFeatures("tnum").SingleLine()
				})
			}
		})
	})
}

func diffModeSwitch(c *ui.Context, fullFile *bool) {
	seg := ui.Row(c).Height(26).Padding(2).Gap(1).Radius(8).Background(colBgFill).AlignItems(ui.Center)
	seg.Children(func() {
		for _, opt := range []struct {
			label string
			full  bool
		}{
			{locales.CompareChangesOnly(), false},
			{locales.CompareFullText(), true},
		} {
			active := *fullFile == opt.full
			btn := ui.ButtonBase(c.Key(opt.label)).Height(22).Padding(0, 9).Radius(6).
				AlignItems(ui.Center).Shrink(0).Label(opt.label)
			if active {
				btn.Background(colBgThumb).Shadow(0, 1, 2, 0, shadow(0.12))
			} else {
				tintBg(btn, ui.Color{}, colBgThumb.Alpha(0.45), colBgThumb.Alpha(0.7))
			}
			if btn.Clicked() {
				*fullFile = opt.full
			}
			btn.Children(func() {
				t := ui.Text(c, opt.label).FontSize(12).SingleLine()
				if active {
					t.FontWeight(500).TextColor(colTextPrimary)
				} else {
					t.TextColor(colTextSecondary)
				}
			})
		}
	})
}

type diffDirGroup struct {
	dir   string
	files []diffIndexedFile
}

type diffIndexedFile struct {
	idx  int
	name string
	diff library.FileDiff
}

func groupDiffFiles(files []library.FileDiff) []diffDirGroup {
	var root []diffIndexedFile
	var dirs []string
	byDir := map[string][]diffIndexedFile{}
	for i, f := range files {
		d, name := path.Split(f.Path)
		d = strings.TrimSuffix(d, "/")
		item := diffIndexedFile{idx: i, name: name, diff: f}
		if d == "" {
			root = append(root, item)
			continue
		}
		if _, ok := byDir[d]; !ok {
			dirs = append(dirs, d)
		}
		byDir[d] = append(byDir[d], item)
	}
	var out []diffDirGroup
	if len(root) > 0 {
		out = append(out, diffDirGroup{files: root})
	}
	for _, d := range dirs {
		out = append(out, diffDirGroup{dir: d, files: byDir[d]})
	}
	return out
}

// diffFileTree 是左侧固定宽度的差异文件目录树（td3-b）。
func diffFileTree(c *ui.Context, st *compareState) {
	var totalDel, totalAdd int
	for _, f := range st.files {
		totalDel += f.Deleted
		totalAdd += f.Added
	}
	ui.Scroll(c).Width(236).Shrink(0).Background(colBgPane).BorderWidth(0, 1, 0, 0).BorderColor(colBorderDefault).
		Scrollbars(false).Children(func() {
		ui.Column(c).Padding(12, 8).Gap(1).Children(func() {
			ui.Row(c).Padding(2, 8, 6, 8).Gap(6).AlignItems(ui.Center).Children(func() {
				ui.Text(c, locales.CompareChangedFiles(len(st.files))).FontSize(11).FontWeight(600).
					TextColor(colTextMuted).FontFeatures("tnum").SingleLine()
				ui.Spacer(c)
				if totalDel > 0 || totalAdd > 0 {
					ui.Row(c).Gap(3).AlignItems(ui.Center).Children(func() {
						if totalDel > 0 {
							ui.Text(c, fmt.Sprintf("−%d", totalDel)).FontSize(11).TextColor(colDiffDelText).FontFeatures("tnum").SingleLine()
						}
						if totalAdd > 0 {
							ui.Text(c, fmt.Sprintf("+%d", totalAdd)).FontSize(11).TextColor(colDiffAddText).FontFeatures("tnum").SingleLine()
						}
					})
				}
			})
			for _, grp := range groupDiffFiles(st.files) {
				if grp.dir != "" {
					ui.Row(c.Key("dir/"+grp.dir)).Height(25).Padding(0, 8).Gap(6).Margin(4, 0, 0, 0).
						AlignItems(ui.Center).Children(func() {
						ui.Icon(c, iconFolderFill).Size(13, 13).TextColor(colAccent)
						ui.Text(c, grp.dir).FontSize(12).FontWeight(500).TextColor(colTextSecondary).SingleLine().MinWidth(0)
					})
				}
				leftPad := float32(8)
				if grp.dir != "" {
					leftPad = 22
				}
				for _, item := range grp.files {
					f := item.diff
					idx := item.idx
					selected := idx == st.file
					row := ui.ButtonBase(c.Key(f.Path)).Height(26).Padding(0, 8, 0, leftPad).Gap(6).Radius(6).
						AlignItems(ui.Center).Label(f.Path).Tooltip(f.Path)
					selectableRow(row, selected, colInkSelected)
					if row.Clicked() {
						st.file, st.open = idx, map[int]bool{}
					}
					row.Children(func() {
						ui.Icon(c, iconDoc).Size(13, 13).TextColor(colTextMuted)
						nameTxt := ui.Text(c, item.name).FontSize(13).SingleLine().Grow(1).MinWidth(0)
						if selected {
							nameTxt.FontWeight(500).TextColor(colTextPrimary)
						} else {
							nameTxt.TextColor(colTextPrimary)
						}
						diffTreeStatus(c, st.updating, f)
					})
				}
			}
		})
	})
}

func diffTreeStatus(c *ui.Context, updating bool, f library.FileDiff) {
	switch {
	case f.OnlyLeft:
		if updating {
			diffMiniTag(c, locales.CompareLocalOnly(), colWarnBg, colWarnText, locales.CompareLocalFileRemovedHint())
		} else {
			diffMiniTag(c, locales.CompareBaselineOnly(), colBgFill, colTextSecondary, "")
		}
	case f.OnlyRight:
		if updating {
			diffMiniTag(c, locales.CompareAddedUpstream(), colOKBg, colDiffAddText, "")
		} else {
			diffMiniTag(c, locales.CompareCopyOnly(), colBgFill, colTextSecondary, "")
		}
	case f.ModeOnly:
		diffMiniTag(c, locales.ComparePermissions(), colBgFill, colTextSecondary, "")
	case f.Left.Unreadable || f.Right.Unreadable:
		diffMiniTag(c, locales.CommonUnreadable(), colDiffDelRow, colDiffDelText, "")
	case f.Binary:
		diffMiniTag(c, locales.CompareBinary(), colBgFill, colTextSecondary, "")
	default:
		ui.Row(c).Gap(3).Shrink(0).AlignItems(ui.Center).Children(func() {
			if f.Deleted > 0 || f.Added == 0 {
				ui.Text(c, fmt.Sprintf("−%d", f.Deleted)).FontSize(11).TextColor(colDiffDelText).FontFeatures("tnum").SingleLine()
			}
			if f.Added > 0 || f.Deleted == 0 {
				ui.Text(c, fmt.Sprintf("+%d", f.Added)).FontSize(11).TextColor(colDiffAddText).FontFeatures("tnum").SingleLine()
			}
		})
	}
}

func diffMiniTag(c *ui.Context, text string, bg, fg ui.Color, tip string) {
	tag := ui.Row(c).Height(16).Padding(0, 5).Radius(4).Background(bg).AlignItems(ui.Center).Shrink(0)
	if tip != "" {
		tag.Tooltip(tip)
	}
	tag.Children(func() {
		ui.Text(c, text).FontSize(10).FontWeight(500).TextColor(fg).SingleLine()
	})
}

// diffVerBar 是右侧正文顶部的双栏对称版本信息栏（td3-b .ver-bar）。
func diffVerBar(c *ui.Context, leftFn, rightFn func()) {
	ui.Row(c).MinHeight(56).Shrink(0).Background(colBgPane).BorderWidth(0, 0, 1, 0).BorderColor(colBorderDefault).
		AlignItems(ui.Stretch).Children(func() {
		ui.Box(c).Grow(1).Basis(0).MinWidth(0).Children(leftFn)
		ui.Box(c).Width(1).Background(colBorderDefault)
		ui.Box(c).Grow(1).Basis(0).MinWidth(0).Children(rightFn)
	})
}

func (a *app) localVerSide(c *ui.Context, st *compareState, k int) {
	vs := st.skill.Versions()
	if k >= len(vs) {
		return
	}
	v := &vs[k]
	var placePath string
	if len(v.Places) > 0 {
		placePath = a.lib.Display(v.Places[0].Path)
	}
	meta := ""
	if !v.Modified.IsZero() {
		meta = locales.CompareModified(dayLabel(v.Modified, time.Now()))
	}
	if len(v.Places) > 1 {
		if meta != "" {
			meta += locales.CompareLocationsSuffix(len(v.Places))
		} else {
			meta = locales.CommonLocationCount(len(v.Places))
		}
	}
	ui.Row(c.Key(k)).Padding(9, 14).Gap(10).AlignItems(ui.Center).Children(func() {
		versionBadge(c, v, k, 22)
		ui.Column(c).Grow(1).MinWidth(0).Gap(2).Children(func() {
			ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
				ui.Text(c, versionDisplayName(v, k)).FontSize(13).FontWeight(600).SingleLine()
				if k == 0 {
					baseTag(c)
				}
				if meta != "" {
					ui.Text(c, meta).FontSize(11).TextColor(colTextMuted).FontFeatures("tnum").SingleLine().MinWidth(0)
				}
			})
			if placePath != "" {
				tip := placePath
				if len(v.Places) > 1 {
					var all []string
					for _, p := range v.Places {
						all = append(all, a.lib.Display(p.Path))
					}
					tip = strings.Join(all, "\n")
				}
				ui.Text(c, placePath).Font("monospace").FontSize(11).TextColor(colTextMuted).SingleLine().MinWidth(0).Tooltip(tip)
			}
		})
		if diffActionButton(c, locales.CompareKeepVersion(), false).Clicked() {
			s, tidy := st.skill, st.tidy
			a.closeCompare()
			a.merge(s, k, tidy)
		}
	})
}

func versionDisplayName(v *library.Version, k int) string {
	if v.Shared {
		return locales.CommonSharedLibrary()
	}
	if len(v.Places) > 0 && v.Places[0].Owner != "" {
		return library.AgentName(v.Places[0].Owner)
	}
	if ids := v.Agents(); len(ids) > 0 {
		return library.AgentName(ids[0])
	}
	return locales.CompareVersion(versionLetter(k))
}

func versionBadge(c *ui.Context, v *library.Version, k int, size float32) {
	if v.Shared {
		badge(c, sharedBrand, locales.CommonSharedLibrary(), size)
		return
	}
	if len(v.Places) > 0 && v.Places[0].Owner != "" {
		agentBadge(c, v.Places[0].Owner, size)
		return
	}
	if ids := v.Agents(); len(ids) > 0 {
		agentBadge(c, ids[0], size)
		return
	}
	badge(c, letter(versionLetter(k), "#8e8e93"), locales.CompareVersion(versionLetter(k)), size)
}

func diffActionButton(c *ui.Context, label string, primary bool) ui.Element {
	if primary {
		b := ui.ButtonBase(c).Height(26).Padding(0, 12).Radius(6).Center().Shrink(0).TextColor(white).Label(label)
		base := tint(b, "bg", colAccent, darker(colAccent, 0.06), darker(colAccent, 0.18))
		b.LinearGradient(ui.LinearGradient{From: base.Mix(white, 0.14), To: base, Angle: 180}).
			InsetShadow(0, 0.5, 0, 0, white.Alpha(0.3))
		hairline(b, 0.22)
		lift(b, 1, 1.5, 0.12)
		b.Children(func() { ui.Text(c, label).FontSize(13).FontWeight(500).SingleLine().ContentTransition(labelTransition) })
		return b
	}
	b := ui.ButtonBase(c).Height(26).Padding(0, 11).Radius(6).Center().Shrink(0).Label(label)
	tintSolid(b, colBgControl)
	hairline(b, 0.18)
	lift(b, 1, 1.5, 0.08)
	b.Children(func() {
		ui.Text(c, label).FontSize(13).TextColor(colTextPrimary).SingleLine().ContentTransition(labelTransition)
	})
	return b
}

// diffFileContent 画文件子标题栏与下面的并排差异内容；item 在远程 Git 对比时传入。
func (a *app) diffFileContent(c *ui.Context, st *compareState, item *library.UpdateItem) {
	if len(st.files) == 0 {
		empty(c, locales.CompareIdenticalContent())
		return
	}
	f := st.files[min(st.file, len(st.files)-1)]
	a.diffSubhead(c, st, f, item)
	a.diffBody(c, st, f)
}

func (a *app) diffSubhead(c *ui.Context, st *compareState, f library.FileDiff, item *library.UpdateItem) {
	ui.Row(c).Height(30).Shrink(0).Padding(0, 14).Gap(8).Background(colBgSurface).
		BorderWidth(0, 0, 1, 0).BorderColor(colBorderDefault).AlignItems(ui.Center).Children(func() {
		ui.Icon(c, iconDoc).Size(13, 13).TextColor(colTextMuted)
		ui.Text(c, f.Path).Font("monospace").FontSize(12).FontWeight(600).TextColor(colTextPrimary).SingleLine().MinWidth(0)
		switch {
		case f.OnlyLeft:
			if st.updating {
				diffMiniTag(c, locales.CompareLocalOnlyRemoved(), colWarnBg, colWarnText, "")
			} else {
				diffMiniTag(c, locales.CompareBaselineOnly(), colBgFill, colTextSecondary, "")
			}
		case f.OnlyRight:
			if st.updating {
				diffMiniTag(c, locales.CompareAddedUpstream(), colOKBg, colDiffAddText, "")
			} else {
				diffMiniTag(c, locales.CompareCopyOnly(), colBgFill, colTextSecondary, "")
			}
		case f.ModeOnly:
			ui.Text(c, modeNote(f)).FontSize(12).TextColor(colTextSecondary).SingleLine()
		case f.Left.Unreadable || f.Right.Unreadable:
			diffMiniTag(c, locales.CommonUnreadable(), colDiffDelRow, colDiffDelText, "")
		case f.Binary:
			diffMiniTag(c, locales.CompareDifferentContent(), colBgFill, colTextSecondary, "")
		default:
			ui.Row(c).Gap(4).Shrink(0).AlignItems(ui.Center).Children(func() {
				ui.Text(c, fmt.Sprintf("−%d", f.Deleted)).FontSize(12).TextColor(colDiffDelText).FontFeatures("tnum").SingleLine()
				ui.Text(c, fmt.Sprintf("+%d", f.Added)).FontSize(12).TextColor(colDiffAddText).FontFeatures("tnum").SingleLine()
			})
		}
		ui.Spacer(c)
		if !st.updating && !f.Binary && hasFoldableRows(f.Rows) {
			lbl := locales.CompareExpandFullText()
			if st.fullFile {
				lbl = locales.CompareCollapseUnchanged()
			}
			b := ui.ButtonBase(c).Height(20).Padding(0, 7).Radius(4).AlignItems(ui.Center).Label(lbl)
			tintBg(b, ui.Color{}, colInkHover, colInkPress)
			if b.Clicked() {
				st.fullFile = !st.fullFile
			}
			b.Children(func() {
				ui.Text(c, lbl).FontSize(11).TextColor(colTextSecondary).SingleLine()
			})
		}
		if item != nil && !f.OnlyLeft {
			if fileURL := item.FileWebURL(f.Path); fileURL != "" {
				b := ui.ButtonBase(c).Height(20).Padding(0, 7).Gap(4).Radius(4).AlignItems(ui.Center).
					Cursor(ui.CursorPointer).Label(locales.CompareViewFileOnGitHub()).Tooltip(locales.ActionOpenTarget(fileURL))
				b.TextColor(tint(b, "furl", colTextSecondary, colAccent, darker(colAccent, 0.3)))
				if b.Clicked() {
					a.openExternalURL(fileURL)
				}
				b.Children(func() {
					ui.Text(c, locales.CompareViewFileOnGitHub()).FontSize(11).SingleLine()
					ui.Icon(c, iconExternal).Size(11, 11)
				})
			}
		}
	})
}

func hasFoldableRows(rows []library.DiffRow) bool {
	for _, it := range foldRows(rows, nil) {
		if it.fold > 0 {
			return true
		}
	}
	return false
}

// diffBody 是一个文件的并排逐行对比（二进制或不可读时展示提示）。
func (a *app) diffBody(c *ui.Context, st *compareState, f library.FileDiff) {
	if f.Binary {
		ui.Row(c).Grow(1).MinHeight(0).AlignItems(ui.Stretch).Children(func() {
			binarySide(c, f.Left, f.OnlyRight)
			ui.Box(c).Width(1).Background(colBorderDefault)
			binarySide(c, f.Right, f.OnlyLeft)
		})
		return
	}
	ui.Scroll(c.Key(f.Path)).Grow(1).Children(func() {
		ui.Column(c).Children(func() {
			if st.fullFile {
				for i, r := range f.Rows {
					diffRow(c.Key(i), r)
				}
				return
			}
			for _, it := range foldRows(f.Rows, st.open) {
				if it.fold > 0 {
					start := it.row
					fold := ui.ButtonBase(c.Key(start)).Height(28).Padding(0, 14).Gap(6).AlignItems(ui.Center).
						Background(colBgSubtle).TextColor(colTextSecondary).Label(locales.CompareExpandUnchanged())
					if fold.Clicked() {
						st.open[start] = true
					}
					fold.Children(func() {
						ui.Icon(c, iconUpDown).Size(12, 12)
						ui.Text(c, locales.CompareHiddenLines(it.fold)).FontSize(12).SingleLine()
					})
					continue
				}
				diffRow(c.Key(it.row), f.Rows[it.row])
			}
		})
	})
}

// binarySide 是没法逐行比的文件的一边：只说这边有没有、是什么、多大；
// 没有这个文件的一边和缺行一样是斜纹。
func binarySide(c *ui.Context, f library.File, missing bool) {
	side := ui.Column(c).Grow(1).Basis(0).MinWidth(0).Padding(0, 20).Gap(4).Center()
	if missing {
		side.Background(colBgSubtle).Stripes(ink(0.045), 1.5, 6, 45)
	}
	side.Children(func() {
		if missing {
			ui.Text(c, locales.CompareFileMissing()).TextColor(colTextDisabled).SingleLine()
			return
		}
		ui.Text(c, path.Base(f.Path)).FontWeight(500).SingleLine().MinWidth(0)
		ui.Text(c, unreadKind(f)+" · "+sizeLabel(f.Size)).FontSize(12).TextColor(colTextMuted).SingleLine().MinWidth(0)
	})
}

// unreadKind 是对比里这一边的文件是什么：另一边没读内容时，这边即使是
// 文本也只能并排列出来。
func unreadKind(f library.File) string {
	switch {
	case f.Unreadable:
		return locales.CompareCannotReadContent()
	case !f.Binary:
		return locales.CompareTextFile()
	case f.TooLarge():
		return locales.CompareFileTooLarge()
	default:
		return locales.CompareNotText()
	}
}

// foldItem 是对比里的一项：一行，或折叠起来的 fold 行（从 row 开始）。
type foldItem struct{ row, fold int }

// foldRows 留下改动和上下各 diffContext 行，其余没改动的连续几行折叠成
// 一项；展开过的不折。
func foldRows(rows []library.DiffRow, open map[int]bool) []foldItem {
	keep := make([]bool, len(rows))
	for i, r := range rows {
		if r.Kind != library.Same {
			for j := max(0, i-diffContext); j <= min(len(rows)-1, i+diffContext); j++ {
				keep[j] = true
			}
		}
	}
	var out []foldItem
	for i := 0; i < len(rows); {
		if keep[i] {
			out = append(out, foldItem{i, 0})
			i++
			continue
		}
		j := i
		for j < len(rows) && !keep[j] {
			j++
		}
		if open[i] || j-i < 2 {
			for k := i; k < j; k++ {
				out = append(out, foldItem{k, 0})
			}
		} else {
			out = append(out, foldItem{i, j - i})
		}
		i = j
	}
	return out
}

// diffRow 是并排的一行：左边是基准，右边是另一种；对面没有这一行时是斜纹。
func diffRow(c *ui.Context, r library.DiffRow) {
	var la, lb, ra, rb int
	if r.Kind == library.Changed {
		la, lb, ra, rb = library.InlineChange(r.LeftText, r.RightText)
	}
	ui.Row(c).AlignItems(ui.Stretch).Children(func() {
		left, right := library.Same, library.Same
		switch r.Kind {
		case library.Changed:
			left, right = library.Deleted, library.Inserted
		case library.Deleted:
			left, right = library.Deleted, -1
		case library.Inserted:
			left, right = -1, library.Inserted
		}
		diffSide(c, r.Left, r.LeftText, left, la, lb)
		ui.Box(c).Width(1).Background(colBorderDefault)
		diffSide(c, r.Right, r.RightText, right, ra, rb)
	})
}

// diffSide 是一行的一边：行号和内容。kind 为 -1 是这一边没有这一行。
// [hl, hlEnd) 是改了的字，再深一档。
func diffSide(c *ui.Context, num int, text string, kind library.RowKind, hl, hlEnd int) {
	side := ui.Row(c).Grow(1).Basis(0).MinWidth(0).AlignItems(ui.Start)
	var word ui.Color
	switch kind {
	case -1:
		side.Background(colBgSubtle).Stripes(ink(0.045), 1.5, 6, 45)
		return
	case library.Deleted:
		side.Background(colDiffDelRow)
		word = colDiffDelWord
	case library.Inserted:
		side.Background(colDiffAddRow)
		word = colDiffAddWord
	}
	side.Children(func() {
		ui.Text(c, fmt.Sprint(num)).Width(40).Shrink(0).Padding(2, 8, 2, 0).TextAlign(ui.End).
			Font("monospace").FontSize(11).TextColor(colTextDisabled).FontFeatures("tnum")
		var t ui.Element
		if word != (ui.Color{}) && hlEnd > hl {
			rs := []rune(text)
			t = ui.RichText(c,
				ui.Span{Text: string(rs[:hl])},
				ui.Span{Text: string(rs[hl:hlEnd]), Background: word, Radius: 2},
				ui.Span{Text: string(rs[hlEnd:])})
		} else {
			t = ui.Text(c, text)
		}
		t.Grow(1).MinWidth(0).Padding(2, 12, 2, 4).Font("monospace").FontSize(12).LineHeight(1.5).PreserveSpaces()
	})
}

// modeNote 说清哪边能执行：「内容一样，只有左边能执行」。
func modeNote(f library.FileDiff) string {
	if f.Left.Exec {
		return locales.CompareOnlyLeftExecutable()
	}
	return locales.CompareOnlyRightExecutable()
}
