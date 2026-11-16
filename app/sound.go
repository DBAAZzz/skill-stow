package main

import "time"

// chimeGap 内最多响一声：接连处理几项时不叠在一起。
const chimeGap = time.Second

// chimeDone 是完成一项整理后的提示音（收成一份、删链接、清记录……）：
// 一次操作只响一声，批量的整批完成、一个都没失败时响；失败不调它。
// 设置里可以关掉。
func (a *app) chimeDone() {
	if a.sound == nil || a.lib.Settings.QuietTidy {
		return
	}
	now := time.Now()
	if a.clock != nil {
		now = a.clock()
	}
	if !a.lastChime.IsZero() && now.Sub(a.lastChime) < chimeGap {
		return
	}
	a.lastChime = now
	a.sound()
}
