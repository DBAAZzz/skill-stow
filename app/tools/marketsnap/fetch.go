package main

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
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"skillmanager/internal/library"
)

const (
	defaultSkillsShBase = "https://skills.sh"
	defaultUserAgent    = "skill-stow-marketsnap/1.0 (+https://github.com/DBAAZzz/skill-stow)"
	defaultConcurrency  = 4
	defaultInterval     = 100 * time.Millisecond
	defaultMaxRetries   = 3
	defaultTimeout      = 20 * time.Second
	defaultMaxBytes     = 4 << 20
	defaultRotateDays   = 30
	maxResponseBytes    = 16 << 20
)

type runOptions struct {
	baseURL     string
	prevPath    string
	prevURL     string
	outPath     string
	userAgent   string
	limit       int
	concurrency int
	interval    time.Duration
	maxRetries  int
	timeout     time.Duration
	maxBytes    int
	minSkills   int
	rotateDays  int
	client      *http.Client
	now         func() time.Time
	sleep       func(context.Context, time.Duration) error
}

type runStats struct {
	Duration        time.Duration
	CandidateSkills int
	Topics          int
	Descriptions    int
	Fetched         int
	FetchFailed     int
	Reused          int
	Bytes           int
}

func (o *runOptions) withDefaults() error {
	if o.baseURL == "" {
		o.baseURL = defaultSkillsShBase
	}
	o.baseURL = strings.TrimRight(o.baseURL, "/")
	if o.userAgent == "" {
		o.userAgent = defaultUserAgent
	}
	if strings.Contains(o.userAgent, "@") {
		return errors.New("user-agent must not contain an email address")
	}
	if o.outPath == "" {
		return errors.New("missing -out path")
	}
	if o.concurrency <= 0 {
		o.concurrency = defaultConcurrency
	}
	if o.interval < 0 {
		o.interval = 0
	}
	if o.maxRetries < 0 {
		o.maxRetries = 0
	}
	if o.timeout <= 0 {
		o.timeout = defaultTimeout
	}
	if o.maxBytes <= 0 {
		o.maxBytes = defaultMaxBytes
	}
	if o.minSkills <= 0 {
		o.minSkills = 1
	}
	if o.rotateDays <= 0 {
		o.rotateDays = defaultRotateDays
	}
	if o.now == nil {
		o.now = time.Now
	}
	if o.sleep == nil {
		o.sleep = sleepWithContext
	}
	if o.client == nil {
		o.client = &http.Client{Timeout: o.timeout}
	}
	return nil
}

type fetcher struct {
	opts       runOptions
	rateMu     sync.Mutex
	nextSlotAt time.Time
}

func newFetcher(opts runOptions) *fetcher {
	return &fetcher{opts: opts}
}

func robotsForbiddenPath(rawPath string) bool {
	p := strings.ToLower(strings.TrimSpace(rawPath))
	for _, prefix := range []string{"/api", "/search", "/internal", "/debug-security"} {
		if p == prefix || strings.HasPrefix(p, prefix+"/") {
			return true
		}
	}
	return false
}

func (f *fetcher) waitTurn(ctx context.Context) error {
	if f.opts.interval <= 0 {
		return nil
	}
	f.rateMu.Lock()
	now := f.opts.now()
	if f.nextSlotAt.After(now) {
		waitUntil := f.nextSlotAt
		f.nextSlotAt = waitUntil.Add(f.opts.interval)
		f.rateMu.Unlock()
		return f.opts.sleep(ctx, waitUntil.Sub(now))
	}
	f.nextSlotAt = now.Add(f.opts.interval)
	f.rateMu.Unlock()
	return nil
}

func (f *fetcher) getBytes(ctx context.Context, rawURL string, checkRobots bool) ([]byte, int, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return nil, 0, err
	}
	if checkRobots && robotsForbiddenPath(u.Path) {
		return nil, 0, fmt.Errorf("blocked by robots.txt rules: %s", u.Path)
	}

	attempts := f.opts.maxRetries + 1
	var lastErr error
	var lastStatus int
	for attempt := range attempts {
		if err := f.waitTurn(ctx); err != nil {
			return nil, 0, err
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
		if err != nil {
			return nil, 0, err
		}
		req.Header.Set("User-Agent", f.opts.userAgent)
		req.Header.Set("Accept", "text/html,application/json;q=0.9,*/*;q=0.8")

		resp, err := f.opts.client.Do(req)
		if err != nil {
			lastErr = err
			if attempt+1 < attempts && ctx.Err() == nil {
				if waitErr := f.opts.sleep(ctx, backoffDelay(attempt, f.opts.interval)); waitErr != nil {
					return nil, 0, waitErr
				}
				continue
			}
			return nil, 0, err
		}

		body, readErr := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
		resp.Body.Close()
		lastStatus = resp.StatusCode
		if readErr != nil {
			lastErr = readErr
			if attempt+1 < attempts && ctx.Err() == nil {
				if waitErr := f.opts.sleep(ctx, backoffDelay(attempt, f.opts.interval)); waitErr != nil {
					return nil, resp.StatusCode, waitErr
				}
				continue
			}
			return nil, resp.StatusCode, readErr
		}
		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			return body, resp.StatusCode, nil
		}

		lastErr = fmt.Errorf("HTTP %d from %s", resp.StatusCode, rawURL)
		retryable := resp.StatusCode == http.StatusTooManyRequests || (resp.StatusCode >= 500 && resp.StatusCode < 600)
		if !retryable || attempt+1 >= attempts {
			return nil, resp.StatusCode, lastErr
		}
		delay := retryAfterDuration(resp.Header.Get("Retry-After"), f.opts.now())
		if delay <= 0 {
			delay = backoffDelay(attempt, f.opts.interval)
		}
		if delay > 30*time.Second {
			delay = 30 * time.Second
		}
		if waitErr := f.opts.sleep(ctx, delay); waitErr != nil {
			return nil, resp.StatusCode, waitErr
		}
	}
	return nil, lastStatus, lastErr
}

func retryAfterDuration(val string, now time.Time) time.Duration {
	val = strings.TrimSpace(val)
	if val == "" {
		return 0
	}
	if secs, err := strconv.Atoi(val); err == nil && secs > 0 {
		return time.Duration(secs) * time.Second
	}
	if t, err := http.ParseTime(val); err == nil && t.After(now) {
		return t.Sub(now)
	}
	return 0
}

func backoffDelay(attempt int, interval time.Duration) time.Duration {
	base := interval * 2
	if base < 150*time.Millisecond {
		base = 150 * time.Millisecond
	}
	d := base << attempt
	if d > 8*time.Second {
		d = 8 * time.Second
	}
	return d
}

func sleepWithContext(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return ctx.Err()
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

func loadPrevExtras(ctx context.Context, f *fetcher, opts runOptions) (*library.MarketExtras, error) {
	var raw []byte
	if opts.prevPath != "" {
		b, err := os.ReadFile(opts.prevPath)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				return nil, nil
			}
			return nil, fmt.Errorf("read -prev %s: %w", opts.prevPath, err)
		}
		raw = b
	} else if opts.prevURL != "" {
		b, status, err := f.getBytes(ctx, opts.prevURL, false)
		if err != nil {
			if status == http.StatusNotFound {
				return nil, nil
			}
			return nil, fmt.Errorf("fetch -prev-url %s: %w", opts.prevURL, err)
		}
		raw = b
	} else {
		return nil, nil
	}
	if len(bytes.TrimSpace(raw)) == 0 {
		return nil, nil
	}
	var prev library.MarketExtras
	if err := json.Unmarshal(raw, &prev); err != nil {
		return nil, fmt.Errorf("decode previous market-extras: %w", err)
	}
	if prev.Descriptions == nil {
		prev.Descriptions = make(map[string]string)
	}
	return &prev, nil
}

func runSnapshot(ctx context.Context, opts runOptions) (runStats, error) {
	if err := opts.withDefaults(); err != nil {
		return runStats{}, err
	}
	started := opts.now()
	f := newFetcher(opts)

	prev, err := loadPrevExtras(ctx, f, opts)
	if err != nil {
		return runStats{}, err
	}

	// 1. 抓首页、趋势、热门三份 HTML 列表页，汇聚当前上榜技能 ID。
	candidateSet := make(map[string]bool)
	var candidateIDs []string
	addCandidate := func(id string) {
		if !candidateSet[id] {
			candidateSet[id] = true
			candidateIDs = append(candidateIDs, id)
		}
	}
	for _, pagePath := range []string{"/", "/trending", "/hot"} {
		body, _, err := f.getBytes(ctx, opts.baseURL+pagePath, true)
		if err != nil {
			return runStats{}, fmt.Errorf("fetch %s: %w", pagePath, err)
		}
		skills := extractSkillsFromListHTML(body)
		if len(skills) == 0 {
			return runStats{}, fmt.Errorf("no skills extracted from %s", pagePath)
		}
		for _, id := range skills {
			addCandidate(id)
		}
	}

	// 2. 抓 /topic 索引与全部专题详情页，拿到专题简介和每个技能的「能做什么」。
	topicIndexBody, _, err := f.getBytes(ctx, opts.baseURL+"/topic", true)
	if err != nil {
		return runStats{}, fmt.Errorf("fetch /topic: %w", err)
	}
	topics, err := extractTopicsIndexHTML(topicIndexBody)
	if err != nil {
		return runStats{}, err
	}

	topicResults := make([]library.MarketTopic, len(topics))
	topicErrs := make([]error, len(topics))
	runPool(ctx, len(topics), opts.concurrency, func(i int) {
		base := topics[i]
		body, _, err := f.getBytes(ctx, opts.baseURL+"/topic/"+base.ID, true)
		if err != nil {
			topicErrs[i] = fmt.Errorf("fetch /topic/%s: %w", base.ID, err)
			return
		}
		detail, err := extractTopicDetailHTML(base.ID, body)
		if err != nil {
			topicErrs[i] = fmt.Errorf("parse /topic/%s: %w", base.ID, err)
			return
		}
		if base.Title != "" {
			detail.Title = base.Title
		}
		if base.Summary != "" {
			detail.Summary = base.Summary
		}
		topicResults[i] = detail
	})
	for _, err := range topicErrs {
		if err != nil {
			return runStats{}, err
		}
	}
	for _, tp := range topicResults {
		for _, sk := range tp.Skills {
			addCandidate(sk.ID)
		}
	}

	// 3. 结合上一份补充文件决定本次需新抓或按 30 天轮换重取的技能清单。
	descriptions := make(map[string]string, len(candidateIDs))
	var prevDescs map[string]string
	if prev != nil {
		prevDescs = prev.Descriptions
	}
	dayBucket := 0
	if opts.rotateDays > 0 {
		days := opts.now().UTC().Unix() / 86400
		if days < 0 {
			days = 0
		}
		dayBucket = int(days % int64(opts.rotateDays))
	}

	var missingIDs []string
	var cachedCandidateIDs []string
	for _, id := range candidateIDs {
		if prevDescs != nil && prevDescs[id] != "" {
			cachedCandidateIDs = append(cachedCandidateIDs, id)
		} else {
			missingIDs = append(missingIDs, id)
		}
	}
	sort.Strings(cachedCandidateIDs)

	var refreshIDs []string
	var reusedCount int
	for idx, id := range cachedCandidateIDs {
		if opts.rotateDays > 0 && idx%opts.rotateDays == dayBucket {
			refreshIDs = append(refreshIDs, id)
			continue
		}
		descriptions[id] = prevDescs[id]
		reusedCount++
	}

	toFetch := append(missingIDs, refreshIDs...)
	if opts.limit > 0 && len(toFetch) > opts.limit {
		// 被 -limit 截断时，原本排在轮换队列但超出上限的旧简介仍继续保留。
		for _, id := range toFetch[opts.limit:] {
			if prevDescs != nil && prevDescs[id] != "" {
				descriptions[id] = prevDescs[id]
				reusedCount++
			}
		}
		toFetch = toFetch[:opts.limit]
	}

	// 4. 并发抓取技能页 <meta name="description">，受全局请求间隔与并发上限约束。
	fetchedOK := make([]string, len(toFetch))
	fetchedFailed := make([]bool, len(toFetch))
	runPool(ctx, len(toFetch), opts.concurrency, func(i int) {
		id := toFetch[i]
		body, _, err := f.getBytes(ctx, opts.baseURL+"/"+id, true)
		if err != nil {
			fetchedFailed[i] = true
			return
		}
		fetchedOK[i] = extractMetaDescription(body)
	})

	var stats runStats
	stats.CandidateSkills = len(candidateIDs)
	stats.Topics = len(topicResults)
	stats.Reused = reusedCount
	for i, id := range toFetch {
		if fetchedFailed[i] {
			stats.FetchFailed++
			if prevDescs != nil && prevDescs[id] != "" {
				descriptions[id] = prevDescs[id]
			}
			continue
		}
		if fetchedOK[i] != "" {
			descriptions[id] = fetchedOK[i]
			stats.Fetched++
		} else if prevDescs != nil && prevDescs[id] != "" {
			descriptions[id] = prevDescs[id]
		}
	}

	out := library.MarketExtras{
		Version:      1,
		GeneratedAt:  opts.now().UTC().Format(time.RFC3339),
		Descriptions: descriptions,
		Topics:       topicResults,
	}

	data, err := encodeAndValidateExtras(out, prev, opts)
	if err != nil {
		return stats, err
	}
	stats.Descriptions = len(out.Descriptions)
	stats.Bytes = len(data)
	stats.Duration = opts.now().Sub(started)

	if err := writeOutput(opts.outPath, data); err != nil {
		return stats, err
	}
	return stats, nil
}

func encodeAndValidateExtras(out library.MarketExtras, prev *library.MarketExtras, opts runOptions) ([]byte, error) {
	if len(out.Topics) == 0 {
		return nil, errors.New("validation failed: topics list is empty")
	}
	for _, tp := range out.Topics {
		if strings.TrimSpace(tp.ID) == "" || strings.TrimSpace(tp.Title) == "" || len(tp.Skills) == 0 {
			return nil, fmt.Errorf("validation failed: topic %q has empty metadata or skills", tp.ID)
		}
		for _, sk := range tp.Skills {
			if strings.TrimSpace(sk.ID) == "" {
				return nil, fmt.Errorf("validation failed: topic %q contains blank skill ID", tp.ID)
			}
		}
	}
	if len(out.Descriptions) == 0 {
		return nil, errors.New("validation failed: descriptions map is empty")
	}
	if len(out.Descriptions) < opts.minSkills {
		return nil, fmt.Errorf("validation failed: descriptions count %d below minimum %d", len(out.Descriptions), opts.minSkills)
	}
	if prev != nil && len(prev.Descriptions) > 0 && opts.limit == 0 && len(out.Descriptions)*2 < len(prev.Descriptions) {
		return nil, fmt.Errorf("validation failed: descriptions dropped from %d to %d (more than 50%% loss)", len(prev.Descriptions), len(out.Descriptions))
	}

	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(out); err != nil {
		return nil, fmt.Errorf("encode market-extras: %w", err)
	}
	data := buf.Bytes()
	if len(data) > opts.maxBytes {
		return nil, fmt.Errorf("validation failed: output size %d bytes exceeds limit %d bytes", len(data), opts.maxBytes)
	}
	var roundTrip library.MarketExtras
	if err := json.Unmarshal(data, &roundTrip); err != nil {
		return nil, fmt.Errorf("round-trip decode failed: %w", err)
	}
	return data, nil
}

func writeOutput(outPath string, data []byte) error {
	if outPath == "-" {
		_, err := os.Stdout.Write(data)
		return err
	}
	dir := filepath.Dir(outPath)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".marketsnap-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.Remove(tmpName)
		}
	}()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmpName, 0o644); err != nil {
		return err
	}
	if err := os.Rename(tmpName, outPath); err != nil {
		return err
	}
	cleanup = false
	return nil
}

func runPool(ctx context.Context, count, concurrency int, fn func(int)) {
	if count == 0 {
		return
	}
	if concurrency < 1 {
		concurrency = 1
	}
	if concurrency > count {
		concurrency = count
	}
	ch := make(chan int, count)
	for i := range count {
		ch <- i
	}
	close(ch)

	var wg sync.WaitGroup
	wg.Add(concurrency)
	for range concurrency {
		go func() {
			defer wg.Done()
			for i := range ch {
				if ctx.Err() != nil {
					return
				}
				fn(i)
			}
		}()
	}
	wg.Wait()
}
