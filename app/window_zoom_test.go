package main

import (
	"testing"

	"github.com/egoist/mygo"
)

type zoomTestWindow struct {
	bounds                mygo.Rectangle
	maximized, fullScreen bool
}

func (w *zoomTestWindow) Bounds() mygo.Rectangle     { return w.bounds }
func (w *zoomTestWindow) IsMaximized() bool          { return w.maximized }
func (w *zoomTestWindow) IsFullScreen() bool         { return w.fullScreen }
func (w *zoomTestWindow) SetBounds(r mygo.Rectangle) { w.bounds = r }

func TestZoomRestoresManuallyEnlargedWindowToDefaultSize(t *testing.T) {
	opts := (&app{}).mainWindowOptions()
	area := mygo.Rectangle{Y: 38, Width: 1512, Height: 888}
	for _, bounds := range []mygo.Rectangle{
		{Width: 1512, Height: 888},
		{X: 20, Y: 50, Width: 1450, Height: 850},
		{Width: 1400, Height: 700},
		{Width: 1100, Height: 850},
	} {
		// Manual resizing does not reliably set AppKit's isZoomed flag.
		w := &zoomTestWindow{bounds: bounds}
		if !restoreEnlargedWindow(w, area, opts) {
			t.Fatalf("manual size %+v was not restored", bounds)
		}
		want := mygo.Rectangle{X: 116, Y: 82, Width: 1280, Height: 800}
		if w.bounds != want {
			t.Fatalf("restored to %+v, want %+v", w.bounds, want)
		}
		if restoreEnlargedWindow(w, area, opts) {
			t.Fatal("next double click should use native zoom to enlarge")
		}
	}
}

func TestZoomLeavesNormalAndFullScreenWindowsToAppKit(t *testing.T) {
	opts := (&app{}).mainWindowOptions()
	area := mygo.Rectangle{Y: 38, Width: 1512, Height: 888}
	for _, w := range []*zoomTestWindow{
		{bounds: mygo.Rectangle{X: 40, Y: 60, Width: 1100, Height: 700}},
		{bounds: mygo.Rectangle{Width: 1512, Height: 982}, fullScreen: true},
	} {
		before := w.bounds
		if restoreEnlargedWindow(w, area, opts) || w.bounds != before {
			t.Fatalf("unexpected restore: %+v", w)
		}
	}
}

func TestZoomDefaultBoundsFitTheCurrentDisplay(t *testing.T) {
	w := &zoomTestWindow{bounds: mygo.Rectangle{X: -1200, Width: 1200, Height: 800}, maximized: true}
	opts := (&app{}).mainWindowOptions()
	area := mygo.Rectangle{X: -1200, Width: 1200, Height: 800}
	if !restoreEnlargedWindow(w, area, opts) {
		t.Fatal("maximized window was not restored")
	}
	if w.bounds.Width >= 1200 || w.bounds.Height >= 800 || w.bounds.Width < 960 || w.bounds.Height < 600 || w.bounds.X < -1200 || w.bounds.X+w.bounds.Width > 0 {
		t.Fatalf("default bounds do not fit current display: %+v", w.bounds)
	}
}
