package main

import (
	"bytes"
	"errors"
	"html"
	"regexp"
	"strings"

	"skillmanager/internal/library"
)

// reservedSkillRoots 排除 skills.sh 上也恰为三段路径的非技能页面路由，避免当作 owner/repo/skill 请求。
var reservedSkillRoots = map[string]bool{
	"_next":          true,
	"about":          true,
	"agent":          true,
	"agents":         true,
	"api":            true,
	"assets":         true,
	"audits":         true,
	"contact":        true,
	"debug-security": true,
	"docs":           true,
	"hot":            true,
	"images":         true,
	"internal":       true,
	"official":       true,
	"package":        true,
	"packs":          true,
	"picks":          true,
	"privacy":        true,
	"search":         true,
	"site":           true,
	"static":         true,
	"terms":          true,
	"topic":          true,
	"topics":         true,
	"trending":       true,
}

// genericSiteDescriptions 是未写明简介的技能页回退到站点通用描述时的文案，不应写入补充文件。
var genericSiteDescriptions = map[string]bool{
	"Discover and install skills for AI agents.": true,
	"Discover and install skills for AI agents":  true,
}

var (
	reAttrHref        = regexp.MustCompile(`(?i)\bhref\s*=\s*(?:"([^"]*)"|'([^']*)')`)
	reTopicLink       = regexp.MustCompile(`(?is)<a\b([^>]*)>(.*?)</a>`)
	reH1Tag           = regexp.MustCompile(`(?is)<h1\b[^>]*>(.*?)</h1>`)
	reH2Tag           = regexp.MustCompile(`(?is)<h2\b[^>]*>(.*?)</h2>`)
	rePTag            = regexp.MustCompile(`(?is)<p\b([^>]*)>(.*?)</p>`)
	reMetaTag         = regexp.MustCompile(`(?is)<meta\b([^>]*)>`)
	reHTMLComment     = regexp.MustCompile(`(?s)<!--.*?-->`)
	reHTMLTag         = regexp.MustCompile(`(?s)<[^>]+>`)
	reAttrPair        = regexp.MustCompile(`([a-zA-Z_:][a-zA-Z0-9_.:-]*)\s*=\s*(?:"([^"]*)"|'([^']*)'|([^\s"'=<>` + "`" + `]+))`)
	reRSCSkillEscaped = regexp.MustCompile(`\\"source\\":\\"([^\\"]+)\\",\\"skillId\\":\\"([^\\"]+)\\"`)
	reRSCSkillPlain   = regexp.MustCompile(`"source":"([^"]+)","skillId":"([^"]+)"`)
)

// parseSkillSlug 验证并规范化三段技能路径 owner/repo/skill。
func parseSkillSlug(raw string) (string, bool) {
	s := strings.TrimSpace(raw)
	if idx := strings.IndexAny(s, "?#"); idx >= 0 {
		s = s[:idx]
	}
	s = strings.Trim(s, "/")
	parts := strings.Split(s, "/")
	if len(parts) != 3 {
		return "", false
	}
	if reservedSkillRoots[strings.ToLower(parts[0])] {
		return "", false
	}
	for _, p := range parts {
		if p == "" || p == "." || p == ".." {
			return "", false
		}
		if strings.ContainsAny(p, " \t\r\n<>\"'\\") {
			return "", false
		}
	}
	return parts[0] + "/" + parts[1] + "/" + parts[2], true
}

// extractSkillsFromListHTML 从 skills.sh 榜单 HTML（含 SSR 锚点与 Next.js RSC 数据）中提取去重后的技能 ID。
func extractSkillsFromListHTML(raw []byte) []string {
	seen := make(map[string]bool)
	var out []string
	add := func(candidate string) {
		id, ok := parseSkillSlug(candidate)
		if !ok || seen[id] {
			return
		}
		seen[id] = true
		out = append(out, id)
	}

	s := string(raw)
	for _, m := range reAttrHref.FindAllStringSubmatch(s, -1) {
		href := m[1]
		if href == "" {
			href = m[2]
		}
		if strings.HasPrefix(href, "/") && !strings.HasPrefix(href, "//") {
			add(href)
		}
	}
	for _, m := range reRSCSkillEscaped.FindAllStringSubmatch(s, -1) {
		add(m[1] + "/" + m[2])
	}
	for _, m := range reRSCSkillPlain.FindAllStringSubmatch(s, -1) {
		add(m[1] + "/" + m[2])
	}
	return out
}

// extractTopicsIndexHTML 从 /topic 索引页提取专题列表与卡片摘要。
func extractTopicsIndexHTML(raw []byte) ([]library.MarketTopic, error) {
	s := string(raw)
	seen := make(map[string]bool)
	var topics []library.MarketTopic
	for _, m := range reTopicLink.FindAllStringSubmatch(s, -1) {
		href := extractAttr(m[1], "href")
		id, ok := parseTopicHref(href)
		if !ok || seen[id] {
			continue
		}
		body := m[2]
		// 仅识别带 <h2> 标题的专题卡片，忽略页眉/页脚里的纯文字专题链接。
		h2 := reH2Tag.FindStringSubmatch(body)
		if len(h2) < 2 {
			continue
		}
		title := cleanHTMLText(h2[1])
		if title == "" {
			continue
		}
		var summary string
		if p := rePTag.FindStringSubmatch(body); len(p) >= 3 {
			summary = cleanHTMLText(p[2])
		}
		seen[id] = true
		topics = append(topics, library.MarketTopic{
			ID:      id,
			Title:   title,
			Summary: summary,
		})
	}
	if len(topics) == 0 {
		return nil, errors.New("no topics found in /topic HTML")
	}
	return topics, nil
}

// extractTopicDetailHTML 从 /topic/<id> 页面提取专题详情及其包含技能的用途说明。
func extractTopicDetailHTML(id string, raw []byte) (library.MarketTopic, error) {
	s := string(raw)
	t := library.MarketTopic{ID: id}
	if h1 := reH1Tag.FindStringSubmatch(s); len(h1) >= 2 {
		t.Title = cleanHTMLText(h1[1])
		if loc := reH1Tag.FindStringIndex(s); loc != nil {
			tail := s[loc[1]:]
			if end := strings.Index(strings.ToLower(tail), "</header>"); end >= 0 {
				tail = tail[:end]
			}
			if p := rePTag.FindStringSubmatch(tail); len(p) >= 3 {
				t.Summary = cleanHTMLText(p[2])
			}
		}
	}

	seen := make(map[string]bool)
	for _, m := range reTopicLink.FindAllStringSubmatch(s, -1) {
		href := extractAttr(m[1], "href")
		skillID, ok := parseSkillSlug(href)
		if !ok || seen[skillID] {
			continue
		}
		body := m[2]
		ps := rePTag.FindAllStringSubmatch(body, -1)
		if len(ps) == 0 {
			continue
		}
		// 优先取 col-span-9 用途段落；若样式类名变更，则退回卡片里最后一个 <p>（首个通常是 owner/repo）。
		var what string
		for _, p := range ps {
			if strings.Contains(p[1], "col-span-9") {
				what = cleanHTMLText(p[2])
				break
			}
		}
		if what == "" {
			what = cleanHTMLText(ps[len(ps)-1][2])
		}
		seen[skillID] = true
		t.Skills = append(t.Skills, library.MarketTopicSkill{
			ID:   skillID,
			What: what,
		})
	}
	if len(t.Skills) == 0 {
		return t, errors.New("no skills found in topic HTML")
	}
	return t, nil
}

// extractMetaDescription 从技能页提取 <meta name="description"> 文本，忽略站点通用占位简介。
func extractMetaDescription(raw []byte) string {
	s := string(raw)
	if idx := bytes.Index(bytes.ToLower(raw), []byte("</head>")); idx >= 0 {
		s = s[:idx]
	}
	var desc, ogDesc string
	for _, m := range reMetaTag.FindAllStringSubmatch(s, -1) {
		attrs := parseHTMLAttrs(m[1])
		content := normalizeMetaContent(attrs["content"])
		if content == "" || genericSiteDescriptions[content] {
			continue
		}
		if strings.EqualFold(attrs["name"], "description") && desc == "" {
			desc = content
		} else if strings.EqualFold(attrs["property"], "og:description") && ogDesc == "" {
			ogDesc = content
		}
	}
	if desc != "" {
		return desc
	}
	return ogDesc
}

func parseTopicHref(href string) (string, bool) {
	if idx := strings.IndexAny(href, "?#"); idx >= 0 {
		href = href[:idx]
	}
	href = strings.TrimSuffix(strings.TrimSpace(href), "/")
	id, ok := strings.CutPrefix(href, "/topic/")
	if !ok || id == "" || strings.Contains(id, "/") {
		return "", false
	}
	if strings.ContainsAny(id, " \t\r\n<>\"'\\") {
		return "", false
	}
	return id, true
}

func parseHTMLAttrs(attrText string) map[string]string {
	out := make(map[string]string)
	for _, m := range reAttrPair.FindAllStringSubmatch(attrText, -1) {
		val := m[2]
		if val == "" && m[3] != "" {
			val = m[3]
		} else if val == "" && m[4] != "" {
			val = m[4]
		}
		out[strings.ToLower(m[1])] = html.UnescapeString(val)
	}
	return out
}

func extractAttr(attrText, key string) string {
	return parseHTMLAttrs(attrText)[strings.ToLower(key)]
}

func cleanHTMLText(raw string) string {
	noComment := reHTMLComment.ReplaceAllString(raw, "")
	noTag := reHTMLTag.ReplaceAllString(noComment, "")
	return normalizeMetaContent(html.UnescapeString(noTag))
}

func normalizeMetaContent(s string) string {
	return strings.Join(strings.Fields(strings.TrimSpace(s)), " ")
}
