package library

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"

	"skillmanager/locales"
)

// 网站来源（well-known）：网站在 /.well-known/agent-skills/index.json 或
// /.well-known/skills/index.json 发布技能索引。写法和 skills@1.8.1 的
// WellKnownProvider 一致：
//
//   - 0.1.0：索引每项列出文件，文件在 {索引所在目录}/{name}/{文件}，逐个下载，
//     全部拿到才算。下载完再读一次索引，文件列表变了就当网站正在发布，提示重读。
//   - 0.2.0（带 $schema）：每项给一个 SKILL.md 或压缩包的网址和 sha256 摘要，
//     下载后核对，不符不能添加。
//
// 网站没有历史版本，读到的就是网站此刻的内容。

const (
	discoverySchemaV2 = "https://schemas.agentskills.io/discovery/0.2.0/schema.json"
	// maxIndexBytes 是索引的上限。
	maxIndexBytes = 4 << 20
	// maxArchiveBytes 是 0.2.0 压缩包的上限（和 npx 一样 50 MB）。
	maxArchiveBytes = 50 << 20
	// discoveryTimeout 是找索引最多等多久。
	discoveryTimeout = 10 * time.Second
)

var wellKnownPaths = []string{".well-known/agent-skills", ".well-known/skills"}

// wkIndex 是找到的索引。
type wkIndex struct {
	Version   string // 0.1.0 或 0.2.0
	URL       string // 索引的网址
	Base      string // .well-known 的上一级
	WellKnown string // .well-known/agent-skills 或 .well-known/skills
	Entries   []wkEntry
}

// wkEntry 是索引里的一个技能。
type wkEntry struct {
	Name, Description string
	Files             []string // 0.1.0
	Type, URL, Digest string   // 0.2.0：skill-md 或 archive；URL 已按索引解析成绝对地址
}

// ReadWebsiteImport 读一个网站来源：找索引、下载技能到暂存。
func ReadWebsiteImport(ctx context.Context, home string, client *http.Client, src ImportSource, progress func(ImportProgress)) (b *ImportBatch, err error) {
	report := func(p ImportProgress) {
		if progress != nil {
			progress(p)
		}
	}
	base, err := url.Parse(src.URL)
	if err != nil || base.Host == "" {
		return nil, &ImportError{Msg: locales.ImportNotSkillSource(), Hint: locales.ImportPasteInstallCommand()}
	}
	idx, err := findWellKnown(ctx, client, base)
	if errors.Is(err, errNotSkillSource) && src.Guessed && src.Subpath != "" {
		if u, perr := base.Parse("/" + escapePath(src.Subpath)); perr == nil {
			idx, err = findWellKnown(ctx, client, u)
		}
	}
	if err != nil {
		return nil, websiteError(ctx, src, base, err)
	}
	entries := idx.Entries
	hidden := 0
	named := func(e wkEntry) bool {
		return slices.ContainsFunc(src.Skills, func(w string) bool { return SkillSlug(w) == SkillSlug(e.Name) })
	}
	if len(src.Skills) > 0 && !src.All {
		if pick := slices.DeleteFunc(slices.Clone(entries), func(e wkEntry) bool { return !named(e) }); len(pick) > 0 {
			hidden, entries = len(entries)-len(pick), pick
		}
	}
	b, err = newBatch(home, src)
	if err != nil {
		return nil, err
	}
	defer func() {
		if err != nil {
			b.Discard()
			b = nil
		}
	}()
	b.Site, b.Protocol, b.BaseURL, b.Hidden, b.web = siteName(idx.Base), idx.Version, idx.Base, hidden, client
	wk := map[*ImportSkill]wkEntry{}
	for i, e := range entries {
		sk := &ImportSkill{Name: e.Name, Folder: e.Name, Description: e.Description, Dir: filepath.Join(b.Dir, fmt.Sprint(i))}
		wk[sk] = e
		b.Skills = append(b.Skills, sk)
	}

	var mu sync.Mutex
	var found []string
	done := 0
	work := make(chan *ImportSkill)
	errs := make(chan error, len(b.Skills))
	var wg sync.WaitGroup
	for range min(importWorkers, len(b.Skills)) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for sk := range work {
				err := b.fetchWebsite(ctx, idx, wk[sk], sk, func(n int64) {
					mu.Lock()
					b.Bytes += n
					p := ImportProgress{Bytes: b.Bytes, Found: slices.Clone(found), Done: done, Total: len(b.Skills)}
					mu.Unlock()
					report(p)
				})
				mu.Lock()
				done++
				found = append(found, sk.Name)
				p := ImportProgress{Bytes: b.Bytes, Found: slices.Clone(found), Done: done, Total: len(b.Skills)}
				mu.Unlock()
				report(p)
				errs <- err
			}
		}()
	}
	for _, sk := range b.Skills {
		work <- sk
	}
	close(work)
	wg.Wait()
	close(errs)
	for e := range errs {
		if e != nil {
			return nil, websiteError(ctx, src, base, e)
		}
	}
	if idx.Version == "0.1.0" {
		// 0.1.0 没有摘要：下载期间网站发布了新内容，读到的可能是一半旧一半新
		if again, err := fetchIndex(ctx, client, idx.URL, idx.Base, idx.WellKnown); err == nil && changedFiles(idx, again, entries) {
			return nil, &ImportError{Msg: locales.ImportWebsiteRecentlyUpdated(), Hint: locales.ImportReadAgain()}
		}
	}
	// 标了 internal 的技能是网站内部用的，不指名就不列（和 npx 一样）
	b.Skills = slices.DeleteFunc(b.Skills, func(sk *ImportSkill) bool {
		if internalSkill(sk.Dir) && !named(wk[sk]) {
			b.Hidden++
			return true
		}
		return false
	})
	if len(b.Skills) == 0 {
		return nil, &ImportError{Msg: locales.ImportWebsiteNoPublicSkills(), Hint: locales.ImportPasteInstallCommand()}
	}
	markDuplicateNames(b.Skills)
	return b, nil
}

// fetchWebsite 下载一个技能到 sk.Dir。网站上缺文件、摘要不符、压缩包不安全
// 都记进 Problem；连不上返回错误。
func (b *ImportBatch) fetchWebsite(ctx context.Context, idx *wkIndex, e wkEntry, sk *ImportSkill, grew func(int64)) error {
	raw := sk.Dir + "-web"
	defer os.RemoveAll(raw)
	if err := os.MkdirAll(raw, 0o755); err != nil {
		return err
	}
	problem := ""
	if idx.Version == "0.1.0" {
		skillBase := idx.Base + "/" + idx.WellKnown + "/" + url.PathEscape(e.Name)
		sk.SourceURL = skillBase + "/SKILL.md"
		files := []string{"SKILL.md"}
		for _, f := range e.Files {
			if !strings.EqualFold(f, "SKILL.md") && !slices.Contains(files, f) {
				files = append(files, f)
			}
		}
		if len(files) > maxImportFiles {
			problem = sizeProblem(len(files), 0)
		}
		var total int64
		got := map[string][]byte{}
		for _, f := range files {
			if problem != "" {
				break
			}
			data, err := webGet(ctx, b.web, skillBase+"/"+escapePath(f), maxImportBytes+1)
			var se *webStatus
			if errors.As(err, &se) {
				problem = locales.ImportWebsiteFileHttpError(f, se.Code)
				break
			}
			if err != nil {
				return err
			}
			total += int64(len(data))
			if total > maxImportBytes {
				problem = sizeProblem(0, total)
				break
			}
			if err := writeStageTreeFile(raw, f, "100644", data); err != nil {
				return err
			}
			got[f] = data
			grew(int64(len(data)))
		}
		if problem == "" {
			sk.Digest = filesDigest(got)
		}
	} else {
		sk.SourceURL, sk.Digest = e.URL, e.Digest
		data, err := webGet(ctx, b.web, e.URL, maxArchiveBytes+1)
		var se *webStatus
		switch {
		case errors.As(err, &se):
			problem = locales.ImportWebsiteSkillHttpError(se.Code)
		case err != nil:
			return err
		case len(data) > maxArchiveBytes:
			problem = locales.ImportArchiveDownloadTooLarge()
		case sha256Digest(data) != e.Digest:
			problem = locales.ImportWebsiteContentMismatch()
		case e.Type == "skill-md":
			grew(int64(len(data)))
			if err := writeStageTreeFile(raw, "SKILL.md", "100644", data); err != nil {
				return err
			}
		default:
			grew(int64(len(data)))
			if err := unpackSkill(data, e.URL, raw); err != nil {
				problem = err.Error()
			}
		}
	}
	if problem == "" && !exists(filepath.Join(raw, "SKILL.md")) {
		problem = locales.ImportMissingSkillFile()
	}
	if problem == "" {
		if err := copySkillDir(raw, sk); err != nil {
			return err
		}
	} else {
		os.MkdirAll(sk.Dir, 0o755)
		if data, err := os.ReadFile(filepath.Join(raw, "SKILL.md")); err == nil {
			os.WriteFile(filepath.Join(sk.Dir, "SKILL.md"), data, 0o644)
		}
		sk.Problem = problem
		b.describe(sk)
	}
	// 装进去的目录名用索引里的名字（和 npx 一样），简介 SKILL.md 没写时用索引的
	sk.Name = e.Name
	if sk.Description == "" {
		sk.Description = e.Description
	}
	if sk.Problem == "" {
		sk.Problem = nameProblem(sk.Name)
	}
	return nil
}

// unpackSkill 解开 0.2.0 的压缩包（zip 或 tar.gz）到 dst，SKILL.md 要在根目录。
func unpackSkill(data []byte, u, dst string) error {
	lower := strings.ToLower(u)
	switch {
	case bytes.HasPrefix(data, []byte("PK")) || strings.HasSuffix(lower, ".zip"):
		f, err := os.CreateTemp(filepath.Dir(dst), "archive-*.zip")
		if err != nil {
			return err
		}
		defer os.Remove(f.Name())
		_, err = f.Write(data)
		if cerr := f.Close(); err == nil {
			err = cerr
		}
		if err != nil {
			return err
		}
		if err := unzip(f.Name(), dst); err != nil {
			return errors.New(locales.ImportArchiveFailed(err.Error()))
		}
		return nil
	case bytes.HasPrefix(data, []byte{0x1f, 0x8b}) || strings.HasSuffix(lower, ".tar.gz") || strings.HasSuffix(lower, ".tgz"):
		return untarGz(bytes.NewReader(data), dst, 0, nil, false)
	}
	return errors.New(locales.ImportUnknownArchiveFormat())
}

// findWellKnown 按 npx 的顺序找索引：先地址本身，地址带路径的再试域名根目录；
// 每处先 agent-skills 再 skills。必须是 JSON 且符合 0.1.0 或 0.2.0 才算找到。
// 一处都没回应时返回连不上的错误，有回应但都不是索引时返回 errNotSkillSource。
func findWellKnown(ctx context.Context, client *http.Client, base *url.URL) (*wkIndex, error) {
	ctx, cancel := context.WithTimeout(ctx, discoveryTimeout)
	defer cancel()
	origin := base.Scheme + "://" + base.Host
	p := strings.TrimSuffix(base.EscapedPath(), "/")
	var netErr error
	answered := false
	for _, wk := range wellKnownPaths {
		bases := []string{origin + p}
		if p != "" {
			bases = append(bases, origin)
		}
		for _, at := range bases {
			idx, err := fetchIndex(ctx, client, at+"/"+wk+"/index.json", at, wk)
			if err == nil {
				return idx, nil
			}
			var se *webStatus
			if errors.As(err, &se) || errors.Is(err, errNotSkillSource) {
				answered = true
			} else if netErr == nil {
				netErr = err
			}
		}
	}
	if ctx.Err() != nil && !answered {
		if netErr == nil {
			netErr = ctx.Err()
		}
		return nil, netErr
	}
	if !answered && netErr != nil {
		return nil, netErr
	}
	return nil, errNotSkillSource
}

var errNotSkillSource = errors.New("not a skill source")

// fetchIndex 读一处索引并校验结构。
func fetchIndex(ctx context.Context, client *http.Client, indexURL, base, wk string) (*wkIndex, error) {
	data, err := webGet(ctx, client, indexURL, maxIndexBytes)
	if err != nil {
		return nil, err
	}
	var raw struct {
		Schema *string           `json:"$schema"`
		Skills []json.RawMessage `json:"skills"`
	}
	if json.Unmarshal(data, &raw) != nil || raw.Skills == nil {
		return nil, errNotSkillSource
	}
	idx := &wkIndex{URL: indexURL, Base: base, WellKnown: wk}
	ref, _ := url.Parse(indexURL)
	switch {
	case raw.Schema != nil && *raw.Schema == discoverySchemaV2:
		idx.Version = "0.2.0"
		for _, m := range raw.Skills {
			var e struct {
				Name, Description, Type, URL, Digest string
			}
			if json.Unmarshal(m, &e) != nil || !validWkName(e.Name) || e.Description == "" || len(e.Description) > 1024 ||
				e.Type != "skill-md" && e.Type != "archive" || e.URL == "" || !wkDigest.MatchString(e.Digest) {
				continue // 0.2.0：不合规矩的项跳过
			}
			u, err := ref.Parse(e.URL)
			if err != nil || u.Scheme != "https" && u.Scheme != "http" {
				continue
			}
			idx.Entries = append(idx.Entries, wkEntry{Name: e.Name, Description: e.Description, Type: e.Type, URL: u.String(), Digest: e.Digest})
		}
	case raw.Schema != nil:
		return nil, errNotSkillSource
	default:
		idx.Version = "0.1.0"
		for _, m := range raw.Skills {
			var e struct {
				Name, Description string
				Files             []string
			}
			if json.Unmarshal(m, &e) != nil || !validWkName(e.Name) || e.Description == "" || len(e.Files) == 0 ||
				!slices.ContainsFunc(e.Files, func(f string) bool { return strings.EqualFold(f, "SKILL.md") }) ||
				slices.ContainsFunc(e.Files, func(f string) bool { return !safeWkPath(f) }) {
				return nil, errNotSkillSource // 0.1.0：有一项不合规矩整个不认（和 npx 一样）
			}
			idx.Entries = append(idx.Entries, wkEntry{Name: e.Name, Description: e.Description, Files: e.Files})
		}
	}
	if len(idx.Entries) == 0 {
		return nil, errNotSkillSource
	}
	return idx, nil
}

var (
	wkName   = regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*$`)
	wkDigest = regexp.MustCompile(`^sha256:[a-f0-9]{64}$`)
	// wkInternal 认 metadata 里的 internal: true（frontmatter 把嵌套字段连成一行）。
	wkInternal = regexp.MustCompile(`(?:^|[\s{,])internal:\s*["']?(?:true|1)["']?(?:$|[\s},])`)
)

func validWkName(s string) bool { return len(s) <= 64 && wkName.MatchString(s) }

// safeWkPath 是 0.1.0 索引里的文件路径：相对、不含 ..、不含反斜杠。
func safeWkPath(f string) bool {
	return f != "" && !strings.HasPrefix(f, "/") && !strings.Contains(f, `\`) && !strings.Contains(f, "..") &&
		!strings.Contains(f, "\x00") && filepath.IsLocal(filepath.FromSlash(f))
}

// changedFiles 报告第二次读到的索引里，这几个技能的文件列表有没有变。
func changedFiles(before, after *wkIndex, picked []wkEntry) bool {
	if after.Version != before.Version {
		return true
	}
	for _, e := range picked {
		i := slices.IndexFunc(after.Entries, func(x wkEntry) bool { return x.Name == e.Name })
		if i < 0 || !slices.Equal(after.Entries[i].Files, e.Files) {
			return true
		}
	}
	return false
}

// internalSkill 报告 SKILL.md 的 metadata 里标了 internal。
func internalSkill(dir string) bool {
	for _, f := range readSkillMD(dir).Fields {
		if f.Key == "metadata" && wkInternal.MatchString(f.Value) {
			return true
		}
	}
	return false
}

// filesDigest 是 0.1.0 技能的内容摘要，和 npx 的 computeWellKnownSkillDigest
// 一样：按路径排序，依次是路径、\0、内容、\0。
func filesDigest(files map[string][]byte) string {
	h := sha256.New()
	keys := make([]string, 0, len(files))
	for k := range files {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	for _, k := range keys {
		h.Write([]byte(k))
		h.Write([]byte{0})
		h.Write(files[k])
		h.Write([]byte{0})
	}
	return "sha256:" + hex.EncodeToString(h.Sum(nil))
}

func sha256Digest(data []byte) string {
	sum := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(sum[:])
}

// siteName 是安装记录里的 source：域名，去掉 www.（和 npx 一样）。
func siteName(u string) string {
	p, err := url.Parse(u)
	if err != nil {
		return u
	}
	return strings.TrimPrefix(p.Hostname(), "www.")
}

func escapePath(p string) string {
	segs := strings.Split(p, "/")
	for i, s := range segs {
		segs[i] = url.PathEscape(s)
	}
	return strings.Join(segs, "/")
}

// webStatus 是网站回了非 200。
type webStatus struct{ Code int }

func (e *webStatus) Error() string { return fmt.Sprintf("HTTP %d", e.Code) }

// webGet 取一个网址，最多读 limit 字节。
func webGet(ctx context.Context, client *http.Client, u string, limit int64) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "skill-manager")
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, &webStatus{resp.StatusCode}
	}
	return io.ReadAll(io.LimitReader(resp.Body, limit))
}

// websiteError 把网站来源的错误换成读取页的说法。
func websiteError(ctx context.Context, src ImportSource, base *url.URL, err error) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	var ie *ImportError
	if errors.As(err, &ie) {
		return ie
	}
	host := strings.TrimPrefix(base.Hostname(), "www.")
	if errors.Is(err, errNotSkillSource) {
		if src.Guessed {
			return &ImportError{Msg: locales.ImportNotAtRoot(host), Hint: locales.ImportPasteSourceInstallCommand(), Err: err}
		}
		return &ImportError{Msg: locales.ImportNotSkillSource(), Hint: locales.ImportPasteInstallCommand(), Err: err}
	}
	var ne net.Error
	var ue *url.Error
	if errors.As(err, &ne) || errors.As(err, &ue) || errors.Is(err, context.DeadlineExceeded) {
		return &ImportError{Msg: locales.ImportCantConnect(host), Hint: locales.ImportCheckNetworkProxy(), Network: true, Err: err}
	}
	return &ImportError{Msg: locales.ImportReadFailed(err.Error()), Err: err}
}
