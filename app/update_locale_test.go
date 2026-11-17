package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"skillmanager/internal/library"
	"skillmanager/locales"
)

// localeRemote 是按仓库给结果的假上游：tree 给目录树，err 让这个仓库的请求失败。
type localeRemote struct {
	calls atomic.Int32
	tree  map[string][]library.TreeEntry
	err   map[string]error
}

func (r *localeRemote) Commit(_ context.Context, repo, _ string) (library.Commit, error) {
	r.calls.Add(1)
	if err := r.err[repo]; err != nil {
		return library.Commit{}, err
	}
	return library.Commit{SHA: "3f8a21c000000000000000000000000000000000", Tree: "4b825dc642cb6eb9a060e54bf8d69288fbee4904", Date: time.Now()}, nil
}

func (r *localeRemote) Tree(_ context.Context, repo, _ string) (library.Tree, error) {
	r.calls.Add(1)
	return library.Tree{Entries: r.tree[repo]}, nil
}

func (r *localeRemote) File(context.Context, string, string, string) ([]byte, error) {
	r.calls.Add(1)
	return nil, errors.New("unexpected download")
}

// installGitHubSkills 在 home 装好技能，并写下它们从 GitHub 哪个仓库装来（name → repo）。
func installGitHubSkills(t *testing.T, home string, repos map[string]string) {
	t.Helper()
	records := map[string]any{}
	for name, repo := range repos {
		dir := filepath.Join(home, ".agents/skills", name)
		must(t, os.MkdirAll(dir, 0o755))
		must(t, os.WriteFile(filepath.Join(dir, "SKILL.md"), fmt.Appendf(nil, "---\nname: %s\ndescription: local\n---\n", name), 0o644))
		hash, err := library.TreeHash(dir)
		must(t, err)
		records[name] = map[string]any{
			"source": repo, "sourceType": "github", "sourceUrl": "https://github.com/" + repo + ".git",
			"skillPath": "skills/" + name + "/SKILL.md", "skillFolderHash": hash,
			"installedAt": "2026-08-20T01:02:03.000Z", "updatedAt": "2026-08-20T01:02:03.000Z",
		}
	}
	b, err := json.MarshalIndent(map[string]any{"version": 3, "skills": records}, "", "  ")
	must(t, err)
	must(t, os.WriteFile(filepath.Join(home, ".agents/.skill-lock.json"), b, 0o644))
}

func TestUpdateResultsSwitchLanguageWithoutCheckingAgain(t *testing.T) {
	// Given 用中文检查过更新：write-docs 在上游换了路径，另一个仓库额度用完，
	// 还有一个仓库登录凭证失效
	t.Cleanup(func() { testLocale.Store("") })
	home := t.TempDir()
	installGitHubSkills(t, home, map[string]string{"write-docs": "acme/docs", "quota-skill": "acme/quota", "private-skill": "acme/private"})
	reset := time.Now().Add(30 * time.Minute)
	remote := &localeRemote{
		tree: map[string][]library.TreeEntry{"acme/docs": {{Path: "docs/write-docs/SKILL.md", Mode: "100644", Type: "blob", SHA: "1111111111111111111111111111111111111111", Size: 10}}},
		err: map[string]error{
			"acme/quota":   &library.RemoteError{Kind: library.RemoteQuota, Reset: reset},
			"acme/private": &library.RemoteError{Kind: library.RemoteAuth, Status: 401},
		},
	}
	must(t, library.Scan(home).CheckUpdates(context.Background(), remote, nil))
	calls := remote.calls.Load()

	a := newApp(library.Scan(home))
	a.scope = scopeUpdates
	tt := newUITester(a.view, 1280, 900)
	shown := func(m locales.Messages) []string {
		return []string{m.UpdateUpstreamPathChanged(), m.UpdateGitHubQuota(reset.Local().Format("15:04")), m.UpdateGithubCredentialsExpired()}
	}
	zh, en := locales.In("zh-Hans"), locales.In("en")
	expect := func(when string, want, gone []string) {
		t.Helper()
		tt.Frame()
		for _, s := range want {
			if !tt.HasText(s) {
				t.Errorf("%s：应显示 %q：%q", when, s, tt.Texts())
			}
		}
		for _, s := range gone {
			if tt.HasText(s) {
				t.Errorf("%s：不该还显示 %q", when, s)
			}
		}
	}
	expect("中文检查后", shown(zh), shown(en))
	report := len(a.upd.report.Items)

	// When 在设置里换成英文
	a.setLanguage("en")
	tt.SetLocale("en")

	// Then 同一页的结果马上用英文说
	expect("换成英文后", shown(en), shown(zh))

	// And 换回中文也跟着回来
	a.setLanguage("zh-Hans")
	tt.SetLocale("zh-Hans")
	expect("换回中文后", shown(zh), shown(en))

	// And 换语言不重新检查，结果和恢复时间都还在
	if remote.calls.Load() != calls {
		t.Errorf("换语言不该联网：%d → %d", calls, remote.calls.Load())
	}
	if len(a.upd.report.Items) != report || a.upd.report.Reset.IsZero() {
		t.Errorf("换语言丢了检查结果：%+v", a.upd.report)
	}
	if dir := os.Getenv("SNAPSHOT_DIR"); dir != "" {
		for _, tag := range []string{"en", "zh-Hans"} {
			a.setLanguage(tag)
			tt.SetLocale(tag)
			tt.Advance(time.Second) // 按钮文字换语言时有淡入淡出，等它走完再拍
			writePNG(t, filepath.Join(dir, "update-locale-"+tag+".png"), tt)
		}
	}
}
