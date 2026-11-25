package main

import (
	"encoding/json"
	"fmt"
	"html"
	"image/png"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"

	"skillmanager/internal/library"
)

func TestEveryNativeToggleAgentHasItsConfigOnTheSite(t *testing.T) {
	for _, a := range library.Catalog {
		if got, want := nativeConfigs[a.ID] != nil, library.NativeToggleSupported(a.ID); got != want {
			t.Errorf("%s: nativeConfigs has it = %v, library supports toggling = %v", a.ID, got, want)
		}
	}
}

// builtSite 生成一次整站，供下面几个测试检查。
// 各测试只读生成结果，整站只生成一次：生成时要把截图转成 WebP，每个测试各生成一遍太慢。
var site struct {
	once  sync.Once
	out   string
	pages map[string]string
	err   error
}

func TestMain(m *testing.M) {
	code := m.Run()
	if site.out != "" {
		os.RemoveAll(site.out)
	}
	os.Exit(code)
}

func builtSite(t *testing.T) (string, map[string]string) {
	t.Helper()
	site.once.Do(func() { site.out, site.pages, site.err = buildSite() })
	if site.err != nil {
		t.Fatal(site.err)
	}
	return site.out, site.pages
}

func buildSite() (string, map[string]string, error) {
	out, err := os.MkdirTemp("", "sitegen-test-")
	if err != nil {
		return "", nil, err
	}
	n, err := build(config{Site: "../../../site", Shots: "../../../docs/screenshots", Out: out, Base: "https://skillstow.xyz", App: "../../mygo.json"})
	if err != nil {
		return out, nil, err
	}
	if want := (2 * (3 + len(library.Catalog))) + 1; n != want {
		return out, nil, fmt.Errorf("built %d pages, want %d (home, guide, agent index and one page per agent in each language, plus 404)", n, want)
	}
	pages := map[string]string{}
	err = filepath.WalkDir(out, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(p, ".html") {
			return err
		}
		b, err := os.ReadFile(p)
		rel, _ := filepath.Rel(out, p)
		pages[filepath.ToSlash(rel)] = string(b)
		return err
	})
	return out, pages, err
}

var (
	titleRe = regexp.MustCompile(`<title>([^<]*)</title>`)
	descRe  = regexp.MustCompile(`<meta name="description" content="([^"]*)">`)
	linkRe  = regexp.MustCompile(`(?:href|src|srcset)="(/[^"#]*)`)
	ldRe    = regexp.MustCompile(`(?s)<script type="application/ld\+json">(.*?)</script>`)
)

func TestEveryPageHasUniqueTitleDescriptionAndCanonical(t *testing.T) {
	_, pages := builtSite(t)
	titles := map[string]string{}
	for file, body := range pages {
		title := titleRe.FindStringSubmatch(body)
		desc := descRe.FindStringSubmatch(body)
		if title == nil || strings.TrimSpace(title[1]) == "" || desc == nil || strings.TrimSpace(desc[1]) == "" {
			t.Errorf("%s: missing title or description", file)
			continue
		}
		if other, dup := titles[title[1]]; dup {
			t.Errorf("%s and %s share the title %q", file, other, title[1])
		}
		titles[title[1]] = file
		if file == "404.html" {
			continue
		}
		if !strings.Contains(body, `<link rel="canonical" href="https://skillstow.xyz/`) {
			t.Errorf("%s: no canonical link", file)
		}
		if n := len([]rune(html.UnescapeString(desc[1]))); n > 200 {
			t.Errorf("%s: description is %d characters, search results cut it off", file, n)
		}
	}
}

func TestInternalLinksPointToGeneratedFiles(t *testing.T) {
	out, pages := builtSite(t)
	for file, body := range pages {
		for _, m := range linkRe.FindAllStringSubmatch(body, -1) {
			target := strings.TrimPrefix(fileFor(m[1]), "/")
			if _, err := os.Stat(filepath.Join(out, filepath.FromSlash(target))); err != nil {
				t.Errorf("%s links to %s, which wasn't generated", file, m[1])
			}
		}
	}
}

func TestStructuredDataIsValidJSONWithFAQs(t *testing.T) {
	_, pages := builtSite(t)
	for file, body := range pages {
		m := ldRe.FindStringSubmatch(body)
		if m == nil {
			t.Errorf("%s: no JSON-LD", file)
			continue
		}
		var v any
		if err := json.Unmarshal([]byte(m[1]), &v); err != nil {
			t.Errorf("%s: JSON-LD doesn't parse: %v", file, err)
		}
		if strings.Contains(body, `class="qa"`) && !strings.Contains(m[1], `"FAQPage"`) {
			t.Errorf("%s: has questions but no FAQPage", file)
		}
	}
}

// 和应用界面一样，网站上也不用点号把几段信息拼成一行（见 AGENTS.md）。
func TestSiteTextDoesNotJoinInformationWithDots(t *testing.T) {
	_, pages := builtSite(t)
	for file, body := range pages {
		for _, dot := range []string{"·", "•", "‧", "⋅", "・"} {
			if strings.Contains(body, dot) {
				t.Errorf("%s contains %q", file, dot)
			}
		}
	}
}

func TestSitemapListsEveryIndexablePageWithBothLanguages(t *testing.T) {
	out, pages := builtSite(t)
	b, err := os.ReadFile(filepath.Join(out, "sitemap.xml"))
	if err != nil {
		t.Fatal(err)
	}
	sitemap := string(b)
	for file := range pages {
		if file == "404.html" {
			continue
		}
		loc := "https://skillstow.xyz/" + strings.TrimSuffix(file, "index.html")
		if !strings.Contains(sitemap, "<loc>"+loc+"</loc>") {
			t.Errorf("sitemap is missing %s", loc)
		}
	}
	if strings.Contains(sitemap, "404") {
		t.Error("sitemap lists the 404 page")
	}
	if !strings.Contains(sitemap, `hreflang="zh-Hans" href="https://skillstow.xyz/zh/agents/codex/"`) {
		t.Error("sitemap has no Chinese alternate for the Codex page")
	}
}

func TestAgentIconsAreReadFromTheAppsBrandTable(t *testing.T) {
	brands, err := loadBrands("../..")
	if err != nil {
		t.Fatal(err)
	}
	if b := brands["claude-code"]; b.icon != "claude" || b.bg != "#D97757" || b.scale != 0.7 {
		t.Errorf("claude-code brand = %+v, want the claude icon on #D97757 at 0.7", b)
	}
	for id := range brands {
		if _, err := iconHTML("../..", brands, id, id); err != nil {
			t.Errorf("%s: %v", id, err)
		}
	}
}

func TestHomeShowsEveryAgentWithALinkToItsPage(t *testing.T) {
	_, pages := builtSite(t)
	for file, prefix := range map[string]string{"index.html": "", "zh/index.html": "/zh"} {
		body := pages[file]
		for _, a := range library.Catalog {
			want := `<a href="` + prefix + `/agents/` + a.ID + `/" data-name=`
			if !strings.Contains(body, want) {
				t.Errorf("%s: agent wall has no tile linking to %s", file, a.ID)
			}
		}
		if n := strings.Count(body, `<span class="tile`); n < len(library.Catalog) {
			t.Errorf("%s: %d agent icons, want at least %d", file, n, len(library.Catalog))
		}
	}
}

func TestFaviconWorksInBrowsersAndGoogleResults(t *testing.T) {
	out, pages := builtSite(t)
	if _, err := os.Stat(filepath.Join(out, "favicon.ico")); err != nil {
		t.Error("favicon.ico is missing; browsers and crawlers request /favicon.ico directly")
	}
	f, err := os.Open(filepath.Join(out, "favicon.png"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	cfg, err := png.DecodeConfig(f)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Width != cfg.Height || cfg.Width%48 != 0 {
		t.Errorf("favicon.png is %dx%d; Google only shows square favicons whose side is a multiple of 48", cfg.Width, cfg.Height)
	}
	if !strings.Contains(pages["index.html"], `href="/favicon.ico"`) {
		t.Error("pages don't link /favicon.ico")
	}
}
