package library

import "os"

// 本文件把「链接」收成一个概念。macOS 上链接就是符号链接，Windows 上还可能是
// 目录联接（junction）：普通用户没有 SeCreateSymbolicLinkPrivilege，建不了符号
// 链接，只能退回目录联接。两者在 Go 眼里不一样——Lstat 对目录联接既不给
// ModeSymlink 也不给 ModeDir，EvalSymlinks 根本不跟着它走——所以判断和解析
// 都要经过这里，不要直接用 ModeSymlink 和 filepath.EvalSymlinks。

// isLink 判断 p 是不是本应用认的链接（符号链接或目录联接）。
func isLink(p string) bool {
	fi, err := os.Lstat(p)
	if err != nil {
		return false
	}
	if fi.Mode()&os.ModeSymlink != 0 {
		return true
	}
	// 目录联接只能靠 Readlink 认出来：读得到重解析点就是链接。
	_, err = os.Readlink(p)
	return err == nil
}

// makeLink 在 dst 建一个指向 target 的链接。target 可以是相对路径。
func makeLink(target, dst string) error {
	return makeLinkPlatform(target, dst)
}

// realPath 解开 p 里所有的链接，返回真实路径。路径不存在时返回错误，
// 和 filepath.EvalSymlinks 一致。
func realPath(p string) (string, error) {
	return realPathPlatform(p)
}
