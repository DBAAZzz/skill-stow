package library

import (
	"context"
	"encoding/json"
	"errors"

	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"

	"skillmanager/locales"
)

// GitHub 上游：查提交、目录树，按提交下载单个文件。提交和目录树走 git 协议
// （gitproto.go），不占 api.github.com 的请求额度；默认匿名，设置里勾选
// 「使用本机 gh 登录状态」后才读取 gh 登录凭证，并按目录区分提交（唯一还要
// 调 API 的地方）。检查和对比、更新都固定在查到的提交上，不再用会变的 HEAD。

// Commit 是查到的一个提交。
type Commit struct {
	SHA  string
	Tree string // 根目录的 tree 哈希（不是提交 ID）
	Date time.Time
}

// TreeEntry 是目录树里的一项，Path 相对仓库根。
type TreeEntry struct {
	Path string
	Mode string // 100644、100755、120000（链接）、160000（子模块）、040000
	Type string // blob、tree、commit
	SHA  string
	Size int64 // 文件字节数；git 协议不带文件大小，下载前为 -1
}

// Tree 是一个提交的完整目录树。Truncated 时不完整，不能据此说上游删了什么。
type Tree struct {
	Entries   []TreeEntry
	Truncated bool
}

// Remote 是技能的上游。测试里换成假的。
type Remote interface {
	// Commit 查 ref（空为默认分支）或一个提交 ID 对应的提交。
	Commit(ctx context.Context, repo, ref string) (Commit, error)
	// Tree 是这个提交的完整目录树。
	Tree(ctx context.Context, repo, commit string) (Tree, error)
	// File 是这个提交里一个文件的原始字节。
	File(ctx context.Context, repo, commit, path string) ([]byte, error)
}

// PathCommitter 查某个子目录最近一次发生变动的提交。
type PathCommitter interface {
	PathCommit(ctx context.Context, repo, ref, subpath string) (Commit, error)
}

// RefLister 列出仓库里以 prefix 开头的分支名和标签名。
type RefLister interface {
	RefNames(ctx context.Context, repo, prefix string) ([]string, error)
}

// AuthRemote 报告当前上游客户端是否带有登录凭证。
type AuthRemote interface {
	Authenticated() bool
}

// RemoteKind 是上游请求失败的种类。
type RemoteKind int

const (
	RemoteOther     RemoteKind = iota
	RemoteQuota                // 额度用完，Reset 后能再查
	RemoteThrottled            // 被限流，等过一次仍不行
	RemoteNotFound             // 404：仓库删了、转私有，或文件不在
	RemoteNetwork              // DNS、超时、连不上
	RemoteAuth                 // 401：token 无效或过期
)

// RemoteError 是上游请求失败，Error() 是给人看的说法。
type RemoteError struct {
	Kind   RemoteKind
	Reset  time.Time // RemoteQuota：额度恢复时间
	Status int
	Err    error
}

func (e *RemoteError) Error() string { return e.note().String() }

// note 是这种失败的说明，存进检查结果后切换语言也跟着换。
func (e *RemoteError) note() Note {
	switch e.Kind {
	case RemoteQuota:
		return Note{Kind: noteGitHubQuota, Reset: e.Reset}
	case RemoteThrottled:
		return note(noteGitHubThrottled)
	case RemoteNotFound:
		return note(noteGitHubNotFound)
	case RemoteNetwork:
		return note(noteGitHubNetwork)
	case RemoteAuth:
		return note(noteGitHubAuth)
	}
	if e.Status != 0 {
		return Note{Kind: noteGitHubStatus, Status: e.Status}
	}
	return note(noteGitHubInvalidReply)
}

func (e *RemoteError) Unwrap() error { return e.Err }

// GitHub 是 github.com 的 git 端点 + raw.githubusercontent.com（+ codeload.github.com，
// + 有凭证时的 api.github.com）。
type GitHub struct {
	API, Raw string
	Git      string // git 端点，空为 https://github.com
	Codeload string // 压缩包下载，空为 codeload.github.com
	Token    string
	Client   *http.Client
	MaxWait  time.Duration // 限流时最多等多久再重试
	sleep    func(ctx context.Context, d time.Duration) error
}

func (g *GitHub) Authenticated() bool { return strings.TrimSpace(g.Token) != "" }

// requestTimeout 是单个请求的超时。
const requestTimeout = 30 * time.Second

// NewGitHub 建一个上游客户端。proxy 是设置里手动填的代理，空时用 macOS
// 的系统代理，再没有时用 HTTPS_PROXY 等环境变量。
func NewGitHub(proxy string) *GitHub {
	tr := http.DefaultTransport.(*http.Transport).Clone()
	tr.Proxy = proxyFor(proxy, sync.OnceValue(systemProxy))
	tr.MaxConnsPerHost = 8
	return &GitHub{
		API:     "https://api.github.com",
		Raw:     "https://raw.githubusercontent.com",
		Client:  &http.Client{Transport: tr, Timeout: requestTimeout},
		MaxWait: time.Minute,
		sleep: func(ctx context.Context, d time.Duration) error {
			t := time.NewTimer(d)
			defer t.Stop()
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-t.C:
				return nil
			}
		},
	}
}

// proxyFor 选代理：手动填的，其次系统的，再其次环境变量。
func proxyFor(manual string, system func() string) func(*http.Request) (*url.URL, error) {
	manual = strings.TrimSpace(manual)
	parse := func(s string) (*url.URL, error) {
		if !strings.Contains(s, "://") {
			s = "http://" + s
		}
		return url.Parse(s)
	}
	return func(r *http.Request) (*url.URL, error) {
		if h := r.URL.Hostname(); h == "localhost" || net.ParseIP(h).IsLoopback() {
			return nil, nil
		}
		if manual != "" {
			return parse(manual)
		}
		if s := system(); s != "" {
			return parse(s)
		}
		return http.ProxyFromEnvironment(r)
	}
}

// parseScutilProxy 从 scutil --proxy 的输出里取 HTTPS 代理（没开时取 HTTP 的），
// 如 http://127.0.0.1:7890；都没开为空。PAC 自动配置不认。
func parseScutilProxy(out string) string {
	f := map[string]string{}
	for _, line := range strings.Split(out, "\n") {
		k, v, ok := strings.Cut(strings.TrimSpace(line), " : ")
		if ok {
			f[k] = strings.TrimSpace(v)
		}
	}
	for _, p := range []string{"HTTPS", "HTTP"} {
		if f[p+"Enable"] == "1" && f[p+"Proxy"] != "" {
			host := f[p+"Proxy"]
			if port := f[p+"Port"]; port != "" {
				host = net.JoinHostPort(host, port)
			}
			return "http://" + host
		}
	}
	return ""
}

type ghCommitPayload struct {
	SHA    string `json:"sha"`
	Commit struct {
		Tree struct {
			SHA string `json:"sha"`
		} `json:"tree"`
		Author struct {
			Date time.Time `json:"date"`
		} `json:"author"`
		Committer struct {
			Date time.Time `json:"date"`
		} `json:"committer"`
	} `json:"commit"`
}

func (p ghCommitPayload) toCommit() Commit {
	date := p.Commit.Committer.Date
	if date.IsZero() {
		date = p.Commit.Author.Date
	}
	return Commit{SHA: p.SHA, Tree: p.Commit.Tree.SHA, Date: date}
}

// apiCommit 用 API 查 ref 对应的提交。只给 git 协议认不出的简写提交 ID 用。
func (g *GitHub) apiCommit(ctx context.Context, repo, ref string) (Commit, error) {
	var body ghCommitPayload
	if err := g.getJSON(ctx, g.API+"/repos/"+repo+"/commits/"+url.PathEscape(ref), &body); err != nil {
		return Commit{}, err
	}
	if body.SHA == "" || body.Commit.Tree.SHA == "" {
		return Commit{}, &RemoteError{Kind: RemoteOther}
	}
	return body.toCommit(), nil
}

func (g *GitHub) PathCommit(ctx context.Context, repo, ref, subpath string) (Commit, error) {
	if subpath == "" {
		return g.Commit(ctx, repo, ref)
	}
	q := url.Values{}
	q.Set("path", subpath)
	q.Set("per_page", "1")
	if ref != "" {
		q.Set("sha", ref)
	}
	var body []ghCommitPayload
	if err := g.getJSON(ctx, g.API+"/repos/"+repo+"/commits?"+q.Encode(), &body); err != nil {
		return Commit{}, err
	}
	if len(body) == 0 || body[0].SHA == "" {
		return Commit{}, &RemoteError{Kind: RemoteOther}
	}
	return body[0].toCommit(), nil
}

// maxFileBytes 是单个下载文件的上限（50 MB，低于 GitHub 单文件告警线，避免 4 并发下载时
// 单文件占满内存），maxSkillBytes 是整个技能所有文件总大小的上限（100 MB，与
// validTreeFiles 的上限提示一致）。
const (
	maxFileBytes  = 50 << 20
	maxSkillBytes = 100 << 20
)

func (g *GitHub) File(ctx context.Context, repo, commit, p string) ([]byte, error) {
	var segs []string
	for _, s := range strings.Split(p, "/") {
		segs = append(segs, url.PathEscape(s))
	}
	resp, err := g.get(ctx, g.Raw+"/"+repo+"/"+commit+"/"+strings.Join(segs, "/"), "")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, maxFileBytes+1))
	if err != nil {
		return nil, g.wrap(ctx, err)
	}
	return b, nil
}

func (g *GitHub) getJSON(ctx context.Context, u string, v any) error {
	resp, err := g.get(ctx, u, "application/vnd.github+json")
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if err := json.NewDecoder(resp.Body).Decode(v); err != nil {
		return g.wrap(ctx, err)
	}
	return nil
}

// get 发一个 GET。限流时按 Retry-After 等一次再试；额度用完直接报恢复时间。
func (g *GitHub) get(ctx context.Context, u, accept string) (*http.Response, error) {
	return g.send(ctx, func() (*http.Request, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
		if err != nil {
			return nil, err
		}
		if tok := strings.TrimSpace(g.Token); tok != "" {
			req.Header.Set("Authorization", "Bearer "+tok)
		}
		if accept != "" {
			req.Header.Set("Accept", accept)
			req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
		}
		return req, nil
	})
}

// send 发 newReq 建的请求，按状态码归类失败。限流时按 Retry-After 等一次再试
// （所以每次重试都重新建请求）；额度用完直接报恢复时间。
func (g *GitHub) send(ctx context.Context, newReq func() (*http.Request, error)) (*http.Response, error) {
	for attempt := 0; ; attempt++ {
		req, err := newReq()
		if err != nil {
			return nil, err
		}
		req.Header.Set("User-Agent", "skill-manager")
		resp, err := g.Client.Do(req)
		if err != nil {
			return nil, g.wrap(ctx, err)
		}
		switch s := resp.StatusCode; {
		case s == http.StatusOK:
			return resp, nil
		case s == http.StatusUnauthorized:
			resp.Body.Close()
			return nil, &RemoteError{Kind: RemoteAuth, Status: s}
		case s == http.StatusBadGateway || s == http.StatusGatewayTimeout:
			// 代理连不上 GitHub 时回 502 / 504
			resp.Body.Close()
			return nil, &RemoteError{Kind: RemoteNetwork, Status: s}
		case s == http.StatusNotFound:
			resp.Body.Close()
			return nil, &RemoteError{Kind: RemoteNotFound, Status: s}
		case s == http.StatusForbidden || s == http.StatusTooManyRequests:
			resp.Body.Close()
			if resp.Header.Get("X-RateLimit-Remaining") == "0" {
				return nil, &RemoteError{Kind: RemoteQuota, Status: s, Reset: resetTime(resp.Header)}
			}
			if attempt > 0 {
				return nil, &RemoteError{Kind: RemoteThrottled, Status: s}
			}
			wait := time.Minute
			if n, err := strconv.Atoi(resp.Header.Get("Retry-After")); err == nil {
				wait = time.Duration(n) * time.Second
			}
			if wait > g.MaxWait {
				return nil, &RemoteError{Kind: RemoteThrottled, Status: s}
			}
			if err := g.sleep(ctx, wait); err != nil {
				return nil, err
			}
		default:
			resp.Body.Close()
			return nil, &RemoteError{Kind: RemoteOther, Status: s}
		}
	}
}

// resetTime 是额度恢复的本机时间：按服务端 Date 修正两边时钟的差。
func resetTime(h http.Header) time.Time {
	n, err := strconv.ParseInt(h.Get("X-RateLimit-Reset"), 10, 64)
	if err != nil {
		return time.Now().Add(time.Hour)
	}
	reset := time.Unix(n, 0)
	if d, err := http.ParseTime(h.Get("Date")); err == nil {
		reset = time.Now().Add(reset.Sub(d))
	}
	return reset
}

// wrap 把网络错误归成「连不上 GitHub」；取消原样返回，不算检查失败。
func (g *GitHub) wrap(ctx context.Context, err error) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if errors.Is(err, context.Canceled) {
		return err
	}
	var ne net.Error
	var ue *url.Error
	if errors.As(err, &ne) || errors.As(err, &ue) || errors.Is(err, io.ErrUnexpectedEOF) {
		return &RemoteError{Kind: RemoteNetwork, Err: err}
	}
	return &RemoteError{Kind: RemoteOther, Err: err}
}

var ghTokenFunc = readGHToken

// ProbeGHAuth 检查本机 gh 登录是否可用（仅供设置了「使用本机 gh 登录状态」时反馈状态）。
func ProbeGHAuth() (bool, string) {
	tok, msg := ghTokenFunc()
	if tok != "" {
		return true, ""
	}
	if msg == "" {
		msg = locales.UpdateGithubLoginInvalid()
	}
	return false, msg
}

func readGHToken() (string, string) {
	bin := findGHBinary()
	if bin == "" {
		return "", locales.UpdateGithubCliMissing()
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, bin, "auth", "token").Output()
	tok := strings.TrimSpace(string(out))
	if err != nil || tok == "" {
		return "", locales.UpdateGithubLoginRequired()
	}
	return tok, ""
}

func findGHBinary() string {
	if p, err := exec.LookPath("gh"); err == nil {
		return p
	}
	for _, p := range []string{"/opt/homebrew/bin/gh", "/usr/local/bin/gh"} {
		if fi, err := os.Stat(p); err == nil && !fi.IsDir() && fi.Mode()&0o111 != 0 {
			return p
		}
	}
	return ""
}

// NewUpdateRemote 根据用户已选择的策略创建客户端；仅在 UseGHAuth 开启时读取 gh 登录凭证。
func NewUpdateRemote(s Settings) *GitHub {
	g := NewGitHub(s.UpdateProxy)
	tr := g.Client.Transport.(*http.Transport)
	switch s.UpdateProxyMode {
	case "env":
		tr.Proxy = http.ProxyFromEnvironment
	case "manual":
		tr.Proxy = nil
		if strings.TrimSpace(s.UpdateProxy) != "" {
			tr.Proxy = proxyFor(s.UpdateProxy, func() string { return "" })
		}
	}
	if s.UseGHAuth {
		g.Token, _ = ghTokenFunc()
	}
	return g
}
