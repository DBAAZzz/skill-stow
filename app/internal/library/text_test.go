package library

import (
	"errors"
	"fmt"
	"testing"

	"github.com/egoist/mygo/i18n"
)

func TestSentinelErrorsSpeakTheCurrentLanguageAndStillMatch(t *testing.T) {
	// Given 包级的哨兵错误在启动时就建好了
	wrapped := fmt.Errorf("删除 git-flow：%w", ErrHasCopies)

	// When 之后界面换成英文
	i18n.SetLocaleFunc(func() string { return "en" })
	t.Cleanup(func() { i18n.SetLocaleFunc(nil) })

	// Then 它用英文说，包了一层也还认得出来
	if got := ErrHasCopies.Error(); got != "There are other copies with the same name. Merge them into one before deleting." {
		t.Errorf("应按当前语言说：%q", got)
	}
	if !errors.Is(wrapped, ErrHasCopies) {
		t.Error("errors.Is 应认得包过的哨兵错误")
	}
}

func TestLabelsAndSentencesFollowTheInterfaceLanguage(t *testing.T) {
	// Given 界面先是中文
	if got := StateRestart.Label(); got != "需要重启" {
		t.Errorf("中文状态词：%q", got)
	}
	if got := joinSentences("可在会话中执行 /skills reload 立即生效。", "命令行仍可直接调用。"); got != "可在会话中执行 /skills reload 立即生效。命令行仍可直接调用。" {
		t.Errorf("中文句子之间不空格：%q", got)
	}

	// When 换成英文
	i18n.SetLocaleFunc(func() string { return "en" })
	t.Cleanup(func() { i18n.SetLocaleFunc(nil) })

	// Then 状态词、分组标题、列举、句子间距、Agent 名都跟着换
	for got, want := range map[string]string{
		StateRestart.Label():                    "Restart Needed",
		GroupHandle.Label():                     "Needs Attention",
		JoinAnd([]string{"Cursor", "Codex"}):    "Cursor and Codex",
		joinSentences("Restart it.", "Or not."): "Restart it. Or not.",
		AgentName("lingma"):                     "Lingma",
	} {
		if got != want {
			t.Errorf("got %q, want %q", got, want)
		}
	}
}
