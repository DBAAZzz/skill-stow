package library

import (
	"archive/zip"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"skillmanager/locales"
)

// 本机文件夹和 zip：一律复制到暂存，不移动、不改原文件。文件夹根目录有
// SKILL.md 就是一个技能；没有就往下找（最多 6 层，覆盖 GitHub 整库 zip 的
// 顶层包装目录及仓库子目录），每个含 SKILL.md 的子目录算一个。
// zip 先解压到暂存，按同样规则找；zip 里有不安全的路径（..、绝对路径）时
// 整个 zip 都不读。

const (
	// maxZipBytes、maxZipEntries 是一个 zip 解压后的上限，防止解压炸弹。
	maxZipBytes        = 200 << 20
	maxZipEntries      = 20000
	maxLocalSkillDepth = 6
)

// ReadLocalImport 读拖入或选取的文件夹、zip（可以一次多个）。
func ReadLocalImport(home string, paths []string) (b *ImportBatch, err error) {
	src := ImportSource{Kind: ImportLocal, Paths: paths, Input: strings.Join(paths, "\n")}
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
	var roots []string // 要找技能的目录：文件夹本身，或 zip 解压出来的
	var zipErr error
	for i, p := range paths {
		fi, err := os.Stat(p)
		if err != nil {
			return nil, &ImportError{Msg: locales.ImportCantRead(filepath.Base(p)), Err: err}
		}
		switch {
		case fi.IsDir():
			roots = append(roots, p)
		case strings.EqualFold(filepath.Ext(p), ".zip"):
			dst := filepath.Join(b.Dir, fmt.Sprintf("zip-%d", i), strings.TrimSuffix(filepath.Base(p), filepath.Ext(p)))
			if err := unzip(p, dst); err != nil {
				zipErr = &ImportError{Msg: locales.ImportZipFailed(filepath.Base(p), err.Error()), Err: err}
				continue
			}
			roots = append(roots, dst)
		default:
			return nil, &ImportError{Msg: locales.ImportNotFolderOrZip(filepath.Base(p)), Hint: locales.ImportDropFolderOrZip()}
		}
	}
	if zipErr != nil && len(roots) == 0 {
		return nil, zipErr
	}
	n := 0
	for _, root := range roots {
		dirs, others := findLocalSkills(root)
		b.NotSkills = append(b.NotSkills, others...)
		for _, d := range dirs {
			sk := &ImportSkill{Folder: filepath.Base(d), RepoPath: originalPath(b, paths, d), Dir: filepath.Join(b.Dir, fmt.Sprint(n))}
			n++
			if err := copySkillDir(d, sk); err != nil {
				return nil, err
			}
			b.Bytes += sk.Size
			b.Skills = append(b.Skills, sk)
		}
	}
	if len(b.Skills) == 0 {
		if zipErr != nil {
			return nil, zipErr
		}
		return nil, notSkillsError(paths, b.NotSkills)
	}
	markDuplicateNames(b.Skills)
	return b, nil
}

// originalPath 是技能在用户电脑上的位置：zip 里的写成「xx.zip/子目录」。
func originalPath(b *ImportBatch, paths []string, dir string) string {
	rel, err := filepath.Rel(b.Dir, dir)
	if err != nil || strings.HasPrefix(rel, "..") {
		return dir
	}
	// zip-<i>/<zip 名>/<子目录…>
	parts := strings.SplitN(filepath.ToSlash(rel), "/", 3)
	var i int
	if len(parts) >= 2 && strings.HasPrefix(parts[0], "zip-") {
		fmt.Sscan(strings.TrimPrefix(parts[0], "zip-"), &i)
		if i < len(paths) {
			if len(parts) == 3 {
				return filepath.Join(paths[i], parts[2])
			}
			return paths[i]
		}
	}
	return dir
}

func notSkillsError(paths, others []string) error {
	name := filepath.Base(paths[0])
	if len(paths) > 1 {
		name = locales.ImportDroppedContent()
	}
	e := &ImportError{Msg: locales.ImportNoSkillMd(name)}
	if len(others) > 0 {
		shown := others
		if len(shown) > 3 {
			shown = shown[:3]
		}
		e.Hint = locales.ImportFoldersNotSkills(len(others), strings.Join(shown, locales.ListSeparator()))
	}
	return e
}

// findLocalSkills 找 root 里的技能目录；others 是第一层里不是技能、下面也没有技能的文件夹名。
func findLocalSkills(root string) (skills, others []string) {
	if isSkill(root) {
		return []string{root}, nil
	}
	items, _ := os.ReadDir(root)
	for _, it := range items {
		if !it.IsDir() || shouldSkipLocalScanDir(it.Name()) {
			continue
		}
		d := filepath.Join(root, it.Name())
		if found := collectLocalSkills(d, 1); len(found) > 0 {
			skills = append(skills, found...)
			continue
		}
		if !skipped(it.Name()) {
			others = append(others, it.Name())
		}
	}
	return skills, others
}

func shouldSkipLocalScanDir(name string) bool {
	return name == "__MACOSX" || slices.Contains(skipDirs, name)
}

func collectLocalSkills(dir string, depth int) []string {
	if isSkill(dir) {
		return []string{dir}
	}
	if depth >= maxLocalSkillDepth {
		return nil
	}
	items, _ := os.ReadDir(dir)
	var skills []string
	for _, it := range items {
		if !it.IsDir() || shouldSkipLocalScanDir(it.Name()) {
			continue
		}
		skills = append(skills, collectLocalSkills(filepath.Join(dir, it.Name()), depth+1)...)
	}
	return skills
}

// copySkillDir 把技能目录复制到 sk.Dir，同时检查：链接不得指向技能目录以外，
// 只认普通文件、目录和链接；超过上限的不复制，记进 Problem。
func copySkillDir(src string, sk *ImportSkill) error {
	realSrc, err := filepath.EvalSymlinks(src)
	if err != nil {
		return err
	}
	// 先数一遍，超过上限就只复制 SKILL.md 供预览
	var problem string
	filepath.WalkDir(realSrc, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			problem = locales.ImportUnreadableFiles(err.Error())
			return filepath.SkipAll
		}
		if p == realSrc {
			return nil
		}
		rel, _ := filepath.Rel(realSrc, p)
		switch t := d.Type(); {
		case t&fs.ModeSymlink != 0:
			if !linkInside(realSrc, p) {
				problem = locales.ImportLinkOutside(filepath.ToSlash(rel))
				return filepath.SkipAll
			}
			sk.Count++
		case d.IsDir():
		case t.IsRegular():
			if fi, err := d.Info(); err == nil {
				sk.Size += fi.Size()
			}
			sk.Count++
		default:
			problem = locales.ImportUnsupportedType(filepath.ToSlash(rel))
			return filepath.SkipAll
		}
		return nil
	})
	if problem == "" {
		problem = sizeProblem(sk.Count, sk.Size)
	}
	sk.Problem = problem
	if problem != "" {
		if err := os.MkdirAll(sk.Dir, 0o755); err != nil {
			return err
		}
		if b, err := os.ReadFile(filepath.Join(realSrc, "SKILL.md")); err == nil {
			os.WriteFile(filepath.Join(sk.Dir, "SKILL.md"), b, 0o644)
		}
	} else if err := copyDir(realSrc, sk.Dir); err != nil {
		return &ImportError{Msg: locales.ImportCopyFailed(filepath.Base(src), err.Error()), Err: err}
	} else if rel, ok := linksInside(sk.Dir); !ok {
		// 检查之后原目录可能又变了：以复制出来的为准再查一遍
		sk.Problem = locales.ImportLinkOutside(filepath.ToSlash(rel))
	}
	(&ImportBatch{}).describe(sk)
	if sk.Problem == "" {
		if sk.Tree, err = TreeHash(sk.Dir); err != nil {
			sk.Problem = locales.ImportUnreadable(err.Error())
		}
	}
	return nil
}

// linkInside 报告 root 里的链接 p 最终指向 root 之内：目标不是绝对路径，
// 按字面不出 root，顺着整条链接链解析后也仍在 root 里（a -> .、b -> a/..
// 这类字面合法、解析后出界的链接不算）。悬空、成环的都不算。
func linkInside(root, p string) bool {
	target, err := os.Readlink(p)
	if err != nil || filepath.IsAbs(target) {
		return false
	}
	rel, err := filepath.Rel(root, p)
	if err != nil || !filepath.IsLocal(filepath.Join(filepath.Dir(rel), target)) {
		return false
	}
	realRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return false
	}
	resolved, err := filepath.EvalSymlinks(p)
	if err != nil {
		return false
	}
	in, err := filepath.Rel(realRoot, resolved)
	return err == nil && (in == "." || filepath.IsLocal(in))
}

// linksInside 查 root 下所有链接，有出界的返回它的相对路径和 false。
func linksInside(root string) (string, bool) {
	bad := ""
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.Type()&fs.ModeSymlink != 0 && !linkInside(root, p) {
			bad, _ = filepath.Rel(root, p)
			return filepath.SkipAll
		}
		return nil
	})
	if err != nil && bad == "" {
		return ".", false
	}
	return bad, bad == ""
}

// unzip 解压到 dst。有 ..、绝对路径或超过上限时整个拒绝。zip 里的链接
// 写成链接，指向哪里由 copySkillDir 再查。
func unzip(file, dst string) error {
	r, err := zip.OpenReader(file)
	if err != nil {
		return errors.New(locales.ZipCantOpen(err.Error()))
	}
	defer r.Close()
	if len(r.File) > maxZipEntries {
		return errors.New(locales.ZipTooManyFiles())
	}
	var total uint64
	for _, f := range r.File {
		name := strings.TrimSuffix(f.Name, "/")
		if name == "" {
			continue
		}
		if strings.Contains(f.Name, `\`) || !filepath.IsLocal(filepath.FromSlash(name)) {
			return errors.New(locales.ZipUnsafePath(f.Name))
		}
		total += f.UncompressedSize64
		if total > maxZipBytes {
			return errors.New(locales.ZipTooLarge())
		}
	}
	// 链接放到最后建：建之前所有文件和目录都已写完，写入不会顺着链接走出 dst
	var links []*zip.File
	for _, f := range r.File {
		name := filepath.FromSlash(strings.TrimSuffix(f.Name, "/"))
		if name == "" || strings.HasPrefix(name, "__MACOSX") || filepath.Base(name) == ".DS_Store" {
			continue
		}
		to := filepath.Join(dst, name)
		mode := f.Mode()
		switch {
		case f.FileInfo().IsDir():
			if err := os.MkdirAll(to, 0o755); err != nil {
				return err
			}
			continue
		case mode&fs.ModeSymlink != 0:
			links = append(links, f)
			continue
		case !mode.IsRegular() && mode.Type() != 0:
			return errors.New(locales.ZipUnsupportedType(f.Name))
		}
		if err := os.MkdirAll(filepath.Dir(to), 0o755); err != nil {
			return err
		}
		perm := os.FileMode(0o644)
		if mode&0o111 != 0 {
			perm = 0o755
		}
		if err := extractZipFile(f, to, perm); err != nil {
			return err
		}
	}
	for _, f := range links {
		name := filepath.FromSlash(strings.TrimSuffix(f.Name, "/"))
		// 链接套在另一个链接下面（alias -> 外面、alias/x -> …）同样会走出 dst
		if linkedParent(dst, name) {
			return errors.New(locales.ZipUnsafeLink(f.Name))
		}
		to := filepath.Join(dst, name)
		if err := os.MkdirAll(filepath.Dir(to), 0o755); err != nil {
			return err
		}
		target, err := readZipFile(f, 4096)
		if err != nil {
			return err
		}
		if err := os.Symlink(string(target), to); err != nil {
			return errors.New(locales.ZipBadLink(f.Name))
		}
	}
	return nil
}

// linkedParent 报告 dst 下 name 的上级目录里有没有链接。
func linkedParent(dst, name string) bool {
	p := dst
	for _, part := range strings.Split(filepath.Dir(name), string(filepath.Separator)) {
		if part == "." {
			break
		}
		p = filepath.Join(p, part)
		fi, err := os.Lstat(p)
		if err != nil {
			return false // 还不存在：之后由 MkdirAll 建成普通目录
		}
		if fi.Mode()&fs.ModeSymlink != 0 {
			return true
		}
	}
	return false
}

func readZipFile(f *zip.File, limit int64) ([]byte, error) {
	rc, err := f.Open()
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	return io.ReadAll(io.LimitReader(rc, limit))
}

func extractZipFile(f *zip.File, to string, perm os.FileMode) error {
	rc, err := f.Open()
	if err != nil {
		return err
	}
	defer rc.Close()
	out, err := os.OpenFile(to, os.O_CREATE|os.O_EXCL|os.O_WRONLY, perm)
	if err != nil {
		return err
	}
	// 按声明的大小截断：声明小、实际大的条目不让它撑爆磁盘
	_, err = io.Copy(out, io.LimitReader(rc, int64(f.UncompressedSize64)+1))
	if cerr := out.Close(); err == nil {
		err = cerr
	}
	return err
}

// LocalSkillCandidates 报告这些路径里是不是可能有技能（文件夹或 .zip），
// 拖入时用来决定要不要接。
func LocalSkillCandidates(paths []string) bool {
	return len(paths) > 0 && !slices.ContainsFunc(paths, func(p string) bool {
		fi, err := os.Stat(p)
		return err != nil || !fi.IsDir() && !strings.EqualFold(filepath.Ext(p), ".zip")
	})
}
