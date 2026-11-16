package library

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"errors"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"

	"skillmanager/locales"
)

// GitHub 压缩包退路：skills.sh 快照缺的文件多、或逐个文件下载失败时，按提交
// 下载一次整个仓库的 tar.gz（codeload.github.com，不占 API 额度、不依赖本机
// git），只解出这批技能所在的目录。内容照样逐个按目录树的 blob 哈希核对。

// rawFileLimit：快照缺的文件不超过这么多时逐个下载，多了改下载压缩包。
const rawFileLimit = 8

// ArchiveRemote 能按提交下载整个仓库的 tar.gz。
type ArchiveRemote interface {
	Archive(ctx context.Context, repo, commit string) (io.ReadCloser, error)
}

func (g *GitHub) Archive(ctx context.Context, repo, commit string) (io.ReadCloser, error) {
	base := g.Codeload
	if base == "" {
		base = "https://codeload.github.com"
	}
	resp, err := g.get(ctx, base+"/"+repo+"/tar.gz/"+commit, "")
	if err != nil {
		return nil, err
	}
	return resp.Body, nil
}

// errNoArchive：上游不提供压缩包（测试里的假上游）。
var errNoArchive = errors.New("upstream provides no archive")

// archive 下载并解出这批技能所在的目录，返回解出的仓库根目录。一批只下载
// 一次；取消时不记结果，下次还能再试。
func (b *ImportBatch) archive(ctx context.Context) (string, error) {
	b.arcMu.Lock()
	defer b.arcMu.Unlock()
	if b.arcDone {
		return b.arcDir, b.arcErr
	}
	dir, err := b.extractArchive(ctx)
	if ctx.Err() != nil {
		os.RemoveAll(dir)
		return "", ctx.Err()
	}
	b.arcDone, b.arcDir, b.arcErr = true, dir, err
	return dir, err
}

func (b *ImportBatch) extractArchive(ctx context.Context) (string, error) {
	ar, ok := b.remote.(ArchiveRemote)
	if !ok {
		return "", errNoArchive
	}
	rc, err := ar.Archive(ctx, b.Source.Repo, b.Commit)
	if err != nil {
		return "", err
	}
	defer rc.Close()
	var prefixes []string
	for _, sk := range b.Skills {
		prefixes = append(prefixes, sk.RepoPath)
	}
	dir := filepath.Join(b.Dir, "repo")
	keep := func(rel string) bool {
		for _, p := range prefixes {
			if p == "" || rel == p || strings.HasPrefix(rel, p+"/") {
				return true
			}
		}
		return false
	}
	if err := untarGz(rc, dir, 1, keep, false); err != nil {
		os.RemoveAll(dir)
		return "", err
	}
	return dir, nil
}

// fromArchive 从压缩包里取一个文件；没有或内容和目录树对不上时返回 false。
func (b *ImportBatch) fromArchive(ctx context.Context, e TreeEntry) ([]byte, bool) {
	dir, err := b.archive(ctx)
	if err != nil {
		return nil, false
	}
	data, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(e.Path)))
	if err != nil || blobID(data) != e.SHA {
		return nil, false
	}
	return data, true
}

// untarGz 把 tar.gz 解到 dst：去掉路径开头 strip 层，只解 keep 认的普通文件
// 和目录。有 ..、绝对路径，或解压后超过上限时整个拒绝。links 为 false 时
// 遇到链接整个拒绝，为 true 时跳过链接。
func untarGz(r io.Reader, dst string, strip int, keep func(rel string) bool, links bool) error {
	zr, err := gzip.NewReader(r)
	if err != nil {
		return errors.New(locales.ImportNotTarGzip())
	}
	defer zr.Close()
	tr := tar.NewReader(zr)
	var total int64
	count := 0
	for {
		h, err := tr.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return errors.New(locales.ArchiveBroken(err.Error()))
		}
		name := strings.TrimSuffix(h.Name, "/")
		if strings.Contains(name, `\`) || name != "" && !filepath.IsLocal(filepath.FromSlash(name)) {
			return errors.New(locales.ArchiveUnsafePath(h.Name))
		}
		parts := strings.Split(name, "/")
		if len(parts) <= strip {
			continue
		}
		rel := path.Join(parts[strip:]...)
		switch h.Typeflag {
		case tar.TypeReg, tar.TypeRegA:
		case tar.TypeDir, tar.TypeXGlobalHeader, tar.TypeXHeader:
			continue
		case tar.TypeSymlink, tar.TypeLink:
			if links {
				continue
			}
			return errors.New(locales.ArchiveHasLink(h.Name))
		default:
			return errors.New(locales.ArchiveUnsupportedType(h.Name))
		}
		if keep != nil && !keep(rel) {
			continue
		}
		total += h.Size
		count++
		if total > maxZipBytes || count > maxZipEntries {
			return errors.New(locales.ImportExtractedArchiveTooLarge())
		}
		to := filepath.Join(dst, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(to), 0o755); err != nil {
			return err
		}
		perm := os.FileMode(0o644)
		if h.Mode&0o111 != 0 {
			perm = 0o755
		}
		out, err := os.OpenFile(to, os.O_CREATE|os.O_EXCL|os.O_WRONLY, perm)
		if err != nil {
			return errors.New(locales.ArchiveDuplicate(h.Name))
		}
		_, err = io.Copy(out, io.LimitReader(tr, h.Size))
		if cerr := out.Close(); err == nil {
			err = cerr
		}
		if err != nil {
			return err
		}
	}
}
