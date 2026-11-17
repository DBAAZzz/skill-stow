package main

import (
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/transfer"
	"github.com/egoist/mygo/ui"

	"skillmanager/internal/library"
	"skillmanager/locales"
)

// 支持与诊断（.docs/support-diagnostics-design.md）：设置 › 通用底部和
// 「帮助」菜单里的三个入口——打开应用数据文件夹、复制诊断信息、反馈问题。
// 诊断信息先在弹窗里给用户看全文，确认后只复制到剪贴板，不上传。

const appIssueURL = "https://github.com/DBAAZzz/skill-stow/issues/new"

// supportState 是诊断预览弹窗和设置里那一组的状态。
type supportState struct {
	open    bool   // 预览弹窗开着
	report  bool   // 从「反馈问题」打开的：复制后前往 GitHub
	loading bool   // 正在后台生成诊断信息
	text    string // 这次生成的诊断信息
	env     library.DiagEnv
	note    string // 设置里这一组下面的结果：已复制、复制失败
	seq     uint64 // 请求序号，忽略过期回调
}

// diagEnv 是诊断里只有界面层知道的部分。
func (a *app) diagEnv() library.DiagEnv {
	theme := "light"
	if darkUI {
		theme = "dark"
	}
	build := "dev"
	if exe, err := os.Executable(); err == nil && strings.Contains(exe, ".app/Contents/MacOS/") {
		build = "packaged"
	}
	lang := a.lib.Settings.Language
	if a.live {
		lang = mygo.App.Locale()
	}
	return library.DiagEnv{Version: appVersion(), Build: build, Language: cmpOr(lang, "system"), Theme: theme}
}

func cmpOr(s, def string) string {
	if s == "" {
		return def
	}
	return s
}

// openDiagnostics 在设置窗口里打开预览弹窗，后台生成诊断信息（要读日志、
// 检查整理问题，真实数据上要几百毫秒）。report 时确认按钮是「复制并前往 GitHub」。
func (a *app) openDiagnostics(report bool) {
	if !a.set.open {
		a.openSettings()
	}
	a.set.tab = tabGeneral
	seq := a.sup.seq + 1
	a.sup = supportState{open: true, report: report, loading: true, env: a.diagEnv(), seq: seq}
	snap, env := a.lib.DiagSnapshot(), a.sup.env
	a.background(func(post func(func())) {
		text := snap.Format(env)
		post(func() {
			if a.sup.seq != seq || !a.sup.open {
				return
			}
			a.sup.text, a.sup.loading = text, false
		})
	})
	a.changed()
}

// copyDiagnostics 把预览里的诊断信息放进剪贴板；report 时再打开 GitHub 的新问题页。
func (a *app) copyDiagnostics() {
	text, report := a.sup.text, a.sup.report
	a.sup.open = false
	if err := a.writeClipboard(text); err != nil {
		a.sup.note = locales.SupportCopyFailed(err.Error())
		a.lib.Log(library.Event{Level: library.LevelError, Cat: "ui", Op: "copy-diagnostics", Msg: err.Error()})
		return
	}
	if !report {
		a.sup.note = locales.SupportDiagnosticsCopied()
		return
	}
	a.sup.note = locales.SupportDiagnosticsCopiedPaste()
	a.openExternalURL(issueURL(a.sup.env))
}

func (a *app) writeClipboard(text string) error {
	if a.copyText != nil {
		return a.copyText(text)
	}
	if !a.live {
		return nil
	}
	return mygo.Clipboard.Write(transfer.TextData(text))
}

// issueURL 是 GitHub 新问题页，用问题模板，只预填版本和系统；完整的诊断
// 信息由用户粘贴，网址放不下。
func issueURL(env library.DiagEnv) string {
	q := url.Values{}
	q.Set("template", "bug_report.yml")
	q.Set("version", env.Version+" ("+env.Build+")")
	return appIssueURL + "?" + q.Encode()
}

// openAppData 在 Finder 里打开应用数据文件夹。示例数据没有。
func (a *app) openAppData() {
	if dir := a.lib.AppDir(); dir != "" && a.live {
		if err := mygo.Shell.OpenPath(dir); err != nil {
			a.sup.note = err.Error()
		}
	}
}

// supportGroup 是设置 › 通用最下面的「支持与诊断」。
func (a *app) supportGroup(c *ui.Context) {
	groupLabel(c, locales.SupportTitle(), false)
	setBox(c, func() {
		setRow(c).Children(func() {
			rowText(c, locales.SupportAppData(), locales.SupportAppDataHint(), false)
			b := pushButton(c, locales.ActionShowInFinder())
			if a.lib.AppDir() == "" {
				b.Disabled(true)
			}
			if b.Clicked() {
				a.openAppData()
			}
		})
		setRow(c).Children(func() {
			rowText(c, locales.SupportDiagnostics(), locales.SupportDiagnosticsHint(), false)
			if pushButton(c, locales.SupportPreviewAndCopy()).Clicked() {
				a.openDiagnostics(false)
			}
		})
		setRow(c).Children(func() {
			rowText(c, locales.SupportReportIssue(), locales.SupportReportIssueHint(), false)
			if pushButton(c, locales.SupportReportOnGitHub()).Clicked() {
				a.openDiagnostics(true)
			}
		})
	})
	if a.sup.note != "" {
		hint(c, func() { ui.Text(c, a.sup.note) })
	}
}

// diagnosticsDialog 是诊断信息的预览：全文可滚动、可选中，确认后才复制。
func (a *app) diagnosticsDialog(c *ui.Context) {
	if !a.sup.open {
		return
	}
	confirm := locales.SupportCopy()
	if a.sup.report {
		confirm = locales.SupportCopyAndOpenGitHub()
	}
	dialog(c, &a.sup.open, locales.SupportDiagnostics(), locales.SupportPreviewBody(), func() {
		ui.Scroll(c).Height(240).Margin(14, 0, 0, 0).Radius(10).Background(colBgSubtle).Children(func() {
			text := a.sup.text
			if a.sup.loading {
				text = locales.SupportGenerating()
			}
			ui.Box(c).Padding(10, 12).Children(func() {
				ui.Text(c, text).Font("monospace").FontSize(11).LineHeight(1.5).TextColor(colTextBody).Selectable()
			})
		})
	}, func() {
		if dialogButton(c, locales.ActionCancel(), false).Clicked() {
			a.sup.open = false
		}
		b := dialogButton(c, confirm, true)
		if a.sup.loading {
			b.Disabled(true)
		}
		if b.Clicked() {
			a.copyDiagnostics()
		}
	})
}

// noticeRecovered 在扫描时发现应用数据文件坏了、已挪到旁边时提示一次。
func (a *app) noticeRecovered(lib *library.Library) {
	for _, p := range lib.Recovered() {
		backup := filepath.Base(p)
		file, _, _ := strings.Cut(backup, ".corrupt-")
		a.showToast(&toast{source: fromAppData, kind: toastWarn, text: locales.SupportDataFileRecovered(file, backup),
			action: &toastAction{label: locales.ActionShowInFinder(), run: func() { a.reveal(p) }}})
	}
}

// logToast 把用户看到的错误和警告记进事件日志，报问题时能对上。
func (a *app) logToast(kind toastKind, text string) {
	switch kind {
	case toastError:
		a.lib.Log(library.Event{Level: library.LevelError, Cat: "ui", Op: "toast", Msg: text})
	case toastWarn:
		a.lib.Log(library.Event{Level: library.LevelWarn, Cat: "ui", Op: "toast", Msg: text})
	}
}

// logSettingsError 把设置窗口里显示的错误记进事件日志。
func (a *app) logSettingsError(op, text string) {
	if text != "" {
		a.lib.Log(library.Event{Level: library.LevelError, Cat: "ui", Op: op, Msg: text})
	}
}
