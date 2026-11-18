package library

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// fakeHome 里的 test-first：技能库一份（Claude Code 链到它），CodeBuddy
// 一份不同的拷贝，没装的 Codex 又一份不同的。
func TestVersions(t *testing.T) {
	l := Scan(fakeHome(t))
	tf := l.ByID("mine:test-first")
	vs := tf.Versions()
	if len(vs) != 3 {
		t.Fatalf("应有 3 种内容，得到 %d", len(vs))
	}
	if !vs[0].Shared || len(vs[0].Places) != 2 {
		t.Errorf("基准应是技能库那种，含 Claude Code 的链接：%+v", vs[0])
	}
	if !tf.CopyIn("codebuddy") || tf.CopyIn("claude-code") {
		t.Errorf("CodeBuddy 目录里是副本，Claude Code 是链接：%v", tf.Copied())
	}
	p := l.Problems()
	if len(p.Diff) != 1 || p.Diff[0] != tf || len(p.Same) != 0 {
		t.Errorf("test-first 应在内容不同的副本里：%v %v", p.Diff, p.Same)
	}
}

func TestMergeKeepBase(t *testing.T) {
	home := fakeHome(t)
	l := Scan(home)
	r, err := l.Merge(l.ByID("mine:test-first"), 0)
	must(t, err)
	// CodeBuddy 不读技能库：换成链接；Codex 读技能库：拷贝移到废纸篓
	if r.Linked != 1 || r.Trashed != 1 {
		t.Errorf("结果 %+v，应 1 处换成链接、1 处移到废纸篓", r)
	}
	if got := r.String(); got != "1 处换成链接，1 处移到废纸篓。" {
		t.Errorf("说明：%q", got)
	}
	cb := filepath.Join(home, ".codebuddy/skills/test-first")
	if real, _ := realPath(cb); real != filepath.Join(home, ".agents/skills/test-first") {
		t.Errorf("CodeBuddy 的应指向技能库，指向 %s", real)
	}
	if _, err := os.Stat(filepath.Join(home, ".Trash/test-first/SKILL.md")); err != nil {
		t.Error("Codex 的拷贝应在废纸篓里")
	}
	l = Scan(home)
	tf := l.ByID("mine:test-first")
	if tf.Copies != 0 || len(tf.Versions()) != 0 || len(tf.Copied()) != 0 {
		t.Errorf("收成一份后不应再有副本：%d %v", tf.Copies, tf.Copied())
	}
	if !tf.EnabledIn("codebuddy") {
		t.Error("CodeBuddy 换成链接后仍应启用")
	}
}

func TestMergeKeepOther(t *testing.T) {
	home := fakeHome(t)
	l := Scan(home)
	tf := l.ByID("mine:test-first")
	keep := slices.IndexFunc(tf.Versions(), func(v Version) bool { return strings.Contains(v.Real, ".codebuddy") })
	r, err := l.Merge(tf, keep)
	must(t, err)
	if r.Trashed != 2 || r.Linked != 1 {
		t.Errorf("结果 %+v：技能库原来那份和 Codex 的进废纸篓，CodeBuddy 的换成链接", r)
	}
	md, _ := os.ReadFile(filepath.Join(home, ".agents/skills/test-first/SKILL.md"))
	if !strings.Contains(string(md), "一份拷贝") {
		t.Errorf("技能库应换成 CodeBuddy 那份：%s", md)
	}
	if _, err := os.Stat(filepath.Join(home, ".agents/skills/test-first/notes.md")); err == nil {
		t.Error("技能库原来那份的文件不应留下")
	}
	if real, _ := realPath(filepath.Join(home, ".claude/skills/test-first")); real != filepath.Join(home, ".agents/skills/test-first") {
		t.Error("Claude Code 的链接应仍指向技能库")
	}
	if tf := Scan(home).ByID("mine:test-first"); tf.Copies != 0 {
		t.Errorf("还有 %d 份副本", tf.Copies)
	}
}

func TestMergeIdentical(t *testing.T) {
	home := fakeHome(t)
	src := filepath.Join(home, ".agents/skills/spark")
	must(t, copyDir(src, filepath.Join(home, ".codebuddy/skills/spark")))
	l := Scan(home)
	sp := l.ByID("mine:spark")
	if !sp.Identical() || !l.CanMerge(sp) {
		t.Fatalf("spark 应有内容相同的副本：%d 份，%d 种", sp.Copies, len(sp.Versions()))
	}
	if p := l.Problems(); !slices.Contains(p.Same, sp) {
		t.Errorf("spark 应在内容相同的副本里：%v", p.Same)
	}
	r, err := l.Merge(sp, 0)
	must(t, err)
	if r.Linked != 1 {
		t.Errorf("结果 %+v", r)
	}
}

// Agent 跑过一次脚本，那份多出 Python 的缓存：不能说成完全一样（需求 §8），
// 算内容不同，让人对比后决定。
func TestVersionsCountPythonCacheAsADifference(t *testing.T) {
	home := fakeHome(t)
	cb := filepath.Join(home, ".codebuddy/skills/spark")
	must(t, copyDir(filepath.Join(home, ".agents/skills/spark"), cb))
	must(t, os.MkdirAll(filepath.Join(cb, "scripts/__pycache__"), 0o755))
	must(t, os.WriteFile(filepath.Join(cb, "scripts/__pycache__/run.cpython-311.pyc"), []byte{0xa7, 0x0d, 0, 0}, 0o644))
	sp := Scan(home).ByID("mine:spark")
	if sp.Identical() || len(sp.Versions()) != 2 {
		t.Fatalf("多了 __pycache__，应算内容不同：%d 种", len(sp.Versions()))
	}
}

func TestDiffLines(t *testing.T) {
	rows := DiffLines("a\nb\nc\nd\n", "a\nB\nc\nd\ne\n")
	var kinds []RowKind
	for _, r := range rows {
		kinds = append(kinds, r.Kind)
	}
	if want := []RowKind{Same, Changed, Same, Same, Inserted}; !slices.Equal(kinds, want) {
		t.Errorf("得到 %v，应为 %v", kinds, want)
	}
	if r := rows[4]; r.Left != 0 || r.Right != 5 {
		t.Errorf("加上的一行：%+v", r)
	}
	if s, e, _, _ := InlineChange("version: 1.2", "version: 1.3"); s != 11 || e != 12 {
		t.Errorf("改了的部分 [%d,%d)", s, e)
	}
}

func TestCompareSample(t *testing.T) {
	l := Sample()
	tf := l.Get("test-first")
	vs := tf.Versions()
	files := l.Compare(&vs[0], &vs[1])
	if len(files) != 2 || files[0].Path != "SKILL.md" || !files[1].OnlyLeft {
		t.Fatalf("应是 SKILL.md 和只在基准里的 references/test-levels.md：%+v", files)
	}
	if files[0].Added == 0 || files[0].Deleted == 0 {
		t.Errorf("SKILL.md 应有增有减：%+v", files[0])
	}
}

// 两份里同名的图片大小一样、字节不同：对比里要列出来，不能当成一样。
func TestCompareListsUnreadFilesThatDifferOnlyInBytes(t *testing.T) {
	home := fakeHome(t)
	cb := filepath.Join(home, ".codebuddy/skills/spark")
	must(t, copyDir(filepath.Join(home, ".agents/skills/spark"), cb))
	must(t, os.WriteFile(filepath.Join(home, ".agents/skills/spark/logo.png"), []byte{0x89, 'P', 'N', 'G', 0, 1}, 0o644))
	must(t, os.WriteFile(filepath.Join(cb, "logo.png"), []byte{0x89, 'P', 'N', 'G', 0, 2}, 0o644))
	vs := Scan(home).ByID("mine:spark").Versions()
	if len(vs) != 2 {
		t.Fatalf("logo.png 字节不同，应是两种内容：%d 种", len(vs))
	}
	files := Scan(home).Compare(&vs[0], &vs[1])
	if len(files) != 1 || files[0].Path != "logo.png" || !files[0].Binary {
		t.Fatalf("应列出 logo.png：%+v", files)
	}
	if files[0].Left.Size != 6 || files[0].Right.Size != 6 {
		t.Errorf("两边大小都应是 6：%+v", files[0])
	}
}

func TestProblemsSample(t *testing.T) {
	l := Sample()
	p := l.Problems()
	for k, want := range map[Issue]int{SameCopies: 1, DiffCopies: 1, ExtraLinks: 1, BrokenLinks: 2, GlobalLinked: 1} {
		if got := p.Count(k); got != want {
			t.Errorf("%s：%d 个，应为 %d", k.Title(), got, want)
		}
	}
	if !p.Broken[0].Relink || p.Broken[1].Relink {
		t.Errorf("技能库有 lark-doc 的能重新连上，old-notes 不能：%+v", p.Broken)
	}
	r, err := l.Merge(l.Get("brainstorming"), 0)
	must(t, err)
	if r.Linked != 2 {
		t.Errorf("示例里收成一份：%+v", r)
	}
	if p := l.Problems(); p.Count(SameCopies) != 0 {
		t.Error("收成一份后不应再列出")
	}
}

func TestFixLinks(t *testing.T) {
	home := fakeHome(t)
	h := func(p string) string { return filepath.Join(home, p) }
	mustLink(t, h("nowhere"), h(".codebuddy/skills/spark")) // 断了，技能库有 spark
	l := Scan(home)
	p := l.Problems()
	byName := map[string]BrokenLink{}
	for _, b := range p.Broken {
		byName[b.Name] = b
	}
	if !byName["spark"].Relink || byName["broken"].Relink {
		t.Fatalf("断掉的链接：%+v", p.Broken)
	}
	must(t, l.Relink(byName["spark"].Path))
	if real, _ := realPath(h(".codebuddy/skills/spark")); real != h(".agents/skills/spark") {
		t.Errorf("重新连上后指向 %s", real)
	}
	must(t, l.RemoveBroken(byName["broken"].Path))
	if _, err := os.Lstat(h(".claude/skills/broken")); err == nil {
		t.Error("断掉的链接应已删掉")
	}
	if len(l.Problems().Broken) != 0 {
		t.Error("处理完不应再列出")
	}

	sd := l.ByID("project:" + h("code/storefront/.claude/skills/notes-sync"))
	if sd == nil || len(sd.GlobalDirs()) != 1 {
		t.Fatalf("notes-sync 应被全局链接：%v", sd)
	}
	must(t, l.Unglobal(sd))
	if _, err := os.Lstat(h(".claude/skills/notes-sync")); err == nil {
		t.Error("全局链接应已删掉")
	}
	if _, err := os.Stat(h("code/storefront/.claude/skills/notes-sync/SKILL.md")); err != nil {
		t.Error("项目里的技能不应动")
	}
}
