//go:build windows

package library

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"skillmanager/locales"
)

// Windows 默认不给普通用户 SeCreateSymbolicLinkPrivilege：建符号链接会返回
// 1314。开了「开发者模式」或提权才有。没有权限时退到目录联接，它不需要特权。
const errPrivilegeNotHeld = syscall.Errno(1314)

// maxLinkHops 和 filepath.EvalSymlinks 的上限一致：跟了这么多层还没到头，
// 当成链接成环。
const maxLinkHops = 255

var errTooManyLinks = textErr(locales.LinkTooManyLevels)

// makeLinkPlatform 先按符号链接建，语义和 macOS 一致；系统拒绝权限时
// 改用目录联接。目录联接只能指向绝对路径，也只能是目录。
func makeLinkPlatform(target, dst string) error {
	err := os.Symlink(target, dst)
	if err == nil {
		return nil
	}
	if !errors.Is(err, errPrivilegeNotHeld) {
		return err
	}
	abs := target
	if !filepath.IsAbs(abs) {
		// 相对目标要从目录的真实位置算：目录本身可能就是一个链接，
		// 直接按链接路径拼会跳错层级。
		base := filepath.Dir(dst)
		if real, err := realPath(base); err == nil {
			base = real
		}
		abs = filepath.Join(base, target)
	}
	abs = filepath.Clean(abs)
	// 目录联接只能指向目录。文件链接没有无特权的替代方案，保持原来的错误。
	if fi, statErr := os.Stat(abs); statErr != nil || !fi.IsDir() {
		return err
	}
	return makeJunction(abs, dst)
}

// realPathPlatform 逐段解开链接。filepath.EvalSymlinks 只跟 ModeSymlink，
// 不跟目录联接，所以这里自己走一遍。
func realPathPlatform(p string) (string, error) {
	if !filepath.IsAbs(p) {
		return filepath.EvalSymlinks(p)
	}
	hops := 0
	return evalLinks(p, &hops)
}

// evalLinks 解开绝对路径 p 里的链接。hops 是整次解析已经跟过的链接数，
// 递归时共用，链接成环时靠它停下来。
func evalLinks(p string, hops *int) (string, error) {
	vol := filepath.VolumeName(p)
	sep := string(filepath.Separator)
	rest := strings.TrimPrefix(strings.TrimPrefix(p, vol), sep)
	cur := vol + sep
	for _, part := range strings.Split(rest, sep) {
		if part == "" || part == "." {
			continue
		}
		next := filepath.Join(cur, part)
		if !isLink(next) {
			cur = next
			continue
		}
		if *hops++; *hops > maxLinkHops {
			return "", &os.PathError{Op: "readlink", Path: next, Err: errTooManyLinks}
		}
		raw, err := os.Readlink(next)
		if err != nil {
			return "", err
		}
		if !filepath.IsAbs(raw) {
			raw = filepath.Join(filepath.Dir(next), raw)
		}
		if cur, err = evalLinks(raw, hops); err != nil {
			return "", err
		}
	}
	if _, err := os.Lstat(cur); err != nil {
		return "", err
	}
	return filepath.Clean(cur), nil
}
