package library

import "strings"

// SkillMD 是解析后的 SKILL.md。
type SkillMD struct {
	Name, Description string
	Body              string       // 去掉 frontmatter 和与 Name 同名的一级标题后的正文
	Fields            []SkillField // frontmatter 的顶层字段，按文件里的顺序
}

// SkillField 是 frontmatter 的一个顶层字段。
type SkillField struct{ Key, Value string }

// GrantsTools 报告这个字段让 Agent 用技能时可以直接调用工具（allowed-tools），
// 预览里要标出来。
func (f SkillField) GrantsTools() bool { return strings.EqualFold(f.Key, "allowed-tools") }

// ParseSkillMD 拆出 frontmatter 的 name / description。Markdown 渲染器
// 不认 frontmatter，会把 --- 当成分隔线和标题，所以在这里先去掉。
func ParseSkillMD(src string) SkillMD {
	front, body, state := splitFrontmatter(src)
	if state != frontClosed {
		return SkillMD{Body: body}
	}
	order := frontmatter(front)
	fields := map[string]string{}
	for _, f := range order {
		fields[f.Key] = f.Value
	}
	md := SkillMD{Name: fields["name"], Description: fields["description"], Body: strings.TrimLeft(body, "\n"), Fields: order}
	// 页头已显示 name，正文开头同名的一级标题不再重复。
	if h1, after, ok := strings.Cut(md.Body, "\n"); ok && md.Name != "" && strings.TrimSpace(strings.TrimPrefix(h1, "# ")) == md.Name {
		md.Body = strings.TrimLeft(after, "\n")
	}
	return md
}

// frontState 是 SKILL.md 开头 frontmatter 的样子。
type frontState int

const (
	frontNone   frontState = iota // 不以 --- 开头
	frontOpen                     // 以 --- 开头，但没有闭合的 ---
	frontClosed                   // 完整的一段
)

// splitFrontmatter 拆出 frontmatter 和正文：先去掉 UTF-8 BOM、统一换行。
// 不是完整的一段时 body 是整份文件。
func splitFrontmatter(src string) (front, body string, state frontState) {
	src = strings.TrimPrefix(strings.ReplaceAll(src, "\r\n", "\n"), "\ufeff")
	rest, ok := strings.CutPrefix(src, "---\n")
	if !ok {
		return "", src, frontNone
	}
	// 空的一段：结束的 --- 紧跟在开头那行后面
	if body, ok = strings.CutPrefix(rest, "---\n"); ok || rest == "---" {
		return "", body, frontClosed
	}
	if front, body, ok = strings.Cut(rest, "\n---\n"); ok {
		return front, body, frontClosed
	}
	if front, ok = strings.CutSuffix(rest, "\n---"); ok {
		return front, "", frontClosed
	}
	return "", src, frontOpen
}

// frontmatter 读顶层的字符串字段。只认 SKILL.md 里常见的写法：
// 普通值、引号、跨行的普通值，以及 > / | 块。嵌套的字段跳过。
func frontmatter(front string) []SkillField {
	var out []SkillField
	lines := strings.Split(front, "\n")
	for i := 0; i < len(lines); i++ {
		line := lines[i]
		if line == "" || line[0] == ' ' || line[0] == '\t' || line[0] == '#' {
			continue
		}
		k, v, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		v = strings.TrimSpace(v)
		// 缩进的后续行属于这个字段
		var more []string
		for i+1 < len(lines) && (lines[i+1] == "" || lines[i+1][0] == ' ' || lines[i+1][0] == '\t') {
			i++
			more = append(more, strings.TrimSpace(lines[i]))
		}
		switch {
		case strings.HasPrefix(v, "|"):
			v = strings.TrimSpace(strings.Join(more, "\n"))
		case strings.HasPrefix(v, ">"):
			v = strings.Join(strings.Fields(strings.Join(more, " ")), " ")
		default:
			if len(more) > 0 {
				v = strings.Join(strings.Fields(v+" "+strings.Join(more, " ")), " ")
			}
			v = unquote(v)
		}
		if v == "" && len(more) > 0 {
			v = strings.Join(more, " ") // 列表等嵌套写法：原样连成一行给人看
		}
		out = append(out, SkillField{strings.TrimSpace(k), v})
	}
	return out
}

func unquote(v string) string {
	if len(v) >= 2 && (v[0] == '"' && v[len(v)-1] == '"' || v[0] == '\'' && v[len(v)-1] == '\'') {
		v = strings.ReplaceAll(v[1:len(v)-1], `\"`, `"`)
	}
	return v
}
