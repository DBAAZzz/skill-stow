package library

import (
	"context"
	"errors"
	"fmt"
	"os"
	"slices"

	"skillmanager/locales"
)

func (l *Library) RecoverUpdates() error {
	if !updateJob.TryLock() {
		return ErrBusy
	}
	defer updateJob.Unlock()
	if l.updatesErr != nil {
		return l.updatesErr
	}
	var errs []error
	for _, tx := range slices.Clone(l.updateState().Transactions) {
		if tx.Status == "retained" {
			continue
		}
		if err := l.recoverTransaction(tx); err != nil {
			errs = append(errs, fmt.Errorf("%s%w", locales.LibrarySkillErrorPrefix(tx.Item.Name), err))
		}
	}
	return errors.Join(errs...)
}
func (l *Library) recoverTransaction(tx *transaction) error {
	release, err := lockSkill(tx.Item.Name)
	if err != nil {
		return err
	}
	defer release()
	if tx.Status == "complete" {
		if !directoryMatches(tx.Item.Place, tx.StageID, tx.Item.Upstream) {
			return ErrUpdateChanged
		}
		return l.cleanTransaction(tx)
	}
	if directoryMatches(tx.Item.Place, tx.TargetID, tx.Item.Local) && directoryMatches(tx.Stage, tx.StageID, tx.Item.Upstream) {
		// 尚未交换，只删除已验证的下载，不清理任何本机旧版。
		if err := os.RemoveAll(tx.Stage); err != nil {
			return err
		}
		st := l.updateState()
		st.Transactions = slices.DeleteFunc(st.Transactions, func(t *transaction) bool { return t.ID == tx.ID })
		return st.save(l.Home)
	}
	_, err = l.finishTransaction(tx)
	return err
}

// 重新检查把有争议的旧事务留档，后续按当前来源检查；保留目录始终可找到。
func (l *Library) RecheckUpdates(ctx context.Context, r Remote, progress func(UpdateProgress)) error {
	if !updateJob.TryLock() {
		return ErrBusy
	}
	if l.updatesErr != nil {
		updateJob.Unlock()
		return l.updatesErr
	}
	for _, tx := range slices.Clone(l.updateState().Transactions) {
		if tx.Status != "pending" {
			continue
		}
		if err := l.recoverTransaction(tx); err != nil {
			if errors.Is(err, ErrBusy) {
				updateJob.Unlock()
				return err
			}
			tx.Status = "retained"
		}
	}
	err := l.updates.save(l.Home)
	updateJob.Unlock()
	if err != nil {
		return err
	}
	return l.CheckUpdates(ctx, r, progress)
}
func (l *Library) changeUpdateMark(change func(*updateStore)) error {
	if !updateJob.TryLock() {
		return ErrBusy
	}
	defer updateJob.Unlock()
	if l.updatesErr != nil {
		return l.updatesErr
	}
	change(l.updateState())
	return l.updates.save(l.Home)
}
func (l *Library) IgnoreUpdate(it UpdateItem) error {
	if !it.CanCompare() {
		return ErrUpdateChanged
	}
	if err := l.validateSnapshot(it); err != nil {
		return err
	}
	return l.changeUpdateMark(func(st *updateStore) {
		m := st.ignore(it.Source, it.Path, it.Place, l.Root, it.Upstream)
		m.Name, m.Commit = it.Name, it.Commit
	})
}
func (l *Library) Unignore(it UpdateItem) error {
	return l.changeUpdateMark(func(st *updateStore) { st.unignore(it.Source, it.Path, it.Place, l.Root, it.Upstream) })
}
func (l *Library) StopChecking(it UpdateItem) error {
	if it.State != UpdRemoved {
		return ErrUpdateChanged
	}
	return l.changeUpdateMark(func(st *updateStore) { st.stop(it.Source, it.Place, it.Name) })
}
func (l *Library) ResumeChecking(it UpdateItem) error {
	return l.changeUpdateMark(func(st *updateStore) {
		st.resume(it.Source, it.Place)
		if st.Check != nil {
			st.Check.Skills = slices.DeleteFunc(st.Check.Skills, func(s skillUpdates) bool { return s.Name == it.Name && s.Source == it.Source })
		}
	})
}
