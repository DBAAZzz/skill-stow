package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"regexp"
	"strconv"
	"strings"
)

type semver struct {
	major int
	minor int
	patch int
}

func parseSemver(s string) (semver, error) {
	s = strings.TrimPrefix(strings.TrimSpace(s), "v")
	parts := strings.Split(s, ".")
	if len(parts) != 3 {
		return semver{}, fmt.Errorf("invalid semver %q: want X.Y.Z", s)
	}
	maj, err1 := strconv.Atoi(parts[0])
	min, err2 := strconv.Atoi(parts[1])
	pat, err3 := strconv.Atoi(parts[2])
	if err1 != nil || err2 != nil || err3 != nil || maj < 0 || min < 0 || pat < 0 {
		return semver{}, fmt.Errorf("invalid semver %q: want non-negative integers", s)
	}
	return semver{major: maj, minor: min, patch: pat}, nil
}

func (v semver) String() string {
	return fmt.Sprintf("%d.%d.%d", v.major, v.minor, v.patch)
}

func compareSemver(a, b semver) int {
	if a.major != b.major {
		if a.major < b.major {
			return -1
		}
		return 1
	}
	if a.minor != b.minor {
		if a.minor < b.minor {
			return -1
		}
		return 1
	}
	if a.patch != b.patch {
		if a.patch < b.patch {
			return -1
		}
		return 1
	}
	return 0
}

type releaseCommit struct {
	SHA      string `json:"sha"`
	Type     string `json:"type"`
	Scope    string `json:"scope,omitempty"`
	Subject  string `json:"subject"`
	Header   string `json:"header"`
	Breaking bool   `json:"breaking,omitempty"`
}

var conventionalHeaderRe = regexp.MustCompile(`^([a-z]+)(?:\(([^)]+)\))?(!)?:\s+(.+)$`)

// parseGitLog 解析 `git log --format=%H%x1f%s%x1f%b%x1e` 输出，提取会触发发版的 Conventional Commits。
func parseGitLog(raw string) []releaseCommit {
	var feats, others []releaseCommit
	for _, rec := range strings.Split(raw, "\x1e") {
		rec = strings.TrimSpace(rec)
		if rec == "" {
			continue
		}
		fields := strings.SplitN(rec, "\x1f", 3)
		if len(fields) < 2 {
			continue
		}
		sha := strings.TrimSpace(fields[0])
		if len(sha) > 7 {
			sha = sha[:7]
		}
		header := strings.TrimSpace(fields[1])
		body := ""
		if len(fields) == 3 {
			body = fields[2]
		}
		m := conventionalHeaderRe.FindStringSubmatch(header)
		if m == nil {
			continue
		}
		typ := m[1]
		scope := strings.TrimSpace(m[2])
		breaking := m[3] == "!" || hasBreakingFooter(body)
		subject := strings.TrimSpace(m[4])

		if !breaking {
			switch typ {
			case "feat", "fix", "perf", "revert":
			default:
				continue
			}
		}

		c := releaseCommit{
			SHA:      sha,
			Type:     typ,
			Scope:    scope,
			Subject:  subject,
			Header:   header,
			Breaking: breaking,
		}
		if breaking || typ == "feat" {
			feats = append(feats, c)
		} else {
			others = append(others, c)
		}
	}
	return append(feats, others...)
}

func hasBreakingFooter(body string) bool {
	for _, line := range splitLines(body) {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "BREAKING CHANGE:") || strings.HasPrefix(trimmed, "BREAKING-CHANGE:") {
			return true
		}
	}
	return false
}

func nextVersion(cur semver, commits []releaseCommit) (semver, bool) {
	if len(commits) == 0 {
		return semver{}, false
	}
	hasBreaking, hasFeat := false, false
	for _, c := range commits {
		if c.Breaking {
			hasBreaking = true
		}
		if c.Type == "feat" {
			hasFeat = true
		}
	}
	switch {
	case hasBreaking:
		if cur.major >= 1 {
			return semver{major: cur.major + 1, minor: 0, patch: 0}, true
		}
		return semver{major: 0, minor: cur.minor + 1, patch: 0}, true
	case hasFeat:
		return semver{major: cur.major, minor: cur.minor + 1, patch: 0}, true
	default:
		return semver{major: cur.major, minor: cur.minor, patch: cur.patch + 1}, true
	}
}

type planResult struct {
	Action         string          `json:"action"` // "tag" | "pr" | "noop"
	CurrentVersion string          `json:"current_version"`
	NextVersion    string          `json:"next_version,omitempty"`
	Tag            string          `json:"tag,omitempty"`
	Commits        []releaseCommit `json:"commits,omitempty"`
}

func runPlan(args []string) error {
	fs := flag.NewFlagSet("plan", flag.ContinueOnError)
	var mygoPath, latestTag, commitsPath string
	var tagExists bool
	fs.StringVar(&mygoPath, "mygo", "mygo.json", "path to app/mygo.json")
	fs.StringVar(&latestTag, "latest-tag", "", "latest v* tag in repository (optional)")
	fs.BoolVar(&tagExists, "tag-exists", false, "whether tag v<mygo.version> already exists")
	fs.StringVar(&commitsPath, "commits", "-", "path to git log output, or - for stdin")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return fmt.Errorf("unexpected arguments: %v", fs.Args())
	}

	curVersion, err := readMygoVersion(mygoPath)
	if err != nil {
		return err
	}
	commitsRaw, err := readInput(commitsPath)
	if err != nil {
		return fmt.Errorf("read commits: %w", err)
	}
	res, err := computePlan(curVersion, latestTag, tagExists, commitsRaw)
	if err != nil {
		return err
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(res)
}

func computePlan(curVersionStr, latestTag string, tagExists bool, commitsRaw string) (planResult, error) {
	cur, err := parseSemver(curVersionStr)
	if err != nil {
		return planResult{}, fmt.Errorf("mygo.json version: %w", err)
	}
	if !tagExists {
		if strings.TrimSpace(latestTag) != "" {
			latest, err := parseSemver(latestTag)
			if err != nil {
				return planResult{}, fmt.Errorf("latest tag %q: %w", latestTag, err)
			}
			if compareSemver(cur, latest) <= 0 {
				return planResult{}, fmt.Errorf("mygo.json version %s is not newer than latest tag %s", cur, latestTag)
			}
		}
		return planResult{
			Action:         "tag",
			CurrentVersion: cur.String(),
			NextVersion:    cur.String(),
			Tag:            "v" + cur.String(),
		}, nil
	}

	commits := parseGitLog(commitsRaw)
	next, ok := nextVersion(cur, commits)
	if !ok {
		return planResult{
			Action:         "noop",
			CurrentVersion: cur.String(),
		}, nil
	}
	return planResult{
		Action:         "pr",
		CurrentVersion: cur.String(),
		NextVersion:    next.String(),
		Tag:            "v" + next.String(),
		Commits:        commits,
	}, nil
}

func runBump(args []string) error {
	fs := flag.NewFlagSet("bump", flag.ContinueOnError)
	var version, mygoPath, changelogPath, commitsPath, latestTag, prBodyOut string
	fs.StringVar(&version, "version", "", "target release version without leading v (required)")
	fs.StringVar(&mygoPath, "mygo", "mygo.json", "path to app/mygo.json")
	fs.StringVar(&changelogPath, "changelog", "CHANGELOG.md", "path to app/CHANGELOG.md")
	fs.StringVar(&commitsPath, "commits", "-", "path to git log output, or - for stdin")
	fs.StringVar(&latestTag, "latest-tag", "", "previous version tag for PR description (optional)")
	fs.StringVar(&prBodyOut, "pr-body-out", "", "optional path to write PR description markdown")
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
	commitsRaw, err := readInput(commitsPath)
	if err != nil {
		return fmt.Errorf("read commits: %w", err)
	}
	commits := parseGitLog(commitsRaw)
	if len(commits) == 0 {
		return errors.New("no release-worthy commits found")
	}

	if err := updateMygoVersion(mygoPath, v.String()); err != nil {
		return err
	}
	if err := prependChangelogDraft(changelogPath, v.String(), commits); err != nil {
		return err
	}
	if prBodyOut != "" {
		body := renderPRBody(v.String(), latestTag, commits)
		if err := os.WriteFile(prBodyOut, []byte(body), 0o644); err != nil {
			return fmt.Errorf("write pr body %s: %w", prBodyOut, err)
		}
	}
	return nil
}

func readMygoVersion(path string) (string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read %s: %w", path, err)
	}
	var cfg struct {
		Version string `json:"version"`
	}
	if err := json.Unmarshal(b, &cfg); err != nil {
		return "", fmt.Errorf("parse %s: %w", path, err)
	}
	if strings.TrimSpace(cfg.Version) == "" {
		return "", fmt.Errorf("%s has empty version", path)
	}
	return cfg.Version, nil
}

var mygoVersionLineRe = regexp.MustCompile(`(?m)^(\s*"version"\s*:\s*")([^"]+)("\s*,?\s*)$`)

func updateMygoVersion(path, newVersion string) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read %s: %w", path, err)
	}
	matches := mygoVersionLineRe.FindAllSubmatchIndex(b, -1)
	if len(matches) != 1 {
		return fmt.Errorf("%s: expected 1 top-level version field, found %d", path, len(matches))
	}
	updated := mygoVersionLineRe.ReplaceAll(b, []byte("${1}"+newVersion+"${3}"))
	var check struct {
		Version string `json:"version"`
	}
	if err := json.Unmarshal(updated, &check); err != nil || check.Version != newVersion {
		return fmt.Errorf("%s: failed to verify updated version", path)
	}
	return os.WriteFile(path, updated, 0o644)
}

func prependChangelogDraft(path, version string, commits []releaseCommit) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read %s: %w", path, err)
	}
	content := strings.ReplaceAll(string(b), "\r\n", "\n")
	for _, existing := range listChangelogVersions(content) {
		if existing == version {
			return fmt.Errorf("%s already has a \"## %s\" section", path, version)
		}
	}

	var listBuf bytes.Buffer
	for i, c := range commits {
		fmt.Fprintf(&listBuf, "%d. %s\n", i+1, c.Header)
	}
	list := strings.TrimRight(listBuf.String(), "\n")
	section := fmt.Sprintf("## %s\n\n中文\n\n%s\n\nEnglish\n\n%s\n", version, list, list)

	idx := strings.Index(content, "\n## ")
	var updated string
	if idx >= 0 {
		prefix := strings.TrimRight(content[:idx], "\n")
		rest := strings.TrimLeft(content[idx+1:], "\n")
		updated = prefix + "\n\n" + section + "\n" + rest
	} else {
		updated = strings.TrimRight(content, "\n") + "\n\n" + section
	}
	if !strings.HasSuffix(updated, "\n") {
		updated += "\n"
	}
	return os.WriteFile(path, []byte(updated), 0o644)
}

func renderPRBody(version, latestTag string, commits []releaseCommit) string {
	var buf bytes.Buffer
	fmt.Fprintf(&buf, "本 PR 由发版工作流自动维护，用于准备 **Skill Stow %s**（`v%s`）。\n\n", version, version)
	if latestTag != "" {
		fmt.Fprintf(&buf, "### 自 `%s` 以来的变更提交\n\n", latestTag)
	} else {
		buf.WriteString("### 包含的变更提交\n\n")
	}
	for _, c := range commits {
		fmt.Fprintf(&buf, "- `%s` %s\n", c.SHA, c.Header)
	}
	buf.WriteString("\n### 合并前检查\n\n")
	fmt.Fprintf(&buf, "- [ ] 润色 `app/CHANGELOG.md` 中 `## %s` 的中英文更新说明（应用内更新窗口使用编号列表）\n", version)
	fmt.Fprintf(&buf, "- [ ] （可选）如需在 GitHub Release 页面展示更详细的说明，可在此 PR 添加 `docs/releases/v%s.md`\n", version)
	buf.WriteString("- [ ] 确认 CI 检查通过\n\n")
	fmt.Fprintf(&buf, "合并此 PR 到 `main` 后，工作流会自动创建 `v%s` 标签、构建 macOS 通用版并更新草稿 Release（不会自动公开，仍需在草稿页面确认后点 **Publish release**）。\n", version)
	return buf.String()
}

func readInput(path string) (string, error) {
	if path == "" {
		return "", nil
	}
	if path == "-" {
		b, err := io.ReadAll(os.Stdin)
		return string(b), err
	}
	b, err := os.ReadFile(path)
	return string(b), err
}
