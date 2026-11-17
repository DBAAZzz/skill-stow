package library

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// corruptCopies 是 file 旁边挪开保存的损坏原件。
func corruptCopies(t *testing.T, file string) []string {
	t.Helper()
	got, err := filepath.Glob(file + ".corrupt-*")
	must(t, err)
	return got
}

func TestCorruptSettingsAreKeptAsideInsteadOfOverwritten(t *testing.T) {
	// Given 一份写坏了的 settings.json
	home := t.TempDir()
	broken := `{"language": "en", "hiddenAgents": ["codex"`
	write(t, appDir(home), "settings.json", broken, 0o644)

	// When 启动扫描，再保存一次设置
	l := Scan(home)
	must(t, l.SaveSettings())

	// Then 设置回到默认，原件原样留在旁边，没有被这次保存覆盖
	if l.Settings.Language != "" || len(l.Settings.HiddenAgents) != 0 || !l.Settings.AutoDiscover {
		t.Errorf("坏文件的设置应回到默认：%+v", l.Settings)
	}
	kept := corruptCopies(t, settingsFile(home))
	if len(kept) != 1 {
		t.Fatalf("应留下一份损坏原件，got %v", kept)
	}
	if b, _ := os.ReadFile(kept[0]); string(b) != broken {
		t.Errorf("损坏原件内容变了：%q", b)
	}
}

func TestCorruptProjectListIsKeptAsideWhenAddingAProject(t *testing.T) {
	// Given 一份写坏了的 projects.json，里面有用户手动加过的项目
	home := t.TempDir()
	broken := `{"projects": [{"dir": "/Users/me/code/app", "added": "2026-01-01T00:00:00Z"},`
	write(t, appDir(home), "projects.json", broken, 0o644)
	l := Scan(home)

	// When 再手动加一个项目
	must(t, l.AddProjects([]string{filepath.Join(home, "code/new")}))

	// Then 新列表只有新项目，原件原样留在旁边
	if got := readManual(home); len(got) != 1 {
		t.Errorf("新列表应只有新加的项目：%+v", got)
	}
	kept := corruptCopies(t, manualFile(home))
	if len(kept) != 1 {
		t.Fatalf("应留下一份损坏原件，got %v", kept)
	}
	if b, _ := os.ReadFile(kept[0]); string(b) != broken {
		t.Errorf("损坏原件内容变了：%q", b)
	}
}

func TestReadableAppDataIsNotMovedAside(t *testing.T) {
	// Given 正常的设置和项目列表
	home := t.TempDir()
	write(t, appDir(home), "settings.json", `{"language": "en"}`, 0o644)
	must(t, writeManual(home, []manualProject{{Dir: filepath.Join(home, "code/app")}}))

	// When 扫描
	l := Scan(home)

	// Then 照常读出，不留任何损坏副本
	if l.Settings.Language != "en" || len(readManual(home)) != 1 {
		t.Errorf("正常文件应照常读出：%+v", l.Settings)
	}
	if n := len(corruptCopies(t, settingsFile(home))) + len(corruptCopies(t, manualFile(home))); n != 0 {
		t.Errorf("正常文件不应被挪开，留下了 %d 份", n)
	}
}

func TestCorruptBackupRenameFailureRedactsHomePath(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root 不受目录只读权限限制")
	}
	home := t.TempDir()
	dir := appDir(home)
	write(t, dir, "settings.json", "{broken", 0o644)
	must(t, os.MkdirAll(filepath.Join(dir, "logs"), 0o755))
	// 让 dir 只读，触发 os.Rename 失败；logs 子目录仍可写以记录事件
	must(t, os.Chmod(dir, 0o555))
	t.Cleanup(func() { os.Chmod(dir, 0o755) })

	Scan(home)

	all := readEvents(dir)
	var found bool
	for _, e := range all {
		if e.Cat == "appdata" && e.Op == "corrupt" {
			found = true
			if strings.Contains(e.Data["backup"], home) {
				t.Errorf("备份改名失败的 Data 不应含主目录：%q", e.Data["backup"])
			}
		}
	}
	if !found {
		t.Fatal("应记一条 appdata.corrupt 事件")
	}
}
