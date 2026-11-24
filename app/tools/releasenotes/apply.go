package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

// 模型把更新说明包在这对标签里输出，前后的思考和寒暄一律丢掉。
const (
	notesOpenTag  = "<release-notes>"
	notesCloseTag = "</release-notes>"
)

// 界面规则禁止用来拼接信息的点号，写成转义免得本文件自己被点号检查拦下。
const dotSeparators = "\u00b7\u2022\u2027\u22c5\u30fb"

var numberedItemRe = regexp.MustCompile(`^(\d+)\. \S`)

// 更新窗口里一条说明只该是一句短话；超过这个长度基本是把实现细节也写进去了。
const (
	maxZHItemRunes = 50
	maxENItemRunes = 140
)

// runApply 把模型写的更新说明换进 CHANGELOG 的 "## <版本>" 一节。
// 说明不合格时返回错误且不改文件，发版 PR 保留 bump 生成的 commit 原文草稿。
func runApply(args []string) error {
	fs := flag.NewFlagSet("apply", flag.ContinueOnError)
	var version, changelogPath, notesPath string
	fs.StringVar(&version, "version", "", "release version whose section is replaced (required)")
	fs.StringVar(&changelogPath, "changelog", "CHANGELOG.md", "path to app/CHANGELOG.md")
	fs.StringVar(&notesPath, "notes", "-", "model output containing <release-notes>…</release-notes>, or - for stdin")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return fmt.Errorf("unexpected arguments: %v", fs.Args())
	}
	v, err := parseSemver(version)
	if err != nil {
		return err
	}
	raw, err := readInput(notesPath)
	if err != nil {
		return fmt.Errorf("read notes: %w", err)
	}
	notes, err := extractTaggedNotes(raw)
	if err != nil {
		return err
	}
	if err := lintReleaseNotes(notes); err != nil {
		return fmt.Errorf("release notes rejected: %w", err)
	}
	b, err := os.ReadFile(changelogPath)
	if err != nil {
		return fmt.Errorf("read %s: %w", changelogPath, err)
	}
	updated, err := replaceChangelogSection(string(b), v.String(), notes)
	if err != nil {
		return fmt.Errorf("%s: %w", changelogPath, err)
	}
	return os.WriteFile(changelogPath, []byte(updated), 0o644)
}

// extractTaggedNotes 取最后一对 <release-notes> 标签之间的内容。
func extractTaggedNotes(output string) (string, error) {
	end := strings.LastIndex(output, notesCloseTag)
	if end < 0 {
		return "", fmt.Errorf("missing %s", notesCloseTag)
	}
	start := strings.LastIndex(output[:end], notesOpenTag)
	if start < 0 {
		return "", fmt.Errorf("missing %s", notesOpenTag)
	}
	notes := strings.TrimSpace(output[start+len(notesOpenTag) : end])
	if notes == "" {
		return "", errors.New("release notes are empty")
	}
	return notes, nil
}

// lintReleaseNotes 检查更新窗口能正确显示、且确实被改写过的说明：
// 中英文两节条数相同，都是从 1 开始的单行编号列表，中文一节是中文、英文一节没有中文，
// 每条是不超长的一句话，不用点号拼接，也不是原样照抄的 Conventional Commits 标题。
func lintReleaseNotes(notes string) error {
	zh, en, err := splitBilingual(notes)
	if err != nil {
		return err
	}
	zhItems, err := numberedItems(zh)
	if err != nil {
		return fmt.Errorf("中文: %w", err)
	}
	enItems, err := numberedItems(en)
	if err != nil {
		return fmt.Errorf("English: %w", err)
	}
	if len(zhItems) != len(enItems) {
		return fmt.Errorf("中文 has %d items but English has %d", len(zhItems), len(enItems))
	}
	for i := range zhItems {
		if !containsHan(zhItems[i]) {
			return fmt.Errorf("中文 item %d has no Chinese text: %q", i+1, zhItems[i])
		}
		if containsHan(enItems[i]) {
			return fmt.Errorf("English item %d contains Chinese text: %q", i+1, enItems[i])
		}
		if n := utf8.RuneCountInString(zhItems[i]); n > maxZHItemRunes {
			return fmt.Errorf("中文 item %d has %d characters, keep it under %d: %q", i+1, n, maxZHItemRunes, zhItems[i])
		}
		if strings.Contains(strings.TrimSuffix(zhItems[i], "。"), "。") {
			return fmt.Errorf("中文 item %d has more than one sentence: %q", i+1, zhItems[i])
		}
		if n := utf8.RuneCountInString(enItems[i]); n > maxENItemRunes {
			return fmt.Errorf("English item %d has %d characters, keep it under %d: %q", i+1, n, maxENItemRunes, enItems[i])
		}
		for _, item := range []string{zhItems[i], enItems[i]} {
			if strings.ContainsAny(item, dotSeparators) {
				return fmt.Errorf("item %d joins information with a dot separator: %q", i+1, item)
			}
			if conventionalHeaderRe.MatchString(item) {
				return fmt.Errorf("item %d is a raw commit header: %q", i+1, item)
			}
		}
	}
	return nil
}

func numberedItems(md string) ([]string, error) {
	var items []string
	for _, line := range splitLines(md) {
		if strings.TrimSpace(line) == "" {
			continue
		}
		m := numberedItemRe.FindStringSubmatch(line)
		if m == nil {
			return nil, fmt.Errorf("line %q is not a numbered list item", strings.TrimSpace(line))
		}
		if n, _ := strconv.Atoi(m[1]); n != len(items)+1 {
			return nil, fmt.Errorf("item %q should be numbered %d", strings.TrimSpace(line), len(items)+1)
		}
		items = append(items, strings.TrimSpace(line[len(m[1])+2:]))
	}
	if len(items) == 0 {
		return nil, errors.New("no list items")
	}
	return items, nil
}

func containsHan(s string) bool {
	for _, r := range s {
		if unicode.Is(unicode.Han, r) {
			return true
		}
	}
	return false
}

// replaceChangelogSection 把 "## <version>" 标题下、下一个 "## " 标题前的正文换成 body，
// 标题识别规则与 extractChangelogSection 一致。
func replaceChangelogSection(changelog, version, body string) (string, error) {
	lines := splitLines(changelog)
	start, end := -1, len(lines)
	for i, line := range lines {
		rest, ok := strings.CutPrefix(line, "## ")
		if !ok {
			continue
		}
		if start >= 0 {
			end = i
			break
		}
		fields := strings.Fields(rest)
		if len(fields) > 0 && strings.TrimPrefix(headingVersion(fields[0]), "v") == version {
			start = i
		}
	}
	if start < 0 {
		return "", fmt.Errorf("missing \"## %s\" section", version)
	}
	out := strings.Join(lines[:start+1], "\n") + "\n\n" + strings.TrimSpace(body) + "\n"
	if end < len(lines) {
		out += "\n" + strings.Join(lines[end:], "\n")
	}
	return strings.TrimRight(out, "\n") + "\n", nil
}
