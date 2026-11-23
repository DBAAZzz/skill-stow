package library

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// MarketSource identifies an upstream cloud skill marketplace.
type MarketSource string

const (
	MarketSkillsSh MarketSource = "skills.sh"
	MarketClawHub  MarketSource = "clawhub"
)

// MarketView selects a leaderboard, topic, or category view inside a marketplace.
type MarketView string

const (
	MarketViewAllTime  MarketView = "all-time"
	MarketViewTrending MarketView = "trending"
	MarketViewHot      MarketView = "hot"
	MarketViewOfficial MarketView = "official"
	MarketViewTopic    MarketView = "topic"
	MarketViewCategory MarketView = "category"
)

const (
	MarketMetricInstalls  = "installs"
	MarketMetricDownloads = "downloads"
)

// ClawHubCategories lists the canonical ClawHub category filter keys in display order.
var ClawHubCategories = []string{
	"integrations",
	"research",
	"automation",
	"creative",
	"agents",
	"development",
	"communication",
	"knowledge",
	"finance",
	"productivity",
	"security",
	"operations",
	"lifestyle",
	"other",
}

const (
	defaultSkillsShBase    = "https://skills.sh"
	defaultMarketExtrasURL = "https://github.com/DBAAZzz/skill-stow/releases/download/market-data/market-extras.json"

	marketLongCacheTTL  = 6 * time.Hour
	marketHotCacheTTL   = 1 * time.Hour
	clawHubPageSize     = 50
	maxMarketBodyBytes  = 16 << 20
	marketFallbackLimit = 200
)

// MarketItem is the unified UI representation of a skill from skills.sh or ClawHub.
type MarketItem struct {
	Source      MarketSource `json:"source"`
	ID          string       `json:"id"`
	SkillID     string       `json:"skillId,omitempty"`
	Name        string       `json:"name"`
	Owner       string       `json:"owner"`
	Repo        string       `json:"repo,omitempty"`
	Description string       `json:"description,omitempty"`
	What        string       `json:"what,omitempty"`
	Topics      []string     `json:"topics,omitempty"`
	Count       int64        `json:"count"`
	Metric      string       `json:"metric"`
	Weekly      []int64      `json:"weekly,omitempty"`
	Official    bool         `json:"official,omitempty"`
	Categories  []string     `json:"categories,omitempty"`
	Installs    int64        `json:"installs,omitempty"`
	Stars       int64        `json:"stars,omitempty"`
	Versions    int          `json:"versions,omitempty"`
	Rank        int          `json:"rank,omitempty"`
	WebURL      string       `json:"webUrl"`
	ImportRef   string       `json:"importRef"`
	Installed   bool         `json:"installed,omitempty"`
}

// MarketListResult contains a marketplace page plus cache and provenance flags.
// When upstream refresh fails and Stale is true, Items holds the expired disk cache
// while FetchErr records the refresh error.
type MarketListResult struct {
	Items      []MarketItem `json:"items"`
	HasMore    bool         `json:"hasMore"`
	NextCursor string       `json:"nextCursor,omitempty"`
	Total      int          `json:"total,omitempty"`
	FetchedAt  time.Time    `json:"fetchedAt"`
	FromCache  bool         `json:"fromCache,omitempty"`
	Stale      bool         `json:"stale,omitempty"`
	FetchErr   error        `json:"-"`
}

// MarketSearchResult holds concurrent search results and independent errors per market.
type MarketSearchResult struct {
	Query       string
	SkillsSh    []MarketItem
	SkillsShErr error
	ClawHub     []MarketItem
	ClawHubErr  error
}

// MarketExtras holds curated descriptions and topic collections published separately from APIs.
type MarketExtras struct {
	Version      int               `json:"version"`
	GeneratedAt  string            `json:"generatedAt"`
	Descriptions map[string]string `json:"descriptions"`
	Topics       []MarketTopic     `json:"topics"`
}

// MarketTopic defines a curated skills.sh topic tab and its member skills.
type MarketTopic struct {
	ID      string             `json:"id"`
	Title   string             `json:"title"`
	Summary string             `json:"summary"`
	Skills  []MarketTopicSkill `json:"skills"`
}

// MarketTopicSkill references a skills.sh skill inside a topic together with a short purpose line.
type MarketTopicSkill struct {
	ID   string `json:"id"`
	What string `json:"what"`
}

// MarketClient fetches, searches, and caches skills.sh and ClawHub marketplace feeds.
// Client inherits proxy and timeout configuration from Settings via NewImportRemote.
type MarketClient struct {
	Client       *http.Client
	SkillsShBase string
	ClawHubBase  string
	ExtrasURL    string
	CacheDir     string
	Now          func() time.Time
	Sleep        func(context.Context, time.Duration) error
	MaxRetryWait time.Duration

	mu          sync.Mutex
	searchCache map[searchCacheKey][]MarketItem
	extras      *MarketExtras
	extrasETag  string
}

type searchCacheKey struct {
	source MarketSource
	query  string
}

// NewMarketClient creates a MarketClient configured with Settings proxy rules and cacheDir.
func NewMarketClient(s Settings, cacheDir string) *MarketClient {
	remote := NewImportRemote(s)
	return &MarketClient{
		Client:       remote.Web,
		SkillsShBase: defaultSkillsShBase,
		ClawHubBase:  defaultClawHubBase,
		ExtrasURL:    defaultMarketExtrasURL,
		CacheDir:     cacheDir,
		Now:          time.Now,
		Sleep:        sleepContext,
		MaxRetryWait: clawHubMaxWait,
		searchCache:  make(map[searchCacheKey][]MarketItem),
	}
}

// MarketClient returns a shared MarketClient storing caches under AppDir()/market.
func (l *Library) MarketClient() *MarketClient {
	if l == nil {
		return NewMarketClient(Settings{}, "")
	}
	l.marketMu.Lock()
	defer l.marketMu.Unlock()
	if l.market == nil {
		var dir string
		if appDir := l.AppDir(); appDir != "" {
			dir = filepath.Join(appDir, "market")
		}
		l.market = NewMarketClient(l.Settings, dir)
	}
	return l.market
}

func (m *MarketClient) now() time.Time {
	if m.Now != nil {
		return m.Now()
	}
	return time.Now()
}

func (m *MarketClient) httpClient() *http.Client {
	if m.Client != nil {
		return m.Client
	}
	return &http.Client{Timeout: requestTimeout}
}

func (m *MarketClient) sleepFn() func(context.Context, time.Duration) error {
	if m.Sleep != nil {
		return m.Sleep
	}
	return sleepContext
}

func (m *MarketClient) retryWait() time.Duration {
	if m.MaxRetryWait > 0 {
		return m.MaxRetryWait
	}
	return clawHubMaxWait
}

func (m *MarketClient) skillsShBase() string {
	if b := strings.TrimRight(m.SkillsShBase, "/"); b != "" {
		return b
	}
	return defaultSkillsShBase
}

func (m *MarketClient) clawHubBase() string {
	if b := strings.TrimRight(m.ClawHubBase, "/"); b != "" {
		return b
	}
	return defaultClawHubBase
}

func (m *MarketClient) extrasURL() string {
	if u := strings.TrimSpace(m.ExtrasURL); u != "" {
		return u
	}
	return defaultMarketExtrasURL
}

// FetchMarketList returns a paginated view for source, honoring per-view TTLs
// and falling back to expired disk cache when upstream requests fail.
func (m *MarketClient) FetchMarketList(ctx context.Context, source MarketSource, view MarketView, key string, page int) (*MarketListResult, error) {
	if page < 0 {
		page = 0
	}
	switch source {
	case MarketSkillsSh:
		return m.fetchSkillsShList(ctx, view, key, page)
	case MarketClawHub:
		return m.fetchClawHubList(ctx, view, key, page)
	default:
		return nil, fmt.Errorf("unknown market source %q", source)
	}
}

// CachedMarketList reads the disk-cached page without touching the network.
// It returns nil when no readable cache exists on disk.
func (m *MarketClient) CachedMarketList(source MarketSource, view MarketView, key string, page int) *MarketListResult {
	if page < 0 {
		page = 0
	}
	switch source {
	case MarketSkillsSh:
		if view == MarketViewOfficial {
			res := m.readCachedList(m.skillsShCachePath(MarketViewAllTime, page), marketLongCacheTTL)
			if res == nil {
				return nil
			}
			out := filterOfficialResult(res)
			m.MergeMarketExtras(out.Items, m.CachedMarketExtras())
			return out
		}
		if view == MarketViewTopic {
			extras := m.CachedMarketExtras()
			if extras == nil {
				return nil
			}
			res := m.buildTopicList(extras, key, page)
			res.FromCache = true
			return res
		}
		ttl := skillsShTTL(view)
		if ttl <= 0 {
			return nil
		}
		res := m.readCachedList(m.skillsShCachePath(view, page), ttl)
		if res != nil {
			m.MergeMarketExtras(res.Items, m.CachedMarketExtras())
		}
		return res
	case MarketClawHub:
		cat, ok := clawHubCategoryForView(view, key)
		if !ok {
			return nil
		}
		return m.readCachedList(m.clawHubCachePath(cat, page), marketLongCacheTTL)
	default:
		return nil
	}
}

func (m *MarketClient) fetchSkillsShList(ctx context.Context, view MarketView, key string, page int) (*MarketListResult, error) {
	switch view {
	case MarketViewAllTime, MarketViewTrending, MarketViewHot:
		ttl := skillsShTTL(view)
		cachePath := m.skillsShCachePath(view, page)
		cached := m.readCachedList(cachePath, ttl)
		if cached != nil && !cached.Stale {
			m.MergeMarketExtras(cached.Items, m.CachedMarketExtras())
			return cached, nil
		}
		fresh, err := m.requestSkillsShFeed(ctx, view, page)
		if err != nil {
			if cached != nil {
				cached.FromCache = true
				cached.Stale = true
				cached.FetchErr = err
				m.MergeMarketExtras(cached.Items, m.CachedMarketExtras())
				return cached, nil
			}
			return nil, err
		}
		m.writeCachedList(cachePath, fresh)
		out := cloneMarketListResult(fresh)
		m.MergeMarketExtras(out.Items, m.CachedMarketExtras())
		return out, nil

	case MarketViewOfficial:
		allTime, err := m.fetchSkillsShList(ctx, MarketViewAllTime, "", page)
		if err != nil {
			return nil, err
		}
		return filterOfficialResult(allTime), nil

	case MarketViewTopic:
		if page > 0 {
			return &MarketListResult{Items: nil, HasMore: false, FetchedAt: m.now()}, nil
		}
		extras, err := m.loadOrFetchExtras(ctx)
		if err != nil {
			return nil, err
		}
		return m.buildTopicList(extras, key, page), nil

	default:
		return nil, fmt.Errorf("skills.sh does not support view %q", view)
	}
}

func filterOfficialResult(allTime *MarketListResult) *MarketListResult {
	out := cloneMarketListResult(allTime)
	official := make([]MarketItem, 0, len(out.Items))
	for _, it := range out.Items {
		if it.Official {
			official = append(official, it)
		}
	}
	out.Items = official
	return out
}

func (m *MarketClient) buildTopicList(extras *MarketExtras, topicID string, page int) *MarketListResult {
	if extras == nil || page > 0 {
		return &MarketListResult{Items: nil, HasMore: false, FetchedAt: m.now()}
	}
	var topic *MarketTopic
	for i := range extras.Topics {
		if extras.Topics[i].ID == topicID {
			topic = &extras.Topics[i]
			break
		}
	}
	if topic == nil {
		return &MarketListResult{Items: nil, HasMore: false, FetchedAt: m.now()}
	}

	idx := m.loadCachedSkillsShIndex()
	items := make([]MarketItem, 0, len(topic.Skills))
	for _, ts := range topic.Skills {
		it := skillsShBaseItem(ts.ID)
		it.What = ts.What
		if cached, ok := idx[it.ID]; ok {
			if cached.Name != "" {
				it.Name = cached.Name
			}
			it.Count = cached.Count
			it.Installs = cached.Installs
			it.Weekly = slices.Clone(cached.Weekly)
			it.Official = cached.Official
			it.Rank = cached.Rank
		}
		items = append(items, it)
	}
	m.MergeMarketExtras(items, extras)
	return &MarketListResult{
		Items:     items,
		HasMore:   false,
		FetchedAt: m.now(),
	}
}

func skillsShTTL(view MarketView) time.Duration {
	switch view {
	case MarketViewHot:
		return marketHotCacheTTL
	case MarketViewAllTime, MarketViewTrending:
		return marketLongCacheTTL
	default:
		return 0
	}
}

func (m *MarketClient) requestSkillsShFeed(ctx context.Context, view MarketView, page int) (*MarketListResult, error) {
	u := fmt.Sprintf("%s/api/skills/%s/%d", m.skillsShBase(), url.PathEscape(string(view)), page)
	data, _, err := m.getBytes(ctx, u, "", false)
	if err != nil {
		return nil, err
	}

	var resp skillsShFeedPayload
	trimmed := bytes.TrimSpace(data)
	if bytes.HasPrefix(trimmed, []byte("[")) {
		if err := json.Unmarshal(trimmed, &resp.Skills); err != nil {
			return nil, fmt.Errorf("decode skills.sh %s array: %w", view, err)
		}
	} else {
		if err := json.Unmarshal(trimmed, &resp); err != nil {
			return nil, fmt.Errorf("decode skills.sh %s: %w", view, err)
		}
	}

	items := make([]MarketItem, 0, len(resp.Skills))
	for i, s := range resp.Skills {
		it := s.toMarketItem()
		if view == MarketViewAllTime {
			pageSize := max(len(resp.Skills), marketFallbackLimit)
			it.Rank = page*pageSize + i + 1
		}
		items = append(items, it)
	}
	return &MarketListResult{
		Items:     items,
		HasMore:   resp.HasMore,
		Total:     resp.Total,
		FetchedAt: m.now(),
	}, nil
}

func (m *MarketClient) fetchClawHubList(ctx context.Context, view MarketView, key string, page int) (*MarketListResult, error) {
	cat, ok := clawHubCategoryForView(view, key)
	if !ok {
		return nil, fmt.Errorf("clawhub does not support view %q", view)
	}

	cachePath := m.clawHubCachePath(cat, page)
	cached := m.readCachedList(cachePath, marketLongCacheTTL)
	if cached != nil && !cached.Stale {
		return cached, nil
	}

	var cursor string
	if page > 0 {
		prev, err := m.fetchClawHubList(ctx, view, key, page-1)
		if err != nil {
			if cached != nil {
				cached.FromCache = true
				cached.Stale = true
				cached.FetchErr = err
				return cached, nil
			}
			return nil, err
		}
		if prev.NextCursor == "" {
			return &MarketListResult{Items: nil, HasMore: false, FetchedAt: m.now()}, nil
		}
		cursor = prev.NextCursor
	}

	fresh, err := m.requestClawHubFeed(ctx, cat, cursor)
	if err != nil {
		if cached != nil {
			cached.FromCache = true
			cached.Stale = true
			cached.FetchErr = err
			return cached, nil
		}
		return nil, err
	}
	m.writeCachedList(cachePath, fresh)
	return cloneMarketListResult(fresh), nil
}

func clawHubCategoryForView(view MarketView, key string) (string, bool) {
	switch view {
	case MarketViewAllTime:
		return "", true
	case MarketViewCategory:
		k := strings.TrimSpace(key)
		if k == "all" {
			k = ""
		}
		return k, true
	default:
		return "", false
	}
}

func (m *MarketClient) requestClawHubFeed(ctx context.Context, category, cursor string) (*MarketListResult, error) {
	q := url.Values{}
	q.Set("sort", "downloads")
	q.Set("limit", strconv.Itoa(clawHubPageSize))
	// ClawHub returns 400 Invalid skill category when category is present and empty.
	if category != "" {
		q.Set("category", category)
	}
	if cursor != "" {
		q.Set("cursor", cursor)
	}
	u := m.clawHubBase() + "/api/v1/skills?" + q.Encode()

	data, _, err := m.getBytes(ctx, u, "", true)
	if err != nil {
		return nil, err
	}
	var resp clawHubListPayload
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, fmt.Errorf("decode clawhub skills: %w", err)
	}
	items := make([]MarketItem, 0, len(resp.Items))
	for _, raw := range resp.Items {
		items = append(items, raw.toMarketItem())
	}
	return &MarketListResult{
		Items:      items,
		HasMore:    resp.NextCursor != "",
		NextCursor: resp.NextCursor,
		FetchedAt:  m.now(),
	}, nil
}

// SearchMarket concurrently searches skills.sh and ClawHub for query, sorting each source
// descending by its own metric and keeping session results cached in memory only.
func (m *MarketClient) SearchMarket(ctx context.Context, query string) MarketSearchResult {
	q := strings.TrimSpace(query)
	out := MarketSearchResult{Query: q}
	if q == "" {
		return out
	}

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		items, err := m.SearchMarketSource(ctx, MarketSkillsSh, q)
		out.SkillsSh, out.SkillsShErr = items, err
	}()
	go func() {
		defer wg.Done()
		items, err := m.SearchMarketSource(ctx, MarketClawHub, q)
		out.ClawHub, out.ClawHubErr = items, err
	}()
	wg.Wait()
	return out
}

// SearchMarketSource runs search against one marketplace and uses the session memory cache.
func (m *MarketClient) SearchMarketSource(ctx context.Context, source MarketSource, query string) ([]MarketItem, error) {
	q := strings.TrimSpace(query)
	if q == "" {
		return nil, nil
	}
	if items, ok := m.getSearchCache(source, q); ok {
		return m.finalizeSearchItems(source, items), nil
	}

	var (
		items []MarketItem
		err   error
	)
	switch source {
	case MarketSkillsSh:
		// Upstream rejects queries shorter than 2 characters with HTTP 400.
		if len([]rune(q)) < 2 {
			return []MarketItem{}, nil
		}
		items, err = m.requestSkillsShSearch(ctx, q)
	case MarketClawHub:
		items, err = m.requestClawHubSearch(ctx, q)
	default:
		return nil, fmt.Errorf("unknown market source %q", source)
	}
	if err != nil {
		return nil, err
	}
	m.putSearchCache(source, q, items)
	return m.finalizeSearchItems(source, items), nil
}

func (m *MarketClient) finalizeSearchItems(source MarketSource, src []MarketItem) []MarketItem {
	out := cloneMarketItems(src)
	if source == MarketSkillsSh {
		idx := m.loadCachedSkillsShIndex()
		for i := range out {
			if cached, ok := idx[out[i].ID]; ok && len(cached.Weekly) > 0 {
				out[i].Weekly = slices.Clone(cached.Weekly)
			}
			// Official badge only applies to curated leaderboards, never search results.
			out[i].Official = false
		}
		m.MergeMarketExtras(out, m.CachedMarketExtras())
	}
	return out
}

func (m *MarketClient) requestSkillsShSearch(ctx context.Context, query string) ([]MarketItem, error) {
	q := url.Values{}
	q.Set("q", query)
	u := m.skillsShBase() + "/api/search?" + q.Encode()
	data, _, err := m.getBytes(ctx, u, "", false)
	if err != nil {
		return nil, err
	}
	var resp struct {
		Skills []skillsShSkill `json:"skills"`
	}
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, fmt.Errorf("decode skills.sh search: %w", err)
	}
	items := make([]MarketItem, 0, len(resp.Skills))
	for _, s := range resp.Skills {
		it := s.toMarketItem()
		it.Official = false
		items = append(items, it)
	}
	sort.SliceStable(items, func(i, j int) bool {
		return items[i].Count > items[j].Count
	})
	return items, nil
}

func (m *MarketClient) requestClawHubSearch(ctx context.Context, query string) ([]MarketItem, error) {
	q := url.Values{}
	q.Set("q", query)
	u := m.clawHubBase() + "/api/v1/search?" + q.Encode()
	data, _, err := m.getBytes(ctx, u, "", true)
	if err != nil {
		return nil, err
	}
	var resp clawHubSearchPayload
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, fmt.Errorf("decode clawhub search: %w", err)
	}
	items := make([]MarketItem, 0, len(resp.Results))
	for _, r := range resp.Results {
		if !r.isNativeClawHub() {
			continue
		}
		items = append(items, r.toMarketItem())
	}
	sort.SliceStable(items, func(i, j int) bool {
		return items[i].Count > items[j].Count
	})
	return items, nil
}

// FetchMarketExtras downloads market-extras.json with ETag revalidation and caches it on disk.
// When offline or upstream fails, it falls back to any cached copy and only returns an error if none exists.
func (m *MarketClient) FetchMarketExtras(ctx context.Context) (*MarketExtras, error) {
	env := m.readExtrasEnvelope()
	var ifNoneMatch string
	m.mu.Lock()
	if m.extrasETag != "" {
		ifNoneMatch = m.extrasETag
	} else if env != nil {
		ifNoneMatch = env.ETag
	}
	m.mu.Unlock()

	data, etag, status, err := m.getBytesWithStatus(ctx, m.extrasURL(), ifNoneMatch, false)
	if err == nil && status == http.StatusNotModified {
		if env != nil && env.Extras != nil {
			m.setMemoryExtras(env.Extras, ifNoneMatch)
			return cloneMarketExtras(env.Extras), nil
		}
		if mem := m.CachedMarketExtras(); mem != nil {
			return mem, nil
		}
	}
	if err != nil {
		if fallback := m.CachedMarketExtras(); fallback != nil {
			return fallback, nil
		}
		return nil, err
	}

	var extras MarketExtras
	if err := json.Unmarshal(data, &extras); err != nil {
		if fallback := m.CachedMarketExtras(); fallback != nil {
			return fallback, nil
		}
		return nil, fmt.Errorf("decode market-extras: %w", err)
	}
	if extras.Descriptions == nil {
		extras.Descriptions = make(map[string]string)
	}
	m.setMemoryExtras(&extras, etag)
	m.writeExtrasEnvelope(&extrasEnvelope{
		ETag:      etag,
		FetchedAt: m.now(),
		Extras:    &extras,
	})
	return cloneMarketExtras(&extras), nil
}

// CachedMarketExtras returns extras from memory or disk without making network calls.
func (m *MarketClient) CachedMarketExtras() *MarketExtras {
	m.mu.Lock()
	mem := m.extras
	m.mu.Unlock()
	if mem != nil {
		return cloneMarketExtras(mem)
	}
	if env := m.readExtrasEnvelope(); env != nil && env.Extras != nil {
		m.setMemoryExtras(env.Extras, env.ETag)
		return cloneMarketExtras(env.Extras)
	}
	return nil
}

func (m *MarketClient) loadOrFetchExtras(ctx context.Context) (*MarketExtras, error) {
	if cached := m.CachedMarketExtras(); cached != nil {
		return cached, nil
	}
	return m.FetchMarketExtras(ctx)
}

func (m *MarketClient) setMemoryExtras(ex *MarketExtras, etag string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.extras = cloneMarketExtras(ex)
	if etag != "" {
		m.extrasETag = etag
	}
}

// MergeMarketExtras enriches skills.sh items with descriptions and topic memberships from extras.
func (m *MarketClient) MergeMarketExtras(items []MarketItem, extras *MarketExtras) {
	if extras == nil || len(items) == 0 {
		return
	}
	topicMap := make(map[string][]string)
	for _, t := range extras.Topics {
		for _, s := range t.Skills {
			topicMap[s.ID] = append(topicMap[s.ID], t.ID)
		}
	}
	for i := range items {
		if items[i].Source != MarketSkillsSh {
			continue
		}
		if desc := strings.TrimSpace(extras.Descriptions[items[i].ID]); desc != "" {
			items[i].Description = desc
		}
		if topics := topicMap[items[i].ID]; len(topics) > 0 {
			items[i].Topics = slices.Clone(topics)
		}
	}
}

// MarkInstalled updates each item's Installed flag from l's lock and app-import records.
func (l *Library) MarkInstalled(items []MarketItem) {
	if len(items) == 0 {
		return
	}
	if l == nil {
		MarkInstalledWithRecords(items, nil)
		return
	}
	MarkInstalledWithRecords(items, l.updateEntries())
}

// MarkInstalledWithRecords sets Installed on items strictly from lock records:
// skills.sh requires Source == owner/repo and Name == skillId; ClawHub requires
// SourceType == "clawhub" with matching owner/slug source and slug.
func MarkInstalledWithRecords(items []MarketItem, entries []LockEntry) {
	for i := range items {
		items[i].Installed = isMarketItemInstalled(items[i], entries)
	}
}

func isMarketItemInstalled(it MarketItem, entries []LockEntry) bool {
	slug := it.SkillID
	if slug == "" {
		slug = it.Name
	}
	switch it.Source {
	case MarketSkillsSh:
		if it.Repo == "" || slug == "" {
			return false
		}
		for _, e := range entries {
			if e.SourceType != "clawhub" && e.Source == it.Repo && e.Name == slug {
				return true
			}
		}
	case MarketClawHub:
		ownerSlug := it.ID
		if it.Owner != "" && slug != "" {
			ownerSlug = it.Owner + "/" + slug
		}
		if ownerSlug == "" || slug == "" {
			return false
		}
		for _, e := range entries {
			if e.SourceType == "clawhub" && e.Source == ownerSlug && (e.Name == "" || strings.EqualFold(e.Name, slug)) {
				return true
			}
		}
	}
	return false
}

func (m *MarketClient) getBytes(ctx context.Context, rawURL, ifNoneMatch string, retryRateLimit bool) ([]byte, string, error) {
	data, etag, _, err := m.getBytesWithStatus(ctx, rawURL, ifNoneMatch, retryRateLimit)
	return data, etag, err
}

func (m *MarketClient) getBytesWithStatus(ctx context.Context, rawURL, ifNoneMatch string, retryRateLimit bool) ([]byte, string, int, error) {
	newReq := func() (*http.Request, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("Accept", "application/json")
		if ifNoneMatch != "" {
			req.Header.Set("If-None-Match", ifNoneMatch)
		}
		return req, nil
	}

	var (
		resp *http.Response
		err  error
	)
	if retryRateLimit {
		resp, err = doWithRetry(ctx, m.httpClient(), m.retryWait(), m.sleepFn(), newReq)
	} else {
		var req *http.Request
		req, err = newReq()
		if err == nil {
			resp, err = m.httpClient().Do(req)
			if err == nil && resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNotModified {
				body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
				resp.Body.Close()
				err = &httpStatusError{Code: resp.StatusCode, Body: strings.TrimSpace(string(body))}
				resp = nil
			}
		}
	}
	if err != nil {
		return nil, "", 0, err
	}
	defer resp.Body.Close()

	etag := strings.TrimSpace(resp.Header.Get("ETag"))
	if resp.StatusCode == http.StatusNotModified {
		return nil, etag, resp.StatusCode, nil
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxMarketBodyBytes+1))
	if err != nil {
		return nil, "", resp.StatusCode, err
	}
	if len(data) > maxMarketBodyBytes {
		return nil, "", resp.StatusCode, errors.New("market response exceeds size limit")
	}
	return data, etag, resp.StatusCode, nil
}

type listCacheEnvelope struct {
	Items      []MarketItem `json:"items"`
	HasMore    bool         `json:"hasMore"`
	NextCursor string       `json:"nextCursor,omitempty"`
	Total      int          `json:"total,omitempty"`
	FetchedAt  time.Time    `json:"fetchedAt"`
}

type extrasEnvelope struct {
	ETag      string        `json:"etag,omitempty"`
	FetchedAt time.Time     `json:"fetchedAt"`
	Extras    *MarketExtras `json:"extras"`
}

func (m *MarketClient) skillsShCachePath(view MarketView, page int) string {
	if m.CacheDir == "" {
		return ""
	}
	return filepath.Join(m.CacheDir, "skills-sh", fmt.Sprintf("%s-p%d.json", sanitizeCacheSegment(string(view)), page))
}

func (m *MarketClient) clawHubCachePath(category string, page int) string {
	if m.CacheDir == "" {
		return ""
	}
	cat := sanitizeCacheSegment(category)
	if cat == "" {
		cat = "all"
	}
	return filepath.Join(m.CacheDir, "clawhub", fmt.Sprintf("%s-p%d.json", cat, page))
}

func (m *MarketClient) extrasCachePath() string {
	if m.CacheDir == "" {
		return ""
	}
	return filepath.Join(m.CacheDir, "market-extras.json")
}

func sanitizeCacheSegment(s string) string {
	var b strings.Builder
	for _, r := range strings.TrimSpace(s) {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	return b.String()
}

func (m *MarketClient) readCachedList(path string, ttl time.Duration) *MarketListResult {
	if path == "" {
		return nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var env listCacheEnvelope
	if err := json.Unmarshal(data, &env); err != nil {
		return nil
	}
	stale := ttl > 0 && (env.FetchedAt.IsZero() || m.now().Sub(env.FetchedAt) > ttl)
	return &MarketListResult{
		Items:      cloneMarketItems(env.Items),
		HasMore:    env.HasMore,
		NextCursor: env.NextCursor,
		Total:      env.Total,
		FetchedAt:  env.FetchedAt,
		FromCache:  true,
		Stale:      stale,
	}
}

func (m *MarketClient) writeCachedList(path string, res *MarketListResult) {
	if path == "" || res == nil {
		return
	}
	_ = writeAtomicCacheJSON(path, listCacheEnvelope{
		Items:      res.Items,
		HasMore:    res.HasMore,
		NextCursor: res.NextCursor,
		Total:      res.Total,
		FetchedAt:  res.FetchedAt,
	})
}

func (m *MarketClient) readExtrasEnvelope() *extrasEnvelope {
	p := m.extrasCachePath()
	if p == "" {
		return nil
	}
	data, err := os.ReadFile(p)
	if err != nil {
		return nil
	}
	var env extrasEnvelope
	if err := json.Unmarshal(data, &env); err != nil || env.Extras == nil {
		return nil
	}
	if env.Extras.Descriptions == nil {
		env.Extras.Descriptions = make(map[string]string)
	}
	return &env
}

func (m *MarketClient) writeExtrasEnvelope(env *extrasEnvelope) {
	p := m.extrasCachePath()
	if p == "" || env == nil {
		return
	}
	_ = writeAtomicCacheJSON(p, env)
}

func writeAtomicCacheJSON(file string, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	dir := filepath.Dir(file)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".market-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if _, err := tmp.Write(b); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, file)
}

func (m *MarketClient) loadCachedSkillsShIndex() map[string]MarketItem {
	idx := make(map[string]MarketItem)
	if m.CacheDir == "" {
		return idx
	}
	dir := filepath.Join(m.CacheDir, "skills-sh")
	entries, err := os.ReadDir(dir)
	if err != nil {
		return idx
	}
	// Prefer all-time (lifetime installs + weeklyInstalls) before trending and hot.
	for _, prefix := range []string{"all-time-p", "trending-p", "hot-p"} {
		var files []string
		for _, de := range entries {
			if !de.IsDir() && strings.HasPrefix(de.Name(), prefix) && strings.HasSuffix(de.Name(), ".json") {
				files = append(files, de.Name())
			}
		}
		slices.Sort(files)
		for _, name := range files {
			cached := m.readCachedList(filepath.Join(dir, name), 0)
			if cached == nil {
				continue
			}
			for _, it := range cached.Items {
				if _, exists := idx[it.ID]; !exists {
					idx[it.ID] = it
				}
			}
		}
	}
	return idx
}

func (m *MarketClient) getSearchCache(source MarketSource, query string) ([]MarketItem, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.searchCache == nil {
		return nil, false
	}
	items, ok := m.searchCache[searchCacheKey{source: source, query: query}]
	return items, ok
}

func (m *MarketClient) putSearchCache(source MarketSource, query string, items []MarketItem) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.searchCache == nil {
		m.searchCache = make(map[searchCacheKey][]MarketItem)
	}
	m.searchCache[searchCacheKey{source: source, query: query}] = cloneMarketItems(items)
}

func cloneMarketListResult(r *MarketListResult) *MarketListResult {
	if r == nil {
		return nil
	}
	cp := *r
	cp.Items = cloneMarketItems(r.Items)
	return &cp
}

func cloneMarketItems(items []MarketItem) []MarketItem {
	if len(items) == 0 {
		return nil
	}
	out := make([]MarketItem, len(items))
	for i, it := range items {
		it.Weekly = slices.Clone(it.Weekly)
		it.Categories = slices.Clone(it.Categories)
		it.Topics = slices.Clone(it.Topics)
		out[i] = it
	}
	return out
}

func cloneMarketExtras(ex *MarketExtras) *MarketExtras {
	if ex == nil {
		return nil
	}
	cp := MarketExtras{
		Version:      ex.Version,
		GeneratedAt:  ex.GeneratedAt,
		Descriptions: make(map[string]string, len(ex.Descriptions)),
		Topics:       make([]MarketTopic, len(ex.Topics)),
	}
	for k, v := range ex.Descriptions {
		cp.Descriptions[k] = v
	}
	for i, t := range ex.Topics {
		cp.Topics[i] = MarketTopic{
			ID:      t.ID,
			Title:   t.Title,
			Summary: t.Summary,
			Skills:  slices.Clone(t.Skills),
		}
	}
	return &cp
}

// --- Upstream wire JSON shapes & field mapping ---

type skillsShFeedPayload struct {
	Skills  []skillsShSkill `json:"skills"`
	Total   int             `json:"total"`
	HasMore bool            `json:"hasMore"`
	Page    int             `json:"page"`
}

type skillsShSkill struct {
	ID             string  `json:"id"`
	Source         string  `json:"source"`
	SkillID        string  `json:"skillId"`
	Name           string  `json:"name"`
	Installs       int64   `json:"installs"`
	WeeklyInstalls []int64 `json:"weeklyInstalls"`
	IsOfficial     bool    `json:"isOfficial"`
}

func (s skillsShSkill) toMarketItem() MarketItem {
	id := strings.TrimSpace(s.ID)
	src := strings.Trim(strings.TrimSpace(s.Source), "/")
	skillID := strings.Trim(strings.TrimSpace(s.SkillID), "/")
	if id == "" && src != "" && skillID != "" {
		id = src + "/" + skillID
	} else if id == "" {
		id = skillID
	}
	it := skillsShBaseItem(id)
	if src != "" {
		it.Repo = src
		if owner, _, ok := strings.Cut(src, "/"); ok {
			it.Owner = owner
		} else {
			it.Owner = src
		}
	}
	if skillID != "" {
		it.SkillID = skillID
	}
	if name := strings.TrimSpace(s.Name); name != "" {
		it.Name = name
	}
	it.Count = s.Installs
	it.Installs = s.Installs
	it.Weekly = slices.Clone(s.WeeklyInstalls)
	it.Official = s.IsOfficial
	return it
}

func skillsShBaseItem(id string) MarketItem {
	cleanID := strings.Trim(strings.TrimSpace(id), "/")
	parts := strings.Split(cleanID, "/")
	var owner, repo, skillID string
	switch len(parts) {
	case 0:
	case 1:
		skillID = parts[0]
	case 2:
		owner, repo, skillID = parts[0], parts[0], parts[1]
	default:
		owner = parts[0]
		repo = parts[0] + "/" + parts[1]
		skillID = strings.Join(parts[2:], "/")
	}
	webURL := defaultSkillsShBase + "/" + cleanID
	return MarketItem{
		Source:    MarketSkillsSh,
		ID:        cleanID,
		SkillID:   skillID,
		Name:      skillID,
		Owner:     owner,
		Repo:      repo,
		Count:     0,
		Metric:    MarketMetricInstalls,
		WebURL:    webURL,
		ImportRef: webURL,
	}
}

type clawHubListPayload struct {
	Items      []clawHubListItem `json:"items"`
	NextCursor string            `json:"nextCursor"`
}

type clawHubStats struct {
	Downloads int64 `json:"downloads"`
	Installs  int64 `json:"installs"`
	Stars     int64 `json:"stars"`
	Versions  int   `json:"versions"`
}

type clawHubListItem struct {
	OwnerHandle string       `json:"ownerHandle"`
	Slug        string       `json:"slug"`
	DisplayName string       `json:"displayName"`
	Summary     string       `json:"summary"`
	Categories  []string     `json:"categories"`
	Stats       clawHubStats `json:"stats"`
}

func (c clawHubListItem) toMarketItem() MarketItem {
	owner := strings.TrimSpace(c.OwnerHandle)
	slug := strings.TrimSpace(c.Slug)
	name := strings.TrimSpace(c.DisplayName)
	if name == "" {
		name = slug
	}
	ref := fmt.Sprintf("%s/%s/skills/%s", defaultClawHubBase, owner, slug)
	return MarketItem{
		Source:      MarketClawHub,
		ID:          owner + "/" + slug,
		SkillID:     slug,
		Name:        name,
		Owner:       owner,
		Description: strings.TrimSpace(c.Summary),
		Count:       c.Stats.Downloads,
		Metric:      MarketMetricDownloads,
		Categories:  slices.Clone(c.Categories),
		Installs:    c.Stats.Installs,
		Stars:       c.Stats.Stars,
		Versions:    c.Stats.Versions,
		WebURL:      ref,
		ImportRef:   ref,
	}
}

type clawHubSearchPayload struct {
	Results []clawHubSearchResult `json:"results"`
}

type clawHubSearchResult struct {
	Source       string   `json:"source"`
	OwnerHandle  string   `json:"ownerHandle"`
	Slug         string   `json:"slug"`
	DisplayName  string   `json:"displayName"`
	Summary      string   `json:"summary"`
	Categories   []string `json:"categories"`
	CanonicalURL string   `json:"canonicalUrl"`
	Downloads    int64    `json:"downloads"`
	Install      struct {
		Kind string `json:"kind"`
	} `json:"install"`
	Metrics struct {
		Rolling60DayInstalls *int64 `json:"rolling60DayInstalls"`
	} `json:"metrics"`
	Native *struct {
		Skill *struct {
			Stats clawHubStats `json:"stats"`
		} `json:"skill"`
	} `json:"native"`
}

func (r clawHubSearchResult) isNativeClawHub() bool {
	if r.Source != "" && r.Source != "clawhub" {
		return false
	}
	if r.Install.Kind != "" && r.Install.Kind != "clawhub" {
		return false
	}
	return r.OwnerHandle != "" && r.Slug != ""
}

func (r clawHubSearchResult) toMarketItem() MarketItem {
	owner := strings.TrimSpace(r.OwnerHandle)
	slug := strings.TrimSpace(r.Slug)
	name := strings.TrimSpace(r.DisplayName)
	if name == "" {
		name = slug
	}
	var installs, stars int64
	var versions int
	count := r.Downloads
	if r.Native != nil && r.Native.Skill != nil {
		st := r.Native.Skill.Stats
		installs = st.Installs
		stars = st.Stars
		versions = st.Versions
		if count == 0 {
			count = st.Downloads
		}
	} else if r.Metrics.Rolling60DayInstalls != nil {
		installs = *r.Metrics.Rolling60DayInstalls
	}

	importRef := fmt.Sprintf("%s/%s/skills/%s", defaultClawHubBase, owner, slug)
	webURL := importRef
	if c := strings.TrimSpace(r.CanonicalURL); strings.HasPrefix(c, "/") {
		webURL = defaultClawHubBase + c
	}

	return MarketItem{
		Source:      MarketClawHub,
		ID:          owner + "/" + slug,
		SkillID:     slug,
		Name:        name,
		Owner:       owner,
		Description: strings.TrimSpace(r.Summary),
		Count:       count,
		Metric:      MarketMetricDownloads,
		Categories:  slices.Clone(r.Categories),
		Installs:    installs,
		Stars:       stars,
		Versions:    versions,
		WebURL:      webURL,
		ImportRef:   importRef,
	}
}
