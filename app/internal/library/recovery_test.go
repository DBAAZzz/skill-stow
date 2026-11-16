//go:build darwin

package library

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"
)

func interruptedUpdate(t *testing.T, swapped bool) (*Library, func(string) string, *transaction) {
	l, h, _, p := preparedVite(t)
	sid, err := identify(p.Dir)
	must(t, err)
	tx := &transaction{ID: "interrupted", Item: p.Item, Stage: p.Dir, TargetID: p.Target, StageID: sid, RecordFile: lockFile(l.Home), RecordHash: p.Item.Upstream, RecordAt: time.Now().Format(time.RFC3339Nano), Policy: "trash", Status: "pending"}
	l.updates.Transactions = append(l.updates.Transactions, tx)
	must(t, l.updates.save(l.Home))
	if swapped {
		must(t, swapDirectories(p.Item.Place, p.Dir))
	}
	return l, h, tx
}
func TestRecoveryCompletesOnlyAnIntactSwappedTransaction(t *testing.T) {
	l, _, tx := interruptedUpdate(t, true)
	fresh := Scan(l.Home)
	must(t, fresh.RecoverUpdates())
	recs, err := ReadInstallRecords(l.Home)
	must(t, err)
	if recs.Get("vite").SkillFolderHash != tx.RecordHash {
		t.Fatal("record not completed")
	}
	if len(fresh.updates.Transactions) != 0 {
		t.Fatal("transaction not finished")
	}
}
func TestRecoveryBeforeSwapLeavesLocalFilesAlone(t *testing.T) {
	l, _, tx := interruptedUpdate(t, false)
	fresh := Scan(l.Home)
	must(t, fresh.RecoverUpdates())
	if treeOf(t, tx.Item.Place) != tx.Item.Local {
		t.Fatal("original changed")
	}
	if _, err := os.Stat(tx.Stage); !os.IsNotExist(err) {
		t.Fatal("unused download left")
	}
}
func TestRecoveryNeverReplaysExternallyReboundRecords(t *testing.T) {
	l, h, tx := interruptedUpdate(t, true)
	writeRecords(t, h, gh("vite", "new/source", "SKILL.md", "new"))
	fresh := Scan(l.Home)
	if fresh.RecoverUpdates() == nil {
		t.Fatal("conflict should be reported")
	}
	r, _ := ReadInstallRecords(l.Home)
	if r.Get("vite").Source != "new/source" {
		t.Fatal("overwrote new source")
	}
	if _, err := os.Stat(tx.Stage); err != nil {
		t.Fatal("old files lost")
	}
	if itemNamed(t, fresh.Updates(), "vite").State != UpdPending {
		t.Fatal("must not hide incomplete update")
	}
	must(t, fresh.RecheckUpdates(context.Background(), newFakeRemote(), nil))
	if fresh.updates.Transactions[0].Status != "retained" {
		t.Fatal("stale transaction remains replayable")
	}
}
func TestRecoveryKeepsAnOldDirectoryThatWasEditedAfterSwap(t *testing.T) {
	l, _, tx := interruptedUpdate(t, true)
	write(t, tx.Stage, "mine", "saved later", 0644)
	fresh := Scan(l.Home)
	if fresh.RecoverUpdates() == nil {
		t.Fatal("must retain changed files")
	}
	if b, _ := os.ReadFile(tx.Stage + "/mine"); string(b) != "saved later" {
		t.Fatal("lost late edit")
	}
}
func TestIgnoreTracksDirectoryContentAndResumeRequiresNewCheck(t *testing.T) {
	home, h, r, _ := viteHome(t)
	write(t, h(".agents/skills/vite"), "mine", "local", 0644)
	l, rep := checkNow(t, home, r)
	it := itemNamed(t, rep, "vite")
	must(t, l.IgnoreUpdate(it))
	if itemNamed(t, Scan(home).Updates(), "vite").State != UpdIgnored {
		t.Fatal("not persisted")
	}
	r.publish(t, "antfu/skills", map[string]string{"skills/vite/SKILL.md": skillMD("vite", "v2"), "skills/vite/run.sh*": "#!/bin/sh\n", "other": "unrelated"})
	l, rep = checkNow(t, home, r)
	if itemNamed(t, rep, "vite").State != UpdIgnored {
		t.Fatal("unrelated commit reset ignore")
	}
	must(t, l.Unignore(it))
	if itemNamed(t, l.Updates(), "vite").State != UpdBoth {
		t.Fatal("unignore failed")
	}
	r.publish(t, "antfu/skills", map[string]string{"other": "gone"})
	l, rep = checkNow(t, home, r)
	it = itemNamed(t, rep, "vite")
	must(t, l.StopChecking(it))
	must(t, l.ResumeChecking(it))
	if itemNamed(t, Scan(home).Updates(), "vite").State != UpdUnchecked {
		t.Fatal("resume reused old check")
	}
}

func TestRecheckRecoversMultipleUnswappedTransactions(t *testing.T) {
	l, h, first := interruptedUpdate(t, false)
	second := *first
	second.ID = "second"
	second.Item.Name = "other"
	second.Item.Place = h("other")
	second.Stage = h("stage-two")
	must(t, copyDir(first.Item.Place, second.Item.Place))
	must(t, copyDir(first.Stage, second.Stage))
	var err error
	second.TargetID, err = identify(second.Item.Place)
	must(t, err)
	second.StageID, err = identify(second.Stage)
	must(t, err)
	l.updates.Transactions = append(l.updates.Transactions, &second)
	must(t, l.updates.save(l.Home))
	must(t, l.RecheckUpdates(context.Background(), newFakeRemote(), nil))
	if len(l.updates.Transactions) != 0 {
		t.Fatal("unfinished downloads left")
	}
}

func TestRecoveryRespectsSkillLockAndLeavesBusyTransactionPending(t *testing.T) {
	for _, recheck := range []bool{false, true} {
		name := "startup"
		if recheck {
			name = "recheck"
		}
		t.Run(name, func(t *testing.T) {
			l, _, tx := interruptedUpdate(t, true)
			release, err := lockSkill(tx.Item.Name)
			must(t, err)
			defer release()
			recover := func() error {
				if recheck {
					return l.RecheckUpdates(context.Background(), newFakeRemote(), nil)
				}
				return l.RecoverUpdates()
			}
			err = recover()
			if !errors.Is(err, ErrBusy) {
				t.Fatalf("busy recovery = %v, want ErrBusy", err)
			}
			records, err := ReadInstallRecords(l.Home)
			must(t, err)
			if records.Get("vite").SkillFolderHash != tx.Item.Record.SkillFolderHash {
				t.Fatal("busy recovery wrote the installation record")
			}
			if len(l.updates.Transactions) != 1 || tx.Status != "pending" {
				t.Fatal("busy recovery consumed or retained a retryable transaction")
			}
			if treeOf(t, tx.Stage) != tx.Item.Local {
				t.Fatal("busy recovery removed the old directory")
			}
			release()
			must(t, recover())
			if len(l.updates.Transactions) != 0 {
				t.Fatal("recovery did not resume after releasing the skill")
			}
		})
	}
}
