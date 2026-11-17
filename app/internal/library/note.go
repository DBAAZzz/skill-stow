package library

import (
	"time"

	"skillmanager/locales"
)

// Note 是更新结果里给人看的一句说明。存的是说明的种类和参数，String() 每次
// 按界面当前的语言现说：检查结果在内存里或存进 updates.json 后，切换语言都
// 跟着换，不用重新检查。种类认不出的（系统错误、旧版本存下的话）存原话，
// 原样显示。
type Note struct {
	Kind   noteKind  `json:"kind,omitempty"`
	Status int       `json:"status,omitempty"` // noteGitHubStatus：HTTP 状态码
	Reset  time.Time `json:"reset,omitempty"`  // noteGitHubQuota：额度恢复时间
	Detail string    `json:"detail,omitempty"` // 原话；带参数的种类是参数
}

// noteKind 存进 updates.json，取值不能改；新说法加新种类。
type noteKind string

const (
	noteNotChecked          noteKind = "not-checked"
	noteMissingFromLibrary  noteKind = "missing-from-library"
	noteSourceUnsupported   noteKind = "source-unsupported"
	noteNotAutomatic        noteKind = "not-automatic"
	noteIncompleteRecord    noteKind = "incomplete-install-record"
	noteChecksStopped       noteKind = "checks-stopped"
	noteLocationUnsupported noteKind = "location-unsupported"
	noteCopiesNeedMerge     noteKind = "copies-need-merge"
	noteSourcePathMissing   noteKind = "source-path-missing"
	noteLocalUnreadable     noteKind = "local-unreadable" // Detail：系统给的原因
	noteUpstreamRemoved     noteKind = "upstream-removed"
	noteUpstreamMoved       noteKind = "upstream-moved"
	noteUpstreamAmbiguous   noteKind = "upstream-ambiguous"
	noteAlreadyCurrent      noteKind = "already-current"
	noteBaselineUnknown     noteKind = "baseline-unknown"
	noteLocalChangesOnly    noteKind = "local-changes-only"
	noteAvailable           noteKind = "available"
	noteBothChanged         noteKind = "local-and-upstream-changed"
	noteLocalVersionKept    noteKind = "local-version-kept"
	notePreviousUnfinished  noteKind = "previous-unfinished"
	noteCleanupPending      noteKind = "old-version-cleanup-pending"
	noteRecoveryStopped     noteKind = "recovery-stopped"
	noteStateUnreadable     noteKind = "state-unreadable" // Detail：系统给的原因
	noteRecordUnusable      noteKind = "record-unusable"
	noteTreeTruncated       noteKind = "tree-truncated"
	noteTooManyFiles        noteKind = "too-many-files"
	noteInvalidUpstreamPath noteKind = "invalid-upstream-path"
	noteFilenameConflict    noteKind = "upstream-filename-conflict"
	noteUnsupportedFiles    noteKind = "unsupported-files"
	noteSingleFileTooLarge  noteKind = "single-file-too-large"
	noteSkillTooLarge       noteKind = "skill-too-large"
	noteSkillRenamed        noteKind = "skill-renamed"
	noteGitHubQuota         noteKind = "github-quota" // Reset
	noteGitHubThrottled     noteKind = "github-throttled"
	noteGitHubNotFound      noteKind = "github-not-found"
	noteGitHubNetwork       noteKind = "github-network"
	noteGitHubAuth          noteKind = "github-auth"
	noteGitHubStatus        noteKind = "github-status" // Status
	noteGitHubInvalidReply  noteKind = "github-invalid-response"
)

// noteSays 是不带参数的种类的说法。
var noteSays = map[noteKind]func() string{
	noteNotChecked:          locales.UpdateNotChecked,
	noteMissingFromLibrary:  locales.UpdateMissingFromLibrary,
	noteSourceUnsupported:   locales.UpdateSourceUnsupported,
	noteNotAutomatic:        locales.UpdateNotAutomatic,
	noteIncompleteRecord:    locales.UpdateIncompleteInstallRecord,
	noteChecksStopped:       locales.UpdateChecksStopped,
	noteLocationUnsupported: locales.UpdateLocationUnsupported,
	noteCopiesNeedMerge:     locales.UpdateIndependentCopiesNeedMerge,
	noteSourcePathMissing:   locales.UpdateInstallSourcePathMissing,
	noteUpstreamRemoved:     locales.UpdateUpstreamSkillRemoved,
	noteUpstreamMoved:       locales.UpdateUpstreamPathChanged,
	noteUpstreamAmbiguous:   locales.UpdateUpstreamNameAmbiguous,
	noteAlreadyCurrent:      locales.UpdateAlreadyCurrent,
	noteBaselineUnknown:     locales.UpdateBaselineUnknown,
	noteLocalChangesOnly:    locales.UpdateLocalChangesOnly,
	noteAvailable:           locales.UpdateAvailable,
	noteBothChanged:         locales.UpdateLocalAndUpstreamChanges,
	noteLocalVersionKept:    locales.UpdateLocalVersionKept,
	notePreviousUnfinished:  locales.UpdatePreviousUnfinished,
	noteCleanupPending:      locales.UpdateOldVersionCleanupPending,
	noteRecoveryStopped:     locales.UpdateRecoveryStoppedFilesKept,
	noteRecordUnusable:      locales.RecordUnusable,
	noteTreeTruncated:       locales.UpdateRepositoryTreeTruncated,
	noteTooManyFiles:        locales.UpdateTooManyFiles,
	noteInvalidUpstreamPath: locales.UpdateInvalidUpstreamPath,
	noteFilenameConflict:    locales.UpdateUpstreamFilenameConflict,
	noteUnsupportedFiles:    locales.UpdateUnsupportedFiles,
	noteSingleFileTooLarge:  locales.UpdateSingleFileTooLarge,
	noteSkillTooLarge:       locales.UpdateSkillTooLarge,
	noteSkillRenamed:        locales.UpdateSkillRenamed,
	noteGitHubThrottled:     locales.UpdateGithubRateLimited,
	noteGitHubNotFound:      locales.UpdateGithubRepositoryUnavailable,
	noteGitHubNetwork:       locales.UpdateGithubConnectionFailed,
	noteGitHubAuth:          locales.UpdateGithubCredentialsExpired,
	noteGitHubInvalidReply:  locales.UpdateGithubInvalidResponse,
}

func (n Note) String() string {
	switch n.Kind {
	case "":
		return n.Detail
	case noteLocalUnreadable:
		return locales.UpdateLocalUnreadable(n.Detail)
	case noteStateUnreadable:
		return locales.UpdateStateUnreadable(n.Detail)
	case noteGitHubQuota:
		return locales.UpdateGitHubQuota(n.Reset.Local().Format("15:04"))
	case noteGitHubStatus:
		return locales.UpdateGithubStatusError(n.Status)
	}
	if say := noteSays[n.Kind]; say != nil {
		return say()
	}
	// 新版本存下、这一版不认识的种类：有原话说原话
	return n.Detail
}

// IsZero 报告是不是没有说明。
func (n Note) IsZero() bool { return n == Note{} }

func note(k noteKind) Note { return Note{Kind: k} }

// noteError 是按种类说的错误：Error() 按当前语言现说，存进检查结果时存种类。
// 用指针，errors.Is 才比得了。
type noteError struct{ note Note }

func (e *noteError) Error() string { return e.note.String() }

func noteErr(k noteKind) error { return &noteError{note(k)} }

// noteOf 是存进检查结果的说明。只认本身就是 RemoteError、noteError 的错误；
// 套过一层的带着别的上下文，种类说不全，和认不出的错误一样存原话。
func noteOf(err error) Note {
	switch e := err.(type) {
	case *RemoteError:
		return e.note()
	case *noteError:
		return e.note
	}
	return Note{Detail: err.Error()}
}

// legacyNote 是旧版本只存了原话的说明：认不出种类，原样显示。
func legacyNote(text string) *Note {
	if text == "" {
		return nil
	}
	return &Note{Detail: text}
}
