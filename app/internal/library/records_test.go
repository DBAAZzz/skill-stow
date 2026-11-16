package library

import (
	"os"
	"slices"
	"strings"
	"testing"
)

// recordsHome 是残留记录用例的主目录：记录里有 dbs、dbs-action（磁盘上哪里
// 都没有）、kami（只在 ~/.claude/skills）、vite（在技能库），以及 lark-mail
// （well-known，也删了）。
func recordsHome(t *testing.T) (string, func(string) string) {
	t.Helper()
	home, h := linkHome(t, "claude-code")
	writeSkill(t, h(".agents/skills/vite"), "")
	writeSkill(t, h(".claude/skills/kami"), "")
	must(t, os.WriteFile(h(".agents/.skill-lock.json"), []byte(recordsLock), 0o644))
	return home, h
}

const recordsLock = `{
  "version": 3,
  "skills": {
    "dbs": {
      "source": "dontbesilent2025/dbskill",
      "sourceType": "github",
      "installedAt": "2026-08-21T00:00:00.000Z"
    },
    "vite": {
      "source": "antfu/skills",
      "sourceType": "github"
    },
    "dbs-action": {
      "source": "dontbesilent2025/dbskill",
      "sourceType": "github",
      "installedAt": "2026-08-20T00:00:00.000Z"
    },
    "kami": {
      "source": "tw93/kami",
      "sourceType": "github"
    },
    "lark-mail": {
      "source": "open.feishu.cn",
      "sourceType": "well-known"
    }
  },
  "dismissed": {
    "findSkillsPrompt": true
  }
}
`

func TestDeletedSkillsStillInTheRecordsShowUpGroupedBySource(t *testing.T) {
	// Given 记录里的 dbs、dbs-action 磁盘上哪里都没有
	home, _ := recordsHome(t)

	// When 检查整理项
	p := Scan(home).Problems()

	// Then 按来源合并成行：dbskill 一行两个技能，飞书一行；kami 挪走了不算
	if len(p.Stale) != 2 {
		t.Fatalf("残留记录 = %+v", p.Stale)
	}
	db := p.Stale[0]
	if db.Source != "dontbesilent2025/dbskill" || !slices.Equal(db.Names, []string{"dbs", "dbs-action"}) {
		t.Errorf("第一行 = %+v", db)
	}
	if db.Installed.Day() != 20 {
		t.Errorf("安装于应是这组里最早的：%v", db.Installed)
	}
	if p.Stale[1].Source != "open.feishu.cn" {
		t.Errorf("第二行 = %+v", p.Stale[1])
	}
	if p.Count(StaleRecords) != 2 {
		t.Errorf("按来源行数计：%d", p.Count(StaleRecords))
	}
	for _, r := range p.Stale {
		if slices.Contains(r.Names, "kami") || slices.Contains(r.Names, "vite") {
			t.Errorf("不该算残留：%+v", r)
		}
	}
}

func TestClearingRecordsChangesOnlyThoseEntries(t *testing.T) {
	// Given 两行残留和其他正常记录
	home, h := recordsHome(t)
	l := Scan(home)

	// When 全部清除
	n, err := l.ClearRecords(l.Problems().Stale...)
	must(t, err)

	// Then 3 条消失，其余条目和顶层字段逐字节不变
	if n != 3 {
		t.Errorf("清除了 %d 条", n)
	}
	after, _ := os.ReadFile(h(".agents/.skill-lock.json"))
	want := `{
  "version": 3,
  "skills": {
    "vite": {
      "source": "antfu/skills",
      "sourceType": "github"
    },
    "kami": {
      "source": "tw93/kami",
      "sourceType": "github"
    }
  },
  "dismissed": {
    "findSkillsPrompt": true
  }
}
`
	if string(after) != want {
		t.Errorf("记录文件：\n%s", after)
	}
	if len(l.Problems().Stale) != 0 {
		t.Error("清完后这一类应消失")
	}
}

func TestARecordReinstalledMeanwhileIsKept(t *testing.T) {
	// Given 残留 dbs 已显示，随后用 npx skills 装回了 dbs
	home, h := recordsHome(t)
	l := Scan(home)
	rows := l.Problems().Stale
	writeSkill(t, h(".agents/skills/dbs"), "")

	// When 清除这一行
	_, err := l.ClearRecords(rows[0])
	must(t, err)

	// Then dbs 的记录保留，dbs-action 清掉
	recs, _ := ReadInstallRecords(home)
	if recs.Get("dbs") == nil || recs.Get("dbs-action") != nil {
		t.Errorf("记录 = %v", recs.Names())
	}
}

func TestNoRecordFileMeansNoStaleRecords(t *testing.T) {
	home, _ := linkHome(t)
	if p := Scan(home).Problems(); len(p.Stale) != 0 {
		t.Errorf("没有记录文件：%+v", p.Stale)
	}
}

func TestABrokenRecordFileShowsNoStaleRecordsAndTheRestStillWorks(t *testing.T) {
	home, h := recordsHome(t)
	must(t, os.WriteFile(h(".agents/.skill-lock.json"), []byte("{ not json"), 0o644))
	l := Scan(home)
	if p := l.Problems(); len(p.Stale) != 0 {
		t.Errorf("坏掉的记录：%+v", p.Stale)
	}
	if l.Get("vite") == nil {
		t.Error("其他技能照常")
	}
}

func TestClearingFailsWithoutChangingAnythingWhenTheFileIsReadOnly(t *testing.T) {
	// Given 记录文件所在目录不能写
	home, h := recordsHome(t)
	l := Scan(home)
	before, _ := os.ReadFile(h(".agents/.skill-lock.json"))
	must(t, os.Chmod(h(".agents"), 0o555))
	t.Cleanup(func() { os.Chmod(h(".agents"), 0o755) })

	// When 清除
	_, err := l.ClearRecords(l.Problems().Stale[0])

	// Then 报原因，文件不变，行仍在
	if err == nil || !strings.Contains(err.Error(), "没能清理安装记录") {
		t.Errorf("err = %v", err)
	}
	if after, _ := os.ReadFile(h(".agents/.skill-lock.json")); string(after) != string(before) {
		t.Error("文件变了")
	}
	if len(l.Problems().Stale) != 2 {
		t.Error("行应仍在")
	}
}
