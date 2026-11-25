package main

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"html/template"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// brand 是应用里一个 Agent 图标的画法，读自 app/agents.go 的 brands，
// 网页上的图标和应用里的一模一样。
type brand struct {
	icon     string // assets/agents/<icon>.svg
	color    bool   // 彩色图标，按原色画在白底上
	bg, fg   string
	gradient [2]string // 径向渐变底（中心色、外圈色）
	scale    float64
	letter   string
}

// 应用里 colors.go 的 white、black
var namedColors = map[string]string{"white": "#fff", "black": "#000"}

// loadBrands 解析 appDir/agents.go 里的 brands 表。表里出现不认识的写法时报错，
// 免得图标悄悄退回字母方块。
func loadBrands(appDir string) (map[string]brand, error) {
	file := filepath.Join(appDir, "agents.go")
	f, err := parser.ParseFile(token.NewFileSet(), file, nil, 0)
	if err != nil {
		return nil, err
	}
	var table *ast.CompositeLit
	ast.Inspect(f, func(n ast.Node) bool {
		if vs, ok := n.(*ast.ValueSpec); ok && len(vs.Names) == 1 && vs.Names[0].Name == "brands" && len(vs.Values) == 1 {
			table, _ = vs.Values[0].(*ast.CompositeLit)
		}
		return table == nil
	})
	if table == nil {
		return nil, fmt.Errorf("%s: no brands table", file)
	}
	out := map[string]brand{}
	for _, el := range table.Elts {
		kv := el.(*ast.KeyValueExpr)
		id, err := str(kv.Key)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", file, err)
		}
		b, err := parseBrand(kv.Value)
		if err != nil {
			return nil, fmt.Errorf("%s: brand %q: %w", file, id, err)
		}
		out[id] = b
	}
	return out, nil
}

func parseBrand(e ast.Expr) (brand, error) {
	switch v := e.(type) {
	case *ast.CallExpr:
		name, _ := v.Fun.(*ast.Ident)
		args := make([]string, len(v.Args))
		for i, a := range v.Args {
			s, err := literal(a)
			if err != nil {
				return brand{}, err
			}
			args[i] = s
		}
		switch {
		case name == nil:
		case name.Name == "mono" && len(args) == 4:
			return brand{icon: args[0], bg: args[1], fg: args[2], scale: num(args[3])}, nil
		case name.Name == "colored" && len(args) == 2:
			return brand{icon: args[0], color: true, bg: "#fff", scale: num(args[1])}, nil
		case name.Name == "letter" && len(args) == 2:
			return brand{letter: args[0], bg: args[1], fg: "#fff"}, nil
		}
		return brand{}, fmt.Errorf("unknown constructor %s", types(e))
	case *ast.CompositeLit:
		b := brand{bg: "#fff", fg: "#fff", scale: 1}
		for _, el := range v.Elts {
			kv := el.(*ast.KeyValueExpr)
			field := kv.Key.(*ast.Ident).Name
			switch field {
			case "icon":
				call, ok := kv.Value.(*ast.CallExpr)
				if !ok || len(call.Args) != 1 {
					return b, fmt.Errorf("icon: %s", types(kv.Value))
				}
				s, err := str(call.Args[0])
				if err != nil {
					return b, err
				}
				b.icon = s
			case "gradient":
				lit, ok := kv.Value.(*ast.CompositeLit)
				if !ok || len(lit.Elts) != 2 {
					return b, fmt.Errorf("gradient: %s", types(kv.Value))
				}
				for i, c := range lit.Elts {
					s, err := literal(c)
					if err != nil {
						return b, err
					}
					b.gradient[i] = s
				}
			case "bg", "fg", "scale", "letter":
				s, err := literal(kv.Value)
				if err != nil {
					return b, err
				}
				switch field {
				case "bg":
					b.bg = s
				case "fg":
					b.fg = s
				case "scale":
					b.scale = num(s)
				case "letter":
					b.letter = s
				}
			case "color":
				b.color = kv.Value.(*ast.Ident).Name == "true"
			default:
				return b, fmt.Errorf("unknown field %s", field)
			}
		}
		return b, nil
	}
	return brand{}, fmt.Errorf("unknown form %s", types(e))
}

// literal 取字符串、数字、white/black 和 ui.Hex("#…") 的值。
func literal(e ast.Expr) (string, error) {
	switch v := e.(type) {
	case *ast.BasicLit:
		if v.Kind == token.STRING {
			return strconv.Unquote(v.Value)
		}
		return v.Value, nil
	case *ast.Ident:
		if c, ok := namedColors[v.Name]; ok {
			return c, nil
		}
	case *ast.CallExpr:
		if sel, ok := v.Fun.(*ast.SelectorExpr); ok && sel.Sel.Name == "Hex" && len(v.Args) == 1 {
			return str(v.Args[0])
		}
	}
	return "", fmt.Errorf("unsupported value %s", types(e))
}

func str(e ast.Expr) (string, error) {
	if lit, ok := e.(*ast.BasicLit); ok && lit.Kind == token.STRING {
		return strconv.Unquote(lit.Value)
	}
	return "", fmt.Errorf("want a string literal, got %s", types(e))
}

func num(s string) float64 { f, _ := strconv.ParseFloat(s, 64); return f }

func types(e ast.Expr) string { return fmt.Sprintf("%T", e) }

var (
	svgHead  = regexp.MustCompile(`(?s)<\?xml.*?\?>|<title>.*?</title>`)
	svgOpen  = regexp.MustCompile(`<svg\b[^>]*>`)
	svgSizes = regexp.MustCompile(`\s(width|height|style)="[^"]*"`)
)

// iconHTML 把 Agent 图标画成 <span class="tile">，按应用里 badge 的规则：
// 渐变底、纯色底，彩色图标按原色，单色图标用 fg，没有图标画字母。
// 没在 brands 里的 Agent 和应用一样画名字首字的灰色方块。
func iconHTML(appDir string, brands map[string]brand, id, name string) (template.HTML, error) {
	b, ok := brands[id]
	if !ok {
		b = brand{letter: strings.ToUpper(string([]rune(name)[:1])), bg: "#8e8e93", fg: "#fff"}
	}
	bg := b.bg
	if b.gradient[0] != "" {
		// 和应用一样：CSS radial-gradient(66.67% 66.67% at 87.5% 102.08%, …)
		bg = fmt.Sprintf("radial-gradient(66.67%% 66.67%% at 87.5%% 102.08%%,%s,%s)", b.gradient[0], b.gradient[1])
	}
	inner := "<b>" + template.HTMLEscapeString(b.letter) + "</b>"
	if b.icon != "" {
		data, err := os.ReadFile(filepath.Join(appDir, "assets", "agents", b.icon+".svg"))
		if err != nil {
			return "", err
		}
		svg := svgHead.ReplaceAllString(string(data), "")
		size := strconv.FormatFloat(b.scale*100, 'f', -1, 64) + "%"
		svg = svgOpen.ReplaceAllStringFunc(svg, func(tag string) string {
			return strings.Replace(svgSizes.ReplaceAllString(tag, ""), "<svg", `<svg aria-hidden="true" width="`+size+`" height="`+size+`"`, 1)
		})
		inner = strings.TrimSpace(svg)
	}
	light := ""
	if b.gradient[0] == "" && (bg == "#fff" || strings.EqualFold(bg, "#ffffff")) {
		light = " light"
	}
	return template.HTML(fmt.Sprintf(`<span class="tile%s" style="background:%s;color:%s">%s</span>`, light, bg, b.fg, inner)), nil
}
