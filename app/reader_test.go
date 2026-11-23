package main

import (
	"fmt"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"skillmanager/internal/library"
)

func TestReaderSourceToggle(t *testing.T) {
	a, tt := newTester(t)
	a.open(a.lib.Get("test-first"))
	tt.Frame()
	tt.Advance(time.Second)
	tt.Frame()
	shot := func(name string) {
		if dir := os.Getenv("READER_SNAPSHOT_DIR"); dir != "" {
			must(t, os.MkdirAll(dir, 0755))
			writePNG(t, filepath.Join(dir, name+".png"), tt)
		}
	}
	shot("rendered")
	must(t, tt.Click("查看原文"))
	if !tt.HasText("原文模式") || tt.HasText("工作流") {
		t.Fatal("source mode should replace rendered Markdown")
	}
	shot("source")
	if a.readerEditor == nil || !a.readerEditor.ReadOnly || !a.readerEditor.SoftWrap || a.readerEditor.Buffer.String() != a.reading.Files[0].Content {
		t.Fatal("source must show the complete file, including frontmatter, in a read-only wrapped editor")
	}
	must(t, tt.Click("原文模式"))
	if !tt.HasText("工作流") {
		t.Fatal("switching back should restore rendered Markdown")
	}
	must(t, tt.Click("查看原文"))
	must(t, tt.Click("references/test-levels.md"))
	if !tt.HasText("查看原文") || !tt.HasText("测试级别") {
		t.Fatal("selecting another file should restore rendered mode")
	}
	must(t, tt.Click("查看原文"))
	a.closeReader()
	a.open(a.lib.Get("code-review"))
	tt.Frame()
	if !tt.HasText("查看原文") {
		t.Fatal("opening another skill should reset source mode")
	}
}

func TestReaderOpensNonMarkdownFileInReadOnlyEditor(t *testing.T) {
	a, tt := newTester(t)
	s := a.lib.Get("test-first")
	a.open(s)
	tt.Frame()
	tt.Advance(time.Second)
	tt.Frame()

	must(t, tt.Click("scripts/run-changed.sh"))
	if tt.HasText("查看原文") || tt.HasText("原文模式") {
		t.Fatalf("非 Markdown 文件不应出现「查看原文/原文模式」切换：%q", tt.Texts())
	}
	if tt.HasText("复制") {
		t.Fatalf("非 Markdown 文件应直接用只读编辑器展示，不应再包一层 Markdown 代码块：%q", tt.Texts())
	}
	if a.readerEditor == nil || !a.readerEditor.ReadOnly || a.readerEditor.SoftWrap || a.readerEditor.Label != "scripts/run-changed.sh" {
		t.Fatalf("应创建只读且不软换行的编辑器：got=%+v", a.readerEditor)
	}
	var wantScript string
	for _, f := range s.Files {
		if f.Path == "scripts/run-changed.sh" {
			wantScript = f.Content
		}
	}
	if a.readerEditor.Buffer.String() != wantScript {
		t.Fatalf("编辑器内容应为脚本全文，got=%q want=%q", a.readerEditor.Buffer.String(), wantScript)
	}

	// 切回 Markdown 文件时应恢复渲染排版，且按 Esc 能退出阅读视图
	must(t, tt.Click("SKILL.md"))
	if !tt.HasText("工作流") {
		t.Fatal("切回 SKILL.md 应恢复渲染排版")
	}
	must(t, tt.Click("scripts/run-changed.sh"))
	tt.Key(0, ui.KeyEscape)
	if a.reading != nil {
		t.Fatal("在编辑器视图按 Esc 应关闭阅读视图")
	}
}

func TestReaderShowsImageAndSwitchesCodeFilesInEditor(t *testing.T) {
	dir := t.TempDir()
	must(t, os.MkdirAll(filepath.Join(dir, "assets"), 0o755))
	must(t, os.MkdirAll(filepath.Join(dir, "scripts"), 0o755))
	must(t, os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte("---\nname: media-skill\ndescription: demo\n---\n# Demo\n"), 0o644))
	py := "print('hello')\n"
	must(t, os.WriteFile(filepath.Join(dir, "scripts/hello.py"), []byte(py), 0o644))
	sh := "#!/bin/sh\necho ok\n"
	must(t, os.WriteFile(filepath.Join(dir, "scripts/build.sh"), []byte(sh), 0o755))

	img := image.NewRGBA(image.Rect(0, 0, 120, 80))
	for y := 0; y < 80; y++ {
		for x := 0; x < 120; x++ {
			img.Set(x, y, color.RGBA{40, 120, 200, 255})
		}
	}
	f, err := os.Create(filepath.Join(dir, "assets/cover.png"))
	must(t, err)
	must(t, png.Encode(f, img))
	must(t, f.Close())

	lib := &library.Library{}
	s := &library.Skill{ID: "media-skill", Name: "media-skill", Dir: dir, Real: dir, Source: library.Mine}
	lib.LoadFiles(s)
	lib.Skills = []*library.Skill{s}

	a := newApp(lib)
	tt := newUITester(a.view, 1280, 800)
	a.open(s)
	tt.Frame()
	tt.Advance(time.Second)
	tt.Frame()

	// 先看 Python 脚本，再切到 Shell 脚本，编辑器内容应同步切换
	must(t, tt.Click("scripts/hello.py"))
	if a.readerEditor == nil || a.readerEditor.Label != "scripts/hello.py" || a.readerEditor.Buffer.String() != py {
		t.Fatalf("应展示 scripts/hello.py：got=%+v", a.readerEditor)
	}
	must(t, tt.Click("scripts/build.sh"))
	if a.readerEditor == nil || a.readerEditor.Label != "scripts/build.sh" || a.readerEditor.Buffer.String() != sh {
		t.Fatalf("切换到 scripts/build.sh 后编辑器未更新：got=%+v", a.readerEditor)
	}

	// 再点图片文件，应按图片预览展示而非「没法直接显示」
	must(t, tt.Click("assets/cover.png"))
	if tt.HasText("没法直接显示") {
		t.Fatalf("图片文件应直接预览，不应提示没法直接显示：%q", tt.Texts())
	}
}

func TestReaderFollowsMarkdownFileLinks(t *testing.T) {
	a, tt := newTester(t)
	s := a.lib.Get("test-first")
	s.Files = []library.File{
		{Path: "SKILL.md", Content: "[Dispatch](references/dispatch.md#tasks)"},
		{Path: "references/dispatch.md", Content: "# Tasks\n\nRead [Notes](../notes%20file.md)"},
		{Path: "notes file.md", Content: "[Missing](missing.md) [Website](https://example.com)"},
	}
	a.open(s)
	tt.Frame()
	tt.Advance(time.Second)
	tt.Frame()
	clickLink(t, tt, "Dispatch")
	if a.file != 1 || !tt.HasText("Tasks") {
		t.Fatalf("file=%d texts=%q", a.file, tt.Texts())
	}
	must(t, tt.Click("Notes"))
	if a.file != 2 || !tt.HasText("Missing") || a.md.Anchor != "" {
		t.Fatal("nested documents should resolve links from their own directory")
	}
	var opened string
	a.openURL = func(dest string) { opened = dest }
	must(t, tt.Click("Missing"))
	if a.file != 2 || opened != "" {
		t.Fatal("missing files should leave the reader in place")
	}
	must(t, tt.Click("Website"))
	if opened != "https://example.com" || a.file != 2 {
		t.Fatal("web links should open externally")
	}
}

func TestReaderLinkAnchorDoesNotPullBackAfterDocumentChanges(t *testing.T) {
	a, tt := newTester(t)
	s := a.lib.Get("test-first")
	var filler strings.Builder
	for i := range 80 {
		fmt.Fprintf(&filler, "Paragraph %d\n\n", i)
	}
	s.Files = []library.File{
		{Path: "SKILL.md", Content: "[Dispatch](references/dispatch.md#tasks)"},
		{Path: "references/dispatch.md", Content: filler.String() + "# Tasks\n\n" + filler.String()},
	}
	a.open(s)
	tt.Frame()
	tt.Advance(time.Second)
	tt.Frame()
	visible := func() bool {
		r, ok := tt.Find("Tasks")
		return ok && r.Y >= 0 && r.Y+r.H <= 800
	}

	// Given 跟着带锚点的链接，滚到了 Tasks
	clickLink(t, tt, "Dispatch")
	tt.Advance(time.Second)
	tt.Frame()
	if a.file != 1 || !visible() {
		t.Fatalf("following the link should scroll to Tasks: file=%d", a.file)
	}
	// And 用户自己滚回顶部
	tt.Scroll(800, 400, 0, -100000)
	tt.Advance(time.Second)
	tt.Frame()
	if visible() {
		t.Fatal("scrolling up should move Tasks out of view")
	}

	// When 文档内容变了（如重新扫描读到新内容）
	s.Files[1].Content = "Changed\n\n" + s.Files[1].Content
	tt.Frame()
	tt.Advance(time.Second)
	tt.Frame()

	// Then 不会又跳回 Tasks
	if visible() {
		t.Fatal("a changed document should not scroll back to the old link anchor")
	}
}

// clickLink 点 Markdown 里的链接：链接文字是行内的一段，按文字找不到，按读屏角色找。
func clickLink(t *testing.T, tt *ui.Tester, name string) {
	t.Helper()
	link, ok := tt.AccessByRole(ui.RoleLink, name)
	if !ok {
		t.Fatalf("missing %s link", name)
	}
	r := link.Bounds
	tt.Press(r.X+r.W/2, r.Y+r.H/2)
	tt.Release(r.X+r.W/2, r.Y+r.H/2)
}
