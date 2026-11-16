package library

import (
	"fmt"
	"github.com/ebitengine/purego"
	"os"
	"runtime"
	"sync"
	"syscall"

	"skillmanager/locales"
)

type directoryID struct {
	Device uint64
	Inode  uint64
}

func identify(dir string) (directoryID, error) {
	fi, err := os.Lstat(dir)
	if err != nil {
		return directoryID{}, err
	}
	if !fi.IsDir() {
		return directoryID{}, ErrUpdateChanged
	}
	st := fi.Sys().(*syscall.Stat_t)
	return directoryID{uint64(st.Dev), st.Ino}, nil
}

type swapFunctions struct {
	rename func(string, string, uint32) int32
	errno  func() *int32
}

var swapAPI = sync.OnceValues(func() (swapFunctions, error) {
	h, err := purego.Dlopen("/usr/lib/libSystem.B.dylib", purego.RTLD_NOW)
	if err != nil {
		return swapFunctions{}, err
	}
	var api swapFunctions
	purego.RegisterLibFunc(&api.rename, h, "renamex_np")
	purego.RegisterLibFunc(&api.errno, h, "__error")
	return api, nil
})

func swapDirectories(a, b string) error {
	api, err := swapAPI()
	if err != nil {
		return err
	}
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	if api.rename(a, b, 2) != 0 {
		return fmt.Errorf("%s%w", locales.LinkSafeUpdateUnsupported(), syscall.Errno(*api.errno()))
	}
	return nil
}

// renameNoReplace 把 a 改名成 b；b 已经存在时不动，返回 os.ErrExist。
func renameNoReplace(a, b string) error {
	api, err := swapAPI()
	if err != nil {
		return err
	}
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	const renameExcl = 0x4 // RENAME_EXCL
	if api.rename(a, b, renameExcl) != 0 {
		errno := syscall.Errno(*api.errno())
		if errno == syscall.EEXIST {
			return os.ErrExist
		}
		return &os.LinkError{Op: "rename", Old: a, New: b, Err: errno}
	}
	return nil
}
