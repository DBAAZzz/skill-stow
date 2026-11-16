package library

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	_ "image/gif"  // image.Decode
	_ "image/jpeg" // image.Decode
	"image/png"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	_ "golang.org/x/image/bmp"  // image.Decode：favicon 里的 BMP
	_ "golang.org/x/image/webp" // image.Decode：部分站点的图标
)

// IconFetcher 抓 well-known 来源网站（open.feishu.cn、agent.qq.com 这类）
// 的站点图标，归一化成 PNG 或 SVG 缓存在应用目录的 icons/ 下，文件名就是
// host，下次启动直接读，不再请求。
type IconFetcher struct {
	Client     *http.Client
	Dir        string // 缓存目录：<host>.png / <host>.svg
	AvatarBase string // GitHub 头像的基址，测试里指向假服务器；空为 https://github.com
}

const (
	iconRequestMax = 1 << 20    // 单个请求最多读 1MB
	iconHTMLMax    = 256 << 10  // 首页 HTML 最多读 256KB
	iconTimeout    = 15 * time.Second
)

// NewIconFetcher 按更新用的代理策略建抓取器，传输层和 NewUpdateRemote 一致。
func NewIconFetcher(s Settings, home string) *IconFetcher {
	tr := http.DefaultTransport.(*http.Transport).Clone()
	tr.Proxy = proxyFor(s.UpdateProxy, sync.OnceValue(systemProxy))
	tr.MaxConnsPerHost = 8
	switch s.UpdateProxyMode {
	case "env":
		tr.Proxy = http.ProxyFromEnvironment
	case "manual":
		tr.Proxy = nil
		if strings.TrimSpace(s.UpdateProxy) != "" {
			tr.Proxy = proxyFor(s.UpdateProxy, func() string { return "" })
		}
	}
	return &IconFetcher{
		Client: &http.Client{Transport: tr, Timeout: iconTimeout},
		Dir:    filepath.Join(appDir(home), "icons"),
	}
}

// Load 读缓存的图标；没有就返回 false，调用方再去 Fetch。
func (f *IconFetcher) Load(host string) ([]byte, bool) {
	if !iconHostOK(host) {
		return nil, false
	}
	for _, ext := range []string{".png", ".svg"} {
		if b, err := os.ReadFile(filepath.Join(f.Dir, host+ext)); err == nil && len(b) > 0 {
			return b, true
		}
	}
	return nil, false
}

// Fetch 按级联抓图标并缓存，成功返回缓存文件路径，失败时缓存不变。
// 顺序：首页 HTML 声明的（质量通常更好）→ /favicon.ico。
func (f *IconFetcher) Fetch(ctx context.Context, baseURL, host string) (string, error) {
	if !iconHostOK(host) {
		return "", fmt.Errorf("icon: source %q is not a host name", host)
	}
	if f.Dir == "" || f.Client == nil {
		return "", errors.New("icon: fetcher is not initialized")
	}
	base := strings.TrimSuffix(strings.TrimSpace(baseURL), "/")
	if base == "" {
		base = "https://" + host
	}
	u, err := url.Parse(base)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" {
		return "", fmt.Errorf("icon: invalid base URL %q", base)
	}

	var lastErr error
	for _, link := range f.iconLinks(ctx, u) {
		data, err := f.get(ctx, link.URL, iconRequestMax)
		if err != nil {
			lastErr = err
			continue
		}
		file, err := normalizeIcon(data)
		if err != nil {
			lastErr = err
			continue
		}
		return f.save(host, file)
	}
	data, err := f.get(ctx, u.String()+"/favicon.ico", iconRequestMax)
	if err == nil {
		var file iconFile
		if file, err = normalizeIcon(data); err == nil {
			return f.save(host, file)
		}
	}
	if lastErr == nil {
		lastErr = err
	}
	return "", lastErr
}

// iconLink 是 HTML 里声明的候选图标。
type iconLink struct {
	URL   string
	score int
}

// iconLinks 抓首页并提取 <link> 图标声明，按质量排序取前三。属性带不带
// 引号都认（agent.qq.com 的 HTML 就是没引号的）。
func (f *IconFetcher) iconLinks(ctx context.Context, base *url.URL) []iconLink {
	data, err := f.get(ctx, base.String(), iconHTMLMax)
	if err != nil {
		return nil
	}
	var links []iconLink
	for _, tag := range iconLinkTag.FindAllString(string(data), -1) {
		attrs := map[string]string{}
		for _, m := range iconLinkAttr.FindAllStringSubmatch(tag, -1) {
			if _, ok := attrs[strings.ToLower(m[1])]; !ok {
				attrs[strings.ToLower(m[1])] = strings.Trim(m[2], `"'`)
			}
		}
		rel, href := strings.ToLower(attrs["rel"]), attrs["href"]
		if href == "" || strings.HasPrefix(href, "data:") || !strings.Contains(rel, "icon") {
			continue
		}
		if strings.Contains(rel, "mask-icon") { // Safari 钉选页签的单色图标，不做界面图标
			continue
		}
		ref, err := url.Parse(href)
		if err != nil {
			continue
		}
		score := 0
		if strings.Contains(rel, "apple-touch-icon") {
			score += 10000
		}
		if s := strings.ToLower(attrs["sizes"]); s != "" && s != "any" {
			if n, err := strconv.Atoi(strings.SplitN(s, "x", 2)[0]); err == nil {
				score += n
			}
		}
		links = append(links, iconLink{URL: base.ResolveReference(ref).String(), score: score})
	}
	slices.SortFunc(links, func(a, b iconLink) int { return b.score - a.score })
	if len(links) > 3 {
		links = links[:3]
	}
	return links
}

var (
	iconLinkTag  = regexp.MustCompile(`(?i)<link\b[^>]*>`)
	iconLinkAttr = regexp.MustCompile(`(?i)([a-z-]+)\s*=\s*("[^"]*"|'[^']*'|[^\s>]+)`)
)

// get 按上限读一个 200 响应的 body。
func (f *IconFetcher) get(ctx context.Context, url string, limit int64) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "skill-manager")
	resp, err := f.Client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("icon: %s returned %d", url, resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("icon: %s is over %d bytes", url, limit)
	}
	if len(data) == 0 {
		return nil, fmt.Errorf("icon: %s is empty", url)
	}
	return data, nil
}

// iconFile 是归一化后的缓存文件：PNG 或 SVG 原文。
type iconFile struct {
	ext  string
	data []byte
}

// normalizeIcon 把抓到的数据归一化：光栅图统一重编码成 PNG，SVG 存原文，
// ICO 先解出最大条目再转 PNG；都解不了就报错。
func normalizeIcon(data []byte) (iconFile, error) {
	head := data
	if len(head) > 512 {
		head = head[:512]
	}
	if low := strings.ToLower(string(head)); bytes.Contains([]byte(low), []byte("<svg")) {
		return iconFile{ext: ".svg", data: data}, nil
	}
	if img, _, err := image.Decode(bytes.NewReader(data)); err == nil {
		return encodeIconPNG(img)
	}
	if img, err := decodeICO(data); err == nil {
		return encodeIconPNG(img)
	}
	return iconFile{}, errors.New("icon: data is not a recognized image")
}

// encodeIconPNG 把解出的图重新编码成 PNG，缓存里只存这一种光栅格式。
func encodeIconPNG(img image.Image) (iconFile, error) {
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return iconFile{}, err
	}
	return iconFile{ext: ".png", data: buf.Bytes()}, nil
}

// save 原子落盘：写临时文件再改名，坏数据不会留半个文件。name 可以带
// 一层子目录（如 github/<owner>）。
func (f *IconFetcher) save(name string, file iconFile) (string, error) {
	path := filepath.Join(f.Dir, name+file.ext)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", err
	}
	tmp, err := os.CreateTemp(f.Dir, ".icon-*")
	if err != nil {
		return "", err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName) // 已改名时是空操作
	if _, err := tmp.Write(file.data); err != nil {
		tmp.Close()
		return "", err
	}
	if err := tmp.Close(); err != nil {
		return "", err
	}
	if err := os.Rename(tmpName, path); err != nil {
		return "", err
	}
	return path, nil
}

// LoadAvatar 读缓存的 GitHub 用户头像（github/<owner>.png）；没有就返回
// false，调用方再去 FetchAvatar。
func (f *IconFetcher) LoadAvatar(owner string) ([]byte, bool) {
	if !iconNameOK(owner) {
		return nil, false
	}
	if b, err := os.ReadFile(filepath.Join(f.Dir, "github", owner+".png")); err == nil && len(b) > 0 {
		return b, true
	}
	return nil, false
}

// FetchAvatar 抓 GitHub 用户头像（github.com/<owner>.png?size=96，不占
// API 配额）并缓存为 PNG。头像变更很少，缓存不设过期。
func (f *IconFetcher) FetchAvatar(ctx context.Context, owner string) (string, error) {
	if !iconNameOK(owner) {
		return "", fmt.Errorf("avatar: %q is not a GitHub user name", owner)
	}
	if f.Dir == "" || f.Client == nil {
		return "", errors.New("avatar: fetcher is not initialized")
	}
	base := strings.TrimSuffix(f.AvatarBase, "/")
	if base == "" {
		base = "https://github.com"
	}
	data, err := f.get(ctx, base+"/"+owner+".png?size=96", iconRequestMax)
	if err != nil {
		return "", err
	}
	file, err := normalizeIcon(data)
	if err != nil {
		return "", err
	}
	if file.ext != ".png" {
		return "", errors.New("avatar: not a recognized bitmap")
	}
	return f.save("github/"+owner, file)
}

// iconNameOK 只认干净的缓存文件名（host 或 GitHub 用户名），防目录跳转。
func iconNameOK(name string) bool {
	if name == "" || len(name) > 253 {
		return false
	}
	if strings.ContainsAny(name, "/\\") || strings.Contains(name, "..") {
		return false
	}
	return !strings.ContainsFunc(name, func(r rune) bool {
		return !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '.' || r == '-' || r == '_')
	})
}

// iconHostOK 只认干净的主机名，防把缓存路径拼出目录跳转。
func iconHostOK(host string) bool {
	return iconNameOK(host)
}
