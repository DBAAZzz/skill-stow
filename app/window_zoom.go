package main

import "github.com/egoist/mygo"

type zoomWindow interface {
	Bounds() mygo.Rectangle
	IsMaximized() bool
	IsFullScreen() bool
	SetBounds(mygo.Rectangle)
}

func restoreEnlargedWindow(w zoomWindow, area mygo.Rectangle, opts mygo.WindowOptions) bool {
	if w.IsFullScreen() || area.Width <= 0 || area.Height <= 0 {
		return false
	}
	width := min(opts.Width, max(opts.MinWidth, area.Width-area.Width/10), area.Width)
	height := min(opts.Height, max(opts.MinHeight, area.Height-area.Height/10), area.Height)
	bounds := w.Bounds()
	if !w.IsMaximized() && bounds.Width <= width && bounds.Height <= height {
		return false
	}
	w.SetBounds(mygo.Rectangle{
		X: area.X + (area.Width-width)/2, Y: area.Y + (area.Height-height)/2,
		Width: width, Height: height,
	})
	return true
}
