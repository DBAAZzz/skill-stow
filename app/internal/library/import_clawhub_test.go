package library

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func clawHubZip(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, body := range files {
		hdr := &zip.FileHeader{Name: strings.TrimSuffix(name, "*"), Method: zip.Deflate}
		if strings.HasSuffix(name, "*") {
			hdr.SetMode(0o755)
		} else {
			hdr.SetMode(0o644)
		}
		w, err := zw.CreateHeader(hdr)
		must(t, err)
		_, err = w.Write([]byte(body))
		must(t, err)
	}
	must(t, zw.Close())
	return buf.Bytes()
}

func TestClawHubSkillDownloadsZipAndRecordsClawHubLockEntry(t *testing.T) {
	home, h := importHome(t)
	payload := clawHubZip(t, map[string]string{
		"SKILL.md":        "---\nname: pdf\ndescription: PDF tools\n---\n# pdf\n\nBody text\n",
		"scripts/run.sh*": "#!/bin/sh\necho ok\n",
		"_meta.json":      `{"slug":"pdf","version":"0.1.0"}`,
		"skill-card.md":   "## Description\nPDF tools\n",
	})
	var gotSlug, gotOwner string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/download" {
			http.NotFound(w, r)
			return
		}
		gotSlug = r.URL.Query().Get("slug")
		gotOwner = r.URL.Query().Get("ownerHandle")
		w.Header().Set("Content-Type", "application/zip")
		w.Write(payload)
	}))
	defer srv.Close()

	src, err := ParseImportInput("https://clawhub.ai/awspace/skills/pdf")
	must(t, err)
	if src.Label() != "awspace/pdf" {
		t.Fatalf("Label = %q, want awspace/pdf", src.Label())
	}

	var prog []ImportProgress
	ch := &ClawHub{Base: srv.URL, Client: srv.Client()}
	b, err := ch.ReadImport(context.Background(), home, src, func(p ImportProgress) {
		prog = append(prog, p)
	})
	must(t, err)
	defer b.Discard()

	if gotSlug != "pdf" || gotOwner != "awspace" {
		t.Fatalf("download query = slug:%q ownerHandle:%q", gotSlug, gotOwner)
	}
	if b.Site != "clawhub.ai" || b.BaseURL != "https://clawhub.ai/awspace/skills/pdf" {
		t.Errorf("batch metadata = %q %q", b.Site, b.BaseURL)
	}
	if len(b.Skills) != 1 {
		t.Fatalf("skills = %d", len(b.Skills))
	}
	sk := b.Skill("pdf")
	if sk == nil || sk.Problem != "" || sk.Description != "PDF tools" || sk.Count != 4 || !slices.Equal(sk.Exec, []string{"scripts/run.sh"}) {
		t.Fatalf("skill = %+v", sk)
	}
	if sk.SourceURL != "https://clawhub.ai/awspace/skills/pdf" || sk.Digest != digestOf(string(payload)) {
		t.Errorf("sourceURL/digest = %q %q", sk.SourceURL, sk.Digest)
	}
	if len(prog) == 0 || prog[len(prog)-1].Done != 1 || prog[len(prog)-1].Total != 1 {
		t.Errorf("progress = %+v", prog)
	}

	l := Scan(home)
	out := l.AddImport(b, []string{"pdf"}, ImportPlacement{Library: true}, nil)
	if !out[0].Added || out[0].Place != h(".agents/skills/pdf") {
		t.Fatalf("AddImport = %+v", out)
	}
	var rec map[string]string
	must(t, json.Unmarshal(lockSkills(t, h)["pdf"], &rec))
	if rec["sourceType"] != "clawhub" || rec["source"] != "awspace/pdf" || rec["sourceUrl"] != "https://clawhub.ai/awspace/skills/pdf" {
		t.Errorf("lock record = %+v", rec)
	}

	l2 := Scan(home)
	recent := l2.RecentImports(5)
	if len(recent) != 1 || recent[0].Source.Type != "clawhub" || recent[0].Source.Repo != "awspace/pdf" {
		t.Errorf("RecentImports = %+v", recent)
	}
	if it := itemNamed(t, l2.Updates(), "pdf"); it.State != UpdUnsupported || it.WebURL() != "https://clawhub.ai/awspace/skills/pdf" {
		t.Errorf("Updates item = %+v WebURL=%q", it, it.WebURL())
	}
}

func TestClawHubSkillWithUppercaseFrontmatterNameInstallsBySlug(t *testing.T) {
	home, h := importHome(t)
	payload := clawHubZip(t, map[string]string{
		"SKILL.md":           "---\nname: SkillScan\ndescription: Security gate\n---\n# SkillScan\n",
		"scripts/scanner.py": "print('ok')\n",
	})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/zip")
		w.Write(payload)
	}))
	defer srv.Close()

	src, err := ParseImportInput("clawhub.ai/tokauthai/skillscan")
	must(t, err)
	ch := &ClawHub{Base: srv.URL, Client: srv.Client()}
	b, err := ch.ReadImport(context.Background(), home, src, nil)
	must(t, err)
	defer b.Discard()

	sk := b.Skill("skillscan")
	if sk == nil || sk.Problem != "" || sk.Tree == "" {
		t.Fatalf("expected valid skill keyed by slug, got %+v", sk)
	}
	out := Scan(home).AddImport(b, []string{"skillscan"}, ImportPlacement{Library: true}, nil)
	if !out[0].Added || out[0].Place != h(".agents/skills/skillscan") {
		t.Fatalf("AddImport = %+v", out)
	}
}

func TestClawHubRejectsUnsafeOrMissingSkillArchives(t *testing.T) {
	home, _ := importHome(t)
	cases := []struct {
		name  string
		build func(t *testing.T) []byte
	}{
		{
			name: "missing SKILL.md",
			build: func(t *testing.T) []byte {
				return clawHubZip(t, map[string]string{"README.md": "hello"})
			},
		},
		{
			name: "path traversal",
			build: func(t *testing.T) []byte {
				return clawHubZip(t, map[string]string{"SKILL.md": "---\nname: bad\n---\n", "../evil": "x"})
			},
		},
		{
			name: "symlink escaping skill dir",
			build: func(t *testing.T) []byte {
				var buf bytes.Buffer
				zw := zip.NewWriter(&buf)
				w, _ := zw.Create("SKILL.md")
				w.Write([]byte("---\nname: evil\n---\n"))
				hd := &zip.FileHeader{Name: "leak"}
				hd.SetMode(os.ModeSymlink | 0o777)
				lw, _ := zw.CreateHeader(hd)
				lw.Write([]byte("/etc/passwd"))
				zw.Close()
				return buf.Bytes()
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			payload := tc.build(t)
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/zip")
				w.Write(payload)
			}))
			defer srv.Close()

			src, _ := ParseImportInput("https://clawhub.ai/awspace/skills/pdf")
			ch := &ClawHub{Base: srv.URL, Client: srv.Client()}
			b, err := ch.ReadImport(context.Background(), home, src, nil)
			if err == nil && b != nil && b.Skill("pdf") != nil && b.Skill("pdf").Problem == "" {
				b.Discard()
				t.Fatalf("%s: expected rejection", tc.name)
			}
			if b != nil {
				if out := Scan(home).AddImport(b, []string{"pdf"}, ImportPlacement{Library: true}, nil); out[0].Added {
					t.Fatalf("%s: unsafe skill should not be addable", tc.name)
				}
				b.Discard()
			}
		})
	}
}

func TestClawHubHandlesRateLimitsErrorsAndNonZipHandoff(t *testing.T) {
	home, _ := importHome(t)
	payload := clawHubZip(t, map[string]string{"SKILL.md": "---\nname: pdf\ndescription: ok\n---\n"})

	// 429 Retry-After then 200
	var attempts atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if attempts.Add(1) == 1 {
			w.Header().Set("Retry-After", "1")
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		w.Header().Set("Content-Type", "application/zip")
		w.Write(payload)
	}))
	defer srv.Close()

	var slept time.Duration
	ch := &ClawHub{
		Base:    srv.URL,
		Client:  srv.Client(),
		MaxWait: 5 * time.Second,
		sleep: func(_ context.Context, d time.Duration) error {
			slept = d
			return nil
		},
	}
	src, _ := ParseImportInput("https://clawhub.ai/awspace/skills/pdf")
	b, err := ch.ReadImport(context.Background(), home, src, nil)
	must(t, err)
	b.Discard()
	if attempts.Load() != 2 || slept != time.Second {
		t.Fatalf("attempts=%d slept=%v", attempts.Load(), slept)
	}

	// JSON handoff instead of ZIP returns clean ImportError
	handoffSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"sourceRef":"public-github","repo":"o/r","commit":"abc","path":"skills/x","contentHash":"h","archiveUrl":"https://example.com/x.tgz"}`))
	}))
	defer handoffSrv.Close()
	chHandoff := &ClawHub{Base: handoffSrv.URL, Client: handoffSrv.Client()}
	if _, err := chHandoff.ReadImport(context.Background(), home, src, nil); err == nil {
		t.Fatal("expected error on non-zip JSON handoff")
	}

	// 404 returns ImportError
	notFoundSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "Skill not found", http.StatusNotFound)
	}))
	defer notFoundSrv.Close()
	ch404 := &ClawHub{Base: notFoundSrv.URL, Client: notFoundSrv.Client()}
	if _, err := ch404.ReadImport(context.Background(), home, src, nil); err == nil {
		t.Fatal("expected error on 404")
	}

	// Network failure sets ImportError.Network
	deadURL := notFoundSrv.URL
	notFoundSrv.Close()
	chDead := &ClawHub{Base: deadURL, Client: notFoundSrv.Client()}
	_, err = chDead.ReadImport(context.Background(), home, src, nil)
	var ie *ImportError
	if !errors.As(err, &ie) || !ie.Network {
		t.Fatalf("expected Network ImportError, got %+v", err)
	}
	if entries, _ := os.ReadDir(filepath.Join(appDir(home), "staging")); len(entries) != 0 {
		t.Errorf("failed read should leave no staging entries: %v", entries)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestReadClawHubImportUsesDefaultClawHubEndpoint(t *testing.T) {
	home, _ := importHome(t)
	payload := clawHubZip(t, map[string]string{"SKILL.md": "---\nname: pdf\ndescription: PDF tools\n---\n"})
	var gotURL string
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		gotURL = r.URL.String()
		rec := httptest.NewRecorder()
		rec.Header().Set("Content-Type", "application/zip")
		rec.Write(payload)
		return rec.Result(), nil
	})}
	src, err := ParseImportInput("https://clawhub.ai/awspace/skills/pdf")
	must(t, err)
	b, err := ReadClawHubImport(context.Background(), home, client, nil, src, nil)
	must(t, err)
	defer b.Discard()
	if gotURL != "https://clawhub.ai/api/v1/download?ownerHandle=awspace&slug=pdf" {
		t.Errorf("request URL = %q", gotURL)
	}
	if b.Skill("pdf") == nil || b.Skill("pdf").Description != "PDF tools" {
		t.Errorf("unexpected batch = %+v", b.Skills)
	}
}

// clawHubHandoff 是 ClawHub 下载接口对「由公开 GitHub 仓库托管」的技能的回应：不给 zip，只给源仓库。
func clawHubHandoff(repo, dir string) *http.Client {
	body, _ := json.Marshal(map[string]string{
		"sourceRef": "public-github", "repo": repo, "commit": "0123abc", "path": dir,
		"contentHash": "h", "archiveUrl": "https://codeload.github.com/" + repo + "/tar.gz/0123abc",
	})
	return &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		rec := httptest.NewRecorder()
		rec.Header().Set("Content-Type", "application/json")
		rec.Write(body)
		return rec.Result(), nil
	})}
}

func TestAddingAGitHubBackedClawHubSkillReadsItFromGitHubSoUpdatesFollowIt(t *testing.T) {
	home, _ := importHome(t)
	f := newFakeImport()
	f.publish(t, "acme/tools", map[string]string{
		"skills/pdf/SKILL.md":    "---\nname: pdf\ndescription: PDF tools\n---\n",
		"skills/pdf/fill.py":     "print(1)\n",
		"skills/sheets/SKILL.md": "---\nname: sheets\ndescription: Sheets\n---\n",
	})
	src, err := ParseImportInput("https://clawhub.ai/acme/skills/pdf")
	must(t, err)

	for _, dir := range []string{"skills/pdf", "/skills/pdf/", "skills/pdf/SKILL.md"} {
		t.Run(dir, func(t *testing.T) {
			// When ClawHub 回应这个技能在 GitHub 仓库 acme/tools 的 skills/pdf
			b, err := ReadClawHubImport(context.Background(), home, clawHubHandoff("acme/tools", dir), f, src, nil)
			must(t, err)
			defer b.Discard()

			// Then 改从 GitHub 读这一个技能，记成 GitHub 来源，「更新 Skill」才查得到它
			if b.Source.Kind != ImportGitHub || b.Source.Repo != "acme/tools" || b.Source.Ref != "" {
				t.Fatalf("来源 = %+v，应是 GitHub acme/tools 的默认分支", b.Source)
			}
			if got := batchNames(b); !slices.Equal(got, []string{"pdf"}) {
				t.Fatalf("只应读 skills/pdf：%v", got)
			}
			rec := lockRecordFor(b, b.Skill("pdf"))
			if rec.SourceType != "github" || rec.SkillPath != "skills/pdf/SKILL.md" || rec.Ref != "" {
				t.Errorf("安装记录 = %+v，应是跟默认分支的 GitHub 记录", rec)
			}
		})
	}
}

func TestAGitHubBackedClawHubSkillWithAnUnusableSourceIsRefused(t *testing.T) {
	home, _ := importHome(t)
	f := newFakeImport()
	src, err := ParseImportInput("https://clawhub.ai/acme/skills/pdf")
	must(t, err)
	for _, c := range []struct{ repo, dir string }{
		{"acme", "skills/pdf"},
		{"../acme/tools", "skills/pdf"},
		{"acme/tools", "../outside"},
		{"acme/tools", "skills/../../outside"},
	} {
		_, err := ReadClawHubImport(context.Background(), home, clawHubHandoff(c.repo, c.dir), f, src, nil)
		var ie *ImportError
		if !errors.As(err, &ie) {
			t.Errorf("repo=%q path=%q 应拒绝并给出说明，得到 %v", c.repo, c.dir, err)
		}
	}
	if len(f.calls) != 0 {
		t.Errorf("来源不可用时不应请求 GitHub：%v", f.calls)
	}
}
