package library

import "os/exec"

// systemProxy 是 macOS 系统设置里的 HTTPS（或 HTTP）代理：从访达启动的
// 应用拿不到终端里设的环境变量。
func systemProxy() string {
	out, err := exec.Command("/usr/sbin/scutil", "--proxy").Output()
	if err != nil {
		return ""
	}
	return parseScutilProxy(string(out))
}
