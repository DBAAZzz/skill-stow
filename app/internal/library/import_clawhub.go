package library

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"skillmanager/locales"
)

const (
	defaultClawHubBase = "https://clawhub.ai"
	clawHubMaxWait     = 5 * time.Second
)

// ClawHub downloads hosted skill archives from clawhub.ai.
type ClawHub struct {
	Base    string
	Client  *http.Client
	MaxWait time.Duration
	sleep   func(ctx context.Context, d time.Duration) error
}

// ReadClawHubImport downloads and validates a ClawHub skill package into staging.
// A skill that ClawHub serves from a public GitHub repository is read from that
// repository through gh instead, so it is recorded as a GitHub source and takes
// part in skill updates; gh may be nil only when no such skill is expected.
func ReadClawHubImport(ctx context.Context, home string, client *http.Client, gh ImportRemote, src ImportSource, progress func(ImportProgress)) (*ImportBatch, error) {
	c := &ClawHub{
		Base:    defaultClawHubBase,
		Client:  client,
		MaxWait: clawHubMaxWait,
	}
	b, err := c.ReadImport(ctx, home, src, progress)
	var backed *GitHubBackedSkill
	if errors.As(err, &backed) && gh != nil {
		return ReadGitHubImport(ctx, home, gh, backed.Source, progress)
	}
	return b, err
}

// GitHubBackedSkill is returned by ClawHub.ReadImport for a skill whose files
// ClawHub does not host: Source is the GitHub source to read it from instead,
// following the default branch so later updates come from GitHub.
type GitHubBackedSkill struct{ Source ImportSource }

func (e *GitHubBackedSkill) Error() string { return locales.ImportUnsupportedSource() }

// githubBackedSource turns ClawHub's public-github handoff into a GitHub
// source; ok is false when the repository or path cannot be used safely.
func githubBackedSource(repo, dir string) (ImportSource, bool) {
	owner, name, ok := strings.Cut(strings.Trim(repo, "/"), "/")
	name = strings.TrimSuffix(name, ".git")
	if !ok || !ghOwner.MatchString(owner) || !ghRepo.MatchString(name) || name == "." || name == ".." {
		return ImportSource{}, false
	}
	sub := strings.Trim(dir, "/")
	if strings.EqualFold(path.Base(sub), "SKILL.md") {
		sub = path.Dir(sub)
	}
	switch sub = path.Clean(sub); {
	case sub == ".":
		sub = ""
	case sub == ".." || strings.HasPrefix(sub, "../"):
		return ImportSource{}, false
	}
	repo = owner + "/" + name
	return ImportSource{Kind: ImportGitHub, Input: "https://github.com/" + repo, Repo: repo, Subpath: sub}, true
}

// ReadImport downloads a ClawHub skill archive via /api/v1/download?slug=&ownerHandle=,
// extracts it safely, and returns a validated ImportBatch ready for selection.
func (c *ClawHub) ReadImport(ctx context.Context, home string, src ImportSource, progress func(ImportProgress)) (b *ImportBatch, err error) {
	defer trackIn(home, "import", "read-clawhub", src.Repo)(&err)

	owner, slug, ok := strings.Cut(src.Repo, "/")
	if !ok || !validClawHubIdent(owner) || !validClawHubIdent(slug) {
		return nil, &ImportError{Msg: locales.ImportUnknownSource()}
	}
	report := func(p ImportProgress) {
		if progress != nil {
			progress(p)
		}
	}
	report(ImportProgress{Total: 1})

	base := strings.TrimRight(c.Base, "/")
	if base == "" {
		base = defaultClawHubBase
	}
	q := url.Values{}
	q.Set("slug", slug)
	q.Set("ownerHandle", owner)
	dlURL := base + "/api/v1/download?" + q.Encode()

	data, contentType, err := c.fetchDownload(ctx, dlURL)
	if err != nil {
		return nil, clawHubImportError(ctx, err)
	}
	if len(data) > maxArchiveBytes {
		return nil, &ImportError{Msg: locales.ImportArchiveDownloadTooLarge()}
	}
	if !bytes.HasPrefix(data, []byte("PK")) {
		if strings.Contains(strings.ToLower(contentType), "json") || bytes.HasPrefix(bytes.TrimSpace(data), []byte("{")) {
			var handoff struct {
				SourceRef string `json:"sourceRef"`
				Repo      string `json:"repo"`
				Path      string `json:"path"`
			}
			if json.Unmarshal(data, &handoff) == nil && handoff.SourceRef == "public-github" && handoff.Repo != "" {
				if gh, ok := githubBackedSource(handoff.Repo, handoff.Path); ok {
					return nil, &GitHubBackedSkill{Source: gh}
				}
				return nil, &ImportError{Msg: locales.ImportUnsupportedSource(), Hint: handoff.Repo}
			}
		}
		return nil, &ImportError{Msg: locales.ImportUnknownArchiveFormat()}
	}

	b, err = newBatch(home, src)
	if err != nil {
		return nil, err
	}
	defer func() {
		if err != nil {
			b.Discard()
			b = nil
		}
	}()

	canonicalURL := src.URL
	if canonicalURL == "" {
		canonicalURL = defaultClawHubBase + "/" + owner + "/skills/" + slug
	}
	b.Site = "clawhub.ai"
	b.BaseURL = canonicalURL

	zipFile, err := os.CreateTemp(b.Dir, "clawhub-*.zip")
	if err != nil {
		return nil, err
	}
	zipPath := zipFile.Name()
	defer os.Remove(zipPath)
	if _, err := zipFile.Write(data); err != nil {
		zipFile.Close()
		return nil, err
	}
	if err := zipFile.Close(); err != nil {
		return nil, err
	}

	raw := filepath.Join(b.Dir, "raw")
	defer os.RemoveAll(raw)
	if err := unzip(zipPath, raw); err != nil {
		return nil, &ImportError{Msg: locales.ImportArchiveFailed(err.Error()), Err: err}
	}
	if !exists(filepath.Join(raw, "SKILL.md")) {
		return nil, &ImportError{Msg: locales.ImportNoSkillMd(slug), Hint: locales.ImportSkillFolderHint()}
	}

	sk := &ImportSkill{
		Name:      slug,
		Folder:    slug,
		Dir:       filepath.Join(b.Dir, "0"),
		SourceURL: canonicalURL,
		Digest:    sha256Digest(data),
	}
	if err := copySkillDir(raw, sk); err != nil {
		return nil, clawHubImportError(ctx, err)
	}
	// Preserve ClawHub's slug as the install name even if SKILL.md frontmatter
	// declares an uppercase display name (e.g. "SkillScan" for slug "skillscan").
	if sk.Name != slug {
		nameOnlyProblem := sk.Problem != "" && sk.Problem == nameProblem(sk.Name)
		sk.Name = slug
		if nameOnlyProblem {
			sk.Problem = nameProblem(slug)
			if sk.Problem == "" {
				if sk.Tree, err = TreeHash(sk.Dir); err != nil {
					sk.Problem = locales.ImportUnreadable(err.Error())
				}
			}
		}
	}
	b.Skills = []*ImportSkill{sk}
	b.Bytes = int64(len(data))
	report(ImportProgress{Bytes: b.Bytes, Found: []string{sk.Name}, Done: 1, Total: 1})
	return b, nil
}

func (c *ClawHub) fetchDownload(ctx context.Context, rawURL string) ([]byte, string, error) {
	client := c.Client
	if client == nil {
		client = &http.Client{Timeout: requestTimeout}
	}
	maxWait := c.MaxWait
	if maxWait == 0 {
		maxWait = clawHubMaxWait
	}
	sleepFn := c.sleep
	if sleepFn == nil {
		sleepFn = sleepContext
	}
	resp, err := doWithRetry(ctx, client, maxWait, sleepFn, func() (*http.Request, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("User-Agent", "skill-manager")
		return req, nil
	})
	if err != nil {
		return nil, "", err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxArchiveBytes+1))
	if err != nil {
		return nil, "", err
	}
	return data, resp.Header.Get("Content-Type"), nil
}

func clawHubImportError(ctx context.Context, err error) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	var ie *ImportError
	if errors.As(err, &ie) {
		return ie
	}
	var se *httpStatusError
	if errors.As(err, &se) {
		if se.Code == http.StatusTooManyRequests {
			return &ImportError{Msg: locales.ImportRemoteError(se.Error()), Hint: locales.ImportRetryLater(), Err: err}
		}
		return &ImportError{Msg: locales.ImportReadFailed(se.Error()), Err: err}
	}
	var ne net.Error
	var ue *url.Error
	if errors.As(err, &ne) || errors.As(err, &ue) || errors.Is(err, context.DeadlineExceeded) {
		return &ImportError{
			Msg:     locales.ImportCantConnect("clawhub.ai"),
			Hint:    locales.ImportCheckNetworkProxy(),
			Network: true,
			Err:     err,
		}
	}
	return &ImportError{Msg: locales.ImportReadFailed(err.Error()), Err: err}
}

type httpStatusError struct {
	Code int
	Body string
}

func (e *httpStatusError) Error() string {
	if e.Body != "" {
		return fmt.Sprintf("HTTP %d: %s", e.Code, e.Body)
	}
	return fmt.Sprintf("HTTP %d", e.Code)
}

func doWithRetry(
	ctx context.Context,
	client *http.Client,
	maxWait time.Duration,
	sleep func(context.Context, time.Duration) error,
	newReq func() (*http.Request, error),
) (*http.Response, error) {
	for attempt := 0; ; attempt++ {
		req, err := newReq()
		if err != nil {
			return nil, err
		}
		resp, err := client.Do(req)
		if err != nil {
			return nil, err
		}
		if resp.StatusCode == http.StatusOK || resp.StatusCode == http.StatusNotModified {
			return resp, nil
		}
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		resp.Body.Close()
		if (resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode == http.StatusServiceUnavailable) && attempt == 0 {
			if wait, ok := retryDelay(resp.Header, time.Now()); ok && wait > 0 && wait <= maxWait {
				if err := sleep(ctx, wait); err != nil {
					return nil, err
				}
				continue
			}
		}
		return nil, &httpStatusError{Code: resp.StatusCode, Body: strings.TrimSpace(string(body))}
	}
}

func retryDelay(h http.Header, now time.Time) (time.Duration, bool) {
	if raw := strings.TrimSpace(h.Get("Retry-After")); raw != "" {
		if secs, err := strconv.Atoi(raw); err == nil {
			return time.Duration(secs) * time.Second, true
		}
		if t, err := http.ParseTime(raw); err == nil {
			return max(t.Sub(now), 0), true
		}
	}
	if raw := strings.TrimSpace(h.Get("RateLimit-Reset")); raw != "" {
		if secs, err := strconv.Atoi(raw); err == nil {
			return time.Duration(secs) * time.Second, true
		}
	}
	if raw := strings.TrimSpace(h.Get("X-RateLimit-Reset")); raw != "" {
		if epoch, err := strconv.ParseInt(raw, 10, 64); err == nil {
			return max(time.Unix(epoch, 0).Sub(now), 0), true
		}
	}
	return 0, false
}

func sleepContext(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}
