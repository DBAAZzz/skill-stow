//go:build !windows

package library

import (
	"os"
	"path/filepath"
)

// macOS 和 Linux 上符号链接随手可建，直接用。
func makeLinkPlatform(target, dst string) error {
	return os.Symlink(target, dst)
}

func makeSymlinkPlatform(target, dst string) error {
	return os.Symlink(target, dst)
}

func realPathPlatform(p string) (string, error) {
	return filepath.EvalSymlinks(p)
}
