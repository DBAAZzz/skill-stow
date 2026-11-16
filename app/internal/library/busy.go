package library

import (
	"sync"

	"skillmanager/locales"
)

// ErrBusy：这个技能正在做另一件事（删除、收成一份、清理记录、更新），等它做完。
var ErrBusy = textErr(locales.BusyRetryLater)

// 应用里对同一个技能的删除、收成一份、清理记录和更新互斥。按名称：
// 「我的」技能按名称合成一个，安装记录的键也是名称。重新扫描会换掉
// Library，所以锁放在包里。
var busy = struct {
	sync.Mutex
	names map[string]bool
}{names: map[string]bool{}}

// lockSkill 占住这个技能，返回放开的函数；已经被占住时返回 ErrBusy。
// 放开函数可以多次调用。
func lockSkill(name string) (release func(), err error) {
	busy.Lock()
	defer busy.Unlock()
	if busy.names[name] {
		return nil, ErrBusy
	}
	busy.names[name] = true
	var once sync.Once
	return func() {
		once.Do(func() {
			busy.Lock()
			delete(busy.names, name)
			busy.Unlock()
		})
	}, nil
}

// lockSkills 一次占住几个技能，有一个占不到就都不占。
func holdSkills(names []string) (release func(), err error) {
	var held []func()
	release = func() {
		for _, r := range held {
			r()
		}
	}
	for _, n := range names {
		r, err := lockSkill(n)
		if err != nil {
			release()
			return nil, err
		}
		held = append(held, r)
	}
	return release, nil
}
