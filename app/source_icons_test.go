package main

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"testing"

	"github.com/egoist/mygo/ui"

	"skillmanager/internal/library"
)

// testIconPNG 造一张 w×h 纯色 PNG，冒充抓回来的站点图标。
func testIconPNG(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	c := color.NRGBA{B: 255, A: 255}
	for i := 0; i < len(img.Pix); i += 4 {
		img.Pix[i], img.Pix[i+1], img.Pix[i+2], img.Pix[i+3] = c.R, c.G, c.B, c.A
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// queueBG 替换后台执行：work 就地跑，post 存进队列手动放行。
func queueBG(a *app) *[]func() {
	var queue []func()
	a.bg = func(work func(post func(func()))) {
		work(func(fn func()) { queue = append(queue, fn) })
	}
	return &queue
}

func flush(queue *[]func()) {
	for _, fn := range *queue {
		fn()
	}
	*queue = nil
}

// 缓存里已经有图标时，ensure 只读文件不解网络，解出的图进 srcIcons。
func TestSourceIconLoadedFromCache(t *testing.T) {
	a := newApp(library.Scan(t.TempDir())) // 空库：没有示例安装记录干扰
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "open.feishu.cn.png"), testIconPNG(t, 32, 32), 0o644); err != nil {
		t.Fatal(err)
	}
	a.iconFetch = &library.IconFetcher{Dir: dir} // 不带 Client：走到网络就是 panic，正好当断言
	updateFixture(a)

	queue := queueBG(a)
	a.ensureSourceIcons()
	flush(queue)
	if a.srcIcons["open.feishu.cn"] == nil {
		t.Fatal("图标没从缓存读出来")
	}
	if a.srcIconBusy["open.feishu.cn"] {
		t.Error("任务结束后不该还是 busy")
	}
	// 已经有的 host 不再排任务
	a.ensureSourceIcons()
	if len(*queue) != 0 {
		t.Error("已有的 host 不该重抓")
	}
}

// 抓取失败不缓存也不标图标，host 放开，下次报告刷新再试。
func TestSourceIconFetchFailureRetriesLater(t *testing.T) {
	a := newApp(library.Sample())
	a.iconFetch = &library.IconFetcher{Dir: t.TempDir()} // 没缓存也没 Client，一定失败
	updateFixture(a)

	queue := queueBG(a)
	a.ensureSourceIcons()
	flush(queue)
	if len(a.srcIcons) != 0 {
		t.Error("失败不该留下图标")
	}
	if a.srcIconBusy["open.feishu.cn"] {
		t.Error("失败后要放开 host")
	}
	a.ensureSourceIcons()
	if len(*queue) == 0 {
		t.Error("失败后再次 ensure 应该重试")
	}
}

// iconFetch 为 nil（现有各 fixture 测试的形态）时整体不启动。
func TestSourceIconsIdleWithoutFetcher(t *testing.T) {
	a := newApp(library.Sample())
	updateFixture(a)
	a.ensureSourceIcons()
	if a.srcIconBusy != nil || a.srcIcons != nil {
		t.Error("没有抓取器不该建状态")
	}
}

// 来源行：抓到图标画白底圆角位图，画布上还能看到行文字。
func TestSourceRowRendersWithIcon(t *testing.T) {
	a := newApp(library.Sample())
	updateFixture(a)
	src, err := ui.DecodeImage(testIconPNG(t, 32, 32))
	if err != nil {
		t.Fatal(err)
	}
	a.srcIcons = map[string]ui.ImageSource{"open.feishu.cn": src}
	tt := newUITester(a.view, 1280, 800)
	if !tt.HasText("open.feishu.cn") {
		t.Fatal(tt.Texts())
	}
	tt.Frame()
}

// 真连网抓本机来源的图标并截图，人工核对级联和缓存；设
// SKILLS_LIVE_ICONS=1 才跑（SNAPSHOT_DIR 有值时存那里，否则存临时目录）。
func TestSourceIconsLive(t *testing.T) {
	if os.Getenv("SKILLS_LIVE_ICONS") == "" {
		t.Skip("SKILLS_LIVE_ICONS")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	a := newApp(library.Scan(home))
	a.iconFetch = library.NewIconFetcher(a.lib.Settings, home)
	a.scope = scopeUpdates
	// win 为 nil 时 background 就地跑，抓完再建画布，图标已经就位。
	a.initSourceIcons()
	var got []string
	for host := range a.srcIcons {
		got = append(got, host)
	}
	t.Logf("抓到图标：%v", got)

	tt := newUITester(a.view, 1280, 2400)
	dir := os.Getenv("SNAPSHOT_DIR")
	if dir == "" {
		dir = t.TempDir()
	}
	f, err := os.Create(filepath.Join(dir, "source-icons.png"))
	if err != nil {
		t.Fatal(err)
	}
	if err := png.Encode(f, tt.Image()); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	t.Logf("截图：%s", filepath.Join(dir, "source-icons.png"))
}

// 来源头像在名称左边（src2-b 实装迭代）：技能卡和来源集合卡都显示，
// 没有图标的集合回落叠卡图标。
func TestSourceIconNextToCardName(t *testing.T) {
	// 临时 home：一个带安装记录的「我的」技能（不在集合里）+ 一个两成员的
	// 来源集合（open.feishu.cn）。
	home := t.TempDir()
	mustWrite(t, filepath.Join(home, ".agents", "skills", "vite", "SKILL.md"),
		"---\nname: vite\ndescription: 构建工具\n---\n正文", 0o644)
	mustWrite(t, filepath.Join(home, ".agents", ".skill-lock.json"), `{
	"version": 3,
	"skills": {
		"vite":     {"source": "antfu/skills", "sourceType": "github", "installedAt": "2026-09-21T02:55:56Z"},
		"lark-doc": {"source": "open.feishu.cn", "sourceType": "well-known", "installedAt": "2026-09-21T02:55:56Z"},
		"lark-im":  {"source": "open.feishu.cn", "sourceType": "well-known", "installedAt": "2026-09-21T02:55:56Z"}
	}
}`, 0o644)
	for _, name := range []string{"lark-doc", "lark-im"} {
		mustWrite(t, filepath.Join(home, ".agents", "skills", name, "SKILL.md"),
			"---\nname: "+name+"\ndescription: 飞书\n---\n正文", 0o644)
	}

	a := newApp(library.Scan(home))
	src := mustImage(t, testIconPNG(t, 32, 32))
	a.srcIcons = map[string]ui.ImageSource{
		"gh:antfu":       src,
		"open.feishu.cn": src,
	}
	a.scope = string(library.ScopeMine)
	a.mode = viewCards
	tt := newUITester(a.view, 1280, 800)
	tt.Frame()
	if _, ok := tt.Find("vite 来源头像"); !ok {
		t.Fatal("技能卡缺来源头像")
	}
	if _, ok := tt.Find("open.feishu.cn 来源头像"); !ok {
		t.Fatal("来源集合卡缺来源头像")
	}
	// footer 里不再有来源徽章
	if _, ok := tt.Find("来源 antfu/skills"); ok {
		t.Error("footer 不该再有来源徽章")
	}
}

func mustWrite(t *testing.T, path, content string, mode os.FileMode) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), mode); err != nil {
		t.Fatal(err)
	}
}

// 详情栏（src2-b）：名称左侧 44px 来源头像，来源行显示「来自 <来源>」。
func TestPaneHeaderShowsSourceAvatar(t *testing.T) {
	a, tt := newTester(t)
	s := a.lib.Get("lark-doc")
	if s == nil {
		t.Fatal("示例缺 lark-doc")
	}
	src := mustImage(t, testIconPNG(t, 32, 32))
	a.srcIcons = map[string]ui.ImageSource{"open.feishu.cn": src}
	a.selected = s
	tt.Frame()
	if !tt.HasText("来自 open.feishu.cn") {
		t.Fatal(tt.Texts())
	}
	tt.Frame()
}

func mustImage(t *testing.T, data []byte) ui.ImageSource {
	t.Helper()
	src, err := ui.DecodeImage(data)
	if err != nil {
		t.Fatal(err)
	}
	return src
}
