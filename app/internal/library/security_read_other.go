//go:build !darwin

package library

import (
	"context"
	"errors"

	"skillmanager/internal/security"
)

// 本地安全检查只做 macOS；其他平台保持可编译，界面不显示入口。

// SecuritySupported 报告本平台能不能做本地安全检查。
func SecuritySupported() bool { return false }

type readLimits struct {
	entries int
	depth   int
	file    int64
	total   int64
}

var defaultLimits = readLimits{entries: 2000, depth: 32, file: 1 << 20, total: 20 << 20}

var errSecurityUnsupported = errors.New("security: unsupported platform")

func readSkill(context.Context, string, readLimits) (security.Input, string, error) {
	return security.Input{}, "", errSecurityUnsupported
}

func metaDigest(context.Context, string, readLimits) (string, error) {
	return "", errSecurityUnsupported
}
