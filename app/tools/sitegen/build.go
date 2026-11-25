package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"html/template"
	"image"
	_ "image/png"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

type config struct {
	Site  string // site/：templates、pages、i18n、static
	Shots string // docs/screenshots/：en、zh 两个子目录
	Out   string
	Base  string // https://skillstow.xyz，不带结尾斜杠
	App   string // mygo.json，取应用版本号
}

// lang 是一种页面语言。英文在根路径，其余语言加前缀。
type lang struct {
	Code   string // 目录名和文案文件名：en、zh
	Tag    string // <html lang> 与 hreflang
	Prefix string // URL 前缀：""、"/zh"
	Label  string // 语言切换上的名字，用这种语言自己写，两种语言的页面上显示一样
}

var langs = []lang{
	{Code: "en", Tag: "en", Prefix: "", Label: "EN"},
	{Code: "zh", Tag: "zh-Hans", Prefix: "/zh", Label: "中文"},
}

// page 是一种语言下的一个页面。Path 不带语言前缀。
type page struct {
	Kind     string // home、guide、agents、agent、404
	Path     string
	Template string     // site/pages/<lang>/ 下的文件
	Agent    *agentView // 只有 Kind 为 agent 时有
	Sources  []string   // 决定 lastmod 的文件
	NoIndex  bool
}

// view 是渲染一个页面时交给模板的全部数据。
type view struct {
	Lang        lang
	Langs       []lang
	Page        page
	Base        string
	URL         string // 本页的绝对地址（canonical）
	Alternates  []alternate
	Title       string
	Description string
	Main        template.HTML
	Schema      template.JS
	Agents      []*agentView
	Agent       *agentView
	Native      []*agentView // 能在应用里按技能启停的 Agent
	Version     string
	Updated     string // 内容最后修改日期，YYYY-MM-DD
	Repo        string
	Download    string
}

type alternate struct{ Tag, URL string }

type catalog struct {
	Strings    map[string]string `json:"strings"`
	AgentNames map[string]string `json:"agentNames"`
}

const (
	repoURL     = "https://github.com/DBAAZzz/skill-stow"
	downloadURL = repoURL + "/releases/latest"
)

func build(c config) (int, error) {
	version, err := appVersion(c.App)
	if err != nil {
		return 0, err
	}
	if err := os.RemoveAll(c.Out); err != nil {
		return 0, err
	}
	if err := copyTree(filepath.Join(c.Site, "static"), c.Out); err != nil {
		return 0, err
	}
	shots := map[string]map[string]shot{}
	for _, l := range langs {
		s, err := copyShots(filepath.Join(c.Shots, l.Code), filepath.Join(c.Out, "img", l.Code), "/img/"+l.Code)
		if err != nil {
			return 0, err
		}
		shots[l.Code] = s
	}

	appDir := filepath.Dir(c.App)
	brands, err := loadBrands(appDir)
	if err != nil {
		return 0, err
	}
	var all []rendered
	for _, l := range langs {
		cat, err := loadCatalog(filepath.Join(c.Site, "i18n", l.Code+".json"))
		if err != nil {
			return 0, err
		}
		agents := agentViews(cat.AgentNames)
		for _, a := range agents {
			if a.Icon, err = iconHTML(appDir, brands, a.ID, a.Name); err != nil {
				return 0, err
			}
		}
		for _, p := range pagesFor(l, agents) {
			r, err := render(c, l, cat, p, agents, shots[l.Code], version)
			if err != nil {
				return 0, fmt.Errorf("%s%s: %w", l.Prefix, p.Path, err)
			}
			all = append(all, r)
		}
	}
	for _, r := range all {
		if err := writeFile(filepath.Join(c.Out, filepath.FromSlash(fileFor(r.URLPath))), r.HTML); err != nil {
			return 0, err
		}
	}
	if err := writeSitemap(c, all); err != nil {
		return 0, err
	}
	if err := writeRobots(c); err != nil {
		return 0, err
	}
	if err := writeLLMs(c, all); err != nil {
		return 0, err
	}
	return len(all), nil
}

func pagesFor(l lang, agents []*agentView) []page {
	agentsSrc := "../app/internal/library/agents.go"
	ps := []page{
		{Kind: "home", Path: "/", Template: "index.html"},
		{Kind: "guide", Path: "/agent-skills/", Template: "agent-skills.html", Sources: []string{agentsSrc}},
		{Kind: "agents", Path: "/agents/", Template: "agents.html", Sources: []string{agentsSrc}},
	}
	for _, a := range agents {
		ps = append(ps, page{Kind: "agent", Path: "/agents/" + a.ID + "/", Template: "agent.html", Agent: a, Sources: []string{agentsSrc}})
	}
	if l.Prefix == "" {
		ps = append(ps, page{Kind: "404", Path: "/404.html", Template: "404.html", NoIndex: true})
	}
	return ps
}

// rendered 是生成好的一页，sitemap 和 llms.txt 也从这里取。
type rendered struct {
	Lang        lang
	Page        page
	URLPath     string // 带语言前缀
	Title       string
	Description string
	Updated     string
	HTML        []byte
}

func render(c config, l lang, cat catalog, p page, agents []*agentView, shots map[string]shot, version string) (rendered, error) {
	tmpl, err := template.New("").Funcs(funcs(l, cat, agents, shots)).ParseFiles(
		filepath.Join(c.Site, "templates", "base.html"),
		filepath.Join(c.Site, "templates", "partials.html"),
		filepath.Join(c.Site, "pages", l.Code, p.Template),
	)
	if err != nil {
		return rendered{}, err
	}
	sources := []string{filepath.Join(c.Site, "pages", l.Code, p.Template)}
	for _, s := range p.Sources {
		sources = append(sources, filepath.Join(c.Site, s))
	}
	urlPath := l.Prefix + p.Path
	v := view{
		Lang: l, Langs: langs, Page: p, Base: c.Base, URL: c.Base + urlPath,
		Agents: agents, Agent: p.Agent, Native: nativeAgentsOf(agents), Version: version,
		Updated: lastModified(sources...), Repo: repoURL, Download: downloadURL,
	}
	if !p.NoIndex {
		for _, alt := range langs {
			v.Alternates = append(v.Alternates, alternate{Tag: alt.Tag, URL: c.Base + alt.Prefix + p.Path})
		}
	}
	for name, dst := range map[string]*string{"title": &v.Title, "description": &v.Description} {
		s, err := execString(tmpl, name, v)
		if err != nil {
			return rendered{}, err
		}
		*dst = strings.Join(strings.Fields(s), " ")
	}
	main, err := execString(tmpl, "main", v)
	if err != nil {
		return rendered{}, err
	}
	v.Main = template.HTML(main)
	schema, err := schemaFor(v, cat, faqs(main))
	if err != nil {
		return rendered{}, err
	}
	v.Schema = template.JS(schema)
	var b bytes.Buffer
	if err := tmpl.ExecuteTemplate(&b, "base", v); err != nil {
		return rendered{}, err
	}
	return rendered{Lang: l, Page: p, URLPath: urlPath, Title: v.Title, Description: v.Description, Updated: v.Updated, HTML: b.Bytes()}, nil
}

func execString(t *template.Template, name string, data any) (string, error) {
	var b bytes.Buffer
	err := t.ExecuteTemplate(&b, name, data)
	return b.String(), err
}

func funcs(l lang, cat catalog, agents []*agentView, shots map[string]shot) template.FuncMap {
	byID := map[string]*agentView{}
	for _, a := range agents {
		byID[a.ID] = a
	}
	return template.FuncMap{
		// t 取界面文案；缺了直接报错，免得页面上出现空白
		"t": func(key string) (string, error) {
			if s, ok := cat.Strings[key]; ok {
				return s, nil
			}
			return "", fmt.Errorf("i18n/%s.json: missing %q", l.Code, key)
		},
		// link 给站内路径加上当前语言的前缀
		"link": func(p string) string { return l.Prefix + p },
		"home": func(p string) string { return "~/" + p },
		"agent": func(id string) (*agentView, error) {
			if a, ok := byID[id]; ok {
				return a, nil
			}
			return nil, fmt.Errorf("unknown agent %q", id)
		},
		"shot": func(name, alt string) (template.HTML, error) { return shotTag(shots, name, alt, false) },
		"heroShot": func(name, alt string) (template.HTML, error) {
			return shotTag(shots, name, alt, true)
		},
		"crop": func(name string, x0, y0, x1, y1 int, alt string) (template.HTML, error) {
			return cropTag(shots, name, x0, y0, x1, y1, alt)
		},
		// agentLinks 把一串 Agent 写成带链接的列举，分隔词取自文案
		"agentLinks": func(list []*agentView) template.HTML {
			var parts []string
			for _, a := range list {
				parts = append(parts, fmt.Sprintf(`<a href="%s/agents/%s/">%s</a>`, l.Prefix, a.ID, template.HTMLEscapeString(a.Name)))
			}
			return template.HTML(joinList(parts, cat.Strings["list.sep"], cat.Strings["list.and"]))
		},
		"sharedReaders": sharedReaders,
		"crossReads":    crossReads,
		"add":           func(a, b int) int { return a + b },
		"mod":           func(a, b int) int { return a % b },
		"list":          func(xs ...any) []any { return xs },
		"last":          func(i, n int) bool { return i == n-1 },
	}
}

// joinList 把 a、b、c 连成「a、b 和 c」这样的列举。
func joinList(parts []string, sep, and string) string {
	if len(parts) < 2 {
		return strings.Join(parts, "")
	}
	return strings.Join(parts[:len(parts)-1], sep) + and + parts[len(parts)-1]
}

type shot struct {
	Src  string
	WebP string // 同一张截图的 WebP 版，没装 cwebp 时为空，页面只用 PNG
	W, H int
}

// picture 给 <img> 套上 WebP 来源；不支持 WebP 的浏览器照旧读 PNG。
func (s shot) picture(img string) string {
	if s.WebP == "" {
		return img
	}
	return `<picture><source type="image/webp" srcset="` + s.WebP + `">` + img + `</picture>`
}

func shotTag(shots map[string]shot, name, alt string, eager bool) (template.HTML, error) {
	s, ok := shots[name]
	if !ok {
		return "", fmt.Errorf("no screenshot %q", name)
	}
	load := `loading="lazy" decoding="async"`
	if eager {
		load = `fetchpriority="high"`
	}
	return template.HTML(s.picture(fmt.Sprintf(`<img src="%s" width="%d" height="%d" alt="%s" %s>`,
		s.Src, s.W, s.H, template.HTMLEscapeString(alt), load))), nil
}

// cropTag 只显示截图里的一块：x0,y0–x1,y1 是 2x 截图上的像素。用的是整张截图
// 加 CSS 裁切，同一张截图在页面上只下载一次，截图更新后裁切跟着更新。
func cropTag(shots map[string]shot, name string, x0, y0, x1, y1 int, alt string) (template.HTML, error) {
	s, ok := shots[name]
	w, h := x1-x0, y1-y0
	switch {
	case !ok:
		return "", fmt.Errorf("no screenshot %q", name)
	case w <= 0 || h <= 0 || x1 > s.W*2 || y1 > s.H*2:
		return "", fmt.Errorf("crop %d,%d-%d,%d is outside %s (%dx%d)", x0, y0, x1, y1, name, s.W*2, s.H*2)
	}
	pct := func(a, b int) string { return fmt.Sprintf("%.3f%%", float64(a)/float64(b)*100) }
	return template.HTML(fmt.Sprintf(
		`<span class="crop" style="aspect-ratio:%d/%d">%s</span>`, w, h,
		s.picture(fmt.Sprintf(`<img src="%s" width="%d" height="%d" alt="%s" loading="lazy" decoding="async" style="width:%s;left:-%s;top:-%s">`,
			s.Src, s.W, s.H, template.HTMLEscapeString(alt), pct(s.W*2, w), pct(x0, w), pct(y0, h))))), nil
}

func copyShots(src, dst, urlDir string) (map[string]shot, error) {
	files, err := filepath.Glob(filepath.Join(src, "*.png"))
	if err != nil || len(files) == 0 {
		return nil, fmt.Errorf("no screenshots in %s", src)
	}
	shots := map[string]shot{}
	for _, f := range files {
		data, err := os.ReadFile(f)
		if err != nil {
			return nil, err
		}
		cfg, _, err := image.DecodeConfig(bytes.NewReader(data))
		if err != nil {
			return nil, fmt.Errorf("%s: %w", f, err)
		}
		name := filepath.Base(f)
		if err := writeFile(filepath.Join(dst, name), data); err != nil {
			return nil, err
		}
		base := strings.TrimSuffix(name, ".png")
		// 截图是 2x 的，按一半标尺寸
		s := shot{Src: urlDir + "/" + name, W: cfg.Width / 2, H: cfg.Height / 2}
		ok, err := encodeWebP(f, filepath.Join(dst, base+".webp"))
		if err != nil {
			return nil, err
		}
		if ok {
			s.WebP = urlDir + "/" + base + ".webp"
		}
		shots[base] = s
	}
	return shots, nil
}

var warnedNoWebP bool

// encodeWebP 用 cwebp 把截图转成 WebP，体积约为 PNG 的四分之一。界面截图里字多，
// 用 -sharp_yuv 保住文字边缘。没装 cwebp（brew install webp）时返回 false，
// 只提示一次，页面退回 PNG；CI 装了 cwebp，部署出去的一定带 WebP。
func encodeWebP(src, dst string) (bool, error) {
	bin, err := exec.LookPath("cwebp")
	if err != nil {
		if !warnedNoWebP {
			warnedNoWebP = true
			fmt.Fprintln(os.Stderr, "sitegen: cwebp not found, screenshots stay PNG only (brew install webp)")
		}
		return false, nil
	}
	if out, err := exec.Command(bin, "-quiet", "-q", "90", "-sharp_yuv", "-m", "6", src, "-o", dst).CombinedOutput(); err != nil {
		return false, fmt.Errorf("cwebp %s: %v: %s", src, err, out)
	}
	return true, nil
}

func loadCatalog(file string) (catalog, error) {
	var c catalog
	data, err := os.ReadFile(file)
	if err != nil {
		return c, err
	}
	if err := json.Unmarshal(data, &c); err != nil {
		return c, fmt.Errorf("%s: %w", file, err)
	}
	return c, nil
}

func appVersion(file string) (string, error) {
	var m struct{ Version string }
	data, err := os.ReadFile(file)
	if err != nil {
		return "", err
	}
	if err := json.Unmarshal(data, &m); err != nil || m.Version == "" {
		return "", fmt.Errorf("%s: no version", file)
	}
	return m.Version, nil
}

// lastModified 是这些文件在 git 里最后一次提交的日期；还没提交（本地预览）时用今天。
func lastModified(files ...string) string {
	out, err := exec.Command("git", append([]string{"log", "-1", "--format=%cs", "--"}, files...)...).Output()
	if d := strings.TrimSpace(string(out)); err == nil && d != "" {
		return d
	}
	return time.Now().Format(time.DateOnly)
}

// fileFor 把 URL 路径换成输出文件：目录形式的路径写成 index.html。
func fileFor(urlPath string) string {
	p := strings.TrimPrefix(urlPath, "/")
	if p == "" || strings.HasSuffix(p, "/") {
		p += "index.html"
	}
	return p
}

func writeFile(file string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		return err
	}
	return os.WriteFile(file, data, 0o644)
}

func copyTree(src, dst string) error {
	return filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || strings.HasPrefix(d.Name(), ".") {
			return err
		}
		rel, _ := filepath.Rel(src, p)
		data, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		return writeFile(filepath.Join(dst, rel), data)
	})
}
