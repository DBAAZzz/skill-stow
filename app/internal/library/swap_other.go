//go:build !darwin

package library

import (
	"errors"
	"os"

	"skillmanager/locales"
)

type directoryID struct {
	Device uint64
	Inode  uint64
}

func identify(string) (directoryID, error) {
	return directoryID{}, errors.New(locales.LinkUpdateMacOnly())
}
func swapDirectories(string, string) error { return errors.New(locales.LinkUpdateMacOnly()) }

// renameNoReplace 把 a 改名成 b；b 已经存在时不动，返回 os.ErrExist。
func renameNoReplace(a, b string) error {
	if _, err := os.Lstat(b); err == nil {
		return os.ErrExist
	}
	return os.Rename(a, b)
}
