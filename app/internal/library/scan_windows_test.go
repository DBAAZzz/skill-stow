//go:build windows

package library

import (
	"os"
	"strings"
	"testing"
)

func TestVSCodeFolderURIBecomesTheSameProjectAsClaudeCodePath(t *testing.T) {
	// VS Code 记 file:///c%3A/…，Claude Code 记 C:\…
	got := canonicalProjectDir(fileURIPath("file:///c%3A/Users/me/code/app"))
	if want := `C:\Users\me\code\app`; got != want {
		t.Errorf("应为 %q，得到 %q", want, got)
	}
}

func TestManualProjectSavedWithLowercaseDriveIsFoundAndRemovable(t *testing.T) {
	// Given 旧版本记下的手动项目用了小写盘符，目录真实存在
	home, h := linkHome(t)
	dir := h("work/demo")
	must(t, os.MkdirAll(dir, 0o755))
	lower := strings.ToLower(dir[:1]) + dir[1:]
	must(t, writeManual(home, []manualProject{{Dir: lower}}))

	// When 扫描
	l := Scan(home)

	// Then 它不算找不到，也不会因为大写写法再加一条
	manual := l.ManualProjects()
	if len(manual) != 1 || manual[0].Missing {
		t.Fatalf("应找到这个手动项目：%+v", manual)
	}
	must(t, l.AddProjects([]string{dir}))
	if got := readManual(home); len(got) != 1 {
		t.Fatalf("大小写不同的同一目录不应重复添加：%+v", got)
	}

	// When 从设置里移除界面上显示的那一项
	must(t, l.RemoveProjects([]string{manual[0].Dir}))

	// Then 旧记录也被删掉
	if got := readManual(home); len(got) != 0 {
		t.Errorf("旧记录应被移除：%+v", got)
	}
}
