package library

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func testGitHub(t *testing.T, h http.HandlerFunc) *GitHub {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	g := NewGitHub("")
	g.API, g.Git, g.Raw = srv.URL, srv.URL, srv.URL+"/raw"
	g.sleep = func(context.Context, time.Duration) error { return nil }
	return g
}

func TestGitHubDownloadsAFileAtACommit(t *testing.T) {
	g := testGitHub(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/raw/antfu/skills/c1/skills/vite/SKILL.md" {
			http.NotFound(w, r)
			return
		}
		fmt.Fprint(w, "hello")
	})
	b, err := g.File(context.Background(), "antfu/skills", "c1", "skills/vite/SKILL.md")
	if err != nil || string(b) != "hello" {
		t.Errorf("file = %q %v", b, err)
	}
}

func TestGitHubQuotaReportsWhenItResets(t *testing.T) {
	reset := time.Now().Add(40 * time.Minute).Truncate(time.Second)
	g := testGitHub(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-RateLimit-Remaining", "0")
		w.Header().Set("X-RateLimit-Reset", fmt.Sprint(reset.Unix()))
		w.WriteHeader(http.StatusForbidden)
	})
	_, err := g.PathCommit(context.Background(), "a/b", "", "skills/x")
	var re *RemoteError
	if !errors.As(err, &re) || re.Kind != RemoteQuota {
		t.Fatalf("err = %v", err)
	}
	if d := re.Reset.Sub(reset); d < -2*time.Second || d > 2*time.Second {
		t.Errorf("恢复时间 %v，want %v", re.Reset, reset)
	}
	if !strings.Contains(err.Error(), "GitHub 请求额度已用完") || !strings.Contains(err.Error(), reset.Local().Format("15:04")) {
		t.Errorf("文案：%v", err)
	}
}

func TestGitHubWaitsOutAThrottleOnceThenGivesUp(t *testing.T) {
	var calls atomic.Int32
	g := testGitHub(t, func(w http.ResponseWriter, r *http.Request) {
		n := calls.Add(1)
		if r.URL.Path == "/repos/a/ok/commits" && n == 2 {
			fmt.Fprint(w, `[{"sha":"c","commit":{"tree":{"sha":"t"},"committer":{"date":"2026-11-10T08:00:00Z"}}}]`)
			return
		}
		w.Header().Set("X-RateLimit-Remaining", "12")
		w.Header().Set("Retry-After", "3")
		w.WriteHeader(http.StatusTooManyRequests)
	})
	var waited time.Duration
	g.sleep = func(_ context.Context, d time.Duration) error { waited += d; return nil }
	if _, err := g.PathCommit(context.Background(), "a/ok", "", "skills/x"); err != nil || waited != 3*time.Second {
		t.Errorf("等 Retry-After 后重试一次应成功：%v，等了 %v", err, waited)
	}
	calls.Store(10)
	_, err := g.PathCommit(context.Background(), "a/bad", "", "skills/x")
	var re *RemoteError
	if !errors.As(err, &re) || re.Kind != RemoteThrottled {
		t.Errorf("重试一次仍失败应报限流：%v", err)
	}
}

func TestGitHubNotFoundIsNotAnUpstreamRemoval(t *testing.T) {
	g := testGitHub(t, func(w http.ResponseWriter, r *http.Request) { http.NotFound(w, r) })
	_, err := g.Commit(context.Background(), "gone/repo", "")
	var re *RemoteError
	if !errors.As(err, &re) || re.Kind != RemoteNotFound || err.Error() != "仓库访问不了（可能已删除或转为私有）" {
		t.Errorf("err = %v", err)
	}
}

func TestGitHubUnreachableIsANetworkFailure(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	srv.Close()
	g := NewGitHub("")
	g.API, g.Git = srv.URL, srv.URL
	_, err := g.Commit(context.Background(), "a/b", "")
	var re *RemoteError
	if !errors.As(err, &re) || re.Kind != RemoteNetwork || err.Error() != "连不上 GitHub" {
		t.Errorf("err = %v", err)
	}
}

func TestGitHubStopsWhenCanceled(t *testing.T) {
	g := testGitHub(t, func(w http.ResponseWriter, r *http.Request) { time.Sleep(200 * time.Millisecond) })
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := g.Commit(ctx, "a/b", ""); !errors.Is(err, context.Canceled) {
		t.Errorf("取消：%v", err)
	}
}

func TestSystemProxyIsReadFromScutil(t *testing.T) {
	out := `<dictionary> {
  ExceptionsList : <array> {
    0 : *.local
  }
  HTTPEnable : 1
  HTTPPort : 7890
  HTTPProxy : 127.0.0.1
  HTTPSEnable : 1
  HTTPSPort : 7891
  HTTPSProxy : 10.0.0.2
}`
	if got := parseScutilProxy(out); got != "http://10.0.0.2:7891" {
		t.Errorf("HTTPS 代理：%q", got)
	}
	if got := parseScutilProxy("<dictionary> {\n  HTTPSEnable : 0\n}"); got != "" {
		t.Errorf("没开代理：%q", got)
	}
}

func TestAManualProxyWinsOverTheSystemOne(t *testing.T) {
	p := proxyFor("127.0.0.1:1080", func() string { return "http://sys:1" })
	req, _ := http.NewRequest("GET", "https://api.github.com/", nil)
	u, err := p(req)
	if err != nil || u == nil || u.String() != "http://127.0.0.1:1080" {
		t.Errorf("手动代理：%v %v", u, err)
	}
	p = proxyFor("", func() string { return "http://sys:1" })
	if u, _ := p(req); u == nil || u.Host != "sys:1" {
		t.Errorf("系统代理：%v", u)
	}
}

func TestManualOnlyProxyNeverFallsBackToEnvironment(t *testing.T) {
	t.Setenv("HTTPS_PROXY", "http://environment.invalid:7890")
	g := NewUpdateRemote(Settings{UpdateProxyMode: "manual"})
	req, _ := http.NewRequest("GET", "https://api.github.com/", nil)
	tr := g.Client.Transport.(*http.Transport)
	if tr.Proxy != nil {
		u, err := tr.Proxy(req)
		must(t, err)
		if u != nil {
			t.Fatalf("manual-only used %s", u)
		}
	}
}

func TestGitHubUsesGHTokenOnlyWhenEnabled(t *testing.T) {
	orig := ghTokenFunc
	t.Cleanup(func() { ghTokenFunc = orig })
	called := 0
	ghTokenFunc = func() (string, string) {
		called++
		return "gho_secret_token", ""
	}
	anon := NewUpdateRemote(Settings{UseGHAuth: false})
	if called != 0 || anon.Authenticated() {
		t.Fatalf("unchecked UseGHAuth touched gh: called=%d auth=%v", called, anon.Authenticated())
	}

	authed := NewUpdateRemote(Settings{UseGHAuth: true})
	if called != 1 || !authed.Authenticated() || authed.Token != "gho_secret_token" {
		t.Fatalf("checked UseGHAuth did not read token: called=%d token=%q", called, authed.Token)
	}

	var gotAuth string
	g := testGitHub(t, func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		if gotAuth == "Bearer expired" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		fmt.Fprint(w, `[{"sha":"c1","commit":{"tree":{"sha":"t1"},"committer":{"date":"2026-11-10T08:00:00Z"}}}]`)
	})
	g.Token = "gho_secret_token"
	if _, err := g.PathCommit(context.Background(), "a/b", "", "skills/x"); err != nil {
		t.Fatalf("authed Commit: %v", err)
	}
	if gotAuth != "Bearer gho_secret_token" {
		t.Fatalf("Authorization = %q", gotAuth)
	}

	g.Token = "expired"
	_, err := g.PathCommit(context.Background(), "a/b", "", "skills/x")
	var re *RemoteError
	if !errors.As(err, &re) || re.Kind != RemoteAuth {
		t.Fatalf("expected RemoteAuth on 401, got %v", err)
	}
}

func TestGitHubPathCommitQueriesSubdirLatestCommit(t *testing.T) {
	g := testGitHub(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/repos/anthropics/skills/commits" {
			http.NotFound(w, r)
			return
		}
		if r.URL.Query().Get("path") != "skills/web-artifacts-builder" || r.URL.Query().Get("sha") != "headsha" || r.URL.Query().Get("per_page") != "1" {
			t.Fatalf("unexpected query: %s", r.URL.RawQuery)
		}
		fmt.Fprint(w, `[{"sha":"dec2025","commit":{"tree":{"sha":"t2025"},"committer":{"date":"2025-12-02T10:15:00Z"}}}]`)
	})
	c, err := g.PathCommit(context.Background(), "anthropics/skills", "headsha", "skills/web-artifacts-builder")
	must(t, err)
	if c.SHA != "dec2025" || c.Date.Year() != 2025 || c.Date.Month() != time.December {
		t.Fatalf("unexpected PathCommit: %+v", c)
	}
}
