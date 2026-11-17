package library

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"time"
)

// 应用自己的数据文件（settings.json、projects.json）坏了的时候：原件改名
// 挪到旁边，记一条事件，界面提示一次；同一个文件的损坏原件只留最近几份。

// keepCorrupt 是一个文件最多留几份损坏原件。
const keepCorrupt = 3

const corruptMark = ".corrupt-"

// readJSON 读出 file 里的 JSON；文件不存在或读不了时返回 def。
//
// 内容解析不了时也返回 def，并把原件改名挪到旁边，返回挪到了哪：调用方
// 之后会拿 def 写回同一个文件，不挪开的话，用户原来的内容就被无声覆盖了。
func readJSON[T any](file string, def T) (T, string) {
	b, err := os.ReadFile(file)
	if err != nil {
		return def, ""
	}
	v := def
	if err := json.Unmarshal(b, &v); err != nil {
		dir := filepath.Dir(file)
		moved := file + corruptMark + time.Now().Format("20060102-150405")
		e := Event{Level: LevelError, Cat: "appdata", Op: "corrupt", Target: filepath.Base(file), Kind: "parse", Msg: safeErrMsg(dir, err)}
		if rerr := os.Rename(file, moved); rerr != nil {
			e.Data = map[string]string{"backup": "failed: " + safeErrMsg(dir, rerr)}
			moved = ""
		} else {
			e.Data = map[string]string{"backup": filepath.Base(moved)}
			pruneCorrupt(file)
		}
		appendEvent(dir, e)
		return def, moved
	}
	return v, ""
}

// pruneCorrupt 删掉 file 较早的损坏原件，只留最近 keepCorrupt 份。
// 文件名里的时间按字典序就是先后。
func pruneCorrupt(file string) {
	copies, _ := filepath.Glob(file + corruptMark + "*")
	slices.Sort(copies)
	for _, p := range copies[:max(0, len(copies)-keepCorrupt)] {
		os.Remove(p)
	}
}

// CorruptCopies 是数据目录里留着的损坏原件（文件名），旧的在前。
func (l *Library) CorruptCopies() []string {
	if !l.disk {
		return nil
	}
	return corruptCopiesIn(appDir(l.Home))
}

func corruptCopiesIn(dir string) []string {
	copies, _ := filepath.Glob(filepath.Join(dir, "*"+corruptMark+"*"))
	for i, p := range copies {
		copies[i] = filepath.Base(p)
	}
	slices.Sort(copies)
	return copies
}

// Recovered 是这次扫描时发现损坏、刚挪到旁边的原件（完整路径），界面据此
// 提示一次。之后的扫描不会再列出它们。
func (l *Library) Recovered() []string { return l.recovered }

// AppDir 是应用数据目录（设置、记录、日志所在）；示例数据为空。
func (l *Library) AppDir() string {
	if !l.disk {
		return ""
	}
	return appDir(l.Home)
}
