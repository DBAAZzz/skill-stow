package library

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/egoist/mygo/i18n"

	"skillmanager/locales"
)

// speak 让这个测试按 tag 的语言说话，测试结束换回默认。
func speak(t *testing.T, tag *string) {
	t.Helper()
	i18n.SetLocaleFunc(func() string { return *tag })
	t.Cleanup(func() { i18n.SetLocaleFunc(nil) })
}

var zh, en = locales.In("zh-Hans"), locales.In("en")

// reason 是条目显示的说明。用 fmt 取，说明是文字还是按语言现说的值都一样比。
func reason(it UpdateItem) string { return fmt.Sprint(it.Reason) }

func TestCheckResultsFollowTheInterfaceLanguageWithoutRechecking(t *testing.T) {
	// Given 用中文检查过：vite 在上游换了路径
	lang := "zh-Hans"
	speak(t, &lang)
	home, _, r, _ := viteHome(t)
	r.publish(t, "antfu/skills", map[string]string{"new/vite/SKILL.md": skillMD("vite", "")})
	_, rep := checkNow(t, home, r)
	it := itemNamed(t, rep, "vite")
	if it.State != UpdRelocated || reason(it) != zh.UpdateUpstreamPathChanged() {
		t.Fatalf("中文检查结果：%s %q", it.State, reason(it))
	}
	calls := len(r.calls)

	// When 界面换成英文
	lang = "en"

	// Then 手里这份结果和重新读盘的结果都用英文说，不联网
	if got := reason(it); got != en.UpdateUpstreamPathChanged() {
		t.Errorf("已有结果应换成英文：%q", got)
	}
	if got := reason(itemNamed(t, Scan(home).Updates(), "vite")); got != en.UpdateUpstreamPathChanged() {
		t.Errorf("读盘的结果应换成英文：%q", got)
	}
	// And 换回中文也跟着回来
	lang = "zh-Hans"
	if got := reason(it); got != zh.UpdateUpstreamPathChanged() {
		t.Errorf("换回中文：%q", got)
	}
	if len(r.calls) != calls {
		t.Errorf("换语言不该联网：%v", r.calls[calls:])
	}
}

func TestRemoteFailuresAreSavedAsKindsAndSpokenInTheCurrentLanguage(t *testing.T) {
	reset := time.Date(2026, 11, 17, 16, 27, 0, 0, time.Local)
	for _, c := range []struct {
		name string
		err  error
		say  func(locales.Messages) string
	}{
		{"quota", &RemoteError{Kind: RemoteQuota, Reset: reset}, func(m locales.Messages) string { return m.UpdateGitHubQuota("16:27") }},
		{"throttled", &RemoteError{Kind: RemoteThrottled}, locales.Messages.UpdateGithubRateLimited},
		{"not found", &RemoteError{Kind: RemoteNotFound, Status: 404}, locales.Messages.UpdateGithubRepositoryUnavailable},
		{"network", &RemoteError{Kind: RemoteNetwork}, locales.Messages.UpdateGithubConnectionFailed},
		{"auth", &RemoteError{Kind: RemoteAuth, Status: 401}, locales.Messages.UpdateGithubCredentialsExpired},
		{"status", &RemoteError{Kind: RemoteOther, Status: 502}, func(m locales.Messages) string { return m.UpdateGithubStatusError(502) }},
		{"invalid", &RemoteError{Kind: RemoteOther}, locales.Messages.UpdateGithubInvalidResponse},
	} {
		t.Run(c.name, func(t *testing.T) {
			lang := "zh-Hans"
			speak(t, &lang)
			home, _, r, _ := viteHome(t)
			r.fail["antfu/skills"] = c.err
			_, rep := checkNow(t, home, r)
			it := itemNamed(t, rep, "vite")
			if it.State != UpdFailed || rep.Complete || reason(it) != c.say(zh) {
				t.Fatalf("中文：%s complete=%v %q", it.State, rep.Complete, reason(it))
			}
			lang = "en"
			if got := reason(itemNamed(t, Scan(home).Updates(), "vite")); got != c.say(en) {
				t.Errorf("英文：%q，想要 %q", got, c.say(en))
			}
			// 失败归类不变：额度用完仍带恢复时间
			if c.name == "quota" && !Scan(home).Updates().Reset.Equal(reset) {
				t.Error("额度用完应带恢复时间")
			}
		})
	}
}

func TestUpstreamFileProblemsAndBlockedUpdatesFollowTheLanguage(t *testing.T) {
	lang := "zh-Hans"
	speak(t, &lang)

	// 上游有不支持的文件：检查时记下
	home, _, r, _ := viteHome(t)
	r.publish(t, "antfu/skills", map[string]string{"skills/vite/SKILL.md": skillMD("vite", "v3"), "skills/vite/l": "->SKILL.md"})
	checkNow(t, home, r)
	lang = "en"
	if it := itemNamed(t, Scan(home).Updates(), "vite"); it.State != UpdFailed || reason(it) != en.UpdateUnsupportedFiles() {
		t.Errorf("不支持的文件：%s %q", it.State, reason(it))
	}

	// 准备更新时发现改了名：拦下这一条
	lang = "zh-Hans"
	home, _, r, _ = viteHome(t)
	r.publish(t, "antfu/skills", map[string]string{"skills/vite/SKILL.md": skillMD("renamed", "new")})
	l, rep := checkNow(t, home, r)
	_, err := l.PrepareUpdate(context.Background(), r, itemNamed(t, rep, "vite"))
	if !errors.Is(err, ErrSkillRenamed) {
		t.Fatalf("改名应仍认得出：%v", err)
	}
	lang = "en"
	if it := itemNamed(t, Scan(home).Updates(), "vite"); it.State != UpdRenamed || reason(it) != en.UpdateSkillRenamed() {
		t.Errorf("改名：%s %q", it.State, reason(it))
	}
}

func TestUnknownFailuresKeepTheirOriginalText(t *testing.T) {
	lang := "zh-Hans"
	speak(t, &lang)
	home, _, r, _ := viteHome(t)
	// 套了一层的远端错误和不认识的错误都原样留着，不硬归成某一类
	r.fail["antfu/skills"] = fmt.Errorf("proxy said no: %w", errors.New("tls handshake"))
	_, rep := checkNow(t, home, r)
	lang = "en"
	it := itemNamed(t, Scan(home).Updates(), "vite")
	if it.State != UpdFailed || reason(it) != "proxy said no: tls handshake" || rep.Complete {
		t.Errorf("未知错误：%s %q", it.State, reason(it))
	}
}

// editCheck 改 updates.json 里上次检查的仓库和技能结果（JSON 原样）。
func editCheck(t *testing.T, home string, fn func(repo, skill map[string]any)) {
	t.Helper()
	b, err := os.ReadFile(updatesFile(home))
	must(t, err)
	var st map[string]any
	must(t, json.Unmarshal(b, &st))
	check := st["check"].(map[string]any)
	fn(check["repos"].([]any)[0].(map[string]any), check["skills"].([]any)[0].(map[string]any))
	b, err = json.Marshal(st)
	must(t, err)
	must(t, os.WriteFile(updatesFile(home), b, 0o644))
}

func TestResultsSavedByOlderVersionsStillLoad(t *testing.T) {
	lang := "zh-Hans"
	speak(t, &lang)
	reset := time.Date(2026, 11, 17, 16, 27, 0, 0, time.Local)

	// Given 旧版本存下的额度用完：只有中文原话和恢复时间
	home, _, r, _ := viteHome(t)
	r.fail["antfu/skills"] = &RemoteError{Kind: RemoteQuota, Reset: reset}
	checkNow(t, home, r)
	editCheck(t, home, func(repo, _ map[string]any) {
		delete(repo, "failure")
		repo["fail"] = "GitHub 请求额度已用完，16:27 后可以再查"
	})
	lang = "en"
	rep := Scan(home).Updates()
	if it := itemNamed(t, rep, "vite"); it.State != UpdFailed || reason(it) != en.UpdateGitHubQuota("16:27") || it.CanUpdate() {
		t.Errorf("旧的额度结果应按英文说：%s %q", it.State, reason(it))
	}
	if rep.Complete || !rep.Reset.Equal(reset) {
		t.Errorf("旧结果的归类不变：complete=%v reset=%v", rep.Complete, rep.Reset)
	}

	// Given 旧版本存下的其他失败：认不出种类，原话照旧显示，仍算失败
	lang = "zh-Hans"
	home, _, r, _ = viteHome(t)
	r.fail["antfu/skills"] = &RemoteError{Kind: RemoteNetwork}
	checkNow(t, home, r)
	editCheck(t, home, func(repo, _ map[string]any) {
		delete(repo, "failure")
		repo["fail"] = "旧版本的说法"
	})
	lang = "en"
	if it := itemNamed(t, Scan(home).Updates(), "vite"); it.State != UpdFailed || reason(it) != "旧版本的说法" {
		t.Errorf("旧的未知失败应原样保留：%s %q", it.State, reason(it))
	}

	// Given 旧版本存下的技能级失败和拦下的原因
	lang = "zh-Hans"
	home, _, r, _ = viteHome(t)
	checkNow(t, home, r)
	editCheck(t, home, func(_, skill map[string]any) { skill["fail"] = "旧的文件问题" })
	if it := itemNamed(t, Scan(home).Updates(), "vite"); it.State != UpdFailed || reason(it) != "旧的文件问题" {
		t.Errorf("旧的 Skill 失败：%s %q", it.State, reason(it))
	}
	editCheck(t, home, func(_, skill map[string]any) {
		delete(skill, "fail")
		skill["blocked"], skill["reason"] = string(UpdRenamed), "旧的拦下原因"
	})
	if it := itemNamed(t, Scan(home).Updates(), "vite"); it.State != UpdRenamed || reason(it) != "旧的拦下原因" {
		t.Errorf("旧的拦下原因：%s %q", it.State, reason(it))
	}
}

func TestSavedFailuresRecordTheirKind(t *testing.T) {
	home, _, r, _ := viteHome(t)
	r.fail["antfu/skills"] = &RemoteError{Kind: RemoteAuth, Status: 401}
	checkNow(t, home, r)
	editCheck(t, home, func(repo, _ map[string]any) {
		f, _ := repo["failure"].(map[string]any)
		if f == nil || f["kind"] == nil || f["kind"] == "" {
			t.Errorf("失败应按种类存：%v", repo)
		}
	})
}
