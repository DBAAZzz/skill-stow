package library

import (
	"encoding/json"
	"errors"

	"strings"

	"skillmanager/locales"
)

// JSON spans refer to the original text; comments and spacing never round-trip through a map.
type jsonNode struct {
	start, end int
	kind       byte
	members    []jsonMember
	items      []*jsonNode
}
type jsonMember struct {
	key          string
	start, comma int
	value        *jsonNode
}
type jsonDocument struct {
	src, clean string
	root       *jsonNode
}

func parseJSON(src string) (*jsonDocument, error) {
	b := []byte(src)
	for i := 0; i < len(b); i++ {
		if b[i] == '"' {
			i++
			for i < len(b) {
				if b[i] == '\\' {
					i += 2
					continue
				}
				if b[i] == '"' {
					break
				}
				i++
			}
			continue
		}
		if b[i] != '/' || i+1 >= len(b) {
			continue
		}
		start := i
		switch b[i+1] {
		case '/':
			i += 2
			for i < len(b) && b[i] != '\n' {
				i++
			}
		case '*':
			i += 2
			for i+1 < len(b) && (b[i] != '*' || b[i+1] != '/') {
				i++
			}
			if i+1 >= len(b) {
				return nil, errors.New(locales.NativeUnterminatedConfigComment())
			}
			i += 2
		default:
			continue
		}
		for j := start; j < i; j++ {
			if b[j] != '\n' && b[j] != '\r' {
				b[j] = ' '
			}
		}
		i--
	}
	commentClean := string(b)
	// JSONC permits a trailing comma, but commas inside strings are data.
	for i := 0; i < len(b); i++ {
		if b[i] == '"' {
			i++
			for i < len(b) {
				if b[i] == '\\' {
					i += 2
					continue
				}
				if b[i] == '"' {
					break
				}
				i++
			}
			continue
		}
		if b[i] == ',' {
			j := i + 1
			for j < len(b) && strings.ContainsRune(" \t\r\n", rune(b[j])) {
				j++
			}
			if j < len(b) && (b[j] == '}' || b[j] == ']') {
				b[i] = ' '
			}
		}
	}
	if !json.Valid(b) {
		return nil, errors.New(locales.NativeInvalidJsonConfig())
	}
	d := &jsonDocument{src: src, clean: commentClean}
	pos := 0
	var parse func() (*jsonNode, error)
	skip := func() {
		for pos < len(b) && strings.ContainsRune(" \t\r\n", rune(b[pos])) {
			pos++
		}
	}
	stringEnd := func() {
		pos++
		for b[pos] != '"' {
			if b[pos] == '\\' {
				pos++
			}
			pos++
		}
		pos++
	}
	parse = func() (*jsonNode, error) {
		skip()
		n := &jsonNode{start: pos, kind: b[pos]}
		switch b[pos] {
		case '{':
			pos++
			skip()
			seen := map[string]bool{}
			for b[pos] != '}' {
				start := pos
				stringEnd()
				var key string
				json.Unmarshal(b[start:pos], &key)
				if seen[key] {
					return nil, errors.New(locales.NativeDuplicateConfigKey(key))
				}
				seen[key] = true
				skip()
				pos++
				v, err := parse()
				if err != nil {
					return nil, err
				}
				skip()
				m := jsonMember{key: key, start: start, comma: -1, value: v}
				if b[pos] == ',' {
					m.comma = pos
					pos++
				}
				n.members = append(n.members, m)
				skip()
			}
			pos++
		case '[':
			pos++
			skip()
			for b[pos] != ']' {
				v, err := parse()
				if err != nil {
					return nil, err
				}
				n.items = append(n.items, v)
				skip()
				if b[pos] == ',' {
					pos++
				}
				skip()
			}
			pos++
		case '"':
			stringEnd()
		default:
			for pos < len(b) && !strings.ContainsRune(",]} \r\n\t", rune(b[pos])) {
				pos++
			}
		}
		n.end = pos
		return n, nil
	}
	var err error
	d.root, err = parse()
	if err != nil {
		return nil, err
	}
	if d.root.kind != '{' {
		return nil, errors.New(locales.NativeConfigRootMustBeObject())
	}
	return d, nil
}

func (d *jsonDocument) at(path []string) (*jsonNode, error) {
	n := d.root
	for _, key := range path {
		if n.kind != '{' {
			return nil, errors.New(locales.NativeConfigKeyNotObject(key))
		}
		var next *jsonNode
		for _, m := range n.members {
			if m.key == key {
				next = m.value
				break
			}
		}
		if next == nil {
			return nil, nil
		}
		n = next
	}
	return n, nil
}
func (d *jsonDocument) raw(path []string) (*string, error) {
	n, err := d.at(path)
	if err != nil || n == nil {
		return nil, err
	}
	s := d.src[n.start:n.end]
	return &s, nil
}
func jsonString(s string) string { b, _ := json.Marshal(s); return string(b) }
func lineIndent(src string, pos int) string {
	start := strings.LastIndex(src[:pos], "\n") + 1
	i := start
	for i < len(src) && (src[i] == ' ' || src[i] == '\t') {
		i++
	}
	return src[start:i]
}
func jsonIndent(d *jsonDocument) string {
	for _, line := range strings.Split(d.src, "\n") {
		trim := strings.TrimLeft(line, " \t")
		if strings.HasPrefix(trim, "\"") && len(trim) < len(line) {
			return line[:len(line)-len(trim)]
		}
	}
	return "  "
}

func editJSON(src string, path []string, value *string) (string, error) {
	d, err := parseJSON(src)
	if err != nil {
		return src, err
	}
	n := d.root
	unit := jsonIndent(d)
	nl := "\n"
	if strings.Contains(src, "\r\n") {
		nl = "\r\n"
	}
	for depth, key := range path {
		if n.kind != '{' {
			return src, errors.New(locales.NativeConfigPathNotObjectUnchanged(strings.Join(path[:depth], ".")))
		}
		idx := -1
		for i, m := range n.members {
			if m.key == key {
				idx = i
				break
			}
		}
		if idx >= 0 {
			m := n.members[idx]
			if depth < len(path)-1 {
				n = m.value
				continue
			}
			if value != nil {
				return src[:m.value.start] + *value + src[m.value.end:], nil
			}
			previous, next := -1, n.end-1
			if idx > 0 {
				previous = n.members[idx-1].value.end
			}
			if idx+1 < len(n.members) {
				next = n.members[idx+1].start
			}
			return removeJSONElement(d, m.start, m.value.end, previous, next), nil
		}
		if value == nil {
			return src, nil
		}
		base := lineIndent(src, n.start)
		child := base + unit
		val := *value
		for j := len(path) - 1; j > depth; j-- {
			indent := child + strings.Repeat(unit, j-depth)
			val = "{" + nl + indent + jsonString(path[j]) + ": " + val + nl + child + strings.Repeat(unit, j-depth-1) + "}"
		}
		entry := jsonString(key) + ": " + val
		multi := strings.Contains(src[n.start:n.end], "\n") || len(n.members) == 0
		if !multi {
			prefix := ""
			if len(n.members) > 0 {
				prefix = ", "
			}
			pos := n.end - 1
			return src[:pos] + prefix + entry + src[pos:], nil
		}
		pos := n.end - 1
		closeIndent := lineIndent(src, pos)
		if strings.TrimSpace(src[strings.LastIndex(src[:pos], "\n")+1:pos]) == "" {
			pos -= len(closeIndent)
		}
		insert := child + entry + nl
		if len(n.members) == 0 {
			if !strings.Contains(src[n.start:pos], "\n") {
				insert = nl + insert + base
			}
			return src[:pos] + insert + src[pos:], nil
		}
		last := n.members[len(n.members)-1]
		// Keep a JSONC trailing comma if there already is one.
		if strings.Contains(d.clean[last.value.end:pos], ",") {
			insert = child + entry + "," + nl
			return src[:pos] + insert + src[pos:], nil
		}
		return src[:last.value.end] + "," + src[last.value.end:pos] + insert + src[pos:], nil
	}
	return src, nil
}

// Remove only the element, its separator, and otherwise empty line; keep sibling comments.
func removeJSONElement(d *jsonDocument, start, end, previous, next int) string {
	src := d.src
	comma := strings.IndexByte(d.clean[end:next], ',')
	if comma >= 0 {
		end += comma + 1
	}
	lineStart := strings.LastIndex(src[:start], "\n") + 1
	if strings.TrimSpace(src[lineStart:start]) == "" {
		start = lineStart
	}
	j := end
	for j < len(src) && (src[j] == ' ' || src[j] == '\t' || src[j] == '\r') {
		j++
	}
	if j < len(src) && src[j] == '\n' {
		end = j + 1
	}
	if comma < 0 && previous >= 0 {
		previous += strings.IndexByte(d.clean[previous:start], ',')
		if !strings.Contains(src[previous+1:start], "\n") && strings.TrimSpace(src[previous+1:start]) == "" {
			start = previous + 1
		}
		return src[:previous] + src[previous+1:start] + src[end:]
	}
	return src[:start] + src[end:]
}

func editJSONArray(src string, path []string, name string, remove bool) (string, error) {
	d, err := parseJSON(src)
	if err != nil {
		return src, err
	}
	n, err := d.at(path)
	if err != nil {
		return src, err
	}
	if n == nil {
		if remove {
			return src, nil
		}
		value := "[" + jsonString(name) + "]"
		return editJSON(src, path, &value)
	}
	if n.kind != '[' {
		return src, errors.New(locales.NativePathMustBeNameArray(strings.Join(path, ".")))
	}
	for i, item := range n.items {
		var value string
		if err := json.Unmarshal([]byte(src[item.start:item.end]), &value); err != nil {
			return src, err
		}
		if !strings.EqualFold(value, name) {
			continue
		}
		if !remove {
			return src, nil
		}
		previous, next := -1, n.end-1
		if i > 0 {
			previous = n.items[i-1].end
		}
		if i+1 < len(n.items) {
			next = n.items[i+1].start
		}
		return editJSONArray(removeJSONElement(d, item.start, item.end, previous, next), path, name, true)
	}
	if remove {
		return src, nil
	}
	pos := n.end - 1
	entry := jsonString(name)
	last := n.start + 1
	if len(n.items) > 0 {
		last = n.items[len(n.items)-1].end
	}
	trailing := strings.Contains(d.clean[last:pos], ",")
	if strings.Contains(src[n.start:pos], "\n") {
		nl := "\n"
		if strings.Contains(src, "\r\n") {
			nl = "\r\n"
		}
		indent := lineIndent(src, n.start) + jsonIndent(d)
		if len(n.items) > 0 {
			indent = lineIndent(src, n.items[0].start)
		}
		if strings.TrimSpace(src[strings.LastIndex(src[:pos], "\n")+1:pos]) == "" {
			pos -= len(lineIndent(src, pos))
		}
		if trailing {
			entry += ","
		}
		entry = indent + entry + nl
	} else if trailing {
		pos = last + strings.IndexByte(d.clean[last:pos], ',') + 1
		entry += ","
		return src[:pos] + entry + src[pos:], nil
	}
	if len(n.items) > 0 && !trailing {
		return src[:last] + "," + src[last:pos] + entry + src[pos:], nil
	}
	return src[:pos] + entry + src[pos:], nil
}

func missingJSONParents(d *jsonDocument, path []string) ([][]string, error) {
	var out [][]string
	for i := 1; i < len(path); i++ {
		n, err := d.at(path[:i])
		if err != nil {
			return nil, err
		}
		if n == nil {
			out = append(out, append([]string(nil), path[:i]...))
		} else if n.kind != '{' {
			return nil, errors.New(locales.NativeConfigPathNotObject(strings.Join(path[:i], ".")))
		}
	}
	return out, nil
}
func pruneJSON(src string, parents [][]string) (string, error) {
	for i := len(parents) - 1; i >= 0; i-- {
		d, err := parseJSON(src)
		if err != nil {
			return src, err
		}
		n, err := d.at(parents[i])
		if err != nil {
			return src, err
		}
		if n != nil && (n.kind == '{' && len(n.members) == 0 || n.kind == '[' && len(n.items) == 0) {
			src, err = editJSON(src, parents[i], nil)
			if err != nil {
				return src, err
			}
		}
	}
	return src, nil
}
