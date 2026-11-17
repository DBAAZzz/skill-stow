package library

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"math"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"

	"skillmanager/locales"
)

// 读取：把技能整份下载（或复制、解压）到应用数据目录的暂存区，校验后交给
// 选择页预览。网络问题只会出现在这一步；添加只在本机复制（import_add.go）。
// 唯一的例外是大仓库：技能超过 largeRepo 个时只先拿 SKILL.md，勾选后再用
// FetchImportSkill 补齐。

const (
	// maxImportBytes、maxImportFiles 是单个技能的上限（暂定）。
	maxImportBytes = 20 << 20
	maxImportFiles = 2000
	// largeRepo：技能多于这个数时读取阶段只拿 SKILL.md。
	largeRepo = 50
	// importWorkers 是同时下载的技能数。
	importWorkers = 4
)

// ImportSkill 是读到的一个技能。
type ImportSkill struct {
	Name, Description string
	Folder            string // 来源里的目录名
	RepoPath          string // GitHub：仓库里的目录，根目录为空；本地：原来的路径
	Dir               string // 暂存的技能目录；Partial 时只有 SKILL.md
	Tree              string // 目录的 Git tree 哈希；Partial 时是上游的
	Size              int64  // 总字节数；只下了 SKILL.md 而目录树又不带大小时为 -1
	Count             int
	Exec              []string      // 可执行文件（有执行位或是脚本），相对技能目录
	Problem           string        // 不能添加的原因，空为能添加
	Partial           bool          // 大仓库：只拿了 SKILL.md，勾选后补齐
	Audit             []AuditResult // skills.sh 的安全审计，取不到为空
	SourceURL         string        // 网站：SKILL.md 或压缩包的网址
	Digest            string        // 网站：内容摘要（sha256:…），写进安装记录
	files             []TreeEntry   // GitHub：目录树里这个技能的文件，补齐用
}

// MissingDescription 报告 SKILL.md 没写 description。
func (s *ImportSkill) MissingDescription() bool { return strings.TrimSpace(s.Description) == "" }

// Risky 报告审计里有一家是 high 或 critical。
func (s *ImportSkill) Risky() bool { return slices.ContainsFunc(s.Audit, AuditResult.Risky) }

// Files 读出暂存的文件，给预览用（规则同阅读视图）。
func (s *ImportSkill) Files() []File { return readFiles(s.Dir) }

// ImportBatch 是一次读取的结果。用完调用 Discard 删掉暂存。
type ImportBatch struct {
	Source     ImportSource
	Commit     string // GitHub：读的是哪个提交
	CommitDate time.Time
	Dir        string // 暂存根目录
	Skills     []*ImportSkill
	NotSkills  []string // 本地：拖入的东西里不是技能的文件夹
	Hidden     int      // 指定了技能时，仓库里没有读的其他技能数
	Bytes      int64    // 已下载或复制的大小

	// 网站来源
	Site     string // 域名，如 open.feishu.cn
	Protocol string // 索引的协议版本：0.1.0 或 0.2.0
	BaseURL  string // 索引所在的地址（.well-known 的上一级）

	remote ImportRemote
	web    *http.Client
	mu     sync.Mutex

	// GitHub 压缩包退路（import_archive.go）
	arcMu   sync.Mutex
	arcDone bool
	arcDir  string
	arcErr  error
}

// Discard 删掉这次读取的暂存。
func (b *ImportBatch) Discard() {
	if b != nil && b.Dir != "" {
		os.RemoveAll(b.Dir)
	}
}

// Skill 按名称找，没有为 nil。
func (b *ImportBatch) Skill(name string) *ImportSkill {
	for _, s := range b.Skills {
		if s.Name == name {
			return s
		}
	}
	return nil
}

// Preselected 是按来源里写的技能（@skill、--skill、skills.sh 链接）预选的
// 名称；来源没指定时为 nil。按 SkillSlug 比较技能名和目录名。
func (b *ImportBatch) Preselected() []string {
	if len(b.Source.Skills) == 0 {
		return nil
	}
	var out []string
	for _, s := range b.Skills {
		for _, want := range b.Source.Skills {
			w := SkillSlug(want)
			if w == SkillSlug(s.Name) || w == SkillSlug(s.Folder) {
				out = append(out, s.Name)
				break
			}
		}
	}
	return out
}

// ImportProgress 是读取进度。
type ImportProgress struct {
	Commit string
	Bytes  int64
	Found  []string // 已找到的技能名
	Done   int      // 已下载完的技能数
	Total  int
}

// ImportError 是读取失败：Msg 是一句话说明，Hint 是下一步。
type ImportError struct {
	Msg, Hint string
	Reset     time.Time // GitHub 额度恢复时间
	Network   bool      // 连不上：界面给「重试」「打开设置…」
	Err       error
}

func (e *ImportError) Error() string { return e.Msg }
func (e *ImportError) Unwrap() error { return e.Err }

// ImportStaging 是读取暂存的目录（和更新用的暂存在一起）。
func importStaging(home string) string { return stagingDir(home) }

// CleanImportStaging 清掉上次没清的读取暂存（中途退出留下的）。启动时调用。
// 替换中途退出留下的（replace-*）里可能是用户唯一的旧版：只移到废纸篓，
// 移不进去就留着，不删。
func CleanImportStaging(home string) {
	items, _ := os.ReadDir(importStaging(home))
	for _, it := range items {
		dir := filepath.Join(importStaging(home), it.Name())
		switch n := it.Name(); {
		case strings.HasPrefix(n, "import-") || strings.HasPrefix(n, "add-"):
			os.RemoveAll(dir)
		case strings.HasPrefix(n, replacePrefix):
			left, _ := os.ReadDir(dir)
			for _, e := range left {
				Trash(home, filepath.Join(dir, e.Name()))
			}
			os.Remove(dir) // 只在空了时删得掉
		}
	}
}

func newBatch(home string, src ImportSource) (*ImportBatch, error) {
	if err := os.MkdirAll(importStaging(home), 0o700); err != nil {
		return nil, err
	}
	dir, err := os.MkdirTemp(importStaging(home), "import-")
	if err != nil {
		return nil, err
	}
	return &ImportBatch{Source: src, Dir: dir}, nil
}

// ——— GitHub ———

// skipDirs 里的目录不找 SKILL.md（和 npx skills 一致）。
var skipDirs = []string{"node_modules", ".git", "dist", "build", "__pycache__"}

// ReadGitHubImport 读一个 GitHub 来源：目录树里找技能，下载到暂存。
func ReadGitHubImport(ctx context.Context, home string, r ImportRemote, src ImportSource, progress func(ImportProgress)) (b *ImportBatch, err error) {
	defer trackIn(home, "import", "read-github", src.Repo)(&err)
	report := func(p ImportProgress) {
		if progress != nil {
			progress(p)
		}
	}
	if src, err = splitRef(ctx, r, src); err != nil {
		return nil, githubImportError(ctx, src, err)
	}
	c, err := r.Commit(ctx, src.Repo, src.Ref)
	if err != nil {
		return nil, githubImportError(ctx, src, err)
	}
	report(ImportProgress{Commit: c.SHA})
	tree, err := r.Tree(ctx, src.Repo, c.SHA)
	if err != nil {
		return nil, githubImportError(ctx, src, err)
	}
	if tree.Truncated {
		return nil, &ImportError{Msg: locales.ImportRepoTooBig(src.Repo), Hint: locales.ImportUseSubdirectoryOrZip()}
	}
	dirs := findSkillDirs(tree, src.Subpath)
	if len(dirs) == 0 {
		where := src.Repo
		if src.Subpath != "" {
			where += "/" + src.Subpath
		}
		return nil, &ImportError{Msg: locales.ImportNoSkillMdIn(where), Hint: locales.ImportSkillFolderHint()}
	}
	hidden := 0
	if len(src.Skills) > 0 && !src.All {
		// 指定了技能：按目录名找到的就只读这几个，其他的不下载、不审计
		if named := dirsNamed(dirs, src); len(named) > 0 {
			hidden, dirs = len(dirs)-len(named), named
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
	b.Commit, b.CommitDate, b.remote, b.Hidden = c.SHA, c.Date, r, hidden
	partial := len(dirs) > largeRepo
	for i, d := range dirs {
		sk := &ImportSkill{RepoPath: d, Folder: path.Base(d), Dir: filepath.Join(b.Dir, fmt.Sprint(i)), Partial: partial}
		if d == "" {
			sk.Folder = path.Base(src.Repo)
		}
		sk.Tree, sk.files = skillTree(tree, c.Tree, d)
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
				err := b.fetch(ctx, sk, func(n int64) {
					mu.Lock()
					b.Bytes += n
					p := ImportProgress{Commit: c.SHA, Bytes: b.Bytes, Found: slices.Clone(found), Done: done, Total: len(b.Skills)}
					mu.Unlock()
					report(p)
				})
				mu.Lock()
				done++
				if sk.Name != "" {
					found = append(found, sk.Name)
				}
				p := ImportProgress{Commit: c.SHA, Bytes: b.Bytes, Found: slices.Clone(found), Done: done, Total: len(b.Skills)}
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
			return nil, githubImportError(ctx, src, e)
		}
	}
	markDuplicateNames(b.Skills)
	b.audit(ctx)
	return b, nil
}

// splitRef 按仓库实际的分支和标签重新切分 /tree/<ref>/<子目录>：分支名可以
// 带斜杠（feature/x），解析网址时只取了第一段。对上多个时报错，不猜。
func splitRef(ctx context.Context, r Remote, src ImportSource) (ImportSource, error) {
	rl, ok := r.(RefLister)
	if !ok || src.Ref == "" || src.Subpath == "" || isSHA(src.Ref) {
		return src, nil
	}
	names, err := rl.RefNames(ctx, src.Repo, src.Ref)
	if err != nil {
		return src, err
	}
	segs := strings.Split(src.Subpath, "/")
	var refs []string
	at := 0 // 子目录里有几段属于分支名
	for n := 0; n <= len(segs); n++ {
		ref := path.Join(append([]string{src.Ref}, segs[:n]...)...)
		if slices.Contains(names, ref) {
			refs, at = append(refs, ref), n
		}
	}
	switch {
	case len(refs) > 1:
		return src, &ImportError{Msg: locales.ImportAmbiguousRef(refs[0], refs[1]), Hint: locales.ImportAmbiguousRefHint()}
	case len(refs) == 1:
		src.Ref, src.Subpath = refs[0], strings.Join(segs[at:], "/")
	}
	return src, nil
}

// dirsNamed 是 dirs 里目录名对得上 src.Skills 的（按 SkillSlug 比较；根目录
// 的技能按仓库名）。技能名和目录名不一样时这里找不到，调用方退回读全部，
// 再按技能名预选。
func dirsNamed(dirs []string, src ImportSource) []string {
	var out []string
	for _, d := range dirs {
		folder := path.Base(d)
		if d == "" {
			folder = path.Base(src.Repo)
		}
		if slices.ContainsFunc(src.Skills, func(w string) bool { return SkillSlug(w) == SkillSlug(folder) }) {
			out = append(out, d)
		}
	}
	return out
}

// findSkillDirs 是目录树里技能所在的目录（相对仓库根，根目录为空），按路径排。
// 只看 sub 下面的；跳过 skipDirs；技能里面再套的技能不算（根目录的技能除外：
// 仓库根有 SKILL.md 时，skills/ 下的照样列出，和 npx skills 一样）。
func findSkillDirs(tree Tree, sub string) []string {
	sub = strings.Trim(sub, "/")
	var mds []string
	for _, e := range tree.Entries {
		if e.Type != "blob" || !strings.EqualFold(path.Base(e.Path), "SKILL.md") {
			continue
		}
		dir := path.Dir(e.Path)
		if dir == "." {
			dir = ""
		}
		if sub != "" && dir != sub && !strings.HasPrefix(dir, sub+"/") {
			continue
		}
		if slices.ContainsFunc(strings.Split(dir, "/"), func(p string) bool { return slices.Contains(skipDirs, p) }) {
			continue
		}
		if strings.Count(e.Path, "/") >= 6 {
			continue
		}
		mds = append(mds, dir)
	}
	var out []string
	for _, d := range mds {
		nested := false
		for _, other := range mds {
			if other != "" && other != d && strings.HasPrefix(d, other+"/") && other != sub {
				nested = true
				break
			}
		}
		if !nested && !slices.Contains(out, d) {
			out = append(out, d)
		}
	}
	slices.Sort(out)
	return out
}

// skillTree 是目录 dir 的哈希（根目录是 root）和它下面的全部文件。
func skillTree(tree Tree, root, dir string) (string, []TreeEntry) {
	hash := root
	var files []TreeEntry
	for _, e := range tree.Entries {
		if e.Path == dir && e.Type == "tree" {
			hash = e.SHA
		}
		if e.Type != "tree" && (dir == "" || strings.HasPrefix(e.Path, dir+"/")) {
			files = append(files, e)
		}
	}
	return hash, files
}

// fetch 下载一个技能到 sk.Dir：Partial 时只下 SKILL.md。先用 skills.sh 的
// 快照，内容按 blob 哈希逐个核对；对不上或缺的文件从 GitHub 按提交下载。
// 文件本身不合规矩（链接、子模块、太大）时不下载，记进 Problem。目录树不带
// 文件大小时（git 协议），总大小边下边算，超出上限时同样只留 SKILL.md。
func (b *ImportBatch) fetch(ctx context.Context, sk *ImportSkill, grew func(int64)) error {
	files := sk.files
	sk.Count, sk.Size = len(files), 0
	sized := true
	for _, f := range files {
		if f.Size < 0 {
			sized = false
		} else {
			sk.Size += f.Size
		}
	}
	prefix := ""
	if sk.RepoPath != "" {
		prefix = sk.RepoPath + "/"
	}
	if err := validTreeFiles(files, sk.RepoPath); err != nil {
		sk.Problem = err.Error()
	} else if p := sizeProblem(sk.Count, sk.Size); p != "" {
		sk.Problem = p
	}
	mdOnly := func() []TreeEntry {
		md := slices.DeleteFunc(slices.Clone(files), func(e TreeEntry) bool { return e.Path != prefix+"SKILL.md" })
		if len(md) == 0 {
			// SKILL.md 大小写不同（skill.md）
			md = slices.DeleteFunc(slices.Clone(files), func(e TreeEntry) bool { return !strings.EqualFold(e.Path, prefix+"SKILL.md") })
		}
		return md
	}
	want := files
	if sk.Partial || sk.Problem != "" {
		want = mdOnly()
	}
	if err := os.MkdirAll(sk.Dir, 0o755); err != nil {
		return err
	}
	snap := map[string]string{}
	if !sk.Partial && sk.Problem == "" {
		md := ""
		for _, e := range want {
			if strings.EqualFold(e.Path, prefix+"SKILL.md") {
				if b, err := b.remote.File(ctx, b.Source.Repo, b.Commit, e.Path); err == nil && blobID(b) == e.SHA {
					md = string(b)
				}
			}
		}
		slug := SkillSlug(ParseSkillMD(md).Name)
		if slug == "" {
			slug = SkillSlug(sk.Folder)
		}
		if fs, err := b.remote.Snapshot(ctx, b.Source.Repo, slug); err == nil {
			for _, f := range fs {
				snap[f.Path] = f.Contents
			}
		}
		if md != "" {
			snap[strings.TrimPrefix(want[slices.IndexFunc(want, func(e TreeEntry) bool { return strings.EqualFold(e.Path, prefix+"SKILL.md") })].Path, prefix)] = md
		}
	}
	inSnap := func(e TreeEntry) ([]byte, bool) {
		if c, hit := snap[relSkillPath(e.Path, sk.RepoPath)]; hit && blobID([]byte(c)) == e.SHA {
			return []byte(c), true
		}
		return nil, false
	}
	// 快照缺的文件多时先用压缩包，少时逐个下载；逐个下载失败再试压缩包
	missing := 0
	for _, e := range want {
		if _, ok := inSnap(e); !ok {
			missing++
		}
	}
	useArchive := missing > rawFileLimit
	// counting：目录树不带大小，整份下载时边下边算总大小。下载中途发现的
	// 问题（如 LFS）不停止计算，否则后面的文件就没有上限了。
	counting := !sized && !sk.Partial && sk.Problem == ""
	get := func(e TreeEntry) ([]byte, error) {
		if data, ok := inSnap(e); ok {
			return data, nil
		}
		if useArchive {
			if data, ok := b.fromArchive(ctx, e); ok {
				return data, nil
			}
		}
		got, err := b.remote.File(ctx, b.Source.Repo, b.Commit, e.Path)
		if err == nil && len(got) > maxFileBytes && counting {
			return got, nil // 截在单个文件的上限处，必然超过总大小的上限，交给下面处理
		}
		if err == nil && blobID(got) != e.SHA {
			err = &ImportError{Msg: locales.ImportGithubTreeMismatch(), Hint: locales.ImportRetryLater()}
		}
		if err != nil {
			if data, ok := b.fromArchive(ctx, e); ok {
				return data, nil
			}
			return nil, err
		}
		return got, nil
	}
	var total int64
	for _, e := range want {
		if err := ctx.Err(); err != nil {
			return err
		}
		data, err := get(e)
		if err != nil {
			return err
		}
		total += int64(len(data))
		grew(int64(len(data)))
		if counting && total > maxImportBytes {
			counting = false // 下面只补 SKILL.md，按常规核对
			sk.Problem = sizeProblem(sk.Count, total)
			if err := os.RemoveAll(sk.Dir); err != nil {
				return err
			}
			if err := os.MkdirAll(sk.Dir, 0o755); err != nil {
				return err
			}
			for _, e := range mdOnly() {
				data, err := get(e)
				if err != nil {
					return err
				}
				if err := writeStageTreeFile(sk.Dir, relSkillPath(e.Path, sk.RepoPath), e.Mode, data); err != nil {
					return err
				}
			}
			break
		}
		if strings.HasPrefix(string(data), "version https://git-lfs.github.com/spec/v1") {
			sk.Problem = locales.ImportGitLfsUnsupported()
		}
		if err := writeStageTreeFile(sk.Dir, relSkillPath(e.Path, sk.RepoPath), e.Mode, data); err != nil {
			return err
		}
	}
	if !sized {
		sk.Size = total // 超限时是下到停下为止的字节数
		if len(want) < len(files) && total <= maxImportBytes {
			sk.Size = -1 // 只下了 SKILL.md，总大小不知道
		}
	}
	b.describe(sk)
	if !sk.Partial && sk.Problem == "" {
		if h, err := TreeHash(sk.Dir); err != nil || h != sk.Tree {
			return &ImportError{Msg: locales.ImportGithubTreeMismatch(), Hint: locales.ImportRetryLater()}
		}
	}
	return nil
}

// FetchImportSkill 补齐大仓库里只拿了 SKILL.md 的技能。
func (b *ImportBatch) FetchImportSkill(ctx context.Context, name string) error {
	sk := b.Skill(name)
	if sk == nil || !sk.Partial {
		return nil
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if !sk.Partial {
		return nil
	}
	dir := sk.Dir + "-full"
	os.RemoveAll(dir)
	full := *sk
	full.Partial, full.Dir, full.Size, full.Count, full.Problem = false, dir, 0, 0, ""
	if err := b.fetch(ctx, &full, func(int64) {}); err != nil {
		os.RemoveAll(dir)
		return githubImportError(ctx, b.Source, err)
	}
	os.RemoveAll(sk.Dir)
	if err := os.Rename(dir, sk.Dir); err != nil {
		return err
	}
	full.Dir = sk.Dir
	full.Audit = sk.Audit
	*sk = full
	return nil
}

// describe 读暂存里的 SKILL.md 和文件，填名称、简介、可执行文件，检查名称。
func (b *ImportBatch) describe(sk *ImportSkill) {
	md := readSkillMD(sk.Dir)
	sk.Name, sk.Description = strings.TrimSpace(md.Name), strings.TrimSpace(md.Description)
	if sk.Name == "" {
		sk.Name = sk.Folder
	}
	if sk.Problem == "" {
		sk.Problem = nameProblem(sk.Name)
	}
	if !sk.Partial {
		sk.Exec = execFiles(sk.Dir)
	} else {
		for _, f := range sk.files {
			rel := relSkillPath(f.Path, sk.RepoPath)
			if f.Mode == "100755" || scriptExt(rel) {
				sk.Exec = append(sk.Exec, rel)
			}
		}
	}
}

// audit 取 skills.sh 的安全审计，3 秒取不到就算了。
func (b *ImportBatch) audit(ctx context.Context) {
	var names []string
	for _, s := range b.Skills {
		names = append(names, s.Name)
	}
	res, err := b.remote.Audit(ctx, b.Source.Repo, names)
	if err != nil {
		return
	}
	for _, s := range b.Skills {
		s.Audit = res[s.Name]
	}
}

// githubImportError 把 GitHub 的错误换成读取页的说法（文档第 7 节）。
func githubImportError(ctx context.Context, src ImportSource, err error) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	var ie *ImportError
	if errors.As(err, &ie) {
		return ie
	}
	var re *RemoteError
	if errors.As(err, &re) {
		switch re.Kind {
		case RemoteNotFound:
			if src.Ref != "" {
				return &ImportError{Msg: locales.ImportRepoOrBranchMissing(src.Repo, src.Ref), Hint: locales.ImportCheckSpellingPublicOnly(), Err: err}
			}
			return &ImportError{Msg: locales.ImportRepoMissing(src.Repo), Hint: locales.ImportCheckSpellingPublicOnly(), Err: err}
		case RemoteQuota:
			mins := int(math.Ceil(time.Until(re.Reset).Minutes()))
			msg := locales.ImportQuota(max(mins, 1))
			return &ImportError{Msg: msg, Hint: locales.ImportDownloadRepositoryZip(), Reset: re.Reset, Err: err}
		case RemoteThrottled:
			return &ImportError{Msg: locales.ImportGithubRateLimited(), Hint: locales.ImportRetryOrDropZip(), Err: err}
		case RemoteNetwork:
			return &ImportError{Msg: locales.ImportGithubConnectionFailed(), Hint: locales.ImportCheckNetworkProxy(), Network: true, Err: err}
		case RemoteAuth:
			return &ImportError{Msg: locales.ImportRemoteError(re.Error()), Err: err}
		}
		return &ImportError{Msg: locales.ImportRemoteError(re.Error()), Hint: locales.ImportRetryLater(), Err: err}
	}
	return &ImportError{Msg: locales.ImportReadFailed(err.Error()), Err: err}
}

// ——— 校验（所有来源）———

var validName = regexp.MustCompile(`^[a-z0-9-]{1,64}$`)

// nameProblem 检查技能名：只能是小写字母、数字、连字符，不超过 64 个字符。
func nameProblem(name string) string {
	if validName.MatchString(name) {
		return ""
	}
	spaces, upper := strings.ContainsAny(name, " \t"), strings.ToLower(name) != name
	switch {
	case spaces && upper:
		return locales.ImportNameHasSpacesAndUppercase()
	case spaces:
		return locales.ImportNameHasSpaces()
	case upper:
		return locales.ImportNameHasUppercase()
	}
	if len(name) > 64 {
		return locales.ImportNameTooLong()
	}
	return locales.ImportNameInvalidCharacters()
}

func sizeProblem(count int, size int64) string {
	switch {
	case count > maxImportFiles:
		return locales.ImportFileCountExceeded(count)
	case size > maxImportBytes:
		return locales.ImportSkillSizeExceeded(humanSize(size))
	}
	return ""
}

func humanSize(n int64) string {
	switch {
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%d KB", (n+512)>>10)
	}
	return fmt.Sprintf("%d B", n)
}

// HumanSize 是给人看的大小。
func HumanSize(n int64) string { return humanSize(n) }

var scriptExts = []string{".sh", ".bash", ".zsh", ".fish", ".py", ".js", ".mjs", ".cjs", ".ts", ".mts", ".rb", ".pl", ".php", ".ps1", ".bat", ".cmd"}

func scriptExt(p string) bool { return slices.Contains(scriptExts, strings.ToLower(path.Ext(p))) }

// execFiles 是目录里有执行位或脚本扩展名的文件，相对目录，按路径排。
func execFiles(dir string) []string {
	var out []string
	filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || d.Type()&fs.ModeSymlink != 0 {
			return nil
		}
		rel, _ := filepath.Rel(dir, p)
		rel = filepath.ToSlash(rel)
		fi, err := d.Info()
		if err == nil && fi.Mode()&0o111 != 0 || scriptExt(rel) {
			out = append(out, rel)
		}
		return nil
	})
	slices.Sort(out)
	return out
}

// markDuplicateNames 给同一批里名称重复的技能（第二个起）记上问题。
func markDuplicateNames(skills []*ImportSkill) {
	seen := map[string]bool{}
	for _, s := range skills {
		if seen[s.Name] && s.Problem == "" {
			s.Problem = locales.ImportDuplicateName(s.Name)
		}
		seen[s.Name] = true
	}
}
