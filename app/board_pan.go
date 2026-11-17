package main

import "github.com/egoist/mygo/ui"

// panSlop 是按住空白处后指针能晃动多远仍算点击：横向超过它才开始横移，
// 任一方向超过它，松手就不再算作取消选中的空白点击。
const panSlop = 4

type boardPanState struct {
	scroll  ui.ScrollState
	travel  ui.Point // 指针相对这次按下点的位移
	panning bool     // 横向越过 panSlop，看板跟着指针横移
	moved   bool     // 任一方向越过 panSlop，这次按下不再是点击
}

// panBoard 让溢出的看板按住空白处拖动横移。卡片、按钮、菜单自己接按下，
// 从它们上面开始的拖动不会横移看板。
func (a *app) panBoard(area ui.Element, content func()) {
	s := ui.Local(area, "board-pan", func() boardPanState { return boardPanState{} })
	dx, dy, pressed := area.Dragged()
	if pressed && s.scroll.MaxX > 0 {
		s.travel.X += dx
		s.travel.Y += dy
		if !s.panning && max(s.travel.X, -s.travel.X) > panSlop {
			s.panning = true
			dx = s.travel.X // 补上越过阈值前攒下的距离，看板不落后于指针
		}
		s.moved = s.moved || s.panning || max(s.travel.Y, -s.travel.Y) > panSlop
		if s.panning {
			s.scroll.X = max(0, min(s.scroll.MaxX, s.scroll.X-dx))
		}
	}
	if s.scroll.MaxX > 0 {
		area.Cursor(ui.CursorGrab)
		if pressed && s.moved {
			area.Cursor(ui.CursorGrabbing)
		}
	}
	area.TrackScroll(&s.scroll).Children(content)
	if s.moved {
		// MyGo 的 Dragged 不会吞掉松手时的点击：这里把它消耗掉，拖动不取消选中。
		area.Clicked()
		a.picked = false
	} else {
		a.deselectOnBlank(area)
	}
	if !pressed {
		s.travel, s.panning, s.moved = ui.Point{}, false, false
	}
}
