package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"

	"skillmanager/internal/library"
)

// 系统切到深色，窗口下一帧就换成深色；切回浅色，颜色一个不差地回来。
func TestWindowFollowsSystemAppearance(t *testing.T) {
	defer usePalette(false)
	// 显式从浅色起步：SNAPSHOT_DARK=1 时 newTester 会是深色的
	a := newApp(library.Sample())
	tt := ui.NewTesterWith(a.view, 1280, 800, ui.TesterOptions{Dark: false})
	contentPixel := func() ui.Color {
		t.Helper()
		r, ok := tt.Find("搜索名称或简介")
		if !ok {
			t.Fatal("找不到搜索框")
		}
		// 搜索框正下方是内容区的底
		px := tt.Image().RGBAAt(int(r.X), int(r.Y+r.H+30))
		return ui.Color{R: px.R, G: px.G, B: px.B, A: px.A}
	}
	surface := toneOf(t, &colBgSurface)
	if got, want := contentPixel(), cssColor(surface.light); got != want {
		t.Fatalf("浅色时内容区应是 %v，得到 %v", want, got)
	}

	tt.SetDark(true)
	tt.Frame()
	if got, want := contentPixel(), cssColor(surface.dark); got != want {
		t.Fatalf("深色时内容区应是 %v，得到 %v", want, got)
	}
	if want := cssColor(toneOf(t, &colTextPrimary).dark); colTextPrimary != want {
		t.Fatalf("深色时正文颜色应换成 %v，得到 %v", want, colTextPrimary)
	}

	tt.SetDark(false)
	tt.Frame()
	for _, c := range tones {
		if want := cssColor(c.light); *c.c != want {
			t.Fatalf("切回浅色后有颜色没换回来：得到 %v，应是 %v", *c.c, want)
		}
	}
}

func toneOf(t *testing.T, c *ui.Color) tone {
	t.Helper()
	for _, x := range tones {
		if x.c == c {
			return x
		}
	}
	t.Fatal("tones 里没有这个颜色")
	return tone{}
}

// 每个颜色都要在 tones 里写上浅色、深色两个值，漏了的一直是零值（透明）。
// 不变的颜色写在 colors.go「不随深浅色变」那一组，直接赋值。
func TestEveryColorHasDarkValue(t *testing.T) {
	file, err := parser.ParseFile(token.NewFileSet(), "colors.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	inTones := map[string]bool{}
	for _, name := range toneNames(t) {
		if inTones[name] {
			t.Errorf("%s 在 tones 里写了两遍", name)
		}
		inTones[name] = true
	}
	for _, d := range file.Decls {
		g, ok := d.(*ast.GenDecl)
		if !ok || g.Tok != token.VAR {
			continue
		}
		for _, spec := range g.Specs {
			vs := spec.(*ast.ValueSpec)
			if sel, ok := vs.Type.(*ast.SelectorExpr); !ok || sel.Sel.Name != "Color" || vs.Values != nil {
				continue
			}
			for _, n := range vs.Names {
				if !inTones[n.Name] {
					t.Errorf("%s 没有值：在 tones 里加一行浅色、深色", n.Name)
				}
			}
		}
	}
}

// 颜色按用途起名，同一个用途只有一个名字：新加的颜色和已有的浅色、深色都
// 一样就是重复；总数守在 75 个以内，再多就该回头合并（设计规范 §13.3）。
func TestPaletteStaysSmall(t *testing.T) {
	if n := len(tones); n > 75 {
		t.Errorf("颜色有 %d 个，超过 75 个：先看能不能用已有的名字或 colInkHover.Over(底色)", n)
	}
	names := toneNames(t)
	seen := map[[2]ui.Color]string{}
	for i, c := range tones {
		key := [2]ui.Color{cssColor(c.light), cssColor(c.dark)}
		if prev, ok := seen[key]; ok {
			t.Errorf("%s 和 %s 浅色、深色都一样，合成一个", names[i], prev)
		}
		seen[key] = names[i]
	}
}

// 颜色值只能写在 colors.go（agents.go 的品牌色除外）：别处写死的颜色不会跟着
// 深浅色换，深色模式下就成了一块突兀的浅色。
func TestColorsLiveInColorsFile(t *testing.T) {
	allowed := map[string]bool{"colors.go": true, "agents.go": true}
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	for _, ent := range entries {
		name := ent.Name()
		if ent.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") || allowed[name] {
			continue
		}
		file, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		ast.Inspect(file, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			if pkg, ok := sel.X.(*ast.Ident); ok && pkg.Name == "ui" {
				switch sel.Sel.Name {
				case "Hex", "RGB", "RGBA":
					t.Errorf("%s:%d: ui.%s() 写死了颜色；在 colors.go 里起个名字再用",
						name, fset.Position(call.Pos()).Line, sel.Sel.Name)
				}
			}
			return true
		})
	}
}

// 包级变量启动时就求值：拿色板里的颜色做初始值（var x = colTextPrimary、
// []ui.Color{colTextPrimary}、立即调用的 func(){…}() 里读到的也算），存下的是那一
// 刻的浅色，换外观不跟着变。要用颜色就在用的时候取，或按深浅色各缓存一份
// （skillMarkdownTheme）。
func TestNoPaletteColorCapturedAtInit(t *testing.T) {
	palette := map[string]bool{}
	for _, name := range toneNames(t) {
		palette[name] = true
	}
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	for _, ent := range entries {
		name := ent.Name()
		if ent.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") || name == "colors.go" {
			continue
		}
		file, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		for _, d := range file.Decls {
			g, ok := d.(*ast.GenDecl)
			if !ok || g.Tok != token.VAR {
				continue
			}
			for _, spec := range g.Specs {
				for _, v := range spec.(*ast.ValueSpec).Values {
					ast.Inspect(v, func(n ast.Node) bool {
						switch n := n.(type) {
						case *ast.CallExpr:
							if lit, ok := n.Fun.(*ast.FuncLit); ok {
								ast.Inspect(lit.Body, func(m ast.Node) bool { return reportPalette(t, fset, name, palette, m) })
								return false
							}
						case *ast.FuncLit:
							return false // 调用时才求值
						}
						return reportPalette(t, fset, name, palette, n)
					})
				}
			}
		}
	}
}

func reportPalette(t *testing.T, fset *token.FileSet, file string, palette map[string]bool, n ast.Node) bool {
	t.Helper()
	if id, ok := n.(*ast.Ident); ok && palette[id.Name] {
		t.Errorf("%s:%d: 包级变量启动时就读了 %s，换外观后不会跟着变；改成用的时候再取",
			file, fset.Position(id.Pos()).Line, id.Name)
	}
	return true
}

// toneNames 是 colors.go 里 tones 列出的颜色名，顺序同 tones。
func toneNames(t *testing.T) []string {
	t.Helper()
	file, err := parser.ParseFile(token.NewFileSet(), "colors.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	ast.Inspect(file, func(n ast.Node) bool {
		vs, ok := n.(*ast.ValueSpec)
		if !ok {
			return true
		}
		for i, id := range vs.Names {
			if id.Name != "tones" {
				continue
			}
			for _, el := range vs.Values[i].(*ast.CompositeLit).Elts {
				ref := el.(*ast.CompositeLit).Elts[0].(*ast.UnaryExpr).X.(*ast.Ident)
				names = append(names, ref.Name)
			}
		}
		return true
	})
	if len(names) != len(tones) {
		t.Fatalf("colors.go 里读出 %d 个颜色名，tones 有 %d 个", len(names), len(tones))
	}
	return names
}
