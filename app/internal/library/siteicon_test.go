package library

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

// mustPNG 造一张 w×h 的纯色 PNG。
func mustPNG(t *testing.T, w, h int, c color.NRGBA) []byte {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for i := 0; i < len(img.Pix); i += 4 {
		img.Pix[i], img.Pix[i+1], img.Pix[i+2], img.Pix[i+3] = c.R, c.G, c.B, c.A
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// iconServer 是 favicon 抓取的假站点，hits 记每个路径被请求的次数。
type iconServer struct {
	srv  *httptest.Server
	hits map[string]int
}

// handler 返回 false 按没这个路径算（404）。baseURL 是测试服务器的根地址。
func newIconServer(t *testing.T, handler func(baseURL, path string, w http.ResponseWriter, r *http.Request) bool) *iconServer {
	t.Helper()
	s := &iconServer{hits: map[string]int{}}
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		s.hits[r.URL.Path]++
		if !handler(s.srv.URL, r.URL.Path, w, r) {
			http.NotFound(w, r)
		}
	})
	s.srv = httptest.NewServer(mux)
	t.Cleanup(s.srv.Close)
	return s
}

func pngDims(t *testing.T, data []byte) (int, int) {
	t.Helper()
	img, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("png.Decode: %v", err)
	}
	return img.Bounds().Dx(), img.Bounds().Dy()
}

// agent.qq.com 式的首页：属性没有引号，声明多个尺寸的 PNG（大的用绝对
// 地址，像它挂 CDN 那样）。
func TestFetchPicksLargestDeclaredIcon(t *testing.T) {
	s := newIconServer(t, func(baseURL, path string, w http.ResponseWriter, r *http.Request) bool {
		switch path {
		case "/":
			w.Write([]byte(`<html><head><title>t</title>` +
				`<link rel=icon type=image/png sizes=16x16 href=/f16.png>` +
				`<link rel=icon type=image/png sizes=32x32 href=/f32.png>` +
				`<link rel="icon" sizes="96x96" href="` + baseURL + `/f96.png">` +
				`</head></html>`))
			return true
		case "/f96.png":
			w.Write(mustPNG(t, 96, 96, color.NRGBA{B: 255, A: 255}))
			return true
		}
		return false
	})
	f := &IconFetcher{Client: s.srv.Client(), Dir: t.TempDir()}

	path, err := f.Fetch(context.Background(), s.srv.URL, "example.test")
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if s.hits["/f16.png"]+s.hits["/f32.png"] != 0 {
		t.Errorf("小尺寸图标不该被请求，hits = %v", s.hits)
	}
	if s.hits["/f96.png"] != 1 {
		t.Errorf("f96 请求数 = %d, want 1", s.hits["/f96.png"])
	}
	if filepath.Base(path) != "example.test.png" {
		t.Errorf("缓存名 = %s", path)
	}
	if w, h := pngDims(t, mustRead(t, path)); w != 96 || h != 96 {
		t.Errorf("缓存图 = %dx%d, want 96x96", w, h)
	}
	// 缓存读回来一致；Load 不依赖 Client，下次启动直接复用。
	cached, ok := f.Load("example.test")
	if !ok {
		t.Fatal("Load: 缓存没读到")
	}
	if w, h := pngDims(t, cached); w != 96 {
		t.Errorf("缓存读回 = %dx%d", w, h)
	}
}

// apple-touch-icon 通常最大，优先于普通 icon。
func TestFetchPrefersAppleTouchIcon(t *testing.T) {
	s := newIconServer(t, func(baseURL, path string, w http.ResponseWriter, r *http.Request) bool {
		switch path {
		case "/":
			w.Write([]byte(`<link rel='icon' href='/f96.png' sizes='96x96'>` +
				`<link rel="apple-touch-icon" sizes="180x180" href="/touch.png">`))
			return true
		case "/touch.png":
			w.Write(mustPNG(t, 180, 180, color.NRGBA{G: 255, A: 255}))
			return true
		}
		return false
	})
	f := &IconFetcher{Client: s.srv.Client(), Dir: t.TempDir()}
	if _, err := f.Fetch(context.Background(), s.srv.URL, "example.test"); err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if s.hits["/f96.png"] != 0 || s.hits["/touch.png"] != 1 {
		t.Errorf("hits = %v，该只抓 touch.png", s.hits)
	}
}

// 首页抓不到（open.feishu.cn 反爬只认浏览器）时退回 /favicon.ico，
// BMP 格式的 ICO 也能解出来转成 PNG。
func TestFetchFallsBackToFaviconICO(t *testing.T) {
	ico, err := os.ReadFile("testdata/feishu.ico")
	if err != nil {
		t.Fatal(err)
	}
	s := newIconServer(t, func(baseURL, path string, w http.ResponseWriter, r *http.Request) bool {
		if path == "/favicon.ico" {
			w.Write(ico)
			return true
		}
		return false // 首页 404
	})
	f := &IconFetcher{Client: s.srv.Client(), Dir: t.TempDir()}

	path, err := f.Fetch(context.Background(), s.srv.URL, "example.test")
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if s.hits["/"] != 1 {
		t.Errorf("首页请求数 = %d, want 1", s.hits["/"])
	}
	if w, h := pngDims(t, mustRead(t, path)); w != 48 || h != 48 {
		t.Errorf("缓存图 = %dx%d, want 48x48", w, h)
	}
}

// 候选一个个试：第一个解不开就换下一个，都不行再退 favicon.ico。
func TestFetchSkipsBrokenCandidates(t *testing.T) {
	s := newIconServer(t, func(baseURL, path string, w http.ResponseWriter, r *http.Request) bool {
		switch path {
		case "/":
			w.Write([]byte(`<link rel="icon" href="/bad.png"><link rel="icon" href="/good.png">`))
			return true
		case "/bad.png":
			w.Write([]byte("这根本不是图片"))
			return true
		case "/good.png":
			w.Write(mustPNG(t, 32, 32, color.NRGBA{R: 255, A: 255}))
			return true
		}
		return false
	})
	f := &IconFetcher{Client: s.srv.Client(), Dir: t.TempDir()}
	path, err := f.Fetch(context.Background(), s.srv.URL, "example.test")
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if w, _ := pngDims(t, mustRead(t, path)); w != 32 {
		t.Errorf("缓存图宽 = %d, want 32", w)
	}
}

// 站点只提供 SVG 图标时存原文。
func TestFetchSVGIcon(t *testing.T) {
	svg := `<svg xmlns="http://www.w3.org/2000/svg" width="32" height="32"><rect width="32" height="32" fill="#0af"/></svg>`
	s := newIconServer(t, func(baseURL, path string, w http.ResponseWriter, r *http.Request) bool {
		if path == "/" {
			w.Write([]byte(`<link rel="icon" type="image/svg+xml" href="/logo.svg">`))
			return true
		}
		if path == "/logo.svg" {
			w.Write([]byte(svg))
			return true
		}
		return false
	})
	f := &IconFetcher{Client: s.srv.Client(), Dir: t.TempDir()}
	path, err := f.Fetch(context.Background(), s.srv.URL, "example.test")
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if filepath.Base(path) != "example.test.svg" {
		t.Errorf("缓存名 = %s", path)
	}
	if cached, ok := f.Load("example.test"); !ok || string(cached) != svg {
		t.Error("SVG 没按原文缓存")
	}
}

func TestFetchErrors(t *testing.T) {
	dir := t.TempDir()
	// 全都失败：缓存目录保持为空，下次启动再试。
	s := newIconServer(t, func(baseURL, path string, w http.ResponseWriter, r *http.Request) bool { return false })
	f := &IconFetcher{Client: s.srv.Client(), Dir: dir}
	if _, err := f.Fetch(context.Background(), s.srv.URL, "example.test"); err == nil {
		t.Fatal("全 404 想要报错")
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 0 {
		t.Errorf("失败不该写缓存，目录里有 %v", entries)
	}
	// 主机名不干净的不抓。
	for _, host := range []string{"", "a/b", "..", "a\\b"} {
		if _, err := f.Fetch(context.Background(), "https://x", host); err == nil {
			t.Errorf("host %q 想要报错", host)
		}
	}
}

func TestLoadWithoutClient(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "example.test.png"), mustPNG(t, 8, 8, color.NRGBA{A: 255}), 0o644); err != nil {
		t.Fatal(err)
	}
	f := &IconFetcher{Dir: dir} // 没建 Client 也能读缓存
	if b, ok := f.Load("example.test"); !ok || len(b) == 0 {
		t.Error("缓存没读到")
	}
	if _, ok := f.Load("other.test"); ok {
		t.Error("不该读到别的 host")
	}
}

func mustRead(t *testing.T, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// GitHub 作者头像：抓取、缓存到 github/<owner>.png、LoadAvatar 复用。
func TestFetchAvatar(t *testing.T) {
	s := newIconServer(t, func(baseURL, path string, w http.ResponseWriter, r *http.Request) bool {
		if path == "/antfu.png" {
			if r.URL.Query().Get("size") != "96" {
				t.Errorf("size 参数 = %q, want 96", r.URL.Query().Get("size"))
			}
			w.Write(mustPNG(t, 96, 96, color.NRGBA{B: 255, A: 255}))
			return true
		}
		return false
	})
	f := &IconFetcher{Client: s.srv.Client(), Dir: t.TempDir(), AvatarBase: s.srv.URL}
	path, err := f.FetchAvatar(context.Background(), "antfu")
	if err != nil {
		t.Fatalf("FetchAvatar: %v", err)
	}
	if want := filepath.Join(f.Dir, "github", "antfu.png"); path != want {
		t.Errorf("缓存路径 = %s, want %s", path, want)
	}
	if b, ok := f.LoadAvatar("antfu"); !ok || len(b) == 0 {
		t.Error("缓存没读到")
	}
	if w, h := pngDims(t, mustRead(t, path)); w != 96 || h != 96 {
		t.Errorf("缓存头像 = %dx%d, want 96x96", w, h)
	}
	for _, bad := range []string{"", "a/b", "..", "a\\b"} {
		if _, err := f.FetchAvatar(context.Background(), bad); err == nil {
			t.Errorf("owner %q 想要报错", bad)
		}
	}
}
