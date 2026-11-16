package library

import (
	"path/filepath"
	"runtime"
)

// appDirName 是数据目录的名字。它是磁盘上的位置，不跟着界面语言或显示名
// 变：改了名字，用户的设置和停用记录就找不到了。
const appDirName = "Skill Stow"

// supportDir 是放应用配置的系统目录：macOS 的 Application Support，
// Windows 的 %APPDATA%。
func supportDir(home string) string {
	if runtime.GOOS == "windows" {
		return filepath.Join(home, "AppData", "Roaming")
	}
	return filepath.Join(home, "Library", "Application Support")
}

func appDir(home string) string { return filepath.Join(supportDir(home), appDirName) }
