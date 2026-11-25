package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"html"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

type faq struct{ Q, A string }

// 页面里的问答统一写成 <details class="qa"><summary>问题</summary>回答</details>，
// FAQPage 结构化数据从渲染结果里取，问答只写一遍。
var (
	qaRe  = regexp.MustCompile(`(?s)<details class="qa"[^>]*>\s*<summary>(.*?)</summary>(.*?)</details>`)
	tagRe = regexp.MustCompile(`<[^>]+>`)
)

func faqs(main string) []faq {
	var out []faq
	for _, m := range qaRe.FindAllStringSubmatch(main, -1) {
		out = append(out, faq{Q: plain(m[1]), A: plain(m[2])})
	}
	return out
}

func plain(s string) string {
	return strings.Join(strings.Fields(html.UnescapeString(tagRe.ReplaceAllString(s, " "))), " ")
}

type obj = map[string]any

func schemaFor(v view, cat catalog, qs []faq) (string, error) {
	site := v.Base + "/"
	author := obj{"@type": "Person", "name": "DBAAZzz", "url": "https://github.com/DBAAZzz"}
	graph := []obj{}
	switch v.Page.Kind {
	case "home":
		graph = append(graph,
			obj{"@type": "WebSite", "@id": site + "#website", "url": v.Base + v.Lang.Prefix + "/", "name": "Skill Stow", "inLanguage": v.Lang.Tag},
			obj{
				"@type": "SoftwareApplication", "name": "Skill Stow", "url": v.URL,
				"description": v.Description, "applicationCategory": "DeveloperApplication",
				"operatingSystem": "macOS 12 or later", "softwareVersion": v.Version,
				"downloadUrl": v.Download, "license": repoURL + "/blob/main/LICENSE",
				"isAccessibleForFree": true, "image": v.Base + "/icon.png",
				"screenshot": v.Base + "/img/" + v.Lang.Code + "/main.png", "author": author,
				"offers": obj{"@type": "Offer", "price": "0", "priceCurrency": "USD"},
				"sameAs": []string{repoURL},
			})
	case "404":
		return "{}", nil
	default:
		graph = append(graph, obj{
			"@type": "TechArticle", "headline": v.Title, "description": v.Description,
			"url": v.URL, "inLanguage": v.Lang.Tag, "dateModified": v.Updated,
			"author": author, "image": v.Base + "/og.png",
			"publisher": obj{"@type": "Organization", "name": "Skill Stow", "logo": obj{"@type": "ImageObject", "url": v.Base + "/icon.png"}},
		})
		crumbs := []obj{{"@type": "ListItem", "position": 1, "name": "Skill Stow", "item": v.Base + v.Lang.Prefix + "/"}}
		if v.Page.Kind == "agent" {
			crumbs = append(crumbs, obj{"@type": "ListItem", "position": 2, "name": cat.Strings["nav.agents"], "item": v.Base + v.Lang.Prefix + "/agents/"})
		}
		crumbs = append(crumbs, obj{"@type": "ListItem", "position": len(crumbs) + 1, "name": v.Title, "item": v.URL})
		graph = append(graph, obj{"@type": "BreadcrumbList", "itemListElement": crumbs})
	}
	if len(qs) > 0 {
		var items []obj
		for _, q := range qs {
			items = append(items, obj{"@type": "Question", "name": q.Q, "acceptedAnswer": obj{"@type": "Answer", "text": q.A}})
		}
		graph = append(graph, obj{"@type": "FAQPage", "mainEntity": items})
	}
	b, err := json.Marshal(obj{"@context": "https://schema.org", "@graph": graph})
	return string(b), err
}

func writeSitemap(c config, pages []rendered) error {
	var b bytes.Buffer
	b.WriteString(`<?xml version="1.0" encoding="UTF-8"?>` + "\n")
	b.WriteString(`<urlset xmlns="http://www.sitemaps.org/schemas/sitemap/0.9" xmlns:xhtml="http://www.w3.org/1999/xhtml">` + "\n")
	for _, p := range pages {
		if p.Page.NoIndex {
			continue
		}
		fmt.Fprintf(&b, "<url>\n<loc>%s%s</loc>\n<lastmod>%s</lastmod>\n", c.Base, p.URLPath, p.Updated)
		for _, l := range langs {
			fmt.Fprintf(&b, `<xhtml:link rel="alternate" hreflang="%s" href="%s%s%s"/>`+"\n", l.Tag, c.Base, l.Prefix, p.Page.Path)
		}
		fmt.Fprintf(&b, `<xhtml:link rel="alternate" hreflang="x-default" href="%s%s"/>`+"\n</url>\n", c.Base, p.Page.Path)
	}
	b.WriteString("</urlset>\n")
	return os.WriteFile(filepath.Join(c.Out, "sitemap.xml"), b.Bytes(), 0o644)
}

func writeRobots(c config) error {
	s := "User-agent: *\nAllow: /\n\nSitemap: " + c.Base + "/sitemap.xml\n"
	return os.WriteFile(filepath.Join(c.Out, "robots.txt"), []byte(s), 0o644)
}

// writeLLMs 按 llmstxt.org 的格式列出英文页面，给 AI 搜索读。
func writeLLMs(c config, pages []rendered) error {
	var b bytes.Buffer
	b.WriteString("# Skill Stow\n\n")
	b.WriteString("> Free, open-source Mac app that manages the skills (SKILL.md folders) of 32 AI coding agents, including Claude Code, Codex, Cursor and Gemini CLI: see which agent reads which skill, turn skills on or off per agent, run an offline security check before installing, update with a diff, and clean up duplicates and broken links. macOS 12+, no account, no telemetry. MIT licensed.\n\n")
	fmt.Fprintf(&b, "- Download: %s\n- Source code: %s\n- 中文版: %s/zh/\n", downloadURL, repoURL, c.Base)
	section := func(title string, keep func(rendered) bool) {
		fmt.Fprintf(&b, "\n## %s\n\n", title)
		for _, p := range pages {
			if p.Lang.Prefix == "" && !p.Page.NoIndex && keep(p) {
				fmt.Fprintf(&b, "- [%s](%s%s): %s\n", p.Title, c.Base, p.URLPath, p.Description)
			}
		}
	}
	section("Pages", func(p rendered) bool { return p.Page.Kind != "agent" })
	section("Where each agent keeps its skills", func(p rendered) bool { return p.Page.Kind == "agent" })
	return os.WriteFile(filepath.Join(c.Out, "llms.txt"), b.Bytes(), 0o644)
}
