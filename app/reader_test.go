package main

import (
	"fmt"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
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

func TestReaderFileListScrollsWhenSkillHasManyFiles(t *testing.T) {
	files := []library.File{
		{Path: "SKILL.md", Size: 64, Content: "---\nname: cloudflare\ndescription: demo\n---\n# Cloudflare\n"},
	}
	for g := range 8 {
		for i := range 5 {
			p := fmt.Sprintf("references/group-%02d/doc-%02d.md", g, i)
			files = append(files, library.File{Path: p, Size: 120, Content: "# " + p + "\ncontent\n"})
		}
	}
	last := "references/group-07/doc-04.md"

	lib := &library.Library{}
	s := &library.Skill{ID: "cloudflare", Name: "cloudflare", Source: library.Mine, Files: files}
	lib.Skills = []*library.Skill{s}

	a := newApp(lib)
	tt := newUITester(a.view, 1280, 800)
	tt.SetScale(2)
	a.open(s)
	tt.Frame()
	tt.Advance(time.Second)
	tt.Frame()

	firstRect, ok := tt.Find("SKILL.md")
	if !ok {
		t.Fatal("missing SKILL.md row in file list")
	}
	lastBefore, ok := tt.Find(last)
	if !ok {
		t.Fatal("missing last file row in file list")
	}
	if lastBefore.Y+lastBefore.H <= 800 {
		t.Fatalf("预期末尾文件初始超出视口（Y=%.1f H=%.1f）", lastBefore.Y, lastBefore.H)
	}

	// 在左侧文件列表区域向下滚动
	tt.Scroll(firstRect.X+firstRect.W/2, 400, 0, 800)
	tt.Frame()

	lastAfter, ok := tt.Find(last)
	if !ok {
		t.Fatal("missing last file row after scroll")
	}
	if lastAfter.Y >= lastBefore.Y {
		t.Fatalf("文件列表未向下滚动：before Y=%.1f, after Y=%.1f", lastBefore.Y, lastAfter.Y)
	}
	if lastAfter.Y+lastAfter.H > 800 || lastAfter.Y < 52 {
		t.Fatalf("滚动后末尾文件仍不在视口内：rect=%+v", lastAfter)
	}
	must(t, tt.Click(last))
	if a.reading.Files[a.file].Path != last {
		t.Fatalf("滚动后点击末尾文件应切换过去，实际=%q", a.reading.Files[a.file].Path)
	}
}
