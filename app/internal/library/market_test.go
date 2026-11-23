package library

import (
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

func loadMarketFixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", "market", name))
	if err != nil {
		t.Fatalf("read market fixture %s: %v", name, err)
	}
	return b
}

func newTestMarketClient(t *testing.T, ts *httptest.Server, cacheDir string, now *time.Time) *MarketClient {
	t.Helper()
	mc := NewMarketClient(Settings{}, cacheDir)
	mc.SkillsShBase = ts.URL
	mc.ClawHubBase = ts.URL
	mc.ExtrasURL = ts.URL + "/market-extras.json"
	mc.Client = ts.Client()
	if now != nil {
		mc.Now = func() time.Time { return *now }
	}
	mc.Sleep = func(ctx context.Context, d time.Duration) error { return nil }
	return mc
}

func TestFetchMarketList_SkillsShLeaderboards(t *testing.T) {
	allTimeBody := loadMarketFixture(t, "skills_sh_alltime.json")
	trendingBody := loadMarketFixture(t, "skills_sh_trending.json")
	hotBody := loadMarketFixture(t, "skills_sh_hot.json")

	var allTimeCalls, trendingCalls, hotCalls atomic.Int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/skills/all-time/0":
			allTimeCalls.Add(1)
			_, _ = w.Write(allTimeBody)
		case "/api/skills/trending/0":
			trendingCalls.Add(1)
			_, _ = w.Write(trendingBody)
		case "/api/skills/hot/0":
			hotCalls.Add(1)
			_, _ = w.Write(hotBody)
		default:
			http.NotFound(w, r)
		}
	}))
	defer ts.Close()

	now := time.Date(2026, 11, 23, 12, 0, 0, 0, time.UTC)
	mc := newTestMarketClient(t, ts, filepath.Join(t.TempDir(), "market"), &now)
	ctx := context.Background()

	// All-time leaderboard parses full fields, ranks, weekly sparkline, and valid ImportRef.
	allTime, err := mc.FetchMarketList(ctx, MarketSkillsSh, MarketViewAllTime, "", 0)
	if err != nil {
		t.Fatalf("FetchMarketList all-time: %v", err)
	}
	if len(allTime.Items) != 6 || !allTime.HasMore {
		t.Fatalf("all-time got %d items hasMore=%v, want 6 true", len(allTime.Items), allTime.HasMore)
	}
	first := allTime.Items[0]
	if first.Source != MarketSkillsSh || first.ID != "vercel-labs/skills/find-skills" ||
		first.Owner != "vercel-labs" || first.Repo != "vercel-labs/skills" || first.SkillID != "find-skills" ||
		first.Name != "find-skills" || first.Count != 4493759 || first.Metric != MarketMetricInstalls ||
		!first.Official || first.Rank != 1 || len(first.Weekly) != 8 ||
		first.WebURL != "https://skills.sh/vercel-labs/skills/find-skills" ||
		first.ImportRef != "https://skills.sh/vercel-labs/skills/find-skills" {
		t.Fatalf("unexpected first all-time item: %+v", first)
	}
	if got, err := ParseImportInput(first.ImportRef); err != nil || got.Kind != ImportGitHub || got.Repo != "vercel-labs/skills" || !slices.Equal(got.Skills, []string{"find-skills"}) {
		t.Fatalf("ParseImportInput(%q) = (%+v, %v), want GitHub vercel-labs/skills [find-skills]", first.ImportRef, got, err)
	}
	if allTime.Items[1].Official || allTime.Items[1].Rank != 2 {
		t.Fatalf("second item official=%v rank=%d, want false 2", allTime.Items[1].Official, allTime.Items[1].Rank)
	}

	// Trending omits weeklyInstalls while preserving isOfficial.
	trending, err := mc.FetchMarketList(ctx, MarketSkillsSh, MarketViewTrending, "", 0)
	if err != nil {
		t.Fatalf("FetchMarketList trending: %v", err)
	}
	if len(trending.Items) != 2 || trending.Items[0].Weekly != nil || !trending.Items[1].Official {
		t.Fatalf("unexpected trending items: %+v", trending.Items)
	}

	// Hot includes single-segment official source ("skills.sh").
	hot, err := mc.FetchMarketList(ctx, MarketSkillsSh, MarketViewHot, "", 0)
	if err != nil {
		t.Fatalf("FetchMarketList hot: %v", err)
	}
	if len(hot.Items) != 2 || hot.HasMore {
		t.Fatalf("unexpected hot result: len=%d hasMore=%v", len(hot.Items), hot.HasMore)
	}
	if hot.Items[1].ID != "skills.sh/simplify" || hot.Items[1].Repo != "skills.sh" || hot.Items[1].Count != 2 {
		t.Fatalf("unexpected single-segment hot item: %+v", hot.Items[1])
	}

	// Official view derives from all-time without an extra HTTP call when cached.
	official, err := mc.FetchMarketList(ctx, MarketSkillsSh, MarketViewOfficial, "", 0)
	if err != nil {
		t.Fatalf("FetchMarketList official: %v", err)
	}
	if allTimeCalls.Load() != 1 {
		t.Fatalf("expected official view to reuse cached all-time, got %d requests", allTimeCalls.Load())
	}
	if len(official.Items) != 3 {
		t.Fatalf("official view got %d items, want 3", len(official.Items))
	}
	for _, it := range official.Items {
		if !it.Official {
			t.Fatalf("non-official item in official view: %+v", it)
		}
	}
	if trendingCalls.Load() != 1 || hotCalls.Load() != 1 {
		t.Fatalf("unexpected request counts: trending=%d hot=%d", trendingCalls.Load(), hotCalls.Load())
	}
}

func TestFetchMarketList_SkillsShTopic(t *testing.T) {
	allTimeBody := loadMarketFixture(t, "skills_sh_alltime.json")
	extrasBody := loadMarketFixture(t, "market_extras.json")

	var leaderboardCalls atomic.Int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/market-extras.json":
			w.Header().Set("ETag", `"extras-v1"`)
			_, _ = w.Write(extrasBody)
		case "/api/skills/all-time/0":
			leaderboardCalls.Add(1)
			_, _ = w.Write(allTimeBody)
		default:
			http.NotFound(w, r)
		}
	}))
	defer ts.Close()

	now := time.Date(2026, 11, 23, 12, 0, 0, 0, time.UTC)
	mc := newTestMarketClient(t, ts, filepath.Join(t.TempDir(), "market"), &now)
	ctx := context.Background()

	// Cold cache: topic view must NOT fetch missing leaderboards over network.
	designCold, err := mc.FetchMarketList(ctx, MarketSkillsSh, MarketViewTopic, "design", 0)
	if err != nil {
		t.Fatalf("cold topic fetch: %v", err)
	}
	if leaderboardCalls.Load() != 0 {
		t.Fatalf("topic view must not fetch uncached leaderboards, got %d calls", leaderboardCalls.Load())
	}
	if len(designCold.Items) != 2 {
		t.Fatalf("cold topic got %d items, want 2", len(designCold.Items))
	}
	if designCold.Items[0].Count != 0 || designCold.Items[0].What == "" || designCold.Items[0].Description == "" {
		t.Fatalf("cold topic first item mismatch: %+v", designCold.Items[0])
	}

	// Warm cache: once all-time is cached, topic view enriches matching skills locally.
	if _, err := mc.FetchMarketList(ctx, MarketSkillsSh, MarketViewAllTime, "", 0); err != nil {
		t.Fatalf("fetch all-time: %v", err)
	}
	designWarm, err := mc.FetchMarketList(ctx, MarketSkillsSh, MarketViewTopic, "design", 0)
	if err != nil {
		t.Fatalf("warm topic fetch: %v", err)
	}
	if leaderboardCalls.Load() != 1 {
		t.Fatalf("warm topic caused extra leaderboard fetch, calls=%d", leaderboardCalls.Load())
	}
	fe := designWarm.Items[0]
	if fe.ID != "anthropics/skills/frontend-design" || fe.Count != 1155771 || !fe.Official || len(fe.Weekly) != 8 {
		t.Fatalf("warm topic item should be enriched from cache: %+v", fe)
	}
	if designWarm.Items[1].Count != 0 {
		t.Fatalf("uncached skill in topic should stay Count=0, got %d", designWarm.Items[1].Count)
	}

	// Unknown topic & page > 0 return empty without error.
	unknown, err := mc.FetchMarketList(ctx, MarketSkillsSh, MarketViewTopic, "nonexistent", 0)
	if err != nil || len(unknown.Items) != 0 || unknown.HasMore {
		t.Fatalf("unknown topic got %+v, err=%v", unknown, err)
	}
	page1, err := mc.FetchMarketList(ctx, MarketSkillsSh, MarketViewTopic, "design", 1)
	if err != nil || len(page1.Items) != 0 || page1.HasMore {
		t.Fatalf("topic page 1 got %+v, err=%v", page1, err)
	}
}

func TestFetchMarketList_ClawHub(t *testing.T) {
	secBody := loadMarketFixture(t, "clawhub_security.json")

	// Extract cursor from fixture to assert cursor forwarding on page 1.
	var parsed struct {
		NextCursor string `json:"nextCursor"`
	}
	if err := json.Unmarshal(secBody, &parsed); err != nil || parsed.NextCursor == "" {
		t.Fatalf("fixture nextCursor missing: %v", err)
	}

	var rateLimitedOnce atomic.Bool
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/skills" {
			http.NotFound(w, r)
			return
		}
		q := r.URL.Query()
		if q.Get("sort") != "downloads" {
			http.Error(w, "bad sort", http.StatusBadRequest)
			return
		}
		// Empty category param must NOT be sent (real API rejects ?category= with 400).
		if q.Has("category") && q.Get("category") == "" {
			http.Error(w, "Invalid skill category", http.StatusBadRequest)
			return
		}
		if q.Get("category") == "security" && q.Get("cursor") == "" {
			if !rateLimitedOnce.Swap(true) {
				w.Header().Set("Retry-After", "1")
				http.Error(w, "rate limited", http.StatusTooManyRequests)
				return
			}
			_, _ = w.Write(secBody)
			return
		}
		if q.Get("category") == "security" && q.Get("cursor") == parsed.NextCursor {
			_, _ = w.Write([]byte(`{"items":[{"ownerHandle":"alice","slug":"audit-check","displayName":"Audit Check","summary":"Final page item.","categories":["security"],"stats":{"downloads":100,"installs":10,"stars":2,"versions":1}}],"nextCursor":null}`))
			return
		}
		if !q.Has("category") && q.Get("cursor") == "" {
			_, _ = w.Write([]byte(`{"items":[{"ownerHandle":"awspace","slug":"pdf","displayName":"Pdf","summary":"PDF tools.","categories":["knowledge"],"stats":{"downloads":52554,"installs":1502,"stars":67,"versions":1}}],"nextCursor":null}`))
			return
		}
		http.Error(w, "unexpected query: "+r.URL.RawQuery, http.StatusBadRequest)
	}))
	defer ts.Close()

	now := time.Date(2026, 11, 23, 12, 0, 0, 0, time.UTC)
	mc := newTestMarketClient(t, ts, filepath.Join(t.TempDir(), "market"), &now)
	ctx := context.Background()

	// "All" must omit empty category parameter and accept both MarketViewAllTime and MarketViewCategory("all").
	allRes, err := mc.FetchMarketList(ctx, MarketClawHub, MarketViewAllTime, "", 0)
	if err != nil {
		t.Fatalf("clawhub all: %v", err)
	}
	if len(allRes.Items) != 1 || allRes.Items[0].Metric != MarketMetricDownloads || allRes.Items[0].ID != "awspace/pdf" {
		t.Fatalf("unexpected clawhub all result: %+v", allRes.Items)
	}
	if allAlias, err := mc.FetchMarketList(ctx, MarketClawHub, MarketViewCategory, "all", 0); err != nil || len(allAlias.Items) != 1 {
		t.Fatalf("clawhub category=all alias failed: err=%v res=%+v", err, allAlias)
	}

	// Category="security" retries on 429 and parses stats & categories.
	sec0, err := mc.FetchMarketList(ctx, MarketClawHub, MarketViewCategory, "security", 0)
	if err != nil {
		t.Fatalf("clawhub security page 0: %v", err)
	}
	if len(sec0.Items) != 3 || !sec0.HasMore || sec0.NextCursor != parsed.NextCursor {
		t.Fatalf("unexpected security page 0: len=%d hasMore=%v cursor=%q", len(sec0.Items), sec0.HasMore, sec0.NextCursor)
	}
	sv := sec0.Items[0]
	if sv.Owner != "spclaudehome" || sv.SkillID != "skill-vetter" || sv.Name != "Skill Vetter" ||
		sv.Count != 284044 || sv.Installs != 12281 || sv.Stars != 1317 || sv.Versions != 1 ||
		sv.WebURL != "https://clawhub.ai/spclaudehome/skills/skill-vetter" ||
		sv.ImportRef != "https://clawhub.ai/spclaudehome/skills/skill-vetter" {
		t.Fatalf("unexpected clawhub item mapping: %+v", sv)
	}
	if parsedRef, err := ParseImportInput(sv.ImportRef); err != nil || parsedRef.Kind != ImportClawHub || parsedRef.Repo != "spclaudehome/skill-vetter" {
		t.Fatalf("ParseImportInput(%q) = (%+v, %v), want ImportClawHub", sv.ImportRef, parsedRef, err)
	}

	// Cursor pagination for page 1 and exhausted page 2.
	sec1, err := mc.FetchMarketList(ctx, MarketClawHub, MarketViewCategory, "security", 1)
	if err != nil {
		t.Fatalf("clawhub security page 1: %v", err)
	}
	if len(sec1.Items) != 1 || sec1.HasMore || sec1.Items[0].ID != "alice/audit-check" {
		t.Fatalf("unexpected security page 1: %+v", sec1)
	}
	sec2, err := mc.FetchMarketList(ctx, MarketClawHub, MarketViewCategory, "security", 2)
	if err != nil || len(sec2.Items) != 0 || sec2.HasMore {
		t.Fatalf("exhausted page 2 got %+v err=%v", sec2, err)
	}
}

func TestMarketExtras_ETagAndMerge(t *testing.T) {
	extrasBody := loadMarketFixture(t, "market_extras.json")
	var reqCount atomic.Int32
	var mode atomic.Int32 // 0=200 with ETag, 1=expect If-None-Match -> 304, 2=500 error

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/market-extras.json" {
			http.NotFound(w, r)
			return
		}
		reqCount.Add(1)
		switch mode.Load() {
		case 0:
			w.Header().Set("ETag", `"etag-1"`)
			_, _ = w.Write(extrasBody)
		case 1:
			if r.Header.Get("If-None-Match") != `"etag-1"` {
				http.Error(w, "missing If-None-Match", http.StatusBadRequest)
				return
			}
			w.WriteHeader(http.StatusNotModified)
		case 2:
			http.Error(w, "upstream down", http.StatusInternalServerError)
		}
	}))
	defer ts.Close()

	now := time.Date(2026, 11, 23, 12, 0, 0, 0, time.UTC)
	mc := newTestMarketClient(t, ts, filepath.Join(t.TempDir(), "market"), &now)
	ctx := context.Background()

	// Cold fetch receives 200 and stores ETag + payload.
	ex1, err := mc.FetchMarketExtras(ctx)
	if err != nil {
		t.Fatalf("initial FetchMarketExtras: %v", err)
	}
	if ex1.Descriptions["vercel-labs/skills/find-skills"] == "" || len(ex1.Topics) != 2 {
		t.Fatalf("unexpected extras payload: %+v", ex1)
	}

	// Second fetch sends If-None-Match and keeps cached data on 304.
	mode.Store(1)
	ex2, err := mc.FetchMarketExtras(ctx)
	if err != nil || len(ex2.Topics) != 2 {
		t.Fatalf("304 FetchMarketExtras failed: err=%v ex=%+v", err, ex2)
	}

	// Offline/server failure falls back to disk-cached extras.
	mode.Store(2)
	mcRestarted := newTestMarketClient(t, ts, mc.CacheDir, &now)
	ex3, err := mcRestarted.FetchMarketExtras(ctx)
	if err != nil || ex3.Descriptions["anthropics/skills/frontend-design"] == "" {
		t.Fatalf("offline fallback failed: err=%v ex=%+v", err, ex3)
	}

	// Merge populates skills.sh Description & Topics without touching ClawHub descriptions.
	items := []MarketItem{
		{Source: MarketSkillsSh, ID: "anthropics/skills/frontend-design", Repo: "anthropics/skills", SkillID: "frontend-design"},
		{Source: MarketClawHub, ID: "spclaudehome/skill-vetter", Description: "Original ClawHub summary"},
	}
	mcRestarted.MergeMarketExtras(items, ex3)
	if items[0].Description == "" || !slices.Equal(items[0].Topics, []string{"design"}) {
		t.Fatalf("MergeMarketExtras skills.sh failed: %+v", items[0])
	}
	if items[1].Description != "Original ClawHub summary" {
		t.Fatalf("MergeMarketExtras should preserve ClawHub description: %+v", items[1])
	}
	if reqCount.Load() != 3 {
		t.Fatalf("expected 3 HTTP calls across modes, got %d", reqCount.Load())
	}
}

func TestSearchMarket_ConcurrentPartialAndSorting(t *testing.T) {
	allTimeBody := loadMarketFixture(t, "skills_sh_alltime.json")
	shSearchBody := loadMarketFixture(t, "skills_sh_search.json")
	chSearchBody := loadMarketFixture(t, "clawhub_search.json")

	var shCalls, chCalls atomic.Int32
	var shFail atomic.Bool
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/skills/all-time/0":
			_, _ = w.Write(allTimeBody)
		case "/api/search":
			shCalls.Add(1)
			if shFail.Load() {
				http.Error(w, "skills.sh 500", http.StatusInternalServerError)
				return
			}
			q := r.URL.Query().Get("q")
			if len([]rune(q)) < 2 {
				http.Error(w, `{"error":"Query must be at least 2 characters"}`, http.StatusBadRequest)
				return
			}
			_, _ = w.Write(shSearchBody)
		case "/api/v1/search":
			chCalls.Add(1)
			_, _ = w.Write(chSearchBody)
		default:
			http.NotFound(w, r)
		}
	}))
	defer ts.Close()

	now := time.Date(2026, 11, 23, 12, 0, 0, 0, time.UTC)
	cacheDir := filepath.Join(t.TempDir(), "market")
	mc := newTestMarketClient(t, ts, cacheDir, &now)
	ctx := context.Background()

	// Warm all-time cache so search can enrich anthropics/skills/pdf sparkline.
	if _, err := mc.FetchMarketList(ctx, MarketSkillsSh, MarketViewAllTime, "", 0); err != nil {
		t.Fatalf("warm all-time cache: %v", err)
	}

	res := mc.SearchMarket(ctx, "pdf")
	if res.SkillsShErr != nil || res.ClawHubErr != nil {
		t.Fatalf("SearchMarket errors: sh=%v ch=%v", res.SkillsShErr, res.ClawHubErr)
	}

	// skills.sh search must be sorted descending by installs (upstream returned react-pdf=2429 before openai/pdf=13995).
	if len(res.SkillsSh) != 6 {
		t.Fatalf("skills.sh search got %d items, want 6", len(res.SkillsSh))
	}
	wantCounts := []int64{235455, 13995, 10000, 9739, 6483, 2429}
	for i, want := range wantCounts {
		if res.SkillsSh[i].Count != want {
			t.Fatalf("skills.sh search[%d].Count = %d, want %d (must sort descending)", i, res.SkillsSh[i].Count, want)
		}
		if res.SkillsSh[i].Official {
			t.Fatalf("search result[%d] must never set Official=true", i)
		}
	}
	if len(res.SkillsSh[0].Weekly) != 8 || res.SkillsSh[0].ID != "anthropics/skills/pdf" {
		t.Fatalf("expected anthropics/skills/pdf weekly sparkline from local cache, got %+v", res.SkillsSh[0])
	}

	// ClawHub search must strip source:"skills-sh" items (openai/pdf, anthropics/pdf) and sort descending by downloads.
	for _, it := range res.ClawHub {
		if it.Owner == "openai" || it.Owner == "anthropics" {
			t.Fatalf("ClawHub search leaked federated skills-sh item: %+v", it)
		}
	}
	if len(res.ClawHub) != 8 {
		t.Fatalf("ClawHub native search got %d items, want 8", len(res.ClawHub))
	}
	if res.ClawHub[0].ID != "awspace/pdf" || res.ClawHub[0].Count != 52554 ||
		res.ClawHub[0].Installs != 1502 || res.ClawHub[0].Stars != 67 || res.ClawHub[0].Versions != 1 ||
		res.ClawHub[0].WebURL != "https://clawhub.ai/awspace/skills/pdf" {
		t.Fatalf("unexpected top ClawHub search item: %+v", res.ClawHub[0])
	}
	for i := 1; i < len(res.ClawHub); i++ {
		if res.ClawHub[i].Count > res.ClawHub[i-1].Count {
			t.Fatalf("ClawHub search not sorted descending at %d: %d > %d", i, res.ClawHub[i].Count, res.ClawHub[i-1].Count)
		}
	}

	// Repeating the search hits session memory cache and leaves zero search files on disk.
	shBefore, chBefore := shCalls.Load(), chCalls.Load()
	_ = mc.SearchMarket(ctx, "pdf")
	if shCalls.Load() != shBefore || chCalls.Load() != chBefore {
		t.Fatalf("repeated search hit network: sh %d->%d, ch %d->%d", shBefore, shCalls.Load(), chBefore, chCalls.Load())
	}
	_ = filepath.WalkDir(cacheDir, func(p string, d os.DirEntry, err error) error {
		if err == nil && strings.Contains(filepath.Base(p), "search") {
			t.Fatalf("search cache must stay in memory, found disk file: %s", p)
		}
		return nil
	})

	// 1-char query on skills.sh returns empty without failing (upstream 400).
	shortRes := mc.SearchMarket(ctx, "a")
	if shortRes.SkillsShErr != nil || len(shortRes.SkillsSh) != 0 {
		t.Fatalf("single-char query on skills.sh got items=%d err=%v", len(shortRes.SkillsSh), shortRes.SkillsShErr)
	}

	// Partial failure: one upstream fails while the other succeeds.
	shFail.Store(true)
	partial := mc.SearchMarket(ctx, "partial-query")
	if partial.SkillsShErr == nil || partial.ClawHubErr != nil || len(partial.ClawHub) == 0 {
		t.Fatalf("expected skills.sh error and ClawHub success, got %+v", partial)
	}
}

func TestMarketCache_TTLExpiryAndStaleFallback(t *testing.T) {
	allTimeBody := loadMarketFixture(t, "skills_sh_alltime.json")
	hotBody := loadMarketFixture(t, "skills_sh_hot.json")

	var failMode atomic.Bool
	var allTimeCalls, hotCalls atomic.Int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if failMode.Load() {
			http.Error(w, "boom", http.StatusBadGateway)
			return
		}
		switch r.URL.Path {
		case "/api/skills/all-time/0":
			allTimeCalls.Add(1)
			_, _ = w.Write(allTimeBody)
		case "/api/skills/hot/0":
			hotCalls.Add(1)
			_, _ = w.Write(hotBody)
		default:
			http.NotFound(w, r)
		}
	}))
	defer ts.Close()

	now := time.Date(2026, 11, 23, 12, 0, 0, 0, time.UTC)
	cacheDir := filepath.Join(t.TempDir(), "market")
	mc := newTestMarketClient(t, ts, cacheDir, &now)
	ctx := context.Background()

	// Cold fetch populates cache.
	if res, err := mc.FetchMarketList(ctx, MarketSkillsSh, MarketViewAllTime, "", 0); err != nil || res.FromCache {
		t.Fatalf("cold all-time fetch: err=%v fromCache=%v", err, res.FromCache)
	}
	if _, err := mc.FetchMarketList(ctx, MarketSkillsSh, MarketViewHot, "", 0); err != nil {
		t.Fatalf("cold hot fetch: %v", err)
	}

	// 59m later: both hit cache.
	now = now.Add(59 * time.Minute)
	if res, err := mc.FetchMarketList(ctx, MarketSkillsSh, MarketViewHot, "", 0); err != nil || !res.FromCache || res.Stale {
		t.Fatalf("hot within 1h TTL should hit fresh cache, got %+v err=%v", res, err)
	}
	if res, err := mc.FetchMarketList(ctx, MarketSkillsSh, MarketViewAllTime, "", 0); err != nil || !res.FromCache {
		t.Fatalf("all-time within 6h TTL should hit fresh cache, got %+v err=%v", res, err)
	}
	if hotCalls.Load() != 1 || allTimeCalls.Load() != 1 {
		t.Fatalf("expected 1 fetch each while fresh, got allTime=%d hot=%d", allTimeCalls.Load(), hotCalls.Load())
	}

	// +2m (total 61m): hot (1h TTL) expires and refreshes; all-time (6h TTL) still hits cache.
	now = now.Add(2 * time.Minute)
	if res, err := mc.FetchMarketList(ctx, MarketSkillsSh, MarketViewHot, "", 0); err != nil || res.FromCache || hotCalls.Load() != 2 {
		t.Fatalf("hot after 1h should refresh, fromCache=%v hotCalls=%d err=%v", res.FromCache, hotCalls.Load(), err)
	}
	if res, err := mc.FetchMarketList(ctx, MarketSkillsSh, MarketViewAllTime, "", 0); err != nil || !res.FromCache || allTimeCalls.Load() != 1 {
		t.Fatalf("all-time after 61m should stay cached, fromCache=%v allTimeCalls=%d err=%v", res.FromCache, allTimeCalls.Load(), err)
	}

	// Advance +6h and fail upstream -> expired disk cache must serve Stale fallback.
	now = now.Add(6 * time.Hour)
	failMode.Store(true)
	stale, err := mc.FetchMarketList(ctx, MarketSkillsSh, MarketViewAllTime, "", 0)
	if err != nil {
		t.Fatalf("stale fallback returned unexpected err: %v", err)
	}
	if !stale.FromCache || !stale.Stale || stale.FetchErr == nil || len(stale.Items) != 6 {
		t.Fatalf("expected stale fallback with items & FetchErr, got fromCache=%v stale=%v fetchErr=%v len=%d",
			stale.FromCache, stale.Stale, stale.FetchErr, len(stale.Items))
	}

	// Cold view with no disk cache returns error on upstream failure.
	if _, err := mc.FetchMarketList(ctx, MarketSkillsSh, MarketViewTrending, "", 0); err == nil {
		t.Fatal("expected error for cold view when upstream fails")
	}

	// Corrupt cache file recovers by refreshing when upstream is healthy.
	failMode.Store(false)
	cacheFile := filepath.Join(cacheDir, "skills-sh", "trending-p0.json")
	if err := os.MkdirAll(filepath.Dir(cacheFile), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cacheFile, []byte("{corrupt json"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Serve trending so recovery succeeds.
	mc2 := NewMarketClient(Settings{}, cacheDir)
	ts2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/skills/trending/0" {
			_, _ = w.Write(loadMarketFixture(t, "skills_sh_trending.json"))
			return
		}
		http.NotFound(w, r)
	}))
	defer ts2.Close()
	mc2.SkillsShBase = ts2.URL
	mc2.Client = ts2.Client()
	mc2.Now = func() time.Time { return now }
	recovered, err := mc2.FetchMarketList(ctx, MarketSkillsSh, MarketViewTrending, "", 0)
	if err != nil || recovered.FromCache || len(recovered.Items) != 2 {
		t.Fatalf("corrupt cache should recover from upstream, got %+v err=%v", recovered, err)
	}

	var _ error = errors.New("check")
}

func TestMarkInstalled_LockEntryRules(t *testing.T) {
	items := []MarketItem{
		{Source: MarketSkillsSh, ID: "anthropics/skills/pdf", Repo: "anthropics/skills", SkillID: "pdf", Name: "pdf"},
		{Source: MarketSkillsSh, ID: "openai/skills/pdf", Repo: "openai/skills", SkillID: "pdf", Name: "pdf"},
		{Source: MarketClawHub, ID: "awspace/pdf", Owner: "awspace", SkillID: "pdf", Name: "Pdf"},
		{Source: MarketClawHub, ID: "thcjp/pdf", Owner: "thcjp", SkillID: "pdf", Name: "PDF"},
		{Source: MarketClawHub, ID: "tokauthai/skillscan", Owner: "tokauthai", SkillID: "skillscan", Name: "SkillScan"},
	}

	entries := []LockEntry{
		// Matches anthropics/skills/pdf only (not openai/skills/pdf nor ClawHub awspace/pdf).
		{Name: "pdf", Source: "anthropics/skills", SourceType: "github"},
		// Same name but github source matching ClawHub repo must NOT mark ClawHub installed.
		{Name: "pdf", Source: "thcjp/pdf", SourceType: "github"},
		// Exact ClawHub lock record.
		{Name: "pdf", Source: "awspace/pdf", SourceType: "clawhub"},
		// Case difference in SKILL.md name vs slug ("SkillScan" vs "skillscan").
		{Name: "SkillScan", Source: "tokauthai/skillscan", SourceType: "clawhub"},
	}

	MarkInstalledWithRecords(items, entries)
	want := []bool{true, false, true, false, true}
	for i, w := range want {
		if items[i].Installed != w {
			t.Fatalf("item[%d] (%s) Installed=%v, want %v", i, items[i].ID, items[i].Installed, w)
		}
	}

	// Clearing records resets Installed back to false, and Library.MarkInstalled reads scanned lock entries.
	home := t.TempDir()
	lockPath := filepath.Join(home, ".agents", ".skill-lock.json")
	if err := os.MkdirAll(filepath.Dir(lockPath), 0o755); err != nil {
		t.Fatal(err)
	}
	lockJSON := `{
  "version": 3,
  "skills": {
    "pdf": {
      "source": "thcjp/pdf",
      "sourceType": "clawhub",
      "sourceUrl": "https://clawhub.ai/thcjp/skills/pdf",
      "skillFolderHash": "sha256:abc",
      "installedAt": "2026-11-23T00:00:00.000Z",
      "updatedAt": "2026-11-23T00:00:00.000Z"
    }
  }
}`
	if err := os.WriteFile(lockPath, []byte(lockJSON), 0o644); err != nil {
		t.Fatal(err)
	}
	lib := Scan(home)
	if lib.MarketClient() == nil {
		t.Fatal("MarketClient returned nil")
	}
	lib.MarkInstalled(items)
	wantLib := []bool{false, false, false, true, false}
	for i, w := range wantLib {
		if items[i].Installed != w {
			t.Fatalf("after Scan item[%d] (%s) Installed=%v, want %v", i, items[i].ID, items[i].Installed, w)
		}
	}

	// End-to-end: ReadImport + AddImport writes lock record picked up by Scan + MarkInstalled.
	e2eHome := t.TempDir()
	zipPayload := clawHubZip(t, map[string]string{"SKILL.md": "---\nname: SkillScan\n---\n# body\n"})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/zip")
		_, _ = w.Write(zipPayload)
	}))
	defer srv.Close()

	src, err := ParseImportInput("https://clawhub.ai/tokauthai/skills/skillscan")
	if err != nil {
		t.Fatalf("ParseImportInput: %v", err)
	}
	ch := &ClawHub{Base: srv.URL, Client: srv.Client()}
	batch, err := ch.ReadImport(context.Background(), e2eHome, src, nil)
	if err != nil {
		t.Fatalf("ReadImport: %v", err)
	}
	defer batch.Discard()
	if len(batch.Skills) != 1 {
		t.Fatalf("expected 1 skill in batch, got %d", len(batch.Skills))
	}
	outcomes := Scan(e2eHome).AddImport(batch, []string{batch.Skills[0].Name}, ImportPlacement{Library: true}, nil)
	if len(outcomes) != 1 || !outcomes[0].Added {
		t.Fatalf("AddImport failed: %+v", outcomes)
	}

	recheck := []MarketItem{
		{Source: MarketClawHub, ID: "tokauthai/skillscan", Owner: "tokauthai", SkillID: "skillscan"},
		{Source: MarketClawHub, ID: "other/skillscan", Owner: "other", SkillID: "skillscan"},
	}
	Scan(e2eHome).MarkInstalled(recheck)
	if !recheck[0].Installed || recheck[1].Installed {
		t.Fatalf("e2e MarkInstalled = [%v, %v], want [true, false]", recheck[0].Installed, recheck[1].Installed)
	}
}
