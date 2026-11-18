package main

import (
	"context"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"

	"skillmanager/internal/library"
	"skillmanager/internal/security"
	"skillmanager/locales"
)

// 本地安全检查的界面：卡片标记、详情栏「本地检查」、阅读视图的「安全检查」
// 报告、整理页的三类问题、添加技能与更新前的检查。所有入口读同一个服务的
// 同一份报告，不另算一套提示类型或文案。

// secFile 是阅读视图里「安全检查」报告占的文件序号。
const secFile = -1

// secHistoryFile 是页面历史里记「安全检查」报告的文件名。
const secHistoryFile = "\x00security"

const secMetaInterval = 10 * time.Minute

type securityUI struct {
	svc       *library.SecurityService
	pending   atomic.Bool // 已经排了一次重画，后台再报变化就不重复排
	stopTick  chan struct{}
	showAcked bool // 整理页：展开已看过的
	jump      secJump

	// 添加技能：每个暂存技能的检查结果，按 importKey；换来源时清空
	impBatch   *library.ImportBatch
	imp        map[string]*impSecurity
	addConfirm bool

	// 更新对比页：准备好的新版本的检查结果
	updFor    *library.Prepared
	updReport *security.Report
}

// secJump 是要在阅读视图里跳到的证据行：文件换成编辑器后滚过去并标底色。
type secJump struct {
	key  string // 技能 ID + 文件路径
	line int
}

type impSecurity struct {
	scanning bool
	dir      string // 查的是哪个暂存目录
	partial  bool   // 检查时还没补齐
	report   *security.Report
	unpicked bool // 检查出问题时替用户取消过勾选：之后用户再勾就不再动它
}

var iconShieldBare = icon(`<path d="M12 3 5 5.8v5.4c0 4.3 3 8 7 9.3 4-1.3 7-5 7-9.3V5.8z"/>`)

// ——— 服务 ———

// startSecurity 在真实数据启动时建服务：没有本机报告的技能全部排队（首次
// 打开即全量检查），已有报告的只比对元信息；之后每 10 分钟再比对一次。
func (a *app) startSecurity() {
	if a.lib.Home == "" || !library.SecuritySupported() {
		return
	}
	a.sec.svc = library.NewSecurity(a.lib.Home, a.securityChanged)
	svc := a.sec.svc
	library.UpdateSecurityCheck = func(ctx context.Context, dir string) library.StagedSecurity {
		return svc.ScanStaged(ctx, dir, false)
	}
	a.syncSecurity()
	stop := make(chan struct{})
	a.sec.stopTick = stop
	go func() {
		t := time.NewTicker(secMetaInterval)
		defer t.Stop()
		for {
			select {
			case <-stop:
				return
			case <-t.C:
				svc.CheckMeta(true)
			}
		}
	}()
}

func (a *app) stopSecurity() {
	if a.sec.stopTick != nil {
		close(a.sec.stopTick)
		a.sec.stopTick = nil
	}
	if a.sec.svc != nil && a.lib.Home != "" {
		a.sec.svc.Close()
	}
}

// securityChanged 由服务在任意 goroutine 调：合并成一次重画。
func (a *app) securityChanged() {
	if a.sec.pending.CompareAndSwap(false, true) {
		a.post(func() { a.sec.pending.Store(false) })
	}
}

// syncSecurity 把这次发现的技能交给服务对齐。
func (a *app) syncSecurity() {
	if a.sec.svc == nil || a.lib == nil {
		return
	}
	var roots []string
	for _, s := range a.lib.Skills {
		roots = append(roots, secKeys(s)...)
	}
	a.sec.svc.Sync(roots)
}

// secKeys 是一个技能要检查的全部实际目录：它自己，以及各 Agent 目录里
// 内容不同的同名副本（Agent 实际读的可能是副本）。
func secKeys(s *library.Skill) []string {
	keys := []string{library.SecurityKey(s)}
	for _, d := range s.CopyDirs() {
		if d != "" && !slices.Contains(keys, d) {
			keys = append(keys, d)
		}
	}
	return keys
}

func (a *app) secOn() bool { return a.sec.svc != nil }

func (a *app) secView(s *library.Skill) library.SkillSecurity {
	_, v := a.secPick(s)
	return v
}

// secPick 挑出这个技能最需要看的那一份（它自己或某个副本）：没看过的风险
// 最严重的优先，其次是没能查完的；一样时用它自己。
func (a *app) secPick(s *library.Skill) (string, library.SkillSecurity) {
	if a.sec.svc == nil || s == nil {
		return "", library.SkillSecurity{}
	}
	score := func(v library.SkillSecurity) int {
		n := v.TopPending().Rank() * 4
		if v.Unfinished() || v.Fresh == library.FreshUnknown {
			n += 2
		}
		return n
	}
	var key string
	var best library.SkillSecurity
	for i, k := range secKeys(s) {
		v := a.sec.svc.View(k)
		if i == 0 || score(v) > score(best) {
			key, best = k, v
		}
	}
	return key, best
}

func (a *app) secRescan(s *library.Skill) {
	if a.sec.svc != nil {
		a.sec.svc.Scan(secKeys(s), true)
	}
}

// secOnCopy 报告最需要看的是同名副本而不是技能自己：阅读视图里的文件不是
// 它的，证据要到副本里去看。
func (a *app) secOnCopy(s *library.Skill) (string, bool) {
	key, _ := a.secPick(s)
	return key, key != "" && key != library.SecurityKey(s)
}

// ——— 文案 ———

func secSummary(msg string) string {
	switch msg {
	case security.MsgRemotePipe:
		return locales.SecFindingRemotePipe()
	case security.MsgRemoteDownloadRun:
		return locales.SecFindingRemoteDownloadRun()
	case security.MsgReverseShell:
		return locales.SecFindingReverseShell()
	case security.MsgEncodedExec:
		return locales.SecFindingEncodedExec()
	case security.MsgBidi:
		return locales.SecFindingBidiChars()
	case security.MsgInvisible:
		return locales.SecFindingInvisibleChars()
	case security.MsgHiddenComment:
		return locales.SecFindingHiddenComment()
	case security.MsgIgnoreInstr:
		return locales.SecFindingIgnoreInstructions()
	case security.MsgConceal:
		return locales.SecFindingConcealFromUser()
	case security.MsgBypassConfirm:
		return locales.SecFindingBypassConfirmation()
	case security.MsgLeakContext:
		return locales.SecFindingLeakContext()
	case security.MsgSecretInChat:
		return locales.SecFindingSecretInChat()
	case security.MsgReadCredentials:
		return locales.SecFindingReadCredentials()
	case security.MsgCredentialUpload:
		return locales.SecFindingCredentialUpload()
	case security.MsgSecretEnvUpload:
		return locales.SecFindingSecretEnvUpload()
	case security.MsgWipeHome:
		return locales.SecFindingWipeHome()
	case security.MsgPersistence:
		return locales.SecFindingPersistence()
	case security.MsgEmbeddedSecret:
		return locales.SecFindingEmbeddedSecret()
	case security.MsgLinkOutside:
		return locales.SecFindingLinkOutside()
	case security.MsgSpecialFile:
		return locales.SecFindingSpecialFile()
	case security.MsgUsesNetwork:
		return locales.SecFindingUsesNetwork()
	case security.MsgHasScripts:
		return locales.SecFindingHasScripts()
	case security.MsgHasBinary:
		return locales.SecFindingHasBinary()
	case security.MsgNeedsAdmin:
		return locales.SecFindingNeedsAdmin()
	}
	return locales.SecFindingUnknown()
}

func secDetail(msg string) string {
	switch msg {
	case security.MsgRemotePipe:
		return locales.SecFindingRemotePipeDetail()
	case security.MsgRemoteDownloadRun:
		return locales.SecFindingRemoteDownloadRunDetail()
	case security.MsgReverseShell:
		return locales.SecFindingReverseShellDetail()
	case security.MsgEncodedExec:
		return locales.SecFindingEncodedExecDetail()
	case security.MsgBidi:
		return locales.SecFindingBidiCharsDetail()
	case security.MsgInvisible:
		return locales.SecFindingInvisibleCharsDetail()
	case security.MsgHiddenComment:
		return locales.SecFindingHiddenCommentDetail()
	case security.MsgIgnoreInstr:
		return locales.SecFindingIgnoreInstructionsDetail()
	case security.MsgConceal:
		return locales.SecFindingConcealFromUserDetail()
	case security.MsgBypassConfirm:
		return locales.SecFindingBypassConfirmationDetail()
	case security.MsgLeakContext:
		return locales.SecFindingLeakContextDetail()
	case security.MsgSecretInChat:
		return locales.SecFindingSecretInChatDetail()
	case security.MsgReadCredentials:
		return locales.SecFindingReadCredentialsDetail()
	case security.MsgCredentialUpload:
		return locales.SecFindingCredentialUploadDetail()
	case security.MsgSecretEnvUpload:
		return locales.SecFindingSecretEnvUploadDetail()
	case security.MsgWipeHome:
		return locales.SecFindingWipeHomeDetail()
	case security.MsgPersistence:
		return locales.SecFindingPersistenceDetail()
	case security.MsgEmbeddedSecret:
		return locales.SecFindingEmbeddedSecretDetail()
	case security.MsgLinkOutside:
		return locales.SecFindingLinkOutsideDetail()
	case security.MsgSpecialFile:
		return locales.SecFindingSpecialFileDetail()
	case security.MsgUsesNetwork:
		return locales.SecFindingUsesNetworkDetail()
	case security.MsgHasScripts:
		return locales.SecFindingHasScriptsDetail()
	case security.MsgHasBinary:
		return locales.SecFindingHasBinaryDetail()
	case security.MsgNeedsAdmin:
		return locales.SecFindingNeedsAdminDetail()
	}
	return locales.SecFindingUnknownDetail()
}

func secNoticeLabel(n security.Notice) string {
	switch n {
	case security.HighRisk:
		return locales.SecNoticeHighRisk()
	case security.Warning:
		return locales.SecNoticeWarning()
	}
	return locales.SecNoticeInfo()
}

func secNoticeTone(n security.Notice) tagTone {
	switch n {
	case security.HighRisk:
		return tagToneCrit
	case security.Warning:
		return tagToneWarn
	}
	return tagToneAccent
}

func secNoticeColor(n security.Notice) ui.Color {
	if n == security.HighRisk {
		return colDangerText
	}
	return colWarnText
}

func secGapLabel(g security.Gap) string {
	switch g {
	case security.GapSourcePartial:
		return locales.SecGapSourcePartial()
	case security.GapBinary:
		return locales.SecGapBinary()
	case security.GapEncoding:
		return locales.SecGapEncoding()
	case security.GapFileLimit:
		return locales.SecGapFileLimit()
	case security.GapByteLimit:
		return locales.SecGapByteLimit()
	case security.GapDepthLimit:
		return locales.SecGapDepthLimit()
	case security.GapDeadline:
		return locales.SecGapDeadline()
	case security.GapChanged:
		return locales.SecGapChanged()
	case security.GapLinkNotFollowed:
		return locales.SecGapLinkNotFollowed()
	case security.GapSpecialFile:
		return locales.SecGapSpecialFile()
	case security.GapAnalysisError:
		return locales.SecGapAnalysisError()
	case security.GapFindingLimit:
		return locales.SecGapFindingLimit()
	case security.GapCancelled:
		return locales.SecGapCancelled()
	}
	return locales.SecGapUnreadable()
}

func secFailLabel(kind string) string {
	switch kind {
	case "not_found":
		return locales.SecFailNotFound()
	case "permission":
		return locales.SecFailPermission()
	case "changed":
		return locales.SecFailChanged()
	case "cancelled":
		return locales.SecGapCancelled()
	}
	return locales.SecFailUnreadable()
}

// secUnfinishedText 是检查没有完整完成时的一句话：失败原因、不完整或无法核实。
func secUnfinishedText(v library.SkillSecurity) string {
	switch {
	case v.Report != nil && v.Report.Execution == security.Failed:
		return secFailLabel(v.Report.Error)
	case v.Report != nil && v.Report.Execution != security.Completed:
		return secGapLabel(cond(v.Report.Execution == security.TimedOut, security.GapDeadline, security.GapCancelled))
	case v.Fresh == library.FreshUnknown:
		return locales.SecUnknown()
	}
	return locales.SecIncomplete()
}

// topPending 是没看过的风险里最先要看的一条（报告已经按严重程度排好）。
func topPending(v library.SkillSecurity) (security.Finding, bool) {
	if v.Report != nil {
		for _, f := range v.Report.Findings {
			if f.Notice.Risky() && !v.Acked[f.Key] {
				return f, true
			}
		}
	}
	return security.Finding{}, false
}

func evLocation(e security.Evidence) string {
	if e.Line > 0 {
		return fmt.Sprintf("%s:%d", e.Path, e.Line)
	}
	return e.Path
}

// ——— 卡片 ———

// securityFlag 是卡片底栏的标记：只标没看过的警告和高风险；扫描中、没扫过、
// 只有提示的都不标，免得首次检查时一屏卡片一起闪。
func (a *app) securityFlag(c *ui.Context, s *library.Skill) {
	if !a.secOn() {
		return
	}
	v := a.secView(s)
	switch top := v.TopPending(); top {
	case security.HighRisk, security.Warning:
		text := cond(top == security.HighRisk, locales.SecFlagHighRisk(), locales.SecFlagWarning())
		f := flag(c, iconWarn, text).TextColor(secNoticeColor(top))
		if first, ok := topPending(v); ok {
			f.Tooltip(secSummary(first.Message))
		}
	}
}

// ——— 详情栏 ———

// securityPane 是详情栏「本地检查」一节：状态、最先要看的一条、检查日期，
// 点开进阅读视图的报告。
func (a *app) securityPane(c *ui.Context, s *library.Skill) {
	if !a.secOn() {
		return
	}
	v := a.secView(s)
	label(c, locales.SecLocalCheck())
	now := time.Now()
	box(c, func() {
		row := ui.ButtonBase(c.Key("sec-summary")).MinHeight(38).Padding(6, 12).Gap(9).AlignItems(ui.Center).
			Cursor(ui.CursorPointer).Label(locales.SecCheck())
		tintInk(row)
		if row.Clicked() {
			a.openSecurityReport(s)
		}
		row.Children(func() {
			a.secStatusLine(c, v)
			ui.Spacer(c)
			if v.Report != nil {
				ui.Text(c, scanDay(v.Report.FinishedAt, now)).FontSize(12).TextColor(colTextDisabled).FontFeatures("tnum").SingleLine().Shrink(0)
			}
			ui.Icon(c, iconChevronRight).Size(12, 12).TextColor(colTextDisabled).Shrink(0)
		})
		if note := secPaneNote(v); note != "" {
			boxRow(c, false).Children(func() {
				ui.Box(c).Width(16).Shrink(0)
				ui.Text(c, note).FontSize(12).LineHeight(1.45).TextColor(colTextSecondary).Grow(1).MinWidth(0)
			})
		}
		if v.Report == nil && !v.Scanning && !v.Queued || v.Fresh == library.FreshStale || v.Fresh == library.FreshUnknown || v.Unfinished() {
			boxRow(c, false).Children(func() {
				ui.Box(c).Width(16).Shrink(0)
				switch {
				case v.Fresh == library.FreshStale && v.Report != nil:
					ui.Text(c, locales.SecStaleNote()).FontSize(12).TextColor(colTextSecondary).Grow(1).MinWidth(0)
				case v.Fresh == library.FreshUnknown && v.Report != nil:
					ui.Text(c, locales.SecUnknownNote()).FontSize(12).TextColor(colTextSecondary).Grow(1).MinWidth(0)
				default:
					ui.Spacer(c)
				}
				if !v.Scanning && !v.Queued {
					if linkButton(c, cond(v.Report == nil, locales.SecScanNow(), locales.SecRescan())).Shrink(0).Clicked() {
						a.secRescan(s)
					}
				}
			})
		}
	})
}

// secStatusLine 是状态那一行：图标和文字，风险时分别写高风险、警告几条。
func (a *app) secStatusLine(c *ui.Context, v library.SkillSecurity) {
	ui.Row(c).Gap(8).AlignItems(ui.Center).MinWidth(0).Shrink(1).Children(func() {
		high, warn := 0, 0
		if v.Report != nil {
			for _, f := range v.Report.Findings {
				if v.Acked[f.Key] {
					continue
				}
				switch f.Notice {
				case security.HighRisk:
					high++
				case security.Warning:
					warn++
				}
			}
		}
		switch {
		case v.Report == nil && (v.Scanning || v.Queued):
			ui.Spinner(c).Size(14, 14).TextColor(colTextMuted)
			ui.Text(c, cond(v.Scanning, locales.SecScanning(), locales.SecQueued())).SingleLine()
		case v.Report == nil:
			ui.Image(c, symInfo).Size(16, 16).Shrink(0)
			ui.Text(c, locales.SecNotScanned()).SingleLine()
		case high > 0 || warn > 0:
			ui.Image(c, cond(high > 0, symCritical, symHigh)).Size(16, 16).Shrink(0)
			if high > 0 {
				ui.Text(c, locales.SecCountHigh(high)).FontWeight(500).TextColor(colDangerText).SingleLine()
			}
			if warn > 0 {
				ui.Text(c, locales.SecCountWarning(warn)).FontWeight(500).TextColor(colWarnText).SingleLine()
			}
		case v.Unfinished() || v.Fresh == library.FreshUnknown:
			ui.Image(c, symAlert).Size(16, 16).Shrink(0)
			ui.Text(c, secUnfinishedText(v)).SingleLine().MinWidth(0)
		case v.Risky() > 0:
			ui.Image(c, symTagMuted).Size(16, 16).Shrink(0)
			ui.Text(c, locales.SecAckedCount(v.Risky())).TextColor(colTextSecondary).SingleLine()
		default:
			// 上次没发现风险，但这次还没核实目录没变：写成灰色，标上次结果
			ui.Image(c, symOK).Size(16, 16).Shrink(0)
			t := ui.Text(c, locales.SecClean()).SingleLine()
			if !v.Clean() {
				t.TextColor(colTextSecondary)
			}
		}
		if v.Report != nil {
			switch {
			case v.Scanning:
				ui.Spinner(c).Size(12, 12).TextColor(colTextMuted)
			case v.Fresh == library.FreshStale:
				tag(c, importTagSpec{text: locales.SecStale(), tone: tagToneMuted}, true)
			case v.Fresh == library.FreshChecking:
				tag(c, importTagSpec{text: locales.SecLastResult(), tone: tagToneMuted}, true)
			}
		}
	})
}

// secPaneNote 是状态下面一行：没看过的那条风险，或只有提示时列出能力。
func secPaneNote(v library.SkillSecurity) string {
	if v.Report == nil {
		return ""
	}
	if f, ok := topPending(v); ok {
		return secSummary(f.Message)
	}
	if v.Risky() > 0 || v.Unfinished() {
		return ""
	}
	var caps []string
	for _, f := range v.Report.Findings {
		if f.Notice == security.Info {
			caps = append(caps, secSummary(f.Message))
		}
	}
	return strings.Join(slices.Compact(caps), locales.ListSeparator())
}

// ——— 阅读视图 ———

// openSecurityReport 打开阅读视图并停在「安全检查」报告上。
func (a *app) openSecurityReport(s *library.Skill) {
	if a.reading == s {
		a.file = secFile
		return
	}
	a.navigate(func() {
		a.selected = s
		a.showReader(s)
		a.file = secFile
	})
}

// securityFileRow 是阅读视图文件列表最上面的「安全检查」。
func (a *app) securityFileRow(c *ui.Context, s *library.Skill) {
	if !a.secOn() {
		return
	}
	v := a.secView(s)
	row := ui.ButtonBase(c.Key("\x00security")).Height(26).Padding(0, 8).Gap(7).Radius(6).AlignItems(ui.Center).
		Label(locales.SecCheck())
	selectableRow(row, a.file == secFile, colInkSelected)
	if row.Clicked() {
		a.file = secFile
	}
	row.Children(func() {
		top := v.TopPending()
		col := colTextMuted
		if top.Risky() {
			col = secNoticeColor(top)
		}
		ui.Icon(c, iconShieldBare).Size(14, 14).TextColor(col).Shrink(0)
		ui.Text(c, locales.SecCheck()).FontSize(13).SingleLine().Grow(1).MinWidth(0)
		switch {
		case v.Scanning:
			ui.Spinner(c).Size(11, 11).TextColor(colTextMuted)
		case v.Pending() > 0:
			ui.Text(c, fmt.Sprint(v.Pending())).FontSize(11).FontWeight(600).TextColor(col).FontFeatures("tnum").Shrink(0)
		}
	})
}

// secFileMark 是文件列表里有风险的文件右边的小圆点颜色；没有时为 false。
func (a *app) secFileMark(s *library.Skill, path string) (ui.Color, bool) {
	if _, onCopy := a.secOnCopy(s); !a.secOn() || onCopy {
		return ui.Color{}, false
	}
	v := a.secView(s)
	var top security.Notice
	if v.Report != nil {
		for _, f := range v.Report.Findings {
			if f.Notice.Risky() && !v.Acked[f.Key] && len(f.Evidence) > 0 && f.Evidence[0].Path == path && f.Notice.Rank() > top.Rank() {
				top = f.Notice
			}
		}
	}
	if top == "" {
		return ui.Color{}, false
	}
	return cond(top == security.HighRisk, colDanger, colWarn), true
}

// securityStrip 是阅读视图顶部状态栏里的一项：没看过的风险，点了看报告。
func (a *app) securityStrip(c *ui.Context, s *library.Skill) {
	if !a.secOn() {
		return
	}
	v := a.secView(s)
	top := v.TopPending()
	if !top.Risky() {
		return
	}
	n := 0
	for _, f := range v.Report.Findings {
		if f.Notice == top && !v.Acked[f.Key] {
			n++
		}
	}
	text := cond(top == security.HighRisk, locales.SecStripHigh(n), locales.SecStripWarning(n))
	ui.Box(c).Size(1, 14).Background(colBorderDefault).Margin(0, 4)
	b := ui.ButtonBase(c).Gap(4).AlignItems(ui.Center).Cursor(ui.CursorPointer).Label(text).TextColor(secNoticeColor(top))
	if b.Clicked() {
		a.file = secFile
	}
	b.Children(func() {
		ui.Icon(c, iconWarn).Size(12, 12)
		ui.Text(c, text).FontSize(12).FontWeight(500).SingleLine()
	})
}

// securityReport 是阅读视图里的报告页：状态、每条发现的原因与证据、没检查到
// 的内容。
func (a *app) securityReport(c *ui.Context, s *library.Skill, fresh bool) {
	v := a.secView(s)
	now := time.Now()
	ui.Scroll(c.Key("sec-report:" + s.ID)).Grow(1).MinHeight(0).Scrollbars(false).Children(func() {
		col := ui.Column(c).Padding(28, 40).Gap(14).MaxWidth(760)
		fadeIn(col, fresh, 220*time.Millisecond).Children(func() {
			ui.Row(c).Gap(10).AlignItems(ui.Center).Children(func() {
				ui.Text(c, locales.SecCheck()).FontSize(20).FontWeight(600).SingleLine()
				if v.Report != nil && (v.Scanning || v.Fresh == library.FreshStale || v.Fresh == library.FreshChecking) {
					tag(c, importTagSpec{text: locales.SecLastResult(), tone: tagToneMuted}, true)
				}
				ui.Spacer(c)
				label := cond(v.Report == nil, locales.SecScanNow(), locales.SecRescan())
				if busyButton(c, iconRefresh, cond(v.Scanning, locales.SecScanning(), label), v.Scanning || v.Queued).
					Disabled(v.Scanning || v.Queued).Clicked() {
					a.secRescan(s)
				}
			})
			if v.Report == nil {
				ui.Text(c, cond(v.Scanning || v.Queued, locales.SecScanning(), locales.SecNotScanned())).FontSize(13).TextColor(colTextSecondary)
				return
			}
			r := v.Report
			if dir, onCopy := a.secOnCopy(s); onCopy {
				// 最需要看的是另一个 Agent 目录里的同名副本：写明是哪一份
				ui.Text(c, a.lib.Display(dir)).Font("monospace").FontSize(12).TextColor(colTextSecondary).SingleLine().Margin(-8, 0, 0, 0)
			}
			metaLine(c, locales.SecCheckedAt(scanDay(r.FinishedAt, now)), locales.SecCheckedFiles(r.FilesAnalyzed, r.FilesDiscovered)).
				FontSize(12).TextColor(colTextMuted).Margin(-8, 0, 0, 0)
			ui.Text(c, locales.SecDisclaimer()).FontSize(12).TextColor(colTextDisabled).Margin(-10, 0, 0, 0)
			switch {
			case v.Fresh == library.FreshStale:
				errorLine(c, locales.SecStaleNote())
			case v.Fresh == library.FreshUnknown:
				errorLine(c, locales.SecUnknownNote())
			case r.Execution != security.Completed:
				errorLine(c, secUnfinishedText(v))
			}
			if len(r.Findings) == 0 && r.Complete {
				ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
					ui.Image(c, symOK).Size(16, 16)
					ui.Text(c, locales.SecNoFindings()).FontSize(13)
				})
			}
			var infos []security.Finding
			for _, f := range r.Findings {
				if f.Notice.Risky() {
					a.findingCard(c.Key(f.Key), s, v, f)
				} else {
					infos = append(infos, f)
				}
			}
			// 提示只说明这个技能会做什么，不用逐条看证据：收成一组，点了跳到文件
			if len(infos) > 0 {
				sectionLabel(c, locales.SecNoticeInfo()).Margin(8, 0, -6, 2)
				box(c, func() {
					for i, f := range infos {
						row := ui.ButtonBase(c.Key(f.Key)).MinHeight(38).Padding(6, 12).Gap(9).AlignItems(ui.Center).
							Cursor(ui.CursorPointer).Label(secSummary(f.Message))
						if i > 0 {
							row.BorderWidth(1, 0, 0, 0).BorderColor(colBorderSubtle)
						}
						tintInk(row)
						if row.Clicked() && len(f.Evidence) > 0 {
							a.jumpToEvidence(s, f.Evidence[0])
						}
						row.Children(func() {
							ui.Text(c, secSummary(f.Message)).FontSize(13).SingleLine().Shrink(1).MinWidth(0)
							ui.Spacer(c)
							if len(f.Evidence) > 0 {
								ui.Text(c, evLocation(f.Evidence[0])).Font("monospace").FontSize(12).TextColor(colTextSecondary).SingleLine().Shrink(0)
							}
						})
					}
				})
			}
			if len(r.Holes) > 0 {
				sectionLabel(c, locales.SecHolesTitle()).Margin(8, 0, -6, 2)
				box(c, func() {
					for i, h := range r.Holes {
						boxRow(c, i == 0).Children(func() {
							where := h.Path
							if where == "" {
								where = locales.SecWholeSkill()
							}
							ui.Text(c, where).Font("monospace").FontSize(12).SingleLine().MinWidth(0).Shrink(1)
							ui.Spacer(c)
							ui.Text(c, secGapLabel(h.Reason)).FontSize(12).TextColor(colTextSecondary).SingleLine().Shrink(0)
						})
					}
				})
			}
			if len(r.External) > 0 {
				sectionLabel(c, locales.SecExternalTitle()).Margin(8, 0, -6, 2)
				box(c, func() {
					for i, u := range r.External {
						boxRow(c, i == 0).Children(func() {
							ui.Text(c, u).Font("monospace").FontSize(12).TextColor(colTextBody).MinWidth(0)
						})
					}
				})
			}
		})
	})
}

// findingCard 是报告里的一条发现：类型标签、摘要、已看过、说明、证据。
func (a *app) findingCard(c *ui.Context, s *library.Skill, v library.SkillSecurity, f security.Finding) {
	ui.Column(c).Padding(12, 14).Gap(8).Radius(8).Background(colBgSubtle).Children(func() {
		ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
			tag(c, importTagSpec{text: secNoticeLabel(f.Notice), tone: secNoticeTone(f.Notice)}, true)
			ui.Text(c, secSummary(f.Message)).FontSize(13).FontWeight(600).Grow(1).MinWidth(0)
			if f.Notice.Risky() {
				acked := v.Acked[f.Key]
				b := ui.ButtonBase(c).Gap(6).AlignItems(ui.Center).Cursor(ui.CursorPointer).Label(locales.SecAcked()).Shrink(0)
				b.Children(func() {
					checkBox(c, acked, false, true, locales.SecAcked())
					ui.Text(c, locales.SecAcked()).FontSize(12).TextColor(colTextSecondary).SingleLine()
				})
				if b.Clicked() {
					key, _ := a.secPick(s)
					var err error
					if acked {
						err = a.sec.svc.Unack(key, f.Key)
					} else {
						err = a.sec.svc.Ack(key, f.Key)
					}
					if err != nil {
						a.notify(fromAppData, toastError, locales.SecAckSaveFailed())
					}
				}
			}
		})
		ui.Text(c, secDetail(f.Message)).FontSize(12).LineHeight(1.5).TextColor(colTextSecondary)
		if f.Confidence == security.Low || f.Context == security.CtxExample || f.Context == security.CtxTeaching {
			note := cond(f.Context == security.CtxExample || f.Context == security.CtxTeaching, locales.SecMaybeExample(), locales.SecWeakEvidence())
			ui.Text(c, note).FontSize(12).TextColor(colTextMuted)
		}
		for i, e := range f.Evidence {
			a.evidenceBlock(c.Key(i), s, e)
		}
	})
}

// evidenceBlock 是一处证据：位置（点了跳到文件那一行）和脱敏后的片段。
func (a *app) evidenceBlock(c *ui.Context, s *library.Skill, e security.Evidence) {
	ui.Column(c).Radius(6).Background(colBgCard).Border(1, colBorderDefault).Clip().Children(func() {
		head := ui.ButtonBase(c).Padding(4, 10).Gap(8).AlignItems(ui.Center).Cursor(ui.CursorPointer).
			Label(locales.SecOpenFile()).BorderWidth(0, 0, 1, 0).BorderColor(colBorderSubtle)
		tintInk(head)
		if head.Clicked() {
			a.jumpToEvidence(s, e)
		}
		head.Children(func() {
			parts := []string{e.Path}
			if e.Line > 0 {
				parts = append(parts, locales.SecLine(e.Line))
			}
			if e.Ref {
				parts = append(parts, locales.SecReferencedFrom())
			}
			metaLine(c, parts...).FontSize(11).TextColor(colAccent)
			ui.Spacer(c)
			ui.Icon(c, iconChevronRight).Size(10, 10).TextColor(colTextDisabled)
		})
		if e.Snippet != "" {
			ui.Text(c, e.Snippet).Font("monospace").FontSize(12).LineHeight(1.5).TextColor(colTextPrimary).Padding(6, 10).
				Background(cond(e.Ref, colBgCard, colWarnBg))
		}
	})
}

// jumpToEvidence 打开证据所在的文件，以原文显示，滚到那一行并标底色。
// 证据在同名副本里时，用编辑器打开副本里的那个文件。
func (a *app) jumpToEvidence(s *library.Skill, e security.Evidence) {
	if dir, onCopy := a.secOnCopy(s); onCopy {
		a.openInEditor(filepath.Join(dir, filepath.FromSlash(e.Path)))
		return
	}
	i := slices.IndexFunc(s.Files, func(f library.File) bool { return f.Path == e.Path })
	if i < 0 {
		return
	}
	a.file = i
	a.readerRaw, a.readerEditor, a.readerEditorKey = true, nil, ""
	a.sec.jump = secJump{key: s.ID + "\x00" + e.Path, line: e.Line}
}

// applySecurityJump 在阅读视图新建编辑器时调用：是要跳的那个文件就滚过去。
func (a *app) applySecurityJump(key string, es *editor.State) {
	j := a.sec.jump
	if j.key != key || j.line <= 0 {
		return
	}
	a.sec.jump = secJump{}
	es.LineBackgrounds = []editor.LineBackground{{First: j.line - 1, Last: j.line - 1, Color: colWarnBg}}
	es.GoTo(editor.Pos{Line: j.line - 1})
}

// ——— 整理页 ———

func secIssue(k library.Issue) bool {
	return k == library.SecurityHighRisk || k == library.SecurityWarning || k == library.SecurityUnfinished
}

// tidyKinds 是整理页列出的问题类型：安全检查的三类在最前面。
func (a *app) tidyKinds() []library.Issue {
	if !a.secOn() {
		return library.Issues
	}
	return append([]library.Issue{library.SecurityHighRisk, library.SecurityWarning, library.SecurityUnfinished}, library.Issues...)
}

type secRow struct {
	skill *library.Skill
	view  library.SkillSecurity
}

// secRows 是这一类的技能。同一份实际内容只列一次。acked 为真时列的是
// 这一类里已经全部看过的。
func (a *app) secRows(k library.Issue, acked bool) []secRow {
	if !a.secOn() {
		return nil
	}
	var out []secRow
	seen := map[string]bool{}
	for _, s := range a.lib.Skills {
		key, v := a.secPick(s)
		if seen[key] {
			continue
		}
		seen[key] = true
		if v.Report == nil {
			continue
		}
		var match bool
		switch k {
		case library.SecurityHighRisk, library.SecurityWarning:
			want := cond(k == library.SecurityHighRisk, security.HighRisk, security.Warning)
			if acked {
				match = v.Pending() == 0 && v.Report.Top() == want
			} else {
				match = v.TopPending() == want
			}
		case library.SecurityUnfinished:
			match = !acked && (v.Unfinished() || v.Fresh == library.FreshUnknown)
		}
		if match {
			out = append(out, secRow{s, v})
		}
	}
	return out
}

// securityTable 是整理页三类安全问题的表格。
func (a *app) securityTable(c *ui.Context, k library.Issue) {
	rows := a.secRows(k, false)
	if k == library.SecurityUnfinished {
		cols := []column{{locales.CommonSkills(), 0, 1}, {locales.CommonSource(), 0, 0.8}, {locales.TidyCondition(), 0, 2.4}}
		table(c, cols, 110, len(rows), func(i, j int) {
			r := rows[i]
			switch j {
			case 0:
				cellText(c, r.skill.Name)
			case 1:
				cellText(c, secSourceLabel(r.skill))
			case 2:
				cellText(c, secUnfinishedText(r.view)).Shrink(1)
			}
		}, func(i int) {
			if pushButton(c, locales.SecRescan()).Disabled(rows[i].view.Scanning || rows[i].view.Queued).Clicked() {
				a.secRescan(rows[i].skill)
			}
		}, nil)
		return
	}
	a.secFindingTable(c, k, rows)
	if hidden := a.secRows(k, true); len(hidden) > 0 {
		label := cond(a.sec.showAcked, locales.SecHideAcked(), locales.SecShowAcked(len(hidden)))
		if linkButton(c, label).Margin(10, 24).AlignSelf(ui.Start).Clicked() {
			a.sec.showAcked = !a.sec.showAcked
		}
		if a.sec.showAcked {
			ui.Column(c).Opacity(0.6).Children(func() { a.secFindingTable(c, k, hidden) })
		}
	}
}

func (a *app) secFindingTable(c *ui.Context, k library.Issue, rows []secRow) {
	want := cond(k == library.SecurityHighRisk, security.HighRisk, security.Warning)
	cols := []column{{locales.CommonSkills(), 0, 1}, {locales.CommonSource(), 0, 0.7}, {locales.SecColumnIssue(), 0, 2.6}}
	table(c, cols, 70, len(rows), func(i, j int) {
		r := rows[i]
		var first security.Finding
		more := 0
		for _, f := range r.view.Report.Findings {
			if f.Notice != want {
				continue
			}
			if first.Key == "" {
				first = f
			} else {
				more++
			}
		}
		switch j {
		case 0:
			cellText(c, r.skill.Name)
		case 1:
			cellText(c, secSourceLabel(r.skill))
		case 2:
			ui.Column(c).Gap(1).MinWidth(0).Shrink(1).Children(func() {
				ui.Row(c).Gap(6).AlignItems(ui.Center).MinWidth(0).Children(func() {
					cellText(c, secSummary(first.Message)).Shrink(1)
					if more > 0 {
						ui.Text(c, locales.SecMoreFindings(more)).FontSize(12).TextColor(colTextMuted).SingleLine().Shrink(0)
					}
				})
				if len(first.Evidence) > 0 {
					ui.Text(c, evLocation(first.Evidence[0])).Font("monospace").FontSize(11).TextColor(colTextSecondary).SingleLine().MinWidth(0)
				}
			})
		}
	}, func(i int) {
		if pushButton(c, locales.ActionView()).Clicked() {
			a.openSecurityReport(rows[i].skill)
		}
	}, nil)
}

func secSourceLabel(s *library.Skill) string {
	if s.Source == library.Project {
		return s.Project
	}
	return s.Source.Label()
}

// secIssueCount 是整理页这一类的数量（技能数）。
func (a *app) secIssueCount(k library.Issue) int { return len(a.secRows(k, false)) }

// securityProgress 是整理页顶栏的后台检查进度：首次全量检查、批量重扫时显示，
// 可以停止。
func (a *app) securityProgress(c *ui.Context) {
	if !a.secOn() {
		return
	}
	done, total := a.sec.svc.Progress()
	if total == 0 {
		return
	}
	ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
		ui.Spinner(c).Size(12, 12).TextColor(colTextMuted)
		ui.Text(c, locales.SecProgress(done, total)).FontSize(12).TextColor(colTextMuted).FontFeatures("tnum").SingleLine()
		if linkButton(c, locales.SecStop()).Clicked() {
			a.sec.svc.CancelAll()
		}
	})
}

// ——— 添加技能 ———

// scanImports 在后台检查这一批里还没检查过的暂存技能。只拿到 SKILL.md 的
// 按不完整检查，补齐后重新检查。
func (a *app) scanImports() {
	st := &a.imp
	if !library.SecuritySupported() || st.batch == nil || a.lib.Home == "" {
		return
	}
	svc := a.sec.svc
	if svc == nil {
		return
	}
	if a.sec.impBatch != st.batch {
		a.sec.impBatch, a.sec.imp, a.sec.addConfirm = st.batch, map[string]*impSecurity{}, false
	}
	b, ctx := st.batch, st.ctx
	for _, sk := range b.Skills {
		k := a.importKey(sk)
		cur := a.sec.imp[k]
		if cur != nil && (cur.scanning || cur.partial == sk.Partial && cur.dir == sk.Dir) {
			continue // 在查，或查的就是现在这一份；在查的那次结束时会再对一次
		}
		if st.fetching[k] {
			continue
		}
		// 在界面线程上取好参数：补齐会改写这个对象，后台不碰它
		dir, partial, tree := sk.Dir, sk.Partial, sk.Tree
		is := &impSecurity{scanning: true, partial: partial, dir: dir}
		if cur != nil {
			is.unpicked = cur.unpicked
		}
		a.sec.imp[k] = is
		a.holdBatch(b)
		a.background(func(post func(func())) {
			res := svc.ScanImport(ctx, dir, partial, tree)
			post(func() {
				defer a.releaseBatch(b)
				if a.sec.impBatch != b || a.sec.imp[k] != is {
					return
				}
				is.scanning = false
				is.report = &res.Report
				if sk.Partial != partial || sk.Dir != dir {
					defer a.scanImports() // 查的时候补齐了：结果是旧的那份，接着查新的
				}
				// 高风险、失败或不完整：替用户取消一次勾选，用户再勾就不再动
				if !is.unpicked && !is.partial && library.NeedsSecurityReview(res.Report) && st.picked[k] {
					is.unpicked = true
					delete(st.picked, k)
				}
			})
		})
	}
}

func (a *app) impSec(s *library.ImportSkill) *impSecurity {
	if a.sec.impBatch != a.imp.batch {
		return nil
	}
	return a.sec.imp[a.importKey(s)]
}

// importSecTag 是添加页列表里本地检查给出的标签；没有要说的返回 false。
func (a *app) importSecTag(s *library.ImportSkill) (importTagSpec, bool) {
	is := a.impSec(s)
	switch {
	case is == nil:
		return importTagSpec{}, false
	case is.scanning:
		return importTagSpec{text: locales.SecTagChecking(), tone: tagToneMuted, loading: true}, true
	case is.report == nil || s.Partial:
		return importTagSpec{}, false
	case is.report.Top() == security.HighRisk:
		return importTagSpec{text: locales.SecNoticeHighRisk(), tone: tagToneCrit, icon: symCritical}, true
	case library.NeedsSecurityReview(*is.report):
		return importTagSpec{text: locales.SecTagUnchecked(), tone: tagToneWarn, icon: symTagWarn}, true
	case is.report.Top() == security.Warning:
		return importTagSpec{text: locales.ImportScanRiskFound(), tone: tagToneWarn, icon: symTagWarn}, true
	}
	return importTagSpec{}, false
}

// importSecuritySection 是添加页预览里的「本地检查」：和 skills.sh 的
// 「安全审计」并列，放在它上面。
func (a *app) importSecuritySection(c *ui.Context, s *library.ImportSkill) {
	is := a.impSec(s)
	if is == nil {
		return
	}
	ui.Column(c).Margin(18, 0, 0, 0).Gap(6).Children(func() {
		ui.Row(c).Padding(0, 2).Children(func() { sectionLabel(c, locales.SecLocalCheck()) })
		insetGroup(c, func() {
			switch {
			case is.scanning:
				insetGroupRow(c, "sec-scanning", true, 40, func() {
					ui.Spinner(c).Size(14, 14).TextColor(colTextMuted)
					ui.Text(c, locales.SecScanning()).FontSize(13).SingleLine()
				})
				return
			case is.report == nil:
				return
			}
			r := is.report
			first := true
			for _, f := range r.Findings {
				if !f.Notice.Risky() {
					continue
				}
				insetGroupClickableRow(c, "sec-"+f.Key, secSummary(f.Message), first, 40, func() {
					ui.Image(c, cond(f.Notice == security.HighRisk, symCritical, symHigh)).Size(16, 16).Shrink(0)
					ui.Text(c, secSummary(f.Message)).FontSize(13).SingleLine().Shrink(1).MinWidth(0)
					ui.Spacer(c)
					if len(f.Evidence) > 0 {
						ui.Text(c, evLocation(f.Evidence[0])).Font("monospace").FontSize(12).TextColor(colTextSecondary).SingleLine().Shrink(0)
					}
				})
				first = false
			}
			if library.NeedsSecurityReview(*r) && r.Top() != security.HighRisk {
				insetGroupRow(c, "sec-unfinished", first, 40, func() {
					ui.Image(c, symAlert).Size(16, 16).Shrink(0)
					text := locales.SecIncomplete()
					if r.Execution == security.Failed {
						text = secFailLabel(r.Error)
					} else if len(r.Holes) > 0 {
						text = secGapLabel(r.Holes[0].Reason)
					}
					ui.Text(c, text).FontSize(13).SingleLine().MinWidth(0)
				})
				first = false
			}
			if first {
				insetGroupRow(c, "sec-clean", true, 40, func() {
					ui.Image(c, symOK).Size(16, 16).Shrink(0)
					ui.Text(c, locales.SecClean()).FontSize(13).SingleLine().Shrink(0)
					ui.Spacer(c)
					if note := secPaneNote(library.SkillSecurity{Report: r}); note != "" {
						ui.Text(c, note).FontSize(12).TextColor(colTextSecondary).SingleLine().MinWidth(0).Shrink(1)
					}
				})
			}
		})
	})
}

// importNeedsConfirm 是勾上的技能里，高风险、检查失败或不完整、还在检查的个数。
func (a *app) importNeedsConfirm() int {
	n := 0
	for _, name := range a.importAddable() {
		sk := a.imp.batch.Skill(name)
		if sk == nil {
			continue
		}
		is := a.impSec(sk)
		if is != nil && (is.scanning || is.report != nil && library.NeedsSecurityReview(*is.report)) {
			n++
		}
	}
	return n
}

// requestAddImports 是「添加」按钮：有要确认的先弹窗。
func (a *app) requestAddImports() {
	if a.importNeedsConfirm() > 0 {
		a.sec.addConfirm = true
		return
	}
	a.addImports()
}

func (a *app) importConfirmDialog(c *ui.Context) {
	if !a.sec.addConfirm {
		return
	}
	n := a.importNeedsConfirm()
	dialog(c, &a.sec.addConfirm, locales.SecAddConfirmTitle(n), locales.SecAddConfirmBody(), func() {}, func() {
		if dialogButton(c, locales.ActionCancel(), false).Clicked() {
			a.sec.addConfirm = false
		}
		if filledButton(c, locales.SecAddAnyway(), colDanger).Clicked() {
			a.sec.addConfirm = false
			a.addImports()
		}
	})
}

// ——— 更新 ———

// updateGroupOf 是更新项在页面上的分组：被安全检查拦下的归到「更新前需确认」。
func updateGroupOf(it library.UpdateItem) library.UpdateGroup {
	if _, held := library.SecurityHold(it); held {
		return library.GroupConfirm
	}
	return it.State.Group()
}

// updateReadyNotHeld 是一键更新要包含的：可更新、没被安全检查拦下。
func updateReadyNotHeld(items []library.UpdateItem) []library.UpdateItem {
	return slices.DeleteFunc(slices.Clone(items), func(it library.UpdateItem) bool {
		_, held := library.SecurityHold(it)
		return held
	})
}

func secHoldText(r security.Report) string {
	if r.Top() == security.HighRisk {
		return locales.SecUpdateHeldHigh()
	}
	return locales.SecUpdateUnchecked()
}

// scanPrepared 在后台检查准备好的新版本，对比页上显示。
func (a *app) scanPrepared(ctx context.Context, p *library.Prepared) *security.Report {
	if a.sec.svc == nil {
		return nil
	}
	r := a.sec.svc.ScanStaged(ctx, p.Dir, false).Bind(p.Item.Upstream) // 对比页上看的就是要装的这一版
	return &r
}

// updateSecurityNotice 是对比页上新版本的检查结果：有风险时列出来。
func (a *app) updateSecurityNotice(c *ui.Context, p *library.Prepared) {
	r := a.sec.updReport
	if a.sec.updFor != p || r == nil || !r.Top().Risky() && !library.NeedsSecurityReview(*r) {
		return
	}
	review := library.NeedsSecurityReview(*r)
	bg, border, fg := colWarnBg, colWarnEdge, colWarnText
	if r.Top() == security.HighRisk {
		bg, border, fg = colDangerBg, colDangerEdge, colDangerText
	}
	ui.Column(c).Shrink(0).Padding(8, 14).Gap(4).Background(bg).BorderWidth(0, 0, 1, 0).BorderColor(border).Children(func() {
		ui.Row(c).Gap(6).AlignItems(ui.Center).TextColor(fg).Children(func() {
			ui.Icon(c, iconShieldBare).Size(12, 12)
			text := locales.SecLocalCheck()
			if review {
				text = secHoldText(*r)
			}
			ui.Text(c, text).FontSize(12).FontWeight(600).SingleLine()
		})
		shown := 0
		for _, f := range r.Findings {
			if !f.Notice.Risky() || shown == 5 {
				continue
			}
			shown++
			ui.Row(c).Gap(8).AlignItems(ui.Center).MinWidth(0).Children(func() {
				ui.Text(c, secSummary(f.Message)).FontSize(12).TextColor(colTextBody).SingleLine().Shrink(1).MinWidth(0)
				if len(f.Evidence) > 0 {
					ui.Text(c, evLocation(f.Evidence[0])).Font("monospace").FontSize(11).TextColor(colTextSecondary).SingleLine().Shrink(0)
				}
			})
		}
	})
}

// updateNeedsApproval 报告对比页的「更新」要不要写成「仍然更新」并带上放行。
func (a *app) updateNeedsApproval(p *library.Prepared) bool {
	if _, held := library.SecurityHold(p.Item); held {
		return true
	}
	return a.sec.updFor == p && a.sec.updReport != nil && library.NeedsSecurityReview(*a.sec.updReport)
}

// securityHint 由目录监听在后台调：变化落在哪个技能里，就把它标过期并排队重查。
func (a *app) securityHint(paths []string) {
	if svc := a.sec.svc; svc != nil {
		svc.Hint(paths)
	}
}
