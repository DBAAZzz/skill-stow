//go:build windows

package library

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestJunctionKeepsShellMetacharactersInPathsLiteral(t *testing.T) {
	// Given 目标目录和链接名里都有 cmd 会当成语法的字符
	home, h := linkHome(t)
	target := h(`R&D ^x %PATH% (1)`)
	writeSkill(t, target, "")
	at := h(`skill&echo pwned`)

	// When 建目录联接
	must(t, makeJunction(target, at))

	// Then 链接原样指向目标，透过它能读到技能
	if got, err := os.Readlink(at); err != nil || got != target {
		t.Fatalf("链接应指向 %q，得到 %q %v", target, got, err)
	}
	if _, err := os.Stat(filepath.Join(at, "SKILL.md")); err != nil {
		t.Fatalf("透过链接读不到 Skill：%v", err)
	}
	if real, err := realPath(at); err != nil || real != target {
		t.Errorf("真实路径应为 %q，得到 %q %v", target, real, err)
	}
	if entries, _ := os.ReadDir(home); len(entries) != 2 {
		t.Errorf("主目录里只应有目标和链接两项，得到 %v", entries)
	}
}

func TestRealPathStopsOnJunctionLoop(t *testing.T) {
	// Given 两个互相指向的目录联接（联接不要求目标存在）
	_, h := linkHome(t)
	must(t, makeJunction(h("b"), h("a")))
	must(t, makeJunction(h("a"), h("b")))

	// When 解析真实路径
	_, err := realPath(h("a"))

	// Then 报链接过多，而不是无限递归
	if !errors.Is(err, errTooManyLinks) {
		t.Errorf("应报链接过多，得到 %v", err)
	}
}
