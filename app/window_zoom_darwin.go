//go:build darwin

package main

import (
	"sync"

	"github.com/ebitengine/purego"
	"github.com/ebitengine/purego/objc"
	"github.com/egoist/mygo"
)

var (
	windowZoomOnce         sync.Once
	windowZoomRestore      = map[objc.ID]func() bool{} // UI thread only.
	setAnimatedWindowFrame func(objc.ID, objc.SEL, cocoaWindowFrame, bool, bool)
)

// cocoaWindowFrame has NSRect's four CGFloat values in ABI order.
type cocoaWindowFrame struct{ X, Y, Width, Height float64 }

func animatedWindowFrame(r mygo.Rectangle, screenHeight int) cocoaWindowFrame {
	return cocoaWindowFrame{float64(r.X), float64(screenHeight - r.Y - r.Height), float64(r.Width), float64(r.Height)}
}

type animatedZoomWindow struct{ *mygo.Window }

func (w animatedZoomWindow) SetBounds(r mygo.Rectangle) {
	frame := animatedWindowFrame(r, mygo.Screen.PrimaryDisplay().Bounds.Height)
	setAnimatedWindowFrame(objc.ID(w.NativeHandle()), objc.RegisterName("setFrame:display:animate:"), frame, true, true)
}

// Keep MyGo's drag regions and macOS double-click preferences. Only the main
// window's native zoom action changes: an enlarged window returns to defaults,
// including one enlarged by dragging that AppKit does not consider zoomed.
func installMainWindowZoom(w *mygo.Window, opts mygo.WindowOptions) {
	mygo.RunOnMain(func() {
		id := objc.ID(w.NativeHandle())
		windowZoomOnce.Do(func() {
			purego.RegisterLibFunc(&setAnimatedWindowFrame, purego.RTLD_DEFAULT, "objc_msgSend")
			// Do not replace NSWindow's class: AppKit uses a KVO subclass for
			// the title-bar appearance. Override only its inherited zoom action.
			cls, cmd := id.Class(), objc.RegisterName("zoom:")
			var implementation func(objc.Class, objc.SEL) uintptr
			purego.RegisterLibFunc(&implementation, purego.RTLD_DEFAULT, "class_getMethodImplementation")
			var nativeZoom func(objc.ID, objc.SEL, objc.ID)
			purego.RegisterFunc(&nativeZoom, implementation(cls, cmd))
			zoom := objc.NewIMP(func(self objc.ID, cmd objc.SEL, sender objc.ID) {
				if restore := windowZoomRestore[self]; restore != nil && restore() {
					return
				}
				nativeZoom(self, cmd, sender)
			})
			if !cls.AddMethod(cmd, zoom, "v@:@") {
				panic("cannot install main window zoom action")
			}
		})
		windowZoomRestore[id] = func() bool {
			return restoreEnlargedWindow(animatedZoomWindow{w}, mygo.Screen.DisplayMatching(w.Bounds()).WorkArea, opts)
		}
		w.OnClosed(func() { delete(windowZoomRestore, id) })
	})
}
