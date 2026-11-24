// releasenotes 为发版 CI 生成 GitHub Release 说明，并核对更新清单引用的附件是否全部上传。
package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"
	"text/template"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	var err error
	switch os.Args[1] {
	case "render":
		err = runRender(os.Args[2:])
	case "verify":
		err = runVerify(os.Args[2:])
	case "plan":
		err = runPlan(os.Args[2:])
	case "bump":
		err = runBump(os.Args[2:])
	case "apply":
		err = runApply(os.Args[2:])
	default:
		usage()
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "releasenotes %s: %v\n", os.Args[1], err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: releasenotes <render|verify|plan|bump|apply> [flags]")
}

type renderOptions struct {
	version       string
	changelogPath string
	overridePath  string
	templatePath  string
	dmgPath       string
	dmgName       string
	dmgSHA256     string
	outPath       string
}

func runRender(args []string) error {
	fs := flag.NewFlagSet("render", flag.ContinueOnError)
	var opts renderOptions
	fs.StringVar(&opts.version, "version", "", "release version without leading v (required)")
	fs.StringVar(&opts.changelogPath, "changelog", "CHANGELOG.md", "path to app/CHANGELOG.md")
	fs.StringVar(&opts.overridePath, "override", "", "optional path to per-version release notes override")
	fs.StringVar(&opts.templatePath, "template", "../.github/release-notes.tmpl.md", "path to release notes template")
	fs.StringVar(&opts.dmgPath, "dmg", "", "path to built .dmg (computes name and SHA-256)")
	fs.StringVar(&opts.dmgName, "dmg-name", "", "dmg filename (used when -dmg is omitted)")
	fs.StringVar(&opts.dmgSHA256, "sha256", "", "dmg SHA-256 hex digest (used when -dmg is omitted)")
	fs.StringVar(&opts.outPath, "out", "", "output file path (defaults to stdout)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return fmt.Errorf("unexpected arguments: %v", fs.Args())
	}

	rendered, _, err := renderNotes(opts)
	if err != nil {
		return err
	}
	if opts.outPath == "" {
		_, err = io.WriteString(os.Stdout, rendered)
		return err
	}
	return os.WriteFile(opts.outPath, []byte(rendered), 0o644)
}

type templateData struct {
	Version   string
	Tag       string
	DMGName   string
	DMGSHA256 string
	ZH        string
	EN        string
}

func renderNotes(opts renderOptions) (rendered, source string, err error) {
	version := strings.TrimPrefix(strings.TrimSpace(opts.version), "v")
	if version == "" {
		return "", "", errors.New("-version is required")
	}

	changelogBytes, err := os.ReadFile(opts.changelogPath)
	if err != nil {
		return "", "", fmt.Errorf("read changelog %s: %w", opts.changelogPath, err)
	}
	section, err := extractChangelogSection(string(changelogBytes), version)
	if err != nil {
		return "", "", fmt.Errorf("%s: %w", opts.changelogPath, err)
	}
	zh, en, err := splitBilingual(section)
	if err != nil {
		return "", "", fmt.Errorf("%s (## %s): %w", opts.changelogPath, version, err)
	}
	source = opts.changelogPath

	if opts.overridePath != "" {
		overrideBytes, readErr := os.ReadFile(opts.overridePath)
		switch {
		case readErr == nil:
			zh, en, err = splitBilingual(string(overrideBytes))
			if err != nil {
				return "", "", fmt.Errorf("%s: %w", opts.overridePath, err)
			}
			source = opts.overridePath
		case !errors.Is(readErr, os.ErrNotExist):
			return "", "", fmt.Errorf("read override %s: %w", opts.overridePath, readErr)
		}
	}

	dmgName, dmgSHA256, err := resolveDMG(opts)
	if err != nil {
		return "", "", err
	}

	tmplBytes, err := os.ReadFile(opts.templatePath)
	if err != nil {
		return "", "", fmt.Errorf("read template %s: %w", opts.templatePath, err)
	}
	tmpl, err := template.New(filepath.Base(opts.templatePath)).Option("missingkey=error").Parse(string(tmplBytes))
	if err != nil {
		return "", "", fmt.Errorf("parse template %s: %w", opts.templatePath, err)
	}

	var buf bytes.Buffer
	data := templateData{
		Version:   version,
		Tag:       "v" + version,
		DMGName:   dmgName,
		DMGSHA256: dmgSHA256,
		ZH:        zh,
		EN:        en,
	}
	if err := tmpl.Execute(&buf, data); err != nil {
		return "", "", fmt.Errorf("render template %s: %w", opts.templatePath, err)
	}
	return strings.TrimSpace(buf.String()) + "\n", source, nil
}

func resolveDMG(opts renderOptions) (name, sum string, err error) {
	if opts.dmgPath != "" {
		if opts.dmgName != "" || opts.dmgSHA256 != "" {
			return "", "", errors.New("-dmg cannot be combined with -dmg-name or -sha256")
		}
		f, err := os.Open(opts.dmgPath)
		if err != nil {
			return "", "", fmt.Errorf("open dmg %s: %w", opts.dmgPath, err)
		}
		defer f.Close()
		h := sha256.New()
		if _, err := io.Copy(h, f); err != nil {
			return "", "", fmt.Errorf("hash dmg %s: %w", opts.dmgPath, err)
		}
		return filepath.Base(opts.dmgPath), hex.EncodeToString(h.Sum(nil)), nil
	}
	name = strings.TrimSpace(opts.dmgName)
	sum = strings.TrimSpace(opts.dmgSHA256)
	if name == "" || sum == "" {
		return "", "", errors.New("provide -dmg or both -dmg-name and -sha256")
	}
	return name, sum, nil
}

// extractChangelogSection 返回 CHANGELOG 中 "## <version>" 或 "## [<version>]" 一节的正文，
// 规则与 mygo internal/update.ReleaseNotes 一致。
func extractChangelogSection(changelog, version string) (string, error) {
	version = strings.TrimPrefix(strings.TrimSpace(version), "v")
	var lines []string
	in := false
	for _, line := range splitLines(changelog) {
		if rest, ok := strings.CutPrefix(line, "## "); ok {
			if in {
				break
			}
			fields := strings.Fields(rest)
			if len(fields) > 0 && strings.TrimPrefix(headingVersion(fields[0]), "v") == version {
				in = true
			}
			continue
		}
		if in {
			lines = append(lines, line)
		}
	}
	body := strings.TrimSpace(strings.Join(lines, "\n"))
	if body == "" {
		return "", fmt.Errorf("missing or empty \"## %s\" section", version)
	}
	return body, nil
}

// listChangelogVersions 返回 CHANGELOG 中所有 "## <version>" 标题里的版本号。
func listChangelogVersions(changelog string) []string {
	var versions []string
	for _, line := range splitLines(changelog) {
		rest, ok := strings.CutPrefix(line, "## ")
		if !ok {
			continue
		}
		fields := strings.Fields(rest)
		if len(fields) == 0 {
			continue
		}
		v := strings.TrimPrefix(headingVersion(fields[0]), "v")
		if v != "" {
			versions = append(versions, v)
		}
	}
	return versions
}

func headingVersion(s string) string {
	if rest, ok := strings.CutPrefix(s, "["); ok {
		s, _, _ = strings.Cut(rest, "]")
	}
	return s
}

// splitBilingual 将双语 Markdown 按「中文」和「English」标记拆成两段，
// 标记可以是单独一行（如 app/CHANGELOG.md）或 Markdown 标题（如 ## 中文 / ## English）。
func splitBilingual(md string) (zh, en string, err error) {
	var zhLines, enLines []string
	var section string
	var fenceChar byte
	var fenceLen int
	seenZH, seenEN := false, false

	for _, line := range splitLines(md) {
		trimmed := strings.TrimSpace(line)
		if fenceLen > 0 {
			if isClosingFence(trimmed, fenceChar, fenceLen) {
				fenceChar, fenceLen = 0, 0
			}
		} else if ch, n, ok := openingFence(trimmed); ok {
			fenceChar, fenceLen = ch, n
		} else if lang, ok := languageMarker(trimmed); ok {
			switch lang {
			case "zh":
				if seenZH {
					return "", "", errors.New("duplicate Chinese section marker")
				}
				seenZH = true
				section = "zh"
			case "en":
				if seenEN {
					return "", "", errors.New("duplicate English section marker")
				}
				seenEN = true
				section = "en"
			}
			continue
		}
		switch section {
		case "zh":
			zhLines = append(zhLines, line)
		case "en":
			enLines = append(enLines, line)
		}
	}

	if !seenZH {
		return "", "", errors.New("missing \"中文\" section")
	}
	if !seenEN {
		return "", "", errors.New("missing \"English\" section")
	}
	zh = trimSection(zhLines)
	en = trimSection(enLines)
	if zh == "" {
		return "", "", errors.New("\"中文\" section is empty")
	}
	if en == "" {
		return "", "", errors.New("\"English\" section is empty")
	}
	return zh, en, nil
}

func openingFence(trimmed string) (byte, int, bool) {
	if len(trimmed) < 3 || (trimmed[0] != '`' && trimmed[0] != '~') {
		return 0, 0, false
	}
	ch := trimmed[0]
	n := 0
	for n < len(trimmed) && trimmed[n] == ch {
		n++
	}
	if n < 3 {
		return 0, 0, false
	}
	// Backtick info strings cannot contain backticks (CommonMark §4.5).
	if ch == '`' && strings.ContainsRune(trimmed[n:], '`') {
		return 0, 0, false
	}
	return ch, n, true
}

func isClosingFence(trimmed string, ch byte, minLen int) bool {
	if len(trimmed) < minLen {
		return false
	}
	for i := 0; i < len(trimmed); i++ {
		if trimmed[i] != ch {
			return false
		}
	}
	return true
}

func languageMarker(trimmedLine string) (string, bool) {
	s := strings.TrimLeft(trimmedLine, "#")
	if len(s) != len(trimmedLine) {
		// 标题形式要求 # 后有空白，避免误判无空格的文本
		if !strings.HasPrefix(s, " ") && !strings.HasPrefix(s, "\t") {
			return "", false
		}
		s = strings.TrimSpace(s)
	}
	switch s {
	case "中文":
		return "zh", true
	case "English":
		return "en", true
	default:
		return "", false
	}
}

func trimSection(lines []string) string {
	text := strings.TrimSpace(strings.Join(lines, "\n"))
	for {
		trimmed := strings.TrimSpace(text)
		if !strings.HasSuffix(trimmed, "\n---") && trimmed != "---" {
			return trimmed
		}
		if trimmed == "---" {
			return ""
		}
		text = strings.TrimSuffix(trimmed, "\n---")
	}
}

func splitLines(s string) []string {
	return strings.Split(strings.ReplaceAll(s, "\r\n", "\n"), "\n")
}

type updateManifest struct {
	Version   string `json:"version"`
	URL       string `json:"url"`
	Size      int64  `json:"size"`
	Signature string `json:"signature"`
	Deltas    []struct {
		From      string `json:"from"`
		URL       string `json:"url"`
		Size      int64  `json:"size"`
		Signature string `json:"signature"`
	} `json:"deltas"`
}

type releaseAssetsPayload struct {
	Assets []releaseAsset `json:"assets"`
}

type releaseAsset struct {
	Name  string `json:"name"`
	Size  int64  `json:"size"`
	State string `json:"state"`
}

func runVerify(args []string) error {
	fs := flag.NewFlagSet("verify", flag.ContinueOnError)
	var version, dmgName, manifestPath, assetsPath string
	fs.StringVar(&version, "version", "", "expected release version without leading v (required)")
	fs.StringVar(&dmgName, "dmg", "", "expected dmg asset filename (required)")
	fs.StringVar(&manifestPath, "manifest", "", "path to update-<target>.json (required)")
	fs.StringVar(&assetsPath, "assets", "-", "path to `gh release view --json assets` output, or - for stdin")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return fmt.Errorf("unexpected arguments: %v", fs.Args())
	}
	if version == "" || dmgName == "" || manifestPath == "" {
		return errors.New("-version, -dmg and -manifest are required")
	}

	var assetsBytes []byte
	var err error
	if assetsPath == "-" {
		assetsBytes, err = io.ReadAll(os.Stdin)
	} else {
		assetsBytes, err = os.ReadFile(assetsPath)
	}
	if err != nil {
		return fmt.Errorf("read release assets: %w", err)
	}
	return verifyAssets(version, dmgName, manifestPath, assetsBytes)
}

// verifyAssets 核对更新清单本身、安装包、SHA256SUMS，以及清单里引用的全包和增量包都已上传到草稿 Release。
func verifyAssets(version, dmgName, manifestPath string, assetsJSON []byte) error {
	version = strings.TrimPrefix(strings.TrimSpace(version), "v")
	tag := "v" + version

	manifestBytes, err := os.ReadFile(manifestPath)
	if err != nil {
		return fmt.Errorf("read manifest %s: %w", manifestPath, err)
	}
	var m updateManifest
	if err := json.Unmarshal(manifestBytes, &m); err != nil {
		return fmt.Errorf("parse manifest %s: %w", manifestPath, err)
	}
	if m.Version != version {
		return fmt.Errorf("manifest version %q does not match expected %q", m.Version, version)
	}
	if m.Size <= 0 || strings.TrimSpace(m.Signature) == "" {
		return errors.New("manifest archive is missing size or signature")
	}

	var payload releaseAssetsPayload
	if err := json.Unmarshal(assetsJSON, &payload); err != nil {
		return fmt.Errorf("parse release assets json: %w", err)
	}
	byName := make(map[string]releaseAsset, len(payload.Assets))
	for _, a := range payload.Assets {
		byName[a.Name] = a
	}

	type expectation struct {
		name string
		size int64 // >0 时校验文件大小
	}
	want := []expectation{
		{name: filepath.Base(manifestPath), size: int64(len(manifestBytes))},
		{name: dmgName},
		{name: "SHA256SUMS"},
	}

	archiveName, err := urlAssetName(m.URL, tag)
	if err != nil {
		return fmt.Errorf("manifest url %q: %w", m.URL, err)
	}
	want = append(want, expectation{name: archiveName, size: m.Size})

	for _, d := range m.Deltas {
		if d.Size <= 0 || strings.TrimSpace(d.Signature) == "" {
			return fmt.Errorf("delta from %s is missing size or signature", d.From)
		}
		deltaName, err := urlAssetName(d.URL, tag)
		if err != nil {
			return fmt.Errorf("delta from %s url %q: %w", d.From, d.URL, err)
		}
		want = append(want, expectation{name: deltaName, size: d.Size})
	}

	var problems []string
	for _, w := range want {
		a, ok := byName[w.name]
		if !ok {
			problems = append(problems, fmt.Sprintf("missing asset %s", w.name))
			continue
		}
		if a.State != "uploaded" {
			problems = append(problems, fmt.Sprintf("asset %s state is %q, want \"uploaded\"", w.name, a.State))
		}
		if w.size > 0 && a.Size != w.size {
			problems = append(problems, fmt.Sprintf("asset %s size is %d, manifest expects %d", w.name, a.Size, w.size))
		}
	}
	if len(problems) > 0 {
		return errors.New(strings.Join(problems, "; "))
	}
	return nil
}

func urlAssetName(raw, expectedTag string) (string, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Scheme == "" || u.Host == "" {
		return "", errors.New("not an absolute URL")
	}
	name := path.Base(u.Path)
	if name == "" || name == "." || name == "/" {
		return "", errors.New("missing filename in URL")
	}
	if path.Base(path.Dir(u.Path)) != expectedTag {
		return "", fmt.Errorf("URL path %q does not target tag %s", u.Path, expectedTag)
	}
	return name, nil
}
