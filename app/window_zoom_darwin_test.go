//go:build darwin

package main

import (
	"testing"

	"github.com/egoist/mygo"
)

func TestAnimatedWindowFrameKeepsTheRestoreTargetOnItsDisplay(t *testing.T) {
	for _, tt := range []struct {
		bounds mygo.Rectangle
		want   cocoaWindowFrame
	}{
		{mygo.Rectangle{X: 116, Y: 82, Width: 1280, Height: 800}, cocoaWindowFrame{116, 100, 1280, 800}},
		{mygo.Rectangle{X: -1140, Y: 40, Width: 1080, Height: 720}, cocoaWindowFrame{-1140, 222, 1080, 720}},
		{mygo.Rectangle{X: 100, Y: -600, Width: 1100, Height: 700}, cocoaWindowFrame{100, 882, 1100, 700}},
	} {
		if got := animatedWindowFrame(tt.bounds, 982); got != tt.want {
			t.Errorf("frame(%+v) = %+v, want %+v", tt.bounds, got, tt.want)
		}
	}
}
