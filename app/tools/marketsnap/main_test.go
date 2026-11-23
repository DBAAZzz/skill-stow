package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"skillmanager/internal/library"
)

func mustReadTestdata(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("read testdata/%s: %v", name, err)
	}
	return b
}

func TestHTMLParsingFixtures(t *testing.T) {
	home := mustReadTestdata(t, "home.html")
	gotSkills := extractSkillsFromListHTML(home)
	wantSkills := []string{
		"vercel-labs/skills/find-skills",
		"vercel-labs/agent-browser/agent-browser",
		"heygen-com/hyperframes/hyperframes-cli",
	}
	if !reflect.DeepEqual(gotSkills, wantSkills) {
		t.Fatalf("extractSkillsFromListHTML = %v, want %v", gotSkills, wantSkills)
	}

	topics, err := extractTopicsIndexHTML(mustReadTestdata(t, "topic.html"))
	if err != nil {
		t.Fatalf("extractTopicsIndexHTML: %v", err)
	}
	if len(topics) != 2 {
		t.Fatalf("topics len = %d, want 2: %+v", len(topics), topics)
	}
	if topics[0].ID != "react" || topics[0].Title != "Frontend & React skills" ||
		topics[0].Summary != "Performance rules, component patterns, and ecosystem knowledge for React." {
		t.Fatalf("topic[0] = %+v", topics[0])
	}

	testingTopic, err := extractTopicDetailHTML("testing", mustReadTestdata(t, "topic_testing.html"))
	if err != nil {
		t.Fatalf("extractTopicDetailHTML: %v", err)
	}
	if len(testingTopic.Skills) != 2 {
		t.Fatalf("testing skills len = %d: %+v", len(testingTopic.Skills), testingTopic.Skills)
	}
	if testingTopic.Skills[0].ID != "obra/superpowers/test-driven-development" ||
		testingTopic.Skills[0].What != "TDD loop: write the failing test first, implement the minimal change, verify, then refactor" {
		t.Fatalf("skill[0] = %+v", testingTopic.Skills[0])
	}
	if testingTopic.Skills[1].What != "Web app & E2E testing patterns" {
		t.Fatalf("skill[1].What = %q", testingTopic.Skills[1].What)
	}

	desc := extractMetaDescription(mustReadTestdata(t, "skill_find_skills.html"))
	if desc != `Helps users discover and install "agent skills" when asked.` {
		t.Fatalf("extractMetaDescription = %q", desc)
	}

	if got := extractMetaDescription(home); got != "" {
		t.Fatalf("generic site description should be skipped, got %q", got)
	}
}

func TestRobotsForbiddenAndUserAgentGuard(t *testing.T) {
	for _, p := range []string{"/api/skills/all-time/0", "/api", "/search", "/search/all", "/internal/x", "/debug-security/1"} {
		if !robotsForbiddenPath(p) {
			t.Errorf("robotsForbiddenPath(%q) = false, want true", p)
		}
	}
	for _, p := range []string{"/", "/trending", "/hot", "/topic", "/topic/testing", "/vercel-labs/skills/find-skills"} {
		if robotsForbiddenPath(p) {
			t.Errorf("robotsForbiddenPath(%q) = true, want false", p)
		}
	}

	out := filepath.Join(t.TempDir(), "extras.json")
	_, err := runSnapshot(context.Background(), runOptions{
		outPath:   out,
		userAgent: "bot (user@example.com)",
	})
	if err == nil || !strings.Contains(err.Error(), "email") {
		t.Fatalf("expected user-agent email rejection, got %v", err)
	}
}

func TestRunSnapshotConcurrencyRateRetryAndRoundTrip(t *testing.T) {
	home := mustReadTestdata(t, "home.html")
	topicIdx := mustReadTestdata(t, "topic.html")
	topicTesting := mustReadTestdata(t, "topic_testing.html")
	skillFind := mustReadTestdata(t, "skill_find_skills.html")

	const topicReact = `<!DOCTYPE html><html><body><a href="/vercel-labs/agent-skills/vercel-react-best-practices"><div class="lg:col-span-3"><p>vercel-labs/agent-skills</p></div><p class="lg:col-span-9">React performance rules</p></a></body></html>`
	const trendingHTML = `<html><body><a href="/vercel-labs/skills/find-skills">x</a></body></html>`
	const hotHTML = `<html><body><a href="/vercel-labs/agent-browser/agent-browser">x</a></body></html>`

	var mu sync.Mutex
	var reqPaths []string
	var maxInFlight, curInFlight atomic.Int32
	var findAttempts atomic.Int32
	var reqTimes []time.Time

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.Header.Get("User-Agent"), "skill-stow-marketsnap/") || strings.Contains(r.Header.Get("User-Agent"), "@") {
			t.Errorf("unexpected User-Agent: %q", r.Header.Get("User-Agent"))
		}
		if robotsForbiddenPath(r.URL.Path) {
			t.Errorf("requested robots-disallowed path %q", r.URL.Path)
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}

		cur := curInFlight.Add(1)
		for {
			prev := maxInFlight.Load()
			if cur <= prev || maxInFlight.CompareAndSwap(prev, cur) {
				break
			}
		}
		defer curInFlight.Add(-1)

		mu.Lock()
		reqPaths = append(reqPaths, r.URL.Path)
		reqTimes = append(reqTimes, time.Now())
		mu.Unlock()

		switch r.URL.Path {
		case "/":
			w.Write(home)
		case "/trending":
			w.Write([]byte(trendingHTML))
		case "/hot":
			w.Write([]byte(hotHTML))
		case "/topic":
			w.Write(topicIdx)
		case "/topic/react":
			w.Write([]byte(topicReact))
		case "/topic/testing":
			w.Write(topicTesting)
		case "/vercel-labs/skills/find-skills":
			if findAttempts.Add(1) == 1 {
				w.Header().Set("Retry-After", "0")
				http.Error(w, "rate limited", http.StatusTooManyRequests)
				return
			}
			w.Write(skillFind)
		case "/vercel-labs/agent-browser/agent-browser":
			w.Write([]byte(`<head><meta name="description" content="Browser automation skill for agents"/></head>`))
		case "/heygen-com/hyperframes/hyperframes-cli":
			w.Write([]byte(`<head><meta property="og:description" content="Render videos from CLI"/></head>`))
		case "/vercel-labs/agent-skills/vercel-react-best-practices":
			w.Write([]byte(`<head><meta name="description" content="React performance &amp; patterns"/></head>`))
		case "/obra/superpowers/test-driven-development":
			http.Error(w, "server error", http.StatusServiceUnavailable)
		case "/currents-dev/playwright-best-practices-skill/playwright-best-practices":
			http.NotFound(w, r)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	var fakeClockMu sync.Mutex
	fakeNow := time.Date(2026, 11, 23, 3, 17, 0, 0, time.UTC)
	nowFn := func() time.Time {
		fakeClockMu.Lock()
		defer fakeClockMu.Unlock()
		return fakeNow
	}
	sleepFn := func(ctx context.Context, d time.Duration) error {
		fakeClockMu.Lock()
		fakeNow = fakeNow.Add(d)
		fakeClockMu.Unlock()
		return nil
	}

	outPath := filepath.Join(t.TempDir(), "market-extras.json")
	stats, err := runSnapshot(context.Background(), runOptions{
		baseURL:     srv.URL,
		outPath:     outPath,
		concurrency: 2,
		interval:    25 * time.Millisecond,
		maxRetries:  2,
		minSkills:   3,
		now:         nowFn,
		sleep:       sleepFn,
	})
	if err != nil {
		t.Fatalf("runSnapshot: %v", err)
	}
	if maxInFlight.Load() > 2 {
		t.Fatalf("max concurrency = %d, want <= 2", maxInFlight.Load())
	}
	if stats.Topics != 2 || stats.Descriptions != 4 || stats.Fetched != 4 || stats.FetchFailed != 2 {
		t.Fatalf("unexpected stats: %+v", stats)
	}
	if findAttempts.Load() != 2 {
		t.Fatalf("find-skills retry count = %d, want 2", findAttempts.Load())
	}
	for _, p := range reqPaths {
		if strings.HasPrefix(p, "/api") || strings.HasPrefix(p, "/search") {
			t.Fatalf("forbid path %q was requested", p)
		}
	}

	// 验证产出的 JSON 与 internal/library.MarketClient 解析链路 100% 互通。
	rawOut, err := os.ReadFile(outPath)
	if err != nil {
		t.Fatal(err)
	}
	libSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/market-extras.json":
			w.Write(rawOut)
		case "/api/skills/all-time/0":
			w.Write([]byte(`{"skills":[{"id":"vercel-labs/skills/find-skills","skillId":"find-skills","name":"find-skills","source":"vercel-labs/skills","installs":4493759}],"total":1,"hasMore":false,"page":0}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer libSrv.Close()

	mc := library.NewMarketClient(library.Settings{}, t.TempDir())
	mc.Client = libSrv.Client()
	mc.SkillsShBase = libSrv.URL
	mc.ExtrasURL = libSrv.URL + "/market-extras.json"
	list, err := mc.FetchMarketList(context.Background(), library.MarketSkillsSh, library.MarketViewTopic, "testing", 0)
	if err != nil {
		t.Fatalf("FetchMarketList(topic): %v", err)
	}
	if len(list.Items) != 2 || list.Items[0].What != "TDD loop: write the failing test first, implement the minimal change, verify, then refactor" {
		t.Fatalf("topic items from library = %+v", list.Items)
	}
	allTime, err := mc.FetchMarketList(context.Background(), library.MarketSkillsSh, library.MarketViewAllTime, "", 0)
	if err != nil {
		t.Fatalf("FetchMarketList(all-time): %v", err)
	}
	if len(allTime.Items) != 1 || allTime.Items[0].Description != `Helps users discover and install "agent skills" when asked.` {
		t.Fatalf("merged item = %+v", allTime.Items)
	}
}

func TestPrevIncrementalRotationAndValidationFailures(t *testing.T) {
	home := mustReadTestdata(t, "home.html")
	topicIdx := mustReadTestdata(t, "topic.html")
	topicTesting := mustReadTestdata(t, "topic_testing.html")
	const topicReact = `<html><body><a href="/vercel-labs/agent-skills/vercel-react-best-practices"><p class="lg:col-span-9">React perf</p></a></body></html>`

	var mu sync.Mutex
	var fetched []string
	var failRefresh atomic.Bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/", "/trending", "/hot":
			w.Write(home)
		case "/topic":
			w.Write(topicIdx)
		case "/topic/react":
			w.Write([]byte(topicReact))
		case "/topic/testing":
			w.Write(topicTesting)
		default:
			mu.Lock()
			fetched = append(fetched, r.URL.Path)
			mu.Unlock()
			if failRefresh.Load() {
				http.Error(w, "boom", http.StatusInternalServerError)
				return
			}
			w.Write([]byte(`<head><meta name="description" content="Updated: ` + r.URL.Path + `"/></head>`))
		}
	}))
	defer srv.Close()

	prevPath := filepath.Join(t.TempDir(), "prev.json")
	prevData := library.MarketExtras{
		Version:     1,
		GeneratedAt: "2026-11-22T00:00:00Z",
		Descriptions: map[string]string{
			"heygen-com/hyperframes/hyperframes-cli":                                 "old hyperframes",
			"obra/superpowers/test-driven-development":                               "old tdd",
			"currents-dev/playwright-best-practices-skill/playwright-best-practices": "old pw",
			"vercel-labs/agent-browser/agent-browser":                                "old browser",
			"vercel-labs/agent-skills/vercel-react-best-practices":                   "old react",
			"vercel-labs/skills/find-skills":                                         "old find",
		},
	}
	rawPrev, _ := json.Marshal(prevData)
	if err := os.WriteFile(prevPath, rawPrev, 0o644); err != nil {
		t.Fatal(err)
	}

	// 验证 -limit 限制只抓取前 1 个，被截断的轮换项仍保留 prev 中的旧简介。
	day0 := time.Unix(900*86400, 0).UTC()
	limitOut := filepath.Join(t.TempDir(), "limit.json")
	limStats, err := runSnapshot(context.Background(), runOptions{
		baseURL:    srv.URL,
		prevPath:   prevPath,
		outPath:    limitOut,
		limit:      1,
		interval:   0,
		maxRetries: 0,
		rotateDays: 3,
		now:        func() time.Time { return day0 },
		sleep:      func(context.Context, time.Duration) error { return nil },
	})
	if err != nil {
		t.Fatalf("limit run: %v", err)
	}
	if limStats.Fetched != 1 || limStats.Reused != 5 || limStats.Descriptions != 6 {
		t.Fatalf("unexpected limit stats: %+v", limStats)
	}

	mu.Lock()
	fetched = nil
	mu.Unlock()

	// Unix days % 3 == 0 时只轮换 sorted cached keys 里 idx % 3 == 0 的两项（idx 0、3）。
	outPath := filepath.Join(t.TempDir(), "out.json")
	stats, err := runSnapshot(context.Background(), runOptions{
		baseURL:    srv.URL,
		prevPath:   prevPath,
		outPath:    outPath,
		interval:   0,
		maxRetries: 0,
		rotateDays: 3,
		now:        func() time.Time { return day0 },
		sleep:      func(context.Context, time.Duration) error { return nil },
	})
	if err != nil {
		t.Fatalf("incremental run: %v", err)
	}
	mu.Lock()
	fetchedCount := len(fetched)
	mu.Unlock()
	if fetchedCount != 2 || stats.Reused != 4 || stats.Fetched != 2 {
		t.Fatalf("expected 2 refreshed & 4 reused, got fetchedCount=%d stats=%+v", fetchedCount, stats)
	}

	// 验证校验失败时不覆盖已有 out 文件：
	// 1) 上一次 6 条，本次只有 2 条有效（少于一半）应报错且保留原 out。
	sentinel := `{"sentinel":"keep-me"}`
	if err := os.WriteFile(outPath, []byte(sentinel), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err = encodeAndValidateExtras(library.MarketExtras{
		Version: 1,
		Topics:  []library.MarketTopic{{ID: "t", Title: "T", Skills: []library.MarketTopicSkill{{ID: "a/b/c", What: "w"}}}},
		Descriptions: map[string]string{
			"a/b/c": "1",
			"d/e/f": "2",
		},
	}, &prevData, runOptions{minSkills: 1, maxBytes: defaultMaxBytes})
	if err == nil || !strings.Contains(err.Error(), "50%") {
		t.Fatalf("expected >50%% drop error, got %v", err)
	}

	// 2) 空专题失败不写入。
	_, err = encodeAndValidateExtras(library.MarketExtras{
		Version:      1,
		Descriptions: map[string]string{"a/b/c": "1"},
	}, nil, runOptions{minSkills: 1, maxBytes: defaultMaxBytes})
	if err == nil || !strings.Contains(err.Error(), "topics list is empty") {
		t.Fatalf("expected empty topics error, got %v", err)
	}

	// 3) 刷新全部报 500 时保留 prev 的旧描述，且整批 -min-skills 不通过也不覆盖 outPath。
	failRefresh.Store(true)
	_, err = runSnapshot(context.Background(), runOptions{
		baseURL:    srv.URL,
		prevPath:   prevPath,
		outPath:    outPath,
		interval:   0,
		maxRetries: 0,
		rotateDays: 3,
		minSkills:  100, // 故意高于实际条目数以触发写前校验失败
		now:        func() time.Time { return day0 },
		sleep:      func(context.Context, time.Duration) error { return nil },
	})
	if err == nil || !strings.Contains(err.Error(), "below minimum") {
		t.Fatalf("expected minSkills error, got %v", err)
	}
	after, _ := os.ReadFile(outPath)
	if string(after) != sentinel {
		t.Fatalf("failed validation overwrote existing out file: got %q", string(after))
	}
}
